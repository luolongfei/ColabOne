# Validation record

## Verified locally

- macOS Apple Silicon: Go vet/tests, Vue TypeScript production build, Python compile/import self-test.
- 53 offline Python tests: authenticated API, data-only model import, shard resolution, remote download/resume/hash failure, low disk/VRAM, model retention, cancellation, SSE recovery, Drive retry, restored history with fresh weight inventory, owned-instance release and failed-start cleanup.
- 4 frontend state tests and 6 headless Chrome interaction tests. UI mocks simulate remote state and do not demonstrate a real GPU.
- Portable macOS package builds with a hash-verified standalone Python and locked wheels; bundled runtime self-test succeeds without accessing credentials or network.
- Actual native ColabOne app launched successfully. Device, model library and setup screens checked through macOS accessibility. Five preset models appeared; GPU actions correctly remained disabled before authorization. The simplified setup page retains the existing Google login and exposes whole-page authorization paste.

## Pending external validation

- Both platform jobs passed in GitHub Actions run 43684278020 (commit b6acbfa). Follow-up changes are subject to the same required CI before merge.
- Real Colab login, T4 allocation, authenticated kernel control, reconnect after desktop restart and remote Qwen2.5 0.5B download/hash verification have passed. Two real chat turns returned text. Release validation is in progress under the authorized 5 CU total test budget.
- Native Colab Drive mounting is implemented; the real browser consent and persistence round-trip are pending. No separate Cloud project or API client is required. Offline tests cover missing mounts, safe paths, upload races, flush failures, automatic consent polling and cancellation.
- Windows interactive UI checks require a Windows desktop; CI startup self-test is a separate level of evidence.
- Large upstream presets have not been GPU-benchmarked in ColabOne. The acceptance model is Qwen2.5 0.5B Q4_K_M, not the large presets.
