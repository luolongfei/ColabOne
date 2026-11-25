"""Self-contained remote TTS worker script body for Breeze TTS 2.

Concatenated directly after ``_WORKER_COMMON_HEADER`` in ``server/tts_engines.py``.
Must remain free of external runtime imports at module import time because it is
bundled directly into the remote Colab service package.
"""

from __future__ import annotations

import textwrap

BREEZE_TTS2_WORKER_BODY: str = textwrap.dedent(
    """\

    def _combine_breeze_instructions(*parts: str) -> str:
        cleaned = []
        for part in parts:
            text = str(part or "").strip()
            if text and text not in cleaned:
                cleaned.append(text)
        return " ".join(cleaned)


    def _select_breeze_template_name(select_fn, request: dict) -> str:
        try:
            return str(select_fn(request))
        except TypeError:
            has_ref = bool(request.get("ref_audio_path") and request.get("ref_text"))
            has_ins = bool(request.get("instruction"))
            try:
                sig = inspect.signature(select_fn)
                params = sig.parameters
                kw = {}
                if "has_ref" in params:
                    kw["has_ref"] = has_ref
                if "has_instruction" in params:
                    kw["has_instruction"] = has_ins
                if kw:
                    return str(select_fn(**kw))
            except (TypeError, ValueError):
                pass
            if has_ref and has_ins:
                return "ref_edit_tata"
            if has_ref:
                return "ref_clone_tata"
            if has_ins:
                return "tts_instruction"
            return "tts_plain"


    def _prepare_breeze_inputs(
        prepare_fn,
        tokenizer,
        audio_tokenizer,
        model,
        request: dict,
        template,
    ):
        supports_cfg = getattr(template, "build_negative_segments", None) is not None
        extra_kwargs = {}
        try:
            sig = inspect.signature(prepare_fn)
            required_defaults = {
                "guidance_scale": 4.0 if supports_cfg else 1.0,
                "guidance_scale_ref": None,
                "guidance_scale_ins": None,
            }
            for param_name, default_val in required_defaults.items():
                param = sig.parameters.get(param_name)
                if param is not None and param.default is inspect.Parameter.empty:
                    extra_kwargs[param_name] = default_val
            if supports_cfg and "guidance_scale" in sig.parameters:
                extra_kwargs["guidance_scale"] = 4.0
        except (TypeError, ValueError):
            pass
        return prepare_fn(
            tokenizer,
            audio_tokenizer,
            model,
            [request],
            template,
            **extra_kwargs,
        )


    def _is_pre_ampere_cuda(torch_mod, use_cuda: bool) -> bool:
        if not use_cuda:
            return False
        cuda_mod = getattr(torch_mod, "cuda", None)
        if cuda_mod is None:
            return False
        cap_checked = False
        cap_fn = getattr(cuda_mod, "get_device_capability", None)
        if callable(cap_fn):
            try:
                major = int(cap_fn()[0])
                cap_checked = True
                if major < 8:
                    return True
            except TypeError:
                try:
                    major = int(cap_fn(0)[0])
                    cap_checked = True
                    if major < 8:
                        return True
                except Exception:
                    pass
            except Exception:
                pass
        bf16_fn = getattr(cuda_mod, "is_bf16_supported", None)
        if callable(bf16_fn):
            try:
                if not bf16_fn(including_emulation=False):
                    return True
            except TypeError:
                if not cap_checked:
                    try:
                        if not bf16_fn():
                            return True
                    except Exception:
                        pass
            except Exception:
                pass
        return False


    def _is_floating_tensor(obj, torch_mod) -> bool:
        is_fp = getattr(obj, "is_floating_point", None)
        if callable(is_fp):
            return bool(is_fp())
        mod_is_fp = getattr(torch_mod, "is_floating_point", None)
        if callable(mod_is_fp):
            try:
                return bool(mod_is_fp(obj))
            except Exception:
                return False
        return False


    def _cast_breeze_value_to_dtype(value, target_dtype, torch_mod):
        if value is None:
            return None
        if _is_floating_tensor(value, torch_mod):
            if getattr(value, "dtype", None) == target_dtype:
                return value
            return value.to(dtype=target_dtype)
        if isinstance(value, tuple):
            mapped = tuple(
                _cast_breeze_value_to_dtype(item, target_dtype, torch_mod)
                for item in value
            )
            if hasattr(value, "_fields"):
                try:
                    return type(value)(*mapped)
                except Exception:
                    return mapped
            return mapped
        if isinstance(value, list):
            return [
                _cast_breeze_value_to_dtype(item, target_dtype, torch_mod)
                for item in value
            ]
        if hasattr(value, "last_hidden_state"):
            lhs = getattr(value, "last_hidden_state", None)
            if (
                lhs is not None
                and _is_floating_tensor(lhs, torch_mod)
                and getattr(lhs, "dtype", None) != target_dtype
            ):
                cast_lhs = lhs.to(dtype=target_dtype)
                try:
                    setattr(value, "last_hidden_state", cast_lhs)
                    if isinstance(value, dict) and "last_hidden_state" in value:
                        value["last_hidden_state"] = cast_lhs
                except Exception:
                    pass
            return value
        return value


    def _cast_breeze_model_for_pre_ampere(model, device, torch_mod):
        # 1. Snapshot all floating-point *inv_freq RoPE buffers in FP32 before casting.
        inv_freq_snapshots = {}
        if hasattr(model, "named_modules"):
            for _mod_name, mod in model.named_modules():
                if not hasattr(mod, "named_buffers"):
                    continue
                try:
                    buffers_iter = mod.named_buffers(recurse=False)
                except TypeError:
                    buffers_iter = mod.named_buffers()
                for buf_name, buf in buffers_iter:
                    if (
                        buf is not None
                        and buf_name.endswith("inv_freq")
                        and _is_floating_tensor(buf, torch_mod)
                    ):
                        snap = buf.detach().to(dtype=torch_mod.float32)
                        if callable(getattr(snap, "clone", None)):
                            snap = snap.clone()
                        inv_freq_snapshots[(mod, buf_name)] = snap

        # 2. Cast model to FP16 while detaching text_encoder/text_encoder_proj (when
        #    present in _modules) so their weights never suffer intermediate FP16 truncation.
        fp32_submod_names = ("text_encoder", "text_encoder_proj")
        model_modules = getattr(model, "_modules", None)
        detached = {}
        orig_order = None
        if isinstance(model_modules, dict):
            orig_order = list(model_modules.keys())
            for name in fp32_submod_names:
                if name in model_modules and model_modules[name] is not None:
                    detached[name] = model_modules.pop(name)

        cast_model = model
        try:
            if hasattr(model, "to"):
                cast_model = model.to(dtype=torch_mod.float16)
        finally:
            if detached and isinstance(model_modules, dict):
                restored_map = {}
                for key in orig_order or ():
                    if key in detached:
                        restored_map[key] = detached[key]
                    elif key in model_modules:
                        restored_map[key] = model_modules[key]
                for key, val in model_modules.items():
                    if key not in restored_map:
                        restored_map[key] = val
                model_modules.clear()
                model_modules.update(restored_map)
                if cast_model is not model:
                    cast_modules = getattr(cast_model, "_modules", None)
                    for key, val in detached.items():
                        if isinstance(cast_modules, dict):
                            cast_modules[key] = val
                        else:
                            setattr(cast_model, key, val)

        model = cast_model

        # 3. Keep text_encoder (and text_encoder_proj if present) in FP32 to prevent
        #    26-layer Gemma2 embed_scale / gelu_pytorch_tanh FP16 overflow.
        for name in fp32_submod_names:
            submod = getattr(model, name, None)
            if submod is not None and hasattr(submod, "to"):
                cast_sub = submod.to(dtype=torch_mod.float32)
                if cast_sub is not None and cast_sub is not submod:
                    setattr(model, name, cast_sub)

        # 4. Restore every snapshotted *inv_freq buffer back to FP32 on its target device.
        for (mod, buf_name), saved_buf in inv_freq_snapshots.items():
            cur_buf = (
                mod._buffers.get(buf_name)
                if hasattr(mod, "_buffers") and isinstance(mod._buffers, dict)
                else getattr(mod, buf_name, None)
            )
            target_dev = (
                getattr(cur_buf, "device", None)
                or getattr(saved_buf, "device", None)
                or device
            )
            restored = saved_buf
            if target_dev is not None:
                try:
                    restored = saved_buf.to(device=target_dev, dtype=torch_mod.float32)
                except TypeError:
                    restored = saved_buf.to(dtype=torch_mod.float32)
            else:
                restored = saved_buf.to(dtype=torch_mod.float32)

            if (
                hasattr(mod, "_buffers")
                and isinstance(mod._buffers, dict)
                and buf_name in mod._buffers
            ):
                mod._buffers[buf_name] = restored
                if buf_name in getattr(mod, "__dict__", {}):
                    setattr(mod, buf_name, restored)
            elif hasattr(mod, "register_buffer"):
                persistent = not (
                    hasattr(mod, "_non_persistent_buffers_set")
                    and buf_name in mod._non_persistent_buffers_set
                )
                try:
                    mod.register_buffer(buf_name, restored, persistent=persistent)
                except TypeError:
                    mod.register_buffer(buf_name, restored)
            else:
                setattr(mod, buf_name, restored)

        # 5. Attach FP32/FP16 boundary hooks on text_encoder_proj (or text_encoder if absent).
        text_encoder = getattr(model, "text_encoder", None)
        text_encoder_proj = getattr(model, "text_encoder_proj", None)

        def _cast_out_fp16(_module, _inputs, output):
            return _cast_breeze_value_to_dtype(output, torch_mod.float16, torch_mod)

        def _cast_in_fp32(_module, inputs):
            return _cast_breeze_value_to_dtype(inputs, torch_mod.float32, torch_mod)

        if text_encoder_proj is not None:
            if not getattr(text_encoder_proj, "_colabone_pre_ampere_hooked", False):
                if callable(getattr(text_encoder_proj, "register_forward_pre_hook", None)):
                    text_encoder_proj.register_forward_pre_hook(_cast_in_fp32)
                if callable(getattr(text_encoder_proj, "register_forward_hook", None)):
                    text_encoder_proj.register_forward_hook(_cast_out_fp16)
                try:
                    text_encoder_proj._colabone_pre_ampere_hooked = True
                except Exception:
                    pass
        elif text_encoder is not None:
            if not getattr(text_encoder, "_colabone_pre_ampere_hooked", False):
                if callable(getattr(text_encoder, "register_forward_hook", None)):
                    text_encoder.register_forward_hook(_cast_out_fp16)
                try:
                    text_encoder._colabone_pre_ampere_hooked = True
                except Exception:
                    pass

        return model


    def _extract_breeze_raw_chunk(chunk):
        if isinstance(chunk, dict):
            return chunk.get("audio")
        if hasattr(chunk, "audio"):
            return getattr(chunk, "audio", None)
        return chunk


    def _chunk_to_mono_f32(audio, np_mod):
        if hasattr(audio, "detach"):
            tensor = audio.detach().cpu()
            dtype_name = str(getattr(tensor, "dtype", ""))
            if "bfloat16" in dtype_name or "float16" in dtype_name:
                tensor = tensor.float()
            try:
                audio = tensor.numpy()
            except TypeError:
                audio = tensor.float().numpy()
        arr = np_mod.asarray(audio)
        if arr.size == 0:
            return np_mod.empty((0,), dtype=np_mod.float32)
        if np_mod.issubdtype(arr.dtype, np_mod.integer):
            scale = (
                32768.0
                if arr.dtype == np_mod.int16
                else float(np_mod.iinfo(arr.dtype).max)
            )
            arr = arr.astype(np_mod.float32) / scale
        else:
            arr = arr.astype(np_mod.float32)
        while arr.ndim > 2 and arr.shape[0] == 1:
            arr = arr[0]
        if arr.ndim == 0:
            arr = arr.reshape(1)
        elif arr.ndim == 2:
            if arr.shape[0] <= 8 and arr.shape[0] < arr.shape[1]:
                arr = arr.mean(axis=0)
            else:
                arr = arr.mean(axis=1)
        elif arr.ndim > 2:
            arr = arr.reshape(-1)
        arr = np_mod.nan_to_num(arr, nan=0.0, posinf=1.0, neginf=-1.0)
        return np_mod.asarray(arr, dtype=np_mod.float32).reshape(-1)


    def _seed_breeze_generators(seed: int, set_all_seeds_fn) -> None:
        norm_seed = int(seed) % (2**32)
        try:
            import random as _py_random

            _py_random.seed(norm_seed)
        except Exception:
            pass
        try:
            import numpy as _np

            _np.random.seed(norm_seed)
        except Exception:
            pass
        try:
            import torch

            torch.manual_seed(norm_seed)
            cuda_mod = getattr(torch, "cuda", None)
            if cuda_mod is not None and callable(getattr(cuda_mod, "manual_seed_all", None)):
                cuda_mod.manual_seed_all(norm_seed)
        except Exception:
            pass
        if callable(set_all_seeds_fn):
            set_all_seeds_fn(norm_seed)


    def _run_breeze_request(cfg: dict, state: dict) -> None:
        token = _hf_token()
        source_dir = (cfg.get("source_dir") or "").strip()
        if source_dir and source_dir not in sys.path:
            sys.path.insert(0, source_dir)

        repo = cfg["repo"]
        revision = cfg.get("revision") or ""
        cache_dir = cfg.get("cache_dir") or ""
        ref_audio_path = (cfg.get("ref_audio_path") or "").strip()
        ref_text = (cfg.get("ref_text") or "").strip()
        voice_prompt = (cfg.get("voice_prompt") or "").strip()
        emotion_prompt = (cfg.get("emotion_prompt") or "").strip()
        voice_seed = cfg.get("voice_seed")
        segments = cfg.get("segments") or []

        if ref_audio_path and not ref_text:
            raise ValueError(
                "Breeze TTS 2 克隆参考音色时必须同时填写「参考音色对应文字」"
            )

        import torch
        from huggingface_hub import snapshot_download
        from breeze_infer.runtime import (
            load_runtime,
            resolve_device,
            set_all_seeds,
            update_generation_config_for_breeze,
        )
        from breeze_infer.templates import (
            get_template,
            prepare_inputs,
            select_template_name,
        )
        from models.fast_streaming import (
            FastBreezeStreamingRuntime,
            FastStreamingConfig,
        )

        _patch_torchaudio_load_soundfile()

        has_valid_seed = (
            isinstance(voice_seed, int)
            and not isinstance(voice_seed, bool)
            and voice_seed >= 0
        )
        seed = int(voice_seed) % (2**32) if has_valid_seed else None
        if has_valid_seed:
            _seed_breeze_generators(seed, set_all_seeds)

        device = resolve_device()
        use_cuda = bool(getattr(torch, "cuda", None) is not None and torch.cuda.is_available())
        if not use_cuda:
            raise RuntimeError("Breeze TTS 2 需要 NVIDIA CUDA GPU 才能运行")
        vram_mib = 0
        if use_cuda and callable(getattr(torch.cuda, "get_device_properties", None)):
            try:
                vram_mib = int(
                    torch.cuda.get_device_properties(0).total_memory // (1024 * 1024)
                )
            except Exception:
                vram_mib = 0

        active_ref_audio = ref_audio_path
        active_ref_text = ref_text
        if active_ref_audio:
            active_instruction = _combine_breeze_instructions(voice_prompt, emotion_prompt)
            initial_mode = "voice_direction" if active_instruction else "voice_clone"
        else:
            active_instruction = _combine_breeze_instructions(voice_prompt, emotion_prompt)
            initial_mode = "voice_design" if active_instruction else "tts_plain"

        cache_key = (repo, revision, cache_dir, str(device))
        cached = state.get("bundle") if state.get("key") == cache_key else None
        if cached is None:
            _release_warm_state(state)
            print(
                f"[colabone-tts] breeze_tts2 mode={initial_mode} repo={repo} device={device} vram_mib={vram_mib}",
                flush=True,
            )
            local_dir = Path(
                _resolve_hf_snapshot(snapshot_download, repo, revision, cache_dir, token)
            )

            load_kwargs = {
                "device": device,
                "attn_implementation": "eager",
            }
            try:
                load_sig = inspect.signature(load_runtime)
                load_params = load_sig.parameters
                load_has_var_kw = any(
                    p.kind == inspect.Parameter.VAR_KEYWORD for p in load_params.values()
                )
                if not load_has_var_kw:
                    load_kwargs = {k: v for k, v in load_kwargs.items() if k in load_params}
            except (TypeError, ValueError):
                pass

            tokenizer, model, audio_tokenizer = load_runtime(local_dir, **load_kwargs)

            pre_ampere_fp16 = _is_pre_ampere_cuda(torch, use_cuda)
            if pre_ampere_fp16 and hasattr(model, "to"):
                model = _cast_breeze_model_for_pre_ampere(model, device, torch)
                print(
                    "[colabone-tts] breeze_tts2 pre-Ampere CUDA GPU detected; "
                    "cast model to float16 with FP32 RoPE inv_freq and text_encoder",
                    flush=True,
                )

            update_generation_config_for_breeze(model)

            # Keep fast_all=False (eager non-CUDA-graph path) for broad Colab GPU compatibility.
            stream_cfg_kwargs = {
                "max_new_tokens": 1500,
                "max_seq_len": 2048,
                "fast_all": False,
                "repetition_penalty": 1.1,
            }
            try:
                cfg_sig = inspect.signature(FastStreamingConfig)
                cfg_params = cfg_sig.parameters
                cfg_has_var_kw = any(
                    p.kind == inspect.Parameter.VAR_KEYWORD for p in cfg_params.values()
                )
                if not cfg_has_var_kw:
                    stream_cfg_kwargs = {
                        k: v for k, v in stream_cfg_kwargs.items() if k in cfg_params
                    }
            except (TypeError, ValueError):
                pass
            stream_cfg = FastStreamingConfig(**stream_cfg_kwargs)

            rt_kwargs = {"tokenizer": tokenizer}
            try:
                rt_sig = inspect.signature(FastBreezeStreamingRuntime.__init__)
                rt_params = rt_sig.parameters
                rt_has_var_kw = any(
                    p.kind == inspect.Parameter.VAR_KEYWORD for p in rt_params.values()
                )
                if not rt_has_var_kw:
                    rt_kwargs = {k: v for k, v in rt_kwargs.items() if k in rt_params}
            except (TypeError, ValueError):
                pass
            runtime = FastBreezeStreamingRuntime(model, audio_tokenizer, stream_cfg, **rt_kwargs)
            cached = (tokenizer, model, audio_tokenizer, runtime)
            state["key"] = cache_key
            state["bundle"] = cached
        else:
            tokenizer, model, audio_tokenizer, runtime = cached

        sample_rate = int(
            getattr(runtime, "sample_rate", None)
            or cfg.get("sample_rate")
            or 24000
        )

        try:
            iter_sig = inspect.signature(runtime.iter_audio_chunks)
            iter_params = iter_sig.parameters
            iter_has_var_kw = any(
                p.kind == inspect.Parameter.VAR_KEYWORD for p in iter_params.values()
            )
        except (TypeError, ValueError):
            iter_params = None
            iter_has_var_kw = True

        try:
            import numpy as _np
        except ImportError:
            _np = None

        total = len(segments)
        for idx, seg in enumerate(segments):
            # Preserve inline vocal events such as [笑], [咳嗽], [清嗓子], [叹气], (laugh), (sigh).
            seg_text = str(seg["text"])
            out_wav = str(seg["output_wav"])
            request_id = f"seg-{idx}"
            request = {
                "id": request_id,
                "text": seg_text,
                "speaker": "S0",
            }
            if active_ref_audio and active_ref_text:
                request["ref_audio_path"] = active_ref_audio
                request["ref_text"] = active_ref_text
            if active_instruction:
                request["instruction"] = active_instruction

            template_name = _select_breeze_template_name(select_template_name, request)
            template = get_template(template_name)
            inputs = _prepare_breeze_inputs(
                prepare_inputs,
                tokenizer,
                audio_tokenizer,
                model,
                request,
                template,
            )

            iter_kwargs = {"request_id": request_id}
            if has_valid_seed:
                iter_kwargs["seed"] = seed
            elif iter_params is not None and "seed" in iter_params:
                seed_default = iter_params["seed"].default
                iter_kwargs["seed"] = (
                    42 if seed_default is inspect.Parameter.empty else seed_default
                )
            elif iter_has_var_kw:
                iter_kwargs["seed"] = None

            if iter_params is not None and not iter_has_var_kw:
                iter_kwargs = {k: v for k, v in iter_kwargs.items() if k in iter_params}

            if _np is not None:
                chunk_arrays = []
                for chunk in runtime.iter_audio_chunks(inputs, **iter_kwargs):
                    raw_chunk = _extract_breeze_raw_chunk(chunk)
                    if raw_chunk is not None:
                        arr = _chunk_to_mono_f32(raw_chunk, _np)
                        if arr.size > 0:
                            chunk_arrays.append(arr)
                if not chunk_arrays:
                    raise RuntimeError(f"Breeze TTS 2 未返回第 {idx + 1} 段音频")
                merged_audio = (
                    chunk_arrays[0]
                    if len(chunk_arrays) == 1
                    else _np.concatenate(chunk_arrays)
                )
                total_samples = int(merged_audio.size)
            else:
                samples: list[float] = []
                for chunk in runtime.iter_audio_chunks(inputs, **iter_kwargs):
                    raw_chunk = _extract_breeze_raw_chunk(chunk)
                    if raw_chunk is not None:
                        samples.extend(_to_mono_list(raw_chunk))
                if not samples:
                    raise RuntimeError(f"Breeze TTS 2 未返回第 {idx + 1} 段音频")
                merged_audio = samples
                total_samples = len(samples)

            _write_pcm16_wav(out_wav, merged_audio, sample_rate)

            # In Voice Design mode, lock speaker timbre from segment 0 for subsequent segments,
            # preserving emotion_prompt as Voice Direction instruction when provided.
            # Gate lock to 1.5s..20.0s so long segment 0 audio never overflows max_seq_len=2048.
            if idx == 0 and not active_ref_audio and seg_text.strip() and total > 1:
                seg0_duration = total_samples / float(sample_rate) if sample_rate > 0 else 0.0
                if 1.5 <= seg0_duration <= 20.0:
                    active_ref_audio = out_wav
                    active_ref_text = seg_text
                    active_instruction = emotion_prompt
                    print(
                        f"[colabone-tts] breeze_tts2 locked speaker timbre from segment 1 "
                        f"({seg0_duration:.2f}s): {out_wav}",
                        flush=True,
                    )
                else:
                    print(
                        f"[colabone-tts] breeze_tts2 skipped speaker timbre lock for segment 1 "
                        f"(duration {seg0_duration:.2f}s outside 1.5s..20.0s)",
                        flush=True,
                    )

            print(f"[colabone-tts] breeze_tts2 segment {idx + 1}/{total} done", flush=True)


    def main() -> None:
        _disable_non_torch_backends()
        parser = argparse.ArgumentParser(description="ColabOne Breeze TTS 2 remote worker")
        parser.add_argument("--config", default="")
        parser.add_argument("--daemon", action="store_true")
        args = parser.parse_args()
        state: dict = {}
        if args.daemon:
            _run_daemon_loop(lambda cfg: _run_breeze_request(cfg, state))
            return
        if not args.config:
            parser.error("--config is required unless --daemon is set")
        cfg = json.loads(Path(args.config).read_text(encoding="utf-8"))
        _run_breeze_request(cfg, state)


    if __name__ == "__main__":
        main()
    """
)
