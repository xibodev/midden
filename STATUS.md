# Development status

## Current development version

**Midden 0.0.1 — unreleased**

The product now uses the standalone recovery-workbench architecture approved
after human UAT. Kuse remains a possible optional client; Midden's Go engine,
SQLite state, source reconciliation, evidence, provenance, and cleanup policy
remain authoritative.

## Product state

| Area | State |
|---|---|
| Copilot CLI, Claude Code, OpenCode discovery | Complete |
| Read-only cross-tool index and reconciliation | Complete |
| Deterministic assay and source fingerprints | Complete |
| Redacted evidence extraction with real depth limits | Complete |
| Durable exact-scope recovery runs | Complete |
| Restart-safe background job history | Complete |
| Persistent recipes/work items, runs, outputs | Complete |
| Persistent per-work-item AI CLI conversations | Complete |
| Work-item budget envelopes | Complete |
| Controlled diagnostic Console | Complete |
| Rendered Markdown and structured data review | Complete |
| Optional D2/media inline renderer | Complete when installed |
| Owned-file browser downloads | Complete |
| Library filters and direct output access | Complete |
| Recovery-aware cleanup eligibility | Complete |
| Archive application blocked outside eligibility | Complete |
| Activity, cost, and audit views | Complete |
| Tools, managed integrations, advanced manifests | Complete |
| Token-bounded read-only MCP server | Complete |
| Unrestricted host terminal | Intentionally not provided |
| Automatic publishing, installation, upload, training, purge | Intentionally not provided |

## Primary workflow

```text
Recover exact sessions
→ durable assay/evidence run
→ Studio work item
→ persistent AI CLI conversation
→ evidence approval
→ local draft production
→ rendered preview/source/provenance
→ download or reviewed export
→ explainable archive eligibility
```

## Human-facing behavior

- Long-running work starts in the background and does not own the screen.
- Mine history records exact scope, depth, counts, timestamps, and failures.
- Session selection is exact; evidence extraction no longer silently truncates
  to eight sessions.
- Summary, Deep, and X-ray use different record limits.
- Studio always retains its work-item list.
- Routine chat resumes the same AI CLI session and uses one cumulative budget
  envelope instead of a per-turn cost dialog.
- Console accepts only allowlisted diagnostics.
- Markdown is rendered as a document.
- JSON and JSONL are shown as readable records.
- D2 and media use optional inline renderers; absent tools produce an honest
  source fallback.
- Output source can always be downloaded.
- Library is independent from the selected Studio item.
- Cleanup states why a session is eligible, held, or protected.
- Web archive application refuses sessions that have not satisfied recovery
  eligibility.
- Midden targets desktop use. Compact desktop windows remain usable; phone
  compatibility is not a product or release goal.

## Safety contract

- Source session stores are opened read-only for indexing, assay, evidence,
  and Studio work.
- Background job persistence removes answer bodies and evidence bodies; the
  authoritative content remains in purpose-built message/evidence tables.
- Raw transcripts are not copied into work-item chat or generated drafts.
- Redaction occurs before evidence is stored.
- Preview requests do not perform paid or destructive work.
- Persistent chat never silently falls back to a new context after a failed
  resume.
- Production writes local drafts and stops before external delivery.
- Console is allowlisted and never invokes a host shell.
- Download paths are confined to Midden's refinery artifact directory.
- Archive is explicit, reversible, and recovery-gated.
- The web server binds only to loopback and protects state-changing requests.

## Automated and local UAT evidence

- Go formatting, vet, full tests, and build pass.
- Persistent jobs survive manager recreation and interrupted jobs close as
  failed after restart.
- Two real Studio turns resumed one Copilot CLI session and persisted messages
  in user/agent order.
- An exact-session mine persisted its exact scope and left every source file
  size and modification time unchanged.
- Summary and Deep extraction previews recorded distinct depth limits without
  invoking a model.
- Desktop browser journeys pass at 1440×900 and 1024×768 with zero body-level
  horizontal overflow across all six views.
- Rendered Markdown, editable source, provenance, D2 fallback, downloads,
  controlled Console, Library filters, Cleanup details, Activity, and Tools
  were exercised against copied real state.

## Architecture status

- Single Go binary.
- Embedded HTML/CSS/JavaScript UI.
- Pure-Go SQLite.
- No Node build.
- No CGO requirement.
- No core Docker dependency.
- Loopback-only HTTP interface.
- JSON-RPC MCP server over stdio.
- Optional external renderers and integrations remain independently installed.

## Known limits

- OpenCode per-session byte accounting remains opt-in behind `--sizes`.
- Reliable live-session detection remains Claude-only.
- Background external processes cannot resume after the Midden process exits;
  their durable job row is marked interrupted instead of remaining falsely
  running.
- Studio responses are returned when the selected CLI turn finishes; token
  streaming is not yet exposed incrementally.
- D2/media inline previews require the matching locally installed renderer or
  file type.
- Cleanup eligibility treats reviewed output references as the current evidence
  ownership gate; record-level evidence review is not yet stored independently.
- The optional manual walkthrough remains available for subjective UX feedback;
  it is not an engineering completion gate.

## Release gate

```text
clean clone
→ go test ./...
→ build 0.0.1
→ isolated MIDDEN_HOME
→ exact-session mine
→ small evidence extraction
→ Studio work item and evidence approval
→ two persistent chat turns
→ draft production
→ rendered preview/source/provenance/download
→ Library and Cleanup inspection
→ desktop keyboard and compact-window check
```

See [docs/ACCEPTANCE_TEST.md](docs/ACCEPTANCE_TEST.md) only when a manual
product walkthrough is useful.
