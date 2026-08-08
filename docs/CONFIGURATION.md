# Configuration

Midden intentionally has a small configuration surface. Core discovery is
automatic; model and integration choices are explicit at the point of use.

## `MIDDEN_HOME`

`MIDDEN_HOME` controls where Midden stores its own state.

Default:

| Platform | Directory |
|---|---|
| Windows | `%USERPROFILE%\.midden` |
| macOS/Linux | `~/.midden` |

PowerShell example:

```powershell
$env:MIDDEN_HOME = 'D:\midden-data'
```

macOS/Linux example:

```bash
export MIDDEN_HOME="$HOME/.midden"
```

Use an absolute path. Set the variable before every Midden command that should
share the same state.

Typical contents:

| Path | Purpose |
|---|---|
| `index.db` plus SQLite sidecars | Sessions, assay manifests, evidence, recipes, runs, outputs, costs, audit data |
| `artifacts\` | Handoff briefs, generated drafts, local exports, provenance sidecars |
| `integrations.json` | Credential-free managed integration settings |
| `plugins\` | Optional advanced YAML manifests |

Do not point `MIDDEN_HOME` into a source store such as `.copilot`, `.claude`,
or OpenCode's data directory.

## Source discovery

Source paths are derived from the current operating-system user:

| Tool | Required source |
|---|---|
| GitHub Copilot CLI | `~\.copilot\session-store.db` |
| Claude Code | `~\.claude\projects\` |
| OpenCode | `~\.local\share\opencode\opencode.db` |

Midden also reads Copilot's `session-state\<id>\events.jsonl` files and Claude
live-session markers when present.

`MIDDEN_HOME` does not override these source locations. Midden opens source
databases read-only and never writes to the source directories.

## Model backends

Model-backed actions look for these commands on `PATH`, in this order:

1. `copilot`
2. `claude`
3. `opencode`

The first installed command is selected when **Auto-detect signed-in CLI** is
used. Choose a backend in the UI or pass `--backend` to override it.

Examples:

```powershell
.\bin\midden.exe reclaim --workspace my-project --backend copilot --dry-run
.\bin\midden.exe ask --backend claude --model <model-name> --dry-run "What changed?"
```

The model override is optional. Omitting it uses the backend's configured
default.

Midden does not store backend credentials and does not call a model API
directly. Authentication belongs to the selected CLI.

## Cost controls

Model-backed CLI commands support a preview:

```powershell
.\bin\midden.exe reclaim --workspace my-project --dry-run
.\bin\midden.exe refine adr --workspace my-project --dry-run
.\bin\midden.exe ask --dry-run "What decisions did I make?"
```

`reclaim` also supports:

- `--budget <n>` for maximum model invocations;
- `--max-credits <n>` for a charge cap when the backend exposes credits;
- `--records <n>` for evidence records per session;
- `--model <name>` for an explicit model;
- `--yes` to skip the interactive confirmation after a deliberate preview.

The browser always keeps preview and apply as separate requests.

## UI address

The web server binds only to loopback:

```powershell
.\bin\midden.exe ui
.\bin\midden.exe ui --port 7788
.\bin\midden.exe ui --no-open
```

Default: `127.0.0.1:7777`.

Midden does not expose a flag to bind to a public interface.

## Browser-local state

The last 40 Conductor messages are stored in the browser's local storage for
continuity. Recipe, run, output, and evidence state is stored in Midden's
SQLite index.

Use **CLEAR** in Conductor to remove the saved chat history for that browser
profile. Clearing it does not delete recipes or outputs.

## Managed integrations

Connections configured in the UI are saved to:

```text
<MIDDEN_HOME>\integrations.json
```

That file stores local URLs, local paths, backend names, and last-test status.
It has no password or token field.

For Open Notebook, a password is requested for the specific send action and
kept in memory only for that request. `OPEN_NOTEBOOK_PASSWORD` remains an
optional fallback for advanced setups:

```powershell
$env:OPEN_NOTEBOOK_PASSWORD = '<set only in the current shell>'
```

Do not commit this value or put it in a manifest.

See [Optional integrations](INTEGRATIONS.md).

## Advanced manifests

Custom manifests live under:

```text
<MIDDEN_HOME>\plugins\
```

List parsing is passive:

```powershell
.\bin\midden.exe plugins list
```

Network or filesystem probes occur only after an explicit command:

```powershell
.\bin\midden.exe plugins probe
.\bin\midden.exe plugins verify
```

Non-loopback HTTP probes and all directory probes require the explicit
`--allow-network` authorization.

## Terminal output

Set `NO_COLOR` to disable terminal color:

```powershell
$env:NO_COLOR = '1'
```

Midden also disables color when `TERM=dumb`.

## Back up or move state

Stop the UI, watcher, and any scan before copying state. Copy the entire
`MIDDEN_HOME` directory, not only `index.db`, because SQLite WAL files can hold
committed data.

To trial a new location without touching the old one:

```powershell
$env:MIDDEN_HOME = 'D:\midden-clean-trial'
.\bin\midden.exe start
```

To return, set `MIDDEN_HOME` back to the original path.
