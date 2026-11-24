import json
from pathlib import Path
import sys
import zipfile

import pytest

from tools import install as install_mod
from tools import live_acceptance
from tools import package as package_mod
from tools import runtime_bundle
from tools.install import replace_tree
from tools.sync_service_bundle import sync_service_bundle


def test_reinstall_removes_obsolete_resources_and_keeps_user_state(tmp_path):
    source, target = tmp_path / "source", tmp_path / "installed" / "app"
    source.mkdir()
    target.mkdir(parents=True)
    (source / "current.py").write_text("new", encoding="utf-8")
    (target / "obsolete.py").write_text("old", encoding="utf-8")
    state = target.parent / "settings.json"
    state.write_text("user settings", encoding="utf-8")
    replace_tree(source, target)
    assert sorted(p.name for p in target.iterdir()) == ["current.py"]
    assert state.read_text(encoding="utf-8") == "user settings"


def test_failed_install_restores_previous_tree(tmp_path, monkeypatch):
    source, target = tmp_path / "source", tmp_path / "app"
    source.mkdir()
    target.mkdir()
    (source / "new").touch()
    (target / "previous").touch()
    rename = Path.rename

    def fail(self, destination):
        if self.name == "new" and self.parent.name.startswith(".colabone-update-"):
            raise OSError("replacement failed")
        return rename(self, destination)

    monkeypatch.setattr(Path, "rename", fail)
    with pytest.raises(OSError, match="replacement failed"):
        replace_tree(source, target)
    assert sorted(p.name for p in target.iterdir()) == ["previous"]


def test_failed_rollback_keeps_the_recoverable_backup(tmp_path, monkeypatch):
    source, target = tmp_path / "source", tmp_path / "app"
    source.mkdir()
    target.mkdir()
    (target / "original").touch()
    rename = Path.rename

    def fail(self, destination):
        if Path(destination) == target:
            raise OSError("destination temporarily locked")
        return rename(self, destination)

    monkeypatch.setattr(Path, "rename", fail)
    with pytest.raises(OSError, match="locked"):
        replace_tree(source, target)
    assert len(list(tmp_path.glob(".colabone-update-*/previous/original"))) == 1


def test_gpl3_license_notices_and_catalog_packaging_invariants():
    root = Path(__file__).resolve().parents[1]
    license_text = (root / "LICENSE").read_text(encoding="utf-8")
    assert "GNU GENERAL PUBLIC LICENSE" in license_text
    assert "Version 3" in license_text
    notice_text = (root / "NOTICE").read_text(encoding="utf-8")
    assert "GPL-3.0" in notice_text
    assert "Apache License, Version 2.0" in notice_text
    assert "ComfyUI" in notice_text
    assert (root / "catalog/comfyui.json").is_file()
    assert not (root / "catalog/creative.json").exists()
    assert not (root / "third_party/stable-diffusion.cpp.LICENSE").exists()
    assert (root / "third_party/rclone.LICENSE").is_file()
    assert (root / "third_party/sarasa-gothic.LICENSE").is_file()
    assert "Sarasa UI SC" in notice_text
    assert "SIL OFL 1.1" in notice_text
    for weight in ("regular", "semibold", "bold"):
        font_file = root / f"frontend/src/assets/fonts/sarasa-ui-sc-{weight}.woff2"
        assert font_file.is_file()
        assert font_file.read_bytes()[:4] == b"wOF2"


