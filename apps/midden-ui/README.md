# Midden UI host

This is a new host for the canonical Midden bundle, not a restored version of
the old Studio UI. It embeds Facet Studio's public kernel in its own Go module.
The deterministic core and canonical bundle remain independent.

The kernel is pinned to the explicitly unreleased candidate
`v1.0.1-0.20260922143928-0b024a53c4f6`.

## Run

Requires Go 1.26.5 to build. Build the normal core from the repository root, then
build this module separately:

```powershell
go build -o midden.exe .\cmd\midden
Set-Location apps\midden-ui
go build -o midden-ui.exe .
.\midden-ui.exe --workspace WORK_DIRECTORY --state HOST_STATE_DIRECTORY --core CORE_EXECUTABLE --bundle BUNDLE_DIRECTORY
```

Paths are explicit. The workspace must exist and must not be a source store.
The bundle path accepts the canonical `bundles` directory or its extracted
distribution root. The default address is `http://127.0.0.1:18890`.

For an isolated rehearsal, set `MIDDEN_CLAUDE_ROOT`, `MIDDEN_COPILOT_ROOT`, or
`MIDDEN_OPENCODE_DB` to synthetic source stores before launch. With none set, the
core's normal source discovery applies. Investigation does not mutate sources.

## Operator flow

1. Configure an exact model in the UI. Use an OpenAI-compatible endpoint/API
   credential, Anthropic credential, or the kernel's native GitHub Copilot
   connection. Choosing native Copilot explicitly permits its existing sign-in
   discovery. No model is selected or contacted merely by opening the page.
   If existing sign-in cannot exchange a session token, use **Sign in with
   GitHub** in Settings. The host uses the existing provider-auth library's device
   flow; only a user code/link reach the browser. Account entitlement and
   organization policies are still enforced by the provider.
2. Create a conversation and describe an outcome in normal language.
3. Review write and command permission requests. Allow or deny one operation;
   denials are not editorial approval records in Midden core.
4. Inspect actual workspace files and sandboxed artifact previews, then request
   revisions in the same conversation.
5. Stop a turn when needed. Cancellation does not undo already completed file
   writes. Refresh/reopen the host to continue stored conversation history.

One UI process owns one workspace and isolated kernel state root. Model
credentials are written only to that kernel auth store and are not returned by
the settings API. Generic read tools cannot inspect host state. The loopback API
rejects foreign hosts/origins and requires a process CSRF token for mutations.
Kernel bookkeeping and history stay in host state, not in the authored-file
workspace. A superseded pending sign-in cannot replace a newer model choice.

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
