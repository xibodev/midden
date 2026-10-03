# Midden v0.3.1

**Two entrances: use the Midden app, or use Midden in your existing AI CLI.**
Both use the deterministic core and the same canonical outcome bundle.

[Installation](INSTALL.md) |
[Release assets](https://github.com/xibodev/midden/releases/tag/v0.3.1)

## App-first installation

- Per-user bootstraps default to the app: `midden-ui`, the matching `midden`
  executable and bundle.
- The packaged app needs no Go, Node.js or Python runtime installation.
- A no-argument launch resolves its sibling components, uses workspace/host state
  outside the install root and opens the browser. Keep the terminal running;
  Ctrl+C stops the host.
- `--no-open` suppresses browser opening while the host remains in the foreground;
  `--version` reports the app version without reading or writing application state.
- An explicit `--workspace` requires `--state`; `--state` alone selects a
  separate host state with the default workspace.
- Core-only and AI CLI installation remain explicit modes. CLI mode retains the
  existing Python 3.9+ project-scoped, receipt-based installer.

## Windows PATH preservation

The Windows bootstrap preserves the exact raw user PATH: whether it existed,
its registry value type, text and expandable environment references. Committed
updates and successful restores notify Windows; notification failures are
reported without undoing committed files or overwriting concurrent edits.

## Midden app, released Compa kernel

The Midden app embeds **Compa v1.0.0**, without transplanting Compa's web UI.
Provider connections, catalogs and resolution use that runtime; the core's
module stays independent of it.

The UI retains conversations, reload-safe workspace-scoped drafts, explicit
per-operation permissions, Stop, durable host turn outcomes, and inspectable
files/downloads. Pending decisions appear ahead of collapsed activity history.
HTML artifacts run inline scripts only inside an isolated preview; they are not
injected into the app page.

Supported connections are OpenAI-compatible, including local servers, and
Anthropic-compatible. **Find models** does not verify inference. **Check model**
is a small, explicit tool-capability inference call with provider usage and no
workspace reads. Exact manual model IDs remain supported. A model selection is
not proof of valid authentication.

## Core and outcome bundles

Core provides ordinary discovery, read, search, measurement, collection, asset
and export commands. Stable views tolerate appended records while detecting
earlier source changes. Portable collections remain separate from interpretation
and authored work.

The bundle provides investigation, article/tutorial, presentation and incremental
long-form methods. The app or AI CLI executes the work with the operator.
Optional Pandoc/browser rendering tools belong to particular outcomes, not to
basic installation or session reading.

Public Go module paths use `github.com/xibodev/midden`; the UI remains a separate
module beneath that namespace. Release archives include generated
`THIRD_PARTY_NOTICES.txt` for compiled dependencies, retaining Compa attribution.

## Preservation

Upgrade/uninstall checks apply to owned installation files. They do not grant
permission to discard modified files, source stores, workspaces or host state.

## Version and validation boundary

**v0.3.1-rc.1** is the prerelease staging tag before **v0.3.1**. Select an RC
explicitly rather than relying on `latest`. Release builds inject the tag's
version into the app and core from the same source commit; do not mix artifacts
from different builds.

Compiled/runtime checks, synthetic local protocol fixtures and browser tests
exercise mechanics. They do **not** certify live AI content quality, every
provider, or an account's authorization. Live acceptance needs legitimate
provider credentials, ordinary goals and inspection of actual artifacts.

Checksums establish integrity against the release manifest, not publisher
authenticity. The v0.3.1 release binaries are unsigned; checksum agreement is not
a publisher signature. Preserve `LICENSE`, copyright and
`THIRD_PARTY_NOTICES.txt` when redistributing files.
