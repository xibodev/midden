"""Verify release provenance, exact archive inventories and hashes; optionally run native products."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import queue
import re
import subprocess
import sys
import tarfile
import tempfile
import threading
import time
import urllib.request
import zipfile

sys.dont_write_bytecode = True
from release_contract import COMMIT, TARGETS, archive_name, bundle_inputs, git_blob_bytes, parse_release_tsv, release_version, safe_member, stray_xibodev_lines, ui_inputs, validate_member_set


ROOT = Path(__file__).resolve().parent.parent
COMPA_NOTICE = "\nCompa \u2014 https://github.com/xibodev/compa \u2014 ".encode("utf-8")


def stream_digest(stream):
    result = hashlib.sha256()
    for block in iter(lambda: stream.read(1024 * 1024), b""):
        result.update(block)
    return result.hexdigest()


def digest(path):
    with path.open("rb") as stream:
        return stream_digest(stream)


def archive_inventory(path):
    hashes, folded, total = {}, set(), 0
    if path.stat().st_size > 256 << 20:
        raise ValueError("Compressed archive exceeds release limit")
    archive = zipfile.ZipFile(path) if path.suffix == ".zip" else tarfile.open(path)
    with archive:
        items = archive.infolist() if isinstance(archive, zipfile.ZipFile) else archive.getmembers()
        if not 0 < len(items) <= 4096:
            raise ValueError("Archive member count exceeds release limit")
        for item in items:
            if isinstance(archive, zipfile.ZipFile):
                name, size = item.filename, item.file_size
                mode = (item.external_attr >> 16) & 0o170000
                regular = not item.is_dir() and mode in (0, 0o100000) and not item.flag_bits & 1
                if item.orig_filename != name:
                    regular = False
            else:
                name, size, regular = item.name, item.size, item.isfile()
            safe_member(name)
            total += size
            if not regular or not 0 < size <= 256 << 20 or total > 256 << 20 or name.casefold() in folded:
                raise ValueError("Unsafe, duplicate or oversized archive member")
            folded.add(name.casefold())
            for parent in Path(name).parents:
                if str(parent) != "." and parent.as_posix().casefold() in folded:
                    raise ValueError("Archive file conflicts with a parent directory")
            stream = archive.open(item) if isinstance(archive, zipfile.ZipFile) else archive.extractfile(item)
            with stream:
                hashes[name] = stream_digest(stream)
    validate_member_set(hashes)
    return hashes


def archive_bytes(path, name):
    if path.suffix == ".zip":
        with zipfile.ZipFile(path) as archive:
            return archive.read(name)
    with tarfile.open(path) as archive:
        with archive.extractfile(name) as stream:
            return stream.read()


def extract_verified(path, destination):
    destination.mkdir(parents=True, exist_ok=True)
    if path.suffix == ".zip":
        with zipfile.ZipFile(path) as archive:
            archive.extractall(destination)
    else:
        with tarfile.open(path) as archive:
            archive.extractall(destination, filter="data")


def verify_static_source(root, commit, inputs, actual):
    for source, name in inputs:
        expected = hashlib.sha256(git_blob_bytes(root, commit, source)).hexdigest()
        if actual.get(name) != expected:
            raise ValueError("Static release member differs from the claimed source commit: " + name)


def ui_smoke(binary, version, stage, env):
    output = subprocess.check_output([str(binary), "--version"], env=env, text=True, timeout=15)
    assert output.strip() == "midden-ui " + version, "UI version mismatch"
    data = stage / "user-data"
    assert not data.exists(), "passive UI version probe wrote user state"
    process = subprocess.Popen([str(binary), "--no-open", "--listen", "127.0.0.1:0"],
                               cwd=stage, env=env, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    lines = queue.Queue()
    transcript = []

    def read():
        for line in process.stdout:
            lines.put(line)

    reader = threading.Thread(target=read, daemon=True)
    reader.start()
    try:
        address = None
        deadline = time.monotonic() + 30
        while time.monotonic() < deadline:
            try:
                line = lines.get(timeout=0.2)
            except queue.Empty:
                if process.poll() is not None:
                    break
                continue
            transcript.append(line)
            match = re.search(r"Midden UI [^:]+: (http://127\.0\.0\.1:[0-9]+)", line)
            if match:
                address = match.group(1)
                break
        assert address, "UI did not start: " + "".join(transcript)
        with urllib.request.urlopen(address + "/api/status", timeout=10) as response:
            status = json.load(response)
        assert status["uiVersion"] == version and status["coreVersion"] == "midden " + version
        assert status["kernelName"] == "Compa" and status["kernelVersion"] == "v1.0.0"
        assert not status["model"]["configured"] and len(status["bundles"]) == 4
        workspace = Path(status["workspace"]).resolve()
        assert binary.parent.resolve() not in workspace.parents, "workspace is inside installed binaries"
        with urllib.request.urlopen(address + "/", timeout=10) as response:
            assert b"<title>Midden</title>" in response.read(), "UI did not serve its app shell"
        with urllib.request.urlopen(address + "/app.js", timeout=10) as response:
            modules = re.findall(rb'from "(/js/[a-z0-9-]+\.js)"', response.read())
        for module in modules:
            with urllib.request.urlopen(address + module.decode("ascii"), timeout=10) as response:
                assert response.headers.get_content_type() == "text/javascript", module
    finally:
        process.terminate()
        try:
            process.wait(timeout=10)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait(timeout=5)
        process.stdout.close()
        reader.join(timeout=2)


def main():
    if not __debug__:
        raise SystemExit("Release verification must not run with Python optimization enabled")
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--directory", type=Path, required=True)
    parser.add_argument("--commit", required=True)
    parser.add_argument("--tag")
    parser.add_argument("--smoke", action="store_true")
    args = parser.parse_args()
    if not COMMIT.fullmatch(args.commit):
        raise SystemExit("--commit must be a full source commit hash")
    root = args.directory
    manifest = json.loads((root / "build-manifest.json").read_text(encoding="utf-8"))
    release_version(manifest["version"])
    assert manifest["commit"] == args.commit, "source revision mismatch"
    if args.tag:
        assert args.tag == "v" + manifest["version"], "tag/version mismatch"
    assert manifest["products"] == ["core", "bundle", "ui"]
    targets = manifest["targets"]
    assert targets and len(set(targets)) == len(targets) and all(t in TARGETS for t in targets)
    expected_archives = {archive_name(p, manifest["version"], t) for p in ("core", "ui") for t in targets}
    expected_archives.add(archive_name("bundle", manifest["version"], "universal"))
    assert set(manifest["archives"]) == expected_archives and len(manifest["archives"]) == len(expected_archives)
    assert manifest["installers"] == ["install.ps1", "install.sh"]
    expected = expected_archives | set(manifest["installers"]) | {"build-manifest.json", "manifest.tsv"}
    checksums = {}
    for line in (root / "SHA256SUMS").read_text(encoding="utf-8").splitlines():
        value, name = line.split("  ", 1)
        safe_member(name)
        assert Path(name).name == name and name not in checksums and re.fullmatch(r"[0-9a-f]{64}", value)
        checksums[name] = value
    assert set(checksums) == expected
    assert {p.name for p in root.iterdir()} == expected | {"SHA256SUMS"}
    for name, value in checksums.items():
        assert digest(root / name) == value, name
    tsv = parse_release_tsv((root / "manifest.tsv").read_text(encoding="utf-8"))
    assert tsv["version"] == manifest["version"] and tsv["commit"] == args.commit
    assert set(tsv["archives"].values()) == expected_archives
    bundle_names = {name for _, name in bundle_inputs(ROOT, args.commit)}
    ui_names = {name for _, name in ui_inputs(ROOT, args.commit)}
    for key, name in tsv["archives"].items():
        product, target = key
        actual = archive_inventory(root / name)
        assert actual == tsv["files"][key], "archive bytes differ from manifest: " + name
        extension = ".exe" if target.startswith("windows/") else ""
        core, ui = "midden" + extension, "midden-ui" + extension
        if product == "core":
            assert set(actual) == {core, "LICENSE", "CORE.md", "THIRD_PARTY_NOTICES.txt"}
            assert actual[core] == manifest["core_binaries"][target]
            verify_static_source(ROOT, args.commit, [(ROOT / "LICENSE", "LICENSE"), (ROOT / "docs" / "CORE.md", "CORE.md")], actual)
            notice = archive_bytes(root / name, "THIRD_PARTY_NOTICES.txt")
            assert b"github.com/xibodev/compa" not in notice and not stray_xibodev_lines(notice), name
        elif product == "bundle":
            assert set(actual) == bundle_names
            verify_static_source(ROOT, args.commit, bundle_inputs(ROOT, args.commit), actual)
        else:
            assert set(actual) == ui_names | {core, ui, "THIRD_PARTY_NOTICES.txt", "package-manifest.json"}
            assert actual[core] == manifest["core_binaries"][target]
            assert actual[ui] == manifest["ui_binaries"][target]
            verify_static_source(ROOT, args.commit, ui_inputs(ROOT, args.commit), actual)
            package = json.loads(archive_bytes(root / name, "package-manifest.json"))
            assert package["kind"] == "midden-ui-release" and package["version"] == manifest["version"]
            assert package["platform"] == target and package["source_commit"] == args.commit
            assert package["kernel"] == "github.com/xibodev/compa v1.0.0"
            assert package["files"] == {k: v for k, v in actual.items() if k != "package-manifest.json"}
            notice = archive_bytes(root / name, "THIRD_PARTY_NOTICES.txt")
            assert COMPA_NOTICE in notice and not stray_xibodev_lines(notice), "UI notices must name Compa in one line: " + name
            core_notice = archive_bytes(root / archive_name("core", manifest["version"], target), "THIRD_PARTY_NOTICES.txt")
            assert core_notice in notice, "UI product omitted its bundled core's notices"
    for name in manifest["installers"]:
        assert (root / name).read_bytes() == git_blob_bytes(ROOT, args.commit, ROOT / "bootstrap" / name)
    if args.smoke:
        system = {"Windows": "windows", "Linux": "linux", "Darwin": "darwin"}[platform.system()]
        arch = "arm64" if platform.machine().lower() in {"arm64", "aarch64"} else "amd64"
        target = system + "/" + arch
        assert target in targets, "native target missing"
        with tempfile.TemporaryDirectory(prefix="midden-native-") as temporary:
            stage = Path(temporary).resolve()
            env = dict(os.environ, MIDDEN_HOME=str(stage / "core-state"), HOME=str(stage / "home"),
                       USERPROFILE=str(stage / "home"), LOCALAPPDATA=str(stage / "user-data"),
                       XDG_DATA_HOME=str(stage / "user-data"),
                       MIDDEN_CLAUDE_ROOT=str(stage / "sources" / "claude"),
                       MIDDEN_COPILOT_ROOT=str(stage / "sources" / "copilot"),
                       MIDDEN_OPENCODE_DB=str(stage / "sources" / "opencode.db"))
            core_root, ui_root, bundle_root = stage / "core", stage / "ui", stage / "bundle"
            for product, destination in (("core", core_root), ("ui", ui_root), ("bundle", bundle_root)):
                extract_verified(root / archive_name(product, manifest["version"], "universal" if product == "bundle" else target), destination)
            binary = core_root / ("midden.exe" if system == "windows" else "midden")
            binary.chmod(0o755)
            assert subprocess.check_output([str(binary), "version"], env=env, text=True).strip() == "midden " + manifest["version"]
            subprocess.run([str(binary), "help"], env=env, check=True, stdout=subprocess.DEVNULL)
            assert not (stage / "core-state").exists(), "passive core probe wrote state"
            ui = ui_root / ("midden-ui.exe" if system == "windows" else "midden-ui")
            ui.chmod(0o755)
            ui_smoke(ui, manifest["version"], stage, env)
            project = stage / "project"
            project.mkdir()
            installer = bundle_root / "installer" / "install.py"
            common = [sys.executable, "-B", str(installer), "--project", str(project)]
            subprocess.run(common + ["--distribution-dir", str(root)], env=env, check=True)
            subprocess.run(common + ["--verify"], env=env, check=True)
            subprocess.run(common + ["--uninstall"], env=env, check=True)
    print("PASS: exact release source, all product/member hashes, license inventories" +
          (", native core/UI startup and actual CLI-bundle installation" if args.smoke else ""))


if __name__ == "__main__":
    main()
