# Install Midden

**Recommended: use Midden through GitHub Copilot CLI, Claude Code, or OpenCode.**
The standalone browser application is experimental. Version **0.2.1** is a preview.

## Requirements

- An installed, authenticated supported agentic CLI.
- Windows x64 with built-in PowerShell 5.1 or PowerShell 7, or Linux/macOS x64 or arm64 with Bash 3.2+,
  curl, tar, and `sha256sum` or `shasum`.
- Network access to GitHub for release downloads.

Go, Git, Node, and a database server are not required for prebuilt installation.
The scripts check host executables, but cannot prove that a host login or model works.

## One-line install

Windows (built-in PowerShell or PowerShell 7):

```powershell
irm https://xibodev.github.io/midden/install.ps1 | iex
```

Linux/macOS (run in an interactive terminal):

```bash
curl -fsSL https://xibodev.github.io/midden/install.sh | sh
```

These small entry points fetch a pinned released installer and manifest into a
temporary directory, verify both against `SHA256SUMS`, and launch the saved
installer. That installer verifies the platform archive. Temporary downloads
are cleaned up when setup finishes or fails. Bash prompts read `/dev/tty`, so
they work even though the bootstrap arrives through a pipe. The one-liner trusts
the HTTPS-hosted entry point; checksum verification starts with its release downloads.

For repeatable automation or lifecycle flags, save the entry point first and
run `powershell -File ./bootstrap.ps1 -Verify` or `sh ./bootstrap.sh --verify`.
Arguments are forwarded to the versioned installer. Use `--yes` with explicit
host choices for automation without a terminal.

### Customize a piped installation

Set `MIDDEN_HOSTS` (comma-separated IDs), `MIDDEN_DEPENDENCIES` (`pandoc,d2` or
`none`), `MIDDEN_INSTALL_DIR`, `MIDDEN_STATE_DIR`, `MIDDEN_VERSION`,
`MIDDEN_NO_PATH=1`, or `MIDDEN_YES=1` before running the entry point.
These are optional; the normal path needs no environment configuration.

```powershell
$env:MIDDEN_HOSTS = 'claude-code'
$env:MIDDEN_DEPENDENCIES = 'none'
irm https://xibodev.github.io/midden/install.ps1 | iex
```

```sh
curl -fsSL https://xibodev.github.io/midden/install.sh | MIDDEN_HOSTS=claude-code MIDDEN_DEPENDENCIES=none sh
```

`MIDDEN_YES=1` accepts the displayed plan without prompting; optional packages
are installed only when explicitly selected. Package managers retain their own
confirmation/elevation requirements.

## Download and verify manually

