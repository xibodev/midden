# prototype/

Throwaway prototype that predates the Go implementation. Kept because it encodes the
storage-format knowledge that M0 needs, and because it is the thing that proved the problem
was real.

## `ai-sessions.ps1`

A PowerShell cross-tool session mapper for Copilot CLI, Claude Code and opencode. Roughly the
`METER` + `RESUME` stages, single-file and Windows-only.

```powershell
ai-sessions.ps1                     # unified recency map + paste-ready resume commands
ai-sessions.ps1 -Days 10            # widen the window
ai-sessions.ps1 -Tool claude        # filter by tool
ai-sessions.ps1 -LongRunning        # only sessions with a 2d+ span (idle-but-open)
ai-sessions.ps1 -GroupByTool
ai-sessions.ps1 -All                # include automated/trivial sessions
ai-sessions.ps1 -Resume <n>         # cd + resume by index
ai-sessions.ps1 -Json               # machine-readable
```

## What it already gets right (port these)

- **Read-only access** to every source store (`file:...?mode=ro`). A live CLI may be writing.
- **opencode must be read from its DB**, not `opencode session list` — the CLI is
  project-scoped and returned 19 of 704 sessions. Filter `parent_id IS NULL` to drop
  sub-agent sessions.
- **Copilot titles fall back to the first user turn** when `summary` is blank, and sessions
  with zero turns are dropped.
- **Claude live-tab detection** via `~/.claude/sessions/<pid>.json` + a `Get-Process` liveness
  check — never offer to resume an already-open session.
- **Noise filtering** — automated agent spawns, shell health-probes and trivial sessions are
  hidden by default but never deleted.
- **Dead-workspace detection** — flag sessions whose `cwd` no longer exists before offering to
  `cd` into it.
- **Calendar-day windows** applied consistently across all three adapters.
- **Span** (`updated - created`) is what surfaces long-running idle-but-open sessions; age
  alone hides them.

## What it does not do

No index, no ASSAY, no cost metering, no disposal, no MCP surface, Windows-only, and it
re-scans every source on every invocation. All of that is why the Go implementation exists.
