# Midden Objective

## What This App Must Be

Midden is an agent-first, local evidence-to-content bundle. Its canonical
experience runs inside the operator's authenticated agentic CLI. It owns source
discovery, exact session scope, assay, evidence selection, editorial projects,
provenance, reusable seeds, reviewed outputs, and recovery-aware cleanup policy.

Standalone Studio is paused and experimental. Preserve its existing behavior;
do not add a second agent loop or make new agent-first features depend on it.
If resumed later, Studio must consume the same domain operations through the
supported Facet Studio kernel, not implement parallel editorial semantics.

The same domain capability must also ship as:

- a Midden Recovery Bundle for a named agentic CLI, with GitHub Copilot
  acceptable as the first supported target; and
- an installable Midden capability module for full Facet Studio.

## Required Order

1. Inspect the current source and tests to establish the real recovery behavior
   and source-safety boundaries.
2. Keep one canonical operation, effect, requirement, artifact, and state model
   across all delivery forms.
3. Keep the headless agent bundle independently buildable and testable.
4. Prove exact-session investigation, competing editorial opportunities,
   human selection, host-authored composition, review, and local export.
5. Extend the same project model to multi-session series, chapter plans, and
   local production handoffs without owning renderers or model credentials.
6. Validate feature branches against staging before release. Defer full
   cross-host and Studio acceptance when explicitly requested; never imply
   focused checks certify those journeys.

## Start Clean

1. Read only this file for intent. Do not reconstruct plans from deleted
   documentation.
2. Run `git status` and preserve all existing work unless the user explicitly
   asks to replace it.
3. Inspect adapters, assay, index, reclaim, refinery, web, module, installer,
   provider-adapter, view, `go.mod`, and test code.
4. Derive supported sources, operations, state paths, safety guarantees, and
   release state from executable code and tests. Verify UI claims against the
   backend that enforces them.
5. Run focused baseline checks before editing. Distinguish read-only recovery
   operations from explicit source mutations such as archive.
6. Keep planning in the host's task tracker. Ship only product documentation
   and purpose-built agent skills; keep executable contracts in code and tests.

## Boundaries

- Midden owns recovery semantics; it does not own conversational providers,
  model catalogs, routes, credentials, or fallback.
- Do not build a second agent loop, session engine, budget engine, hook system,
  or generic approval engine.
- Prompt instructions are not an authorization boundary.
- Redaction reduces risk but never proves that all sensitive content is absent.
- Indexing, assay, evidence extraction, and MCP reads must not modify source
  stores. Any archive action must be explicit, accurately described, and tested.
- Standalone, bundle, and module state must not mix implicitly.
- Studio is not a release criterion for agent-first feature branches.
- Do not commit, tag, push, publish, or release without explicit user approval.
