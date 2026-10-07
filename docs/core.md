# The `midden` command

Core is the `midden` command. It finds, reads, measures and collects the
sessions that AI agents record on your computer. It never calls a model and
makes no network connections. It opens session stores read-only; only
`prune --apply --replace` and `archive --apply` change them.

```
midden <command> [flags]
```

`midden help` lists the commands, and every command has `--help`. Flags may
come before or after other arguments. `midden version` prints the version.

## Sources

| `--tool` | Agent | What Midden reads | Default location | Override |
|---|---|---|---|---|
| `claude` | Claude Code | `projects/**/*.jsonl` | `~/.claude` | `MIDDEN_CLAUDE_ROOT` (a folder) |
| `copilot` | GitHub Copilot CLI | `session-store.db` and `session-state/` | `~/.copilot` | `MIDDEN_COPILOT_ROOT` (a folder) |
| `opencode` | OpenCode | `opencode.db` | `~/.local/share/opencode/opencode.db` | `MIDDEN_OPENCODE_DB` (a file) |

`~` is your home folder (`%USERPROFILE%` on Windows), and the paths are the
same on every system. A store that doesn't exist is skipped. Once you set
any of the three variables, Midden reads only the stores you set.

## Commands

### Discover and measure

| Command | What it does |
|---|---|
| `ls`, `list` | List sessions, newest first, 20 per page. Filter with `--tool`, `--days N`, `--workspace TEXT` and `--repo TEXT`; `--all` includes automated sessions; `--offset N` pages; `--group` groups by tool. |
| `find TEXT` | Find Claude Code and Copilot CLI sessions whose transcript contains TEXT, ignoring case. Opens up to 300 sessions (`--scan`) and reports up to 15 (`--limit`). |
| `show ID` | One session: workspace, repository, times, turns, size, resume risk and the command that resumes it. ID may be a unique prefix, or use `--session ID`. |
| `brief ID` | A short brief of a session: its goal, its last 10 turns (`--turns`) and its last answer. `--handoff` formats it as a prompt for a fresh session and saves it in `~/.midden/artifacts`. |
| `doctor` | Sessions and bytes per tool, live sessions, sessions whose workspace is gone, and sessions at resume risk. |
| `scan` | Refresh the index. `--assay` also measures transcripts, skipping unchanged ones unless `--force`. |
| `assay` | What transcripts are made of: record kinds, bytes and duplication. Without a session it reports the totals of the last `scan --assay`; with a session or `--live` it measures now. |
| `resume ID` | Print the command that resumes a session in its own agent, such as `cd '<dir>' && claude --resume ID`. `--with TEXT` adds a first message. It doesn't run anything. |
| `watch` | Every 5 minutes (`--interval`), report sessions at or above a resume risk (`--min-risk watch\|warn\|critical`). `--once` checks once, for cron or Task Scheduler. |

Resume risk is about transcript size: an agent can fail to resume a very
large session (Copilot CLI at about 680 MiB). Midden flags transcripts from
300 MiB as `watch`, from 450 MiB as `warn` and from 640 MiB as `critical`.

### Work with material

