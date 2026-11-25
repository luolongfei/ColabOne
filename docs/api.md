# Remote API v1

`/health` is the only unauthenticated route on the control service (`127.0.0.1:7860`). All `/api/v1/*` requests require `Authorization: Bearer <per-runtime token>` on the runtime loopback interface, reached through authenticated Colab kernel RPC. Credentials are not returned to the frontend.

| Method | Route | Behavior |
|---|---|---|
| GET | `/api/v1/snapshot` | Runtime (including `protocol: 7`, `capabilities`, `resources`, `idle`, `public_api`, `comfyui`), `models`, `workflows`, `comfy_outputs`, and `tts_engines` in one control request |
| GET | `/api/v1/status` | Runtime identity, `protocol: 7`, `capabilities: ["public_api", "comfyui", "comfyui_hf_token", "comfyui_drive_export", "comfyui_tts_preview"]`, `active_engine`, live `resources` telemetry (`gpu`, `memory`, `disk`, `cpu`, `history`), `idle` countdown, `loaded_model`, `public_api`, `comfyui`, and task state; does not reset idle |
| POST | `/api/v1/configure` | Unified configurable idle auto-release timeout (default 30 min; presets `15 min`, `30 min`, `45 min`, `1 hr`, `2 hr`); single-launch `max_cu` budget deadline removed |
| POST | `/api/v1/activity` | Explicit trusted user interaction that resets the idle auto-release countdown; never called by polling |
| POST | `/api/v1/drive/connect` | Attach and restore `ColabOne_Drive/workspace` via mounted official Colab DriveFS (`colab_drivefs`) or headless quota-project `rclone` |
| POST | `/api/v1/drive/sync` | Synchronize dirty `models.json`, `/workflows/**`, and `/comfyui-outputs/**` to `ColabOne_Drive/workspace` |
| GET / POST | `/api/v1/models` | List current remote GGUF installation state / add a validated `ModelSpec` |
| POST | `/api/v1/models/{id}/download` | Start remote verified GGUF download; 16 or 32 threads (default 16), optional transient Hugging Face token, and optional `load=true` with `context_length` / `temperature` |
| POST | `/api/v1/models/{id}/load` | Load downloaded GGUF files into `llama-server` (`127.0.0.1:8081`); accepts optional `context_length` and `temperature`; stops ComfyUI first to enforce strict VRAM mutual exclusion |
| POST | `/api/v1/unload` | Stop `llama-server`, preserving cached weights |
| DELETE | `/api/v1/models/{id}` | Delete only this model's remote weight directory |
| POST | `/api/v1/public-api/start` | Start the separate LLM OpenAI gateway (`127.0.0.1:7861`) and its Cloudflared tunnel |
| POST | `/api/v1/public-api/stop` | Revoke public API access, stop its tunnel, and cancel active public jobs |
| GET | `/api/v1/tasks/{id}/events?after=N` | SSE with monotonic IDs and incremental task/log/progress events |
| GET | `/api/v1/tasks/{id}/poll?after=N` | Incremental event batch and task state; reconnect from cursor |
| DELETE | `/api/v1/tasks/{id}` | Cooperatively cancel the task (download, load, or queued ComfyUI workflow run) |
| POST | `/api/v1/prepare-release` | Stop Public API and ComfyUI engine/canvas tunnel, cancel and await running tasks, and flush `ColabOne_Drive` before unassigning |

`ModelSpec` (`common/models.py`) includes a safe ID, display metadata, `kind: "chat"`, context length, temperature, resource hint, and a list of Hugging Face GGUF files (`role` is `"model"` or `"shard"`, strictly `.gguf`). Each file specifies `owner/repo`, a full 40-char commit SHA, a relative GGUF path, file size, and a 64-char SHA-256 digest. Unknown fields (including executable code) are rejected. The first shard (`role: "model"`) is the sole model entrypoint.

Task states are `running`, `completed`, `cancelled`, and `failed`. Resource conflicts return `409`; missing IDs return `404`; invalid manifests return `422`; invalid tokens return `401`; bad parameters return `400`. Runtime `stopped`, `starting`, `disconnected`, and `releasing` are desktop lifecycle states; a network failure never implies release.

