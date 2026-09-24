"""Offline, receipt-owned installation of a separately built core and bundles."""

import argparse
from contextlib import contextmanager
from dataclasses import dataclass
import errno
import gzip
import hashlib
import io
import json
import os
from pathlib import Path, PurePosixPath
import re
import shutil
import stat
import subprocess
import sys
import tarfile
import tempfile
from typing import Optional
import zipfile
import zlib


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
METADATA_LIMIT = 1024 * 1024
ARCHIVE_LIMIT = 256 * 1024 * 1024
MEMBER_LIMIT = 4096
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
        or not path.parts
        or str(path) != value
        or any(part in (".", "..") for part in path.parts)
    ):
        raise InstallError(f"Unsafe manifest/receipt path: {value!r}")
    return path


def file_bytes(path: Path, limit: Optional[int] = None) -> bytes:
    safe_path(path)
    info = path.stat()
    if not stat.S_ISREG(info.st_mode):
        raise InstallError(f"Expected a regular file: {path}")
    if limit is not None and info.st_size > limit:
        raise InstallError(f"Input size limit exceeded: {path.name}")
    with path.open("rb") as stream:
        data = stream.read() if limit is None else stream.read(limit + 1)
    if limit is not None and len(data) > limit:
        raise InstallError(f"Input size limit exceeded: {path.name}")
    return data


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


def bundle_file_manifest(contents: dict) -> dict:
    files = []
    destinations = set()
    for name, data in contents.items():
        source = relative_path(name)
        if len(source.parts) < 2 or source.parts[0] not in LAYOUT:
            raise InstallError("Unsupported canonical bundle source")
        destination = str(PurePosixPath(LAYOUT[source.parts[0]], *source.parts[1:]))
        if destination.casefold() in destinations:
            raise InstallError(f"Case-colliding bundle destination: {destination}")
        destinations.add(destination.casefold())
        files.append({"source": name, "destination": destination, "sha256": digest(data)})
    if not required_bundle_paths().issubset(contents):
        raise InstallError("The bundle is missing required skills or shared guidance")
    return {"schema": BUNDLE_SCHEMA, "files": files}


def bundle_manifest(bundle: Path) -> dict:
    bundle = resolve_bundle_directory(bundle)
    contents = {}
    for source_dir in LAYOUT:
        directory = bundle / source_dir
        safe_path(directory)
        if not directory.is_dir():
            raise InstallError(f"Missing canonical bundle directory: {directory}")
        for source in sorted(directory.rglob("*")):
            safe_path(source)
            if source.is_dir():
                continue
            relative = source.relative_to(bundle).as_posix()
            contents[relative] = file_bytes(source)
    return bundle_file_manifest(contents)


def checked_bundle_entries(actual: list, manifest: dict) -> list:
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
    if {entry["source"] for entry in actual} != set(declared):
        raise InstallError("Bundle files differ from the checksum manifest")
    for entry in actual:
        if entry != declared[entry["source"]]:
            raise InstallError(f"Bundle checksum mismatch: {entry['source']}")
    return actual


def verified_bundle(bundle: Path, manifest_path: Path) -> list:
    return checked_bundle_entries(
        bundle_manifest(bundle)["files"], json.loads(file_bytes(manifest_path))
    )


def native_target() -> str:
    system = {"win32": "windows", "linux": "linux", "darwin": "darwin"}.get(sys.platform)
    if system is None:
        raise InstallError(f"Unsupported native system: {sys.platform}")
    # platform.uname() may launch `ver` on Windows, even in an otherwise read-only plan.
    machine = (
        os.environ.get("PROCESSOR_ARCHITEW6432") or os.environ.get("PROCESSOR_ARCHITECTURE", "")
        if system == "windows" else os.uname().machine
    ).lower()
    arch = {"amd64": "amd64", "x86_64": "amd64", "arm64": "arm64", "aarch64": "arm64"}.get(machine)
    if arch is None:
        raise InstallError(f"Unsupported native system/architecture: {system}/{machine}")
    return system + "/" + arch


def archive_path(name: str) -> PurePosixPath:
    try:
        path = relative_path(name)
    except InstallError as error:
        raise InstallError(f"Unsafe archive path: {name!r}") from error
    reserved = {"con", "prn", "aux", "nul", "conin$", "conout$"}
    # Windows also recognizes superscript 1/2/3 in device names.
    reserved.update(f"{prefix}{number}" for prefix in ("com", "lpt") for number in "123456789\xb9\xb2\xb3")
    if any(
        part.endswith((".", " ")) or any(character in '<>"|?*\x7f' for character in part)
        or part.split(".")[0].casefold() in reserved
        for part in path.parts
    ):
        raise InstallError(f"Unsafe archive path: {name!r}")
    return path


