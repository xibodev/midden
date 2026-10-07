"""Exercise the published installers against a verified release in a disposable user profile."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import shutil
import subprocess
import tempfile
import urllib.request


def invoke(command, env, success=True):
    result = subprocess.run(command, env=env, capture_output=True, text=True,
                            encoding="utf-8", errors="replace", timeout=600)
    if (result.returncode == 0) != success:
        raise RuntimeError("Unexpected installer outcome:\n" + result.stdout[-5000:] + result.stderr[-5000:])
    return result


def native_target():
    system = {"Windows": "windows", "Linux": "linux", "Darwin": "darwin"}[platform.system()]
    return system + "/" + ("arm64" if platform.machine().lower() in {"arm64", "aarch64"} else "amd64")


def local_distribution(release, destination, target):
    """A copy of the release plus the pinned Pandoc archive, so the app mode installs offline."""
    shutil.copytree(release, destination)
    for line in (release / "manifest.tsv").read_text(encoding="utf-8").splitlines():
        fields = line.split("\t")
        if fields[:3] == ["fetch", "pandoc", target]:
            url, expected = fields[3], fields[4]
            archive = destination / url.rsplit("/", 1)[1]
            with urllib.request.urlopen(url, timeout=300) as response, archive.open("wb") as output:
                shutil.copyfileobj(response, output)
            if hashlib.sha256(archive.read_bytes()).hexdigest() != expected:
                raise RuntimeError("The pinned Pandoc download does not match its SHA-256")
            return
    raise RuntimeError("The release manifest pins no Pandoc for " + target)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--directory", type=Path, required=True)
    args = parser.parse_args()
    release = args.directory.resolve()
    version = json.loads((release / "build-manifest.json").read_text(encoding="utf-8"))["version"]
    windows = os.name == "nt"
    if windows:
        shells = list(dict.fromkeys(p for name in ("powershell", "pwsh") if (p := shutil.which(name))))
    else:
        shells = ["/bin/sh"]
    if not shells:
        raise SystemExit("No supported installer shell is available")
    extension = ".exe" if windows else ""
    with tempfile.TemporaryDirectory(prefix="midden-install-smoke-dist-") as cache:
        distribution = Path(cache).resolve() / "release"
        local_distribution(release, distribution, native_target())
        for shell in shells:
            for mode in ("app", "core", "bundle"):
                with tempfile.TemporaryDirectory(prefix="midden-install-smoke-") as temporary:
                    root = Path(temporary).resolve()
                    home, data, install, project = root / "home", root / "data", root / "install", root / "project"
                    for path in (home, data, project):
                        path.mkdir()
                    marker = data / "user-work.txt"
                    marker.write_text("Synthetic user-owned data; preserve it.\n", encoding="utf-8")
                    env = dict(os.environ, HOME=str(home), USERPROFILE=str(home), APPDATA=str(home / "roaming"),
                               LOCALAPPDATA=str(data), XDG_DATA_HOME=str(data))
                    if windows:
                        base = [shell, "-NoProfile", "-NonInteractive", "-File", str(release / "install.ps1"),
                                "-InstallDir", str(install), "-DistributionDir", str(distribution),
                                "-NoPath", "-NoLaunch", "-NoOpen"]
                        flag = lambda name: "-" + name
                    else:
                        base = [shell, str(release / "install.sh"), "--install-dir", str(install),
                                "--distribution-dir", str(distribution), "--no-path", "--no-launch", "--no-open"]
                        flag = lambda name: "--" + {"DryRun": "dry-run"}.get(name, name.lower())
                    common = base + [flag("Mode"), mode]
                    if mode == "bundle":
                        common += [flag("Harness"), "claude"]
                    invoke(common + [flag("DryRun")], env)
                    if install.exists() or list(project.iterdir()) or (home / ".claude").exists():
                        raise RuntimeError("Dry-run changed the installation, a project or a harness folder")
                    invoke(common, env)
                    invoke(base + [flag("Verify")], env)
                    binary = install / ("midden" + extension)
                    actual = subprocess.check_output([str(binary), "version"], env=env, text=True).strip()
                    if actual != "midden " + version:
                        raise RuntimeError("Installed core has the wrong release identity")
                    if mode == "app":
                        app = install / ("midden-ui" + extension)
                        actual = subprocess.check_output([str(app), "--version"], env=env, text=True).strip()
                        if actual != "midden-ui " + version:
                            raise RuntimeError("Installed app has the wrong release identity")
                        pandoc = install / "app" / "tools" / ("pandoc" + extension)
                        actual = subprocess.check_output([str(pandoc), "--version"], env=env, text=True)
                        if not actual.startswith("pandoc "):
                            raise RuntimeError("The app's Pandoc does not run")
                    if mode == "bundle":
                        skill = home / ".claude" / "skills" / "midden-investigation" / "SKILL.md"
                        if not skill.is_file() or str(install) in skill.read_text(encoding="utf-8"):
                            raise RuntimeError("Skills were not copied unchanged into the harness folder")
                        invoke(base + [flag("Mode"), "bundle", flag("Harness"), "copilot", flag("Project"), str(project)], env)
                        if not (project / ".github" / "skills" / "midden-article" / "SKILL.md").is_file():
                            raise RuntimeError("Project skills were not installed")
                    unowned = install / "unowned.txt"
                    unowned.write_text("Synthetic unowned file.\n", encoding="utf-8")
                    invoke(base + [flag("Upgrade")], env)
                    original = binary.read_bytes()
                    binary.write_bytes(original + b"\nsynthetic local modification\n")
                    invoke(base + [flag("Verify")], env, success=False)
                    invoke(base + [flag("Uninstall")], env, success=False)
                    if binary.read_bytes() != original + b"\nsynthetic local modification\n":
                        raise RuntimeError("Rejected removal altered the modified file")
                    binary.write_bytes(original)
                    invoke(base + [flag("Uninstall")], env)
                    if (binary.exists() or not unowned.exists() or (home / ".claude" / "skills" / "midden-article").exists()
                            or (project / ".github" / "skills" / "midden-article").exists()
                            or marker.read_text(encoding="utf-8") != "Synthetic user-owned data; preserve it.\n"):
                        raise RuntimeError("Uninstall did not keep to Midden's own files")
                    print(f"PASS: {Path(shell).name} {mode} plan/install/verify/upgrade/refusal/uninstall")


if __name__ == "__main__":
    main()
