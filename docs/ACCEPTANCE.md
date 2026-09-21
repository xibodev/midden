# Independent acceptance

## Core

Run the standard build, full Go suite and vet. No headless tag is needed.
Exercise exact source selection, malformed inputs, partial inventories, bounded
reading, append-only source growth, earlier changes/truncation, collection
integrity, asset confinement, portable export and legacy-store preservation.

Use synthetic stores for public tests. Source and fixture paths must be explicit.
Compare source hashes before/after read operations. Check actual files, not only
success-shaped JSON.

## Installation

Verify a clean core binary and separately installed bundle. Test project-scoped
installation, collisions, receipt-backed upgrade and safe removal. Existing
modified files must be preserved. No provider settings, model permissions, global
PATH or source stores should be changed as an incidental effect.

## Bundle outcomes

Use a fresh host conversation and ordinary operator requests. Keep expected
answers and test criteria outside those prompts.

| Outcome | Inspect |
|---|---|
| Investigation | Appropriate scope, useful possibilities, later corrections, clear coverage limitations; no unsolicited production |
| Article/tutorial | Actual editable file, supported material claims, scoped outcomes, useful structure and explicit gaps |
| Presentation | Slide source and actual rendered artifact; inspect content, media, layout and offline behavior |
| Incremental long-form | Multiple source collections, chapter continuity, explicit revisions, real book output and reading order |
| Resume/revise | Existing files are discovered; current and superseded work are distinguishable; no duplicate draft hidden as the only result |

The agent should execute tools and inspect results. It should not hand the
operator an internal command sequence or claim that chat-only prose is a stored
deliverable.

## Cost and limits

Record source view, model/reasoning settings, cache condition, input/cache/output
tokens, response sizes, retries and credits per stage. Compare like-for-like
work; fewer tool calls alone do not prove a better or cheaper outcome.

Deterministic core work does not invoke an AI model. Host reasoning still uses
the host's budget. Missing credentials, unsupported host behavior and interactive
checks not actually performed must be reported as limitations, never passes.

## Public evidence

Publish synthetic fixtures and reproducible commands. Keep private transcripts,
business details, local account paths, real session identifiers and operational
budgets outside the public repository. A public evaluation summary must not
reconstruct the private source.
