# Midden

**Turn recorded work into something you can carry forward.**

Investigate your AI sessions, develop an article or tutorial, make a presentation,
or continue a longer draft. Midden keeps selected source material and authored
outputs in ordinary, inspectable files.

**v0.3.1** offers two ways in: the **Midden app** for a local browser workspace,
or **Midden in your existing AI CLI**. Both use the same deterministic core and
canonical outcome bundle.

[Website](https://xibodev.github.io/midden/) |
[Downloads](https://github.com/xibodev/midden/releases/tag/v0.3.1) |
[Installation](docs/INSTALL.md) |
[Migration](docs/MIGRATION.md) |
[Release notes](docs/RELEASE_NOTES.md)

## Use the Midden app

The default installation includes `midden-ui`, the `midden` core executable and
the matching bundle. **No Go, Node.js or Python is needed to run the app.**
It installs for your user account; no administrator access is required.

Review the [Windows script](https://xibodev.github.io/midden/install.ps1) or
[Unix script](https://xibodev.github.io/midden/install.sh) before running remote
code.

**Windows, PowerShell**

```powershell
irm https://xibodev.github.io/midden/install.ps1 | iex
```

**macOS or Linux**

```sh
curl -fsSL https://xibodev.github.io/midden/install.sh | sh
```

The app opens in your browser. Keep its terminal running; **Ctrl+C stops the
host**. Start it again with `midden-ui`. Use `midden-ui --no-open` when you want
to open the printed local address yourself.

In **Settings**, choose an **OpenAI-compatible** connection, including a local
server, or an **Anthropic-compatible** connection. A blank endpoint uses the
official provider; a compatible local server may not need an API key.

- **Find models** lists a catalog. It does not verify inference.
- **Check model** makes a small tool-capability inference probe. It uses provider
  usage but reads no workspace files.
- You can enter an exact model ID manually, then **Save settings**.

Direct native GitHub Copilot/Codex sign-in is not bundled in this Compa-backed
app. An old native connection requires explicit reconnection; its credentials
are not silently converted. See [Configuration](docs/CONFIGURATION.md).

## Use Midden in your AI CLI

Choose this route if you already work in an AI CLI. It requires **Python 3.9+**
and a compatible AI host installed and authenticated separately. Midden does
not configure that host's model, MCP servers or permissions.

Download and review the bootstrap, then install into an existing project:

```powershell
Invoke-WebRequest -Uri https://xibodev.github.io/midden/install.ps1 -OutFile .\midden-install.ps1
.\midden-install.ps1 -Mode cli -ProjectDir . -Host copilot
```

```sh
curl -fsSL https://xibodev.github.io/midden/install.sh -o midden-install.sh
sh midden-install.sh --mode cli --project . --host copilot
```

Project scope and `copilot` are the defaults. `claude` and `agents` are also
supported installation layouts. The Python installer verifies the matching core
and bundle and records ownership for later verification, upgrade and removal.

Start a fresh host conversation in that project and describe the work normally:

> Find an idea in this session that is worth explaining, and show me the limits
> of the source material before drafting.

> Turn these notes into a short tutorial, keeping the editable source.

> Make a presentation about how this feature evolved, then inspect the rendered
> slides.

The AI host follows the bundle, uses tools, writes files and revises with you.
It uses **its own authentication**, independently of the app's provider support.
Optional renderers such as Pandoc and browser inspection tools are needed only
for outcomes that call for them; installation does not download those tools.

## The pieces stay independent

| Piece | Responsibility |
|---|---|
| `midden` core | Discover, read, search, measure, collect and export recorded material from Copilot CLI, Claude Code and OpenCode. It never calls a model. |
| [Outcome bundle](bundles/README.md) | Investigation, article/tutorial, presentation and long-form guidance, templates and helpers. It is not a runtime. |
| `midden-ui` app | Conversation, model connection, explicit tool permissions and file previews, using the embedded Compa v1.0.0 kernel. Its dependencies stay outside the core module. |

Use `-Mode core` / `--mode core` to install only the deterministic core, or use
it directly when you need data rather than AI-assisted production:

```text
midden ls --days 7 --json
midden read --tool copilot --session SESSION_ID --json
midden search "design decision" --view VIEW_ID --json
midden collect --view VIEW_ID --record RECORD_ID --out sources --json
midden collection verify sources --json
```

Copy the exact IDs returned by the preceding command. Read
[Core commands](docs/CORE.md) for bounds, portable collections and source safety.

## Your files, your decisions

With no path overrides, the app's workspace and host state live in per-user data
directories, separate from the installation. An explicit `--workspace` without
`--state` preserves the legacy `.midden-ui` state directory inside that workspace;
specify both flags to choose a separate state location.
[Installation](docs/INSTALL.md#locations-and-launching) lists the platform
defaults and lifecycle commands. `midden-ui --version` reports the version
without reading or writing application state.

Source investigation is read-only. Drafts, notes and delivered artifacts are
ordinary files. Tool approval authorizes an operation, not publication.
**Approved shell commands run with your account; the app is not an OS sandbox.**
Material sent to a remote model is governed by that provider and your chosen
connection, not kept on-device merely because the UI runs locally.

References establish origin, not factual truth. Credential filtering is not
privacy clearance. Review prose, code, images and intended disclosures before
sharing them.

## Updating, trust and migration

Stop a running app before upgrading. Use the installer's **Upgrade**, **Verify**
and **Uninstall** operations for the same installation. Removal is limited to
owned files; workspace, state and unrelated work are preserved. Changed owned
files need attention rather than being silently overwritten.

Use matching artifacts and [SHA256SUMS](https://github.com/xibodev/midden/releases/download/v0.3.1/SHA256SUMS).
Checksums establish integrity against that manifest, **not publisher
authenticity**. The v0.3.1 release binaries are unsigned.

Coming from v0.2 or an earlier development build? Read
[Migration](docs/MIGRATION.md) before reusing any state directory. Retired
module/Studio/editorial interfaces are not restored by the new app.

## Documentation and development

[Getting started](docs/GETTING_STARTED.md) |
[Configuration](docs/CONFIGURATION.md) |
[Operations](docs/OPERATIONS.md) |
[Troubleshooting](docs/TROUBLESHOOTING.md) |
[Development](docs/DEVELOPMENT.md) |
[Acceptance boundaries](docs/ACCEPTANCE.md)

Building all components requires Go 1.26.6; this is not an app installation
prerequisite. Core correctness, browser/runtime behavior and live bundle
effectiveness have separate acceptance criteria. Local protocol fixtures and
browser checks do not certify live AI content quality or every provider.

Released under the [MIT license](LICENSE). Release archives include generated
`THIRD_PARTY_NOTICES.txt` for compiled dependencies, retaining Compa attribution.
Preserve the license, copyright and accompanying notices when redistributing
release files.
