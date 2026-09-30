# Midden UI host

This is a new host for the canonical Midden bundle, not a restored version of
the old Studio UI. It embeds Compa's public kernel in its own Go module.
The deterministic core and canonical bundle remain independent.

The kernel is pinned to the published `github.com/xibodev/compa v1.0.0`
release (commit `3fd39ad7f5cb3d63115535c4b433b714ab6a2812`). No separate
Compa application, web UI, module adapter or kernel process is required.

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

The default creates `workspace` and `host-state` beneath that parent. Existing
explicit `--workspace` launches still use `WORKSPACE\.midden-ui` when `--state`
is omitted; supply both paths to preserve a custom installation's bindings.
The `--core`, `--bundle` and `--listen` overrides remain available.

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

## Operator flow

1. Open Settings and choose an OpenAI-compatible or Anthropic-compatible
   provider. Leave Endpoint blank for the official service and enter its API
   key, or supply a compatible service/local model server URL and its key if
   required. A local endpoint that needs no key can leave credentials blank.
   **Find models** queries the selected account/endpoint without saving the
   supplied key or selecting a model. Exact manual identifiers remain supported.
   **Check model** makes one small model request for an inert tool call; it
   may incur provider usage, but reads no workspace data and executes no tool.
   A catalog response or text-only answer is not verified bundle execution.
   Save the selected model when ready. Nothing contacts a provider merely by
   opening the page. The saved manual model identifier is registered as a local
   selection, not as proof of upstream availability. The kernel uses Compa's
   provider-instance, catalog and exact-target resolver for every turn.
2. Create a conversation and describe an outcome in normal language.
3. Review write and command permission requests. Allow or deny one operation;
   denials are not editorial approval records in Midden core.
4. Inspect actual workspace files and sandboxed artifact previews, then request
   revisions in the same conversation.
5. Stop a turn when needed. Cancellation does not undo already completed file
   writes. Refresh/reopen the host to continue stored conversation history.
   New turns retain completed, failed, cancelled or interrupted outcomes beside
   their request; a host restart marks unfinished attempts interrupted rather
   than silently showing them as completed. Old histories without outcome
   records cannot retrospectively reconstruct earlier failures.

Unsent drafts are saved in this browser, scoped to the workspace and host-state
identity as well as the conversation. They survive page reloads and are cleared
only when the matching message is accepted. This is a local convenience cache,
not encrypted backup or cross-device synchronization; clear site data when
using a shared browser. Storage failures are reported rather than silently
claiming that a draft was saved. Model credentials never enter this draft cache.
Changing providers or endpoints does not silently reuse another service's key.

One UI process owns one workspace and isolated kernel state root. Model
credentials are written only to that kernel auth store and are not returned by
the settings API. Generic read tools cannot inspect host state. The loopback API
rejects foreign hosts/origins and requires a process CSRF token for mutations.
Kernel bookkeeping, memory and history stay in host state, not in the
authored-file workspace. The public prompt-contributor API supplies the actual
artifact tool binding; agents inspect workspace instructions through scoped
file tools. An explicit runtime tool policy is applied before registration;
the model sees only the scoped Midden and workspace tools.

## Upgrading from the Facet-backed host

Keep the same `--workspace` and `--state` values. The host binds `COMPA_HOME`
to the existing private `STATE\kernel` directory and never adopts a global
`~/.compa` configuration or key store. Existing API-key references in that
private store remain usable. UI conversations, durable outcomes and the stable
browser-draft identity remain unchanged.

Direct native Copilot/Codex sign-in is not part of this Compa-backed host.
Existing native-provider settings remain on disk and are shown as requiring
reconnection; they are not silently deleted or treated as an API key. Choose a
supported connection to continue. Those providers would require a separately
selected and verified extension integration, which is not bundled here.

Kernel conversation persistence now uses JSONL. Before starting its first turn,
the host converts legacy `STATE\kernel-history` JSON into `STATE\compa-history`,
including full tool-call messages, tool results and summaries. It keeps the old
directory unchanged as a backup, verifies source digests, and prevents repeat
imports from overwriting newer conversation data. Corrupt or conflicting
history is an explicit error rather than a silently empty conversation.
Do not run the old and new hosts concurrently against the same state directory.
Retain the original state backup when upgrading; continuing with the old binary
will not see turns recorded only in the new JSONL store.

Approved shell commands run as the operator's account. This is **not an OS
sandbox**. Do not approve a command you would not run yourself. The normal core
data tool blocks source maintenance and host-binding overrides.

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
Browser acceptance covers the operator interface separately.

`testsupport/provider.py` is a deliberately synthetic local protocol fixture for
full UI/kernel/tool integration. It is not a live model and must never be
presented as model-quality or real-account authentication evidence.

For a local native testing directory, run `python package.py --out ABSOLUTE_PATH`
from this module with Go on PATH. The directory must be new and outside the
checkout. The package contains the UI executable, independently built core and
unchanged canonical bundle; optional production renderers are not bundled.
