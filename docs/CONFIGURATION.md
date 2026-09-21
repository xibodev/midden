# Configuration

## Core state

`MIDDEN_HOME` selects the core cache directory. Without it, Midden uses
`.midden` under the resolved user home.

- `core-index.db`: derived session metadata, measurements and maintenance log.
- `views`: bounded, pinned source views for continued investigation.

The core does not store model credentials or configure a provider. A legacy
`index.db` remains separate and untouched. Collections and authored work should
be saved to explicit working directories, not left only in the cache.

## Source stores

| Tool | Default source |
|---|---|
| Copilot CLI | `.copilot` under the user profile |
| Claude Code | `.claude` under the user profile |
| OpenCode | `.local/share/opencode/opencode.db` under the user profile |

Material commands accept explicit `--copilot-root`, `--claude-root` and
`--opencode-db` values. `--sources-only` disables fallback to other profile
locations. `--state` changes only the core cache location.

For a whole shell or agent conversation, set `MIDDEN_COPILOT_ROOT`,
`MIDDEN_CLAUDE_ROOT` and/or `MIDDEN_OPENCODE_DB` to the explicit source locations.
When any are set, that set is closed: unrelated ambient stores are not read.
This does not change the host CLI's own home, authentication or session storage.

Changing `MIDDEN_HOME` does not move source stores. Source paths are read-only
during discovery, analysis, reading, collection and asset extraction.

## Bundle tools

The bundle installer binds skills to a specific core executable and state
directory. It does not configure models or grant host permissions. Optional
external tool requirements belong to the outcome bundle; see the
[bundle guide](../bundles/README.md).

Use the host's existing settings for model selection, account access,
permissions and operator interaction.
