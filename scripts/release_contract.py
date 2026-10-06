"""Release inputs are reviewed Git objects, not everything in local folders."""
from pathlib import Path, PurePosixPath
import re
import subprocess


ROOT_FILES = {"LICENSE", "install.ps1", "install.sh"}
BUNDLE_SUFFIXES = {".md", ".py", ".ps1", ".sh", ".json", ".yaml", ".yml", ".css", ".svg", ".png", ".lua"}
TARGETS = ("windows/amd64", "linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64")
HASH = re.compile(r"[0-9a-f]{64}")
COMMIT = re.compile(r"[0-9a-f]{40}(?:[0-9a-f]{24})?")
# xibodev components (the team's own modules) are named in one line each; their texts are not reproduced.
XIBODEV_PREFIX = "github.com/xibodev/"
XIBODEV_NAMES = {"github.com/xibodev/compa": "Compa"}
LICENSE_SIGNATURES = (
    ("MIT License", ("Permission is hereby granted, free of charge, to any person obtaining a copy",
                     "The above copyright notice and this permission notice shall be included")),
    ("Apache License 2.0", ("Apache License", "Version 2.0, January 2004")),
)
# The only lines of shipped notices that may mention a xibodev component: its one-line entry.
XIBODEV_ENTRY = re.compile(r"[A-Za-z0-9._-]+ \u2014 https://github\.com/xibodev/[A-Za-z0-9._-]+ \u2014 (?:"
                           + "|".join(re.escape(name) for name, _ in LICENSE_SIGNATURES) + ")")


def release_version(value):
    if not isinstance(value, str) or len(value) > 64 or not re.fullmatch(
        r"(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)(?:-[0-9A-Za-z.-]+)?", value
    ):
        raise ValueError("Version must be a plain semantic version without a v prefix")
    if "-" in value:
        for identifier in value.split("-", 1)[1].split("."):
            if not identifier or (identifier.isdigit() and len(identifier) > 1 and identifier.startswith("0")):
                raise ValueError("Invalid prerelease identifier")
    return value


def safe_member(name):
    if not isinstance(name, str) or not re.fullmatch(r"[A-Za-z0-9._/-]+", name):
        raise ValueError("Archive paths must use portable ASCII names")
    path = PurePosixPath(name)
    if path.is_absolute() or str(path) != name or not path.parts:
        raise ValueError("Archive path is not canonical and relative")
    reserved = {"con", "prn", "aux", "nul"} | {f"{prefix}{i}" for prefix in ("com", "lpt") for i in range(1, 10)}
    for part in path.parts:
        if part in (".", "..") or part.endswith(".") or part.split(".", 1)[0].casefold() in reserved:
            raise ValueError("Unsafe portable archive path")
    return name


def archive_name(product, version, target):
    release_version(version)
    if product == "bundle":
        if target != "universal":
            raise ValueError("Bundle target must be universal")
        return f"midden-bundle_{version}.zip"
    if product not in ("core", "ui") or target not in TARGETS:
        raise ValueError("Unsupported release product or target")
    suffix = ".zip" if target.startswith("windows/") else ".tar.gz"
    return f"midden-{product}_{version}_{target.replace('/', '_')}{suffix}"


def validate_member_set(names):
    names = list(names)
    folded = {safe_member(name).casefold() for name in names}
    if len(folded) != len(names):
        raise ValueError("Duplicate or case-colliding archive members")
    for name in names:
        for parent in PurePosixPath(name).parents:
            if str(parent) != "." and str(parent).casefold() in folded:
                raise ValueError("An archive file conflicts with a parent directory")


def release_tsv(version, commit, entries):
    release_version(version)
    if not COMMIT.fullmatch(commit):
        raise ValueError("Invalid release source commit")
    lines = ["format\tmidden-release-v1", f"version\t{version}", f"commit\t{commit}"]
    for product, target, archive, files in entries:
        lines.append("\t".join(("archive", product, target, archive)))
        for name, digest in sorted(files.items()):
            lines.append("\t".join(("file", product, target, name, digest)))
    text = "\n".join(lines) + "\n"
    parse_release_tsv(text)
    return text


