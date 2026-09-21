"""Build independent core and bundle archives from a clean, public checkout."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import tarfile
import tempfile
import zipfile


ROOT = Path(__file__).resolve().parent.parent
TARGETS = ("windows/amd64", "linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64")


def run(*args):
    return subprocess.check_output(args, cwd=ROOT, text=True).strip()


def version():
    text = (ROOT / "internal/core/identity.go").read_text(encoding="utf-8")
    match = re.search(r'\bVersion\s*=\s*"([^"]+)"', text)
    if not match:
        raise RuntimeError("Core version is not declared")
    return match.group(1)


def write_archive(path, files):
    if any(source.is_symlink() or not source.is_file() for source, _ in files):
        raise RuntimeError("Archive inputs must be ordinary files, not links")
    names = [name for _, name in files]
    if len(names) != len(set(names)) or any(
        name.startswith("/") or ".." in Path(name).parts for name in names
    ):
        raise RuntimeError("Unsafe or duplicate archive entry")
    if path.suffix == ".zip":
        with zipfile.ZipFile(path, "w", zipfile.ZIP_DEFLATED) as archive:
            for source, name in files:
                archive.write(source, name)
    else:
        with tarfile.open(path, "w:gz") as archive:
            for source, name in files:
                archive.add(source, arcname=name, recursive=False)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--out", type=Path, required=True)
    parser.add_argument("--targets", nargs="+", choices=TARGETS, default=list(TARGETS))
    args = parser.parse_args()
    if run("git", "status", "--porcelain"):
        raise SystemExit("Packaging requires a clean checkout")
    if not args.out.is_absolute():
        raise SystemExit("--out must be absolute")
    args.out.mkdir(parents=True, exist_ok=True)
    if any(args.out.iterdir()):
        raise SystemExit("Output directory must be empty")
    release = version()
    archives = []
    core_digests = {}
    with tempfile.TemporaryDirectory(prefix="midden-release-") as temporary:
        temporary = Path(temporary)
        for target in args.targets:
            system, arch = target.split("/")
            binary = temporary / ("midden.exe" if system == "windows" else "midden")
            env = dict(os.environ, GOOS=system, GOARCH=arch, CGO_ENABLED="0")
            subprocess.run(
                ["go", "build", "-trimpath", "-ldflags=-s -w", "-o", str(binary), "./cmd/midden"],
                cwd=ROOT, env=env, check=True,
            )
            binary.chmod(0o755)
            core_digests[target] = hashlib.sha256(binary.read_bytes()).hexdigest()
            suffix = ".zip" if system == "windows" else ".tar.gz"
            archive = args.out / f"midden-core_{release}_{system}_{arch}{suffix}"
            write_archive(archive, [
                (binary, binary.name),
                (ROOT / "LICENSE", "LICENSE"),
                (ROOT / "docs/CORE.md", "CORE.md"),
            ])
            archives.append(archive)
        files = [(ROOT / "LICENSE", "LICENSE")]
        for name in ("install.ps1", "install.sh"):
            files.append((ROOT / name, name))
        for folder in ("bundles", "installer"):
            for path in sorted((ROOT / folder).rglob("*")):
                if not path.is_file() or "__pycache__" in path.parts:
                    continue
                if path.suffix not in {".md", ".py", ".ps1", ".sh", ".json", ".yaml", ".yml", ".css", ".svg", ".png", ".lua"}:
                    raise RuntimeError(f"Unexpected bundle file type: {path.relative_to(ROOT)}")
                files.append((path, path.relative_to(ROOT).as_posix()))
        bundle = args.out / f"midden-bundle_{release}.zip"
        write_archive(bundle, files)
        archives.append(bundle)
    manifest = args.out / "build-manifest.json"
    manifest.write_text(json.dumps({
        "version": release, "commit": run("git", "rev-parse", "HEAD"),
        "go": run("go", "version"), "targets": args.targets,
        "products": ["core", "bundle"],
        "core_binaries": core_digests,
        "archives": [path.name for path in archives],
    }, indent=2) + "\n", encoding="utf-8")
    archives.append(manifest)
    (args.out / "SHA256SUMS").write_text(
        "".join(hashlib.sha256(path.read_bytes()).hexdigest() + "  " + path.name + "\n"
                for path in sorted(archives)),
        encoding="utf-8",
    )
    print(f"Built core and bundle artifacts for {', '.join(args.targets)}")


if __name__ == "__main__":
    main()
