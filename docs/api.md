# Remote API v1

`/health` is the only unauthenticated route on the control service (`127.0.0.1:7860`). All `/api/v1/*` requests require `Authorization: Bearer <per-runtime token>` on the runtime loopback interface, reached through authenticated Colab kernel RPC. Credentials are not returned to the frontend.

| Method | Route | Behavior |
|---|---|---|
| GET | `/api/v1/snapshot` | Runtime (including `protocol: 7`, `capabilities`, `resources`, `idle`, `public_api`, `comfyui`), `models`, `workflows` and `comfy_outputs` in one control request |
| GET | `/api/v1/status` | Runtime identity, `protocol: 7`, `capabilities: ["public_api", "comfyui"]`, `active_engine`, live `resources` telemetry (`gpu`, `memory`, `disk`, `cpu`, `history`), `idle` countdown, `loaded_model`, `public_api`, `comfyui` and task state; does not reset idle |
| POST | `/api/v1/configure` | Unified configurable idle auto-release timeout (default 30 min; presets `15m`, `30m`, `45m`, `1h`, `2h`); single-launch `max_cu` budget deadline removed |
| POST | `/api/v1/activity` | Explicit trusted user interaction that resets the idle auto-release countdown; never called by polling |
| POST | `/api/v1/drive/connect` | Attach & restore `ColabOne_Drive/workspace` via mounted official Colab DriveFS (`colab_drivefs`) or headless quota-project `rclone` |
| POST | `/api/v1/drive/sync` | Synchronize dirty `models.json`, `/workflows/**` and `/comfyui-outputs/**` to `ColabOne_Drive/workspace` |
| GET / POST | `/api/v1/models` | List actual remote GGUF installation state / add validated `ModelSpec` |
| POST | `/api/v1/models/{id}/download` | Start remote verified GGUF download; 16/32 threads (default 16), optional transient HF token, and optional `load=true` with `context_length` / `temperature` |
| POST | `/api/v1/models/{id}/load` | Load downloaded GGUF files into `llama-server` (`127.0.0.1:8081`); accepts optional `context_length` and `temperature`; stops ComfyUI first to enforce strict VRAM mutual exclusion |
| POST | `/api/v1/unload` | Stop `llama-server`, preserve weights |
| DELETE | `/api/v1/models/{id}` | Delete only this model's remote weight directory |
| POST | `/api/v1/public-api/start` | Start the separate LLM OpenAI gateway (`127.0.0.1:7861`) and its Cloudflared tunnel |
| POST | `/api/v1/public-api/stop` | Revoke public API access, stop its tunnel and cancel active public jobs |
| GET | `/api/v1/tasks/{id}/events?after=N` | SSE with monotonic IDs and incremental task/log/progress events |
| GET | `/api/v1/tasks/{id}/poll?after=N` | Incremental event batch and task state; reconnect from cursor |
| DELETE | `/api/v1/tasks/{id}` | Cooperatively cancel the task (download, load or queued ComfyUI workflow run) |
| POST | `/api/v1/prepare-release` | Stop public API and ComfyUI engine/canvas tunnel, cancel and await running tasks, and flush `ColabOne_Drive` before unassigning |

`ModelSpec` (`common/models.py`) includes a safe ID, display metadata, `kind: "chat"`, context length, temperature, resource hint, and a list of HF GGUF files (`role` is `"model"` or `"shard"`, strictly `.gguf`). Each file has `owner/repo`, full 40-char commit SHA, relative GGUF path, size and 64-char SHA-256. Unknown fields (including executable code) are rejected. The first shard (`role: "model"`) is the sole model entrypoint.

Task state is `running`, `completed`, `cancelled` or `failed`. Resource conflicts return `409`; missing IDs `404`; invalid manifests `422`; wrong tokens `401`; bad parameters `400`. Runtime `stopped`, `starting`, `disconnected` and `releasing` are desktop lifecycle states; a network failure never implies release.

Go services exposed to the frontend are generated in `frontend/src/colabOneBridge.ts` (`ColabOneDesktop` in `console/main.go` & `console/comfyui.go`): `Snapshot`, `Command`, `SetAppearance`, `ReadClipboard` (user-triggered paste), `WriteClipboard`, `OpenURL` (allowlisted HTTPS only), `ExportWorkflowOutput(id)` (native save dialog accepting only a 32-hex artifact ID, bounded 512 KiB chunks, max 512 MiB, allowlisted extensions, full SHA-256 verification and atomic rename), and `OpenComfyCanvas(target)` (`"window"` or `"browser"`, fetching the authenticated HTTPS `/_colabone_auth?token=...` URL exclusively from the trusted Python bridge). Python RPC travels over private process pipes and exposes no local HTTP server. No command accepts arbitrary shell code.

