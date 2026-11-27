"""Standalone loopback FastAPI + WebSocket real-time server for Live Studio.

Binds strictly to 127.0.0.1:<port> (default 8190), enforces first-frame JSON
ephemeral token auth (closing with WebSocket code 4401 on failure or 5s timeout),
and orchestrates VAD, ASR, dual-mode streaming LLM, inline directive parsing,
streaming 24kHz TTS, visual frame rendering, and instant barge-in cancellation.
"""

from __future__ import annotations

import asyncio
from collections import deque
from contextlib import asynccontextmanager
import hmac
import io
import json
import logging
import math
import os
from pathlib import Path
import queue
import re
import shutil
import subprocess
import threading
import time
from typing import Any, AsyncIterator, Callable
from urllib.parse import urlparse

import httpx
import uvicorn
from fastapi import FastAPI, WebSocket, WebSocketDisconnect
from PIL import Image

from common.live_studio import (
    COLABONE_LIVE_AUDIO_IN_RATE,
    COLABONE_LIVE_DIRECTOR_STATES,
    COLABONE_LIVE_FRAME_AUDIO_PCM,
    COLABONE_LIVE_FRAME_FMP4,
    COLABONE_LIVE_FRAME_MIC_PCM,
    COLABONE_LIVE_FRAME_VIDEO,
    COLABONE_LIVE_HEALTH_PATH,
    COLABONE_LIVE_PORT,
    COLABONE_LIVE_WS_PATH,
    ColabOneLiveDirectiveParser,
    ColabOneLiveExternalLLMConfig,
    ColabOneLiveSessionConfig,
    build_colabone_live_system_prompt,
    pack_colabone_live_frame,
    scrub_colabone_live_secrets,
    unpack_colabone_live_frame,
    validate_colabone_external_llm_resolved_ips,
    validate_colabone_live_id,
    validate_colabone_live_rtmp_resolved_ips,
    validate_colabone_live_rtmp_url,
)
from server.live_worker_tts import (
    ColabOneMockStreamingTtsEngine,
    ColabOneStreamingTtsBase,
)
from server.live_worker_visual import (
    ColabOneFlashHeadVisualEngine,
    ColabOneMuseTalkVisualEngine,
    ColabOnePersonaLiveVisualEngine,
    ColabOneSyntheticVisualEngine,
    ColabOneVisualEngineBase,
    ColabOneVisualFrame,
    compute_pcm16_rms,
)

COLABONE_LIVE_WS_AUTH_CLOSE_CODE = 4401
COLABONE_LIVE_WS_AUTH_TIMEOUT_S = 5.0
COLABONE_LIVE_MAX_UTTERANCE_BYTES = 16000 * 2 * 30  # 30 seconds at 16 kHz mono Int16
COLABONE_LIVE_MAX_MIC_FRAME_BYTES = 64 * 1024  # 64 KiB cap per incoming MIC_PCM frame
COLABONE_LIVE_MAX_HISTORY_TURNS = 16

_LOGGER = logging.getLogger(__name__)
_SENSITIVE_LIVE_ENV_KEYS = frozenset(
    {
        "COLABONE_REMOTE_CONFIG",
        "HF_TOKEN",
        "HUGGING_FACE_HUB_TOKEN",
        "HUGGINGFACE_HUB_TOKEN",
        "HF_API_TOKEN",
        "OPENAI_API_KEY",
    }
)


def _sanitized_live_subprocess_env() -> dict[str, str]:
    return {
        k: v
        for k, v in os.environ.items()
        if k not in _SENSITIVE_LIVE_ENV_KEYS and not k.startswith("TUNNEL_")
    }
_SENSEVOICE_TAG_RE = re.compile(r"<\|([^|]+)\|>")
_SENSEVOICE_EMOTION_MAP = {
    "HAPPY": "happy",
    "SAD": "sad",
    "ANGRY": "angry",
    "NEUTRAL": "neutral",
    "FEARFUL": "fearful",
    "DISGUSTED": "disgusted",
    "SURPRISED": "surprised",
}


# ---------------------------------------------------------------------------
# VAD & ASR adapters (energy-based + Silero / SenseVoice runtime hooks)
# ---------------------------------------------------------------------------


