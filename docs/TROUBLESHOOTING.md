# Troubleshooting

## Install, launch and update

| Symptom | Check |
|---|---|
| Script, application or version probe blocked by policy | Stop and seek administrator or publisher review of the release version, checksum and reviewed diagnostics. A matching checksum does not authorize execution. |
| No native artifact matches | Check the release's actual platform/architecture list. Do not substitute a foreign executable. |
| Checksum or manifest mismatch | Stop. Obtain a coherent set from one trusted release; do not edit checksums to force installation. |
| `midden-ui` is not found | Open a new terminal after command-path setup, or use the installed executable path if you selected `-NoPath` / `--no-path`. |
| Core or bundle cannot be found | Keep the matching release layout together. A lone UI executable is not the full app installation. |
| Browser did not open | Keep the host terminal running and open its printed local address. `--no-open` intentionally suppresses opening. |
| App disappears after closing the terminal | The terminal owns the host process. Start `midden-ui` again; Ctrl+C is the normal stop action. |
| Upgrade reports a file in use | Stop the running app before retrying. Do not force-replace its files. |
| Modified owned file or unowned collision | Inspect the reported path and preserve your edits. Do not discard receipts or unrelated files to bypass ownership checks. |
| CLI installation needs Python | CLI mode requires an existing Python 3.9+ interpreter and filesystem hard-link support. App/core modes do not need Python. |
| The AI CLI does not discover the bundle | Check the selected project and `copilot`/`claude`/`agents` layout, then start a fresh host conversation. Its account/model setup is separate. |

Release binaries are unsigned. Application control may block even an intact
binary's version probe; that is a blocked validation step, not a passing check
or necessarily a corrupt artifact. Do not disable Defender/ASR or other security
controls, bypass execution policy, or use another shell/launcher to evade the
block. Continue only through an approved deployment path after review.

## App connection and conversation

| Symptom | Check |
|---|---|
| Send is disabled / Set up model | Configure a supported connection and exact model ID in Settings, then save. Drafting and file access remain available. |
| Authentication or authorization error | Read the provider's actual error. Check endpoint, credentials, model availability and account access; a selected model is not verified authentication. Do not infer a single cause from an HTTP status alone. |
| Find models fails or omits an ID | Catalog discovery is not inference verification. Check service support and enter the exact ID manually if appropriate. |
| Check model fails | The explicit tool-capability probe could not complete. Inspect its error and the connection; do not treat a catalog listing as a substitute. |
| Key/reference fields became blank | Provider/endpoint changes clear inherited credentials. Successful requests and closing the dialog clear key input. Explicitly supply credentials for the intended service when needed. |
| Live updates disconnected | The UI reports reconnect status. Use Refresh host if the stream closes; Stop remains available for an active turn. |
| A permission is waiting | Inspect the pending card's arguments, then Allow or Deny that operation. Prior decisions are in Previous activity. |
| Host status says Interrupted | The host stopped before the turn reached a durable finish. Inspect existing files and decide whether to resume; the status is not assistant-authored output. |
| Draft storage unavailable | Keep the tab open, copy important text and use Retry draft save. Private browsing, disabled storage or quota can prevent persistence. |
| Preview differs from opening a file directly | HTML previews allow inline runtime scripts but block network, parent-page access and privileged navigation. Inspect the artifact's offline dependencies. |

## Sources, state and artifacts

| Symptom | Check |
|---|---|
| No session matched | Confirm the exact source tool/ID, source roots and inventory warnings. Do not widen silently. |
| Some sources skipped | Read the reason. Omitted sessions are not automatically related to your target. |
| Source view changed | Appends are tolerated; earlier edits, truncation or record reordering require an explicit new view. |
| Missing view | Check the core's `MIDDEN_HOME`/`--state`. Portable collections do not depend on that cache. |
| JSON result too large | Narrow `--limit`/`--chars`, page records, or save complete output with `--out` where supported. |
| Asset unavailable | Distinguish a reference, embedded data, missing file, remote URL and unsafe location. Missing assets are not described as copied. |
| Existing output path | Choose a new destination. Core collection/export does not silently overwrite work. |
| Draft not found | Check the intended workspace and its files. An empty index or different app state is not proof that no draft exists. |
| Rendering unavailable | Provision only the selected bundle's required renderer/inspection tools. Installation does not download them for you. |

See [Installation](INSTALL.md), [Configuration](CONFIGURATION.md) and
[Core commands](CORE.md).
