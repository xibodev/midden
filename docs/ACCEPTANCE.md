# Independent acceptance

Midden's deterministic core, installers, app host and bundle outcomes require
different evidence. Compiled/runtime checks, local protocol fixtures and browser
tests do not certify live AI content quality or all model providers.

## Core

Run the standard build, full Go suite and vet. Exercise exact source selection,
malformed inputs, partial inventories, bounded reading, append-only growth,
earlier changes/truncation, collection integrity, asset confinement, portable
export and legacy-store preservation.

Use synthetic stores for public tests. Compare source hashes before/after read
operations and inspect actual files, not only successful JSON responses.

## Installation and release

Verify app, core-only and AI CLI entrances independently, including native target
selection, matching versions/checksums, dry runs, owned-file collisions,
receipt-backed upgrades and safe removal. Workspace/state and modified or
unrelated files must be preserved.

App/core bootstraps may perform their documented per-user command-path setup;
`NoPath` must opt out. CLI mode retains its Python 3.9+ project/host binding and
does not incidentally configure providers, MCP servers or host permissions.
No mode installs language runtimes or optional renderers without separate
operator action.

Check that both executables and release metadata identify the same tag/source
commit, and that website bootstrap links resolve to that release's actual
assets. Checksums are integrity evidence, not publisher authentication.
Current release binaries are unsigned. Verify that generated
`THIRD_PARTY_NOTICES.txt` accompanies compiled dependencies and retains Compa
attribution.

## App mechanics

Verify no-argument per-user defaults, sibling core/bundle resolution,
`--workspace`-only compatibility with workspace-local `.midden-ui` state,
foreground shutdown and `--no-open`. The `--version` path must report its
version without state IO.

Exercise setup gating, exact manual model IDs, explicit catalog/check actions,
write-only credentials and provider/endpoint changes. A model catalog is not
verified inference; a tool-capability check is not a content-quality evaluation.

Verify scoped persistent drafts, conversation reload, streamed/final text,
replayed events, Stop, independent permission decisions, mobile visibility and
keyboard focus, host turn outcomes after restart, downloads and isolated
artifact runtimes.

Verify that retired native connections remain visible and require explicit
reconnection, and that history conversion retains legacy bytes and refuses
conflicts. Synthetic HTTP/SSE and local provider fixtures test these contracts
without proving a real account's authorization.

## Bundle outcomes

Use a legitimate provider connection, a fresh conversation in the chosen host
and ordinary requests. Keep expected answers and evaluation criteria outside
those prompts.

| Outcome | Inspect |
|---|---|
| Investigation | Appropriate scope, useful possibilities, corrections, coverage limits; no unsolicited production |
| Article/tutorial | Actual editable file, supported claims, useful structure and explicit gaps |
| Presentation | Slide source and rendered artifact; inspect content, media, layout and offline behavior |
| Incremental long-form | Multiple source collections, continuity, explicit revisions and reading order |
| Resume/revise | Existing files are discovered; current and superseded work are distinguishable |

The host should execute tools and inspect results, not hand the operator an
internal API sequence or claim that chat prose is a stored deliverable.

## Cost, limits and disclosure

Compare source scope, model settings, cache conditions, recorded usage, retries
and actual outputs. Fewer tool calls alone do not prove a better or cheaper
result. Core work never invokes a model; host reasoning and explicit model
checks can use provider usage.

Missing credentials, unsupported behavior and interactive checks not performed
must be reported as limitations, never passes. References prove origin, not
semantic truth; credential filtering is not privacy clearance.

Publish only synthetic fixtures and reproducible public examples. Keep real
transcripts, accounts, session identifiers, local operational paths and private
budgets out of the repository and its screenshots.
