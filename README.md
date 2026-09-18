# Midden

Midden is a local evidence refinery for AI coding sessions. It indexes sessions
from GitHub Copilot CLI, Claude Code, and OpenCode, identifies reusable evidence,
and turns approved evidence into reviewed drafts with provenance.

Midden is:

- **Local-first:** a Go recovery engine and local SQLite index, used from your agentic CLI.
- **Read-only recovery:** inventory, assay and extraction do not modify source
  stores. Explicit cleanup commands are separate source-mutation operations.
- **Approval-gated:** model spend, evidence approval, production, export, and
  cleanup are separate decisions.
- **Evidence-grounded:** Studio will not create or run an unsupported work item
  when no reclaimed evidence exists.

Current version: **0.2.3 (preview)**. **The agentic CLI bundle is the canonical
product experience. Standalone Studio development is paused and experimental.**

The host agent and operator investigate sources, compare editorial opportunities,
choose an audience and purpose, and compose content. Midden supplies bounded
evidence, durable project state, shared content types, review gates, and local
delivery. It does not introduce another agent loop or claim that an evidence
count measures a story's editorial value.

## Install for your agentic CLI

Install and authenticate GitHub Copilot CLI, Claude Code, or OpenCode first.
Run the one-line installer in your terminal:

```powershell
# Windows, built-in PowerShell or PowerShell 7
irm https://xibodev.github.io/midden/install.ps1 | iex
```

```bash
# Linux/macOS, Bash 3.2+, curl and tar
curl -fsSL https://xibodev.github.io/midden/install.sh | sh
```

The entry points download a pinned released installer and manifest, verify both
against the release checksums, then open interactive setup. For manual downloads
or explicit lifecycle options, see [Install](docs/INSTALL.md).

The terminal wizard offers **Quick start** and **Custom setup**, keyboard-selectable
hosts and capabilities, an install-plan summary, and three visible progress stages.
Use Up/Down and Enter, or Q to cancel. Quick start uses personal skills and PATH;
Custom setup lets you choose project scope, folders, and PATH interactively.
Redirected terminals use numbered text choices; `MIDDEN_PLAIN=1` also selects that
mode. They download
and verify a prebuilt **headless** archive; Go and Git are not needed to install.
Pandoc adds editable PowerPoint/HTML rendering; D2 adds SVG diagrams. Both are
optional, separately installed tools. Package managers confirm actual sizes and
versions; unavailable size information is shown as not reported.

Restart your CLI to discover `midden-editorial-production`, then ask:

> Use midden-editorial-production to investigate these sessions and compare
> worthwhile stories. Help me choose an audience and outcome before drafting.

Your CLI owns the model, authentication, and permissions. Midden supplies the
recovery operations, evidence, content composition, and provenance. Inspect
generated claims and drafts before publication. Installer tests do not certify
live behavior in every host; cross-host acceptance remains in progress.

See [Install](docs/INSTALL.md) for verification, upgrades, removal, and source
builds; [Editorial workflow](docs/EDITORIAL_WORKFLOW.md) for the agent-first
journey. `midden-session-recovery` remains available for recovery-only work.

## Experimental standalone journey

Download a **standalone** archive from the release, verify `SHA256SUMS`, extract
it, and run `midden ui` (`.\midden.exe ui` on Windows). It opens a loopback-only
browser application at `http://127.0.0.1:7777`. Use `--port 7788` if needed.
Its embedded Facet Studio v1.0.0 kernel manages models and conversation;
provider onboarding and end-to-end product validation remain incomplete.

1. Open **Mine / Recover**, filter or select exact sessions, and start a free assay.
   The job continues in the background while you use the rest of Midden.
2. Extract a small evidence scope. Choose a real depth, inspect the long-running
   estimate once, then approve the recovery job.
3. Create a **Plan / Studio** work item from that evidence.
4. Continue with one persistent workspace agent. It can inspect files, use
   local tools, and run commands in the work context. Destructive, publishing,
   credential, upload, and unapproved paid-provider actions remain explicit
   approval points in chat.
5. Approve the evidence, run the output plan, and inspect rendered previews,
   editable source, and provenance side by side.
6. Download your source or rendered output, or export a reviewed result.
7. Open **Cleanup** to see which dormant source sessions are eligible for a
   reversible archive and exactly which recovery gates support that decision.

Nothing publishes, installs software, uploads data, trains a model, or executes
a shell command merely because a page was opened. Studio tool use begins only
after an operator sends a work request.

See [Getting started](docs/GETTING_STARTED.md) for a guided first run and the
[optional manual walkthrough](docs/ACCEPTANCE_TEST.md) for subjective UX
feedback.

## What is free and what can spend

Most of Midden is deterministic and free: scanning, indexing, assay, session
search, briefs, recipes, evidence review, deterministic packs, provenance,
local export, MCP, and operations history.

These operations can call a model through an authenticated backend:

- `reclaim`
- `refine`
- `ask`
- persistent Studio work-item chat
- model-backed refinery outputs

Long-running model-backed actions have a preview step. Studio chat instead uses
one visible per-work-item budget envelope and shows cumulative estimated usage.
Changing scope or exceeding that envelope is blocked explicitly. The CLI
equivalents support `--dry-run`.

With the recommended agentic CLI route, the host owns model access. The
experimental standalone runtime uses the embedded kernel's provider configuration.
Provider subscriptions, quotas, and charges remain the user's responsibility.

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

The installer binds the installed skills to a separate state directory, defaulting
to `~/.local/share/midden-cli/state`. Direct binary invocations outside that binding
use these defaults unless `MIDDEN_HOME` is set:

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

## Experimental web surfaces

| Surface | Purpose |
|---|---|
| **Recover** | Session inventory, exact scopes, free assay, evidence extraction, and durable mine history |
| **Studio** | Persistent tool-capable workspace agent, collapsible work-item list, controlled Console, evidence approval, production, rendered media preview, source, and provenance |
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
- Studio's workspace agent can use local tools and shell commands after an
  explicit work request. It is instructed to ask before destructive,
  publishing, credential, upload, or unapproved paid-provider actions.
- Studio Console remains allowlisted diagnostics, not an arbitrary host shell.
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
