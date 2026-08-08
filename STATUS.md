# Release status

## Current release

**Midden 2.1.0**

Implementation is complete through the clarification-first evidence refinery.
Automated quality gates pass. The remaining release safeguard is a fresh-clone
human acceptance run using [docs/ACCEPTANCE_TEST.md](docs/ACCEPTANCE_TEST.md).

## Product state

| Area | State |
|---|---|
| Copilot CLI, Claude Code, OpenCode discovery | Complete |
| Read-only cross-tool index and reconciliation | Complete |
| Session search, detail, resume, handoff, and risk | Complete |
| Deterministic assay and yield | Complete |
| Redacted evidence extraction with provenance | Complete |
| Catalog and CLI artifact generation | Complete |
| Persistent recipes, evidence approval, runs, outputs | Complete |
| Clarification-first Conductor | Complete |
| Structured review and provenance filtering | Complete |
| Local reviewed export | Complete |
| Knowledge, agent, personalization output journeys | Complete |
| Managed Connections and advanced manifests | Complete |
| Token-bounded read-only MCP server | Complete |
| Operations search, pagination, cost, and audit | Complete |
| Automatic publishing, agent installation, upload, training | Intentionally not provided |
| Conductor shell execution | Disabled |

## Human-facing behavior

The v2.1 corrective pass establishes these user-visible rules:

- A greeting receives a greeting.
- Ambiguous Conductor input is clarified instead of guessed.
- A design request is previewed in chat before a recipe is created.
- No recipe is recommended or created without reclaimed evidence.
- Assay-only state reports measured signal but does not invent asset counts.
- Mine waits for an in-flight refresh instead of exposing a scan-lock race.
- Evidence approval and production approval are separate.
- Cost approval identifies backend, model, time, calibration confidence, and
  external writes.
- Generated content is a draft until reviewed.
- Structured packs are reviewed as records, not as an unreadable JSONL wall.
- Filtering structured records also filters stored evidence IDs and the
  provenance sidecar.
- Mobile navigation has a scrim and close control.
- Keyboard navigation includes a skip link, dialog focus trap, and focus
  restoration.
- Operations sessions are searched, consequence-sorted, and paginated.

## Safety contract

- Source session stores are opened read-only.
- Midden writes only to its own state directory and explicit derived outputs.
- Scan and assay are deterministic and model-free.
- Model-backed work uses an already authenticated local AI CLI.
- Raw transcripts are not persisted as reclaimed evidence.
- Redaction occurs before evidence is stored.
- Preview actions do not perform the paid action.
- Production writes local drafts and stops before external delivery.
- Cleanup commands default to preview and do not mutate source transcripts in
  place.
- Managed integration settings contain no credentials.
- The web server binds only to loopback.

## Quality evidence

The completed v2.1 automated and assisted-human pass produced:

- Go formatting, vet, unit, integration, and build success;
- 18 of 18 cross-browser headless journeys passing;
- 4 of 4 headed human journeys passing;
- 24 of 24 vision frames passing;
- a 97.9 out of 100 mean visual score;
- zero horizontal overflow at six tested viewports;
- zero undersized controls at six tested viewports;
- measured LCP around 204 ms, CLS 0, and INP 48 ms.

The final evidence bundle is intentionally outside the distributable source
tree. `.quality-run` is generated test output and is ignored.

The release is not marked human-accepted until an operator completes a new
clone, build, configuration, and first production journey.

## Architecture status

- Single Go binary.
- Embedded UI through `embed.FS`.
- Pure-Go SQLite through `modernc.org/sqlite`.
- No Node build.
- No CGO requirement.
- No core Docker dependency.
- Loopback-only HTTP interface.
- JSON-RPC MCP server over stdio.

The index is the fast read path. Complete successful scans reconcile stale
source rows; partial adapters preserve prior rows rather than claiming
deletion authority. Cross-process scan locks, generation stamps, tombstones,
and SQLite triggers prevent overlapping or older writers from resurrecting
stale sessions.

## Known limits

- OpenCode per-session byte accounting requires a full `part` aggregate and is
  opt-in behind `--sizes`.
- Copilot and OpenCode expose no trustworthy live-session marker; reliable live
  detection is Claude-only.
- `watch` polls rather than subscribing to filesystem events.
- Model-backed output quality depends on the selected backend and model.
- A first model-backed estimate is conservative until actual usage calibrates
  later estimates.
- The application cross-compiles to Linux and macOS, but the full human journey
  has been exercised on Windows.
- The Go race detector cannot run on a workstation without a C compiler even
  though normal tests use pure-Go SQLite.
- The repository currently has no configured Git remote. Documentation uses
  `<repository-url>` until a remote is deliberately attached.

## Release gate

The release gate is:

```text
clean clone
→ go test ./...
→ build 2.1.0
→ isolated MIDDEN_HOME
→ Mine
→ small evidence extraction
→ Conductor greeting and clarification
→ plan preview
→ evidence approval
→ draft production
→ review and local export
→ mobile and keyboard check
```

See [docs/ACCEPTANCE_TEST.md](docs/ACCEPTANCE_TEST.md).