def test_sync_service_bundle_mirrors_remote_files_and_cleans_stale(tmp_path):
    fake_root = tmp_path / "repo"
    for folder in ("server", "common", "catalog"):
        (fake_root / folder).mkdir(parents=True)
    (fake_root / "server/runtime.py").write_text("print('ok')\n", encoding="utf-8")
    (fake_root / "server/requirements.txt").write_text("fastapi==0.165.0\n", encoding="utf-8")
    (fake_root / "server/ignored.pyc").write_bytes(b"\x00\x01")
    (fake_root / "common/models.py").write_text("VALUE = 1\n", encoding="utf-8")
    (fake_root / "catalog/comfyui.json").write_text('{"port": 8188}\n', encoding="utf-8")

    embed_dir = tmp_path / "embedded"
    (embed_dir / "server").mkdir(parents=True)
    (embed_dir / "server/stale.py").write_text("old", encoding="utf-8")

    copied = sync_service_bundle(root=fake_root, embed_dir=embed_dir)
    assert copied == 4
    assert not (embed_dir / "server/stale.py").exists()
    assert not (embed_dir / "server/ignored.pyc").exists()
    assert (embed_dir / "server/runtime.py").read_text(encoding="utf-8") == "print('ok')\n"
    assert (embed_dir / "server/requirements.txt").read_text(encoding="utf-8") == "fastapi==0.165.0\n"
    assert (embed_dir / "common/models.py").read_text(encoding="utf-8") == "VALUE = 1\n"
    assert (embed_dir / "catalog/comfyui.json").read_text(encoding="utf-8") == '{"port": 8188}\n'

    # Also verify against the real repository tree into an isolated temp dir.
    real_embed = tmp_path / "real-embedded"
    real_count = sync_service_bundle(embed_dir=real_embed)
    assert real_count > 0
    assert (real_embed / "server/runtime.py").is_file()
    assert (real_embed / "common/models.py").is_file()
    assert (real_embed / "catalog/comfyui.json").is_file()


def _make_clean_pure_go_stage(stage: Path) -> None:
    stage.mkdir(parents=True, exist_ok=True)
    (stage / "ColabOne.exe").write_bytes(b"MZ-stub")
    (stage / "README.md").write_text("# ColabOne\n", encoding="utf-8")
    (stage / "LICENSE").write_text("GPL-3.0\n", encoding="utf-8")
    (stage / "NOTICE").write_text("Notices\n", encoding="utf-8")
    tp = stage / "third_party"
    tp.mkdir()
    (tp / "rclone.LICENSE").write_text("MIT\n", encoding="utf-8")


@pytest.mark.parametrize(
    "legacy_dir",
    ["runtime", "site-packages", "bridge", "server", "common", "catalog"],
)
def test_package_audit_rejects_accidental_legacy_dirs(tmp_path, legacy_dir):
    assert set(package_mod.FORBIDDEN_LEGACY_DIRS) == {
        "runtime",
        "site-packages",
        "bridge",
        "server",
        "common",
        "catalog",
    }
    stage = tmp_path / "stage"
    _make_clean_pure_go_stage(stage)
    package_mod.audit(stage)

    # Rejects even an empty leaked directory.
    leaked = stage / legacy_dir
    leaked.mkdir()
    with pytest.raises(RuntimeError, match=legacy_dir):
        package_mod.audit(stage)

    # Also rejects populated leaked directory.
    (leaked / "artifact.txt").write_text("legacy", encoding="utf-8")
    with pytest.raises(RuntimeError, match=legacy_dir):
        package_mod.audit(stage)


@pytest.mark.parametrize(
    ("goos", "goarch", "expected"),
    [
        ("darwin", "arm64", "macos-arm64"),
        ("darwin", "amd64", "macos-x64"),
        ("windows", "amd64", "windows-x64"),
        ("windows", "arm64", "windows-arm64"),
        ("linux", "amd64", "linux-x64"),
        ("linux", "arm64", "linux-arm64"),
    ],
)
def test_package_archive_platform_mapping(goos, goarch, expected):
    assert package_mod._archive_platform(goos, goarch) == expected


def test_package_archive_platform_defaults_and_fallback(monkeypatch):
    monkeypatch.setattr(install_mod, "_host_goos", lambda: "linux")
    monkeypatch.setattr(install_mod, "_host_goarch", lambda: "arm64")
    assert package_mod._archive_platform() == "linux-arm64"
    assert package_mod._archive_platform("linux", "riscv64") == "linux-riscv64"


