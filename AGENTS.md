# ColabOne development

- One desktop client; inference, weights and conversation execution belong to the remote runtime.
- Run `python tools/install.py --root <temporary directory> --check` before commits. All tests are offline.
- Never access live Google or Hugging Face from unit tests. Live acceptance is explicit and budgeted.
- Preserve Apache-2.0 and upstream MIT notices. Never commit credentials, session state or model weights.
- Changes land through a pull request with green macOS and Windows CI.
- Agent and local MCP execution are out of scope for v0.1.