Go services exposed to the frontend are generated in `frontend/src/colabOneBridge.ts` (`ColabOneDesktop` in `console/main.go` and `console/comfyui.go`): `Snapshot`, `Command`, `SetAppearance`, `ReadClipboard` (user-triggered paste), `WriteClipboard`, `OpenURL` (allowlisted HTTPS only), `ExportWorkflowOutput(id)` (native save dialog accepting only a 32-hex artifact ID, bounded 512 KiB chunks, max 512 MiB, allowlisted extensions, full SHA-256 verification, and atomic rename), and `OpenComfyCanvas(target)` (`"window"` or `"browser"`, fetching the authenticated HTTPS `/_colabone_auth?token=...` URL exclusively from the trusted in-process Go controller). The controller executes directly inside the pure Go desktop binary over typed MyGo IPC and exposes no local HTTP server. No command accepts arbitrary shell code.

Runtime `/api/v1/status` (and `/api/v1/snapshot` `runtime`) returns:
- `protocol: 7` and `capabilities: ["public_api", "comfyui", "comfyui_hf_token", "comfyui_drive_export", "comfyui_tts_preview"]`, plus `active_engine` (`""`, `"llama"`, or `"comfyui"`).
- `resources` (collected by `ColabOneResourceSampler` in `server/telemetry.py` and rendered on the desktop by `ResourceMonitor.vue` as ColabOne real-time hardware charts in **2×2 Grid** or **4-Row Wide** layouts): `gpu` (utilization and VRAM usage/capacity), `memory` (system RAM parsed from `/proc/meminfo`), `disk` (filesystem capacity/usage plus live I/O throughput rates from `/proc/diskstats`), `cpu` (utilization computed from `/proc/stat`), and a 60-sample rolling `history` time series.
- `idle`: unified configurable idle auto-release countdown state (`phase`, `remaining_seconds`, `countdown_seconds`, `idle_seconds`, `server_time`) defaulting to **30 min** (selectable options: `15 min`, `30 min`, `45 min`, `1 hr`, `2 hr` from either the **Devices** dropdown or **Settings**). Single-launch CU budgets (`max_cu`) and mandatory per-runtime budget deadlines are no longer used; active downloads, VRAM loads, Public API inference, and ComfyUI queue or Native Canvas requests pause the countdown, while trusted user interaction resets it via `runtime.activity` (`POST /api/v1/activity`).

Desktop snapshots also report live account billing state—active vs. idle billing status, hourly CU burn rate (`CU/hr`), remaining CU balance, and human-friendly estimated remaining runtime (days & hours, e.g., `~2d 4h`)—surfaced in both the persistent top bar and the **Devices** overview card. Task `download` telemetry includes `downloaded_bytes`, `total_bytes`, `speed_bps`, `eta_seconds`, `elapsed_seconds`, `resumed_bytes`, `threads`, `connections`, `phase`, `filename`, and file counts. Bounded task `logs` carry monotonic sequence IDs; the desktop deduplicates them independently of progress updates.

Core desktop commands (`ColabOneDesktop.Command`) include:
- Runtime & account: `account.refresh`, `runtime.start`, `runtime.select`, `runtime.release`, `runtime.release.preview`, `runtime.release.all`, `runtime.upgrade` (in-place control-service update to protocol `>= 7` when idle with no running task, loaded model, or active engine), `runtime.activity`, `settings.save`, `drive.connect`, `drive.setup_headless`, `drive.cancel`, `drive.sync`, `task.cancel`, `logs.clear`.
- Model picker (**Select Model**): `hub.search`, `hub.files`, `hub.install`, `model.import`, `model.download`, `model.load`, `model.unload`, `model.delete`, `model.common`, `model.remove_local`, `common_repo.save`, `common_repo.remove`.

## Public inference API (LLM OpenAI gateway)

This is a **separate** ASGI application (`server/public_api.py`) on remote `127.0.0.1:7861`, fronted by Cloudflared. Management remains on private `127.0.0.1:7860`; none of `/api/v1/*`, login flows, file management, or ComfyUI routes exist on the public LLM gateway. `/health` reveals only a random gateway instance identifier for tunnel verification. Every inference route requires `Authorization: Bearer <API Key>`; there is no loopback bypass, query-string key, or anonymous mode. CORS allows Bearer-based clients (`GET`, `POST`) without cookies.

