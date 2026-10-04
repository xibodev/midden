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

## App models and conversation

| Symptom | Check |
|---|---|
| No model connected / Send is disabled | Open **Models** and connect one: free models, a local model server, a provider with an API key or address, or an extension service. Sessions, Evidence, Sources and Files work without a model. |
| Model setup needed | A default model is set but unavailable; the notice gives the reason, such as a stored key that cannot be read. Choose another default, or connect again, in **Models**. |
| "The selected model doesn't support tool calls" | The assistant needs tool calls, so the turn stopped instead of answering without tools. Choose a model that passes **Test tool calling**, or a route of such models. |
| Test tool calling fails | "Did not return the required tool call" means the model is not verified for tool use. Other errors concern the address, key, account or service. |
| Try free models connects nothing or shows Busy | Public services can be rate limited or unavailable. Try later, or connect a local model server or a provider. |
| No local model server answered | Start the server and load a model. Detection checks only the usual ports on this computer; connect a server elsewhere with its address under **With an API key or address**. |
| Could not reach the extension service | Start the service, check its address and secret, then connect again. Connecting lists its providers. |
| An extension provider shows Sign-in needed | Save its token, or sign in by device code or pasted code. An unfinished sign-in expires within ten minutes; start it again. |
| Authentication or authorization error | Read the provider's actual error. Check address, credentials, model availability and account access; a default model is not verified authentication. Do not infer a single cause from an HTTP status alone. |
| Model settings refuse to change | Model changes and tool-calling tests are refused while a turn runs. Stop the turn, then retry. |
| Live updates disconnected | The UI reports reconnect status. Use Refresh host if the stream closes; Stop remains available for an active turn. |
| A permission is waiting | Inspect the pending card's arguments, then Allow or Deny that operation. An unanswered card is denied after four minutes. Prior decisions are in Previous activity. |
| Host status says Interrupted | The host stopped before the turn reached a durable finish. Inspect existing files and decide whether to resume; the status is not assistant-authored output. |
| Draft storage unavailable | Keep the tab open, copy important text and use Retry draft save. Private browsing, disabled storage or quota can prevent persistence. |
| Preview differs from opening a file directly | HTML previews allow inline runtime scripts but block network, parent-page access and privileged navigation. Inspect the artifact's offline dependencies. |

## Sources, state and artifacts

| Symptom | Check |
|---|---|
| No session matched | Confirm the exact source tool/ID, source roots and inventory warnings. Do not widen silently. |
| Some sources skipped | Read the reason. Omitted sessions are not automatically related to your target. |
| Find text misses OpenCode sessions | Text find covers Copilot CLI and Claude Code transcripts. Open the OpenCode session's evidence and search its pinned view. |
| Source view changed | Appends are tolerated; earlier edits, truncation or record reordering require an explicit new view. |
| Missing view | Check the core's `MIDDEN_HOME`/`--state`. The app keeps its views in host state (`core`); open evidence again from Sessions. Portable collections do not depend on that cache. |
| JSON result too large | Narrow `--limit`/`--chars`, page records, or save complete output with `--out` where supported. |
| Asset unavailable | Distinguish a reference, embedded data, missing file, remote URL and unsafe location. Missing assets are not described as copied. |
| Existing output path | Choose a new destination. Core collection/export and the app's Collect, Export and Merge do not overwrite work. |
| A collection is missing from Sources | Sources lists collection folders up to five levels deep in the workspace, skipping hidden folders, `sessions` and `node_modules`. |
| A collection shows Not checked | Integrity is checked automatically for collections of up to 500 records. Use **Verify** for larger ones. |
| Draft not found | Check the intended workspace and its files. An empty index or different app state is not proof that no draft exists. |
| Rendering unavailable | Provision only the selected bundle's required renderer/inspection tools. Installation does not download them for you. |

See [Installation](INSTALL.md), [Configuration](CONFIGURATION.md) and
[Core commands](CORE.md).
