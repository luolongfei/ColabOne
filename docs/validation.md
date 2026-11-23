# Validation record

## Pure Go single-binary desktop architecture (current)

- The desktop client is now a pure Go single-binary native application (`MyGo v0.10.0` + in-process Go controller + `coder/websocket`), shipping zero local Python runtime, zero subprocess bridge pipes, and no sibling `runtime/` or `site-packages/` folders.
- The remote Python control service (`server/`, `common/`, `catalog/`) is embedded directly inside the Go binary (`console/internal/servicebundle/embedded/` via `go:embed`, synced with `tools/sync_service_bundle.py`) and deployed exclusively onto the remote Colab GPU runtime.
- Offline validation (`tools/install.py --root work/check --check`) runs `go vet`/`go test` across all Go desktop/controller packages, cross-language contract tests (`tests/test_go_python_contracts.py`), remote-service & packaging pytest suites, Vitest frontend tests, and production frontend + native binary builds with zero live Google, Hugging Face, or GPU calls.
- The dated `2026-11-15` entries below are historical records captured prior to retiring the legacy local Python bridge and bundled CPython runtime.

## Full change review — 2026-11-15 (historical)

Reviewed the ColabOne changes from the initial unified-client implementation through the white-background vector icon, focusing on remote lifecycle, credentials, Drive synchronization, download/load/chat/creative paths, public API, desktop transport, appearance and packaging. This review did not allocate a GPU or change the user's account credentials.

Reproduced and fixed:

- Expired proxy tokens were used for RPC, and failed periodic configuration could prevent the heartbeat that renews them. Late kernel callbacks could overwrite renewed tokens or recreate a released session.
- Concurrent status checks could clear the engine handle during stop/load, causing an exception or reporting an exited engine as loaded. Shutdown during engine preparation could also launch an orphan process.
- Initial background upload after mounting Drive could overwrite the previous instance's model catalog before restore. Restore and upload now share a lock, with merge required before the first write.
- A second streamed chat turn could temporarily replace the first answer while snapshots lagged. The stream now addresses its own turn and preserves completed output until persistence catches up.
- Public creative downloads lacked the size/hash validation already used by desktop exports.
- A private bridge reply larger than 16 MiB stopped the Go scanner while Python was still writing, leaving both processes waiting. JSON stream decoding removes this failure mode.
- Reinstalling by merging directories retained obsolete app resources and Python modules. Installations now stage a complete replacement and preserve the previous tree if replacement/rollback fails.

Local validation: required installer check passed, including 134 offline Python tests, 9 frontend state tests, Go vet/tests, TypeScript production build and bundled import self-test. All 25 browser interaction tests passed, including the delayed-snapshot regression and existing theme, billing, drawer, model search, creative and API controls. Browser tests now also run in macOS and Windows CI using Playwright's pinned Chromium; screenshot paths use platform-aware file URL conversion.

The user's final color preference restores blue accents for navigation, primary actions, links and focus indicators. Light/dark/system appearance, the white portrait icon and red billing/danger colors remain. Both appearances pass the existing contrast and overflow checks.

These checks use mock engines/transports and temporary storage. They do not replace a new real-GPU acceptance run; real image/video generation and external Cloudflare routing remain unverified in this review because the user declined another paid allocation. Earlier real T4 results below are historical and are not presented as validation of this patch.

## Historical local verification — 2026-11-15

- macOS Apple Silicon: Go vet/tests, Vue TypeScript production build, Python compile/import self-test.
- 80 offline Python tests: authenticated remote API, model manifests/shards, 8/32-worker HTTP download, sparse resume/hash verification, low disk/VRAM, model retention, cancellation, event replay, upload races, Drive flush/disconnect protection, consent propagation, owned-instance release, idle countdown/authorization leases, failed-start cleanup, whole-page authorization parsing, OAuth callback state, and UTF-8 process pipes under an ASCII locale.
- 4 frontend state tests and 16 headless Chrome interaction tests. Coverage includes overlay drawer geometry, full-row search, model sizes/likes/downloads, delayed metadata across pagination, exact selected download sizes and idle countdown interaction. UI mocks simulate remote state and do not demonstrate a real GPU.
- Portable macOS package builds with a hash-verified standalone Python and locked wheels; bundled runtime self-test succeeds without accessing credentials or network.
- Native ColabOne app: Google login retained; simplified settings, remote model management, chat and Markdown export exercised on macOS.

## Real Colab acceptance — 2026-11-15

