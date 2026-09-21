"""Verify archive ownership, source revision and checksums; optionally run native core."""
import argparse
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import platform
import subprocess
import sys
import tarfile
import tempfile
import zipfile


def members(path):
    if path.suffix == ".zip":
        with zipfile.ZipFile(path) as archive:
            return [(item.filename, item.file_size, not item.is_dir()) for item in archive.infolist()]
    with tarfile.open(path) as archive:
        return [(item.name, item.size, item.isfile()) for item in archive.getmembers()]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--directory", type=Path, required=True)
    parser.add_argument("--commit", required=True)
    parser.add_argument("--tag")
    parser.add_argument("--smoke", action="store_true")
    args = parser.parse_args()
    root = args.directory
    manifest = json.loads((root / "build-manifest.json").read_text(encoding="utf-8"))
    assert manifest["commit"] == args.commit, "source revision mismatch"
    if args.tag:
        assert args.tag == "v" + manifest["version"], "tag/version mismatch"
    assert manifest["products"] == ["core", "bundle"]
    expected = set(manifest["archives"]) | {"build-manifest.json"}
    checksums = {}
    for line in (root / "SHA256SUMS").read_text(encoding="utf-8").splitlines():
        digest, name = line.split("  ", 1)
        assert name not in checksums and Path(name).name == name
        checksums[name] = digest
    assert set(checksums) == expected
    assert {p.name for p in root.iterdir()} == expected | {"SHA256SUMS"}
    for name, digest in checksums.items():
        assert hashlib.sha256((root / name).read_bytes()).hexdigest() == digest, name
    for name in manifest["archives"]:
        entries = members(root / name)
        names = {entry[0] for entry in entries}
        assert len(names) == len(entries)
        for entry, size, regular in entries:
            path = PurePosixPath(entry)
            assert not path.is_absolute() and ".." not in path.parts and regular and size > 0
        if name.startswith("midden-core_"):
            binary = "midden.exe" if "_windows_" in name else "midden"
            assert names == {binary, "LICENSE", "CORE.md"}, "core archive contains bundle/runtime data"
        else:
            assert name.startswith("midden-bundle_")
            assert {"LICENSE", "install.ps1", "install.sh"} <= names
            assert all(n in {"LICENSE", "install.ps1", "install.sh"} or n.startswith(("bundles/", "installer/")) for n in names)
            assert all(f"bundles/{outcome}/SKILL.md" in names for outcome in ("investigation", "article", "presentation", "long-form"))
    if args.smoke:
        system = {"Windows": "windows", "Linux": "linux", "Darwin": "darwin"}[platform.system()]
        arch = "arm64" if platform.machine().lower() in {"arm64", "aarch64"} else "amd64"
        prefix = f"midden-core_{manifest['version']}_{system}_{arch}"
        matches = [n for n in manifest["archives"] if n.startswith(prefix + ".")]
        assert len(matches) == 1, "native core archive missing"
        with tempfile.TemporaryDirectory(prefix="midden-native-") as temporary:
            stage = Path(temporary)
            archive = root / matches[0]
            if archive.suffix == ".zip":
                with zipfile.ZipFile(archive) as z:
                    z.extractall(stage)
            else:
                with tarfile.open(archive) as tar:
                    tar.extractall(stage, filter="data")
            binary = stage / ("midden.exe" if system == "windows" else "midden")
            binary.chmod(0o755)
            env = dict(os.environ, MIDDEN_HOME=str(stage / "state"))
            actual = subprocess.check_output([str(binary), "version"], env=env, text=True)
            assert actual.strip() == "midden " + manifest["version"]
            binary_digest = hashlib.sha256(binary.read_bytes()).hexdigest()
            assert binary_digest == manifest["core_binaries"][system + "/" + arch]
            subprocess.run([str(binary), "help"], env=env, check=True, stdout=subprocess.DEVNULL)
            assert not (stage / "state").exists(), "passive core invocation wrote state"
            bundle_name = f"midden-bundle_{manifest['version']}.zip"
            bundle_root = stage / "bundle"
            with zipfile.ZipFile(root / bundle_name) as bundle:
                bundle.extractall(bundle_root)
            project = stage / "project"
            project.mkdir()
            installer = bundle_root / "installer" / "install.py"
            common = [sys.executable, "-B", str(installer), "--project", str(project)]
            subprocess.run(common + ["--core-binary", str(binary), "--core-sha256", binary_digest,
                "--bundle-root", str(bundle_root)], env=env, check=True)
            subprocess.run(common + ["--verify"], env=env, check=True)
            subprocess.run(common + ["--uninstall"], env=env, check=True)
    print("PASS: independent products, source revision, checksums, allowlists" +
          (", native core and actual bundle installation" if args.smoke else ""))


if __name__ == "__main__":
    main()
