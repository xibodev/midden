# Midden 0.4.0

**A journey-based Midden app: sessions, evidence, sources and files without a
model, and an assistant powered by Compa.** The deterministic core and the
canonical outcome bundle serve both entrances: the app and your existing AI CLI.

[Installation](INSTALL.md) |
[Release assets](https://github.com/xibodev/midden/releases/tag/v0.4.0)

## The journey-based app

The app opens on **Sessions**. Its left navigation holds Sessions, Sources,
Files, Assistant and Models; Evidence opens from a session.

- **Sessions**: browse and filter recorded Copilot CLI, Claude Code and OpenCode
  sessions, find text, and read a detail panel with facts, usage and a brief.
- **Evidence**: a pinned source view. Page records, run an explicit search that
  pins a new view, show context around a record, select records, list assets,
  then **Collect into workspace** or **Use in chat**.
- **Sources**: portable collections in the workspace. Check integrity, read,
  search, export as a folder, JSONL or Markdown, and merge. The Investigation,
  Article, Presentation and Long-form starters hand a collection to the
  assistant.
- **Files**: workspace files with a sandboxed preview, download,
  **Ask for a revision** and a quote check against a collection record.
- **Assistant**: conversations with the agent, tool activity with rendered
  results, permission cards and a context tray with **Preview what's sent**.
- **Models**: connections, routes, the default model and **Test tool calling**.

Sessions, Evidence, Sources and Files need no model.

## Model connections through Compa

Models are managed in Compa's own format under host state, in `STATE/kernel`:
`config.json`, `model_catalogs.json` and `auth.json`.

- **Try free models** connects public services that need no key and answer a
  short check. Prompts go to the service that answers; that check does not test
  tool calling.
- **Find local model servers** looks for Ollama, LM Studio, llama.cpp, vLLM,
  LocalAI and Jan on their usual ports.
- Providers from Compa's list connect with an API key or an address.
- An extension service connects by address and optional secret. The providers
  it offers appear; each needs nothing, a token, or a sign-in by device code or
  pasted code.
- Routes are ordered fallbacks. The default model is one exact model or a route.
- **Test tool calling** asks one model for an inert tool call.

Secrets are write-only. The assistant identifies itself as Midden, powered by
Compa, and its memory is off. If the selected model cannot call tools, the turn
stops with a clear message instead of answering without tools.

## Approvals by effect

Deterministic work goes straight to the `midden` core through the app's own
routes: no model and no approval card. Its writes always create new files or
folders inside the workspace.

The assistant's `midden` tool uses the same validator. Read-only and
cache-writing calls run without a card. Calls that write the workspace, file
writes and shell commands ask for Allow or Deny, one operation at a time.
**Approved shell commands run with your account; the app is not an OS sandbox.**

## Core JSON contract

Each command's `--json` output is one JSON document with snake_case keys. Lists
are `[]` when empty, never `null`, and free text passes credential filtering.
Failures exit non-zero with a reason on stderr. See
[JSON output](CORE.md#json-output).

## Launch

- A no-argument launch uses per-user data: `workspace` and `host-state` under
  the app data root, outside the installation.
- `--workspace` must be used together with `--state`; `--state` alone keeps the
  default workspace.
- `--no-open`, `--listen` (loopback only), `--core` and `--bundle` adjust the
  launch; `--version` reports the version without reading or writing state.
- Keep the terminal running; Ctrl+C stops the host.

## Installation

- The default per-user bootstrap installs the app with the matching `midden`
  core and bundle. The packaged app needs no Go, Node.js or Python.
- Core-only and AI CLI installation are explicit modes. CLI mode uses the
  Python 3.9+ project-scoped, receipt-based installer.
- Stop a running app before upgrading. Upgrade and uninstall checks apply to
  owned installation files, not to workspaces, host state or source stores.

## Version and validation boundary

Release builds inject the tag's version into the app and core from the same
source commit; do not mix artifacts from different builds. Select a release
candidate explicitly rather than relying on `latest`.

Compiled/runtime checks, scripted local providers and browser tests exercise
mechanics. They do **not** certify live AI content quality, every provider, or
an account's authorization. Live acceptance needs legitimate provider
credentials, ordinary goals and inspection of actual artifacts.

Checksums establish integrity against the release manifest, not publisher
authenticity. Release binaries are unsigned; checksum agreement is not a
publisher signature. Release archives include generated
`THIRD_PARTY_NOTICES.txt` for compiled dependencies, retaining Compa
attribution. Preserve `LICENSE`, copyright and those notices when
redistributing files.
