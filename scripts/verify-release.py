"""Verify release provenance, exact archive inventories and hashes; optionally run native products."""
import argparse
import hashlib
import http.cookiejar
import json
import os
from pathlib import Path
import platform
import queue
import re
import signal
import subprocess
import sys
import tarfile
import tempfile
import threading
import time
import urllib.error
import urllib.request
import zipfile

sys.dont_write_bytecode = True
from release_contract import (COMMIT, COMPA_NOTICE, COMPA_VERSION, INSTALLERS, PANDOC_NOTICE, PRODUCTS, TARGETS, XIBODEV_PREFIX,
                              app_inputs, archive_name, bundle_inputs, compa_members, git_blob_bytes, pandoc_records,
                              parse_release_tsv, release_version, safe_member, stray_xibodev_lines, validate_member_set)


ROOT = Path(__file__).resolve().parent.parent


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


def verify_kernel_notices(kernel, notice, temporary):
    """Every module compiled into compa-kernel, after replacements, is named in the app's notices."""
    path = Path(temporary) / "compa-kernel-check"
    path.write_bytes(kernel)
    info = subprocess.check_output(["go", "version", "-m", str(path)], text=True)
    compiled = []
    for line in info.splitlines()[1:]:
        fields = line.strip().split("\t")
        if fields[0] == "dep" and len(fields) >= 3:
            compiled.append((fields[1], fields[2]))
        elif fields[0] == "=>" and len(fields) >= 3 and compiled:
            compiled[-1] = (fields[1], fields[2])
    assert compiled, "compa-kernel records no compiled modules"
    for module, version in compiled:
        named = (f"https://{'/'.join(module.split('/')[:3])}" if module.startswith(XIBODEV_PREFIX) else f"{module} {version}")
        assert named.encode("utf-8") in notice, "App notices omit a module compiled into compa-kernel: " + module


def process_alive(pid):
    if platform.system() == "Windows":
        output = subprocess.run(["tasklist", "/FI", f"PID eq {pid}", "/NH"], capture_output=True, text=True).stdout
        return str(pid) in output
    try:
        os.kill(pid, 0)
    except ProcessLookupError:
        return False
    except PermissionError:
        return True
    return True


