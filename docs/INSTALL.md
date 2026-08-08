# Install Midden

Midden is a single Go binary. The browser UI is embedded in that binary, so
there is no Node, npm, Docker, database server, or web build to install.

## Requirements

- Git
- Go 1.26.4 or newer
- Windows, macOS, or Linux

For session discovery, at least one of these local stores must exist:

- GitHub Copilot CLI
- Claude Code
- OpenCode

For model-backed actions, at least one of the corresponding commands must also
be installed, available on `PATH`, and signed in:

- `copilot`
- `claude`
- `opencode`

Scanning, assay, browsing, session rescue, recipes, deterministic outputs, MCP,
and local review do not need a model backend.

## Windows: run from a fresh clone

Open PowerShell:

```powershell
git clone <repository-url>
Set-Location .\midden

go version
go test ./...

New-Item -ItemType Directory -Force .\bin | Out-Null
go build -trimpath -o .\bin\midden.exe .\cmd\midden
.\bin\midden.exe version
```

Expected version:

```text
midden 0.0.1
```

For an isolated first run, keep Midden's own data inside the clone:

```powershell
$env:MIDDEN_HOME = (Join-Path $PWD '.midden')
.\bin\midden.exe start
.\bin\midden.exe ui
```

The `.midden` directory is ignored by Git. It contains only Midden-owned state;
the source AI CLI stores in your user profile remain read-only.

`MIDDEN_HOME` is process environment. Set it again in each new PowerShell
window before running the binary.

## Windows: install on PATH

From the repository root:

```powershell
go install .\cmd\midden
$goBin = if (go env GOBIN) { go env GOBIN } else { Join-Path (go env GOPATH) 'bin' }
& (Join-Path $goBin 'midden.exe') version
```

If `midden` is not found in a new terminal, add the displayed `$goBin`
directory to your user `PATH`, then reopen PowerShell.

Installing the binary does not move or delete `%USERPROFILE%\.midden`. Binary
installation and application data are independent.

## macOS or Linux

```bash
git clone <repository-url>
cd midden

go version
go test ./...

mkdir -p bin
go build -trimpath -o ./bin/midden ./cmd/midden
./bin/midden version

MIDDEN_HOME="$PWD/.midden" ./bin/midden start
MIDDEN_HOME="$PWD/.midden" ./bin/midden ui
```

To install on `PATH`:

```bash
go install ./cmd/midden
"$(go env GOPATH)/bin/midden" version
```

If `GOBIN` is configured, use `$(go env GOBIN)` instead.

## Verify the installation

These checks do not call a model or mutate a source session store:

```powershell
.\bin\midden.exe version
.\bin\midden.exe help
.\bin\midden.exe doctor
.\bin\midden.exe scan
```

`scan` writes Midden's local index and reads source stores. Add `--assay` when
you are ready to classify transcript content:

```powershell
.\bin\midden.exe scan --assay
```

The first full assay can take time on a large history. Midden prints progress,
and unchanged transcripts are skipped on later runs.

## Upgrade

Back up your Midden state before a major upgrade:

```powershell
Copy-Item -Recurse -LiteralPath $env:MIDDEN_HOME -Destination "$env:MIDDEN_HOME.backup"
```

Then update and rebuild:

```powershell
git pull --ff-only
go test ./...
go build -trimpath -o .\bin\midden.exe .\cmd\midden
.\bin\midden.exe version
```

Migrations are applied when Midden opens its own index. Do not copy only
`index.db` while Midden is running because SQLite may have committed data in
`index.db-wal`. Stop Midden and copy the complete `MIDDEN_HOME` directory.

## Remove

Stop every running Midden UI, watcher, or MCP process. Then remove the binary:

```powershell
Remove-Item -LiteralPath .\bin\midden.exe
```

If installed with `go install`, remove `midden.exe` from `GOBIN` or
`GOPATH\bin`.

Midden does not automatically delete its state. Keep it for a future reinstall,
rename it for backup, or remove that exact directory only after confirming its
path:

```powershell
$env:MIDDEN_HOME
```

Never remove an existing `%USERPROFILE%\.midden` just to test a clean install.
Set `MIDDEN_HOME` to a new directory instead.
