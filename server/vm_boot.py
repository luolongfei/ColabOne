# Executed inside the Colab kernel. It contains no secrets: those arrive via ContentsClient.
# The control service is loopback-only; the authenticated Colab kernel carries RPC.
import json
import os
import subprocess
import sys
import time
import urllib.request
import zipfile
from pathlib import Path


def colabone_boot():
    config_path = Path("/content/colabone-boot.json")
    config = json.loads(config_path.read_text(encoding="utf-8"))
    # An independent deadline guard starts before dependency installation or networking.
    addr = os.environ.get("TBE_RUNTIME_ADDR", "")
    if not addr:
        raise RuntimeError(
            "Colab runtime release endpoint unavailable; refusing unprotected startup"
        )
    guard = (
        "import time,urllib.request\n"
        "time.sleep(max(0,%r-time.time()))\n"
        "while True:\n"
        " try:\n"
        "  urllib.request.build_opener(urllib.request.ProxyHandler({})).open(urllib.request.Request(%r,data=b'',method='POST'),timeout=15)\n"
        "  break\n"
        " except Exception: time.sleep(5)\n"
    ) % (config["deadline"] + 40, "http://" + addr + "/unassign")
    subprocess.Popen(
        [sys.executable, "-c", guard],
        stdin=subprocess.DEVNULL,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
        start_new_session=True,
    )
    with zipfile.ZipFile("/content/colabone.zip") as archive:
        archive.extractall("/content")
    subprocess.run(
        [
            sys.executable,
            "-m",
            "pip",
            "install",
            "--quiet",
            "-r",
            "/content/colabone/server/requirements.txt",
        ],
        check=True,
        timeout=180,
    )
    config_path.write_text(json.dumps(config))
    config_path.chmod(0o600)
    env = dict(
        os.environ,
        PYTHONPATH="/content/colabone",
        COLABONE_REMOTE_CONFIG=str(config_path),
        COLABONE_RUNTIME_ID=config["runtime_id"],
        PYTHONUNBUFFERED="1",
    )
    log = open("/content/colabone-server.log", "w")
    proc = subprocess.Popen(
        [sys.executable, "-m", "server.app"],
        cwd="/content/colabone",
        env=env,
        stdin=subprocess.DEVNULL,
        stdout=log,
        stderr=log,
        start_new_session=True,
    )
    for _ in range(60):
        if proc.poll() is not None:
            raise RuntimeError("ColabOne service failed to start")
        try:
            urllib.request.urlopen("http://127.0.0.1:7860/health", timeout=1)
            break
        except Exception:
            time.sleep(0.5)
    else:
        raise RuntimeError("ColabOne service readiness timeout")
    print("COLABONE_READY=1", flush=True)


colabone_boot()