def parse_release_tsv(text):
    result = {"archives": {}, "files": {}}
    seen_archives = set()
    folded_files = {}
    for line in text.splitlines():
        parts = line.split("\t")
        if len(parts) == 2 and parts[0] in ("format", "version", "commit"):
            key, value = parts
            if key in result:
                raise ValueError("Duplicate manifest metadata")
            result[key] = value
        elif len(parts) == 4 and parts[0] == "archive":
            _, product, target, name = parts
            key = (product, target)
            if key in result["archives"] or name.casefold() in seen_archives:
                raise ValueError("Duplicate release archive")
            safe_member(name)
            if PurePosixPath(name).name != name:
                raise ValueError("Release archive must be a filename")
            result["archives"][key] = name
            result["files"][key] = {}
            folded_files[key] = set()
            seen_archives.add(name.casefold())
        elif len(parts) == 5 and parts[0] == "file":
            _, product, target, name, digest = parts
            key = (product, target)
            safe_member(name)
            if key not in result["archives"] or name.casefold() in folded_files[key] or not HASH.fullmatch(digest):
                raise ValueError("Unknown archive, duplicate member or invalid digest")
            result["files"][key][name] = digest
            folded_files[key].add(name.casefold())
        else:
            raise ValueError("Invalid release manifest record")
    if result.get("format") != "midden-release-v1" or not COMMIT.fullmatch(result.get("commit", "")):
        raise ValueError("Invalid manifest format or source commit")
    release_version(result.get("version"))
    if not result["archives"]:
        raise ValueError("No release archives")
    for key, name in result["archives"].items():
        if name != archive_name(key[0], result["version"], key[1]) or not result["files"][key]:
            raise ValueError("Archive identity or file inventory is invalid")
        validate_member_set(result["files"][key])
    return result


def bundle_inputs(root, revision):
    root = Path(root).resolve()
    commit = subprocess.check_output(
        ["git", "-C", str(root), "rev-parse", "--verify", revision + "^{commit}"],
        text=True,
    ).strip()
    if not re.fullmatch(r"[0-9a-f]{40,64}", commit):
        raise RuntimeError("Release source revision did not resolve to a commit")
    raw = subprocess.check_output(
        ["git", "-C", str(root), "ls-tree", "-r", "-z", "--name-only", commit, "--",
         "LICENSE", "install.ps1", "install.sh", "bundles", "installer"],
    )
    names = sorted(name.decode("utf-8") for name in raw.split(b"\0") if name)
    if not ROOT_FILES <= set(names):
        raise RuntimeError("Required distribution files are absent from the source commit")
    for name in names:
        path = PurePosixPath(name)
        if name not in ROOT_FILES and (
            path.parts[0] not in {"bundles", "installer"} or path.suffix not in BUNDLE_SUFFIXES
        ):
            raise RuntimeError(f"Unexpected tracked bundle file: {name}")
    return [(root.joinpath(*PurePosixPath(name).parts), name) for name in names]


def verify_bundle_members(names, expected):
    names = list(names)
    if len(names) != len(set(names)) or set(names) != set(expected):
        raise RuntimeError("Bundle members differ from the exact reviewed source-commit inputs")


