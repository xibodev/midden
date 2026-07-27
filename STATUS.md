# Status

## M2 — Speak ✅ shipped

The differentiator: an MCP server so any AI CLI can reason about session history cheaply.
Still **zero LLM calls** — Midden costs tokens only in the sense that its output enters
your context.

```
midden mcp     JSON-RPC 2.0 over stdio, stdlib only, no SDK dependency
```

### Measured against 682 real sessions / 36.3 GiB

| Tool | Actual | Budget |
|---|---|---|
| `midden_health` | **158** tokens | 400 |
| `midden_list_sessions` (all 682) | **3,859** tokens | 4,000 |
| `midden_search` | 514 | 1,500 |
| `midden_session_brief` (681 MiB session) | **283** | 1,500 |
| `midden_resume_command` | 61 | 200 |

**An entire multi-gigabyte session history is surveyable for under 4k tokens.** No existing
tool offers this; incumbents render for humans and dump raw transcript.

See [docs/MCP.md](docs/MCP.md) for registration in Copilot CLI, Claude Code and opencode.

### Design notes

- **Budgets are ceilings, not targets.** `budgetLines` drops whole records rather than
  cutting mid-line, because a half-written session entry is worse than a short list.
- **Truncation is always announced** with the omitted count and a hint to narrow scope.
  Silent truncation would let a model conclude it had seen everything.
- **Budgets are ordered** (`small < health < brief < list`) and a test pins that ordering, so
  the cheap-survey-then-drill-down flow cannot invert.
- **Tool errors are results with `isError`, not protocol errors**, so a model can read and
  recover from them instead of the transport failing.
- **Notifications get no reply.** Answering one is a protocol violation that confuses clients.

## M1 — Protect ✅ shipped

The milestone that stops the data loss. Still **zero LLM calls**.

```
midden watch     warn before a session hits the resume cliff (--once for schedulers)
midden brief     harvest a session into a handoff brief (--handoff for a paste-able prompt)
```

### The result that matters

A session past the cliff cannot be resumed — but its context can be recovered:

| Measure | Result |
|---|---|
| Source | `ac0c39cf`, **681.4 MiB**, unresumable |
| Harvest time | **2.1 s** |
| Peak memory | **19.8 MB** — bounded, not O(file size) |
| Recovered | original goal, 37 user turns counted, last 3 exchanges, 4,668 records scanned |
| Token cost | **zero** |

`midden watch --once` exits **3** when attention is needed, so Task Scheduler, cron or a
git hook can act on it. It currently finds all four sessions that died.

### Design notes

- **Streaming with a ring buffer.** The first user turn (the original goal) is kept
  separately, plus a bounded window of the most recent turns. A 774 MiB transcript costs
  O(turns) memory, never O(file).
- **`eachLine`, not `bufio.Scanner`.** Scanner stops dead at the first line larger than its
  buffer, which would silently truncate a harvest — tool-result lines reach tens of MB.
  Oversized lines are truncated and the scan continues.
- **Cheap reject before parsing.** Copilot transcripts are ~68% tool events; a substring test
  skips them before any JSON decoding. That is why 681 MiB parses in 2 s.
- **`data.content`, not `transformedContent`.** The latter carries injected wrappers.
- **Handoff tells the next session to verify.** The prior session's final claims may describe
  actions that never completed — exactly the failure that motivated the project.

## M0 — See ✅ shipped

Deterministic, read-only session mapping across Copilot CLI, Claude Code and opencode.

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
- Cross-compiles cleanly to linux/amd64, linux/arm64 and darwin/arm64 (pure-Go SQLite, no
  cgo), but has only been *run* on Windows. The POSIX shell dialect is unit-tested, not
  exercised end to end.
- `watch` polls rather than using filesystem notifications — cheap, but a session can cross
  the cliff between ticks. Default interval is 5 minutes.

---

## Next

**M3 — Sort.** ASSAY: classify every event as signal / exhaust / artifact and report the
compression ratio, which governs every downstream cost. This is where the index arrives.

**M4 — Clean.** prune / compact / archive, gated on verify-by-resume.

See [README.md](README.md) §11 for the full M0–M8 build order.
