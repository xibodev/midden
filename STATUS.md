# Status

**All milestones shipped (M0–M11).** Version 1.4.0.

Every measurement below comes from a real workstation running Copilot CLI, Claude Code and
opencode across 688 sessions and 37 GiB.

---

## The whole chain, end to end

A session that could not be resumed became publishable documentation:

| Stage | Result |
|---|---|
| Source | `ac0c39cf` — **681 MiB**, dead, `--resume` times out |
| **ASSAY** | signal 15.8% · exhaust 26.8% · artifact 56.1% · **6.3x** compression |
| Salvage slice | **~1,811 tokens** — a **98,613:1** reduction |
| **RECLAIM** | **9 nuggets** for ~3,069 tokens: decisions, an error→fix with root cause, a dead end |
| **REFINE** | ADR + troubleshooting guide, one warm session, 128s |
| **DISPOSE** | 126 MiB recovered, 78,500 records and 157,000 identifiers preserved exactly |

The generated ADR cites its source session, records confidence, and states *"Date: not
recorded in the evidence"* rather than inventing one.

---

## Milestones

### M0 — See
`ls` `show` `resume` `doctor`. Read-only mapping across all three CLIs.

- opencode is read from its **database**, not `opencode session list` — the CLI is
  project-scoped and returned 19 of 704 sessions, hiding an entire active project.
- Copilot keeps **both** SQLite metadata and a per-session `events.jsonl`; size lives in the
  jsonl.
- Copilot titles fall back to the first user turn when `summary` is blank — filtering on a
  blank summary hides exactly the long-running sessions that matter.
- Claude live tabs detected via `sessions/<pid>.json` plus a PID liveness check.
- **Span, not age**, surfaces idle-but-open sessions.

### M1 — Protect
`watch` `brief`. The milestone that stops the data loss.

- Copilot `--resume` fails silently above ~680 MiB. Four sessions (774/740/687/681 MiB) died
  this way before this existed.
- `brief` harvests a dead 681 MiB transcript in **2.1s using 19.8 MB RAM** — bounded ring
  buffer, not O(file size).
- `watch --once` exits **3** so schedulers and hooks can act on it.

### M2 — Speak
`mcp`. Token-budgeted MCP server so any agent can reason about sessions cheaply.

| Tool | Actual | Budget |
|---|---|---|
| `midden_health` | 158 tok | 400 |
| `midden_list_sessions` (all 688) | 3,859 tok | 4,000 |
| `midden_session_brief` (681 MiB) | 283 tok | 1,500 |

Truncation is always announced with the omitted count. See [docs/MCP.md](docs/MCP.md).

### M3 — Sort
`scan` `assay`. Classification built from **32 Copilot and 18 Claude record kinds observed in
the wild**, since none of these formats are documented.

- Unknown kinds are classified structurally, defaulting to signal — discarding meaning is
  worse than keeping bulk.
- Manifests are fingerprinted by source size and mtime: a second full pass went **90s → 1s**.
- An early version scored a session at 1.4x because `session.binary_asset` (56% of the file)
  fell through to signal. A regression test pins it.

### M4 — Clean
`prune` `archive` `ops`. Disposal that cannot destroy meaning.

- Sources are **never mutated**; pruned output is a new file.
- Records are preserved and payloads replaced **in place**, because transcripts are chained by
  id and dropping a record breaks replay.
- **Splitting is deliberately not offered**: `tool_use`/`tool_result` pairs must stay together
  and no CLI can relink split files.
- **Artifacts preserved by default** — screenshots are the raw material for tutorials.
- Verification is structural: record count, identifier count, signal-record count, parse
  errors, and the output must actually be smaller.
- Dry run is the default. Every mutating operation is audited.

### M5 — Mine
`reclaim` `nuggets`. Shells out to your **already-authenticated CLIs** — no API keys, seats
already paid for.

- Redaction runs at **extraction**, not publication: a secret reaching the nugget store has
  already escaped. It caught a real AWS access key on the first live run.
- Placeholders are actionable (`<AWS_ACCOUNT_ID — ask operator>`) so documentation stays
  usable.
- Salvage strips MCP entirely — it only reads text, so tool definitions are pure waste.
- Three real integration failures found and fixed: the Windows 8191-char command-line limit
  (prompts are now staged to a file), `--additional-mcp-config` requiring the `mcpServers`
  key, and needing `--allow-all-paths` to read a staged prompt.

