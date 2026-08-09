# Troubleshooting

## `go` rejects the module version

Check:

```powershell
go version
```

The module requires Go 1.26.4 or newer. Install a current Go release, reopen
the terminal, then run:

```powershell
go mod download
go test ./...
```

## `midden` is not found after `go install`

Find the install directory:

```powershell
$goBin = if (go env GOBIN) { go env GOBIN } else { Join-Path (go env GOPATH) 'bin' }
$goBin
& (Join-Path $goBin 'midden.exe') version
```

Add that directory to your user `PATH` and reopen PowerShell.

## No sessions are found

Confirm at least one source exists:

```powershell
Test-Path "$HOME\.copilot\session-store.db"
Test-Path "$HOME\.claude\projects"
Test-Path "$HOME\.local\share\opencode\opencode.db"
```

Then run:

```powershell
.\bin\midden.exe scan
.\bin\midden.exe doctor
```

Midden reads the stores for the operating-system user running the process.
Running it under another account or elevated context can point it at a
different home directory.

## The first scan or assay is slow

`scan` indexes metadata. `scan --assay` also parses transcripts and is expected
to take longer on a large history.

Start with a narrow scope:

```powershell
.\bin\midden.exe scan --assay --days 7
.\bin\midden.exe scan --assay --tool claude --days 30
.\bin\midden.exe scan --assay --max-bytes 500
```

Unchanged transcripts are skipped on later runs. OpenCode byte accounting is
intentionally opt-in behind `--sizes` because it requires a full part-table
aggregate.

## The UI port is already in use

Choose another loopback port:

```powershell
.\bin\midden.exe ui --port 7788
```

The terminal prints the exact URL.

## The browser does not open

Start without browser automation:

```powershell
.\bin\midden.exe ui --no-open
```

Open the printed `http://127.0.0.1:<port>` address manually. A browser-launch
failure does not stop the server.

## Studio says evidence is required

This is intentional. Assayed session volume is not evidence, and Midden will
not invent asset counts or create an unsupported recipe.

1. Open **Recover**.
2. Start a free assay for one session or small scope.
3. Select **Extract evidence**.
4. Approve a small model-backed extraction.
5. Return to **Studio** and create a work item from that evidence.

## No AI CLI backend is found

Midden looked for `copilot`, `claude`, and `opencode` on `PATH`.

```powershell
Get-Command copilot -ErrorAction SilentlyContinue
Get-Command claude -ErrorAction SilentlyContinue
Get-Command opencode -ErrorAction SilentlyContinue
```

Install and authenticate at least one backend, reopen the terminal, and run a
dry preview:

```powershell
.\bin\midden.exe reclaim --workspace <name> --dry-run
```

Core scanning and review remain available without a backend.

## A selected backend is not available

Use an installed backend or leave the UI set to
**Auto-detect signed-in CLI**.

CLI example:

```powershell
.\bin\midden.exe reclaim --backend copilot --dry-run
```

The model name is backend-specific; omit `--model` to use the backend default.

## The index is busy or a scan lock is reported

Only one authoritative source refresh should write at a time. The job remains
visible in Activity and the background dock; wait for it to finish before
starting another source scan.

If a process was interrupted:

1. Stop every `midden ui`, `midden watch`, and `midden scan` process you
   deliberately started.
2. Confirm all commands use the same `MIDDEN_HOME`.
3. Retry the scan.

Do not delete SQLite `-wal` or `-shm` files manually.

## I want a clean first run

Do not delete the existing state. Point the process at a new directory:

```powershell
$env:MIDDEN_HOME = (Join-Path $PWD '.midden-clean')
.\bin\midden.exe start
.\bin\midden.exe ui
```

The source session stores remain the same and read-only.

## My copied index is missing evidence or recipes

Copying only `index.db` while Midden is running can omit WAL-backed changes.
Stop all Midden processes and copy the complete `MIDDEN_HOME` directory.

For a programmatic SQLite snapshot, use SQLite's backup mechanism or
`VACUUM INTO`; do not perform a live single-file copy.

## MCP appears to hang when run manually

`midden mcp` is a stdio server. It waits for newline-delimited JSON-RPC input,
so a quiet terminal is expected.

Register the absolute binary path in the MCP client and restart that client.
See [MCP setup](MCP.md).

## An MCP client cannot start Midden

Verify the exact configured path:

```powershell
& 'C:\absolute\path\to\midden.exe' version
```

Use the absolute executable path, include `mcp` as the argument, and make sure
the MCP client inherits the intended `MIDDEN_HOME`.

## Open Notebook does not connect

- The API and UI URLs must be loopback addresses.
- Saving settings does not test the connection; select **Test connection**.
- Start Open Notebook first.
- If password protection is enabled, provide the password only in the send
  action or the current-shell `OPEN_NOTEBOOK_PASSWORD` fallback.
- Review the upstream Open Notebook encryption key and bind addresses.

See [Optional integrations](INTEGRATIONS.md).

## OpenMontage verification fails

The configured home must be an absolute local path. Follow OpenMontage's
upstream setup and ensure its Python, Node, FFmpeg, and selected agentic CLI
requirements are satisfied. Tools → OpenMontage can browse for the repository
and **Save & test** reports the first missing prerequisite. Midden does not
silently install those dependencies.

## Reporting a problem safely

Include:

- `midden version`;
- operating system and Go version;
- the exact command or UI action;
- the complete error text;
- whether `MIDDEN_HOME` was customized.

Do not attach raw session transcripts, databases, credentials, or generated
evidence unless you have reviewed and redacted them.
