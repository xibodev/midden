# Historical prototype

`ai-sessions.ps1` is the Windows-only research prototype that preceded the Go
application. It is retained as implementation provenance for the original
cross-tool storage discoveries.

It is **not** part of the build, installation, first-run workflow, or supported
product surface. New users should start at the repository
[README](../README.md).

The prototype established several rules that remain in the Go implementation:

- open source session stores read-only;
- read OpenCode from its database rather than its project-scoped CLI list;
- fall back to the first Copilot user turn when a summary is blank;
- detect Claude live sessions using its PID markers and process liveness;
- hide automated and trivial noise by default without deleting it;
- flag missing workspaces before offering a resume command;
- use calendar-day windows consistently;
- use session span to reveal long-running work.

The script has no index, assay, evidence extraction, refinery, cost controls,
MCP server, browser UI, or cross-platform support. Do not use it as an
alternative installer.
