---
name: midden-recovery
description: Recover past agentic-CLI sessions and produce evidence-grounded content. Inventory, assay, and seeds are model-free; extraction and narrative production use an authorized AI CLI. Source stores are read-only. Requires the midden binary.
---

# Midden: session recovery

Midden inventories past agentic-CLI sessions and prepares them for reuse and
content production. Inventory, assay, and seeds are deterministic; evidence
extraction and narrative production use an authenticated AI CLI. Source stores
are read-only throughout.

## What it is for

A session that has grown too large to resume is not lost — it is unindexed.
Midden measures what a transcript is made of, selects the part that carries
meaning, and packages it so work can continue in a fresh session.

## Choosing a step

- `sessions.list` — inventory sessions in an exact scope. Start here.
- `sessions.assay` — classify one or more sessions into signal, exhaust,
  artifact and bookkeeping, and report reclaimable yield. Free and model-free.
- `seed.create` — build a portable content seed from selected sessions.
- `evidence.extract` — extract bounded, redacted evidence into Midden's index
  through an authorized AI CLI. Required before producing from a fresh index.
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

## Producing content

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
by a model through an AI CLI the user is already signed in to, and Midden holds
no API key: `content.produce` needs the host to grant subprocess authority and
supply that binary. Without a grant it returns `subprocess_denied` and names the
free kinds instead of producing something weaker.

**Cost is per document, not per step.** A free pack reports zero. A
model-backed document reports UNKNOWN cost, because the spend happens inside
someone's own subscription and Midden never sees a bill. Never describe a
model-backed output as free.

**Every output is source text.** A diagram is `.d2` source, a deck is markdown
with Marp front matter. Midden renders nothing, and the `maker` field names the
tool a person would run next rather than a dependency Midden invokes.

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

```
midden ls --tool claude --days 7
midden assay <session-id>
midden reclaim <session-id>
midden catalog
```

`seed.create` writes, so it additionally needs
`"roots":{"midden_home":{"path":"<absolute writable dir>","mode":"rw"}}`.
Without it the call returns `missing_root` and writes nothing.

For a person at a terminal, `midden ls`, `midden doctor` and `midden brief <id>`
are usually the faster answer than the module surface.
