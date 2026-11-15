# Source provenance

- Colab-yoga, Apache-2.0, c29b186e11ed4a9df92ad5b3631b4560f6cb615a. Git history retained; allocation/readiness/heartbeat and verified Python packaging adapted. https://github.com/incredible404-rgb/Colab-yoga
- colab-gpu-llm-service, MIT, 32915c0d059f53f635a2f9fc737cf58c23104314. Model manifests, pinned llama.cpp CUDA deployment adapted. https://github.com/incredible404-rgb/colab-gpu-llm-service

Unsafe runtime source-string runners, local MCP forwarding, implicit model deletion and notebook workflows are not shipped.

- Google Colab CLI, Apache-2.0, version 0.7.5. Credential propagation protocol in `commands/automation.py` adapted into `bridge/drive_mount.py`, with asynchronous handling and no terminal input. https://github.com/googlecolab/google-colab-cli
- WittmannF/easycolab, MIT, 74e58edda37de6b6ceb6c064f83243ba07514fda (2019-02-26). Referenced its native `drive.mount()` approach; its legacy `tkmount()` token implementation is not used. https://github.com/WittmannF/easycolab
