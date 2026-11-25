"""Self-contained remote GPU worker script body for OpenMOSS-Team/MOSS-TTS-v1.5.

Bundled directly into the remote Colab service bundle; imports only Python stdlib
at module import time. Concatenated right after ``_WORKER_COMMON_HEADER`` in
``server/tts_engines.py``.
"""

from __future__ import annotations

import textwrap

__all__ = ["MOSS_TTS_V1_5_WORKER_BODY"]

MOSS_TTS_V1_5_WORKER_BODY: str = textwrap.dedent(
    """\

    def _configure_moss_sdpa(torch_mod) -> None:
        cuda_backends = getattr(getattr(torch_mod, "backends", None), "cuda", None)
        if cuda_backends is None:
            return
        if hasattr(cuda_backends, "enable_cudnn_sdp"):
            try:
                torch_mod.backends.cuda.enable_cudnn_sdp(False)
            except Exception:
                pass
        if hasattr(cuda_backends, "enable_flash_sdp"):
            try:
                torch_mod.backends.cuda.enable_flash_sdp(True)
            except Exception:
                pass
        if hasattr(cuda_backends, "enable_mem_efficient_sdp"):
            try:
                torch_mod.backends.cuda.enable_mem_efficient_sdp(True)
            except Exception:
                pass
        if hasattr(cuda_backends, "enable_math_sdp"):
            try:
                torch_mod.backends.cuda.enable_math_sdp(True)
            except Exception:
                pass


    def _resolve_moss_attn_implementation(torch_mod, device: str, dtype) -> str:
        import importlib.util

        if str(device).startswith("cuda") and torch_mod.cuda.is_available():
            has_flash_attn = importlib.util.find_spec("flash_attn") is not None
            if has_flash_attn and dtype in (torch_mod.float16, torch_mod.bfloat16):
                try:
                    major, _minor = torch_mod.cuda.get_device_capability()
                except Exception:
                    major = 0
                if major >= 8:
                    return "flash_attention_2"
            return "sdpa"
        return "eager"


    def _seed_moss_generators(seed) -> None:
        if not (isinstance(seed, int) and not isinstance(seed, bool) and seed >= 0):
            return
        try:
            import random as _py_random

            _py_random.seed(seed)
        except Exception:
            pass
        try:
            import numpy as _np

            _np.random.seed(seed % (2**32))
        except Exception:
            pass
        try:
            import torch as _torch

            _torch.manual_seed(seed)
            cuda_mod = getattr(_torch, "cuda", None)
            if cuda_mod is not None and callable(getattr(cuda_mod, "manual_seed_all", None)):
                cuda_mod.manual_seed_all(seed)
        except Exception:
            pass


    def _is_kana_letter(code: int) -> bool:
        return (
            0x3041 <= code <= 0x3096
            or 0x30A1 <= code <= 0x30FA
            or 0x31F0 <= code <= 0x31FF
            or 0xFF66 <= code <= 0xFF6F
            or 0xFF71 <= code <= 0xFF9D
        )


    def _detect_moss_language(text: str) -> str | None:
        import re

        cleaned = re.sub(r"\\[[^\\[\\]\\n]{1,80}\\]", " ", str(text or ""))
        han_count = 0
        latin_count = 0
        kana_count = 0
        hangul_count = 0
        thai_count = 0
        greek_count = 0
        hebrew_count = 0
        devanagari_count = 0
        for ch in cleaned:
            code = ord(ch)
            if _is_kana_letter(code):
                kana_count += 1
            elif (
                0xAC00 <= code <= 0xD7AF
                or 0x1100 <= code <= 0x11FF
                or 0x3130 <= code <= 0x318F
            ):
                hangul_count += 1
            elif (
                0x4E00 <= code <= 0x9FFF
                or 0x3400 <= code <= 0x4DBF
                or 0x20000 <= code <= 0x2A6DF
            ):
                han_count += 1
            elif (
                0x0041 <= code <= 0x005A
                or 0x0061 <= code <= 0x007A
                or (0x00C0 <= code <= 0x024F and ch.isalpha())
            ):
                latin_count += 1
            elif 0x0E01 <= code <= 0x0E3A or 0x0E40 <= code <= 0x0E4E:
                thai_count += 1
            elif (0x0370 <= code <= 0x03FF or 0x1F00 <= code <= 0x1FFF) and ch.isalpha():
                greek_count += 1
            elif 0x05D0 <= code <= 0x05EA:
                hebrew_count += 1
            elif 0x0900 <= code <= 0x0963 or 0x0970 <= code <= 0x097F:
                devanagari_count += 1

        if kana_count >= 2 or (kana_count >= 1 and han_count == 0 and latin_count == 0):
            return "Japanese"
        if hangul_count >= 1 and (
            hangul_count >= 2 or (han_count == 0 and latin_count == 0)
        ):
            return "Korean"
        if thai_count >= 2 or (thai_count >= 1 and han_count == 0 and latin_count == 0):
            return "Thai"
        if hebrew_count >= 2 or (
            hebrew_count >= 1 and han_count == 0 and latin_count == 0
        ):
            return "Hebrew"
        if devanagari_count >= 2 or (
            devanagari_count >= 1 and han_count == 0 and latin_count == 0
        ):
            return "Hindi"
        if greek_count >= 3 and greek_count >= (han_count + latin_count):
            return "Greek"
        return None


    def _encode_moss_reference(processor, ref_audio_path: str):
        if not ref_audio_path:
            return None
        encode_fn = getattr(processor, "encode_audios_from_path", None)
        if callable(encode_fn):
            try:
                encoded = encode_fn([ref_audio_path])
                if encoded is not None and len(encoded) > 0 and encoded[0] is not None:
                    return encoded[0]
            except Exception:
                pass
        return ref_audio_path


    def _load_moss_model(auto_model_cls, local_dir: str, attn_impl: str, dtype, device: str):
        try:
            model = auto_model_cls.from_pretrained(
                local_dir,
                trust_remote_code=True,
                attn_implementation=attn_impl,
                dtype=dtype,
            )
        except TypeError:
            model = auto_model_cls.from_pretrained(
                local_dir,
                trust_remote_code=True,
                attn_implementation=attn_impl,
                torch_dtype=dtype,
            )
        model = model.to(device)
        model.eval()
        return model


    def _normalize_moss_audio(audio):
        if hasattr(audio, "detach"):
            tensor = audio.detach().cpu()
            dtype_name = str(getattr(tensor, "dtype", ""))
            if "bfloat16" in dtype_name or "float16" in dtype_name:
                tensor = tensor.float()
            while getattr(tensor, "ndim", 0) > 2 and tensor.shape[0] == 1:
                tensor = tensor.squeeze(0)
            if getattr(tensor, "ndim", 0) == 2 and tensor.shape[0] == 1:
                tensor = tensor.squeeze(0)
            return tensor
        try:
            import numpy as np

            arr = np.asarray(audio)
            while arr.ndim > 2 and arr.shape[0] == 1:
                arr = arr.squeeze(0)
            if arr.ndim == 2 and arr.shape[0] == 1:
                arr = arr.squeeze(0)
            return arr
        except ImportError:
            while (
                isinstance(audio, (list, tuple))
                and len(audio) == 1
                and isinstance(audio[0], (list, tuple))
            ):
                audio = audio[0]
            return audio


    def _extract_moss_audio(decoded):
        if decoded is None:
            return None
        if isinstance(decoded, (list, tuple)):
            messages = decoded
        elif hasattr(decoded, "audio_codes_list") or isinstance(decoded, dict):
            messages = [decoded]
        else:
            messages = list(decoded)

        for message in messages:
            if message is None:
                continue
            codes_list = getattr(message, "audio_codes_list", None)
            if codes_list is None:
                if isinstance(message, dict):
                    codes_list = message.get("audio_codes_list") or message.get("audio")
                elif hasattr(message, "audio"):
                    codes_list = [message.audio]
            if not codes_list:
                continue
            if isinstance(codes_list, (list, tuple)):
                audio = codes_list[0]
            else:
                audio = codes_list
            if audio is not None:
                return _normalize_moss_audio(audio)
        return None


    def _ensure_moss_loaded(cfg: dict, state: dict):
        token = _hf_token()
        repo = cfg["repo"]
        revision = cfg.get("revision") or ""
        aux_repo = (cfg.get("aux_repo") or "").strip()
        aux_revision = (cfg.get("aux_revision") or "").strip()
        cache_dir = cfg.get("cache_dir") or ""
        default_sr = int(cfg.get("sample_rate") or 24000)

        if cache_dir:
            os.environ["HF_HUB_CACHE"] = cache_dir
            os.environ["HUGGINGFACE_HUB_CACHE"] = cache_dir

        cache_key = (repo, revision, aux_repo, aux_revision, cache_dir)
        if state.get("key") == cache_key and state.get("bundle") is not None:
            return state["bundle"]
        _release_warm_state(state)

        import importlib.util
        import torch
        from huggingface_hub import snapshot_download
        from transformers import AutoModel, AutoProcessor

        _configure_moss_sdpa(torch)
        _patch_torchaudio_load_soundfile()

        use_cuda = torch.cuda.is_available()
        device = "cuda" if use_cuda else "cpu"

        has_capability = False
        major = 0
        if use_cuda:
            cap_fn = getattr(torch.cuda, "get_device_capability", None)
            if callable(cap_fn):
                try:
                    major = int(cap_fn()[0])
                    has_capability = True
                except TypeError:
                    try:
                        major = int(cap_fn(0)[0])
                        has_capability = True
                    except Exception:
                        pass
                except Exception:
                    pass

        bf16_supported = False
        if use_cuda and (not has_capability or major >= 8):
            bf16_fn = getattr(torch.cuda, "is_bf16_supported", None)
            if callable(bf16_fn):
                try:
                    bf16_supported = bool(bf16_fn(including_emulation=False))
                except TypeError:
                    bf16_supported = bool(bf16_fn())
                except Exception:
                    bf16_supported = has_capability and major >= 8
            else:
                bf16_supported = has_capability and major >= 8

        dtype = (
            torch.bfloat16
            if bf16_supported
            else (torch.float16 if use_cuda else torch.float32)
        )
        attn_impl = _resolve_moss_attn_implementation(torch, device, dtype)

        vram_bytes = 0
        if use_cuda and callable(getattr(torch.cuda, "get_device_properties", None)):
            try:
                vram_bytes = int(torch.cuda.get_device_properties(0).total_memory)
            except Exception:
                vram_bytes = 0

        print(
            f"[colabone-tts] moss_tts_v1_5 repo={repo} device={device} attn={attn_impl}",
            flush=True,
        )

        local_dir = _resolve_hf_snapshot(
            snapshot_download, repo, revision, cache_dir, token
        )

        local_codec_dir = None
        if aux_repo:
            local_codec_dir = _resolve_hf_snapshot(
                snapshot_download, aux_repo, aux_revision, cache_dir, token
            )

        if local_codec_dir:
            try:
                processor = AutoProcessor.from_pretrained(
                    local_dir,
                    trust_remote_code=True,
                    codec_path=local_codec_dir,
                )
            except TypeError as exc:
                if "codec_path" not in str(exc):
                    raise
                processor = AutoProcessor.from_pretrained(
                    local_dir,
                    trust_remote_code=True,
                )
        else:
            processor = AutoProcessor.from_pretrained(
                local_dir,
                trust_remote_code=True,
            )

        audio_tokenizer = getattr(processor, "audio_tokenizer", None)
        if audio_tokenizer is not None and hasattr(audio_tokenizer, "to"):
            codec_target_device = (
                device
                if (not use_cuda or vram_bytes >= 32 * 1024 * 1024 * 1024)
                else "cpu"
            )
            processor.audio_tokenizer = audio_tokenizer.to(codec_target_device)

        model = _load_moss_model(AutoModel, local_dir, attn_impl, dtype, device)

        try:
            build_msg_sig = inspect.signature(processor.build_user_message)
            build_msg_params = build_msg_sig.parameters
            build_msg_has_var_kw = any(
                p.kind == inspect.Parameter.VAR_KEYWORD
                for p in build_msg_params.values()
            )
        except (TypeError, ValueError):
            build_msg_params = None
            build_msg_has_var_kw = True

        sr = int(
            getattr(getattr(processor, "model_config", None), "sampling_rate", 0)
            or getattr(getattr(processor, "model_config", None), "sample_rate", 0)
            or default_sr
        )
        bundle = {
            "torch": torch,
            "device": device,
            "vram_bytes": vram_bytes,
            "processor": processor,
            "model": model,
            "build_msg_params": build_msg_params,
            "build_msg_has_var_kw": build_msg_has_var_kw,
            "sr": sr,
        }
        state["key"] = cache_key
        state["bundle"] = bundle
        return bundle


    def _run_moss_request(cfg: dict, state: dict) -> None:
        bundle = _ensure_moss_loaded(cfg, state)
        torch = bundle["torch"]
        device = bundle["device"]
        processor = bundle["processor"]
        model = bundle["model"]
        build_msg_params = bundle["build_msg_params"]
        build_msg_has_var_kw = bundle["build_msg_has_var_kw"]
        sr = bundle["sr"]

        ref_audio_path = (cfg.get("ref_audio_path") or "").strip()
        voice_seed = cfg.get("voice_seed")
        segments = cfg.get("segments") or []

        _seed_moss_generators(voice_seed)

        active_ref_audio = ref_audio_path
        active_ref_item = (
            _encode_moss_reference(processor, active_ref_audio)
            if active_ref_audio
            else None
        )
        total = len(segments)
        for idx, seg in enumerate(segments):
            # Preserve inline [pause X.Ys] pause markers verbatim for MOSS-TTS-v1.5.
            seg_text = str(seg["text"])
            out_wav = str(seg["output_wav"])
            lang = _detect_moss_language(seg_text)

            msg_kwargs = {
                "text": seg_text,
            }
            if lang is not None:
                msg_kwargs["language"] = lang
            if active_ref_item is not None:
                msg_kwargs["reference"] = [active_ref_item]
            if build_msg_params is not None and not build_msg_has_var_kw:
                msg_kwargs = {
                    k: v for k, v in msg_kwargs.items() if k in build_msg_params
                }

            user_msg = processor.build_user_message(**msg_kwargs)
            batch = processor([[user_msg]], mode="generation")
            input_ids = batch["input_ids"].to(device)
            attention_mask = (
                batch["attention_mask"].to(device)
                if ("attention_mask" in batch and batch["attention_mask"] is not None)
                else None
            )

            with torch.no_grad():
                if attention_mask is not None:
                    outputs = model.generate(
                        input_ids=input_ids,
                        attention_mask=attention_mask,
                        max_new_tokens=4096,
                    )
                else:
                    outputs = model.generate(
                        input_ids=input_ids,
                        max_new_tokens=4096,
                    )

            decoded = processor.decode(outputs)
            audio = _extract_moss_audio(decoded)
            if audio is None:
                raise RuntimeError(f"MOSS-TTS-v1.5 未返回第 {idx + 1} 段音频")

            _write_pcm16_wav(out_wav, audio, sr)
            if idx == 0 and not active_ref_audio and total > 1:
                active_ref_audio = out_wav
                active_ref_item = _encode_moss_reference(processor, active_ref_audio)
                print(
                    f"[colabone-tts] moss_tts_v1_5 locked speaker timbre from segment 1: {out_wav}",
                    flush=True,
                )
            print(f"[colabone-tts] moss_tts_v1_5 segment {idx + 1}/{total} done", flush=True)


    def main() -> None:
        _disable_non_torch_backends()
        parser = argparse.ArgumentParser(
            description="ColabOne MOSS-TTS-v1.5 remote TTS worker"
        )
        parser.add_argument("--config", default="")
        parser.add_argument("--daemon", action="store_true")
        args = parser.parse_args()
        state: dict = {}
        if args.daemon:
            _run_daemon_loop(lambda cfg: _run_moss_request(cfg, state))
            return
        if not args.config:
            parser.error("--config is required unless --daemon is set")
        cfg = json.loads(Path(args.config).read_text(encoding="utf-8"))
        _run_moss_request(cfg, state)


    if __name__ == "__main__":
        main()
    """
)
