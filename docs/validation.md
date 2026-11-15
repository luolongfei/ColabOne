# Validation record

## Verified locally

- macOS Apple Silicon: Go vet/tests, Vue TypeScript production build, Python compile/import self-test.
- 54 offline Python tests: authenticated remote API, model manifests/shards, download resume/hash verification, low disk/VRAM, model retention, cancellation, event replay, upload races, Drive flush/disconnect protection, owned-instance release, failed-start cleanup, whole-page authorization parsing, OAuth callback state, and UTF-8 process pipes under an ASCII locale.
- 4 frontend state tests and 6 headless Chrome interaction tests. UI mocks simulate remote state and do not demonstrate a real GPU.
- Portable macOS package builds with a hash-verified standalone Python and locked wheels; bundled runtime self-test succeeds without accessing credentials or network.
- Native ColabOne app: Google login retained; simplified settings, remote model management, chat and Markdown export exercised on macOS.

## Real Colab acceptance — 2026-11-15

- An owned Tesla T4 instance was allocated and reached ready state through the authenticated Colab kernel channel, with no Tailscale installation.
- Qwen2.5 0.5B Q4_K_M downloaded directly to remote storage (469 MiB), passed SHA-256 verification, and loaded with CUDA. No model weights were downloaded to the desktop.
- Two chat turns returned actual model text; the second returned “你好”. A Markdown conversation export was verified locally.
- Unloading cleared the engine/GPU allocation while preserving downloaded weights. Reopening the desktop reconnected to the same owned instance without allocating another GPU.
- Reused control-channel reads reached a 0.26-second round trip after connection warmup. This is a single observation, not a latency guarantee or inference benchmark.
- The owned instance was released and its absence confirmed through Colab assignments. Account consumption rate returned to zero. Observed balance reduction: 0.474 CU, within the authorized 5 CU total test budget. Billing observations can lag.
- Native Drive consent reached Google's “Google Drive for desktop” authorization page. The user has not yet completed that consent, so real mount, upload flush and cross-runtime restoration remain **unverified**. The test conversation was exported before releasing the instance.

## Platform CI and remaining checks

- Both macOS and Windows checks/packages passed in Actions run 43684822722 (commit c7bc9c7). Later changes must pass the same CI before merge; the PR check links are authoritative for the final head.
- Windows interactive UI still requires a Windows desktop; CI build/runtime self-test is a separate level of evidence.
- Large upstream presets have not been GPU-benchmarked in ColabOne. The acceptance model was the lightweight Qwen2.5 preset.
- No separate Google Cloud project, API key, OAuth JSON, or Tailscale setup is part of the final flow. The earlier API-project experiment stopped at Google's Terms-of-Service prerequisite and was removed.
