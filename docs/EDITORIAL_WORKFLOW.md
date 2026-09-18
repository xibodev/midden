# Agent-first editorial workflow

Midden provides bounded source access and durable editorial state. The host
agent interprets the material; Midden does not start a second agent loop.
Studio is paused. Nothing in this workflow needs Studio or changes source stores.

## Discover tools, not scripts

```powershell
midden agent list
midden agent schema evidence.prepare
midden agent schema projects.create
midden agent schema editorial.analyze
```

`midden agent <capability> --input request.json --home <state-directory>` accepts
the capability's payload, not a module envelope. Use `--input -` for stdin.
`module invoke` still accepts full envelopes, including explicit read-only source
roots, and now also accepts stdin. Use the installer-bound state path or
`MIDDEN_HOME` consistently: these are not the same as source-store locations.

For example, pipe an exact-source request to the installed binary:

```powershell
'{"source":{"tool":"copilot","session_id":"EXACT-SESSION-ID"},"max_records":40}' |
    midden agent evidence.prepare --input -
```

The session ID is **not** prefixed with `copilot:`; the source tool is a separate
field. The placeholder must be replaced with an ID returned by `sessions.list`.

## Source to editorial project

1. Narrow `sessions.list`, then run the free `sessions.assay`. Counts and bytes
   describe composition, not publishable value.
2. Check `evidence.list` using `tool` and exact `session_id`, plus bounded
   `limit`/`offset`, before re-extracting.
3. `evidence.prepare` returns at most 80 redacted excerpts from one exact source,
   record IDs, signal denominators, and a packet digest. The default is 40.
   The host performs semantic extraction, then `evidence.compose` verifies the
   current digest and every record citation before storing host-authored items.
   This is **no additional model invocation by Midden**, not free host reasoning.
4. `projects.create` requires a title, goal, exact `sources` and selected
   `evidence_ids`. It rejects evidence outside that corpus.
5. `editorial.prepare` supplies the scoped evidence and investigative guidance.
   The host authors an analysis and submits it to `editorial.analyze` with the
   inspected `expected_revision`.
6. Compare opportunities by hook, audience, purpose, evidence, caveats, usefulness,
   effort and risks. A format catalog or nugget count is not an editorial map.
   Analysis validation verifies references and consistency, not factual truth.

Record decision supersession explicitly; a contradicted claim cannot be marked
supported. Describe unverified claims and unresolved gaps rather than inventing
missing facts. Asset locators are relative **references**, not proof that an image
has been inspected or permission to read arbitrary files. Time-based image
clusters do not establish visual duplicates.

## Selected opportunity to reviewed output

`editorial.select` creates a **draft recipe**, never approval. Its evidence and
request contain only the selected story, its claims, reversal chain, gaps, assets,
and risks. Open gaps need explicit `acknowledge_gaps` to proceed with disclosure.

Inspect the recipe, present its exact evidence to the operator, and use
`recipes.evidence` with `decision: approved` only after that review. The host
authors drafts and submits `recipes.compose`; no nested model is needed.
Deterministic packs use the same recipe lifecycle; submit `"drafts": {}` for a
deterministic-only recipe. Every narrative output still requires an authored draft.

Inspect each output and check material claims against the cited evidence.
`outputs.review` requires an inspected `expected_digest` and a decision.
Approval of evidence is not approval of the draft. Review decisions represent
the operator, not the agent's self-review. Export only through `outputs.export`;
it verifies reviewed bytes and provenance. Rendering remains a separate step
and requires installed renderers and inspection of the actual delivered format.

## Multi-session work and long-form projects

`projects.update` replaces the explicit source/evidence scope at a known
revision, preserves history, and marks existing analysis stale. Prepare and
submit a fresh analysis before selecting another opportunity. No new sessions
are silently added because they share a directory.

Analysis can retain a series/book chapter plan: each chapter references an
opportunity, dependencies, notes, status and optionally an output. Dependency
cycles and unknown references are rejected. A complete chapter needs a reviewed
or exported output belonging to that project's selected opportunity. Persist
partial progress; do not write a book merely because the corpus is large.
`projects.inspect` accepts an optional historical `revision` for comparison.

## Local production handoffs

`handoffs.create` takes `project_id`, `expected_revision`, a `target`, and
explicit `output_ids`. It verifies the outputs belong to the project's selected
opportunities and that their reviewed bytes are unchanged. Quarto chapter
dependencies must appear before dependents. It writes a local portable directory:

```text
manifest.json         output identities, relative paths and digests
editorial.json        selected historical analyses, not the whole corpus
sources/              reviewed editable source and provenance sidecars
README.md             adapter instructions and unresolved delivery boundaries
_quarto.yml           only for a Quarto book handoff
```

Supported targets are `markdown`, `quarto`, `pandoc`, and `d2`.
Generic video briefs remain Markdown content and can use the `markdown` target.
They are source documents, not rendered videos or a video-runtime integration.
No adapter launches software, installs dependencies, uploads, or publishes.
The assembled handoff stays `unreviewed`; reviewed sources do not establish that
the book, diagram, presentation, or video has been rendered or is ready to publish.
Handoffs are recorded in the existing artifact inventory.

## Limits and acceptance

Projects contain at most 25 exact sources and 300 selected evidence items, with
256 KiB of evidence and 512 KiB of project state per revision. Lists are bounded.
Narrow a corpus instead of constructing a whole transcript through repeated
requests. Changes to selected evidence invalidate preparation/selection until
the project is explicitly refreshed. Concurrent edits require reinspection.

Synthetic tests exercise the source-to-review/export lifecycle, stale digests,
scope confinement, reversed decisions, chapter dependencies and retry safety.
They do not establish narrative quality or live acceptance in every host.
Do not commit private sessions, screenshots or extracted work as fixtures.

Feature branches targeting `staging` have a dedicated **Stage agentic bundle**
workflow. It runs focused checks and packages only the headless binary and declared
skills/overlay into staging artifacts. It does not publish a release or deploy
Pages. The full Go suite remains opt-in through its `full_tests` dispatch input;
the repository's existing release validation is unchanged.
