# ColabOne

A desktop client for remote Google Colab GPUs. Start a GPU, download and load a GGUF model **on the remote machine**, chat, sync conversations to Google Drive, and release the exact owned runtime.

Targets macOS Apple Silicon and Windows x64. The desktop does not download weights or run inference. One runtime and one loaded model per profile; Agent and local MCP execution are deferred.

See [README.md](README.md) for setup/build instructions, [architecture](docs/architecture.md), [API](docs/api.md), and [upstream provenance](third_party/SOURCES.md). Google Colab, Tailscale and a Google Drive Desktop OAuth client must be configured in the app before live use. Offline tests never provision GPUs.
