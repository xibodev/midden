# Independent acceptance

Midden's deterministic core, installers, app host and bundle outcomes require
different evidence. Compiled/runtime checks, local protocol fixtures and browser
tests do not certify live AI content quality or all model providers.

## Core

Run the standard build, full Go suite and vet. Exercise exact source selection,
malformed inputs, partial inventories, bounded reading, append-only growth,
earlier changes/truncation, collection integrity, asset confinement and portable
export.

Use synthetic stores for public tests. Compare source hashes before/after read
operations and inspect actual files, not only successful JSON responses. Check
that `--json` results keep the [JSON output contract](CORE.md#json-output).

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

Verify no-argument per-user defaults, sibling core/bundle resolution, rejection
of `--workspace` without `--state`, `--state` alone with the default workspace,
foreground shutdown, `--no-open` and a loopback-only `--listen`. The `--version`
path must report its version without state IO.

Deterministic journeys must work with no model connected and without approval
cards: Sessions (filters, text find, facts, usage, brief), Evidence (paging,
search pinning a new view, context, selection, assets, collection), Sources
(integrity, read, search, export, merge) and Files (preview, download, quote
check). Each write must create a new path inside the workspace and refuse an
existing one. Inspect the actual files, not only the success message.

Run agent journeys against a scripted local provider: tool activity and
rendered results, permission cards by effect (none for reads and cache writes;
Allow/Deny for workspace writes, file writes and shell commands), independent
decisions and denial, Stop, a turn stopped because the model cannot call tools,
context handed over from other journeys, scoped persistent drafts, conversation
reload, replayed events, host turn outcomes after restart, mobile visibility,
keyboard focus, downloads and isolated artifact runtimes.

Exercise Models against synthetic services: free-model outcomes, local server
detection, API-key connections, extension services with token and sign-in
flows, routes, the default model, **Test tool calling** and write-only secrets.
A passed tool-calling test is not a content-quality evaluation.

Scripted providers and synthetic HTTP/SSE fixtures test these contracts without
proving a real account's authorization. Live-provider effectiveness is accepted
separately, under bundle outcomes.

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
