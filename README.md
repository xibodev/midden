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

It starts on **Sessions**; its views follow the work:

- **Sessions** lists recorded Copilot CLI, Claude Code and OpenCode work with
  filters, text find and a detail panel of facts, usage and a brief.
- **Evidence** opens from a session as a pinned view: page and search records,
  show their context, then **Collect into workspace** or **Use in chat**.
- **Sources** checks, reads, searches, exports and merges portable collections,
  and starts an investigation, article, presentation or long-form piece.
- **Files** previews and downloads workspace files and checks a quote against a
  source record.
- **Assistant** holds conversations with Midden, powered by Compa: tool activity,
  permission cards and the context you hand over.

Sessions, Evidence, Sources and Files call the deterministic core directly. They
need no model and ask for no approval; their writes create new files or folders
inside the workspace.

The assistant needs a model. In **Models**, choose **Try free models** (public
services, no key; your prompts go to the service that answers),
**Find local model servers**, a provider from Compa's list with an API key or
address, or an extension service. Set a default model or a route of fallbacks,
then use **Test tool calling**: a model that cannot call tools stops the turn
with a message saying so. Keys and secrets are write-only.

See [Configuration](docs/CONFIGURATION.md#app-model-connections).

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
| `midden-ui` app | Sessions, evidence, sources and files through the core without a model; an assistant powered by the embedded Compa kernel, with model connections and permission cards for writes and commands. Its dependencies stay outside the core module. |

Use `-Mode core` / `--mode core` to install only the deterministic core, or use
it directly when you need data rather than AI-assisted production:

```text
midden ls --days 7 --json
midden read --tool copilot --session SESSION_ID --json
midden search "design decision" --view VIEW_ID --json
midden collect --view VIEW_ID --record RECORD_ID --out sources --json
midden collection verify sources --json
```

Copy the exact IDs returned by the preceding command. Each `--json` result
follows the core's [JSON output contract](docs/CORE.md#json-output). Read
[Core commands](docs/CORE.md) for bounds, portable collections and source safety.

## Your files, your decisions

With no path overrides, the app's workspace and host state live in per-user data
directories, separate from the installation. An explicit `--workspace` requires
`--state` as well; `--state` alone keeps the default workspace.
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

## Updating and trust

Stop a running app before upgrading. Use the installer's **Upgrade**, **Verify**
and **Uninstall** operations for the same installation. Removal is limited to
owned files; workspace, state and unrelated work are preserved. Changed owned
files need attention rather than being silently overwritten.

Use matching artifacts and [SHA256SUMS](https://github.com/xibodev/midden/releases/download/v0.3.1/SHA256SUMS).
Checksums establish integrity against that manifest, **not publisher
authenticity**. The v0.3.1 release binaries are unsigned.

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