def archive_members(data: bytes, name: str) -> dict:
    files = {}
    directories = {}
    seen = set()
    expanded = 0

    def check_member(member_name, regular, size):
        nonlocal expanded
        path = archive_path(member_name)
        key = str(path).casefold()
        if not regular or key in seen or key in directories:
            raise InstallError(f"Non-regular, duplicate or conflicting archive member: {member_name!r}")
        if len(seen) >= MEMBER_LIMIT:
            raise InstallError(f"Archive member limit exceeded: {name}")
        if size < 0 or expanded + size > ARCHIVE_LIMIT:
            raise InstallError(f"Archive expanded size limit exceeded: {name}")
        expanded += size
        for parent in path.parents:
            if parent == PurePosixPath("."):
                continue
            parent_key = str(parent).casefold()
            if parent_key in seen or directories.get(parent_key, str(parent)) != str(parent):
                raise InstallError(f"Conflicting archive member: {member_name!r}")
            directories[parent_key] = str(parent)
        seen.add(key)

    try:
        if name.endswith(".zip"):
            with zipfile.ZipFile(io.BytesIO(data)) as archive:
                for member in archive.infolist():
                    mode = stat.S_IFMT(member.external_attr >> 16)
                    check_member(
                        member.orig_filename,
                        not member.is_dir() and mode in (0, stat.S_IFREG)
                        and member.orig_filename == member.filename,
                        member.file_size,
                    )
                    files[member.filename] = archive.read(member)
                    if len(files[member.filename]) != member.file_size:
                        raise InstallError(f"Archive member size mismatch: {member.filename}")
        else:
            # Bound the decompressed stream before tarfile reads extended headers.
            with gzip.GzipFile(fileobj=io.BytesIO(data)) as stream:
                tar_data = stream.read(ARCHIVE_LIMIT + 1)
            if len(tar_data) > ARCHIVE_LIMIT:
                raise InstallError(f"Archive expanded size limit exceeded: {name}")
            with tarfile.open(fileobj=io.BytesIO(tar_data), mode="r:") as archive:
                for member in archive:
                    check_member(member.name, member.isfile() and not member.issparse(), member.size)
                    with archive.extractfile(member) as stream:
                        files[member.name] = stream.read()
    except (
        zipfile.BadZipFile, tarfile.TarError, gzip.BadGzipFile,
        EOFError, RuntimeError, NotImplementedError, zlib.error,
    ) as error:
        raise InstallError(f"Invalid distribution archive {name}: {error}") from error
    if not files:
        raise InstallError(f"Empty distribution archive: {name}")
    return files


@dataclass(frozen=True)
class Distribution:
    version: str
    target: str
    core_archive: str
    bundle_archive: str
    core: dict
    bundle: dict
    entries: list

    @property
    def sources(self) -> dict:
        return {entry["source"]: self.bundle["bundles/" + entry["source"]] for entry in self.entries}

    def extract(self, work: Path) -> None:
        # Never use extractall: names and file types were checked before any writes.
        for directory, members in (("core", self.core), ("bundle", self.bundle)):
            for name, data in members.items():
                path = safe_path(work / directory / Path(*archive_path(name).parts))
                make_directory(path.parent)
                with path.open("xb") as stream:
                    stream.write(data)


