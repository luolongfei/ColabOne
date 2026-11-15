# Validation record

## Verified locally

- macOS Apple Silicon: Go vet/tests, Vue TypeScript production build, Python compile/import self-test.
- 35 offline Python tests: authenticated API, data-only model import, shard resolution, remote download/resume/hash failure, low disk/VRAM, model retention, cancellation, SSE recovery, Drive retry, restored history with fresh weight inventory, owned-instance release and failed-start cleanup.
- 4 frontend state tests and 4 headless Chrome interaction tests. UI mocks simulate remote state and do not demonstrate a real GPU.
- Portable macOS package builds with a hash-verified standalone Python and locked wheels; bundled runtime self-test succeeds without accessing credentials or network.
- Actual native ColabOne app launched successfully. Device, model library and setup screens checked through macOS accessibility. Five preset models appeared; GPU actions correctly remained disabled before authorization.

## Pending external validation

- GitHub Windows/macOS CI and native package results are recorded after their runs finish.
- Real Colab allocation, Tailscale routing, GPU model download/load and inference require user authorization in the app. No GPU has been provisioned by these offline checks and no CU has been spent.
- Google Drive OAuth and remote snapshot round-trip need the user's Desktop OAuth client. Storage failure/recovery is covered by offline tests only.
- Windows interactive UI checks require a Windows desktop; CI startup self-test is a separate level of evidence.
- Large upstream presets have not been GPU-benchmarked in ColabOne. The acceptance model is Qwen2.5 0.5B Q4_K_M, not the large presets.
