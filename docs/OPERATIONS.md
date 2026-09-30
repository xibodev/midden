# Operations and ownership

The app and your existing AI CLI are alternative hosts. Both use the same
deterministic core and outcome bundle; neither turns a source reference into
proof that an authored claim is true.

## In the app

| Action | Effect and boundary |
|---|---|
| Find models | Lists a provider catalog; no inference verification or saved-configuration change |
| Check model | Explicit small tool-capability inference probe; provider usage, no workspace reads |
| Save settings | Saves the chosen connection/model; it is not an authentication or content-quality certificate |
| Send | Starts a turn using the configured model and ordinary request |
| Allow / Deny | Decides one requested operation; it is not a blanket grant or publication approval |
| Stop | Requests cancellation; inspect files already written rather than assuming rollback |
| File preview / download | Inspects or copies an existing workspace artifact; the preview does not edit the original |
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
| Legacy export | Read old working state and copy recoverable data/files without rewriting it |
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

See [Installation](INSTALL.md#verify-update-and-remove),
[Migration](MIGRATION.md) and [Troubleshooting](TROUBLESHOOTING.md).
