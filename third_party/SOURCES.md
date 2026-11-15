# Source provenance

- Colab-yoga, Apache-2.0, c29b186e11ed4a9df92ad5b3631b4560f6cb615a. Git history retained; allocation/readiness/heartbeat and verified Python packaging adapted. https://github.com/incredible404-rgb/Colab-yoga
- colab-gpu-llm-service, MIT, 32915c0d059f53f635a2f9fc737cf58c23104314. Model manifests, pinned llama.cpp CUDA deployment and `scripts/parallel_download.py` 64 MiB range/thread-pool strategy adapted into `server/download.py` (with cancellation, bounded retries and durable resume metadata). https://github.com/incredible404-rgb/colab-gpu-llm-service

Unsafe runtime source-string runners, local MCP forwarding, implicit model deletion and notebook workflows are not shipped.

- Google Colab CLI, Apache-2.0, version 0.7.5. Credential propagation protocol in `commands/automation.py` adapted into `bridge/drive_mount.py`, with asynchronous handling and no terminal input. https://github.com/googlecolab/google-colab-cli
- WittmannF/easycolab, MIT, 74e58edda37de6b6ceb6c064f83243ba07514fda (2019-02-26). Referenced its native `drive.mount()` approach; its legacy `tkmount()` token implementation is not used. https://github.com/WittmannF/easycolab

- stable-diffusion.cpp, MIT, 5dcfa79801213f23e27b3e83617350ebb8ea93aa. Remote CUDA build and native async image/video API integration; original engine source is fetched only on the remote runtime, retaining upstream/submodule notices. https://github.com/leejet/stable-diffusion.cpp
- LocModel public UI, https://locmodel.com/, consulted for model/run/creative interaction patterns. No private source, brand assets or proprietary code copied.
- Image/video model recipes are recorded in `catalog/creative.json`, with source repositories, exact commits, byte sizes and LFS SHA-256 for all three components per recipe. Weights are fetched directly on the remote runtime, not bundled in the desktop distribution.