Use the desktop **Public API** page to configure and enable a remotely managed Named Tunnel (your fixed public HTTPS domain plus your Cloudflare install command or tunnel token; temporary `trycloudflare.com` Quick Tunnels are not supported). Configure the public hostname's HTTP service target as `http://127.0.0.1:7861`. Interactive Cloudflare Access login prevents programmatic API clients from connecting. The page reports **Live** only after the public `/health` identity matches, anonymous `/v1/models` is rejected, and the authenticated model list succeeds. Verification repeats every 20 seconds; transient failures report **Reconnecting**. The API key is distinct from the internal control token, stored in the OS credential store, and sent only to the remote GPU. Explicit copy or reveal returns it to the desktop UI; snapshots and logs omit both the API key and the tunnel token. Key rotation disables the remote gateway first, so a failed revocation cannot be mistaken for success.

| Method | Public route | Contract |
|---|---|---|
| GET | `/health` | Unauthenticated gateway identity (`service`, random `instance`) for tunnel verification |
| GET | `/v1/models` / `/v1/models/{id}` | OpenAI model list & detail, **currently loaded GGUF LLM only** (`kind: "chat"`, includes active `context_length`) |
| POST | `/v1/chat/completions` | OpenAI Chat Completions (`system`/`developer`/`user`/`assistant`/`tool` messages, string or text-part arrays, `tools`, `tool_choice`, `parallel_tool_calls`, `response_format`, `stream`, `stream_options`, `max_tokens`/`max_completion_tokens`, sampling params) with automatic 3-stage context compaction on overflow |
| POST | `/v1/responses` | OpenAI Responses API (`wire_api = "responses"` for Codex & OpenAI SDKs): `instructions`, string/item `input`, `function` & `custom`/`apply_patch` tools, reasoning summaries, SSE lifecycle events (`response.created` … `response.completed`/`incomplete`/`failed`), `compaction`/`compaction_summary` replay, inline `context_management` compaction, automatic 3-stage overflow compaction with incremental prefix summary caching (reporting pre-compaction `usage.input_tokens` so Codex auto-compact triggers cleanly), and bounded in-memory `previous_response_id` |
| GET / DELETE | `/v1/responses/{id}` | Retrieve or delete a stored response from the bounded in-memory cache (cleared when the API stops) |
| POST | `/v1/responses/compact` | Codex / OpenAI context compaction (`object: "response.compaction"`): CJK-aware chunked summarization within the active model's `context_length` (with adaptive shrink retry on overflow) returning a preserved user anchor plus a stateless portable `type: "compaction"` item (`encrypted_content`) and token `usage` |

Legacy built-in desktop chat/image/video endpoints (`/api/v1/turns`, `/api/v1/conversations`, `/v1/images/generations`, `/v1/jobs`) have been removed. Vision/audio inputs (`image_url`, `input_image`, `input_audio`) and model download/switch routes are rejected without echoing request bodies. Send `X-ColabOne-Auto-Compact: off` to disable gateway-side overflow compaction and return OpenAI-standard `context_length_exceeded` directly (`HTTP 400` for non-streaming or `response.failed` with `error.code = "context_length_exceeded"` for streaming Responses). HTTP `401` = invalid key, `400` = invalid parameters or `context_length_exceeded`, `409` = wrong/unloaded model or GPU busy (including when ComfyUI owns VRAM), `413` = body exceeds 16 MiB, `503` = disabled gateway, and `504` = timeout or client disconnect. Errors use an `error.message` envelope.

Both SSE streaming (`stream=true`) and non-streaming (`stream=false`) requests are supported over the fixed-domain Named Tunnel. Non-streaming requests enforce a wait deadline and cancel unfinished work on timeout or disconnect; streaming cancels on disconnect. Active inference and compaction requests pause the idle auto-release countdown (default 30 min) while running.

Example (replace model ID with the loaded GGUF model):

```python
import os
from openai import OpenAI

client = OpenAI(base_url="https://YOUR-HOST/v1", api_key=os.environ["COLABONE_API_KEY"])
stream = client.responses.create(
    model="YOUR-GGUF-MODEL",
    input="Hello",
    stream=True,
)
for event in stream:
    if event.type == "response.output_text.delta":
        print(event.delta, end="", flush=True)
```