class ColabOneSileroVadAdapter:
    """16 kHz mono Int16 VAD with 160 ms speech-onset barge-in and 380 ms endpointing.

    Uses an optional Silero ONNX/PyTorch probability callback when installed and
    falls back cleanly to deterministic normalized RMS energy detection offline.
    """

    def __init__(
        self,
        *,
        sample_rate: int = COLABONE_LIVE_AUDIO_IN_RATE,
        speech_onset_ms: int = 160,
        silence_endpoint_ms: int = 380,
        energy_threshold: float = 0.02,
        prob_threshold: float = 0.5,
        probability_fn: Callable[[bytes], float] | None = None,
    ) -> None:
        self.sample_rate = int(sample_rate)
        self.speech_onset_ms = max(20, int(speech_onset_ms))
        self.silence_endpoint_ms = max(40, int(silence_endpoint_ms))
        self.energy_threshold = float(energy_threshold)
        self.prob_threshold = float(prob_threshold)
        self._probability_fn = probability_fn

        self._in_speech = False
        self._speech_ms = 0.0
        self._silence_ms = 0.0
        self._utterance_ms = 0.0
        self._pending_speech = bytearray()
        self._utterance = bytearray()

    @property
    def is_speaking(self) -> bool:
        return self._in_speech

    def reset(self) -> None:
        self._in_speech = False
        self._speech_ms = 0.0
        self._silence_ms = 0.0
        self._utterance_ms = 0.0
        self._pending_speech.clear()
        self._utterance.clear()

    def _is_speech_frame(self, pcm_16k: bytes) -> bool:
        if self._probability_fn is not None:
            try:
                return float(self._probability_fn(pcm_16k)) >= self.prob_threshold
            except Exception:
                pass
        return compute_pcm16_rms(pcm_16k) >= self.energy_threshold

    def _frame_duration_ms(self, pcm_16k: bytes) -> float:
        samples = len(pcm_16k) // 2
        if samples <= 0 or self.sample_rate <= 0:
            return 0.0
        return (samples * 1000.0) / float(self.sample_rate)

    def append_ptt_frame(self, pcm_16k: bytes) -> None:
        """Buffer raw microphone audio while push-to-talk (PTT) is held."""
        if not pcm_16k:
            return
        if len(self._utterance) + len(pcm_16k) <= COLABONE_LIVE_MAX_UTTERANCE_BYTES:
            self._utterance.extend(pcm_16k)
            self._utterance_ms += self._frame_duration_ms(pcm_16k)

    def _process_chunk(self, chunk: bytes) -> list[dict[str, Any]]:
        frame_ms = self._frame_duration_ms(chunk)
        if frame_ms <= 0.0:
            return []

        events: list[dict[str, Any]] = []
        speech = self._is_speech_frame(chunk)

        if not self._in_speech:
            if speech:
                self._speech_ms += frame_ms
                if len(self._pending_speech) + len(chunk) <= COLABONE_LIVE_MAX_UTTERANCE_BYTES:
                    self._pending_speech.extend(chunk)
                if self._speech_ms >= self.speech_onset_ms and len(self._pending_speech) > 0:
                    self._in_speech = True
                    self._silence_ms = 0.0
                    self._utterance.clear()
                    self._utterance.extend(self._pending_speech)
                    self._utterance_ms = self._speech_ms
                    self._pending_speech.clear()
                    events.append(
                        {
                            "event": "speech_start",
                            "speech_ms": round(self._speech_ms, 1),
                        }
                    )
            else:
                self._speech_ms = 0.0
                self._pending_speech.clear()
            return events

        # Currently inside active speech segment
        if len(self._utterance) + len(chunk) > COLABONE_LIVE_MAX_UTTERANCE_BYTES:
            audio_pcm = bytes(self._utterance)
            duration_ms = round(self._utterance_ms, 1)
            self.reset()
            if audio_pcm:
                events.append(
                    {
                        "event": "speech_end",
                        "audio_pcm": audio_pcm,
                        "duration_ms": duration_ms,
                    }
                )
            return events

        self._utterance.extend(chunk)
        self._utterance_ms += frame_ms

        if speech:
            self._silence_ms = 0.0
        else:
            self._silence_ms += frame_ms
            if self._silence_ms >= self.silence_endpoint_ms:
                audio_pcm = bytes(self._utterance)
                duration_ms = round(self._utterance_ms, 1)
                self.reset()
                events.append(
                    {
                        "event": "speech_end",
                        "audio_pcm": audio_pcm,
                        "duration_ms": duration_ms,
                    }
                )
        return events

    def process_frame(self, pcm_16k: bytes) -> list[dict[str, Any]]:
        """Feed 16 kHz mono Int16 PCM sliced into fixed 20 ms chunks and return VAD events."""
        if not pcm_16k or len(pcm_16k) < 2:
            return []
        usable = len(pcm_16k) - (len(pcm_16k) % 2)
        if usable <= 0:
            return []
        chunk_bytes = max(2, int(self.sample_rate * 20 // 1000) * 2)
        events: list[dict[str, Any]] = []
        offset = 0
        while offset < usable:
            piece = pcm_16k[offset : min(usable, offset + chunk_bytes)]
            offset += chunk_bytes
            events.extend(self._process_chunk(piece))
        return events

    def flush(self) -> dict[str, Any] | None:
        """Finalize any buffered audio immediately (used for PTT release)."""
        audio_pcm = bytes(self._utterance) if self._utterance else bytes(self._pending_speech)
        duration_ms = round(self._utterance_ms or self._speech_ms, 1)
        self.reset()
        if not audio_pcm:
            return None
        return {
            "event": "speech_end",
            "audio_pcm": audio_pcm,
            "duration_ms": duration_ms,
        }


class ColabOneSenseVoiceAsrAdapter:
    """SenseVoice-Small ASR adapter with injectable test hook, real model loading, and offline fallback."""

    def __init__(
        self,
        *,
        model_dir: str = "",
        device: str = "cuda",
        transcribe_fn: Callable[[bytes, str], dict[str, Any] | str] | None = None,
    ) -> None:
        self.model_dir = str(model_dir or "").strip()
        self.device = device
        self._transcribe_fn = transcribe_fn
        self._model: Any = None
        self._model_load_attempted = False
        self._lock = threading.Lock()

    def _ensure_model(self) -> Any:
        if self._model is not None:
            return self._model
        with self._lock:
            if self._model is not None:
                return self._model
            if self._model_load_attempted or not self.model_dir:
                return None
            self._model_load_attempted = True
            model_path = Path(self.model_dir)
            if model_path.is_symlink() or not model_path.exists():
                return None
            try:
                from funasr import AutoModel  # type: ignore[import-not-found]
            except Exception:
                return None
            try:
                eff_device = self.device
                try:
                    import torch  # type: ignore[import-not-found]

                    if eff_device.startswith("cuda") and not torch.cuda.is_available():
                        eff_device = "cpu"
                except Exception:
                    eff_device = "cpu"
                self._model = AutoModel(
                    model=str(model_path),
                    device=eff_device,
                    disable_update=True,
                )
            except Exception as exc:
                _LOGGER.warning(
                    "SenseVoice ASR model load skipped: %s",
                    scrub_colabone_live_secrets(str(exc)),
                )
                self._model = None
            return self._model

    @staticmethod
    def _parse_sensevoice_output(raw_text: str, default_lang: str) -> tuple[str, str, str]:
        emotion = "neutral"
        detected_lang = default_lang
        for match in _SENSEVOICE_TAG_RE.finditer(raw_text or ""):
            tag = match.group(1).strip().upper()
            if tag in _SENSEVOICE_EMOTION_MAP:
                emotion = _SENSEVOICE_EMOTION_MAP[tag]
            elif tag in ("ZH", "EN", "JA", "KO", "YUE"):
                detected_lang = tag.lower()
        clean_text = _SENSEVOICE_TAG_RE.sub("", raw_text or "").strip()
        return clean_text, emotion, detected_lang

    def transcribe(self, pcm_16k: bytes, *, language: str = "auto") -> dict[str, Any]:
        started = time.monotonic()
        if not pcm_16k:
            return {
                "text": "",
                "emotion": "neutral",
                "language": language,
                "elapsed_ms": 0.0,
            }
        if self._transcribe_fn is not None:
            raw = self._transcribe_fn(pcm_16k, language)
            elapsed_ms = round((time.monotonic() - started) * 1000.0, 2)
            if isinstance(raw, str):
                return {
                    "text": raw.strip(),
                    "emotion": "neutral",
                    "language": language,
                    "elapsed_ms": elapsed_ms,
                }
            if isinstance(raw, dict):
                return {
                    "text": str(raw.get("text") or "").strip(),
                    "emotion": str(raw.get("emotion") or "neutral"),
                    "language": str(raw.get("language") or language),
                    "elapsed_ms": float(raw.get("elapsed_ms", elapsed_ms)),
                }

        model = self._ensure_model()
        if model is not None:
            try:
                import numpy as np  # type: ignore[import-not-found]

                usable = len(pcm_16k) - (len(pcm_16k) % 2)
                if usable >= 2:
                    wav = (
                        np.frombuffer(pcm_16k[:usable], dtype="<i2").astype(np.float32)
                        / 32768.0
                    )
                    res = model.generate(
                        input=wav,
                        cache={},
                        language=language,
                        use_itn=True,
                    )
                    raw_text = ""
                    if isinstance(res, list) and res and isinstance(res[0], dict):
                        raw_text = str(res[0].get("text") or "")
                    elif isinstance(res, dict):
                        raw_text = str(res.get("text") or "")
                    elif isinstance(res, str):
                        raw_text = res
                    clean_text, emotion, detected_lang = self._parse_sensevoice_output(
                        raw_text, language
                    )
                    return {
                        "text": clean_text,
                        "emotion": emotion,
                        "language": detected_lang,
                        "elapsed_ms": round((time.monotonic() - started) * 1000.0, 2),
                    }
            except Exception as exc:
                _LOGGER.warning(
                    "SenseVoice ASR transcribe failed: %s",
                    scrub_colabone_live_secrets(str(exc)),
                )

        # Offline no-weight fallback returns empty transcript safely.
        return {
            "text": "",
            "emotion": "neutral",
            "language": language,
            "elapsed_ms": round((time.monotonic() - started) * 1000.0, 2),
        }


# ---------------------------------------------------------------------------
# Dual-mode streaming LLM client (local llama-server & external OpenAI-compat)
# ---------------------------------------------------------------------------


def parse_openai_sse_delta(line: str) -> str | None:
    """Extract incremental text content from one OpenAI-compatible SSE data line."""
    stripped = (line or "").strip()
    if not stripped or not stripped.startswith("data:"):
        return None
    payload = stripped[5:].strip()
    if not payload or payload == "[DONE]":
        return None
    try:
        obj = json.loads(payload)
    except ValueError:
        return None
    choices = obj.get("choices")
    if not isinstance(choices, list) or not choices:
        return None
    first = choices[0]
    if not isinstance(first, dict):
        return None
    delta = first.get("delta")
    if isinstance(delta, dict):
        content = delta.get("content")
        if isinstance(content, str) and content:
            return content
    message = first.get("message")
    if isinstance(message, dict):
        content = message.get("content")
        if isinstance(content, str) and content:
            return content
    return None


def _cancel_is_set(cancel_event: asyncio.Event | threading.Event | None) -> bool:
    return cancel_event is not None and cancel_event.is_set()


class _ColabOnePinnedAsyncTransport(httpx.AsyncHTTPTransport):
    """Async httpx transport pinning TCP dials to a validated IP while preserving SNI and Host."""

    def __init__(
        self,
        *,
        expected_host: str,
        pinned_ip: str,
        **kwargs: Any,
    ) -> None:
        super().__init__(**kwargs)
        self.expected_host = expected_host.strip().lower().rstrip(".")
        self.pinned_ip = pinned_ip.strip()

    async def handle_async_request(self, request: httpx.Request) -> httpx.Response:
        req_host = (request.url.host or "").strip().lower().rstrip(".")
        if req_host != self.expected_host:
            raise ValueError("外部 LLM 请求主机与已校验解析结果不一致")

        scheme = (request.url.scheme or "").lower()
        default_port = 443 if scheme == "https" else 80
        port = request.url.port
        host_header = (
            f"{req_host}:{port}"
            if port is not None and port != default_port
            else req_host
        )
        pinned_url = request.url.copy_with(host=self.pinned_ip)

        headers = httpx.Headers(request.headers)
        headers["Host"] = host_header
        extensions = dict(request.extensions)
        if scheme == "https":
            extensions["sni_hostname"] = self.expected_host

        pinned_request = httpx.Request(
            method=request.method,
            url=pinned_url,
            headers=headers,
            stream=request.stream,
            extensions=extensions,
        )
        return await super().handle_async_request(pinned_request)


async def stream_llm_completion(
    messages: list[dict[str, str]],
    *,
    llm_mode: str = "local_gguf",
    local_base_url: str = "http://127.0.0.1:8081/v1",
    local_model: str = "local",
    external_config: ColabOneLiveExternalLLMConfig | dict[str, Any] | None = None,
    temperature: float = 0.7,
    max_tokens: int = 256,
    cancel_event: asyncio.Event | threading.Event | None = None,
    timeout_seconds: float = 30.0,
    http_client: httpx.AsyncClient | None = None,
) -> AsyncIterator[str]:
    """Stream chat completion text deltas from local llama-server or external API.

    Pins validated resolved public IPs to eliminate DNS rebinding TOCTOU and
    scrubs API keys and Bearer tokens from all raised errors.
    """
    mode = str(llm_mode or "local_gguf").strip().lower()
    api_key = ""
    pinned_transport: httpx.AsyncBaseTransport | None = None
    if mode == "external_api":
        if external_config is None:
            raise ValueError("外部 API 模式需要提供 external_llm 配置")
        cfg = (
            external_config
            if isinstance(external_config, ColabOneLiveExternalLLMConfig)
            else ColabOneLiveExternalLLMConfig.model_validate(external_config)
        )
        if http_client is None:
            pinned_ips = await asyncio.to_thread(
                validate_colabone_external_llm_resolved_ips, cfg.base_url
            )
            if pinned_ips:
                orig_host = (urlparse(cfg.base_url).hostname or "").strip().lower().rstrip(".")
                pinned_transport = _ColabOnePinnedAsyncTransport(
                    expected_host=orig_host,
                    pinned_ip=pinned_ips[0],
                )
        endpoint = f"{cfg.base_url.rstrip('/')}/chat/completions"
        model_name = cfg.model
        api_key = cfg.api_key
        eff_temp = cfg.temperature
        eff_max_tokens = cfg.max_tokens
    elif mode == "local_gguf":
        endpoint = f"{local_base_url.rstrip('/')}/chat/completions"
        model_name = (local_model or "local").strip() or "local"
        eff_temp = float(temperature)
        eff_max_tokens = int(max_tokens)
    else:
        raise ValueError(f"不支持的 LLM 模式: {llm_mode!r}")

    headers: dict[str, str] = {"Content-Type": "application/json"}
    if api_key:
        headers["Authorization"] = f"Bearer {api_key}"

    body = {
        "model": model_name,
        "messages": messages,
        "stream": True,
        "temperature": eff_temp,
        "max_tokens": eff_max_tokens,
    }

    owns_client = http_client is None
    if http_client is not None:
        client = http_client
    else:
        client_kwargs: dict[str, Any] = {
            "timeout": httpx.Timeout(timeout_seconds),
            "trust_env": False,
            "follow_redirects": False,
        }
        if pinned_transport is not None:
            client_kwargs["transport"] = pinned_transport
        client = httpx.AsyncClient(**client_kwargs)
    try:
        async with client.stream("POST", endpoint, headers=headers, json=body) as resp:
            if 300 <= resp.status_code < 400:
                raise RuntimeError(
                    f"外部 LLM 接口返回重定向 (HTTP {resp.status_code})，出于安全策略已拒绝跟随跳转"
                )
            if resp.status_code >= 400:
                raw_err = await resp.aread()
                snippet = raw_err.decode("utf-8", errors="replace")[:300]
                raise RuntimeError(
                    scrub_colabone_live_secrets(
                        f"LLM 流式请求失败 (HTTP {resp.status_code}): {snippet}",
                        api_key,
                    )
                )
            async for line in resp.aiter_lines():
                if _cancel_is_set(cancel_event):
                    return
                if line.strip() == "data: [DONE]":
                    return
                delta = parse_openai_sse_delta(line)
                if delta:
                    yield delta
    except asyncio.CancelledError:
        raise
    except RuntimeError:
        raise
    except Exception as exc:
        raise RuntimeError(
            scrub_colabone_live_secrets(f"LLM 流式连接异常: {exc}", api_key)
        ) from None
    finally:
        if owns_client:
            await client.aclose()


# ---------------------------------------------------------------------------
# Zero-latency video encoder helper (NVENC / libx264 / pure-Python JPEG)
# ---------------------------------------------------------------------------


class ColabOneLiveVideoEncoder:
    """Probes h264_nvenc with libx264 fallback and pure-Python JPEG offline mode."""

    def __init__(
        self,
        *,
        width: int = 720,
        height: int = 1280,
        fps: int = 25,
        mode: str = "jpeg",
        jpeg_quality: int = 75,
        rtmp_url: str = "",
        enable_ffmpeg: bool = False,
        ffmpeg_bin: str = "ffmpeg",
        dns_resolver: Callable[..., Any] | None = None,
    ) -> None:
        w, h = int(width), int(height)
        if w <= 0 or h <= 0 or w % 2 != 0 or h % 2 != 0:
            raise ValueError(f"视频编码分辨率宽高必须为正偶数: {w}x{h}")
        self.width = w
        self.height = h
        self.fps = max(1, int(fps))
        self.mode = mode
        self.jpeg_quality = max(20, min(95, int(jpeg_quality)))
        self.rtmp_url = validate_colabone_live_rtmp_url(rtmp_url) if rtmp_url else ""
        self.enable_ffmpeg = bool(enable_ffmpeg)
        self.ffmpeg_bin = str(ffmpeg_bin or "ffmpeg")
        self._dns_resolver = dns_resolver

        self._lock = threading.Lock()
        self._proc: subprocess.Popen[bytes] | None = None
        self._stdout_queue: queue.Queue[bytes] = queue.Queue(maxsize=64)
        self._stdout_thread: threading.Thread | None = None
        self._stderr_lines: deque[str] = deque(maxlen=64)
        self._stderr_thread: threading.Thread | None = None
        self._closed = False
        self._ffmpeg_failed = False

    @staticmethod
    def select_h264_level(width: int, height: int, fps: int = 25) -> str:
        """Select the H.264 level ('4.0' / '4.2' / '5.1') from frame macroblocks and MB/s."""
        w, h = int(width), int(height)
        f = max(1, int(fps))
        mb_frame = math.ceil(w / 16) * math.ceil(h / 16)
        mb_per_sec = mb_frame * f
        if mb_frame <= 8192 and mb_per_sec <= 245760:
            return "4.0"
        if mb_frame <= 8704 and mb_per_sec <= 522240:
            return "4.2"
        return "5.1"

    @staticmethod
    def probe_encoder(
        ffmpeg_bin: str = "ffmpeg",
        *,
        runner: Callable[[list[str]], tuple[int, str]] | None = None,
    ) -> str:
        """Probe ffmpeg for working h264_nvenc (1-frame null smoke test), else libx264, else jpeg."""
        resolved = ffmpeg_bin
        if runner is None:
            found = shutil.which(ffmpeg_bin)
            if not found:
                return "jpeg"
            resolved = found

        def _exec(cmd: list[str], timeout_s: float = 5.0) -> tuple[int, str]:
            if runner is not None:
                return runner(cmd)
            try:
                proc = subprocess.run(
                    cmd,
                    capture_output=True,
                    text=True,
                    timeout=timeout_s,
                    check=False,
                    stdin=subprocess.DEVNULL,
                    env=_sanitized_live_subprocess_env(),
                )
                return proc.returncode, (proc.stdout or "") + (proc.stderr or "")
            except Exception:
                return 1, ""

        code, output = _exec([resolved, "-hide_banner", "-encoders"], 5.0)
        if code != 0:
            return "jpeg"
        if "h264_nvenc" in output:
            smoke_cmd = [
                resolved,
                "-hide_banner",
                "-loglevel",
                "error",
                "-f",
                "lavfi",
                "-i",
                "color=c=black:s=256x256:d=0.04:r=25",
                "-frames:v",
                "1",
                "-pix_fmt",
                "yuv420p",
                "-c:v",
                "h264_nvenc",
                "-preset",
                "p4",
                "-f",
                "null",
                "-",
            ]
            smoke_code, _ = _exec(smoke_cmd, 8.0)
            if smoke_code == 0:
                return "h264_nvenc"
        if "libx264" in output:
            return "libx264"
        return "jpeg"

    @staticmethod
    def build_ffmpeg_command(
        encoder: str,
        width: int,
        height: int,
        fps: int = 25,
        *,
        ffmpeg_bin: str = "ffmpeg",
        rtmp_url: str = "",
        audio_input: str = "",
    ) -> list[str]:
        """Build a zero-latency ffmpeg encoding command for NVENC or libx264."""
        w, h = int(width), int(height)
        f = max(1, int(fps))
        if w <= 0 or h <= 0 or w % 2 != 0 or h % 2 != 0:
            raise ValueError(f"视频编码分辨率宽高必须为正偶数: {w}x{h}")
        level = ColabOneLiveVideoEncoder.select_h264_level(w, h, f)
        safe_rtmp = validate_colabone_live_rtmp_url(rtmp_url) if rtmp_url else ""
        protocol_whitelist = (
            "pipe,lavfi,rtmp,rtmps,tcp,tls,crypto" if safe_rtmp else "pipe"
        )
        cmd: list[str] = [
            ffmpeg_bin,
            "-nostdin",
            "-hide_banner",
            "-loglevel",
            "error",
            "-protocol_whitelist",
            protocol_whitelist,
            "-f",
            "rawvideo",
            "-pix_fmt",
            "rgb24",
            "-s",
            f"{w}x{h}",
            "-r",
            str(f),
            "-i",
            "pipe:0",
        ]
        if safe_rtmp:
            if audio_input:
                cmd.extend(
                    [
                        "-f",
                        "s16le",
                        "-ar",
                        "24000",
                        "-ac",
                        "1",
                        "-i",
                        str(audio_input),
                    ]
                )
            else:
                cmd.extend(
                    [
                        "-f",
                        "lavfi",
                        "-i",
                        "anullsrc=channel_layout=mono:sample_rate=24000",
                    ]
                )
        if encoder == "h264_nvenc":
            cmd.extend(
                [
                    "-c:v",
                    "h264_nvenc",
                    "-preset",
                    "p4",
                    "-tune",
                    "ull",
                    "-zerolatency",
                    "1",
                    "-profile:v",
                    "baseline",
                    "-level",
                    level,
                    "-bf",
                    "0",
                    "-g",
                    str(f),
                    "-pix_fmt",
                    "yuv420p",
                ]
            )
        elif encoder == "libx264":
            cmd.extend(
                [
                    "-c:v",
                    "libx264",
                    "-preset",
                    "ultrafast",
                    "-tune",
                    "zerolatency",
                    "-profile:v",
                    "baseline",
                    "-level",
                    level,
                    "-bf",
                    "0",
                    "-g",
                    str(f),
                    "-pix_fmt",
                    "yuv420p",
                ]
            )
        else:
            raise ValueError(f"不支持的 ffmpeg 编码器: {encoder!r}")

        if safe_rtmp:
            cmd.extend(
                [
                    "-map",
                    "0:v:0",
                    "-map",
                    "1:a:0",
                    "-c:a",
                    "aac",
                    "-b:a",
                    "128k",
                    "-ar",
                    "44100",
                    "-ac",
                    "2",
                    "-f",
                    "flv",
                    safe_rtmp,
                ]
            )
        else:
            cmd.extend(
                [
                    "-f",
                    "mp4",
                    "-movflags",
                    "frag_keyframe+empty_moov+default_base_moof",
                    "pipe:1",
                ]
            )
        return cmd

    def _cleanup_ffmpeg_proc(
        self,
        proc: subprocess.Popen[bytes],
        t_out: threading.Thread | None,
        t_err: threading.Thread | None,
    ) -> None:
        if proc.stdin is not None:
            try:
                proc.stdin.close()
            except Exception:
                pass
        if proc.poll() is None:
            try:
                proc.terminate()
                proc.wait(timeout=2.0)
            except Exception:
                try:
                    proc.kill()
                except Exception:
                    pass
                try:
                    proc.wait(timeout=1.5)
                except Exception:
                    pass
        else:
            try:
                proc.wait(timeout=0.5)
            except Exception:
                pass
        cur_thread = threading.current_thread()
        for reader in (t_out, t_err):
            if reader is not None and reader is not cur_thread:
                try:
                    reader.join(timeout=1.0)
                except Exception:
                    pass
        for stream in (proc.stdout, proc.stderr):
            if stream is not None:
                try:
                    stream.close()
                except Exception:
                    pass

    def start_ffmpeg(self, *, validate_dns: bool = True) -> None:
        """Start supervised FFmpeg process with RTMP DNS validation and stderr draining."""
        if self.mode not in ("h264_nvenc", "libx264"):
            if self.rtmp_url:
                raise RuntimeError("启用 RTMP 推流需要可用的 H.264 编码器（h264_nvenc 或 libx264）")
            return
        if self.rtmp_url and validate_dns:
            validate_colabone_live_rtmp_resolved_ips(
                self.rtmp_url, resolver=self._dns_resolver
            )
        found = shutil.which(self.ffmpeg_bin)
        if not found:
            if self.rtmp_url:
                raise RuntimeError("系统未安装 ffmpeg，无法启动 RTMP 推流")
            self.mode = "jpeg"
            return

        with self._lock:
            if self._closed:
                return
            if self._proc is not None and self._proc.poll() is None:
                return
            cmd = self.build_ffmpeg_command(
                self.mode,
                self.width,
                self.height,
                self.fps,
                ffmpeg_bin=found,
                rtmp_url=self.rtmp_url,
            )
            proc = subprocess.Popen(
                cmd,
                stdin=subprocess.PIPE,
                stdout=subprocess.PIPE if not self.rtmp_url else subprocess.DEVNULL,
                stderr=subprocess.PIPE,
                env=_sanitized_live_subprocess_env(),
            )
            out_q: queue.Queue[bytes] = queue.Queue(maxsize=64)
            stderr_lines: deque[str] = deque(maxlen=64)

            def _drain_stderr() -> None:
                stream = proc.stderr
                try:
                    if stream is not None:
                        for raw_line in stream:
                            line = raw_line.decode("utf-8", errors="replace").strip()
                            if line:
                                stderr_lines.append(
                                    scrub_colabone_live_secrets(line, self.rtmp_url)
                                )
                except Exception:
                    pass

            t_err = threading.Thread(
                target=_drain_stderr,
                name="colabone-ffmpeg-stderr",
                daemon=True,
            )
            t_err.start()

            t_out: threading.Thread | None = None
            if not self.rtmp_url and proc.stdout is not None:
                def _drain_stdout() -> None:
                    stream = proc.stdout
                    try:
                        while stream is not None:
                            chunk = stream.read(65536)
                            if not chunk:
                                break
                            try:
                                out_q.put_nowait(chunk)
                            except queue.Full:
                                try:
                                    out_q.get_nowait()
                                except queue.Empty:
                                    pass
                                try:
                                    out_q.put_nowait(chunk)
                                except queue.Full:
                                    pass
                    except Exception:
                        pass

                t_out = threading.Thread(
                    target=_drain_stdout,
                    name="colabone-ffmpeg-stdout",
                    daemon=True,
                )
                t_out.start()

            self._proc = proc
            self._stdout_queue = out_q
            self._stdout_thread = t_out
            self._stderr_lines = stderr_lines
            self._stderr_thread = t_err

    def _detach_ffmpeg_locked(
        self,
    ) -> tuple[
        subprocess.Popen[bytes] | None,
        threading.Thread | None,
        threading.Thread | None,
    ]:
        proc = self._proc
        t_out = self._stdout_thread
        t_err = self._stderr_thread
        self._proc = None
        self._stdout_thread = None
        self._stderr_thread = None
        return proc, t_out, t_err

    def close(self) -> None:
        """Cleanly shut down any running FFmpeg process and join reader threads."""
        with self._lock:
            self._closed = True
            proc, t_out, t_err = self._detach_ffmpeg_locked()
        if proc is not None:
            self._cleanup_ffmpeg_proc(proc, t_out, t_err)

    def _encode_jpeg(self, frame: ColabOneVisualFrame) -> bytes:
        img = Image.frombytes("RGB", (frame.width, frame.height), frame.rgb_bytes)
        buf = io.BytesIO()
        img.save(buf, format="JPEG", quality=self.jpeg_quality)
        return buf.getvalue()

    def encode_frame(self, frame: ColabOneVisualFrame) -> tuple[int, bytes]:
        """Encode a visual frame into fMP4/RTMP via supervised FFmpeg or pure-Python JPEG."""
        need_start = False
        with self._lock:
            can_use_ffmpeg = (
                self.enable_ffmpeg
                and not self._closed
                and not self._ffmpeg_failed
                and self.mode in ("h264_nvenc", "libx264")
            )
            if can_use_ffmpeg and self._proc is None:
                need_start = True

        if can_use_ffmpeg:
            if need_start:
                try:
                    self.start_ffmpeg()
                except Exception as exc:
                    if self.rtmp_url:
                        with self._lock:
                            self._ffmpeg_failed = True
                        raise RuntimeError(
                            scrub_colabone_live_secrets(str(exc), self.rtmp_url)
                        ) from None
                    with self._lock:
                        self.mode = "jpeg"
                    return COLABONE_LIVE_FRAME_VIDEO, self._encode_jpeg(frame)

            failed_proc: subprocess.Popen[bytes] | None = None
            failed_out: threading.Thread | None = None
            failed_err: threading.Thread | None = None
            write_error: Exception | None = None
            chunks: list[bytes] = []

            with self._lock:
                if self._closed or self._ffmpeg_failed or self.mode not in ("h264_nvenc", "libx264"):
                    return COLABONE_LIVE_FRAME_VIDEO, self._encode_jpeg(frame)
                proc = self._proc
                if proc is not None:
                    if proc.poll() is not None or proc.stdin is None:
                        write_error = RuntimeError("FFmpeg 编码进程已意外退出")
                        failed_proc, failed_out, failed_err = self._detach_ffmpeg_locked()
                        self._ffmpeg_failed = True
                        if not self.rtmp_url:
                            self.mode = "jpeg"
                    else:
                        try:
                            proc.stdin.write(frame.rgb_bytes)
                            proc.stdin.flush()
                            if not self.rtmp_url:
                                while True:
                                    try:
                                        chunks.append(self._stdout_queue.get_nowait())
                                    except queue.Empty:
                                        break
                        except Exception as exc:
                            write_error = exc
                            failed_proc, failed_out, failed_err = self._detach_ffmpeg_locked()
                            self._ffmpeg_failed = True
                            if not self.rtmp_url:
                                self.mode = "jpeg"

            if failed_proc is not None:
                self._cleanup_ffmpeg_proc(failed_proc, failed_out, failed_err)
            if write_error is not None:
                scrubbed = scrub_colabone_live_secrets(str(write_error), self.rtmp_url)
                _LOGGER.warning(
                    "FFmpeg encode failed; falling back to JPEG: %s",
                    scrubbed,
                )
                if self.rtmp_url:
                    raise RuntimeError(f"RTMP 推流编码失败: {scrubbed}") from None
            elif chunks and not self.rtmp_url:
                return COLABONE_LIVE_FRAME_FMP4, b"".join(chunks)

        return COLABONE_LIVE_FRAME_VIDEO, self._encode_jpeg(frame)


# ---------------------------------------------------------------------------
# Thread/async-safe Live Director State Machine
# ---------------------------------------------------------------------------


class ColabOneLiveDirector:
    """Monotonic-epoch director state machine with instant barge-in cancellation."""

    def __init__(
        self,
        *,
        director_mode: str = "dialogue",
        mic_mode: str = "vad",
        default_action: str = "idle_loop",
    ) -> None:
        self._lock = threading.RLock()
        self._state: str = "IDLE"
        self._turn_epoch: int = 0
        self._director_mode: str = (
            director_mode if director_mode in ("dialogue", "director") else "dialogue"
        )
        self._mic_mode: str = mic_mode if mic_mode in ("vad", "ptt") else "vad"
        self._default_action: str = default_action
        self._active_action: str = default_action
        self._active_card: str = ""
        self._active_mood: str = "neutral"

        self._cancel_event = threading.Event()
        self._async_cancel_event: asyncio.Event | None = None
        self._active_tasks: set[asyncio.Task[Any]] = set()
        self._asr_tasks: set[asyncio.Task[Any]] = set()
        self._queues: list[Any] = []
        self._history: list[dict[str, str]] = []

    @property
    def state(self) -> str:
        with self._lock:
            return self._state

    @property
    def turn_epoch(self) -> int:
        with self._lock:
            return self._turn_epoch

    @property
    def director_mode(self) -> str:
        with self._lock:
            return self._director_mode

    @property
    def mic_mode(self) -> str:
        with self._lock:
            return self._mic_mode

    @property
    def active_action(self) -> str:
        with self._lock:
            return self._active_action

    @property
    def active_card(self) -> str:
        with self._lock:
            return self._active_card

    @property
    def active_mood(self) -> str:
        with self._lock:
            return self._active_mood

    @property
    def cancel_event(self) -> threading.Event:
        return self._cancel_event

    def get_turn_epoch(self) -> int:
        with self._lock:
            return self._turn_epoch

    def register_queue(self, q: Any) -> None:
        with self._lock:
            if q not in self._queues:
                self._queues.append(q)

    def register_task(self, task: asyncio.Task[Any]) -> None:
        with self._lock:
            self._active_tasks.add(task)

        def _cleanup(done_task: asyncio.Task[Any]) -> None:
            with self._lock:
                self._active_tasks.discard(done_task)

        task.add_done_callback(_cleanup)

    def register_asr_task(self, task: asyncio.Task[Any]) -> None:
        with self._lock:
            self._asr_tasks.add(task)

        def _cleanup(done_task: asyncio.Task[Any]) -> None:
            with self._lock:
                self._asr_tasks.discard(done_task)

        task.add_done_callback(_cleanup)

    def has_active_asr_tasks(self) -> bool:
        with self._lock:
            return any(not t.done() for t in self._asr_tasks)

    def set_state(self, new_state: str) -> str:
        if new_state not in COLABONE_LIVE_DIRECTOR_STATES:
            raise ValueError(f"非法的导播状态: {new_state!r}")
        with self._lock:
            self._state = new_state
            return self._state

    def set_modes(
        self, *, director_mode: str | None = None, mic_mode: str | None = None
    ) -> dict[str, str]:
        with self._lock:
            if director_mode in ("dialogue", "director"):
                self._director_mode = director_mode
            if mic_mode in ("vad", "ptt"):
                self._mic_mode = mic_mode
            return {
                "director_mode": self._director_mode,
                "mic_mode": self._mic_mode,
            }

    def set_action(self, action_id: str) -> str:
        aid = validate_colabone_live_id(action_id, "动作 ID")
        with self._lock:
            self._active_action = aid
            return aid

    def set_card(self, product_id: str) -> str:
        pid = validate_colabone_live_id(product_id, "商品 ID") if product_id else ""
        with self._lock:
            self._active_card = pid
            return pid

    def set_mood(self, mood: str) -> str:
        cleaned = str(mood or "neutral").strip()[:40] or "neutral"
        with self._lock:
            self._active_mood = cleaned
            return cleaned

    def append_history(self, role: str, content: str) -> None:
        text = str(content or "").strip()
        if not text:
            return
        with self._lock:
            self._history.append({"role": role, "content": text})
            max_items = COLABONE_LIVE_MAX_HISTORY_TURNS * 2
            if len(self._history) > max_items:
                self._history = self._history[-max_items:]

    def history_copy(self) -> list[dict[str, str]]:
        with self._lock:
            return list(self._history)

    def _flush_queues(self) -> int:
        flushed = 0
        for q in list(self._queues):
            while True:
                try:
                    if isinstance(q, (asyncio.Queue, queue.Queue)):
                        q.get_nowait()
                        flushed += 1
                    else:
                        break
                except (asyncio.QueueEmpty, queue.Empty):
                    break
        return flushed

    def _cancel_task_set_locked(
        self,
        task_set: set[asyncio.Task[Any]],
        current_task: asyncio.Task[Any] | None,
    ) -> int:
        tasks = list(task_set)
        task_set.clear()
        cancelled = 0
        for task in tasks:
            if task is current_task:
                continue
            if not task.done():
                task.cancel()
                cancelled += 1
        return cancelled

    def begin_listening(self, reason: str = "listening") -> int:
        """Cancel any in-flight ASR tasks, bump turn epoch, and enter LISTENING atomically."""
        _ = reason
        try:
            current_task = asyncio.current_task()
        except RuntimeError:
            current_task = None
        with self._lock:
            self._cancel_task_set_locked(self._asr_tasks, current_task)
            self._turn_epoch += 1
            self._state = "LISTENING"
            return self._turn_epoch

    def cancel_asr_tasks(self, *, bump_epoch: bool = False) -> tuple[int, int]:
        """Cancel any in-flight ASR tasks and optionally advance epoch atomically."""
        try:
            current_task = asyncio.current_task()
        except RuntimeError:
            current_task = None
        with self._lock:
            cancelled = self._cancel_task_set_locked(self._asr_tasks, current_task)
            if bump_epoch:
                self._turn_epoch += 1
            return cancelled, self._turn_epoch

    def begin_turn(self, reason: str = "user_input") -> tuple[int, threading.Event, asyncio.Event | None]:
        """Start a new reasoning/speaking turn atomically, cancelling prior turn/ASR tasks."""
        _ = reason
        try:
            current_task = asyncio.current_task()
        except RuntimeError:
            current_task = None
        with self._lock:
            self._cancel_event.set()
            if self._async_cancel_event is not None:
                self._async_cancel_event.set()
            self._cancel_task_set_locked(self._active_tasks, current_task)
            self._cancel_task_set_locked(self._asr_tasks, current_task)
            self._turn_epoch += 1
            self._cancel_event = threading.Event()
            try:
                asyncio.get_running_loop()
                self._async_cancel_event = asyncio.Event()
            except RuntimeError:
                self._async_cancel_event = None
            self._state = "THINKING"
            return self._turn_epoch, self._cancel_event, self._async_cancel_event

    def trigger_barge_in(
        self,
        reason: str = "user_speech",
        *,
        next_state: str = "LISTENING",
        min_epoch: int | None = None,
    ) -> dict[str, Any]:
        """Immediately abort active LLM/TTS/ASR tasks, flush queues, and advance epoch."""
        target = next_state if next_state in ("LISTENING", "IDLE") else "LISTENING"
        try:
            current_task = asyncio.current_task()
        except RuntimeError:
            current_task = None
        with self._lock:
            next_epoch = self._turn_epoch + 1
            if min_epoch is not None:
                clamped = min(self._turn_epoch + 4, int(min_epoch))
                if clamped > next_epoch:
                    next_epoch = clamped
            self._turn_epoch = next_epoch
            new_epoch = self._turn_epoch
            self._cancel_event.set()
            if self._async_cancel_event is not None:
                self._async_cancel_event.set()
            cancelled_count = self._cancel_task_set_locked(self._active_tasks, current_task)
            cancelled_count += self._cancel_task_set_locked(self._asr_tasks, current_task)
            flushed = self._flush_queues()
            self._state = target
            self._active_action = self._default_action
            return {
                "type": "barge_in",
                "epoch": new_epoch,
                "reason": str(reason or "barge_in"),
                "state": self._state,
                "cancelled_tasks": cancelled_count,
                "flushed_items": flushed,
            }

    def complete_performing_if_matching(
        self, expected_epoch: int, default_action: str | None = None
    ) -> bool:
        """Atomically transition PERFORMING back to IDLE only if epoch and state still match."""
        target_action = (
            validate_colabone_live_id(default_action, "动作 ID")
            if default_action
            else self._default_action
        )
        with self._lock:
            if self._turn_epoch != int(expected_epoch) or self._state != "PERFORMING":
                return False
            self._state = "IDLE"
            self._active_action = target_action
            return True

    def snapshot(self) -> dict[str, Any]:
        with self._lock:
            return {
                "state": self._state,
                "epoch": self._turn_epoch,
                "director_mode": self._director_mode,
                "mic_mode": self._mic_mode,
                "action_id": self._active_action,
                "card_id": self._active_card,
                "mood": self._active_mood,
            }


class _CombinedCancel:
    """Composite Event-like cancel handle supporting is_set() and timed wait()."""

    def __init__(self, *events: threading.Event) -> None:
        self._events = tuple(ev for ev in events if ev is not None)

    def is_set(self) -> bool:
        return any(ev.is_set() for ev in self._events)

    def wait(self, timeout: float | None = None) -> bool:
        if self.is_set():
            return True
        if timeout is not None and timeout <= 0.0:
            return self.is_set()
        deadline = None if timeout is None else (time.monotonic() + float(timeout))
        slice_s = 0.01
        while True:
            if self.is_set():
                return True
            if deadline is not None:
                remaining = deadline - time.monotonic()
                if remaining <= 0.0:
                    return self.is_set()
                wait_s = min(slice_s, remaining)
            else:
                wait_s = slice_s
            if self._events:
                if self._events[0].wait(wait_s):
                    return True
            else:
                time.sleep(wait_s)


# ---------------------------------------------------------------------------
# FastAPI ASGI application & loopback uvicorn runner
# ---------------------------------------------------------------------------


_CLEAR_CARD_TOKENS = frozenset({"", "hide", "none", "clear", "off"})


def _resolve_product_card(
    avatar_dict: dict[str, Any],
    pid: str,
    inline_card: dict[str, Any] | None = None,
) -> dict[str, Any]:
    """Resolve a product ID into a show/clear card payload with full product metadata."""
    cleaned = str(pid or "").strip()
    if cleaned.lower() in _CLEAR_CARD_TOKENS:
        return {"action": "clear", "product_id": "", "card": None}
    safe_pid = validate_colabone_live_id(cleaned, "商品 ID")
    matched: dict[str, Any] | None = None
    products = avatar_dict.get("products") if isinstance(avatar_dict, dict) else None
    if isinstance(products, list):
        for item in products:
            if isinstance(item, dict) and str(item.get("id") or "").strip() == safe_pid:
                matched = {
                    "id": safe_pid,
                    "name": str(item.get("name") or ""),
                    "price": str(item.get("price") or ""),
                    "highlights": str(item.get("highlights") or ""),
                    "script_hint": str(item.get("script_hint") or ""),
                    "tag": str(item.get("tag") or ""),
                    "image_input_id": str(item.get("image_input_id") or ""),
                }
                break
    if matched is None and isinstance(inline_card, dict):
        matched = {
            "id": safe_pid,
            "name": str(inline_card.get("name") or "").strip()[:120],
            "price": str(inline_card.get("price") or "").strip()[:64],
            "highlights": str(inline_card.get("highlights") or "").strip()[:600],
            "script_hint": str(inline_card.get("script_hint") or "").strip()[:1000],
            "tag": str(inline_card.get("tag") or "").strip()[:40],
            "image_input_id": str(inline_card.get("image_input_id") or "").strip()[:64],
        }
    return {"action": "show", "product_id": safe_pid, "card": matched}


def _recreate_visual_engine_clone(
    old_engine: ColabOneVisualEngineBase,
) -> ColabOneVisualEngineBase:
    """Create a fresh, non-disposed visual engine instance with matching parameters."""
    if isinstance(old_engine, ColabOneSyntheticVisualEngine):
        return ColabOneSyntheticVisualEngine(
            width=old_engine.width,
            height=old_engine.height,
            fps=old_engine.fps,
            default_action=old_engine.default_action,
        )
    if isinstance(old_engine, ColabOneFlashHeadVisualEngine):
        return ColabOneFlashHeadVisualEngine(
            width=old_engine.width,
            height=old_engine.height,
            fps=old_engine.fps,
            portrait_path=old_engine.portrait_path,
            venv_python=old_engine.venv_python,
            model_dir=old_engine.model_dir,
            allow_fallback=old_engine.allow_fallback,
        )
    if isinstance(old_engine, ColabOneMuseTalkVisualEngine):
        return ColabOneMuseTalkVisualEngine(
            width=old_engine.width,
            height=old_engine.height,
            fps=old_engine.fps,
            avatar_dir=old_engine.avatar_dir,
            venv_python=old_engine.venv_python,
            model_dir=old_engine.model_dir,
            default_crossfade_frames=old_engine.default_crossfade_frames,
        )
    if isinstance(old_engine, ColabOnePersonaLiveVisualEngine):
        return ColabOnePersonaLiveVisualEngine(
            width=old_engine.width,
            height=old_engine.height,
            fps=old_engine.fps,
            portrait_path=old_engine.portrait_path,
            cache_dir=old_engine.cache_dir,
            venv_python=old_engine.venv_python,
            model_dir=old_engine.model_dir,
        )
    return ColabOneSyntheticVisualEngine(
        width=old_engine.width,
        height=old_engine.height,
        fps=old_engine.fps,
        default_action=old_engine.default_action,
    )


def create_colabone_live_app(
    *,
    verify_token: Callable[[str], bool],
    session_config: ColabOneLiveSessionConfig | dict[str, Any] | None = None,
    avatar: dict[str, Any] | None = None,
    external_llm: ColabOneLiveExternalLLMConfig | dict[str, Any] | None = None,
    tts_engine: ColabOneStreamingTtsBase | None = None,
    visual_engine: ColabOneVisualEngineBase | None = None,
    encoder: ColabOneLiveVideoEncoder | None = None,
    vad: ColabOneSileroVadAdapter | None = None,
    asr: ColabOneSenseVoiceAsrAdapter | None = None,
    llm_stream_fn: Callable[..., AsyncIterator[str]] | None = None,
    auth_timeout_seconds: float = COLABONE_LIVE_WS_AUTH_TIMEOUT_S,
    on_activity: Callable[[], None] | None = None,
    on_switch_engine: Callable[[dict[str, Any]], dict[str, Any]] | None = None,
    on_trigger_driving: Callable[[str], Any] | None = None,
    on_recreate_base_visual: Callable[[], ColabOneVisualEngineBase] | None = None,
    vram_budget: dict[str, Any] | None = None,
    active_secrets: list[str] | None = None,
    frame_pump_enabled: bool = False,
    ws_path: str = COLABONE_LIVE_WS_PATH,
    health_path: str = COLABONE_LIVE_HEALTH_PATH,
) -> FastAPI:
    """Create the standalone FastAPI Live Studio app (docs disabled)."""
    cfg = (
        session_config
        if isinstance(session_config, ColabOneLiveSessionConfig)
        else ColabOneLiveSessionConfig.model_validate(session_config or {})
    )
    avatar_dict = dict(avatar) if isinstance(avatar, dict) else {}
    ext_cfg = (
        external_llm
        if isinstance(external_llm, ColabOneLiveExternalLLMConfig)
        else (
            ColabOneLiveExternalLLMConfig.model_validate(external_llm)
            if isinstance(external_llm, dict)
            else None
        )
    )

    director = ColabOneLiveDirector(
        director_mode=cfg.director_mode,
        mic_mode=cfg.mic_mode,
    )
    tts = tts_engine or ColabOneMockStreamingTtsEngine(engine_id=cfg.tts_engine)
    visual = visual_engine or ColabOneSyntheticVisualEngine(
        width=160, height=120, fps=cfg.target_fps
    )
    eff_encoder = encoder or ColabOneLiveVideoEncoder(
        width=visual.width,
        height=visual.height,
        fps=cfg.target_fps,
        mode="jpeg",
        rtmp_url=cfg.rtmp_url if cfg.enable_rtmp else "",
    )
    vad_template = vad
    asr_adapter = asr or ColabOneSenseVoiceAsrAdapter()
    active_clients = {"count": 0}
    active_sockets: set[WebSocket] = set()
    authed_sockets: dict[WebSocket, asyncio.Lock] = {}
    active_tts_producers: set[asyncio.Future[Any]] = set()
    started_monotonic = time.monotonic()
    last_activity_at = [0.0]
    render_lock_box: dict[str, Any] = {"loop": None, "lock": None}
    engine_op_lock_box: dict[str, Any] = {"loop": None, "lock": None}
    extra_secrets: list[str] = [s for s in (active_secrets or []) if isinstance(s, str) and s]

    def _collect_active_secrets() -> list[str]:
        items: list[str] = list(extra_secrets)
        if ext_cfg is not None and ext_cfg.api_key:
            items.append(ext_cfg.api_key)
        cur_cfg = getattr(app.state, "config", None)
        if cur_cfg is not None and getattr(cur_cfg, "rtmp_url", ""):
            items.append(str(cur_cfg.rtmp_url))
        return items

    def _scrub_text(text: str) -> str:
        return scrub_colabone_live_secrets(text, *_collect_active_secrets())

    def _get_render_lock() -> asyncio.Lock:
        loop = asyncio.get_running_loop()
        if render_lock_box["loop"] is not loop or render_lock_box["lock"] is None:
            render_lock_box["loop"] = loop
            render_lock_box["lock"] = asyncio.Lock()
        return render_lock_box["lock"]

    def _get_engine_op_lock() -> asyncio.Lock:
        loop = asyncio.get_running_loop()
        if engine_op_lock_box["loop"] is not loop or engine_op_lock_box["lock"] is None:
            engine_op_lock_box["loop"] = loop
            engine_op_lock_box["lock"] = asyncio.Lock()
        return engine_op_lock_box["lock"]

    def _now_pts_ms() -> int:
        return int(max(0.0, (time.monotonic() - started_monotonic) * 1000.0))

    async def _drain_tts_producers(timeout_s: float = 2.0) -> None:
        pending = [fut for fut in list(active_tts_producers) if not fut.done()]
        if not pending:
            return
        try:
            await asyncio.wait_for(
                asyncio.gather(*pending, return_exceptions=True),
                timeout=max(0.1, float(timeout_s)),
            )
        except Exception:
            pass

    async def _broadcast_bytes(payload: bytes) -> None:
        dead: list[WebSocket] = []
        for ws_conn, ws_lock in list(authed_sockets.items()):
            try:
                async with ws_lock:
                    await ws_conn.send_bytes(payload)
            except Exception:
                dead.append(ws_conn)
        for ws_conn in dead:
            authed_sockets.pop(ws_conn, None)

    async def _broadcast_json(payload: dict[str, Any]) -> None:
        dead: list[WebSocket] = []
        for ws_conn, ws_lock in list(authed_sockets.items()):
            try:
                async with ws_lock:
                    await ws_conn.send_json(payload)
            except Exception:
                dead.append(ws_conn)
        for ws_conn in dead:
            authed_sockets.pop(ws_conn, None)

    async def _restore_base_visual_engine_locked() -> None:
        base_vis = getattr(app.state, "base_visual_engine", None)
        if base_vis is None:
            return
        base_enc = getattr(app.state, "base_encoder", None)
        was_hot_swapped = bool(getattr(app.state, "base_visual_hot_swapped", False))

        # Atomically detach driving visual engine and encoder inside render_lock
        # BEFORE invoking unload()/close() so no concurrent frame render uses them.
        render_lock = _get_render_lock()
        async with render_lock:
            driving_vis = app.state.visual_engine
            driving_enc = app.state.encoder
            if driving_vis is not None and driving_vis is not base_vis:
                app.state.visual_engine = None if (
                    was_hot_swapped or getattr(base_vis, "is_disposed", False)
                ) else base_vis
            if isinstance(base_enc, ColabOneLiveVideoEncoder) and driving_enc is not base_enc:
                app.state.encoder = base_enc

        # Unload PersonaLive first so VRAM is released BEFORE recreating/reloading base visual.
        if driving_vis is not None and driving_vis is not base_vis:
            if callable(getattr(driving_vis, "unload", None)):
                try:
                    await asyncio.to_thread(driving_vis.unload)
                except Exception:
                    pass
        if (
            isinstance(driving_enc, ColabOneLiveVideoEncoder)
            and driving_enc is not base_enc
        ):
            try:
                await asyncio.to_thread(driving_enc.close)
            except Exception:
                pass

        restored_vis = base_vis
        if was_hot_swapped or getattr(base_vis, "is_disposed", False):
            if callable(on_recreate_base_visual):
                try:
                    restored_vis = await asyncio.to_thread(on_recreate_base_visual)
                except Exception:
                    restored_vis = _recreate_visual_engine_clone(base_vis)
            else:
                restored_vis = _recreate_visual_engine_clone(base_vis)
            if callable(getattr(restored_vis, "load", None)):
                try:
                    await asyncio.to_thread(restored_vis.load)
                except Exception:
                    pass

        try:
            restored_vis.set_action(
                restored_vis.default_action, loop=True, crossfade_frames=0
            )
        except Exception:
            pass

        async with render_lock:
            app.state.visual_engine = restored_vis
            app.state.base_visual_engine = None
            app.state.base_visual_hot_swapped = False
            if isinstance(base_enc, ColabOneLiveVideoEncoder):
                app.state.encoder = base_enc
                app.state.base_encoder = None

    async def _restore_base_visual_engine() -> None:
        async with _get_engine_op_lock():
            await _restore_base_visual_engine_locked()

    async def _app_frame_pump_loop() -> None:
        consecutive_errors = 0
        while authed_sockets:
            try:
                fps = max(1, int(getattr(app.state.config, "target_fps", 25) or 25))
                interval = 1.0 / float(fps)
                tick_started = time.monotonic()
                cur_state = director.state
                if cur_state in ("IDLE", "LISTENING", "THINKING", "PERFORMING"):
                    cur_epoch = director.turn_epoch
                    pts = _now_pts_ms()
                    render_lock = _get_render_lock()
                    async with render_lock:
                        cur_visual = app.state.visual_engine
                        cur_encoder = app.state.encoder
                        if cur_visual is None or getattr(cur_visual, "is_disposed", False):
                            vframe = None
                            prev_action = ""
                            after_action = ""
                            driving_completed = False
                        else:
                            prev_action = cur_visual.current_action
                            vframe = await asyncio.to_thread(
                                cur_visual.render_frame,
                                b"",
                                epoch=cur_epoch,
                                pts_ms=pts,
                                state=cur_state,
                            )
                            after_action = cur_visual.current_action
                            driving_completed = bool(
                                getattr(
                                    cur_visual, "consume_driving_completed", lambda: False
                                )()
                            )
                    if (
                        vframe is None
                        or director.turn_epoch != cur_epoch
                        or director.state
                        not in ("IDLE", "LISTENING", "THINKING", "PERFORMING")
                    ):
                        consecutive_errors = 0
                        elapsed = time.monotonic() - tick_started
                        await asyncio.sleep(max(0.005, interval - elapsed))
                        continue
                    ftype, encoded = await asyncio.to_thread(
                        cur_encoder.encode_frame, vframe
                    )
                    if (
                        director.turn_epoch != cur_epoch
                        or director.state
                        not in ("IDLE", "LISTENING", "THINKING", "PERFORMING")
                    ):
                        consecutive_errors = 0
                        elapsed = time.monotonic() - tick_started
                        await asyncio.sleep(max(0.005, interval - elapsed))
                        continue
                    await _broadcast_bytes(
                        pack_colabone_live_frame(ftype, cur_epoch, pts, encoded)
                    )
                    oneshot_completed = cur_state == "PERFORMING" and (
                        driving_completed
                        or (
                            prev_action != cur_visual.default_action
                            and after_action == cur_visual.default_action
                        )
                    )
                    if oneshot_completed:
                        base_vis = getattr(app.state, "base_visual_engine", None)
                        restore_action = (
                            base_vis.default_action
                            if driving_completed and base_vis is not None
                            else cur_visual.default_action
                        )
                        if director.complete_performing_if_matching(
                            cur_epoch, restore_action
                        ):
                            if driving_completed and base_vis is not None:
                                await _restore_base_visual_engine()
                            await _broadcast_json(
                                {
                                    "type": "state",
                                    "state": "IDLE",
                                    "action_id": restore_action,
                                    "epoch": cur_epoch,
                                }
                            )
                consecutive_errors = 0
                elapsed = time.monotonic() - tick_started
                await asyncio.sleep(max(0.005, interval - elapsed))
            except asyncio.CancelledError:
                return
            except Exception as exc:
                consecutive_errors += 1
                backoff = min(1.0, 0.05 * (2 ** min(consecutive_errors - 1, 5)))
                _LOGGER.warning(
                    "Live Studio frame pump transient error (#%d): %s",
                    consecutive_errors,
                    _scrub_text(str(exc)),
                )
                await asyncio.sleep(backoff)

    def _ensure_app_frame_pump() -> None:
        if not frame_pump_enabled:
            return
        existing = getattr(app.state, "frame_pump_task", None)
        if existing is not None and not existing.done():
            return
        app.state.frame_pump_task = asyncio.create_task(_app_frame_pump_loop())

    async def _stop_app_frame_pump() -> None:
        pump_task = getattr(app.state, "frame_pump_task", None)
        app.state.frame_pump_task = None
        if pump_task is not None and not pump_task.done():
            pump_task.cancel()
            try:
                await asyncio.gather(pump_task, return_exceptions=True)
            except Exception:
                pass

    @asynccontextmanager
    async def _lifespan(_app: FastAPI):
        yield
        director.trigger_barge_in("server_shutdown", next_state="IDLE")
        if getattr(app.state, "tts_engine", None) is not None:
            try:
                app.state.tts_engine.abort(director.turn_epoch)
            except Exception:
                pass
        await _stop_app_frame_pump()
        await _drain_tts_producers(timeout_s=1.5)
        await _restore_base_visual_engine()
        for enc_attr in ("encoder", "base_encoder"):
            enc_obj = getattr(app.state, enc_attr, None)
            if isinstance(enc_obj, ColabOneLiveVideoEncoder):
                try:
                    await asyncio.to_thread(enc_obj.close)
                except Exception:
                    pass
        authed_sockets.clear()
        sockets = list(active_sockets)
        active_sockets.clear()
        for ws_conn in sockets:
            try:
                await ws_conn.close(code=1001)
            except Exception:
                pass

    app = FastAPI(
        title="ColabOne Live Studio",
        docs_url=None,
        redoc_url=None,
        openapi_url=None,
        lifespan=_lifespan,
    )
    app.state.director = director
    app.state.tts_engine = tts
    app.state.visual_engine = visual
    app.state.base_visual_engine = None
    app.state.base_visual_hot_swapped = False
    app.state.encoder = eff_encoder
    app.state.base_encoder = None
    app.state.vram_budget = dict(vram_budget) if isinstance(vram_budget, dict) else None
    app.state.avatar = avatar_dict
    app.state.vad = vad_template or ColabOneSileroVadAdapter()
    app.state.asr = asr_adapter
    app.state.config = cfg
    app.state.active_clients = active_clients
    app.state.active_sockets = active_sockets
    app.state.authed_sockets = authed_sockets
    app.state.active_tts_producers = active_tts_producers
    app.state.frame_pump_task = None

    def _new_connection_vad() -> ColabOneSileroVadAdapter:
        if vad_template is not None:
            return ColabOneSileroVadAdapter(
                sample_rate=vad_template.sample_rate,
                speech_onset_ms=vad_template.speech_onset_ms,
                silence_endpoint_ms=vad_template.silence_endpoint_ms,
                energy_threshold=vad_template.energy_threshold,
                prob_threshold=vad_template.prob_threshold,
                probability_fn=vad_template._probability_fn,
            )
        return ColabOneSileroVadAdapter()

    def _safe_on_activity() -> None:
        if on_activity is None:
            return
        try:
            on_activity()
        except Exception:
            pass

    def _touch() -> None:
        if on_activity is None:
            return
        now = time.monotonic()
        if last_activity_at[0] > 0.0 and (now - last_activity_at[0]) < 3.0:
            return
        last_activity_at[0] = now
        try:
            loop = asyncio.get_running_loop()
            loop.run_in_executor(None, _safe_on_activity)
        except RuntimeError:
            _safe_on_activity()

    @app.get(health_path)
    def live_health() -> dict[str, Any]:
        snap = director.snapshot()
        cur_vis = getattr(app.state, "visual_engine", None)
        vis_engine_id = (
            cur_vis.engine_id
            if cur_vis is not None
            else str(getattr(app.state.config, "visual_engine", ""))
        )
        return {
            "ok": True,
            "service": "colabone_live",
            "state": snap["state"],
            "epoch": snap["epoch"],
            "director_mode": snap["director_mode"],
            "mic_mode": snap["mic_mode"],
            "active_clients": active_clients["count"],
            "visual_engine": vis_engine_id,
            "tts_engine": app.state.tts_engine.engine_id,
        }

    @app.websocket(ws_path)
    async def live_ws(websocket: WebSocket) -> None:
        await websocket.accept()
        active_sockets.add(websocket)
        send_lock = asyncio.Lock()
        conn_vad = _new_connection_vad()
        ptt_holding = False
        client_registered = False

        async def _send_json_safe(payload: dict[str, Any]) -> None:
            async with send_lock:
                await websocket.send_json(payload)

        async def _send_json_best_effort(payload: dict[str, Any]) -> None:
            try:
                async with send_lock:
                    await websocket.send_json(payload)
            except Exception:
                pass

        async def _run_turn(user_text: str, mode_override: str | None = None) -> None:
            epoch: int | None = None
            sync_cancel: threading.Event | None = None
            try:
                turn_started = time.monotonic()
                first_token_ms: float | None = None
                first_audio_ms: float | None = None

                epoch, sync_cancel, async_cancel = director.begin_turn("text_input")
                eff_mode = (
                    mode_override
                    if mode_override in ("dialogue", "director")
                    else director.director_mode
                )
                await _broadcast_json(
                    {
                        "type": "state",
                        "state": "THINKING",
                        "epoch": epoch,
                        "director_mode": eff_mode,
                    }
                )

                cur_avatar = app.state.avatar if isinstance(app.state.avatar, dict) else {}
                system_prompt = build_colabone_live_system_prompt(
                    eff_mode,
                    cur_avatar,
                    cur_avatar.get("products") or [],
                    cur_avatar.get("actions") or [],
                )
                messages: list[dict[str, str]] = [{"role": "system", "content": system_prompt}]
                messages.extend(director.history_copy())
                messages.append({"role": "user", "content": user_text})

                parser = ColabOneLiveDirectiveParser()
                spoken_parts: list[str] = []
                audio_base_pts_ms: int | None = None
                audio_samples_sent = 0
                audio_wall_origin: float | None = None

                async def _render_and_broadcast_video_window(
                    pcm_window: bytes,
                    v_pts_ms: int,
                    cur_tts_ref: ColabOneStreamingTtsBase,
                    clause_cancel_ev: threading.Event,
                ) -> bool:
                    assert epoch is not None and sync_cancel is not None
                    render_lock = _get_render_lock()
                    async with render_lock:
                        if (
                            sync_cancel.is_set()
                            or director.turn_epoch != epoch
                        ):
                            clause_cancel_ev.set()
                            cur_tts_ref.abort(epoch)
                            return False
                        cur_visual = app.state.visual_engine
                        cur_encoder = app.state.encoder
                        if cur_visual is None or getattr(cur_visual, "is_disposed", False):
                            return True
                        vframe = await asyncio.to_thread(
                            cur_visual.render_frame,
                            pcm_window,
                            epoch=epoch,
                            pts_ms=v_pts_ms,
                            state="SPEAKING",
                        )
                    if sync_cancel.is_set() or director.turn_epoch != epoch:
                        clause_cancel_ev.set()
                        cur_tts_ref.abort(epoch)
                        return False
                    ftype, encoded = await asyncio.to_thread(
                        cur_encoder.encode_frame, vframe
                    )
                    if sync_cancel.is_set() or director.turn_epoch != epoch:
                        clause_cancel_ev.set()
                        cur_tts_ref.abort(epoch)
                        return False
                    await _broadcast_bytes(
                        pack_colabone_live_frame(ftype, epoch, v_pts_ms, encoded)
                    )
                    return True

                async def _dispatch_events(events: list[dict[str, str]]) -> None:
                    nonlocal first_audio_ms, audio_base_pts_ms, audio_samples_sent, audio_wall_origin
                    assert epoch is not None and sync_cancel is not None
                    for ev in events:
                        if sync_cancel.is_set() or director.turn_epoch != epoch:
                            return
                        kind = ev.get("kind")
                        if kind == "action":
                            aid = ev.get("value", "")
                            try:
                                safe_aid = director.set_action(aid)
                                app.state.visual_engine.set_action(safe_aid)
                                await _broadcast_json(
                                    {
                                        "type": "action",
                                        "action_id": safe_aid,
                                        "epoch": epoch,
                                    }
                                )
                            except ValueError:
                                continue
                        elif kind == "card":
                            pid = ev.get("value", "")
                            try:
                                resolved_card = _resolve_product_card(
                                    app.state.avatar, pid
                                )
                                director.set_card(resolved_card["product_id"])
                                card_msg: dict[str, Any] = {
                                    "type": "card",
                                    "product_id": resolved_card["product_id"],
                                    "card": resolved_card["card"],
                                    "epoch": epoch,
                                }
                                if resolved_card["action"] == "clear":
                                    card_msg["action"] = "clear"
                                await _broadcast_json(card_msg)
                            except ValueError:
                                continue
                        elif kind == "mood":
                            mood_val = director.set_mood(ev.get("value", "neutral"))
                            await _broadcast_json(
                                {
                                    "type": "mood",
                                    "mood": mood_val,
                                    "epoch": epoch,
                                }
                            )
                        elif kind == "speech":
                            clause = ev.get("text", "").strip()
                            if not clause:
                                continue
                            spoken_parts.append(clause)
                            director.set_state("SPEAKING")
                            await _broadcast_json(
                                {
                                    "type": "transcript_delta",
                                    "role": "assistant",
                                    "text": clause,
                                    "epoch": epoch,
                                }
                            )

                            loop = asyncio.get_running_loop()
                            pcm_queue: asyncio.Queue[tuple[str, Any]] = asyncio.Queue()
                            cur_tts = app.state.tts_engine
                            voice_preset = app.state.config.voice_preset
                            clause_cancel = threading.Event()
                            saw_producer_done = False

                            def _tts_producer(
                                clause_text: str,
                                preset_name: str,
                                tts_ref: ColabOneStreamingTtsBase,
                                turn_ep: int,
                                turn_cancel_ev: threading.Event,
                                local_cancel_ev: threading.Event,
                            ) -> None:
                                def _enqueue_safe(item: tuple[str, Any]) -> bool:
                                    if loop.is_closed():
                                        local_cancel_ev.set()
                                        return False
                                    try:
                                        loop.call_soon_threadsafe(
                                            pcm_queue.put_nowait, item
                                        )
                                        return True
                                    except RuntimeError:
                                        local_cancel_ev.set()
                                        return False

                                combined_cancel = _CombinedCancel(
                                    turn_cancel_ev, local_cancel_ev
                                )
                                try:
                                    for pcm_piece in tts_ref.synthesize_stream(
                                        clause_text,
                                        voice_preset=preset_name,
                                        epoch=turn_ep,
                                        cancel_event=combined_cancel,  # type: ignore[arg-type]
                                        get_current_epoch=director.get_turn_epoch,
                                    ):
                                        if (
                                            turn_cancel_ev.is_set()
                                            or local_cancel_ev.is_set()
                                            or director.turn_epoch != turn_ep
                                        ):
                                            break
                                        if not _enqueue_safe(("pcm", pcm_piece)):
                                            break
                                except Exception as exc:
                                    if (
                                        not turn_cancel_ev.is_set()
                                        and not local_cancel_ev.is_set()
                                        and director.turn_epoch == turn_ep
                                    ):
                                        _enqueue_safe(("error", exc))
                                finally:
                                    _enqueue_safe(("done", None))

                            producer_future = loop.run_in_executor(
                                None,
                                _tts_producer,
                                clause,
                                voice_preset,
                                cur_tts,
                                epoch,
                                sync_cancel,
                                clause_cancel,
                            )
                            active_tts_producers.add(producer_future)

                            def _consume_producer_future(fut: Any) -> None:
                                active_tts_producers.discard(fut)
                                try:
                                    if not fut.cancelled():
                                        fut.exception()
                                except Exception:
                                    pass

                            producer_future.add_done_callback(_consume_producer_future)

                            sample_rate = max(
                                1, int(getattr(cur_tts, "sample_rate", 24000) or 24000)
                            )
                            target_fps = max(
                                1, int(getattr(app.state.config, "target_fps", 25) or 25)
                            )
                            frame_window_bytes = max(
                                2, int(round(sample_rate / float(target_fps))) * 2
                            )
                            should_pace_live = frame_pump_enabled and not isinstance(
                                cur_tts, ColabOneMockStreamingTtsEngine
                            )
                            video_pcm_buf = bytearray()
                            window_start_pts_ms: int | None = None

                            try:
                                while True:
                                    if sync_cancel.is_set() or director.turn_epoch != epoch:
                                        clause_cancel.set()
                                        cur_tts.abort(epoch)
                                        return
                                    item_tag, item_val = await pcm_queue.get()
                                    if item_tag == "done":
                                        saw_producer_done = True
                                        break
                                    if item_tag == "error":
                                        raise item_val
                                    pcm_chunk: bytes = item_val
                                    if sync_cancel.is_set() or director.turn_epoch != epoch:
                                        clause_cancel.set()
                                        cur_tts.abort(epoch)
                                        return
                                    if first_audio_ms is None:
                                        first_audio_ms = round(
                                            (time.monotonic() - turn_started) * 1000.0, 1
                                        )
                                    if audio_base_pts_ms is None:
                                        audio_base_pts_ms = _now_pts_ms()
                                        audio_wall_origin = time.monotonic()

                                    audio_pts_ms = audio_base_pts_ms + int(
                                        round((audio_samples_sent * 1000.0) / float(sample_rate))
                                    )
                                    chunk_samples = len(pcm_chunk) // 2
                                    audio_samples_sent += chunk_samples

                                    await _broadcast_bytes(
                                        pack_colabone_live_frame(
                                            COLABONE_LIVE_FRAME_AUDIO_PCM,
                                            epoch,
                                            audio_pts_ms,
                                            pcm_chunk,
                                        )
                                    )

                                    if not video_pcm_buf:
                                        window_start_pts_ms = audio_pts_ms
                                    video_pcm_buf.extend(pcm_chunk)

                                    while len(video_pcm_buf) >= frame_window_bytes:
                                        pcm_window = bytes(video_pcm_buf[:frame_window_bytes])
                                        del video_pcm_buf[:frame_window_bytes]
                                        v_pts = (
                                            window_start_pts_ms
                                            if window_start_pts_ms is not None
                                            else audio_pts_ms
                                        )
                                        ok = await _render_and_broadcast_video_window(
                                            pcm_window, v_pts, cur_tts, clause_cancel
                                        )
                                        if not ok:
                                            return
                                        window_ms = int(
                                            round(
                                                ((len(pcm_window) // 2) * 1000.0)
                                                / float(sample_rate)
                                            )
                                        )
                                        window_start_pts_ms = v_pts + max(1, window_ms)

                                    if should_pace_live and audio_wall_origin is not None:
                                        audio_elapsed_s = audio_samples_sent / float(sample_rate)
                                        wall_elapsed_s = time.monotonic() - audio_wall_origin
                                        ahead_s = audio_elapsed_s - wall_elapsed_s - 0.06
                                        if ahead_s > 0.005:
                                            await asyncio.sleep(min(ahead_s, 0.05))
                                    else:
                                        await asyncio.sleep(0)

                                # Flush any remaining buffered PCM (< 1 frame window) so 100%
                                # of spoken audio drives lip-sync without dropping tail audio.
                                if (
                                    video_pcm_buf
                                    and not sync_cancel.is_set()
                                    and director.turn_epoch == epoch
                                ):
                                    tail_pts = (
                                        window_start_pts_ms
                                        if window_start_pts_ms is not None
                                        else _now_pts_ms()
                                    )
                                    ok = await _render_and_broadcast_video_window(
                                        bytes(video_pcm_buf),
                                        tail_pts,
                                        cur_tts,
                                        clause_cancel,
                                    )
                                    video_pcm_buf.clear()
                                    if not ok:
                                        return
                            finally:
                                if not saw_producer_done and not producer_future.done():
                                    clause_cancel.set()

                cur_cfg = app.state.config
                if llm_stream_fn is not None:
                    stream_iter = llm_stream_fn(
                        messages,
                        director_mode=eff_mode,
                        cancel_event=async_cancel,
                    )
                else:
                    stream_iter = stream_llm_completion(
                        messages,
                        llm_mode=cur_cfg.llm_mode,
                        local_model=cur_cfg.local_llm_model_id or "local",
                        external_config=ext_cfg,
                        cancel_event=async_cancel,
                    )

                async for delta in stream_iter:
                    if sync_cancel.is_set() or director.turn_epoch != epoch:
                        return
                    if delta and first_token_ms is None:
                        first_token_ms = round(
                            (time.monotonic() - turn_started) * 1000.0, 1
                        )
                    parsed = parser.feed(delta)
                    if parsed:
                        await _dispatch_events(parsed)

                if sync_cancel.is_set() or director.turn_epoch != epoch:
                    return
                tail = parser.flush()
                if tail:
                    await _dispatch_events(tail)

                if not sync_cancel.is_set() and director.turn_epoch == epoch:
                    director.append_history("user", user_text)
                    if spoken_parts:
                        director.append_history("assistant", " ".join(spoken_parts))
                    e2e_ms = round((time.monotonic() - turn_started) * 1000.0, 1)
                    await _broadcast_json(
                        {
                            "type": "telemetry",
                            "ttft_ms": first_token_ms if first_token_ms is not None else 0.0,
                            "ttfa_ms": first_audio_ms if first_audio_ms is not None else 0.0,
                            "e2e_ms": e2e_ms,
                            "video_codec": app.state.encoder.mode,
                            "epoch": epoch,
                        }
                    )
                    director.set_state("IDLE")
                    await _broadcast_json(
                        {
                            "type": "turn_end",
                            "epoch": epoch,
                            "state": "IDLE",
                        }
                    )
            except asyncio.CancelledError:
                if epoch is not None:
                    app.state.tts_engine.abort(epoch)
                    if director.turn_epoch == epoch and director.state in (
                        "THINKING",
                        "SPEAKING",
                    ):
                        director.set_state("IDLE")
                return
            except Exception as exc:
                if (
                    epoch is not None
                    and (sync_cancel is None or not sync_cancel.is_set())
                    and director.turn_epoch == epoch
                ):
                    director.set_state("IDLE")
                    await _send_json_best_effort(
                        {
                            "type": "error",
                            "code": "turn_error",
                            "epoch": epoch,
                            "detail": _scrub_text(str(exc)),
                        }
                    )

        async def _launch_turn(text: str, mode_override: str | None = None) -> None:
            if director.state in ("THINKING", "SPEAKING", "PERFORMING"):
                barge_ev = director.trigger_barge_in("new_turn", next_state="IDLE")
                app.state.tts_engine.abort(barge_ev["epoch"])
                await _restore_base_visual_engine()
                await _broadcast_json(barge_ev)
            else:
                if director.state == "LISTENING" or director.has_active_asr_tasks():
                    director.cancel_asr_tasks(bump_epoch=True)
                if getattr(app.state, "base_visual_engine", None) is not None:
                    await _restore_base_visual_engine()
            task = asyncio.create_task(_run_turn(text, mode_override=mode_override))
            director.register_task(task)

        def _launch_asr_turn(
            audio_pcm: bytes, *, duration_ms: float | None = None
        ) -> None:
            asr_epoch = director.turn_epoch

            async def _run_asr() -> None:
                try:
                    asr_res = await asyncio.to_thread(asr_adapter.transcribe, audio_pcm)
                    if director.turn_epoch != asr_epoch:
                        return
                    text = str(asr_res.get("text") or "").strip()
                    asr_msg: dict[str, Any] = {
                        "type": "asr_final",
                        "text": text,
                        "emotion": asr_res.get("emotion", "neutral"),
                        "epoch": director.turn_epoch,
                    }
                    if duration_ms is not None:
                        asr_msg["duration_ms"] = duration_ms
                    await _broadcast_json(asr_msg)
                    if director.turn_epoch != asr_epoch:
                        return
                    if text:
                        await _launch_turn(text)
                    else:
                        director.set_state("IDLE")
                        await _broadcast_json(
                            {
                                "type": "state",
                                "state": "IDLE",
                                "epoch": director.turn_epoch,
                            }
                        )
                except asyncio.CancelledError:
                    return
                except Exception as exc:
                    if director.turn_epoch == asr_epoch:
                        director.set_state("IDLE")
                        await _send_json_best_effort(
                            {
                                "type": "error",
                                "code": "asr_error",
                                "epoch": asr_epoch,
                                "detail": _scrub_text(str(exc)),
                            }
                        )

            task = asyncio.create_task(_run_asr())
            director.register_asr_task(task)

        try:
            # 1. Strict first-frame JSON auth within auth_timeout_seconds
            try:
                first_msg = await asyncio.wait_for(
                    websocket.receive_json(),
                    timeout=max(0.05, float(auth_timeout_seconds)),
                )
            except Exception:
                await _send_json_best_effort(
                    {"type": "error", "code": "unauthorized", "detail": "认证超时或首帧格式无效"}
                )
                try:
                    await websocket.close(code=COLABONE_LIVE_WS_AUTH_CLOSE_CODE)
                except Exception:
                    pass
                return

            token_val = first_msg.get("token") if isinstance(first_msg, dict) else None
            msg_type = first_msg.get("type") if isinstance(first_msg, dict) else None
            authorized = (
                msg_type == "auth"
                and isinstance(token_val, str)
                and bool(token_val)
                and bool(verify_token(token_val))
            )
            if not authorized:
                await _send_json_best_effort(
                    {"type": "error", "code": "unauthorized", "detail": "会话令牌无效或已过期"}
                )
                try:
                    await websocket.close(code=COLABONE_LIVE_WS_AUTH_CLOSE_CODE)
                except Exception:
                    pass
                return

            active_clients["count"] += 1
            client_registered = True
            authed_sockets[websocket] = send_lock
            _touch()

            try:
                await _send_json_safe(
                    {
                        "type": "auth_ok",
                        "epoch": director.turn_epoch,
                        "state": director.state,
                        "director_mode": director.director_mode,
                        "mic_mode": director.mic_mode,
                        "visual_engine": app.state.visual_engine.engine_id,
                        "tts_engine": app.state.tts_engine.engine_id,
                        "codec": app.state.encoder.mode,
                        "audio_sample_rate": 24000,
                        "fps": app.state.config.target_fps,
                    }
                )
            except Exception:
                return

            if frame_pump_enabled:
                _ensure_app_frame_pump()

            while True:
                raw_msg = await websocket.receive()
                if raw_msg.get("type") == "websocket.disconnect":
                    break

                if raw_msg.get("text") is not None:
                    _touch()
                    try:
                        payload = json.loads(raw_msg["text"])
                    except ValueError:
                        await _send_json_safe(
                            {"type": "error", "code": "bad_json", "detail": "JSON 格式错误"}
                        )
                        continue
                    if not isinstance(payload, dict):
                        continue
                    mtype = str(payload.get("type") or "").strip().lower()

                    if mtype == "ping":
                        await _send_json_safe(
                            {"type": "pong", "ts": payload.get("ts", time.time())}
                        )
                    elif mtype in ("text_input", "chat", "director_cmd"):
                        text = str(payload.get("text") or "").strip()
                        if not text:
                            continue
                        mode_ov = payload.get("mode")
                        if mtype == "director_cmd" and not mode_ov:
                            mode_ov = "director"
                        await _launch_turn(text, mode_override=mode_ov)
                    elif mtype == "trigger_action":
                        req_kind = str(payload.get("kind") or "").strip().lower()
                        driving_motion_id = str(
                            payload.get("driving_motion_id") or ""
                        ).strip()
                        is_personalive_drive = (
                            req_kind == "personalive_drive" or bool(driving_motion_id)
                        )
                        if is_personalive_drive:
                            raw_mid = (
                                driving_motion_id
                                or str(payload.get("action_id") or "").strip()
                            )
                            try:
                                safe_mid = validate_colabone_live_id(
                                    raw_mid, "驱动动作 ID"
                                )
                            except ValueError as exc:
                                await _send_json_safe(
                                    {
                                        "type": "error",
                                        "code": "invalid_action",
                                        "detail": str(exc),
                                    }
                                )
                                continue

                            async with _get_engine_op_lock():
                                if (
                                    not bool(
                                        getattr(
                                            app.state.config, "personalive_enabled", False
                                        )
                                    )
                                    or on_trigger_driving is None
                                ):
                                    await _send_json_safe(
                                        {
                                            "type": "error",
                                            "code": "personalive_disabled",
                                            "detail": "请先开启 PersonaLive 实时姿态驱动开关",
                                        }
                                    )
                                    continue
                                if director.state in ("THINKING", "SPEAKING"):
                                    barge_ev = director.trigger_barge_in(
                                        "trigger_driving", next_state="IDLE"
                                    )
                                    app.state.tts_engine.abort(barge_ev["epoch"])
                                    await _broadcast_json(barge_ev)
                                elif director.state == "LISTENING" or director.has_active_asr_tasks():
                                    director.cancel_asr_tasks(bump_epoch=True)

                                expected_epoch = director.turn_epoch
                                try:
                                    drive_res = await asyncio.to_thread(
                                        on_trigger_driving, safe_mid
                                    )
                                except ValueError as exc:
                                    err_text = _scrub_text(str(exc))
                                    err_code = (
                                        "personalive_disabled"
                                        if "PersonaLive" in err_text
                                        or not bool(
                                            getattr(
                                                app.state.config,
                                                "personalive_enabled",
                                                False,
                                            )
                                        )
                                        else "driving_failed"
                                    )
                                    await _send_json_safe(
                                        {
                                            "type": "error",
                                            "code": err_code,
                                            "detail": err_text,
                                        }
                                    )
                                    continue
                                except KeyError:
                                    await _send_json_safe(
                                        {
                                            "type": "error",
                                            "code": "driving_not_found",
                                            "detail": f"驱动动作不存在: {safe_mid}",
                                        }
                                    )
                                    continue
                                except Exception as exc:
                                    await _send_json_safe(
                                        {
                                            "type": "error",
                                            "code": "driving_failed",
                                            "detail": _scrub_text(str(exc)),
                                        }
                                    )
                                    continue

                                drive_engine = (
                                    drive_res
                                    if isinstance(drive_res, ColabOneVisualEngineBase)
                                    else (
                                        drive_res.get("visual_engine")
                                        if isinstance(drive_res, dict)
                                        else None
                                    )
                                )
                                if not isinstance(drive_engine, ColabOneVisualEngineBase):
                                    await _send_json_safe(
                                        {
                                            "type": "error",
                                            "code": "driving_failed",
                                            "detail": "PersonaLive 驱动引擎初始化失败",
                                        }
                                    )
                                    continue

                                # Re-verify PersonaLive enablement and epoch after await before committing
                                if (
                                    not bool(
                                        getattr(
                                            app.state.config, "personalive_enabled", False
                                        )
                                    )
                                    or director.turn_epoch != expected_epoch
                                ):
                                    if callable(getattr(drive_engine, "unload", None)):
                                        try:
                                            await asyncio.to_thread(drive_engine.unload)
                                        except Exception:
                                            pass
                                    continue

                                loop_flag = bool(payload.get("loop", False))
                                drive_engine.set_action(safe_mid, loop=loop_flag)

                                old_driving_to_unload = None
                                prev_enc_to_close = None
                                if getattr(app.state, "base_visual_engine", None) is None:
                                    app.state.base_visual_engine = app.state.visual_engine
                                    app.state.base_encoder = app.state.encoder
                                elif (
                                    app.state.visual_engine
                                    is not app.state.base_visual_engine
                                ):
                                    old_driving_to_unload = app.state.visual_engine
                                    if app.state.encoder is not app.state.base_encoder:
                                        prev_enc_to_close = app.state.encoder

                                # Enforce hot-swap: unload base visual engine BEFORE loading PersonaLive
                                vram_budget_cur = getattr(app.state, "vram_budget", None)
                                is_hot_swap = (
                                    isinstance(vram_budget_cur, dict)
                                    and vram_budget_cur.get("personalive_mode") == "hot_swap"
                                )
                                base_vis_for_hotswap = (
                                    app.state.base_visual_engine
                                    if is_hot_swap
                                    and not bool(
                                        getattr(app.state, "base_visual_hot_swapped", False)
                                    )
                                    else None
                                )

                                new_drive_encoder = ColabOneLiveVideoEncoder(
                                    width=drive_engine.width,
                                    height=drive_engine.height,
                                    fps=app.state.config.target_fps,
                                    mode=app.state.encoder.mode,
                                    rtmp_url=(
                                        app.state.config.rtmp_url
                                        if app.state.config.enable_rtmp
                                        else ""
                                    ),
                                    enable_ffmpeg=getattr(
                                        app.state.encoder, "enable_ffmpeg", False
                                    ),
                                )
                                render_lock = _get_render_lock()
                                if (
                                    old_driving_to_unload is not None
                                    or base_vis_for_hotswap is not None
                                    or prev_enc_to_close is not None
                                ):
                                    async with render_lock:
                                        if (
                                            old_driving_to_unload is not None
                                            or base_vis_for_hotswap is not None
                                        ):
                                            app.state.visual_engine = None
                                        if prev_enc_to_close is not None:
                                            app.state.encoder = new_drive_encoder
                                    if old_driving_to_unload is not None and callable(
                                        getattr(old_driving_to_unload, "unload", None)
                                    ):
                                        try:
                                            await asyncio.to_thread(
                                                old_driving_to_unload.unload
                                            )
                                        except Exception:
                                            pass
                                    if isinstance(
                                        prev_enc_to_close, ColabOneLiveVideoEncoder
                                    ):
                                        try:
                                            await asyncio.to_thread(
                                                prev_enc_to_close.close
                                            )
                                        except Exception:
                                            pass
                                    if base_vis_for_hotswap is not None and callable(
                                        getattr(base_vis_for_hotswap, "unload", None)
                                    ):
                                        try:
                                            await asyncio.to_thread(
                                                base_vis_for_hotswap.unload
                                            )
                                            app.state.base_visual_hot_swapped = True
                                        except Exception:
                                            pass

                                # Load PersonaLive now that VRAM is free (for hot-swap),
                                # restoring base visual engine cleanly on failure.
                                try:
                                    if not drive_engine.is_loaded:
                                        await asyncio.to_thread(drive_engine.load)
                                except Exception as exc:
                                    if callable(getattr(drive_engine, "unload", None)):
                                        try:
                                            await asyncio.to_thread(drive_engine.unload)
                                        except Exception:
                                            pass
                                    if isinstance(
                                        new_drive_encoder, ColabOneLiveVideoEncoder
                                    ):
                                        async with render_lock:
                                            if app.state.encoder is new_drive_encoder:
                                                base_enc_ref = getattr(
                                                    app.state, "base_encoder", None
                                                )
                                                if isinstance(
                                                    base_enc_ref,
                                                    ColabOneLiveVideoEncoder,
                                                ):
                                                    app.state.encoder = base_enc_ref
                                        try:
                                            await asyncio.to_thread(
                                                new_drive_encoder.close
                                            )
                                        except Exception:
                                            pass
                                    await _restore_base_visual_engine_locked()
                                    await _send_json_safe(
                                        {
                                            "type": "error",
                                            "code": "driving_failed",
                                            "detail": _scrub_text(str(exc)),
                                        }
                                    )
                                    continue

                                async with render_lock:
                                    app.state.visual_engine = drive_engine
                                    app.state.encoder = new_drive_encoder

                                director.set_action(safe_mid)
                                director.set_state("PERFORMING")
                                epoch = director.turn_epoch
                                await _broadcast_json(
                                    {
                                        "type": "action",
                                        "action_id": safe_mid,
                                        "driving_motion_id": safe_mid,
                                        "kind": "personalive_drive",
                                        "epoch": epoch,
                                        "state": director.state,
                                    }
                                )
                                pts = _now_pts_ms()
                                render_lock = _get_render_lock()
                                async with render_lock:
                                    cur_visual = app.state.visual_engine
                                    cur_encoder = app.state.encoder
                                    if (
                                        cur_visual is not drive_engine
                                        or getattr(cur_visual, "is_disposed", False)
                                        or director.turn_epoch != epoch
                                        or director.state != "PERFORMING"
                                    ):
                                        vframe = None
                                        driving_completed = False
                                    else:
                                        vframe = await asyncio.to_thread(
                                            cur_visual.render_frame,
                                            b"",
                                            epoch=epoch,
                                            pts_ms=pts,
                                            state="PERFORMING",
                                        )
                                        driving_completed = bool(
                                            getattr(
                                                cur_visual,
                                                "consume_driving_completed",
                                                lambda: False,
                                            )()
                                        )
                                if (
                                    vframe is None
                                    or director.turn_epoch != epoch
                                    or director.state != "PERFORMING"
                                ):
                                    continue
                                ftype, encoded = await asyncio.to_thread(
                                    cur_encoder.encode_frame, vframe
                                )
                                if (
                                    director.turn_epoch != epoch
                                    or director.state != "PERFORMING"
                                ):
                                    continue
                                await _broadcast_bytes(
                                    pack_colabone_live_frame(ftype, epoch, pts, encoded)
                                )
                                if driving_completed:
                                    base_vis = getattr(
                                        app.state, "base_visual_engine", None
                                    )
                                    restore_action = (
                                        base_vis.default_action
                                        if base_vis is not None
                                        else "idle_loop"
                                    )
                                    if director.complete_performing_if_matching(
                                        epoch, restore_action
                                    ):
                                        await _restore_base_visual_engine_locked()
                                        await _broadcast_json(
                                            {
                                                "type": "state",
                                                "state": "IDLE",
                                                "action_id": restore_action,
                                                "epoch": epoch,
                                            }
                                        )
                            continue

                        aid = str(payload.get("action_id") or "").strip()
                        try:
                            safe_aid = validate_colabone_live_id(aid, "动作 ID")
                        except ValueError as exc:
                            await _send_json_safe(
                                {
                                    "type": "error",
                                    "code": "invalid_action",
                                    "detail": str(exc),
                                }
                            )
                            continue
                        async with _get_engine_op_lock():
                            if director.state in ("THINKING", "SPEAKING"):
                                barge_ev = director.trigger_barge_in(
                                    "trigger_action", next_state="IDLE"
                                )
                                app.state.tts_engine.abort(barge_ev["epoch"])
                                await _broadcast_json(barge_ev)
                            elif director.state == "LISTENING" or director.has_active_asr_tasks():
                                director.cancel_asr_tasks(bump_epoch=True)
                            if getattr(app.state, "base_visual_engine", None) is not None:
                                await _restore_base_visual_engine_locked()
                            director.set_action(safe_aid)
                            director.set_state("PERFORMING")
                            if "loop" in payload and payload.get("loop") is not None:
                                loop_flag = bool(payload.get("loop"))
                            else:
                                action_kind = None
                                avatar_actions = (
                                    app.state.avatar.get("actions")
                                    if isinstance(app.state.avatar, dict)
                                    else None
                                )
                                if isinstance(avatar_actions, list):
                                    for act_item in avatar_actions:
                                        if (
                                            isinstance(act_item, dict)
                                            and str(act_item.get("id") or "").strip()
                                            == safe_aid
                                        ):
                                            action_kind = (
                                                str(act_item.get("kind") or "")
                                                .strip()
                                                .lower()
                                            )
                                            break
                                if action_kind is not None:
                                    loop_flag = action_kind == "loop"
                                else:
                                    loop_flag = safe_aid.endswith("_loop")
                            epoch = director.turn_epoch
                            target_visual = app.state.visual_engine
                            if target_visual is None:
                                continue
                            target_visual.set_action(safe_aid, loop=loop_flag)
                            await _broadcast_json(
                                {
                                    "type": "action",
                                    "action_id": safe_aid,
                                    "epoch": epoch,
                                    "state": director.state,
                                }
                            )
                            pts = _now_pts_ms()
                            render_lock = _get_render_lock()
                            async with render_lock:
                                cur_visual = app.state.visual_engine
                                cur_encoder = app.state.encoder
                                if (
                                    cur_visual is not target_visual
                                    or getattr(cur_visual, "is_disposed", False)
                                    or director.turn_epoch != epoch
                                    or director.state != "PERFORMING"
                                ):
                                    vframe = None
                                    prev_action = ""
                                    after_action = ""
                                else:
                                    prev_action = cur_visual.current_action
                                    vframe = await asyncio.to_thread(
                                        cur_visual.render_frame,
                                        b"",
                                        epoch=epoch,
                                        pts_ms=pts,
                                        state="PERFORMING",
                                    )
                                    after_action = cur_visual.current_action
                            if (
                                vframe is None
                                or director.turn_epoch != epoch
                                or director.state != "PERFORMING"
                            ):
                                continue
                            ftype, encoded = await asyncio.to_thread(
                                cur_encoder.encode_frame, vframe
                            )
                            if (
                                director.turn_epoch != epoch
                                or director.state != "PERFORMING"
                            ):
                                continue
                            await _broadcast_bytes(
                                pack_colabone_live_frame(ftype, epoch, pts, encoded)
                            )
                            if (
                                prev_action != cur_visual.default_action
                                and after_action == cur_visual.default_action
                            ):
                                if director.complete_performing_if_matching(
                                    epoch, cur_visual.default_action
                                ):
                                    await _broadcast_json(
                                        {
                                            "type": "state",
                                            "state": "IDLE",
                                            "action_id": cur_visual.default_action,
                                            "epoch": epoch,
                                        }
                                    )
                    elif mtype in ("show_card", "set_card"):
                        pid = str(payload.get("product_id") or "").strip()
                        inline_card = (
                            payload.get("card")
                            if isinstance(payload.get("card"), dict)
                            else None
                        )
                        try:
                            resolved_card = _resolve_product_card(
                                app.state.avatar, pid, inline_card=inline_card
                            )
                            director.set_card(resolved_card["product_id"])
                        except ValueError as exc:
                            await _send_json_safe(
                                {"type": "error", "code": "invalid_card", "detail": str(exc)}
                            )
                            continue
                        card_msg: dict[str, Any] = {
                            "type": "card",
                            "product_id": resolved_card["product_id"],
                            "card": resolved_card["card"],
                            "epoch": director.turn_epoch,
                        }
                        if resolved_card["action"] == "clear":
                            card_msg["action"] = "clear"
                        await _broadcast_json(card_msg)
                    elif mtype == "switch_engine":
                        req_vis = str(payload.get("visual_engine") or "").strip()
                        req_persona = payload.get("personalive_enabled")
                        req_tts = str(payload.get("tts_engine") or "").strip()
                        req_avatar = str(payload.get("avatar_id") or "").strip()
                        if req_vis and req_vis not in ("flashhead_lite", "musetalk_bank"):
                            await _send_json_safe(
                                {
                                    "type": "error",
                                    "code": "invalid_engine",
                                    "detail": f"不支持的视觉引擎: {req_vis}",
                                }
                            )
                            continue
                        if req_tts and req_tts not in ("qwen3_tts", "voxcpm2"):
                            await _send_json_safe(
                                {
                                    "type": "error",
                                    "code": "invalid_engine",
                                    "detail": f"不支持的流式 TTS 引擎: {req_tts}",
                                }
                            )
                            continue
                        if req_avatar:
                            try:
                                req_avatar = validate_colabone_live_id(req_avatar, "主播包 ID")
                            except ValueError as exc:
                                await _send_json_safe(
                                    {
                                        "type": "error",
                                        "code": "invalid_engine",
                                        "detail": str(exc),
                                    }
                                )
                                continue

                        switch_req: dict[str, Any] = {}
                        if req_vis:
                            switch_req["visual_engine"] = req_vis
                        if isinstance(req_persona, bool):
                            switch_req["personalive_enabled"] = req_persona
                        if req_tts:
                            switch_req["tts_engine"] = req_tts
                        if req_avatar:
                            switch_req["avatar_id"] = req_avatar

                        async with _get_engine_op_lock():
                            # Barge-in active turn/performance and abort TTS FIRST
                            if director.state in ("THINKING", "SPEAKING", "PERFORMING"):
                                barge_ev = director.trigger_barge_in(
                                    "switch_engine", next_state="IDLE"
                                )
                                app.state.tts_engine.abort(barge_ev["epoch"])
                                await _restore_base_visual_engine_locked()
                                await _broadcast_json(barge_ev)
                            else:
                                if director.state == "LISTENING" or director.has_active_asr_tasks():
                                    director.cancel_asr_tasks(bump_epoch=True)
                                if getattr(app.state, "base_visual_engine", None) is not None:
                                    await _restore_base_visual_engine_locked()

                            if on_switch_engine is not None:
                                try:
                                    result = await asyncio.to_thread(
                                        on_switch_engine, switch_req
                                    )
                                except Exception as exc:
                                    await _send_json_safe(
                                        {
                                            "type": "error",
                                            "code": "switch_engine_failed",
                                            "detail": _scrub_text(str(exc)),
                                        }
                                    )
                                    continue
                                old_visuals_to_unload: list[Any] = []
                                old_encoders_to_close: list[ColabOneLiveVideoEncoder] = []
                                old_tts_to_unload = None
                                if isinstance(result, dict):
                                    # Atomically swap all references on the event loop FIRST.
                                    if isinstance(
                                        result.get("config"), ColabOneLiveSessionConfig
                                    ):
                                        app.state.config = result["config"]
                                    if isinstance(result.get("vram_budget"), dict):
                                        app.state.vram_budget = result["vram_budget"]
                                    new_vis = result.get("visual_engine") or result.get(
                                        "visual_engine_obj"
                                    )
                                    if isinstance(new_vis, ColabOneVisualEngineBase):
                                        render_lock = _get_render_lock()
                                        async with render_lock:
                                            for cand_vis in (
                                                result.get("old_visual"),
                                                app.state.visual_engine,
                                                getattr(app.state, "base_visual_engine", None),
                                            ):
                                                if (
                                                    cand_vis is not None
                                                    and cand_vis is not new_vis
                                                    and not any(
                                                        cand_vis is existing
                                                        for existing in old_visuals_to_unload
                                                    )
                                                ):
                                                    old_visuals_to_unload.append(cand_vis)
                                            for cand_enc in (
                                                app.state.encoder,
                                                getattr(app.state, "base_encoder", None),
                                            ):
                                                if (
                                                    isinstance(
                                                        cand_enc, ColabOneLiveVideoEncoder
                                                    )
                                                    and not any(
                                                        cand_enc is existing
                                                        for existing in old_encoders_to_close
                                                    )
                                                ):
                                                    old_encoders_to_close.append(cand_enc)
                                            app.state.visual_engine = new_vis
                                            app.state.base_visual_engine = None
                                            app.state.base_visual_hot_swapped = False
                                            app.state.base_encoder = None
                                            new_enc = result.get("encoder")
                                            if isinstance(new_enc, ColabOneLiveVideoEncoder):
                                                app.state.encoder = new_enc
                                            else:
                                                app.state.encoder = ColabOneLiveVideoEncoder(
                                                    width=new_vis.width,
                                                    height=new_vis.height,
                                                    fps=app.state.config.target_fps,
                                                    mode=app.state.encoder.mode,
                                                    rtmp_url=(
                                                        app.state.config.rtmp_url
                                                        if app.state.config.enable_rtmp
                                                        else ""
                                                    ),
                                                    enable_ffmpeg=getattr(
                                                        app.state.encoder, "enable_ffmpeg", False
                                                    ),
                                                )
                                    new_tts = result.get("tts_engine") or result.get(
                                        "tts_engine_obj"
                                    )
                                    if isinstance(new_tts, ColabOneStreamingTtsBase):
                                        prev_tts = result.get(
                                            "old_tts", app.state.tts_engine
                                        )
                                        if prev_tts is not new_tts:
                                            old_tts_to_unload = prev_tts
                                        app.state.tts_engine = new_tts
                                    if isinstance(result.get("avatar"), dict):
                                        app.state.avatar = dict(result["avatar"])

                                # ONLY AFTER atomic swap (and waiting for any in-flight render_frame /
                                # TTS producer), unload old visual/TTS engines off the event loop.
                                if old_visuals_to_unload or old_encoders_to_close:
                                    render_lock = _get_render_lock()
                                    async with render_lock:
                                        pass
                                    for old_vis in old_visuals_to_unload:
                                        if callable(getattr(old_vis, "unload", None)):
                                            try:
                                                await asyncio.to_thread(old_vis.unload)
                                            except Exception:
                                                pass
                                    for old_enc in old_encoders_to_close:
                                        if old_enc is not app.state.encoder:
                                            try:
                                                await asyncio.to_thread(old_enc.close)
                                            except Exception:
                                                pass
                                if old_tts_to_unload is not None:
                                    try:
                                        old_tts_to_unload.abort(director.turn_epoch)
                                    except Exception:
                                        pass
                                    await _drain_tts_producers(timeout_s=2.0)
                                    if callable(getattr(old_tts_to_unload, "unload", None)):
                                        try:
                                            await asyncio.to_thread(
                                                old_tts_to_unload.unload
                                            )
                                        except Exception:
                                            pass
                            else:
                                cur_cfg = app.state.config
                                updates: dict[str, Any] = {}
                                if req_vis:
                                    updates["visual_engine"] = req_vis
                                if isinstance(req_persona, bool):
                                    updates["personalive_enabled"] = req_persona
                                if req_tts:
                                    updates["tts_engine"] = req_tts
                                if req_avatar:
                                    updates["avatar_id"] = req_avatar
                                if updates:
                                    app.state.config = cur_cfg.model_copy(update=updates)

                            cur_cfg = app.state.config
                            await _broadcast_json(
                                {
                                    "type": "state",
                                    "state": director.state,
                                    "epoch": director.turn_epoch,
                                    "visual_engine": cur_cfg.visual_engine,
                                    "personalive_enabled": cur_cfg.personalive_enabled,
                                    "tts_engine": cur_cfg.tts_engine,
                                    "avatar_id": cur_cfg.avatar_id,
                                }
                            )
                    elif mtype == "barge_in":
                        ptt_holding = False
                        reason = str(payload.get("reason") or "manual_barge_in")
                        next_state = str(payload.get("next_state") or "IDLE")
                        raw_epoch = payload.get("epoch")
                        min_epoch = (
                            raw_epoch
                            if isinstance(raw_epoch, int)
                            and not isinstance(raw_epoch, bool)
                            else None
                        )
                        ev = director.trigger_barge_in(
                            reason, next_state=next_state, min_epoch=min_epoch
                        )
                        app.state.tts_engine.abort(ev["epoch"])
                        await _restore_base_visual_engine()
                        app.state.visual_engine.set_action("idle_loop", loop=True)
                        await _broadcast_json(ev)
                    elif mtype == "set_mode":
                        modes = director.set_modes(
                            director_mode=payload.get("director_mode"),
                            mic_mode=payload.get("mic_mode"),
                        )
                        if modes["mic_mode"] != "ptt":
                            ptt_holding = False
                        await _broadcast_json(
                            {
                                "type": "state",
                                "state": director.state,
                                "epoch": director.turn_epoch,
                                **modes,
                            }
                        )
                    elif mtype == "ptt_start":
                        ptt_holding = True
                        if director.state in ("SPEAKING", "THINKING", "PERFORMING"):
                            ev = director.trigger_barge_in(
                                "ptt_start", next_state="LISTENING"
                            )
                            app.state.tts_engine.abort(ev["epoch"])
                            await _restore_base_visual_engine()
                            app.state.visual_engine.set_action("idle_loop", loop=True)
                            await _broadcast_json(ev)
                        else:
                            new_ep = director.begin_listening("ptt_start")
                            await _broadcast_json(
                                {
                                    "type": "state",
                                    "state": "LISTENING",
                                    "epoch": new_ep,
                                }
                            )
                        conn_vad.reset()
                    elif mtype == "ptt_end":
                        ptt_holding = False
                        end_ev = conn_vad.flush()
                        if end_ev and end_ev.get("audio_pcm"):
                            _launch_asr_turn(
                                end_ev["audio_pcm"],
                                duration_ms=end_ev.get("duration_ms"),
                            )
                        else:
                            director.set_state("IDLE")
                            await _broadcast_json(
                                {
                                    "type": "state",
                                    "state": "IDLE",
                                    "epoch": director.turn_epoch,
                                }
                            )

                elif raw_msg.get("bytes") is not None:
                    _touch()
                    try:
                        frame_type, _f_epoch, _pts, body = unpack_colabone_live_frame(
                            raw_msg["bytes"]
                        )
                    except ValueError as exc:
                        await _send_json_safe(
                            {"type": "error", "code": "bad_frame", "detail": str(exc)}
                        )
                        continue
                    if frame_type != COLABONE_LIVE_FRAME_MIC_PCM:
                        continue
                    if len(body) > COLABONE_LIVE_MAX_MIC_FRAME_BYTES:
                        await _send_json_safe(
                            {
                                "type": "error",
                                "code": "bad_frame",
                                "detail": f"音频帧过大（单帧上限 {COLABONE_LIVE_MAX_MIC_FRAME_BYTES} 字节）",
                            }
                        )
                        continue
                    if director.mic_mode == "ptt":
                        if ptt_holding:
                            conn_vad.append_ptt_frame(body)
                        continue

                    vad_events = conn_vad.process_frame(body)
                    for vev in vad_events:
                        if vev["event"] == "speech_start":
                            if director.state in ("SPEAKING", "THINKING", "PERFORMING"):
                                barge_ev = director.trigger_barge_in(
                                    "vad_speech_onset", next_state="LISTENING"
                                )
                                app.state.tts_engine.abort(barge_ev["epoch"])
                                await _restore_base_visual_engine()
                                app.state.visual_engine.set_action(
                                    "idle_loop", loop=True
                                )
                                await _broadcast_json(barge_ev)
                            else:
                                new_ep = director.begin_listening("vad_speech_onset")
                                await _broadcast_json(
                                    {
                                        "type": "state",
                                        "state": "LISTENING",
                                        "epoch": new_ep,
                                        "speech_ms": vev.get("speech_ms", 0.0),
                                    }
                                )
                        elif vev["event"] == "speech_end":
                            _launch_asr_turn(
                                vev["audio_pcm"],
                                duration_ms=vev.get("duration_ms", 0.0),
                            )
        except WebSocketDisconnect:
            pass
        finally:
            authed_sockets.pop(websocket, None)
            active_sockets.discard(websocket)
            if not authed_sockets:
                await _stop_app_frame_pump()
            if client_registered:
                active_clients["count"] = max(0, active_clients["count"] - 1)
                if active_clients["count"] == 0:
                    director.trigger_barge_in("client_disconnect", next_state="IDLE")
                    if getattr(app.state, "tts_engine", None) is not None:
                        try:
                            app.state.tts_engine.abort(director.turn_epoch)
                        except Exception:
                            pass
                    await _restore_base_visual_engine()

    return app


class ColabOneLiveServerRunner:
    """Runs the Live Studio FastAPI ASGI app on 127.0.0.1:<port> in a daemon thread."""

    def __init__(
        self,
        app: FastAPI,
        *,
        host: str = "127.0.0.1",
        port: int = COLABONE_LIVE_PORT,
    ) -> None:
        if host != "127.0.0.1":
            raise ValueError("Live Studio 服务端仅允许绑定 127.0.0.1 回环地址")
        self.app = app
        self.host = host
        self.port = int(port)
        self._lock = threading.RLock()
        self._server: uvicorn.Server | None = None
        self._thread: threading.Thread | None = None

    def _is_running_locked(self) -> bool:
        return (
            self._thread is not None
            and self._thread.is_alive()
            and self._server is not None
            and not self._server.should_exit
        )

    def is_running(self) -> bool:
        with self._lock:
            return self._is_running_locked()

    def start(self, *, wait_timeout_s: float = 5.0) -> None:
        with self._lock:
            if self._is_running_locked():
                return
            config = uvicorn.Config(
                self.app,
                host=self.host,
                port=self.port,
                log_level="warning",
                access_log=False,
                timeout_graceful_shutdown=2,
            )
            server = uvicorn.Server(config)
            server.install_signal_handlers = lambda: None  # type: ignore[method-assign]
            thread = threading.Thread(
                target=server.run,
                name=f"colabone-live-{self.port}",
                daemon=True,
            )
            self._server = server
            self._thread = thread
            thread.start()

        deadline = time.monotonic() + max(0.2, float(wait_timeout_s))
        while time.monotonic() < deadline:
            if server.started:
                return
            if not thread.is_alive():
                with self._lock:
                    if self._server is server:
                        self._server = None
                        self._thread = None
                raise RuntimeError(
                    f"Live Studio 本地服务 (127.0.0.1:{self.port}) 启动失败"
                )
            time.sleep(0.05)

        server.should_exit = True
        with self._lock:
            if self._server is server:
                self._server = None
                self._thread = None
        if thread.is_alive():
            thread.join(timeout=2.0)
        raise RuntimeError(f"Live Studio 本地服务 (127.0.0.1:{self.port}) 启动超时")

    def stop(self, *, join_timeout_s: float = 5.0) -> None:
        with self._lock:
            server = self._server
            thread = self._thread
            self._server = None
            self._thread = None
        if server is not None:
            server.should_exit = True
        if thread is not None and thread.is_alive():
            thread.join(timeout=max(0.5, float(join_timeout_s)))


def constant_time_token_verifier(expected_token: str) -> Callable[[str], bool]:
    """Return a constant-time token comparison predicate for standalone testing."""
    secret = str(expected_token or "")

    def _verify(candidate: str) -> bool:
        if not secret or not isinstance(candidate, str) or not candidate:
            return False
        return hmac.compare_digest(candidate, secret)

    return _verify
