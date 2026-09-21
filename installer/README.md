# Install the core and outcome bundles

The distribution has two independent artifacts:

- A platform core archive containing only the binary, `LICENSE`, and `CORE.md`.
- A universal bundle ZIP containing `bundles/`, `install.ps1`, `install.sh`,
  `installer/`, and `LICENSE`, without a core binary.

Extract both before installing. The binary can remain in a different directory
from the extracted bundle. The core does not contain or import installer/bundle
code, and the installer does not unpack or download either artifact.

The installer requires **Python 3.9+** and a core reporting `midden 0.3.0-dev`
or a newer version. The core itself does not require Python. Set `PYTHON` to an
existing interpreter when it is not otherwise discoverable. Nothing downloads
or installs a dependency, changes PATH or profiles, configures models/MCP, or
grants host permissions.

PowerShell checks `python` candidates in PATH order, then `python3`, selecting
the first interpreter that passes the version probe. An explicit `PYTHON`
override is authoritative; an unsuitable override fails without fallback.

## Install

Replace `CORE`, `SHA256`, `BUNDLE_ROOT`, and `PROJECT` with the extracted binary
path, its trusted SHA-256, the extracted bundle ZIP root, and an existing target
project. The checksum is for the **binary**, not the core archive. Quote paths
containing spaces.

PowerShell 5.1+:

```powershell
.\install.ps1 -CoreBinary CORE -CoreSha256 SHA256 -BundleRoot BUNDLE_ROOT -ProjectDir PROJECT
```

Bash:

```text
bash install.sh --core-binary CORE --core-sha256 SHA256 --bundle-root BUNDLE_ROOT --project PROJECT
```

The launchers pass the same options to `installer/install.py`. Both inputs are
verified before the sole executable probe, `version`, runs from a verified
private copy. Expected digests must come from trusted distribution metadata;
hashing an arbitrary local binary does not establish its authenticity.

GNU-style options work through both launchers. `--bundle-dir` / `-BundleDir`
are aliases and also accept the canonical `bundles` directory directly. The
default checksum manifest is `installer/bundle-manifest.json` beside that
directory, so the selected artifact supplies its own metadata. `--manifest`
selects an explicit alternative. Ambiguous direct-plus-nested roots are refused.

Project scope is the default. The project defaults to the current directory.
The binary and receipt go under `.midden/bin`; state is bound to `.midden/state`
without creating or deleting state contents. Override them with `--bin-dir` and
`--state-dir`. Keep binary, skills, and state directories separate.

| `--host` | Project skills | Personal skills with `--scope user` |
|---|---|---|
| `copilot` (default) | `.github/skills` | `.copilot/skills` |
| `agents` | `.agents/skills` | `.agents/skills` |
| `claude` | `.claude/skills` | `.claude/skills` |

Personal scope uses the current home directory, or an explicit `--home-dir`.
Its default binary/state paths are also beneath that base's `.midden` directory.
These are file destinations, not a claim that every host has loaded the skills.
No host executable, sign-in, or mandatory MCP setup is involved in installation.

Outcome folders are named `midden-investigation`, `midden-article`,
`midden-presentation`, and `midden-long-form`. Resources live in the sibling
`midden-shared` directory, preserving relative links without claiming `shared`.
Each installed skill receives a generated footer containing the exact executable
and per-process `MIDDEN_HOME` binding. Generated skills and receipts contain
machine-local paths; keep them out of version control. Canonical sources contain
no machine bindings.

Start with ordinary intent in the host: **"What is worth explaining from this
work?"** Or ask for a tutorial, presentation, or continuation of existing prose.

## Verify, upgrade, remove

Use the same project/scope/host and any custom binary directory:

```text
python -B installer/install.py --project PROJECT --verify
python -B installer/install.py --project PROJECT --core-binary CORE --core-sha256 SHA256 --bundle-root BUNDLE_ROOT --upgrade
python -B installer/install.py --project PROJECT --uninstall
```

An existing receipt retains its state binding when `--state-dir` is omitted.
Relocating or changing bindings requires a separate installation.

Collisions are refused even when bytes match. Upgrade requires an unchanged,
supported receipt; changed or missing owned files block verification, upgrade,
and uninstall rather than discarding edits. Uninstall removes only unchanged
receipt-owned files and its receipt. State, unrelated files, and possibly empty
directories remain. Old module-era receipts/configuration are not migrated or
removed; use a fresh scoped location or the matching old uninstaller.

Operations use an exclusive installation lock and rollback on file-operation
failure. Interrupted operations leave explicit checksum/lock diagnostics.
If rollback cannot safely restore a file, the error identifies retained recovery
backups and their path/checksum map. Do not edit installed files during an operation.
Symbolic-link/reparse paths
are refused; atomic new-file creation requires filesystem hard-link support.

## Distribution metadata and tests

`bundle-manifest.json` lists installable source/destination paths and exact
SHA-256 values. The top-level bundle README is a source index, not a host skill.
Packaging generates metadata from LF-normalized staged sources, consistent with
the repository's `.gitattributes`. Create the staging directories first, then:

```text
python -B installer\make_manifest.py --bundle-dir STAGE\bundles --out STAGE\installer\bundle-manifest.json
```

Use the platform's path separator. The helper's CLI requires both arguments;
`--bundle-dir` also accepts the staged artifact root. It reads the source tree
and writes only the explicit `--out` file: no default checkout destination,
source rewrites, or import bytecode caches. Keep `--out` in staging when packaging.
Ship this manifest with those exact bundle files. Installation never regenerates
checksums implicitly.

From the repository root, using the platform's path separator:

```text
python -B -m unittest discover -s tests/installer -p "test_*.py" -v
python -B -m unittest discover -s tests/bundles -p "test_*.py" -v
```

Set `PANDOC` to an existing renderer for installed-template checks. Installer
unit tests use synthetic bundles and a version-only fake executable, external
temporary directories, and available native launchers. They do not establish
actual-core compatibility or live agent discovery. Run those smoke checks, and
native platform checks for the intended release targets, separately.