Runtime `/api/v1/status` (and `/api/v1/snapshot` `runtime`) returns:
- `protocol: 7` and `capabilities: ["public_api", "comfyui"]`, plus `active_engine` (`""`, `"llama"`, or `"comfyui"`).
- `resources` (collected by `ColabOneResourceSampler` in `server/telemetry.py` and rendered on the desktop by `ResourceMonitor.vue` as ColabOne real-time hardware charts in 2×2/4×1 grid or 4-row stacked modes): `gpu` (utilization and VRAM usage/capacity), `memory` (system RAM parsed from `/proc/meminfo`), `disk` (filesystem capacity/usage plus live I/O throughput rates from `/proc/diskstats`), `cpu` (utilization computed from `/proc/stat`), and a 60-sample rolling `history` time series.
- `idle`: unified configurable idle auto-release countdown state (`phase`, `remaining_seconds`, `countdown_seconds`, `idle_seconds`, `server_time`) defaulting to **30 minutes** (selectable options: `15m`, `30m`, `45m`, `1h`, `2h` from either the homepage dropdown or Settings). Single-launch CU budget (`max_cu`) and mandatory per-runtime budget deadlines are no longer used; active downloads, VRAM loads, external API inference, and ComfyUI queue/canvas requests pause the countdown, while trusted user interaction resets it via `runtime.activity` (`POST /api/v1/activity`).

Desktop snapshots also report live account billing state—active vs. idle billing status, hourly CU burn rate (`CU/h`), remaining CU balance, and human-friendly estimated remaining runtime (`X 天 Y 小时` / days & hours)—surfaced in both the persistent top bar and the homepage overview card. Task `download` includes `downloaded_bytes`, `total_bytes`, `speed_bps`, `eta_seconds`, `elapsed_seconds`, `resumed_bytes`, `threads`, `connections`, `phase`, `filename` and file counts. Bounded task `logs` carry monotonic sequence IDs; the desktop deduplicates them independently of progress.

Core desktop commands (`ColabOneDesktop.Command`) include:
- Runtime & account: `account.refresh`, `runtime.start`, `runtime.select`, `runtime.release`, `runtime.release.preview`, `runtime.release.all`, `runtime.upgrade` (in-place control-service upgrade to protocol `>= 7` when idle with no running task, loaded model or active engine), `runtime.activity`, `settings.save`, `drive.connect`, `drive.setup_headless`, `drive.cancel`, `drive.sync`, `task.cancel`, `logs.clear`.
- Model Picker (`选择 API 模型`): `hub.search`, `hub.files`, `hub.install`, `model.import`, `model.download`, `model.load`, `model.unload`, `model.delete`, `model.common`, `model.remove_local`, `common_repo.save`, `common_repo.remove`.

## Public inference API (LLM OpenAI gateway)

This is a **separate** ASGI application (`server/public_api.py`) on remote `127.0.0.1:7861`, fronted by Cloudflared. Management remains on private `127.0.0.1:7860`; none of `/api/v1/*`, login flows, file management, or ComfyUI routes exist on the public LLM gateway. `/health` reveals only a random gateway instance identifier for tunnel verification. Every inference route requires `Authorization: Bearer <API Key>`; there is no loopback bypass, query-string key or anonymous mode. CORS allows Bearer-based clients (`GET`, `POST`) without cookies.

Use the desktop **对外 API** page to configure and enable a remotely managed Named Tunnel (your fixed public HTTPS domain + Cloudflare installation command/token; temporary `trycloudflare.com` Quick Tunnels are not supported). Configure the public hostname's HTTP service as `http://127.0.0.1:7861`. Interactive Cloudflare Access login prevents programmatic API clients from connecting. The page reports ready only after the public `/health` identity matches, anonymous `/v1/models` is rejected and the authenticated model list succeeds. Verification is repeated every 20 seconds; transient failure reports reconnecting. The API Key is distinct from the internal control token, kept in the OS credential vault, and sent only to the GPU. Explicit copy/reveal returns it to the desktop UI; snapshots and logs omit both API Key and Tunnel Token. Rotation first disables the remote gateway, so failed revocation cannot be mistaken for success.

