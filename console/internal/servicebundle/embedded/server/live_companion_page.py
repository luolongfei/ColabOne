"""Self-contained mobile companion web UI for Live Studio remote mic & camera input.

Served directly from the remote Live Studio server over the existing bound HTTPS
tunnel domain (`/_colabone_live/remote`), with zero external CDN dependencies so
smartphones can stream 16 kHz microphone audio, live camera frames (multimodal
vision + real-time motion driving), and listen/watch the digital human live.
"""

from __future__ import annotations

import json

from common.live_studio import (
    COLABONE_LIVE_COMPANION_REDEEM_PATH,
    COLABONE_LIVE_WS_PATH,
)

_SUPPORTED_LOCALES = ("zh-CN", "en-US", "zh-TW", "zh-HK", "ja-JP", "ko-KR")


def resolve_colabone_companion_locale(
    accept_language: str = "", explicit_lang: str = ""
) -> str:
    """Resolve one of the 6 ColabOne locales from explicit ?lang= or Accept-Language."""
    raw = (explicit_lang or "").strip()
    if raw:
        tag = raw.lower().replace("_", "-")
        if (
            tag.startswith("zh-hk")
            or tag.startswith("zh-mo")
            or tag.startswith("zh-hant-hk")
            or tag.startswith("zh-hant-mo")
            or tag.startswith("yue")
        ):
            return "zh-HK"
        if tag.startswith("zh-tw") or tag.startswith("zh-hant"):
            return "zh-TW"
        if tag.startswith("zh"):
            return "zh-CN"
        if tag.startswith("ja"):
            return "ja-JP"
        if tag.startswith("ko"):
            return "ko-KR"
        if tag.startswith("en"):
            return "en-US"

    candidates: list[tuple[float, int, str]] = []
    for idx, item in enumerate((accept_language or "").split(",")):
        parts = [p.strip() for p in item.split(";") if p.strip()]
        if not parts:
            continue
        tag = parts[0].lower().replace("_", "-")
        q = 1.0
        for param in parts[1:]:
            if param.lower().startswith("q="):
                try:
                    q = float(param[2:])
                except ValueError:
                    q = 0.0
        if q > 0:
            candidates.append((q, -idx, tag))
    candidates.sort(reverse=True)
    for _, _, tag in candidates:
        if (
            tag.startswith("zh-hk")
            or tag.startswith("zh-mo")
            or tag.startswith("zh-hant-hk")
            or tag.startswith("zh-hant-mo")
            or tag.startswith("yue")
        ):
            return "zh-HK"
        if tag.startswith("zh-tw") or tag.startswith("zh-hant"):
            return "zh-TW"
        if tag.startswith("zh"):
            return "zh-CN"
        if tag.startswith("ja"):
            return "ja-JP"
        if tag.startswith("ko"):
            return "ko-KR"
        if tag.startswith("en"):
            return "en-US"
    return "zh-CN"


def _js_safe_json(value: object) -> str:
    return (
        json.dumps(value, ensure_ascii=False)
        .replace("<", "\\u003c")
        .replace(">", "\\u003e")
        .replace("&", "\\u0026")
        .replace("\u2028", "\\u2028")
        .replace("\u2029", "\\u2029")
    )


def render_colabone_live_companion_html(
    *,
    ws_path: str = COLABONE_LIVE_WS_PATH,
    redeem_path: str = COLABONE_LIVE_COMPANION_REDEEM_PATH,
    locale: str = "zh-CN",
) -> str:
    """Return the self-contained mobile companion HTML document."""
    safe_locale = locale if locale in _SUPPORTED_LOCALES else "zh-CN"
    html = (
        _COMPANION_HTML_TEMPLATE.replace("__COLABONE_HTML_LANG__", safe_locale)
        .replace("__COLABONE_WS_PATH_JSON__", _js_safe_json(ws_path))
        .replace("__COLABONE_REDEEM_PATH_JSON__", _js_safe_json(redeem_path))
        .replace("__COLABONE_INITIAL_LOCALE_JSON__", _js_safe_json(safe_locale))
    )
    return html


