"""Stage the website: the page from this checkout beside the installers of a
stable release, which this checkout's history must contain."""
import argparse
import hashlib
import json
from pathlib import Path
import re
import shutil
import subprocess
import sys

sys.dont_write_bytecode = True

PAGE_FILES = ("index.html", "site.css", "favicon.svg")
RELEASE_FILES = ("install.ps1", "install.sh", "SHA256SUMS", "build-manifest.json")


def stage_site(release, page, output, tag, contains):
    """contains(commit) answers whether this checkout's history includes the release's commit."""
    if not re.fullmatch(r"v(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)", tag):
        raise ValueError("Only a stable release may replace the public installers")
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
    commit = manifest.get("commit")
    if (manifest.get("version") != tag[1:] or not isinstance(commit, str)
            or not re.fullmatch(r"[0-9a-f]{40}(?:[0-9a-f]{24})?", commit)):
        raise ValueError("The release manifest does not match the tag")
    if not contains(commit):
        raise ValueError("This checkout does not contain the release's source commit")
    if output.exists():
        raise ValueError("Choose a new site staging directory")
    inputs = [(release / name, name) for name in RELEASE_FILES] + [(page / name, name) for name in PAGE_FILES]
    for path, _ in inputs:
        if not path.is_file() or path.is_symlink():
            raise ValueError("Site input is not a regular file: " + path.name)
    index = (page / "index.html").read_text(encoding="utf-8")
    if "@TAG@" not in index or "@VERSION@" not in index:
        raise ValueError("The page names no release")
    output.mkdir(parents=True)
    for path, name in inputs:
        shutil.copyfile(path, output / name)
    (output / "index.html").write_text(index.replace("@TAG@", tag).replace("@VERSION@", tag[1:]),
                                       encoding="utf-8", newline="\n")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--release-dir", type=Path, required=True)
    parser.add_argument("--out", type=Path, required=True)
    parser.add_argument("--tag", required=True)
    args = parser.parse_args()
    root = Path(__file__).resolve().parents[1]

    def contains(commit):
        return subprocess.run(["git", "merge-base", "--is-ancestor", commit, "HEAD"], cwd=root,
                              stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode == 0

    stage_site(args.release_dir, root / "site", args.out, args.tag, contains)
    print("Staged the site page with the verified installers of " + args.tag)


if __name__ == "__main__":
    main()