| Method | Public route | Contract |
|---|---|---|
| GET | `/health` | Unauthenticated gateway identity (`service`, random `instance`) for tunnel verification |
| GET | `/v1/models` / `/v1/models/{id}` | OpenAI model list & detail, **currently loaded GGUF LLM only** (`kind: "chat"`, includes active `context_length`) |
| POST | `/v1/chat/completions` | OpenAI Chat Completions (`system`/`developer`/`user`/`assistant`/`tool` messages, string or text-part arrays, `tools`, `tool_choice`, `parallel_tool_calls`, `response_format`, `stream`, `stream_options`, `max_tokens`/`max_completion_tokens`, sampling params) with automatic 3-stage context compaction on overflow |
| POST | `/v1/responses` | OpenAI Responses API (`wire_api = "responses"` for Codex & OpenAI SDKs): `instructions`, string/item `input`, `function` & `custom`/`apply_patch` tools, reasoning summaries, SSE lifecycle events (`response.created` … `response.completed`/`incomplete`/`failed`), `compaction`/`compaction_summary` replay, inline `context_management` compaction, automatic 3-stage overflow compaction with incremental prefix summary caching (reporting pre-compaction `usage.input_tokens` so Codex auto-compact triggers cleanly), and bounded in-memory `previous_response_id` |
| GET / DELETE | `/v1/responses/{id}` | Retrieve or delete a stored response from the bounded in-memory cache (cleared when API stops) |
| POST | `/v1/responses/compact` | Codex / OpenAI context compaction (`object: "response.compaction"`): CJK-aware chunked summarization within the active model's `context_length` (with adaptive shrink retry on overflow) returning a preserved user anchor plus a stateless portable `type: "compaction"` item (`encrypted_content`) and token `usage` |

Built-in desktop chat/image/video endpoints (`/api/v1/turns`, `/api/v1/conversations`, `/v1/images/generations`, `/v1/jobs`) have been removed. Vision/audio inputs (`image_url`, `input_image`, `input_audio`) and model download/switch routes are rejected without echoing request bodies. Send `X-ColabOne-Auto-Compact: off` to disable gateway-side overflow compaction and return OpenAI-standard `context_length_exceeded` directly (`HTTP 400` for non-streaming or `response.failed` with `error.code = "context_length_exceeded"` for streaming Responses). HTTP `401` = invalid key, `400` = invalid parameters or `context_length_exceeded`, `409` = wrong/unloaded model or GPU busy (including when ComfyUI owns VRAM), `413` = body exceeds 16 MiB, `503` = disabled gateway, `504` = timeout or client disconnect. Errors use an `error.message` envelope.

Both SSE streaming (`stream=true`) and non-streaming (`stream=false`) requests are supported over the fixed-domain Named Tunnel. Non-streaming requests enforce a wait deadline and cancel unfinished work on timeout or disconnect; streaming cancels on disconnect. Active inference and compaction requests pause the 30-minute idle auto-release countdown while running.

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

Control-only routes: `POST /api/v1/public-api/start` accepts `mode` (`"named"`)/`domain`/`tunnel_token`/`api_key`; `POST /api/v1/public-api/stop` revokes access and cancels public jobs. Neither endpoint starts a GPU. Runtime snapshot `public_api` carries state, mode (`"named"`), URL, last public probe, request counts and bounded redacted lifecycle logs. Desktop commands are `api.save`, `api.start`, `api.stop`, `api.key` (explicit reveal/copy only), `api.rotate`.

## ComfyUI workflow studio and native canvas (protocol 7)

Protocol `7` adds remote-only **ComfyUI** (`v0.46.0`, commit `a277b5bf213e8a44fa34817b7770b54f86ef4cea`, GPL-3.0) on loopback `127.0.0.1:8188` plus an authenticated Native Canvas HTTP + WebSocket reverse proxy on `127.0.0.1:8189`. `llama-server` (`8081`) and `ComfyUI` (`8188`) are strictly mutually exclusive on GPU VRAM; starting ComfyUI unloads any loaded LLM while retaining cached weights. Active ComfyUI queue runs, pack downloads and canvas proxy traffic pause the 30-minute idle auto-release countdown.

All `/api/v1/comfyui/*` routes live on the private control service (`127.0.0.1:7860`) and require the per-runtime control Bearer token. Live ComfyUI engine/canvas status is included in `/api/v1/status` and `/api/v1/snapshot`, and output records (`comfy_outputs`) are returned in `/api/v1/snapshot`:

