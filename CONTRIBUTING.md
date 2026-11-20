# Contributing

Run `python tools/install.py --root work/check --check` with the development Python environment before committing. Tests use fixtures and mocks; no real Google, Hugging Face or GPU calls.

Keep model manifests data-only. Keep all inference and weight operations on the remote runtime. Credentials, downloaded weights, local state and build artifacts must not be committed. Preserve the licenses in LICENSE, NOTICE and third_party.

Use feature branches and a pull request. macOS and Windows CI must pass before merging. Record the exact scope of manual/live validation in docs/validation.md; passing mocks does not prove a real GPU deployment.
