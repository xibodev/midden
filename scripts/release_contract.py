"""Release inputs are reviewed Git objects, not everything in local folders."""
from pathlib import Path, PurePosixPath
import re
import subprocess


ROOT_FILES = {"LICENSE", "install.ps1", "install.sh"}
BUNDLE_SUFFIXES = {".md", ".py", ".ps1", ".sh", ".json", ".yaml", ".yml", ".css", ".svg", ".png", ".lua"}


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
