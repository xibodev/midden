"""Offline, receipt-owned installation of a separately built core and bundles."""

import argparse
from contextlib import contextmanager
from dataclasses import dataclass
import errno
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import re
import shutil
import stat
import subprocess
import sys
import tempfile
from typing import Optional


BUNDLE_SCHEMA = "midden.bundle-files/v1"
RECEIPT_SCHEMA = "midden.install/v1"
RECEIPT_NAME = "midden-install-receipt.json"
BINARY_NAME = "midden.exe" if os.name == "nt" else "midden"
LAYOUT = {
    "investigation": "midden-investigation",
    "article": "midden-article",
    "presentation": "midden-presentation",
    "long-form": "midden-long-form",
    "midden-shared": "midden-shared",
}
HOSTS = {
    "copilot": {"project": ".github/skills", "user": ".copilot/skills"},
    "agents": {"project": ".agents/skills", "user": ".agents/skills"},
    "claude": {"project": ".claude/skills", "user": ".claude/skills"},
}
HASH = re.compile(r"[0-9a-fA-F]{64}")
VERSION = re.compile(
    r"midden ([0-9]+)\.([0-9]+)\.([0-9]+)(?:[-+][0-9A-Za-z.+-]+)?"
)


class InstallError(Exception):
    pass


class RecoveryRequired(InstallError):
    pass


def safe_path(value) -> Path:
    raw = os.fspath(value)
    if not raw or any(ord(character) < 32 for character in raw):
        raise InstallError("Paths must be nonempty and contain no control characters")
    path = Path(os.path.abspath(raw))
    for candidate in (path, *path.parents):
        try:
            info = candidate.lstat()
        except FileNotFoundError:
            continue
        linked = stat.S_ISLNK(info.st_mode)
        reparse = getattr(info, "st_file_attributes", 0) & getattr(
            stat, "FILE_ATTRIBUTE_REPARSE_POINT", 0
        )
        if linked or reparse:
            raise InstallError(f"Refusing linked/reparse path: {candidate}")
    return path


def relative_path(value) -> PurePosixPath:
    if not isinstance(value, str) or not value:
        raise InstallError("A manifest/receipt path must be a nonempty string")
    path = PurePosixPath(value)
    if (
        "\\" in value
        or ":" in value
        or any(ord(character) < 32 for character in value)
        or path.is_absolute()
        or str(path) != value
        or any(part in (".", "..") for part in path.parts)
    ):
        raise InstallError(f"Unsafe manifest/receipt path: {value!r}")
    return path


def file_bytes(path: Path) -> bytes:
    safe_path(path)
    if not stat.S_ISREG(path.stat().st_mode):
        raise InstallError(f"Expected a regular file: {path}")
    return path.read_bytes()


