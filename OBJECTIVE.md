# Midden Objective

Midden has two product layers, delivered through an existing AI CLI or the
Midden app. The deterministic core is not an agent.

## Core

The `midden` command is a deterministic session-data toolbox. It discovers and
reads supported stores, normalizes records, provides stable bounded source views,
searches and measures material, and collects/manipulates/exports portable data
and available assets. It is useful to humans, scripts and AI hosts alike.

Core must not depend on an agentic bundle, an AI runtime, model providers,
editorial recipes or approval receipts. Source stores are read-only during
investigation. Any retained archive/prune operation must be explicit and tested.

SQLite is a derived index/cache. Authoritative selected material is portable and
inspectable as files. A record reference proves where text came from, not that
the text's claim is true. Credential filtering is not privacy clearance.

## Agentic bundle

Outcome-oriented guidance, templates, examples, helpers and external-tool
requirements enable an existing AI CLI and operator to investigate and produce
content end to end. The AI executes the method: it reads, reasons, writes files,
runs tools, inspects outputs, revises and delivers. The operator directs and
evaluates the work.

Ship investigation, article/tutorial, presentation and incremental long-form
workflows. Keep one canonical source for bundle materials. Ordinary working files
hold source collections, notes, outlines, drafts and delivered artifacts; a
database project is not a prerequisite for creating or finding a draft.

The host owns conversation, models, permissions and credentials. Bundle guidance
does not bypass host denials or grant publication authority.

## Retired boundaries

The old module adapter, host descriptors/grants and editorial database remain
retired. AI/provider execution does not belong in the core. The separate
`apps/midden-ui` Go module embeds Compa to execute the same canonical bundle;
it is a host, not a second implementation of core or bundle behavior.
MCP, if retained, is only a thin optional core adapter, not a second lifecycle.

Do not recreate a generic approval, provider, session, budget or orchestration
engine. External rendering belongs to existing tools invoked by the host agent
under bundle guidance.

## Execution and acceptance

1. Establish and test core data contracts before developing bundle outcomes.
2. Validate core deterministically, including source preservation, scope,
   append-only reads, truncation/edits, asset handling and portable collections.
3. Validate bundle effectiveness through natural operator goals and actual
   editable/rendered artifacts, not a prescribed tool-call sequence.
4. Preserve private sessions, old test labs and existing work. Test migration on
   copies and provide explicit read-only legacy export. Never commit private
   source material.
5. Run full relevant tests, clean-install checks and Windows/Linux staging CI.
   Do not label missing credentials or interactive-only checks as passes.
6. Keep plans in the host task tracker. Ship product documentation and reusable
   bundle materials, not implementation diaries.

Use feature branches and release validation. Keep private evaluation transcripts,
source identities, internal business details and local operational settings out
of public commits. Publish only synthetic examples and reviewable product changes.
