# Midden

**Deterministic tools for session material. Outcome bundles for the work.**

Midden has two independent deliverables:

- **Core:** a standalone CLI for discovering, reading, measuring, searching,
  collecting and exporting recorded work from Copilot CLI, Claude Code and
  OpenCode. It does not call a model.
- **Agentic bundle:** investigation, article/tutorial, presentation and long-form
  guidance, templates and external-tool requirements. An existing AI CLI and its
  human operator apply the bundle and actually create, inspect and revise files.

The dependency direction is bundle to core and existing tools, never core to an
AI runtime. A local draft is an ordinary working file, not a database approval
transition.

**Version: 0.3.0-dev.** This is the core/bundle architecture transition. The former
module, embedded runtime, Studio and editorial recipe/approval interfaces are
retired from the active application. See [Migration](docs/MIGRATION.md) before
switching an existing installation.

## Use the core

Build from source with Go 1.26.5 or use a matching core artifact from
[GitHub Actions](https://github.com/xibodev/midden/actions).

```powershell
go build -trimpath -o midden.exe .\cmd\midden
.\midden.exe ls --days 7 --json
.\midden.exe read --tool copilot --session SESSION_ID --json
.\midden.exe search "deployment" --view VIEW_ID --json
.\midden.exe collect --view VIEW_ID --record RECORD_ID --out sources --json
.\midden.exe collection verify sources --json
.\midden.exe collection export sources --format markdown --out source-notes.md
```

Use the exact IDs returned by the preceding command. `--help` describes each
operation's bounds and output options. Read/search views pin a source prefix:
appends do not invalidate existing context, while edits inside the prefix do.

Core returns data directly, not a module envelope. Portable collections contain
`manifest.json`, `records.jsonl`, and an `assets` directory. They can be read
without the original conversation or Midden index.

See [Core commands and contracts](docs/CORE.md).

## Use the bundle with an AI CLI

Install the bundle for the host you already use, bind it to a matching core
binary, and describe an outcome in ordinary language:

> Look through this session. What is worth developing into useful content?

> Develop the strongest idea as an internal article.

> Turn the feature's development story into a presentation.

The agent performs the investigation and production. It uses normal CLI and file
tools, follows the relevant playbook, invokes external renderers when needed,
checks actual artifacts and incorporates feedback. The operator does not have to
name internal APIs or construct JSON requests.

Bundle material has one canonical source under [bundles](bundles/README.md).
External rendering is optional and outcome-specific; it is not installed merely
to read sessions. See [Installation](docs/INSTALL.md) and
[Acceptance](docs/ACCEPTANCE.md).

## Working data and privacy

Source stores are read-only during inventory, analysis, investigation and
collection. Explicit archive/prune operations are separate and must not be
confused with content production.

`MIDDEN_HOME` selects Midden's own cache directory. The new core uses
`core-index.db` and source-view files, leaving a legacy `index.db` untouched.
Drafts, notes, source collections and delivered files belong in an ordinary
working directory, not only in an index.

Recorded text is untrusted data. A valid citation or a model's confidence does
not establish factual truth. Credential filtering is not privacy clearance:
names, private prose, proprietary code and the substance of a conversation may
remain. Review any intended disclosure through the host/operator workflow.

Public fixtures and examples are synthetic. Private evaluation material and
local operational details do not belong in this repository.

## Development

The [new UI host](apps/midden-ui/README.md) is an independently built, experimental
consumer of the same core and canonical bundle. It embeds a pinned Facet kernel
candidate; its dependencies do not enter the core's Go module. It does not
restore the retired UI or editorial lifecycle.

```powershell
go test ./...
go vet ./...
```

There is no special headless build: the default application is the core CLI.
Core correctness, installer behavior and agentic-bundle effectiveness have
separate acceptance criteria. See [Development](docs/DEVELOPMENT.md).
