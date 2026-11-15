# Remote API v1

`/health` is the only unauthenticated route. All `/api/v1/*` requests require `Authorization: Bearer <per-runtime token>` on the runtime loopback interface, reached through authenticated Colab kernel RPC. Credentials are not returned to the frontend.

| Method | Route | Behavior |
|---|---|---|
| GET | `/api/v1/snapshot` | Runtime, models and conversations in one control request |
| GET | `/api/v1/status` | Runtime identity, GPU/disk, loaded model, task and sync state; does not reset idle |
| POST | `/api/v1/configure` | Idle grace (default 3 min), countdown (default 60 sec), original budget deadline |
| POST | `/api/v1/activity` | Explicit user activity; optional bounded Drive consent lease, never called by polling |
| POST | `/api/v1/restore` | Restore cloud model config and conversation snapshots |
| GET / POST | `/api/v1/models` | List actual remote installation state / add validated ModelSpec |
| POST | `/api/v1/models/{id}/download` | Start remote verified download; 8/32 threads and optional transient HF token |
| POST | `/api/v1/models/{id}/load` | Start loading already downloaded files |
| POST | `/api/v1/unload` | Stop the inference process, preserve weights |
| DELETE | `/api/v1/models/{id}` | Delete only this model's remote weight directory |
| GET | `/api/v1/conversations` | Full remote conversation snapshots |
| POST | `/api/v1/turns` | `{conversation_id?, text}` → task ID and conversation ID |
| GET | `/api/v1/tasks/{id}/events?after=N` | SSE with monotonic IDs, conversation/delta/done events |
| GET | `/api/v1/tasks/{id}/poll?after=N` | Incremental event batch and task state; reconnect from cursor |
| DELETE | `/api/v1/tasks/{id}` | Cooperatively cancel the task |
| POST | `/api/v1/sync` | Write dirty snapshots to mounted Drive; report pending count |
| POST | `/api/v1/prepare-release` | Cancel and await work, sync, flush Drive uploads and unmount; fail if not confirmed |

`ModelSpec` includes a safe ID, display metadata, context length, resource hint, and a list of HF files. Each file has owner/repo, full commit SHA, relative GGUF path, size, SHA-256 and model/shard role. Unknown fields (including executable code) are rejected. The first shard is the sole model entrypoint.

Task state is `running`, `completed`, `cancelled` or `failed`. Resource conflicts return 409; missing IDs 404; invalid manifests 422; wrong tokens 401. Runtime `stopped`, `starting`, `disconnected` and `releasing` are desktop lifecycle states; a network failure never implies release.

Go services exposed to the frontend are generated in `frontend/src/mygo.ts`: Snapshot, Command, OpenURL (allowlisted HTTPS only), ReadClipboard (user-triggered paste), and ExportConversation (native save dialog). Python RPC travels over private process pipes and exposes no local HTTP server. No command accepts arbitrary shell code.


Protocol 2 runtime status includes `idle` (phase, remaining_seconds, countdown_seconds, idle_seconds, server_time). Task `download` includes downloaded_bytes, total_bytes, speed_bps, eta_seconds, elapsed_seconds, resumed_bytes, threads, connections, phase, filename and file counts. Bounded task `logs` carry monotonic sequence IDs; the desktop deduplicates them independently of progress.

Desktop commands `hub.search`, `hub.files` and `hub.install` cover metadata search, pinned version selection and remote installation. `runtime.upgrade` updates an idle older control service without changing its storage or budget. `runtime.activity` queues explicit interaction and does not run a blocking kernel call on the UI thread.

## Creative protocol 3

Runtime status advertises `capabilities: [chat, image, video]` and the actual `active_options` for a loaded chat model. Snapshot also includes creation metadata. Loading chat accepts optional `context_length` and `temperature`; the base manifest is unchanged.

| Method | Route | Behavior |
|---|---|---|
| POST | `/api/v1/creative/references` | Validated PNG/JPEG/WebP base64, max 8 MiB / 16M pixels; returns remote reference ID |
| POST | `/api/v1/creative/generate` | Validated CreativeRequest → cancellable image/video task; requires matching loaded model |
| GET | `/api/v1/creations/{id}/asset` | Bounded binary chunk as base64; offset, total, SHA-256, MIME; max 512 KiB per call |
| DELETE | `/api/v1/creations/{id}` | Move artifact and metadata to remote/cloud trash |

Creative recipes allow GGUF and safetensors components with model/llm/t5xxl/vae roles. Each supported recipe validates its complete role set. Only fixed catalog recipes are enabled; arbitrary repository Python is never executed. Generation progress reports conditioning, sampling, decoding and encoding; a sampling pass reaching 100% is not completion. Native `ExportCreation(id)` uses a save dialog, verifies the complete SHA-256 and atomically publishes the chosen file. It accepts no remote-supplied destination path.

## Public inference API (protocol 4)

This is a **separate** ASGI application on remote `127.0.0.1:7861`. Cloudflared routes to this port. Management remains on private `127.0.0.1:7860`; none of `/api/v1/*`, the login flow, file manager or conversation history exists on the public app. `/health` reveals only a random gateway instance identifier for tunnel verification. Every inference route requires `Authorization: Bearer <API Key>`; there is no loopback bypass, query-string key or anonymous mode. CORS allows Bearer-based clients without cookies.

