# MCP setup

Midden exposes a small, read-only, token-bounded MCP server over stdio. It lets
an AI client find and summarize local AI coding sessions without reading raw
multi-gigabyte stores into context.

The MCP surface does not call a model and does not mutate a source store.
The default tools remain read-only. An explicit `--workflow` opt-in adds the
local evidence-to-content workflow described below.

## Opt-in agentic production

Start `midden mcp --workflow --home <absolute-state-directory>` to expose
`midden_evidence_prepare`, `midden_evidence_compose`, project/editorial operations,
and the shared recipe, composition, review, rendering and local-export tools.
Tool names are capability names prefixed by `midden_` with dots replaced by
underscores. Each tool uses the module's input schema and returns its envelope.

The server operator fixes the writable state root at startup; a tool call cannot
override it. Mutations affect Midden state only. Model subprocess extraction,
publishing, installation and cleanup are not exposed. The host agent performs
semantic work between prepare/compose operations and owns its model budget.
Clients supporting MCP elicitation receive real operator confirmation forms for
evidence approval, draft review and larger read budgets. A declined/cancelled
form, missing elicitation capability or disconnected client leaves review pending.
Tool arguments cannot supply a confirmation callback. Read-only CLI discovery
and investigation remain useful without elicitation; CLI approval calls cannot
turn an agent's assertion into human consent.
For production use, configure the host's per-tool approval policy for state
changes, evidence approvals, draft reviews and local exports. Turning on workflow
tools does not mean every output is approved.

See [Editorial workflow](EDITORIAL_WORKFLOW.md). Configure MCP `args` as
`["mcp", "--workflow", "--home", "C:\\absolute\\midden-state"]` on Windows;
leave `["mcp"]` unchanged when only recovery tools are wanted.

For the exact installed binary/state binding, use
`midden agent mcp-config --home <state-directory>` and load that generated
configuration in the host. This is a one-time setup step, not operator-driven
tool orchestration during an investigation.

## Before registration

Build or install Midden and refresh its index:

```powershell
New-Item -ItemType Directory -Force .\bin | Out-Null
go build -trimpath -o .\bin\midden.exe .\cmd\midden

$env:MIDDEN_HOME = (Join-Path $PWD '.midden')
.\bin\midden.exe scan
.\bin\midden.exe version
```

Use the absolute executable path in MCP configuration. A relative path often
breaks because an AI client starts servers from a different working directory.

If you use a custom `MIDDEN_HOME`, configure the AI client to pass the same
environment variable to the MCP process, or start the client from a shell that
already has it set.

## GitHub Copilot CLI

Add a server entry to `%USERPROFILE%\.copilot\mcp-config.json`:

```json
{
  "mcpServers": {
    "midden": {
      "type": "local",
      "command": "C:\\absolute\\path\\to\\midden.exe",
      "args": ["mcp"],
      "tools": ["*"]
    }
  }
}
```

Merge this entry with any existing `mcpServers` object rather than replacing
the complete file.

Restart Copilot CLI after editing the configuration.

## Claude Code

From a shell where the intended `MIDDEN_HOME` is set:

```powershell
claude mcp add midden -- 'C:\absolute\path\to\midden.exe' mcp
```

Use Claude Code's MCP inspection command to confirm the server is connected,
then restart the session if the tool list was already loaded.

## OpenCode

Add a local MCP server to `opencode.json`:

```json
{
  "mcp": {
    "midden": {
      "type": "local",
      "command": [
        "C:\\absolute\\path\\to\\midden.exe",
        "mcp"
      ],
      "enabled": true
    }
  }
}
```

Merge this object with existing OpenCode configuration and restart OpenCode.

## Tools

| Tool | Purpose | Output budget |
|---|---|---|
| `midden_health` | Footprint, session counts, open sessions, dead workspaces, resume risk | about 400 tokens |
| `midden_list_sessions` | Compact session list with optional tool, day, workspace, and limit filters | about 4,000 tokens |
| `midden_search` | Match title, workspace, or repository | about 1,500 tokens |
| `midden_session_brief` | Original goal and recent exchanges for one session | about 1,500 tokens |
| `midden_resume_command` | Exact host-OS resume command with safety warnings | about 200 tokens |

Truncation is explicit and includes the omitted count. The server never
silently floods the caller's context.

## Recommended agent flow

```text
1. midden_health
2. midden_list_sessions with days or workspace filters
3. midden_session_brief for one session
4. midden_resume_command only when the workspace exists and the session is safe
```

Suggested instruction for an MCP-enabled agent:

```text
Use midden_health first. Narrow session lists before requesting briefs. Never
resume a session marked open or past the resume-risk threshold; recover a brief
and hand off instead.
```

## Behavior and limits

- The transport is newline-delimited JSON-RPC 2.0 over stdio.
- `midden mcp` is expected to wait quietly when run by hand.
- Tool output is deterministic extraction, not a model answer.
- Source databases are opened read-only.
- The default server does not expose mutations. The opt-in workflow exposes
  local composition and review, but not cleanup or integration probes.
- A brief can recover context from a transcript too large for its original CLI
  to resume.
- Claude live-session detection prevents an agent from being told to resume a
  session already open in another terminal.
- Copilot and OpenCode do not expose a trustworthy live marker, so their
  liveness cannot be reported.

## Troubleshooting

### The client reports that the executable does not exist

Verify the configured path:

```powershell
& 'C:\absolute\path\to\midden.exe' version
```

Use the final `.exe` path, not the repository directory.

### The server connects but returns no sessions

Run:

```powershell
& 'C:\absolute\path\to\midden.exe' scan
& 'C:\absolute\path\to\midden.exe' doctor
```

Confirm the MCP process receives the same `MIDDEN_HOME` and runs as the same
operating-system user.

### Starting `midden mcp` appears to hang

That is normal. It is waiting for stdio JSON-RPC messages. Press `Ctrl+C` when
testing manually.

### The tool list does not appear

Restart the AI client after changing MCP configuration. Many clients load MCP
tools only when a session starts.

See [Troubleshooting](TROUBLESHOOTING.md) for source discovery and state-path
issues.
