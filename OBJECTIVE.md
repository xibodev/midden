# Midden Objective

## What This App Must Be

Midden is a standalone local-first session-recovery application. It owns source
discovery, session identity, scope, assay, evidence selection, redaction,
provenance, reusable seeds and outputs, recovery-aware cleanup policy, and a
recovery-focused user interface.

Standalone Midden must embed the complete supported Facet Studio kernel for
conversation, providers, authentication, models, tool dispatch, sessions,
events, cancellation, and approvals. Midden must register recovery tools
natively and must not keep its own external-CLI conversation loop as the final
runtime.

The same domain capability must also ship as:

- a Midden Recovery Bundle for a named agentic CLI, with GitHub Copilot
  acceptable as the first supported target; and
- an installable Midden capability module for full Facet Studio.

## Required Order

1. Inspect the current source and tests to establish the real recovery behavior
   and source-safety boundaries.
2. Keep one canonical operation, effect, requirement, artifact, and state model
   across all delivery forms.
3. Make the agent bundle and Studio module independently buildable and testable.
4. Wait for Facet Studio to publish a supported tagged kernel contract.
5. Pin that release without a sibling `replace`, embed it, and remove bespoke
   provider, conversation, budget, and generic approval ownership from the
   standalone path.
6. Verify clean installs, state isolation, source safety, and real recovery
   journeys for all three forms before release.

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
6. Do not create new Markdown plans, status logs, prompts, handoffs, skills, or
   architecture essays. Put durable contracts in code and tests. Use Git history
   only when provenance is necessary.

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
- Standalone completion is blocked until the Studio kernel is tagged, supported,
  pinned, and proven from a clean clone.
- Do not commit, tag, push, publish, or release without explicit user approval.
