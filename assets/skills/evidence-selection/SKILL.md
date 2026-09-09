---
name: midden-evidence-selection
description: Choose what is worth carrying out of a recovered session. Use when reading a Midden assay result, deciding which records to keep, or judging whether recovery is affordable. Explains the signal/exhaust/artifact/bookkeeping classes, the bounded candidate set that governs real cost, and what must never leave the session. Requires the midden binary.
---

# Evidence selection

Choosing what is worth carrying out of a session.

## The classes

Midden's assay sorts every record into four classes:

- **signal** — the exchange that carries meaning: prompts, reasoning, decisions.
- **exhaust** — tool output, file reads, command results. Large, mostly
  re-derivable.
- **artifact** — produced content worth keeping in its own right.
- **bookkeeping** — protocol and state records. Nearly pure overhead.

`reclaimable_bytes` is exhaust plus bookkeeping: what could be dropped without
losing meaning.

## Candidates, not the signal class

`signal_bytes` is usually far too large to feed to a model — signal at the
record level still includes megabytes of assistant output. The bounded
**candidate set** is what actually matters: the signal records most worth
selecting, with previews rather than full content.

`slice_bytes` and `est_slice_tokens` describe that set. Those are the numbers
that govern cost, not `signal_bytes`.

## Selecting

**Bound the request.** `max_candidates` defaults to 40 and is capped at 500.
A wider net is rarely better; it mostly adds near-duplicates.

**Duplicate reads are a signal about the session, not the evidence.**
High `duplicate_reads` means the same files were read repeatedly — often a sign
the session was going in circles. Worth mentioning to the user; not worth
carrying forward.

**Image clusters compress well.** `image_count` against `image_clusters` shows
how many screenshots were near-duplicates taken seconds apart. Rarely worth
including.

## What never travels

Assay results carry measurements and record kinds. They do not carry transcript
content, and previews are deliberately withheld from the wire.

Evidence in a seed carries a bounded, redacted excerpt plus a pointer — never a
whole transcript. This is a privacy boundary, not a size optimisation: do not
work around it by requesting more evidence records to reconstruct a transcript.

## Honesty about cost

Deterministic assay and selection are free and involve no model. Say so.
Model-backed extraction is a separate, explicitly estimated step. Never imply
a free operation might cost money, or that a paid one might not.

## Turning evidence into a document

```
midden catalog
```

Proposes only the artifacts this evidence can actually carry. If it does not
propose the kind the user asked for, say so before producing it -- a document
the tool's own analysis does not back is worse than no document.

Then choose the route deliberately.

**Write it yourself.** You have the evidence and a model. For an ADR,
handbook, summary or lessons-learned this is usually right: you can shape it
to the question actually asked, and cite nugget ids so provenance survives.

**Ask Midden**, for the structured packs -- `retrieval_pack`, `eval_pack`,
`sft_pack`, `privacy_manifest` and the other deterministic kinds. These are
exact formats with per-row provenance, they run no model, and they cost
nothing:

```
midden refine retrieval_pack --session <session-id>
```

Reproducing one of those formats by hand gets it subtly wrong every time.

## Measuring before you spend

```
midden assay <session-id> --max-candidates 40
```

The result carries `counts`, `bytes`, `by_kind`, `signal_share`,
`reclaimable_bytes`, `candidate_count`, `slice_bytes` and `est_slice_tokens`.
Read `slice_bytes`/`est_slice_tokens` for cost, not `signal_bytes`.

Assay is free and calls no model. Say so rather than implying it might cost.