Control-only routes: `POST /api/v1/public-api/start` accepts `mode` (`"named"`), `domain`, `tunnel_token`, and `api_key`; `POST /api/v1/public-api/stop` revokes access and cancels public jobs. Neither endpoint starts a GPU. Runtime snapshot `public_api` carries state, mode (`"named"`), URL, last public probe, request counts, and bounded redacted lifecycle logs. Desktop commands are `api.save`, `api.start`, `api.stop`, `api.key` (explicit reveal/copy only), and `api.rotate`.

## ComfyUI workflow studio and native canvas (protocol 7)

Protocol `7` adds remote-only **ComfyUI** (`v0.46.0`, commit `a277b5bf213e8a44fa34817b7770b54f86ef4cea`, GPL-3.0) on loopback `127.0.0.1:8188` plus an authenticated Native Canvas HTTP + WebSocket reverse proxy on `127.0.0.1:8189`. `llama-server` (`8081`) and `ComfyUI` (`8188`) are strictly mutually exclusive on GPU VRAM; starting ComfyUI unloads any loaded LLM while retaining cached weights. Active ComfyUI queue runs, pack downloads, and Native Canvas proxy traffic pause the idle auto-release countdown (default 30 min).

All `/api/v1/comfyui/*` routes live on the private control service (`127.0.0.1:7860`) and require the per-runtime control Bearer token. Live ComfyUI engine and canvas status is included in `/api/v1/status` and `/api/v1/snapshot`, and output records (`comfy_outputs`) are returned in `/api/v1/snapshot`:

| Method | Route | Behavior |
|---|---|---|
| POST | `/api/v1/comfyui/start` | Install (on first run) and launch remote ComfyUI on `127.0.0.1:8188`, unloading `llama-server` first |
| POST | `/api/v1/comfyui/stop` | Stop ComfyUI (`8188`) and its authenticated canvas proxy/tunnel (`8189`), preserving cached models and synced Drive data |
| POST | `/api/v1/comfyui/free` | Ask running ComfyUI to free GPU VRAM and unload cached models without stopping the ComfyUI process |
| POST | `/api/v1/comfyui/canvas/start` | Start the authenticated HTTP + WebSocket reverse proxy (`127.0.0.1:8189` to `127.0.0.1:8188`) and fixed-domain Named Cloudflared tunnel (`ComfyCanvasConfig`: `mode: "named"`, `domain` + `tunnel_token`) with a random session token exchanged at `/_colabone_auth?token=<token>` for an `HttpOnly` session cookie |
| POST | `/api/v1/comfyui/canvas/stop` | Stop the canvas tunnel and authenticated proxy (`8189`), invalidating active canvas sessions |
| GET | `/api/v1/comfyui/canvas/url` | Return the authenticated HTTPS `/_colabone_auth?token=<token>` launch URL on demand (omitted from snapshots and logs) |
| GET | `/api/v1/comfyui/tts-engines` | List the 8 declared remote flagship TTS engines (`catalog/comfyui.json`), capabilities, VRAM/license metadata, 10 built-in Chinese voices, and 9 official Qwen3-TTS CustomVoice speakers |
| POST | `/api/v1/comfyui/tts-preview` | Return a cached remote GPU short-prose voice audition WAV (`cached: true`; omits inline `data` when `size > 512 KiB` for chunked retrieval) or queue an async cancellable `comfyui_tts_preview` task (`cached: false`, HTTP `200`) via isolated remote TTS worker subprocesses (`server/tts_engines.py` and `server/tts_worker_*.py`) with VRAM/disk preflight before unloading active engines |
| GET | `/api/v1/comfyui/tts-previews/{preview_id}` | Fetch a cached remote WAV audition record (`cached: true`, `preview_id`, `engine_id`, `voice_preset`, `text`, `duration_seconds`, `sample_rate`, `size`, `sha256`, `mime`, base64 `data`; chunked reads also return `offset`, `chunk_size`, and `has_more`) by 32-hex `preview_id`, with optional bounded chunk parameters `?offset=...&length=...` (`length <= 512 KiB`). Status codes: `200`, `400` (malformed `preview_id` or `offset >= size`), `401` (missing/invalid Bearer token), `404` (missing/corrupted cached preview), `409` (quiescing/releasing), `422` (invalid query bounds) |
| GET | `/api/v1/comfyui/workflows` | List built-in pack templates (`catalog/comfyui.json`) and custom imported workflows stored in `/content/colabone-data/workflows/**` |
| POST | `/api/v1/comfyui/workflows` | Validate and import a ComfyUI API-format JSON workflow (`WorkflowImportRequest`: max 2 MiB, max 500 nodes; rejects canvas-format graphs with top-level `nodes`), auto-extract up to 32 typed form fields (`WorkflowField`), and sync to `ColabOne_Drive/workspace/workflows/**` |
| DELETE | `/api/v1/comfyui/workflows/{workflow_id}` | Delete a custom workflow and move its remote Drive file to `workflows/.trash/` |
| POST | `/api/v1/comfyui/inputs` | Upload validated input media (`filename` + base64 `data`) for `image`/`audio` fields (extensions `png/jpg/jpeg/webp/wav/mp3/flac/ogg/m4a`; max 16 MiB image, max 64 MiB audio); returns a 32-hex asset ID |
| POST | `/api/v1/comfyui/run` | Submit `WorkflowRunRequest` (`workflow_id`, up to 128 `{node_id}.{input_name}` scalar `overrides`, up to 32 uploaded media `inputs`); launches a cancellable remote task executing over ComfyUI's loopback HTTP/WebSocket API |
| GET | `/api/v1/comfyui/outputs/{output_id}/asset` | Bounded base64 binary chunk read (`offset`, `length` <= 512 KiB) returning `data`, `size`, `offset`, `sha256`, `extension`, `mime`, and `filename` for SHA-256-verified preview and native export |
| POST | `/api/v1/comfyui/outputs/{output_id}/export-drive` | Copy a verified remote output directly to `My Drive / ColabOne_Drive/outputs` when Drive is connected |
| DELETE | `/api/v1/comfyui/outputs/{output_id}` | Delete an output artifact and move its Drive files to `comfyui-outputs/.trash/` |

