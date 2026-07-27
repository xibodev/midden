# Midden

**A materials recovery facility for AI coding exhaust.**

> *midden* (n.) — an archaeological refuse heap. Not what a civilisation claimed in its
> monuments, but what it actually ate, made, and threw away. The most honest record we have.

> **Status: M0 + M1 + M2 shipped.** `ls` · `show` · `resume` · `doctor` · `brief` · `watch` ·
> `mcp` work across Copilot CLI, Claude Code and opencode — read-only, zero LLM calls.
> See [STATUS.md](STATUS.md) and [docs/MCP.md](docs/MCP.md).

```console
$ midden doctor
  on disk    36.3 GiB across 682 sessions
  at risk    5      critical  773.8 MiB  Tackle Backlog Tasks
  open now   4      dead dirs 198

$ midden watch --once          # exit 3 when attention is needed
  4 session(s) need attention
    critical  681.4 MiB  Review Project Current Status
    past the cliff — do not resume; hand off instead:
    midden brief ac0c39cf --handoff

$ midden brief ac0c39cf --handoff
  # 681 MiB unresumable session -> handoff prompt in 2.1s, 19.8 MB RAM, zero tokens

$ midden mcp                   # register with any AI CLI; see docs/MCP.md
  # all 682 sessions surveyable for 3,859 tokens
```

```bash
go build -o midden ./cmd/midden && ./midden doctor
```

---

## 1. One-liner

Midden indexes, measures, salvages, and safely disposes of the data your AI CLIs leave
behind — turning 36 GB of discarded session exhaust into resumable context, publishable
content, and cost savings, with a hard budget cap and full provenance.

---

## 2. The problem, measured

Every AI CLI session produces far more data than it consumes. That data is written to
undocumented private formats, never garbage-collected, never indexed, and never re-read —
until it silently breaks the tool that wrote it.

Measured on a single heavy user's workstation (2026-07-26):

| Store | Size | Sessions |
|---|---|---|
| `~/.copilot` | **23.30 GB** | 506 |
| `~/.local/share/opencode` | **11.82 GB** | 704 |
| `~/.claude` | **0.88 GB** | 915 jsonl (137 real) |
| **Total** | **~36 GB** | |

Composition of one 681 MiB Copilot session (35 turns — **~19 MiB per turn**):

| Event type | Share |
|---|---|
| `tool.execution_complete` | 59% |
| `assistant.message` | 25% |
| `tool.execution_start` | 9% |
| base64 images | 3% |
| **`user.message`** | **1.3%** |

**Your actual input is 1.3% of the volume. Everything else is exhaust.**

### Four concrete failures this causes

1. **Silent data loss.** Copilot writes a per-session `events.jsonl` alongside its SQLite
   store. Above ~680 MiB, `--resume` times out after ~18s, logs a `Deferred resume` warning
   to a file nobody reads, and **silently starts a new session instead**. Four sessions
   (774 / 740 / 687 / 681 MiB — 2.8 GB) died this way on one machine. The user believed the
   work was lost; it was a load failure.

2. **Sessions are unfindable.** `opencode session list` returns only the *current project's*
   sessions — 19 of 704 on this machine. An entire active project (3 tabs, ~1000 messages
   each, worked on that same day) was invisible from the home directory.

3. **No hygiene discipline.** Nothing archives, compacts, or expires. Dead worktrees keep
   their sessions. Automated sub-agent spawns outnumber real work ~2:1.

4. **Total value discard.** The reasoning, rejected approaches, error→fix pairs, working
   commands, and UAT screenshots — the *why* behind every commit — are deleted or abandoned
   in place. Git records what changed; only sessions record why, including the failures.

---

## 3. The thesis

> **You can't responsibly delete until you've harvested. You can't afford to keep unless
> you've harvested.**

Harvest is the pivot that makes hygiene and content generation the *same machinery*.
Without reclamation, cleanup is just `rm -rf` with a nicer UI. With it, deletion becomes the
final stage of a value chain.

Two supporting observations:

- **Sessions are the only honest record.** A changelog says what shipped. A session says what
  was tried, why it failed, and what actually fixed it. That is exactly what makes good
  tutorials and terrible marketing copy.
- **The same bytes have two fates.** UAT browser-automation screenshots are *already* the
  images a tutorial needs — real state, real flow, with surrounding narration explaining what
  was being verified. Today they are 19 MiB/turn of garbage. That is a sorting problem, not a
  generation problem.

**Provenance is the differentiator.** Every artifact Midden produces cites `session:turn` and
records the model that produced it. This is not "AI writes docs about my repo" — it is
*extracted, attributable, verifiable* output.