@pytest.mark.parametrize(
    ("platform_name", "machine", "expected_platform"),
    [
        ("darwin", "arm64", "macos-arm64"),
        ("darwin", "x86_64", "macos-x64"),
        ("win32", "AMD64", "windows-x64"),
        ("win32", "ARM64", "windows-arm64"),
        ("linux", "x86_64", "linux-x64"),
        ("linux", "aarch64", "linux-arm64"),
    ],
)
def test_package_main_names_archive_for_host_platform(
    tmp_path, monkeypatch, capsys, platform_name, machine, expected_platform
):
    monkeypatch.setattr(sys, "platform", platform_name)
    monkeypatch.setattr(install_mod.platform, "machine", lambda: machine)

    fake_root = tmp_path / "repo"
    (fake_root / "console").mkdir(parents=True)
    (fake_root / "console" / "mygo.json").write_text(
        json.dumps({"version": "0.9.9"}), encoding="utf-8"
    )

    def fake_install(root_dir):
        target = Path(root_dir) / "Programs" / "ColabOne"
        _make_clean_pure_go_stage(target)
        return target

    monkeypatch.setattr(install_mod, "ROOT", fake_root)
    monkeypatch.setattr(install_mod, "install", fake_install)

    out_dir = tmp_path / "dist"
    monkeypatch.setattr(sys, "argv", ["package.py", "--out", str(out_dir)])
    package_mod.main()

    expected_zip = (out_dir / f"ColabOne-0.9.9-{expected_platform}.zip").resolve()
    assert expected_zip.is_file()
    assert capsys.readouterr().out.strip() == str(expected_zip)

    with zipfile.ZipFile(expected_zip) as zf:
        names = set(zf.namelist())
    assert "ColabOne/README.md" in names
    for forbidden in package_mod.FORBIDDEN_LEGACY_DIRS:
        assert not any(forbidden in Path(name).parts for name in names)


def test_package_audit_rejects_private_state_and_credentials(tmp_path):
    stage = tmp_path / "stage"
    _make_clean_pure_go_stage(stage)
    package_mod.audit(stage)

    settings = stage / "settings.json"
    settings.write_text("{}", encoding="utf-8")
    with pytest.raises(RuntimeError, match="Private state"):
        package_mod.audit(stage)
    settings.unlink()

    notes = stage / "notes.txt"
    notes.write_text("hf_" + "A" * 35, encoding="utf-8")
    with pytest.raises(RuntimeError, match="Credential pattern"):
        package_mod.audit(stage)


def test_runtime_bundle_pure_go_helpers(tmp_path):
    paths = runtime_bundle.ColabOneInstallPaths(str(tmp_path))
    assert paths.is_sandbox is True
    assert paths.target_install == tmp_path.resolve() / "Programs" / "ColabOne"
    assert runtime_bundle.go_env()["GOTOOLCHAIN"]

    exe = tmp_path / "ColabOne.exe"
    exe.write_bytes(b"stub")
    assert runtime_bundle.app_binary_exists(exe) is True
    assert runtime_bundle.app_executable(exe) == exe

    app_bundle = tmp_path / "ColabOne.app"
    app_bundle.mkdir()
    assert runtime_bundle.app_binary_exists(app_bundle) is True
    assert runtime_bundle.app_executable(app_bundle) == (
        app_bundle / "Contents" / "MacOS" / "ColabOne"
    )


@pytest.mark.parametrize(
    (
        "platform_name",
        "expected_local_parts",
        "expected_start_menu_parts",
        "expected_exe_name",
    ),
    [
        ("darwin", ("Library", "Application Support"), ("Applications",), "ColabOne.app"),
        ("win32", ("AppData", "Local"), ("StartMenu",), "ColabOne.exe"),
        ("linux", (".local", "share"), (".local", "share", "applications"), "colabone"),
    ],
)
def test_install_paths_sandbox_across_platforms(
    tmp_path,
    monkeypatch,
    platform_name,
    expected_local_parts,
    expected_start_menu_parts,
    expected_exe_name,
):
    monkeypatch.setattr(sys, "platform", platform_name)
    sandbox = tmp_path / f"sandbox-{platform_name}"
    paths = runtime_bundle.ColabOneInstallPaths(str(sandbox))
    resolved = sandbox.resolve()

    assert paths.is_sandbox is True
    assert paths.local_appdata == resolved.joinpath(*expected_local_parts)
    assert paths.target_install == resolved / "Programs" / "ColabOne"
    assert paths.target_config == paths.local_appdata / "ColabOne"
    assert paths.start_menu == resolved.joinpath(*expected_start_menu_parts)
    assert paths.desktop == resolved / "Desktop"
    assert paths.target_exe == paths.target_install / expected_exe_name
    assert paths.cache_dir == paths.target_config / "cache"


