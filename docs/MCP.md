# Using Midden from an AI CLI

Midden exposes its session index as an MCP server, so any agent can survey your whole
session history cheaply instead of guessing or shelling out to `ls`.

Every tool declares a hard token budget and truncates to it, reporting what was omitted.
A model can call `midden_list_sessions` with no filters and know it will never blow context.

## Measured cost

Run against 682 real sessions / 36.3 GiB:

| Tool | Actual | Budget |
|---|---|---|
| `midden_health` | **158** tokens | 400 |
| `midden_list_sessions` (all 682) | **3,859** tokens | 4,000 |
| `midden_list_sessions --days 5` | 1,278 tokens | 4,000 |
| `midden_search` | 514 tokens | 1,500 |
| `midden_session_brief` (681 MiB session) | **283** tokens | 1,500 |
| `midden_resume_command` | 61 tokens | 200 |

Surveying an entire multi-gigabyte session history costs less than reading one source file.

## Register the server

Build first:

```bash
go build -o midden ./cmd/midden
```

### Copilot CLI — `~/.copilot/mcp-config.json`

```json
{
  "mcpServers": {
    "midden": {
      "command": "C:\\path\\to\\midden.exe",
      "args": ["mcp"],
      "type": "local",
      "tools": ["*"]
    }
  }
}
```

### Claude Code

```bash
claude mcp add midden -- /path/to/midden mcp
```

### opencode — `opencode.json`

```json
{
  "mcp": {
    "midden": {
      "type": "local",
      "command": ["/path/to/midden", "mcp"],
      "enabled": true
    }
  }
}
```

## Tools

| Tool | Purpose |
|---|---|
| `midden_health` | Orientation: footprint, counts, at-risk, open now. **Call this first.** |
| `midden_list_sessions` | One compact line per session. Filters: `tool`, `days`, `workspace`, `limit`. |
| `midden_search` | Match on title, workspace or repository. |
| `midden_session_brief` | Original goal + recent exchanges for one session. Works on sessions too large to resume. |
| `midden_resume_command` | The exact shell one-liner, in the host OS dialect. Warns if open or oversized. |

### Intended flow

```
midden_health                 → orientation, ~150 tokens
midden_list_sessions(days=7)  → find the session
midden_session_brief(id=...)  → recover its context
midden_resume_command(id=...) → get back into it
```

## Notes

- **Read-only.** No tool mutates a source store. Sessions are opened `mode=ro` + `query_only`.
- **No model calls.** Everything is deterministic extraction; the server costs tokens only in
  the sense that its *output* enters your context.
- **Truncation is always announced**, with the omitted count and a hint to narrow scope.
  Silent truncation would be worse than short output.
- **`midden_session_brief` works past the resume cliff.** A 681 MiB transcript that
  `--resume` cannot load still yields its goal and last exchanges in ~280 tokens.
