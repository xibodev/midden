# Midden UI host

This is a host for the canonical Midden bundle. It embeds Compa's public kernel
in its own Go module. The deterministic core and canonical bundle remain
independent.

The kernel is the published `github.com/xibodev/compa` release pinned in this
module's `go.mod`; the startup line and the app footer name its version. No
separate Compa application, web UI or kernel process is required.

## Run

In an extracted native UI package, run `midden-ui.exe` on Windows or
`./midden-ui` on macOS/Linux. The executable finds the core and canonical bundle
beside itself. It opens the browser only after binding the loopback listener;
`--no-open` suppresses that step. Keep the terminal running; Ctrl+C stops the host.
`--version` prints the UI release version without opening user state.

Default writable data stays outside the installation:

| Platform | Workspace and host state parent |
|---|---|
| Windows | `%LOCALAPPDATA%\Midden` |
| macOS | `~/Library/Application Support/Midden` |
| Linux | `$XDG_DATA_HOME/midden`, or `~/.local/share/midden` |

The default creates `workspace` and `host-state` beneath that parent. An explicit
`--workspace` requires `--state`; `--state` alone uses the default workspace.
`--core`, `--bundle` and `--listen` override the sibling core, the sibling bundle
and the address; `--listen` accepts only a loopback IP address.

Building from source requires Go 1.26.6 for this host. Build the normal core from
the repository root, then build this module separately:

```powershell
go build -o midden.exe .\cmd\midden
Set-Location apps\midden-ui
go build -o midden-ui.exe .
.\midden-ui.exe --workspace WORK_DIRECTORY --state HOST_STATE_DIRECTORY --core CORE_EXECUTABLE --bundle BUNDLE_DIRECTORY
```

An explicitly chosen workspace must exist and must not be a source store.
The bundle path accepts the canonical `bundles` directory or its extracted
distribution root. The default address is `http://127.0.0.1:18890`.

For an isolated rehearsal, set `MIDDEN_CLAUDE_ROOT`, `MIDDEN_COPILOT_ROOT`, or
`MIDDEN_OPENCODE_DB` to synthetic source stores before launch. With none set, the
core's normal source discovery applies. Investigation does not mutate sources.

## Journeys

The left navigation holds Sessions, Sources, Files, Assistant and Models;
Evidence opens from a session or from an assistant result.

| Journey | What it does |
|---|---|
| Sessions | Browse and filter recorded sessions, find text across Copilot CLI and Claude Code transcripts, and read a detail panel with facts, usage and a brief. **Copy resume command** and **Ask the assistant about this session** start from here. |
| Evidence | Page a pinned source view, search it (each search pins a new view), show the two records before and after one, select records, list assets, then **Collect into workspace** or **Use in chat**. |
| Sources | Check integrity, read, search, export and merge portable collections in the workspace. Export writes a folder, or JSONL or Markdown for collections without assets. The Investigation, Article, Presentation and Long-form starters hand a collection to the assistant. |
| Files | List workspace files, preview them read-only (HTML in a sandboxed frame), download them, **Ask for a revision**, and check a quote against a collection record. |
| Assistant | Conversations with the agent, tool activity with rendered results, permission cards, and a context tray with **Preview what's sent**. |
| Models | Connections, routes, the default model and **Test tool calling**. |

Sessions, Evidence, Sources and Files need no model. Context handed to the
assistant is appended to the next message as a visible "Context selected in
Midden:" block; outcome starters fill only an empty composer.

The assistant is Midden, powered by Compa. Memory is off. It requires tool
calls: if the selected model cannot make them, the turn stops with a message
saying so instead of answering without tools. Sending needs a working default
model.

Stop a turn when needed. Cancellation does not undo already completed file
writes. Refresh/reopen the host to continue stored conversation history. Turns
retain completed, failed, cancelled or interrupted outcomes beside their
request; a host restart marks unfinished attempts interrupted rather than
silently showing them as completed.

Unsent drafts are saved in this browser, scoped to the workspace and host-state
identity as well as the conversation. They survive page reloads and are cleared
only when the matching message is accepted. This is a local convenience cache,
not encrypted backup or cross-device synchronization; clear site data when
using a shared browser. Storage failures are reported rather than silently
claiming that a draft was saved. Model credentials never enter this draft cache.

## Models

Model connections are stored in Compa's own format under `STATE\kernel`:
`config.json` holds connections, routes and the default model,
`model_catalogs.json` the model lists, and `auth.json` keys, tokens, sign-ins
and the extension service secret. Tool-calling results are kept in
`STATE\model-checks.json`. Opening Models contacts no provider.

