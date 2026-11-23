# Contributing

ColabOne ships as a pure Go single-binary desktop client (MyGo `v0.10.0` + in-process Go controller; zero local Python runtime). The remote Python control service (`server/`, `common/`, `catalog/`) is embedded into the Go binary via `console/internal/servicebundle/embedded/` and runs exclusively on the remote Colab VM.

Whenever you modify `server/`, `common/`, or `catalog/`, run `python tools/sync_service_bundle.py` to keep the embedded bundle in sync. Before committing, run `python tools/install.py --root work/check --check` (covering embedded-bundle sync, Go vet/test, remote-service Python pytest, Vitest, and frontend build). All automated tests use fixtures and mocks — no real Google, Hugging Face, or GPU calls.

Keep model manifests data-only. Keep all inference and weight operations on the remote runtime. Credentials, downloaded weights, local state, and build artifacts must not be committed. Preserve the licenses in LICENSE, NOTICE, and third_party.

Use feature branches and a pull request. macOS and Windows CI must pass before merging. Record the exact scope of manual/live validation in docs/validation.md; passing mocks does not prove a real GPU deployment.
