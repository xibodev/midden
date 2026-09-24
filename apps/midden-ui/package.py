"""Build an isolated testing directory for the new UI host, core and canonical bundle."""
import argparse
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import shutil
import subprocess


ROOT = Path(__file__).resolve().parents[2]
APP = Path(__file__).resolve().parent


def digest(path):
    result = hashlib.sha256()
    with path.open("rb") as source:
        for block in iter(lambda: source.read(1024 * 1024), b""):
            result.update(block)
    return result.hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--out", type=Path, required=True)
    args = parser.parse_args()
    out = args.out.resolve()
    if out == ROOT or ROOT in out.parents:
        raise SystemExit("Testing package must be outside the checkout")
    if out.exists():
        raise SystemExit("Choose a new output directory; existing work is never replaced")
    out.mkdir(parents=True)
    extension = ".exe" if os.name == "nt" else ""
    env = dict(os.environ, CGO_ENABLED="0")
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
        target = out.joinpath(*name.parts)
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(source, target)
    for source, name in ((ROOT / "LICENSE", "LICENSE"), (APP / "README.md", "README.md"),
                         (APP / "start.ps1", "start.ps1")):
        shutil.copyfile(source, out / name)
    manifest = {
        "kind": "midden-ui-testing",
        "kernel": "v1.0.1-0.20260922143928-0b024a53c4f6",
        "kernel_status": "unreleased upstream candidate",
        "source_commit": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip(),
        "working_tree_modified": bool(subprocess.check_output(["git", "status", "--porcelain"], cwd=ROOT)),
        "files": {p.relative_to(out).as_posix(): digest(p) for p in sorted(out.rglob("*")) if p.is_file()},
    }
    (out / "package-manifest.json").write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8")
    print(out)


if __name__ == "__main__":
    main()