- **Try free models** checks a few public services that need no key and
  connects those that answer a short check. Prompts go to the service that
  answers; tool calling is not tested by that check.
- **Find local model servers** probes Ollama, LM Studio, llama.cpp, vLLM,
  LocalAI and Jan on their usual loopback ports and offers **Connect**.
- **With an API key or address** lists Compa's providers. Connecting reads the
  provider's model list; a connection whose models cannot be listed is not saved.
- **From an extension service** connects a separately running service by
  address and optional secret. The providers it offers appear; each is ready,
  or needs a token or a sign-in by device code or pasted code.
- **Routes** are ordered fallbacks: if one model is busy or fails, the next
  answers. The default model is one exact model or a route.
- **Test tool calling** asks one model for an inert tool call. It reads no
  workspace data and executes no tool, but may incur provider usage.

Secrets are write-only: the models API never returns a key, token, sign-in or
service secret, and errors redact them. A stored service secret is reused only
for the address it was saved for. Model changes and tests are refused while a
turn runs; a saved change applies from the next turn.

## Approvals by effect

The operator's door is `/api/core/*`: Sessions, Evidence, Sources and Files call
the core there, with no model and no approval card. Each route declares its
effect and the host refuses a call whose arguments do not match it. Writes
always create a new file or folder inside the workspace; an existing path is
refused.

The assistant's `midden` tool uses the same validator. Its effect is derived
from the parsed arguments:

| Effect | Examples | Permission card |
|---|---|---|
| Read-only | `ls`, `find`, `show`, `brief`, `usage`, paging a pinned view, `collection inspect`/`read`/`search`/`verify` | None |
| Writes cache | `read` that pins a view or reads context around records, `search`, `assay` | None |
| Writes workspace | `collect`, `collection select`/`merge`/`export`, any `--out` | Allow / Deny |

The cache is the app's own core cache in host state (`STATE\core`). Workspace
reads (`read_file`, `list_dir`, `load_image`) run without a card; file writes
(`write_file`, `edit_file`, `append_file`) and shell commands ask for Allow or
Deny. File tools read only the workspace and the mounted bundle and write only
the workspace; other host state is refused. An unanswered card is denied after
four minutes. A denial is final for that operation and is not an editorial
approval record in Midden core.

Approved shell commands run as the operator's account. This is **not an OS
sandbox**. Do not approve a command you would not run yourself. The `midden`
tool blocks source maintenance and host-binding overrides.

One UI process owns one workspace and isolated kernel state root. The host binds
`COMPA_HOME` to `STATE\kernel` and never reads a global `~/.compa` configuration
or key store. Model credentials are written only to that kernel auth store.
Generic read tools cannot inspect host state. The loopback API rejects foreign
hosts/origins and requires a process CSRF token for mutations. Kernel
bookkeeping and history stay in host state, not in the authored-file workspace.
The public prompt-contributor API supplies the actual artifact tool binding;
agents inspect workspace instructions through scoped file tools. An explicit
runtime tool policy is applied before registration; the model sees only the
scoped Midden and workspace tools.

## Bundles and artifacts

The host maps canonical outcome folders to the kernel's advertised skill names
without changing their bytes, keeping shared references/templates beside them.
The kernel executes file and shell tools; the UI lists real workspace files.
No editorial project table is required for a saved draft.

HTML artifacts are displayed in a sandboxed iframe with network access disabled.
Downloads preserve the original file. Pandoc and a browser/Playwright environment
are optional tools needed only for corresponding production and inspection
outcomes. Missing tools are reported, not installed silently.

## Tests

```powershell
go test ./...
go vet ./...
```

Kernel integration tests use a local synthetic HTTP provider and exercise the
real public agent/tool loop, permissions, cancellation and persisted history.
They are not evidence that every real provider/account configuration works.

Browser tests cover the journeys against a synthetic host. From the repository
root, with an existing Python Playwright installation and its Chromium browser:

```powershell
python -B -m unittest discover -s apps/midden-ui/webtest
```

`testsupport/provider.py` is a deliberately synthetic local protocol fixture for
full UI/kernel/tool integration. It is not a live model and must never be
presented as model-quality or real-account authentication evidence.

For a local native testing directory, run `python package.py --out ABSOLUTE_PATH`
from this module with Go on PATH. The directory must be new and outside the
checkout. The package contains the UI executable, independently built core and
unchanged canonical bundle; optional production renderers are not bundled.
