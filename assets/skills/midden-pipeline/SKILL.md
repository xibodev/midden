---
name: midden-pipeline
description: Turn past agentic-CLI sessions into reusable knowledge and content. Use when a user asks what they decided, what they learned, or wants an ADR, handbook, lessons-learned, FAQ or summary built from prior work. Covers discovering sessions, searching their content, mining bounded evidence, and producing artifacts with provenance. Requires the midden binary.
---

# Midden pipeline

Midden recovers material from finished agentic-CLI sessions. You supply the
reasoning; Midden supplies discovery, bounded evidence, and provenance.

## The flow

```
discover -> narrow -> mine -> assess -> create
```

Every step below is one command. Run them the way this environment runs
commands; nothing here needs a file written first.

## 1. Discover

```
midden ls --days 30
```

Reports `total` (after filtering) and `matched` (before). `matched` is the
larger number. It also names any source store it could NOT read — when that
happens the counts cover the stores that opened, and `partial_inventory` says
so. Do not quote a total without checking it.

## 2. Narrow

A user asking "my work on X" means content, not metadata. Titles are derived
from a session's FIRST PROMPT, so title matching misses most real work.

```
midden find "retry behaviour" --days 30
```

This reads transcript content. Report what it scanned and what it skipped —
the output says both.

## 3. Measure before spending

```
midden assay <session-id>
```

Free, deterministic, no model. Reports size, signal share, and the bounded
candidate set. **`slice_bytes` governs cost, not `signal_bytes`** — the slice
is what reaches a model and is typically a small fraction of the session.

## 4. Mine

```
midden reclaim <session-id>
```

Costs money: it drives an AI CLI the user is already signed in to. Midden
never sees the amount, so `cost_known` is false and both cost fields report
`null` rather than `0`.

**Check coverage before trusting a thin result.** The output reports
`coverage_percent` — what fraction of the session's signal the model saw. A
low number means few nuggets may reflect a narrow net rather than a quiet
session. Widen with `--max-candidates` and re-run if it matters.

## 5. See what the evidence supports

```
midden catalog
```

Proposes only artifacts the evidence can carry. If it does not propose the
kind the user asked for, say so before producing it — a document the tool's
own analysis does not back is worse than no document.

## 6. Create

Two routes. Choose deliberately.

**Write it yourself.** You have the evidence and a model. For an ADR,
handbook, summary or FAQ this is usually right: you can shape it to the
question actually asked. Cite nugget ids so provenance survives.

**Ask Midden.** For structured packs — `retrieval_pack`, `eval_pack`,
`sft_pack`, `privacy_manifest` and the other deterministic kinds — use the
tool. These are exact formats with per-row provenance, free, and no model
runs:

```
midden refine retrieval_pack --session <session-id>
```

## What to tell the user

- what was scanned, and what was skipped
- coverage: how much of the session the evidence came from
- that cost is unknown rather than zero, when a model ran
- where the artifact landed, and that it is a draft

## Refusals worth respecting

Midden refuses to write from no evidence. An invented document carries the
same provenance header as a derived one, which would make the header a lie.
If it refuses, report that rather than writing the file yourself and
presenting it as mined.
