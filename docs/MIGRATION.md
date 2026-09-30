# Moving to Midden v0.3.1

**Back up the old workspace and state before upgrading. Stop the old host
first.** v0.3.1 is not an in-place rewrite of original AI CLI session stores and
does not implicitly delete old working data.

The new app is one host for the same core and outcome bundle used by an existing
AI CLI. It does not restore v0.2 module, Studio or editorial lifecycle APIs.

## What stays yours

- Original Copilot CLI, Claude Code and OpenCode source stores. Investigation
  remains read-only.
- Existing drafts, collections, exports, assets and ordinary working directories.
- Legacy `index.db`. The deterministic core uses a separate `core-index.db`.
- Existing host state and credentials. Unsupported connections are reported, not
  silently reassigned to another provider.

Install into a fresh directory if ownership or compatibility is uncertain.
Uninstall removes owned installation files, not workspace/state data. Do not
delete a data directory because a new installation has an empty conversation
list or cache.

## From the withdrawn Windows v0.3.0 bootstrap

The first Windows bootstrap recorded an expanded PATH string rather than its
original registry text and value type. The corrected bootstrap preserves raw
PATH presence, text, expansion tokens and `String`/`ExpandString` type. It
restores them only while the current value still matches its owned after-state;
concurrent edits remain untouched.

An old string-only receipt cannot reconstruct information it never recorded.
Use an explicit `-NoPath` when upgrading or removing such an installation with
the corrected script. This leaves PATH unchanged and emits a warning; it does
not pretend to recover the original value. Review or restore PATH from a known
backup separately if needed. The usual file-ownership and data-preservation
checks still apply.

Fresh installations receive the corrected raw-state receipt. Workspace, host
state, credentials and the agentic bundle are not reset by this correction.

## From v0.2 and retired interfaces

| Retired interface | v0.3.1 equivalent or owner |
|---|---|
| Module invocation envelopes, descriptors and grants | Ordinary `midden` commands and direct data results |
| Mandatory agent/workflow protocol | Natural requests in the app or an existing AI CLI, following the outcome bundle |
| Editorial projects, recipes and approval APIs | Ordinary working files, portable source collections and operator decisions |
| Model execution inside core (`ask`, `reclaim`, `refine`) | The separate app host or your existing AI CLI |
| Old Studio UI | The new Midden app; no old UI/state-machine compatibility layer |
| Core article/deck type enum | Outcome guidance and external tools in the canonical bundle |
| Duplicated embedded skills or workflow MCP setup | The matching outcome bundle; no mandatory MCP setup |

Old module-era installer receipts and host registrations are not automatically
converted. Keep a copy of the matching old installation/uninstaller, or use a
fresh scoped destination for v0.3.1. Do not assume an old public revision or
download will remain available as your backup.

### Explicit, read-only legacy export

```text
midden legacy export --from OLD_STATE --out NEW_DIRECTORY --json
```

The destination must not exist, its parent must already exist, and it must
resolve outside the old store. The operation opens the legacy database
read-only, writes known working tables as JSONL, and copies available recorded
artifacts confined to the old state root.

Missing or out-of-scope artifacts are reported, not described as recovered.
Per-file, aggregate-asset and per-table bounds prevent unbounded export. Review
the report and the actual files before retiring the old installation.

Legacy approval/status fields are historical records. Exporting them creates
no new human approval and does not certify or publish content.

## From a Facet-backed app development build

The app embeds **Compa v1.0.0**. This runtime migration is separate from the
v0.2 legacy database export above.

When reopening the same explicit host-state directory, startup validates the
legacy `kernel-history` JSON files and stages their conversion into the
`compa-history` JSONL store before starting the agent. The original
`kernel-history` directory remains byte-for-byte as the legacy backup.
`compa-history/migration-v1.json` records the migration inventory and digests.

Keep the old history, migration receipt and new history together in backups.
Later starts check that the legacy inventory and bytes still match the receipt;
new Compa turns are not overwritten by reimporting the old files.

Startup refuses conflicts such as a nonempty target without a valid receipt,
changed legacy history, or an interrupted/concurrent staging directory. Failed
staging is retained for inspection rather than resumed or merged implicitly.
Do not delete history, receipts or staging files to bypass the error. Preserve
both copies and restore a coherent backup, or use a separate new state directory
while investigating.

The UI's conversation records and working artifacts remain distinct from kernel
history. An earlier launch using only `--workspace WORKSPACE` keeps its original
`WORKSPACE\.midden-ui` state binding (`WORKSPACE/.midden-ui` on Unix); the new
no-argument per-user defaults do not silently move that history.

If the earlier build used a separate host-state directory, reopen both paths
explicitly:

```powershell
midden-ui --workspace .\example-work --state .\example-state
```

## Reconnect unsupported model settings

This release supports **OpenAI-compatible** and **Anthropic-compatible**
connections, including suitable local endpoints. Direct native Copilot/Codex
sign-in is not bundled. A separately configured extension provider would be a
different integration; this host does not include one.

A saved retired provider remains visible with **Reconnect required**. Sending
is disabled, but conversations, drafts and files remain accessible. The app does
not silently choose another provider, convert credentials, or treat a previous
model selection as authenticated.

Open **Settings**, explicitly choose a supported provider and endpoint, then
enter the intended credentials and exact model ID. **Find models** is only a
catalog; **Check model** makes a small inference probe with provider usage and
no workspace reads. Save the replacement settings yourself.

Changing provider or endpoint clears inherited API-key/reference fields. You
may explicitly enter a reference afterward if you intend to reuse it. Existing
supported API-key connections retain their provider identity.

An AI CLI installed through `-Mode cli` / `--mode cli` continues to use that
host's own authenticated connection. App provider limits do not change the AI
CLI's sign-in.

## Keep existing work discoverable

Point the host at the working directory containing the material you want to
continue. Selected records, notes, outlines, drafts and rendered outputs are
files; their existence does not depend on an editorial project table.

See [Installation](INSTALL.md) for defaults, version-pinned updates and ownership
checks, and [Configuration](CONFIGURATION.md) for source scope and state paths.
