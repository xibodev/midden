# Midden

Midden is a local evidence refinery for AI coding sessions. It indexes sessions
from GitHub Copilot CLI, Claude Code, and OpenCode, identifies reusable evidence,
and turns approved evidence into reviewed drafts with provenance.

Midden is:

- **Local-first:** one Go binary, an embedded web UI, and a local SQLite index.
- **Read-only on source stores:** it does not write to Copilot, Claude, or
  OpenCode session data.
- **Approval-gated:** model spend, evidence approval, production, export, and
  cleanup are separate decisions.
- **Evidence-grounded:** Studio will not create or run an unsupported work item
  when no reclaimed evidence exists.

Current development version: **0.0.1**. It has not been publicly released.

## Fastest clean install on Windows

Prerequisites:

- Git
- Go 1.26.4 or newer
- At least one supported AI CLI session store if you want Midden to find data
- A signed-in `copilot`, `claude`, or `opencode` command only for model-backed
  evidence extraction and generation

From PowerShell:

```powershell
git clone <repository-url>
Set-Location .\midden

go test ./...
New-Item -ItemType Directory -Force .\bin | Out-Null
go build -trimpath -o .\bin\midden.exe .\cmd\midden
.\bin\midden.exe version

# Use isolated Midden state for this first run. Your AI CLI stores are still
# discovered from your user profile and remain read-only.
$env:MIDDEN_HOME = (Join-Path $PWD '.midden')

.\bin\midden.exe start
.\bin\midden.exe ui
```

The browser opens at `http://127.0.0.1:7777`. If that port is occupied:

```powershell
.\bin\midden.exe ui --port 7788
```

See [Install](docs/INSTALL.md) for PATH installation, macOS/Linux commands,
upgrades, and removal.

## First useful journey

1. Open **Recover**, filter or select exact sessions, and start a free assay.
   The job continues in the background while you use the rest of Midden.
2. Extract a small evidence scope. Choose a real depth, inspect the long-running
   estimate once, then approve the recovery job.
3. Create a **Studio** work item from that evidence.
4. Continue in one persistent AI CLI conversation. Routine turns use the
   work-item budget envelope rather than opening a cost dialog every time.
5. Approve the evidence, run the output plan, and inspect rendered previews,
   editable source, and provenance side by side.
6. Download your source or rendered output, or export a reviewed result.
7. Open **Cleanup** to see which dormant source sessions are eligible for a
   reversible archive and exactly which recovery gates support that decision.

Nothing publishes, installs an agent, uploads data, trains a model, or executes
a shell command automatically.

See [Getting started](docs/GETTING_STARTED.md) for a guided first run and the
[optional manual walkthrough](docs/ACCEPTANCE_TEST.md) for subjective UX
feedback.

## What is free and what can spend

Most of Midden is deterministic and free: scanning, indexing, assay, session
search, briefs, recipes, evidence review, deterministic packs, provenance,
local export, MCP, and operations history.

The following can call a model through an already authenticated AI CLI:

- `reclaim`
- `refine`
- `ask`
- persistent Studio work-item chat
- model-backed refinery outputs

Long-running model-backed actions have a preview step. Studio chat instead uses
one visible per-work-item budget envelope and shows cumulative estimated usage.
Changing scope or exceeding that envelope is blocked explicitly. The CLI
equivalents support `--dry-run`.

Midden never calls a model API directly and does not require a model API key.

## CLI orientation

```text
midden start      guided first run
midden scan       refresh the session index
midden scan --assay
                  classify transcripts and calculate reclaimable yield
midden ls         list indexed sessions
midden doctor     show risk, footprint, and dead workspaces
midden brief ID   recover context from an oversized session
midden reclaim    extract reusable evidence; previews before spending
midden catalog    show what the current evidence can support
midden refine     generate named artifacts; previews before spending
midden ask        answer from reclaimed evidence; previews before spending
midden cost       show recorded model operations and estimate accuracy
midden ui         start the loopback web app
midden mcp        expose the bounded read-only MCP server over stdio
```

Run `midden help` for every command in workflow order and
`midden <command> -h` for exact flags.

## Data and privacy

By default, Midden stores its own data under:

- Windows: `%USERPROFILE%\.midden`
- macOS/Linux: `~/.midden`

Set `MIDDEN_HOME` before launching Midden to use another directory. This changes
only Midden's index, settings, recipes, runs, and exports; it does not relocate
or alter source session stores.

Supported source locations:

| Source | Location |
|---|---|
| GitHub Copilot CLI | `~\.copilot\session-store.db` and `~\.copilot\session-state\` |
| Claude Code | `~\.claude\projects\` |
| OpenCode | `~\.local\share\opencode\opencode.db` |

Midden opens source databases read-only. Its own SQLite database is the only
database it writes.

See [Configuration](docs/CONFIGURATION.md) for the complete state layout,
backend selection, ports, environment variables, and backup guidance.

## Web surfaces

| Surface | Purpose |
|---|---|
| **Recover** | Session inventory, exact scopes, free assay, evidence extraction, and durable mine history |
| **Studio** | Persistent work-item list, AI CLI conversation, controlled Console, evidence approval, production, rendered preview, source, and provenance |
| **Library** | Every generated output with type filters, download, review, and export |
| **Cleanup** | Explainable eligibility gates and reversible archive previews |
| **Activity** | Restart-safe jobs, recovery runs, cost ledger, and audit history |
| **Tools** | Plugins, callable tools, skills, viewers, destinations, and managed integrations |

## Safety boundaries

- Source stores remain read-only.
- Scan and assay do not call a model.
- Raw transcripts are not copied into generated drafts.
- Reclaimed evidence is redacted and provenance-carrying.
- A recipe cannot run before its evidence is approved.
- Generated outputs begin as drafts.
- Export is local and requires per-output review.
- Studio Console is allowlisted diagnostics, not an arbitrary host shell.
- Publishing, installation, upload, training, and unrestricted shell execution
  remain out of scope or require a separate explicit action.
- Cleanup commands default to preview and preserve source meaning in new files.

## Documentation

- [Install](docs/INSTALL.md)
- [Getting started](docs/GETTING_STARTED.md)
- [Configuration](docs/CONFIGURATION.md)
- [Troubleshooting](docs/TROUBLESHOOTING.md)
- [MCP setup](docs/MCP.md)
- [Optional integrations](docs/INTEGRATIONS.md)
- [Development](docs/DEVELOPMENT.md)
- [Optional manual walkthrough](docs/ACCEPTANCE_TEST.md)
- [Release status](STATUS.md)

## Development snapshot

The application is a pure-Go module with an embedded HTML/CSS/JavaScript UI and
pure-Go SQLite. It has no Node build, Docker, CGO, or external service
requirement for the core product.

```powershell
go fmt ./...
go vet ./...
go test ./...
go build -trimpath -o .\bin\midden.exe .\cmd\midden
```

See [Development](docs/DEVELOPMENT.md) before changing adapters, persistence,
the refinery workflow, or the embedded UI.
