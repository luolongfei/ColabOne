"""Remote GPU multi-engine TTS runner and pure offline audio/script utilities.

Supports eight remote GPU TTS engines (Fish Audio Drama 3, MOSS-TTS v1.5,
Breeze TTS 2, Ming-3.0-TTS, VoxCPM2, Qwen3-TTS, IndexTTS-2/2.5, and Fish
Speech S2 Pro). All audio preprocessing, script segmentation, silence trimming,
resampling, loudness normalization, and master WAV assembly work 100% offline
with Python stdlib (with optional numpy/scipy/soxr/pyloudnorm acceleration when
installed on the remote Colab GPU runtime).
"""

from __future__ import annotations

import array
import base64
import collections
import copy
import hashlib
import importlib.metadata
import inspect
import json
import math
import os
import queue
import re
import shutil
import signal
import site
import stat
import struct
import subprocess
import sys
import sysconfig
import tempfile
import textwrap
import threading
import time
import uuid
import wave
from contextlib import nullcontext
from pathlib import Path
from typing import Any, Callable, Iterator, Sequence

try:
    import tomllib  # type: ignore[import-not-found]
except ImportError:  # pragma: no cover - Python 3.10 fallback
    tomllib = None  # type: ignore[assignment]

from common.files import atomic_json
from server.tts_worker_breeze import BREEZE_TTS2_WORKER_BODY
from server.tts_worker_drama3 import FISH_DRAMA3_WORKER_BODY
from server.tts_worker_ming import MING_3_TTS_WORKER_BODY
from server.tts_worker_moss import MOSS_TTS_V1_5_WORKER_BODY

TORCH_SKIP_PACKAGES = frozenset(
    {
        "torch",
        "torchvision",
        "torchaudio",
        "xformers",
        "triton",
        "flash-attn",
        "sageattention",
        "sageattn3",
    }
)
_UNBUILDABLE_SKIP_PACKAGES = frozenset({"pyaudio"})
_PIP_SKIP_PACKAGES = TORCH_SKIP_PACKAGES | _UNBUILDABLE_SKIP_PACKAGES
_TORCH_CONSTRAINT_PACKAGES = (
    "torch",
    "torchvision",
    "torchaudio",
    "xformers",
    "triton",
)
SUPPORTED_TTS_ENGINE_IDS = frozenset(
    {
        "fish_drama3",
        "moss_tts_v1_5",
        "breeze_tts2",
        "ming_3_tts",
        "voxcpm2",
        "qwen3_tts",
        "indextts2",
        "fish_s2_pro",
    }
)
_PIP_PACKAGED_ENGINES = frozenset({"voxcpm2", "qwen3_tts", "moss_tts_v1_5"})
_SOURCE_CHECKOUT_ENGINES = frozenset(
    {"fish_drama3", "breeze_tts2", "ming_3_tts", "fish_s2_pro", "indextts2"}
)
_ENGINE_REQUIREMENTS_FILES: dict[str, str] = {
    "ming_3_tts": "requirements-tiny.txt",
}
_VOICE_DESIGN_PRESET_ENGINES = frozenset(
    {"voxcpm2", "qwen3_tts", "breeze_tts2", "ming_3_tts"}
)
_REF_AUDIO_PRESET_ENGINES = frozenset(
    {"fish_drama3", "moss_tts_v1_5", "fish_s2_pro", "indextts2"}
)
MAX_PREVIEW_AUDIO_BYTES = 16 * 1024 * 1024
MAX_PREVIEW_CACHE_BYTES = 512 * 1024 * 1024
MAX_PREVIEW_CACHE_ENTRIES = 128
_PREVIEW_ID_RE = re.compile(r"^[a-f0-9]{32}$")
_TTS_MODEL_WEIGHT_EXTS = frozenset(
    {".safetensors", ".pth", ".bin", ".onnx", ".pt", ".ckpt"}
)
_BEARER_TOKEN_RE = re.compile(r"(?i)\bBearer\s+[A-Za-z0-9._~+/=-]{8,}")
_HF_TOKEN_RE = re.compile(
    r"\bhf_(?!(?:download|cache|hub|home|transfer|endpoint|xet)(?:_|\b))[A-Za-z0-9_-]{8,}\b"
)
_TTS_SENSITIVE_ENV_KEYS = frozenset(
    {
        "HF_TOKEN",
        "HUGGING_FACE_HUB_TOKEN",
        "HUGGINGFACE_HUB_TOKEN",
        "HF_API_TOKEN",
    }
)
_TTS_SETUP_STRIP_ENV_KEYS = frozenset(
    {
        "PIP_USER",
        "PIP_TARGET",
        "PIP_PREFIX",
        "PIP_CONSTRAINT",
        "PIP_REQUIRE_VIRTUALENV",
        "PYTHONUSERBASE",
        "PYTHONPATH",
        "PYTHONNOUSERSITE",
    }
)
_MAX_TTS_LOG_FILE_BYTES = 5 * 1024 * 1024  # 5 MiB truncation threshold
_MAX_TTS_STREAM_LINE_BYTES = 65536  # 64 KiB cap per streamed stdout/stderr line
_VOXCPM2_BOOTSTRAP_DISK_MIB = 6144  # ~6 GiB uncached VoxCPM2 preset anchor budget

_ENGINE_ID_RE = re.compile(r"^[a-zA-Z0-9_-]{1,80}$")
_REPO_SLUG_RE = re.compile(r"^[\w.-]+/[\w.-]+$")
_GIT_COMMIT_SHA_RE = re.compile(r"^[0-9a-fA-F]{40}$")
_GITHUB_SEGMENT_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9_.-]{0,99}$")
_GITHUB_REPO_URL_RE = re.compile(
    r"^https://github\.com/(?P<owner>[A-Za-z0-9][A-Za-z0-9_.-]{0,99})/(?P<repo>[A-Za-z0-9][A-Za-z0-9_.-]{0,99}?)(?:\.git)?$"
)
_BRACKET_INLINE_TAG_PAT = r"\[[^\[\]\n]{1,80}\]"
_SPEAKER_INLINE_TAG_PAT = r"<\|speaker:\d+\|>"
_XML_INLINE_TAG_PAT = r"</?[A-Za-z_][A-Za-z0-9_:-]{0,47}>"
_PINYIN_TAG_RE = re.compile(
    r"<([^|<>\s]{1,16})\|[A-Za-z0-9_-]{1,16}(?:\s+[A-Za-z0-9_-]{1,16})*>"
)
_INLINE_TAG_RE = re.compile(
    rf"(?:{_BRACKET_INLINE_TAG_PAT}|{_SPEAKER_INLINE_TAG_PAT}|<[^|<>\s]{{1,16}}\|[A-Za-z0-9_-]{{1,16}}(?:\s+[A-Za-z0-9_-]{{1,16}})*>|{_XML_INLINE_TAG_PAT})"
)
_NON_SPEAKABLE_INLINE_TAG_PAT = (
    rf"(?:{_BRACKET_INLINE_TAG_PAT}|{_SPEAKER_INLINE_TAG_PAT}|{_XML_INLINE_TAG_PAT})"
)
_NON_SPEAKABLE_INLINE_TAG_RE = re.compile(_NON_SPEAKABLE_INLINE_TAG_PAT)
_PURE_INLINE_TAG_RE = re.compile(
    rf"^{_NON_SPEAKABLE_INLINE_TAG_PAT}(?:\s*{_NON_SPEAKABLE_INLINE_TAG_PAT})*$"
)
_WHITESPACE_RUN_RE = re.compile(r"[ \t\u00a0\u3000]+")

_CJK_HARD_SENTENCE_DELIMS = frozenset("。！？；")
_ASCII_HARD_SENTENCE_DELIMS = frozenset("!?;")
_HARD_SENTENCE_DELIMS = _CJK_HARD_SENTENCE_DELIMS | _ASCII_HARD_SENTENCE_DELIMS
_CLAUSE_DELIMS = frozenset("，、：,:—–")
_CLOSING_TRAILING = frozenset("”’\"'」』）》】）)]")
_CJK_PUNCT_END = frozenset("。！？；，、：”’」』）》】）")
_ENGLISH_ABBREVS = frozenset(
    {
        "mr",
        "mrs",
        "ms",
        "dr",
        "prof",
        "sr",
        "jr",
        "st",
        "vs",
        "etc",
        "e.g",
        "i.e",
        "fig",
        "no",
        "inc",
        "ltd",
        "co",
    }
)

_MIN_SHORT_FRAGMENT_CHARS = 12
_SOFT_LIMITER_KNEE = 0.85
_SOFT_LIMITER_CEILING = 0.96
_SILENCE_FLOOR_RMS = 10.0 ** (-70.0 / 20.0)

DEFAULT_COLABONE_VOICE_PRESET = "shen_zhiyi"
COLABONE_VOICE_PRESET_ANCHOR_TEXT = (
    "大家好，欢迎来到今天的节目。我们将用清晰自然的声音，为您娓娓道来每一个精彩细节，"
    "希望这段分享能带给您轻松愉快的收听体验。"
)
COLABONE_TTS_VOICE_PRESETS: dict[str, dict[str, Any]] = {
    "shen_zhiyi": {
        "id": "shen_zhiyi",
        "name": "知性女主播 · 沈知意",
        "gender": "female",
        "style": "知性主播",
        "description": "清澈温润、自然流畅的棚录知性女声，适合知识科普、品牌介绍与通用口播（默认推荐）。",
        "seed": 202601,
        "speaker_id": "",
        "preview_text": (
            "大家好，我是沈知意。真正打动人心的讲述，不在于声嘶力竭，"
            "而在于把复杂的世界说得清澈而温暖。今天，让我们从最细微的灵感出发，一起看见未来的轮廓。"
        ),
        "voxcpm_prompt": (
            "A 26-year-old Chinese female host, clear warm articulate studio microphone voice, "
            "natural smooth pacing, friendly confident professional tone"
        ),
        "qwen_instruct": (
            "26岁年轻知性女性主播，清澈温润的现代标准普通话，吐字清晰圆润，"
            "棚录近麦高保真质感，语速自然流畅，专业沉稳且富有亲和力。"
        ),
    },
    "lin_xiaoman": {
        "id": "lin_xiaoman",
        "name": "甜美活力 · 林小满",
        "gender": "female",
        "style": "甜美活力",
        "description": "清亮灵动略带笑意的元气女声，节奏明快富有感染力，适合种草口播与短视频带货。",
        "seed": 202602,
        "speaker_id": "",
        "preview_text": (
            "嗨，我是林小满！刚拆开这份春日限定惊喜，我就忍不住想第一时间分享给你——"
            "每一个细节都闪着轻快的光，准备好了吗？我们现在就一起出发吧！"
        ),
        "voxcpm_prompt": (
            "A 22-year-old Chinese young woman, bright sweet lively voice with a gentle smile, "
            "clear crisp articulation, cheerful energetic natural rhythm"
        ),
        "qwen_instruct": (
            "22岁甜美活泼的年轻女生，嗓音清亮灵动略带笑意，元气自然不腻耳，"
            "咬字轻快清晰，节奏明快富有感染力。"
        ),
    },
    "su_wanqing": {
        "id": "su_wanqing",
        "name": "温柔治愈 · 苏晚晴",
        "gender": "female",
        "style": "温柔治愈",
        "description": "柔和细腻、气息温暖舒缓的治愈系女声，适合故事解说、情感电台与深度陪伴口播。",
        "seed": 202603,
        "speaker_id": "",
        "preview_text": (
            "晚安，我是苏晚晴。窗外的风轻轻掠过树梢，喧嚣的城市也渐渐慢了下来。"
            "如果今天有些疲惫，不妨给自己留出几分钟，听一段温柔的故事，把心安放在此刻。"
        ),
        "voxcpm_prompt": (
            "A 25-year-old Chinese young woman, soft gentle healing radio voice, "
            "warm delicate breathy tone, calm unhurried soothing studio narration"
        ),
        "qwen_instruct": (
            "25岁温柔治愈系年轻女声，声线柔和细腻、气息温暖舒缓，语调轻柔自然，"
            "像深夜电台好友娓娓道来，耐听且有安全感。"
        ),
    },
    "gu_qinglan": {
        "id": "gu_qinglan",
        "name": "干练职场 · 顾清岚",
        "gender": "female",
        "style": "干练御姐",
        "description": "醇厚沉稳、吐字干净利落的职场女声，富有说服力，适合商业分析、科技测评与课程讲解。",
        "seed": 202604,
        "speaker_id": "",
        "preview_text": (
            "你好，我是顾清岚。在高速变化的行业周期里，真正拉开差距的从来不是盲目追风，"
            "而是看清底层逻辑后的果断落子。接下来三分钟，我们直击核心结论。"
        ),
        "voxcpm_prompt": (
            "A 30-year-old mature Chinese professional woman, rich poised warm mid-low voice, "
            "crisp decisive articulation, persuasive confident executive tone"
        ),
        "qwen_instruct": (
            "30岁成熟干练的职场女性，中低音区醇厚清亮，吐字干净利落，"
            "逻辑停顿清晰，语气温和而坚定，极富专业说服力。"
        ),
    },
    "lu_mingwei": {
        "id": "lu_mingwei",
        "name": "播音气质 · 陆明薇",
        "gender": "female",
        "style": "新闻播音",
        "description": "共鸣饱满、字正腔圆的专业女播音员声线，沉稳大气，适合资讯播报与正式专题讲解。",
        "seed": 202605,
        "speaker_id": "",
        "preview_text": (
            "各位观众大家好，我是陆明薇。从实验室的前沿突破，到千家万户的日常变迁，"
            "每一次技术跃迁都在书写时代的新注脚。下面为您带来本期专题深度观察。"
        ),
        "voxcpm_prompt": (
            "A 28-year-old professional Chinese female broadcast news anchor, "
            "resonant polished studio voice, crisp standard Mandarin diction, "
            "steady authoritative warm cadence"
        ),
        "qwen_instruct": (
            "28岁专业新闻女播音员，胸腔共鸣饱满大气，标准普通话字正腔圆，"
            "重音与停连考究，沉稳大方且自然流畅。"
        ),
    },
    "chen_jingzhou": {
        "id": "chen_jingzhou",
        "name": "沉稳男主讲 · 陈景舟",
        "gender": "male",
        "style": "沉稳商业",
        "description": "低沉磁性、节奏稳健的成熟男主讲声线，富有厚度与可信度，适合商业洞察、金融解读与品牌发布。",
        "seed": 202606,
        "speaker_id": "",
        "preview_text": (
            "大家好，我是陈景舟。长期主义的魅力，在于时间终会奖励那些坚持做难而正确之事的人。"
            "今天，我们一同拆解表象之下的长期价值与增长飞轮。"
        ),
        "voxcpm_prompt": (
            "A 34-year-old mature Chinese male keynote speaker, deep resonant warm baritone studio voice, "
            "steady composed cadence, authoritative trustworthy executive narration"
        ),
        "qwen_instruct": (
            "34岁沉稳儒雅的成熟男性主讲人，低沉富有磁性的男中音，棚录近麦浑厚通透，"
            "语速稳健从容，重音清晰有力，兼具商业洞察力与信赖感。"
        ),
    },
    "lin_chenxu": {
        "id": "lin_chenxu",
        "name": "阳光青年 · 林晨煦",
        "gender": "male",
        "style": "科技测评",
        "description": "清爽明朗、自然有活力的青年男声，逻辑利落不油腻，适合数码评测、产品开箱与泛科技口播。",
        "seed": 202607,
        "speaker_id": "",
        "preview_text": (
            "哈喽各位，我是林晨煦！上手实测了整整七天，这台新设备最让我惊喜的并不是冰冷的参数表，"
            "而是点亮屏幕那一刻丝滑到上头的真实手感，咱们直接看实测！"
        ),
        "voxcpm_prompt": (
            "A 24-year-old Chinese young male tech host, crisp bright energetic voice, "
            "clean natural diction, engaging upbeat conversational studio delivery"
        ),
        "qwen_instruct": (
            "24岁阳光清爽的青年科技男主播，嗓音清澈明朗富有朝气，吐字干净利落，"
            "语调自然生动不刻意，非常适合数码开箱与产品深度体验口播。"
        ),
    },
    "zhou_moyan": {
        "id": "zhou_moyan",
        "name": "温柔书生 · 周墨言",
        "gender": "male",
        "style": "散文朗读",
        "description": "清润雅致、气息绵长的书卷气男声，娓娓道来富于画面感，适合文学朗读、历史人文与文化随笔。",
        "seed": 202608,
        "speaker_id": "",
        "preview_text": (
            "见字如晤，我是周墨言。江南的春雨总落得极轻，打湿青石巷陌，也润开了案头半卷旧书。"
            "翻过岁月的折痕，文字仍在静谧处替我们留住山河与故人。"
        ),
        "voxcpm_prompt": (
            "A 27-year-old gentle scholarly Chinese male narrator, warm refined soft-spoken voice, "
            "lyrical unhurried phrasing, intimate poetic studio audiobook tone"
        ),
        "qwen_instruct": (
            "27岁温润雅致的书卷气青年男声，声音清朗柔和、气息舒展从容，"
            "语调抑扬有致而克制，自带人文与散文朗读的静谧画面感。"
        ),
    },
    "gu_zhengyuan": {
        "id": "gu_zhengyuan",
        "name": "新闻男播音 · 顾正远",
        "gender": "male",
        "style": "权威播报",
        "description": "声如洪钟、端庄正气的专业男播音员声线，字正腔圆气场十足，适合时政资讯、官方公告与大型专题片。",
        "seed": 202609,
        "speaker_id": "",
        "preview_text": (
            "观众朋友们好，我是顾正远。立足当下、放眼全局，每一组扎实的数据背后，"
            "都是无数建设者笃行不怠的身影。接下来，请看本台发回的最新深度报道。"
        ),
        "voxcpm_prompt": (
            "A 36-year-old professional Chinese male broadcast news anchor, rich sonorous authoritative baritone, "
            "impeccable standard Mandarin articulation, crisp formal studio broadcast cadence"
        ),
        "qwen_instruct": (
            "36岁资深专业男性新闻播音员，声音浑厚铿锵、共鸣饱满端正，标准普通话字正腔圆，"
            "断句精准有力，兼具庄重权威感与广播级清晰度。"
        ),
    },
    "su_hechuan": {
        "id": "su_hechuan",
        "name": "沧桑讲述者 · 苏鹤川",
        "gender": "male",
        "style": "纪录片旁白",
        "description": "略带质感沙哑、深沉厚重的叙事男声，饱含时间沉淀与电影史诗感，适合纪录片旁白、悬疑故事与人物传记。",
        "seed": 202610,
        "speaker_id": "",
        "preview_text": (
            "我是苏鹤川。当戈壁的最后一缕夕阳沉入地平线，风沙掩埋了昔日的驼铃，"
            "却掩不住刻在岩壁上的千年回响。有些故事，只有走过漫长黑夜的人，才真正听得懂。"
        ),
        "voxcpm_prompt": (
            "A 45-year-old seasoned Chinese male documentary narrator, deep husky textured low baritone, "
            "cinematic unhurried gravitas, evocative storytelling with warm aged resonance"
        ),
        "qwen_instruct": (
            "45岁历经沧桑的深沉纪录片男旁白，声线低沉厚重略带岁月沙哑质感，"
            "气息沉稳悠长，充满电影级叙事张力与历史厚重感。"
        ),
    },
    "qwen_vivian": {
        "id": "qwen_vivian",
        "name": "Qwen 官方 · Vivian（明快锐气女声）",
        "gender": "female",
        "style": "官方原生 · 中文",
        "description": "Qwen3-TTS-CustomVoice 官方原生音色（Vivian），明亮干脆、略带个性锐气的年轻女声，中文表达灵动自然。",
        "seed": 202611,
        "speaker_id": "Vivian",
        "preview_text": (
            "你好，我是 Vivian。比起循规蹈矩地复述答案，我更喜欢用鲜活的观点和利落的节奏，"
            "把每一个灵感讲出让人眼前一亮的火花。"
        ),
        "voxcpm_prompt": (
            "A bright crisp young Chinese female voice with lively edge, confident modern diction, "
            "natural energetic phrasing"
        ),
        "qwen_instruct": "语气自然明快，略带自信俏皮的现代年轻女声表达。",
    },
    "qwen_serena": {
        "id": "qwen_serena",
        "name": "Qwen 官方 · Serena（温婉柔和女声）",
        "gender": "female",
        "style": "官方原生 · 中文",
        "description": "Qwen3-TTS-CustomVoice 官方原生音色（Serena），温润柔美、亲切舒缓的年轻女声，适合陪伴式口播与情感讲述。",
        "seed": 202612,
        "speaker_id": "Serena",
        "preview_text": (
            "你好，我是 Serena。愿这段轻柔的声音，能像午后洒在书页上的一缕暖阳，"
            "陪你放慢脚步，在细水长流的日常里听见心底的宁静。"
        ),
        "voxcpm_prompt": (
            "A warm gentle young Chinese female voice, soft soothing studio timbre, "
            "friendly unhurried natural narration"
        ),
        "qwen_instruct": "温柔柔和、娓娓道来的温暖女声，语调自然舒缓。",
    },
    "qwen_uncle_fu": {
        "id": "qwen_uncle_fu",
        "name": "Qwen 官方 · Uncle_Fu（醇厚低沉大叔）",
        "gender": "male",
        "style": "官方原生 · 中文",
        "description": "Qwen3-TTS-CustomVoice 官方原生音色（Uncle_Fu），浑厚醇和、沉稳老练的中年低音男声，富有阅历感与温度。",
        "seed": 202613,
        "speaker_id": "Uncle_Fu",
        "preview_text": (
            "你好，我是傅叔。走过半生风雨才会明白，真正经得起推敲的道理，从来不需要高声喧哗；"
            "沏一壶热茶，咱们慢慢把这事儿聊透。"
        ),
        "voxcpm_prompt": (
            "A seasoned mature Chinese male voice, deep mellow resonant low baritone, "
            "calm unhurried storytelling warmth"
        ),
        "qwen_instruct": "低沉醇厚、不疾不徐的成熟男声，富有阅历与沉稳感染力。",
    },
    "qwen_dylan": {
        "id": "qwen_dylan",
        "name": "Qwen 官方 · Dylan（京味青年男声）",
        "gender": "male",
        "style": "官方原生 · 北京话",
        "description": "Qwen3-TTS-CustomVoice 官方原生音色（Dylan），清亮爽朗、自带自然北京胡同腔的青年男声，轻松接地气。",
        "seed": 202614,
        "speaker_id": "Dylan",
        "preview_text": (
            "嘿，您好！我是 Dylan。秋天的北京天儿最清亮，胡同口儿飘着糖炒栗子的香，"
            "咱今儿个不整虚的，就坐一块儿把这新鲜事儿给您唠明白！"
        ),
        "voxcpm_prompt": (
            "A youthful clear Beijing Mandarin male voice, relaxed crisp colloquial cadence, "
            "friendly down-to-earth natural delivery"
        ),
        "qwen_instruct": "清爽松弛、自然带京味儿的青年男声，口语感真实流畅。",
    },
    "qwen_eric": {
        "id": "qwen_eric",
        "name": "Qwen 官方 · Eric（川渝爽朗男声）",
        "gender": "male",
        "style": "官方原生 · 四川话",
        "description": "Qwen3-TTS-CustomVoice 官方原生音色（Eric），活泼亮堂、略带沙哑磁性的成都青年男声，松弛鲜活富于烟火气。",
        "seed": 202615,
        "speaker_id": "Eric",
        "preview_text": (
            "哈喽大家好，我是 Eric！成都这边的茶馆头最安逸，泡一杯盖碗茶，摆两句龙门阵，"
            "今天带大家看看巴适得很的新鲜玩法，绝对让你眼前一亮！"
        ),
        "voxcpm_prompt": (
            "A lively bright Sichuan Chinese male voice with subtle warm huskiness, "
            "expressive approachable conversational charm"
        ),
        "qwen_instruct": "活泼自然、带着成都烟火气与些许沙哑亮度的爽朗男声。",
    },
    "qwen_ryan": {
        "id": "qwen_ryan",
        "name": "Qwen 官方 · Ryan（节奏感英文男声）",
        "gender": "male",
        "style": "官方原生 · English",
        "description": "Qwen3-TTS-CustomVoice 官方原生音色（Ryan），富有动感与演讲张力的英文男声，节奏利落，适合国际产品发布与英文口播。",
        "seed": 202616,
        "speaker_id": "Ryan",
        "preview_text": (
            "Hello everyone, I'm Ryan. Great storytelling lives in the rhythm between ideas—"
            "sharp, fearless, and alive. Let's dive right in and build something truly unforgettable today."
        ),
        "voxcpm_prompt": (
            "Dynamic confident English male presenter with crisp articulation, "
            "strong rhythmic pacing, clear studio voice"
        ),
        "qwen_instruct": "Dynamic confident English male presenter with crisp articulation and energetic rhythm.",
    },
    "qwen_aiden": {
        "id": "qwen_aiden",
        "name": "Qwen 官方 · Aiden（阳光美式男声）",
        "gender": "male",
        "style": "官方原生 · English",
        "description": "Qwen3-TTS-CustomVoice 官方原生音色（Aiden），清朗温暖、中音区通透的阳光美式青年男声，亲切自然。",
        "seed": 202617,
        "speaker_id": "Aiden",
        "preview_text": (
            "Hey there, I'm Aiden. There's something electric about a crisp morning when a fresh idea "
            "clicks into place. Grab a coffee, settle in, and let's walk through this together."
        ),
        "voxcpm_prompt": (
            "Warm sunny American English young male voice with clear midrange clarity "
            "and friendly conversational pacing"
        ),
        "qwen_instruct": "Warm sunny American male voice with clear midrange clarity and friendly conversational pacing.",
    },
    "qwen_ono_anna": {
        "id": "qwen_ono_anna",
        "name": "Qwen 官方 · Ono_Anna（灵动日语女声）",
        "gender": "female",
        "style": "官方原生 · 日本語",
        "description": "Qwen3-TTS-CustomVoice 官方原生音色（Ono_Anna），轻盈灵动、活泼清脆的日语女声，表现力丰富。",
        "seed": 202618,
        "speaker_id": "Ono_Anna",
        "preview_text": (
            "こんにちは、小野アンナです。澄んだ朝の光みたいに、軽やかで心が弾む言葉をお届けします。"
            "今日はどんな素敵な発見が待っているのか、一緒に確かめましょうね！"
        ),
        "voxcpm_prompt": (
            "Playful light nimble Japanese young female voice, bright crisp articulation, "
            "cheerful expressive studio cadence"
        ),
        "qwen_instruct": "明るく軽やかで親しみやすい日本語の女性ボイス。",
    },
    "qwen_sohee": {
        "id": "qwen_sohee",
        "name": "Qwen 官方 · Sohee（情感韩语女声）",
        "gender": "female",
        "style": "官方原生 · 한국어",
        "description": "Qwen3-TTS-CustomVoice 官方原生音色（Sohee），温暖细腻、情感充沛的韩语女声，自然真挚富于感染力。",
        "seed": 202619,
        "speaker_id": "Sohee",
        "preview_text": (
            "안녕하세요, 소희입니다. 진심이 담긴 목소리는 말 너머의 온도까지 전해준다고 믿어요. "
            "오늘의 이야기가 여러분의 하루에 잔잔한 울림이 되길 바랄게요."
        ),
        "voxcpm_prompt": (
            "Warm expressive Korean young female voice with rich emotional resonance "
            "and clear intimate studio narration"
        ),
        "qwen_instruct": "따뜻하고 감정이 풍부한 자연스러운 한국어 여성 목소리.",
    },
}
_VOICE_PRESET_ID_RE = re.compile(r"^[a-zA-Z0-9_-]{1,64}$")


def resolve_colabone_voice_preset(
    engine: dict[str, Any], preset_id: str | None
) -> dict[str, Any] | None:
    """Resolve a built-in named voice preset for a TTS engine, merging catalog & canonical defaults."""
    raw = str(preset_id or "").strip()
    if raw.lower() == "custom":
        return None
    target_id = raw or DEFAULT_COLABONE_VOICE_PRESET

    if hasattr(engine, "model_dump"):
        engine_dict = engine.model_dump()
    elif isinstance(engine, dict):
        engine_dict = engine
    else:
        engine_dict = {}
    engine_id = str(engine_dict.get("id") or "").strip()

    raw_presets = engine_dict.get("voice_presets") or []
    engine_entry: dict[str, Any] | None = None
    for item in raw_presets:
        if hasattr(item, "model_dump"):
            candidate = item.model_dump()
        elif isinstance(item, dict):
            candidate = copy.deepcopy(item)
        else:
            continue
        if str(candidate.get("id") or "").strip() == target_id:
            engine_entry = candidate
            break

    # When an engine declares voice_presets, treat it as an authoritative allowlist.
    if raw_presets and engine_entry is None:
        return None

    fallback_entry = COLABONE_TTS_VOICE_PRESETS.get(target_id)
    if engine_entry is None:
        if fallback_entry is None:
            return None
        if str(fallback_entry.get("speaker_id") or "").strip() and not (
            bool(engine_dict.get("supports_custom_speakers"))
            or engine_id == "qwen3_tts"
        ):
            return None

    merged: dict[str, Any] = copy.deepcopy(fallback_entry) if fallback_entry is not None else {}
    if engine_entry is not None:
        for key, val in engine_entry.items():
            if key == "seed":
                if val is not None and not isinstance(val, bool):
                    try:
                        merged["seed"] = max(0, int(val))
                    except (TypeError, ValueError):
                        pass
            elif key in ("prompt", "preview_text", "speaker_id"):
                if str(val or "").strip():
                    merged[key] = str(val).strip()
            elif val not in (None, ""):
                merged[key] = copy.deepcopy(val)

    merged["id"] = str(merged.get("id") or target_id).strip()
    merged["name"] = str(merged.get("name") or merged["id"]).strip()
    merged["gender"] = str(merged.get("gender") or "female").strip() or "female"
    merged["style"] = str(merged.get("style") or "").strip()
    merged["description"] = str(merged.get("description") or "").strip()
    merged["speaker_id"] = str(merged.get("speaker_id") or "").strip()
    merged["preview_text"] = str(merged.get("preview_text") or "").strip()

    seed_val = merged.get("seed")
    if isinstance(seed_val, bool) or seed_val is None:
        seed_val = int(fallback_entry["seed"]) if fallback_entry is not None else 42
    else:
        try:
            seed_val = int(seed_val)
        except (TypeError, ValueError):
            seed_val = int(fallback_entry["seed"]) if fallback_entry is not None else 42
    merged["seed"] = max(0, seed_val)

    prompt_val = str(merged.get("prompt") or "").strip()
    if not prompt_val:
        if engine_id in ("qwen3_tts", "ming_3_tts", "breeze_tts2"):
            prompt_val = str(
                merged.get("qwen_instruct") or merged.get("voxcpm_prompt") or ""
            ).strip()
        else:
            prompt_val = str(
                merged.get("voxcpm_prompt") or merged.get("qwen_instruct") or ""
            ).strip()
    merged["prompt"] = prompt_val
    if not str(merged.get("voxcpm_prompt") or "").strip():
        merged["voxcpm_prompt"] = prompt_val
    if not str(merged.get("qwen_instruct") or "").strip():
        merged["qwen_instruct"] = prompt_val
    return merged


def _atomic_write_text(path: Path, text: str) -> None:
    if path.is_symlink():
        raise ValueError(f"目标文件不能是符号链接：{path.name}")
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, temp = tempfile.mkstemp(prefix=".save-", suffix=".tmp", dir=str(path.parent))
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as stream:
            stream.write(text)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temp, path)
    finally:
        if os.path.exists(temp):
            os.unlink(temp)


# ---------------------------------------------------------------------------
# 1. Pure script segmentation helpers
# ---------------------------------------------------------------------------


def _inline_tag_spans(text: str) -> list[tuple[int, int]]:
    return [(m.start(), m.end()) for m in _INLINE_TAG_RE.finditer(text)]


def _in_spans(index: int, spans: list[tuple[int, int]]) -> tuple[int, int] | None:
    for start, end in spans:
        if start <= index < end:
            return (start, end)
        if start > index:
            break
    return None


def _is_cjk_char(ch: str) -> bool:
    code = ord(ch)
    return (
        0x4E00 <= code <= 0x9FFF
        or 0x3400 <= code <= 0x4DBF
        or 0x3000 <= code <= 0x303F
        or 0x3040 <= code <= 0x30FF
        or 0xAC00 <= code <= 0xD7AF
        or 0xFF00 <= code <= 0xFFEF
        or 0x20000 <= code <= 0x2A6DF
    )


def _is_ascii_sentence_delim(text: str, idx: int) -> bool:
    """Return True if ASCII '!', '?', or ';' ends a sentence (not inside a URL/query)."""
    n = len(text)
    if text[idx] not in _ASCII_HARD_SENTENCE_DELIMS:
        return False
    after = idx + 1
    while after < n and (
        text[after] in _HARD_SENTENCE_DELIMS or text[after] == "."
    ):
        after += 1
    while after < n and text[after] in _CLOSING_TRAILING:
        after += 1
    if after >= n:
        return True
    nxt = text[after]
    return nxt.isspace() or _is_cjk_char(nxt)


def _is_english_sentence_period(text: str, idx: int) -> bool:
    """Return True if '.' at idx marks an English sentence boundary (not decimal/abbrev)."""
    n = len(text)
    if text[idx] != ".":
        return False
    # Skip ellipsis run except at the end of the run.
    if idx + 1 < n and text[idx + 1] == ".":
        return False
    # Look past optional closing quotes after the period.
    after = idx + 1
    while after < n and text[after] in _CLOSING_TRAILING:
        after += 1
    # Must be followed by whitespace, a CJK character, or end-of-line (prevents splitting URLs/versions/decimals).
    if after < n and not (text[after].isspace() or _is_cjk_char(text[after])):
        return False
    # Avoid splitting numbered list prefixes like "1. " or "12. " at the start of a line/segment,
    # while still splitting sentences ending in years like "in 2026. It gained...".
    if idx > 0 and text[idx - 1].isdigit():
        j = idx - 1
        while j >= 0 and text[j].isdigit():
            j -= 1
        digit_len = idx - (j + 1)
        if digit_len <= 2 and (j < 0 or text[: j + 1].strip() == ""):
            return False
    # Avoid common abbreviations like "Mr.", "Dr.", "e.g.".
    k = idx - 1
    while k >= 0 and (text[k].isalpha() or text[k] == "."):
        k -= 1
    token = text[k + 1 : idx].lower()
    if token in _ENGLISH_ABBREVS:
        return False
    # Single uppercase initial like "J. K. Rowling".
    if len(token) == 1 and text[idx - 1].isupper():
        return False
    return True


def _split_line_sentences(line: str) -> list[str]:
    """Split one cleaned line on Chinese/English sentence boundaries, preserving [...] tags."""
    spans = _inline_tag_spans(line)
    parts: list[str] = []
    n = len(line)
    start = 0
    i = 0
    while i < n:
        span = _in_spans(i, spans)
        if span is not None:
            i = span[1]
            continue
        ch = line[i]
        is_delim = (
            ch in _CJK_HARD_SENTENCE_DELIMS
            or (ch in _ASCII_HARD_SENTENCE_DELIMS and _is_ascii_sentence_delim(line, i))
            or (ch == "." and _is_english_sentence_period(line, i))
        )
        if is_delim:
            j = i + 1
            while j < n and _in_spans(j, spans) is None and (
                line[j] in _HARD_SENTENCE_DELIMS or line[j] == "."
            ):
                j += 1
            while j < n and _in_spans(j, spans) is None and line[j] in _CLOSING_TRAILING:
                j += 1
            piece = line[start:j].strip()
            if piece:
                parts.append(piece)
            start = j
            i = j
            continue
        i += 1
    tail = line[start:].strip()
    if tail:
        parts.append(tail)
    return parts


def _split_clause_boundaries(text: str) -> list[str]:
    """Split text on clause punctuation outside [...] inline tags."""
    spans = _inline_tag_spans(text)
    parts: list[str] = []
    n = len(text)
    start = 0
    i = 0
    while i < n:
        span = _in_spans(i, spans)
        if span is not None:
            i = span[1]
            continue
        ch = text[i]
        if ch in _CLAUSE_DELIMS:
            if ch == ":" and i + 1 < n and (text[i + 1] == "/" or text[i + 1].isdigit()):
                i += 1
                continue
            if (
                ch == ","
                and i > 0
                and text[i - 1].isdigit()
                and i + 1 < n
                and text[i + 1].isdigit()
            ):
                i += 1
                continue
            j = i + 1
            while j < n and _in_spans(j, spans) is None and text[j] in _CLAUSE_DELIMS:
                j += 1
            while j < n and _in_spans(j, spans) is None and text[j] in _CLOSING_TRAILING:
                j += 1
            piece = text[start:j].strip()
            if piece:
                parts.append(piece)
            start = j
            i = j
            continue
        i += 1
    tail = text[start:].strip()
    if tail:
        parts.append(tail)
    return parts


