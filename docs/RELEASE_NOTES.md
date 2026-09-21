# Core and bundle architecture preview

This preview separates Midden into a deterministic session-data CLI and a
separately installable set of outcome bundles.

- Ordinary read/search/collection operations replace module invocation envelopes.
- Stable views tolerate appended records while rejecting earlier source changes.
- Portable source collections remain distinct from interpretations and drafts.
- Host AI execution replaces the embedded runtime and mandatory editorial
  project/recipe/approval lifecycle.
- Core cache data uses `core-index.db`; legacy `index.db` is not migrated
  automatically. Explicit legacy export preserves recoverable files and tables.
- Public fixtures and packaging are synthetic/allowlisted. Private work is not
  release material.

The old module, workflow MCP and Studio interfaces are breaking changes. Consult
Migration and the matching installer documentation. Optional rendering requires
the dependencies named by the selected outcome bundle.
