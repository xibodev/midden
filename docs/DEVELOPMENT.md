# Development

## Stack

- Go module: `github.com/mekjr1/midden`
- Go version: see `go.mod`
- SQLite: `modernc.org/sqlite` (pure Go)
- UI: embedded HTML, CSS, and JavaScript under `internal\web\ui`
- No Node build
- No CGO requirement

## Build and test

From PowerShell:

```powershell
go fmt ./...
go vet ./...
go test ./...

New-Item -ItemType Directory -Force .\bin | Out-Null
go build -trimpath -o .\bin\midden.exe .\cmd\midden
.\bin\midden.exe version
```

Use a temporary home for manual development:

```powershell
$env:MIDDEN_HOME = (Join-Path $PWD '.midden-dev')
.\bin\midden.exe ui --no-open --port 7788
```

The repository ignores root directories whose names begin with `.midden`, so
this development state does not pollute the worktree.

GNU Make is optional:

```text
make check
make build
```

The documented Go commands are the cross-platform source of truth.

## Package map

| Path | Responsibility |
|---|---|
| `cmd\midden` | CLI, guided start, MCP server, command orchestration |
| `internal\adapter` | Read-only Copilot, Claude, and OpenCode adapters |
| `internal\assay` | Deterministic record classification and evidence candidates |
| `internal\index` | Midden-owned SQLite schema, scans, evidence, recipes, runs |
| `internal\reclaim` | Redacted evidence extraction and parsing |
| `internal\refine` | Evidence-to-artifact planning and templates |
| `internal\refinery` | Yield, recipe, readiness, and output-domain rules |
| `internal\web` | Loopback API, jobs, integrations, production, export |
| `internal\web\ui` | Embedded human interface |
| `internal\exec` | Existing authenticated AI CLI process execution |
| `internal\plugins` | Advanced manifest validation and probes |
| `internal\integrations` | Managed credential-free integration settings |

## Invariants

Keep these properties true:

1. Source stores are opened read-only.
2. Indexed reads do not reparse source stores.
3. Full successful scans reconcile stale rows; partial scans do not claim
   deletion authority.
4. Scan writers serialize across processes.
5. Model calls receive bounded, redacted evidence rather than raw corpora.
6. Preview requests do not mutate durable production state.
7. Recipes require evidence before production.
8. Evidence approval and production approval are separate transitions.
9. Outputs start as drafts and export requires review.
10. Evidence IDs, structured output rows, and provenance sidecars remain
    aligned after review edits.
11. Empty API collections encode as `[]`, not `null`.
12. The UI binds only to loopback and protects state-changing requests.

## UI changes

The UI is served directly from `embed.FS`; edit the files in
`internal\web\ui` and rebuild the Go binary.

There is no generated web bundle to commit.

### Collection discipline

Any collection that can grow with normal use must define:

- a finite page size;
- a visible total and current range;
- Previous/Next or an intentional incremental-history control;
- search/filter state where discovery matters;
- a backend limit or paged query rather than an unbounded read.

Do not render hundreds of sessions, jobs, messages, evidence items, outputs,
records, or provenance entries into one document. Current default page sizes:

| Collection | Page size |
|---|---:|
| Recover sessions | 20 |
| Studio work items | 12 |
| Studio chat history | 30, loaded incrementally |
| Evidence review | 15 |
| Library outputs | 12 |
| Cleanup candidates | 20 |
| Activity jobs, recovery runs, audit | 10 |
| JSONL records | 20 |
| Provenance sources | 15 |

Changing a page size requires a browser check with a collection larger than two
pages. Search and filter changes must reset to page one.

For human testing, exercise:

- normal desktop and compact desktop window widths;
- keyboard-only navigation;
- persistent Studio workspace-agent turns, shell/tool execution in the bounded
  work area, controlled Console, collapsible work-item selection, and budget state;
- OpenMontage setup/test, Backlot launch, video handoff import, and MP4 preview;
- Recover with no assay, exact-session assay, recovery history, and reclaimed-evidence states;
- structured JSONL review and filtered provenance;
- Library filters, downloads, Activity history, and Cleanup eligibility;
- dialog focus trap and focus restoration.

## Test data

Never commit real session stores, databases, evidence, screenshots containing
private content, or `MIDDEN_HOME`.

Use `t.TempDir()` and set `MIDDEN_HOME` in tests. Real-data fixtures are
explicitly ignored under `testdata\real`.

When snapshotting live SQLite state for a local test, use SQLite backup or
`VACUUM INTO`. Copying only `index.db` can omit WAL-backed data.

## Generated local paths

These are not source and must remain untracked:

- `bin\`
- root `midden` or `midden.exe`
- `*.db`, `*.db-wal`, `*.db-shm`
- `.midden\`
- `.quality-run\`
- local agent-tool directories such as `.claude\` and `.codex\`

The historical `prototype\ai-sessions.ps1` is retained as research provenance;
it is not part of the build or installation.

## Release check

Before a release:

```powershell
go fmt ./...
go vet ./...
go test ./...
go build -trimpath -o .\bin\midden.exe .\cmd\midden
.\bin\midden.exe version
```

For subjective UX feedback, optionally run the clean-clone procedure in
[the manual walkthrough](ACCEPTANCE_TEST.md) using a new `MIDDEN_HOME`.
