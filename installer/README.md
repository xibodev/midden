# Install the core and outcome bundles

The distribution has two independent artifacts:

- A platform core archive containing only the binary, `LICENSE`, and `CORE.md`.
- A universal bundle ZIP containing `bundles/`, `install.ps1`, `install.sh`,
  `installer/`, and `LICENSE`, without a core binary.

Use `--distribution-dir` to select the native core and matching bundle from a
local release/staging directory, or supply already extracted inputs explicitly.
The core does not contain or import installer/bundle code. Nothing is downloaded.

The installer requires **Python 3.9+** and a core reporting `midden 0.3.0-dev`
or a newer version. The core itself does not require Python. Set `PYTHON` to an
existing interpreter when it is not otherwise discoverable. Nothing downloads
or installs a dependency, changes PATH or profiles, configures models/MCP, or
grants host permissions.

PowerShell checks `python` candidates in PATH order, then `python3`, selecting
the first interpreter that passes the version probe. An explicit `PYTHON`
override is authoritative; an unsuitable override fails without fallback.

## Install from a local distribution

Start the launcher from a reviewed source checkout or a **verified, extracted
bundle artifact**. The archives do not contain an independently runnable
bootstrap outside that bundle; verify it before extracting/running its launcher.
Keep `build-manifest.json`, `SHA256SUMS`, the native core archive and the matching
bundle ZIP in the release directory. For example, a Windows AMD64 build has:

```text
build-manifest.json
SHA256SUMS
midden-core_0.3.0-dev_windows_amd64.zip
midden-bundle_0.3.0-dev.zip
```

The project must already exist. From the checkout or extracted bundle root,
preview and then install (PowerShell 5.1+):

```powershell
.\install.ps1 -DistributionDir 'C:\midden-release' -ProjectDir 'C:\work\example' -DryRun
.\install.ps1 -DistributionDir 'C:\midden-release' -ProjectDir 'C:\work\example'
```

Bash accepts the same GNU-style options:

```text
bash install.sh --distribution-dir RELEASE_DIR --project PROJECT --dry-run
bash install.sh --distribution-dir RELEASE_DIR --project PROJECT
```

No manual core extraction, binary checksum lookup or `--bundle-root` is needed.
`--distribution-dir` / `-DistributionDir` cannot be combined with the explicit
core, bundle or `--manifest` options.

The installer verifies the build manifest's digest in `SHA256SUMS`, requires
archive names consistent with its version/targets, and selects the exact native
core plus universal bundle. It verifies both selected archive digests, the
binary digest in `core_binaries`, and canonical source digests in the archived
`installer/bundle-manifest.json` **before execution**. Archive snapshots are
extracted into a private external temporary directory; installer scripts inside
that snapshot are not executed. The sole core probe is `version`, and its
reported version must exactly match the build manifest before installation.
The existing receipt, collision, lock and rollback rules apply.

Windows AMD64 and Linux/macOS AMD64/ARM64 naming follows the existing release
contract. Unsupported systems, architectures or missing native targets fail
instead of falling back to a foreign binary. Only the selected archive bytes
are checked; this is not full multi-target or source-revision release validation.
Extra local files are not selected, executed or installed.

Archive members must be ordinary files with portable, unambiguous relative
paths. Links, devices, traversal, directory entries and case collisions are
refused. Each selected archive is limited to 256 MiB compressed and expanded,
with at most 4,096 members; each external metadata file is limited to 1 MiB.
Temporary extraction must be outside the project, distribution, installer
source and installed binary/skill/state directories. Temporary-path aliases are
resolved before these containment checks. Set `TMPDIR`/`TEMP` to an external
location if the default is unsuitable. Successful operations and
ordinary failures clean up temporary files; recovery failures retain backups
as described below.

**Local checksums establish integrity, not cryptographic authenticity.** Obtain
the launchers, archives and metadata through a trusted distribution channel.
Replacing a payload and its local checksums can satisfy these checks; this mode
does not validate signatures or establish who built the artifacts.

## Install from explicit extracted inputs

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

## Read-only plan

Add `--dry-run` / `--plan` (`-DryRun` / `-Plan`) to either input route, or to
`--upgrade`, `--verify` or `--uninstall`. It prints a JSON plan containing the
host/scope, project or home, bin/skills/state bindings, every destination and its
create/replace/remove/verify action, and required dependencies. Distribution
plans also identify the selected target, archive names and declared version.

Plans read and verify supplied inputs and existing ownership, so corrupt inputs,
unowned collisions and changed/missing owned files fail rather than producing a
successful plan. They do not extract, create directories/receipts/locks, write
files, or run the core or optional tools. The launchers still need to start and
select a suitable Python interpreter.

Executable compatibility, version-probe results, filesystem writability and
hard-link support are **not probed** by a plan. Rendering tools such as Pandoc
are outcome-specific, not installation prerequisites; see the installed
`midden-shared/tools.md`. A plan is not a reservation or installation guarantee:
applying the operation repeats the checks. Plan output contains machine-local
paths; keep it out of public version control.

## Verify, upgrade, remove

Use the same project/scope/host and any custom binary directory:

```text
python -B installer/install.py --project PROJECT --verify
python -B installer/install.py --project PROJECT --core-binary CORE --core-sha256 SHA256 --bundle-root BUNDLE_ROOT --upgrade
python -B installer/install.py --project PROJECT --uninstall
```

For a distribution upgrade, replace the explicit input options with
`--distribution-dir NEW_RELEASE_DIR`, for example:

```powershell
.\install.ps1 -DistributionDir 'C:\midden-release' -ProjectDir 'C:\work\example' -Upgrade -DryRun
.\install.ps1 -DistributionDir 'C:\midden-release' -ProjectDir 'C:\work\example' -Upgrade
.\install.ps1 -ProjectDir 'C:\work\example' -Verify
.\install.ps1 -ProjectDir 'C:\work\example' -Uninstall
```

Verify and uninstall need no source archives or extracted source files. As with
the explicit route, any supplied input options are unused for these operations.

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
Symbolic-link/reparse source and installation paths are refused; atomic new-file
creation requires filesystem hard-link support.

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
python -B -m unittest discover -s tests/release -p "test_*.py" -v
python -B -m unittest discover -s tests/bundles -p "test_*.py" -v
```

Set `PANDOC` to an existing renderer for installed-template checks. Installer
unit tests use synthetic bundles and a version-only fake executable, external
temporary directories, and available native launchers. Distribution fixtures
cover ZIP/TAR selection, checksums, unsafe members, limits, version mismatch and
receipt-owned lifecycle behavior. Plan process tests deny writes and process
launches with Python's audit hook. These tests do not establish actual-core
compatibility or live agent discovery. Run those smoke checks, and native
platform checks for the intended release targets, separately.
