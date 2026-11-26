"""Resident streaming TTS worker and offline mock adapter for Live Studio.

Outputs 24 kHz mono Int16 PCM frames in 20 ms chunks (960 bytes per chunk) with
cooperative epoch / cancel_event barge-in abortion. Heavy ML packages (torch,
qwen-tts, voxcpm) execute only inside the isolated remote GPU worker subprocess.
"""

from __future__ import annotations

import array
import base64
import json
import os
import queue
import subprocess
import sys
import threading
import time
from abc import ABC, abstractmethod
from collections import deque
from pathlib import Path
from typing import Any, Callable, Iterator

from common.live_studio import (
    COLABONE_LIVE_AUDIO_CHUNK_MS,
    COLABONE_LIVE_AUDIO_OUT_CHUNK_BYTES,
    COLABONE_LIVE_AUDIO_OUT_RATE,
    scrub_colabone_live_secrets,
)

COLABONE_LIVE_TTS_SAMPLE_RATE = COLABONE_LIVE_AUDIO_OUT_RATE
COLABONE_LIVE_TTS_CHUNK_MS = COLABONE_LIVE_AUDIO_CHUNK_MS
COLABONE_LIVE_TTS_CHUNK_BYTES = COLABONE_LIVE_AUDIO_OUT_CHUNK_BYTES
COLABONE_LIVE_SUPPORTED_TTS_ENGINES = frozenset({"qwen3_tts", "voxcpm2"})

_SENSITIVE_ENV_KEYS = frozenset(
    {
        "HF_TOKEN",
        "HUGGING_FACE_HUB_TOKEN",
        "HUGGINGFACE_HUB_TOKEN",
        "HF_API_TOKEN",
        "OPENAI_API_KEY",
    }
)


def resample_pcm16_mono(
    pcm_bytes: bytes, src_rate: int, dst_rate: int = COLABONE_LIVE_TTS_SAMPLE_RATE
) -> bytes:
    """Resample mono little-endian Int16 PCM with 3-tap low-pass anti-aliasing on downsample."""
    if src_rate <= 0 or dst_rate <= 0:
        raise ValueError("采样率必须大于 0")
    if not pcm_bytes:
        return b""
    usable = len(pcm_bytes) - (len(pcm_bytes) % 2)
    if usable <= 0:
        return b""
    trimmed = pcm_bytes[:usable]
    if src_rate == dst_rate:
        return bytes(trimmed)

    n_src = usable // 2
    if n_src == 0:
        return b""

    try:
        import numpy as np  # type: ignore[import-not-found]
    except Exception:
        np = None

    if np is not None:
        src_arr = np.frombuffer(trimmed, dtype="<i2").astype(np.float32)
        if n_src == 1:
            n_dst = max(1, int(round(dst_rate / src_rate)))
            dst_arr = np.full(n_dst, src_arr[0], dtype=np.float32)
        else:
            if dst_rate < src_rate and n_src >= 3:
                # Lightweight 3-tap [0.25, 0.5, 0.25] anti-aliasing pre-filter (unity DC gain)
                padded = np.pad(src_arr, (1, 1), mode="edge")
                src_arr = 0.25 * padded[:-2] + 0.5 * padded[1:-1] + 0.25 * padded[2:]
            n_dst = max(1, int(round(n_src * (dst_rate / src_rate))))
            x_old = np.arange(n_src, dtype=np.float64)
            x_new = np.linspace(0.0, float(n_src - 1), n_dst, dtype=np.float64)
            dst_arr = np.interp(x_new, x_old, src_arr)
        clamped = np.clip(np.rint(dst_arr), -32768, 32767).astype("<i2")
        return clamped.tobytes()

    src = array.array("h")
    src.frombytes(trimmed)
    if sys.byteorder != "little":
        src.byteswap()
    if n_src == 1:
        n_dst = max(1, int(round(dst_rate / src_rate)))
        out = array.array("h", [src[0]] * n_dst)
    else:
        if dst_rate < src_rate and n_src >= 3:
            filtered: list[float] = [0.0] * n_src
            filtered[0] = 0.75 * src[0] + 0.25 * src[1]
            for i in range(1, n_src - 1):
                filtered[i] = 0.25 * src[i - 1] + 0.5 * src[i] + 0.25 * src[i + 1]
            filtered[-1] = 0.25 * src[-2] + 0.75 * src[-1]
        else:
            filtered = [float(s) for s in src]

        n_dst = max(1, int(round(n_src * (dst_rate / src_rate))))
        ratio = (n_src - 1) / max(1, n_dst - 1) if n_dst > 1 else 0.0
        out = array.array("h")
        for i in range(n_dst):
            pos = i * ratio
            idx = int(pos)
            frac = pos - idx
            if idx + 1 < n_src:
                val = filtered[idx] * (1.0 - frac) + filtered[idx + 1] * frac
            else:
                val = filtered[-1]
            clamped = max(-32768, min(32767, int(round(val))))
            out.append(clamped)
    if sys.byteorder != "little":
        out.byteswap()
    return out.tobytes()


def _is_aborted(
    epoch: int,
    cancel_event: threading.Event | None,
    get_current_epoch: Callable[[], int] | None,
) -> bool:
    if cancel_event is not None and cancel_event.is_set():
        return True
    if get_current_epoch is not None and get_current_epoch() != epoch:
        return True
    return False


