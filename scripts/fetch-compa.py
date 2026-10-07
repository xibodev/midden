"""Fetch the pinned compa-kernel, as the App release takes it, for this computer or a given target."""
import argparse
from pathlib import Path
import platform
import sys

sys.dont_write_bytecode = True
from release_contract import TARGETS, take_compa


def native_target():
    system = {"Windows": "windows", "Linux": "linux", "Darwin": "darwin"}[platform.system()]
    return system + "/" + ("arm64" if platform.machine().lower() in {"arm64", "aarch64"} else "amd64")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--out", type=Path, required=True, help="Folder for compa-kernel and Compa's LICENSE and NOTICE")
    parser.add_argument("--target", choices=TARGETS, help="Defaults to this computer")
    parser.add_argument("--downloads", type=Path, help="Folder that keeps Compa's release archives; defaults to --out")
    args = parser.parse_args()
    args.out.mkdir(parents=True, exist_ok=True)
    downloads = args.downloads or args.out
    downloads.mkdir(parents=True, exist_ok=True)
    for path, _ in take_compa(args.target or native_target(), downloads, args.out):
        print(path.resolve())


if __name__ == "__main__":
    main()