### M6 — Make
`catalog` `refine` `artifacts`. Nine templates, each declaring its audience and shape, because
"write a tutorial" without a shape produces mush.

- **Catalog-then-generate**: evidence is loaded once and every artifact reuses it as cached
  context. Cache writes were ~55% of cost in a real session, roughly 3× cache reads.
- Session ids are **assigned** via `--session-id`, never discovered. An earlier version
  pattern-matched CLI output and silently fell back to fresh invocations — creating three
  separate sessions and writing artifacts from no evidence at all. They looked plausible.

### M7 — Show
`ui`. Embedded via `embed.FS`, **loopback only**, no build step, no node_modules.

Overview with live footprint and assay bars, session browser with resume one-liners, nugget
browser with provenance, artifact list, and the operation log.

### M8 — Advise
`advise`. Deterministic recommendations, each citing its evidence.

Real output: 4 sessions past the cliff (2.8 GiB) · 4 of 688 sessions hold half the transcript
bytes · tool output is 49% of classified bytes · 4,478 screenshots collapse to 977 distinct
moments · 198 sessions point at deleted workspaces.

### M9 — Account
`cost`. Per-run usage read back from each tool's own records, with calibration.

Estimates were wrong by 70–200× before calibration. After it, a prediction of 76.5 AIU landed
against 75.6 actual — within 1%.

### M10 — Act
Operations run from the UI rather than being described by it. A panel that only reports is an
instrument, not a tool.

### M11 — Guide
`start`, tiered summaries, `ask`. The pipeline order stopped living only in the author's head.

---

## Index authority

The index is the fast read path, but it is not a source of truth by itself.
Every complete scan now reconciles it against the source stores:

- sessions absent from a fully read source are removed before a stale resume
  command can silently start a new session;
- manifests whose source changed, orphan manifests, and duplicate artifact
  rows are removed;
- partial adapters preserve their existing rows and withhold deletion
  authority;
- scan generations, tombstones, cross-process locks, and SQLite triggers
  prevent overlapping or pre-upgrade writers from resurrecting deleted rows.

The web UI exposes this as **Refresh data**. Sessions state how many rows are
shown, in range, automated-hidden, or not loaded, and freshness travels with
the same cache snapshot that supplied the rows.

---

## Performance

Reads come from the index. Re-deriving them from the source stores meant opening every Claude
transcript, because Claude keeps cwd and title inside the file. On Windows each open triggers
an on-access virus scan of the whole file for the ~60 lines actually read — 1.7s per
transcript cold, 0.16s warm, measured over 135 files.

| | before | after |
|---|---|---|
| `midden ls` | 551s | **0.17s** |
| `/api/health` | 180s+ | **0.14s** |
| `/api/sessions` | hung | **0.05s** |

It stayed hidden because every result was *correct*. Nothing measured latency, and the CLI
always passed a narrow scope while the UI asked for everything.

Anything still slow now says what it is doing. Silence during a multi-minute first run is
indistinguishable from a hang.

---

## Known limits

- opencode per-session byte accounting needs a full `part` scan (~190s over 341k rows), so it
  is opt-in behind `--sizes`.
- Copilot and opencode write no live-session marker, so `open now` is Claude-only.
  `~/.copilot/restart/<pid>.json` looks like one — PID-named, carries a `sessionId` — and is
  not. It records restart intent, persists after the process dies, and is absent for a
  normally-running session: verified with Copilot live, its session had no marker while seven
  stale ones remained. Wiring it up would report seven closed sessions as open.
- Liveness on Windows needs `GetExitCodeProcess`, not `os.FindProcess`: the latter succeeds
  for a process that has already exited, which made every stale marker report its session as
  open forever. The process start time is also checked against the marker's, because PIDs are
  recycled and marker files are not.
- Cross-compiles cleanly to linux/amd64, linux/arm64 and darwin/arm64 (pure-Go SQLite, no
  cgo), but has only been *run* on Windows.
- `watch` polls rather than using filesystem notifications; a session can cross the cliff
  between ticks.
- Reclamation quality depends on the model you point it at. That is a dial you hold — every
  nugget records the model that produced it, so weak extractions can be identified and re-run.
