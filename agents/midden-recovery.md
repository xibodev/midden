---
name: midden-recovery
description: Midden inventories past agentic-CLI sessions and prepares them for reuse. Use for recovering a session too large to resume, measuring what a transcript is made of, selecting evidence worth keeping, or building a portable content seed. Deterministic and model-free; source stores are read-only. Requires the midden binary.
---

# Midden: session recovery

Midden inventories past agentic-CLI sessions and prepares them for reuse. It is
deterministic and reads source stores read-only.

## What it is for

A session that has grown too large to resume is not lost — it is unindexed.
Midden measures what a transcript is made of, selects the part that carries
meaning, and packages it so work can continue in a fresh session.

## Choosing a capability

- `sessions.list` — inventory sessions in an exact scope. Start here.
- `sessions.assay` — classify one or more sessions into signal, exhaust,
  artifact and bookkeeping, and report reclaimable yield. Free and model-free.
- `seed.create` — build a portable content seed from selected sessions.

All three are deterministic, local, and cost nothing. None calls a model.

## Rules that matter

**Scope exactly.** Prefer explicit session IDs over an inferred scope. A wrong
scope produces a confident, well-formed, wrong answer — worse than no answer.
When the user names a session, pass its ID rather than a filter that probably
matches it.

**Assay before proposing anything expensive.** The assay is free and tells you
whether recovery is worth doing at all. A session that is 95% tool exhaust
needs a different plan from one that is 70% signal.

**Zero results and unreachable stores are different.** An empty result means the
scope matched nothing. A `no_source_stores` error means Midden could not see the
stores at all. Never report the second as "you have no sessions."

**Never report a session count without its denominator.** `sessions.list`
excludes automated and trivial sessions unless `include_noise` is set, so
`total` is a filtered figure rather than everything on disk. The result carries
`excluded_noise` and `matched` — report them together. "93 sessions" invites a
user who counts 218 files to conclude Midden lost 125 of them; "93 of 215, 122
automated excluded" is the same fact and cannot be misread.

**Source stores are read-only.** Midden never writes to Copilot, Claude, or
OpenCode data. If a user asks to delete or edit a session through Midden, that
is out of scope — say so.

**Private material stays private.** Assay results report measurements, never
transcript content. Seeds carry bounded, redacted excerpts with provenance.
Do not ask Midden to return raw transcripts to make an integration simpler.

**Redaction is not a privacy filter.** It matches credential shapes — tokens,
keys, connection strings. It does not remove names, private prose, or
proprietary code. A redacted seed is safe from leaking a secret, not
automatically safe to publish.

## Reading a result

- `signal_share` — fraction of bytes that carry meaning. Low means most of the
  session is exhaust.
- `reclaimable_bytes` — what could be removed without losing meaning.
- `compression` — how much smaller the session becomes if only signal is kept.
- `candidate_count` / `est_slice_tokens` — the bounded slice worth carrying
  forward, and roughly what it costs to feed to a model.

Report these as measurements, not recommendations. Deciding to prune or
archive is the user's call.

## Seeds

A seed is a portable directory: `manifest.json`, `brief.md`, `evidence.jsonl`,
`provenance.json`, `attachments/`. It is readable without Midden, its index, or
its environment. Its identity is its digest, not its path — a staged copy at a
different location is the same seed.

A seed carries `review_state: "unreviewed"` and `model_used: false`. Midden
never marks a seed human-approved; technical processing is not editorial
acceptance.

## Invoking Midden

```bash
midden module describe --json          # capabilities, schemas, permissions
midden module invoke <capability> --input <request.json>
```

stdout carries exactly one JSON envelope; diagnostics go to stderr, so parse
stdout alone. Read `ok` first, then route on `error.code` rather than message
text. A minimal request:

```json
{"protocol":"xibodev.module/v1","capability":"sessions.list",
 "request_id":"r1","input":{"tool":"claude","days":7}}
```

`seed.create` writes, so it additionally needs
`"roots":{"midden_home":{"path":"<absolute writable dir>","mode":"rw"}}`.
Without it the call returns `missing_root` and writes nothing.

For a person at a terminal, `midden ls`, `midden doctor` and `midden brief <id>`
are usually the faster answer than the module surface.