def iter_pcm24k_chunks(
    pcm_bytes: bytes,
    chunk_bytes: int = COLABONE_LIVE_TTS_CHUNK_BYTES,
    *,
    epoch: int = 0,
    cancel_event: threading.Event | None = None,
    get_current_epoch: Callable[[], int] | None = None,
    pad_last: bool = True,
) -> Iterator[bytes]:
    """Slice 24 kHz mono Int16 PCM into exact 20 ms chunks with cooperative abort."""
    if chunk_bytes <= 0 or chunk_bytes % 2 != 0:
        raise ValueError("chunk_bytes 必须为正偶数")
    if not pcm_bytes:
        return
    offset = 0
    total = len(pcm_bytes)
    while offset < total:
        if _is_aborted(epoch, cancel_event, get_current_epoch):
            return
        chunk = pcm_bytes[offset : offset + chunk_bytes]
        offset += chunk_bytes
        if len(chunk) < chunk_bytes:
            if not pad_last:
                if len(chunk) % 2 != 0:
                    chunk += b"\x00"
                yield chunk
                return
            chunk = chunk + (b"\x00" * (chunk_bytes - len(chunk)))
        yield chunk


class ColabOneStreamingTtsBase(ABC):
    """Abstract streaming TTS engine producing 24 kHz mono Int16 20 ms chunks."""

    def __init__(
        self,
        engine_id: str = "qwen3_tts",
        *,
        sample_rate: int = COLABONE_LIVE_TTS_SAMPLE_RATE,
        chunk_ms: int = COLABONE_LIVE_TTS_CHUNK_MS,
    ) -> None:
        if engine_id not in COLABONE_LIVE_SUPPORTED_TTS_ENGINES:
            raise ValueError(f"不支持的流式 TTS 引擎: {engine_id!r}")
        self.engine_id = engine_id
        self.sample_rate = int(sample_rate)
        self.chunk_ms = int(chunk_ms)
        self.chunk_bytes = int(self.sample_rate * self.chunk_ms // 1000) * 2
        self._loaded = False
        self._disposed = False

    @property
    def is_loaded(self) -> bool:
        return self._loaded

    @property
    def is_disposed(self) -> bool:
        return self._disposed

    def _ensure_not_disposed(self) -> None:
        if self._disposed:
            raise RuntimeError(f"流式 TTS 引擎 {self.engine_id} 已卸载释放，无法继续使用")

    @abstractmethod
    def load(self) -> None:
        """Load or warm the resident TTS engine."""

    @abstractmethod
    def unload(self) -> None:
        """Unload and free GPU/process resources."""

    def abort(self, epoch: int) -> None:
        """Signal an in-flight synthesis turn to stop without unloading weights."""
        _ = epoch

    @abstractmethod
    def synthesize_stream(
        self,
        text: str,
        *,
        voice_preset: str = "shen_zhiyi",
        epoch: int = 0,
        cancel_event: threading.Event | None = None,
        get_current_epoch: Callable[[], int] | None = None,
    ) -> Iterator[bytes]:
        """Yield 20 ms 24 kHz Int16 PCM chunks for a text clause."""


class ColabOneMockStreamingTtsEngine(ColabOneStreamingTtsBase):
    """Deterministic tone-free PCM streaming TTS adapter for offline tests & dry runs.

    Emits silent (all-zero) 24 kHz mono Int16 20 ms chunks proportional to text
    length so tests can verify framing, timing, and cooperative epoch/cancel aborts
    without requiring GPU weights or audible test tones.
    """

    def __init__(
        self,
        engine_id: str = "qwen3_tts",
        *,
        chunks_per_char: int = 1,
        min_chunks: int = 2,
        max_chunks: int = 16,
        chunk_delay_s: float = 0.0,
    ) -> None:
        super().__init__(engine_id=engine_id)
        self.chunks_per_char = max(1, int(chunks_per_char))
        self.min_chunks = max(1, int(min_chunks))
        self.max_chunks = max(self.min_chunks, int(max_chunks))
        self.chunk_delay_s = max(0.0, float(chunk_delay_s))
        self.calls: list[dict[str, Any]] = []
        self.aborted_epochs: list[int] = []

    def load(self) -> None:
        self._ensure_not_disposed()
        self._loaded = True

    def unload(self) -> None:
        self._disposed = True
        self._loaded = False

    def abort(self, epoch: int) -> None:
        self.aborted_epochs.append(int(epoch))

    def synthesize_stream(
        self,
        text: str,
        *,
        voice_preset: str = "shen_zhiyi",
        epoch: int = 0,
        cancel_event: threading.Event | None = None,
        get_current_epoch: Callable[[], int] | None = None,
    ) -> Iterator[bytes]:
        self._ensure_not_disposed()
        cleaned = str(text or "").strip()
        if not cleaned:
            return
        if _is_aborted(epoch, cancel_event, get_current_epoch):
            self.abort(epoch)
            return
        if not self._loaded:
            self.load()
        self.calls.append(
            {
                "text": cleaned,
                "voice_preset": voice_preset,
                "epoch": epoch,
                "engine_id": self.engine_id,
            }
        )
        n_chunks = min(
            self.max_chunks,
            max(self.min_chunks, len(cleaned) * self.chunks_per_char),
        )
        silent_chunk = b"\x00" * self.chunk_bytes
        for _ in range(n_chunks):
            if self._disposed:
                return
            if _is_aborted(epoch, cancel_event, get_current_epoch):
                self.abort(epoch)
                return
            yield silent_chunk
            if self.chunk_delay_s > 0.0:
                if cancel_event is not None:
                    if cancel_event.wait(self.chunk_delay_s):
                        self.abort(epoch)
                        return
                else:
                    time.sleep(self.chunk_delay_s)


# Embedded resident worker protocol script executed inside an engine's isolated
# remote venv on Colab GPUs. Kept strictly out of host process imports.
COLABONE_LIVE_TTS_WORKER_SCRIPT = r"""
import base64
import inspect
import json
import os
import sys
import traceback
from pathlib import Path

# Duplicate FD 1 exclusively for JSON-lines IPC and redirect FD 1 + sys.stdout
# to stderr so third-party library prints can never corrupt the protocol stream.
_PROTO_FD = os.dup(1)
os.dup2(2, 1)
_PROTO_OUT = os.fdopen(_PROTO_FD, "w", encoding="utf-8", buffering=1)
sys.stdout = sys.stderr


def _emit(obj):
    _PROTO_OUT.write(json.dumps(obj, ensure_ascii=False) + "\n")
    _PROTO_OUT.flush()


def _to_float32_1d(arr):
    import numpy as np

    if hasattr(arr, "detach"):
        arr = arr.detach()
    if hasattr(arr, "cpu"):
        arr = arr.cpu()
    if hasattr(arr, "float") and callable(getattr(arr, "float")):
        arr = arr.float()
    if hasattr(arr, "numpy"):
        arr = arr.numpy()
    return np.asarray(arr, dtype=np.float32).reshape(-1)


def _sanitize_voxcpm_prompt(raw_prompt):
    cleaned = (
        str(raw_prompt or "")
        .replace("(", " ")
        .replace(")", " ")
        .replace("（", " ")
        .replace("）", " ")
    )
    return " ".join(cleaned.split())


def _detect_qwen3_mode(model_dir, model):
    explicit = os.environ.get("COLABONE_LIVE_TTS_QWEN_MODE", "").strip().lower()
    if explicit in ("voice_design", "custom_voice", "voice_clone"):
        return explicit
    hints = [str(model_dir or "").lower()]
    try:
        cfg_path = Path(model_dir) / "config.json"
        if cfg_path.is_file():
            cfg = json.loads(cfg_path.read_text(encoding="utf-8"))
            if isinstance(cfg, dict):
                for key in ("tts_model_type", "_name_or_path", "model_type"):
                    val = cfg.get(key)
                    if val:
                        hints.append(str(val).lower())
    except Exception:
        pass
    joined = " ".join(hints)
    if "customvoice" in joined or "custom_voice" in joined:
        return "custom_voice"
    if "voicedesign" in joined or "voice_design" in joined:
        return "voice_design"
    if "voice_clone" in joined or "1.7b-base" in joined or "-base" in joined:
        return "voice_clone"
    if callable(getattr(model, "generate_voice_design", None)):
        return "voice_design"
    if callable(getattr(model, "generate_custom_voice", None)):
        return "custom_voice"
    if callable(getattr(model, "generate_voice_clone", None)):
        return "voice_clone"
    return "generic"


def _filter_kwargs(fn, kwargs):
    try:
        sig = inspect.signature(fn)
        params = sig.parameters
        if any(p.kind == inspect.Parameter.VAR_KEYWORD for p in params.values()):
            return kwargs
        return {k: v for k, v in kwargs.items() if k in params}
    except (TypeError, ValueError):
        return kwargs


def _inspect_voxcpm_generate(model):
    gen_sig = inspect.signature(model.generate)
    gen_params = gen_sig.parameters
    named = [
        p
        for p in gen_params.values()
        if p.kind not in (inspect.Parameter.VAR_POSITIONAL, inspect.Parameter.VAR_KEYWORD)
    ]
    if not named:
        inner = getattr(model, "_generate", model.generate)
        if inner is not model.generate and callable(inner):
            try:
                inner_sig = inspect.signature(inner)
                inner_params = inner_sig.parameters
                if any(
                    p.kind not in (inspect.Parameter.VAR_POSITIONAL, inspect.Parameter.VAR_KEYWORD)
                    for p in inner_params.values()
                ):
                    gen_params = inner_params
            except (TypeError, ValueError):
                pass
    has_var_kw = any(p.kind == inspect.Parameter.VAR_KEYWORD for p in gen_params.values())
    return gen_params, has_var_kw


def main():
    engine_id = os.environ.get("COLABONE_LIVE_TTS_ENGINE", "qwen3_tts")
    model_dir = os.environ.get("COLABONE_LIVE_TTS_MODEL_DIR", "")
    model = None
    sample_rate = 24000
    qwen_mode = "generic"
    vox_params = {}
    vox_has_var_kw = True

    try:
        if engine_id == "qwen3_tts":
            import torch
            from qwen_tts import Qwen3TTSModel

            use_cuda = torch.cuda.is_available()
            dtype = (
                torch.bfloat16
                if (use_cuda and torch.cuda.is_bf16_supported())
                else (torch.float16 if use_cuda else torch.float32)
            )
            device = "cuda:0" if use_cuda else "cpu"
            try:
                model = Qwen3TTSModel.from_pretrained(model_dir, device_map=device, dtype=dtype)
            except TypeError:
                model = Qwen3TTSModel.from_pretrained(
                    model_dir, device_map=device, torch_dtype=dtype
                )
            qwen_mode = _detect_qwen3_mode(model_dir, model)
            sample_rate = 24000
        elif engine_id == "voxcpm2":
            from voxcpm import VoxCPM

            sig = inspect.signature(VoxCPM.from_pretrained)
            base_kwargs = {}
            if "load_denoiser" in sig.parameters:
                base_kwargs["load_denoiser"] = False
            if "optimize" in sig.parameters:
                base_kwargs["optimize"] = False
            model = VoxCPM.from_pretrained(model_dir, **base_kwargs)
            sample_rate = int(
                getattr(getattr(model, "tts_model", None), "sample_rate", None)
                or getattr(model, "sample_rate", None)
                or 48000
            )
            vox_params, vox_has_var_kw = _inspect_voxcpm_generate(model)
        else:
            raise ValueError(f"Unsupported engine_id: {engine_id}")
    except Exception as exc:
        _emit({"type": "error", "detail": f"load_failed: {exc}"})
        return 1

    _emit({"type": "ready", "engine_id": engine_id, "sample_rate": sample_rate})

    for raw_line in sys.stdin:
        line = raw_line.strip()
        if not line:
            continue
        try:
            req = json.loads(line)
        except Exception:
            continue
        cmd = req.get("cmd")
        if cmd == "shutdown":
            _emit({"type": "bye"})
            return 0
        if cmd == "abort":
            _emit({"type": "aborted", "epoch": int(req.get("epoch", 0))})
            continue
        if cmd != "synthesize":
            continue

        epoch = int(req.get("epoch", 0))
        text = str(req.get("text", "")).strip()
        instruct = str(req.get("instruct", "")).strip()
        speaker = str(req.get("speaker", "")).strip() or "Vivian"
        ref_audio = str(req.get("ref_audio", "")).strip()
        ref_text = str(req.get("ref_text", "")).strip()
        if not text:
            _emit({"type": "done", "epoch": epoch})
            continue

        try:
            import numpy as np

            if engine_id == "qwen3_tts":
                eff_instruct = instruct or "自然女声"
                if qwen_mode == "custom_voice" and callable(
                    getattr(model, "generate_custom_voice", None)
                ):
                    cv_kwargs = _filter_kwargs(
                        model.generate_custom_voice,
                        {
                            "text": text,
                            "language": "Auto",
                            "speaker": speaker,
                            "instruct": eff_instruct or None,
                        },
                    )
                    wavs, sr = model.generate_custom_voice(**cv_kwargs)
                elif qwen_mode == "voice_design" and callable(
                    getattr(model, "generate_voice_design", None)
                ):
                    wavs, sr = model.generate_voice_design(
                        text=text, language="Auto", instruct=eff_instruct
                    )
                elif qwen_mode == "voice_clone" and callable(
                    getattr(model, "generate_voice_clone", None)
                ):
                    vc_kwargs = _filter_kwargs(
                        model.generate_voice_clone,
                        {
                            "text": text,
                            "language": "Auto",
                            "ref_audio": ref_audio or None,
                            "ref_text": ref_text or None,
                            "x_vector_only_mode": (not bool(ref_text)),
                        },
                    )
                    wavs, sr = model.generate_voice_clone(**vc_kwargs)
                elif callable(getattr(model, "generate_voice_design", None)):
                    wavs, sr = model.generate_voice_design(
                        text=text, language="Auto", instruct=eff_instruct
                    )
                elif callable(getattr(model, "generate_custom_voice", None)):
                    cv_kwargs = _filter_kwargs(
                        model.generate_custom_voice,
                        {
                            "text": text,
                            "language": "Auto",
                            "speaker": speaker,
                            "instruct": eff_instruct or None,
                        },
                    )
                    wavs, sr = model.generate_custom_voice(**cv_kwargs)
                elif callable(getattr(model, "generate_voice_clone", None)):
                    vc_kwargs = _filter_kwargs(
                        model.generate_voice_clone,
                        {
                            "text": text,
                            "language": "Auto",
                            "ref_audio": ref_audio or None,
                            "ref_text": ref_text or None,
                            "x_vector_only_mode": (not bool(ref_text)),
                        },
                    )
                    wavs, sr = model.generate_voice_clone(**vc_kwargs)
                elif callable(getattr(model, "generate", None)):
                    out = model.generate(text=text)
                    if isinstance(out, tuple) and len(out) == 2:
                        wavs, sr = out
                    else:
                        wavs, sr = out, sample_rate
                else:
                    raise RuntimeError("Qwen3-TTS model has no supported generate method")
                raw_wav = wavs[0] if isinstance(wavs, (list, tuple)) else wavs
                wav = _to_float32_1d(raw_wav)
                sr = int(sr or sample_rate)
            else:
                instruct = _sanitize_voxcpm_prompt(instruct)
                gen_kwargs = {
                    "text": text,
                    "inference_timesteps": 18,
                    "cfg_value": 2.0,
                    "normalize": True,
                }
                if "voice_prompt" in vox_params and instruct:
                    gen_kwargs["voice_prompt"] = instruct
                else:
                    eff_text = f"({instruct}){text}" if instruct else text
                    gen_kwargs["text"] = eff_text
                if not vox_has_var_kw:
                    gen_kwargs = {k: v for k, v in gen_kwargs.items() if k in vox_params}
                out = model.generate(**gen_kwargs)
                if isinstance(out, tuple) and len(out) == 2:
                    raw_wav, sr = out
                else:
                    raw_wav, sr = out, sample_rate
                wav = _to_float32_1d(raw_wav)
                sr = int(sr or sample_rate)

            pcm16 = np.clip(wav, -1.0, 1.0)
            pcm_bytes = (pcm16 * 32767.0).astype("<i2").tobytes()
            _emit(
                {
                    "type": "pcm",
                    "epoch": epoch,
                    "sample_rate": int(sr),
                    "data_b64": base64.b64encode(pcm_bytes).decode("ascii"),
                }
            )
            _emit({"type": "done", "epoch": epoch})
        except Exception as exc:
            _emit({"type": "error", "epoch": epoch, "detail": str(exc)})
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
"""


class ColabOneResidentTtsWorker(ColabOneStreamingTtsBase):
    """Resident isolated-venv GPU TTS worker adapter for Qwen3-TTS and VoxCPM2.

    Keeps model weights resident in VRAM across turns and supports cooperative
    epoch cancellation without restarting the worker process.
    """

    def __init__(
        self,
        engine_id: str = "qwen3_tts",
        *,
        venv_python: Path | str | None = None,
        model_dir: Path | str | None = None,
        hf_token: str = "",
        startup_timeout_s: float = 120.0,
        read_timeout_s: float = 60.0,
        fallback_mock: bool = False,
    ) -> None:
        super().__init__(engine_id=engine_id)
        self.venv_python = Path(venv_python) if venv_python else None
        self.model_dir = Path(model_dir) if model_dir else None
        self._hf_token = hf_token
        self.startup_timeout_s = max(0.2, float(startup_timeout_s))
        self.read_timeout_s = max(0.2, float(read_timeout_s))
        self.fallback_mock = bool(fallback_mock)
        self._mock = ColabOneMockStreamingTtsEngine(engine_id=engine_id) if fallback_mock else None
        self._proc: subprocess.Popen[str] | None = None
        self._starting_proc: subprocess.Popen[str] | None = None
        self._lock = threading.RLock()
        self._load_lock = threading.Lock()
        self._turn_lock = threading.Lock()
        self._stdin_lock = threading.Lock()
        self._abort_lock = threading.Lock()
        self._aborted_epoch: int = -1
        self._active_proc: subprocess.Popen[str] | None = None
        self._abort_queue: queue.SimpleQueue[tuple[subprocess.Popen[str], int] | None] | None = None
        self._abort_thread: threading.Thread | None = None
        self._native_sample_rate = 48000 if engine_id == "voxcpm2" else 24000
        self._stdout_queue: queue.Queue[str | None] | None = None
        self._stdout_thread: threading.Thread | None = None
        self._stderr_lines: deque[str] = deque(maxlen=64)
        self._stderr_thread: threading.Thread | None = None

    def _is_epoch_aborted(self, epoch: int) -> bool:
        with self._abort_lock:
            return self._aborted_epoch >= 0 and int(epoch) <= self._aborted_epoch

    def _ensure_abort_dispatcher_locked(self) -> queue.SimpleQueue[tuple[subprocess.Popen[str], int] | None]:
        """Start a lightweight background daemon thread for non-blocking abort stdin writes."""
        if self._abort_queue is not None and self._abort_thread is not None and self._abort_thread.is_alive():
            return self._abort_queue
        q: queue.SimpleQueue[tuple[subprocess.Popen[str], int] | None] = queue.SimpleQueue()

        def _dispatch_aborts() -> None:
            while True:
                item = q.get()
                if item is None:
                    return
                proc, ep = item
                if proc.poll() is not None or proc.stdin is None:
                    continue
                try:
                    with self._stdin_lock:
                        if proc.poll() is None and proc.stdin is not None:
                            proc.stdin.write(
                                json.dumps({"cmd": "abort", "epoch": int(ep)}) + "\n"
                            )
                            proc.stdin.flush()
                except Exception:
                    pass

        t = threading.Thread(
            target=_dispatch_aborts,
            name=f"colabone-tts-abort-{self.engine_id}",
            daemon=True,
        )
        self._abort_queue = q
        self._abort_thread = t
        t.start()
        return q

    def _scrub_error(self, msg: str) -> str:
        return scrub_colabone_live_secrets(msg, self._hf_token)

    def _recent_stderr_summary(self, stderr_lines: deque[str] | None = None) -> str:
        buf = self._stderr_lines if stderr_lines is None else stderr_lines
        lines = [line for line in list(buf)[-6:] if line]
        if not lines:
            return ""
        return self._scrub_error(" | ".join(lines)[:300])

    @staticmethod
    def _close_proc_pipes(proc: subprocess.Popen[str]) -> None:
        for stream in (proc.stdin, proc.stdout, proc.stderr):
            if stream is not None:
                try:
                    stream.close()
                except Exception:
                    pass

    @classmethod
    def _cleanup_proc(
        cls,
        proc: subprocess.Popen[str],
        *,
        graceful_shutdown: bool = False,
        stdout_thread: threading.Thread | None = None,
        stderr_thread: threading.Thread | None = None,
    ) -> None:
        if graceful_shutdown:
            try:
                if proc.poll() is None and proc.stdin is not None:
                    proc.stdin.write(json.dumps({"cmd": "shutdown"}) + "\n")
                    proc.stdin.flush()
                    proc.wait(timeout=3.0)
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
                    proc.wait(timeout=2.0)
                except Exception:
                    pass
        else:
            try:
                proc.wait(timeout=0.5)
            except Exception:
                pass
        cur_thread = threading.current_thread()
        for reader in (stdout_thread, stderr_thread):
            if reader is not None and reader is not cur_thread:
                try:
                    reader.join(timeout=1.0)
                except Exception:
                    pass
        cls._close_proc_pipes(proc)

    def _start_stream_readers(
        self, proc: subprocess.Popen[str]
    ) -> tuple[queue.Queue[str | None], threading.Thread, threading.Thread, deque[str]]:
        out_q: queue.Queue[str | None] = queue.Queue()
        stderr_lines: deque[str] = deque(maxlen=64)

        def _drain_stdout() -> None:
            stream = proc.stdout
            try:
                if stream is not None:
                    for line in stream:
                        out_q.put(line)
            except Exception:
                pass
            finally:
                out_q.put(None)

        def _drain_stderr() -> None:
            stream = proc.stderr
            try:
                if stream is not None:
                    for line in stream:
                        cleaned = line.strip()
                        if cleaned:
                            stderr_lines.append(cleaned)
            except Exception:
                pass

        t_out = threading.Thread(
            target=_drain_stdout,
            name=f"colabone-tts-out-{self.engine_id}",
            daemon=True,
        )
        t_err = threading.Thread(
            target=_drain_stderr,
            name=f"colabone-tts-err-{self.engine_id}",
            daemon=True,
        )
        t_out.start()
        t_err.start()
        return out_q, t_out, t_err, stderr_lines

    def build_worker_command(self) -> list[str]:
        py = str(self.venv_python) if self.venv_python else sys.executable
        return [py, "-u", "-c", COLABONE_LIVE_TTS_WORKER_SCRIPT]

    def build_worker_env(self) -> dict[str, str]:
        env = {k: v for k, v in os.environ.items() if k not in _SENSITIVE_ENV_KEYS}
        env["COLABONE_LIVE_TTS_ENGINE"] = self.engine_id
        if self.model_dir is not None:
            env["COLABONE_LIVE_TTS_MODEL_DIR"] = str(self.model_dir)
        if self._hf_token:
            env["HF_TOKEN"] = self._hf_token
        env["PYTHONUNBUFFERED"] = "1"
        env["PYTHONUTF8"] = "1"
        env["PYTHONIOENCODING"] = "utf-8"
        return env

    def _voice_instruct(self, voice_preset: str) -> str:
        try:
            from server.tts_engines import COLABONE_TTS_VOICE_PRESETS

            preset = COLABONE_TTS_VOICE_PRESETS.get(voice_preset)
            if isinstance(preset, dict):
                if self.engine_id == "voxcpm2":
                    return str(preset.get("voxcpm_prompt") or "")
                return str(preset.get("qwen_instruct") or "")
        except Exception:
            pass
        return ""

    def _voice_speaker(self, voice_preset: str) -> str:
        try:
            from server.tts_engines import COLABONE_TTS_VOICE_PRESETS

            preset = COLABONE_TTS_VOICE_PRESETS.get(voice_preset)
            if isinstance(preset, dict):
                return str(preset.get("speaker_id") or "").strip()
        except Exception:
            pass
        return ""

    def _reset_proc(self, *, graceful_shutdown: bool = False) -> None:
        """Detach and reap current worker subprocess outside self._lock (without disposing)."""
        with self._lock:
            proc = self._proc
            t_out = self._stdout_thread
            t_err = self._stderr_thread
            self._proc = None
            self._stdout_queue = None
            self._stdout_thread = None
            self._stderr_thread = None
            self._stderr_lines = deque(maxlen=64)
            self._loaded = False
        with self._abort_lock:
            if self._active_proc is proc:
                self._active_proc = None
        if proc is not None:
            self._cleanup_proc(
                proc,
                graceful_shutdown=graceful_shutdown,
                stdout_thread=t_out,
                stderr_thread=t_err,
            )

    def load(self) -> None:
        with self._load_lock:
            with self._lock:
                self._ensure_not_disposed()
                if self._loaded:
                    return
                if self._mock is not None and (
                    self.venv_python is None or not self.venv_python.is_file()
                ):
                    self._mock.load()
                    self._loaded = True
                    return
                if self.venv_python is None or not self.venv_python.is_file():
                    raise RuntimeError(
                        f"流式 TTS 引擎 {self.engine_id} 的独立运行环境尚未就绪"
                    )
                cmd = self.build_worker_command()
                env = self.build_worker_env()

            proc = subprocess.Popen(
                cmd,
                stdin=subprocess.PIPE,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                text=True,
                encoding="utf-8",
                errors="replace",
                env=env,
            )
            out_q, t_out, t_err, stderr_lines = self._start_stream_readers(proc)
            with self._lock:
                if self._disposed:
                    disposed_early = True
                else:
                    self._starting_proc = proc
                    disposed_early = False
            if disposed_early:
                self._cleanup_proc(
                    proc,
                    graceful_shutdown=False,
                    stdout_thread=t_out,
                    stderr_thread=t_err,
                )
                self._ensure_not_disposed()

            try:
                deadline = time.monotonic() + self.startup_timeout_s
                msg: dict[str, Any] | None = None
                while True:
                    if self._disposed:
                        raise RuntimeError(
                            f"流式 TTS 引擎 {self.engine_id} 已卸载释放，无法继续加载"
                        )
                    remaining = deadline - time.monotonic()
                    if remaining <= 0.0:
                        detail = self._recent_stderr_summary(stderr_lines)
                        suffix = f": {detail}" if detail else ""
                        raise RuntimeError(
                            self._scrub_error(
                                f"流式 TTS 常驻进程 {self.engine_id} 启动超时（>{self.startup_timeout_s:.1f}s）{suffix}"
                            )
                        )
                    try:
                        raw_line = out_q.get(timeout=min(0.1, remaining))
                    except queue.Empty:
                        continue
                    if raw_line is None:
                        if self._disposed:
                            raise RuntimeError(
                                f"流式 TTS 引擎 {self.engine_id} 已卸载释放"
                            )
                        detail = self._recent_stderr_summary(stderr_lines)
                        suffix = f": {detail}" if detail else ""
                        raise RuntimeError(
                            self._scrub_error(
                                f"流式 TTS 常驻进程 {self.engine_id} 启动失败{suffix}"
                            )
                        )
                    cleaned_line = raw_line.strip()
                    if not cleaned_line:
                        continue
                    try:
                        parsed = json.loads(cleaned_line)
                    except ValueError:
                        # Skip non-JSON stdout noise emitted by third-party libraries
                        continue
                    if not isinstance(parsed, dict):
                        continue
                    if parsed.get("type") in ("ready", "error"):
                        msg = parsed
                        break

                if msg is None or msg.get("type") != "ready":
                    detail = (
                        msg.get("detail", "unknown")
                        if isinstance(msg, dict)
                        else "unknown"
                    )
                    raise RuntimeError(
                        self._scrub_error(f"流式 TTS 常驻进程初始化失败: {detail}")
                    )

                with self._lock:
                    if self._starting_proc is proc:
                        self._starting_proc = None
                    if self._disposed:
                        publish_ok = False
                    else:
                        self._native_sample_rate = int(
                            msg.get("sample_rate", self._native_sample_rate)
                        )
                        self._proc = proc
                        self._stdout_queue = out_q
                        self._stdout_thread = t_out
                        self._stderr_lines = stderr_lines
                        self._stderr_thread = t_err
                        self._loaded = True
                        publish_ok = True
                if not publish_ok:
                    self._cleanup_proc(
                        proc,
                        graceful_shutdown=True,
                        stdout_thread=t_out,
                        stderr_thread=t_err,
                    )
                    self._ensure_not_disposed()
                with self._abort_lock:
                    self._active_proc = proc
                    self._ensure_abort_dispatcher_locked()
            except Exception:
                with self._lock:
                    if self._starting_proc is proc:
                        self._starting_proc = None
                self._cleanup_proc(
                    proc,
                    graceful_shutdown=False,
                    stdout_thread=t_out,
                    stderr_thread=t_err,
                )
                raise

    def abort(self, epoch: int) -> None:
        """Non-blocking abort safe to call directly from the asyncio event loop."""
        ep = int(epoch)
        with self._abort_lock:
            if ep > self._aborted_epoch:
                self._aborted_epoch = ep
            proc = self._active_proc
            abort_q = self._abort_queue
        if self._mock is not None:
            self._mock.abort(ep)
        if proc is not None and proc.poll() is None and proc.stdin is not None:
            if abort_q is not None:
                abort_q.put_nowait((proc, ep))
            else:
                def _send_once(target_proc: subprocess.Popen[str], target_ep: int) -> None:
                    try:
                        with self._stdin_lock:
                            if target_proc.poll() is None and target_proc.stdin is not None:
                                target_proc.stdin.write(
                                    json.dumps({"cmd": "abort", "epoch": target_ep}) + "\n"
                                )
                                target_proc.stdin.flush()
                    except Exception:
                        pass

                threading.Thread(
                    target=_send_once,
                    args=(proc, ep),
                    name=f"colabone-tts-abort-once-{self.engine_id}",
                    daemon=True,
                ).start()

    def unload(self) -> None:
        with self._lock:
            self._disposed = True
            if self._mock is not None:
                self._mock.unload()
            proc = self._proc
            starting_proc = self._starting_proc
            t_out = self._stdout_thread
            t_err = self._stderr_thread
            self._proc = None
            self._starting_proc = None
            self._stdout_queue = None
            self._stdout_thread = None
            self._stderr_thread = None
            self._stderr_lines = deque(maxlen=64)
            self._loaded = False
        with self._abort_lock:
            self._active_proc = None
            abort_q = self._abort_queue
            self._abort_queue = None
            self._abort_thread = None
        if abort_q is not None:
            try:
                abort_q.put_nowait(None)
            except Exception:
                pass
        if starting_proc is not None and starting_proc is not proc:
            self._cleanup_proc(starting_proc, graceful_shutdown=False)
        if proc is not None:
            self._cleanup_proc(
                proc,
                graceful_shutdown=True,
                stdout_thread=t_out,
                stderr_thread=t_err,
            )

    def synthesize_stream(
        self,
        text: str,
        *,
        voice_preset: str = "shen_zhiyi",
        epoch: int = 0,
        cancel_event: threading.Event | None = None,
        get_current_epoch: Callable[[], int] | None = None,
    ) -> Iterator[bytes]:
        self._ensure_not_disposed()
        cleaned = str(text or "").strip()
        if not cleaned:
            return
        if _is_aborted(epoch, cancel_event, get_current_epoch) or self._is_epoch_aborted(epoch):
            self.abort(epoch)
            return
        if not self._loaded:
            self.load()
        if self._proc is None and self._mock is not None:
            yield from self._mock.synthesize_stream(
                cleaned,
                voice_preset=voice_preset,
                epoch=epoch,
                cancel_event=cancel_event,
                get_current_epoch=get_current_epoch,
            )
            return

        with self._turn_lock:
            self._ensure_not_disposed()
            if _is_aborted(epoch, cancel_event, get_current_epoch) or self._is_epoch_aborted(epoch):
                self.abort(epoch)
                return

            with self._lock:
                self._ensure_not_disposed()
                proc = self._proc
                out_q = self._stdout_queue
                stderr_lines = self._stderr_lines
                proc_invalid = (
                    proc is None
                    or out_q is None
                    or proc.poll() is not None
                    or proc.stdin is None
                    or proc.stdout is None
                )
            if proc_invalid:
                detail = self._recent_stderr_summary(stderr_lines)
                suffix = f": {detail}" if detail else ""
                self._reset_proc(graceful_shutdown=False)
                raise RuntimeError(
                    self._scrub_error(
                        f"流式 TTS 常驻进程 {self.engine_id} 未在运行{suffix}"
                    )
                )

            assert proc is not None and out_q is not None and proc.stdin is not None
            payload = {
                "cmd": "synthesize",
                "epoch": int(epoch),
                "text": cleaned,
                "voice_preset": voice_preset,
                "instruct": self._voice_instruct(voice_preset),
                "speaker": self._voice_speaker(voice_preset),
            }
            try:
                with self._stdin_lock:
                    proc.stdin.write(json.dumps(payload, ensure_ascii=False) + "\n")
                    proc.stdin.flush()
            except OSError as exc:
                detail = self._recent_stderr_summary(stderr_lines)
                suffix = f" ({detail})" if detail else ""
                self._reset_proc(graceful_shutdown=False)
                raise RuntimeError(
                    self._scrub_error(f"流式 TTS 写入任务失败: {exc}{suffix}")
                ) from None

            chunk_deadline = time.monotonic() + self.read_timeout_s
            while True:
                if self._disposed:
                    return
                if _is_aborted(epoch, cancel_event, get_current_epoch) or self._is_epoch_aborted(epoch):
                    self.abort(epoch)
                    return
                remaining = chunk_deadline - time.monotonic()
                if remaining <= 0.0:
                    detail = self._recent_stderr_summary(stderr_lines)
                    suffix = f": {detail}" if detail else ""
                    self.abort(epoch)
                    self._reset_proc(graceful_shutdown=False)
                    raise RuntimeError(
                        self._scrub_error(
                            f"流式 TTS 引擎 {self.engine_id} 合成超时（>{self.read_timeout_s:.1f}s）{suffix}"
                        )
                    )
                try:
                    raw_line = out_q.get(timeout=min(0.05, remaining))
                except queue.Empty:
                    continue

                if raw_line is None:
                    if (
                        self._disposed
                        or _is_aborted(epoch, cancel_event, get_current_epoch)
                        or self._is_epoch_aborted(epoch)
                    ):
                        return
                    detail = self._recent_stderr_summary(stderr_lines)
                    suffix = f": {detail}" if detail else ""
                    self._reset_proc(graceful_shutdown=False)
                    raise RuntimeError(
                        self._scrub_error(
                            f"流式 TTS 常驻进程 {self.engine_id} 意外退出{suffix}"
                        )
                    )
                raw_line = raw_line.strip()
                if not raw_line:
                    continue
                try:
                    msg = json.loads(raw_line)
                except ValueError:
                    continue
                if not isinstance(msg, dict):
                    continue

                mtype = msg.get("type")
                msg_epoch_raw = msg.get("epoch")
                if msg_epoch_raw is not None:
                    try:
                        if int(msg_epoch_raw) != int(epoch):
                            # Drain stale-epoch pcm / done / aborted / error messages
                            continue
                    except (TypeError, ValueError):
                        continue

                chunk_deadline = time.monotonic() + self.read_timeout_s

                if mtype == "pcm":
                    raw_pcm = base64.b64decode(msg.get("data_b64", ""))
                    src_rate = int(msg.get("sample_rate", self._native_sample_rate))
                    pcm_24k = resample_pcm16_mono(raw_pcm, src_rate, self.sample_rate)
                    for chunk in iter_pcm24k_chunks(
                        pcm_24k,
                        chunk_bytes=self.chunk_bytes,
                        epoch=epoch,
                        cancel_event=cancel_event,
                        get_current_epoch=get_current_epoch,
                    ):
                        if self._disposed or self._is_epoch_aborted(epoch):
                            self.abort(epoch)
                            return
                        yield chunk
                    if _is_aborted(epoch, cancel_event, get_current_epoch) or self._is_epoch_aborted(epoch):
                        self.abort(epoch)
                        return
                elif mtype in ("done", "aborted"):
                    return
                elif mtype == "error":
                    raise RuntimeError(
                        self._scrub_error(
                            f"流式 TTS 合成失败: {msg.get('detail', 'unknown')}"
                        )
                    )


def create_colabone_streaming_tts_engine(
    engine_id: str = "qwen3_tts",
    *,
    mock: bool = False,
    **kwargs: Any,
) -> ColabOneStreamingTtsBase:
    """Factory creating either an offline mock or resident GPU streaming TTS engine."""
    if mock:
        kwargs.pop("fallback_mock", None)
        return ColabOneMockStreamingTtsEngine(engine_id=engine_id, **kwargs)
    return ColabOneResidentTtsWorker(engine_id=engine_id, **kwargs)
