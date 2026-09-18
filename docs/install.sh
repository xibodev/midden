#!/bin/sh
# curl -fsSL https://xibodev.github.io/midden/install.sh | sh
# Parse the complete function before executing anything from piped input.
midden_bootstrap() (
  set -eu
  version=v0.2.3
  version=${MIDDEN_VERSION:-$version}
  case "$version" in v[0-9]*.[0-9]*.[0-9]*) ;; *) echo 'MIDDEN_VERSION must be a release tag.' >&2; exit 1;; esac
  case "$version" in *[!a-zA-Z0-9.-]*) echo 'Invalid MIDDEN_VERSION.' >&2; exit 1;; esac
  base="https://github.com/xibodev/midden/releases/download/$version"
  case "$(uname -s)" in Linux|Darwin) ;; *) echo 'Use install.ps1 on Windows.' >&2; exit 1;; esac
  command -v curl >/dev/null || { echo 'curl is required.' >&2; exit 1; }
  command -v bash >/dev/null || { echo 'Bash is required for setup. Install bash with your system package manager, then rerun.' >&2; exit 1; }
  if command -v sha256sum >/dev/null; then hash_command=sha256sum
  elif command -v shasum >/dev/null; then hash_command=shasum
  else echo 'sha256sum or shasum is required.' >&2; exit 1; fi
  temporary=$(mktemp -d)
  temporary=$(cd -- "$temporary" && pwd -P)
  trap 'rm -rf -- "$temporary"' EXIT
  for file in SHA256SUMS install.sh manifest.tsv; do
    curl --retry 3 --connect-timeout 15 --max-time 120 -fsSL "$base/$file" -o "$temporary/$file"
  done
  for file in install.sh manifest.tsv; do
    expected=$(awk -v name="$file" '$2==name {print $1}' "$temporary/SHA256SUMS")
    case "$expected" in ''|*[!a-f0-9]*) echo "Missing/invalid/duplicate checksum: $file" >&2; exit 1;; esac
    [ "${#expected}" -eq 64 ] || { echo "Invalid checksum: $file" >&2; exit 1; }
    if [ "$hash_command" = shasum ]; then actual=$(shasum -a 256 "$temporary/$file")
    else actual=$(sha256sum "$temporary/$file"); fi
    [ "${actual%% *}" = "$expected" ] || { echo "Checksum mismatch: $file" >&2; exit 1; }
  done
  echo "Starting verified Midden $version installer..."
  interactive=1
  [ "${MIDDEN_YES:-0}" != 1 ] || interactive=0
  for arg in "$@"; do
    case "$arg" in --yes|--non-interactive|--verify|--help|-h) interactive=0;; esac
  done
  if [ "$interactive" = 1 ]; then
    # stdin contains the bootstrap when piped; prompts must read the terminal.
    if ( : < /dev/tty ) 2>/dev/null; then
      bash "$temporary/install.sh" --version "${MIDDEN_VERSION:-$version}" "$@" < /dev/tty
    else
      echo 'No interactive terminal. Set MIDDEN_YES=1 and MIDDEN_HOSTS=claude-code (or your host) for unattended setup.' >&2
      exit 1
    fi
  else
    bash "$temporary/install.sh" --version "${MIDDEN_VERSION:-$version}" "$@" < /dev/null
  fi
)
midden_bootstrap "$@"
