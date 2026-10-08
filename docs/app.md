# The Midden App

The App is a web page that runs on your computer. You use it to browse the
AI sessions recorded on your computer, collect evidence from them, and work
with an assistant that writes findings, articles, presentations and
long-form pieces into your files.

`midden-ui` serves the page on 127.0.0.1. It uses Core (`midden`) to read
your sessions, and runs `compa-kernel` 3.0.0, the agent runtime of
[Compa](https://github.com/xibodev/compa), as the assistant. Both come with
the App; see [Installing](install.md).

## Start and stop

- **Windows:** choose Midden in the Start menu. A console window opens and
  your browser opens Midden.
- **macOS:** open Midden in `~/Applications`. **Linux:** choose Midden in
  your application menu. Midden runs without a window.
- **Anywhere:** run `midden-ui` in a terminal.

To stop Midden, choose **Quit Midden** at the top of the page. A running turn
is stopped first. On Windows you can also close the console window, and in a
terminal press Ctrl+C.

At start the App prints its address and opens it in your browser:

```
Midden App 0.4.1: http://127.0.0.1:18890/?key=...
Your files: ...
Powered by Compa 3.0.0
```

The key changes at every start, so an old bookmark shows "Open Midden from
its Start entry"; start Midden again instead. Starting Midden while it runs
opens the running App. Closing the browser doesn't stop it.

### Options

```
Usage of midden-ui:
  -core string
        Midden core executable (defaults to the sibling binary)
  -data string
        the App's data folder (defaults to the per-user Midden folder)
  -kernel string
        compa-kernel executable (defaults to app/compa-kernel beside midden-ui)
  -listen string
        loopback listen address (default "127.0.0.1:18890")
  -no-open
        do not open the browser automatically
  -skills string
        skills folder (defaults to the sibling skills folder)
  -version
        print the release version without opening data
```

`--listen` accepts only a loopback IP address. If port 18890 is taken, the
App stops with an error: use another port, such as
`--listen 127.0.0.1:18891`, or `127.0.0.1:0` for any free port.

## Screens

Sessions, Sources and Files work without a model. The assistant needs one;
see [Models](#models).

### Sessions

Your sessions from Copilot CLI, Claude Code and OpenCode, newest first. Filter
by tool, age and workspace, and choose whether to include automated
sessions. **Find text across sessions** searches Copilot CLI and Claude Code
sessions; OpenCode sessions aren't text-searched.

A session shows its workspace, times, size, recorded model usage and a brief:
its goal, its recent turns and its last answer. From there you can open its
evidence, copy the command that resumes it in its own agent, or ask the
assistant about it.

### Evidence

A pinned view of one session, 24 records per page. Search within it, show
the records around one, and refresh it when the session has newer activity.
Tick records to build a selection, then:

- **Collect into workspace** saves them as a new collection in your files,
  optionally with the images and files they reference.
- **Use in chat** hands them to the assistant.

### Sources

The collections in your files: folders with `manifest.json`, `records.jsonl`
and `assets/`, as made by Collect. Each shows its records, source sessions,
assets and integrity. You can read, search and verify a collection, export it
as a folder, JSON Lines or Markdown, merge collections into a new one, or
start the assistant on it with an investigation, article, presentation or
long-form request.

### Files

Everything in your files folder. Text files preview as plain text. HTML files
preview in an isolated frame that runs their scripts but blocks the network.
Other files can be downloaded. **Ask for a revision** hands a file to the
assistant.

**Check a quote against a source record** confirms that quoted words appear
in a record of a collection. It checks the text, not whether the quote is
true.

### Assistant

Conversations with the assistant. Give it context with **Use in chat** on a
session, evidence, a collection or a file; **Preview what's sent** shows
exactly what is added to your message. Send with Ctrl+Enter (Cmd+Enter on
macOS).

One turn runs at a time across the App. **Stop** cancels the turn; files it
already wrote stay. **Tool activity** lists the tools the assistant used in
the turn and any approval it is waiting for.

**Delete conversation** removes the open conversation after you confirm:
its messages in the App and the assistant's own record of it. Files it made
stay in your files, and so do notes the assistant kept in its memory. A
conversation can't be deleted while its turn runs.

### Models

The assistant needs a model you connect here:

- **Try free models** connects the keyless services that answer: Kilo Code,
  LLM7.io, OVH AI Endpoints and Pollinations.ai. The first one that answers
  becomes the default if you have none. Your prompts go to that service, and
  its tool calling isn't tested.
- **Find local model servers** looks on this computer for Ollama (port 11434),
  LM Studio (1234), llama.cpp or LocalAI (8080), vLLM (8000) and Jan (1337).
- **With an API key or address** connects a provider from Compa's list, such
  as OpenAI, Anthropic, Google Gemini, Mistral AI, Groq, OpenRouter or Azure
  OpenAI, or any OpenAI-compatible or Anthropic-compatible address. The App
  lists the provider's models with your key and makes the first one the
  default if you have none.
- **From an extension service** connects the providers of a separate service
  you run, including providers you sign in to.
- **Test tool calling** checks that a model returns a tool call, which the
  skills need. It sends one request with a harmless tool and reads no files.
- **Routes** list several models in order: if one is busy or fails, the next
  one answers. A route can be the default.

Model settings can't change while a turn runs.

## The assistant

- It is Compa's agent, named Midden. For each request it reads the skill
  that fits and follows it: investigation, article, presentation or
  long-form. See [the skills](skills.md).
- It runs `midden` and has Pandoc 3.12, so it can make HTML slides, HTML
  books and EPUB. The App includes no browser or Python, so PDF and the
  screenshot check depend on what your computer has.
- Your files are the `files` folder of its workspace. It is told to run its
  commands and save its work there. Its commands run in Windows PowerShell
  on Windows and in `sh` elsewhere.
- Its file tools reach only its workspace and the skills. Its commands run
  with your account; this is not a sandbox. It can also search and fetch web
  pages.

### Approvals

The App uses Compa's default approval policy. The assistant reads and writes
files in its workspace, runs commands, and searches and fetches web pages
without asking. It asks before installing a skill, and before an added
module tool that may cost money, reach the network or change things outside
your computer. A request you don't answer within 60 seconds is refused.

To be asked before every command, add an approval rule to `kernel/config.json`
in the App's data folder, alongside the settings already there, and restart
Midden:

```json
"tools": { "approval": { "rules": [ { "tool": "exec", "action": "ask" } ] } }
```

The App keeps such rules when it writes its own settings.

## Where the App keeps its data

| System | Folder |
|---|---|
| Windows | `%LOCALAPPDATA%\Midden` |
| macOS | `~/Library/Application Support/Midden` |
| Linux | `~/.local/share/midden`, or `$XDG_DATA_HOME/midden` |

`--data` uses another folder. Inside it:

| Path | What it holds |
|---|---|
| `workspace/files/` | Your files: everything the assistant makes for you, and your collections. |
| `workspace/` | The assistant's own folder: `AGENT.md` (rewritten at each start), its history, memory and state. |
| `kernel/` | Compa's settings: `config.json`, `auth.json` (API keys and sign-ins, as plain JSON), `model_catalogs.json`, and its logs in `logs/`. |
| `app/` | The App's state: `sessions.json` (your conversations), `model-checks.json`, and `kernel.log`. |
| `midden-ui.lock` | Lets one App run per data folder. |

Core keeps its own state in `~/.midden`, shared with the `midden` command.
Uninstalling Midden keeps all of this.

The first start after an upgrade from 0.3 moves the 0.3 App's files into
`workspace/files` and copies its model connections. The old `host-state`
folder is left as it was.

## Privacy and security

- The page and everything behind it listen only on 127.0.0.1. The App
  answers only a browser that opened the address with this launch's key,
  and every change needs a token from the same launch.
- What you send in a conversation, with the context you add, goes to the
  model provider of your default model or route. The Models screen contacts
  providers when you list models, test them or try free models. The
  assistant's web tools contact the sites it searches and fetches.
- API keys stay in `kernel/auth.json` and are never shown in the page again.
- Midden and Compa send no telemetry.

## Troubleshooting

- **"The assistant is not running"** comes with the reason. When the
  assistant stops unexpectedly the App restarts it, unless it stopped three
  times within five minutes; saving a model setting also restarts it. Its
  output is in `app/kernel.log` (latest run only) and Compa's log in
  `kernel/logs/gateway.log`. Restart Midden to try again.
- **Port in use:** start with `--listen 127.0.0.1:18891`, or another free
  port.
- **"This Midden page is from an earlier launch":** start Midden again from
  its Start entry or with `midden-ui`.
- **"Midden is already running for this data folder, but it does not
  answer":** stop the other `midden-ui` and start again.
- **A file the assistant made isn't in Files:** it saved it outside
  `workspace/files`; ask it to save there.
- **"conversation history limit reached"** or **"Midden keeps at most 200
  conversations":** the App keeps up to 200 conversations, at most 8 MiB in
  `app/sessions.json`. Delete conversations you no longer need.