| Method | Route | Behavior |
|---|---|---|
| POST | `/api/v1/comfyui/start` | Install (on first run) and launch remote ComfyUI on `127.0.0.1:8188`, unloading `llama-server` first |
| POST | `/api/v1/comfyui/stop` | Stop ComfyUI (`8188`) and its authenticated canvas proxy/tunnel (`8189`), preserving cached models and synced Drive data |
| POST | `/api/v1/comfyui/free` | Ask running ComfyUI to free GPU VRAM and unload cached models without stopping the ComfyUI process |
| POST | `/api/v1/comfyui/canvas/start` | Start the authenticated HTTP + WebSocket reverse proxy (`127.0.0.1:8189` → `127.0.0.1:8188`) and fixed-domain Named Cloudflared tunnel (`ComfyCanvasConfig`: `mode: "named"`, `domain` + `tunnel_token`) with a random session token exchanged at `/_colabone_auth?token=<token>` for an `HttpOnly` session cookie |
| POST | `/api/v1/comfyui/canvas/stop` | Stop the canvas tunnel and authenticated proxy (`8189`), invalidating active canvas sessions |
| GET | `/api/v1/comfyui/canvas/url` | Return the authenticated HTTPS `/_colabone_auth?token=<token>` launch URL on demand (omitted from snapshots and logs) |
| GET | `/api/v1/comfyui/workflows` | List built-in pack templates (`catalog/comfyui.json`) and custom imported workflows stored in `/content/colabone-data/workflows/**` |
| POST | `/api/v1/comfyui/workflows` | Validate and import a ComfyUI API-format JSON workflow (`WorkflowImportRequest`: max 2 MiB, max 500 nodes; rejects canvas-format graphs with top-level `nodes`), auto-extract up to 32 typed form fields (`WorkflowField`), and sync to `ColabOne_Drive/workspace/workflows/**` |
| DELETE | `/api/v1/comfyui/workflows/{workflow_id}` | Delete a custom workflow and move its remote Drive file to `workflows/.trash/` |
| POST | `/api/v1/comfyui/inputs` | Upload validated input media (`filename` + base64 `data`) for `image`/`audio` fields (extensions `png/jpg/jpeg/webp/wav/mp3/flac/ogg/m4a`; max 16 MiB image, max 64 MiB audio) → returns 32-hex asset ID |
| POST | `/api/v1/comfyui/run` | Submit `WorkflowRunRequest` (`workflow_id`, up to 128 `{node_id}.{input_name}` scalar `overrides`, up to 32 uploaded media `inputs`) → cancellable remote task executing over ComfyUI's loopback HTTP/WebSocket API |
| GET | `/api/v1/comfyui/outputs/{output_id}/asset` | Bounded base64 binary chunk read (`offset`, `length` <= 512 KiB) returning `data`, `size`, `offset`, `sha256`, `extension`, `mime` and `filename` for SHA-256 verified preview and native export |
| DELETE | `/api/v1/comfyui/outputs/{output_id}` | Delete output artifact and move its Drive files to `comfyui-outputs/.trash/` |

Manifests and validation (`common/comfyui.py` & `catalog/comfyui.json`):
- `catalog/comfyui.json` (`ComfyCatalog`) pins ComfyUI `repo`, `tag` (`v0.46.0`), `commit` (`a277b5bf213e8a44fa34817b7770b54f86ef4cea`), `port` (`8188`), `proxy_port` (`8189`), and an extensible `templates` list of `WorkflowTemplate` packs (Phase 1 ships the universal Workflow Studio foundation with `templates: []` ready for curated packs).
- `WorkflowTemplate` declaratively lists pinned `custom_nodes` (`ComfyNodeSpec` with 40-char commit SHA), validated `pip_packages`, and pinned `models` (`ComfyModelRef` with 40-char HF revision, 64-char SHA-256, and allowlisted ComfyUI `target_dir`: `checkpoints`, `diffusion_models`, `loras`, `vae`, `text_encoders`, `clip_vision`, `wav2vec2`, `controlnet`, `upscale_models`, `audio_encoders`).
- Auto-extracted `WorkflowField` entries support `text`, `textarea`, `number`, `integer`, `boolean`, `select`, `image`, and `audio`.
- Output media allowlist (`ALLOWED_OUTPUT_MIME`): `image` (`png`, `jpg`, `jpeg`, `webp`, `gif`), `video` (`mp4`, `webm`), and `audio` (`wav`, `mp3`, `flac`), up to 512 MiB per file. Desktop preview (`frontend/src/comfyui.ts`) blocks active/scriptable MIME types (`image/svg+xml`, HTML, JS) and verifies SHA-256 before creating an object URL; native `ExportWorkflowOutput(id)` verifies SHA-256 and atomically writes to the user-selected save path.

Desktop commands (`ColabOneDesktop.Command`) for ComfyUI:
- Engine: `comfyui.start`, `comfyui.free`, `comfyui.stop`
- Workflow Studio: `comfyui.workflow.import`, `comfyui.workflow.delete`, `comfyui.input.upload`, `comfyui.run` (cancellable via `task.cancel`)
- Outputs: `comfyui.output.asset` (chunked verified read used by preview and `ExportWorkflowOutput`), `comfyui.output.delete`
- Native Canvas: `comfyui.canvas.start`, `comfyui.canvas.stop`, `comfyui.canvas.url` (used by `ColabOneDesktop.OpenComfyCanvas("window" | "browser")` in `console/comfyui.go` and explicit link copy; session tokens and tunnel tokens never appear in desktop snapshots or logs).
