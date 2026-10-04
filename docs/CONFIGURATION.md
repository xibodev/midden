# Configuration

The app, deterministic core and external AI CLI have different responsibilities.
Changing one component's state path does not reconfigure another host's account.

## App workspace and state

`midden-ui` resolves the installed sibling core and canonical bundle. Its
no-argument launch keeps `workspace` and `host-state` under a per-user data root,
outside the installation:

| Platform | App data root |
|---|---|
| Windows | `%LOCALAPPDATA%\Midden` |
| macOS | `~/Library/Application Support/Midden` |
| Linux | `$XDG_DATA_HOME/midden`, otherwise `~/.local/share/midden` |

Use `--workspace PATH` and `--state PATH` together to reopen a deliberate
alternative; `--workspace` without `--state` is an error. `--state PATH` alone
selects a separate host state with the default workspace.

Keep the installation separate from working data. Workspace files are your
ordinary drafts, notes, source collections and output. Host state holds
conversations, model connections and keys (`kernel`), the app's own core cache
with its pinned views (`core`) and the mounted bundle. Back it up with the
workspace when moving an installation.

`--no-open` starts the foreground host without opening a browser. `--listen`
selects another loopback address (default `127.0.0.1:18890`); `--core` and
`--bundle` select a specific core executable and bundle. `--version` prints
`midden-ui VERSION` without reading or writing application state. Keep the
host's terminal running and use Ctrl+C to stop it.

### Browser drafts

Unsent text is saved in browser storage, scoped by stable workspace identity and
conversation, including a not-yet-created conversation. Accepted messages clear
only matching drafts. This is not cross-device synchronization.

If the browser refuses storage, the UI keeps text in memory, reports the failure
and offers retry. Keep that tab open and copy important text before closing or
clearing browser data. Removing the app does not itself clear browser storage.
Credential inputs are not stored with drafts. Records selected in Evidence stay
selected for that browser tab, per workspace.

## App model connections

The **Models** view manages connections in Compa's own format. `midden-ui` binds
Compa's home to `STATE/kernel` and reads no global Compa configuration:

| File in `STATE/kernel` | Holds |
|---|---|
| `config.json` | Connections, routes and the default model |
| `model_catalogs.json` | Each connection's model list |
| `auth.json` | API keys, tokens, sign-ins and the extension service secret |

Tool-calling results are kept in `STATE/model-checks.json`. Opening Models
contacts no provider; connecting, refreshing and testing do.

| Section | What happens |
|---|---|
| Free models, no key | **Try free models** checks a few public services that need no key and connects those that answer a short check. Your prompts go to the service that answers. The check does not test tool calling. |
| On this computer | **Find local model servers** looks for Ollama, LM Studio, llama.cpp, vLLM, LocalAI and Jan on their usual ports on this computer. **Connect** adds a server it found. |
| With an API key or address | Choose a provider from Compa's list and enter its API key and, where needed, its address. Connecting reads the provider's model list; a connection whose models cannot be listed is not saved. |
| From an extension service | Enter the address of a separately running extension service and its secret, if it needs one. The providers it offers appear; each is ready, or needs a token or a sign-in by device code or pasted code. |

A connection's first chat model becomes the default when none is set. In
**Default model**, choose one exact model or a **route**: an ordered list of
models where, if one is busy or fails, the next answers. **Refresh models**
reads a connection's list again; routes and the default drop models it does not
list. **Remove** is refused for a connection that a route uses; take it out of
the route first.

**Test tool calling** asks one model for an inert tool call. It reads no
workspace files and runs no tool, but uses the provider's service. The assistant
needs tool calls: when the selected model cannot make them, the turn stops with
a message saying so rather than answering without tools. A passed test does not
certify future output.

Secrets are write-only. No answer returns an API key, token, sign-in or service
secret, and error messages redact them. A stored extension service secret is
reused only for the address it was saved for; do not send a service's key to an
untrusted address. Model changes and tests are refused while a turn runs; a
saved change applies from the next turn.

## Core cache and source scope

`MIDDEN_HOME` selects the deterministic core's cache directory. Without it, the
core uses `.midden` under the resolved user home.

- `core-index.db` contains derived session metadata, measurements and maintenance
  records.
- `views` contains bounded, pinned source views for continued investigation.

The core stores no model credentials. Collections and authored work belong in
explicit working directories rather than only in a cache.

| Source tool | Default recorded source |
|---|---|
| Copilot CLI | `.copilot` under the user profile |
| Claude Code | `.claude` under the user profile |
| OpenCode | `.local/share/opencode/opencode.db` under the user profile |

Material commands accept `--copilot-root`, `--claude-root` and `--opencode-db`.
`--sources-only` disables fallback to other profile locations. The core's
`--state` changes its cache location, independently of those sources.

For a shell or host conversation, `MIDDEN_COPILOT_ROOT`, `MIDDEN_CLAUDE_ROOT`
and `MIDDEN_OPENCODE_DB` select explicit source locations. When any are set,
that set is closed: unrelated ambient stores are not read. This does not change
the AI CLI's own home, authentication or session storage.

The app runs the core with `MIDDEN_HOME` set to `STATE/core` and passes only the
source locations set when it started. Its pinned views stay with that host
state, separate from the core cache of your shell.

Changing `MIDDEN_HOME` does not move sources. Investigation, reading, collection
and asset extraction leave source stores read-only.

## External AI CLI and bundle tools

CLI mode binds installed guidance to a specific core executable and scoped
state. It does not configure the host's models, MCP servers or permissions.
Use that AI CLI's own settings and authenticated account independently of the
Midden app's provider support.

External renderers belong to particular outcomes. See the
[bundle guide](../bundles/README.md) and [tool requirements](../bundles/midden-shared/tools.md).

Both entrances leave publication decisions to you. Approved shell commands in
the app run with your account, not inside an OS sandbox. Content sent to remote
providers is subject to those connections and their policies.
