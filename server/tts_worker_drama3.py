"""Self-contained remote TTS worker script body for Fish Audio Drama 3.

Concatenated directly after ``_WORKER_COMMON_HEADER`` in ``server/tts_engines.py``.
Must remain free of external runtime imports at module import time because it is
bundled directly into the remote Colab service package.
"""

from __future__ import annotations

import textwrap

FISH_DRAMA3_WORKER_BODY: str = textwrap.dedent(
    """\

    def _apply_drama3_emotion_tag(text: str, emotion_prompt: str) -> str:
        import re

        prompt = emotion_prompt.strip()
        if not prompt:
            return text
        if "[" not in prompt and "]" not in prompt:
            tag = f"[{prompt}]"
        else:
            tokens = []
            for part in re.split(r"(\\[[^\\[\\]]+\\])", prompt):
                piece = part.strip()
                if not piece:
                    continue
                if piece.startswith("[") and piece.endswith("]"):
                    tokens.append(piece)
                elif "[" not in piece and "]" not in piece:
                    tokens.append(f"[{piece}]")
                else:
                    tokens.append(piece)
            tag = " ".join(tokens) if tokens else prompt
        stripped = text.lstrip()
        # Preserve leading <|speaker:N|> turn markers while inserting the director/emotion tag right after.
        if stripped.startswith("<|speaker:") and "|>" in stripped:
            end_idx = stripped.find("|>") + 2
            speaker_prefix = stripped[:end_idx]
            rest = stripped[end_idx:].lstrip()
            if rest.startswith(tag):
                return text
            return f"{speaker_prefix} {tag} {rest}" if rest else f"{speaker_prefix} {tag}"
        if stripped.startswith(tag):
            return text
        return f"{tag} {text}"


    def _find_decoder_checkpoint(model_dir: Path) -> Path:
        candidates = [
            "codec.pth",
            "firefly-gan-vq-fsq-8x1024-21hz-generator.pth",
        ]
        for name in candidates:
            candidate = model_dir / name
            if candidate.is_file():
                return candidate
        for path in sorted(model_dir.glob("*.pth")):
            return path
        raise FileNotFoundError(f"在 {model_dir} 中未找到 Fish Audio Drama 3 解码器权重文件")


    def _load_audio_soundfile(reference_audio, sr):
        import numpy as np
        import soundfile as sf

        target_sr = int(sr)
        if isinstance(reference_audio, (bytes, bytearray, memoryview)):
            source = io.BytesIO(bytes(reference_audio))
        elif isinstance(reference_audio, (str, Path, os.PathLike)):
            source = str(reference_audio)
        elif hasattr(reference_audio, "read"):
            source = reference_audio
        else:
            source = io.BytesIO(bytes(reference_audio))

        audio, orig_sr = sf.read(source, dtype="float32", always_2d=False)
        arr = np.asarray(audio, dtype=np.float32)
        if arr.ndim > 1:
            arr = arr.mean(axis=1, dtype=np.float32)
        arr = arr.reshape(-1)
        orig_sr = int(orig_sr)

        if orig_sr > 0 and target_sr > 0 and orig_sr != target_sr and arr.size > 0:
            resampled = None
            try:
                from scipy import signal as sp_signal

                divisor = math.gcd(orig_sr, target_sr)
                up = target_sr // divisor
                down = orig_sr // divisor
                if max(up, down) <= 1000:
                    resampled = np.asarray(
                        sp_signal.resample_poly(arr, up, down), dtype=np.float32
                    ).reshape(-1)
            except ImportError:
                pass
            if resampled is None:
                try:
                    import torch
                    import torchaudio.functional as ta_F

                    tensor = torch.from_numpy(arr.copy()).unsqueeze(0)
                    resampled = (
                        ta_F.resample(tensor, orig_sr, target_sr)
                        .squeeze(0)
                        .cpu()
                        .numpy()
                        .astype(np.float32)
                        .reshape(-1)
                    )
                except Exception:
                    out_len = max(1, int(round(arr.size * target_sr / orig_sr)))
                    positions = np.clip(
                        np.arange(out_len, dtype=np.float64) * (orig_sr / target_sr),
                        0.0,
                        float(arr.size - 1),
                    )
                    resampled = np.interp(
                        positions,
                        np.arange(arr.size, dtype=np.float64),
                        arr.astype(np.float64),
                    ).astype(np.float32)
            arr = resampled

        return np.asarray(arr, dtype=np.float32).reshape(-1)


    def _normalize_drama3_devices(
        resolved_devs, fallback_device: str
    ) -> tuple[str, list[str]]:
        codec_device = fallback_device
        all_devices: list[str] = []

        if hasattr(resolved_devs, "tower_device") and getattr(
            resolved_devs, "tower_device"
        ):
            codec_device = str(getattr(resolved_devs, "tower_device"))
            bb = getattr(resolved_devs, "backbone_devices", None)
            if isinstance(bb, (list, tuple)):
                all_devices = [*[str(d) for d in bb if d], codec_device]
            elif bb:
                all_devices = [str(bb), codec_device]
            else:
                devs = getattr(resolved_devs, "devices", None)
                if isinstance(devs, (list, tuple)):
                    all_devices = [str(d) for d in devs if d]
                elif devs:
                    all_devices = [str(devs)]
        elif isinstance(resolved_devs, dict) and resolved_devs.get("tower_device"):
            codec_device = str(resolved_devs["tower_device"])
            bb = resolved_devs.get("backbone_devices")
            if isinstance(bb, (list, tuple)):
                all_devices = [*[str(d) for d in bb if d], codec_device]
            elif bb:
                all_devices = [str(bb), codec_device]
            else:
                devs = resolved_devs.get("devices")
                if isinstance(devs, (list, tuple)):
                    all_devices = [str(d) for d in devs if d]
                elif devs:
                    all_devices = [str(devs)]
        elif isinstance(resolved_devs, (tuple, list)) and len(resolved_devs) >= 2:
            if (
                len(resolved_devs) == 2
                and isinstance(resolved_devs[0], (list, tuple))
                and not isinstance(resolved_devs[1], (list, tuple))
            ):
                backbone_devices, tower_device = resolved_devs
                if tower_device:
                    codec_device = str(tower_device)
                all_devices = [str(d) for d in backbone_devices if d]
                if tower_device:
                    all_devices.append(str(tower_device))
            else:
                tower_device = resolved_devs[-1]
                if tower_device:
                    codec_device = str(tower_device)
                all_devices = [str(d) for d in resolved_devs if d]
        elif isinstance(resolved_devs, (tuple, list)) and len(resolved_devs) == 1:
            only_dev = resolved_devs[0]
            if isinstance(only_dev, (list, tuple)):
                flat = [str(d) for d in only_dev if d]
                if flat:
                    codec_device = flat[-1]
                    all_devices = flat
            elif only_dev:
                codec_device = str(only_dev)
                all_devices = [codec_device]
        elif isinstance(resolved_devs, str) and resolved_devs:
            codec_device = resolved_devs
            all_devices = [codec_device]

        if not all_devices:
            all_devices = [codec_device]
        return codec_device, all_devices


    def _seed_drama3_generators(seed: int) -> None:
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
            import torch

            torch.manual_seed(seed)
            cuda_mod = getattr(torch, "cuda", None)
            if cuda_mod is not None and callable(getattr(cuda_mod, "manual_seed_all", None)):
                cuda_mod.manual_seed_all(seed)
        except Exception:
            pass


    def main() -> None:
        parser = argparse.ArgumentParser(description="ColabOne Fish Audio Drama 3 remote worker")
        parser.add_argument("--config", required=True)
        args = parser.parse_args()

        cfg = json.loads(Path(args.config).read_text(encoding="utf-8"))
        token = _hf_token()
        source_dir = (cfg.get("source_dir") or "").strip()
        if source_dir and source_dir not in sys.path:
            sys.path.insert(0, source_dir)

        repo = cfg["repo"]
        revision = cfg.get("revision") or ""
        cache_dir = cfg.get("cache_dir") or ""
        ref_audio_path = (cfg.get("ref_audio_path") or "").strip()
        ref_text = (cfg.get("ref_text") or "").strip()
        emotion_prompt = (cfg.get("emotion_prompt") or "").strip()
        voice_seed = cfg.get("voice_seed")
        segments = cfg.get("segments") or []

        if ref_audio_path and not ref_text:
            raise ValueError(
                "Fish Audio Drama 3 克隆参考音色时必须同时填写「参考音色对应文字」"
            )

        import importlib.util
        import torch

        use_cuda = bool(getattr(torch, "cuda", None) is not None and torch.cuda.is_available())
        if not use_cuda:
            raise RuntimeError("Fish Audio Drama 3 需要 NVIDIA CUDA GPU 才能运行")

        device = "cuda"
        has_capability = False
        major, minor = 0, 0
        cap_fn = getattr(torch.cuda, "get_device_capability", None)
        if callable(cap_fn):
            try:
                cap = cap_fn()
                major, minor = int(cap[0]), int(cap[1])
                has_capability = True
            except TypeError:
                try:
                    cap = cap_fn(0)
                    major, minor = int(cap[0]), int(cap[1])
                    has_capability = True
                except Exception:
                    pass
            except Exception:
                pass

        vram_mib = 0
        if callable(getattr(torch.cuda, "get_device_properties", None)):
            try:
                vram_mib = int(
                    torch.cuda.get_device_properties(0).total_memory // (1024 * 1024)
                )
            except Exception:
                vram_mib = 0

        device_count = 1
        count_fn = getattr(torch.cuda, "device_count", None)
        if callable(count_fn):
            try:
                device_count = int(count_fn())
            except Exception:
                device_count = 1

        if vram_mib > 0 and device_count <= 1 and vram_mib < 45056:
            vram_gib = vram_mib / 1024.0
            raise RuntimeError(
                f"Fish Audio Drama 3 至少需要 48 GB 显存（当前 {vram_gib:.1f} GB），请切换至 G4 96 GB / A100 80 GB 运行时，或改用 MOSS-TTS v1.5 / VoxCPM2"
            )

        is_rocm = bool(getattr(getattr(torch, "version", None), "hip", None))
        has_triton = importlib.util.find_spec("triton") is not None
        if (
            vram_mib > 0
            and device_count <= 1
            and (is_rocm or (has_capability and (major, minor) < (8, 9)) or not has_triton)
            and vram_mib < 73728
        ):
            raise RuntimeError(
                "Fish Audio Drama 3 在非 Ada/Hopper (SM89+) 或缺少 Triton 的显卡上需反量化为 BF16（约需 72 GiB 显存），当前显存不足"
            )

        bf16_supported = False
        if not has_capability or major >= 8:
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

        dtype = torch.bfloat16 if bf16_supported else torch.float16

        from huggingface_hub import snapshot_download
        from fish_speech.inference_engine import TTSInferenceEngine
        from fish_speech.models.text2semantic.inference import launch_thread_safe_queue
        from fish_speech.utils.schema import ServeReferenceAudio, ServeTTSRequest

        try:
            from fish_speech.models.text2semantic.inference import load_codec_model
        except ImportError:
            load_codec_model = None
        try:
            from fish_speech.models.text2semantic.fish_omni.generate import (
                resolve_drama3_devices,
            )
        except ImportError:
            try:
                from fish_speech.models.text2semantic.inference import (
                    resolve_drama3_devices,
                )
            except ImportError:
                resolve_drama3_devices = None
        try:
            from fish_speech.models.text2semantic.fish_omni.generate import (
                resolve_drama3_dtype,
            )
        except ImportError:
            try:
                from fish_speech.models.text2semantic.inference import (
                    resolve_drama3_dtype,
                )
            except ImportError:
                resolve_drama3_dtype = None
        try:
            from fish_speech.models.dac.inference import load_model as load_decoder_model
        except ImportError:
            load_decoder_model = None

        _patch_torchaudio_load_soundfile()

        has_valid_seed = (
            isinstance(voice_seed, int)
            and not isinstance(voice_seed, bool)
            and voice_seed >= 0
        )
        if has_valid_seed:
            _seed_drama3_generators(voice_seed)

        dtype_label = (
            "bf16"
            if dtype == torch.bfloat16
            else ("fp16" if dtype == torch.float16 else "fp32")
        )
        print(
            f"[colabone-tts] fish_drama3 repo={repo} dtype={dtype_label} vram_mib={vram_mib}",
            flush=True,
        )

        local_dir = Path(
            snapshot_download(
                repo_id=repo,
                revision=revision or None,
                cache_dir=cache_dir or None,
                token=token,
            )
        )
        decoder_ckpt = _find_decoder_checkpoint(local_dir)

        codec_device = device
        resolved_all_devices = [codec_device]
        if callable(resolve_drama3_devices):
            try:
                dev_sig = inspect.signature(resolve_drama3_devices)
                dev_params = list(dev_sig.parameters.values())
                dev_kw = {}
                for p in dev_params:
                    if p.name == "device":
                        dev_kw["device"] = device
                    elif p.name in ("checkpoint_path", "model_path", "model_dir"):
                        dev_kw[p.name] = str(local_dir)
                if dev_kw:
                    resolved_devs = resolve_drama3_devices(**dev_kw)
                elif not dev_params or all(
                    p.default is not inspect.Parameter.empty
                    or p.kind
                    in (inspect.Parameter.VAR_POSITIONAL, inspect.Parameter.VAR_KEYWORD)
                    for p in dev_params
                ):
                    resolved_devs = resolve_drama3_devices()
                else:
                    resolved_devs = resolve_drama3_devices(device)
                codec_device, resolved_all_devices = _normalize_drama3_devices(
                    resolved_devs, device
                )
            except Exception as exc:
                codec_device = device
                resolved_all_devices = [codec_device]
                print(
                    f"[drama3] resolve_drama3_devices fallback: {exc}",
                    flush=True,
                )

        if callable(resolve_drama3_dtype):
            try:
                dt_sig = inspect.signature(resolve_drama3_dtype)
                dt_params = list(dt_sig.parameters.values())
                dt_kw = {}
                for p in dt_params:
                    if p.name in ("precision", "dtype", "torch_dtype"):
                        dt_kw[p.name] = dtype
                    elif p.name == "devices":
                        dt_kw["devices"] = resolved_all_devices
                    elif p.name == "device":
                        dt_kw["device"] = device
                    elif p.name in ("checkpoint_path", "model_path", "model_dir"):
                        dt_kw[p.name] = str(local_dir)
                if dt_kw:
                    resolved_dt = resolve_drama3_dtype(**dt_kw)
                elif not dt_params or all(
                    p.default is not inspect.Parameter.empty
                    or p.kind
                    in (inspect.Parameter.VAR_POSITIONAL, inspect.Parameter.VAR_KEYWORD)
                    for p in dt_params
                ):
                    resolved_dt = resolve_drama3_dtype()
                else:
                    resolved_dt = resolve_drama3_dtype(dtype)
                if resolved_dt is not None:
                    dtype = resolved_dt
            except Exception as exc:
                print(
                    f"[drama3] resolve_drama3_dtype fallback: {exc}",
                    flush=True,
                )

        # Drama 3 (35B MoE) does not support torch.compile; ALWAYS keep compile=False.
        llama_sig = inspect.signature(launch_thread_safe_queue)
        llama_params = llama_sig.parameters
        llama_has_var_kw = any(
            p.kind == inspect.Parameter.VAR_KEYWORD for p in llama_params.values()
        )
        llama_kwargs = {
            "checkpoint_path": str(local_dir),
            "device": device,
            "precision": dtype,
            "compile": False,
        }
        if not llama_has_var_kw:
            llama_kwargs = {k: v for k, v in llama_kwargs.items() if k in llama_params}
        llama_queue = launch_thread_safe_queue(**llama_kwargs)

        # Upstream Drama 3 loads its codec with load_codec_model(codec_checkpoint_path, device, precision)
        # so weight norm is folded in fp32 before casting to model precision; fall back to load_decoder_model.
        if callable(load_codec_model):
            codec_sig = inspect.signature(load_codec_model)
            codec_params = codec_sig.parameters
            codec_has_var_kw = any(
                p.kind == inspect.Parameter.VAR_KEYWORD for p in codec_params.values()
            )
            codec_kwargs = {}
            if "codec_checkpoint_path" in codec_params:
                codec_kwargs["codec_checkpoint_path"] = str(decoder_ckpt)
            elif "checkpoint_path" in codec_params:
                codec_kwargs["checkpoint_path"] = str(decoder_ckpt)
            elif codec_has_var_kw:
                codec_kwargs["codec_checkpoint_path"] = str(decoder_ckpt)
            if "device" in codec_params or codec_has_var_kw:
                codec_kwargs["device"] = codec_device
            if "precision" in codec_params or codec_has_var_kw:
                codec_kwargs["precision"] = dtype
            decoder_model = load_codec_model(**codec_kwargs)
        elif callable(load_decoder_model):
            dec_sig = inspect.signature(load_decoder_model)
            dec_params = dec_sig.parameters
            dec_has_var_kw = any(
                p.kind == inspect.Parameter.VAR_KEYWORD for p in dec_params.values()
            )
            dec_kwargs = {
                "config_name": "modded_dac_vq",
                "checkpoint_path": str(decoder_ckpt),
                "device": codec_device,
            }
            if not dec_has_var_kw:
                dec_kwargs = {k: v for k, v in dec_kwargs.items() if k in dec_params}
            decoder_model = load_decoder_model(**dec_kwargs)
        else:
            raise ImportError(
                "未找到 Fish Audio Drama 3 解码器加载接口（load_codec_model / load_model）"
            )

        engine_sig = inspect.signature(TTSInferenceEngine.__init__)
        engine_params = engine_sig.parameters
        engine_has_var_kw = any(
            p.kind == inspect.Parameter.VAR_KEYWORD for p in engine_params.values()
        )
        engine_kwargs = {
            "llama_queue": llama_queue,
            "decoder_model": decoder_model,
            "precision": dtype,
            "compile": False,
            "model_family": "drama3",
        }
        if not engine_has_var_kw:
            engine_kwargs = {k: v for k, v in engine_kwargs.items() if k in engine_params}
        engine = TTSInferenceEngine(**engine_kwargs)
        # Bypass torchaudio.load (torchcodec on PyTorch >= 2.9) using soundfile + scipy/torchaudio.functional.
        engine.load_audio = _load_audio_soundfile

        references = []
        if ref_audio_path:
            references.append(
                ServeReferenceAudio(
                    audio=Path(ref_audio_path).read_bytes(),
                    text=ref_text,
                )
            )

        model_fields = getattr(ServeTTSRequest, "model_fields", None)
        if isinstance(model_fields, dict) and model_fields:
            req_field_names = set(model_fields.keys())
            req_has_var_kw = False
        else:
            try:
                req_sig = inspect.signature(ServeTTSRequest)
                req_params = req_sig.parameters
                req_field_names = set(req_params.keys())
                req_has_var_kw = any(
                    p.kind == inspect.Parameter.VAR_KEYWORD for p in req_params.values()
                )
            except (TypeError, ValueError):
                req_field_names = None
                req_has_var_kw = True

        total = len(segments)
        default_sr = int(cfg.get("sample_rate") or 44100)
        for idx, seg in enumerate(segments):
            # Preserve inline vocal/director tags ([laughs], [sighs], <|speaker:0|>) and apply emotion_prompt.
            seg_text = _apply_drama3_emotion_tag(str(seg["text"]), emotion_prompt)
            out_wav = str(seg["output_wav"])
            req_kwargs = {
                "text": seg_text,
                "references": references,
                "format": "wav",
                "streaming": False,
            }
            if references:
                req_kwargs["use_memory_cache"] = "on"
            if has_valid_seed:
                req_kwargs["seed"] = voice_seed
            if req_field_names is not None and not req_has_var_kw:
                req_kwargs = {k: v for k, v in req_kwargs.items() if k in req_field_names}
            req = ServeTTSRequest(**req_kwargs)
            sample_rate = default_sr
            final_audio = None
            for result in engine.inference(req):
                if getattr(result, "code", "") == "error":
                    raise RuntimeError(
                        f"Fish Audio Drama 3 推理失败：{getattr(result, 'error', result)}"
                    )
                if (
                    getattr(result, "code", "") == "final"
                    and getattr(result, "audio", None) is not None
                ):
                    sample_rate, final_audio = result.audio
            if final_audio is None:
                raise RuntimeError(f"Fish Audio Drama 3 未返回第 {idx + 1} 段音频")
            _write_pcm16_wav(out_wav, final_audio, int(sample_rate or default_sr))
            if idx == 0 and not references and total > 1:
                references = [
                    ServeReferenceAudio(
                        audio=Path(out_wav).read_bytes(),
                        text=seg_text,
                    )
                ]
                print(
                    f"[colabone-tts] fish_drama3 locked speaker timbre from segment 1: {out_wav}",
                    flush=True,
                )
            print(f"[colabone-tts] fish_drama3 segment {idx + 1}/{total} done", flush=True)


    if __name__ == "__main__":
        main()
    """
)
