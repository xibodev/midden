#!/bin/sh
set -eu
directory=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
if [ ! -x "$directory/midden-ui" ]; then
    printf '%s\n' "Packaged UI executable is missing or not executable: $directory/midden-ui" >&2
    exit 1
fi
exec "$directory/midden-ui" "$@"
