# Install Midden v0.3.1

Choose the **app** unless you specifically want the deterministic core or want
to install Midden into an existing AI CLI.

| Mode | What you get | Prerequisites |
|---|---|---|
| `ui` (default) | `midden-ui`, matching `midden` core and the unchanged canonical bundle | A supported native release target and a browser; no Go, Node.js or Python |
| `core` | The deterministic `midden` executable | A supported native release target; no model or AI host |
| `cli` | A verified core and host-bound outcome bundle in an existing project | Python 3.9+, filesystem hard-link support, and a compatible AI CLI installed/authenticated separately |

All modes preserve the distinction between core data operations and host AI
execution. The bootstrap downloads Midden release payloads, not language
runtimes, AI hosts or optional rendering dependencies. It requires no
administrator access and does not change execution policy.

For an existing installation, read [Migration](MIGRATION.md) first. Stop a
running app before an upgrade.

## App quick install

Review the [PowerShell script](https://xibodev.github.io/midden/install.ps1) or
[shell script](https://xibodev.github.io/midden/install.sh) before executing it.

**Windows, PowerShell**

```powershell
irm https://xibodev.github.io/midden/install.ps1 | iex
```

**macOS or Linux**

```sh
curl -fsSL https://xibodev.github.io/midden/install.sh | sh
```

The default is a per-user app installation followed by launch. The browser opens
on the local host. **Leave the terminal running; Ctrl+C stops the host.** Closing
the browser tab alone is not the same as stopping the process.

Open **Settings**, choose an OpenAI-compatible or Anthropic-compatible
connection, and supply an official API key or a compatible local endpoint.
Local servers may not require a key. **Find models** is a catalog lookup;
**Check model** is an explicit, small tool-capability inference call that uses
provider usage without reading workspace files. You can enter the exact model
ID yourself. **Save settings** enables sending when a model has been selected.

Native Copilot/Codex account sign-in is not included in this app. Using Copilot
CLI or another authenticated AI CLI is a separate entrance below, not an app
authentication workaround.

## Reviewable and pinned installation

To pass options, download a script, inspect it, then run that local copy. These
examples pin both the bootstrap and payload version:

```powershell
Invoke-WebRequest -Uri https://github.com/xibodev/midden/releases/download/v0.3.1/install.ps1 -OutFile .\midden-install.ps1
Get-Content .\midden-install.ps1
.\midden-install.ps1 -Version v0.3.1 -NoLaunch
```

```sh
curl -fsSL https://github.com/xibodev/midden/releases/download/v0.3.1/install.sh -o midden-install.sh
cat midden-install.sh
sh midden-install.sh --version v0.3.1 --no-launch
```

If a script or application is blocked by organizational policy, stop and seek
administrator or publisher review. A different shell, launcher or archive route
is not a workaround for that block.

The same bootstraps are available as
[latest PowerShell](https://github.com/xibodev/midden/releases/latest/download/install.ps1)
and [latest shell](https://github.com/xibodev/midden/releases/latest/download/install.sh)
release assets. A tag-specific `/releases/download/v0.3.1/...` URL is the pinned
alternative to the moving `latest` URL.

### Release candidate

The prerelease channel is **v0.3.1-rc.1**. Select it explicitly; do not use
`latest` as a prerelease selector. Download the corresponding
[RC PowerShell script](https://github.com/xibodev/midden/releases/download/v0.3.1-rc.1/install.ps1)
or [RC shell script](https://github.com/xibodev/midden/releases/download/v0.3.1-rc.1/install.sh),
review it, then pass `-Version v0.3.1-rc.1` or `--version v0.3.1-rc.1`.
Use a separate install directory and separate workspace/state when evaluating
an RC alongside an existing installation.

## Locations and launching

| Platform | Default app/core installation | Default app data root |
|---|---|---|
| Windows | `%LOCALAPPDATA%\Programs\Midden` | `%LOCALAPPDATA%\Midden` |
| macOS | `~/.local/lib/midden` | `~/Library/Application Support/Midden` |
| Linux | `~/.local/lib/midden` | `$XDG_DATA_HOME/midden`, or `~/.local/share/midden` when unset |

The app keeps its workspace and host-state directories under the data root,
**outside the installation**. Preserve that data root when updating or removing
the executables. The separate core cache and AI CLI project state are described
in [Configuration](CONFIGURATION.md).

After installation, use a new terminal if its command path has not refreshed:

```text
midden-ui
midden-ui --no-open
midden-ui --version
midden version
```

`midden-ui --version` prints `midden-ui VERSION` without reading or writing
application state. A normal no-argument launch resolves the matching sibling
core and bundle, uses the per-user data defaults and opens the browser.
`--no-open` suppresses only browser opening; the host still runs in the foreground
until you stop it.

To choose a workspace and host state explicitly, run from a working directory
outside the installation:

```powershell
midden-ui --workspace .\example-work --state .\example-state --no-open
```

```sh
midden-ui --workspace ./example-work --state ./example-state --no-open
```

For compatibility, an explicit `--workspace WORKSPACE` **without `--state`**
retains host state at `WORKSPACE\.midden-ui` (`WORKSPACE/.midden-ui` on Unix).
It does not relocate that existing state to the new per-user default.

Keep the installation separate from working data. Set both path flags if you
want workspace and state in separate directories. If you opted out of
command-path setup with `-NoPath` / `--no-path`, invoke the executable by its
installed path instead.

## Install into your AI CLI

Use the reviewed local bootstrap from above and an existing project:

```powershell
.\midden-install.ps1 -Mode cli -Version v0.3.1 -ProjectDir . -Host copilot
```

```sh
sh midden-install.sh --mode cli --version v0.3.1 --project . --host copilot
```

The project defaults to the current directory. Supported `Host` values are
`copilot` (default), `claude` and `agents`. This route delegates to the existing
Python 3.9+ receipt-based installer; it does not replace that installer with a
new ownership scheme.

The installer verifies core/bundle inputs, binds installed guidance to the
scoped core executable, and records the files it owns. It does not sign in to
the AI host, configure its provider or MCP servers, grant permissions, or read
your session material as an installation step.

The [Python backend guide](../installer/README.md) documents advanced local
inputs, receipt semantics and recovery without changing the public bootstrap's
default app mode.

Start a fresh conversation in the project. Ask for an investigation, article,
tutorial, presentation or continuation of existing prose in ordinary language.
The host follows the bundle and produces files; you do not need to orchestrate
internal APIs. Its own authentication and model support are independent of the
Midden app.

Pandoc, browser inspection and other renderers are **optional, outcome-specific**
tools. Follow the [bundle tool requirements](../bundles/midden-shared/tools.md)
when an outcome needs them. The bootstrap does not install them automatically.

## Core only

```powershell
.\midden-install.ps1 -Mode core -Version v0.3.1
```

```sh
sh midden-install.sh --mode core --version v0.3.1
```

Run `midden help` and see [Core commands](CORE.md). The core runs without a
model, provider configuration, AI host or special build mode.

## Bootstrap options

Use GNU-style names with `sh midden-install.sh`; PowerShell uses the names in
the first column. In particular, Unix project selection is **`--project`**,
not `--project-dir`.

| PowerShell | Shell | Purpose |
|---|---|---|
| `-Mode ui\|core\|cli` | `--mode ui\|core\|cli` | Select the entrance; default `ui` |
| `-Version TAG` | `--version TAG` | Select an exact release, including an explicit RC tag |
| `-InstallDir PATH` | `--install-dir PATH` | Choose the app/core installation directory |
| `-ProjectDir PATH` | `--project PATH` | Choose the existing AI CLI project; default current directory |
| `-Host NAME` | `--host NAME` | CLI host layout: `copilot`, `claude` or `agents` |
| `-DistributionDir PATH` | `--distribution-dir PATH` | Use matching local release assets/metadata; required for CLI Verify/Uninstall through the bootstrap |
| `-NoPath` | `--no-path` | Skip command-path setup |
| `-NoLaunch` | `--no-launch` | Install without starting the app |
| `-NoOpen` | `--no-open` | Suppress automatic browser opening when launching the app |
| `-Upgrade` | `--upgrade` | Update an existing owned installation |
| `-Verify` | `--verify` | Check the existing installation's ownership and file integrity |
| `-Uninstall` | `--uninstall` | Remove unchanged owned installation files, not working data |
| `-DryRun` | `--dry-run` | Inspect intended actions without installing or launching |

A dry run is not a runtime test or a reservation of the destination. Applying
the operation repeats its checks. Use the same mode, destination and CLI
project/host selection for later lifecycle operations.

## Verify, update and remove

In **`ui` and `core` modes**, Verify and Uninstall use the installed receipt
without source files, release archives or network access. Upgrades still need
the new release payloads. Examples for the default app installation, using a
reviewed local script:

```powershell
.\midden-install.ps1 -Verify
.\midden-install.ps1 -Version v0.3.1 -Upgrade -NoLaunch
.\midden-install.ps1 -Uninstall -DryRun
.\midden-install.ps1 -Uninstall
```

```sh
sh midden-install.sh --verify
sh midden-install.sh --version v0.3.1 --upgrade --no-launch
sh midden-install.sh --uninstall --dry-run
sh midden-install.sh --uninstall
```

Repeat `-InstallDir` / `--install-dir` for a custom app/core installation.
For core-only installations, also select `-Mode core` / `--mode core`.

**CLI Verify/Uninstall require a reviewed matching distribution directory** so
the bootstrap can use its matching Python backend. It deliberately does not
fetch or run a different backend implicitly. Keep the same project and host
selection used for installation:

```powershell
.\midden-install.ps1 -Mode cli -DistributionDir .\matching-release -ProjectDir . -Host copilot -Verify
.\midden-install.ps1 -Mode cli -DistributionDir .\matching-release -ProjectDir . -Host copilot -Uninstall
```

```sh
sh midden-install.sh --mode cli --distribution-dir ./matching-release --project . --host copilot --verify
sh midden-install.sh --mode cli --distribution-dir ./matching-release --project . --host copilot --uninstall
```

Alternatively, invoke an already extracted, reviewed matching
`installer/install.py` directly as described in the
[Python backend lifecycle guide](../installer/README.md#verify-upgrade-remove).
CLI mode does not use the app/core receipt-only removal path.

**Stop a running UI before upgrading files it is using.** Modified owned files
and unowned collisions require explicit attention; do not remove ownership
receipts merely to force an upgrade. Uninstall is not a workspace/state wipe.
Back up your work separately and inspect the plan before removal.

On Windows, new receipts preserve the user PATH's raw registry text, value type
and missing/empty state. Removal restores that state only if the value still
matches the installer's owned update; unrelated changes are preserved. An old
v0.3.0 string-only receipt requires explicit `-NoPath` for upgrade or removal,
because the original raw state cannot be reconstructed. See
[the Windows bootstrap migration note](MIGRATION.md#from-the-withdrawn-windows-v030-bootstrap).

## Archives, checksums and trust

[Release assets](https://github.com/xibodev/midden/releases/tag/v0.3.1) provide
native binaries, the matching bundle, bootstrap scripts and verification
metadata. Use a native target actually listed by that release. A missing target
is an unsupported installation, not a reason to substitute a foreign binary.

For manual extraction, retain the release's layout: the app must be able to
resolve its matching `midden` executable and `bundles` beside its installation.
Do not mix payload versions. Keep `LICENSE` and the generated
`THIRD_PARTY_NOTICES.txt` for compiled dependencies, including Compa attribution.

Compare artifacts against the matching
[SHA256SUMS](https://github.com/xibodev/midden/releases/download/v0.3.1/SHA256SUMS).
Windows has `Get-FileHash -Algorithm SHA256`; Linux commonly has `sha256sum`,
and macOS has `shasum -a 256`.

**Checksums establish integrity against the supplied manifest, not publisher
authenticity.** Replaced payloads and replaced checksums can agree. Obtain both
through a trusted channel. The v0.3.1 release binaries are unsigned; a matching
SHA-256 digest is not a publisher signature.

Application-control policies can block an intact unsigned binary, including the
bootstrap's version probe. A reported application-control failure means execution
was blocked, not that the source or checksum is necessarily invalid. Stop and
seek administrator or publisher review with the release version, checksum and
reviewed diagnostics. Do not disable Defender/ASR or other security controls,
bypass execution policy, or switch launchers to evade the block.

See [Getting started](GETTING_STARTED.md), [Troubleshooting](TROUBLESHOOTING.md)
and [Migration](MIGRATION.md).
