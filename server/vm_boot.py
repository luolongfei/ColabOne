# Executed inside the Colab kernel. It contains no secrets: those arrive via ContentsClient.
# Userspace Tailscale bootstrap adapted from colab-gpu-llm-service (MIT).
import json
import hashlib
import os
import subprocess
import sys
import tarfile
import time
import urllib.request
import zipfile
from pathlib import Path


def colabone_boot():
    config_path = Path("/content/colabone-boot.json")
    config = json.loads(config_path.read_text())
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
    ) % (config["deadline"], "http://" + addr + "/unassign")
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
    ts_dir = Path("/content/colabone-ts")
    ts_dir.mkdir(exist_ok=True)
    archive_path = ts_dir / "tailscale.tgz"
    urllib.request.urlretrieve(
        "https://pkgs.tailscale.com/stable/tailscale_1.104.1_amd64.tgz", archive_path
    )
    if (
        hashlib.sha256(archive_path.read_bytes()).hexdigest()
        != "108d1d96ecf410d305571e173516f27038f870919a9b89c914a167c6d33a4528"
    ):
        raise RuntimeError("Tailscale archive checksum mismatch")
    with tarfile.open(archive_path) as archive:
        archive.extractall(ts_dir, filter="data")
    ts = next(ts_dir.rglob("tailscale"))
    daemon = next(ts_dir.rglob("tailscaled"))
    sock = str(ts_dir / "tailscaled.sock")
    log = open("/content/colabone-network.log", "w")
    subprocess.Popen(
        [
            str(daemon),
            "--tun=userspace-networking",
            "--socket=" + sock,
            "--state=" + str(ts_dir / "state"),
        ],
        stdout=log,
        stderr=log,
        start_new_session=True,
    )
    for _ in range(40):
        if Path(sock).exists():
            break
        time.sleep(0.5)
    subprocess.run(
        [
            str(ts),
            "--socket=" + sock,
            "up",
            "--auth-key=" + config.pop("ts_key"),
            "--hostname=colabone-" + config["runtime_id"][-8:],
            "--accept-dns=false",
            "--timeout=60s",
        ],
        capture_output=True,
        check=True,
        timeout=90,
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
    subprocess.run(
        [
            str(ts),
            "--socket=" + sock,
            "serve",
            "--bg",
            "--tcp=7860",
            "tcp://127.0.0.1:7860",
        ],
        check=True,
        capture_output=True,
        timeout=30,
    )
    status = json.loads(
        subprocess.check_output([str(ts), "--socket=" + sock, "status", "--json"])
    )
    ip = next(ip for ip in status["TailscaleIPs"] if ":" not in ip)
    print("COLABONE_URL=http://" + ip + ":7860", flush=True)


colabone_boot()
