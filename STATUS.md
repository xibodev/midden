# Status

## M0 — See ✅ shipped

Deterministic, read-only session mapping across Copilot CLI, Claude Code and opencode.
**No LLM calls anywhere in this milestone.**

```
midden ls        list sessions across every installed AI CLI
midden show      one session in detail
midden resume    walk-to-workspace + resume one-liner (--with "instruction")
midden doctor    footprint, at-risk sessions, dead workspaces, live tabs
```

### Verified against real data

Run on the workstation that motivated the project (2026-07-26):

| Measure | Result |
|---|---|
| Total footprint | **36.3 GiB** across 682 sessions |
| copilot | 510 sessions · 23.4 GiB store · 4.6 GiB tracked transcripts |
| claude | 135 sessions · 914.8 MiB store |
| opencode | 37 sessions · 12.0 GiB store |
| Unattributable | 31.3 GiB (caches, indexes, snapshots) |
| At-risk sessions found | **5** — including all four known to have died |
| Live tabs detected | 4 |
| Dead workspaces | 198 |

The four sessions previously lost to silent resume failure (774 / 740 / 687 / 681 MiB) are
all correctly scored `critical`. A regression test pins those exact sizes.

### What M0 already gets right

- **Read-only everywhere.** Source stores are opened `mode=ro` + `query_only`, with a
  temp-snapshot fallback when a live WAL blocks the open. A running CLI is never at risk.
- **opencode is read from its database**, not `opencode session list` — the CLI is
  project-scoped and returned 19 of 704 sessions on this machine, hiding an entire active
  project. `parent_id IS NULL` drops sub-agent sessions.
- **Copilot size lives in `events.jsonl`**, not SQLite. Both are read, because the jsonl is
  what breaks `--resume`.
- **Copilot titles fall back to the first user turn** when `summary` is blank — filtering on
  a blank summary hides exactly the long-running sessions that matter most.
- **Claude live-tab detection** via `~/.claude/sessions/<pid>.json` plus a PID liveness check.
  Resuming an already-open session is refused with a warning.
- **Calendar-day windows** across all three adapters, so a `--days 5` query means the same
  thing everywhere.
- **Span, not just age** — a 12-day-old session touched an hour ago is the idle-but-open
  shape that age alone hides.
- **Adapter isolation** — one tool's format drift degrades to a warning, never a blank screen.
- **Flags before or after positionals**, because stdlib `flag` silently drops
  `resume <id> --with "…"` otherwise.
- **Noise hidden, never deleted** — automated spawns, health probes and temp-dir test runs
  are excluded by default and always recoverable with `--all`.

### Known gaps

- opencode per-session byte accounting requires a full `part` table scan (~190s over 341k
  rows), so it is opt-in behind `--sizes` and off by default.
- No index yet: every invocation re-reads the source stores. Fine at this size, will not
  scale to ASSAY.
- Copilot has no live-session marker, so `open now` is Claude-only.
- Windows-verified only; the shell dialect switch exists but is untested on POSIX.

---

## Next

**M1 — Protect.** `midden watch`: warn before the resume cliff and offer harvest-and-handoff.
This is the milestone that stops the data loss which motivated the project.

**M2 — Speak.** MCP server over the same core, with hard token budgets
(~200 tokens/session listing, ~1.5k for a session brief).

See [README.md](README.md) §11 for the full M0–M8 build order.