| Command | What it does |
|---|---|
| `read` | Open a view of a session (`--tool T --session ID`), page through a view (`--view V --offset N`), or read chosen records with their neighbours (`--view V --record R --before 2 --after 2`). |
| `search PHRASE --view V` | Find a literal phrase (3 to 300 bytes, ignoring case) in a view. The matches become a new view. |
| `collect --view V --out DIR` | Save records into a new collection. Add `--record R` to choose records (otherwise all records of the views) and `--assets` to copy their images and files. |
| `collection OP PATH` | Work with a collection; see [Collections](#collections). |
| `assets` | List the images and files recorded in a view (`--view V`) or session (`--tool T --session ID`). With `--record R` and `--out DIR` it copies the available ones. Remote references are never downloaded. |
| `usage --tool T --session ID` | Model usage the agent recorded: model, tokens, duration, and Copilot's AIU or OpenCode's cost when recorded. |

`read` shows up to 24 records per page (`--limit`, 1 to 256) and up to 800
characters of each (`--chars`, 80 to 8192). `--include-tools` adds recorded
tool output. `--out FILE` saves the complete result to a new file.

`read`, `search`, `collect`, `assets` and `usage` also take
`--claude-root`, `--copilot-root`, `--opencode-db`, `--sources-only` and
`--state`, which set the stores and the state folder for one run.

### Explicit source maintenance

These print what they would do. Only `--apply` changes anything.

| Command | What it does |
|---|---|
| `prune` | Make smaller copies of transcripts of 50 MiB or more (`--min-session`) by replacing large payloads with a short marker. `--apply` writes the copies to `~/.midden/pruned`; `--replace` then swaps a verified copy in and keeps the original beside it as `.original`. `--artifacts` also prunes images. |
| `archive` | Move Claude Code and Copilot CLI transcripts out of the agent's store into `~/.midden/archive`, each with a `manifest.json`. Live sessions are skipped. The archive then holds the only copy. |
| `ops` | The log of prune and archive operations. |

Both take a session prefix and the same filters as `ls`; `archive` without
any targets every Claude Code and Copilot CLI transcript.

## Views and records

A view is a pinned selection of one session's records, with an ID of `v-`
and a hash. It covers the records that existed when it was opened: newer
activity needs a new view, and a view whose records changed in the source is
refused. Views are saved in `~/.midden/views` and never change.

A record carries its text (clipped to `--chars`), kind, role, time and an
opaque ID; keep record IDs exactly as given. By default records are the
messages of the conversation and references to its images and files;
`--include-tools` adds tool output. Instructions injected by an agent are left
out.

## Collections

A collection is a portable folder of chosen records:

| Path | What it holds |
|---|---|
| `manifest.json` | The sources with their checksums, counts, assets and warnings. It is written last and marks the collection complete. |
| `records.jsonl` | The records, by time. |
| `assets/` | Copied images and files. |

`midden collection OP PATH`:

| OP | What it does |
|---|---|
| `inspect` | Read the manifest without checking it. |
| `read` | Page through the records (`--offset`, `--limit`, `--record R`). |
| `search --query TEXT` | Records containing TEXT, ignoring case. |
| `select --record R --out NEW` | A new collection with chosen records and their assets. |
| `merge A B --out NEW` | A new collection from several. Conflicting versions of a record are refused. |
| `verify` | Check the collection against its manifest. Exits 1 if it is not valid. |
| `verify --record R --quote TEXT` | Check that TEXT appears in record R. This checks the text, not whether it is true. |
| `export --out NEW --format directory\|jsonl\|markdown` | Write it out. JSON Lines and Markdown need a collection without assets. |

A collection holds at most 10,000 records. Output paths must not exist yet;
Midden never overwrites.

## JSON output

`--json` prints exactly one JSON document on standard output:

- Lists are always arrays, empty as `[]`; fields are never `null`, and
  optional fields are left out. Names are `snake_case`.
- On failure nothing is printed on standard output; the reason goes to
  standard error with a non-zero exit code. `collection verify` is the
  exception: it prints its report, then exits 1 when the collection is not
  valid.
- `ls` and the material commands keep a result under 16 KiB. A page shrinks
  to fit and gives `next_offset`; use `--out` for complete results.

Progress, warnings and errors go to standard error. Colour is used only on a
terminal, and `NO_COLOR` turns it off.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Success. |
| 1 | The command failed, or a collection or quote didn't verify. |
| 2 | Unknown command, or invalid flags for some commands. |
| 3 | `watch --once` found a session at or above the risk level. |

## State

Core keeps derived data in `~/.midden`, or in `$MIDDEN_HOME`:

| Path | What it holds |
|---|---|
| `core-index.db` | The index that `scan` refreshes, measurements, and the log of prune and archive operations. |
| `views/` | Pinned views. |
| `artifacts/` | Handoff notes from `brief --handoff`. |
| `pruned/` | Copies made by `prune --apply`. |
| `archive/` | Transcripts moved by `archive --apply`: the only copy of each. |

Commands read the session stores directly every time; the index only speeds
some of them up. Views and the index can be rebuilt, but the operation log
in `core-index.db` and everything in `archive/` cannot.

## Credentials in output

Core hides recognisable credentials in what it shows, in text and JSON: API
keys and tokens (GitHub, AWS, Slack, OpenAI, Anthropic, Google and others),
private keys, JSON Web Tokens, passwords in connection strings and
assignments, and bearer tokens. IDs, paths and numbers are shown as they
are. This is not a privacy review: check what you share.