def test_install_paths_non_sandbox_across_platforms(tmp_path, monkeypatch):
    fake_home = tmp_path / "home"
    fake_roaming = fake_home / "AppData" / "Roaming"
    monkeypatch.setattr(Path, "home", classmethod(lambda cls: fake_home))
    monkeypatch.delenv("LOCALAPPDATA", raising=False)
    monkeypatch.setenv("APPDATA", str(fake_roaming))
    monkeypatch.setattr(
        runtime_bundle, "desktop_folder", lambda: fake_home / "Desktop"
    )

    monkeypatch.setattr(sys, "platform", "darwin")
    mac = runtime_bundle.ColabOneInstallPaths()
    assert mac.is_sandbox is False
    assert mac.local_appdata == fake_home / "Library" / "Application Support"
    assert mac.target_install == mac.local_appdata / "Programs" / "ColabOne"
    assert mac.target_config == fake_home / "Library" / "Application Support" / "ColabOne"
    assert mac.start_menu == fake_home / "Applications"
    assert mac.desktop == fake_home / "Desktop"
    assert mac.target_exe == mac.target_install / "ColabOne.app"

    monkeypatch.setattr(sys, "platform", "win32")
    win = runtime_bundle.ColabOneInstallPaths()
    assert win.is_sandbox is False
    assert win.local_appdata == fake_home / "AppData" / "Local"
    assert win.target_install == win.local_appdata / "Programs" / "ColabOne"
    assert win.target_config == fake_home / "AppData" / "Local" / "ColabOne"
    assert win.start_menu == (
        fake_roaming / "Microsoft" / "Windows" / "Start Menu" / "Programs"
    )
    assert win.desktop == fake_home / "Desktop"
    assert win.target_exe == win.target_install / "ColabOne.exe"

    monkeypatch.setattr(sys, "platform", "linux")
    lin = runtime_bundle.ColabOneInstallPaths()
    assert lin.is_sandbox is False
    assert lin.local_appdata == fake_home / ".local" / "share"
    assert lin.target_install == lin.local_appdata / "Programs" / "ColabOne"
    assert lin.target_config == fake_home / ".local" / "share" / "ColabOne"
    assert lin.start_menu == fake_home / ".local" / "share" / "applications"
    assert lin.desktop == fake_home / "Desktop"
    assert lin.target_exe == lin.target_install / "colabone"
    assert lin.cache_dir == lin.target_config / "cache"

    # LOCALAPPDATA override on Linux stays consistent with Go DefaultConfigDir().
    custom_local = tmp_path / "custom-local"
    monkeypatch.setenv("LOCALAPPDATA", str(custom_local))
    lin_override = runtime_bundle.ColabOneInstallPaths()
    assert lin_override.target_config == custom_local / "ColabOne"
    assert lin_override.target_exe == custom_local / "Programs" / "ColabOne" / "colabone"


@pytest.mark.parametrize(
    ("platform_name", "goos", "goarch", "expected_app_name"),
    [
        ("darwin", "darwin", "arm64", "ColabOne.app"),
        ("win32", "windows", "amd64", "ColabOne.exe"),
        ("linux", "linux", "amd64", "colabone"),
    ],
)
def test_install_stages_pure_go_binary_and_removes_legacy_dirs(
    tmp_path, monkeypatch, platform_name, goos, goarch, expected_app_name
):
    monkeypatch.setattr(sys, "platform", platform_name)
    monkeypatch.setattr(install_mod, "_host_goarch", lambda: goarch)

    fake_root = tmp_path / "repo"
    (fake_root / "third_party").mkdir(parents=True)
    (fake_root / "third_party/rclone.LICENSE").write_text("MIT\n", encoding="utf-8")
    for name in ("README.md", "LICENSE", "NOTICE"):
        (fake_root / name).write_text(name + "\n", encoding="utf-8")

    dist_dir = fake_root / "console/dist" / f"{goos}-{goarch}"
    dist_dir.mkdir(parents=True)
    if goos == "darwin":
        built = dist_dir / "ColabOne.app"
        (built / "Contents/MacOS").mkdir(parents=True)
        (built / "Contents/MacOS/ColabOne").write_bytes(b"macho-stub")
    elif goos == "windows":
        built = dist_dir / "ColabOne.exe"
        built.write_bytes(b"pe-stub")
        (dist_dir / "colabone-setup-0.1.3-amd64.exe").write_bytes(b"nsis-stub")
    else:
        built = dist_dir / "colabone"
        built.write_bytes(b"elf-stub")
        (dist_dir / "colabone-0.1.3-linux-amd64.tar.gz").write_bytes(b"tar-stub")
        (dist_dir / "PKGBUILD").write_text("pkgname=colabone\n", encoding="utf-8")

    sandbox = tmp_path / "sandbox"
    pre_target = sandbox / "Programs" / "ColabOne"
    for legacy in runtime_bundle.LEGACY_STAGE_DIRS:
        (pre_target / legacy).mkdir(parents=True, exist_ok=True)
        (pre_target / legacy / "old.txt").write_text("stale", encoding="utf-8")

    events = []

    def fake_sync(root=fake_root, embed_dir=None):
        events.append(("sync", Path(root)))
        return 10

    def fake_run(args, cwd=fake_root, timeout=300, **kwargs):
        events.append(("run", list(args), Path(cwd)))

    monkeypatch.setattr(install_mod, "ROOT", fake_root)
    monkeypatch.setattr(install_mod, "sync_service_bundle", fake_sync)
    monkeypatch.setattr(install_mod, "run", fake_run)

    target = install_mod.install(str(sandbox), check_only=False)
    assert target == pre_target.resolve()
    assert events[0] == ("sync", fake_root)
    assert events[1][0] == "run"
    assert events[1][1][:6] == [
        "go",
        "tool",
        "mygo",
        "build",
        "-platform",
        f"{goos}/{goarch}",
    ]

    expected_exec = runtime_bundle.app_executable(target / expected_app_name)
    assert events[2] == ("run", [str(expected_exec), "--self-test"], target)

    staged_names = sorted(p.name for p in target.iterdir())
    assert staged_names == sorted(
        [expected_app_name, "LICENSE", "NOTICE", "README.md", "third_party"]
    )
    package_mod.audit(target)


