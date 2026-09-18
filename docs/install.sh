#!/usr/bin/env bash
# curl -fsSL https://xibodev.github.io/midden/install.sh | bash
# Parse the complete function before executing anything from piped input.
midden_bootstrap() (
  set -euo pipefail
  version=v0.2.0
  base="https://github.com/xibodev/midden/releases/download/$version"
  case "$(uname -s)" in Linux|Darwin) ;; *) echo 'Use install.ps1 on Windows.' >&2; exit 1;; esac
  command -v curl >/dev/null || { echo 'curl is required.' >&2; exit 1; }
  if command -v sha256sum >/dev/null; then hash_command=sha256sum
  elif command -v shasum >/dev/null; then hash_command=shasum
  else echo 'sha256sum or shasum is required.' >&2; exit 1; fi
  temporary=$(mktemp -d)
  temporary=$(cd -- "$temporary" && pwd -P)
  trap 'rm -rf -- "$temporary"' EXIT
  for file in SHA256SUMS install.sh manifest.tsv; do
    curl -fsSL "$base/$file" -o "$temporary/$file"
  done
  for file in install.sh manifest.tsv; do
    expected=$(awk -v name="$file" '$2==name {print $1}' "$temporary/SHA256SUMS")
    [[ "$expected" =~ ^[a-f0-9]{64}$ ]] || { echo "Missing/invalid/duplicate checksum: $file" >&2; exit 1; }
    if [[ "$hash_command" = shasum ]]; then actual=$(shasum -a 256 "$temporary/$file")
    else actual=$(sha256sum "$temporary/$file"); fi
    [[ "${actual%% *}" = "$expected" ]] || { echo "Checksum mismatch: $file" >&2; exit 1; }
  done
  echo "Starting verified Midden $version installer..."
  interactive=1
  for arg in "$@"; do
    case "$arg" in --yes|--non-interactive|--verify|--help|-h) interactive=0;; esac
  done
  if (( interactive )); then
    # stdin contains the bootstrap when piped; prompts must read the terminal.
    bash "$temporary/install.sh" --version "$version" "$@" < /dev/tty
  else
    bash "$temporary/install.sh" --version "$version" "$@" < /dev/null
  fi
)
midden_bootstrap "$@"