_COMPANION_HTML_TEMPLATE = r"""<!doctype html>
<html lang="__COLABONE_HTML_LANG__">
<head>
<meta charset="utf-8" />
<meta name="viewport" content="width=device-width,initial-scale=1,maximum-scale=1,user-scalable=no,viewport-fit=cover" />
<meta name="referrer" content="no-referrer" />
<title>ColabOne 实时演播 · 远端设备接入</title>
<style>
  :root {
    --bg: #0b0e14;
    --card: #151a24;
    --card-2: #1c2331;
    --border: rgba(255, 255, 255, 0.1);
    --text: #f3f5f9;
    --muted: #9aa4b8;
    --accent: #3b82f6;
    --accent-2: #2563eb;
    --ok: #10b981;
    --warn: #f59e0b;
    --danger: #ef4444;
  }
  * { box-sizing: border-box; -webkit-tap-highlight-color: transparent; }
  html, body {
    margin: 0;
    padding: 0;
    background: var(--bg);
    color: var(--text);
    font-family: -apple-system, BlinkMacSystemFont, "SF Pro Text", "Segoe UI", Roboto, "PingFang SC", "Microsoft YaHei", sans-serif;
    min-height: 100dvh;
    overscroll-behavior: none;
  }
  .wrap {
    max-width: 540px;
    margin: 0 auto;
    padding: max(12px, env(safe-area-inset-top)) 12px max(18px, env(safe-area-inset-bottom));
    display: flex;
    flex-direction: column;
    gap: 10px;
  }
  .topbar {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
    background: var(--card);
    border: 1px solid var(--border);
    border-radius: 14px;
    padding: 10px 12px;
  }
  .brand {
    display: flex;
    flex-direction: column;
    gap: 2px;
    min-width: 0;
  }
  .brand-title {
    font-size: 14px;
    font-weight: 700;
    letter-spacing: 0.2px;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .brand-sub {
    font-size: 11px;
    color: var(--muted);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .pill {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    font-size: 11.5px;
    font-weight: 600;
    padding: 5px 10px;
    border-radius: 999px;
    background: rgba(255, 255, 255, 0.08);
    color: var(--muted);
    white-space: nowrap;
  }
  .pill.ok { background: rgba(16, 185, 129, 0.18); color: #6ee7b7; }
  .pill.warn { background: rgba(245, 158, 11, 0.2); color: #fcd34d; }
  .pill.err { background: rgba(239, 68, 68, 0.2); color: #fca5a5; }
  .dot {
    width: 7px;
    height: 7px;
    border-radius: 50%;
    background: currentColor;
  }
  .stage-box {
    position: relative;
    width: 100%;
    aspect-ratio: 9 / 12;
    max-height: 44dvh;
    border-radius: 16px;
    overflow: hidden;
    background: radial-gradient(circle at 50% 25%, rgba(59, 130, 246, 0.16), #090c12 72%);
    border: 1px solid var(--border);
    display: flex;
    align-items: center;
    justify-content: center;
  }
  .stage-canvas, .stage-video {
    position: absolute;
    inset: 0;
    width: 100%;
    height: 100%;
    object-fit: contain;
  }
  .stage-video { display: none; }
  .stage-empty {
    text-align: center;
    padding: 18px;
    color: var(--muted);
    font-size: 13px;
    line-height: 1.5;
    z-index: 1;
  }
  .stage-hud {
    position: absolute;
    top: 8px;
    left: 8px;
    right: 8px;
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: 6px;
    z-index: 3;
    pointer-events: none;
  }
  .stage-hud > * { pointer-events: auto; }
  .hud-tag {
    background: rgba(11, 14, 20, 0.72);
    backdrop-filter: blur(8px);
    border: 1px solid rgba(255, 255, 255, 0.12);
    border-radius: 999px;
    padding: 4px 10px;
    font-size: 11px;
    color: #e5e7eb;
  }
  .btn-icon {
    appearance: none;
    border: 1px solid rgba(255, 255, 255, 0.18);
    background: rgba(11, 14, 20, 0.76);
    color: var(--text);
    border-radius: 999px;
    padding: 5px 11px;
    font-size: 12px;
    font-weight: 600;
    cursor: pointer;
  }
  .btn-icon.muted { border-color: rgba(245, 158, 11, 0.5); color: #fcd34d; }
  .pip-wrap {
    position: absolute;
    right: 10px;
    bottom: 10px;
    width: 92px;
    height: 122px;
    border-radius: 12px;
    overflow: hidden;
    border: 1.5px solid rgba(59, 130, 246, 0.7);
    background: #000;
    z-index: 4;
    display: none;
    box-shadow: 0 6px 20px rgba(0, 0, 0, 0.5);
  }
  .pip-wrap.show { display: block; }
  .pip-video { width: 100%; height: 100%; object-fit: cover; }
  .pip-badge {
    position: absolute;
    left: 4px;
    bottom: 4px;
    right: 4px;
    text-align: center;
    font-size: 10px;
    background: rgba(0, 0, 0, 0.65);
    color: #93c5fd;
    border-radius: 6px;
    padding: 2px 4px;
  }
  .unlock-bar {
    display: none;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
    background: rgba(245, 158, 11, 0.16);
    border: 1px solid rgba(245, 158, 11, 0.35);
    color: #fde68a;
    border-radius: 12px;
    padding: 8px 12px;
    font-size: 12.5px;
  }
  .unlock-bar.show { display: flex; }
  .card {
    background: var(--card);
    border: 1px solid var(--border);
    border-radius: 14px;
    padding: 12px;
    display: flex;
    flex-direction: column;
    gap: 10px;
  }
  .card-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
    font-size: 13px;
    font-weight: 600;
  }
  .sub-hint {
    font-size: 11.5px;
    color: var(--muted);
    line-height: 1.4;
  }
  .row {
    display: flex;
    align-items: center;
    gap: 8px;
    flex-wrap: wrap;
  }
  .btn {
    appearance: none;
    border: 1px solid var(--border);
    background: var(--card-2);
    color: var(--text);
    border-radius: 11px;
    padding: 9px 14px;
    font-size: 13px;
    font-weight: 600;
    cursor: pointer;
    flex: 1;
    min-width: 96px;
    text-align: center;
  }
  .btn:disabled { opacity: 0.45; cursor: not-allowed; }
  .btn.primary { background: var(--accent); border-color: var(--accent); color: #fff; }
  .btn.danger { background: rgba(239, 68, 68, 0.18); border-color: rgba(239, 68, 68, 0.45); color: #fca5a5; }
  .btn.active-ok { background: rgba(16, 185, 129, 0.2); border-color: rgba(16, 185, 129, 0.55); color: #6ee7b7; }
  .btn.ptt {
    padding: 14px 16px;
    font-size: 15px;
    background: linear-gradient(135deg, #2563eb, #3b82f6);
    border-color: #60a5fa;
    color: #fff;
    user-select: none;
    -webkit-user-select: none;
    touch-action: none;
  }
  .btn.ptt.holding {
    background: linear-gradient(135deg, #059669, #10b981);
    border-color: #6ee7b7;
    transform: scale(0.99);
  }
  .seg {
    display: grid;
    grid-template-columns: repeat(4, 1fr);
    gap: 5px;
    background: rgba(255, 255, 255, 0.04);
    padding: 4px;
    border-radius: 11px;
    border: 1px solid var(--border);
  }
  .seg.cols-2 { grid-template-columns: repeat(2, 1fr); }
  .seg button {
    appearance: none;
    border: 0;
    background: transparent;
    color: var(--muted);
    padding: 7px 4px;
    border-radius: 8px;
    font-size: 12px;
    font-weight: 600;
    cursor: pointer;
  }
  .seg button.active {
    background: var(--accent);
    color: #fff;
  }
  .meter {
    height: 7px;
    width: 100%;
    border-radius: 999px;
    background: rgba(255, 255, 255, 0.08);
    overflow: hidden;
  }
  .meter-fill {
    height: 100%;
    width: 0%;
    border-radius: 999px;
    background: linear-gradient(90deg, #3b82f6, #10b981);
    transition: width 60ms linear;
  }
  .transcripts {
    max-height: 132px;
    overflow-y: auto;
    display: flex;
    flex-direction: column;
    gap: 6px;
    font-size: 12.5px;
    line-height: 1.45;
  }
  .msg-item {
    padding: 6px 9px;
    border-radius: 9px;
    background: rgba(255, 255, 255, 0.05);
    word-break: break-word;
  }
  .msg-item.user { background: rgba(59, 130, 246, 0.16); }
  .msg-item.system { background: rgba(245, 158, 11, 0.14); color: #fde68a; }
  .msg-role { font-weight: 700; margin-right: 6px; color: #93c5fd; }
  .msg-item.assistant .msg-role { color: #6ee7b7; }
  .msg-item.system .msg-role { color: #fcd34d; }
  .input-row {
    display: flex;
    gap: 8px;
  }
  .text-in {
    flex: 1;
    min-width: 0;
    background: rgba(255, 255, 255, 0.06);
    border: 1px solid var(--border);
    border-radius: 10px;
    padding: 9px 11px;
    color: var(--text);
    font-size: 13.5px;
    outline: none;
  }
  .text-in:focus { border-color: var(--accent); }
  .banner {
    display: none;
    padding: 10px 12px;
    border-radius: 12px;
    font-size: 12.5px;
    line-height: 1.45;
    background: rgba(239, 68, 68, 0.16);
    border: 1px solid rgba(239, 68, 68, 0.4);
    color: #fecaca;
  }
  .banner.show { display: block; }
  .banner.info {
    background: rgba(59, 130, 246, 0.16);
    border-color: rgba(59, 130, 246, 0.4);
    color: #bfdbfe;
  }
</style>
</head>
<body>
<div class="wrap">
  <div class="topbar">
    <div class="brand">
      <div class="brand-title" id="brandTitle">ColabOne 实时演播 · 远端设备</div>
      <div class="brand-sub" id="deviceLabel">正在检测设备…</div>
    </div>
    <div class="pill warn" id="connPill"><span class="dot"></span><span id="connText">正在配对…</span></div>
  </div>

  <div class="banner" id="alertBanner"></div>

  <div class="unlock-bar" id="unlockBar">
    <span id="unlockText">浏览器暂停了自动播放，点一下即可收听 AI 回复</span>
    <button class="btn-icon" id="unlockAudioBtn" type="button">开启声音</button>
  </div>

  <div class="stage-box" id="stageBox">
    <canvas class="stage-canvas" id="stageCanvas"></canvas>
    <video class="stage-video" id="stageVideo" playsinline muted autoplay></video>
    <div class="stage-empty" id="stageEmpty">实时数字人画面准备中…<br />开启下方麦克风或摄像头即可与 AI 主播实时互动</div>
    <div class="stage-hud">
      <span class="hud-tag" id="directorTag">待机</span>
      <button class="btn-icon" id="muteBtn" type="button">🔊 监听已开</button>
    </div>
    <div class="pip-wrap" id="pipWrap">
      <video class="pip-video" id="camPreview" playsinline muted autoplay></video>
      <div class="pip-badge" id="pipBadge">摄像头</div>
    </div>
  </div>

  <!-- Microphone Control Card -->
  <div class="card">
    <div class="card-head">
      <span id="micCardTitle">🎙️ 手机麦克风语音输入</span>
      <span class="sub-hint" id="micHint">未开启</span>
    </div>
    <div class="row">
      <button class="btn primary" id="micToggleBtn" type="button" disabled>开启手机麦克风</button>
      <button class="btn danger" id="bargeInBtn" type="button" disabled>一键打断</button>
    </div>
    <div class="seg cols-2" id="micModeSeg">
      <button type="button" data-mode="vad" class="active" id="micVadBtn">自由全双工 (VAD)</button>
      <button type="button" data-mode="ptt" id="micPttBtn">按住说话 (PTT)</button>
    </div>
    <button class="btn ptt" id="pttBtn" type="button" style="display:none">按住说话 · 松开发送</button>
    <div class="meter"><div class="meter-fill" id="rmsFill"></div></div>
  </div>

  <!-- Camera Video Stream Card -->
  <div class="card">
    <div class="card-head">
      <span id="camCardTitle">📷 手机摄像头视频流</span>
      <span class="sub-hint" id="camHint">已关闭</span>
    </div>
    <div class="seg" id="camModeSeg">
      <button type="button" data-cam="off" class="active" id="camOffBtn">关闭</button>
      <button type="button" data-cam="vision" id="camVisionBtn">看图看货</button>
      <button type="button" data-cam="motion" id="camMotionBtn">动作驱动</button>
      <button type="button" data-cam="vision_motion" id="camDualBtn">双开</button>
    </div>
    <div class="row">
      <button class="btn" id="flipCamBtn" type="button" disabled>切换前后镜头</button>
      <button class="btn" id="lookNowBtn" type="button" disabled>让主播看一眼</button>
    </div>
    <div class="sub-hint" id="camModeDesc">
      「看图看货」会把实时镜头画面随语音发给多模态大模型讲解；「动作驱动」会把你的头部/身体姿态实时映射到数字人动作。
    </div>
  </div>

  <!-- Subtitles & Quick Text / Director Input -->
  <div class="card">
    <div class="card-head">
      <span id="subCardTitle">💬 实时字幕与文字导播</span>
      <div class="seg cols-2" id="dirModeSeg" style="width:170px">
        <button type="button" data-dir="dialogue" class="active" id="dirDialogueBtn">观众连麦</button>
        <button type="button" data-dir="director" id="dirDirectorBtn">幕后导演</button>
      </div>
    </div>
    <div class="transcripts" id="transcriptBox">
      <div class="sub-hint" id="transcriptEmpty">开口说话或输入文字，实时对话记录将显示在这里。</div>
    </div>
    <div class="input-row">
      <input class="text-in" id="textInput" type="text" placeholder="输入提问或导演指令…" />
      <button class="btn primary" id="sendBtn" type="button" style="flex:0 0 auto;min-width:68px" disabled>发送</button>
    </div>
  </div>
</div>

<script>
(() => {
  const WS_PATH = __COLABONE_WS_PATH_JSON__;
  const REDEEM_PATH = __COLABONE_REDEEM_PATH_JSON__;
  const INITIAL_LOCALE = __COLABONE_INITIAL_LOCALE_JSON__;
  const FRAME_MIC_PCM = 0x01;
  const FRAME_VIDEO = 0x02;
  const FRAME_AUDIO_PCM = 0x03;
  const FRAME_FMP4 = 0x04;
  const FRAME_CAMERA_JPEG = 0x05;
  const HEADER_SIZE = 13;
  const MIC_RATE = 16000;
  const MIC_CHUNK_SAMPLES = 320; // 20ms @ 16kHz
  const OUT_AUDIO_RATE = 24000;

  const I18N = {
    "zh-CN": {
      pageTitle: "ColabOne 实时演播 · 远端设备接入",
      brandTitle: "ColabOne 实时演播 · 远端设备",
      pairing: "正在配对…",
      verifying: "验证二维码…",
      connecting: "建立实时连接…",
      connected: "已连接演播室",
      missingCode: "缺少配对码",
      missingCodeDesc: "请使用手机扫描电脑端 ColabOne 实时演播页面的二维码接入。",
      pairFailed: "配对失败",
      pairExpired: "二维码已失效或已被使用，请在电脑端点击「刷新二维码」后重新扫码。",
      errRateLimited: "尝试次数过多，请稍后再试。",
      errDisabled: "当前实时演播会话未启用远端设备配对。",
      errBadRequest: "配对请求格式无效，请重新扫码。",
      errTimeout: "配对请求超时，请检查网络后重试。",
      errRedeemFailed: "配对验证失败，请刷新二维码后重试。",
      errUnauthorized: "认证失效或会话已过期，请在电脑端重新扫码。",
      errForbidden: "当前设备无权执行该操作。",
      errTextTooLong: "输入文本过长（单次上限 4000 字符）。",
      errTurnFailed: "本轮 AI 回复失败，请稍后重试。",
      errAsrFailed: "语音识别暂时失败，请重试。",
      noticeVisionFallback: "当前大脑模型不支持图像理解，已自动切换纯文本回复；如需看图看货，请配置支持视觉的多模态模型。",
      netErr: "网络异常",
      netErrPrefix: "连接远端演播服务失败：",
      unlockText: "浏览器暂停了自动播放，点一下即可收听 AI 回复",
      unlockBtn: "开启声音",
      stageEmpty1: "实时数字人画面准备中…",
      stageEmpty2: "开启下方麦克风或摄像头即可与 AI 主播实时互动",
      audioOn: "🔊 监听已开",
      audioOff: "🔇 已静音",
      cameraBadge: "摄像头",
      micCardTitle: "🎙️ 手机麦克风语音输入",
      micOffHint: "未开启",
      micStart: "开启手机麦克风",
      micStop: "关闭手机麦克风",
      bargeIn: "一键打断",
      micVad: "自由全双工 (VAD)",
      micPtt: "按住说话 (PTT)",
      pttIdle: "按住说话 · 松开发送",
      pttHolding: "正在说话… 松开发送",
      micHintPtt: "按住下方按钮说话",
      micHintVad: "全双工收音中",
      micUnsupported: "当前浏览器不支持麦克风权限，请确认通过 HTTPS 域名访问（iOS 建议使用 Safari，Android 建议使用 Chrome）",
      micErrPrefix: "无法开启手机麦克风：",
      camCardTitle: "📷 手机摄像头视频流",
      camOffHint: "已关闭",
      camOff: "关闭",
      camVision: "看图看货",
      camMotion: "动作驱动",
      camDual: "双开",
      camVisionActive: "看图看货中",
      camMotionActive: "动作驱动中",
      camDualActive: "看图+动作双开",
      camDualShort: "看图+动作",
      flipCam: "切换前后镜头",
      lookNow: "让主播看一眼",
      camModeDesc: "「看图看货」会把实时镜头画面随语音发给多模态大模型讲解；「动作驱动」会把你的头部/身体姿态实时映射到数字人动作。",
      camUnsupported: "当前浏览器不支持摄像头权限，请确认通过 HTTPS 域名访问",
      camErrPrefix: "无法开启手机摄像头：",
      camInterrupted: "摄像头流已被系统中断，已自动关闭。",
      subCardTitle: "💬 实时字幕与文字导播",
      dirDialogue: "观众连麦",
      dirDirector: "幕后导演",
      transcriptEmpty: "开口说话或输入文字，实时对话记录将显示在这里。",
      textPlaceholder: "输入提问或导演指令…",
      send: "发送",
      roleUser: "我",
      roleSystem: "提示",
      roleAssistant: "主播",
      lookPrompt: "请看一下我手机镜头里正在展示的画面，并用自然的主播口吻介绍一下。",
      lookEcho: "（镜头看图）请介绍一下镜头里的画面",
      kickedReplaced: "已有新设备扫码接入，本设备已自动下线。",
      kickedRevoked: "电脑端已主动断开本设备的连接。",
      statusReplaced: "已被新设备接管",
      statusRevoked: "已被电脑端断开",
      statusAuthExpired: "认证失效",
      statusDisconnected: "连接已断开",
      descReplaced: "已有新设备扫码接入，当前连接已自动断开。",
      descRevoked: "电脑端已断开远端设备连接；如需重连请在电脑端重新扫码。",
      descAuthExpired: "配对凭证已过期，请在电脑端刷新二维码后重新扫码。",
      descDisconnected: "实时连接已中断，请在电脑端确认演播运行中并重新扫码。",
      mobileDevice: "移动设备",
      browser: "浏览器",
      states: { IDLE: "待机", LISTENING: "正在倾听…", THINKING: "思考中…", SPEAKING: "播报中", PERFORMING: "才艺表演" },
    },
    "en-US": {
      pageTitle: "ColabOne Live Studio · Remote Companion",
      brandTitle: "ColabOne Live Studio · Remote",
      pairing: "Pairing…",
      verifying: "Verifying QR…",
      connecting: "Connecting…",
      connected: "Connected",
      missingCode: "Missing code",
      missingCodeDesc: "Scan the QR code from ColabOne Live Studio on your computer to connect.",
      pairFailed: "Pairing failed",
      pairExpired: "QR code expired or already used. Click Refresh QR on desktop and scan again.",
      errRateLimited: "Too many attempts. Please wait a moment and try again.",
      errDisabled: "Remote companion pairing is not enabled for this Live Studio session.",
      errBadRequest: "Invalid pairing request. Please scan the QR code again.",
      errTimeout: "Pairing request timed out. Please check your connection and retry.",
      errRedeemFailed: "Pairing verification failed. Refresh the QR code and try again.",
      errUnauthorized: "Session token invalid or expired. Scan a fresh QR code on desktop.",
      errForbidden: "This device does not have permission for that host action.",
      errTextTooLong: "Message is too long (maximum 4,000 characters).",
      errTurnFailed: "AI response failed for this turn. Please try again.",
      errAsrFailed: "Speech recognition failed. Please try speaking again.",
      noticeVisionFallback: "The active LLM does not support image input; switched to text-only replies. Choose a multimodal model (e.g., GPT-4o, Gemini, Qwen-VL) for camera vision.",
      netErr: "Network error",
      netErrPrefix: "Failed to reach Live Studio: ",
      unlockText: "Autoplay was paused by your browser. Tap to hear the AI host.",
      unlockBtn: "Enable sound",
      stageEmpty1: "Preparing live digital human video…",
      stageEmpty2: "Enable mic or camera below to interact in real time.",
      audioOn: "🔊 Audio On",
      audioOff: "🔇 Muted",
      cameraBadge: "Camera",
      micCardTitle: "🎙️ Mobile Microphone Input",
      micOffHint: "Off",
      micStart: "Start Microphone",
      micStop: "Stop Microphone",
      bargeIn: "Interrupt",
      micVad: "Full-Duplex (VAD)",
      micPtt: "Push-to-Talk (PTT)",
      pttIdle: "Hold to talk · Release to send",
      pttHolding: "Speaking… Release to send",
      micHintPtt: "Hold the button below to speak",
      micHintVad: "Full-duplex listening",
      micUnsupported: "Microphone API unavailable. Please access via HTTPS (Safari on iOS, Chrome on Android).",
      micErrPrefix: "Unable to start microphone: ",
      camCardTitle: "📷 Mobile Camera Stream",
      camOffHint: "Off",
      camOff: "Off",
      camVision: "Vision",
      camMotion: "Motion",
      camDual: "Both",
      camVisionActive: "Vision active",
      camMotionActive: "Motion active",
      camDualActive: "Vision + Motion",
      camDualShort: "Vision+Motion",
      flipCam: "Switch Camera",
      lookNow: "Ask Host to Look",
      camModeDesc: "Vision sends live frames to multimodal LLMs; Motion transfers your head/body movement to the avatar.",
      camUnsupported: "Camera API unavailable. Please access via HTTPS.",
      camErrPrefix: "Unable to start camera: ",
      camInterrupted: "Camera stream was interrupted by the system and stopped.",
      subCardTitle: "💬 Live Subtitles & Director Chat",
      dirDialogue: "Audience",
      dirDirector: "Director",
      transcriptEmpty: "Speak or type below to see live conversation transcripts here.",
      textPlaceholder: "Ask a question or send a cue…",
      send: "Send",
      roleUser: "Me",
      roleSystem: "Notice",
      roleAssistant: "Host",
      lookPrompt: "Please look at what my phone camera is showing right now and describe it naturally.",
      lookEcho: "(Camera) Please describe what is in view",
      kickedReplaced: "A newer device paired; this device has been disconnected.",
      kickedRevoked: "Disconnected by the desktop host.",
      statusReplaced: "Replaced",
      statusRevoked: "Disconnected",
      statusAuthExpired: "Auth expired",
      statusDisconnected: "Disconnected",
      descReplaced: "A newer device paired; this session was automatically closed.",
      descRevoked: "Desktop host disconnected this device. Scan a new QR code to reconnect.",
      descAuthExpired: "Pairing token expired. Refresh the QR code on desktop and scan again.",
      descDisconnected: "Live connection lost. Verify Live Studio is running on desktop and scan again.",
      mobileDevice: "Mobile",
      browser: "Browser",
      states: { IDLE: "Idle", LISTENING: "Listening…", THINKING: "Thinking…", SPEAKING: "Speaking", PERFORMING: "Performing" },
    },
    "zh-TW": {
      pageTitle: "ColabOne 即時演播 · 遠端裝置接入",
      brandTitle: "ColabOne 即時演播 · 遠端裝置",
      pairing: "正在配對…",
      verifying: "驗證 QR Code…",
      connecting: "建立即時連線…",
      connected: "已連線演播室",
      missingCode: "缺少配對碼",
      missingCodeDesc: "請使用手機掃描電腦端 ColabOne 即時演播頁面的 QR Code 接入。",
      pairFailed: "配對失敗",
      pairExpired: "QR Code 已失效或已被使用，請在電腦端點擊「重新整理 QR Code」後重新掃描。",
      errRateLimited: "嘗試次數過多，請稍後再試。",
      errDisabled: "目前即時演播工作階段未啟用遠端裝置配對。",
      errBadRequest: "配對請求格式無效，請重新掃描。",
      errTimeout: "配對請求逾時，請檢查網路後重試。",
      errRedeemFailed: "配對驗證失敗，請重新整理 QR Code 後重試。",
      errUnauthorized: "驗證失效或工作階段已過期，請在電腦端重新掃描。",
      errForbidden: "目前裝置無權執行此操作。",
      errTextTooLong: "輸入文字過長（單次上限 4000 字元）。",
      errTurnFailed: "本輪 AI 回覆失敗，請稍後重試。",
      errAsrFailed: "語音辨識暫時失敗，請重試。",
      noticeVisionFallback: "目前大腦模型不支援影像理解，已自動切換純文字回覆；如需看圖看貨，請設定支援視覺的多模態模型。",
      netErr: "網路異常",
      netErrPrefix: "連線遠端演播服務失敗：",
      unlockText: "瀏覽器已暫停自動播放，點一下即可收聽 AI 回覆",
      unlockBtn: "開啟聲音",
      stageEmpty1: "即時數位人畫面準備中…",
      stageEmpty2: "開啟下方麥克風或鏡頭即可與 AI 主播即時互動",
      audioOn: "🔊 監聽已開",
      audioOff: "🔇 已靜音",
      cameraBadge: "鏡頭",
      micCardTitle: "🎙️ 手機麥克風語音輸入",
      micOffHint: "未開啟",
      micStart: "開啟手機麥克風",
      micStop: "關閉手機麥克風",
      bargeIn: "一鍵打斷",
      micVad: "自由全雙工 (VAD)",
      micPtt: "按住說話 (PTT)",
      pttIdle: "按住說話 · 放開發送",
      pttHolding: "正在說話… 放開發送",
      micHintPtt: "按住下方按鈕說話",
      micHintVad: "全雙工收音中",
      micUnsupported: "目前瀏覽器不支援麥克風權限，請確認透過 HTTPS 網域存取（iOS 建議使用 Safari，Android 建議使用 Chrome）",
      micErrPrefix: "無法開啟手機麥克風：",
      camCardTitle: "📷 手機鏡頭視訊串流",
      camOffHint: "已關閉",
      camOff: "關閉",
      camVision: "看圖看貨",
      camMotion: "動作驅動",
      camDual: "雙開",
      camVisionActive: "看圖看貨中",
      camMotionActive: "動作驅動中",
      camDualActive: "看圖+動作雙開",
      camDualShort: "看圖+動作",
      flipCam: "切換前後鏡頭",
      lookNow: "讓主播看一眼",
      camModeDesc: "「看圖看貨」會將即時鏡頭畫面隨語音送給多模態模型解說；「動作驅動」會將頭部/身體姿態即時映射到數位人動作。",
      camUnsupported: "目前瀏覽器不支援鏡頭權限，請確認透過 HTTPS 網域存取",
      camErrPrefix: "無法開啟手機鏡頭：",
      camInterrupted: "鏡頭串流已被系統中斷，已自動關閉。",
      subCardTitle: "💬 即時字幕與文字導播",
      dirDialogue: "觀眾連線",
      dirDirector: "幕後導演",
      transcriptEmpty: "開口說話或輸入文字，即時對話紀錄會顯示在這裡。",
      textPlaceholder: "輸入提問或導演指令…",
      send: "傳送",
      roleUser: "我",
      roleSystem: "提示",
      roleAssistant: "主播",
      lookPrompt: "請看一下我手機鏡頭裡正在展示的畫面，並用自然的主播口吻介紹一下。",
      lookEcho: "（鏡頭看圖）請介紹一下鏡頭裡的畫面",
      kickedReplaced: "已有新裝置掃描接入，本裝置已自動離線。",
      kickedRevoked: "電腦端已主動中斷本裝置的連線。",
      statusReplaced: "已被新裝置接管",
      statusRevoked: "已被電腦端中斷",
      statusAuthExpired: "驗證失效",
      statusDisconnected: "連線已中斷",
      descReplaced: "已有新裝置掃描接入，目前連線已自動中斷。",
      descRevoked: "電腦端已中斷遠端裝置連線；如需重新連線請在電腦端重新掃描。",
      descAuthExpired: "配對憑證已過期，請在電腦端重新整理 QR Code 後重新掃描。",
      descDisconnected: "即時連線已中斷，請在電腦端確認演播執行中並重新掃描。",
      mobileDevice: "行動裝置",
      browser: "瀏覽器",
      states: { IDLE: "待機", LISTENING: "正在傾聽…", THINKING: "思考中…", SPEAKING: "播報中", PERFORMING: "才藝表演" },
    },
    "zh-HK": {
      pageTitle: "ColabOne 即時演播 · 遠端裝置接入",
      brandTitle: "ColabOne 即時演播 · 遠端裝置",
      pairing: "正在配對…",
      verifying: "驗證二維碼…",
      connecting: "建立即時連線…",
      connected: "已連接演播室",
      missingCode: "缺少配對碼",
      missingCodeDesc: "請用手機掃描電腦端 ColabOne 即時演播頁面的二維碼接入。",
      pairFailed: "配對失敗",
      pairExpired: "二維碼已失效或已被使用，請在電腦端按「重新整理二維碼」後重新掃描。",
      errRateLimited: "嘗試次數過多，請稍後再試。",
      errDisabled: "目前即時演播會話未啟用遠端裝置配對。",
      errBadRequest: "配對請求格式無效，請重新掃描。",
      errTimeout: "配對請求逾時，請檢查網絡後重試。",
      errRedeemFailed: "配對驗證失敗，請重新整理二維碼後重試。",
      errUnauthorized: "認證失效或會話已過期，請在電腦端重新掃描。",
      errForbidden: "目前裝置無權執行此操作。",
      errTextTooLong: "輸入文字過長（單次上限 4000 字元）。",
      errTurnFailed: "本輪 AI 回覆失敗，請稍後重試。",
      errAsrFailed: "語音識別暫時失敗，請重試。",
      noticeVisionFallback: "目前大腦模型不支援圖像理解，已自動切換純文字回覆；如需看圖看貨，請配置支援視覺的多模態模型。",
      netErr: "網絡異常",
      netErrPrefix: "連接遠端演播服務失敗：",
      unlockText: "瀏覽器已暫停自動播放，輕按一下即可收聽 AI 回覆",
      unlockBtn: "開啟聲音",
      stageEmpty1: "即時數碼人畫面準備中…",
      stageEmpty2: "開啟下方咪高峰或鏡頭即可與 AI 主播即時互動",
      audioOn: "🔊 監聽已開",
      audioOff: "🔇 已靜音",
      cameraBadge: "鏡頭",
      micCardTitle: "🎙️ 手機咪高峰語音輸入",
      micOffHint: "未開啟",
      micStart: "開啟手機咪高峰",
      micStop: "關閉手機咪高峰",
      bargeIn: "一鍵打斷",
      micVad: "自由全雙工 (VAD)",
      micPtt: "按住說話 (PTT)",
      pttIdle: "按住說話 · 放開發送",
      pttHolding: "正在說話… 放開發送",
      micHintPtt: "按住下方按鈕說話",
      micHintVad: "全雙工收音中",
      micUnsupported: "目前瀏覽器不支援咪高峰權限，請確認經 HTTPS 域名存取（iOS 建議用 Safari，Android 建議用 Chrome）",
      micErrPrefix: "無法開啟手機咪高峰：",
      camCardTitle: "📷 手機鏡頭視訊串流",
      camOffHint: "已關閉",
      camOff: "關閉",
      camVision: "看圖看貨",
      camMotion: "動作驅動",
      camDual: "雙開",
      camVisionActive: "看圖看貨中",
      camMotionActive: "動作驅動中",
      camDualActive: "看圖+動作雙開",
      camDualShort: "看圖+動作",
      flipCam: "切換前後鏡頭",
      lookNow: "讓主播看一眼",
      camModeDesc: "「看圖看貨」會將即時鏡頭畫面連語音發給多模態模型介紹；「動作驅動」會將頭部/身體姿態即時映射到數碼人動作。",
      camUnsupported: "目前瀏覽器不支援鏡頭權限，請確認經 HTTPS 域名存取",
      camErrPrefix: "無法開啟手機鏡頭：",
      camInterrupted: "鏡頭串流已被系統中斷，已自動關閉。",
      subCardTitle: "💬 即時字幕與文字導播",
      dirDialogue: "觀眾連麥",
      dirDirector: "幕後導演",
      transcriptEmpty: "開口說話或輸入文字，即時對話記錄會顯示於此。",
      textPlaceholder: "輸入提問或導演指令…",
      send: "發送",
      roleUser: "我",
      roleSystem: "提示",
      roleAssistant: "主播",
      lookPrompt: "請看一下我手機鏡頭裡正在展示的畫面，並用自然的主播口吻介紹一下。",
      lookEcho: "（鏡頭看圖）請介紹一下鏡頭裡的畫面",
      kickedReplaced: "已有新裝置掃描接入，本裝置已自動離線。",
      kickedRevoked: "電腦端已主動斷開本裝置的連線。",
      statusReplaced: "已被新裝置接管",
      statusRevoked: "已被電腦端斷開",
      statusAuthExpired: "認證失效",
      statusDisconnected: "連線已斷開",
      descReplaced: "已有新裝置掃描接入，目前連線已自動斷開。",
      descRevoked: "電腦端已斷開遠端裝置連線；如需重連請在電腦端重新掃描。",
      descAuthExpired: "配對憑證已過期，請在電腦端重新整理二維碼後重新掃描。",
      descDisconnected: "即時連線已中斷，請在電腦端確認演播執行中並重新掃描。",
      mobileDevice: "流動裝置",
      browser: "瀏覽器",
      states: { IDLE: "待機", LISTENING: "正在傾聽…", THINKING: "思考中…", SPEAKING: "播報中", PERFORMING: "才藝表演" },
    },
    "ja-JP": {
      pageTitle: "ColabOne ライブスタジオ · リモートデバイス",
      brandTitle: "ColabOne ライブスタジオ · リモート",
      pairing: "ペアリング中…",
      verifying: "QR コード確認中…",
      connecting: "接続中…",
      connected: "接続済み",
      missingCode: "コード未指定",
      missingCodeDesc: "PC 版 ColabOne ライブスタジオの QR コードをスキャンして接続してください。",
      pairFailed: "ペアリング失敗",
      pairExpired: "QR コードが無効または使用済みです。PC 側で QR コードを更新して再スキャンしてください。",
      errRateLimited: "試行回数が多すぎます。しばらくしてから再試行してください。",
      errDisabled: "このライブスタジオセッションではリモートペアリングが無効です。",
      errBadRequest: "ペアリング要求の形式が無効です。もう一度スキャンしてください。",
      errTimeout: "ペアリング要求がタイムアウトしました。通信環境を確認してください。",
      errRedeemFailed: "ペアリング検証に失敗しました。QR コードを更新して再試行してください。",
      errUnauthorized: "認証が無効または期限切れです。PC 側で QR を再スキャンしてください。",
      errForbidden: "この端末ではその操作を実行できません。",
      errTextTooLong: "入力テキストが長すぎます（最大 4000 文字）。",
      errTurnFailed: "このターンの AI 応答に失敗しました。もう一度お試しください。",
      errAsrFailed: "音声認識に失敗しました。もう一度話してみてください。",
      noticeVisionFallback: "現在の LLM は画像入力に未対応のためテキスト応答へ切り替えました。映像認識を使うにはマルチモーダルモデルを設定してください。",
      netErr: "通信エラー",
      netErrPrefix: "ライブスタジオへの接続に失敗しました：",
      unlockText: "ブラウザにより自動再生が一時停止されました。タップして音声を有効にしてください。",
      unlockBtn: "音声を有効化",
      stageEmpty1: "デジタルヒューマン映像を準備中…",
      stageEmpty2: "下のマイクまたはカメラをオンにしてリアルタイムで会話できます",
      audioOn: "🔊 音声オン",
      audioOff: "🔇 ミュート",
      cameraBadge: "カメラ",
      micCardTitle: "🎙️ スマホマイク音声入力",
      micOffHint: "オフ",
      micStart: "マイクをオン",
      micStop: "マイクをオフ",
      bargeIn: "割り込み停止",
      micVad: "全二重 (VAD)",
      micPtt: "長押し通話 (PTT)",
      pttIdle: "長押しで話す · 離して送信",
      pttHolding: "発話中… 離して送信",
      micHintPtt: "下のボタンを長押しして話してください",
      micHintVad: "全二重収音中",
      micUnsupported: "このブラウザはマイク権限に未対応です。HTTPS 経由でアクセスしてください（iOS は Safari、Android は Chrome 推奨）。",
      micErrPrefix: "マイクを起動できません：",
      camCardTitle: "📷 スマホカメラ映像配信",
      camOffHint: "オフ",
      camOff: "オフ",
      camVision: "映像認識",
      camMotion: "モーション",
      camDual: "両方",
      camVisionActive: "映像認識中",
      camMotionActive: "モーション駆動中",
      camDualActive: "映像+モーション",
      camDualShort: "映像+動作",
      flipCam: "カメラ切替",
      lookNow: "画面を見せる",
      camModeDesc: "「映像認識」はカメラ映像をマルチモーダル LLM へ送信し、「モーション」は頭・体の動きをアバターに反映します。",
      camUnsupported: "このブラウザはカメラ権限に未対応です。HTTPS 経由でアクセスしてください。",
      camErrPrefix: "カメラを起動できません：",
      camInterrupted: "カメラ映像がシステムにより中断されたため停止しました。",
      subCardTitle: "💬 ライブ字幕・テキスト指示",
      dirDialogue: "視聴者モード",
      dirDirector: "ディレクター",
      transcriptEmpty: "話すかテキストを入力すると、リアルタイムの会話ログがここに表示されます。",
      textPlaceholder: "質問やディレクター指示を入力…",
      send: "送信",
      roleUser: "自分",
      roleSystem: "案内",
      roleAssistant: "ホスト",
      lookPrompt: "スマホのカメラに映っている画面を見て、自然な口調で紹介してください。",
      lookEcho: "（カメラ）映っているものを紹介して",
      kickedReplaced: "新しいデバイスが接続されたため、このデバイスは切断されました。",
      kickedRevoked: "PC 側から接続が解除されました。",
      statusReplaced: "別デバイスへ切替",
      statusRevoked: "PC 側から切断",
      statusAuthExpired: "認証切れ",
      statusDisconnected: "切断されました",
      descReplaced: "新しいデバイスが接続されたため、現在の接続を終了しました。",
      descRevoked: "PC 側でリモートデバイスが切断されました。再接続するには QR コードを再スキャンしてください。",
      descAuthExpired: "ペアリング認証の有効期限が切れました。PC 側で QR を更新して再スキャンしてください。",
      descDisconnected: "リアルタイム接続が切れました。PC 側で配信が稼働中か確認して再スキャンしてください。",
      mobileDevice: "モバイル端末",
      browser: "ブラウザ",
      states: { IDLE: "待機中", LISTENING: "聞き取り中…", THINKING: "思考中…", SPEAKING: "発話中", PERFORMING: "パフォーマンス" },
    },
    "ko-KR": {
      pageTitle: "ColabOne 라이브 스튜디오 · 원격 기기",
      brandTitle: "ColabOne 라이브 스튜디오 · 원격",
      pairing: "페어링 중…",
      verifying: "QR 확인 중…",
      connecting: "연결 중…",
      connected: "연결됨",
      missingCode: "코드 없음",
      missingCodeDesc: "PC의 ColabOne 라이브 스튜디오 QR 코드를 스캔해 연결해 주세요.",
      pairFailed: "페어링 실패",
      pairExpired: "QR 코드가 만료되었거나 이미 사용되었습니다. PC에서 QR 코드를 새로고침 후 다시 스캔해 주세요.",
      errRateLimited: "시도 횟수가 너무 많습니다. 잠시 후 다시 시도해 주세요.",
      errDisabled: "현재 라이브 스튜디오 세션에서 원격 페어링이 활성화되어 있지 않습니다.",
      errBadRequest: "페어링 요청 형식이 올바르지 않습니다. 다시 스캔해 주세요.",
      errTimeout: "페어링 요청 시간이 초과되었습니다. 네트워크를 확인해 주세요.",
      errRedeemFailed: "페어링 확인에 실패했습니다. QR 코드를 새로고침 후 다시 시도해 주세요.",
      errUnauthorized: "인증이 만료되었습니다. PC에서 새 QR 코드를 스캔해 주세요.",
      errForbidden: "현재 기기에서는 해당 작업을 실행할 수 없습니다.",
      errTextTooLong: "입력 텍스트가 너무 깁니다(최대 4000자).",
      errTurnFailed: "이번 AI 응답 생성에 실패했습니다. 다시 시도해 주세요.",
      errAsrFailed: "음성 인식에 실패했습니다. 다시 말씀해 주세요.",
      noticeVisionFallback: "현재 LLM은 이미지 입력을 지원하지 않아 텍스트 응답으로 전환되었습니다. 화면 인식을 사용하려면 멀티모달 모델을 설정해 주세요.",
      netErr: "네트워크 오류",
      netErrPrefix: "라이브 스튜디오 연결 실패: ",
      unlockText: "브라우저가 자동 재생을 일시 중지했습니다. 탭해서 AI 음성을 켜 주세요.",
      unlockBtn: "소리 켜기",
      stageEmpty1: "실시간 디지털 휴먼 화면 준비 중…",
      stageEmpty2: "아래 마이크나 카메라를 켜면 AI 진행자와 실시간으로 대화할 수 있어요",
      audioOn: "🔊 소리 켜짐",
      audioOff: "🔇 음소거",
      cameraBadge: "카메라",
      micCardTitle: "🎙️ 모바일 마이크 음성 입력",
      micOffHint: "꺼짐",
      micStart: "마이크 켜기",
      micStop: "마이크 끄기",
      bargeIn: "즉시 중단",
      micVad: "양방향 자동 (VAD)",
      micPtt: "눌러서 말하기 (PTT)",
      pttIdle: "누르고 말하기 · 떼면 전송",
      pttHolding: "말하는 중… 떼면 전송",
      micHintPtt: "아래 버튼을 누른 채 말씀해 주세요",
      micHintVad: "양방향 음성 수신 중",
      micUnsupported: "현재 브라우저는 마이크 권한을 지원하지 않습니다. HTTPS 도메인으로 접속해 주세요(iOS Safari, Android Chrome 권장).",
      micErrPrefix: "마이크를 켤 수 없습니다: ",
      camCardTitle: "📷 모바일 카메라 영상 스트림",
      camOffHint: "꺼짐",
      camOff: "끄기",
      camVision: "화면 인식",
      camMotion: "동작 구동",
      camDual: "동시 사용",
      camVisionActive: "화면 인식 중",
      camMotionActive: "동작 구동 중",
      camDualActive: "인식+동작",
      camDualShort: "인식+동작",
      flipCam: "전/후면 전환",
      lookNow: "화면 보여주기",
      camModeDesc: "'화면 인식'은 카메라 영상을 멀티모달 LLM에 전달하고, '동작 구동'은 머리·몸 움직임을 아바타에 실시간 반영해요.",
      camUnsupported: "현재 브라우저는 카메라 권한을 지원하지 않습니다. HTTPS 도메인으로 접속해 주세요.",
      camErrPrefix: "카메라를 켤 수 없습니다: ",
      camInterrupted: "시스템에 의해 카메라 스트림이 중단되어 자동으로 꺼졌습니다.",
      subCardTitle: "💬 실시간 자막 및 디렉터 입력",
      dirDialogue: "시청자 대화",
      dirDirector: "디렉터 지시",
      transcriptEmpty: "말씀하시거나 텍스트를 입력하면 실시간 대화 기록이 여기에 표시됩니다.",
      textPlaceholder: "질문이나 디렉터 지시를 입력해 주세요…",
      send: "전송",
      roleUser: "나",
      roleSystem: "안내",
      roleAssistant: "진행자",
      lookPrompt: "제 스마트폰 카메라에 비친 화면을 보고 자연스러운 진행자 말투로 소개해 주세요.",
      lookEcho: "(카메라) 화면에 보이는 것을 소개해 주세요",
      kickedReplaced: "새 기기가 페어링되어 현재 기기 연결이 종료되었습니다.",
      kickedRevoked: "PC에서 원격 기기 연결을 해제했습니다.",
      statusReplaced: "새 기기로 전환됨",
      statusRevoked: "PC에서 연결 해제됨",
      statusAuthExpired: "인증 만료",
      statusDisconnected: "연결 끊김",
      descReplaced: "새 기기가 QR 스캔으로 연결되어 현재 세션이 자동으로 종료되었습니다.",
      descRevoked: "PC에서 원격 기기 연결을 해제했습니다. 다시 연결하려면 PC에서 QR 코드를 새로 스캔해 주세요.",
      descAuthExpired: "페어링 토큰이 만료되었습니다. PC에서 QR 코드를 새로고침 후 다시 스캔해 주세요.",
      descDisconnected: "실시간 연결이 끊어졌습니다. PC에서 라이브가 실행 중인지 확인하고 다시 스캔해 주세요.",
      mobileDevice: "모바일 기기",
      browser: "브라우저",
      states: { IDLE: "대기", LISTENING: "듣는 중…", THINKING: "생각 중…", SPEAKING: "말하는 중", PERFORMING: "공연 중" },
    },
  };

  function resolveClientLocale() {
    const qs = new URLSearchParams(window.location.search || "");
    const raw = (qs.get("lang") || INITIAL_LOCALE || navigator.language || "zh-CN").toLowerCase().replace(/_/g, "-");
    if (raw.startsWith("zh-hk") || raw.startsWith("zh-mo") || raw.startsWith("zh-hant-hk") || raw.startsWith("zh-hant-mo") || raw.startsWith("yue")) return "zh-HK";
    if (raw.startsWith("zh-tw") || raw.startsWith("zh-hant")) return "zh-TW";
    if (raw.startsWith("zh")) return "zh-CN";
    if (raw.startsWith("ja")) return "ja-JP";
    if (raw.startsWith("ko")) return "ko-KR";
    if (raw.startsWith("en")) return "en-US";
    return "zh-CN";
  }

  const activeLocale = resolveClientLocale();
  const L = I18N[activeLocale] || I18N["zh-CN"];
  document.documentElement.lang = activeLocale;
  document.title = L.pageTitle;

  const $ = (id) => document.getElementById(id);
  const connPill = $("connPill");
  const connText = $("connText");
  const deviceLabelEl = $("deviceLabel");
  const alertBanner = $("alertBanner");
  const unlockBar = $("unlockBar");
  const unlockAudioBtn = $("unlockAudioBtn");
  const stageCanvas = $("stageCanvas");
  const stageVideo = $("stageVideo");
  const stageEmpty = $("stageEmpty");
  const directorTag = $("directorTag");
  const muteBtn = $("muteBtn");
  const pipWrap = $("pipWrap");
  const camPreview = $("camPreview");
  const pipBadge = $("pipBadge");
  const micToggleBtn = $("micToggleBtn");
  const bargeInBtn = $("bargeInBtn");
  const micHint = $("micHint");
  const micModeSeg = $("micModeSeg");
  const pttBtn = $("pttBtn");
  const rmsFill = $("rmsFill");
  const camModeSeg = $("camModeSeg");
  const camHint = $("camHint");
  const flipCamBtn = $("flipCamBtn");
  const lookNowBtn = $("lookNowBtn");
  const dirModeSeg = $("dirModeSeg");
  const transcriptBox = $("transcriptBox");
  const textInput = $("textInput");
  const sendBtn = $("sendBtn");

  // Apply localized static UI strings immediately
  $("brandTitle").textContent = L.brandTitle;
  connText.textContent = L.pairing;
  $("unlockText").textContent = L.unlockText;
  unlockAudioBtn.textContent = L.unlockBtn;
  stageEmpty.textContent = "";
  stageEmpty.appendChild(document.createTextNode(L.stageEmpty1));
  stageEmpty.appendChild(document.createElement("br"));
  stageEmpty.appendChild(document.createTextNode(L.stageEmpty2));
  directorTag.textContent = L.states.IDLE;
  muteBtn.textContent = L.audioOn;
  pipBadge.textContent = L.cameraBadge;
  $("micCardTitle").textContent = L.micCardTitle;
  micHint.textContent = L.micOffHint;
  micToggleBtn.textContent = L.micStart;
  bargeInBtn.textContent = L.bargeIn;
  $("micVadBtn").textContent = L.micVad;
  $("micPttBtn").textContent = L.micPtt;
  pttBtn.textContent = L.pttIdle;
  $("camCardTitle").textContent = L.camCardTitle;
  camHint.textContent = L.camOffHint;
  $("camOffBtn").textContent = L.camOff;
  $("camVisionBtn").textContent = L.camVision;
  $("camMotionBtn").textContent = L.camMotion;
  $("camDualBtn").textContent = L.camDual;
  flipCamBtn.textContent = L.flipCam;
  lookNowBtn.textContent = L.lookNow;
  $("camModeDesc").textContent = L.camModeDesc;
  $("subCardTitle").textContent = L.subCardTitle;
  $("dirDialogueBtn").textContent = L.dirDialogue;
  $("dirDirectorBtn").textContent = L.dirDirector;
  $("transcriptEmpty").textContent = L.transcriptEmpty;
  textInput.placeholder = L.textPlaceholder;
  sendBtn.textContent = L.send;

  function detectDeviceLabel() {
    const ua = navigator.userAgent || "";
    let os = L.mobileDevice;
    if (/iPhone/i.test(ua)) os = "iPhone";
    else if (/iPad/i.test(ua)) os = "iPad";
    else if (/Android/i.test(ua)) os = "Android";
    else if (/Macintosh/i.test(ua)) os = "Mac";
    else if (/Windows/i.test(ua)) os = "Windows";
    let br = L.browser;
    if (/MicroMessenger/i.test(ua)) br = activeLocale.startsWith("zh") ? "微信" : "WeChat";
    else if (/CriOS|Chrome/i.test(ua) && !/Edg/i.test(ua)) br = "Chrome";
    else if (/Safari/i.test(ua) && !/Chrome/i.test(ua)) br = "Safari";
    else if (/Firefox/i.test(ua)) br = "Firefox";
    return os + " · " + br;
  }

  const deviceLabel = detectDeviceLabel();
  deviceLabelEl.textContent = deviceLabel;

  const MseCtor = typeof window.ManagedMediaSource !== "undefined"
    ? window.ManagedMediaSource
    : (typeof window.MediaSource !== "undefined" ? window.MediaSource : null);
  const usesManagedMse = typeof window.ManagedMediaSource !== "undefined" && MseCtor === window.ManagedMediaSource;

  let ws = null;
  let authed = false;
  let kickedReason = "";
  let currentEpoch = 0;
  let micMode = "vad";
  let directorMode = "dialogue";
  let audioMuted = false;
  let micRunning = false;
  let micStarting = false;
  let micReqSeq = 0;
  let pttHolding = false;
  let cameraMode = "off";
  let cameraFacing = "environment";
  let camReqSeq = 0;
  let micStream = null;
  let micCtx = null;
  let micSrc = null;
  let micProc = null;
  let micZeroGain = null;
  let micResidual = new Float32Array(0);
  let camStream = null;
  let camTimer = null;
  let camCanvas = null;
  let camCtx2d = null;
  let camSending = false;
  let camSendingToken = 0;
  let camFramesSent = 0;
  let camFpsWindowStart = performance.now();
  let audioOutCtx = null;
  let audioOutGain = null;
  let nextPlayTime = 0;
  let activeAudioSources = [];
  let mediaSource = null;
  let mediaSourceObjectUrl = "";
  let sourceBuffer = null;
  let fmp4MimeHint = "";
  let fmp4Queue = [];
  let fmp4InitSegment = null;
  let fmp4InitSent = false;
  let fmp4ErrorCount = 0;
  let fmp4SuppressError = false;
  let fmp4SeekToLive = false;
  let fallbackToJpeg = false;
  let jpegDecoding = false;
  let pendingJpegPayload = null;
  let hasFrame = false;
  const subtitles = [];

  function setStatus(kind, text) {
    connPill.className = "pill " + kind;
    connText.textContent = text;
  }

  function showBanner(msg, isInfo) {
    if (!msg) {
      alertBanner.className = "banner";
      alertBanner.textContent = "";
      return;
    }
    alertBanner.className = isInfo ? "banner show info" : "banner show";
    alertBanner.textContent = msg;
  }

  function setControlsEnabled(enabled) {
    micToggleBtn.disabled = !enabled;
    bargeInBtn.disabled = !enabled;
    sendBtn.disabled = !enabled;
    lookNowBtn.disabled = !enabled || (cameraMode !== "vision" && cameraMode !== "vision_motion");
    flipCamBtn.disabled = !enabled || cameraMode === "off";
  }

  function packFrame(frameType, epoch, ptsMs, payload) {
    const out = new Uint8Array(HEADER_SIZE + payload.byteLength);
    const dv = new DataView(out.buffer);
    dv.setUint8(0, frameType & 0xff);
    dv.setUint32(1, epoch >>> 0, false);
    const hi = Math.floor(ptsMs / 4294967296) >>> 0;
    const lo = (ptsMs >>> 0);
    dv.setUint32(5, hi, false);
    dv.setUint32(9, lo, false);
    out.set(payload, HEADER_SIZE);
    return out;
  }

  function unpackFrame(buf) {
    const arr = new Uint8Array(buf);
    if (arr.byteLength < HEADER_SIZE) return null;
    const dv = new DataView(arr.buffer, arr.byteOffset, arr.byteLength);
    const type = dv.getUint8(0);
    const epoch = dv.getUint32(1, false);
    const ptsMs = dv.getUint32(5, false) * 4294967296 + dv.getUint32(9, false);
    return { type, epoch, ptsMs, payload: arr.subarray(HEADER_SIZE) };
  }

  function sendJson(obj) {
    if (ws && ws.readyState === WebSocket.OPEN) {
      try {
        ws.send(JSON.stringify(obj));
      } catch (_) {}
    }
  }

  function syncCompanionState() {
    if (!authed) return;
    sendJson({
      type: "companion_state",
      mic_active: micRunning,
      camera_mode: cameraMode,
      device_label: deviceLabel,
    });
  }

  function addSubtitle(role, text, isDelta, epoch) {
    if (!text) return;
    if (isDelta && subtitles.length) {
      const last = subtitles[subtitles.length - 1];
      if (last.role === role && last.epoch === epoch && !last.final) {
        last.text += text;
        renderSubtitles();
        return;
      }
    }
    subtitles.push({ role, text, final: !isDelta, epoch });
    if (subtitles.length > 20) subtitles.shift();
    renderSubtitles();
  }

  function renderSubtitles() {
    transcriptBox.innerHTML = "";
    for (const item of subtitles) {
      const div = document.createElement("div");
      div.className = "msg-item " + item.role;
      const rSpan = document.createElement("span");
      rSpan.className = "msg-role";
      rSpan.textContent = item.role === "user" ? L.roleUser : item.role === "system" ? L.roleSystem : L.roleAssistant;
      const tSpan = document.createElement("span");
      tSpan.textContent = item.text;
      div.appendChild(rSpan);
      div.appendChild(tSpan);
      transcriptBox.appendChild(div);
    }
    transcriptBox.scrollTop = transcriptBox.scrollHeight;
  }

  function updateUnlockBarState() {
    if (!audioOutCtx) return;
    if (audioOutCtx.state !== "running" && audioOutCtx.state !== "closed") {
      unlockBar.classList.add("show");
    } else {
      unlockBar.classList.remove("show");
    }
  }

  function ensureOutAudio() {
    if (!audioOutCtx || audioOutCtx.state === "closed") {
      const Ctor = window.AudioContext || window.webkitAudioContext;
      if (!Ctor) return null;
      try {
        audioOutCtx = new Ctor({ sampleRate: OUT_AUDIO_RATE });
      } catch (_) {
        audioOutCtx = new Ctor();
      }
      audioOutGain = audioOutCtx.createGain();
      audioOutGain.gain.value = audioMuted ? 0 : 1;
      audioOutGain.connect(audioOutCtx.destination);
      nextPlayTime = audioOutCtx.currentTime;
      audioOutCtx.onstatechange = updateUnlockBarState;
    }
    updateUnlockBarState();
    return audioOutCtx;
  }

  async function resumeOutAudio() {
    const ctx = ensureOutAudio();
    if (!ctx) return;
    try {
      if (ctx.state !== "running" && ctx.state !== "closed") {
        await Promise.race([
          ctx.resume(),
          new Promise((resolve) => setTimeout(resolve, 600)),
        ]);
      }
    } catch (_) {}
    updateUnlockBarState();
  }

  function interruptOutAudio() {
    for (const src of activeAudioSources) {
      try { src.stop(); } catch (_) {}
      try { src.disconnect(); } catch (_) {}
    }
    activeAudioSources = [];
    if (audioOutCtx && audioOutCtx.state !== "closed") {
      nextPlayTime = audioOutCtx.currentTime;
    }
  }

  function enqueuePcm24k(payloadBytes) {
    const ctx = ensureOutAudio();
    if (!ctx || payloadBytes.byteLength < 2) return;
    if (ctx.state !== "running") {
      updateUnlockBarState();
      return;
    }
    const evenLen = payloadBytes.byteLength - (payloadBytes.byteLength % 2);
    const copyBuf = payloadBytes.slice(0, evenLen).buffer;
    const pcm16 = new Int16Array(copyBuf);
    const audioBuf = ctx.createBuffer(1, pcm16.length, OUT_AUDIO_RATE);
    const ch = audioBuf.getChannelData(0);
    for (let i = 0; i < pcm16.length; i++) {
      ch[i] = pcm16[i] < 0 ? pcm16[i] / 32768 : pcm16[i] / 32767;
    }
    const src = ctx.createBufferSource();
    src.buffer = audioBuf;
    src.connect(audioOutGain);
    const now = ctx.currentTime;
    if (nextPlayTime > now + 2.0) {
      interruptOutAudio();
      nextPlayTime = now + 0.025;
    } else if (nextPlayTime < now + 0.015) {
      nextPlayTime = now + 0.025;
    }
    src.start(nextPlayTime);
    nextPlayTime += audioBuf.duration;
    activeAudioSources.push(src);
    src.onended = () => {
      const idx = activeAudioSources.indexOf(src);
      if (idx >= 0) activeAudioSources.splice(idx, 1);
    };
  }

  function decodeNextJpeg() {
    if (jpegDecoding || !pendingJpegPayload) return;
    if (document.hidden) {
      pendingJpegPayload = null;
      return;
    }
    const payload = pendingJpegPayload;
    pendingJpegPayload = null;
    jpegDecoding = true;
    const blob = new Blob([payload], { type: "image/jpeg" });
    const url = URL.createObjectURL(blob);
    const img = new Image();
    img.onload = () => {
      try {
        if (!hasFrame) {
          hasFrame = true;
          stageEmpty.style.display = "none";
        }
        stageVideo.style.display = "none";
        stageCanvas.style.display = "block";
        if (stageCanvas.width !== img.naturalWidth || stageCanvas.height !== img.naturalHeight) {
          stageCanvas.width = img.naturalWidth;
          stageCanvas.height = img.naturalHeight;
        }
        const ctx = stageCanvas.getContext("2d");
        if (ctx) ctx.drawImage(img, 0, 0);
      } finally {
        URL.revokeObjectURL(url);
        jpegDecoding = false;
        if (pendingJpegPayload) decodeNextJpeg();
      }
    };
    img.onerror = () => {
      URL.revokeObjectURL(url);
      jpegDecoding = false;
      if (pendingJpegPayload) decodeNextJpeg();
    };
    img.src = url;
  }

  function renderJpegFrame(payload) {
    if (payload.byteLength < 4 || payload[0] !== 0xff || payload[1] !== 0xd8) return;
    if (document.hidden) return;
    pendingJpegPayload = payload.slice();
    decodeNextJpeg();
  }

  function isFmp4InitBytes(bytes) {
    return (
      bytes &&
      bytes.byteLength >= 8 &&
      bytes[4] === 0x66 &&
      bytes[5] === 0x74 &&
      bytes[6] === 0x79 &&
      bytes[7] === 0x70
    );
  }

  function splitFmp4InitAndMedia(bytes) {
    if (!isFmp4InitBytes(bytes)) {
      return { init: null, media: bytes };
    }
    const dv = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
    let offset = 0;
    let sawMoov = false;
    while (offset + 8 <= bytes.byteLength) {
      let boxSize = dv.getUint32(offset, false);
      const t0 = bytes[offset + 4], t1 = bytes[offset + 5], t2 = bytes[offset + 6], t3 = bytes[offset + 7];
      if (boxSize === 1) {
        if (offset + 16 > bytes.byteLength) break;
        const hi = dv.getUint32(offset + 8, false);
        const lo = dv.getUint32(offset + 12, false);
        boxSize = hi * 4294967296 + lo;
        if (boxSize < 16) break;
      } else if (boxSize < 8) {
        break;
      }
      if (offset + boxSize > bytes.byteLength) break;
      offset += boxSize;
      // "moov" = 0x6d 0x6f 0x6f 0x76
      if (t0 === 0x6d && t1 === 0x6f && t2 === 0x6f && t3 === 0x76) {
        sawMoov = true;
        break;
      }
    }
    if (!sawMoov) {
      return { init: bytes, media: null };
    }
    return {
      init: bytes.subarray(0, offset),
      media: offset < bytes.byteLength ? bytes.subarray(offset) : null,
    };
  }

  function teardownFmp4() {
    fmp4SuppressError = true;
    fmp4Queue = [];
    fmp4InitSegment = null;
    fmp4InitSent = false;
    fmp4SeekToLive = false;
    if (sourceBuffer) {
      try { sourceBuffer.removeEventListener("updateend", flushFmp4Queue); } catch (_) {}
      sourceBuffer = null;
    }
    if (mediaSource) {
      try {
        if (mediaSource.readyState === "open") mediaSource.endOfStream();
      } catch (_) {}
      mediaSource = null;
    }
    try { stageVideo.pause(); } catch (_) {}
    stageVideo.srcObject = null;
    stageVideo.removeAttribute("src");
    try { stageVideo.load(); } catch (_) {}
    if (mediaSourceObjectUrl) {
      try { URL.revokeObjectURL(mediaSourceObjectUrl); } catch (_) {}
      mediaSourceObjectUrl = "";
    }
    setTimeout(() => { fmp4SuppressError = false; }, 0);
  }

  function pickSupportedFmp4Mime() {
    if (!MseCtor) return "";
    const candidates = [
      fmp4MimeHint,
      'video/mp4; codecs="avc1.42E028"',
      'video/mp4; codecs="avc1.42E01E"',
      'video/mp4; codecs="avc1.4D4028"',
    ];
    for (const mime of candidates) {
      if (mime && MseCtor.isTypeSupported(mime)) return mime;
    }
    return "";
  }

  function requestJpegFallback() {
    if (fallbackToJpeg) return;
    fallbackToJpeg = true;
    teardownFmp4();
    stageVideo.style.display = "none";
    stageCanvas.style.display = "block";
    if (authed) {
      sendJson({ type: "video_prefs", video_format: "jpeg", supports_fmp4: false });
    }
  }

  function handleFmp4DecodeError() {
    fmp4ErrorCount += 1;
    const savedInit = fmp4InitSegment;
    teardownFmp4();
    if (fmp4ErrorCount >= 2 || !savedInit) {
      requestJpegFallback();
      return;
    }
    fmp4InitSegment = savedInit;
    fmp4Queue = [savedInit];
    initFmp4Pipeline();
  }

  function initFmp4Pipeline() {
    if (fallbackToJpeg || !MseCtor || mediaSource) return;
    const mime = pickSupportedFmp4Mime();
    if (!mime) {
      requestJpegFallback();
      return;
    }
    fmp4SuppressError = false;
    mediaSource = new MseCtor();
    if (usesManagedMse) {
      stageVideo.disableRemotePlayback = true;
      stageVideo.srcObject = mediaSource;
    } else {
      mediaSourceObjectUrl = URL.createObjectURL(mediaSource);
      stageVideo.src = mediaSourceObjectUrl;
    }
    const msRef = mediaSource;
    msRef.addEventListener("sourceopen", () => {
      if (mediaSource !== msRef || fallbackToJpeg) return;
      try {
        sourceBuffer = msRef.addSourceBuffer(mime);
        sourceBuffer.mode = "sequence";
        sourceBuffer.addEventListener("updateend", flushFmp4Queue);
        sourceBuffer.addEventListener("error", () => {
          if (mediaSource === msRef && !fmp4SuppressError) handleFmp4DecodeError();
        });
        flushFmp4Queue();
      } catch (_) {
        handleFmp4DecodeError();
      }
    });
  }

  stageVideo.addEventListener("error", () => {
    if (!fallbackToJpeg && !fmp4SuppressError && mediaSource) {
      handleFmp4DecodeError();
    }
  });

  function pushFmp4(payload) {
    if (fallbackToJpeg || !MseCtor || !payload || !payload.byteLength) return;
    const copy = payload.slice();
    const parts = splitFmp4InitAndMedia(copy);
    if (parts.init) {
      fmp4InitSegment = parts.init.slice();
      if (!mediaSource) initFmp4Pipeline();
      if (fallbackToJpeg) return;
      if (fmp4InitSent) {
        // New init segment (e.g. resolution/encoder change): enqueue in order so MSE switches cleanly.
        fmp4Queue.push(fmp4InitSegment);
      } else {
        fmp4Queue = [fmp4InitSegment];
      }
      if (parts.media && parts.media.byteLength > 0) {
        if (!hasFrame) {
          hasFrame = true;
          stageEmpty.style.display = "none";
        }
        stageCanvas.style.display = "none";
        stageVideo.style.display = "block";
        fmp4Queue.push(parts.media.slice());
      }
      flushFmp4Queue();
      return;
    }
    if (!mediaSource) initFmp4Pipeline();
    if (fallbackToJpeg) return;
    if (!hasFrame) {
      hasFrame = true;
      stageEmpty.style.display = "none";
    }
    stageCanvas.style.display = "none";
    stageVideo.style.display = "block";
    if (!fmp4InitSent && !fmp4InitSegment) {
      return;
    }
    fmp4Queue.push(copy);
    if (fmp4Queue.length > 16) {
      const keepInit = !fmp4InitSent && fmp4Queue.length > 0 && isFmp4InitBytes(fmp4Queue[0]) ? fmp4Queue[0] : null;
      fmp4Queue = fmp4Queue.slice(-8);
      if (keepInit && !isFmp4InitBytes(fmp4Queue[0])) {
        fmp4Queue.unshift(keepInit);
      }
    }
    flushFmp4Queue();
  }

  function flushFmp4Queue() {
    if (fallbackToJpeg || !sourceBuffer || sourceBuffer.updating) return;
    if (mediaSource && mediaSource.readyState !== "open") return;
    try {
      if (sourceBuffer.buffered && sourceBuffer.buffered.length > 0) {
        const start = sourceBuffer.buffered.start(0);
        const end = sourceBuffer.buffered.end(sourceBuffer.buffered.length - 1);
        const cur = stageVideo.currentTime || 0;
        if (fmp4SeekToLive || cur < start - 0.05 || (end - cur > 1.5 && stageVideo.readyState >= 2)) {
          stageVideo.currentTime = Math.max(start, end - 0.12);
          fmp4SeekToLive = false;
        }
        if (cur - start > 3.0) {
          sourceBuffer.remove(start, Math.max(start + 0.1, cur - 1.5));
          return;
        }
        if (end - start > 6.0) {
          fmp4SeekToLive = true;
          sourceBuffer.remove(start, Math.max(start + 0.25, end - 1.0));
          return;
        }
      }
    } catch (_) {}
    if (!fmp4Queue.length) return;
    const nextChunk = fmp4Queue.shift();
    if (!nextChunk) return;
    try {
      sourceBuffer.appendBuffer(nextChunk);
      if (isFmp4InitBytes(nextChunk)) fmp4InitSent = true;
      if (stageVideo.paused) {
        stageVideo.play().catch(() => {});
      }
    } catch (err) {
      if (err && err.name === "QuotaExceededError" && sourceBuffer && !sourceBuffer.updating) {
        fmp4Queue.unshift(nextChunk);
        try {
          if (sourceBuffer.buffered && sourceBuffer.buffered.length > 0) {
            const start = sourceBuffer.buffered.start(0);
            const end = sourceBuffer.buffered.end(sourceBuffer.buffered.length - 1);
            if (end > start + 0.5) {
              fmp4SeekToLive = true;
              sourceBuffer.remove(start, Math.max(start + 0.25, end - 0.5));
              return;
            }
          }
        } catch (_) {}
      }
      handleFmp4DecodeError();
    }
  }

  function resampleLinear(input, fromRate, toRate) {
    if (fromRate === toRate) return input;
    const ratio = fromRate / toRate;
    const outLen = Math.max(1, Math.round(input.length / ratio));
    const out = new Float32Array(outLen);
    const last = input.length - 1;
    for (let i = 0; i < outLen; i++) {
      const pos = i * ratio;
      const idx = Math.floor(pos);
      if (idx >= last) {
        out[i] = input[last];
      } else {
        const frac = pos - idx;
        out[i] = input[idx] + (input[idx + 1] - input[idx]) * frac;
      }
    }
    return out;
  }

  async function startMic() {
    if (micRunning || micStarting || !authed) return;
    const seq = ++micReqSeq;
    micStarting = true;
    // Create and kick AudioContexts synchronously inside the user gesture BEFORE awaiting getUserMedia (iOS Safari).
    const Ctor = window.AudioContext || window.webkitAudioContext;
    let localCtx = null;
    if (Ctor) {
      try { localCtx = new Ctor(); } catch (_) { localCtx = null; }
    }
    if (localCtx && localCtx.state !== "running") {
      try { void localCtx.resume().catch(() => {}); } catch (_) {}
    }
    void resumeOutAudio();

    if (!navigator.mediaDevices || !navigator.mediaDevices.getUserMedia) {
      if (localCtx) { try { localCtx.close(); } catch (_) {} }
      if (seq === micReqSeq) {
        micStarting = false;
        stopMic();
      }
      showBanner(L.micUnsupported);
      return;
    }
    let localStream = null;
    try {
      localStream = await navigator.mediaDevices.getUserMedia({
        audio: {
          echoCancellation: true,
          noiseSuppression: true,
          autoGainControl: true,
          channelCount: 1,
        },
      });
      if (seq !== micReqSeq || !micStarting || !authed || !ws || ws.readyState !== WebSocket.OPEN) {
        for (const t of localStream.getTracks()) {
          try { t.stop(); } catch (_) {}
        }
        if (localCtx) { try { localCtx.close(); } catch (_) {} }
        if (seq === micReqSeq) micStarting = false;
        return;
      }
      if (!localCtx || localCtx.state === "closed") {
        if (!Ctor) throw new Error("AudioContext unavailable");
        localCtx = new Ctor();
      }
      if (localCtx.state !== "running") {
        await Promise.race([
          localCtx.resume().catch(() => {}),
          new Promise((resolve) => setTimeout(resolve, 600)),
        ]);
      }
      if (seq !== micReqSeq || !micStarting || !authed || !ws || ws.readyState !== WebSocket.OPEN) {
        for (const t of localStream.getTracks()) {
          try { t.stop(); } catch (_) {}
        }
        if (localCtx) { try { localCtx.close(); } catch (_) {} }
        if (seq === micReqSeq) micStarting = false;
        return;
      }
      micStream = localStream;
      micCtx = localCtx;
      micCtx.onstatechange = () => {
        if (micRunning && micCtx && (micCtx.state === "suspended" || micCtx.state === "interrupted")) {
          micCtx.resume().catch(() => {});
        }
      };
      // iOS Safari may interrupt output AudioContext when getUserMedia activates the mic.
      void resumeOutAudio();
      micSrc = micCtx.createMediaStreamSource(micStream);
      micProc = micCtx.createScriptProcessor(2048, 1, 1);
      micZeroGain = micCtx.createGain();
      micZeroGain.gain.value = 0;
      micResidual = new Float32Array(0);
      micProc.onaudioprocess = (ev) => {
        if (!micRunning || !authed || !micCtx) return;
        const raw = ev.inputBuffer.getChannelData(0);
        let sumSq = 0;
        for (let i = 0; i < raw.length; i++) sumSq += raw[i] * raw[i];
        const rms = Math.sqrt(sumSq / Math.max(1, raw.length));
        rmsFill.style.width = Math.min(100, Math.round(rms * 380)) + "%";
        if (micMode === "ptt" && !pttHolding) return;
        const down = resampleLinear(raw, micCtx.sampleRate, MIC_RATE);
        const merged = new Float32Array(micResidual.length + down.length);
        merged.set(micResidual, 0);
        merged.set(down, micResidual.length);
        let offset = 0;
        while (offset + MIC_CHUNK_SAMPLES <= merged.length) {
          const slice = merged.subarray(offset, offset + MIC_CHUNK_SAMPLES);
          offset += MIC_CHUNK_SAMPLES;
          const pcm16 = new Int16Array(MIC_CHUNK_SAMPLES);
          for (let i = 0; i < MIC_CHUNK_SAMPLES; i++) {
            const c = Math.max(-1, Math.min(1, slice[i]));
            pcm16[i] = c < 0 ? Math.max(-32768, Math.round(c * 32768)) : Math.min(32767, Math.round(c * 32767));
          }
          if (ws && ws.readyState === WebSocket.OPEN && ws.bufferedAmount < 128 * 1024) {
            try {
              ws.send(packFrame(FRAME_MIC_PCM, currentEpoch, Date.now(), new Uint8Array(pcm16.buffer)));
            } catch (_) {}
          }
        }
        micResidual = merged.slice(offset);
      };
      micSrc.connect(micProc);
      micProc.connect(micZeroGain);
      micZeroGain.connect(micCtx.destination);
      for (const t of micStream.getTracks()) {
        t.onended = () => {
          if (seq === micReqSeq && micRunning) stopMic();
        };
      }
      micStarting = false;
      micRunning = true;
      micToggleBtn.textContent = L.micStop;
      micToggleBtn.className = "btn active-ok";
      micHint.textContent = micMode === "ptt" ? L.micHintPtt : L.micHintVad;
      pttBtn.style.display = micMode === "ptt" ? "block" : "none";
      showBanner("");
      syncCompanionState();
    } catch (err) {
      if (localStream && localStream !== micStream) {
        for (const t of localStream.getTracks()) {
          try { t.stop(); } catch (_) {}
        }
      }
      if (localCtx && localCtx !== micCtx) {
        try { localCtx.close(); } catch (_) {}
      }
      if (seq !== micReqSeq) return;
      micStarting = false;
      stopMic();
      showBanner(L.micErrPrefix + (err && err.message ? err.message : String(err)));
    }
  }

  function stopMic() {
    micReqSeq += 1;
    micStarting = false;
    const wasHolding = pttHolding;
    micRunning = false;
    pttHolding = false;
    pttBtn.classList.remove("holding");
    pttBtn.textContent = L.pttIdle;
    if (wasHolding && authed) {
      sendJson({ type: "ptt_end" });
    }
    rmsFill.style.width = "0%";
    micResidual = new Float32Array(0);
    if (micProc) {
      try { micProc.onaudioprocess = null; } catch (_) {}
      try { micProc.disconnect(); } catch (_) {}
      micProc = null;
    }
    if (micSrc) {
      try { micSrc.disconnect(); } catch (_) {}
      micSrc = null;
    }
    if (micZeroGain) {
      try { micZeroGain.disconnect(); } catch (_) {}
      micZeroGain = null;
    }
    if (micCtx) {
      try { micCtx.close(); } catch (_) {}
      micCtx = null;
    }
    if (micStream) {
      for (const t of micStream.getTracks()) {
        try { t.onended = null; } catch (_) {}
        try { t.stop(); } catch (_) {}
      }
      micStream = null;
    }
    micToggleBtn.textContent = L.micStart;
    micToggleBtn.className = "btn primary";
    micHint.textContent = L.micOffHint;
    pttBtn.style.display = "none";
    syncCompanionState();
  }

  function updateCamButtonsActive() {
    for (const btn of camModeSeg.querySelectorAll("button")) {
      btn.classList.toggle("active", btn.dataset.cam === cameraMode);
    }
  }

  async function setCameraMode(nextMode) {
    const prevMode = cameraMode;
    cameraMode = nextMode;
    updateCamButtonsActive();
    if (nextMode === "off") {
      stopCamera();
      syncCompanionState();
      return;
    }
    if (prevMode === "off") {
      cameraFacing = nextMode === "motion" ? "user" : "environment";
    }
    await startCameraStream();
    syncCompanionState();
  }

  async function startCameraStream() {
    const seq = ++camReqSeq;
    if (!navigator.mediaDevices || !navigator.mediaDevices.getUserMedia) {
      showBanner(L.camUnsupported);
      cameraMode = "off";
      updateCamButtonsActive();
      stopCamera();
      syncCompanionState();
      return;
    }
    stopCameraTracksOnly(false);
    const isMotion = cameraMode === "motion" || cameraMode === "vision_motion";
    const idealW = isMotion ? 480 : 640;
    const idealH = isMotion ? 640 : 480;
    try {
      const stream = await navigator.mediaDevices.getUserMedia({
        video: {
          facingMode: { ideal: cameraFacing },
          width: { ideal: idealW },
          height: { ideal: idealH },
        },
        audio: false,
      });
      if (seq !== camReqSeq || !authed || cameraMode === "off" || !ws || ws.readyState !== WebSocket.OPEN) {
        for (const t of stream.getTracks()) {
          try { t.stop(); } catch (_) {}
        }
        return;
      }
      camStream = stream;
      for (const t of camStream.getTracks()) {
        t.onended = () => {
          if (seq !== camReqSeq || cameraMode === "off") return;
          cameraMode = "off";
          updateCamButtonsActive();
          stopCamera();
          syncCompanionState();
          showBanner(L.camInterrupted, true);
        };
      }
      camPreview.srcObject = camStream;
      await camPreview.play().catch(() => {});
      if (seq !== camReqSeq || !authed || cameraMode === "off" || !ws || ws.readyState !== WebSocket.OPEN) {
        for (const t of stream.getTracks()) {
          try { t.onended = null; } catch (_) {}
          try { t.stop(); } catch (_) {}
        }
        if (camStream === stream) {
          camStream = null;
          camPreview.srcObject = null;
        }
        return;
      }
      pipWrap.classList.add("show");
      flipCamBtn.disabled = !authed;
      lookNowBtn.disabled = !authed || (cameraMode !== "vision" && cameraMode !== "vision_motion");
      const modeLabel =
        cameraMode === "vision"
          ? L.camVisionActive
          : cameraMode === "motion"
            ? L.camMotionActive
            : L.camDualActive;
      pipBadge.textContent = modeLabel;
      camHint.textContent = modeLabel;
      scheduleCameraPump(seq);
      showBanner("");
    } catch (err) {
      if (seq !== camReqSeq) return;
      cameraMode = "off";
      updateCamButtonsActive();
      stopCamera();
      syncCompanionState();
      showBanner(L.camErrPrefix + (err && err.message ? err.message : String(err)));
    }
  }

  function stopCameraTracksOnly(bumpSeq = true) {
    if (bumpSeq) camReqSeq += 1;
    camSending = false;
    camSendingToken += 1;
    if (camTimer) {
      clearInterval(camTimer);
      camTimer = null;
    }
    if (camStream) {
      for (const t of camStream.getTracks()) {
        try { t.onended = null; } catch (_) {}
        try { t.stop(); } catch (_) {}
      }
      camStream = null;
    }
    camPreview.srcObject = null;
  }

  function stopCamera() {
    stopCameraTracksOnly(true);
    pipWrap.classList.remove("show");
    camHint.textContent = L.camOffHint;
    flipCamBtn.disabled = true;
    lookNowBtn.disabled = true;
  }

  function scheduleCameraPump(seq) {
    if (camTimer) clearInterval(camTimer);
    if (!camCanvas) {
      camCanvas = document.createElement("canvas");
      camCtx2d = camCanvas.getContext("2d");
    }
    const isMotion = cameraMode === "motion" || cameraMode === "vision_motion";
    const intervalMs = isMotion ? 100 : 750;
    const maxEdge = isMotion ? 360 : 512;
    const quality = isMotion ? 0.62 : 0.72;
    camFramesSent = 0;
    camFpsWindowStart = performance.now();

    camTimer = setInterval(() => {
      if (seq !== camReqSeq || cameraMode === "off") return;
      if (!authed || !ws || ws.readyState !== WebSocket.OPEN || !camStream || camSending) return;
      if (ws.bufferedAmount > 256 * 1024) return;
      const vw = camPreview.videoWidth || 0;
      const vh = camPreview.videoHeight || 0;
      if (vw < 16 || vh < 16) return;
      const scale = Math.min(1, maxEdge / Math.max(vw, vh));
      const tw = Math.max(16, Math.round(vw * scale));
      const th = Math.max(16, Math.round(vh * scale));
      if (camCanvas.width !== tw || camCanvas.height !== th) {
        camCanvas.width = tw;
        camCanvas.height = th;
      }
      camCtx2d.drawImage(camPreview, 0, 0, tw, th);
      camSending = true;
      const sendTok = ++camSendingToken;
      camCanvas.toBlob(
        async (blob) => {
          try {
            if (
              seq === camReqSeq &&
              cameraMode !== "off" &&
              camStream &&
              blob &&
              ws &&
              ws.readyState === WebSocket.OPEN &&
              authed
            ) {
              const ab = await blob.arrayBuffer();
              if (
                seq === camReqSeq &&
                cameraMode !== "off" &&
                camStream &&
                ws &&
                ws.readyState === WebSocket.OPEN &&
                authed &&
                ab.byteLength <= 512 * 1024
              ) {
                ws.send(packFrame(FRAME_CAMERA_JPEG, currentEpoch, Date.now(), new Uint8Array(ab)));
                camFramesSent += 1;
                const elapsed = (performance.now() - camFpsWindowStart) / 1000;
                if (elapsed >= 1.5) {
                  const fps = (camFramesSent / elapsed).toFixed(1);
                  camFramesSent = 0;
                  camFpsWindowStart = performance.now();
                  const modeLabel =
                    cameraMode === "vision"
                      ? L.camVision
                      : cameraMode === "motion"
                        ? L.camMotion
                        : L.camDualShort;
                  camHint.textContent = modeLabel + " · " + fps + " FPS";
                }
              }
            }
          } catch (_) {
          } finally {
            if (sendTok === camSendingToken) camSending = false;
          }
        },
        "image/jpeg",
        quality
      );
    }, intervalMs);
  }

  function handleStateLabel(st) {
    directorTag.textContent = L.states[st] || st || L.states.IDLE;
  }

  function resolveLocalizedCodeError(code, fallback) {
    const map = {
      pair_expired: L.pairExpired,
      rate_limited: L.errRateLimited,
      companion_disabled: L.errDisabled,
      bad_request: L.errBadRequest,
      payload_too_large: L.errBadRequest,
      timeout: L.errTimeout,
      redeem_failed: L.errRedeemFailed,
      unauthorized: L.errUnauthorized,
      forbidden: L.errForbidden,
      text_too_long: L.errTextTooLong,
      turn_error: L.errTurnFailed,
      asr_error: L.errAsrFailed,
      vision_unsupported_fallback: L.noticeVisionFallback,
    };
    if (code && map[code]) return map[code];
    return fallback || L.pairExpired;
  }

  async function bootstrap() {
    const hash = (window.location.hash || "").replace(/^#/, "");
    const params = new URLSearchParams(hash);
    const code = (params.get("code") || "").trim();
    if (window.history && window.history.replaceState) {
      window.history.replaceState(null, "", window.location.pathname + window.location.search);
    }
    if (!code) {
      setStatus("err", L.missingCode);
      showBanner(L.missingCodeDesc);
      return;
    }

    setStatus("warn", L.verifying);
    let redeemData = null;
    try {
      const resp = await fetch(REDEEM_PATH, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ code, device_label: deviceLabel }),
      });
      redeemData = await resp.json().catch(() => ({}));
      if (!resp.ok || !redeemData.token) {
        const defaultCode = resp.status === 401 ? "pair_expired" : resp.status === 429 ? "rate_limited" : "";
        const msg = resolveLocalizedCodeError(
          redeemData.code || defaultCode,
          activeLocale === "zh-CN" ? (redeemData.error || redeemData.detail || L.pairExpired) : L.pairExpired
        );
        setStatus("err", L.pairFailed);
        showBanner(msg);
        return;
      }
    } catch (err) {
      setStatus("err", L.netErr);
      showBanner(L.netErrPrefix + (err && err.message ? err.message : String(err)));
      return;
    }

    setStatus("warn", L.connecting);
    const wsScheme = window.location.protocol === "https:" ? "wss:" : "ws:";
    const wsUrl = wsScheme + "//" + window.location.host + (redeemData.ws_path || WS_PATH);
    ws = new WebSocket(wsUrl);
    ws.binaryType = "arraybuffer";

    ws.onopen = () => {
      const canFmp4 = Boolean(MseCtor && pickSupportedFmp4Mime());
      fallbackToJpeg = !canFmp4;
      fmp4ErrorCount = 0;
      sendJson({
        type: "auth",
        token: redeemData.token,
        client_role: "companion",
        device_label: deviceLabel,
        supports_fmp4: canFmp4,
        video_format: canFmp4 ? "fmp4" : "jpeg",
      });
    };

    ws.onmessage = (ev) => {
      if (typeof ev.data === "string") {
        let msg = null;
        try { msg = JSON.parse(ev.data); } catch (_) { return; }
        if (!msg || typeof msg !== "object") return;
        const t = msg.type;
        if (t === "auth_ok") {
          authed = true;
          kickedReason = "";
          currentEpoch = Number(msg.epoch || 0);
          if (typeof msg.video_codec_mime === "string" && msg.video_codec_mime) {
            fmp4MimeHint = msg.video_codec_mime;
          }
          if (!fallbackToJpeg && !pickSupportedFmp4Mime()) {
            requestJpegFallback();
          }
          micMode = msg.mic_mode === "ptt" ? "ptt" : "vad";
          directorMode = msg.director_mode === "director" ? "director" : "dialogue";
          for (const b of micModeSeg.querySelectorAll("button")) {
            b.classList.toggle("active", b.dataset.mode === micMode);
          }
          for (const b of dirModeSeg.querySelectorAll("button")) {
            b.classList.toggle("active", b.dataset.dir === directorMode);
          }
          handleStateLabel(msg.state);
          setStatus("ok", L.connected);
          setControlsEnabled(true);
          ensureOutAudio();
          syncCompanionState();
        } else if (t === "state") {
          if (typeof msg.epoch === "number" && msg.epoch >= currentEpoch) currentEpoch = msg.epoch;
          handleStateLabel(msg.state);
          if (msg.mic_mode) {
            const prevMicMode = micMode;
            micMode = msg.mic_mode === "ptt" ? "ptt" : "vad";
            if (prevMicMode === "ptt" && micMode !== "ptt" && pttHolding) {
              pttHolding = false;
              micResidual = new Float32Array(0);
              pttBtn.classList.remove("holding");
              pttBtn.textContent = L.pttIdle;
            }
            for (const b of micModeSeg.querySelectorAll("button")) {
              b.classList.toggle("active", b.dataset.mode === micMode);
            }
            if (micRunning) {
              pttBtn.style.display = micMode === "ptt" ? "block" : "none";
              micHint.textContent = micMode === "ptt" ? L.micHintPtt : L.micHintVad;
            }
          }
          if (msg.director_mode) {
            directorMode = msg.director_mode === "director" ? "director" : "dialogue";
            for (const b of dirModeSeg.querySelectorAll("button")) {
              b.classList.toggle("active", b.dataset.dir === directorMode);
            }
          }
        } else if (t === "barge_in") {
          if (typeof msg.epoch === "number") {
            if (msg.epoch < currentEpoch) return;
            currentEpoch = msg.epoch;
          }
          interruptOutAudio();
          handleStateLabel(msg.state || "IDLE");
        } else if (t === "asr_final") {
          if (msg.text) addSubtitle("user", msg.text, false, msg.epoch || currentEpoch);
        } else if (t === "transcript_delta" || t === "transcript" || t === "llm_delta") {
          const role = msg.role === "user" || msg.role === "system" ? msg.role : "assistant";
          const isDelta = t === "transcript_delta" || t === "llm_delta";
          if (msg.text) addSubtitle(role, msg.text, isDelta, msg.epoch || currentEpoch);
        } else if (t === "turn_end") {
          handleStateLabel("IDLE");
          if (subtitles.length) subtitles[subtitles.length - 1].final = true;
        } else if (t === "system_notice") {
          const noticeText = resolveLocalizedCodeError(msg.code, msg.text || "");
          if (noticeText) addSubtitle("system", noticeText, false, currentEpoch);
        } else if (t === "companion_kicked") {
          kickedReason = msg.reason === "replaced_by_new_device"
            ? "replaced_by_new_device"
            : "revoked_by_desktop";
          const reason = kickedReason === "replaced_by_new_device"
            ? L.kickedReplaced
            : L.kickedRevoked;
          showBanner(reason, true);
        } else if (t === "error") {
          const errText = resolveLocalizedCodeError(
            msg.code,
            activeLocale === "zh-CN" ? (msg.detail || L.errTurnFailed) : (msg.detail || L.errTurnFailed)
          );
          if (errText) showBanner(errText);
        }
        return;
      }
      if (ev.data instanceof ArrayBuffer) {
        const frame = unpackFrame(ev.data);
        if (!frame) return;
        if (frame.type === FRAME_AUDIO_PCM) {
          if (frame.epoch >= currentEpoch) {
            currentEpoch = frame.epoch;
            enqueuePcm24k(frame.payload);
          }
        } else if (frame.type === FRAME_VIDEO) {
          renderJpegFrame(frame.payload);
        } else if (frame.type === FRAME_FMP4) {
          pushFmp4(frame.payload);
        }
      }
    };

    ws.onclose = (ev) => {
      authed = false;
      interruptOutAudio();
      teardownFmp4();
      pendingJpegPayload = null;
      stopMic();
      cameraMode = "off";
      updateCamButtonsActive();
      stopCamera();
      setControlsEnabled(false);
      if (ev.code === 4409 || kickedReason === "replaced_by_new_device") {
        setStatus("warn", L.statusReplaced);
        showBanner(L.descReplaced, true);
      } else if (ev.code === 4403 || kickedReason === "revoked_by_desktop") {
        setStatus("warn", L.statusRevoked);
        showBanner(L.descRevoked, true);
      } else if (ev.code === 4401) {
        setStatus("err", L.statusAuthExpired);
        showBanner(L.descAuthExpired);
      } else {
        setStatus("err", L.statusDisconnected);
        showBanner(L.descDisconnected);
      }
    };
  }

  muteBtn.addEventListener("click", async () => {
    await resumeOutAudio();
    audioMuted = !audioMuted;
    if (audioOutGain) audioOutGain.gain.value = audioMuted ? 0 : 1;
    muteBtn.textContent = audioMuted ? L.audioOff : L.audioOn;
    muteBtn.classList.toggle("muted", audioMuted);
  });

  unlockAudioBtn.addEventListener("click", () => { void resumeOutAudio(); });
  function tryUnlockOutAudioOnGesture() {
    if (audioOutCtx && audioOutCtx.state !== "running" && audioOutCtx.state !== "closed") {
      void resumeOutAudio();
    }
    if (micRunning && micCtx && (micCtx.state === "suspended" || micCtx.state === "interrupted")) {
      micCtx.resume().catch(() => {});
    }
  }
  document.addEventListener("touchstart", tryUnlockOutAudioOnGesture, { passive: true });
  document.addEventListener("touchend", tryUnlockOutAudioOnGesture, { passive: true });
  document.addEventListener("click", tryUnlockOutAudioOnGesture);
  document.addEventListener("visibilitychange", () => {
    if (document.hidden) {
      setPtt(false);
    } else {
      tryUnlockOutAudioOnGesture();
    }
  });
  window.addEventListener("blur", () => setPtt(false));

  micToggleBtn.addEventListener("click", () => {
    if (micRunning || micStarting) stopMic();
    else void startMic();
  });

  bargeInBtn.addEventListener("click", () => {
    if (!authed) return;
    currentEpoch += 1;
    interruptOutAudio();
    sendJson({ type: "barge_in", reason: "companion_button", epoch: currentEpoch });
  });

  micModeSeg.addEventListener("click", (ev) => {
    const btn = ev.target.closest("button[data-mode]");
    if (!btn || !authed) return;
    const nextMode = btn.dataset.mode === "ptt" ? "ptt" : "vad";
    if (micMode === "ptt" && nextMode !== "ptt" && pttHolding) {
      setPtt(false);
    }
    micMode = nextMode;
    for (const b of micModeSeg.querySelectorAll("button")) {
      b.classList.toggle("active", b.dataset.mode === micMode);
    }
    sendJson({ type: "set_mode", director_mode: directorMode, mic_mode: micMode });
    if (micRunning) {
      pttBtn.style.display = micMode === "ptt" ? "block" : "none";
      micHint.textContent = micMode === "ptt" ? L.micHintPtt : L.micHintVad;
    }
  });

  dirModeSeg.addEventListener("click", (ev) => {
    const btn = ev.target.closest("button[data-dir]");
    if (!btn || !authed) return;
    directorMode = btn.dataset.dir === "director" ? "director" : "dialogue";
    for (const b of dirModeSeg.querySelectorAll("button")) {
      b.classList.toggle("active", b.dataset.dir === directorMode);
    }
    sendJson({ type: "set_mode", director_mode: directorMode, mic_mode: micMode });
  });

  function setPtt(active) {
    if (active && (!authed || !micRunning || micMode !== "ptt")) return;
    if (pttHolding === active) return;
    pttHolding = active;
    micResidual = new Float32Array(0);
    pttBtn.classList.toggle("holding", active);
    pttBtn.textContent = active ? L.pttHolding : L.pttIdle;
    if (authed) {
      sendJson({ type: active ? "ptt_start" : "ptt_end" });
    }
  }
  pttBtn.addEventListener("pointerdown", (e) => {
    e.preventDefault();
    try {
      if (e.currentTarget && e.currentTarget.setPointerCapture) {
        e.currentTarget.setPointerCapture(e.pointerId);
      }
    } catch (_) {}
    setPtt(true);
  });
  pttBtn.addEventListener("pointerup", (e) => { e.preventDefault(); setPtt(false); });
  pttBtn.addEventListener("pointercancel", () => setPtt(false));
  pttBtn.addEventListener("lostpointercapture", () => setPtt(false));

  camModeSeg.addEventListener("click", (ev) => {
    const btn = ev.target.closest("button[data-cam]");
    if (!btn || !authed) return;
    void setCameraMode(btn.dataset.cam || "off");
  });

  flipCamBtn.addEventListener("click", () => {
    if (!authed || cameraMode === "off") return;
    cameraFacing = cameraFacing === "user" ? "environment" : "user";
    void startCameraStream();
  });

  lookNowBtn.addEventListener("click", () => {
    if (!authed) return;
    sendJson({ type: "text_input", text: L.lookPrompt, mode: directorMode });
    addSubtitle("user", L.lookEcho, false, currentEpoch);
  });

  function submitText() {
    const txt = (textInput.value || "").trim();
    if (!txt || !authed) return;
    sendJson({ type: "text_input", text: txt, mode: directorMode });
    addSubtitle("user", txt, false, currentEpoch);
    textInput.value = "";
  }
  sendBtn.addEventListener("click", submitText);
  textInput.addEventListener("keydown", (e) => {
    if (e.key === "Enter" && !e.isComposing && e.keyCode !== 229) {
      e.preventDefault();
      submitText();
    }
  });

  setInterval(() => {
    if (authed) sendJson({ type: "ping", ts: Date.now() / 1000 });
  }, 15000);

  void bootstrap();
})();
</script>
</body>
</html>
"""