From [v0.2.1 release assets](https://github.com/xibodev/midden/releases/tag/v0.2.1),
save the platform installer, `manifest.tsv`, and `SHA256SUMS` in one directory.
Run commands from that directory. These release scripts need the adjacent
manifest; the Pages one-line entry points fetch that pair for you.

PowerShell (Windows):

```powershell
foreach ($file in @('install.ps1', 'manifest.tsv')) {
    $line = @(Get-Content ./SHA256SUMS | Where-Object { ($_ -split '\s+')[1] -eq $file })
    if ($line.Count -ne 1) { throw "Missing or duplicate checksum: $file" }
    $expected = ($line[0] -split '\s+')[0]
    if ((Get-FileHash $file -Algorithm SHA256).Hash.ToLowerInvariant() -ne $expected) {
        throw "Checksum mismatch: $file"
    }
}
powershell -NoProfile -File ./install.ps1 -Version v0.2.1
```

Bash (Linux/macOS):

```bash
awk '$2 == "install.sh" || $2 == "manifest.tsv"' SHA256SUMS > installer-checksums.txt
test "$(wc -l < installer-checksums.txt | tr -d ' ')" = 2 || exit 1
if command -v sha256sum >/dev/null; then
  sha256sum -c installer-checksums.txt || exit 1
else
  shasum -a 256 -c installer-checksums.txt || exit 1
fi
bash ./install.sh --version v0.2.1
```

Checksums detect damaged or mismatched downloads, not compromise of the release
source. Use the official repository. The installer separately verifies the selected
archive against the release checksums before writing installation files.

## Interactive choices

1. A single detected host is selected automatically. If several are installed,
   choose numbered hosts or accept all. Missing hosts produce setup links.
2. Choose optional PowerPoint/HTML (Pandoc) and SVG diagrams (D2), or core only.
3. Confirm one summary: hosts, skill paths, binary/state locations and PATH.

Personal skills are the default. Use `-Scope project -ProjectDir <absolute-path>`
or `--scope project --project <absolute-path>` for a project. Use `-NoPath` /
`--no-path` to opt out of PATH setup. Explicit paths remain available as flags.
Repeat runs reuse receipt scope/state. Interactive updates show the previous
version and preserve backups; unattended replacement requires `-Upgrade` / `--upgrade`.

Defaults: `~/.local/share/midden-cli/bin` for binaries/receipt and
`~/.local/share/midden-cli/state` for recovered work. On Windows, `~` is your user
profile. The state directory is created when recovery needs it, not by installation.

| Host | Personal skills | Project skills |
|---|---|---|
| Copilot CLI | `~/.copilot/skills` | `.github/skills` |
| Claude Code | `~/.claude/skills` | `.claude/skills` |
| OpenCode | `~/.config/opencode/skills` | `.opencode/skills` |

The scripts install three canonical skills, recovery guidance, and an absolute
binary/state binding. They do not change host model settings or grant permissions.
Restart the host if needed to discover `midden-session-recovery`.

### Optional dependencies

| Tool | Adds | Install routes |
|---|---|---|
| Pandoc | Editable PPTX and standalone HTML | winget, Homebrew, apt |
| D2 | SVG diagrams | winget, Homebrew; otherwise install separately |

Versions follow the selected package manager. The preview reports reused tools as
zero additional download/disk; unavailable download, installed, and transitive
sizes are not guessed. Managers show their own confirmation and may request
elevation. Dependency installs are separate system changes and are not undone by
Midden uninstall or a later installer failure. Facet is a separately installed
[sister project for video creation](https://github.com/xibodev/facet).

## Preview and explicit selection

```powershell
powershell -NoProfile -File ./install.ps1 -Version v0.2.1 -Hosts copilot-cli -NonInteractive -DryRun
```

```bash
bash ./install.sh --version v0.2.1 --hosts copilot-cli --yes --dry-run
```

For project scope, use `-Scope project -ProjectDir <absolute-path>` or
`--scope project --project <absolute-path>`. An existing host outside PATH can be
selected with `-HostPath` / `--host-path` when selecting exactly one host.
Use `-BundleDir` / `--bundle-dir` for an already extracted, verified headless
archive. Local bundle mode trusts your supplied files and does not download them.

## Verify, upgrade, and uninstall

Keep the installer and matching manifest. If you chose a custom installation
directory, pass `-InstallDir` / `--install-dir` on every lifecycle command.

```powershell
powershell -NoProfile -File ./install.ps1 -Verify
powershell -NoProfile -File ./install.ps1 -Version v0.2.1 -Upgrade
powershell -NoProfile -File ./install.ps1 -Uninstall
```

```bash
bash ./install.sh --verify
bash ./install.sh --version v0.2.1 --upgrade
bash ./install.sh --uninstall
```

Upgrade with the newer release's script, manifest, and explicit version; repeat
the original scope/state choices. Upgrades preserve backups and refuse to
overwrite modified or unowned files. Verification checks owned bytes and guidance;
it does not run a live agent. Uninstall removes only receipt-owned, unchanged
files and its PATH entry. Recovered work, user files, optional dependencies, and
upgrade backups remain. Stop any running Midden process before replacing it.

For manual CLI commands, set `MIDDEN_HOME` to the state directory chosen at install
time. Installed skills supply that directory in their module requests. Direct
commands otherwise default to `~/.midden`.

## Experimental standalone

Choose a **standalone** archive, verify it against `SHA256SUMS`, and extract it.
Run `./midden ui` or `.\midden.exe ui`. The UI is embedded and opens on loopback
at `http://127.0.0.1:7777`; use `--port` to change it. Headless archives omit the UI.
Set `MIDDEN_HOME` to a separate directory before trying this delivery form.

## Build from source

Requires Git and the Go version declared in `go.mod` (currently 1.26.5).
The pinned Go dependencies are public.

```bash
git clone https://github.com/xibodev/midden.git
cd midden
go test ./...
go build -trimpath -tags=headless -o ./bin/midden ./cmd/midden
```

On Windows use `-o ./bin/midden.exe`. Omit `-tags=headless` for the experimental
standalone binary. Building alone does not register host skills; use a release
installer for that. See [Development](DEVELOPMENT.md) for release CI.