- An owned Tesla T4 instance was allocated and reached ready state through the authenticated Colab kernel channel, with no Tailscale installation.
- Qwen2.5 0.5B Q4_K_M downloaded directly to remote storage (469 MiB), passed SHA-256 verification, and loaded with CUDA. No model weights were downloaded to the desktop.
- Two chat turns returned actual model text; the second returned “你好”. A Markdown conversation export was verified locally.
- Unloading cleared the engine/GPU allocation while preserving downloaded weights. Reopening the desktop reconnected to the same owned instance without allocating another GPU.
- Reused control-channel reads reached a 0.26-second round trip after connection warmup. This is a single observation, not a latency guarantee or inference benchmark.
- The owned instance was released and its absence confirmed through Colab assignments. Account consumption rate returned to zero. Observed balance reduction: 0.474 CU, within the authorized 5 CU total test budget. Billing observations can lag.
- On the user-started G4 instance, browser consent completed but the old dry-run polling stayed false. A diagnostic on that exact instance returned success=false for the dry run and success=true for real propagation. The revised flow retries real propagation after the initial check. Native macOS subsequently showed Drive connected; restarting restored both Google login and the mounted runtime. Cross-runtime restoration remains unverified.

## Platform CI and remaining checks

- Both macOS and Windows checks/packages passed in Actions run 43685341941 (feature head 333ea55, merged as 8bef127). Later changes must pass the same CI before merge; the PR check links are authoritative for each head.
- Windows interactive UI still requires a Windows desktop; CI build/runtime self-test is a separate level of evidence.
- Large upstream presets have not been GPU-benchmarked in ColabOne. The acceptance model was the lightweight Qwen2.5 preset.
- No separate Google Cloud project, API key, OAuth JSON, or Tailscale setup is part of the final flow. The earlier API-project experiment stopped at Google's Terms-of-Service prerequisite and was removed.

## Authorization and visibility regression checks

- Offline tests reproduce consent requiring real propagation, reject ambiguous success, preserve saved refresh credentials after network failures, and serialize concurrent refreshes.
- Stop All tests cover reviewed targets, account changes, partial failures, lost responses and instances created after preview. Automatic single-instance cleanup remains covered.
- The activity feed is tested for bounded storage, credential redaction and duplicate progress suppression. Browser tests cover blue layout, red billing indicators, instance review before Stop All, filtering/clearing/collapsing logs and retained sidebar preference.

- The updated native macOS app restored the saved login and showed the existing G4, mounted Drive, one account instance, its 10.05 CU/hour rate, the blue activity panel, and the reviewed Stop All instance list. The preview was cancelled without releasing the user's running instance. Multi-instance termination is covered offline; no additional runtimes were allocated to test it.

## Parallel download and idle-release acceptance — 2026-11-15

The user approved one T4 test with a maximum budget of 1 CU. Times below are Asia/Shanghai.

- 16:45:09: started one Tesla T4 from the native client. Saved Google login was reused. The account reported 1.57 CU/hour.
- On this fresh runtime, Google requested Drive confirmation. The browser reached “Please close this tab / window”; ColabOne received the grant, mounted Drive and confirmed synchronization at 16:46:37. No Cloud project or API key was used. This validates receipt of fresh-runtime consent, not permanent consent across future runtimes; Google controls that requirement. A pending HTTP 400 is separately covered by the offline regression test.
- Eight workers downloaded the 469 MiB Qwen2.5 0.5B model directly to the GPU. Download plus SHA-256 verification completed in 4.4 seconds; logged average transfer was 106.6 MiB/s.
- Live community search selected Qwen's 3B Q4_K_M model (1.96 GiB). With 32 workers, observed telemetry showed 32 simultaneous connections and a 345.7 MiB/s sample. Download plus verification completed in 14.3 seconds; average transfer was 140.4 MiB/s. These are single-run observations, not guaranteed speeds.
- The 0.5B model loaded remotely and answered the Chinese test question with “1 加 1 等于 2。” at 16:55:15. The control channel briefly reported errors and recovered automatically; first-turn latency was high and is not presented as an inference benchmark.
- Without further interaction, the client logged the start of the 60-second countdown at 16:58:15, release at 16:59:19 and confirmed stopped at 16:59:40. The account displayed zero instances and no billing. Displayed balance moved from 2386.84 to 2386.56 CU (approximately 0.28 CU; billing observations can lag), within the authorized limit. No second runtime was allocated.
- The new search card uses complete GGUF version sizes, summing all shards in each version. It does not mistake whole-repository storage for a model's download size. Size metadata is loaded progressively and reused when choosing a version; weights remain remote.