def _join_fragments(left: str, right: str) -> str:
    left = left.strip()
    right = right.strip()
    if not left:
        return right
    if not right:
        return left
    if left[-1] in _CJK_PUNCT_END:
        return left + right
    if left[-1] == "]" or left.endswith("|>"):
        if right[0] in _CJK_PUNCT_END or right[0] in ".!?;,:":
            return left + right
        return f"{left} {right}"
    if left[-1].isascii() and right[0].isascii():
        return f"{left} {right}"
    if left[-1] in ".!?;,:":
        return f"{left} {right}"
    if not left[-1].isascii() and not right[0].isascii():
        return f"{left} {right}" if left[-1] not in _CJK_PUNCT_END and right[0] not in _CJK_PUNCT_END else left + right
    return f"{left} {right}"


def _atomic_units(text: str) -> list[str]:
    """Tokenize text into indivisible units so [...] inline tags and ASCII words stay intact."""
    spans = _inline_tag_spans(text)
    units: list[str] = []
    n = len(text)
    i = 0
    while i < n:
        span = _in_spans(i, spans)
        if span is not None:
            units.append(text[span[0] : span[1]])
            i = span[1]
            continue
        ch = text[i]
        if ch.isspace():
            units.append(" ")
            while i < n and text[i].isspace():
                i += 1
            continue
        if ch.isascii() and (ch.isalnum() or ch in "_'-"):
            j = i + 1
            while (
                j < n
                and _in_spans(j, spans) is None
                and text[j].isascii()
                and (text[j].isalnum() or text[j] in "_'-")
            ):
                j += 1
            # Keep immediately attached ASCII punctuation with the word.
            while (
                j < n
                and _in_spans(j, spans) is None
                and text[j] in ".,!?;:)\"'"
            ):
                j += 1
            units.append(text[i:j])
            i = j
            continue
        units.append(ch)
        i += 1
    return units


def _split_overlong_fragment(fragment: str, max_chars: int) -> list[str]:
    """Break a single fragment exceeding max_chars without ever breaking inside [...]."""
    fragment = fragment.strip()
    if len(fragment) <= max_chars:
        return [fragment] if fragment else []

    clauses = _split_clause_boundaries(fragment)
    if len(clauses) > 1:
        packed: list[str] = []
        current = ""
        for clause in clauses:
            if not current:
                if len(clause) <= max_chars:
                    current = clause
                else:
                    packed.extend(_split_by_units(clause, max_chars))
            else:
                candidate = _join_fragments(current, clause)
                if len(candidate) <= max_chars:
                    current = candidate
                else:
                    packed.append(current)
                    if len(clause) <= max_chars:
                        current = clause
                    else:
                        packed.extend(_split_by_units(clause, max_chars))
                        current = ""
        if current:
            packed.append(current)
        return [p for p in packed if p]

    return _split_by_units(fragment, max_chars)


def _is_inline_tag_unit(unit: str) -> bool:
    return bool(_INLINE_TAG_RE.fullmatch(unit))


def _has_speakable_text(text: str) -> bool:
    cleaned = _PINYIN_TAG_RE.sub(r"\1", text)
    cleaned = _NON_SPEAKABLE_INLINE_TAG_RE.sub("", cleaned)
    return bool(cleaned.strip())


def _split_by_units(text: str, max_chars: int) -> list[str]:
    raw_units = _atomic_units(text)
    units: list[str] = []
    for unit in raw_units:
        if len(unit) > max_chars and not _is_inline_tag_unit(unit):
            for pos in range(0, len(unit), max_chars):
                units.append(unit[pos : pos + max_chars])
        else:
            units.append(unit)

    chunks: list[str] = []
    buf = ""
    for unit in units:
        if not buf and unit == " ":
            continue
        if not buf:
            buf = unit
            continue
        if len(buf) + len(unit) <= max_chars:
            buf += unit
        else:
            cleaned = buf.strip()
            if cleaned:
                chunks.append(cleaned)
            buf = "" if unit == " " else unit
    cleaned = buf.strip()
    if cleaned:
        chunks.append(cleaned)
    return chunks


def split_colabone_tts_script(text: str, max_chars: int = 120) -> list[str]:
    """Split a TTS script into clean, speakable segments respecting max_chars.

    - Cleans blank lines and normalizes whitespace.
    - Splits on paragraph/line breaks and Chinese/English sentence boundaries.
    - Never splits inside inline emotion/style bracket tags like [laugh] or [whisper].
    - Merges overly short fragments (< 12 chars) with neighbors while respecting max_chars.
    - Drops scripts or isolated fragments containing only inline tags with no speakable text.
    """
    if max_chars < 1:
        raise ValueError("max_chars 必须大于 0")
    if not isinstance(text, str):
        return []
    normalized = text.replace("\r\n", "\n").replace("\r", "\n")
    raw_lines = [
        _WHITESPACE_RUN_RE.sub(" ", line).strip()
        for line in normalized.split("\n")
    ]
    lines = [line for line in raw_lines if line]
    if not lines or not _has_speakable_text(normalized):
        return []

    fragments: list[str] = []
    for line in lines:
        for sentence in _split_line_sentences(line):
            if len(sentence) <= max_chars:
                fragments.append(sentence)
            else:
                fragments.extend(_split_overlong_fragment(sentence, max_chars))

    if len(fragments) <= 1:
        return [seg for seg in fragments if seg and _has_speakable_text(seg)]

    min_chars = min(_MIN_SHORT_FRAGMENT_CHARS, max_chars)
    merged = list(fragments)
    changed = True
    while changed and len(merged) > 1:
        changed = False
        for idx in range(len(merged)):
            frag = merged[idx]
            is_pure_tag = bool(_PURE_INLINE_TAG_RE.fullmatch(frag))
            if len(frag) >= min_chars and not is_pure_tag:
                continue

            left_cand = (
                _join_fragments(merged[idx - 1], frag) if idx > 0 else None
            )
            right_cand = (
                _join_fragments(frag, merged[idx + 1])
                if idx + 1 < len(merged)
                else None
            )
            can_left = left_cand is not None and len(left_cand) <= max_chars
            can_right = right_cand is not None and len(right_cand) <= max_chars

            if not can_left and not can_right:
                continue

            # Inline tags like [laugh] / [whisper] attach forward by preference.
            if is_pure_tag and can_right:
                merged[idx : idx + 2] = [right_cand]  # type: ignore[list-item]
                changed = True
                break

            if can_left and can_right:
                # Prefer the neighbor that yields the more balanced (shorter) combined chunk.
                if len(right_cand) < len(left_cand):  # type: ignore[arg-type]
                    merged[idx : idx + 2] = [right_cand]  # type: ignore[list-item]
                else:
                    merged[idx - 1 : idx + 1] = [left_cand]  # type: ignore[list-item]
                changed = True
                break
            elif can_right:
                merged[idx : idx + 2] = [right_cand]  # type: ignore[list-item]
                changed = True
                break
            else:
                merged[idx - 1 : idx + 1] = [left_cand]  # type: ignore[list-item]
                changed = True
                break

    return [seg for seg in merged if seg and _has_speakable_text(seg)]


# ---------------------------------------------------------------------------
# 2. Pure offline WAV I/O, resampling, trimming, loudness & master assembly
# ---------------------------------------------------------------------------


def _decode_pcm_bytes(raw: bytes, sampwidth: int, nchannels: int) -> list[float]:
    """Decode little-endian 8/16/24/32-bit PCM bytes into mono float samples in [-1, 1]."""
    if not raw or nchannels <= 0:
        return []
    if sampwidth not in (1, 2, 3, 4):
        raise ValueError(f"不支持的 PCM 位宽：{sampwidth} 字节")
    frame_bytes = sampwidth * nchannels
    usable = len(raw) - (len(raw) % frame_bytes)
    if usable <= 0:
        return []
    payload = raw[:usable]

    try:
        import numpy as np  # type: ignore[import-not-found]

        if sampwidth == 1:
            arr = (np.frombuffer(payload, dtype=np.uint8).astype(np.float64) - 128.0) / 128.0
        elif sampwidth == 2:
            arr = np.frombuffer(payload, dtype="<i2").astype(np.float64) / 32768.0
        elif sampwidth == 3:
            b = np.frombuffer(payload, dtype=np.uint8).reshape(-1, 3)
            vals = (
                b[:, 0].astype(np.int32)
                | (b[:, 1].astype(np.int32) << 8)
                | (b[:, 2].astype(np.int32) << 16)
            )
            vals = np.where(vals >= 0x800000, vals - 0x1000000, vals)
            arr = vals.astype(np.float64) / 8388608.0
        else:
            arr = np.frombuffer(payload, dtype="<i4").astype(np.float64) / 2147483648.0
        if nchannels > 1:
            arr = arr.reshape(-1, nchannels).mean(axis=1)
        return arr.tolist()
    except ImportError:
        pass

    if sampwidth == 1:
        floats = [(b - 128) / 128.0 for b in payload]
    elif sampwidth == 2:
        arr_py = array.array("h")
        arr_py.frombytes(payload)
        if sys.byteorder != "little":
            arr_py.byteswap()
        scale = 1.0 / 32768.0
        floats = [x * scale for x in arr_py]
    elif sampwidth == 3:
        scale = 1.0 / 8388608.0
        floats = []
        mv = memoryview(payload)
        for i in range(0, len(payload) - 2, 3):
            b0, b1, b2 = mv[i], mv[i + 1], mv[i + 2]
            val = b0 | (b1 << 8) | (b2 << 16)
            if val & 0x800000:
                val -= 0x1000000
            floats.append(val * scale)
    else:
        arr_py = array.array("i")
        arr_py.frombytes(payload)
        if sys.byteorder != "little":
            arr_py.byteswap()
        scale = 1.0 / 2147483648.0
        floats = [x * scale for x in arr_py]

    if nchannels <= 1:
        return floats

    inv_ch = 1.0 / nchannels
    mono: list[float] = []
    limit = len(floats) - (len(floats) % nchannels)
    for i in range(0, limit, nchannels):
        mono.append(sum(floats[i : i + nchannels]) * inv_ch)
    return mono