def test_package_stages_mygo_updater_artifacts_and_mygo_files(tmp_path, monkeypatch):
    monkeypatch.setattr(sys, "platform", "win32")
    monkeypatch.setattr(install_mod, "_host_goarch", lambda: "amd64")

    fake_root = tmp_path / "repo"
    (fake_root / "third_party").mkdir(parents=True)
    (fake_root / "third_party/rclone.LICENSE").write_text("MIT\n", encoding="utf-8")
    for name in ("README.md", "LICENSE", "NOTICE"):
        (fake_root / name).write_text(name + "\n", encoding="utf-8")
    (fake_root / "console").mkdir(parents=True)
    (fake_root / "console/mygo.json").write_text(
        json.dumps({"name": "ColabOne", "version": "0.1.7"}), encoding="utf-8"
    )

    dist_dir = fake_root / "console/dist/windows-amd64"
    dist_dir.mkdir(parents=True)
    (dist_dir / "ColabOne.exe").write_bytes(b"pe-stub")
    (dist_dir / ".mygo-files").write_text("ColabOne.exe\nLICENSE\nNOTICE\nREADME.md\nthird_party\n", encoding="utf-8")
    (dist_dir / "colabone-0.1.7-windows-amd64.tar.gz").write_bytes(b"tar-stub")
    (dist_dir / "colabone-0.1.6-to-0.1.7-windows-amd64.delta").write_bytes(b"delta-stub")
    (dist_dir / "update-windows-amd64.json").write_text(
        json.dumps({"version": "0.1.7", "url": "https://example.invalid/a.tar.gz", "size": 8, "signature": "sig"}),
        encoding="utf-8",
    )

    monkeypatch.setattr(install_mod, "ROOT", fake_root)
    monkeypatch.setattr(install_mod, "sync_service_bundle", lambda root=fake_root, embed_dir=None: 10)
    monkeypatch.setattr(install_mod, "run", lambda args, cwd=fake_root, timeout=300, **kw: None)

    out_dir = tmp_path / "out"
    package_mod.main(["--out", str(out_dir)])

    zip_path = out_dir / "ColabOne-0.1.7-windows-x64.zip"
    assert zip_path.is_file()
    with zipfile.ZipFile(zip_path) as zf:
        assert "ColabOne/.mygo-files" in set(zf.namelist())
    assert (out_dir / "colabone-0.1.7-windows-amd64.tar.gz").is_file()
    assert (out_dir / "colabone-0.1.6-to-0.1.7-windows-amd64.delta").is_file()
    assert (out_dir / "update-windows-amd64.json").is_file()


