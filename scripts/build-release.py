"""Build the reviewed core, bundle and app release files: one product per file."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tarfile
import tempfile
import zipfile

sys.dont_write_bytecode = True
from release_contract import (INSTALLERS, PANDOC_NOTICE, PRODUCTS, TARGETS, app_inputs, archive_name, bundle_inputs,
                              dependency_notices, git_blob_bytes, materialize_inputs, pandoc_records, release_tsv,
                              release_version, validate_member_set)


ROOT = Path(__file__).resolve().parent.parent
APP = ROOT / "apps" / "midden-ui"


def run(*args):
    return subprocess.check_output(args, cwd=ROOT, text=True).strip()


def version():
    text = (ROOT / "internal" / "core" / "identity.go").read_text(encoding="utf-8")
    match = re.search(r'\bVersion\s*=\s*"([^"]+)"', text)
    if not match:
        raise RuntimeError("Core development version is not declared")
    return release_version(match.group(1))


def digest(path):
    result = hashlib.sha256()
    with Path(path).open("rb") as source:
        for block in iter(lambda: source.read(1024 * 1024), b""):
            result.update(block)
    return result.hexdigest()


def write_archive(path, files):
    if any(source.is_symlink() or not source.is_file() or
           getattr(source.lstat(), "st_file_attributes", 0) & 0x400 for source, _ in files):
        raise RuntimeError("Archive inputs must be ordinary files, not links")
    names = [name for _, name in files]
    try:
        validate_member_set(names)
    except ValueError as error:
        raise RuntimeError(str(error)) from error
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
    parser.add_argument("--version", help="Release SemVer, injected into both independently built binaries")
    args = parser.parse_args()
    release = release_version(args.version) if args.version else version()
    if len(set(args.targets)) != len(args.targets):
        raise SystemExit("Duplicate target")
    if run("git", "status", "--porcelain"):
        raise SystemExit("Packaging requires a clean checkout")
    commit = run("git", "rev-parse", "HEAD")
    if not args.out.is_absolute() or args.out.resolve() == ROOT or ROOT in args.out.resolve().parents:
        raise SystemExit("--out must be absolute and outside the checkout")
    args.out.mkdir(parents=True, exist_ok=True)
    if any(args.out.iterdir()):
        raise SystemExit("Output directory must be empty")
    bundle_files = bundle_inputs(ROOT, commit)
    static_app_files = app_inputs(ROOT, commit)
    for name in INSTALLERS:
        run("git", "cat-file", "-e", f"{commit}:{name}")
    archives, entries = [], []
    core_digests, app_digests = {}, {}
    with tempfile.TemporaryDirectory(prefix="midden-release-") as temporary:
        temporary = Path(temporary)
        bundle_files = materialize_inputs(ROOT, commit, bundle_files, temporary / "source")
        static_app_files = materialize_inputs(ROOT, commit, static_app_files, temporary / "source")
        static_core_files = materialize_inputs(ROOT, commit, [(ROOT / "LICENSE", "LICENSE")], temporary / "source")

        def archive_product(product, target, files):
            name = archive_name(product, release, target)
            write_archive(args.out / name, files)
            entries.append((product, target, name, {relative: digest(source) for source, relative in files}))
            archives.append(name)

        for target in args.targets:
            system, arch = target.split("/")
            suffix = ".exe" if system == "windows" else ""
            core = temporary / ("midden" + suffix)
            app = temporary / ("midden-ui" + suffix)
            env = dict(os.environ, GOOS=system, GOARCH=arch, CGO_ENABLED="0", GOWORK="off")
            for cwd, binary, package, symbol in (
                (ROOT, core, "./cmd/midden", "github.com/xibodev/midden/internal/core.Version"),
                (APP, app, ".", "main.version"),
            ):
                subprocess.run(["go", "build", "-mod=readonly", "-trimpath",
                                f"-ldflags=-s -w -X {symbol}={release}", "-o", str(binary), package],
                               cwd=cwd, env=env, check=True)
                binary.chmod(0o755)
            core_digests[target], app_digests[target] = digest(core), digest(app)
            core_notice = temporary / "core-notices.txt"
            core_notice.write_bytes(dependency_notices(ROOT, "./cmd/midden", env))
            archive_product("core", target, [
                (core, core.name), *static_core_files, (core_notice, "THIRD_PARTY_NOTICES.txt"),
            ])
            app_notice = temporary / "app-notices.txt"
            app_notice.write_bytes(dependency_notices(APP, ".", env) + PANDOC_NOTICE.encode("utf-8"))
            archive_product("app", target, [
                (app, app.name), *static_app_files, (app_notice, "app/THIRD_PARTY_NOTICES.txt"),
            ])
        archive_product("bundle", "universal", bundle_files)
    for name in INSTALLERS:
        (args.out / name).write_bytes(git_blob_bytes(ROOT, commit, ROOT / name))
    manifest = {
        "version": release, "commit": commit, "go": run("go", "version"),
        "targets": args.targets, "products": list(PRODUCTS),
        "core_binaries": core_digests, "app_binaries": app_digests,
        "archives": archives, "installers": list(INSTALLERS),
    }
    (args.out / "build-manifest.json").write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8", newline="\n")
    (args.out / "manifest.tsv").write_text(
        release_tsv(release, commit, entries, pandoc_records(args.targets)), encoding="utf-8", newline="\n")
    names = archives + list(INSTALLERS) + ["build-manifest.json", "manifest.tsv"]
    (args.out / "SHA256SUMS").write_text(
        "".join(digest(args.out / name) + "  " + name + "\n" for name in sorted(names)), encoding="utf-8", newline="\n")
    if run("git", "status", "--porcelain"):
        raise SystemExit("Source tree changed while packaging; these artifacts must not be published")
    print(f"Built Midden {release}: core, bundle and app for {', '.join(args.targets)}")


if __name__ == "__main__":
    main()
