# Operations and ownership

The app and your existing AI CLI are alternative hosts. Both use the same
deterministic core and outcome bundle; neither turns a source reference into
proof that an authored claim is true.

## In the app

| Action | Effect and boundary |
|---|---|
| Sessions, Evidence, Sources, Files | Run the deterministic core through the app's own routes; no model and no approval card. Collect, export and merge create new paths inside the workspace |
| Try free models | Checks public services that need no key and connects those that answer; prompts then go to the service that answers |
| Find local model servers | Probes the usual local ports; nothing is connected until you choose **Connect** |
| Connect a provider or extension service | Reads its model list with the supplied credential, then stores the credential write-only in host state |
| Test tool calling | One inert tool-call request to the chosen model; provider usage, no workspace reads, no tool run |
| Use as default | Selects the model or route for future turns; it is not an authentication or content-quality certificate |
| Send | Starts a turn using the default model and ordinary request |
| Allow / Deny | Decides one requested workspace write, file write or shell command; it is not a blanket grant or publication approval |
| Stop | Requests cancellation; inspect files already written rather than assuming rollback |
| File preview / download | Inspects or copies an existing workspace artifact; the preview does not edit the original |
| Use in chat / Ask for a revision | Adds context to the next message; **Preview what's sent** shows it before sending |
| Ctrl+C in the terminal | Stops the host; closing the browser alone does not stop it |

Pending decisions appear ahead of collapsible activity history. Arguments remain
inspectable. Failed, cancelled or interrupted turns have host-status evidence
next to the originating request, separate from assistant-authored text.

HTML artifacts use an isolated preview with inline scripts permitted and network
access blocked. They are not injected into the app page. This preview boundary
does not sandbox shell tools: **approved shell commands run with your account**.

## Deterministic core

| Operation | Effect |
|---|---|
| Inventory, show, brief, assay, recorded usage | Read sources and report data/limits |
| Scan | Update the separate derived core cache |
| Read/search | Read a pinned source prefix and retain bounded source windows |
| Collect/select/merge/export | Create portable material at an explicit destination |
| Asset extraction | Copy available selected assets under bounded confinement |
| Collection verification | Check structural/reference/hash correspondence |
| Explicit quote check | Check supplied words against a source window, not narrative truth |
| Prune/archive | Separate explicit source-maintenance actions, not content-production steps |

Core does not perform AI interpretation or require an editorial lifecycle.
See [Core commands](CORE.md) for exact bounds and destination rules.

## Bundle and operator

The bundle guides investigation, audience, interpretation, writing, rendering,
inspection and revision. The selected host executes the tools; the operator
chooses direction and evaluates the results. An external AI CLI keeps its own
provider, account and permission model.

Keep selected sources distinguishable from notes and authored outputs.
Source stores are read-only during investigation. References establish origin,
not truth, and credential filtering is not privacy clearance. Review intended
disclosures before publication.

## Installation lifecycle

Use **Verify**, **Upgrade** and **Uninstall** with the same installation mode and
destination. App/core Verify and Uninstall use the installed receipt without
source or network access. CLI Verify/Uninstall instead require a reviewed
matching `DistributionDir`, or an already extracted matching Python backend,
with the same project and host selection. The bootstrap never fetches a different
CLI backend implicitly. Stop an app before replacing its running files.

Owned-file checks are not permission to remove modified or unrelated files.
Uninstallation preserves workspace, host state and source data. Do not delete
receipts or data to bypass a reported conflict.

See [Installation](INSTALL.md#verify-update-and-remove) and
[Troubleshooting](TROUBLESHOOTING.md).