def test_check_runs_pure_go_pipeline_without_bridge(monkeypatch):
    events = []
    committed_bridge = install_mod.ROOT / "frontend" / "src" / "colabOneBridge.ts"
    bridge_bytes = committed_bridge.read_bytes()

    def fake_check_bundle(root=install_mod.ROOT, embed_dir=None):
        events.append(("check_bundle", Path(root)))
        return 27

    def fake_run(args, cwd=install_mod.ROOT, timeout=300, **kwargs):
        events.append(("run", list(args), Path(cwd)))
        if args[:4] == ["go", "tool", "mygo", "generate"] and "-o" in args:
            out_path = Path(args[args.index("-o") + 1])
            out_path.write_bytes(bridge_bytes)

    monkeypatch.setattr(install_mod, "check_service_bundle", fake_check_bundle)
    monkeypatch.setattr(install_mod, "run", fake_run)

    install_mod.check()

    assert events[0][0] == "run"
    compileall_args = events[0][1]
    assert "compileall" in compileall_args
    assert "bridge" not in compileall_args
    assert {"server", "common", "tools"}.issubset(set(compileall_args))

    assert events[1] == ("check_bundle", install_mod.ROOT)
    assert events[2][1][:4] == ["go", "tool", "mygo", "generate"]
    assert "-o" in events[2][1]
    assert events[-1] == ("run", ["go", "run", ".", "--self-test"], install_mod.ROOT / "console")
    for kind, *rest in events:
        if kind == "run":
            assert not any("bridge" in str(arg) for arg in rest[0])


def test_verify_mygo_bindings_fails_when_output_missing_or_drifted(tmp_path, monkeypatch):
    fake_root = tmp_path / "repo"
    committed = fake_root / "frontend" / "src" / "colabOneBridge.ts"
    committed.parent.mkdir(parents=True)
    committed.write_text("// committed\n", encoding="utf-8")
    (fake_root / "console").mkdir()
    monkeypatch.setattr(install_mod, "ROOT", fake_root)

    # Case 1: generator exits 0 without writing the output file -> must fail loudly.
    monkeypatch.setattr(install_mod, "run", lambda args, cwd=fake_root, **kw: None)
    with pytest.raises(RuntimeError, match="did not produce output file"):
        install_mod._verify_mygo_bindings_up_to_date()

    # Case 2: generator writes drifted content -> must fail loudly without overwriting committed file.
    def fake_run_drift(args, cwd=fake_root, **kw):
        out_path = Path(args[args.index("-o") + 1])
        out_path.write_text("// drifted\n", encoding="utf-8")

    monkeypatch.setattr(install_mod, "run", fake_run_drift)
    with pytest.raises(RuntimeError, match="out of date"):
        install_mod._verify_mygo_bindings_up_to_date()
    assert committed.read_text(encoding="utf-8") == "// committed\n"


def test_check_service_bundle_detects_drift_without_mutating(tmp_path):
    from tools.sync_service_bundle import check_service_bundle, sync_service_bundle

    (tmp_path / "server").mkdir()
    (tmp_path / "common").mkdir()
    (tmp_path / "catalog").mkdir()
    (tmp_path / "server" / "app.py").write_text("x = 1\n", encoding="utf-8")
    embed = tmp_path / "embedded"

    assert sync_service_bundle(tmp_path, embed) == 1
    assert check_service_bundle(tmp_path, embed) == 1

    # Drift in source must fail read-only check and NOT overwrite embedded/.
    (tmp_path / "server" / "app.py").write_text("x = 2\n", encoding="utf-8")
    with pytest.raises(RuntimeError, match="mismatched=server/app.py"):
        check_service_bundle(tmp_path, embed)
    assert (embed / "server" / "app.py").read_text(encoding="utf-8") == "x = 1\n"


def test_live_acceptance_module_has_no_bridge_dependency_and_validates_budget(monkeypatch):
    assert live_acceptance.SMOKE_MODEL["id"] == "qwen25-05b"
    monkeypatch.delenv("COLABONE_ACCEPTANCE_URL", raising=False)
    monkeypatch.delenv("COLABONE_ACCEPTANCE_TOKEN", raising=False)

    monkeypatch.setattr(sys, "argv", ["live_acceptance.py", "--max-cu", "6"])
    with pytest.raises(SystemExit, match="within 5 CU"):
        live_acceptance.main()

    monkeypatch.setattr(sys, "argv", ["live_acceptance.py", "--max-cu", "5"])
    with pytest.raises(SystemExit, match="pure Go"):
        live_acceptance.main()