---

## 4. Who it's for / non-goals

**For:** individual heavy users running two or more AI CLIs across many workspaces, who
generate more session data than they can track and who want to reuse it.

**Explicitly not:**

- ❌ A hosted service, SaaS, or multi-tenant product. Single-user, local, open source.
- ❌ A team analytics / cost-attribution dashboard.
- ❌ A replacement for any CLI's own session UI.
- ❌ A general-purpose writing tool. It writes *only* from your own harvested evidence.
- ❌ A quality guarantee. Output quality is a dial the user holds (see §8).

---

## 5. The pipeline

Seven stages. Every feature belongs to exactly one.

| Stage | Does | Cost | Layer |
|---|---|---|---|
| **METER** | Inventory: size, count, $ / credits, resume-risk, per tool / workspace / drive / repo / date | free | deterministic |
| **ASSAY** | Classify every event → signal / exhaust / artifact. Emit manifest + compression estimate | free | deterministic |
| **RESUME** | OS-correct walk-to-workspace one-liners; live-tab detection; resume-with-instruction | free | deterministic |
| **RECLAIM** | Extract *nuggets* with provenance from an ASSAY-filtered slice | cheap model, low reasoning | LLM |
| **REFINE** | Nuggets → tutorial / how-to / FAQ / ADR / changelog / post / demo script | mid-tier | LLM |
| **DISPOSE** | Prune, compact, archive, delete — *after* harvest, savings quantified | free | deterministic |
| **ADVISE** | Reduce future exhaust: config, MCP, skills, agent, workflow recommendations | mostly deterministic | mixed |

### Original feature map

| Requested | Stage |
|---|---|
| 1 — see & organise sessions | METER + RESUME |
| 2 — session detail, stats, tiered summaries | ASSAY (stats) + RECLAIM (summary / deep / x-ray) |
| 3 — walk + resume commands | RESUME |
| 4 — compress, optimise, delete, archive | DISPOSE |
| 5 — book / tutorial / article writer | REFINE |
| 6 — optimiser oracle | ADVISE |

---

## 6. Stage detail

### 6.1 METER

Builds and maintains a local index (SQLite) over every adapter. Never mutates a source store.

- Per session: tool, workspace, repo, branch, drive, created, last-touched, **span** (idle-but-open
  detection), turn/message count, bytes on disk, tokens in / out / cached, credits or cost,
  model(s) used, agent, live-PID status, **resume-risk score**.
- Roll-ups by tool, workspace, drive, repo, day/week, model.
- Noise classification: automated sub-agent spawns, health probes, trivial (`hi`) sessions —
  hidden by default, never deleted without consent.
- Diffable snapshots so growth is visible over time ("+4.2 GB this week, 87% from two sessions").

### 6.2 ASSAY

The economic engine. Determines what an LLM will ever be allowed to see.

- Parses each session's event stream, classifying every record:
  - **signal** — user intent, decisions, assistant reasoning, error→fix pairs, commands that worked
  - **exhaust** — tool call payloads, file dumps, repeated reads, redundant snapshots
  - **artifact** — screenshots, generated files, diffs, reports
- Emits a **manifest**: counts and bytes per class, dedup candidates, compression estimate,
  and a **nugget candidate list** with byte offsets.
- Perceptual-hash + timestamp clustering for images; one representative per cluster.
- **Secret detection runs here**, not at publication (see §9).

ASSAY's compression ratio is the single most important number in the product and is reported
on every run.

### 6.3 RESUME

- OS-correct one-liners (`Set-Location …; claude --resume <id>` / `cd … && …`), because
  Claude and Copilot key sessions to their original cwd.
- **Live-tab detection** — Claude writes `~/.claude/sessions/<pid>.json` with `sessionId`,
  `cwd`, `status`. Never offer to resume a session that is already open.
- **Dead-workspace detection** — flag sessions whose cwd no longer exists.
- **Resume-with-instruction** — compose a custom instruction in the UI/CLI, get back a
  one-liner that resumes *and* delivers the instruction:
  `midden resume <id> --with "re-run the audit, writing incrementally this time"`
- **Handoff** — for at-risk sessions, generate a distilled brief and a one-liner that starts a
  *fresh* session pre-loaded with it. (Same code path as RECLAIM — see §6.7.)

### 6.4 RECLAIM

Runs only over an ASSAY-filtered, user-scoped slice. Never over a raw corpus.

Nugget types:

