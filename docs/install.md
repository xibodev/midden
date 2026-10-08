# Installing, updating and removing Midden

One installer installs, updates, checks and removes all three parts of
Midden: `install.ps1` on Windows and `install.sh` on macOS and Linux. It
installs for your user account only and needs no administrator rights,
Python, Go or Node.

## Requirements

- **Windows** on x64 or Arm64, with Windows PowerShell 5.1 or PowerShell 7.
- **macOS or Linux** on x86-64 or Arm64, with `curl`, `tar`, `gzip`, `unzip`,
  and `sha256sum` or `shasum`.

## Install

### The App

Windows, in PowerShell:

```powershell
irm https://xibodev.github.io/midden/install.ps1 | iex
```

macOS and Linux:

```sh
curl -fsSL https://xibodev.github.io/midden/install.sh | sh
```

The installer:

1. Downloads the latest release from GitHub and checks every file against
   the release's SHA-256 checksums.
2. Installs Core, the skills and the App in the programs folder (see
   [Where things go](#where-things-go)).
3. Downloads Pandoc 3.12 from Pandoc's own release, checks it against the
   checksum in Midden's release, and keeps it in the App's `app/tools`
   folder for the App's assistant. It is not put on your PATH.
4. Makes Midden easy to start. On Windows it adds the programs folder to
   your user PATH and Midden to the Start menu. On macOS it adds
   `~/Applications/Midden.app`, on Linux an entry in your application menu.
   On macOS and Linux it links `midden` and `midden-ui` into `~/.local/bin`
   and changes no shell profile, so `~/.local/bin` must be on your PATH.
5. Starts the App in that terminal and opens it in your browser. Choose
   **Quit Midden** in the page, or press Ctrl+C, to stop it.

### The skills for your agent

Bundle mode installs Core and copies the skills into your agent's skills
folder:

```powershell
& ([scriptblock]::Create((irm https://xibodev.github.io/midden/install.ps1))) -Mode bundle -Harness claude
```

```sh
curl -fsSL https://xibodev.github.io/midden/install.sh | sh -s -- --mode bundle --harness claude
```

| Harness | Your skills folder | With `-Project DIR` / `--project DIR` |
|---|---|---|
| `claude` (Claude Code) | `~/.claude/skills` | `DIR/.claude/skills` |
| `copilot` (GitHub Copilot CLI) | `~/.copilot/skills` | `DIR/.github/skills` |
| `agents` (agents that read `~/.agents/skills`) | `~/.agents/skills` | `DIR/.agents/skills` |

- Name several harnesses with `-Harness claude,copilot`, or repeat
  `--harness`.
- The skills are copied unchanged and run `midden` by name, so Core must be
  on the PATH your agent uses.
- Installing the App doesn't put skills in your agent's folders. Run bundle
  mode as well if you want both.
- Some formats need tools of your own, such as Pandoc for HTML slides and
  EPUB. See [Tools for each format](skills.md#tools-for-each-format).

### Core only

Use `-Mode core` or `--mode core` to install just the `midden` command.

### Options on Windows

`irm ... | iex` always installs the App with the default options. To pass
options, run the script as a script block, as shown above, or save it first:

```powershell
irm https://xibodev.github.io/midden/install.ps1 -OutFile install.ps1
powershell -ExecutionPolicy Bypass -File .\install.ps1 -Mode core
```

The installer never changes your execution policy. `sh install.sh --help`
lists the options on macOS and Linux.

## Options

| Windows | macOS and Linux | What it does |
|---|---|---|
| `-Mode app\|core\|bundle` | `--mode app\|core\|bundle` | What to install; `app` by default. Parts are only ever added; an upgrade without a mode keeps what is installed. |
| `-Harness NAMES` | `--harness NAME` | `claude`, `copilot` or `agents`. Required for bundle mode; with `-Uninstall`, removes only those copies of the skills. |
| `-Project DIR` | `--project DIR` | Put the skills in this existing project instead of your own folders. |
| `-Version X.Y.Z` | `--version X.Y.Z` | Install this release instead of the latest. |
| `-InstallDir DIR` | `--install-dir DIR` | Use another programs folder. Give it again on every later run. |
| `-DistributionDir DIR` | `--distribution-dir DIR` | Install from a local copy of a release; nothing is downloaded. |
| `-Repository OWNER/NAME` | `--repository OWNER/NAME` | Install from a fork. Give it again on every later run. |
| `-NoPath` | `--no-path` | Windows: leave PATH unchanged. macOS and Linux: make no links in `~/.local/bin`. Give it again on every later run. |
| `-NoLaunch` | `--no-launch` | Don't start the App afterwards. |
| `-NoOpen` | `--no-open` | Start the App without opening the browser. |
| `-Upgrade` | `--upgrade` | Move the installation to the selected release. |
| `-Verify` | `--verify` | Check the installed files against the receipt. |
| `-Uninstall` | `--uninstall` | Remove the installation. |
| `-DryRun` | `--dry-run` | Check everything and print the plan as JSON, without changing anything. |

`-Version` takes the version without a `v` prefix.

## Where things go

| | Windows | macOS | Linux |
|---|---|---|---|
| Programs | `%LOCALAPPDATA%\Programs\Midden` | `~/.local/lib/midden` | `~/.local/lib/midden` |
| Commands | the programs folder, on your user PATH | links in `~/.local/bin` | links in `~/.local/bin` |
| Start entry (App) | Start menu: Midden | `~/Applications/Midden.app` | `~/.local/share/applications/midden.desktop` |
| App data | `%LOCALAPPDATA%\Midden` | `~/Library/Application Support/Midden` | `~/.local/share/midden` |
| Core state | `%USERPROFILE%\.midden` | `~/.midden` | `~/.midden` |

On Linux, `$XDG_DATA_HOME` replaces `~/.local/share` when it is set. Core
keeps its state in `$MIDDEN_HOME` when that is set.

Depending on the mode, the programs folder holds `midden`, `midden-ui`,
`skills/`, `app/compa-kernel` (the App's assistant), `app/tools/` (Pandoc),
license notices, and `install-receipt.tsv`. The receipt records every file
the installer owns with its checksum, every skills folder it wrote to, the
Start entry and, on Windows, the PATH change.

## Update

```powershell
& ([scriptblock]::Create((irm https://xibodev.github.io/midden/install.ps1))) -Upgrade
```

```sh
curl -fsSL https://xibodev.github.io/midden/install.sh | sh -s -- --upgrade
```

- Stop the App first. On Windows a running App can't be replaced, so the
  upgrade fails and changes nothing.
- Running the plain install command again checks the installed release and
  starts the App. Once a newer release exists, it asks you to use
  `-Upgrade` instead.
- The upgrade replaces the program files and the copies of the skills in
  your own folders. A copy you changed stays at its version, with a note.
  Copies in projects are listed with the command that updates them.
- If the App is installed, it starts again afterwards; add `-NoLaunch` to
  skip that.
- Everything is checked before anything is written. If a step fails, the
  installer puts back what was there.

## Verify

`-Verify` or `--verify` compares every installed file with its checksum in
the receipt, without using the network, and lists any file that changed or
is missing.

## Uninstall

```powershell
& ([scriptblock]::Create((irm https://xibodev.github.io/midden/install.ps1))) -Uninstall
```

```sh
curl -fsSL https://xibodev.github.io/midden/install.sh | sh -s -- --uninstall
```

- Removes the programs, the receipt, the Start entry, the `~/.local/bin`
  links, and the copies of the skills it made, except copies you changed.
- On Windows it puts your user PATH back as it was before the install, if
  nothing else changed PATH since. Otherwise it leaves PATH as it is, says
  so, and you remove the Midden entry yourself.
- It keeps Core's state, the App's data (your files, conversations and
  model connections) and anything Midden didn't install. Delete those
  folders yourself if you want them gone; see
  [Where things go](#where-things-go).
- A program file that changed since installation stops the uninstall
  before anything is removed. `-Verify` shows which file it is.
- `-Uninstall -Harness claude` removes only those copies of the skills; add
  `-Project DIR` for a project's copies.
- It also removes installations made by the 0.3 installer. To replace such
  an installation with the current release, use `-Upgrade`.

## Install without network access

Copy a release's `SHA256SUMS`, `build-manifest.json`, `manifest.tsv` and the
archives you need into a folder, and pass it with `-DistributionDir` or
`--distribution-dir`. App mode also needs the Pandoc archive named in
`manifest.tsv` (for example `pandoc-3.12-windows-x86_64.zip`) in that folder.

## Security

- The installer works only in your user account: no elevation, no machine
  PATH, no change to your execution policy.
- It downloads only from GitHub: Midden's release and Pandoc's release.
  Every file is checked against SHA-256 checksums before it is used.
- Checksums show that files are intact, not who published them. Midden's
  programs are not code-signed, so Windows may warn about an unknown
  publisher.
- It never overwrites or removes a file it didn't install.