### Remote TTS voice audition endpoints & Go controller commands

All TTS audition routes require the per-runtime control Bearer token on `127.0.0.1:7860` and execute exclusively on the remote Colab GPU.

#### `POST /api/v1/comfyui/tts-preview`

Resolves a deterministic 32-hex `preview_id` (from `engine_id`, `voice_preset`, `preview_text`, `voice_prompt`, `emotion_prompt`, `ref_text`, and SHA-256 digests of any uploaded reference audio inputs) across all 8 flagship TTS engines (`catalog/comfyui.json`), supporting the **10 curated built-in Chinese female & male voices**, the **9 official Qwen3-TTS CustomVoice native speakers**, custom Voice Design / emotion prompts, and uploaded voice/emotion reference audio.

Request JSON shape (`ComfyTtsPreviewRequest` / `ColabOneComfyTtsPreviewRequest`, unknown fields forbidden):

```json
{
  "engine_id": "voxcpm2",
  "voice_preset": "shen_zhiyi",
  "preview_text": "",
  "voice_prompt": "",
  "emotion_prompt": "",
  "ref_audio_input_id": "",
  "ref_text": "",
  "emotion_ref_input_id": "",
  "hf_token": ""
}
```

- `engine_id` (required): Safe engine ID (`^[a-zA-Z0-9_-]{1,80}$`) declared in `catalog/comfyui.json`.
- `voice_preset` (optional): Built-in Chinese voice preset ID or official Qwen3-TTS CustomVoice speaker preset ID (`^[a-zA-Z0-9_-]{1,64}$`); defaults to the engine's default preset when blank. Official Qwen3-TTS CustomVoice speakers are addressed via their `qwen_*` preset IDs (`qwen_vivian`, `qwen_serena`, `qwen_uncle_fu`, `qwen_dylan`, `qwen_eric`, `qwen_ryan`, `qwen_aiden`, `qwen_ono_anna`, `qwen_sohee`).
- `preview_text` (optional, max 600 chars): Custom audition text; when blank, automatically resolves to the tailored short prose passage paired with the selected voice/speaker.
- `voice_prompt` (optional, max 600 chars) & `emotion_prompt` (optional, max 240 chars): Optional Voice Design / emotion style direction overrides.
- `ref_audio_input_id` / `emotion_ref_input_id` (optional, 32-hex asset IDs from `POST /api/v1/comfyui/inputs`) & `ref_text` (optional, max 600 chars): Reference audio and transcript for voice cloning or disentangled emotion reference (e.g., `IndexTTS-2.5`).
- `hf_token` (optional, omitted when empty, redacted from logs and snapshots): Transient Hugging Face token for gated upstream weights, automatically injected from `vault.enc` by the Go desktop controller when saved.

