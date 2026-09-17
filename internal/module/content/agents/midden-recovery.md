---
name: midden-recovery
description: Recover past agentic-CLI sessions and produce evidence-grounded content. Inventory, assay, and seeds are model-free; extraction and narrative production use the host's authorized model runtime. Source recovery reads are read-only.
---

# Midden: session recovery

Midden inventories past agentic-CLI sessions and prepares them for reuse and
content production. Inventory, assay, and seeds are deterministic; evidence
extraction and narrative production use the host's authorized model runtime. Source stores
are read-only throughout.

## What it is for

A session that has grown too large to resume is not lost — it is unindexed.
Midden measures what a transcript is made of, selects the part that carries
meaning, and packages it so work can continue in a fresh session.

## Choosing a capability

- `sessions.list` — inventory sessions in an exact scope. Start here.
- `sessions.assay` — classify one or more sessions into signal, exhaust,
  artifact and bookkeeping, and report reclaimable yield. Free and model-free.
- `seed.create` — build a portable content seed from selected sessions.
- `evidence.extract` — extract bounded, redacted evidence into Midden's index
  through the host's authorized model runtime. Required before producing from a fresh index.
- `content.types` — list the documents Midden can write from mined evidence,
  which are free and which cost a model call. Call before producing.
- `content.produce` — write one of them.

Inventory, assay, seeds, and the content-type catalog are model-free.
Extraction always uses a model; production depends on the selected content type.

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
proprietary code. Redaction reduces credential exposure but cannot guarantee
that secrets are absent or that content is suitable for publication.

## Reading a result

- `signal_share` — fraction of bytes that carry meaning. Low means most of the
  session is exhaust.
- `reclaimable_bytes` — what could be removed without losing meaning.
- `compression` — how much smaller the session becomes if only signal is kept.
- `candidate_count` / `est_slice_tokens` — the bounded slice worth carrying
  forward, and roughly what it costs to feed to a model.

Report these as measurements, not recommendations. Deciding to prune or
archive is the user's call.

## Producing content

For a complete reviewed production, inspect `evidence.list` and `recipes.list`
first. Use `recipes.preview` to discuss a plan without saving; `recipes.design`
saves it. `recipes.update` revises intent or outputs and invalidates approval.
`recipes.evidence` selects exact evidence IDs; record `decision: approved` only
after the user's review. `recipes.produce` creates drafts from that approved
plan. Read `outputs.inspect` before revising or reviewing with `outputs.review`,
passing its `content_digest` as `expected_digest`. `outputs.export` copies only
reviewed, unchanged bytes and provenance into the local vault. A draft, reviewed
output, rendered deliverable and exported file are distinct outcomes.

The host owns model execution: standalone uses its embedded kernel; a detached
CLI/module receives an authorized model driver. Do not require an external AI
CLI when the host supplies a native runtime. Model use alone does not prove a
monetary charge; report pricing as unknown unless the host supplies evidence.

Mining is not the end. `content.produce` turns stored evidence into a document
a person reads: a tutorial, an ADR, a slide deck, a diagram, a video brief.

Read extraction coverage before choosing the document's scope. A small extracted
slice does not support claims about the whole session. After production, review
the actual draft against its cited evidence, revise unsupported framing, and
export through the available product or host workflow. Report missing revision,
rendering, or export support explicitly; a seed or a draft is not a completed
content-production workflow.

Seven kinds are deterministic and free — notebook, retrieval, eval, sft and
preference packs, and the privacy and provenance manifests. Twelve are written
by a model supplied by the host. Standalone uses the embedded kernel; detached
CLI/module execution requires an explicitly granted driver. The host manages
provider credentials. Missing runtime authority is an error, never an invitation
to silently choose another provider or fabricate output.

**Cost is per document, not per capability.** A free pack reports zero. A
model-backed document reports UNKNOWN cost, because the spend happens inside
the selected provider and pricing is not necessarily available. Model usage and
monetary charge are separate facts; report a free tier only when the host identifies it.

**Every output is source text.** A diagram is `.d2` source, a deck is markdown
with Marp front matter. The `maker` field names the
renderer for that source. `outputs.render` invokes Pandoc to create editable
PowerPoint from slide source or standalone HTML from Markdown. Rendering does
not approve the content; inspect the actual delivered format before declaring it ready.

Outputs are drafts. Producing a document is not approving it.

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