def _read_ieee_float_wav(path: Path) -> tuple[list[float], int]:
    """Fallback RIFF WAV reader supporting PCM (format 1) and IEEE float (format 3), including WAVE_FORMAT_EXTENSIBLE."""
    data = path.read_bytes()
    if len(data) < 12 or data[:4] != b"RIFF" or data[8:12] != b"WAVE":
        raise ValueError(f"无效的 WAV 文件头：{path}")
    pos = 12
    audio_format: int | None = None
    nchannels = 1
    sample_rate = 0
    bits_per_sample = 32
    pcm_bytes: bytes | None = None

    while pos + 8 <= len(data):
        chunk_id = data[pos : pos + 4]
        chunk_size = struct.unpack_from("<I", data, pos + 4)[0]
        chunk_start = pos + 8
        chunk_end = min(len(data), chunk_start + chunk_size)
        if chunk_id == b"fmt " and chunk_end - chunk_start >= 16:
            audio_format, nchannels, sample_rate, _, _, bits_per_sample = (
                struct.unpack_from("<HHIIHH", data, chunk_start)
            )
            if audio_format == 0xFFFE and chunk_end - chunk_start >= 40:
                subformat_tag = struct.unpack_from("<H", data, chunk_start + 24)[0]
                audio_format = subformat_tag
        elif chunk_id == b"data":
            pcm_bytes = data[chunk_start:chunk_end]
        pos = chunk_start + chunk_size + (chunk_size & 1)

    if (
        audio_format not in (1, 3)
        or sample_rate <= 0
        or nchannels <= 0
        or pcm_bytes is None
    ):
        raise ValueError(f"不支持的 WAV 编码格式：{path}")

    if audio_format == 1:
        if bits_per_sample not in (8, 16, 24, 32):
            raise ValueError(f"不支持的 PCM WAV 位深：{bits_per_sample}")
        return (
            _decode_pcm_bytes(pcm_bytes, bits_per_sample // 8, nchannels),
            int(sample_rate),
        )

    if bits_per_sample == 32:
        typecode = "f"
        np_dtype = "<f4"
        step = 4
    elif bits_per_sample == 64:
        typecode = "d"
        np_dtype = "<f8"
        step = 8
    else:
        raise ValueError(f"不支持的浮点 WAV 位深：{bits_per_sample}")

    usable = len(pcm_bytes) - (len(pcm_bytes) % (step * max(1, nchannels)))
    if usable <= 0:
        return [], int(sample_rate)

    try:
        import numpy as np  # type: ignore[import-not-found]

        arr_np = np.frombuffer(pcm_bytes[:usable], dtype=np_dtype).astype(np.float64)
        arr_np = np.where(np.isfinite(arr_np), arr_np, 0.0)
        if nchannels > 1:
            arr_np = arr_np.reshape(-1, nchannels).mean(axis=1)
        return arr_np.tolist(), int(sample_rate)
    except ImportError:
        pass

    arr = array.array(typecode)
    arr.frombytes(pcm_bytes[:usable])
    if sys.byteorder != "little":
        arr.byteswap()
    if nchannels <= 1:
        return [float(x) if math.isfinite(x) else 0.0 for x in arr], int(sample_rate)
    inv_ch = 1.0 / nchannels
    out: list[float] = []
    for idx in range(0, len(arr), nchannels):
        acc = 0.0
        for ch in range(nchannels):
            val = arr[idx + ch]
            if math.isfinite(val):
                acc += val
        out.append(acc * inv_ch)
    return out, int(sample_rate)


def read_wav_mono_float(path: Path) -> tuple[list[float], int]:
    """Read a WAV file (8/16/24/32-bit PCM, WAVE_FORMAT_EXTENSIBLE, or 32/64-bit IEEE float) as mono float samples in [-1, 1]."""
    wav_path = Path(path)
    if not wav_path.is_file():
        raise FileNotFoundError(f"WAV 文件不存在：{wav_path}")

    try:
        with wave.open(str(wav_path), "rb") as wf:
            nchannels = wf.getnchannels()
            sampwidth = wf.getsampwidth()
            sample_rate = wf.getframerate()
            nframes = wf.getnframes()
            raw = wf.readframes(nframes)
    except wave.Error:
        return _read_ieee_float_wav(wav_path)

    if sample_rate <= 0:
        raise ValueError(f"WAV 采样率无效：{sample_rate}")
    if nframes == 0 or not raw:
        return [], int(sample_rate)

    return _decode_pcm_bytes(raw, sampwidth, nchannels), int(sample_rate)


def write_wav_pcm16(path: Path, samples: Sequence[float], sample_rate: int) -> None:
    """Write mono floating-point samples in [-1, 1] as a 16-bit PCM WAV file atomically."""
    sr = int(sample_rate)
    if sr <= 0:
        raise ValueError(f"采样率必须大于 0：{sample_rate}")
    out_path = Path(path)
    out_path.parent.mkdir(parents=True, exist_ok=True)

    pcm_bytes: bytes
    try:
        import numpy as np  # type: ignore[import-not-found]

        arr = np.asarray(samples, dtype=np.float64).reshape(-1)
        arr = np.nan_to_num(arr, nan=0.0, posinf=1.0, neginf=-1.0)
        arr = np.clip(arr, -1.0, 1.0)
        int16_arr = np.clip(np.rint(arr * 32767.0), -32768, 32767).astype("<i2")
        pcm_bytes = int16_arr.tobytes()
    except ImportError:
        pcm = array.array("h")
        for sample in samples:
            val = float(sample)
            if not math.isfinite(val):
                pcm.append(0)
                continue
            if val > 1.0:
                val = 1.0
            elif val < -1.0:
                val = -1.0
            q = int(round(val * 32767.0))
            if q > 32767:
                q = 32767
            elif q < -32768:
                q = -32768
            pcm.append(q)
        if sys.byteorder != "little":
            pcm.byteswap()
        pcm_bytes = pcm.tobytes()

    fd, tmp_name = tempfile.mkstemp(prefix=".wav-", suffix=".tmp", dir=out_path.parent)
    tmp_path = Path(tmp_name)
    try:
        with os.fdopen(fd, "wb") as raw_f:
            with wave.open(raw_f, "wb") as wf:
                wf.setnchannels(1)
                wf.setsampwidth(2)
                wf.setframerate(sr)
                wf.writeframes(pcm_bytes)
            raw_f.flush()
            os.fsync(raw_f.fileno())
        os.replace(tmp_path, out_path)
    finally:
        tmp_path.unlink(missing_ok=True)


def _resample_audio_np(arr: Any, src_sr: int, dst_sr: int) -> Any:
    """Vectorized NumPy/soxr/scipy resampling returning a 1-D float64 ndarray."""
    import numpy as np  # type: ignore[import-not-found]

    s_sr = int(src_sr)
    d_sr = int(dst_sr)
    arr_f64 = np.asarray(arr, dtype=np.float64).reshape(-1)
    n = arr_f64.size
    if n == 0 or s_sr == d_sr:
        return arr_f64
    out_len = max(1, int(round(n * d_sr / s_sr)))
    if n == 1:
        return np.full(out_len, float(arr_f64[0]), dtype=np.float64)

    if n >= 64:
        try:
            import soxr  # type: ignore[import-not-found]

            resampled = soxr.resample(
                arr_f64.astype(np.float32), s_sr, d_sr, quality="HQ"
            )
            return np.asarray(resampled, dtype=np.float64).reshape(-1)
        except ImportError:
            pass

        try:
            from scipy import signal as sp_signal  # type: ignore[import-not-found]

            divisor = math.gcd(s_sr, d_sr)
            up = d_sr // divisor
            down = s_sr // divisor
            # Bound polyphase filter length so prime sample rates cannot allocate gigabyte FIR filters.
            if max(up, down) <= 1000:
                resampled = sp_signal.resample_poly(arr_f64, up, down)
                return np.asarray(resampled, dtype=np.float64).reshape(-1)
        except ImportError:
            pass

    positions = np.arange(out_len, dtype=np.float64) * (s_sr / d_sr)
    positions = np.clip(positions, 0.0, float(n - 1))
    return np.interp(positions, np.arange(n, dtype=np.float64), arr_f64)


def resample_audio_linear(
    samples: Sequence[float], src_sr: int, dst_sr: int
) -> list[float]:
    """Resample mono audio from src_sr to dst_sr.

    Uses soxr or bounded scipy.signal.resample_poly when installed (for clips >= 64 samples),
    and falls back cleanly to linear interpolation using numpy or Python stdlib.
    """
    s_sr = int(src_sr)
    d_sr = int(dst_sr)
    if s_sr <= 0 or d_sr <= 0:
        raise ValueError(f"采样率必须大于 0：src_sr={src_sr}, dst_sr={dst_sr}")

    n = len(samples)
    if n == 0:
        return []
    if s_sr == d_sr:
        try:
            import numpy as np  # type: ignore[import-not-found]

            return np.asarray(samples, dtype=np.float64).reshape(-1).tolist()
        except ImportError:
            return [float(x) for x in samples]

    out_len = max(1, int(round(n * d_sr / s_sr)))
    if n == 1:
        return [float(samples[0])] * out_len

    try:
        return _resample_audio_np(samples, s_sr, d_sr).tolist()
    except ImportError:
        pass

    # Pure-Python linear interpolation fallback (zero third-party dependencies).
    src = [float(x) for x in samples]
    ratio = s_sr / d_sr
    last_idx = n - 1
    out: list[float] = [0.0] * out_len
    for i in range(out_len):
        pos = i * ratio
        if pos >= last_idx:
            out[i] = src[last_idx]
        else:
            idx = int(pos)
            frac = pos - idx
            out[i] = src[idx] + (src[idx + 1] - src[idx]) * frac
    return out


def _trim_wav_silence_np(
    arr: Any,
    sample_rate: int,
    threshold_db: float = -42.0,
    pad_ms: float = 35.0,
) -> Any:
    import numpy as np  # type: ignore[import-not-found]

    arr_f64 = np.asarray(arr, dtype=np.float64).reshape(-1)
    n = arr_f64.size
    if n == 0:
        return arr_f64
    threshold = 10.0 ** (float(threshold_db) / 20.0)
    pad_samples = max(0, int(round(sample_rate * max(0.0, float(pad_ms)) / 1000.0)))
    active = np.flatnonzero(np.abs(arr_f64) >= threshold)
    if active.size == 0:
        return np.empty(0, dtype=np.float64)
    lo = max(0, int(active[0]) - pad_samples)
    hi = min(n, int(active[-1]) + 1 + pad_samples)
    return arr_f64[lo:hi]


def trim_wav_silence(
    samples: Sequence[float],
    sample_rate: int,
    threshold_db: float = -42.0,
    pad_ms: float = 35.0,
) -> list[float]:
    """Trim leading and trailing silence below threshold_db while preserving pad_ms margin."""
    sr = int(sample_rate)
    if sr <= 0:
        raise ValueError(f"采样率必须大于 0：{sample_rate}")
    n = len(samples)
    if n == 0:
        return []

    try:
        return _trim_wav_silence_np(
            samples, sr, threshold_db=threshold_db, pad_ms=pad_ms
        ).tolist()
    except ImportError:
        pass

    threshold = 10.0 ** (float(threshold_db) / 20.0)
    pad_samples = max(0, int(round(sr * max(0.0, float(pad_ms)) / 1000.0)))

    first_idx = -1
    for i in range(n):
        if abs(float(samples[i])) >= threshold:
            first_idx = i
            break
    if first_idx < 0:
        return []

    last_idx = first_idx
    for i in range(n - 1, first_idx - 1, -1):
        if abs(float(samples[i])) >= threshold:
            last_idx = i
            break

    lo = max(0, first_idx - pad_samples)
    hi = min(n, last_idx + 1 + pad_samples)
    return [float(x) for x in samples[lo:hi]]


def _soft_limit_sample(
    value: float,
    knee: float = _SOFT_LIMITER_KNEE,
    ceiling: float = _SOFT_LIMITER_CEILING,
) -> float:
    if not math.isfinite(value):
        return 0.0
    mag = abs(value)
    if mag <= knee:
        return float(value)
    headroom = ceiling - knee
    limited = knee + headroom * math.tanh((mag - knee) / headroom)
    return math.copysign(min(ceiling, limited), value)


def _soft_limit_array_np(
    arr: Any,
    knee: float = _SOFT_LIMITER_KNEE,
    ceiling: float = _SOFT_LIMITER_CEILING,
) -> Any:
    import numpy as np  # type: ignore[import-not-found]

    clean = np.asarray(arr, dtype=np.float64).reshape(-1)
    clean = np.where(np.isfinite(clean), clean, 0.0)
    mag = np.abs(clean)
    over = mag > knee
    if not np.any(over):
        return clean
    headroom = ceiling - knee
    limited = knee + headroom * np.tanh((mag[over] - knee) / headroom)
    limited = np.minimum(ceiling, limited)
    out = clean.copy()
    out[over] = np.copysign(limited, clean[over])
    return out


def _soft_limit_samples(samples: Sequence[float]) -> list[float]:
    try:
        return _soft_limit_array_np(samples).tolist()
    except ImportError:
        return [_soft_limit_sample(v) for v in samples]


def _normalize_audio_loudness_np(
    arr: Any, sample_rate: int, target_lufs: float = -23.0
) -> Any:
    import numpy as np  # type: ignore[import-not-found]

    clean = np.asarray(arr, dtype=np.float64).reshape(-1)
    if clean.size == 0:
        return clean
    clean = np.where(np.isfinite(clean), clean, 0.0)
    rms = float(np.sqrt(np.mean(clean * clean)))
    # Never boost near-silence / noise floor (below -70 dBFS RMS).
    if rms < _SILENCE_FLOOR_RMS:
        return clean

    try:
        import pyloudnorm as pyln  # type: ignore[import-not-found]

        if clean.size > int(sample_rate * 0.4) + 1:
            meter = pyln.Meter(sample_rate)
            loudness = float(meter.integrated_loudness(clean))
            # Silence or sub-gate audio (<= -70 LUFS) must be returned unchanged, never RMS-boosted.
            if not math.isfinite(loudness) or loudness <= -70.0:
                return clean
            gain_db = max(-40.0, min(40.0, float(target_lufs) - loudness))
            gain = 10.0 ** (gain_db / 20.0)
            return _soft_limit_array_np(clean * gain)
    except ImportError:
        pass
    except Exception:
        pass

    target_rms = 10.0 ** (float(target_lufs) / 20.0)
    max_gain = 10.0 ** (40.0 / 20.0)
    min_gain = 10.0 ** (-40.0 / 20.0)
    gain = max(min_gain, min(max_gain, target_rms / rms))
    return _soft_limit_array_np(clean * gain)


def _try_pyloudnorm(
    vals: list[float], sample_rate: int, target_lufs: float
) -> list[float] | None:
    try:
        import pyloudnorm as pyln  # type: ignore[import-not-found]
    except ImportError:
        return None
    # ITU-R BS.1770 gating block requires > 400 ms of audio.
    if len(vals) <= int(sample_rate * 0.4) + 1:
        return None
    try:
        try:
            import numpy as np  # type: ignore[import-not-found]

            data: Any = np.asarray(vals, dtype=np.float64)
        except ImportError:
            data = vals
        meter = pyln.Meter(sample_rate)
        loudness = float(meter.integrated_loudness(data))
        # Return unchanged (not None!) so silence/hiss <= -70 LUFS never falls through to +40 dB RMS gain.
        if not math.isfinite(loudness) or loudness <= -70.0:
            return list(vals)
        gain_db = max(-40.0, min(40.0, float(target_lufs) - loudness))
        gain = 10.0 ** (gain_db / 20.0)
        return _soft_limit_samples([v * gain for v in vals])
    except Exception:
        return None


def normalize_audio_loudness(
    samples: Sequence[float], sample_rate: int, target_lufs: float = -23.0
) -> list[float]:
    """Normalize mono audio loudness to target_lufs with a soft peak limiter (<= 0.96).

    Uses pyloudnorm.Meter(sample_rate) when installed (matching EBU R128 -23 LUFS),
    and deterministically falls back to RMS loudness normalization + soft peak limiting.
    Near-silence (RMS < -70 dBFS or integrated loudness <= -70 LUFS) is returned unchanged.
    """
    sr = int(sample_rate)
    if sr <= 0:
        raise ValueError(f"采样率必须大于 0：{sample_rate}")
    if len(samples) == 0:
        return []

    try:
        return _normalize_audio_loudness_np(
            samples, sr, target_lufs=float(target_lufs)
        ).tolist()
    except ImportError:
        pass

    vals = [float(x) if math.isfinite(float(x)) else 0.0 for x in samples]
    sum_sq = sum(v * v for v in vals)
    rms = math.sqrt(sum_sq / len(vals))
    if rms < _SILENCE_FLOOR_RMS:
        return vals

    pyln_out = _try_pyloudnorm(vals, sr, float(target_lufs))
    if pyln_out is not None:
        return pyln_out

    target_rms = 10.0 ** (float(target_lufs) / 20.0)
    max_gain = 10.0 ** (40.0 / 20.0)
    min_gain = 10.0 ** (-40.0 / 20.0)
    gain = max(min_gain, min(max_gain, target_rms / rms))
    return _soft_limit_samples([v * gain for v in vals])


def _apply_edge_fade_np(arr: Any, sample_rate: int, fade_ms: float = 3.0) -> Any:
    """Apply a subtle symmetric fade-in/fade-out at trimmed segment boundaries to eliminate clicks."""
    import numpy as np  # type: ignore[import-not-found]

    n = int(arr.size)
    if n < 2 or sample_rate <= 0 or fade_ms <= 0.0:
        return arr
    fade_len = min(n // 2, int(round(sample_rate * fade_ms / 1000.0)))
    if fade_len <= 1:
        return arr
    out = arr.copy()
    ramp = np.linspace(0.0, 1.0, fade_len, endpoint=False, dtype=np.float64)
    out[:fade_len] *= ramp
    out[-fade_len:] *= ramp[::-1]
    return out


def _apply_edge_fade(
    samples: Sequence[float], sample_rate: int, fade_ms: float = 3.0
) -> list[float]:
    """Pure-Python symmetric fade-in/fade-out at trimmed segment boundaries."""
    out = [float(x) for x in samples]
    n = len(out)
    if n < 2 or sample_rate <= 0 or fade_ms <= 0.0:
        return out
    fade_len = min(n // 2, int(round(sample_rate * fade_ms / 1000.0)))
    if fade_len <= 1:
        return out
    inv = 1.0 / fade_len
    for i in range(fade_len):
        gain = i * inv
        out[i] *= gain
        out[n - 1 - i] *= gain
    return out


def assemble_colabone_tts_master(
    segment_wavs: list[tuple[str, Path]],
    out_wav: Path,
    manifest_path: Path,
    pause_seconds: float = 0.28,
    target_sr: int = 48000,
    target_lufs: float = -23.0,
) -> dict[str, Any]:
    """Trim, resample, stitch, normalize, and write master WAV + timeline manifest.json."""
    if not segment_wavs:
        raise ValueError("没有可拼接的语音片段")
    sr = int(target_sr)
    if sr <= 0:
        raise ValueError(f"目标采样率必须大于 0：{target_sr}")
    pause_sec = max(0.0, float(pause_seconds))
    pause_sample_count = int(round(pause_sec * sr))

    segments_meta: list[dict[str, Any]] = []
    silence_valleys: list[float] = []
    total_segments = len(segment_wavs)
    out_wav_path = Path(out_wav)
    manifest_file = Path(manifest_path)

    try:
        import numpy as np  # type: ignore[import-not-found]

        use_numpy = True
    except ImportError:
        np = None  # type: ignore[assignment]
        use_numpy = False

    if use_numpy:
        chunks_np: list[Any] = []
        pause_arr = (
            np.zeros(pause_sample_count, dtype=np.float64)
            if pause_sample_count > 0
            else None
        )
        cursor = 0
        for idx, (seg_text, wav_path) in enumerate(segment_wavs):
            raw_samples, src_sr = read_wav_mono_float(Path(wav_path))
            raw_arr = np.asarray(raw_samples, dtype=np.float64).reshape(-1)
            trimmed_arr = _trim_wav_silence_np(raw_arr, src_sr)
            if trimmed_arr.size == 0 and raw_arr.size > 0:
                trimmed_arr = raw_arr
            resampled_arr = _resample_audio_np(trimmed_arr, src_sr, sr)
            resampled_arr = _apply_edge_fade_np(resampled_arr, sr, fade_ms=3.0)

            start_sample = cursor
            if resampled_arr.size > 0:
                chunks_np.append(resampled_arr)
                cursor += int(resampled_arr.size)
            end_sample = cursor

            start_sec = round(start_sample / sr, 4)
            end_sec = round(end_sample / sr, 4)
            pause_midpoint: float | None = None

            if idx < total_segments - 1 and pause_sec > 0.0 and pause_sample_count > 0:
                if pause_arr is not None:
                    chunks_np.append(pause_arr)
                    cursor += pause_sample_count
                pause_midpoint = round(
                    (end_sample + pause_sample_count / 2.0) / sr, 4
                )
                silence_valleys.append(pause_midpoint)

            segments_meta.append(
                {
                    "index": idx,
                    "text": str(seg_text),
                    "start": start_sec,
                    "end": end_sec,
                    "pause_midpoint": pause_midpoint,
                }
            )

        master_arr = (
            np.concatenate(chunks_np)
            if chunks_np
            else np.empty(0, dtype=np.float64)
        )
        normalized_arr = _normalize_audio_loudness_np(
            master_arr, sr, target_lufs=float(target_lufs)
        )
        write_wav_pcm16(out_wav_path, normalized_arr, sr)
        sample_count = int(normalized_arr.size)
    else:
        pause_zeros = [0.0] * pause_sample_count
        master_samples: list[float] = []

        for idx, (seg_text, wav_path) in enumerate(segment_wavs):
            raw_samples, src_sr = read_wav_mono_float(Path(wav_path))
            trimmed = trim_wav_silence(raw_samples, src_sr)
            if not trimmed and raw_samples:
                trimmed = raw_samples
            resampled = resample_audio_linear(trimmed, src_sr, sr)
            resampled = _apply_edge_fade(resampled, sr, fade_ms=3.0)

            start_sample = len(master_samples)
            master_samples.extend(resampled)
            end_sample = len(master_samples)

            start_sec = round(start_sample / sr, 4)
            end_sec = round(end_sample / sr, 4)
            pause_midpoint: float | None = None

            if idx < total_segments - 1 and pause_sec > 0.0 and pause_sample_count > 0:
                master_samples.extend(pause_zeros)
                pause_midpoint = round(
                    (end_sample + pause_sample_count / 2.0) / sr, 4
                )
                silence_valleys.append(pause_midpoint)

            segments_meta.append(
                {
                    "index": idx,
                    "text": str(seg_text),
                    "start": start_sec,
                    "end": end_sec,
                    "pause_midpoint": pause_midpoint,
                }
            )

        normalized = normalize_audio_loudness(
            master_samples, sr, target_lufs=float(target_lufs)
        )
        write_wav_pcm16(out_wav_path, normalized, sr)
        sample_count = len(normalized)

    duration = round(sample_count / sr, 4)
    manifest: dict[str, Any] = {
        "sample_rate": sr,
        "pause_seconds": round(pause_sec, 4),
        "target_lufs": float(target_lufs),
        "duration": duration,
        "segments": segments_meta,
        "silence_valleys": silence_valleys,
    }
    atomic_json(manifest_file, manifest)
    return manifest


# ---------------------------------------------------------------------------
# 3. Subprocess & package installation helpers
# ---------------------------------------------------------------------------


_PEP508_IDENT = r"[A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?"
_PEP508_VERSION_CLAUSE = (
    r"(?:===|==|!=|<=|>=|~=|<|>)\s*[A-Za-z0-9][A-Za-z0-9.*_+!-]*"
)
_PEP508_VERSION_LIST = (
    rf"{_PEP508_VERSION_CLAUSE}(?:\s*,\s*{_PEP508_VERSION_CLAUSE})*"
)
_PEP508_NAME_REQ_RE = re.compile(
    rf"^(?P<name>{_PEP508_IDENT})"
    rf"(?:\s*\[\s*{_PEP508_IDENT}(?:\s*,\s*{_PEP508_IDENT})*\s*\])?"
    rf"(?:\s*(?:\(\s*{_PEP508_VERSION_LIST}\s*\)|{_PEP508_VERSION_LIST}))?$"
)
_PEP508_MARKER_VARS = frozenset(
    {
        "python_version",
        "python_full_version",
        "os_name",
        "sys_platform",
        "platform_release",
        "platform_system",
        "platform_version",
        "platform_machine",
        "platform_python_implementation",
        "implementation_name",
        "implementation_version",
        "extra",
    }
)
_PEP508_MARKER_TOKEN_RE = re.compile(
    r"""\s*(?:
        (?P<str>'[^'\r\n\x00]*'|"[^"\r\n\x00]*")
        |(?P<op>===|==|!=|<=|>=|~=|<=|<|>)
        |(?P<paren>[()])
        |(?P<word>[A-Za-z_][A-Za-z0-9_]*)
    )""",
    re.VERBOSE,
)
_DIRECT_URL_OR_VCS_RE = re.compile(
    r"(?:://|\b(?:git|hg|svn|bzr)\+|\bfile:)",
    re.IGNORECASE,
)
_CLI_FLAG_TOKEN_RE = re.compile(r"(?:^|\s)(?:--|-[A-Za-z])")


def _normalize_pkg_name(name: str) -> str:
    return re.sub(r"[-_.]+", "-", name.strip()).lower()


def _req_package_name(spec: str) -> str:
    raw = re.sub(r"\s+#.*$", "", str(spec)).strip()
    if not raw or raw.startswith("-"):
        return ""
    match = re.match(r"^([A-Za-z0-9_.-]+)", raw)
    if not match:
        return ""
    return _normalize_pkg_name(match.group(1))


def _validate_pep508_marker(marker: str) -> bool:
    s = marker.strip()
    if not s:
        return False
    if _DIRECT_URL_OR_VCS_RE.search(s):
        return False
    pos = 0
    depth = 0
    n = len(s)
    while pos < n:
        m = _PEP508_MARKER_TOKEN_RE.match(s, pos)
        if not m or m.end() == pos:
            return False
        pos = m.end()
        if m.group("paren"):
            depth += 1 if m.group("paren") == "(" else -1
            if depth < 0:
                return False
        elif m.group("word"):
            w = m.group("word")
            if w not in _PEP508_MARKER_VARS and w not in ("and", "or", "in", "not"):
                return False
    return depth == 0


def _sanitize_requirement_spec(raw: str) -> str:
    """Strictly validate a PEP 508 named package requirement; reject flags, newlines, and URLs."""
    raw_str = str(raw or "")
    if "\r" in raw_str or "\n" in raw_str or "\x00" in raw_str:
        return ""
    spec = raw_str.lstrip("\ufeff").strip()
    if not spec or spec.startswith("#"):
        return ""
    spec = re.sub(r"\s+#.*$", "", spec).strip()
    if not spec or spec.startswith("-"):
        return ""
    if _DIRECT_URL_OR_VCS_RE.search(spec):
        return ""

    if ";" in spec:
        req_part, marker_part = spec.split(";", 1)
        req_part = req_part.strip()
        marker_part = marker_part.strip()
        if not req_part or not _validate_pep508_marker(marker_part):
            return ""
    else:
        req_part = spec
        marker_part = ""

    if _CLI_FLAG_TOKEN_RE.search(req_part):
        return ""
    match = _PEP508_NAME_REQ_RE.fullmatch(req_part)
    if not match:
        return ""
    pkg_name = _normalize_pkg_name(match.group("name"))
    if pkg_name in _PIP_SKIP_PACKAGES:
        return ""
    return f"{req_part}; {marker_part}" if marker_part else req_part


def _join_requirement_lines(text: str) -> list[str]:
    joined: list[str] = []
    buf = ""
    for raw_line in text.splitlines():
        line = raw_line.rstrip()
        if line.endswith("\\"):
            buf += line[:-1] + " "
            continue
        if buf:
            joined.append(buf + line)
            buf = ""
        else:
            joined.append(line)
    if buf:
        joined.append(buf)
    return joined


def _filter_pip_packages(packages: Sequence[str]) -> list[str]:
    kept: list[str] = []
    for raw in packages:
        cleaned = _sanitize_requirement_spec(raw)
        if cleaned:
            kept.append(cleaned)
    return kept


def _normalize_pep508_marker(marker: str) -> str:
    """Normalize whitespace and quote style in a PEP 508 environment marker."""
    s = marker.strip()
    if not s:
        return ""
    tokens: list[str] = []
    pos = 0
    n = len(s)
    while pos < n:
        m = _PEP508_MARKER_TOKEN_RE.match(s, pos)
        if not m or m.end() == pos:
            return " ".join(s.replace("'", '"').split())
        pos = m.end()
        if m.group("str"):
            raw_s = m.group("str")
            tokens.append(f'"{raw_s[1:-1]}"')
        elif m.group("op"):
            tokens.append(m.group("op"))
        elif m.group("paren"):
            tokens.append(m.group("paren"))
        elif m.group("word"):
            tokens.append(m.group("word"))
    return " ".join(tokens)


def _parse_requirement_parts(
    spec: str,
) -> tuple[str, str, list[str], str, str, str, str]:
    """Return (cleaned_spec, raw_name, pkg_name, extras, version_part, raw_marker, norm_marker)."""
    cleaned = re.sub(r"\s+#.*$", "", str(spec or "")).strip()
    if not cleaned or cleaned.startswith("-") or cleaned.startswith("#"):
        return "", "", "", [], "", "", ""
    if ";" in cleaned:
        req_part, marker_part = cleaned.split(";", 1)
        req_part = req_part.strip()
        marker_part = marker_part.strip()
    else:
        req_part = cleaned
        marker_part = ""
    match = re.match(r"^([A-Za-z0-9_.-]+)", req_part)
    if not match:
        return "", "", "", [], "", "", ""
    raw_name = match.group(1)
    pkg_name = _normalize_pkg_name(raw_name)
    remainder = req_part[len(raw_name) :].strip()
    extras: list[str] = []
    if remainder.startswith("[") and "]" in remainder:
        extras_raw, remainder = remainder[1:].split("]", 1)
        for item in extras_raw.split(","):
            ex = _normalize_pkg_name(item)
            if ex and ex not in extras:
                extras.append(ex)
        remainder = remainder.strip()
    version_part = remainder
    norm_marker = _normalize_pep508_marker(marker_part)
    return cleaned, raw_name, pkg_name, extras, version_part, marker_part, norm_marker


def _format_requirement_spec(
    raw_name: str, extras: Sequence[str], version_part: str, marker_part: str
) -> str:
    out = raw_name
    if extras:
        out += "[" + ",".join(extras) + "]"
    if version_part:
        out += version_part
    if marker_part:
        out += f"; {marker_part}"
    return out


def _split_requirement_meta(spec: str) -> tuple[str, str, bool, bool]:
    """Return (normalized_pkg_name, norm_marker, has_version_spec, has_extras)."""
    _, _, pkg_name, extras, version_part, _, norm_marker = _parse_requirement_parts(spec)
    return pkg_name, norm_marker, bool(version_part), bool(extras)


def _merge_requirement_specs(
    primary: Sequence[str], secondary: Sequence[str] = ()
) -> list[str]:
    """Merge PEP 508 requirement lists without duplicate PEP-503 distribution names.

    When both ``primary`` (catalog pins) and ``secondary`` (upstream requirements /
    pyproject) specify the same package & normalized marker:
    - Strip inline ``# ...`` comments and normalize marker whitespace/quotes.
    - Union ``[extras]`` across both specs.
    - Keep ``primary`` version constraints when present; upgrade unpinned slots
      (including extras-only primary specs) cleanly to ``secondary`` version constraints.
    - Never emit two entries with the same ``(normalized_name, norm_marker)``.
    """
    merged: list[str] = []
    # key -> (index, raw_name, extras, version_part, raw_marker)
    index_by_key: dict[tuple[str, str], tuple[int, str, list[str], str, str]] = {}

    def _add(spec: str) -> None:
        cleaned, raw_name, pkg_name, extras, version_part, raw_marker, norm_marker = (
            _parse_requirement_parts(spec)
        )
        if not pkg_name:
            return
        key = (pkg_name, norm_marker)
        existing = index_by_key.get(key)
        if existing is None:
            index_by_key[key] = (len(merged), raw_name, list(extras), version_part, raw_marker)
            merged.append(cleaned)
            return

        idx, ex_raw_name, ex_extras, ex_version, ex_marker = existing
        union_extras = list(ex_extras)
        for ex in extras:
            if ex not in union_extras:
                union_extras.append(ex)

        if not ex_version and not ex_extras and (version_part or extras):
            merged[idx] = cleaned
            index_by_key[key] = (
                idx,
                raw_name,
                union_extras,
                version_part,
                raw_marker or ex_marker,
            )
            return

        chosen_version = ex_version or version_part
        if union_extras != ex_extras or chosen_version != ex_version:
            chosen_marker = ex_marker or raw_marker
            merged[idx] = _format_requirement_spec(
                ex_raw_name, union_extras, chosen_version, chosen_marker
            )
            index_by_key[key] = (
                idx,
                ex_raw_name,
                union_extras,
                chosen_version,
                chosen_marker,
            )

    for spec in primary:
        _add(spec)
    for spec in secondary:
        _add(spec)
    return merged


def _extract_requirements_file_packages(req_path: Path) -> list[str]:
    text = req_path.read_text(encoding="utf-8-sig")
    return _filter_pip_packages(_join_requirement_lines(text))


def _strip_toml_header_comment(line: str) -> str:
    in_quote: str | None = None
    escaped = False
    for idx, ch in enumerate(line):
        if in_quote is not None:
            if in_quote == '"' and ch == "\\" and not escaped:
                escaped = True
                continue
            if ch == in_quote and not escaped:
                in_quote = None
            escaped = False
        else:
            if ch in ('"', "'"):
                in_quote = ch
            elif ch == "#":
                return line[:idx].strip()
    return line.strip()


def _update_toml_multiline_state(line: str, ml_quote: str | None) -> str | None:
    """Track whether TOML scanning is currently inside a triple-quoted multiline string."""
    i = 0
    n = len(line)
    in_single: str | None = None
    escaped = False
    while i < n:
        ch = line[i]
        if ml_quote is not None:
            if ml_quote == '"""':
                if ch == "\\" and not escaped:
                    escaped = True
                    i += 1
                    continue
                if not escaped and line[i : i + 3] == '"""':
                    ml_quote = None
                    i += 3
                    continue
                escaped = False
            else:
                if line[i : i + 3] == "'''":
                    ml_quote = None
                    i += 3
                    continue
            i += 1
            continue

        if in_single is not None:
            if in_single == '"' and ch == "\\" and not escaped:
                escaped = True
                i += 1
                continue
            if ch == in_single and not escaped:
                in_single = None
            escaped = False
            i += 1
            continue

        if ch == "#":
            break
        if line[i : i + 3] in ('"""', "'''"):
            ml_quote = line[i : i + 3]
            escaped = False
            i += 3
            continue
        if ch in ('"', "'"):
            in_single = ch
            escaped = False
            i += 1
            continue
        i += 1
    return ml_quote


def _parse_pyproject_dependencies_fallback(raw_text: str) -> list[str]:
    """Safe zero-dependency fallback parser for [project].dependencies on Python 3.10."""
    in_project = False
    collecting = False
    ml_quote: str | None = None
    depth = 0
    quote: str | None = None
    escaped = False
    current_token: list[str] = []
    deps: list[str] = []

    def _consume_chunk(chunk: str) -> bool:
        nonlocal depth, quote, escaped, current_token
        i = 0
        n = len(chunk)
        while i < n:
            ch = chunk[i]
            if quote is not None:
                if quote in ('"""', "'''"):
                    if chunk[i : i + 3] == quote and not escaped:
                        deps.append("".join(current_token))
                        current_token = []
                        quote = None
                        i += 3
                        continue
                    if quote == '"""' and ch == "\\" and not escaped:
                        escaped = True
                        i += 1
                        continue
                    if escaped:
                        escape_map = {"n": "\n", "t": "\t", "r": "\r", '"': '"', "\\": "\\"}
                        current_token.append(escape_map.get(ch, ch))
                        escaped = False
                    else:
                        current_token.append(ch)
                    i += 1
                    continue
                if quote == '"' and ch == "\\" and not escaped:
                    escaped = True
                    i += 1
                    continue
                if ch == quote and not escaped:
                    deps.append("".join(current_token))
                    current_token = []
                    quote = None
                    i += 1
                    continue
                if escaped:
                    escape_map = {"n": "\n", "t": "\t", "r": "\r", '"': '"', "\\": "\\"}
                    current_token.append(escape_map.get(ch, ch))
                    escaped = False
                else:
                    current_token.append(ch)
                i += 1
                continue

            if ch == "#":
                break
            if ch == "[":
                depth += 1
                i += 1
                continue
            if ch == "]":
                depth -= 1
                i += 1
                if depth <= 0:
                    return True
                continue
            if ch in ('"', "'"):
                if chunk[i : i + 3] == ch * 3:
                    quote = ch * 3
                    current_token = []
                    escaped = False
                    i += 3
                    continue
                quote = ch
                current_token = []
                escaped = False
                i += 1
                continue
            i += 1
        return False

    for raw_line in raw_text.splitlines():
        if not collecting:
            if ml_quote is not None:
                ml_quote = _update_toml_multiline_state(raw_line, ml_quote)
                continue
            stripped = raw_line.strip()
            if not stripped or stripped.startswith("#"):
                continue
            if stripped.startswith("["):
                header = _strip_toml_header_comment(stripped)
                in_project = header == "[project]"
                continue
            if in_project:
                match = re.match(r"^dependencies\s*=\s*(.*)$", stripped)
                if match:
                    rhs = match.group(1)
                    bracket_pos = rhs.find("[")
                    if bracket_pos >= 0:
                        collecting = True
                        depth = 0
                        if _consume_chunk(rhs[bracket_pos:]):
                            break
                        continue
            ml_quote = _update_toml_multiline_state(raw_line, ml_quote)
        else:
            if _consume_chunk(raw_line):
                break
    return deps


def _extract_pyproject_dependencies(pyproject_path: Path) -> list[str]:
    """Extract and filter [project].dependencies from pyproject.toml."""
    raw_text = pyproject_path.read_text(encoding="utf-8-sig")
    raw_deps: list[str] = []
    if tomllib is not None:
        try:
            parsed = tomllib.loads(raw_text)
            project = parsed.get("project") if isinstance(parsed, dict) else None
            deps = project.get("dependencies") if isinstance(project, dict) else None
            if isinstance(deps, list):
                raw_deps = [str(item) for item in deps if isinstance(item, str)]
        except Exception:
            raw_deps = _parse_pyproject_dependencies_fallback(raw_text)
    else:
        raw_deps = _parse_pyproject_dependencies_fallback(raw_text)
    return _filter_pip_packages(raw_deps)


def _collect_installed_torch_constraints() -> list[str]:
    lines: list[str] = []
    for pkg in _TORCH_CONSTRAINT_PACKAGES:
        try:
            ver = importlib.metadata.version(pkg)
        except importlib.metadata.PackageNotFoundError:
            continue
        except Exception:
            continue
        ver_str = str(ver or "").strip()
        if ver_str:
            lines.append(f"{pkg}=={ver_str}")
    return lines


def _write_torch_constraints_file(
    out_path: Path, constraints: Sequence[str] | None = None
) -> Path:
    if out_path.is_symlink():
        raise ValueError("PyTorch 约束文件不能是符号链接")
    lines = (
        list(constraints)
        if constraints is not None
        else _collect_installed_torch_constraints()
    )
    out_path.parent.mkdir(parents=True, exist_ok=True)
    body = "# ColabOne pinned PyTorch stack constraints\n"
    if lines:
        body += "\n".join(lines) + "\n"
    _atomic_write_text(out_path, body)
    return out_path


def _find_venv_python_on_disk(venv_dir: Path) -> Path | None:
    """Return the venv Python executable path if it exists on disk, else None."""
    for candidate in (
        venv_dir / "bin" / "python",
        venv_dir / "bin" / "python3",
        venv_dir / "Scripts" / "python.exe",
    ):
        if candidate.is_file():
            return candidate
    return None


def _resolve_venv_python(venv_dir: Path) -> Path:
    """Resolve an isolated per-engine venv Python executable, falling back to sys.executable."""
    found = _find_venv_python_on_disk(venv_dir)
    if found is not None:
        return found
    return Path(sys.executable)


def _is_standard_mono_pcm16_wav(path: Path) -> bool:
    """Return True if path is a standard uncompressed mono 16-bit PCM WAV."""
    try:
        with wave.open(str(path), "rb") as wf:
            return (
                wf.getcomptype() == "NONE"
                and wf.getnchannels() == 1
                and wf.getsampwidth() == 2
                and wf.getframerate() > 0
                and wf.getnframes() > 0
            )
    except (wave.Error, EOFError, OSError):
        return False


_is_standard_pcm_wav = _is_standard_mono_pcm16_wav


def _normalize_git_repo_url(repo: str) -> str:
    cleaned = str(repo or "").strip()
    if _REPO_SLUG_RE.fullmatch(cleaned):
        owner, name = cleaned.split("/", 1)
        if not _GITHUB_SEGMENT_RE.fullmatch(owner) or not _GITHUB_SEGMENT_RE.fullmatch(name):
            raise ValueError(f"非法的 GitHub 仓库地址：{repo!r}")
        return f"https://github.com/{owner}/{name}.git"
    match = _GITHUB_REPO_URL_RE.fullmatch(cleaned)
    if not match:
        raise ValueError(
            f"TTS 引擎源码仓库仅允许使用 https://github.com/<owner>/<repo> 地址：{repo!r}"
        )
    owner, name = match.group("owner"), match.group("repo")
    return f"https://github.com/{owner}/{name}.git"


def _scrub_secret(text: str, secret: str) -> str:
    if not text:
        return text
    out = str(text)
    if secret:
        raw_secret = str(secret)
        stripped = raw_secret.strip()
        candidates: list[str] = []
        for candidate in (
            raw_secret,
            repr(raw_secret)[1:-1],
            stripped,
            repr(stripped)[1:-1] if stripped else "",
        ):
            if candidate and candidate not in candidates:
                candidates.append(candidate)
        candidates.sort(key=len, reverse=True)
        for candidate in candidates:
            out = out.replace(candidate, "***")
    out = _BEARER_TOKEN_RE.sub("Bearer ***", out)
    out = _HF_TOKEN_RE.sub("***", out)
    return out


def _pgid_alive(pgid: int) -> bool:
    if not hasattr(os, "killpg"):
        return False
    try:
        os.killpg(pgid, 0)
        return True
    except ProcessLookupError:
        return False
    except PermissionError:
        return True
    except OSError:
        return False


def _terminate_process_group(proc: subprocess.Popen, timeout: float = 5.0) -> None:
    """Terminate the entire process group (SIGTERM -> wait -> SIGKILL), even after leader exit."""
    pid = getattr(proc, "pid", None)
    has_killpg = isinstance(pid, int) and pid > 0 and hasattr(os, "killpg")
    if has_killpg:
        try:
            os.killpg(pid, signal.SIGTERM)
        except ProcessLookupError:
            pass
        except (PermissionError, OSError):
            if proc.poll() is None:
                try:
                    proc.terminate()
                except OSError:
                    pass
    elif proc.poll() is None:
        try:
            proc.terminate()
        except OSError:
            pass

    deadline = time.monotonic() + max(0.0, float(timeout))
    if proc.poll() is None:
        remaining = max(0.0, deadline - time.monotonic())
        try:
            proc.wait(timeout=remaining)
        except subprocess.TimeoutExpired:
            pass

    if has_killpg:
        # Even if the leader has already exited, orphaned child processes may remain in the group.
        # Poll inside the loop to reap a zombie leader immediately so _pgid_alive clears without spinning.
        while True:
            proc.poll()
            if not _pgid_alive(pid) or time.monotonic() >= deadline:
                break
            time.sleep(0.05)
        kill_deadline = time.monotonic() + min(1.0, max(0.15, float(timeout)))
        while True:
            proc.poll()
            if not _pgid_alive(pid) or time.monotonic() >= kill_deadline:
                break
            try:
                os.killpg(pid, signal.SIGKILL)
            except (ProcessLookupError, PermissionError, OSError):
                break
            time.sleep(0.05)
        proc.poll()

    if proc.poll() is None:
        try:
            proc.kill()
        except OSError:
            pass
        try:
            proc.wait(timeout=max(0.5, float(timeout)))
        except subprocess.TimeoutExpired:
            pass


def _read_log_tail(log_path: Path, max_bytes: int = 4096) -> str:
    """Read only the tail of a worker log file by seeking near EOF without splitting UTF-8 codepoints."""
    try:
        with log_path.open("rb") as fh:
            fh.seek(0, os.SEEK_END)
            size = fh.tell()
            truncated = size > max_bytes
            fh.seek(max(0, size - max_bytes), os.SEEK_SET)
            raw = fh.read()
        if truncated and raw:
            idx = 0
            n = len(raw)
            while idx < n and (raw[idx] & 0xC0) == 0x80:
                idx += 1
            if idx:
                raw = raw[idx:]
        return raw.decode("utf-8", errors="replace")
    except OSError:
        return ""


_PYTORCH_ONLY_WORKER_ENV: dict[str, str] = {
    "USE_TORCH": "1",
    "USE_TF": "0",
    "USE_FLAX": "0",
    "USE_JAX": "0",
    "TRANSFORMERS_NO_TF": "1",
    "TRANSFORMERS_NO_FLAX": "1",
    "TRANSFORMERS_NO_JAX": "1",
    "DISABLE_TENSORFLOW": "1",
    "TF_CPP_MIN_LOG_LEVEL": "3",
}


def _sanitized_tts_env(extra: dict[str, str] | None = None) -> dict[str, str]:
    blocked = _TTS_SENSITIVE_ENV_KEYS | _TTS_SETUP_STRIP_ENV_KEYS
    env = {k: v for k, v in os.environ.items() if k not in blocked}
    if extra:
        env.update(extra)
    return env


def _build_tts_worker_env(hf_cache_dir: str, hf_token: str = "") -> dict[str, str]:
    """Build a sanitized PyTorch-only worker environment blocking Colab TF/JAX leaks."""
    extra = {
        **_PYTORCH_ONLY_WORKER_ENV,
        "PYTHONUNBUFFERED": "1",
        "HF_HUB_CACHE": hf_cache_dir,
        "HUGGINGFACE_HUB_CACHE": hf_cache_dir,
    }
    worker_env = _sanitized_tts_env(extra)
    token = str(hf_token or "").strip()
    if token:
        worker_env["HF_TOKEN"] = token
        worker_env["HUGGING_FACE_HUB_TOKEN"] = token
    return worker_env


def _summarize_subprocess_error_tail(
    lines: Sequence[str],
    fallback_text: str = "",
    *,
    max_chars: int = 800,
) -> str:
    """Keep complete trailing lines (always including the final exception line) within max_chars."""
    cleaned = [str(line).strip() for line in lines if str(line).strip()]
    if not cleaned and fallback_text:
        cleaned = [ln.strip() for ln in str(fallback_text).splitlines() if ln.strip()]
    if not cleaned:
        return ""
    budget = max(80, int(max_chars))
    kept: list[str] = []
    used = 0
    for line in reversed(cleaned):
        sep = 1 if kept else 0
        if kept and used + sep + len(line) > budget:
            break
        if not kept and len(line) > budget:
            return line[-budget:]
        kept.append(line)
        used += sep + len(line)
    kept.reverse()
    return "\n".join(kept)


def _cancelled_error_cls() -> type[BaseException]:
    try:
        from server.runtime import ColabOneCancelledError

        return ColabOneCancelledError
    except ImportError:
        return RuntimeError


def _is_cancelled_error(exc: BaseException) -> bool:
    cls = _cancelled_error_cls()
    if cls is not RuntimeError and isinstance(exc, cls):
        return True
    return type(exc).__name__ == "ColabOneCancelledError"


def _check_tts_cancel(task: Any, stopping: threading.Event | None = None) -> None:
    check_fn = getattr(task, "check", None)
    if callable(check_fn):
        check_fn()
    if isinstance(stopping, threading.Event) and stopping.is_set():
        raise _cancelled_error_cls()("实例正在停止，已取消远端 TTS 任务")


def _collect_parent_venv_site_packages() -> list[str]:
    """Return site-packages directories of the host virtualenv when sys.prefix != sys.base_prefix."""
    try:
        prefix = Path(sys.prefix).resolve()
        base_prefix = Path(sys.base_prefix).resolve()
    except OSError:
        return []
    if prefix == base_prefix:
        return []

    base_site_set: set[Path] = set()
    try:
        for p in site.getsitepackages([sys.base_prefix]):
            try:
                base_site_set.add(Path(p).resolve())
            except OSError:
                continue
    except Exception:
        pass

    raw_dirs: list[str] = []
    try:
        raw_dirs.extend(site.getsitepackages([sys.prefix]))
    except Exception:
        pass
    try:
        paths = sysconfig.get_paths()
        for key in ("purelib", "platlib"):
            val = paths.get(key)
            if val:
                raw_dirs.append(val)
    except Exception:
        pass

    candidates: list[str] = []
    seen: set[Path] = set()
    for raw in raw_dirs:
        if not raw or any(ch in raw for ch in ("\r", "\n", "\x00")):
            continue
        p = Path(raw)
        try:
            if p.is_symlink() or not p.is_dir():
                continue
            rp = p.resolve()
        except OSError:
            continue
        if rp in seen or rp in base_site_set:
            continue
        if not rp.is_relative_to(prefix):
            continue
        seen.add(rp)
        candidates.append(str(rp))
    return candidates


def _find_venv_site_packages_dir(venv_dir: Path) -> Path | None:
    """Locate the site-packages directory inside `venv_dir`, if present on disk."""
    candidates: list[Path] = [
        venv_dir
        / "lib"
        / f"python{sys.version_info.major}.{sys.version_info.minor}"
        / "site-packages",
        venv_dir
        / "lib64"
        / f"python{sys.version_info.major}.{sys.version_info.minor}"
        / "site-packages",
        venv_dir / "Lib" / "site-packages",
    ]
    lib_dir = venv_dir / "lib"
    if lib_dir.is_dir() and not lib_dir.is_symlink():
        try:
            for child in sorted(lib_dir.iterdir()):
                if (
                    child.name.startswith("python")
                    and child.is_dir()
                    and not child.is_symlink()
                ):
                    candidates.append(child / "site-packages")
        except OSError:
            pass
    for cand in candidates:
        try:
            if not cand.is_symlink() and cand.is_dir():
                return cand
        except OSError:
            continue
    return None


def _ensure_parent_venv_pth(venv_dir: Path) -> Path | None:
    """Write a .pth file into child venv site-packages so parent-venv PyTorch stays visible."""
    parent_sites = _collect_parent_venv_site_packages()
    if not parent_sites:
        return None
    child_site = _find_venv_site_packages_dir(venv_dir)
    if child_site is None:
        return None
    pth_path = child_site / "_colabone_parent_venv.pth"
    if pth_path.is_symlink():
        raise ValueError("TTS 虚拟环境继承路径文件不能是符号链接")
    content = "\n".join(parent_sites) + "\n"
    try:
        if pth_path.is_file() and pth_path.read_text(encoding="utf-8") == content:
            return pth_path
    except OSError:
        pass
    _atomic_write_text(pth_path, content)
    return pth_path


def _open_secure_tts_log_file(
    log_path: Path,
    *,
    append: bool = False,
    max_bytes: int = _MAX_TTS_LOG_FILE_BYTES,
) -> Any:
    """Open `log_path` with 0o600 permissions (no symlinks), truncating if > max_bytes."""
    if log_path.parent.is_symlink():
        raise ValueError("TTS 日志目录不能是符号链接")
    if log_path.is_symlink():
        raise ValueError("TTS 日志文件路径不能是符号链接")
    log_path.parent.mkdir(parents=True, exist_ok=True)

    truncate = not append
    if append:
        try:
            st = log_path.lstat()
            if stat.S_ISLNK(st.st_mode):
                raise ValueError("TTS 日志文件路径不能是符号链接")
            if st.st_size > max_bytes:
                truncate = True
        except FileNotFoundError:
            truncate = True

    flags = os.O_WRONLY | os.O_CREAT | (os.O_TRUNC if truncate else os.O_APPEND)
    if hasattr(os, "O_NOFOLLOW"):
        flags |= os.O_NOFOLLOW
    fd = os.open(str(log_path), flags, 0o600)
    try:
        try:
            os.fchmod(fd, 0o600)
        except (AttributeError, OSError):
            try:
                os.chmod(log_path, 0o600)
            except OSError:
                pass
        return os.fdopen(fd, "wb")
    except BaseException:
        try:
            os.close(fd)
        except OSError:
            pass
        raise


def _iter_tts_pipe_lines(
    stream: Any, *, max_line_bytes: int = _MAX_TTS_STREAM_LINE_BYTES
) -> Iterator[str]:
    """Yield decoded text lines split on CR or LF with a hard per-line byte cap."""
    buf = bytearray()
    pending_lf = False
    while True:
        chunk = stream.read(4096)
        if not chunk:
            break
        if pending_lf:
            pending_lf = False
            if chunk.startswith(b"\n"):
                chunk = chunk[1:]
                if not chunk:
                    continue
        buf.extend(chunk)
        while buf:
            nl_idx = buf.find(b"\n")
            cr_idx = buf.find(b"\r")
            if nl_idx >= 0 and cr_idx >= 0:
                split_idx = min(nl_idx, cr_idx)
            elif nl_idx >= 0:
                split_idx = nl_idx
            elif cr_idx >= 0:
                split_idx = cr_idx
            else:
                split_idx = -1

            if split_idx < 0:
                if len(buf) >= max_line_bytes:
                    raw = bytes(buf[:max_line_bytes])
                    del buf[:max_line_bytes]
                    yield raw.decode("utf-8", errors="replace")
                    continue
                break

            if split_idx > max_line_bytes:
                raw = bytes(buf[:max_line_bytes])
                del buf[:max_line_bytes]
                yield raw.decode("utf-8", errors="replace")
                continue

            raw = bytes(buf[:split_idx])
            delim = buf[split_idx]
            consume_end = split_idx + 1
            if delim == 13:  # b"\r"
                if split_idx + 1 < len(buf):
                    if buf[split_idx + 1] == 10:  # b"\r\n"
                        consume_end = split_idx + 2
                else:
                    pending_lf = True
            del buf[:consume_end]
            yield raw.decode("utf-8", errors="replace")
    if buf:
        yield bytes(buf).decode("utf-8", errors="replace")


def _run_tts_subprocess(
    args: Sequence[str],
    task: Any,
    log: Path,
    *,
    timeout: int = 1800,
    cwd: Path | str | None = None,
    env: dict[str, str] | None = None,
    stopping: threading.Event | None = None,
    secret_to_scrub: str = "",
    append_log: bool = False,
) -> None:
    """Run a cancelable subprocess in its own process group with 0o600 live-scrubbed logs."""
    _check_tts_cancel(task, stopping)
    log_fh = _open_secure_tts_log_file(log, append=append_log)
    try:
        proc = subprocess.Popen(
            list(args),
            cwd=str(cwd) if cwd is not None else None,
            env=env,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            stdin=subprocess.DEVNULL,
            start_new_session=True,
            bufsize=0,
        )
    except BaseException:
        try:
            log_fh.close()
        except OSError:
            pass
        raise

    has_cancel_hook = task is not None and hasattr(task, "cancel_hook")
    task_lock = getattr(task, "lock", None) if has_cancel_hook else None
    prev_cancel_hook = None
    term_lock = threading.Lock()
    pgrp_cleaned = False
    tail_lock = threading.Lock()
    tail_lines: collections.deque[str] = collections.deque(maxlen=80)

    def _cleanup_pgrp(grace: float = 4.0) -> None:
        nonlocal pgrp_cleaned
        with term_lock:
            if not pgrp_cleaned:
                pgrp_cleaned = True
                _terminate_process_group(proc, timeout=grace)

    def _cancel_proc() -> None:
        _cleanup_pgrp(grace=2.0)
        if callable(prev_cancel_hook):
            try:
                prev_cancel_hook()
            except Exception:
                pass

    if has_cancel_hook:
        with task_lock if task_lock is not None else nullcontext():
            prev_cancel_hook = getattr(task, "cancel_hook", None)
            task.cancel_hook = _cancel_proc

    def _reader_loop() -> None:
        try:
            assert proc.stdout is not None
            with log_fh:
                for raw_line in _iter_tts_pipe_lines(proc.stdout):
                    scrubbed = _scrub_secret(raw_line, secret_to_scrub)
                    log_fh.write((scrubbed + "\n").encode("utf-8", errors="replace"))
                    log_fh.flush()
                    stripped = scrubbed.strip()
                    if stripped:
                        with tail_lock:
                            tail_lines.append(stripped)
        except OSError:
            pass
        finally:
            try:
                if proc.stdout is not None:
                    proc.stdout.close()
            except OSError:
                pass

    reader = threading.Thread(
        target=_reader_loop,
        name=f"colabone-tts-cmd-{proc.pid}",
        daemon=True,
    )
    reader.start()

    end = time.monotonic() + max(1, int(timeout))
    noted = 0.0
    last_noted_line = ""
    try:
        while proc.poll() is None:
            _check_tts_cancel(task, stopping)
            if time.monotonic() > end:
                raise RuntimeError("远端 TTS 执行超时，请查看运行日志")
            if time.monotonic() - noted > 5.0:
                noted = time.monotonic()
                with tail_lock:
                    latest = tail_lines[-1] if tail_lines else ""
                if latest and latest != last_noted_line:
                    last_noted_line = latest
                    _safe_task_note(task, latest[:250])
            time.sleep(0.25)

        reader.join(timeout=2.0)
        _check_tts_cancel(task, stopping)
        if proc.returncode:
            with tail_lock:
                captured = list(tail_lines)
            fallback = (
                ""
                if captured
                else _scrub_secret(_read_log_tail(log, 4096), secret_to_scrub)
            )
            tail = _summarize_subprocess_error_tail(captured, fallback, max_chars=800)
            raise RuntimeError(f"远端 TTS 执行失败（退出码 {proc.returncode}）：{tail}")
    finally:
        try:
            _cleanup_pgrp(grace=5.0)
            reader.join(timeout=2.0)
        finally:
            if has_cancel_hook:
                with task_lock if task_lock is not None else nullcontext():
                    if getattr(task, "cancel_hook", None) is _cancel_proc:
                        task.cancel_hook = prev_cancel_hook


def _safe_task_note(task: Any, message: str, level: str = "info") -> None:
    note_fn = getattr(task, "note", None)
    if not callable(note_fn):
        return
    try:
        note_fn(message, level=level)
    except TypeError:
        note_fn(message)


def _safe_task_update(
    task: Any, *, progress: float | None = None, message: str | None = None
) -> None:
    """Thread-safely update task.progress and/or task.message under task.lock when present."""
    if task is None:
        return
    lock = getattr(task, "lock", None)
    if lock is not None:
        with lock:
            if progress is not None and hasattr(task, "progress"):
                task.progress = progress
            if message is not None and hasattr(task, "message"):
                task.message = message
    else:
        if progress is not None and hasattr(task, "progress"):
            task.progress = progress
        if message is not None and hasattr(task, "message"):
            task.message = message


def _call_with_compatible_kwargs(
    fn: Callable[..., Any],
    positional: tuple[Any, ...],
    optional_kwargs: dict[str, Any],
) -> Any:
    """Invoke a user-injected test callback passing only kwargs it accepts."""
    try:
        sig = inspect.signature(fn)
    except (TypeError, ValueError):
        return fn(*positional, **optional_kwargs)

    params = list(sig.parameters.values())
    has_var_kw = any(p.kind == inspect.Parameter.VAR_KEYWORD for p in params)
    has_var_pos = any(p.kind == inspect.Parameter.VAR_POSITIONAL for p in params)

    pos_params = [
        p
        for p in params
        if p.kind
        in (
            inspect.Parameter.POSITIONAL_ONLY,
            inspect.Parameter.POSITIONAL_OR_KEYWORD,
        )
    ]
    used_pos = positional if has_var_pos else positional[: len(pos_params)]
    bound_names = {p.name for p in pos_params[: len(used_pos)]}

    if has_var_kw:
        filtered_kw = {
            k: v for k, v in optional_kwargs.items() if k not in bound_names
        }
        return fn(*used_pos, **filtered_kw)

    accepted_kw = {
        p.name
        for p in params
        if p.kind
        in (
            inspect.Parameter.POSITIONAL_OR_KEYWORD,
            inspect.Parameter.KEYWORD_ONLY,
        )
        and p.name not in bound_names
    }
    filtered_kw = {k: v for k, v in optional_kwargs.items() if k in accepted_kw}
    return fn(*used_pos, **filtered_kw)


# ---------------------------------------------------------------------------
# 4. Self-contained per-engine worker script generators
# ---------------------------------------------------------------------------

_WORKER_COMMON_HEADER = textwrap.dedent(
    """\
    #!/usr/bin/env python3
    from __future__ import annotations

    import argparse
    import array
    import gc
    import inspect
    import io
    import json
    import math
    import os
    import sys
    import wave
    from pathlib import Path


    def _hf_token() -> str | None:
        return (
            os.environ.get("HF_TOKEN")
            or os.environ.get("HUGGING_FACE_HUB_TOKEN")
            or None
        )


    _NON_TORCH_BLOCKED_MODULES = frozenset(
        {
            "tensorflow",
            "tf_keras",
            "keras",
            "jax",
            "jaxlib",
            "flax",
        }
    )


    class _ColabOneNonTorchMetaPathFilter:
        _colabone_non_torch_filter = True

        def __init__(self, inner) -> None:
            self._inner = inner

        def find_spec(self, fullname, path=None, target=None):
            if str(fullname).partition(".")[0] in _NON_TORCH_BLOCKED_MODULES:
                return None
            find_fn = getattr(self._inner, "find_spec", None)
            if callable(find_fn):
                return find_fn(fullname, path, target)
            return None

        def __getattr__(self, name: str):
            return getattr(self._inner, name)


    def _disable_non_torch_backends() -> None:
        \"\"\"Prevent Colab host TensorFlow/JAX/Flax from leaking into PyTorch-only TTS workers.

        Wraps sys.meta_path finders (instead of setting sys.modules[mod] = None) so libraries
        like einops that probe `if 'tensorflow' in sys.modules:` do NOT treat TensorFlow as
        loaded and crash trying to import it.
        \"\"\"
        for _k, _v in (
            ("USE_TORCH", "1"),
            ("USE_TF", "0"),
            ("USE_FLAX", "0"),
            ("USE_JAX", "0"),
            ("TRANSFORMERS_NO_TF", "1"),
            ("TRANSFORMERS_NO_FLAX", "1"),
            ("TRANSFORMERS_NO_JAX", "1"),
            ("DISABLE_TENSORFLOW", "1"),
            ("TF_CPP_MIN_LOG_LEVEL", "3"),
        ):
            os.environ.setdefault(_k, _v)
        for _mod_name in _NON_TORCH_BLOCKED_MODULES:
            if sys.modules.get(_mod_name) is None:
                sys.modules.pop(_mod_name, None)
        sys.meta_path[:] = [
            f
            if getattr(f, "_colabone_non_torch_filter", False)
            else _ColabOneNonTorchMetaPathFilter(f)
            for f in sys.meta_path
        ]


    def _release_warm_state(state: dict) -> None:
        \"\"\"Evict any previously cached model/bundle in `state` before loading a different key.\"\"\"
        if not state:
            return
        state.clear()
        try:
            import gc as _gc

            _gc.collect()
        except Exception:
            pass
        try:
            import torch as _torch

            if getattr(_torch, "cuda", None) is not None and _torch.cuda.is_available():
                _torch.cuda.empty_cache()
        except Exception:
            pass


    def _to_mono_list(audio) -> list[float]:
        if hasattr(audio, "detach"):
            tensor = audio.detach().cpu()
            dtype_name = str(getattr(tensor, "dtype", ""))
            if "bfloat16" in dtype_name or "float16" in dtype_name:
                tensor = tensor.float()
            try:
                audio = tensor.numpy()
            except TypeError:
                audio = tensor.float().numpy()
        try:
            import numpy as np

            arr = np.asarray(audio)
            arr_shape = getattr(arr, "shape", ())
            if getattr(arr, "size", None) == 0 or (arr_shape and 0 in arr_shape):
                return []
            np_int16 = getattr(np, "int16", None)
            np_int32 = getattr(np, "int32", None)
            np_int8 = getattr(np, "int8", None)
            np_uint8 = getattr(np, "uint8", None)
            if np_int16 is not None and arr.dtype == np_int16:
                arr = arr.astype(np.float32) / 32768.0
            elif np_int32 is not None and arr.dtype == np_int32:
                arr = arr.astype(np.float32) / 2147483648.0
            elif np_int8 is not None and arr.dtype == np_int8:
                arr = arr.astype(np.float32) / 128.0
            elif np_uint8 is not None and arr.dtype == np_uint8:
                arr = (arr.astype(np.float32) - 128.0) / 128.0
            else:
                arr = arr.astype(np.float32)
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
            arr = np.nan_to_num(arr, nan=0.0, posinf=1.0, neginf=-1.0)
            return arr.tolist()
        except ImportError:
            def _clean_float(val) -> float:
                f = float(val)
                return f if math.isfinite(f) else 0.0

            def _is_seq(obj) -> bool:
                return isinstance(obj, (list, tuple)) or (
                    hasattr(obj, "__iter__")
                    and not isinstance(obj, (str, bytes, bytearray))
                )

            def _materialize(obj):
                if isinstance(obj, (int, float)):
                    return _clean_float(obj)
                if _is_seq(obj):
                    return [_materialize(x) for x in obj]
                return _clean_float(obj)

            def _flatten(obj, out: list[float]) -> None:
                if isinstance(obj, list):
                    for item in obj:
                        _flatten(item, out)
                else:
                    out.append(float(obj))

            if isinstance(audio, (int, float)):
                return [_clean_float(audio)]
            data = _materialize(audio)
            if not isinstance(data, list):
                return [float(data)]
            if not data:
                return []
            while (
                isinstance(data, list)
                and len(data) == 1
                and isinstance(data[0], list)
                and data[0]
                and isinstance(data[0][0], list)
            ):
                data = data[0]
            if isinstance(data[0], list):
                if any(
                    isinstance(row, list) and any(isinstance(x, list) for x in row)
                    for row in data
                ):
                    flat: list[float] = []
                    _flatten(data, flat)
                    return flat
                if any(not isinstance(row, list) for row in data):
                    flat = []
                    _flatten(data, flat)
                    return flat
                n_rows = len(data)
                row_lens = {len(row) for row in data}
                if len(row_lens) != 1:
                    flat = []
                    _flatten(data, flat)
                    return flat
                n_cols = next(iter(row_lens))
                if n_cols == 0:
                    return []
                if n_rows <= 8 and n_rows < n_cols:
                    return [
                        sum(data[r][c] for r in range(n_rows)) / n_rows
                        for c in range(n_cols)
                    ]
                return [sum(row) / n_cols for row in data]
            return [float(x) for x in data]


    def _write_pcm16_wav(out_path: str, audio, sample_rate: int) -> None:
        target = Path(out_path)
        target.parent.mkdir(parents=True, exist_ok=True)
        try:
            import numpy as np
            import soundfile as sf

            arr = np.asarray(_to_mono_list(audio), dtype=np.float32)
            arr = np.clip(arr, -1.0, 1.0)
            sf.write(str(target), arr, int(sample_rate), subtype="PCM_16")
            return
        except ImportError:
            pass

        mono = _to_mono_list(audio)
        pcm = array.array("h")
        for sample in mono:
            val = float(sample)
            if not math.isfinite(val):
                pcm.append(0)
                continue
            val = max(-1.0, min(1.0, val))
            pcm.append(max(-32768, min(32767, int(round(val * 32767.0)))))
        if sys.byteorder != "little":
            pcm.byteswap()
        with wave.open(str(target), "wb") as wf:
            wf.setnchannels(1)
            wf.setsampwidth(2)
            wf.setframerate(int(sample_rate))
            wf.writeframes(pcm.tobytes())


    def _patch_torchaudio_load_soundfile() -> None:
        \"\"\"Fallback torchaudio.load to soundfile when torchcodec is unavailable on PyTorch >= 2.9.\"\"\"
        try:
            import torchaudio as _ta
        except Exception:
            return
        _orig_load = getattr(_ta, "load", None)
        if not callable(_orig_load) or getattr(_orig_load, "_colabone_sf_load_patched", False):
            return

        def _safe_ta_load(*args, **kwargs):
            orig_uri = args[0] if args else kwargs.get("uri")
            orig_pos = None
            if orig_uri is not None and hasattr(orig_uri, "seek") and hasattr(orig_uri, "tell"):
                try:
                    orig_pos = orig_uri.tell()
                except Exception:
                    orig_pos = None
            try:
                return _orig_load(*args, **kwargs)
            except Exception as orig_exc:
                try:
                    import numpy as _np
                    import soundfile as _sf
                    import torch as _torch

                    uri = args[0] if len(args) > 0 else kwargs.get("uri")
                    if uri is None:
                        raise orig_exc
                    frame_offset = args[1] if len(args) > 1 else kwargs.get("frame_offset", 0)
                    num_frames = args[2] if len(args) > 2 else kwargs.get("num_frames", -1)
                    normalize = args[3] if len(args) > 3 else kwargs.get("normalize", True)
                    channels_first = args[4] if len(args) > 4 else kwargs.get("channels_first", True)

                    src = str(uri) if isinstance(uri, (str, Path, os.PathLike)) else uri
                    if hasattr(src, "seek"):
                        try:
                            src.seek(0 if orig_pos is None else orig_pos)
                        except Exception:
                            pass
                    start = max(0, int(frame_offset or 0))
                    frames = int(num_frames) if num_frames is not None else -1
                    data, sr = _sf.read(
                        src,
                        start=start,
                        frames=frames,
                        dtype="float32" if normalize else "int16",
                        always_2d=True,
                    )
                    arr = _np.asarray(data)
                    if channels_first:
                        arr = arr.T
                    tensor = _torch.from_numpy(_np.ascontiguousarray(arr))
                    return tensor, int(sr)
                except Exception:
                    raise orig_exc

        _safe_ta_load._colabone_sf_load_patched = True  # type: ignore[attr-defined]
        _ta.load = _safe_ta_load


    _COLABONE_DAEMON_RESP_PREFIX = "__COLABONE_DAEMON_RESP__:"


    def _resolve_hf_snapshot(
        snapshot_download_fn,
        repo: str,
        revision: str,
        cache_dir: str,
        token: str | None,
    ) -> str:
        \"\"\"Resolve cached Hugging Face snapshot offline first; fall back to Hub download on miss.\"\"\"
        try:
            return snapshot_download_fn(
                repo_id=repo,
                revision=revision or None,
                cache_dir=cache_dir or None,
                token=token,
                local_files_only=True,
            )
        except Exception:
            return snapshot_download_fn(
                repo_id=repo,
                revision=revision or None,
                cache_dir=cache_dir or None,
                token=token,
            )


    def _emit_daemon_resp(payload: dict) -> None:
        line = _COLABONE_DAEMON_RESP_PREFIX + json.dumps(
            payload, ensure_ascii=False, separators=(",", ":")
        )
        sys.stdout.write("\\n" + line + "\\n")
        sys.stdout.flush()


    def _format_daemon_exc(exc: BaseException) -> str:
        msg = str(exc).strip()
        if isinstance(exc, (RuntimeError, ValueError)) and msg:
            return msg
        name = type(exc).__name__
        if not msg:
            return name
        return msg if msg.startswith(f"{name}:") else f"{name}: {msg}"


    def _run_daemon_loop(handle_config_fn, handle_batch_fn=None) -> None:
        _emit_daemon_resp({"event": "ready", "ok": True})
        for raw_line in sys.stdin:
            line = raw_line.strip()
            if not line:
                continue
            try:
                req = json.loads(line)
            except Exception as exc:
                _emit_daemon_resp({"ok": False, "error": f"invalid request json: {exc}"})
                continue
            if not isinstance(req, dict):
                _emit_daemon_resp({"ok": False, "error": "request must be a JSON object"})
                continue
            op = str(req.get("op") or "").strip()
            req_id = str(req.get("id") or "")
            if op == "ping":
                _emit_daemon_resp({"id": req_id, "op": "ping", "ok": True})
                continue
            if op == "shutdown":
                _emit_daemon_resp({"id": req_id, "op": "shutdown", "ok": True})
                break
            if op == "synthesize":
                try:
                    cfg_path = str(req.get("config_path") or "").strip()
                    if cfg_path:
                        cfg = json.loads(Path(cfg_path).read_text(encoding="utf-8"))
                    else:
                        cfg = req.get("config")
                    if not isinstance(cfg, dict):
                        raise ValueError("synthesize config must be an object")
                    handle_config_fn(cfg)
                    _emit_daemon_resp({"id": req_id, "op": "synthesize", "ok": True})
                except Exception as exc:
                    _emit_daemon_resp(
                        {
                            "id": req_id,
                            "op": "synthesize",
                            "ok": False,
                            "error": _format_daemon_exc(exc),
                        }
                    )
                continue
            if op == "synthesize_batch":
                raw_items = req.get("items")
                if not isinstance(raw_items, list):
                    _emit_daemon_resp(
                        {
                            "id": req_id,
                            "op": "synthesize_batch",
                            "ok": False,
                            "error": "items must be a list",
                        }
                    )
                    continue
                configs: list[dict] = []
                load_errors: dict[int, str] = {}
                for idx, item in enumerate(raw_items):
                    try:
                        if isinstance(item, dict) and item.get("config_path"):
                            cfg = json.loads(
                                Path(str(item["config_path"])).read_text(encoding="utf-8")
                            )
                        elif isinstance(item, dict) and isinstance(item.get("config"), dict):
                            cfg = item["config"]
                        elif isinstance(item, dict):
                            cfg = item
                        else:
                            raise ValueError("batch item must be an object")
                        if not isinstance(cfg, dict):
                            raise ValueError("batch item config must be an object")
                        configs.append(cfg)
                    except Exception as exc:
                        configs.append({})
                        load_errors[idx] = _format_daemon_exc(exc)
                try:
                    if callable(handle_batch_fn) and not load_errors:
                        results = handle_batch_fn(configs)
                    else:
                        results = []
                        for idx, cfg in enumerate(configs):
                            if idx in load_errors:
                                results.append(
                                    {"index": idx, "ok": False, "error": load_errors[idx]}
                                )
                                continue
                            try:
                                handle_config_fn(cfg)
                                results.append({"index": idx, "ok": True})
                            except Exception as exc:
                                results.append(
                                    {
                                        "index": idx,
                                        "ok": False,
                                        "error": _format_daemon_exc(exc),
                                    }
                                )
                    _emit_daemon_resp(
                        {
                            "id": req_id,
                            "op": "synthesize_batch",
                            "ok": True,
                            "results": results,
                        }
                    )
                except Exception as exc:
                    _emit_daemon_resp(
                        {
                            "id": req_id,
                            "op": "synthesize_batch",
                            "ok": False,
                            "error": _format_daemon_exc(exc),
                        }
                    )
                continue
            _emit_daemon_resp(
                {"id": req_id, "op": op, "ok": False, "error": f"unsupported op: {op}"}
            )
    """
)

_VOXCPM2_WORKER_BODY = textwrap.dedent(
    """\

    def _load_voxcpm_model(repo: str, revision: str, cache_dir: str, token: str | None):
        from huggingface_hub import snapshot_download
        import voxcpm

        sig = inspect.signature(voxcpm.VoxCPM.from_pretrained)
        base_kwargs = {}
        if "load_denoiser" in sig.parameters:
            base_kwargs["load_denoiser"] = False
        if "optimize" in sig.parameters:
            base_kwargs["optimize"] = False

        local_dir = _resolve_hf_snapshot(
            snapshot_download, repo, revision, cache_dir, token
        )
        return voxcpm.VoxCPM.from_pretrained(local_dir, **base_kwargs)


    def _run_voxcpm_request(cfg: dict, state: dict) -> None:
        _patch_torchaudio_load_soundfile()
        token = _hf_token()
        repo = cfg["repo"]
        revision = cfg.get("revision") or ""
        cache_dir = cfg.get("cache_dir") or ""
        ref_audio_path = (cfg.get("ref_audio_path") or "").strip()
        ref_text = (cfg.get("ref_text") or "").strip()
        voice_prompt = (cfg.get("voice_prompt") or "").strip()
        emotion_prompt = str(cfg.get("emotion_prompt") or "").strip()
        voice_seed = cfg.get("voice_seed")
        default_voice_prompt = (
            "A 26-year-old Chinese female host, clear warm articulate studio microphone voice, "
            "natural smooth pacing, friendly confident professional tone"
        )
        segments = cfg.get("segments") or []

        cache_key = (repo, revision, cache_dir)
        model = state.get("model") if state.get("key") == cache_key else None
        if model is None:
            _release_warm_state(state)
            model = _load_voxcpm_model(repo, revision, cache_dir, token)
            state["key"] = cache_key
            state["model"] = model
        sample_rate = int(
            getattr(getattr(model, "tts_model", None), "sample_rate", None)
            or getattr(model, "sample_rate", None)
            or cfg.get("sample_rate")
            or 48000
        )
        gen_sig = inspect.signature(model.generate)
        gen_params = gen_sig.parameters
        named_gen_params = [
            p
            for p in gen_params.values()
            if p.kind
            not in (inspect.Parameter.VAR_POSITIONAL, inspect.Parameter.VAR_KEYWORD)
        ]
        if not named_gen_params:
            target_fn = getattr(model, "_generate", model.generate)
            if target_fn is not model.generate and callable(target_fn):
                try:
                    target_sig = inspect.signature(target_fn)
                    target_params = target_sig.parameters
                    if any(
                        p.kind
                        not in (
                            inspect.Parameter.VAR_POSITIONAL,
                            inspect.Parameter.VAR_KEYWORD,
                        )
                        for p in target_params.values()
                    ):
                        gen_sig = target_sig
                        gen_params = target_params
                except (TypeError, ValueError):
                    pass
        has_var_kw = any(
            p.kind == inspect.Parameter.VAR_KEYWORD for p in gen_params.values()
        )

        if ref_audio_path and not ref_text and "reference_wav_path" not in gen_params:
            raise ValueError("VoxCPM2 当前版本克隆音色时需要同时填写参考音频对应文本")

        active_ref_audio = ref_audio_path
        active_ref_text = ref_text

        has_valid_seed = (
            isinstance(voice_seed, int)
            and not isinstance(voice_seed, bool)
            and voice_seed >= 0
        )
        if has_valid_seed:
            try:
                import random as _py_random

                _py_random.seed(voice_seed)
            except Exception:
                pass
            try:
                import numpy as _np

                _np.random.seed(voice_seed % (2**32))
            except Exception:
                pass
            try:
                import torch

                torch.manual_seed(voice_seed)
                cuda_mod = getattr(torch, "cuda", None)
                if cuda_mod is not None and callable(getattr(cuda_mod, "manual_seed_all", None)):
                    cuda_mod.manual_seed_all(voice_seed)
            except Exception:
                pass

        def _sanitize_voxcpm_prompt(raw_prompt: str) -> str:
            cleaned = (
                raw_prompt.replace("(", " ")
                .replace(")", " ")
                .replace("（", " ")
                .replace("）", " ")
            )
            return " ".join(cleaned.split())

        total = len(segments)
        for idx, seg in enumerate(segments):
            seg_text = str(seg["text"])
            out_wav = str(seg["output_wav"])
            kwargs = {
                "text": seg_text,
                "inference_timesteps": 18,
                "cfg_value": 2.0,
                "normalize": True,
            }
            if has_valid_seed and ("seed" in gen_params or has_var_kw):
                kwargs["seed"] = int(voice_seed + idx) % (2**32)
            if active_ref_audio:
                if active_ref_text:
                    kwargs["prompt_wav_path"] = active_ref_audio
                    kwargs["prompt_text"] = active_ref_text
                    if "reference_wav_path" in gen_params:
                        kwargs["reference_wav_path"] = active_ref_audio
                elif "reference_wav_path" in gen_params:
                    kwargs["reference_wav_path"] = active_ref_audio
                    control_parts = [
                        p
                        for p in (
                            _sanitize_voxcpm_prompt(voice_prompt),
                            _sanitize_voxcpm_prompt(emotion_prompt),
                        )
                        if p
                    ]
                    if control_parts:
                        kwargs["text"] = f"({', '.join(control_parts)}){seg_text}"
                else:
                    raise ValueError(
                        "VoxCPM2 当前版本克隆音色时需要同时填写参考音频对应文本"
                    )
            else:
                base_voice_prompt = (
                    _sanitize_voxcpm_prompt(voice_prompt) or default_voice_prompt
                )
                safe_emo = _sanitize_voxcpm_prompt(emotion_prompt)
                effective_voice_prompt = (
                    f"{base_voice_prompt}, {safe_emo}" if safe_emo else base_voice_prompt
                )
                if "voice_prompt" in gen_params:
                    kwargs["voice_prompt"] = effective_voice_prompt
                else:
                    kwargs["text"] = f"({effective_voice_prompt}){seg_text}"

            if not has_var_kw:
                kwargs = {k: v for k, v in kwargs.items() if k in gen_params}

            wav = model.generate(**kwargs)
            _write_pcm16_wav(out_wav, wav, sample_rate)
            if idx == 0 and not active_ref_audio and total > 1:
                active_ref_audio = out_wav
                active_ref_text = seg_text
                print(
                    f"[colabone-tts] voxcpm2 locked speaker timbre from segment 1: {out_wav}",
                    flush=True,
                )
            print(f"[colabone-tts] voxcpm2 segment {idx + 1}/{total} done", flush=True)


    def main() -> None:
        _disable_non_torch_backends()
        parser = argparse.ArgumentParser(description="ColabOne VoxCPM2 remote TTS worker")
        parser.add_argument("--config", default="")
        parser.add_argument("--daemon", action="store_true")
        args = parser.parse_args()
        state: dict = {}
        if args.daemon:
            _run_daemon_loop(lambda cfg: _run_voxcpm_request(cfg, state))
            return
        if not args.config:
            parser.error("--config is required unless --daemon is set")
        cfg = json.loads(Path(args.config).read_text(encoding="utf-8"))
        _run_voxcpm_request(cfg, state)


    if __name__ == "__main__":
        main()
    """
)

_QWEN3_TTS_WORKER_BODY = textwrap.dedent(
    """\

    def _qwen3_cuda_vram_mib() -> int:
        try:
            import torch

            if torch.cuda.is_available() and callable(
                getattr(torch.cuda, "get_device_properties", None)
            ):
                return int(
                    torch.cuda.get_device_properties(0).total_memory // (1024 * 1024)
                )
        except Exception:
            pass
        return 0


    def _load_qwen3_model(repo: str, revision: str, cache_dir: str, token: str | None):
        import importlib.util
        import torch
        from huggingface_hub import snapshot_download
        import qwen_tts

        use_cuda = torch.cuda.is_available()
        device_map = "cuda:0" if use_cuda else "cpu"
        dtype = (
            torch.bfloat16
            if (use_cuda and torch.cuda.is_bf16_supported())
            else (torch.float16 if use_cuda else torch.float32)
        )

        local_dir = _resolve_hf_snapshot(
            snapshot_download, repo, revision, cache_dir, token
        )
        attn_candidates: list[str | None] = []
        if use_cuda:
            if importlib.util.find_spec("flash_attn") is not None:
                attn_candidates.append("flash_attention_2")
            attn_candidates.append("sdpa")
        attn_candidates.append(None)

        last_exc: Exception | None = None
        for attn_impl in attn_candidates:
            extra_kw = {"attn_implementation": attn_impl} if attn_impl else {}
            try:
                return qwen_tts.Qwen3TTSModel.from_pretrained(
                    local_dir,
                    device_map=device_map,
                    dtype=dtype,
                    **extra_kw,
                )
            except TypeError as exc:
                last_exc = exc
                try:
                    return qwen_tts.Qwen3TTSModel.from_pretrained(
                        local_dir,
                        device_map=device_map,
                        torch_dtype=dtype,
                        **extra_kw,
                    )
                except (TypeError, ValueError) as exc2:
                    last_exc = exc2
                    if attn_impl is not None:
                        continue
                    raise
            except (ValueError, ImportError, RuntimeError) as exc:
                last_exc = exc
                if attn_impl is not None:
                    continue
                raise
        if last_exc is not None:
            raise last_exc
        return qwen_tts.Qwen3TTSModel.from_pretrained(
            local_dir,
            device_map=device_map,
            dtype=dtype,
        )


    def _seed_qwen3_generators(voice_seed) -> None:
        if isinstance(voice_seed, int) and not isinstance(voice_seed, bool) and voice_seed >= 0:
            try:
                import random as _py_random

                _py_random.seed(voice_seed)
            except Exception:
                pass
            try:
                import numpy as _np

                _np.random.seed(voice_seed % (2**32))
            except Exception:
                pass
            try:
                import torch

                torch.manual_seed(voice_seed)
                cuda_mod = getattr(torch, "cuda", None)
                if cuda_mod is not None and callable(getattr(cuda_mod, "manual_seed_all", None)):
                    cuda_mod.manual_seed_all(voice_seed)
            except Exception:
                pass


    def _resolve_qwen3_mode(cfg: dict) -> dict:
        repo = (cfg.get("repo") or "").strip()
        revision = cfg.get("revision") or ""
        aux_repo = (cfg.get("aux_repo") or "").strip()
        aux_revision = (cfg.get("aux_revision") or "").strip()
        custom_voice_repo = (cfg.get("custom_voice_repo") or "").strip()
        custom_voice_revision = (cfg.get("custom_voice_revision") or "").strip()
        ref_audio_path = (cfg.get("ref_audio_path") or "").strip()
        ref_text = (cfg.get("ref_text") or "").strip()
        voice_prompt = (cfg.get("voice_prompt") or "").strip()
        emotion_prompt = (cfg.get("emotion_prompt") or "").strip()
        speaker_id = (cfg.get("speaker_id") or "").strip()

        # Mode A: CustomVoice native speaker; Mode B: VoiceDesign; Mode C: Base zero-shot clone.
        use_custom_voice = (
            (not ref_audio_path) and bool(speaker_id) and bool(custom_voice_repo)
        )
        use_voice_design = (
            (not use_custom_voice) and (not ref_audio_path) and bool(aux_repo)
        )

        if not ref_audio_path and not use_custom_voice and not aux_repo:
            raise ValueError(
                "Qwen3-TTS 缺少音色参考音频、CustomVoice 或 VoiceDesign 辅助模型配置"
            )

        if use_custom_voice:
            mode_label = "custom_voice"
            selected_repo = custom_voice_repo
            selected_rev = custom_voice_revision
            parts = [p for p in (emotion_prompt, voice_prompt) if p]
            effective_instruct = "；".join(parts)
        elif use_voice_design:
            mode_label = "voice_design"
            selected_repo = aux_repo
            selected_rev = aux_revision
            base_instruct = (
                voice_prompt
                or "26岁年轻知性女性主播，清澈温润的现代标准普通话，吐字清晰圆润，棚录近麦高保真质感，语速自然流畅，专业沉稳且富有亲和力。"
            )
            effective_instruct = "；".join(
                p for p in (base_instruct, emotion_prompt) if p
            )
        else:
            mode_label = "voice_clone"
            selected_repo = repo
            selected_rev = revision
            effective_instruct = voice_prompt

        return {
            "mode_label": mode_label,
            "use_custom_voice": use_custom_voice,
            "use_voice_design": use_voice_design,
            "selected_repo": selected_repo,
            "selected_rev": selected_rev,
            "effective_instruct": effective_instruct,
            "repo": repo,
            "revision": revision,
            "aux_repo": aux_repo,
            "aux_revision": aux_revision,
            "ref_audio_path": ref_audio_path,
            "ref_text": ref_text,
            "speaker_id": speaker_id,
        }


    class _Qwen3WorkerSession:
        def __init__(self) -> None:
            self.models: dict[tuple[str, str, str], object] = {}
            self.order: list[tuple[str, str, str]] = []
            vram_mib = _qwen3_cuda_vram_mib()
            self.max_models = 2 if vram_mib >= 20480 else 1

        def _evict_key(self, key: tuple[str, str, str]) -> None:
            model = self.models.pop(key, None)
            if key in self.order:
                self.order.remove(key)
            if model is not None:
                del model
                gc.collect()
                try:
                    import torch

                    if getattr(torch, "cuda", None) is not None and torch.cuda.is_available():
                        torch.cuda.empty_cache()
                except Exception:
                    pass

        def get_model(
            self, repo: str, revision: str, cache_dir: str, token: str | None
        ):
            key = (repo, revision, cache_dir)
            if key in self.models:
                if key in self.order:
                    self.order.remove(key)
                self.order.append(key)
                return self.models[key]
            while len(self.order) >= self.max_models and self.order:
                oldest = self.order[0]
                self._evict_key(oldest)
            model = _load_qwen3_model(repo, revision, cache_dir, token)
            self.models[key] = model
            self.order.append(key)
            return model

        def run_one(self, cfg: dict) -> None:
            _patch_torchaudio_load_soundfile()
            token = _hf_token()
            cache_dir = cfg.get("cache_dir") or ""
            voice_seed = cfg.get("voice_seed")
            segments = cfg.get("segments") or []
            info = _resolve_qwen3_mode(cfg)
            use_custom_voice = info["use_custom_voice"]
            use_voice_design = info["use_voice_design"]
            mode_label = info["mode_label"]
            selected_repo = info["selected_repo"]
            selected_rev = info["selected_rev"]
            effective_instruct = info["effective_instruct"]
            repo = info["repo"]
            revision = info["revision"]
            aux_repo = info["aux_repo"]
            aux_revision = info["aux_revision"]
            ref_audio_path = info["ref_audio_path"]
            ref_text = info["ref_text"]
            speaker_id = info["speaker_id"]

            print(
                f"[colabone-tts] qwen3_tts mode={mode_label} repo={selected_repo}"
                + (f" speaker={speaker_id}" if use_custom_voice else ""),
                flush=True,
            )
            model = self.get_model(selected_repo, selected_rev, cache_dir, token)

            cv_params = None
            cv_has_var_kw = True
            if use_custom_voice:
                gen_cv_fn = getattr(model, "generate_custom_voice", None)
                if not callable(gen_cv_fn):
                    raise RuntimeError("Qwen3-TTS 当前模型不支持 generate_custom_voice 接口")
                try:
                    cv_sig = inspect.signature(gen_cv_fn)
                    cv_params = cv_sig.parameters
                    cv_has_var_kw = any(
                        p.kind == inspect.Parameter.VAR_KEYWORD
                        for p in cv_params.values()
                    )
                except (TypeError, ValueError):
                    cv_params = None
                    cv_has_var_kw = True

            clone_prompt = None
            if (
                not use_custom_voice
                and not use_voice_design
                and ref_audio_path
                and hasattr(model, "create_voice_clone_prompt")
            ):
                clone_prompt = model.create_voice_clone_prompt(
                    ref_audio=ref_audio_path,
                    ref_text=ref_text or None,
                    x_vector_only_mode=(not bool(ref_text)),
                )

            _seed_qwen3_generators(voice_seed)

            total = len(segments)
            default_sr = int(cfg.get("sample_rate") or 24000)
            start_idx = 0

            # Lock speaker timbre across multi-sentence voice_design runs by generating segment 0 with
            # VoiceDesign, then transitioning to the Base model with a voice_clone_prompt from segment 0.
            # Mode A (CustomVoice) already has a fixed native speaker, so no handoff is needed.
            if use_voice_design and total > 1 and repo:
                import torch

                seg0 = segments[0]
                seg0_text = str(seg0["text"])
                seg0_out = str(seg0["output_wav"])
                wavs0, sr0 = model.generate_voice_design(
                    text=seg0_text,
                    language="Auto",
                    instruct=effective_instruct,
                )
                wav0 = wavs0[0] if isinstance(wavs0, (list, tuple)) else wavs0
                seg0_sr = int(sr0 or default_sr)
                _write_pcm16_wav(seg0_out, wav0, seg0_sr)
                print(f"[colabone-tts] qwen3_tts segment 1/{total} done", flush=True)
                start_idx = 1

                base_model = None
                try:
                    print(
                        f"[colabone-tts] qwen3_tts locking speaker timbre via base repo={repo}",
                        flush=True,
                    )
                    if self.max_models <= 1:
                        self._evict_key((selected_repo, selected_rev, cache_dir))
                    model = None
                    base_model = self.get_model(repo, revision, cache_dir, token)
                    try:
                        import numpy as np

                        seg0_ref_audio = (
                            np.asarray(_to_mono_list(wav0), dtype=np.float32),
                            seg0_sr,
                        )
                    except ImportError:
                        seg0_ref_audio = seg0_out

                    if hasattr(base_model, "create_voice_clone_prompt"):
                        clone_prompt = base_model.create_voice_clone_prompt(
                            ref_audio=seg0_ref_audio,
                            ref_text=seg0_text,
                            x_vector_only_mode=False,
                        )
                    model = base_model
                    use_voice_design = False
                    ref_audio_path = seg0_out
                    ref_text = seg0_text
                except Exception as exc:
                    print(
                        f"[colabone-tts] qwen3_tts timbre lock fallback to voice_design: {exc}",
                        flush=True,
                    )
                    if base_model is not None:
                        self._evict_key((repo, revision, cache_dir))
                    model = self.get_model(aux_repo, aux_revision, cache_dir, token)
                    use_voice_design = True
                    clone_prompt = None

            for idx in range(start_idx, total):
                seg = segments[idx]
                seg_text = str(seg["text"])
                out_wav = str(seg["output_wav"])
                if use_custom_voice:
                    cv_kwargs = {
                        "text": seg_text,
                        "language": "Auto",
                        "speaker": speaker_id,
                        "instruct": effective_instruct or None,
                    }
                    if cv_params is not None and not cv_has_var_kw:
                        cv_kwargs = {k: v for k, v in cv_kwargs.items() if k in cv_params}
                    wavs, sr = model.generate_custom_voice(**cv_kwargs)
                elif use_voice_design:
                    wavs, sr = model.generate_voice_design(
                        text=seg_text,
                        language="Auto",
                        instruct=effective_instruct,
                    )
                elif clone_prompt is not None:
                    wavs, sr = model.generate_voice_clone(
                        text=seg_text,
                        language="Auto",
                        voice_clone_prompt=clone_prompt,
                    )
                else:
                    wavs, sr = model.generate_voice_clone(
                        text=seg_text,
                        language="Auto",
                        ref_audio=ref_audio_path or None,
                        ref_text=ref_text or None,
                        x_vector_only_mode=(not bool(ref_text)),
                    )

                wav = wavs[0] if isinstance(wavs, (list, tuple)) else wavs
                _write_pcm16_wav(out_wav, wav, int(sr or default_sr))
                print(f"[colabone-tts] qwen3_tts segment {idx + 1}/{total} done", flush=True)

        def run_batch(self, configs: list[dict]) -> list[dict]:
            _patch_torchaudio_load_soundfile()
            token = _hf_token()
            results: list[dict | None] = [None] * len(configs)
            grouped: dict[tuple[str, str, str, str], list[tuple[int, dict, dict]]] = {}
            fallback_indices: list[int] = []

            for idx, cfg in enumerate(configs):
                try:
                    info = _resolve_qwen3_mode(cfg)
                    segs = cfg.get("segments") or []
                    if (
                        len(segs) == 1
                        and info["mode_label"] in ("voice_design", "custom_voice")
                    ):
                        gkey = (
                            info["mode_label"],
                            info["selected_repo"],
                            info["selected_rev"],
                            cfg.get("cache_dir") or "",
                        )
                        grouped.setdefault(gkey, []).append((idx, cfg, info))
                    else:
                        fallback_indices.append(idx)
                except Exception as exc:
                    results[idx] = {
                        "index": idx,
                        "ok": False,
                        "error": str(exc) or type(exc).__name__,
                    }

            for (mode_label, sel_repo, sel_rev, cache_dir), entries in grouped.items():
                if len(entries) <= 1:
                    for idx, _, _ in entries:
                        fallback_indices.append(idx)
                    continue
                try:
                    model = self.get_model(sel_repo, sel_rev, cache_dir, token)
                    first_seed = entries[0][1].get("voice_seed")
                    _seed_qwen3_generators(first_seed)
                    texts = [str(e[1]["segments"][0]["text"]) for e in entries]
                    langs = ["Auto"] * len(entries)
                    default_sr = int(entries[0][1].get("sample_rate") or 24000)

                    if mode_label == "voice_design":
                        instructs = [e[2]["effective_instruct"] for e in entries]
                        print(
                            f"[colabone-tts] qwen3_tts batch voice_design size={len(entries)}",
                            flush=True,
                        )
                        wavs, sr = model.generate_voice_design(
                            text=texts,
                            language=langs,
                            instruct=instructs,
                        )
                    else:
                        gen_cv_fn = getattr(model, "generate_custom_voice", None)
                        if not callable(gen_cv_fn):
                            raise RuntimeError(
                                "Qwen3-TTS 当前模型不支持 generate_custom_voice 接口"
                            )
                        speakers = [e[2]["speaker_id"] for e in entries]
                        instructs = [e[2]["effective_instruct"] for e in entries]
                        cv_kwargs = {
                            "text": texts,
                            "language": langs,
                            "speaker": speakers,
                        }
                        if any(instructs):
                            cv_kwargs["instruct"] = [inst or "" for inst in instructs]
                        print(
                            f"[colabone-tts] qwen3_tts batch custom_voice size={len(entries)}",
                            flush=True,
                        )
                        wavs, sr = gen_cv_fn(**cv_kwargs)

                    if not isinstance(wavs, (list, tuple)) or len(wavs) != len(entries):
                        raise RuntimeError("Qwen3-TTS batch returned unexpected audio count")

                    out_sr = int(sr or default_sr)
                    for (idx, cfg, _), wav in zip(entries, wavs):
                        out_wav = str(cfg["segments"][0]["output_wav"])
                        _write_pcm16_wav(out_wav, wav, out_sr)
                        results[idx] = {"index": idx, "ok": True}
                    print(
                        f"[colabone-tts] qwen3_tts batch {mode_label} done ({len(entries)} items)",
                        flush=True,
                    )
                except Exception as exc:
                    print(
                        f"[colabone-tts] qwen3_tts batch {mode_label} fallback to sequential: {exc}",
                        flush=True,
                    )
                    for idx, _, _ in entries:
                        if results[idx] is None:
                            fallback_indices.append(idx)

            for idx in fallback_indices:
                if results[idx] is not None:
                    continue
                try:
                    self.run_one(configs[idx])
                    results[idx] = {"index": idx, "ok": True}
                except Exception as exc:
                    results[idx] = {
                        "index": idx,
                        "ok": False,
                        "error": str(exc) or type(exc).__name__,
                    }

            return [
                r if r is not None else {"index": i, "ok": False, "error": "no result"}
                for i, r in enumerate(results)
            ]


    def main() -> None:
        _disable_non_torch_backends()
        parser = argparse.ArgumentParser(description="ColabOne Qwen3-TTS remote worker")
        parser.add_argument("--config", default="")
        parser.add_argument("--daemon", action="store_true")
        args = parser.parse_args()
        session = _Qwen3WorkerSession()
        if args.daemon:
            _run_daemon_loop(session.run_one, session.run_batch)
            return
        if not args.config:
            parser.error("--config is required unless --daemon is set")
        cfg = json.loads(Path(args.config).read_text(encoding="utf-8"))
        session.run_one(cfg)


    if __name__ == "__main__":
        main()
    """
)

_FISH_S2_PRO_WORKER_BODY = textwrap.dedent(
    """\

    def _apply_emotion_tag(text: str, emotion_prompt: str) -> str:
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
        raise FileNotFoundError(f"在 {model_dir} 中未找到 Fish Speech 解码器权重文件")


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


    def _seed_fish_s2_generators(seed: int) -> None:
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


    def _run_fish_s2_request(cfg: dict, state: dict) -> None:
        token = _hf_token()
        source_dir = (cfg.get("source_dir") or "").strip()
        if source_dir and source_dir not in sys.path:
            sys.path.insert(0, source_dir)

        import torch
        from huggingface_hub import snapshot_download
        from fish_speech.inference_engine import TTSInferenceEngine
        from fish_speech.models.dac.inference import load_model as load_decoder_model
        from fish_speech.models.text2semantic.inference import launch_thread_safe_queue
        from fish_speech.utils.schema import ServeReferenceAudio, ServeTTSRequest

        _patch_torchaudio_load_soundfile()

        repo = cfg["repo"]
        revision = cfg.get("revision") or ""
        cache_dir = cfg.get("cache_dir") or ""
        ref_audio_path = (cfg.get("ref_audio_path") or "").strip()
        ref_text = (cfg.get("ref_text") or "").strip()
        emotion_prompt = (cfg.get("emotion_prompt") or "").strip()
        voice_seed = cfg.get("voice_seed")
        segments = cfg.get("segments") or []

        has_valid_seed = (
            isinstance(voice_seed, int)
            and not isinstance(voice_seed, bool)
            and voice_seed >= 0
        )
        if has_valid_seed:
            _seed_fish_s2_generators(voice_seed)

        cache_key = (repo, revision, cache_dir)
        engine = state.get("engine") if state.get("key") == cache_key else None
        if engine is None:
            _release_warm_state(state)
            use_cuda = torch.cuda.is_available()
            device = "cuda" if use_cuda else "cpu"
            dtype = (
                torch.bfloat16
                if (use_cuda and torch.cuda.is_bf16_supported())
                else (torch.float16 if use_cuda else torch.float32)
            )
            dtype_label = (
                "bf16"
                if dtype == torch.bfloat16
                else ("fp16" if dtype == torch.float16 else "fp32")
            )
            vram_mib = (
                int(torch.cuda.get_device_properties(0).total_memory // (1024 * 1024))
                if use_cuda
                else 0
            )

            print(
                f"[colabone-tts] fish_s2_pro repo={repo} dtype={dtype_label} vram_mib={vram_mib}",
                flush=True,
            )

            local_dir = Path(
                _resolve_hf_snapshot(snapshot_download, repo, revision, cache_dir, token)
            )
            decoder_ckpt = _find_decoder_checkpoint(local_dir)

            llama_sig = inspect.signature(launch_thread_safe_queue)
            llama_kwargs = {
                "checkpoint_path": str(local_dir),
                "device": device,
                "precision": dtype,
                "compile": False,
            }
            llama_kwargs = {k: v for k, v in llama_kwargs.items() if k in llama_sig.parameters}
            llama_queue = launch_thread_safe_queue(**llama_kwargs)

            dec_sig = inspect.signature(load_decoder_model)
            dec_kwargs = {
                "config_name": "modded_dac_vq",
                "checkpoint_path": str(decoder_ckpt),
                "device": device,
            }
            dec_kwargs = {k: v for k, v in dec_kwargs.items() if k in dec_sig.parameters}
            decoder_model = load_decoder_model(**dec_kwargs)

            engine = TTSInferenceEngine(
                llama_queue=llama_queue,
                decoder_model=decoder_model,
                precision=dtype,
                compile=False,
            )
            # Bypass torchaudio.load (torchcodec on PyTorch >= 2.9) using soundfile + scipy/torchaudio.functional.
            engine.load_audio = _load_audio_soundfile
            state["key"] = cache_key
            state["engine"] = engine

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
            # Preserve inline emotion tags like [laugh] / [whisper] and prepend emotion_prompt if set.
            seg_text = _apply_emotion_tag(str(seg["text"]), emotion_prompt)
            out_wav = str(seg["output_wav"])
            req_kwargs = {
                "text": seg_text,
                "references": references,
                "format": "wav",
                "streaming": False,
                "max_new_tokens": 0,
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
                    raise RuntimeError(f"Fish Speech S2 Pro 推理失败：{getattr(result, 'error', result)}")
                if getattr(result, "code", "") == "final" and getattr(result, "audio", None) is not None:
                    sample_rate, final_audio = result.audio
            if final_audio is None:
                raise RuntimeError(f"Fish Speech S2 Pro 未返回第 {idx + 1} 段音频")
            _write_pcm16_wav(out_wav, final_audio, int(sample_rate or default_sr))
            if idx == 0 and not references and total > 1:
                references = [
                    ServeReferenceAudio(
                        audio=Path(out_wav).read_bytes(),
                        text=seg_text,
                    )
                ]
                print(
                    f"[colabone-tts] fish_s2_pro locked speaker timbre from segment 1: {out_wav}",
                    flush=True,
                )
            print(f"[colabone-tts] fish_s2_pro segment {idx + 1}/{total} done", flush=True)


    def main() -> None:
        _disable_non_torch_backends()
        parser = argparse.ArgumentParser(description="ColabOne Fish Speech S2 Pro remote worker")
        parser.add_argument("--config", default="")
        parser.add_argument("--daemon", action="store_true")
        args = parser.parse_args()
        state: dict = {}
        if args.daemon:
            _run_daemon_loop(lambda cfg: _run_fish_s2_request(cfg, state))
            return
        if not args.config:
            parser.error("--config is required unless --daemon is set")
        cfg = json.loads(Path(args.config).read_text(encoding="utf-8"))
        _run_fish_s2_request(cfg, state)


    if __name__ == "__main__":
        main()
    """
)

_INDEXTTS2_WORKER_BODY = textwrap.dedent(
    """\

    def _colabone_save_pcm_wav(*args, **kwargs) -> None:
        out_path = (
            kwargs.get("output_path")
            or kwargs.get("path")
            or kwargs.get("filepath")
            or kwargs.get("uri")
        )
        audio = kwargs.get("wav")
        if audio is None:
            audio = kwargs.get("audio")
        if audio is None:
            audio = kwargs.get("waveform")
        if audio is None:
            audio = kwargs.get("src")
        sr = (
            kwargs.get("sampling_rate")
            or kwargs.get("sample_rate")
            or kwargs.get("sr")
        )
        remaining = list(args)
        if out_path is None:
            for idx, val in enumerate(remaining):
                if isinstance(val, (str, Path, os.PathLike)):
                    out_path = val
                    remaining.pop(idx)
                    break
        if sr is None:
            for idx in range(len(remaining) - 1, -1, -1):
                val = remaining[idx]
                if isinstance(val, (int, float)) and not isinstance(val, bool):
                    sr = int(val)
                    remaining.pop(idx)
                    break
        if audio is None and remaining:
            audio = remaining[0]
        if out_path is None or audio is None:
            raise ValueError("save_pcm_wav 缺少输出路径或音频数据")
        scaled_audio = audio
        try:
            if hasattr(audio, "detach"):
                t = audio.detach().cpu()
                dtype_name = str(getattr(t, "dtype", ""))
                is_pcm_int = any(
                    tok in dtype_name for tok in ("int8", "int16", "int32", "uint8")
                )
                if not is_pcm_int:
                    if "bfloat16" in dtype_name or "float16" in dtype_name:
                        t = t.float()
                    try:
                        import numpy as _np

                        arr = _np.asarray(t.numpy(), dtype=_np.float32)
                        finite = arr[_np.isfinite(arr)]
                        if finite.size > 0 and float(_np.max(_np.abs(finite))) > 1.5:
                            scaled_audio = arr / 32767.0
                        elif "int64" in dtype_name:
                            scaled_audio = arr
                    except Exception:
                        t_f = t.float()
                        has_elems = (
                            t_f.numel() > 0
                            if callable(getattr(t_f, "numel", None))
                            else True
                        )
                        if has_elems:
                            finite_t = t_f
                            try:
                                import torch as _torch

                                finite_t = t_f[_torch.isfinite(t_f)]
                            except Exception:
                                if callable(getattr(t_f, "isfinite", None)):
                                    finite_t = t_f[t_f.isfinite()]
                            if (
                                (
                                    not callable(getattr(finite_t, "numel", None))
                                    or finite_t.numel() > 0
                                )
                                and hasattr(finite_t, "abs")
                                and float(finite_t.abs().max()) > 1.5
                            ):
                                scaled_audio = t_f / 32767.0
                            elif "int64" in dtype_name:
                                scaled_audio = t_f
            else:
                import numpy as _np

                arr = _np.asarray(audio)
                if (
                    _np.issubdtype(arr.dtype, _np.floating)
                    or arr.dtype == _np.int64
                    or (
                        isinstance(audio, (list, tuple))
                        and _np.issubdtype(arr.dtype, _np.integer)
                    )
                ):
                    arr_f = arr.astype(_np.float32)
                    finite = arr_f[_np.isfinite(arr_f)]
                    if finite.size > 0 and float(_np.max(_np.abs(finite))) > 1.5:
                        scaled_audio = arr_f / 32767.0
                    else:
                        scaled_audio = arr_f
        except Exception:
            try:
                if isinstance(audio, (list, tuple)) and audio:
                    vals = _to_mono_list(audio)
                    if (
                        vals
                        and max((abs(v) for v in vals if math.isfinite(v)), default=0.0)
                        > 1.5
                    ):
                        scaled_audio = [v / 32767.0 for v in vals]
            except Exception:
                pass
        _write_pcm16_wav(str(out_path), scaled_audio, int(sr or 22050))


    def _detect_indextts_lang(text: str) -> str:
        import re as _re

        cleaned = _re.sub(r"\\[[^\\[\\]\\n]{1,80}\\]", " ", str(text or ""))
        cleaned = _re.sub(
            r"<([^|<>\\s]{1,16})\\|[A-Za-z0-9_-]{1,16}(?:\\s+[A-Za-z0-9_-]{1,16})*>",
            r"\\1",
            cleaned,
        )
        ar_count = 0
        kana_count = 0
        cjk_count = 0
        latin_count = 0
        es_unique_count = 0
        es_accent_count = 0
        for ch in cleaned:
            code = ord(ch)
            if (
                0x0600 <= code <= 0x06FF
                or 0x0750 <= code <= 0x077F
                or 0x08A0 <= code <= 0x08FF
                or 0xFB50 <= code <= 0xFDFF
                or 0xFE70 <= code <= 0xFEFF
            ):
                ar_count += 1
            elif (
                0x3040 <= code <= 0x30FF
                or 0x31F0 <= code <= 0x31FF
                or 0xFF66 <= code <= 0xFF9F
            ):
                kana_count += 1
            elif (
                0x4E00 <= code <= 0x9FFF
                or 0x3400 <= code <= 0x4DBF
                or 0x20000 <= code <= 0x2A6DF
            ):
                cjk_count += 1
            elif ch in "ñÑ¿¡":
                es_unique_count += 1
                latin_count += 1
            elif ch in "áéíóúüÁÉÍÓÚÜ":
                es_accent_count += 1
                latin_count += 1
            elif ("a" <= ch <= "z") or ("A" <= ch <= "Z"):
                latin_count += 1
        if ar_count > 0 and ar_count >= max(kana_count, cjk_count):
            return "AR"
        if kana_count > 0:
            return "JA"
        if cjk_count > 0 and cjk_count * 4 >= latin_count:
            return "ZH"
        if latin_count > 0:
            if es_unique_count > 0:
                return "ES"
            words = {w.strip(".,!?;:\\"'()¡¿") for w in cleaned.lower().split()}
            es_markers = {
                "hola",
                "gracias",
                "buenos",
                "buenas",
                "bienvenidos",
                "programa",
                "hoy",
                "porque",
                "para",
                "como",
                "cómo",
                "esta",
                "está",
                "estoy",
                "todos",
                "muy",
                "senor",
                "senora",
                "mundo",
                "días",
                "más",
                "también",
                "qué",
            }
            es_marker_hits = len(words & es_markers)
            if es_marker_hits >= 2 or (
                es_accent_count >= 1 and es_marker_hits >= 1
            ):
                return "ES"
            return "EN"
        return "ZH"


    def _patch_indextts_audio_save() -> None:
        # Leave upstream indextts.utils.common.save_pcm_wav intact so it performs its
        # PCM-16 scaling (/ 32767.0) before calling torchaudio.save, which we intercept here.
        try:
            import torchaudio as _ta

            _orig_ta_save = getattr(_ta, "save", None)
            if callable(_orig_ta_save) and not getattr(_orig_ta_save, "_colabone_patched", False):
                def _safe_ta_save(uri, src, sample_rate, *args, **kwargs):
                    if isinstance(uri, (str, Path, os.PathLike)):
                        _write_pcm16_wav(str(uri), src, int(sample_rate))
                        return None
                    return _orig_ta_save(uri, src, sample_rate, *args, **kwargs)

                _safe_ta_save._colabone_patched = True  # type: ignore[attr-defined]
                _ta.save = _safe_ta_save
        except Exception:
            pass


    def _run_indextts2_request(cfg: dict, state: dict) -> None:
        token = _hf_token()
        source_dir = (cfg.get("source_dir") or "").strip()
        if source_dir and source_dir not in sys.path:
            sys.path.insert(0, source_dir)

        repo = cfg["repo"]
        revision = cfg.get("revision") or ""
        cache_dir = (cfg.get("cache_dir") or "").strip()
        ref_audio_path = (cfg.get("ref_audio_path") or "").strip()
        emotion_ref_path = (cfg.get("emotion_ref_path") or "").strip()
        emotion_prompt = (cfg.get("emotion_prompt") or "").strip()
        segments = cfg.get("segments") or []

        import torch
        from huggingface_hub import snapshot_download

        _patch_torchaudio_load_soundfile()
        _patch_indextts_audio_save()
        try:
            from indextts.infer_v2_5 import IndexTTS2 as IndexTTSClass
        except ImportError:
            try:
                from indextts.infer_v2 import IndexTTS2 as IndexTTSClass
            except ImportError:
                from indextts.infer import IndexTTS as IndexTTSClass
        # Upstream indextts/infer_v2_5.py mutates os.environ['HF_HUB_CACHE'] = './checkpoints/hf_cache'
        # at import time (inside our ephemeral work_dir); immediately restore persistent cache dirs.
        if cache_dir:
            os.environ["HF_HUB_CACHE"] = cache_dir
            os.environ["HUGGINGFACE_HUB_CACHE"] = cache_dir
            os.environ["MODELSCOPE_CACHE"] = str(Path(cache_dir) / "modelscope")
        os.environ.setdefault("USE_MODELSCOPE", "false")
        _patch_indextts_audio_save()

        if not ref_audio_path:
            raise ValueError("IndexTTS2 需要提供音色参考音频（ref_audio_path）")

        cache_key = (repo, revision, cache_dir, bool(emotion_prompt and not emotion_ref_path))
        tts = state.get("tts") if state.get("key") == cache_key else None
        if tts is None:
            _release_warm_state(state)
            model_dir = Path(
                _resolve_hf_snapshot(snapshot_download, repo, revision, cache_dir, token)
            )
            cfg_path = model_dir / "config.yaml"
            if not cfg_path.is_file():
                raise FileNotFoundError(f"在 {model_dir} 中未找到 IndexTTS2 配置文件 config.yaml")

            use_cuda = bool(getattr(torch, "cuda", None) is not None and torch.cuda.is_available())
            use_bf16 = bool(
                use_cuda
                and callable(getattr(torch.cuda, "is_bf16_supported", None))
                and torch.cuda.is_bf16_supported()
            )
            init_sig = inspect.signature(IndexTTSClass.__init__)
            supports_bf16_arg = "use_bf16" in init_sig.parameters
            init_kwargs = {
                "cfg_path": str(cfg_path),
                "model_dir": str(model_dir),
                "use_fp16": use_cuda and not (use_bf16 and supports_bf16_arg),
                "use_bf16": use_bf16,
                "use_cuda_kernel": False,
                "use_deepspeed": False,
                "use_qwen_emo": bool(emotion_prompt and not emotion_ref_path),
            }
            init_kwargs = {k: v for k, v in init_kwargs.items() if k in init_sig.parameters}
            tts = IndexTTSClass(**init_kwargs)
            state["key"] = cache_key
            state["tts"] = tts

        infer_sig = inspect.signature(tts.infer)
        infer_params = infer_sig.parameters
        has_var_kw = any(
            p.kind == inspect.Parameter.VAR_KEYWORD for p in infer_params.values()
        )
        default_sr = int(cfg.get("sample_rate") or 22050)

        total = len(segments)
        for idx, seg in enumerate(segments):
            seg_text = str(seg["text"])
            out_wav = str(seg["output_wav"])
            kwargs = {
                "spk_audio_prompt": ref_audio_path,
                "text": seg_text,
                "output_path": out_wav,
            }
            if "audio_prompt" in infer_params and "spk_audio_prompt" not in infer_params:
                kwargs["audio_prompt"] = ref_audio_path
                kwargs.pop("spk_audio_prompt", None)
            if "lang" in infer_params:
                kwargs["lang"] = _detect_indextts_lang(seg_text)
            if emotion_ref_path:
                kwargs["emo_audio_prompt"] = emotion_ref_path
                if "use_emo_text" in infer_params:
                    kwargs["use_emo_text"] = False
            elif emotion_prompt:
                kwargs["emo_text"] = emotion_prompt
                kwargs["use_emo_text"] = True
            if not has_var_kw:
                kwargs = {k: v for k, v in kwargs.items() if k in infer_params}

            result = tts.infer(**kwargs)
            if not Path(out_wav).is_file():
                if isinstance(result, tuple) and len(result) == 2:
                    sr, wav = result
                    _write_pcm16_wav(out_wav, wav, int(sr or default_sr))
                elif result is not None:
                    _write_pcm16_wav(out_wav, result, default_sr)
                else:
                    raise RuntimeError(f"IndexTTS2 未生成第 {idx + 1} 段音频：{out_wav}")
            print(f"[colabone-tts] indextts2 segment {idx + 1}/{total} done", flush=True)


    def main() -> None:
        _disable_non_torch_backends()
        parser = argparse.ArgumentParser(description="ColabOne IndexTTS2 remote worker")
        parser.add_argument("--config", default="")
        parser.add_argument("--daemon", action="store_true")
        args = parser.parse_args()
        state: dict = {}
        if args.daemon:
            _run_daemon_loop(lambda cfg: _run_indextts2_request(cfg, state))
            return
        if not args.config:
            parser.error("--config is required unless --daemon is set")
        cfg = json.loads(Path(args.config).read_text(encoding="utf-8"))
        _run_indextts2_request(cfg, state)


    if __name__ == "__main__":
        main()
    """
)

_WORKER_BODIES: dict[str, str] = {
    "fish_drama3": FISH_DRAMA3_WORKER_BODY,
    "moss_tts_v1_5": MOSS_TTS_V1_5_WORKER_BODY,
    "breeze_tts2": BREEZE_TTS2_WORKER_BODY,
    "ming_3_tts": MING_3_TTS_WORKER_BODY,
    "voxcpm2": _VOXCPM2_WORKER_BODY,
    "qwen3_tts": _QWEN3_TTS_WORKER_BODY,
    "fish_s2_pro": _FISH_S2_PRO_WORKER_BODY,
    "indextts2": _INDEXTTS2_WORKER_BODY,
}


# ---------------------------------------------------------------------------
# 5. Remote GPU Multi-Engine TTS Runner & Warm Worker Pool
# ---------------------------------------------------------------------------

_COLABONE_DAEMON_RESP_PREFIX = "__COLABONE_DAEMON_RESP__:"
_COLABONE_WARM_WORKER_IDLE_SECONDS = 300.0
_BATCH_CAPABLE_PREVIEW_ENGINES = frozenset({"qwen3_tts"})

_WARM_CUDA_FATAL_TOKENS = (
    "cuda out of memory",
    "out of memory",
    "cuda error",
    "device-side assert",
    "cublas",
    "cudnn",
    "illegal memory access",
)

_FATAL_PREFETCH_ERROR_TOKENS = (
    "cuda out of memory",
    "out of memory",
    "cuda error",
    "device-side assert",
    "modulenotfounderror",
    "importerror",
    "attributeerror",
    "filenotfounderror",
    "no space left on device",
    "401",
    "403",
    "unauthorized",
    "forbidden",
    "gated repo",
    "显存不足",
    "磁盘空间不足",
    "隔离虚拟环境缺少",
    "未检测到可用远端 gpu",
    "启动失败",
    "提前退出",
    "退出码",
)


class _ColabOneWarmWorkerTransportError(RuntimeError):
    """Raised when the warm TTS daemon process crashes, disconnects, or fails startup."""


def _is_warm_worker_cuda_fatal_error(err: Any) -> bool:
    text = str(err or "").lower()
    return any(tok in text for tok in _WARM_CUDA_FATAL_TOKENS)


def _is_fatal_prefetch_error(err: Any) -> bool:
    text = str(err or "").lower()
    return any(tok in text for tok in _FATAL_PREFETCH_ERROR_TOKENS)


class ColabOneWarmTtsWorker:
    """Persistent resident TTS subprocess keeping weights hot in GPU VRAM across requests."""

    def __init__(
        self,
        *,
        engine_id: str,
        worker_python: str,
        worker_script: str,
        work_dir: Path,
        env: dict[str, str],
        has_hf_token: bool = False,
        secret_to_scrub: str = "",
        stopping: threading.Event | None = None,
    ) -> None:
        self.engine_id = engine_id
        self.worker_python = worker_python
        self.worker_script = worker_script
        self.work_dir = Path(work_dir)
        self.env = dict(env)
        self.has_hf_token = bool(has_hf_token)
        self.secret_to_scrub = str(secret_to_scrub or "")
        self.stopping = stopping
        self.proc: subprocess.Popen | None = None
        self._line_queue: queue.Queue[str | None] = queue.Queue()
        self._reader_thread: threading.Thread | None = None
        self._closed = False
        self._lock = threading.Lock()
        self._stdin_lock = threading.RLock()
        self.last_used = time.monotonic()

    def is_alive(self) -> bool:
        with self._lock:
            closed = self._closed
            proc = self.proc
        return (not closed) and proc is not None and proc.poll() is None

    def start(
        self,
        *,
        timeout: float = 45.0,
        cancel_event: threading.Event | None = None,
        task: Any = None,
    ) -> None:
        cancel_cls = _cancelled_error_cls()

        def _check_cancelled() -> None:
            if cancel_event is not None and cancel_event.is_set():
                raise cancel_cls("操作已取消")
            if self.stopping is not None and self.stopping.is_set():
                raise cancel_cls("操作已取消")
            check_fn = getattr(task, "check", None)
            if callable(check_fn):
                check_fn()

        _check_cancelled()

        self.work_dir.mkdir(parents=True, exist_ok=True)
        script_path = self.work_dir / f"warm_worker_{self.engine_id}.py"
        if script_path.is_symlink():
            raise ValueError("TTS 常驻工作脚本路径不能是符号链接")
        script_path.write_text(self.worker_script, encoding="utf-8")

        popen_kwargs: dict[str, Any] = {
            "stdin": subprocess.PIPE,
            "stdout": subprocess.PIPE,
            "stderr": subprocess.STDOUT,
            "cwd": str(self.work_dir),
            "env": self.env,
            "bufsize": 0,
        }
        if os.name == "posix":
            popen_kwargs["start_new_session"] = True

        prev_cancel_hook = None
        hook_installed = False
        _cancel_start = None
        if task is not None and hasattr(task, "lock") and hasattr(task, "cancel_hook"):
            with task.lock:
                prev_cancel_hook = task.cancel_hook

                def _cancel_start_fn() -> None:
                    self.close()
                    if callable(prev_cancel_hook):
                        try:
                            prev_cancel_hook()
                        except Exception:
                            pass

                _cancel_start = _cancel_start_fn
                task.cancel_hook = _cancel_start
                hook_installed = True

        try:
            _check_cancelled()
            proc = subprocess.Popen(
                [self.worker_python, str(script_path), "--daemon"],
                **popen_kwargs,
            )
            with self._lock:
                if self._closed:
                    _terminate_process_group(proc, timeout=3.0)
                    raise cancel_cls("操作已取消")
                self.proc = proc

            def _reader() -> None:
                if proc.stdout is None:
                    self._line_queue.put(None)
                    return
                try:
                    for raw_line in _iter_tts_pipe_lines(proc.stdout):
                        text = (
                            raw_line.decode("utf-8", errors="replace")
                            if isinstance(raw_line, (bytes, bytearray))
                            else str(raw_line)
                        ).rstrip("\r\n")
                        self._line_queue.put(text)
                except Exception:
                    pass
                finally:
                    self._line_queue.put(None)

            self._reader_thread = threading.Thread(
                target=_reader,
                name=f"colabone-tts-warm-{self.engine_id}",
                daemon=True,
            )
            self._reader_thread.start()

            startup_lines: collections.deque[str] = collections.deque(maxlen=25)
            deadline = time.monotonic() + max(5.0, float(timeout))
            while True:
                try:
                    _check_cancelled()
                except BaseException:
                    self.close()
                    raise
                remaining = deadline - time.monotonic()
                if remaining <= 0:
                    self.close()
                    raise _ColabOneWarmWorkerTransportError(
                        f"远端常驻 TTS 进程启动超时（{self.engine_id}）"
                    )
                try:
                    line = self._line_queue.get(timeout=min(0.15, remaining))
                except queue.Empty:
                    try:
                        _check_cancelled()
                    except BaseException:
                        self.close()
                        raise
                    if not self.is_alive():
                        self.close()
                        tail = _summarize_subprocess_error_tail(
                            list(startup_lines), max_chars=420
                        )
                        detail = f"：{tail}" if tail else ""
                        raise _ColabOneWarmWorkerTransportError(
                            f"远端常驻 TTS 进程提前退出（{self.engine_id}）{detail}"
                        )
                    continue
                if line is None:
                    try:
                        _check_cancelled()
                    except BaseException:
                        self.close()
                        raise
                    self.close()
                    tail = _summarize_subprocess_error_tail(
                        list(startup_lines), max_chars=420
                    )
                    detail = f"：{tail}" if tail else ""
                    raise _ColabOneWarmWorkerTransportError(
                        f"远端常驻 TTS 进程在就绪前退出（{self.engine_id}）{detail}"
                    )
                if line.startswith(_COLABONE_DAEMON_RESP_PREFIX):
                    try:
                        msg = json.loads(line[len(_COLABONE_DAEMON_RESP_PREFIX) :])
                    except ValueError:
                        continue
                    if isinstance(msg, dict) and msg.get("event") == "ready":
                        try:
                            _check_cancelled()
                        except BaseException:
                            self.close()
                            raise
                        self.last_used = time.monotonic()
                        return
                elif line:
                    startup_lines.append(_scrub_secret(line, self.secret_to_scrub))
        except BaseException:
            self.close()
            raise
        finally:
            if hook_installed and task is not None:
                with task.lock:
                    if getattr(task, "cancel_hook", None) is _cancel_start:
                        task.cancel_hook = prev_cancel_hook

    def call(
        self,
        payload: dict[str, Any],
        *,
        task: Any = None,
        cancel_event: threading.Event | None = None,
        timeout: float = 600.0,
        log_path: Path | None = None,
        secret_to_scrub: str = "",
    ) -> dict[str, Any]:
        with self._lock:
            closed = self._closed
            proc = self.proc
        if closed or proc is None or proc.poll() is not None or proc.stdin is None:
            raise _ColabOneWarmWorkerTransportError(
                f"远端常驻 TTS 进程未运行（{self.engine_id}）"
            )

        req_id = uuid.uuid4().hex[:12]
        wire = dict(payload)
        wire["id"] = req_id
        encoded = (
            json.dumps(wire, ensure_ascii=False, separators=(",", ":")) + "\n"
        ).encode("utf-8")

        scrub_secret = str(secret_to_scrub or self.secret_to_scrub or "")
        recent_lines: collections.deque[str] = collections.deque(maxlen=40)
        log_stream = None
        if log_path is not None:
            try:
                log_stream = _open_secure_tts_log_file(log_path, append=True)
            except OSError:
                log_stream = None

        cancel_cls = _cancelled_error_cls()

        def _check_cancelled() -> None:
            if cancel_event is not None and cancel_event.is_set():
                raise cancel_cls("操作已取消")
            if self.stopping is not None and self.stopping.is_set():
                raise cancel_cls("操作已取消")
            check_fn = getattr(task, "check", None)
            if callable(check_fn):
                check_fn()

        prev_cancel_hook = None
        hook_installed = False
        _cancel_warm = None
        if task is not None and hasattr(task, "lock") and hasattr(task, "cancel_hook"):
            with task.lock:
                prev_cancel_hook = task.cancel_hook

                def _cancel_warm_fn() -> None:
                    self.close()
                    if callable(prev_cancel_hook):
                        try:
                            prev_cancel_hook()
                        except Exception:
                            pass

                _cancel_warm = _cancel_warm_fn
                task.cancel_hook = _cancel_warm
                hook_installed = True

        try:
            _check_cancelled()
            with self._stdin_lock:
                with self._lock:
                    if self._closed or self.proc is not proc:
                        raise BrokenPipeError("warm worker closed")
                if proc.stdin is None or proc.stdin.closed:
                    raise BrokenPipeError("warm worker stdin closed")
                proc.stdin.write(encoded)
                proc.stdin.flush()

            deadline = time.monotonic() + max(5.0, float(timeout))
            while True:
                try:
                    _check_cancelled()
                except BaseException:
                    self.close()
                    raise

                remaining = deadline - time.monotonic()
                if remaining <= 0:
                    self.close()
                    raise RuntimeError(f"远端 TTS 常驻合成超时（{self.engine_id}）")

                try:
                    line = self._line_queue.get(timeout=min(0.15, remaining))
                except queue.Empty:
                    try:
                        _check_cancelled()
                    except BaseException:
                        self.close()
                        raise
                    if not self.is_alive():
                        self.close()
                        tail = _summarize_subprocess_error_tail(
                            list(recent_lines), max_chars=420
                        )
                        detail = f"：{tail}" if tail else ""
                        raise _ColabOneWarmWorkerTransportError(
                            f"远端 TTS 常驻进程意外退出（{self.engine_id}）{detail}"
                        )
                    continue

                if line is None:
                    try:
                        _check_cancelled()
                    except BaseException:
                        self.close()
                        raise
                    self.close()
                    tail = _summarize_subprocess_error_tail(
                        list(recent_lines), max_chars=420
                    )
                    detail = f"：{tail}" if tail else ""
                    raise _ColabOneWarmWorkerTransportError(
                        f"远端 TTS 常驻进程意外结束（{self.engine_id}）{detail}"
                    )

                if line.startswith(_COLABONE_DAEMON_RESP_PREFIX):
                    raw_json = line[len(_COLABONE_DAEMON_RESP_PREFIX) :]
                    try:
                        resp = json.loads(raw_json)
                    except ValueError:
                        continue
                    if not isinstance(resp, dict):
                        continue
                    if resp.get("id") != req_id:
                        continue
                    self.last_used = time.monotonic()
                    if not resp.get("ok"):
                        err_msg = _scrub_secret(
                            str(resp.get("error") or "TTS 合成失败"), scrub_secret
                        )
                        raise RuntimeError(err_msg)
                    return resp

                cleaned = _scrub_secret(line, scrub_secret)
                recent_lines.append(cleaned)
                if log_stream is not None:
                    try:
                        log_stream.write((cleaned + "\n").encode("utf-8", errors="replace"))
                        log_stream.flush()
                    except OSError:
                        pass
        except (BrokenPipeError, OSError, ValueError):
            self.close()
            _check_cancelled()
            raise _ColabOneWarmWorkerTransportError(
                f"远端 TTS 常驻进程通道已断开（{self.engine_id}）"
            ) from None
        finally:
            if hook_installed and task is not None:
                with task.lock:
                    if getattr(task, "cancel_hook", None) is _cancel_warm:
                        task.cancel_hook = prev_cancel_hook
            if log_stream is not None:
                try:
                    log_stream.close()
                except OSError:
                    pass

    def close(self) -> None:
        with self._lock:
            if self._closed:
                return
            self._closed = True
            proc = self.proc
            self.proc = None
        if proc is not None:
            with self._stdin_lock:
                try:
                    if proc.stdin is not None and not proc.stdin.closed and proc.poll() is None:
                        try:
                            proc.stdin.write(b'{"op":"shutdown"}\n')
                            proc.stdin.flush()
                        except (OSError, ValueError):
                            pass
                    if proc.stdin is not None and not proc.stdin.closed:
                        try:
                            proc.stdin.close()
                        except (OSError, ValueError):
                            pass
                except (OSError, ValueError):
                    pass
            try:
                proc.wait(timeout=1.5)
            except subprocess.TimeoutExpired:
                _terminate_process_group(proc, timeout=3.0)
            except Exception:
                _terminate_process_group(proc, timeout=3.0)
        if self._reader_thread is not None and self._reader_thread.is_alive():
            self._reader_thread.join(timeout=1.0)
        shutil.rmtree(self.work_dir, ignore_errors=True)


def _file_sha256(path: Path) -> str:
    hasher = hashlib.sha256()
    with Path(path).open("rb") as fh:
        while chunk := fh.read(256 * 1024):
            hasher.update(chunk)
    return hasher.hexdigest()


class ColabOneRemoteTtsRunner:
    """Manages remote GPU TTS engine environments and isolated subprocess synthesis."""

    MAX_PREVIEW_AUDIO_BYTES = MAX_PREVIEW_AUDIO_BYTES

    def __init__(
        self,
        runtime: Any,
        engines_by_id: dict[str, Any],
        run_cmd_fn: Callable[..., Any] | None = None,
        synth_fn: Callable[..., Any] | None = None,
    ) -> None:
        self.runtime = runtime
        self.run_cmd_fn = run_cmd_fn
        self.synth_fn = synth_fn
        self.engines_dir = Path(runtime.root) / "tts-engines"
        self.previews_dir = self.engines_dir / "_voice_previews"
        self._lock = threading.RLock()
        self._preview_lock = threading.RLock()
        self._locks_guard = threading.Lock()
        self._preset_locks: dict[str, threading.Lock] = {}
        self._env_locks: dict[str, threading.Lock] = {}

        # Warm daemon worker & background preview prefetcher state.
        self._warm_lock = threading.RLock()
        self._warm_exec_lock = threading.RLock()
        self._warm_worker: ColabOneWarmTtsWorker | None = None
        self._warm_release_gen: int = 0
        self._warm_idle_timer: threading.Timer | None = None
        self._warm_idle_seconds = _COLABONE_WARM_WORKER_IDLE_SECONDS
        self._foreground_waiting = threading.Event()
        self._foreground_done = threading.Condition(threading.Lock())
        self._prefetch_lock = threading.RLock()
        self._prefetch_job: dict[str, Any] | None = None
        self._prefetch_cancel: threading.Event | None = None
        self._prefetch_thread: threading.Thread | None = None
        self._prior_prefetch_threads: list[threading.Thread] = []

        normalized: dict[str, dict[str, Any]] = {}
        for eid, spec in (engines_by_id or {}).items():
            raw = (
                spec.model_dump()
                if hasattr(spec, "model_dump")
                else copy.deepcopy(dict(spec))
            )
            key = str(raw.get("id") or eid).strip()
            raw["id"] = key
            normalized[key] = raw
        self.engines_by_id: dict[str, dict[str, Any]] = normalized

    def list_engines(self) -> list[dict[str, Any]]:
        return [copy.deepcopy(spec) for spec in self.engines_by_id.values()]

    def get_engine(self, engine_id: str) -> dict[str, Any]:
        key = str(engine_id or "").strip()
        if key not in self.engines_by_id:
            raise ValueError(f"未知的远端 TTS 引擎：{engine_id!r}")
        return copy.deepcopy(self.engines_by_id[key])

    def check_vram(
        self, engine: dict[str, Any], *, allow_unavailable: bool = False
    ) -> None:
        min_vram_mib = int(engine.get("min_vram_mib") or 0)
        if min_vram_mib <= 0:
            return
        label = str(engine.get("name") or engine.get("id") or "TTS 引擎")
        gpu_fn = getattr(self.runtime, "gpu_info", None)
        if not callable(gpu_fn):
            if allow_unavailable:
                return
            raise RuntimeError(
                f"未检测到可用远端 GPU，{label} 需要至少 {min_vram_mib} MiB 显存"
            )
        gpu = gpu_fn() or {}
        total_mib = int(gpu.get("total_mib") or 0)
        if total_mib <= 0:
            if allow_unavailable:
                return
            raise RuntimeError(
                f"未检测到可用远端 GPU，{label} 需要至少 {min_vram_mib} MiB 显存"
            )
        if total_mib < min_vram_mib:
            raise RuntimeError(
                f"远端 TTS 引擎 {label} 需要至少 {min_vram_mib} MiB 显存，当前仅 {total_mib} MiB"
            )

    def _get_preset_lock(self, preset_id: str) -> threading.Lock:
        with self._locks_guard:
            lock = self._preset_locks.get(preset_id)
            if lock is None:
                lock = threading.Lock()
                self._preset_locks[preset_id] = lock
            return lock

    def _get_env_lock(self, engine_id: str) -> threading.Lock:
        with self._locks_guard:
            lock = self._env_locks.get(engine_id)
            if lock is None:
                lock = threading.Lock()
                self._env_locks[engine_id] = lock
            return lock

    def _has_hf_repo_cache(self, repo: str, revision: str = "") -> bool:
        repo_str = str(repo or "").strip()
        if not repo_str or "/" not in repo_str or ".." in repo_str or "\\" in repo_str:
            return False
        rev_str = str(revision or "").strip()
        if rev_str and (
            "/" in rev_str
            or "\\" in rev_str
            or ".." in rev_str
            or rev_str in (".", "..")
        ):
            return False
        if self.engines_dir.is_symlink():
            return False
        hf_cache = self.engines_dir / "_hf_cache"
        if hf_cache.is_symlink() or not hf_cache.is_dir():
            return False
        model_dir = hf_cache / ("models--" + repo_str.replace("/", "--"))
        if model_dir.is_symlink() or not model_dir.is_dir():
            return False
        blobs_dir = model_dir / "blobs"
        if blobs_dir.is_symlink():
            return False
        if blobs_dir.is_dir():
            try:
                for entry in blobs_dir.iterdir():
                    if entry.name.endswith(".incomplete"):
                        return False
            except OSError:
                return False
        snapshots_dir = model_dir / "snapshots"
        if snapshots_dir.is_symlink() or not snapshots_dir.is_dir():
            return False
        try:
            model_dir_resolved = model_dir.resolve(strict=True)
        except OSError:
            return False

        def _snapshot_has_weights(snap_dir: Path) -> bool:
            if snap_dir.is_symlink() or not snap_dir.is_dir():
                return False
            index_files: list[tuple[Path, Path]] = []
            has_weight_file = False
            try:
                for root_str, _dirs, files in os.walk(snap_dir, followlinks=False):
                    root_p = Path(root_str)
                    for fname in files:
                        fpath = root_p / fname
                        if fname.lower().endswith(".index.json"):
                            index_files.append((root_p, fpath))
                            continue
                        if (
                            has_weight_file
                            or fpath.suffix.lower() not in _TTS_MODEL_WEIGHT_EXTS
                        ):
                            continue
                        try:
                            resolved = fpath.resolve(strict=True)
                            resolved.relative_to(model_dir_resolved)
                            if resolved.is_file() and resolved.stat().st_size > 0:
                                has_weight_file = True
                        except (OSError, ValueError):
                            continue
            except OSError:
                return False

            if index_files:
                for root_p, idx_path in index_files:
                    try:
                        idx_resolved = idx_path.resolve(strict=True)
                        idx_resolved.relative_to(model_dir_resolved)
                        if not idx_resolved.is_file() or idx_resolved.stat().st_size <= 0:
                            return False
                        idx_data = json.loads(
                            idx_resolved.read_text(encoding="utf-8")
                        )
                    except (OSError, ValueError):
                        return False
                    if not isinstance(idx_data, dict):
                        return False
                    weight_map = idx_data.get("weight_map")
                    if not isinstance(weight_map, dict) or not weight_map:
                        return False
                    for shard_name in set(weight_map.values()):
                        if (
                            not isinstance(shard_name, str)
                            or not shard_name.strip()
                            or "\\" in shard_name
                        ):
                            return False
                        shard_rel = Path(shard_name)
                        if shard_rel.is_absolute() or ".." in shard_rel.parts:
                            return False
                        try:
                            shard_resolved = (root_p / shard_rel).resolve(strict=True)
                            shard_resolved.relative_to(model_dir_resolved)
                            if (
                                not shard_resolved.is_file()
                                or shard_resolved.stat().st_size <= 0
                            ):
                                return False
                        except (OSError, ValueError):
                            return False
                return True

            return has_weight_file

        if rev_str:
            return _snapshot_has_weights(snapshots_dir / rev_str)
        try:
            return any(
                _snapshot_has_weights(child) for child in snapshots_dir.iterdir()
            )
        except OSError:
            return False

    def _has_engine_model_cache(self, engine: dict[str, Any]) -> bool:
        if self.engines_dir.is_symlink():
            return False
        repo = str(engine.get("repo") or "").strip()
        revision = str(engine.get("revision") or "").strip()
        if not self._has_hf_repo_cache(repo, revision):
            return False
        aux_repo = str(engine.get("aux_repo") or "").strip()
        aux_revision = str(engine.get("aux_revision") or "").strip()
        if aux_repo and not self._has_hf_repo_cache(aux_repo, aux_revision):
            return False
        custom_voice_repo = str(engine.get("custom_voice_repo") or "").strip()
        custom_voice_revision = str(engine.get("custom_voice_revision") or "").strip()
        if custom_voice_repo and not self._has_hf_repo_cache(
            custom_voice_repo, custom_voice_revision
        ):
            return False
        return True

    def cached_engine_ids(self) -> list[str]:
        """Return sorted TTS engine IDs whose weights/env are already cached locally (read-only)."""
        if self.engines_dir.is_symlink() or not self.engines_dir.is_dir():
            return []
        cached: list[str] = []
        for eid, spec in self.engines_by_id.items():
            try:
                repo = str(spec.get("repo") or "").strip()
                if repo:
                    if self._has_engine_model_cache(spec):
                        cached.append(eid)
                else:
                    env_dir = self.engines_dir / eid
                    marker = env_dir / ".ready.json"
                    if (
                        not env_dir.is_symlink()
                        and not marker.is_symlink()
                        and marker.is_file()
                        and _find_venv_python_on_disk(env_dir / ".venv") is not None
                    ):
                        cached.append(eid)
            except OSError:
                continue
        return sorted(cached)

    def _estimate_preset_bootstrap_disk_mib(
        self,
        engine: dict[str, Any],
        *,
        ref_audio_path: Path | str | None = None,
        voice_preset: str = "",
        preset: dict[str, Any] | None = None,
    ) -> int:
        """Return uncached VoxCPM2 disk budget (MiB) if engine requires preset anchor synthesis."""
        eid = str(engine.get("id") or "").strip()
        if eid not in _REF_AUDIO_PRESET_ENGINES:
            return 0
        if ref_audio_path is not None and str(ref_audio_path).strip():
            return 0
        target_preset = preset
        if target_preset is None:
            raw_preset = str(voice_preset or "").strip()
            if raw_preset.lower() == "custom":
                return 0
            target_preset = resolve_colabone_voice_preset(
                engine, raw_preset or DEFAULT_COLABONE_VOICE_PRESET
            )
        if target_preset is None:
            return 0
        if self._is_preset_reference_cached(target_preset):
            return 0
        vox_spec = self.engines_by_id.get("voxcpm2")
        if vox_spec is not None:
            if self._has_engine_model_cache(vox_spec):
                return 0
            vox_mib = int(vox_spec.get("min_disk_mib") or 0)
            return vox_mib if vox_mib > 0 else _VOXCPM2_BOOTSTRAP_DISK_MIB
        return _VOXCPM2_BOOTSTRAP_DISK_MIB

    def check_disk(
        self,
        engine: dict[str, Any],
        *,
        allow_unavailable: bool = False,
        ref_audio_path: Path | str | None = None,
        voice_preset: str = "",
        preset: dict[str, Any] | None = None,
    ) -> None:
        if self.engines_dir.is_symlink():
            raise ValueError("TTS 引擎目录不能是符号链接")
        if allow_unavailable:
            return
        min_disk_mib = max(0, int(engine.get("min_disk_mib") or 0))
        engine_needed_mib = (
            0
            if (min_disk_mib <= 0 or self._has_engine_model_cache(engine))
            else min_disk_mib
        )
        bootstrap_needed_mib = self._estimate_preset_bootstrap_disk_mib(
            engine,
            ref_audio_path=ref_audio_path,
            voice_preset=voice_preset,
            preset=preset,
        )
        required_mib = engine_needed_mib + bootstrap_needed_mib
        if required_mib <= 0:
            return
        label = str(engine.get("name") or engine.get("id") or "TTS 引擎")
        usage_path = Path("/")
        for candidate in (self.engines_dir, *self.engines_dir.parents):
            if candidate.exists():
                usage_path = candidate
                break
        try:
            free_bytes = int(shutil.disk_usage(usage_path).free)
        except OSError:
            raise RuntimeError(
                f"无法检测远端磁盘空间，{label} 需要至少 {required_mib} MiB 可用磁盘空间"
            )
        required_bytes = required_mib * 1024 * 1024
        if free_bytes < required_bytes:
            free_mib = free_bytes // (1024 * 1024)
            req_gib = required_mib / 1024.0
            free_gib = free_bytes / (1024.0**3)
            extra_note = (
                "（含首次内置音色锚点 VoxCPM2 缓存）"
                if bootstrap_needed_mib > 0
                else ""
            )
            raise RuntimeError(
                f"远端磁盘空间不足：TTS 引擎 {label}{extra_note} 至少需要 {req_gib:.1f} GiB"
                f"（{required_mib} MiB）可用空间，当前仅剩 {free_gib:.1f} GiB（{free_mib} MiB）"
            )

    def _require_venv_python(self, venv_dir: Path, *, label: str) -> Path:
        found = _find_venv_python_on_disk(venv_dir)
        if found is not None:
            return found
        if self.run_cmd_fn is not None:
            return _resolve_venv_python(venv_dir)
        raise RuntimeError(
            f"{label} 隔离虚拟环境缺少 Python 可执行文件，拒绝回退到宿主环境：{venv_dir}"
        )

    @staticmethod
    def _verify_git_checkout_head(
        source_dir: Path,
        expected_commit: str,
        *,
        env: dict[str, str] | None = None,
    ) -> bool:
        """Verify an existing Git checkout has intact HEAD matching `expected_commit`."""
        git_dir = source_dir / ".git"
        if source_dir.is_symlink() or git_dir.is_symlink() or not git_dir.is_dir():
            return False
        try:
            has_worktree = any(p.name != ".git" for p in source_dir.iterdir())
        except OSError:
            return False
        if not has_worktree:
            return False
        try:
            res = subprocess.run(
                ["git", "-C", str(source_dir), "rev-parse", "--verify", "HEAD"],
                capture_output=True,
                text=True,
                timeout=30,
                stdin=subprocess.DEVNULL,
                env=env,
                check=False,
            )
        except (OSError, subprocess.SubprocessError):
            return False
        return (
            res.returncode == 0
            and res.stdout.strip().lower() == expected_commit.strip().lower()
        )

    @staticmethod
    def _needs_source_checkout(engine: dict[str, Any]) -> bool:
        source_repo = str(engine.get("source_repo") or "").strip()
        source_commit = str(engine.get("source_commit") or "").strip()
        if not source_repo or not source_commit:
            return False
        engine_id = str(engine.get("id") or "").strip()
        if engine_id in _SOURCE_CHECKOUT_ENGINES:
            return True
        if bool(engine.get("require_source_checkout")):
            return True
        return engine_id not in _PIP_PACKAGED_ENGINES

    def _invoke_command(
        self,
        args: Sequence[str],
        task: Any,
        log: Path,
        *,
        timeout: int = 1800,
        cwd: Path | str | None = None,
        env: dict[str, str] | None = None,
        secret_to_scrub: str = "",
        append_log: bool = False,
    ) -> None:
        stopping = getattr(self.runtime, "stopping", None)
        if self.run_cmd_fn is not None:
            _call_with_compatible_kwargs(
                self.run_cmd_fn,
                (list(args),),
                {
                    "task": task,
                    "log": log,
                    "timeout": timeout,
                    "cwd": cwd,
                    "env": env,
                    "stopping": stopping,
                },
            )
            return
        _run_tts_subprocess(
            args,
            task,
            log,
            timeout=timeout,
            cwd=cwd,
            env=env,
            stopping=stopping,
            secret_to_scrub=secret_to_scrub,
            append_log=append_log,
        )

    def ensure_engine_env(
        self, task: Any, engine: dict[str, Any], hf_token: str = ""
    ) -> Path:
        """Create isolated per-engine venv, clone pinned source when needed, and install deps in one pass."""
        engine_id = str(engine.get("id") or "").strip()
        if not _ENGINE_ID_RE.fullmatch(engine_id):
            raise ValueError(f"非法的 TTS 引擎编号：{engine_id!r}")
        engine_name = str(engine.get("name") or engine_id)

        filtered_packages = _filter_pip_packages(engine.get("pip_packages") or [])
        needs_source = self._needs_source_checkout(engine)
        source_repo = str(engine.get("source_repo") or "").strip()
        source_commit = str(engine.get("source_commit") or "").strip()
        repo_url = ""
        if needs_source:
            if not _GIT_COMMIT_SHA_RE.fullmatch(source_commit):
                raise ValueError(
                    f"TTS 引擎源码提交哈希必须为完整 40 位十六进制 SHA：{source_commit!r}"
                )
            repo_url = _normalize_git_repo_url(source_repo)
        elif source_commit and not _GIT_COMMIT_SHA_RE.fullmatch(source_commit):
            raise ValueError(
                f"TTS 引擎源码提交哈希必须为完整 40 位十六进制 SHA：{source_commit!r}"
            )

        if self.engines_dir.is_symlink():
            raise ValueError("TTS 引擎目录不能是符号链接")
        self.engines_dir.mkdir(parents=True, exist_ok=True)
        env_dir = self.engines_dir / engine_id
        if env_dir.is_symlink():
            raise ValueError("TTS 引擎子目录不能是符号链接")
        env_dir.mkdir(parents=True, exist_ok=True)

        marker_path = env_dir / ".ready.json"
        venv_dir = env_dir / ".venv"
        if venv_dir.is_symlink():
            raise ValueError("TTS 引擎虚拟环境目录不能是符号链接")
        source_dir = env_dir / "source"
        torch_constraints = _collect_installed_torch_constraints()

        req_filename = _ENGINE_REQUIREMENTS_FILES.get(engine_id, "requirements.txt")
        expected_fingerprint = {
            "engine_id": engine_id,
            "repo": str(engine.get("repo") or ""),
            "revision": str(engine.get("revision") or ""),
            "aux_repo": str(engine.get("aux_repo") or ""),
            "aux_revision": str(engine.get("aux_revision") or ""),
            "custom_voice_repo": str(engine.get("custom_voice_repo") or ""),
            "custom_voice_revision": str(engine.get("custom_voice_revision") or ""),
            "source_repo": source_repo,
            "source_commit": source_commit,
            "pip_packages": filtered_packages,
            "needs_source_checkout": needs_source,
            "requirements_file": req_filename if needs_source else "",
            "venv_mode": "system-site-packages",
            "torch_constraints": torch_constraints,
        }

        with self._get_env_lock(engine_id):
            if marker_path.is_symlink():
                raise ValueError("TTS 就绪标记文件不能是符号链接")
            sub_env = _sanitized_tts_env()
            venv_present = (
                (
                    (venv_dir / "pyvenv.cfg").is_file()
                    and _find_venv_python_on_disk(venv_dir) is not None
                )
                or self.run_cmd_fn is not None
            )
            marker_mismatch = False
            if marker_path.is_file():
                try:
                    cached = json.loads(marker_path.read_text(encoding="utf-8"))
                    source_ok = (
                        not needs_source
                        or (
                            source_dir.is_dir()
                            and not source_dir.is_symlink()
                            and (
                                self.run_cmd_fn is not None
                                or self._verify_git_checkout_head(
                                    source_dir, source_commit, env=sub_env
                                )
                            )
                        )
                    )
                    if (
                        isinstance(cached, dict)
                        and all(
                            cached.get(k) == v for k, v in expected_fingerprint.items()
                        )
                        and venv_present
                        and source_ok
                    ):
                        _ensure_parent_venv_pth(venv_dir)
                        self._require_venv_python(venv_dir, label=engine_name)
                        _safe_task_note(
                            task, f"复用已就绪的远端 TTS 引擎环境：{engine_name}"
                        )
                        return env_dir
                except (OSError, ValueError):
                    pass
                marker_mismatch = True
                try:
                    marker_path.unlink(missing_ok=True)
                except OSError:
                    pass

            _check_tts_cancel(task, getattr(self.runtime, "stopping", None))
            log_path = env_dir / "setup.log"
            if log_path.is_symlink():
                raise ValueError("TTS 安装日志路径不能是符号链接")

            if marker_mismatch and (self.run_cmd_fn is None or venv_dir.exists()):
                if venv_dir.exists():
                    shutil.rmtree(venv_dir, ignore_errors=True)
                has_venv_files = False
            else:
                has_venv_files = (
                    (venv_dir / "pyvenv.cfg").is_file()
                    and _find_venv_python_on_disk(venv_dir) is not None
                )
            if not has_venv_files:
                if venv_dir.exists():
                    shutil.rmtree(venv_dir, ignore_errors=True)
                msg = f"正在为远端 TTS 引擎创建隔离虚拟环境：{engine_name}"
                _safe_task_update(task, message=msg)
                _safe_task_note(task, msg)
                try:
                    self._invoke_command(
                        [
                            sys.executable,
                            "-m",
                            "venv",
                            "--without-pip",
                            "--system-site-packages",
                            str(venv_dir),
                        ],
                        task,
                        log_path,
                        timeout=300,
                        env=sub_env,
                        secret_to_scrub=hf_token,
                        append_log=True,
                    )
                    _ensure_parent_venv_pth(venv_dir)
                    venv_python_on_disk = _find_venv_python_on_disk(venv_dir)
                    if venv_python_on_disk is not None:
                        try:
                            self._invoke_command(
                                [str(venv_python_on_disk), "-m", "pip", "--version"],
                                task,
                                log_path,
                                timeout=120,
                                env=sub_env,
                                secret_to_scrub=hf_token,
                                append_log=True,
                            )
                        except RuntimeError as exc:
                            if _is_cancelled_error(exc):
                                raise
                            self._invoke_command(
                                [
                                    str(venv_python_on_disk),
                                    "-m",
                                    "ensurepip",
                                    "--upgrade",
                                ],
                                task,
                                log_path,
                                timeout=180,
                                env=sub_env,
                                secret_to_scrub=hf_token,
                                append_log=True,
                            )
                    elif self.run_cmd_fn is None:
                        raise RuntimeError(
                            f"远端 TTS 引擎虚拟环境创建不完整，缺少 Python 可执行文件：{venv_dir}"
                        )
                except BaseException:
                    shutil.rmtree(venv_dir, ignore_errors=True)
                    raise
            else:
                _ensure_parent_venv_pth(venv_dir)

            venv_python = str(self._require_venv_python(venv_dir, label=engine_name))
            constraints_path = _write_torch_constraints_file(
                env_dir / ".colabone-torch-constraints.txt",
                torch_constraints,
            )

            source_deps: list[str] = []
            if needs_source:
                if self.run_cmd_fn is None and not shutil.which("git"):
                    raise RuntimeError(
                        f"远端缺少 Git，无法拉取 TTS 引擎源码：{engine_name}"
                    )
                if source_dir.is_symlink():
                    raise ValueError("TTS 源码目录不能是符号链接")
                git_dir = source_dir / ".git"
                if git_dir.is_symlink():
                    raise ValueError("TTS Git 元数据目录不能是符号链接")

                def _checkout_source_once(*, fresh: bool) -> None:
                    if fresh and source_dir.exists():
                        if source_dir.is_symlink():
                            raise ValueError("TTS 源码目录不能是符号链接")
                        if source_dir.is_dir():
                            shutil.rmtree(source_dir, ignore_errors=True)
                        else:
                            source_dir.unlink(missing_ok=True)
                    if source_dir.is_symlink():
                        raise ValueError("TTS 源码目录不能是符号链接")
                    if git_dir.is_symlink():
                        raise ValueError("TTS Git 元数据目录不能是符号链接")
                    if git_dir.exists() and not git_dir.is_dir():
                        shutil.rmtree(source_dir, ignore_errors=True)
                    source_dir.mkdir(parents=True, exist_ok=True)
                    if git_dir.is_dir():
                        for lock_file in git_dir.rglob("*.lock"):
                            try:
                                if not lock_file.is_symlink():
                                    lock_file.unlink(missing_ok=True)
                            except OSError:
                                pass
                        try:
                            self._invoke_command(
                                [
                                    "git",
                                    "-C",
                                    str(source_dir),
                                    "remote",
                                    "set-url",
                                    "--",
                                    "origin",
                                    repo_url,
                                ],
                                task,
                                log_path,
                                timeout=300,
                                env=sub_env,
                                secret_to_scrub=hf_token,
                                append_log=True,
                            )
                        except RuntimeError as exc:
                            if _is_cancelled_error(exc):
                                raise
                            self._invoke_command(
                                [
                                    "git",
                                    "-C",
                                    str(source_dir),
                                    "remote",
                                    "add",
                                    "--",
                                    "origin",
                                    repo_url,
                                ],
                                task,
                                log_path,
                                timeout=300,
                                env=sub_env,
                                secret_to_scrub=hf_token,
                                append_log=True,
                            )
                    else:
                        self._invoke_command(
                            ["git", "init", "--", str(source_dir)],
                            task,
                            log_path,
                            timeout=300,
                            env=sub_env,
                            secret_to_scrub=hf_token,
                            append_log=True,
                        )
                        self._invoke_command(
                            [
                                "git",
                                "-C",
                                str(source_dir),
                                "remote",
                                "add",
                                "--",
                                "origin",
                                repo_url,
                            ],
                            task,
                            log_path,
                            timeout=300,
                            env=sub_env,
                            secret_to_scrub=hf_token,
                            append_log=True,
                        )

                    self._invoke_command(
                        [
                            "git",
                            "-C",
                            str(source_dir),
                            "fetch",
                            "--depth",
                            "1",
                            "--",
                            "origin",
                            source_commit,
                        ],
                        task,
                        log_path,
                        timeout=600,
                        env=sub_env,
                        secret_to_scrub=hf_token,
                        append_log=True,
                    )
                    self._invoke_command(
                        [
                            "git",
                            "-C",
                            str(source_dir),
                            "checkout",
                            "--force",
                            "--detach",
                            source_commit,
                        ],
                        task,
                        log_path,
                        timeout=300,
                        env=sub_env,
                        secret_to_scrub=hf_token,
                        append_log=True,
                    )
                    self._invoke_command(
                        [
                            "git",
                            "-C",
                            str(source_dir),
                            "clean",
                            "-ffdx",
                        ],
                        task,
                        log_path,
                        timeout=300,
                        env=sub_env,
                        secret_to_scrub=hf_token,
                        append_log=True,
                    )
                    if self.run_cmd_fn is None and not self._verify_git_checkout_head(
                        source_dir, source_commit, env=sub_env
                    ):
                        raise RuntimeError(
                            f"{engine_name} 源码检出校验失败，HEAD 与目标提交 {source_commit[:10]} 不一致"
                        )

                msg = f"正在拉取远端 TTS 引擎源码：{engine_name}@{source_commit[:10]}"
                _safe_task_update(task, message=msg)
                _safe_task_note(task, msg)

                had_existing_source = False
                try:
                    had_existing_source = source_dir.exists() and (
                        not source_dir.is_dir() or any(source_dir.iterdir())
                    )
                except OSError:
                    had_existing_source = source_dir.exists()
                try:
                    _checkout_source_once(fresh=False)
                except Exception as exc:
                    if _is_cancelled_error(exc) or not had_existing_source:
                        raise
                    _check_tts_cancel(task, getattr(self.runtime, "stopping", None))
                    _safe_task_note(
                        task,
                        f"{engine_name} 现有 Git 仓库异常，正在清空并重新检出：{str(exc)[:240]}",
                        level="warning",
                    )
                    _checkout_source_once(fresh=True)

                req_file = source_dir / req_filename
                req_specs: list[str] = []
                if req_file.is_file() and not req_file.is_symlink():
                    req_specs = _extract_requirements_file_packages(req_file)
                elif req_filename != "requirements.txt":
                    fallback_req = source_dir / "requirements.txt"
                    if fallback_req.is_file() and not fallback_req.is_symlink():
                        req_specs = _extract_requirements_file_packages(fallback_req)
                pyproject_specs: list[str] = []
                pyproject_file = source_dir / "pyproject.toml"
                if pyproject_file.is_file() and not pyproject_file.is_symlink():
                    pyproject_specs = _extract_pyproject_dependencies(pyproject_file)
                source_deps = _merge_requirement_specs(req_specs, pyproject_specs)

            merged_packages = _merge_requirement_specs(filtered_packages, source_deps)

            if merged_packages:
                merged_req = env_dir / ".colabone-requirements.txt"
                if merged_req.is_symlink():
                    raise ValueError("TTS 依赖清单文件不能是符号链接")
                _atomic_write_text(merged_req, "\n".join(merged_packages) + "\n")
                msg = (
                    f"正在安装远端 TTS 引擎依赖：{engine_name}"
                    f"（共 {len(merged_packages)} 个包，保留现有 PyTorch）"
                )
                _safe_task_update(task, message=msg)
                _safe_task_note(task, msg)
                self._invoke_command(
                    [
                        venv_python,
                        "-m",
                        "pip",
                        "install",
                        "--no-input",
                        "--disable-pip-version-check",
                        "-c",
                        str(constraints_path),
                        "-r",
                        str(merged_req),
                    ],
                    task,
                    log_path,
                    timeout=1200,
                    env=sub_env,
                    secret_to_scrub=hf_token,
                    append_log=True,
                )

            marker_payload = dict(expected_fingerprint)
            marker_payload["ready_at"] = time.time()
            atomic_json(marker_path, marker_payload)
            _safe_task_note(task, f"远端 TTS 引擎环境就绪：{engine_name}")
            return env_dir

    def _prepare_ref_audio_wav(
        self,
        task: Any,
        src_path: Path,
        work_root: Path,
        role: str,
        target_sr: int = 24000,
    ) -> Path:
        """Ensure reference audio is standard mono 16-bit PCM WAV; transcode via ffmpeg when needed."""
        if src_path.suffix.lower() == ".wav" and _is_standard_mono_pcm16_wav(src_path):
            return src_path
        if self.synth_fn is not None and self.run_cmd_fn is None:
            return src_path
        sr = max(24000, int(target_sr or 24000))
        dst_path = work_root / f"ref_{role}.wav"
        if dst_path.is_symlink():
            raise ValueError("参考音频转码目标不能是符号链接")
        if self.run_cmd_fn is None and not shutil.which("ffmpeg"):
            raise RuntimeError(
                f"远端缺少 ffmpeg，无法将参考音频转换为标准 WAV 格式：{src_path.name}"
            )
        ffmpeg_env = _sanitized_tts_env()
        _safe_task_note(
            task,
            f"正在将参考音频转码为标准 {sr} Hz 单声道 PCM16 WAV：{src_path.name}",
        )
        self._invoke_command(
            [
                "ffmpeg",
                "-y",
                "-v",
                "error",
                "-protocol_whitelist",
                "file,pipe",
                "-i",
                str(src_path),
                "-ac",
                "1",
                "-ar",
                str(sr),
                "-c:a",
                "pcm_s16le",
                str(dst_path),
            ],
            task,
            work_root / f"transcode_{role}.log",
            timeout=300,
            cwd=work_root,
            env=ffmpeg_env,
        )
        if self.run_cmd_fn is None and (
            not dst_path.is_file()
            or dst_path.stat().st_size <= 0
            or not _is_standard_mono_pcm16_wav(dst_path)
        ):
            raise RuntimeError(
                f"参考音频转码失败，未生成有效的单声道 PCM16 WAV：{dst_path}"
            )
        return dst_path

    def build_worker_script(self, engine_id: str) -> str:
        """Generate a self-contained Python worker script for one of the 8 TTS engines."""
        key = str(engine_id or "").strip()
        body = _WORKER_BODIES.get(key)
        if body is None:
            raise ValueError(f"不支持的 TTS 引擎脚本：{engine_id!r}")
        return _WORKER_COMMON_HEADER + body

    def _preset_anchor_fingerprint(
        self, preset: dict[str, Any]
    ) -> tuple[str, str, int, dict[str, Any]]:
        preset_id = str(preset.get("id") or "").strip()
        canonical = COLABONE_TTS_VOICE_PRESETS.get(preset_id) or {}
        vox_prompt = str(
            preset.get("voxcpm_prompt")
            or canonical.get("voxcpm_prompt")
            or preset.get("prompt")
            or COLABONE_TTS_VOICE_PRESETS[DEFAULT_COLABONE_VOICE_PRESET]["voxcpm_prompt"]
        ).strip()
        raw_seed = preset.get("seed", canonical.get("seed", 42))
        preset_seed = (
            int(raw_seed)
            if isinstance(raw_seed, int)
            and not isinstance(raw_seed, bool)
            and raw_seed >= 0
            else 42
        )
        vox_revision = str((self.engines_by_id.get("voxcpm2") or {}).get("revision") or "")
        anchor_text = COLABONE_VOICE_PRESET_ANCHOR_TEXT
        expected_meta = {
            "voxcpm_revision": vox_revision,
            "vox_prompt": vox_prompt,
            "preset_seed": preset_seed,
            "anchor_text": anchor_text,
        }
        return preset_id, vox_prompt, preset_seed, expected_meta

    def _preset_cache_matches(
        self,
        wav_path: Path,
        txt_path: Path,
        meta_path: Path,
        expected_meta: dict[str, Any],
    ) -> bool:
        if wav_path.is_symlink() or txt_path.is_symlink() or meta_path.is_symlink():
            raise ValueError("内置音色缓存文件不能是符号链接")
        if (
            not wav_path.is_file()
            or wav_path.stat().st_size <= 0
            or not txt_path.is_file()
            or not meta_path.is_file()
        ):
            return False
        if self.run_cmd_fn is None and not _is_standard_mono_pcm16_wav(wav_path):
            return False
        try:
            meta = json.loads(meta_path.read_text(encoding="utf-8"))
        except (OSError, ValueError):
            return False
        if not isinstance(meta, dict):
            return False
        return all(meta.get(k) == v for k, v in expected_meta.items())

    def _is_preset_reference_cached(self, preset: dict[str, Any]) -> bool:
        preset_id, _, _, expected_meta = self._preset_anchor_fingerprint(preset)
        if not _VOICE_PRESET_ID_RE.fullmatch(preset_id):
            return False
        if self.engines_dir.is_symlink():
            return False
        presets_dir = self.engines_dir / "_voice_presets"
        if presets_dir.is_symlink() or not presets_dir.is_dir():
            return False
        wav_path = presets_dir / f"{preset_id}.wav"
        txt_path = presets_dir / f"{preset_id}.txt"
        meta_path = presets_dir / f"{preset_id}.meta.json"
        try:
            return self._preset_cache_matches(wav_path, txt_path, meta_path, expected_meta)
        except (OSError, ValueError):
            return False

    def _ensure_preset_reference_audio(
        self,
        task: Any,
        preset: dict[str, Any],
        *,
        hf_token: str = "",
    ) -> tuple[Path, str]:
        """Return a cached 48 kHz studio WAV anchor & transcript, synthesizing once via VoxCPM2."""
        hf_token = str(hf_token or "").strip()
        preset_id, vox_prompt, preset_seed, expected_meta = (
            self._preset_anchor_fingerprint(preset)
        )
        if not _VOICE_PRESET_ID_RE.fullmatch(preset_id):
            raise ValueError(f"非法的内置音色预设编号：{preset_id!r}")
        preset_name = str(preset.get("name") or preset_id).strip()
        anchor_text = COLABONE_VOICE_PRESET_ANCHOR_TEXT

        if self.engines_dir.is_symlink():
            raise ValueError("TTS 引擎目录不能是符号链接")
        self.engines_dir.mkdir(parents=True, exist_ok=True)
        presets_dir = self.engines_dir / "_voice_presets"
        if presets_dir.is_symlink():
            raise ValueError("内置音色缓存目录不能是符号链接")
        presets_dir.mkdir(parents=True, exist_ok=True)

        wav_path = presets_dir / f"{preset_id}.wav"
        txt_path = presets_dir / f"{preset_id}.txt"
        meta_path = presets_dir / f"{preset_id}.meta.json"
        if wav_path.is_symlink() or txt_path.is_symlink() or meta_path.is_symlink():
            raise ValueError("内置音色缓存文件不能是符号链接")

        preset_lock = self._get_preset_lock(preset_id)
        with preset_lock:
            if self._preset_cache_matches(wav_path, txt_path, meta_path, expected_meta):
                cached_text = txt_path.read_text(encoding="utf-8").strip() or anchor_text
                _safe_task_note(task, f"复用已缓存的内置音色参考样本：{preset_name}")
                return wav_path, cached_text

            check_fn = getattr(task, "check", None)
            if callable(check_fn):
                check_fn()

            if "voxcpm2" not in self.engines_by_id:
                raise RuntimeError(
                    f"缺少 VoxCPM2 引擎配置，无法生成内置音色参考样本：{preset_name}"
                )
            vox_engine = self.get_engine("voxcpm2")
            self.check_vram(
                vox_engine,
                allow_unavailable=(self.synth_fn is not None or self.run_cmd_fn is not None),
            )
            self.check_disk(
                vox_engine,
                allow_unavailable=(self.synth_fn is not None or self.run_cmd_fn is not None),
            )

            gen_msg = f"正在生成并缓存内置音色参考样本：{preset_name}"
            _safe_task_update(task, message=gen_msg)
            _safe_task_note(task, gen_msg)

            tmp_work = Path(
                tempfile.mkdtemp(prefix=f".preset-{preset_id}-", dir=str(presets_dir))
            )
            tmp_wav_target: Path | None = None
            try:
                raw_anchor_wav = tmp_work / "anchor_raw.wav"
                seg_entries = [
                    {
                        "index": 0,
                        "text": anchor_text,
                        "output_wav": str(raw_anchor_wav),
                    }
                ]
                anchor_config: dict[str, Any] = {
                    "engine_id": "voxcpm2",
                    "repo": str(vox_engine.get("repo") or ""),
                    "revision": str(vox_engine.get("revision") or ""),
                    "aux_repo": str(vox_engine.get("aux_repo") or ""),
                    "aux_revision": str(vox_engine.get("aux_revision") or ""),
                    "custom_voice_repo": str(vox_engine.get("custom_voice_repo") or ""),
                    "custom_voice_revision": str(vox_engine.get("custom_voice_revision") or ""),
                    "source_dir": "",
                    "cache_dir": str(self.engines_dir / "_hf_cache"),
                    "sample_rate": int(vox_engine.get("sample_rate") or 48000),
                    "segments": seg_entries,
                    "ref_audio_path": "",
                    "ref_text": "",
                    "voice_prompt": vox_prompt,
                    "voice_preset": preset_id,
                    "voice_seed": preset_seed,
                    "speaker_id": "",
                    "emotion_ref_path": "",
                    "emotion_prompt": "",
                }

                generated_wav = raw_anchor_wav
                if self.synth_fn is not None:
                    segment_wavs = self._invoke_synth_fn(
                        task=task,
                        engine=vox_engine,
                        segments=[anchor_text],
                        segments_dir=tmp_work,
                        seg_entries=seg_entries,
                        config=anchor_config,
                        ref_audio_path=None,
                        ref_text="",
                        voice_prompt=vox_prompt,
                        voice_preset=preset_id,
                        voice_seed=preset_seed,
                        speaker_id="",
                        emotion_ref_path=None,
                        emotion_prompt="",
                        hf_token=hf_token,
                    )
                    if segment_wavs:
                        generated_wav = Path(segment_wavs[0][1])
                else:
                    env_dir = self.ensure_engine_env(task, vox_engine, hf_token="")
                    worker_python = str(
                        self._require_venv_python(env_dir / ".venv", label="VoxCPM2")
                    )
                    source_dir = env_dir / "source"
                    if source_dir.is_symlink():
                        raise ValueError("TTS 源码目录不能是符号链接")
                    if source_dir.is_dir():
                        anchor_config["source_dir"] = str(source_dir)

                    log_path = tmp_work / "preset_voxcpm2.log"
                    if log_path.is_symlink():
                        raise ValueError("内置音色生成临时路径不能是符号链接")

                    used_warm_anchor = False
                    if self._should_use_warm_worker():
                        try:
                            with self._warm_exec_lock:
                                worker = self._ensure_warm_worker_locked(
                                    task, vox_engine, hf_token=hf_token
                                )
                                worker.call(
                                    {"op": "synthesize", "config": anchor_config},
                                    task=task,
                                    timeout=1800.0,
                                    log_path=log_path,
                                    secret_to_scrub=hf_token,
                                )
                                self._schedule_warm_idle_timer()
                            used_warm_anchor = raw_anchor_wav.is_file()
                        except _ColabOneWarmWorkerTransportError:
                            self.release_warm_worker("anchor_fallback_oneshot")
                            used_warm_anchor = False
                        except Exception as exc:
                            if _is_cancelled_error(exc):
                                raise
                            if _is_warm_worker_cuda_fatal_error(exc):
                                self.release_warm_worker("anchor_cuda_fatal")
                            raise

                    if not used_warm_anchor:
                        self.release_warm_worker(reason="before_oneshot_anchor")
                        worker_path = tmp_work / "worker_voxcpm2.py"
                        config_path = tmp_work / "config_voxcpm2.json"
                        if worker_path.is_symlink() or config_path.is_symlink():
                            raise ValueError("内置音色生成临时路径不能是符号链接")

                        worker_path.write_text(
                            self.build_worker_script("voxcpm2"), encoding="utf-8"
                        )
                        atomic_json(config_path, anchor_config)

                        hf_cache_dir = str(self.engines_dir / "_hf_cache")
                        worker_env = _build_tts_worker_env(
                            hf_cache_dir, hf_token=hf_token
                        )

                        self._invoke_command(
                            [worker_python, str(worker_path), "--config", str(config_path)],
                            task,
                            log_path,
                            timeout=1800,
                            cwd=tmp_work,
                            env=worker_env,
                            secret_to_scrub=hf_token,
                        )

                if not generated_wav.is_file() or generated_wav.stat().st_size <= 0:
                    raise RuntimeError(f"内置音色参考样本生成失败：{preset_name}")

                fd, tmp_wav_str = tempfile.mkstemp(
                    prefix=f".{preset_id}-", suffix=".wav.tmp", dir=str(presets_dir)
                )
                os.close(fd)
                tmp_wav_target = Path(tmp_wav_str)

                try:
                    assemble_colabone_tts_master(
                        [(anchor_text, generated_wav)],
                        out_wav=tmp_wav_target,
                        manifest_path=tmp_work / "anchor_manifest.json",
                        pause_seconds=0.0,
                        target_sr=48000,
                        target_lufs=-23.0,
                    )
                except (wave.Error, EOFError, ValueError):
                    if self.run_cmd_fn is None:
                        raise
                    shutil.copyfile(generated_wav, tmp_wav_target)

                with open(tmp_wav_target, "r+b") as stream:
                    os.fsync(stream.fileno())
                with self._lock:
                    if (
                        wav_path.is_symlink()
                        or txt_path.is_symlink()
                        or meta_path.is_symlink()
                    ):
                        raise ValueError("内置音色缓存文件不能是符号链接")
                    os.replace(tmp_wav_target, wav_path)
                    tmp_wav_target = None
                    _atomic_write_text(txt_path, anchor_text + "\n")
                    atomic_json(meta_path, expected_meta)
                return wav_path, anchor_text
            finally:
                if tmp_wav_target is not None and tmp_wav_target.exists():
                    try:
                        tmp_wav_target.unlink()
                    except OSError:
                        pass
                shutil.rmtree(tmp_work, ignore_errors=True)

    def _invoke_synth_fn(
        self,
        *,
        task: Any,
        engine: dict[str, Any],
        segments: list[str],
        segments_dir: Path,
        seg_entries: list[dict[str, Any]],
        config: dict[str, Any],
        ref_audio_path: Path | None,
        ref_text: str,
        voice_prompt: str,
        emotion_ref_path: Path | None,
        emotion_prompt: str,
        hf_token: str,
        voice_preset: str = "",
        voice_seed: int | None = None,
        speaker_id: str = "",
    ) -> list[tuple[str, Path]]:
        assert self.synth_fn is not None
        fn = self.synth_fn
        result: Any

        try:
            sig = inspect.signature(fn)
            params = list(sig.parameters.values())
            first_name = params[0].name if params else ""
        except (TypeError, ValueError):
            params = []
            first_name = ""

        kw_pool: dict[str, Any] = {
            "task": task,
            "engine": copy.deepcopy(engine),
            "engine_id": engine["id"],
            "segments": list(segments),
            "segments_dir": segments_dir,
            "work_dir": segments_dir.parent,
            "ref_audio_path": ref_audio_path,
            "ref_text": ref_text,
            "voice_prompt": voice_prompt,
            "voice_preset": voice_preset,
            "voice_seed": voice_seed,
            "speaker_id": speaker_id,
            "emotion_ref_path": emotion_ref_path,
            "emotion_prompt": emotion_prompt,
            "hf_token": hf_token,
            "config": copy.deepcopy(config),
        }

        if len(params) == 1 and first_name in ("config", "payload"):
            result = fn(copy.deepcopy(config))
        elif first_name == "task":
            result = _call_with_compatible_kwargs(
                fn,
                (task, copy.deepcopy(engine), list(segments), segments_dir),
                kw_pool,
            )
        else:
            result = _call_with_compatible_kwargs(
                fn,
                (copy.deepcopy(engine), list(segments), segments_dir),
                kw_pool,
            )

        if result is None:
            return [(str(s["text"]), Path(s["output_wav"])) for s in seg_entries]

        segment_wavs: list[tuple[str, Path]] = []
        if isinstance(result, (list, tuple)):
            for idx, item in enumerate(result):
                if isinstance(item, (list, tuple)) and len(item) == 2:
                    segment_wavs.append((str(item[0]), Path(item[1])))
                else:
                    seg_text = segments[idx] if idx < len(segments) else ""
                    segment_wavs.append((seg_text, Path(item)))
            return segment_wavs

        raise RuntimeError(f"synth_fn 返回了不支持的结果类型：{type(result).__name__}")

    def compute_preview_id(
        self,
        engine_id: str,
        *,
        voice_preset: str = "",
        preview_text: str = "",
        voice_prompt: str = "",
        emotion_prompt: str = "",
        ref_audio_sha256: str = "",
        ref_text: str = "",
        emotion_ref_sha256: str = "",
    ) -> tuple[str, str]:
        """Compute deterministic 32-char hex preview ID and resolved audition text."""
        engine = self.get_engine(engine_id)
        norm_engine_id = str(engine["id"])
        raw_preset = str(voice_preset or "").strip()
        norm_raw_preset = "custom" if raw_preset.lower() == "custom" else raw_preset
        norm_ref_sha = str(ref_audio_sha256 or "").strip().lower()
        norm_emo_sha = str(emotion_ref_sha256 or "").strip().lower()
        norm_ref_text = str(ref_text or "").strip() if norm_ref_sha else ""

        resolved_preset = (
            None
            if norm_ref_sha
            else resolve_colabone_voice_preset(engine, norm_raw_preset)
        )
        custom_text = str(preview_text or "").strip()
        if custom_text:
            resolved_text = custom_text
        elif resolved_preset is not None and str(
            resolved_preset.get("preview_text") or ""
        ).strip():
            resolved_text = str(resolved_preset["preview_text"]).strip()
        else:
            resolved_text = COLABONE_VOICE_PRESET_ANCHOR_TEXT

        effective_preset = (
            ""
            if norm_ref_sha
            else (
                str(resolved_preset["id"])
                if resolved_preset is not None
                else norm_raw_preset
            )
        )
        resolved_preset_prompt = (
            str(resolved_preset.get("prompt") or "").strip()
            if resolved_preset is not None
            else ""
        )
        resolved_preset_seed = (
            int(resolved_preset["seed"])
            if resolved_preset is not None and resolved_preset.get("seed") is not None
            else None
        )
        resolved_preset_speaker = (
            str(resolved_preset.get("speaker_id") or "").strip()
            if resolved_preset is not None
            else ""
        )
        payload = [
            norm_engine_id,
            str(engine.get("repo") or ""),
            str(engine.get("revision") or ""),
            str(engine.get("aux_repo") or ""),
            str(engine.get("aux_revision") or ""),
            str(engine.get("custom_voice_repo") or ""),
            str(engine.get("custom_voice_revision") or ""),
            str(engine.get("source_commit") or ""),
            effective_preset,
            resolved_preset_prompt,
            resolved_preset_seed,
            resolved_preset_speaker,
            resolved_text,
            str(voice_prompt or "").strip(),
            str(emotion_prompt or "").strip(),
            norm_ref_sha,
            norm_ref_text,
            norm_emo_sha,
        ]
        digest = hashlib.sha256(
            json.dumps(payload, ensure_ascii=False, separators=(",", ":")).encode(
                "utf-8"
            )
        ).hexdigest()[:32]
        return digest, resolved_text

    def _load_cached_preview_locked(
        self, preview_id: str, *, touch: bool = True
    ) -> tuple[str, dict[str, Any], bytes, str] | None:
        pid = str(preview_id or "").strip()
        if not _PREVIEW_ID_RE.fullmatch(pid):
            raise ValueError("试听音频编号不合法")
        if self.engines_dir.is_symlink():
            raise ValueError("TTS 引擎目录不能是符号链接")
        if self.previews_dir.is_symlink():
            raise ValueError("音色试听缓存目录不能是符号链接")
        if not self.previews_dir.is_dir():
            return None

        wav_path = self.previews_dir / f"{pid}.wav"
        meta_path = self.previews_dir / f"{pid}.json"
        if wav_path.is_symlink() or meta_path.is_symlink():
            raise ValueError("音色试听缓存文件不能是符号链接")
        if not wav_path.is_file() or not meta_path.is_file():
            return None

        try:
            wav_size = wav_path.stat().st_size
            meta_size = meta_path.stat().st_size
            if (
                wav_size <= 0
                or wav_size > MAX_PREVIEW_AUDIO_BYTES
                or meta_size <= 0
                or meta_size > 256 * 1024
            ):
                return None
            meta = json.loads(meta_path.read_text(encoding="utf-8"))
            if not isinstance(meta, dict):
                return None
            raw = wav_path.read_bytes()
        except (OSError, ValueError):
            return None

        if not raw or len(raw) > MAX_PREVIEW_AUDIO_BYTES:
            return None

        raw_size = meta.get("size")
        if isinstance(raw_size, bool):
            return None
        try:
            expected_size = int(raw_size if raw_size is not None else -1)
        except (TypeError, ValueError):
            return None
        if len(raw) != expected_size:
            return None

        raw_sr = meta.get("sample_rate")
        if raw_sr is None:
            sample_rate = 48000
        elif isinstance(raw_sr, bool) or not isinstance(raw_sr, int) or raw_sr <= 0:
            return None
        else:
            sample_rate = raw_sr

        raw_dur = meta.get("duration_seconds")
        if raw_dur is None:
            duration_seconds = 0.0
        elif isinstance(raw_dur, bool) or not isinstance(raw_dur, (int, float)):
            return None
        else:
            duration_seconds = float(raw_dur)
            if not math.isfinite(duration_seconds) or duration_seconds < 0.0:
                return None

        sha256 = hashlib.sha256(raw).hexdigest()
        expected_sha256 = str(meta.get("sha256") or "").strip().lower()
        if not expected_sha256 or sha256 != expected_sha256:
            return None

        meta["sample_rate"] = sample_rate
        meta["duration_seconds"] = duration_seconds

        if touch:
            try:
                os.utime(wav_path, None)
            except OSError:
                pass

        return pid, meta, raw, sha256

    def get_cached_preview(
        self, preview_id: str, *, max_inline_bytes: int | None = None
    ) -> dict[str, Any] | None:
        """Read a cached voice audition WAV + metadata from disk if valid and non-symlinked."""
        with self._preview_lock:
            loaded = self._load_cached_preview_locked(preview_id)
            if loaded is None:
                return None
            pid, meta, raw, sha256 = loaded
        payload: dict[str, Any] = {
            "cached": True,
            "preview_id": pid,
            "engine_id": str(meta.get("engine_id") or ""),
            "voice_preset": str(meta.get("voice_preset") or ""),
            "text": str(meta.get("text") or ""),
            "sample_rate": meta["sample_rate"],
            "duration_seconds": meta["duration_seconds"],
            "mime": "audio/wav",
            "size": len(raw),
            "sha256": sha256,
        }
        if "voice_prompt" in meta:
            payload["voice_prompt"] = str(meta.get("voice_prompt") or "")
        if "emotion_prompt" in meta:
            payload["emotion_prompt"] = str(meta.get("emotion_prompt") or "")
        if max_inline_bytes is None or len(raw) <= max_inline_bytes:
            payload["data"] = base64.b64encode(raw).decode("ascii")
        return payload

    def get_cached_preview_chunk(
        self,
        preview_id: str,
        offset: int = 0,
        length: int = 512 * 1024,
    ) -> dict[str, Any] | None:
        """Read one byte-range chunk of a cached voice audition WAV for chunked transport."""
        if isinstance(offset, bool) or not isinstance(offset, int) or offset < 0:
            raise ValueError("试听音频分片偏移量必须为非负整数")
        if isinstance(length, bool) or not isinstance(length, int) or length <= 0:
            raise ValueError("试听音频分片长度必须为正整数")
        capped_length = min(length, MAX_PREVIEW_AUDIO_BYTES)
        with self._preview_lock:
            loaded = self._load_cached_preview_locked(preview_id, touch=False)
            if loaded is None:
                return None
            pid, meta, raw, sha256 = loaded
            if offset >= len(raw):
                raise ValueError("试听音频读取范围不合法")
            try:
                os.utime(self.previews_dir / f"{pid}.wav", None)
            except OSError:
                pass
        chunk = raw[offset : offset + capped_length]
        chunk_payload: dict[str, Any] = {
            "cached": True,
            "preview_id": pid,
            "engine_id": str(meta.get("engine_id") or ""),
            "voice_preset": str(meta.get("voice_preset") or ""),
            "text": str(meta.get("text") or ""),
            "sample_rate": meta["sample_rate"],
            "duration_seconds": meta["duration_seconds"],
            "mime": "audio/wav",
            "size": len(raw),
            "sha256": sha256,
            "offset": offset,
            "chunk_size": len(chunk),
            "has_more": (offset + len(chunk)) < len(raw),
            "data": base64.b64encode(chunk).decode("ascii"),
        }
        if "voice_prompt" in meta:
            chunk_payload["voice_prompt"] = str(meta.get("voice_prompt") or "")
        if "emotion_prompt" in meta:
            chunk_payload["emotion_prompt"] = str(meta.get("emotion_prompt") or "")
        return chunk_payload

    def _prune_preview_cache_locked(self, *, keep_pid: str = "") -> None:
        """Evict orphaned preview artifacts and oldest-mtime cached previews above LRU bounds."""
        if self.previews_dir.is_symlink() or not self.previews_dir.is_dir():
            return
        wav_files: dict[str, Path] = {}
        meta_files: dict[str, Path] = {}
        try:
            children = list(self.previews_dir.iterdir())
        except OSError:
            return

        for child in children:
            name = child.name
            if name.startswith("."):
                continue
            if child.is_symlink():
                continue
            if not child.is_file():
                continue
            if name.endswith(".wav"):
                stem = name[:-4]
                if _PREVIEW_ID_RE.fullmatch(stem):
                    wav_files[stem] = child
            elif name.endswith(".json"):
                stem = name[:-5]
                if _PREVIEW_ID_RE.fullmatch(stem):
                    meta_files[stem] = child

        for pid, wav_p in list(wav_files.items()):
            if pid not in meta_files and pid != keep_pid:
                try:
                    wav_p.unlink(missing_ok=True)
                except OSError:
                    pass
                wav_files.pop(pid, None)
        for pid, meta_p in list(meta_files.items()):
            if pid not in wav_files and pid != keep_pid:
                try:
                    meta_p.unlink(missing_ok=True)
                except OSError:
                    pass
                meta_files.pop(pid, None)

        entries: list[tuple[float, int, str, Path, Path]] = []
        total_bytes = 0
        for pid, wav_p in wav_files.items():
            meta_p = meta_files.get(pid)
            if meta_p is None:
                continue
            try:
                wav_st = wav_p.stat()
                meta_st = meta_p.stat()
            except OSError:
                continue
            entry_bytes = int(wav_st.st_size) + int(meta_st.st_size)
            total_bytes += entry_bytes
            entries.append((float(wav_st.st_mtime), entry_bytes, pid, wav_p, meta_p))

        if (
            len(entries) <= MAX_PREVIEW_CACHE_ENTRIES
            and total_bytes <= MAX_PREVIEW_CACHE_BYTES
        ):
            return

        entries.sort(key=lambda item: item[0])
        remaining = len(entries)
        for _mtime, entry_bytes, pid, wav_p, meta_p in entries:
            if (
                remaining <= MAX_PREVIEW_CACHE_ENTRIES
                and total_bytes <= MAX_PREVIEW_CACHE_BYTES
            ):
                break
            if pid == keep_pid:
                continue
            try:
                wav_p.unlink(missing_ok=True)
            except OSError:
                pass
            try:
                meta_p.unlink(missing_ok=True)
            except OSError:
                pass
            total_bytes -= entry_bytes
            remaining -= 1

    # -----------------------------------------------------------------------
    # Warm TTS Worker Pool & Background Batch Preview Prefetcher
    # -----------------------------------------------------------------------

    def _should_use_warm_worker(self) -> bool:
        return self.synth_fn is None and self.run_cmd_fn is None

    def _cancel_warm_idle_timer_locked(self) -> None:
        timer = self._warm_idle_timer
        self._warm_idle_timer = None
        if timer is not None:
            try:
                timer.cancel()
            except Exception:
                pass

    def _schedule_warm_idle_timer(self) -> None:
        idle_sec = float(self._warm_idle_seconds or 0.0)
        if idle_sec <= 0.0:
            return
        with self._warm_lock:
            self._cancel_warm_idle_timer_locked()
            worker = self._warm_worker
            if worker is None or not worker.is_alive():
                return

            def _evict_if_idle() -> None:
                if not self._warm_exec_lock.acquire(blocking=False):
                    # Worker is currently busy; reschedule after another window.
                    self._schedule_warm_idle_timer()
                    return
                need_reschedule = False
                cur_to_close: ColabOneWarmTtsWorker | None = None
                try:
                    with self._warm_lock:
                        cur = self._warm_worker
                        if cur is None or not cur.is_alive():
                            self._warm_worker = None
                            return
                        if (time.monotonic() - cur.last_used) < (idle_sec * 0.9):
                            need_reschedule = True
                        else:
                            self._warm_worker = None
                            self._cancel_warm_idle_timer_locked()
                            cur_to_close = cur
                    if cur_to_close is not None:
                        cur_to_close.close()
                finally:
                    self._warm_exec_lock.release()
                if need_reschedule:
                    self._schedule_warm_idle_timer()

            timer = threading.Timer(idle_sec, _evict_if_idle)
            timer.daemon = True
            self._warm_idle_timer = timer
            timer.start()

    def release_warm_worker(self, reason: str = "") -> None:
        """Immediately shut down any resident warm TTS worker to return 100% of GPU VRAM."""
        with self._warm_lock:
            self._warm_release_gen += 1
            self._cancel_warm_idle_timer_locked()
            worker = self._warm_worker
            self._warm_worker = None
        if worker is not None:
            try:
                worker.close()
            except Exception:
                pass

    def _collect_prefetch_threads_locked(self) -> list[threading.Thread]:
        self._prior_prefetch_threads = [
            t for t in self._prior_prefetch_threads if t.is_alive()
        ]
        threads = list(self._prior_prefetch_threads)
        cur = self._prefetch_thread
        if cur is not None and cur.is_alive() and cur not in threads:
            threads.append(cur)
        return threads

    def cancel_preview_prefetch(
        self, *, wait: bool = False, timeout: float = 2.0
    ) -> dict[str, Any]:
        """Cancel any active background voice preview prefetch job."""
        threads_to_join: list[threading.Thread] = []
        with self._prefetch_lock:
            ev = self._prefetch_cancel
            if ev is not None:
                ev.set()
            job = self._prefetch_job
            if job is not None and job.get("state") == "running":
                job["state"] = "cancelled"
                job["finished_at"] = time.time()
            threads_to_join = self._collect_prefetch_threads_locked()
        if wait and threads_to_join:
            deadline = time.monotonic() + max(0.1, float(timeout))
            for t in threads_to_join:
                if t is threading.current_thread() or not t.is_alive():
                    continue
                rem = deadline - time.monotonic()
                if rem <= 0:
                    break
                t.join(timeout=rem)
            with self._prefetch_lock:
                self._prior_prefetch_threads = [
                    t for t in self._prior_prefetch_threads if t.is_alive()
                ]
        return self.prefetch_status()

    def cancel_prefetch_and_release_warm_worker(self, reason: str = "") -> None:
        """Cancel background preview prefetch and free any resident warm TTS worker VRAM."""
        self.cancel_preview_prefetch(wait=False)
        # Immediately release any currently published worker and bump _warm_release_gen
        # so an active prefetch call unblocks promptly and cannot publish a stale worker.
        self.release_warm_worker(reason=reason)
        with self._prefetch_lock:
            threads_to_join = self._collect_prefetch_threads_locked()
        deadline = time.monotonic() + 2.0
        for t in threads_to_join:
            if t is threading.current_thread() or not t.is_alive():
                continue
            rem = deadline - time.monotonic()
            if rem <= 0:
                break
            t.join(timeout=rem)
        with self._prefetch_lock:
            self._prior_prefetch_threads = [
                t for t in self._prior_prefetch_threads if t.is_alive()
            ]
        # Final release AFTER joining prefetch thread(s) to guarantee zero resident VRAM.
        self.release_warm_worker(reason=reason)

    def prefetch_status(self) -> dict[str, Any]:
        """Return a lightweight JSON-safe snapshot of the latest background prefetch job."""
        with self._prefetch_lock:
            job = self._prefetch_job
            if job is None:
                return {
                    "active": False,
                    "job_id": "",
                    "engine_id": "",
                    "state": "idle",
                    "total": 0,
                    "completed": 0,
                    "failed": 0,
                    "ready_items": [],
                }
            items = list(job.get("items") or [])
            ready_items = [
                {
                    "voice_preset": str(it["voice_preset"]),
                    "preview_id": str(it["preview_id"]),
                }
                for it in items
                if it.get("status") == "ready"
            ]
            completed = len(ready_items)
            failed = sum(1 for it in items if it.get("status") == "failed")
            state = str(job.get("state") or "idle")
            return {
                "active": state == "running",
                "job_id": str(job.get("job_id") or ""),
                "engine_id": str(job.get("engine_id") or ""),
                "state": state,
                "total": len(items),
                "completed": completed,
                "failed": failed,
                "ready_items": ready_items,
            }

    def _ensure_warm_worker_locked(
        self,
        task: Any,
        engine: dict[str, Any],
        *,
        hf_token: str = "",
        cancel_event: threading.Event | None = None,
    ) -> ColabOneWarmTtsWorker:
        """Return a live warm daemon worker for `engine` (caller must hold `_warm_exec_lock`)."""
        engine_id = str(engine["id"])
        engine_name = str(engine.get("name") or engine_id)
        want_token = bool(str(hf_token or "").strip())

        eff_cancel = cancel_event
        if eff_cancel is None:
            task_cancel = getattr(task, "cancel", None)
            if isinstance(task_cancel, threading.Event):
                eff_cancel = task_cancel
        stopping = getattr(self.runtime, "stopping", None)
        cancel_cls = _cancelled_error_cls()

        def _check_not_cancelled() -> None:
            if eff_cancel is not None and eff_cancel.is_set():
                raise cancel_cls("操作已取消")
            if isinstance(stopping, threading.Event) and stopping.is_set():
                raise cancel_cls("操作已取消")
            check_fn = getattr(task, "check", None)
            if callable(check_fn):
                check_fn()

        _check_not_cancelled()

        with self._warm_lock:
            existing = self._warm_worker
        if (
            existing is not None
            and existing.is_alive()
            and existing.engine_id == engine_id
            and (not want_token or existing.has_hf_token)
        ):
            existing.last_used = time.monotonic()
            self._schedule_warm_idle_timer()
            return existing

        # Evict any previous warm worker before spawning a new one so VRAM is never doubled.
        self.release_warm_worker(reason=f"switch_to_{engine_id}")
        with self._warm_lock:
            start_gen = self._warm_release_gen

        _check_not_cancelled()

        env_dir = self.ensure_engine_env(task, engine, hf_token="")
        worker_python = str(
            self._require_venv_python(env_dir / ".venv", label=engine_name)
        )
        hf_cache_dir = str(self.engines_dir / "_hf_cache")
        worker_env = _build_tts_worker_env(hf_cache_dir, hf_token=hf_token)

        warm_root = self.engines_dir / "_warm_workers"
        if warm_root.is_symlink():
            raise ValueError("TTS 常驻工作目录不能是符号链接")
        warm_root.mkdir(parents=True, exist_ok=True)
        worker_work_dir = Path(
            tempfile.mkdtemp(prefix=f".warm-{engine_id}-", dir=str(warm_root))
        )
        worker = ColabOneWarmTtsWorker(
            engine_id=engine_id,
            worker_python=worker_python,
            worker_script=self.build_worker_script(engine_id),
            work_dir=worker_work_dir,
            env=worker_env,
            has_hf_token=want_token,
            secret_to_scrub=hf_token,
            stopping=stopping,
        )
        try:
            worker.start(cancel_event=eff_cancel, task=task)
        except BaseException:
            worker.close()
            raise

        stale_or_cancelled = False
        cancel_exc: BaseException | None = None
        with self._warm_lock:
            if self._warm_release_gen != start_gen:
                stale_or_cancelled = True
            elif (eff_cancel is not None and eff_cancel.is_set()) or (
                isinstance(stopping, threading.Event) and stopping.is_set()
            ):
                stale_or_cancelled = True
            else:
                check_fn = getattr(task, "check", None)
                if callable(check_fn):
                    try:
                        check_fn()
                    except BaseException as exc:
                        stale_or_cancelled = True
                        cancel_exc = exc
            if not stale_or_cancelled:
                self._warm_worker = worker
        if stale_or_cancelled:
            worker.close()
            if cancel_exc is not None:
                raise cancel_exc
            raise cancel_cls("操作已取消")
        self._schedule_warm_idle_timer()
        return worker

    def _recommended_preview_batch_size(self, engine_id: str) -> int:
        if engine_id not in _BATCH_CAPABLE_PREVIEW_ENGINES:
            return 1
        gpu_fn = getattr(self.runtime, "gpu_info", None)
        total_mib = 0
        if callable(gpu_fn):
            try:
                gpu = gpu_fn() or {}
                total_mib = int(gpu.get("total_mib") or 0)
            except Exception:
                total_mib = 0
        if total_mib >= 40960:
            return 4
        if total_mib >= 20480:
            return 3
        return 2

    def _persist_preview_wav(
        self,
        *,
        target_pid: str,
        engine: dict[str, Any],
        effective_preset_id: str,
        resolved_text: str,
        master_wav: Path,
        sample_rate: int,
        duration_seconds: float,
        voice_prompt: str = "",
        emotion_prompt: str = "",
        include_data: bool = True,
    ) -> dict[str, Any]:
        if not master_wav.is_file() or master_wav.stat().st_size <= 0:
            raise RuntimeError("音色试听音频合成失败：未生成有效 WAV 文件")
        if master_wav.stat().st_size > MAX_PREVIEW_AUDIO_BYTES:
            raise ValueError("音色试听音频超过最大体积限制（16 MiB）")

        wav_bytes = master_wav.read_bytes()
        if not wav_bytes:
            raise RuntimeError("音色试听音频合成失败：未生成有效 WAV 文件")
        if len(wav_bytes) > MAX_PREVIEW_AUDIO_BYTES:
            raise ValueError("音色试听音频超过最大体积限制（16 MiB）")
        wav_size = len(wav_bytes)
        wav_sha256 = hashlib.sha256(wav_bytes).hexdigest()
        sr = int(sample_rate) if int(sample_rate or 0) > 0 else int(engine.get("sample_rate") or 24000)
        dur = (
            round(float(duration_seconds), 4)
            if (math.isfinite(float(duration_seconds)) and float(duration_seconds) >= 0.0)
            else 0.0
        )
        resolved_engine_id = str(engine["id"])
        clean_vp = str(voice_prompt or "").strip()
        clean_ep = str(emotion_prompt or "").strip()
        meta = {
            "preview_id": target_pid,
            "engine_id": resolved_engine_id,
            "voice_preset": effective_preset_id,
            "text": resolved_text,
            "voice_prompt": clean_vp,
            "emotion_prompt": clean_ep,
            "sample_rate": sr,
            "duration_seconds": dur,
            "mime": "audio/wav",
            "size": wav_size,
            "sha256": wav_sha256,
            "created_at": time.time(),
        }
        wav_path = self.previews_dir / f"{target_pid}.wav"
        meta_path = self.previews_dir / f"{target_pid}.json"
        tmp_wav_target: Path | None = None
        try:
            with self._preview_lock:
                if wav_path.is_symlink() or meta_path.is_symlink():
                    raise ValueError("音色试听缓存文件不能是符号链接")
                fd, tmp_wav_str = tempfile.mkstemp(
                    prefix=f".{target_pid[:8]}-",
                    suffix=".wav.tmp",
                    dir=str(self.previews_dir),
                )
                tmp_wav_target = Path(tmp_wav_str)
                with os.fdopen(fd, "wb") as stream:
                    stream.write(wav_bytes)
                    stream.flush()
                    os.fsync(stream.fileno())
                os.replace(tmp_wav_target, wav_path)
                tmp_wav_target = None
                atomic_json(meta_path, meta)
                self._prune_preview_cache_locked(keep_pid=target_pid)
        finally:
            if tmp_wav_target is not None and tmp_wav_target.exists():
                try:
                    tmp_wav_target.unlink()
                except OSError:
                    pass

        payload: dict[str, Any] = {
            "cached": True,
            "preview_id": target_pid,
            "engine_id": resolved_engine_id,
            "voice_preset": effective_preset_id,
            "text": resolved_text,
            "voice_prompt": clean_vp,
            "emotion_prompt": clean_ep,
            "sample_rate": sr,
            "duration_seconds": dur,
            "mime": "audio/wav",
            "size": wav_size,
            "sha256": wav_sha256,
        }
        if include_data:
            payload["data"] = base64.b64encode(wav_bytes).decode("ascii")
        return payload

    def start_preview_prefetch(
        self,
        *,
        engine_id: str,
        items: Sequence[dict[str, Any]],
        hf_token: str = "",
        ensure_vram_fn: Callable[[], None] | None = None,
    ) -> dict[str, Any]:
        """Start a low-priority, preemptible background batch prefetch for built-in voice presets."""
        hf_token = str(hf_token or "").strip()
        engine = self.get_engine(engine_id)
        eid = str(engine["id"])

        prepared: list[dict[str, Any]] = []
        seen_pids: set[str] = set()
        for raw_item in items:
            if not isinstance(raw_item, dict):
                continue
            preset_id = str(raw_item.get("voice_preset") or "").strip()
            if not preset_id or preset_id.lower() == "custom":
                continue
            resolved_preset = resolve_colabone_voice_preset(engine, preset_id)
            if resolved_preset is None:
                continue
            p_text = str(raw_item.get("preview_text") or "").strip()
            v_prompt = str(raw_item.get("voice_prompt") or "").strip()
            e_prompt = str(raw_item.get("emotion_prompt") or "").strip()
            pid, resolved_text = self.compute_preview_id(
                eid,
                voice_preset=preset_id,
                preview_text=p_text,
                voice_prompt=v_prompt,
                emotion_prompt=e_prompt,
            )
            if pid in seen_pids:
                continue
            seen_pids.add(pid)
            # Validate inputs cleanly upfront; skip invalid items rather than failing whole batch.
            try:
                self.validate_tts_synthesis_inputs(
                    eid,
                    script_text=resolved_text,
                    voice_preset=preset_id,
                    voice_prompt=v_prompt,
                    emotion_prompt=e_prompt,
                    allow_legacy_empty_indextts_check=(self.synth_fn is not None),
                )
            except ValueError:
                continue
            already = self.get_cached_preview(pid, max_inline_bytes=0) is not None
            prepared.append(
                {
                    "voice_preset": str(resolved_preset["id"]),
                    "preset": resolved_preset,
                    "preview_id": pid,
                    "preview_text": resolved_text,
                    "voice_prompt": v_prompt,
                    "emotion_prompt": e_prompt,
                    "status": "ready" if already else "pending",
                }
            )

        if not prepared:
            return {
                "active": False,
                "job_id": "",
                "engine_id": eid,
                "state": "idle",
                "total": 0,
                "completed": 0,
                "failed": 0,
                "ready_items": [],
            }

        pending_exists = any(it["status"] == "pending" for it in prepared)
        job_id = uuid.uuid4().hex[:16]
        cancel_ev = threading.Event()

        threads_to_join: list[threading.Thread] = []
        with self._prefetch_lock:
            prev_ev = self._prefetch_cancel
            if prev_ev is not None:
                prev_ev.set()
            prev_job = self._prefetch_job
            if prev_job is not None and prev_job.get("state") == "running":
                prev_job["state"] = "cancelled"
                prev_job["finished_at"] = time.time()
            self._prior_prefetch_threads = [
                t for t in self._prior_prefetch_threads if t.is_alive()
            ]
            if self._prefetch_thread is not None and self._prefetch_thread.is_alive():
                if self._prefetch_thread not in self._prior_prefetch_threads:
                    self._prior_prefetch_threads.append(self._prefetch_thread)
                self._prefetch_thread = None
            threads_to_join = list(self._prior_prefetch_threads)

        if threads_to_join:
            deadline = time.monotonic() + 2.0
            for t in threads_to_join:
                if t is threading.current_thread() or not t.is_alive():
                    continue
                rem = deadline - time.monotonic()
                if rem <= 0:
                    break
                t.join(timeout=rem)

        with self._prefetch_lock:
            self._prior_prefetch_threads = [
                t for t in self._prior_prefetch_threads if t.is_alive()
            ]
            if self._prefetch_cancel is not None and self._prefetch_cancel is not prev_ev:
                self._prefetch_cancel.set()
            if (
                self._prefetch_job is not None
                and self._prefetch_job.get("state") == "running"
            ):
                self._prefetch_job["state"] = "cancelled"
                self._prefetch_job["finished_at"] = time.time()
            if self._prefetch_thread is not None and self._prefetch_thread.is_alive():
                if self._prefetch_thread not in self._prior_prefetch_threads:
                    self._prior_prefetch_threads.append(self._prefetch_thread)
                self._prefetch_thread = None

            job: dict[str, Any] = {
                "job_id": job_id,
                "engine_id": eid,
                "state": "running" if pending_exists else "completed",
                "items": prepared,
                "created_at": time.time(),
                "finished_at": None if pending_exists else time.time(),
            }
            self._prefetch_job = job
            self._prefetch_cancel = cancel_ev

            if not pending_exists:
                self._prefetch_thread = None
                return self.prefetch_status()

            thread = threading.Thread(
                target=self._run_prefetch_job,
                args=(job, engine, hf_token, cancel_ev, ensure_vram_fn),
                name=f"colabone-tts-prefetch-{job_id[:8]}",
                daemon=True,
            )
            self._prefetch_thread = thread
            thread.start()

        return self.prefetch_status()

    def _prefetch_should_stop(self, cancel_ev: threading.Event) -> bool:
        if cancel_ev.is_set():
            return True
        stopping = getattr(self.runtime, "stopping", None)
        if stopping is not None and stopping.is_set():
            return True
        if getattr(self.runtime, "quiescing", False):
            return True
        # Yield cleanly if a heavy non-preview task (e.g. ComfyUI run or LLM load) started.
        tasks = getattr(self.runtime, "tasks", None)
        if isinstance(tasks, dict):
            for t in list(tasks.values()):
                if getattr(t, "state", "") == "running" and getattr(t, "kind", "") != "comfyui_tts_preview":
                    return True
        return False

    def _wait_if_foreground_waiting(self, cancel_ev: threading.Event) -> bool:
        while self._foreground_waiting.is_set():
            if self._prefetch_should_stop(cancel_ev):
                return False
            with self._foreground_done:
                if self._foreground_waiting.is_set():
                    self._foreground_done.wait(timeout=0.2)
        return not self._prefetch_should_stop(cancel_ev)

    def _run_prefetch_job(
        self,
        job: dict[str, Any],
        engine: dict[str, Any],
        hf_token: str,
        cancel_ev: threading.Event,
        ensure_vram_fn: Callable[[], None] | None,
    ) -> None:
        eid = str(engine["id"])
        native_sr = max(8000, int(engine.get("sample_rate") or 24000))
        raw_mc = int(engine.get("max_segment_chars") or 0)
        max_segment_chars = raw_mc if raw_mc > 0 else 120
        batch_size = self._recommended_preview_batch_size(eid)

        class _PrefetchTask:
            def __init__(self, ev: threading.Event, stopping: Any) -> None:
                self._ev = ev
                self._stopping = stopping
                self.cancel = ev

            def check(self) -> None:
                if self._ev.is_set() or (
                    self._stopping is not None and self._stopping.is_set()
                ):
                    raise _cancelled_error_cls()("操作已取消")

            def note(self, _msg: str, level: str = "info") -> None:
                return None

            def update(self, **_kw: Any) -> None:
                return None

        bg_task = _PrefetchTask(cancel_ev, getattr(self.runtime, "stopping", None))

        try:
            if not self._wait_if_foreground_waiting(cancel_ev):
                return

            self.check_vram(engine, allow_unavailable=(self.synth_fn is not None))
            self.check_disk(engine, allow_unavailable=(self.synth_fn is not None))
            if callable(ensure_vram_fn):
                ensure_vram_fn()

            if self.engines_dir.is_symlink():
                raise ValueError("TTS 引擎目录不能是符号链接")
            self.engines_dir.mkdir(parents=True, exist_ok=True)
            if self.previews_dir.is_symlink():
                raise ValueError("音色试听缓存目录不能是符号链接")
            self.previews_dir.mkdir(parents=True, exist_ok=True)

            # For ref-audio-based engines, warm up any missing VoxCPM2 anchors in one pass
            # before loading the target engine so we never ping-pong between two models.
            if eid in _REF_AUDIO_PRESET_ENGINES and self._should_use_warm_worker():
                for item in list(job["items"]):
                    if not self._wait_if_foreground_waiting(cancel_ev):
                        return
                    if item.get("status") != "pending":
                        continue
                    preset = item["preset"]
                    if not self._is_preset_reference_cached(preset):
                        self._ensure_preset_reference_audio(
                            bg_task, preset, hf_token=hf_token
                        )

            items_list: list[dict[str, Any]] = job["items"]
            cursor = 0
            consecutive_failures = 0

            def _abort_prefetch_job(reason: str) -> None:
                clean_reason = _scrub_secret(reason or "远端预生成失败", hf_token)[:300]
                with self._prefetch_lock:
                    for pending_it in items_list:
                        if pending_it.get("status") == "pending":
                            pending_it["status"] = "cancelled"
                    if job.get("state") == "running":
                        job["state"] = "failed"
                        job["error"] = clean_reason
                        job["finished_at"] = time.time()

            while cursor < len(items_list):
                if not self._wait_if_foreground_waiting(cancel_ev):
                    return

                # Collect next micro-batch of still-pending uncached items.
                chunk: list[dict[str, Any]] = []
                while cursor < len(items_list) and len(chunk) < batch_size:
                    item = items_list[cursor]
                    cursor += 1
                    if item.get("status") != "pending":
                        continue
                    if (
                        self.get_cached_preview(item["preview_id"], max_inline_bytes=0)
                        is not None
                    ):
                        with self._prefetch_lock:
                            item["status"] = "ready"
                        continue
                    chunk.append(item)

                if not chunk:
                    continue

                if (
                    len(chunk) > 1
                    and eid in _BATCH_CAPABLE_PREVIEW_ENGINES
                    and self._should_use_warm_worker()
                ):
                    batch_err = self._synthesize_prefetch_batch_chunk(
                        bg_task=bg_task,
                        engine=engine,
                        chunk=chunk,
                        hf_token=hf_token,
                        cancel_ev=cancel_ev,
                        max_segment_chars=max_segment_chars,
                        native_sr=native_sr,
                    )
                    if batch_err:
                        consecutive_failures += 1
                        if _is_fatal_prefetch_error(batch_err) or consecutive_failures >= 2:
                            _abort_prefetch_job(batch_err)
                            return
                    else:
                        consecutive_failures = 0
                    continue

                for item in chunk:
                    if not self._wait_if_foreground_waiting(cancel_ev):
                        return
                    if (
                        self.get_cached_preview(item["preview_id"], max_inline_bytes=0)
                        is not None
                    ):
                        with self._prefetch_lock:
                            item["status"] = "ready"
                        continue
                    try:
                        self.synthesize_preview(
                            bg_task,
                            engine_id=eid,
                            voice_preset=item["voice_preset"],
                            preview_text=item["preview_text"],
                            voice_prompt=item["voice_prompt"],
                            emotion_prompt=item["emotion_prompt"],
                            hf_token=hf_token,
                            preview_id=item["preview_id"],
                            _is_background_prefetch=True,
                        )
                        consecutive_failures = 0
                        with self._prefetch_lock:
                            item["status"] = "ready"
                    except Exception as exc:
                        if _is_cancelled_error(exc) or self._prefetch_should_stop(
                            cancel_ev
                        ):
                            return
                        err_msg = _scrub_secret(
                            str(exc) or type(exc).__name__, hf_token
                        )[:300]
                        with self._prefetch_lock:
                            item["status"] = "failed"
                            item["error"] = err_msg
                        consecutive_failures += 1
                        if _is_fatal_prefetch_error(err_msg) or consecutive_failures >= 2:
                            _abort_prefetch_job(err_msg)
                            return
        except Exception as exc:
            if _is_cancelled_error(exc) or self._prefetch_should_stop(cancel_ev):
                return
            with self._prefetch_lock:
                for pending_it in list(job.get("items") or []):
                    if pending_it.get("status") == "pending":
                        pending_it["status"] = "cancelled"
                if job.get("state") == "running":
                    job["state"] = "failed"
                    job["error"] = _scrub_secret(
                        str(exc) or type(exc).__name__, hf_token
                    )[:300]
                    job["finished_at"] = time.time()
            return
        finally:
            with self._prefetch_lock:
                if job.get("state") == "running":
                    if cancel_ev.is_set() or self._prefetch_should_stop(cancel_ev):
                        job["state"] = "cancelled"
                    else:
                        ready_cnt = sum(
                            1 for it in (job.get("items") or []) if it.get("status") == "ready"
                        )
                        failed_items = [
                            it
                            for it in (job.get("items") or [])
                            if it.get("status") == "failed"
                        ]
                        if ready_cnt == 0 and failed_items:
                            job["state"] = "failed"
                            if not job.get("error"):
                                job["error"] = str(failed_items[0].get("error") or "预生成失败")
                        else:
                            job["state"] = "completed"
                    job["finished_at"] = time.time()

    def _synthesize_prefetch_batch_chunk(
        self,
        *,
        bg_task: Any,
        engine: dict[str, Any],
        chunk: list[dict[str, Any]],
        hf_token: str,
        cancel_ev: threading.Event,
        max_segment_chars: int,
        native_sr: int,
    ) -> str:
        eid = str(engine["id"])
        tmp_work = Path(
            tempfile.mkdtemp(prefix=f".prefetch-{eid}-", dir=str(self.previews_dir))
        )
        try:
            env_dir = self.ensure_engine_env(bg_task, engine, hf_token="")
            source_dir = env_dir / "source"
            source_dir_str = (
                str(source_dir)
                if (source_dir.is_dir() and not source_dir.is_symlink())
                else ""
            )

            configs: list[dict[str, Any]] = []
            seg_lists: list[list[tuple[str, Path]]] = []
            for idx, item in enumerate(chunk):
                item_dir = tmp_work / f"item_{idx:02d}"
                item_dir.mkdir(parents=True, exist_ok=True)
                segs = split_colabone_tts_script(
                    item["preview_text"], max_chars=max_segment_chars
                )
                if len(segs) > 1:
                    packed = segs[0]
                    for frag in segs[1:]:
                        packed = _join_fragments(packed, frag)
                    if len(packed) <= max_segment_chars:
                        segs = [packed]
                seg_entries = [
                    {
                        "index": s_idx,
                        "text": s_txt,
                        "output_wav": str(item_dir / f"seg_{s_idx:04d}.wav"),
                    }
                    for s_idx, s_txt in enumerate(segs)
                ]
                seg_lists.append(
                    [(str(se["text"]), Path(se["output_wav"])) for se in seg_entries]
                )

                preset = item["preset"]
                preset_id = str(preset["id"])
                preset_seed = int(preset["seed"])
                user_vp = str(item.get("voice_prompt") or "").strip()
                speaker_id = (
                    str(preset.get("speaker_id") or "").strip()
                    if eid == "qwen3_tts"
                    else ""
                )
                preset_prompt = str(
                    preset.get("prompt")
                    or (
                        preset.get("qwen_instruct")
                        if eid in ("qwen3_tts", "ming_3_tts", "breeze_tts2")
                        else preset.get("voxcpm_prompt")
                    )
                    or ""
                ).strip()
                if speaker_id:
                    eff_vp = user_vp
                elif not user_vp and preset_prompt:
                    eff_vp = preset_prompt
                else:
                    eff_vp = user_vp

                configs.append(
                    {
                        "engine_id": eid,
                        "repo": str(engine.get("repo") or ""),
                        "revision": str(engine.get("revision") or ""),
                        "aux_repo": str(engine.get("aux_repo") or ""),
                        "aux_revision": str(engine.get("aux_revision") or ""),
                        "custom_voice_repo": str(engine.get("custom_voice_repo") or ""),
                        "custom_voice_revision": str(
                            engine.get("custom_voice_revision") or ""
                        ),
                        "source_dir": source_dir_str,
                        "cache_dir": str(self.engines_dir / "_hf_cache"),
                        "sample_rate": native_sr,
                        "segments": seg_entries,
                        "ref_audio_path": "",
                        "ref_text": "",
                        "voice_prompt": eff_vp,
                        "voice_preset": preset_id,
                        "voice_seed": preset_seed,
                        "speaker_id": speaker_id,
                        "emotion_ref_path": "",
                        "emotion_prompt": str(item.get("emotion_prompt") or ""),
                    }
                )

            with self._warm_exec_lock:
                if self._prefetch_should_stop(cancel_ev):
                    return ""
                worker = self._ensure_warm_worker_locked(
                    bg_task, engine, hf_token=hf_token, cancel_event=cancel_ev
                )
                resp = worker.call(
                    {"op": "synthesize_batch", "items": configs},
                    task=bg_task,
                    cancel_event=cancel_ev,
                    timeout=900.0,
                    log_path=tmp_work / f"prefetch_{eid}.log",
                    secret_to_scrub=hf_token,
                )
                self._schedule_warm_idle_timer()

            results = resp.get("results") if isinstance(resp, dict) else None
            if not isinstance(results, list):
                results = [{"index": i, "ok": True} for i in range(len(chunk))]

            batch_first_err = ""
            batch_any_ready = False
            for idx, item in enumerate(chunk):
                res_entry = (
                    results[idx]
                    if idx < len(results) and isinstance(results[idx], dict)
                    else {"ok": False, "error": "missing batch result"}
                )
                if not res_entry.get("ok"):
                    item_err = _scrub_secret(
                        str(res_entry.get("error") or "批量试听合成失败"), hf_token
                    )[:300]
                    if not batch_first_err:
                        batch_first_err = item_err
                    with self._prefetch_lock:
                        item["status"] = "failed"
                        item["error"] = item_err
                    continue
                seg_wavs = seg_lists[idx]
                if not all(p.is_file() for _, p in seg_wavs):
                    if not batch_first_err:
                        batch_first_err = "批量试听未生成有效音频片段"
                    with self._prefetch_lock:
                        item["status"] = "failed"
                        item["error"] = "批量试听未生成有效音频片段"
                    continue
                master_wav = tmp_work / f"master_{idx:02d}.wav"
                manifest_path = tmp_work / f"manifest_{idx:02d}.json"
                manifest = assemble_colabone_tts_master(
                    seg_wavs,
                    out_wav=master_wav,
                    manifest_path=manifest_path,
                    pause_seconds=0.18,
                    target_sr=native_sr,
                    target_lufs=-23.0,
                )
                self._persist_preview_wav(
                    target_pid=item["preview_id"],
                    engine=engine,
                    effective_preset_id=item["voice_preset"],
                    resolved_text=item["preview_text"],
                    master_wav=master_wav,
                    sample_rate=int(manifest["sample_rate"]),
                    duration_seconds=float(manifest["duration"]),
                    voice_prompt=str(item.get("voice_prompt") or ""),
                    emotion_prompt=str(item.get("emotion_prompt") or ""),
                    include_data=False,
                )
                batch_any_ready = True
                with self._prefetch_lock:
                    item["status"] = "ready"
            if batch_first_err and _is_warm_worker_cuda_fatal_error(batch_first_err):
                self.release_warm_worker("prefetch_batch_cuda_fatal")
            return "" if batch_any_ready else batch_first_err
        except Exception as exc:
            if _is_cancelled_error(exc) or self._prefetch_should_stop(cancel_ev):
                return ""
            if _is_warm_worker_cuda_fatal_error(exc) or isinstance(
                exc, _ColabOneWarmWorkerTransportError
            ):
                self.release_warm_worker("prefetch_batch_error")
            err_msg = _scrub_secret(str(exc) or type(exc).__name__, hf_token)[:300]
            with self._prefetch_lock:
                for item in chunk:
                    if item.get("status") == "pending":
                        item["status"] = "failed"
                        item["error"] = err_msg
            return err_msg
        finally:
            shutil.rmtree(tmp_work, ignore_errors=True)

    def synthesize_preview(
        self,
        task: Any,
        *,
        engine_id: str,
        voice_preset: str = "",
        preview_text: str = "",
        voice_prompt: str = "",
        emotion_prompt: str = "",
        ref_audio_path: Path | None = None,
        ref_text: str = "",
        emotion_ref_path: Path | None = None,
        hf_token: str = "",
        preview_id: str = "",
        _is_background_prefetch: bool = False,
    ) -> dict[str, Any]:
        """Synthesize or reuse a cached voice audition WAV clip for the selected TTS engine."""
        hf_token = str(hf_token or "").strip()
        check_fn = getattr(task, "check", None)
        if callable(check_fn):
            check_fn()

        resolved_ref: Path | None = None
        ref_sha = ""
        if ref_audio_path is not None and str(ref_audio_path).strip():
            resolved_ref = Path(ref_audio_path)
            if resolved_ref.is_symlink():
                raise ValueError("音色参考音频不能是符号链接")
            if not resolved_ref.is_file():
                raise ValueError(f"音色参考音频文件不存在：{resolved_ref}")
            ref_sha = _file_sha256(resolved_ref)

        resolved_emo: Path | None = None
        emo_sha = ""
        if emotion_ref_path is not None and str(emotion_ref_path).strip():
            resolved_emo = Path(emotion_ref_path)
            if resolved_emo.is_symlink():
                raise ValueError("情绪参考音频不能是符号链接")
            if not resolved_emo.is_file():
                raise ValueError(f"情绪参考音频文件不存在：{resolved_emo}")
            emo_sha = _file_sha256(resolved_emo)

        computed_pid, resolved_text = self.compute_preview_id(
            engine_id,
            voice_preset=voice_preset,
            preview_text=preview_text,
            voice_prompt=voice_prompt,
            emotion_prompt=emotion_prompt,
            ref_audio_sha256=ref_sha,
            ref_text=ref_text,
            emotion_ref_sha256=emo_sha,
        )
        explicit_pid = str(preview_id or "").strip()
        if explicit_pid:
            if not _PREVIEW_ID_RE.fullmatch(explicit_pid):
                raise ValueError(f"非法的试听音频编号：{preview_id!r}")
            if explicit_pid != computed_pid:
                raise ValueError("试听音频编号与请求参数不匹配")
        target_pid = computed_pid

        cached = self.get_cached_preview(target_pid)
        if cached is not None:
            _safe_task_note(task, f"复用已缓存的音色试听样本：{target_pid[:10]}")
            return cached

        if self.engines_dir.is_symlink():
            raise ValueError("TTS 引擎目录不能是符号链接")
        self.engines_dir.mkdir(parents=True, exist_ok=True)
        if self.previews_dir.is_symlink():
            raise ValueError("音色试听缓存目录不能是符号链接")
        self.previews_dir.mkdir(parents=True, exist_ok=True)

        wav_path = self.previews_dir / f"{target_pid}.wav"
        meta_path = self.previews_dir / f"{target_pid}.json"
        if wav_path.is_symlink() or meta_path.is_symlink():
            raise ValueError("音色试听缓存文件不能是符号链接")

        engine = self.get_engine(engine_id)
        raw_preset = str(voice_preset or "").strip()
        norm_raw_preset = "custom" if raw_preset.lower() == "custom" else raw_preset
        resolved_preset = (
            None
            if resolved_ref is not None
            else resolve_colabone_voice_preset(engine, norm_raw_preset)
        )
        effective_synth_preset = (
            norm_raw_preset
            if (resolved_ref is not None or resolved_preset is None)
            else str(resolved_preset["id"])
        )
        native_preview_sr = max(8000, int(engine.get("sample_rate") or 24000))

        tmp_work = Path(
            tempfile.mkdtemp(
                prefix=f".preview-{target_pid[:8]}-", dir=str(self.previews_dir)
            )
        )
        foreground_marked = False
        try:
            if not _is_background_prefetch:
                self._foreground_waiting.set()
                foreground_marked = True

            with self._warm_exec_lock:
                # Re-check cache after acquiring lock in case background prefetch just finished it!
                cached_after_wait = self.get_cached_preview(target_pid)
                if cached_after_wait is not None:
                    _safe_task_note(
                        task, f"复用已缓存的音色试听样本：{target_pid[:10]}"
                    )
                    return cached_after_wait

                synth_result = self.synthesize(
                    task,
                    engine_id=engine_id,
                    script_text=resolved_text,
                    work_dir=tmp_work,
                    ref_audio_path=resolved_ref,
                    ref_text=ref_text,
                    voice_prompt=voice_prompt,
                    emotion_ref_path=resolved_emo,
                    emotion_prompt=emotion_prompt,
                    pause_seconds=0.18,
                    hf_token=hf_token,
                    voice_preset=effective_synth_preset,
                    target_sr=native_preview_sr,
                    single_segment_if_fit=True,
                    prefer_warm=True,
                    _warm_lock_held=True,
                )

            master_wav = Path(synth_result["audio_path"])
            effective_preset_id = (
                ""
                if resolved_ref is not None
                else (
                    str(resolved_preset["id"])
                    if resolved_preset is not None
                    else norm_raw_preset
                )
            )
            raw_sr = int(synth_result.get("sample_rate") or native_preview_sr)
            raw_dur = float(synth_result.get("duration") or 0.0)
            return self._persist_preview_wav(
                target_pid=target_pid,
                engine=engine,
                effective_preset_id=effective_preset_id,
                resolved_text=resolved_text,
                master_wav=master_wav,
                sample_rate=raw_sr,
                duration_seconds=raw_dur,
                voice_prompt=voice_prompt,
                emotion_prompt=emotion_prompt,
                include_data=(not _is_background_prefetch),
            )
        finally:
            if foreground_marked:
                self._foreground_waiting.clear()
                with self._foreground_done:
                    self._foreground_done.notify_all()
            shutil.rmtree(tmp_work, ignore_errors=True)

    def validate_tts_synthesis_inputs(
        self,
        engine_id: str,
        *,
        script_text: str,
        ref_audio_path: Path | str | None = None,
        ref_text: str = "",
        voice_preset: str = "",
        voice_prompt: str = "",
        emotion_ref_path: Path | str | None = None,
        emotion_prompt: str = "",
        allow_legacy_empty_indextts_check: bool = False,
    ) -> dict[str, Any]:
        """Validate TTS synthesis inputs upfront before unloading VRAM or launching workers."""
        engine = self.get_engine(engine_id)
        eid = str(engine["id"])
        engine_name = str(engine.get("name") or eid)

        if (
            not isinstance(script_text, str)
            or not script_text.strip()
            or not _has_speakable_text(script_text)
        ):
            raise ValueError("配音脚本文本不能为空")

        resolved_ref_audio: Path | None = None
        if ref_audio_path is not None and str(ref_audio_path).strip():
            resolved_ref_audio = Path(ref_audio_path)
            if resolved_ref_audio.is_symlink():
                raise ValueError("音色参考音频不能是符号链接")
            if not resolved_ref_audio.is_file():
                raise ValueError(f"音色参考音频文件不存在：{resolved_ref_audio}")
            if resolved_ref_audio.stat().st_size <= 0:
                raise ValueError(f"音色参考音频文件为空：{resolved_ref_audio}")

        resolved_emo_audio: Path | None = None
        if emotion_ref_path is not None and str(emotion_ref_path).strip():
            resolved_emo_audio = Path(emotion_ref_path)
            if resolved_emo_audio.is_symlink():
                raise ValueError("情绪参考音频不能是符号链接")
            if not resolved_emo_audio.is_file():
                raise ValueError(f"情绪参考音频文件不存在：{resolved_emo_audio}")
            if resolved_emo_audio.stat().st_size <= 0:
                raise ValueError(f"情绪参考音频文件为空：{resolved_emo_audio}")

        raw_preset = str(voice_preset or "").strip()
        resolved_preset: dict[str, Any] | None = None
        if resolved_ref_audio is None:
            resolved_preset = resolve_colabone_voice_preset(engine, raw_preset)

        if (
            resolved_ref_audio is None
            and raw_preset
            and raw_preset.lower() != "custom"
            and resolved_preset is None
        ):
            raise ValueError(f"当前 TTS 引擎不支持该内置音色：{raw_preset}")

        if eid == "indextts2" and resolved_ref_audio is None:
            if (
                raw_preset.lower() == "custom"
                or resolved_preset is None
                or (raw_preset == "" and allow_legacy_empty_indextts_check)
            ):
                raise ValueError("IndexTTS2 需要上传音色参考音频才能进行语音合成")
        elif resolved_ref_audio is None and raw_preset.lower() == "custom":
            supports_voice_design = (
                eid in _VOICE_DESIGN_PRESET_ENGINES
                or bool(engine.get("supports_voice_design"))
            )
            if eid in _REF_AUDIO_PRESET_ENGINES or not supports_voice_design:
                raise ValueError(
                    f"{engine_name} 在自定义音色模式下需要上传音色参考音频才能进行语音合成"
                )

        cleaned_ref_text = str(ref_text or "").strip()
        cleaned_emotion_prompt = str(emotion_prompt or "").strip()
        if resolved_ref_audio is not None and not cleaned_ref_text:
            if eid == "breeze_tts2":
                raise ValueError(
                    "Breeze TTS 2 克隆参考音色时必须同时填写「参考音色对应文字」"
                )
            if eid == "fish_drama3":
                raise ValueError(
                    "Fish Audio Drama 3 克隆参考音色时必须同时填写「参考音色对应文字」"
                )
            if eid == "ming_3_tts" and not cleaned_emotion_prompt:
                raise ValueError(
                    "Ming-3.0-TTS 克隆参考音色时必须同时填写「参考音色对应文字」"
                )

        effective_voice_preset = (
            ""
            if resolved_ref_audio is not None
            else (
                str(resolved_preset["id"])
                if resolved_preset is not None
                else ("custom" if raw_preset.lower() == "custom" else raw_preset)
            )
        )
        return {
            "engine": engine,
            "resolved_ref_audio": resolved_ref_audio,
            "resolved_emo_audio": resolved_emo_audio,
            "resolved_preset": resolved_preset,
            "raw_voice_preset": raw_preset,
            "effective_voice_preset": effective_voice_preset,
        }

    def synthesize(
        self,
        task: Any,
        *,
        engine_id: str,
        script_text: str,
        work_dir: Path,
        ref_audio_path: Path | None = None,
        ref_text: str = "",
        voice_prompt: str = "",
        emotion_ref_path: Path | None = None,
        emotion_prompt: str = "",
        pause_seconds: float = 0.28,
        hf_token: str = "",
        voice_preset: str = "",
        target_sr: int | None = None,
        single_segment_if_fit: bool = False,
        prefer_warm: bool = False,
        _warm_lock_held: bool = False,
    ) -> dict[str, Any]:
        """Run remote GPU TTS synthesis in an isolated or warm daemon subprocess and assemble master WAV."""
        hf_token = str(hf_token or "").strip()
        check_fn = getattr(task, "check", None)
        if callable(check_fn):
            check_fn()

        validated = self.validate_tts_synthesis_inputs(
            engine_id,
            script_text=script_text,
            ref_audio_path=ref_audio_path,
            ref_text=ref_text,
            voice_preset=voice_preset,
            voice_prompt=voice_prompt,
            emotion_ref_path=emotion_ref_path,
            emotion_prompt=emotion_prompt,
            allow_legacy_empty_indextts_check=(self.synth_fn is not None),
        )
        engine = validated["engine"]
        engine_name = str(engine.get("name") or engine["id"])
        resolved_ref_audio: Path | None = validated["resolved_ref_audio"]
        resolved_emo_audio: Path | None = validated["resolved_emo_audio"]
        resolved_preset: dict[str, Any] | None = validated["resolved_preset"]

        raw_mc = int(engine.get("max_segment_chars") or 0)
        max_segment_chars = raw_mc if raw_mc > 0 else 120
        segments = split_colabone_tts_script(script_text, max_chars=max_segment_chars)
        if single_segment_if_fit and len(segments) > 1:
            packed = segments[0]
            for frag in segments[1:]:
                packed = _join_fragments(packed, frag)
            if len(packed) <= max_segment_chars:
                segments = [packed]
        if not segments:
            raise ValueError("配音脚本文本不能为空")

        user_voice_prompt = str(voice_prompt or "").strip()
        effective_ref_text = (
            str(ref_text or "") if resolved_ref_audio is not None else ""
        )
        effective_voice_prompt = str(voice_prompt or "")
        effective_voice_preset = ""
        effective_voice_seed: int | None = None
        effective_speaker_id = ""

        if not engine.get("commercial_ok", True):
            lic = str(engine.get("license") or "Non-Commercial").strip()
            notice = str(engine.get("license_notice") or "").strip()
            msg = f"非商用许可提示：当前 TTS 引擎「{engine_name}」采用 {lic}"
            if notice:
                msg += f" — {notice}"
            _safe_task_note(task, msg, level="warning")

        self.check_vram(engine, allow_unavailable=(self.synth_fn is not None))
        self.check_disk(
            engine,
            allow_unavailable=(self.synth_fn is not None),
            ref_audio_path=resolved_ref_audio,
            voice_preset=str(voice_preset or ""),
            preset=resolved_preset,
        )

        work_root = Path(work_dir)
        if work_root.is_symlink():
            raise ValueError("TTS 工作目录不能是符号链接")
        work_root.mkdir(parents=True, exist_ok=True)
        segments_dir = work_root / "segments"
        if segments_dir.is_symlink():
            raise ValueError("TTS 片段目录不能是符号链接")
        segments_dir.mkdir(parents=True, exist_ok=True)

        if resolved_ref_audio is None and resolved_preset is not None:
            preset_id = str(resolved_preset["id"])
            preset_seed = int(resolved_preset["seed"])
            preset_prompt = str(
                resolved_preset.get("prompt")
                or (
                    resolved_preset.get("qwen_instruct")
                    if engine["id"] in ("qwen3_tts", "ming_3_tts", "breeze_tts2")
                    else resolved_preset.get("voxcpm_prompt")
                )
                or ""
            ).strip()
            effective_voice_preset = preset_id
            effective_voice_seed = preset_seed

            supports_design = (
                engine["id"] in _VOICE_DESIGN_PRESET_ENGINES
                or bool(engine.get("supports_voice_design"))
            )
            if supports_design and engine["id"] not in _REF_AUDIO_PRESET_ENGINES:
                if engine["id"] == "qwen3_tts":
                    effective_speaker_id = str(
                        resolved_preset.get("speaker_id") or ""
                    ).strip()
                if effective_speaker_id:
                    effective_voice_prompt = user_voice_prompt
                elif not user_voice_prompt and preset_prompt:
                    effective_voice_prompt = preset_prompt
            else:
                if not user_voice_prompt and preset_prompt:
                    effective_voice_prompt = preset_prompt
                presets_dir = self.engines_dir / "_voice_presets"
                cached_wav = presets_dir / f"{preset_id}.wav"
                cached_txt = presets_dir / f"{preset_id}.txt"
                cached_meta = presets_dir / f"{preset_id}.meta.json"
                has_symlink_preset = (
                    self.engines_dir.is_symlink()
                    or presets_dir.is_symlink()
                    or cached_wav.is_symlink()
                    or cached_txt.is_symlink()
                    or cached_meta.is_symlink()
                )
                has_cached_preset = self._is_preset_reference_cached(resolved_preset)
                anchor_helper_overridden = (
                    getattr(self._ensure_preset_reference_audio, "__func__", None)
                    is not ColabOneRemoteTtsRunner._ensure_preset_reference_audio
                )
                if (
                    self.synth_fn is None
                    or has_cached_preset
                    or has_symlink_preset
                    or anchor_helper_overridden
                ):
                    was_cached_before = has_cached_preset
                    anchor_wav, anchor_text = self._ensure_preset_reference_audio(
                        task,
                        resolved_preset,
                        hf_token=hf_token,
                    )
                    resolved_ref_audio = Path(anchor_wav)
                    effective_ref_text = str(
                        anchor_text or COLABONE_VOICE_PRESET_ANCHOR_TEXT
                    )
                    if not was_cached_before:
                        self.check_disk(
                            engine,
                            allow_unavailable=(self.synth_fn is not None),
                            ref_audio_path=resolved_ref_audio,
                            preset=resolved_preset,
                        )

        if resolved_ref_audio is None:
            effective_ref_text = ""

        ref_target_sr = max(24000, int(engine.get("sample_rate") or 24000))
        if resolved_ref_audio is not None:
            resolved_ref_audio = self._prepare_ref_audio_wav(
                task,
                resolved_ref_audio,
                work_root,
                "voice",
                target_sr=ref_target_sr,
            )
        if resolved_emo_audio is not None:
            resolved_emo_audio = self._prepare_ref_audio_wav(
                task,
                resolved_emo_audio,
                work_root,
                "emotion",
                target_sr=ref_target_sr,
            )

        seg_entries = [
            {
                "index": idx,
                "text": seg_text,
                "output_wav": str(segments_dir / f"seg_{idx:04d}.wav"),
            }
            for idx, seg_text in enumerate(segments)
        ]

        # Config written to disk strictly omits hf_token (passed ONLY via subprocess env).
        base_config: dict[str, Any] = {
            "engine_id": engine["id"],
            "repo": str(engine.get("repo") or ""),
            "revision": str(engine.get("revision") or ""),
            "aux_repo": str(engine.get("aux_repo") or ""),
            "aux_revision": str(engine.get("aux_revision") or ""),
            "custom_voice_repo": str(engine.get("custom_voice_repo") or ""),
            "custom_voice_revision": str(engine.get("custom_voice_revision") or ""),
            "source_dir": "",
            "cache_dir": str(self.engines_dir / "_hf_cache"),
            "sample_rate": int(engine.get("sample_rate") or 48000),
            "segments": seg_entries,
            "ref_audio_path": str(resolved_ref_audio) if resolved_ref_audio else "",
            "ref_text": effective_ref_text,
            "voice_prompt": effective_voice_prompt,
            "voice_preset": effective_voice_preset,
            "voice_seed": effective_voice_seed,
            "speaker_id": effective_speaker_id,
            "emotion_ref_path": str(resolved_emo_audio) if resolved_emo_audio else "",
            "emotion_prompt": str(emotion_prompt or ""),
        }

        start_msg = f"开始远端 TTS 合成：{engine_name}（共 {len(segments)} 段）"
        _safe_task_update(task, message=start_msg)
        _safe_task_note(task, start_msg)

        if self.synth_fn is not None:
            segment_wavs = self._invoke_synth_fn(
                task=task,
                engine=engine,
                segments=segments,
                segments_dir=segments_dir,
                seg_entries=seg_entries,
                config=base_config,
                ref_audio_path=resolved_ref_audio,
                ref_text=effective_ref_text,
                voice_prompt=effective_voice_prompt,
                voice_preset=effective_voice_preset,
                voice_seed=effective_voice_seed,
                speaker_id=effective_speaker_id,
                emotion_ref_path=resolved_emo_audio,
                emotion_prompt=str(emotion_prompt or ""),
                hf_token=hf_token,
            )
        else:
            env_dir = self.ensure_engine_env(task, engine, hf_token="")
            worker_python = str(
                self._require_venv_python(env_dir / ".venv", label=engine_name)
            )
            source_dir = env_dir / "source"
            if source_dir.is_symlink():
                raise ValueError("TTS 源码目录不能是符号链接")
            if source_dir.is_dir():
                base_config["source_dir"] = str(source_dir)

            log_path = work_root / f"tts_{engine['id']}.log"
            if log_path.is_symlink():
                raise ValueError("TTS 日志文件路径不能是符号链接")

            used_warm = False
            if prefer_warm and self._should_use_warm_worker():
                lock_ctx = nullcontext() if _warm_lock_held else self._warm_exec_lock
                try:
                    with lock_ctx:
                        worker = self._ensure_warm_worker_locked(
                            task, engine, hf_token=hf_token
                        )
                        worker.call(
                            {"op": "synthesize", "config": base_config},
                            task=task,
                            timeout=1800.0,
                            log_path=log_path,
                            secret_to_scrub=hf_token,
                        )
                        self._schedule_warm_idle_timer()
                    used_warm = True
                except _ColabOneWarmWorkerTransportError:
                    # Only fall back to a cold one-shot subprocess when the daemon transport/process died.
                    self.release_warm_worker(reason="fallback_oneshot")
                    if not all(Path(s["output_wav"]).is_file() for s in seg_entries):
                        _safe_task_note(
                            task,
                            f"常驻 TTS 进程通道中断，自动回退至独立子进程重试：{engine_name}",
                            level="warning",
                        )
                        used_warm = False
                    else:
                        used_warm = True
                except Exception as exc:
                    if _is_cancelled_error(exc):
                        raise
                    if _is_warm_worker_cuda_fatal_error(exc):
                        self.release_warm_worker(reason="cuda_fatal_error")
                    raise

            if not used_warm:
                self.release_warm_worker(reason="before_oneshot_synth")
                worker_path = work_root / f"worker_{engine['id']}.py"
                config_path = work_root / f"config_{engine['id']}.json"
                if worker_path.is_symlink():
                    raise ValueError("TTS 工作脚本路径不能是符号链接")
                if config_path.is_symlink():
                    raise ValueError("TTS 配置文件路径不能是符号链接")

                worker_path.write_text(
                    self.build_worker_script(engine["id"]), encoding="utf-8"
                )
                atomic_json(config_path, base_config)

                # Pass HF_TOKEN strictly via worker subprocess environment (never CLI args, config JSON, or pip/git).
                hf_cache_dir = str(self.engines_dir / "_hf_cache")
                worker_env = _build_tts_worker_env(hf_cache_dir, hf_token=hf_token)

                self._invoke_command(
                    [worker_python, str(worker_path), "--config", str(config_path)],
                    task,
                    log_path,
                    timeout=1800,
                    cwd=work_root,
                    env=worker_env,
                    secret_to_scrub=hf_token,
                )
            segment_wavs = [
                (str(seg["text"]), Path(seg["output_wav"])) for seg in seg_entries
            ]

        for idx, (_, wav_file) in enumerate(segment_wavs):
            if not Path(wav_file).is_file():
                raise RuntimeError(
                    f"远端 TTS 引擎 {engine_name} 未生成第 {idx + 1} 段音频：{wav_file}"
                )

        resolved_target_sr = (
            int(target_sr)
            if (target_sr is not None and int(target_sr) > 0)
            else max(48000, int(engine.get("sample_rate") or 48000))
        )
        master_wav = work_root / "master.wav"
        manifest_path = work_root / "manifest.json"
        manifest = assemble_colabone_tts_master(
            segment_wavs,
            out_wav=master_wav,
            manifest_path=manifest_path,
            pause_seconds=pause_seconds,
            target_sr=resolved_target_sr,
            target_lufs=-23.0,
        )

        done_msg = (
            f"远端 TTS 合成完成：{engine_name}"
            f"（总时长 {manifest['duration']:.2f} 秒，{manifest['sample_rate']} Hz）"
        )
        _safe_task_update(task, message=done_msg)
        _safe_task_note(task, done_msg)
        return {
            "audio_path": master_wav,
            "manifest_path": manifest_path,
            "manifest": manifest,
            "duration": float(manifest["duration"]),
            "engine_id": engine["id"],
            "sample_rate": int(manifest["sample_rate"]),
        }