Response JSON shape — **cache hit (`200 OK`, `cached: true`)**:

```json
{
  "cached": true,
  "preview_id": "a1b2c3d4e5f60718293a4b5c6d7e8f90",
  "engine_id": "voxcpm2",
  "voice_preset": "shen_zhiyi",
  "text": "……",
  "duration_seconds": 4.0,
  "sample_rate": 48000,
  "size": 384044,
  "sha256": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
  "mime": "audio/wav",
  "data": "<base64-encoded WAV>"
}
```

When a cached preview WAV is larger than `512 KiB` (`524,288` bytes), `POST /api/v1/comfyui/tts-preview` (and the `comfyui.tts.preview` desktop command) returns the metadata-only `cached: true` payload (omitting inline `data` while returning `size` and `sha256`). The desktop frontend then invokes `comfyui.tts.preview.asset`, which retrieves and reassembles the WAV in bounded `<= 512 KiB` chunks via `GET /api/v1/comfyui/tts-previews/{preview_id}?offset=...&length=...` and verifies size and SHA-256.

Response JSON shape — **cache miss (`200 OK`, async synthesis task queued)**:

```json
{
  "cached": false,
  "preview_id": "a1b2c3d4e5f60718293a4b5c6d7e8f90",
  "engine_id": "voxcpm2",
  "voice_preset": "shen_zhiyi",
  "text": "……",
  "sample_rate": 48000,
  "task": {
    "id": "f0e1d2c3b4a5968778695a4b3c2d1e0f",
    "kind": "comfyui_tts_preview",
    "model_id": "voxcpm2",
    "state": "running",
    "progress": 0.05,
    "message": "正在合成音色试听音频（voxcpm2）"
  }
}
```

On a cache miss, `POST /api/v1/comfyui/tts-preview` always returns HTTP `200` with the queued background `task`. The worker task runs VRAM and disk preflight first (before disturbing resident workloads), then unloads any loaded `llama-server` model and calls ComfyUI `/free` to reclaim GPU VRAM, executes an isolated remote TTS worker subprocess (`server/tts_engines.py` and `server/tts_worker_*.py`) while pausing the idle auto-release countdown, caches the mastered WAV under `preview_id`, and supports cooperative cancellation via `DELETE /api/v1/tasks/{id}`. Preflight failures or worker runtime errors surface asynchronously via `task.state == "failed"` and `task.error` (not HTTP `502`). Status codes: `200` on cache hit or queued background task submission, `400` / `422` on invalid engine/preset/asset IDs or validation failure, `401` on invalid Bearer token, `409` when another remote task owns the GPU or the instance is releasing.

#### `GET /api/v1/comfyui/tts-previews/{preview_id}`

Fetches a cached remote WAV audition asset by its 32-hex `preview_id` (used both after a `comfyui_tts_preview` task reaches `completed` and whenever an oversized `> 512 KiB` cache hit omits inline `data`). Touches runtime activity and supports optional bounded chunked reads via `?offset=<byte>&length=<bytes>` (`length <= 512 KiB`), returning the cached preview JSON (`cached: true`, `preview_id`, `engine_id`, `voice_preset`, `text`, `duration_seconds`, `sample_rate`, `size`, `sha256`, `mime`, base64-encoded WAV `data`, plus `offset`, `chunk_size`, and `has_more` for chunked reads). Status codes: `200` on success, `400` on malformed `preview_id` or out-of-bounds `offset >= size`, `401` on missing/invalid Bearer token, `404` on missing/corrupted cached preview, `409` when the instance is quiescing/releasing, and `422` on invalid query parameter bounds (such as `offset < 0` or `length > 524288`).

#### Go controller desktop commands (`ColabOneDesktop.Command`)

