"""Stage the public page only with verified installers from its matching stable release."""
import argparse
import hashlib
import json
from pathlib import Path
import re
import shutil
import subprocess
import sys

sys.dont_write_bytecode = True


def stage_site(source, release, output, tag, commit):
    if not re.fullmatch(r"v(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)", tag):
        raise ValueError("Only a stable release may replace the public installer page")
    if not re.fullmatch(r"[0-9a-f]{40}(?:[0-9a-f]{24})?", commit):
        raise ValueError("Invalid source commit")
    checksums = {}
    for line in (release / "SHA256SUMS").read_text(encoding="utf-8").splitlines():
        match = re.fullmatch(r"([0-9a-f]{64})  ([A-Za-z0-9._-]+)", line)
        if not match or match[2] in checksums:
            raise ValueError("Invalid release checksum inventory")
        checksums[match[2]] = match[1]
    for name in ("install.ps1", "install.sh", "build-manifest.json"):
        path = release / name
        if not path.is_file() or path.is_symlink() or hashlib.sha256(path.read_bytes()).hexdigest() != checksums.get(name):
            raise ValueError("Release download is missing or corrupt: " + name)
    manifest = json.loads((release / "build-manifest.json").read_text(encoding="utf-8"))
    if manifest.get("version") != tag[1:] or manifest.get("commit") != commit:
        raise ValueError("Page source and published release do not match")
    if output.exists():
        raise ValueError("Choose a new site staging directory")
    inputs = [(source / "docs" / name, name) for name in ("index.html", "site.css", "favicon.svg")]
    inputs += [(release / name, name) for name in ("install.ps1", "install.sh", "SHA256SUMS", "build-manifest.json")]
    for path, _ in inputs:
        if not path.is_file() or path.is_symlink():
            raise ValueError("Site input is not a regular file")
    output.mkdir(parents=True)
    for path, name in inputs:
        shutil.copyfile(path, output / name)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--release-dir", type=Path, required=True)
    parser.add_argument("--out", type=Path, required=True)
    parser.add_argument("--tag", required=True)
    args = parser.parse_args()
    root = Path(__file__).resolve().parents[1]
    commit = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip()
    stage_site(root, args.release_dir, args.out, args.tag, commit)
    print("Staged page and verified stable-release installers; no source commit was created")


if __name__ == "__main__":
    main()
