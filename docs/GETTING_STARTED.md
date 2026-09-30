# Getting started

Choose one entrance. You do not need to install an AI CLI to use the app, or run
the app to use Midden in your existing AI CLI.

## In the Midden app

1. Follow the default [app installation](INSTALL.md#app-quick-install).
2. Keep the app terminal running while you use the local browser workspace.
3. Open **Settings**. Choose an OpenAI-compatible or Anthropic-compatible
   connection, with an official API key or a compatible local endpoint.
4. Use **Find models**, or enter an exact model ID. A catalog entry is not
   verified inference. **Check model** makes an explicit small inference probe
   with provider usage and no workspace reads.
5. **Save settings**, then give a normal request. Review each requested tool
   permission and inspect the resulting files.

For example:

> Investigate this session and show me one idea worth explaining, together with
> the source limitations. Do not start a draft yet.

Then, once you have chosen the direction:

> Develop that idea as a short tutorial. Keep the editable source, and tell me
> which claims still need checking.

The conversation list, drafts and file previews remain usable before model
setup. Sending is gated until a model is selected. Selection does not guarantee
authentication, entitlement or useful output.

The app embeds Compa v1.0.0. Native Copilot/Codex sign-in is not bundled.
For **Reconnect required**, follow [Migration](MIGRATION.md#reconnect-unsupported-model-settings).

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
selector into an all-history scan.

See [Core](CORE.md), [Configuration](CONFIGURATION.md),
[Operations](OPERATIONS.md) and [Troubleshooting](TROUBLESHOOTING.md).