def verified_distribution(directory: Path) -> Distribution:
    directory = safe_path(directory)
    if not directory.is_dir():
        raise InstallError("--distribution-dir must be an existing local build directory")
    checksums = {}
    checksum_names = set()
    for line in file_bytes(directory / "SHA256SUMS", METADATA_LIMIT).decode("utf-8").splitlines():
        match = re.fullmatch(r"([0-9a-fA-F]{64})  (.+)", line)
        if match is None:
            raise InstallError("Invalid SHA256SUMS checksum entry")
        checksum, name = match.groups()
        try:
            path = archive_path(name)
        except InstallError as error:
            raise InstallError(f"Unsafe checksum filename: {name!r}") from error
        if len(path.parts) != 1 or name.casefold() in checksum_names:
            raise InstallError("Unsafe or duplicate checksum filename")
        checksum_names.add(name.casefold())
        checksums[name] = checksum.lower()
    raw = file_bytes(directory / "build-manifest.json", METADATA_LIMIT)
    if digest(raw) != checksums.get("build-manifest.json"):
        raise InstallError("Build manifest checksum mismatch")
    manifest = json.loads(raw)
    fields = {"version", "commit", "go", "targets", "products", "core_binaries", "archives"}
    if not isinstance(manifest, dict) or set(manifest) != fields:
        raise InstallError("Unsupported build manifest")
    version = manifest["version"]
    if not isinstance(version, str):
        raise InstallError("Invalid build manifest version")
    checked_version("midden " + version)
    targets = manifest["targets"]
    supported = {"windows/amd64", "linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64"}
    if (
        manifest["products"] != ["core", "bundle"]
        or not isinstance(targets, list) or not targets
        or any(not isinstance(target, str) or target not in supported for target in targets)
        or len(set(targets)) != len(targets)
    ):
        raise InstallError("Unsupported build manifest products/targets")
    core_hashes = manifest["core_binaries"]
    if (
        not isinstance(core_hashes, dict) or set(core_hashes) != set(targets)
        or any(not isinstance(value, str) or not HASH.fullmatch(value) for value in core_hashes.values())
    ):
        raise InstallError("Invalid build manifest core_binaries checksums")
    archive_names = {
        target: f"midden-core_{version}_{target.replace('/', '_')}"
        + (".zip" if target.startswith("windows/") else ".tar.gz")
        for target in targets
    }
    bundle_name = f"midden-bundle_{version}.zip"
    expected = set(archive_names.values()) | {bundle_name}
    declared = manifest["archives"]
    if (
        not isinstance(declared, list) or any(not isinstance(name, str) for name in declared)
        or len(declared) != len(expected) or set(declared) != expected
    ):
        raise InstallError("Build manifest archive names do not match its version/targets")
    if set(checksums) != expected | {"build-manifest.json"}:
        raise InstallError("Checksum inventory differs from build manifest archives")
    target = native_target()
    if target not in archive_names:
        raise InstallError(f"No native core archive declared for {target}")
    core_name = archive_names[target]
    snapshots = {}
    for name in (core_name, bundle_name):
        data = file_bytes(directory / name, ARCHIVE_LIMIT)
        if digest(data) != checksums[name]:
            raise InstallError(f"Archive checksum mismatch: {name}")
        snapshots[name] = data
    core = archive_members(snapshots[core_name], core_name)
    binary_name = "midden.exe" if target.startswith("windows/") else "midden"
    if set(core) != {binary_name, "LICENSE", "CORE.md"}:
        raise InstallError("Core archive must contain only the native binary, LICENSE and CORE.md")
    if digest(core[binary_name]) != core_hashes[target].lower():
        raise InstallError("Core checksum mismatch against build manifest; the binary was not executed")
    bundle = archive_members(snapshots[bundle_name], bundle_name)
    required = {"LICENSE", "install.ps1", "install.sh", "installer/install.py", "installer/bundle-manifest.json"}
    if not required.issubset(bundle) or any(
        name not in {"LICENSE", "install.ps1", "install.sh"}
        and (len(PurePosixPath(name).parts) < 2 or PurePosixPath(name).parts[0] not in {"bundles", "installer"})
        for name in bundle
    ):
        raise InstallError("Unsupported bundle archive layout")
    sources = {
        name[len("bundles/"):]: data for name, data in bundle.items()
        if name.startswith("bundles/") and len(PurePosixPath(name).parts) >= 3
        and PurePosixPath(name).parts[1] in LAYOUT
    }
    entries = checked_bundle_entries(
        bundle_file_manifest(sources)["files"], json.loads(bundle["installer/bundle-manifest.json"])
    )
    return Distribution(version, target, core_name, bundle_name, core, bundle, entries)


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


def atomic_write(path: Path, data: bytes, mode: int, expected: Optional[str]) -> Optional[Path]:
    check_expected(path, expected)
    descriptor, name = tempfile.mkstemp(prefix=".midden-write-", dir=path.parent)
    temporary = Path(name)
    published = False
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
        published = True
        # The transaction records publication before cleaning up the staging link.
        return temporary if expected is None else None
    finally:
        if not published:
            temporary.unlink(missing_ok=True)