| Type | Example |
|---|---|
| `decision` | why Postgres over SQLite, with the rejected alternatives |
| `error_fix` | stack trace → the change that resolved it |
| `command` | the invocation that actually worked, with its context |
| `gotcha` | "opencode session list only shows the current project" |
| `artifact` | screenshot + what it was demonstrating |
| `dead_end` | approach tried and abandoned, and why |
| `brief` | distilled session state for handoff |

Each nugget: content, type, confidence, `session:turn` provenance, **model that produced it**,
timestamp, redaction status.

Three depths: **summary** (shallow, cheap) · **deep** (cross-turn synthesis) · **x-ray**
(session + workspace + repo state + git history).

### 6.5 REFINE

Nuggets → artifacts. Never touches raw events.

Templates: tutorial · how-to · FAQ · troubleshooting guide · ADR · changelog · release notes ·
blog post · thread · README section · wiki page · demo script · course outline · book chapter.

**Catalog-then-generate.** Propose the full artifact set for a scope first (the way a
publication plan is written by hand), confirm, then generate in **one cached session** — because
each artifact after the first costs a fraction of the first (see §8).

Conversational entry point: *"What do you want to make from the last two weeks on `orvantix`?"*

### 6.6 DISPOSE

Every operation is preceded by a harvest check and reports reclaimed bytes.

- **prune** — strip tool-result payloads, attachments and base64 in place of markers
  (`[pruned: 240 KB file read — session:turn]`), preserving every record and every uuid link.
  Measured headroom: 80–90% on real files, with none of the chain-breaking risk of splitting.
- **compact** — dedup repeated file reads, collapse image clusters.
- **archive** — compress + move out of the CLI's active path, keeping the index entry and all
  nuggets. Reversible.
- **delete** — hard removal, harvest-gated, double-confirmed.
- **quarantine** — for sessions past the resume cliff: preserve, harvest, hand off.

**Non-negotiable:** always write to a *new* session id (`opencode run --fork` where available),
never mutate the original, and **verify by actually resuming the result** before offering to
delete the source.

### 6.7 ADVISE

Deterministic-first. Every recommendation cites evidence.

- **Exhaust reduction** — "three sessions produce 60% of your bytes; all three re-read the same
  large files every turn."
- **Config cost** — "your MCP config loads ~150 tools from one server; they are unused in 94%
  of sessions and cost context on every launch."
- **Workflow patterns** — "you asked this same question in 12 sessions; make it a skill."
- **Dead weight** — MCP servers never invoked, skills never triggered, agents never selected.
- **Risk** — "two sessions are within 15% of the resume cliff."
- **Tool/repo/workspace-specific** rule and instruction-file suggestions.

Conversational depth on request; the base layer is pure analytics.

---

## 7. Architecture

```
┌──────────────────────────────────────────────────────┐
│  Interfaces                                          │
│  CLI  ·  Web UI (embed.FS, localhost)  ·  MCP server │
└───────────────────────┬──────────────────────────────┘
                        │  one core, three faces
┌───────────────────────▼──────────────────────────────┐
│  Core: METER ASSAY RESUME RECLAIM REFINE DISPOSE ADVISE│
└───────────────────────┬──────────────────────────────┘
        ┌───────────────┼───────────────┐
┌───────▼──────┐ ┌──────▼──────┐ ┌──────▼───────┐
│ Adapters     │ │ Index       │ │ Executor     │
│ copilot      │ │ SQLite      │ │ shells out   │
│ claude       │ │ nuggets     │ │ to installed │
│ opencode     │ │ manifests   │ │ AI CLIs      │
│ (+ codex …)  │ │ snapshots   │ │              │
└──────────────┘ └─────────────┘ └──────────────┘
```

**Single Go binary.** UI embedded via `embed.FS`, binds localhost, opens a browser, exits when
closed. Technically client-server; operationally a double-click. `modernc.org/sqlite` (pure Go,
no cgo) keeps cross-compilation trivial.

### Adapters

Each adapter implements: `Discover`, `Sessions`, `Events`, `Locate`, `ResumeCmd`, `Capabilities`.

| Tool | Metadata | Transcript | Live detection |
|---|---|---|---|
| Copilot | `~/.copilot/session-store.db` | `session-state/<id>/events.jsonl` | — |
| Claude | `~/.claude/projects/<enc-cwd>/<uuid>.jsonl` | same | `~/.claude/sessions/<pid>.json` |
| opencode | `~/.local/share/opencode/opencode.db` (`session`, `message`, `part`) | same | — |

