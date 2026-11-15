# Remote API v1

`/health` is the only unauthenticated route. All `/api/v1/*` requests require `Authorization: Bearer <per-runtime token>` on the runtime loopback interface, reached through authenticated Colab kernel RPC. Credentials are not returned to the frontend.

| Method | Route | Behavior |
|---|---|---|
| GET | `/api/v1/snapshot` | Runtime, models and conversations in one control request |
| GET | `/api/v1/status` | Runtime identity, GPU/disk, loaded model, task and sync state; does not reset idle |
| POST | `/api/v1/configure` | Idle minutes and current budget deadline |
| POST | `/api/v1/restore` | Restore cloud model config and conversation snapshots |
| GET / POST | `/api/v1/models` | List actual remote installation state / add validated ModelSpec |
| POST | `/api/v1/models/{id}/download` | Start remote verified download; optional transient HF token |
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