def commit(changes: dict, expected: dict, modes: dict, work: Path) -> None:
    backups = {}
    created = []
    applied = []
    temporary_files = {}
    for index, path in enumerate(changes):
        check_expected(path, expected[path])
        if expected[path] is not None:
            backup = work / f"backup-{index}"
            backup.write_bytes(file_bytes(path))
            if digest(backup.read_bytes()) != expected[path]:
                raise InstallError(f"File changed while backing up: {path}")
            backups[path] = (backup, stat.S_IMODE(path.stat().st_mode))
    recovery = [
        {
            "path": str(path),
            "backup": backups[path][0].name if path in backups else None,
            "sha256": expected[path],
            "replacement_sha256": digest(data) if data is not None else None,
            "mode": backups[path][1] if path in backups else modes[path],
            "published": False,
            "temporary_files": [],
        }
        for path, data in changes.items()
    ]
    (work / "recovery.json").write_bytes(json_bytes(recovery))
    try:
        for path, data in changes.items():
            check_expected(path, expected[path])
            temporary = None
            if data is None:
                path.unlink()
            else:
                created.extend(make_directory(path.parent))
                temporary = atomic_write(path, data, modes[path], expected[path])
            applied.append(path)
            if temporary is not None:
                temporary_files[temporary] = (path, digest(data))
                temporary.unlink(missing_ok=True)
                del temporary_files[temporary]
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
                    temporary = atomic_write(path, backup.read_bytes(), mode, current)
                    if temporary is not None:
                        temporary_files[temporary] = (path, expected[path])
                        temporary.unlink(missing_ok=True)
                        del temporary_files[temporary]
                else:
                    path.unlink()
            except (OSError, InstallError) as error:
                errors.append(f"{path}: {error}")
        for temporary, (_target, checksum) in list(temporary_files.items()):
            try:
                if temporary.exists():
                    check_expected(temporary, checksum)
                temporary.unlink(missing_ok=True)
                del temporary_files[temporary]
            except (OSError, InstallError) as error:
                errors.append(f"Temporary cleanup {temporary}: {error}")
        if errors:
            for entry in recovery:
                path = Path(entry["path"])
                entry["published"] = path in applied and changes[path] is not None
                entry["temporary_files"] = [
                    str(temporary) for temporary, (target, _) in temporary_files.items()
                    if target == path
                ]
            try:
                (work / "recovery.json").write_bytes(json_bytes(recovery))
            except OSError as error:
                errors.append(f"Could not update recovery map: {error}")
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


def installation_changes(ctx: Context, core: bytes, entries: list, sources: dict) -> dict:
    changes = {ctx.binary: core}
    for entry in entries:
        data = sources[entry["source"]]
        if digest(data) != entry["sha256"]:
            raise InstallError("Bundle changed after checksum verification")
        destination = PurePosixPath(entry["destination"])
        if len(destination.parts) == 2 and destination.name == "SKILL.md":
            data.decode("utf-8")
            data += footer(ctx)
        changes[ctx.skills.joinpath(*destination.parts)] = data
    return changes


def show_plan(args, ctx: Context, record: Optional[dict], owned: dict,
              changes: dict, distribution: Optional[Distribution]) -> None:
    operation = "verify" if args.verify else "uninstall" if args.uninstall else "upgrade" if args.upgrade else "install"
    planned = dict(changes)
    planned[ctx.receipt] = None if args.uninstall else b""
    destinations = []
    for path, data in sorted(planned.items(), key=lambda item: str(item[0]).casefold()):
        action = (
            "verify" if args.verify else "remove" if data is None
            else "replace" if path in owned or (path == ctx.receipt and record is not None)
            else "create"
        )
        destinations.append({"path": str(path), "action": action})
    plan = {
        "dry_run": True, "operation": operation, "scope": ctx.scope, "host": ctx.host,
        "project": str(ctx.base) if ctx.scope == "project" else None,
        "home": str(ctx.base) if ctx.scope == "user" else None,
        "bin_dir": str(ctx.binary_dir), "state_dir": str(ctx.state),
        "skills_dir": str(ctx.skills), "destinations": destinations,
        "distribution": None if distribution is None else {
            "directory": str(safe_path(args.distribution_dir)),
            "target": distribution.target, "declared_version": distribution.version,
            "core_archive": distribution.core_archive, "bundle_archive": distribution.bundle_archive,
        },
        "dependencies": {
            "python": {"required": "Python 3.9+", "executable": sys.executable,
                       "running_version": ".".join(map(str, sys.version_info[:3]))},
            "filesystem": (
                "Hard-link support for installation/rollback and external temporary space; "
                "writability and hard links not probed."
            ),
            "core": "Install/upgrade requires midden 0.3.0+ (including 0.3.0-dev); version not probed.",
            "host": "No host executable, sign-in, model or permission configuration is required.",
            "outcomes": "Optional renderers are not installation dependencies; see midden-shared/tools.md.",
        },
        "notes": [
            "The core and optional tools were not executed; no extraction, writes or installation lock.",
            "State contents and unowned files are preserved. The plan is not a reservation; rerun checks when applying.",
            "Local checksums establish integrity, not cryptographic authenticity.",
        ],
    }
    print(json_bytes(plan).decode("utf-8"), end="")


