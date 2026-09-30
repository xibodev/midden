"""Exercise downloaded native bootstrap products in a disposable user profile."""
import argparse
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile


def invoke(command, env, success=True):
    result = subprocess.run(command, env=env, capture_output=True, text=True,
                            encoding="utf-8", errors="replace", timeout=120)
    if (result.returncode == 0) != success:
        raise RuntimeError("Unexpected bootstrap outcome:\n" + result.stdout[-5000:] + result.stderr[-5000:])
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--directory", type=Path, required=True)
    args = parser.parse_args()
    release = args.directory.resolve()
    version = json.loads((release / "build-manifest.json").read_text(encoding="utf-8"))["version"]
    if os.name == "nt":
        shells = list(dict.fromkeys(p for name in ("powershell", "pwsh") if (p := shutil.which(name))))
    else:
        shells = ["/bin/sh"]
    if not shells:
        raise SystemExit("No supported installer shell is available")
    for shell in shells:
        for mode in ("ui", "core", "cli"):
            with tempfile.TemporaryDirectory(prefix="midden-bootstrap-smoke-") as temporary:
                root = Path(temporary).resolve()
                home, data, install, project = root / "home", root / "data", root / "install", root / "project"
                home.mkdir()
                data.mkdir()
                project.mkdir()
                marker = data / "user-work.txt"
                marker.write_text("Synthetic user-owned data; preserve it.\n", encoding="utf-8")
                env = dict(os.environ, HOME=str(home), USERPROFILE=str(home),
                           LOCALAPPDATA=str(data), XDG_DATA_HOME=str(data))
                if os.name == "nt":
                    common = [shell, "-NoProfile", "-NonInteractive", "-File", str(release / "install.ps1"),
                              "-Mode", mode, "-InstallDir", str(install), "-ProjectDir", str(project),
                              "-DistributionDir", str(release), "-NoPath", "-NoLaunch", "-NoOpen"]
                    flag = lambda name: "-" + name
                else:
                    common = [shell, str(release / "install.sh"), "--mode", mode, "--install-dir", str(install),
                              "--project", str(project), "--distribution-dir", str(release),
                              "--no-path", "--no-launch", "--no-open"]
                    flag = lambda name: "--" + {"DryRun": "dry-run"}.get(name, name.lower())
                invoke(common + [flag("DryRun")], env)
                if install.exists() or list(project.iterdir()):
                    raise RuntimeError("Dry-run modified installation/project contents")
                invoke(common, env)
                invoke(common + [flag("Verify")], env)
                extension = ".exe" if os.name == "nt" else ""
                binary = install / ("midden" + extension)
                actual = subprocess.check_output([str(binary), "version"], env=env, text=True).strip()
                if actual != "midden " + version:
                    raise RuntimeError("Installed core has the wrong release identity")
                if mode == "ui":
                    ui = install / ("midden-ui" + extension)
                    actual = subprocess.check_output([str(ui), "--version"], env=env, text=True).strip()
                    if actual != "midden-ui " + version:
                        raise RuntimeError("Installed UI has the wrong release identity")
                unowned = (project if mode == "cli" else install) / "unowned.txt"
                unowned.write_text("Synthetic unowned file.\n", encoding="utf-8")
                invoke(common + [flag("Upgrade")], env)
                original = binary.read_bytes()
                binary.write_bytes(original + b"\nsynthetic local modification\n")
                invoke(common + [flag("Verify")], env, success=False)
                invoke(common + [flag("Uninstall")], env, success=False)
                if binary.read_bytes() != original + b"\nsynthetic local modification\n":
                    raise RuntimeError("Rejected removal altered the modified file")
                binary.write_bytes(original)
                invoke(common + [flag("Uninstall")], env)
                if binary.exists() or not unowned.exists() or marker.read_text() != "Synthetic user-owned data; preserve it.\n":
                    raise RuntimeError("Uninstall did not preserve the ownership boundary")
                print(f"PASS: {Path(shell).name} {mode} plan/install/verify/upgrade/refusal/uninstall")


if __name__ == "__main__":
    main()