def digest(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def json_bytes(value) -> bytes:
    return (json.dumps(value, indent=2, ensure_ascii=True) + "\n").encode("utf-8")


def required_bundle_paths() -> set:
    return {
        name + "/SKILL.md" for name in LAYOUT if name != "midden-shared"
    } | {"midden-shared/sources.md", "midden-shared/tools.md"}


def resolve_bundle_directory(value) -> Path:
    root = safe_path(value)
    if not root.is_dir():
        raise InstallError(f"Bundle root must be an existing directory: {root}")
    nested = safe_path(root / "bundles")
    if nested.exists():
        if any((root / name).exists() for name in LAYOUT):
            raise InstallError("Ambiguous bundle root: both direct and nested bundle material are present")
        if not nested.is_dir():
            raise InstallError("The bundle artifact's bundles entry must be a directory")
        return nested
    return root


def bundle_manifest(bundle: Path) -> dict:
    bundle = resolve_bundle_directory(bundle)
    files = []
    destinations = set()
    for source_dir, destination_dir in LAYOUT.items():
        directory = bundle / source_dir
        safe_path(directory)
        if not directory.is_dir():
            raise InstallError(f"Missing canonical bundle directory: {directory}")
        for source in sorted(directory.rglob("*")):
            safe_path(source)
            if source.is_dir():
                continue
            relative = source.relative_to(bundle).as_posix()
            relative_path(relative)
            destination = (
                destination_dir + "/" + source.relative_to(directory).as_posix()
            )
            if destination.casefold() in destinations:
                raise InstallError(f"Case-colliding bundle destination: {destination}")
            destinations.add(destination.casefold())
            files.append(
                {"source": relative, "destination": destination,
                 "sha256": digest(file_bytes(source))}
            )
    if not required_bundle_paths().issubset({item["source"] for item in files}):
        raise InstallError("The bundle is missing required skills or shared guidance")
    return {"schema": BUNDLE_SCHEMA, "files": files}


def verified_bundle(bundle: Path, manifest_path: Path) -> list:
    manifest = json.loads(file_bytes(manifest_path))
    if not isinstance(manifest, dict) or set(manifest) != {"schema", "files"}:
        raise InstallError("Invalid bundle manifest object")
    if manifest["schema"] != BUNDLE_SCHEMA or not isinstance(manifest["files"], list):
        raise InstallError("Unsupported bundle manifest schema")
    declared = {}
    for entry in manifest["files"]:
        if not isinstance(entry, dict) or set(entry) != {"source", "destination", "sha256"}:
            raise InstallError("Invalid bundle manifest entry")
        source = relative_path(entry["source"])
        destination = relative_path(entry["destination"])
        checksum = entry["sha256"]
        if not isinstance(checksum, str) or not HASH.fullmatch(checksum):
            raise InstallError("Invalid bundle checksum")
        if source.parts[0] not in LAYOUT or len(source.parts) < 2:
            raise InstallError("Unsupported canonical bundle source")
        expected = PurePosixPath(LAYOUT[source.parts[0]], *source.parts[1:])
        if destination != expected or entry["source"] in declared:
            raise InstallError("Invalid or duplicate bundle source/destination mapping")
        declared[entry["source"]] = {**entry, "sha256": checksum.lower()}
    actual = bundle_manifest(bundle)["files"]
    if {entry["source"] for entry in actual} != set(declared):
        raise InstallError("Bundle files differ from the checksum manifest")
    for entry in actual:
        if entry != declared[entry["source"]]:
            raise InstallError(f"Bundle checksum mismatch: {entry['source']}")
    return actual


@dataclass(frozen=True)
class Context:
    base: Path
    binary_dir: Path
    skills: Path
    state: Path
    scope: str
    host: str

    @property
    def binary(self) -> Path:
        return self.binary_dir / BINARY_NAME

    @property
    def receipt(self) -> Path:
        return self.binary_dir / RECEIPT_NAME

    @property
    def binding(self) -> dict:
        return {
            "base": str(self.base), "bin_dir": str(self.binary_dir),
            "state_dir": str(self.state), "scope": self.scope, "host": self.host,
        }


def overlaps(first: Path, second: Path) -> bool:
    return first == second or first in second.parents or second in first.parents


def load_context(args) -> tuple[Context, Optional[dict]]:
    base = safe_path(args.project if args.scope == "project" else args.home_dir)
    if not base.is_dir():
        raise InstallError("The selected project/home directory must already exist")
    binary_dir = safe_path(args.bin_dir or base / ".midden" / "bin")
    skills = safe_path(base.joinpath(*PurePosixPath(HOSTS[args.host][args.scope]).parts))
    receipt = read_receipt(binary_dir / RECEIPT_NAME)
    state = args.state_dir
    if state is None and receipt is not None:
        binding = receipt.get("binding")
        if not isinstance(binding, dict) or not isinstance(binding.get("state_dir"), str):
            raise InstallError("Invalid installation receipt binding")
        state = binding["state_dir"]
    state = safe_path(state or base / ".midden" / "state")
    for first, second in ((binary_dir, skills), (binary_dir, state), (skills, state)):
        if overlaps(first, second):
            raise InstallError("Binary, skill, and state directories must not overlap")
    for directory in (binary_dir, skills, state):
        if directory.exists() and not directory.is_dir():
            raise InstallError(f"Expected a directory: {directory}")
    return Context(base, binary_dir, skills, state, args.scope, args.host), receipt


def read_receipt(path: Path) -> Optional[dict]:
    safe_path(path)
    if not path.exists():
        return None
    value = json.loads(file_bytes(path))
    if (
        not isinstance(value, dict)
        or set(value) != {"schema", "binding", "version", "files"}
        or value["schema"] != RECEIPT_SCHEMA
    ):
        raise InstallError("Unsupported installation receipt; legacy receipts are not migrated")
    return value


def checked_version(value: str) -> str:
    match = VERSION.fullmatch(value)
    if match is None or tuple(map(int, match.groups()[:3])) < (0, 3, 0):
        raise InstallError("The installer requires deterministic core midden 0.3.0 or newer (including 0.3.0-dev)")
    return value


def receipt_files(record: dict, ctx: Context) -> dict:
    if record["binding"] != ctx.binding:
        raise InstallError("Receipt scope/host/paths differ; use the original binding or a separate installation")
    if not isinstance(record["version"], str):
        raise InstallError("Invalid receipt core version")
    checked_version(record["version"])
    if not isinstance(record["files"], list) or not record["files"]:
        raise InstallError("The installation receipt has no owned file inventory")
    owned = {}
    seen = set()
    for entry in record["files"]:
        if not isinstance(entry, dict) or set(entry) != {"root", "path", "sha256"}:
            raise InstallError("Invalid receipt file entry")
        relative = relative_path(entry["path"])
        if entry["root"] == "bin" and relative == PurePosixPath(BINARY_NAME):
            target = ctx.binary
        elif (
            entry["root"] == "skills" and len(relative.parts) >= 2
            and relative.parts[0] in LAYOUT.values()
        ):
            target = ctx.skills.joinpath(*relative.parts)
        else:
            raise InstallError("Receipt claims a file outside the owned namespaces")
        checksum = entry["sha256"]
        if not isinstance(checksum, str) or not HASH.fullmatch(checksum):
            raise InstallError("Invalid receipt checksum")
        key = str(target).casefold()
        if key in seen:
            raise InstallError("Duplicate receipt path")
        seen.add(key)
        owned[target] = checksum.lower()
    required = {ctx.binary} | {
        ctx.skills / name / "SKILL.md"
        for name in LAYOUT.values() if name != "midden-shared"
    } | {ctx.skills / "midden-shared" / "sources.md", ctx.skills / "midden-shared" / "tools.md"}
    if not required.issubset(owned):
        raise InstallError("Incomplete installation receipt")
    return owned


def check_expected(path: Path, expected: Optional[str]) -> None:
    safe_path(path)
    if expected is None:
        if path.exists():
            raise InstallError(f"Refusing an unowned collision: {path}")
    elif not path.exists() or digest(file_bytes(path)) != expected:
        raise InstallError(f"Preserving modified or missing receipt-owned file: {path}")


def make_directory(path: Path) -> list:
    safe_path(path)
    missing = []
    cursor = path
    while not cursor.exists():
        missing.append(cursor)
        cursor = cursor.parent
    created = []
    for directory in reversed(missing):
        directory.mkdir()
        created.append(directory)
    return created


def remove_empty(directories: list) -> None:
    for directory in reversed(directories):
        try:
            directory.rmdir()
        except OSError as error:
            if error.errno not in (errno.ENOTEMPTY, errno.EEXIST):
                raise


@contextmanager
def installation_lock(ctx: Context):
    created = make_directory(ctx.binary_dir)
    lock = ctx.binary_dir / ".midden-install.lock"
    safe_path(lock)
    try:
        stream = lock.open("xb")
    except FileExistsError as error:
        remove_empty(created)
        raise InstallError("Installation lock exists; inspect the active/stale operation before retrying") from error
    failure = None
    try:
        with stream:
            stream.write(b"midden file installation\n")
        yield
    except (OSError, InstallError, KeyboardInterrupt) as error:
        failure = error
        raise
    finally:
        try:
            lock.unlink()
            remove_empty(created)
        except OSError as error:
            if failure is not None:
                kind = RecoveryRequired if isinstance(failure, RecoveryRequired) else InstallError
                raise kind(f"{failure}; lock cleanup failed: {error}") from failure
            raise


def atomic_write(path: Path, data: bytes, mode: int, expected: Optional[str]) -> None:
    check_expected(path, expected)
    descriptor, name = tempfile.mkstemp(prefix=".midden-write-", dir=path.parent)
    temporary = Path(name)
    try:
        with os.fdopen(descriptor, "wb") as stream:
            stream.write(data)
            stream.flush()
            os.fsync(stream.fileno())
        temporary.chmod(mode)
        check_expected(path, expected)
        if expected is None:
            # Same-directory hard linking refuses a late, unowned collision atomically.
            os.link(temporary, path)
        else:
            os.replace(temporary, path)
    finally:
        temporary.unlink(missing_ok=True)


def commit(changes: dict, expected: dict, modes: dict, work: Path) -> None:
    backups = {}
    created = []
    applied = []
    for index, path in enumerate(changes):
        check_expected(path, expected[path])
        if expected[path] is not None:
            backup = work / f"backup-{index}"
            backup.write_bytes(file_bytes(path))
            if digest(backup.read_bytes()) != expected[path]:
                raise InstallError(f"File changed while backing up: {path}")
            backups[path] = (backup, stat.S_IMODE(path.stat().st_mode))
    (work / "recovery.json").write_bytes(
        json_bytes([
            {"path": str(path), "backup": backup.name, "sha256": expected[path], "mode": mode}
            for path, (backup, mode) in backups.items()
        ])
    )
    try:
        for path, data in changes.items():
            check_expected(path, expected[path])
            if data is None:
                path.unlink()
            else:
                created.extend(make_directory(path.parent))
                atomic_write(path, data, modes[path], expected[path])
            applied.append(path)
            check_expected(path, digest(data) if data is not None else None)
    except (OSError, InstallError, KeyboardInterrupt) as failure:
        errors = []
        for path in reversed(applied):
            data = changes[path]
            current = digest(data) if data is not None else None
            try:
                check_expected(path, current)
                if path in backups:
                    backup, mode = backups[path]
                    atomic_write(path, backup.read_bytes(), mode, current)
                else:
                    path.unlink()
            except (OSError, InstallError) as error:
                errors.append(f"{path}: {error}")
        if errors:
            raise RecoveryRequired(
                f"Installation failed: {failure}. Rollback needs recovery; backups remain in {work}. "
                + "; ".join(errors)
            ) from failure
        remove_empty(created)
        raise


def footer(ctx: Context) -> bytes:
    binding = {"executable": str(ctx.binary), "environment": {"MIDDEN_HOME": str(ctx.state)}}
    return (
        "\n\n## Local core binding\n\n"
        "Use this exact installed executable and set its state environment for each\n"
        "invocation through the host's shell tool. These are local file bindings,\n"
        "not permissions or global shell settings.\n\n```json\n"
        + json_bytes(binding).decode("utf-8").rstrip()
        + "\n```\n"
    ).encode("utf-8")


def probe(data: bytes, ctx: Context, work: Path) -> str:
    executable = work / BINARY_NAME
    executable.write_bytes(data)
    executable.chmod(0o755)
    env = dict(os.environ, MIDDEN_HOME=str(ctx.state))
    result = subprocess.run(
        [str(executable), "version"], stdin=subprocess.DEVNULL, capture_output=True,
        text=True, encoding="utf-8", env=env, timeout=15, check=False,
    )
    if result.returncode != 0 or result.stderr.strip():
        raise InstallError(f"Core version probe failed ({result.returncode}): {result.stderr.strip()}")
    return checked_version(result.stdout.strip())


def execute(args) -> None:
    ctx, record = load_context(args)
    owned = receipt_files(record, ctx) if record is not None else {}
    for path, checksum in owned.items():
        check_expected(path, checksum)
    receipt_checksum = digest(file_bytes(ctx.receipt)) if record is not None else None
    lock = ctx.binary_dir / ".midden-install.lock"
    safe_path(lock)
    if lock.exists():
        raise InstallError("An installation operation or stale lock exists")
    if args.verify:
        if record is None:
            raise InstallError("No supported installation receipt")
        print(f"Verified {record['version']}: all receipt-owned files match")
        return
    if args.uninstall and record is None:
        raise InstallError("No supported installation receipt; no files were removed")
    if not args.uninstall:
        if args.upgrade != (record is not None):
            raise InstallError("Use --upgrade only with an existing, unchanged installation receipt")
        if not args.core_binary or not args.bundle_dir or not args.core_sha256:
            raise InstallError("Installation requires --core-binary, --core-sha256, and --bundle-root (or --bundle-dir)")
        if not HASH.fullmatch(args.core_sha256):
            raise InstallError("--core-sha256 must be a trusted 64-character SHA-256 digest")
        core = file_bytes(safe_path(args.core_binary))
        if digest(core) != args.core_sha256.lower():
            raise InstallError("Core checksum mismatch; the binary was not executed")
        bundle = resolve_bundle_directory(args.bundle_dir)
        manifest = args.manifest or bundle.parent / "installer" / "bundle-manifest.json"
        entries = verified_bundle(bundle, safe_path(manifest))
        changes = {ctx.binary: core}
        for entry in entries:
            data = file_bytes(bundle.joinpath(*PurePosixPath(entry["source"]).parts))
            if digest(data) != entry["sha256"]:
                raise InstallError("Bundle changed after checksum verification")
            destination = PurePosixPath(entry["destination"])
            if len(destination.parts) == 2 and destination.name == "SKILL.md":
                data.decode("utf-8")
                data += footer(ctx)
            changes[ctx.skills.joinpath(*destination.parts)] = data
    else:
        changes = {}
    for path in owned:
        if path not in changes:
            changes[path] = None
    for path in changes:
        check_expected(path, owned.get(path))
    work = Path(tempfile.mkdtemp(prefix="midden-install-")).resolve()
    retain_work = False
    try:
        if not args.uninstall:
            version = probe(core, ctx, work)
            files = []
            for path, data in changes.items():
                if data is not None:
                    root = "bin" if path == ctx.binary else "skills"
                    relative = BINARY_NAME if root == "bin" else path.relative_to(ctx.skills).as_posix()
                    files.append({"root": root, "path": relative, "sha256": digest(data)})
            changes[ctx.receipt] = json_bytes(
                {"schema": RECEIPT_SCHEMA, "binding": ctx.binding, "version": version, "files": files}
            )
        else:
            changes[ctx.receipt] = None
        expected = {path: owned.get(path) for path in changes}
        expected[ctx.receipt] = receipt_checksum
        modes = {path: 0o755 if path == ctx.binary else 0o644 for path in changes}
        with installation_lock(ctx):
            check_expected(ctx.receipt, receipt_checksum)
            for path, checksum in owned.items():
                check_expected(path, checksum)
            commit(changes, expected, modes, work)
    except RecoveryRequired:
        retain_work = True
        raise
    finally:
        if not retain_work:
            shutil.rmtree(work)
    if args.uninstall:
        print("Removed unchanged receipt-owned files; state and unowned files were preserved")
    else:
        print(f"Installed {version} for {args.host} ({args.scope} scope)")
        print(f"Core executable: {ctx.binary}")
        print(f"State binding: {ctx.state}")
        print('In your AI host, ask: "What is worth explaining from this work?"')


def parser() -> argparse.ArgumentParser:
    result = argparse.ArgumentParser(description=__doc__)
    result.add_argument("--core-binary", "-CoreBinary", help="Explicit path to a separately built core")
    result.add_argument("--core-sha256", "-CoreSha256", help="Expected binary digest from trusted release metadata")
    result.add_argument(
        "--bundle-dir", "--bundle-root", "-BundleDir", "-BundleRoot",
        help="Extracted bundle artifact root or its canonical bundles directory",
    )
    result.add_argument(
        "--manifest", "-Manifest",
        help="Defaults to installer/bundle-manifest.json beside the selected bundles directory",
    )
    result.add_argument("--project", "-ProjectDir", default=os.getcwd())
    result.add_argument("--home-dir", "-HomeDir", default=str(Path.home()))
    result.add_argument("--scope", "-Scope", choices=("project", "user"), default="project")
    result.add_argument("--host", "-Host", choices=tuple(HOSTS), default="copilot")
    result.add_argument("--bin-dir", "-BinDir", help="Scoped binary/receipt directory")
    result.add_argument("--state-dir", "-StateDir", help="Per-process MIDDEN_HOME binding; never removed")
    operation = result.add_mutually_exclusive_group()
    operation.add_argument("--upgrade", "-Upgrade", action="store_true")
    operation.add_argument("--verify", "-Verify", action="store_true")
    operation.add_argument("--uninstall", "-Uninstall", action="store_true")
    return result


def main(argv=None) -> int:
    try:
        execute(parser().parse_args(argv))
    except (InstallError, OSError, ValueError, subprocess.SubprocessError) as error:
        print(f"midden installer: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
