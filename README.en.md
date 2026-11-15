# ColabOne

A desktop client for remote Google Colab GPUs. Start a GPU, download and load a GGUF model **on the remote machine**, chat, sync conversations to Google Drive, and release the exact owned runtime.

Targets macOS Apple Silicon and Windows x64. The desktop does not download weights or run inference. One runtime and one loaded model per profile; Agent and local MCP execution are deferred.

See [README.md](README.md) for setup/build instructions, [architecture](docs/architecture.md), [API](docs/api.md), and [upstream provenance](third_party/SOURCES.md). Sign in with Google once, then start the GPU. The client uses Colab’s native Drive mount; confirm its browser consent when prompted. No Tailscale, separate Google Cloud project, API key, or OAuth JSON is required. Sessions are stored in My Drive / ColabOne. New runtimes may require Drive consent again; cloud history is restored after the runtime is connected. Offline tests never provision GPUs.