Notes that cost real time to learn:
- Copilot keeps **both** SQLite *and* a per-session `events.jsonl`. Size lives in the jsonl.
- opencode's CLI is project-scoped; **read the DB**, and filter `parent_id IS NULL` to exclude
  sub-agent sessions.
- Claude keys sessions by encoded cwd; resume requires the original directory.
- All source stores opened **read-only** (`mode=ro`). A live CLI may be writing.

### Execution model

Midden **never calls a model API directly.** It shells out to the user's already-authenticated
CLIs:

| Tool | Headless | Structured output | Useful |
|---|---|---|---|
| copilot | `-p/--prompt` | — | `--allow-all`, `--additional-mcp-config` |
| claude | `-p/--print` | `--output-format stream-json` | `--fallback-model` |
| opencode | `run` | `--format json` | `-s --fork`, `-f <file>`, `--dir`, `--pure` |

Consequences:
- **No API keys.** Uses seats already paid for.
- **Rate-limit resilience is native** (`--fallback-model`, provider auto-fallback).
- **Vision comes free** via `-f/--file`.
- **Fork, don't mutate** (`opencode run --fork`).
- **Strip MCP for salvage runs** (`--pure` / minimal `--additional-mcp-config`) — salvage reads
  text and needs no tools. Likely the largest per-invocation saving available.
- **Self-metering:** token and credit costs are read back out of the very session stores Midden
  already indexes. The tool measures itself with its own adapters.

---

## 8. Economics

**Meter in the subscription's unit.** Under a seat, dollars are a fiction — the scarce resource
is premium requests / AI credits. Budgets are expressed and enforced in that unit.

- **Pre-flight estimate** before any LLM stage: scope size, ASSAY compression, predicted
  credits, predicted wall time. Nothing starts without it.
- **Hard cap per task** with a kill-switch. `--budget 50` means stop at 50.
- **Model tiering:** discovery/classification deterministic (free) → extraction cheap + low
  reasoning → synthesis mid-tier → x-ray/oracle expensive, opt-in.
- **Scope is mandatory:** tool · workspace · repo · drive · date range · session set. There is no
  "salvage everything."

### Cache discipline (measured, and a real trap)

From a 66-turn session: **6.8M in (5.8M cached, 974k written), 80k out.** Cache *writes* were
~55% of cost — 3× the reads. Loading context is expensive; re-reading it is nearly free.

Therefore:
- **Batch inside one session, not across invocations.** N separate `-p` shell-outs are N full
  context loads with no reuse. Use `opencode run -s <id>` / `claude --continue` to send N
  messages to the *same* session.
- **Stable prefix first** (schema, rules, examples), variable slice second — reordering churns
  cache and repays the 1.25× write.
- Reasoning was 38% of output tokens: **low/no reasoning for ASSAY and RECLAIM.**

**Quality is a dial the user holds.** Better models produce better artifacts; that is a runtime
configuration choice, not a product defect. Midden states this plainly, records the model on
every nugget, and lets low-confidence extractions be re-run later when quota resets.

---

## 9. Safety

| Risk | Control |
|---|---|
| Corrupting a live store | All sources opened read-only; writes only to new files/forks |
| Destroying irreplaceable work | Harvest-gate → verify-by-resume → double-confirm; archive is reversible |
| Leaking secrets into published output | **Redact at extraction, not publication** — secrets must never enter the nugget store, which may be synced or committed |
| Silent inference errors | Adaptive for *read*; conservative and version-pinned for *write*. Never let inference drive a mutation |
| Format drift | Versioned adapters, fixture-based contract tests, graceful degradation. (opencode has already shipped 38 schema migrations) |
| Bad publication | Human gate before anything leaves the machine; loud warnings, never silent auto-publish |

Redaction replaces secrets with actionable placeholders —
`<AWS_ACCOUNT_ID — ask operator>` — so artifacts stay useful.

---

## 10. Interfaces

### CLI

```
midden scan                          # build/refresh index
midden ls        --tool claude --since 7d --group workspace
midden show      <id>                # deterministic stats, no LLM
midden resume    <id> [--with "instruction"]
midden doctor                        # at-risk sessions, dead dirs, live tabs, growth
midden assay     --workspace orvantix --since 14d
midden reclaim   --workspace orvantix --since 14d --budget 50
midden refine    tutorial --from <scope> --budget 30
midden catalog   --from <scope>      # propose artifact set before generating
midden prune|compact|archive|rm <scope>
midden advise    [--scope global|tool|repo|workspace]
midden watch                         # prevention daemon
midden ui                            # localhost web app
midden mcp                           # MCP server on stdio
```

### Web UI