- **`comfyui.tts.preview`**:
  - Validates the request strictly via `comfyui.ValidateTtsPreviewRequest` (`ColabOneComfyTtsPreviewRequest`, forbidding unknown fields and nulls), automatically attaches the saved Hugging Face token from local `vault.enc` when not explicitly passed (redacted in all logs and snapshots), proxies `POST /api/v1/comfyui/tts-preview` (120 s RPC timeout), triggers a remote state refresh when `cached` is `false`, and returns the JSON payload above. On an oversized (`> 512 KiB`) cache hit, it returns the metadata-only `cached: true` payload (omitting inline `data`), and the desktop frontend then invokes `comfyui.tts.preview.asset` to retrieve the audio.
  - Example payload: `{ "engine_id": "qwen3_tts", "voice_preset": "qwen_vivian", "preview_text": "" }` (official Qwen3-TTS CustomVoice speakers use their `qwen_*` preset IDs: `qwen_vivian`, `qwen_serena`, `qwen_uncle_fu`, `qwen_dylan`, `qwen_eric`, `qwen_ryan`, `qwen_aiden`, `qwen_ono_anna`, `qwen_sohee`).
- **`comfyui.tts.preview.asset`**:
  - Accepts `preview_id` (or alias `id`), validates strict 32-hex format, retrieves and reassembles the cached WAV in bounded `<= 512 KiB` chunks via `GET /api/v1/comfyui/tts-previews/{preview_id}?offset=...&length=...` (60 s RPC timeout per request), verifies size and SHA-256, and returns the reassembled cached WAV preview JSON so the desktop frontend (`verifyAndDecodeColabOneBase64AudioUrl`) can verify `sha256` in memory before creating an object URL and playing the audition clip locally.
  - Example payload: `{ "preview_id": "a1b2c3d4e5f60718293a4b5c6d7e8f90" }`.

Manifests and validation (`common/comfyui.py` and `catalog/comfyui.json`):
- `catalog/comfyui.json` (`ComfyCatalog`) pins ComfyUI `repo`, `tag` (`v0.46.0`), `commit` (`a277b5bf213e8a44fa34817b7770b54f86ef4cea`), `port` (`8188`), `proxy_port` (`8189`), `tts_engines` (the 8 flagship remote GPU TTS engines with pinned repos/revisions, VRAM/disk thresholds, upstream license metadata, and voice presets), and an extensible `templates` list of `WorkflowTemplate` packs (including built-in digital-human long-video pipelines whose default TTS engine is `"fish_drama3"`).
- `WorkflowTemplate` declaratively lists pinned `custom_nodes` (`ComfyNodeSpec` with 40-char commit SHA), validated `pip_packages`, and pinned `models` (`ComfyModelRef` with 40-char Hugging Face revision, 64-char SHA-256, and allowlisted ComfyUI `target_dir`: `checkpoints`, `diffusion_models`, `loras`, `vae`, `text_encoders`, `clip_vision`, `wav2vec2`, `controlnet`, `upscale_models`, `audio_encoders`).
- Auto-extracted `WorkflowField` entries support `text`, `textarea`, `number`, `integer`, `boolean`, `select`, `image`, and `audio`.
- Output media allowlist (`ALLOWED_OUTPUT_MIME`): `image` (`png`, `jpg`, `jpeg`, `webp`, `gif`), `video` (`mp4`, `webm`), and `audio` (`wav`, `mp3`, `flac`), up to 512 MiB per file. Desktop preview (`frontend/src/comfyui.ts`) blocks active or scriptable MIME types (`image/svg+xml`, HTML, JS) and verifies SHA-256 in memory before creating an object URL (including `verifyAndDecodeColabOneBase64AudioUrl` for TTS audition WAVs); native `ExportWorkflowOutput(id)` verifies SHA-256 and atomically writes to the user-selected save path.

Desktop commands (`ColabOneDesktop.Command`) for ComfyUI:
- Engine: `comfyui.start`, `comfyui.free`, `comfyui.stop`
- Workflow Studio: `comfyui.workflow.import`, `comfyui.workflow.delete`, `comfyui.input.upload`, `comfyui.run` (cancellable via `task.cancel`)
- TTS Voice Audition: `comfyui.tts.preview` (cached remote GPU voice audition synthesis / async `comfyui_tts_preview` task), `comfyui.tts.preview.asset` (cached SHA-256-verified WAV preview fetch)
- Outputs: `comfyui.output.asset` (chunked verified read used by preview and `ExportWorkflowOutput`), `comfyui.output.export_drive`, `comfyui.output.delete`
- Native Canvas: `comfyui.canvas.start`, `comfyui.canvas.stop`, `comfyui.canvas.url` (used by `ColabOneDesktop.OpenComfyCanvas("window" | "browser")` in `console/comfyui.go` and explicit link copy; session tokens and tunnel tokens never appear in desktop snapshots or logs).