def ui_smoke(binary, version, stage, env):
    output = subprocess.check_output([str(binary), "--version"], env=env, text=True, timeout=15)
    assert output.strip() == "midden-ui " + version, "App version mismatch"
    data = stage / "user-data"
    assert not data.exists(), "passive app version probe wrote user state"
    windows = platform.system() == "Windows"
    process = subprocess.Popen([str(binary), "--no-open", "--listen", "127.0.0.1:0"],
                               cwd=stage, env=env, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                               creationflags=subprocess.CREATE_NEW_PROCESS_GROUP if windows else 0)
    lines = queue.Queue()
    transcript = []

    def read():
        for line in process.stdout:
            lines.put(line)

    reader = threading.Thread(target=read, daemon=True)
    reader.start()
    kernel_pid = None
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
            match = re.search(r"Midden App \S+: (http://127\.0\.0\.1:[0-9]+)(/\?key=[0-9a-f]{32})\s*$", line)
            if match:
                address, launch = match.group(1), match.group(1) + match.group(2)
                break
        assert address, "App did not start: " + "".join(transcript)
        try:
            urllib.request.urlopen(address + "/api/status", timeout=10)
            raise AssertionError("the App answered without its launch key")
        except urllib.error.HTTPError as error:
            assert error.code == 401, "unexpected answer without the launch key: " + str(error.code)
        browser = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
        with browser.open(launch, timeout=10) as response:
            assert b"<title>Midden</title>" in response.read(), "the launch address did not open the app shell"
        deadline = time.monotonic() + 60
        while True:
            with browser.open(address + "/api/status", timeout=10) as response:
                status = json.load(response)
            if status["kernel"]["state"] != "starting" or time.monotonic() > deadline:
                break
            time.sleep(0.5)
        assert status["uiVersion"] == version and status["coreVersion"] == "midden " + version
        assert status["kernelName"] == "Compa" and status["kernelVersion"] == COMPA_VERSION
        assert status["kernel"]["state"] == "ready", "compa-kernel did not start: " + json.dumps(status["kernel"])
        assert not status["model"]["configured"] and len(status["skills"]) == 4
        workspace = Path(status["workspace"]).resolve()
        assert binary.parent.resolve() not in workspace.parents, "the person's files are inside installed binaries"
        kernels = list(data.glob("*/kernel/.compa.pid"))
        assert len(kernels) == 1, "compa-kernel wrote no pid file in the App's kernel folder"
        kernel_pid = json.loads(kernels[0].read_text(encoding="utf-8"))["pid"]
        with browser.open(address + "/app.js", timeout=10) as response:
            modules = re.findall(rb'from "(/js/[a-z0-9-]+\.js)"', response.read())
        for module in modules:
            with browser.open(address + module.decode("ascii"), timeout=10) as response:
                assert response.headers.get_content_type() == "text/javascript", module
    finally:
        process.send_signal(signal.CTRL_BREAK_EVENT) if windows else process.terminate()
        try:
            process.wait(timeout=20)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait(timeout=5)
        process.stdout.close()
        reader.join(timeout=2)
    if kernel_pid is not None:
        deadline = time.monotonic() + 15
        while process_alive(kernel_pid) and time.monotonic() < deadline:
            time.sleep(0.3)
        assert not process_alive(kernel_pid), "compa-kernel outlived the App"
    assert not (stage / "home" / ".compa").exists(), "the App touched the person's own Compa home"


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
    assert manifest["products"] == list(PRODUCTS)
    targets = manifest["targets"]
    assert targets and len(set(targets)) == len(targets) and all(t in TARGETS for t in targets)
    expected_archives = {archive_name(p, manifest["version"], t) for p in ("core", "app") for t in targets}
    expected_archives.add(archive_name("bundle", manifest["version"], "universal"))
    assert set(manifest["archives"]) == expected_archives and len(manifest["archives"]) == len(expected_archives)
    assert manifest["installers"] == list(INSTALLERS)
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
    pins = {(record[2], record[0]): [] for record in pandoc_records(targets)}
    for record in pandoc_records(targets):
        pins[(record[2], record[0])].append(record[3:])
    for target in targets:
        assert [tsv["fetch"][target]] == pins[(target, "fetch")], "Pandoc pin differs from the reviewed contract: " + target
        assert sorted(tsv["take"][target].items()) == sorted(pins[(target, "take")]), "Pandoc members differ: " + target
    assert set(tsv["fetch"]) == set(targets)
    bundle = bundle_inputs(ROOT, args.commit)
    static_app = app_inputs(ROOT, args.commit)
    for key, name in tsv["archives"].items():
        product, target = key
        actual = archive_inventory(root / name)
        assert actual == tsv["files"][key], "archive bytes differ from manifest: " + name
        extension = ".exe" if target.startswith("windows/") else ""
        core, app = "midden" + extension, "midden-ui" + extension
        if product == "core":
            assert set(actual) == {core, "LICENSE", "THIRD_PARTY_NOTICES.txt"}
            assert actual[core] == manifest["core_binaries"][target]
            verify_static_source(ROOT, args.commit, [(ROOT / "LICENSE", "LICENSE")], actual)
            notice = archive_bytes(root / name, "THIRD_PARTY_NOTICES.txt")
            assert b"github.com/xibodev/compa" not in notice and not stray_xibodev_lines(notice), name
        elif product == "bundle":
            assert set(actual) == {name for _, name in bundle}
            verify_static_source(ROOT, args.commit, bundle, actual)
        else:
            compa = compa_members(target)
            assert set(actual) == ({name for _, name in static_app} | {app, "app/THIRD_PARTY_NOTICES.txt"} |
                                   {name for _, name, _ in compa}), "app inventory differs: " + name
            assert actual[app] == manifest["app_binaries"][target]
            for _, member, expected in compa:
                assert actual[member] == expected, "Compa file differs from the pinned release: " + member
            verify_static_source(ROOT, args.commit, static_app, actual)
            notice = archive_bytes(root / name, "app/THIRD_PARTY_NOTICES.txt")
            assert not stray_xibodev_lines(notice), "App notices may name xibodev components only in one line each: " + name
            assert notice.endswith((COMPA_NOTICE + PANDOC_NOTICE).encode("utf-8")), \
                "App notices must name the shipped Compa and the Pandoc the installer fetches: " + name
            with tempfile.TemporaryDirectory(prefix="midden-kernel-") as temporary:
                verify_kernel_notices(archive_bytes(root / name, compa[0][1]), notice, temporary)
    for name in manifest["installers"]:
        assert (root / name).read_bytes() == git_blob_bytes(ROOT, args.commit, ROOT / name)
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
            # The installer combines the three products in one folder; so does this check.
            programs = stage / "programs"
            for product in PRODUCTS:
                extract_verified(root / archive_name(product, manifest["version"],
                                                     "universal" if product == "bundle" else target), programs)
            binary = programs / ("midden.exe" if system == "windows" else "midden")
            binary.chmod(0o755)
            assert subprocess.check_output([str(binary), "version"], env=env, text=True).strip() == "midden " + manifest["version"]
            subprocess.run([str(binary), "help"], env=env, check=True, stdout=subprocess.DEVNULL)
            assert not (stage / "core-state").exists(), "passive core probe wrote state"
            app = programs / ("midden-ui.exe" if system == "windows" else "midden-ui")
            app.chmod(0o755)
            (programs / "app" / ("compa-kernel.exe" if system == "windows" else "compa-kernel")).chmod(0o755)
            ui_smoke(app, manifest["version"], stage, env)
    print("PASS: exact release source, all product/member hashes, license inventories, Pandoc and Compa pins" +
          (", native core and app startup with its kernel" if args.smoke else ""))


if __name__ == "__main__":
    main()
