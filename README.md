# Midden

Midden reads the AI sessions already recorded on your computer by Claude
Code, GitHub Copilot CLI and OpenCode, and helps you turn that work into
findings, articles, tutorials, presentations and longer pieces that point
back to the records they came from.

It has three parts, installed together or separately by one installer:

| Part | What it is |
|---|---|
| **Core** | The `midden` command. It lists, reads, searches and collects records from your sessions. It opens them read-only, never calls a model and makes no network connections. |
| **The skills** | Four skills for the AI agent you already use (Claude Code, GitHub Copilot CLI, or an agent that reads `~/.agents/skills`): investigation, article, presentation and long-form. They run `midden` and write the result as files. |
| **The App** | A web page on your computer with its own assistant, if you don't work in a terminal agent. It shows your sessions, collections and files, and its assistant uses the same skills with a model you choose. |

## Install

Windows, in PowerShell:

```powershell
irm https://xibodev.github.io/midden/install.ps1 | iex
```

macOS and Linux:

```sh
curl -fsSL https://xibodev.github.io/midden/install.sh | sh
```

This installs the App, with Core and the skills, for your user account only,
then starts it and opens it in your browser. Choose **Quit Midden** in the
page to stop it, and start it again from the Start menu, your Applications
folder or your application menu.

To install the skills for your agent, with Core:

```powershell
& ([scriptblock]::Create((irm https://xibodev.github.io/midden/install.ps1))) -Mode bundle -Harness claude
```

```sh
curl -fsSL https://xibodev.github.io/midden/install.sh | sh -s -- --mode bundle --harness claude
```

Use `claude`, `copilot` or `agents` for the harness, or `core` as the mode
for Core alone. On macOS and Linux the commands are linked into
`~/.local/bin`, which must be on your PATH. [Installing](docs/install.md)
covers every option, updates and removal.

## Use it

**The App** opens on your sessions. Open one to read it, pin its evidence
and collect records into your files. For the assistant, connect a model in
Models: free models, a local server such as Ollama, or a provider with an
API key. Then ask, for example, "What did I work on in this repository last
week?" What it writes appears in Files.

**In your agent**, ask for the outcome you want:

- "Investigate my sessions on this repository from the last two weeks and
  suggest what is worth writing up."
- "Write a tutorial from the sessions where I set up the release pipeline."
- "Make a 10-minute presentation about the release as HTML slides."
- "Start a handbook about our release process; write the first chapter."

**In a terminal**, Core works on its own:

```sh
midden ls --days 7
midden find "release pipeline"
midden brief SESSION_ID
midden read --tool claude --session SESSION_ID
```

## Documentation

- [Installing, updating and removing](docs/install.md)
- [The App](docs/app.md)
- [The skills](docs/skills.md)
- [The `midden` command](docs/core.md)
- [Development and releases](docs/development.md)

## Privacy

- Core opens session stores read-only. Only `prune --apply --replace` and
  `archive --apply`, which you have to run yourself, change them.
- Core never calls a model and has no network code.
- The App listens only on 127.0.0.1 and answers only pages opened with the
  key it prints at each start. What you send to its assistant goes to the
  model provider you connect, and the assistant's web tools can reach the
  internet.
- Midden sends no telemetry.
- Core hides recognisable credentials in what it shows. That is not a
  privacy review: check what you share.

## License

Midden is under the [MIT License](LICENSE). The App includes Compa's
`compa-kernel`, unchanged, under Compa's MIT License, and its installer
downloads Pandoc (GPL-2.0-or-later) from Pandoc's own release. See
[NOTICE](NOTICE).