def ui_inputs(root, revision):
    root = Path(root).resolve()
    inputs = [(source, name) for source, name in bundle_inputs(root, revision) if name.startswith("bundles/")]
    selected = [
        ("LICENSE", "LICENSE"), ("NOTICE", "NOTICE"),
        ("apps/midden-ui/start.ps1", "start.ps1"),
        ("apps/midden-ui/start.sh", "start.sh"),
    ]
    for source, name in selected:
        subprocess.run(["git", "-C", str(root), "cat-file", "-e", f"{revision}:{source}"],
                       check=True, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
        inputs.append((root.joinpath(*PurePosixPath(source).parts), name))
    return inputs


def git_blob(root, revision, source):
    root = Path(root).absolute()
    relative = Path(source).absolute().relative_to(root).as_posix()
    safe_member(relative)
    entry = subprocess.check_output(["git", "-C", str(root), "ls-tree", revision, "--", relative]).rstrip(b"\n")
    if b"\t" not in entry:
        raise ValueError("Source file is absent from the claimed commit: " + relative)
    header, recorded = entry.split(b"\t", 1)
    mode, kind, oid = header.decode("ascii").split()
    if recorded.decode("utf-8") != relative or kind != "blob" or mode not in ("100644", "100755"):
        raise ValueError("Release source must be a regular tracked blob: " + relative)
    return subprocess.check_output(["git", "-C", str(root), "cat-file", "blob", oid]), mode == "100755"


def git_blob_bytes(root, revision, source):
    return git_blob(root, revision, source)[0]


def materialize_inputs(root, revision, files, destination):
    root = Path(root).absolute()
    result = []
    for source, name in files:
        raw, executable = git_blob(root, revision, source)
        relative = Path(source).absolute().relative_to(root)
        target = destination / relative
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(raw)
        target.chmod(0o755 if executable else 0o644)
        result.append((target, name))
    return result


def dependency_notices(cwd, package, env):
    """Collect license/notice texts only from the production dependency graph."""
    return format_notices(dependency_modules(cwd, package, env))


def dependency_modules(cwd, package, env):
    """(module path, version, source directory) for each compiled module, plus the Go standard library."""
    template = '{{if .Module}}{{if not .Module.Main}}{{.Module.Path}}|{{.Module.Version}}|{{.Module.Dir}}{{end}}{{end}}'
    raw = subprocess.check_output(["go", "list", "-mod=readonly", "-deps", "-f", template, package],
                                  cwd=cwd, env=env, text=True)
    modules = set()
    for line in raw.splitlines():
        if line:
            path, version, directory = line.split("|", 2)
            if not directory:
                raise RuntimeError(f"Dependency has no reviewable source directory: {path}")
            modules.add((path, version, directory))
    go_root = subprocess.check_output(["go", "env", "GOROOT"], cwd=cwd, env=env, text=True).strip()
    go_version = subprocess.check_output(["go", "env", "GOVERSION"], cwd=cwd, env=env, text=True).strip()
    modules.add(("Go standard library", go_version, go_root))
    return modules


def notice_files(directory):
    return [p for p in Path(directory).iterdir()
            if p.is_file() and not p.is_symlink()
            and p.name.upper().startswith(("LICENSE", "COPYING", "NOTICE"))]


def license_name(text):
    """The standard license a license file grants, or None when unrecognised or ambiguous."""
    words = " ".join(text.split())
    names = [name for name, phrases in LICENSE_SIGNATURES if all(phrase in words for phrase in phrases)]
    return names[0] if len(names) == 1 else None


def xibodev_line(path, directory):
    """Name a xibodev component in one line: display name, repository and the license its files grant."""
    licenses = set()
    for candidate in notice_files(directory):
        if not candidate.name.upper().startswith("NOTICE"):
            name = license_name(candidate.read_text(encoding="utf-8", errors="replace"))
            if name is None:
                raise RuntimeError(f"Unrecognised license file {candidate.name} in xibodev component {path}")
            licenses.add(name)
    if len(licenses) != 1:
        raise RuntimeError(f"xibodev component {path} must declare exactly one recognised license")
    repository = "/".join(path.split("/")[:3])
    title = XIBODEV_NAMES.get(repository, repository.rsplit("/", 1)[-1])
    return f"{title} \u2014 https://{repository} \u2014 {licenses.pop()}\n"


def stray_xibodev_lines(text):
    """Lines of rendered notice bytes that mention a xibodev component outside its one-line entry."""
    return [line for line in text.decode("utf-8", errors="replace").splitlines()
            if "xibodev" in line.lower() and not XIBODEV_ENTRY.fullmatch(line)]


def format_notices(modules):
    """Render THIRD_PARTY_NOTICES.txt from (module path, version, source directory) entries.

    xibodev components are named in one line each without their license or notice texts;
    every other module keeps its complete license and notice files.
    """
    components, sections = [], []
    for path, version, directory in sorted(modules):
        if path.startswith(XIBODEV_PREFIX):
            components.append(xibodev_line(path, directory))
            continue
        candidates = notice_files(directory)
        if not candidates:
            if path == "github.com/kagisearch/kagi-openapi-golang" and version == "v0.0.0-20260526215348-96575e864d62":
                declaration = (Path(directory) / "api" / "openapi.yaml").read_text(encoding="utf-8")
                if not re.search(r"  license:\r?\n    name: Apache-2\.0\r?\n    url: https://www\.apache\.org/licenses/LICENSE-2\.0\.html", declaration):
                    raise RuntimeError("Pinned generated Kagi SDK license declaration changed")
                sections.append((
                    f"\n{'=' * 72}\n{path} {version}\n"
                    "The generated SDK declares Apache-2.0 in its shipped api/openapi.yaml.\n"
                    "It carries no standalone license file; the declared standard license follows.\n"
                    "Source: https://github.com/kagisearch/kagi-openapi-golang/blob/96575e864d62/api/openapi.yaml\n"
                ).encode("utf-8"))
                sections.append((Path(__file__).resolve().parents[1] / "licenses" / "Apache-2.0.txt").read_bytes())
                continue
            raise RuntimeError(f"No license/notice found for compiled dependency {path}@{version}")
        for notice in sorted(candidates):
            heading = f"\n{'=' * 72}\n{path} {version} / {notice.name}\n{'=' * 72}\n".encode("utf-8")
            sections.extend((heading, notice.read_bytes(), b"\n"))
    header = "Third-party licenses and attribution for this compiled artifact.\n"
    if components:
        header += "\n" + "".join(components)
    text = header.encode("utf-8") + b"".join(sections)
    stray = stray_xibodev_lines(text)
    if stray:
        # The lines themselves are not repeated: release logs are public.
        raise RuntimeError(f"Third-party notices mention xibodev components outside their one-line entries ({len(stray)} lines)")
    return text
