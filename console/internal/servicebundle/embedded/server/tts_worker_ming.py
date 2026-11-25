"""Self-contained remote GPU worker script body for inclusionAI/Ming-3.0-tts-tiny.

Bundled directly into the remote Colab service bundle; imports only Python stdlib
at module import time. Concatenated right after ``_WORKER_COMMON_HEADER`` in
``server/tts_engines.py``.
"""

from __future__ import annotations

import textwrap

__all__ = ["MING_3_TTS_WORKER_BODY"]

MING_3_TTS_WORKER_BODY: str = textwrap.dedent(
    """\

    _MING_DEFAULT_VOICE_STYLE = (
        "26岁年轻知性女性主播，清澈温润的现代标准普通话，吐字清晰圆润，自然流畅。"
    )
    _MING_SPEAKER_ANCHOR_TEXT = (
        "你好，很高兴认识你，今天天气很不错，我们现在开始。"
    )
    _MING_EMOTIONS = ("中性", "悲伤", "愤怒", "高兴", "惊讶", "恐惧", "厌恶")
    _MING_EMOTION_ALIASES = {
        "中性": "中性",
        "平静": "中性",
        "neutral": "中性",
        "calm": "中性",
        "悲伤": "悲伤",
        "难过": "悲伤",
        "伤心": "悲伤",
        "sad": "悲伤",
        "sadness": "悲伤",
        "愤怒": "愤怒",
        "生气": "愤怒",
        "angry": "愤怒",
        "anger": "愤怒",
        "高兴": "高兴",
        "开心": "高兴",
        "愉快": "高兴",
        "happy": "高兴",
        "joy": "高兴",
        "惊讶": "惊讶",
        "吃惊": "惊讶",
        "surprised": "惊讶",
        "surprise": "惊讶",
        "恐惧": "恐惧",
        "害怕": "恐惧",
        "fear": "恐惧",
        "fearful": "恐惧",
        "厌恶": "厌恶",
        "讨厌": "厌恶",
        "disgust": "厌恶",
        "disgusted": "厌恶",
    }
    _MING_MULTILINGUAL_LANGUAGES = frozenset(
        {"Japanese", "Korean", "German", "Spanish", "French"}
    )
    _MING_PARALINGUISTIC_TAGS = (
        "[Breathing]",
        "[Cough]",
        "[Sigh]",
        "[Laughter]",
        "[Surprise-ah]",
        "[Surprise-oh]",
        "[Dissatisfaction-hnn]",
        "[Uhm]",
        "[Confirmation-en]",
        "[Question-ei]",
        "[Question-ah]",
        "[Question-en]",
        "[Question-oh]",
        "[Question-huh]",
    )


    def _seed_ming_generators(seed) -> None:
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


    def _detect_ming_language(text: str) -> str:
        import re

        cleaned = re.sub(
            r"\\[[^\\[\\]\\n]{1,64}\\]|<[^<>\\n]{1,48}>",
            " ",
            str(text or ""),
        )
        has_kana = False
        has_hangul = False
        has_han = False
        has_spanish_unique = False
        has_german_unique = False
        german_umlaut_count = 0
        has_french_unique = False
        french_accent_count = 0

        for ch in cleaned:
            code = ord(ch)
            if (
                0x3041 <= code <= 0x3096
                or 0x30A1 <= code <= 0x30FA
                or 0x31F0 <= code <= 0x31FF
                or 0xFF66 <= code <= 0xFF6F
                or 0xFF71 <= code <= 0xFF9D
            ):
                has_kana = True
                break
            if (
                0xAC00 <= code <= 0xD7AF
                or 0x1100 <= code <= 0x11FF
                or 0x3130 <= code <= 0x318F
            ):
                has_hangul = True
            elif (
                0x4E00 <= code <= 0x9FFF
                or 0x3400 <= code <= 0x4DBF
                or 0x20000 <= code <= 0x2A6DF
            ):
                has_han = True
            elif ch in ("ñ", "Ñ", "¿", "¡"):
                has_spanish_unique = True
            elif ch == "ß":
                has_german_unique = True
            elif ch in ("ä", "ö", "ü", "Ä", "Ö", "Ü"):
                german_umlaut_count += 1
            elif ch in ("ç", "Ç", "œ", "Œ"):
                has_french_unique = True
            elif ch in ("à", "è", "ê", "ô", "û", "ù", "À", "È", "Ê", "Ô", "Û", "Ù"):
                french_accent_count += 1

        if has_kana:
            return "Japanese"
        if has_hangul:
            return "Korean"
        if has_han:
            return "Chinese"
        if has_spanish_unique:
            return "Spanish"
        if has_german_unique or german_umlaut_count >= 2:
            return "German"
        if has_french_unique or french_accent_count >= 2:
            return "French"
        return "English"


    def _detect_ming_dialect(emotion_prompt: str) -> str | None:
        raw = str(emotion_prompt or "").strip()
        if not raw:
            return None
        lowered = raw.lower()
        if (
            "川渝话" in raw
            or "川渝" in raw
            or "四川话" in raw
            or "重庆话" in raw
            or "sichuan" in lowered
            or "chuanyu" in lowered
        ):
            return "川渝话"
        if (
            "广粤话" in raw
            or "粤语" in raw
            or "广东话" in raw
            or "广州话" in raw
            or "cantonese" in lowered
            or "canton" in lowered
            or lowered == "yue"
        ):
            return "广粤话"
        return None


    def _detect_ming_emotion(emotion_prompt: str) -> str | None:
        raw = str(emotion_prompt or "").strip()
        if not raw:
            return None
        stripped = raw.strip("[]<>()（）「」『』《》\\\"'.,。，！？!?;；:： \\t")
        if not stripped:
            return None
        if stripped in _MING_EMOTIONS:
            return stripped
        return _MING_EMOTION_ALIASES.get(stripped.lower())


    def _has_ming_paralinguistic_tag(text: str) -> bool:
        raw = str(text or "")
        return any(tag in raw for tag in _MING_PARALINGUISTIC_TAGS)


    def _select_ming_task(
        seg_text: str,
        active_ref_audio: str,
        active_ref_text: str,
        voice_prompt: str,
        emotion_prompt: str,
    ) -> tuple[str, dict, dict]:
        vp = str(voice_prompt or "").strip()
        ep = str(emotion_prompt or "").strip()
        if active_ref_audio:
            wav_only_ref_kwargs = {
                "prompt_wav": active_ref_audio,
            }
            dialect = _detect_ming_dialect(ep)
            if dialect is not None:
                return "dialect", {"dialect": dialect}, wav_only_ref_kwargs
            emotion = _detect_ming_emotion(ep)
            if emotion is not None:
                return "emotion", {"emotion": emotion}, wav_only_ref_kwargs
            if ep:
                return "freeform", {"instruction": ep}, wav_only_ref_kwargs

            ref_kwargs = {
                "prompt_wav": active_ref_audio,
                "prompt_text": active_ref_text or None,
            }
            lang = _detect_ming_language(seg_text)
            if lang in _MING_MULTILINGUAL_LANGUAGES:
                return "multilingual", {"target_language": lang}, ref_kwargs
            if lang in {"Chinese", "English"} and _has_ming_paralinguistic_tag(seg_text):
                return "paralinguistic", {"target_language": lang}, ref_kwargs
            return "seed", {"target_language": lang}, ref_kwargs

        if ep:
            if (
                not vp
                and _detect_ming_dialect(ep) is None
                and _detect_ming_emotion(ep) is None
            ):
                return "freeform", {"instruction": ep}, {}
            base_style = vp or _MING_DEFAULT_VOICE_STYLE
            effective_style = base_style if ep in base_style else f"{base_style} {ep}"
            return "voice_design", {"style": effective_style}, {}

        effective_style = vp or _MING_DEFAULT_VOICE_STYLE
        return "voice_design", {"style": effective_style}, {}


    def _needs_ming_reference_anchor(
        segments,
        active_ref_audio: str,
        voice_prompt: str,
        emotion_prompt: str,
    ) -> bool:
        if active_ref_audio or not segments:
            return False
        if _detect_ming_dialect(emotion_prompt) is not None:
            return True
        if _detect_ming_emotion(emotion_prompt) is not None:
            return True
        first_seg = segments[0]
        seg0_text = (
            str(first_seg.get("text") or "")
            if isinstance(first_seg, dict)
            else str(first_seg or "")
        )
        if _detect_ming_language(seg0_text) in _MING_MULTILINGUAL_LANGUAGES:
            return True
        if _has_ming_paralinguistic_tag(seg0_text):
            return True
        if (
            len(segments) > 1
            and _select_ming_task(seg0_text, "", "", voice_prompt, emotion_prompt)[0]
            == "voice_design"
            and len(seg0_text) > 48
        ):
            return True
        return False


    def _build_ming_generate_kwargs(
        gen_params,
        gen_has_var_kw: bool,
        task: str,
        text: str,
        output_path: str,
        task_controls: dict,
        ref_kwargs: dict,
        valid_seed,
    ) -> dict:
        gen_kwargs = {
            "task": task,
            "text": text,
            "output_path": output_path,
        }
        gen_kwargs.update(ref_kwargs)
        if valid_seed is not None:
            gen_kwargs["seed"] = valid_seed

        if (
            gen_params is not None
            and not gen_has_var_kw
            and "controls" in gen_params
            and not any(k in gen_params for k in task_controls)
        ):
            gen_kwargs["controls"] = dict(task_controls)
        else:
            gen_kwargs.update(task_controls)

        if gen_params is not None and not gen_has_var_kw:
            gen_kwargs = {k: v for k, v in gen_kwargs.items() if k in gen_params}
        return gen_kwargs


    def _run_ming_generate(tts, gen_kwargs: dict):
        try:
            return tts.generate(**gen_kwargs)
        except (ImportError, OSError, RuntimeError):
            if (
                getattr(tts, "text_normalization", False)
                and getattr(tts, "_normalizer", None) is None
            ):
                tts.text_normalization = False
                print(
                    "[colabone-tts] ming_3_tts TalkerTN unavailable during generate(); "
                    "disabling text_normalization and retrying",
                    flush=True,
                )
                return tts.generate(**gen_kwargs)
            raise


    def _has_non_empty_waveform(waveform) -> bool:
        if waveform is None:
            return False
        numel_fn = getattr(waveform, "numel", None)
        if callable(numel_fn):
            try:
                return int(numel_fn()) > 0
            except Exception:
                pass
        size_attr = getattr(waveform, "size", None)
        if isinstance(size_attr, int):
            return size_attr > 0
        try:
            return len(waveform) > 0
        except TypeError:
            return True


    def _lock_ming_seg0_anchor(
        output,
        out_wav: str,
        seg_text: str,
        work_dir: Path,
        default_sr: int,
    ) -> tuple[str, str]:
        out_segments = getattr(output, "segments", None)
        if isinstance(out_segments, (list, tuple)) and len(out_segments) > 0:
            first_frag = out_segments[0]
            frag_waveform = getattr(first_frag, "waveform", None)
            if frag_waveform is None and isinstance(first_frag, dict):
                frag_waveform = first_frag.get("waveform")
            if _has_non_empty_waveform(frag_waveform):
                frag_sr = int(
                    getattr(first_frag, "sample_rate", 0)
                    or (
                        first_frag.get("sample_rate", 0)
                        if isinstance(first_frag, dict)
                        else 0
                    )
                    or getattr(output, "sample_rate", 0)
                    or default_sr
                )
                frag_text_raw = (
                    first_frag.get("text")
                    if isinstance(first_frag, dict)
                    else getattr(first_frag, "text", None)
                )
                frag_text = str(frag_text_raw).strip() if frag_text_raw else ""
                work_dir.mkdir(parents=True, exist_ok=True)
                seg0_anchor_wav = work_dir / "ming_seg0_anchor.wav"
                _write_pcm16_wav(str(seg0_anchor_wav), frag_waveform, frag_sr)
                if seg0_anchor_wav.is_file() and seg0_anchor_wav.stat().st_size > 44:
                    return str(seg0_anchor_wav), (frag_text or seg_text)
        return out_wav, seg_text


    def _extract_ming_waveform(output, default_sr: int):
        if output is None:
            return None, default_sr
        waveform = getattr(output, "waveform", None)
        if waveform is not None:
            out_sr = int(getattr(output, "sample_rate", 0) or default_sr)
            return waveform, out_sr
        if isinstance(output, dict):
            for key in ("waveform", "audio", "wav"):
                if output.get(key) is not None:
                    out_sr = int(
                        output.get("sample_rate")
                        or output.get("sampling_rate")
                        or default_sr
                    )
                    return output[key], out_sr
            return None, default_sr
        if isinstance(output, tuple) and len(output) == 2:
            first, second = output
            if isinstance(first, (int, float)) and not isinstance(first, bool):
                return second, int(first or default_sr)
            if isinstance(second, (int, float)) and not isinstance(second, bool):
                return first, int(second or default_sr)
            return first, default_sr
        if isinstance(output, (str, Path, os.PathLike, bool)):
            return None, default_sr
        return output, default_sr


    def _patch_ming_torchaudio_save() -> None:
        try:
            import torchaudio as _ta

            _orig_ta_save = getattr(_ta, "save", None)
            if callable(_orig_ta_save) and not getattr(
                _orig_ta_save, "_colabone_ming_patched", False
            ):
                def _safe_ta_save(uri, src, sample_rate, *args, **kwargs):
                    try:
                        return _orig_ta_save(uri, src, sample_rate, *args, **kwargs)
                    except Exception:
                        if isinstance(uri, (str, Path, os.PathLike)):
                            _write_pcm16_wav(str(uri), src, int(sample_rate))
                            return None
                        raise

                _safe_ta_save._colabone_ming_patched = True  # type: ignore[attr-defined]
                _ta.save = _safe_ta_save
        except Exception:
            pass


    def main() -> None:
        parser = argparse.ArgumentParser(
            description="ColabOne Ming-3.0-TTS remote worker"
        )
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
        voice_prompt = (cfg.get("voice_prompt") or "").strip()
        emotion_prompt = (cfg.get("emotion_prompt") or "").strip()
        voice_seed = cfg.get("voice_seed")
        segments = cfg.get("segments") or []
        default_sr = int(cfg.get("sample_rate") or 44100)

        if ref_audio_path and not emotion_prompt and not ref_text:
            raise ValueError(
                "Ming-3.0-TTS 克隆参考音色时必须同时填写「参考音色对应文字」"
            )

        import importlib.util
        import torch
        from huggingface_hub import snapshot_download
        from ming_tiny import MingTinyTTS

        try:
            from ming_tiny import task_protocol as ming_task_protocol
        except ImportError:
            ming_task_protocol = None

        _patch_torchaudio_load_soundfile()
        _patch_ming_torchaudio_save()

        use_cuda = torch.cuda.is_available()
        device = "cuda:0" if use_cuda else "cpu"
        has_pynini = bool(importlib.util.find_spec("pynini")) and bool(
            importlib.util.find_spec("inflect")
        )

        print(
            f"[colabone-tts] ming_3_tts repo={repo} device={device} "
            f"text_normalization={has_pynini} protocol={ming_task_protocol is not None}",
            flush=True,
        )

        local_dir = snapshot_download(
            repo_id=repo,
            revision=revision or None,
            cache_dir=cache_dir or None,
            token=token,
        )

        init_kwargs = {
            "device": device,
            "text_normalization": has_pynini,
        }
        try:
            from_pre_sig = inspect.signature(MingTinyTTS.from_pretrained)
            from_pre_params = from_pre_sig.parameters
            from_pre_has_var_kw = any(
                p.kind == inspect.Parameter.VAR_KEYWORD
                for p in from_pre_params.values()
            )
            if not from_pre_has_var_kw:
                init_kwargs = {
                    k: v for k, v in init_kwargs.items() if k in from_pre_params
                }
        except (TypeError, ValueError):
            pass

        try:
            tts = MingTinyTTS.from_pretrained(local_dir, **init_kwargs)
        except (ImportError, OSError):
            if init_kwargs.get("text_normalization"):
                init_kwargs["text_normalization"] = False
                tts = MingTinyTTS.from_pretrained(local_dir, **init_kwargs)
            else:
                raise

        valid_seed = (
            voice_seed
            if isinstance(voice_seed, int)
            and not isinstance(voice_seed, bool)
            and voice_seed >= 0
            else None
        )
        _seed_ming_generators(valid_seed)

        try:
            gen_sig = inspect.signature(tts.generate)
            gen_params = gen_sig.parameters
            gen_has_var_kw = any(
                p.kind == inspect.Parameter.VAR_KEYWORD
                for p in gen_params.values()
            )
        except (TypeError, ValueError):
            gen_params = None
            gen_has_var_kw = True

        model_sr = int(getattr(tts, "sample_rate", 44100) or default_sr)
        active_ref_audio = ref_audio_path
        active_ref_text = ref_text
        total = len(segments)
        cfg_work_dir = (cfg.get("work_dir") or "").strip()
        work_dir = (
            Path(cfg_work_dir)
            if cfg_work_dir
            else Path(args.config).resolve().parent
        )

        if _needs_ming_reference_anchor(
            segments, active_ref_audio, voice_prompt, emotion_prompt
        ):
            anchor_text = _MING_SPEAKER_ANCHOR_TEXT
            anchor_style = (voice_prompt or "").strip() or _MING_DEFAULT_VOICE_STYLE
            work_dir.mkdir(parents=True, exist_ok=True)
            anchor_wav = work_dir / "ming_speaker_anchor.wav"
            anchor_kwargs = _build_ming_generate_kwargs(
                gen_params=gen_params,
                gen_has_var_kw=gen_has_var_kw,
                task="voice_design",
                text=anchor_text,
                output_path=str(anchor_wav),
                task_controls={"style": anchor_style},
                ref_kwargs={},
                valid_seed=valid_seed,
            )
            anchor_output = _run_ming_generate(tts, anchor_kwargs)
            if not anchor_wav.is_file() or anchor_wav.stat().st_size <= 0:
                anchor_waveform, anchor_sr = _extract_ming_waveform(
                    anchor_output, model_sr
                )
                if anchor_waveform is not None:
                    _write_pcm16_wav(
                        str(anchor_wav), anchor_waveform, int(anchor_sr or model_sr)
                    )
            if not anchor_wav.is_file() or anchor_wav.stat().st_size <= 0:
                raise RuntimeError(
                    f"Ming-3.0-TTS 未生成参考锚点音频：{anchor_wav}"
                )
            active_ref_audio = str(anchor_wav)
            active_ref_text = anchor_text
            print(
                f"[colabone-tts] ming_3_tts synthesized speaker anchor for consistent timbre: {anchor_wav}",
                flush=True,
            )

        for idx, seg in enumerate(segments):
            seg_text = str(seg["text"])
            out_wav = str(seg["output_wav"])

            task, task_controls, ref_kwargs = _select_ming_task(
                seg_text=seg_text,
                active_ref_audio=active_ref_audio,
                active_ref_text=active_ref_text,
                voice_prompt=voice_prompt,
                emotion_prompt=emotion_prompt,
            )

            gen_kwargs = _build_ming_generate_kwargs(
                gen_params=gen_params,
                gen_has_var_kw=gen_has_var_kw,
                task=task,
                text=seg_text,
                output_path=out_wav,
                task_controls=task_controls,
                ref_kwargs=ref_kwargs,
                valid_seed=valid_seed,
            )

            output = _run_ming_generate(tts, gen_kwargs)
            out_path = Path(out_wav)
            if not out_path.is_file() or out_path.stat().st_size <= 0:
                waveform, out_sr = _extract_ming_waveform(output, model_sr)
                if waveform is not None:
                    _write_pcm16_wav(out_wav, waveform, int(out_sr or model_sr))
            if not out_path.is_file() or out_path.stat().st_size <= 0:
                raise RuntimeError(f"Ming-3.0-TTS 未生成第 {idx + 1} 段音频：{out_wav}")

            if idx == 0 and not active_ref_audio and total > 1:
                active_ref_audio, active_ref_text = _lock_ming_seg0_anchor(
                    output=output,
                    out_wav=out_wav,
                    seg_text=seg_text,
                    work_dir=work_dir,
                    default_sr=model_sr,
                )
                print(
                    f"[colabone-tts] ming_3_tts locked speaker timbre from segment 1: {active_ref_audio}",
                    flush=True,
                )
            print(f"[colabone-tts] ming_3_tts segment {idx + 1}/{total} done", flush=True)


    if __name__ == "__main__":
        main()
    """
)
