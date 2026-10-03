# Configuration

The app, deterministic core and external AI CLI have different responsibilities.
Changing one component's state path does not reconfigure another host's account.

## App workspace and state

`midden-ui` resolves the installed sibling core and canonical bundle. Its
no-argument launch keeps workspace and host state outside the installation:

| Platform | App data root |
|---|---|
| Windows | `%LOCALAPPDATA%\Midden` |
| macOS | `~/Library/Application Support/Midden` |
| Linux | `$XDG_DATA_HOME/midden`, otherwise `~/.local/share/midden` |

Use `--workspace PATH` and `--state PATH` together to reopen a deliberate
alternative; `--workspace` without `--state` is an error. `--state PATH` alone
selects a separate host state with the default workspace.

Keep the installation separate from working data. Workspace files are your
ordinary drafts, notes, source collections and output. Host state contains
conversation/runtime data and connection configuration; back it up with the
workspace when moving an installation.

`--no-open` starts the foreground host without opening a browser. `--version`
prints `midden-ui VERSION` without reading or writing application state.
Keep the host's terminal running and use Ctrl+C to stop it.

### Browser drafts

Unsent text is saved in browser storage, scoped by stable workspace identity and
conversation, including a not-yet-created conversation. Accepted messages clear
only matching drafts. This is not cross-device synchronization.

If the browser refuses storage, the UI keeps text in memory, reports the failure
and offers retry. Keep that tab open and copy important text before closing or
clearing browser data. Removing the app does not itself clear browser storage.
Credential inputs are not stored with drafts.

## App model connections

The Compa v1.0.0-backed app exposes two connection types:

| Provider choice | Endpoint and credentials |
|---|---|
| OpenAI-compatible | Blank endpoint uses the official provider; an explicit endpoint may select a compatible local server. Supply an API key when that service requires one. |
| Anthropic-compatible | Blank endpoint uses the official provider; use an explicit compatible endpoint and the credentials required by that service. |

A local server may require no API key. Do not send a service's key to an
untrusted endpoint. Changing provider or endpoint clears inherited API-key and
credential-reference fields; explicitly enter a reference afterward only if you
intend to reuse it.

**Find models** performs a catalog lookup without saving settings or verifying
inference. If discovery is unavailable, an exact manually entered model ID
remains valid input. **Check model** makes a small, inert tool-capability
inference probe, using provider usage but no workspace files. A successful check
does not certify future outputs or every feature of that provider.

**Save settings** selects the connection/model for future turns. "Model
selected" is not evidence that authentication has been verified. Errors from
the provider remain explicit. API keys are write-only: they are not returned
to the form and are cleared after successful requests or when the dialog closes.
You may need to enter a key again for a later request that has not been saved.

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