Session map (by tool / workspace / drive / date, with growth over time) · session detail with
deterministic stats and optional tiered summary · one-click copy of resume one-liners ·
instruction composer · disposal queue with reclaimed-bytes preview · nugget browser with
provenance · artifact studio ("what do you want to make today?") · budget + credit dashboard.

### MCP server — *the differentiator*

Hard token budgets so an agent can reason about sessions without drowning:

| Tool | Budget |
|---|---|
| `midden_list_sessions` | ~200 tokens/session |
| `midden_session_brief` | ~1.5k tokens |
| `midden_search` | bounded |
| `midden_nuggets` | bounded, provenance-carrying |
| `midden_health` | ~300 tokens |

This is what lets *any* AI CLI understand your whole session history cheaply — the seam that
makes Midden usable by agents, not just humans. No incumbent offers it.

### Daemon

`midden watch` is deliberately tiny and LLM-free: poll file sizes and live PIDs, warn at ~400 MiB,
offer harvest-and-handoff before the resume cliff. **Prevention is the only thing that runs
continuously**; everything expensive is explicitly invoked.

---

## 11. Build order

| Milestone | Contents | Value on day one |
|---|---|---|
| **M0 — See** | 3 adapters, index, `scan`/`ls`/`show`/`resume`/`doctor` | Replaces ad-hoc scripts; finds lost sessions |
| **M1 — Protect** | `watch` daemon, risk scoring, handoff briefs | Stops the data loss that motivated the project |
| **M2 — Speak** | MCP server with token budgets | Every AI CLI gains session awareness |
| **M3 — Sort** | ASSAY, manifests, compression estimates, image clustering | Makes everything downstream affordable |
| **M4 — Clean** | prune / compact / archive / rm, verify-by-resume | Reclaims tens of GB |
| **M5 — Mine** | RECLAIM, nugget store, budgets, redaction | Salvage becomes real |
| **M6 — Make** | REFINE, catalog-then-generate, templates | Content from exhaust |
| **M7 — Show** | Web UI via `embed.FS` | The commodity layer, built last |
| **M8 — Advise** | ADVISE analytics + oracle | Reduce future exhaust |

M0–M2 are deterministic, free to run, and independently useful. A refinery nobody visits
processes nothing — earn daily use first.

---

## 12. Success criteria

**M0–M2**
- Zero sessions unfindable across all installed tools.
- No session ever again lost to the resume cliff.
- An agent can survey 500+ sessions for < 5k tokens.

**M3–M4**
- ASSAY compression ≥ 10× on real sessions.
- ≥ 50% of tracked bytes reclaimable.
- 100% of pruned sessions verified resumable before any source is deleted.

**M5–M6**
- One publishable artifact from a previously-dead session — the cheapest falsification of the
  whole thesis, and the first thing to attempt.
- Marginal cost of artifact N+1 from a cached scope < 20% of the first.
- Every artifact carries `session:turn` provenance.

**M8**
- At least one ADVISE recommendation that measurably reduces exhaust per session.

---

## 13. Prior art & positioning

| Tool | Does | Doesn't |
|---|---|---|
| `ccusage` | multi-agent token/cost analytics | reclaim, dispose, agent API |
| `ccmanager` | multi-agent session management | assay, salvage, hygiene |
| `ccstat` | activity visualisation | everything else |
| Claude/Codex Assist (VS Code) | history, diffs, archive, resume, usage | reclamation, MCP surface, budgets |
| `claude-code-otel` | team observability | single-user hygiene, content |

Viewing and metering are commoditised. **Reclamation and an agent-facing, token-budgeted API
are not.** Midden competes on the second half of the lifecycle.

---

## 14. Open questions

1. Nugget store format — plain markdown + front-matter (greppable, git-friendly) vs SQLite
   (queryable)? Probably both: SQLite index over markdown files.
2. Should archived sessions stay resumable via transparent rehydration?
3. Cross-tool session linking — same task continued in a different CLI. Detectable by cwd +
   time adjacency; worth it?
4. How much of ADVISE overlaps existing local tooling (pattern stores, rules audits) and should
   integrate rather than duplicate?

---

## 15. Provenance of this document

Written from a single 66-turn session (2026-07-26) that measured the machine it ran on, lost an
argument about its own cost model three times, and produced: a working cross-tool session
mapper, discovery of a silent Copilot data-loss bug, a corrected technical model of three
undocumented storage formats, and this specification.

That session is itself a Midden salvage candidate — an article, a troubleshooting guide, and a
case study, all with provenance. The thesis demonstrating itself.
