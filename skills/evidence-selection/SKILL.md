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

`content.types` lists what can be produced and how much evidence exists.
`content.produce` writes one. Read `free_kinds` first: those need no grant and
no model.

```bash
cat > /tmp/types.json <<'EOF'
{"protocol":"xibodev.module/v1","capability":"content.types",
 "request_id":"r1","input":{},
 "roots":{"midden_home":{"path":"/absolute/writable/dir","mode":"rw"}}}
EOF
midden module invoke content.types --input /tmp/types.json

cat > /tmp/produce.json <<'EOF'
{"protocol":"xibodev.module/v1","capability":"content.produce",
 "request_id":"r2",
 "input":{"kind":"adr","title":"Why we chose X","max_evidence":20,"name":"adr-x"},
 "roots":{"midden_home":{"path":"/absolute/writable/dir","mode":"rw"}},
 "grants":{"subprocess":["claude"]},
 "binaries":{"claude":"/absolute/path/to/claude"}}
EOF
midden module invoke content.produce --input /tmp/produce.json
```

A model-backed kind without a `grants.subprocess` entry and a matching
`binaries` path returns `subprocess_denied` — that is a missing authorization,
not a broken module. The free kinds work with neither.

`no_evidence` means nothing matched the scope. Midden will not write a document
from nothing, because an invented output carries the same provenance fields as
a derived one.

## Invoking Midden

```bash
cat > /tmp/assay.json <<'EOF'
{"protocol":"xibodev.module/v1","capability":"sessions.assay",
 "request_id":"r1","input":{"ids":["<session-id>"],"max_candidates":40}}
EOF
midden module invoke sessions.assay --input /tmp/assay.json
```

The result carries `counts`, `bytes`, `by_kind`, `signal_share`,
`reclaimable_bytes`, `candidate_count`, `slice_bytes` and `est_slice_tokens`.
Read `slice_bytes`/`est_slice_tokens` for cost, not `signal_bytes`.

Assay is free and calls no model. Say so rather than implying it might cost.