Use the desktop **API 服务** page to enable either a Quick Tunnel (temporary `trycloudflare.com` hostname, no account required) or a remotely managed named Tunnel (your domain + Cloudflare installation command/token). For named tunnels, configure the public hostname's HTTP service as `127.0.0.1:7861`. Interactive Cloudflare Access login prevents programmatic API clients from connecting. The page reports ready only after the public `/health` identity matches, anonymous `/v1/models` is rejected and the authenticated model list succeeds. Verification is repeated every 20 seconds; transient failure reports reconnecting. The API Key is distinct from the internal control token, kept in the OS credential vault, and sent only to the GPU. Explicit copy/reveal returns it to the desktop UI; snapshots and logs omit both API Key and Tunnel Token. Rotation first disables the remote gateway, so failed revocation cannot be mistaken for success.

| Method | Public route | Contract |
|---|---|---|
| GET | `/v1/models` | OpenAI list, **currently loaded model only**, with `kind` extension |
| POST | `/v1/chat/completions` | Text-only OpenAI Chat Completions subset: model, system/user/assistant string messages, stream, temperature, top_p, max_tokens, seed, stop; preserves engine choices and usage |
| POST | `/v1/images/generations` | `model`, `prompt`, `size` (e.g. `512x512`), `n` (1–4), `response_format=b64_json`; returns `created` and `data[].b64_json` |
| POST | `/v1/jobs` | ColabOne **asynchronous** image/video API: CreativeRequest without reference images; returns 202 and job |
| GET | `/v1/jobs/{id}` | `status`, progress, error, output IDs, MIME, size, and authenticated `content_path` |
| DELETE | `/v1/jobs/{id}` | Cancel an API-created job |
| GET | `/v1/jobs/{id}/outputs/{output_id}` | PNG / WebM bytes; only artifacts created by that API job |

No Responses API, tools/function calling, vision messages, arbitrary URLs, model download/switch or private desktop references are exposed. Unknown fields are rejected without echoing the request body. HTTP 401 = invalid key, 400 = unsupported parameters, 409 = wrong/unloaded model or busy, 413 = body exceeds 1 MiB, 503 = disabled gateway. Errors use an `error.message` envelope. One GPU task runs at a time across desktop and external requests; clients should retry 409 after existing work completes.

[Cloudflare Quick Tunnels do not support SSE](https://developers.cloudflare.com/tunnel/get-started/quick-tunnels/). Quick-mode `stream=true` therefore returns 400; use `stream=false` or a named tunnel. Nonstream chat has an 85-second engine deadline, synchronous image waiting is limited to 90 seconds, and disconnect/timeout cancels unfinished work. Named streaming has a 10-minute maximum and cancels on disconnect. Prefer `/v1/jobs` for slow image/video generation: it returns immediately, survives caller disconnection and is explicitly cancellable. Async jobs use the existing creative maximum of 60 minutes, always bounded by the instance's independent CU deadline. Completed job metadata is retained for the latest 100 public jobs in this gateway process; artifacts also appear in the desktop gallery and follow normal Drive backup rules. Public chat does not enter desktop conversation history.

Example (replace model ID with the loaded model):

```python
import os
from openai import OpenAI
client = OpenAI(base_url="https://YOUR-HOST/v1", api_key=os.environ["COLABONE_API_KEY"])
print(client.chat.completions.create(
    model="YOUR-CHAT-MODEL",
    messages=[{"role": "user", "content": "Hello"}],
    max_tokens=512, stream=False,
).choices[0].message.content)
```

Async image/video example:

```python
import os, time, requests
base = "https://YOUR-HOST"
headers = {"Authorization": "Bearer " + os.environ["COLABONE_API_KEY"]}
r = requests.post(base + "/v1/jobs", headers=headers, json={
    "model_id": "YOUR-VIDEO-MODEL", "kind": "video", "prompt": "Clouds crossing a valley",
    "width": 832, "height": 480, "frames": 33, "fps": 16,
}, timeout=30)
r.raise_for_status()
job = r.json()
while job["status"] == "running":
    time.sleep(2)
    r = requests.get(base + "/v1/jobs/" + job["id"], headers=headers, timeout=30)
    r.raise_for_status()
    job = r.json()
if job["status"] != "completed":
    raise RuntimeError(job.get("error") or job["status"])
for index, output in enumerate(job["outputs"]):
    r = requests.get(base + output["content_path"], headers=headers, timeout=60)
    r.raise_for_status()
    with open(f"result-{index}.webm", "wb") as file:
        file.write(r.content)
```

Control-only additions: `POST /api/v1/public-api/start` accepts mode/domain/tunnel_token/api_key, `POST /api/v1/public-api/stop` revokes access and cancels public jobs. Neither endpoint starts a GPU. Runtime snapshot `public_api` carries state, mode, URL, last public probe, request counts and bounded redacted lifecycle logs. Desktop commands are `api.save`, `api.start`, `api.stop`, `api.key` (explicit reveal/copy only), `api.rotate`.
