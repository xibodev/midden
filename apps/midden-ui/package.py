"""Build an isolated testing directory laid out as the installer lays out the app: core, app, kernel and skills."""
import argparse
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import platform
import shutil
import subprocess
import sys
import tempfile


ROOT = Path(__file__).resolve().parents[2]
APP = Path(__file__).resolve().parent
sys.dont_write_bytecode = True
sys.path.insert(0, str(ROOT / "scripts"))
from release_contract import COMPA_VERSION, SKILL_FOLDERS, take_compa  # noqa: E402


def digest(path):
    result = hashlib.sha256()
    with path.open("rb") as source:
        for block in iter(lambda: source.read(1024 * 1024), b""):
            result.update(block)
    return result.hexdigest()


def native_target():
    system = {"Windows": "windows", "Linux": "linux", "Darwin": "darwin"}[platform.system()]
    return system + "/" + ("arm64" if platform.machine().lower() in {"arm64", "aarch64"} else "amd64")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--out", type=Path, required=True)
    parser.add_argument("--downloads", type=Path, help="Folder that keeps Compa's release archives")
    args = parser.parse_args()
    out = args.out.resolve()
    if out == ROOT or ROOT in out.parents:
        raise SystemExit("Testing package must be outside the checkout")
    if out.exists():
        raise SystemExit("Choose a new output directory; existing work is never replaced")
    out.mkdir(parents=True)
    extension = ".exe" if os.name == "nt" else ""
    env = dict(os.environ, CGO_ENABLED="0", GOWORK="off")
    for cwd, name, package in ((ROOT, "midden", "./cmd/midden"), (APP, "midden-ui", ".")):
        subprocess.run(["go", "build", "-trimpath", "-o", str(out / (name + extension)), package],
                       cwd=cwd, env=env, check=True)
    tracked = subprocess.check_output(["git", "ls-files", "-z", "--", "bundles"], cwd=ROOT)
    for value in tracked.split(b"\0"):
        if not value:
            continue
        name = PurePosixPath(value.decode("utf-8"))
        if ".." in name.parts or name.is_absolute():
            raise RuntimeError("Unsafe tracked bundle path")
        source = ROOT.joinpath(*name.parts)
        if source.is_symlink() or not source.is_file():
            raise RuntimeError("Bundle input is not a regular file")
        if len(name.parts) < 3 or name.parts[1] not in SKILL_FOLDERS:
            raise RuntimeError("Unexpected tracked bundle path: " + name.as_posix())
        target = out.joinpath("skills", SKILL_FOLDERS[name.parts[1]], *name.parts[2:])
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(source, target)
    for source, name in ((ROOT / "LICENSE", "LICENSE"), (APP / "start.ps1", "start.ps1"), (APP / "start.sh", "start.sh")):
        shutil.copyfile(source, out / name)
    with tempfile.TemporaryDirectory(prefix="midden-compa-") as temporary:
        downloads = args.downloads or Path(temporary)
        downloads.mkdir(parents=True, exist_ok=True)
        for path, name in take_compa(native_target(), downloads, Path(temporary) / "taken"):
            destination = out.joinpath(*PurePosixPath(name).parts)
            destination.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(path, destination)
    manifest = {
        "kind": "midden-ui-testing",
        "kernel": f"compa-kernel {COMPA_VERSION}",
        "kernel_status": "pinned upstream release, checked against its SHA-256",
        "source_commit": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip(),
        "working_tree_modified": bool(subprocess.check_output(["git", "status", "--porcelain"], cwd=ROOT)),
        "files": {p.relative_to(out).as_posix(): digest(p) for p in sorted(out.rglob("*")) if p.is_file()},
    }
    (out / "package-manifest.json").write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8")
    print(out)


if __name__ == "__main__":
    main()
