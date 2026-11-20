# ColabOne development

- One desktop client; inference, weights, API serving and ComfyUI workflows belong to the remote runtime.
- Run `python tools/install.py --root <temporary directory> --check` before commits. All tests are offline.
- On WSL2/Linux non-interactive shells, ALWAYS prepend `$HOME/.local/bin` to `PATH` and `$HOME/.local/sysroot/usr/lib/x86_64-linux-gnu` to `LD_LIBRARY_PATH`, and redirect stdin (`< /dev/null`). Never invoke Windows `/mnt/c/.../node.exe` or `npx.cmd` from Linux shells (they hang over WSL2 interop).
- After any UI-related change (`frontend/`, desktop window/chrome in `console/`, or user-visible state/telemetry shown in the UI), ALWAYS rebuild and automatically restart the local desktop app (on WSL2/Windows via `bash work/run-win-dev.sh < /dev/null`) once checks pass, so the user always sees the latest UI immediately.
- Never access live Google or Hugging Face from unit tests. Live acceptance is explicit and budgeted.
- Preserve GPL-3.0, Apache-2.0 and upstream MIT notices. Never commit credentials, session state or model weights.
- Follow the ColabOne hybrid layered naming convention across all new and refactored code:
  - Core domain classes, Go structs/RPC services, and exported domain types use `ColabOne*` (e.g. `ColabOneDesktop`, `ColabOneController`, `ColabOneProvider`, `ColabOneRuntime`, `ColabOneSnapshot`, `ColabOneTelemetryTracker`).
  - Branded entrypoints, bridge helpers, and theme/state helpers use `colabOne*` (TS/Go) or `colabone_*` / `create_colabone_*` (Python); keep local helper variables concise and idiomatic.
  - Environment variables, protocol markers, and top-level brand constants use `COLABONE_*` (storage keys `colabone.*`, cookies `colabone_*`).
  - Frontend CSS classes and CSS custom properties use `co-*` and `--co-*` (preserving `ant-*` for Ant Design Vue and `is-*` state modifiers).
  - Never carry third-party reference app names (e.g. `Antigravity-Manager`, `Windows Task Manager`, or legacy `COLAB_YOGA_*` env vars) into ColabOne code, comments, or tests; keep upstream attribution strictly in `LICENSE`, `NOTICE`, `third_party/`, and legal headers.
- Changes land through a pull request with green macOS and Windows CI.
- Agent and local MCP execution are out of scope for v0.1.