def distribution_temporary_parent(ctx: Context, directory: Path) -> Path:
    parent = safe_path(Path(tempfile.gettempdir()).resolve())
    protected = [
        ctx.binary_dir, ctx.skills, ctx.state, safe_path(directory),
        safe_path(Path(__file__).parent.parent),
    ]
    if ctx.scope == "project":
        protected.append(ctx.base)
    if any(parent == root or root in parent.parents for root in protected):
        raise InstallError(
            "Distribution extraction requires an external temporary directory; "
            "set TMPDIR/TEMP outside the project, distribution and installation directories"
        )
    return parent


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
        if args.dry_run:
            show_plan(args, ctx, record, owned, owned, None)
        else:
            print(f"Verified {record['version']}: all receipt-owned files match")
        return
    if args.uninstall and record is None:
        raise InstallError("No supported installation receipt; no files were removed")
    distribution = None
    if not args.uninstall:
        if args.upgrade != (record is not None):
            raise InstallError("Use --upgrade only with an existing, unchanged installation receipt")
        if args.distribution_dir:
            if any((args.core_binary, args.core_sha256, args.bundle_dir, args.manifest)):
                raise InstallError("Do not combine --distribution-dir with explicit core/bundle/manifest options")
            distribution = verified_distribution(args.distribution_dir)
            core = distribution.core[BINARY_NAME]
            entries = distribution.entries
            sources = distribution.sources
        else:
            if not args.core_binary or not args.bundle_dir or not args.core_sha256:
                raise InstallError(
                    "Installation requires --distribution-dir, or --core-binary, --core-sha256 "
                    "and --bundle-root (or --bundle-dir)"
                )
            if not HASH.fullmatch(args.core_sha256):
                raise InstallError("--core-sha256 must be a trusted 64-character SHA-256 digest")
            core = file_bytes(safe_path(args.core_binary))
            if digest(core) != args.core_sha256.lower():
                raise InstallError("Core checksum mismatch; the binary was not executed")
            bundle = resolve_bundle_directory(args.bundle_dir)
            manifest = args.manifest or bundle.parent / "installer" / "bundle-manifest.json"
            entries = verified_bundle(bundle, safe_path(manifest))
            sources = {
                entry["source"]: file_bytes(bundle.joinpath(*PurePosixPath(entry["source"]).parts))
                for entry in entries
            }
        changes = installation_changes(ctx, core, entries, sources)
    else:
        changes = {}
    for path in owned:
        if path not in changes:
            changes[path] = None
    for path in changes:
        check_expected(path, owned.get(path))
    if args.dry_run:
        show_plan(args, ctx, record, owned, changes, distribution)
        return
    temporary_parent = (
        distribution_temporary_parent(ctx, args.distribution_dir) if distribution is not None else None
    )
    work = Path(tempfile.mkdtemp(prefix="midden-install-", dir=temporary_parent)).resolve()
    retain_work = False
    try:
        if not args.uninstall:
            if distribution is not None:
                distribution.extract(work)
                staged_core = file_bytes(work / "core" / BINARY_NAME)
                if digest(staged_core) != digest(core):
                    raise InstallError("Core checksum changed during distribution extraction")
                core = staged_core
                sources = {
                    entry["source"]: file_bytes(
                        work / "bundle" / "bundles" / Path(*PurePosixPath(entry["source"]).parts)
                    )
                    for entry in entries
                }
                changes.update(installation_changes(ctx, core, entries, sources))
            version = probe(core, ctx, work)
            if distribution is not None and version != "midden " + distribution.version:
                raise InstallError("Core version does not match the build manifest; nothing was installed")
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
        if distribution is not None:
            print("Local checksums establish integrity, not cryptographic authenticity.")
        print(f"Core executable: {ctx.binary}")
        print(f"State binding: {ctx.state}")
        print('In your AI host, ask: "What is worth explaining from this work?"')


def parser() -> argparse.ArgumentParser:
    result = argparse.ArgumentParser(description=__doc__)
    result.add_argument(
        "--distribution-dir", "-DistributionDir",
        help="Local release/staging directory containing build-manifest.json, SHA256SUMS and native archives",
    )
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
    result.add_argument(
        "--dry-run", "--plan", "-DryRun", "-Plan", action="store_true",
        help="Verify inputs/ownership and print a JSON plan without extraction, writes or running the core",
    )
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
