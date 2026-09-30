"""Generate release checksums from the canonical, installable bundle files."""

import sys

# Packaging must not leave import caches beside a checkout-resident helper.
sys.dont_write_bytecode = True

import argparse
from pathlib import Path

from install import InstallError, bundle_manifest, json_bytes, safe_path


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--bundle-dir", required=True, help="Canonical bundles directory or artifact root")
    parser.add_argument("--out", required=True, help="Only output file; its parent must already exist")
    args = parser.parse_args()
    try:
        content = json_bytes(bundle_manifest(safe_path(args.bundle_dir)))
        output = safe_path(args.out)
        if not output.parent.is_dir():
            raise InstallError("The output parent directory must already exist")
        output.write_bytes(content)
    except (InstallError, OSError, ValueError) as error:
        print(f"midden bundle manifest: {error}", file=sys.stderr)
        return 1
    print(f"Wrote bundle checksums: {Path(args.out).name}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
