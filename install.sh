#!/usr/bin/env bash
# Python is an explicit prerequisite; this launcher installs no dependencies.
set -euo pipefail
script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
python=${PYTHON:-}
if [[ -z "$python" ]]; then
  if command -v python3 >/dev/null 2>&1; then
    python=$(command -v python3)
  elif command -v python >/dev/null 2>&1; then
    python=$(command -v python)
  else
    printf '%s\n' 'Python 3.9+ is required. Set PYTHON to an existing interpreter; nothing was installed.' >&2
    exit 1
  fi
fi
if ! "$python" -c 'import sys; sys.exit(0 if sys.version_info >= (3, 9) else 1)'; then
  printf '%s\n' 'Python 3.9+ is required; nothing was installed.' >&2
  exit 1
fi
[[ -f "$script_dir/installer/install.py" ]] || {
  printf '%s\n' 'Keep the installer directory beside install.sh.' >&2
  exit 1
}
exec "$python" -B "$script_dir/installer/install.py" "$@"
