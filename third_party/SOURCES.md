# Source provenance

- Colab-yoga, Apache-2.0, c29b186e11ed4a9df92ad5b3631b4560f6cb615a. Allocation/readiness/heartbeat and verified Python packaging adapted; upstream Apache-2.0 attribution retained in NOTICE. https://github.com/incredible404-rgb/Colab-yoga
- colab-gpu-llm-service, MIT, 32915c0d059f53f635a2f9fc737cf58c23104314. Model manifests, pinned llama.cpp CUDA deployment and `scripts/parallel_download.py` 64 MiB range/thread-pool strategy adapted into `server/download.py` (with cancellation, bounded retries and durable resume metadata). https://github.com/incredible404-rgb/colab-gpu-llm-service

Unsafe runtime source-string runners, local MCP forwarding, implicit model deletion and notebook workflows are not shipped.

- Google Colab CLI, Apache-2.0, version 0.7.5. Public OAuth scopes (`colaboratory` + `drive.file`) and refresh-token lifecycle reused in `bridge/auth.py` to generate headless rclone Drive credentials without per-instance browser prompts. https://github.com/googlecolab/google-colab-cli
- EasyColab (wy51ai/easycolab), Apache-2.0. Headless `rclone` `drive.file` mount and direct-API fallback pattern adapted into `bridge/auth.py` (`[colabone_gdrive]`) and `server/drive.py` (`ColabOne_Drive`). https://github.com/wy51ai/easycolab

- ComfyUI, GPL-3.0, v0.46.0 (a277b5bf213e8a44fa34817b7770b54f86ef4cea). Remote-only workflow engine fetched on the Colab GPU runtime and accessed over loopback HTTP/WebSocket API and an authenticated canvas proxy. https://github.com/Comfy-Org/ComfyUI
- LocModel public UI, https://locmodel.com/, consulted for model/run/creative interaction patterns. No private source, brand assets or proprietary code copied.

- luolongfei/Colab, private repository, a1ecb49d9a51fa12b867e56312aebc107a70b28a. Inspected at the owner's request for API-key controls, Quick/named Cloudflare workflows, token-paste parsing and public readiness checks. ColabOne implements its own separate, mandatory-auth inference gateway; no source, packaged binaries, credentials, notebooks or model weights from this repository are redistributed. https://github.com/luolongfei/Colab
- cloudflared, Apache-2.0, release 2026.11.0. Remote-only connector downloaded from official release assets with pinned SHA-256. https://github.com/cloudflare/cloudflared/releases/tag/2026.11.0
- rclone, MIT, release v1.76.0. Remote-only Google Drive mount/sync utility downloaded from official release assets with pinned SHA-256. https://github.com/rclone/rclone/releases/tag/v1.76.0
- Sarasa Gothic (Sarasa UI SC), SIL OFL 1.1, release v1.0.43 (`SarasaUiSC-TTF-1.0.43.7z`, SHA-256 `3dc0f534b2cfa5f98b58a574948d3e3f9eb140600bd738f259c3d077acb2de66`). Hinted Simplified Chinese + Latin/UI WOFF2 subsets (`Regular`, `SemiBold`, `Bold`) built reproducibly via `tools/build_fonts.py` into `frontend/src/assets/fonts/` and distributed under `third_party/sarasa-gothic.LICENSE`. https://github.com/be5invis/Sarasa-Gothic/releases/tag/v1.0.43
- flag-icons, MIT, version 7.6.0, Copyright (c) 2013 Panayiotis Lipiridis. Ready-made 4:3 SVG country/region flags (`cn`, `us`, `tw`, `hk`, `jp`, `kr`) bundled offline in the desktop language switcher. https://github.com/lipis/flag-icons
