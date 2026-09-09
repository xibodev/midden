---
name: midden-content-seed
description: Build a portable content seed from recovered session material. Use when handing recovered work to another tool or a fresh session, or when asked what a Midden seed contains and guarantees. Covers the xibodev.midden.seed/v1 bundle, evidence digests, and the redaction and review limits that must be stated accurately. Requires the midden binary.
---

# Content seeds

Packaging recovered work so another module can use it.

## What a seed is

`xibodev.midden.seed/v1` — a portable directory:

```
manifest.json     entry file; the machine-readable facts
brief.md          prose for a reader
evidence.jsonl    selected evidence, one record per line
provenance.json   source identities and revisions
attachments/      referenced files
```

Readable with no Midden binary, no index, and no environment. Every internal
path is relative to the seed root, because the host stages a seed into its own
store — **the path a consumer sees is not the path Midden wrote.**

## Identity is the digest, not the path

`evidence_digest` is sha256 over `evidence.jsonl`. It is scoped to the evidence
set deliberately: regenerating `brief.md` prose does not invalidate a
consumer's provenance claim, but changing the evidence does.

When referring to a seed, cite its digest. A path is a location, and locations
change when a seed is staged or copied.

## Creating one

`seed.create` takes a scope plus intent:

- `goal` — what the seed is for. The one field a useful seed should carry.
- `title`, `summary`, `key_points` — optional structure.
- `suggested_output_types` — advisory only. The consuming module owns this
  vocabulary; an unrecognised value degrades to "no suggestion" rather than
  failing.

It requires the `midden_home` write root. Without it, `seed.create` returns
`missing_root` and writes nothing — it will not fall back to guessing a
location.

## What a seed does not claim

`review_state` is always `unreviewed` and `model_used` is `false`. **No module
sets a seed to approved.** Technical processing is not editorial acceptance,
and a downstream consumer must not treat a well-formed seed as a reviewed one.

An empty evidence set is valid. A seed carrying a goal and no recovered
material is a legitimate request, not an error.

## Redaction, stated accurately

Excerpts and titles are redacted for credential shapes — tokens, keys,
connection strings — and clipped. That is a secret filter, not a privacy
filter. It does not remove names, private prose, proprietary code, or the
substance of a conversation.

Do not describe a seed as "safe to publish". Describe it as "redacted for
credentials, unreviewed."

## Handing it on

A seed is the cross-module handoff. Midden produces it; it never invokes the
consumer. The host decides when a seed is needed and passes it along — with the
digest, so the consumer can verify the bytes it actually read.

## Invoking Midden

```bash
midden seed --session <session-id> --goal "<what this is for>"
```

`seed.create` WRITES, so it needs a writable root. Supply `midden_home` with an
absolute path and mode `rw`. Without it the call returns `missing_root` and
writes nothing — it will not guess a location.

The result gives `path` relative to that root, plus `evidence_digest`. Cite the
digest when referring to the seed; the path changes if the bundle is copied.
