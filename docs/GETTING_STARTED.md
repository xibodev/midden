# Getting started

Choose one entrance. You do not need to install an AI CLI to use the app, or run
the app to use Midden in your existing AI CLI.

## In the Midden app

1. Follow the default [app installation](INSTALL.md#app-quick-install).
2. Keep the app terminal running while you use the local browser workspace.
3. In **Sessions**, filter recorded work or find text, then pick a session to
   see its facts, usage and brief. **Open evidence** pins its records.
4. In **Evidence**, page or search the pinned view, select records and
   **Collect into workspace**. The new collection appears in **Sources**, where
   you can check its integrity, read, search, export or merge it.
5. To work with the assistant, open **Models**. Try free models, find a local
   model server, connect a provider with an API key or address, or connect an
   extension service. Set a default model and use **Test tool calling**.
6. In **Sources**, use a collection's outcome starter (Investigation, Article,
   Presentation or Long-form), or describe the work in **Assistant**. Review
   each permission card and inspect the resulting files in **Files**.

For example:

> Investigate this session and show me one idea worth explaining, together with
> the source limitations. Do not start a draft yet.

Then, once you have chosen the direction:

> Develop that idea as a short tutorial. Keep the editable source, and tell me
> which claims still need checking.

Sessions, Evidence, Sources and Files work without a model and ask for no
approval. Sending needs a working default model; a default does not guarantee
authentication, entitlement or useful output. The assistant needs tool calls:
if the selected model cannot make them, the turn stops with a message saying so.

The assistant is Midden, powered by Compa; the footer names the embedded Compa
version. Its memory is off; each conversation keeps its own history.
**Use in chat**, **Ask for a revision** and the outcome starters add context to
your next message; **Preview what's sent** shows it before you send.

Use **Stop** to cancel an active turn; inspect any files already written.
Ctrl+C in the terminal stops the host. Later, run `midden-ui` again. Use
`--no-open` if you prefer to open the printed local address manually.

## In an existing AI CLI

Install [CLI mode](INSTALL.md#install-into-your-ai-cli) into an existing project.
You need Python 3.9+ and an independently installed/authenticated host. `copilot`
is the default installation layout; `claude` and `agents` are alternatives.

Start a fresh host conversation in the project and use the same kind of natural
request. The host follows the bundle and does the investigation, writing,
rendering and revision. Midden does not configure its account, provider, MCP
servers or permissions.

Point the host at existing work when continuing a draft. Notes, source
collections and authored files are not conditional on an editorial project
table. A presentation should include an actual rendered artifact, not only a
description in chat.

Pandoc and browser inspection tools are optional prerequisites for relevant
outcomes, not requirements for reading sessions. See the
[bundle tool guide](../bundles/midden-shared/tools.md).

## Read session material without a model

The deterministic core is also useful by itself:

```text
midden ls --days 7 --json
midden read --tool copilot --session SESSION_ID --json
midden search "design decision" --view VIEW_ID --json
midden collect --view VIEW_ID --record RECORD_ID --out sources --json
```

Copy the exact source tool, session, view and record IDs from the preceding
results. Read scope, clipping and inventory warnings. Do not turn an invalid
selector into an all-history scan. Each `--json` result follows the
[JSON output contract](CORE.md#json-output).

See [Core](CORE.md), [Configuration](CONFIGURATION.md),
[Operations](OPERATIONS.md) and [Troubleshooting](TROUBLESHOOTING.md).
