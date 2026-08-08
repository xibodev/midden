# Getting started

This guide takes a new installation from an empty Midden index to one reviewed,
locally exported output. It does not use destructive actions.

## The workflow in one line

**Scan → assay → extract evidence → design → approve evidence → produce drafts
→ review → export.**

Source sessions remain read-only throughout.

## 1. Choose a clean Midden data directory

For a disposable first run from the repository root:

```powershell
$env:MIDDEN_HOME = (Join-Path $PWD '.midden')
```

This isolates Midden's index and outputs. It does not change where Copilot,
Claude, or OpenCode store their sessions.

If `.midden` already exists and you need a truly fresh trial, use a new name:

```powershell
$env:MIDDEN_HOME = (Join-Path $PWD '.midden-first-run')
```

Do not delete or overwrite an existing Midden home.

## 2. Confirm the binary

```powershell
.\bin\midden.exe version
.\bin\midden.exe start
```

`start` reports what Midden can currently see, which actions are free, which
can spend, and the highest-value next steps.

## 3. Start the UI

```powershell
.\bin\midden.exe ui
```

Midden binds only to `127.0.0.1` and normally opens
`http://127.0.0.1:7777`.

To prevent automatic browser launch:

```powershell
.\bin\midden.exe ui --no-open
```

Keep that terminal open. Press `Ctrl+C` when finished.

## 4. Mine the local session stores

Open **Mine** and select **Scan and calculate yield**.

This first pass:

- discovers supported local session stores;
- refreshes Midden's local index;
- classifies recent transcripts;
- calculates signal, exhaust, artifact, and reclaimable yield;
- makes no model call.

The default UI mine covers the last 30 days. A source that is not installed is
reported as unavailable rather than treated as an error.

If no sessions appear, see [Troubleshooting](TROUBLESHOOTING.md).

## 5. Extract a small evidence scope

After assay completes, select **Extract evidence**.

For a first run:

1. Choose one workspace rather than all evidence.
2. Choose **Last 7 days** or **Last 30 days**.
3. Choose **Summary · cheapest**.
4. Keep **Auto-detect signed-in CLI**, or choose a known backend.
5. Select **Estimate evidence extraction**.
6. Inspect the backend, model, time, charge confidence, and external writes.
7. Select **Approve spend and run** only if the estimate is acceptable.

The selected backend receives a bounded, redacted evidence slice rather than
the raw transcript corpus. Extracted items retain source provenance and
confidence.

If no supported AI CLI command is installed and signed in, stop here. You can
still use session discovery, assay, rescue, Operations, and MCP.

## 6. Use Conductor like a conversation

Open **Conductor**.

Expected behavior:

- `hi` receives a greeting and no action is created.
- `help` gives examples.
- An ambiguous statement triggers a clarification.
- A simple evidence-count or yield question is answered locally.
- A model-backed question shows an estimate before calling a backend.
- A creation request previews a plan in chat.
- A plan is persisted only after **Create this plan**.

Try:

```text
hi
```

Then:

```text
What can I create from my evidence?
```

Then a scoped request:

```text
Create an ADR and troubleshooting guide from my recent authentication work.
```

Review the proposed outputs and evidence count. Adjust the request if the
interpretation is wrong. Creating the plan does not run it or spend.

## 7. Approve the evidence in Studio

Conductor opens the saved plan in **Studio**.

1. Review every selected evidence item.
2. Search or filter the candidate list.
3. Remove weak or unrelated evidence.
4. Inspect low-confidence warnings and claim-type coverage.
5. Select **Save selection** while iterating.
6. Select **Approve evidence set** only when the allowed claims are correct.

The evidence gate controls every output in the bundle.

## 8. Produce drafts

Select **Preview cost and run**.

The confirmation states:

- output count;
- evidence count;
- backend and model;
- estimated time;
- whether the estimate is calibrated;
- external writes.

The run writes only to Midden's artifact workspace. It stops at drafts and does
not publish, upload, install, train, or invoke a shell.

## 9. Review and export

Each output has its own review state. For structured JSONL packs, Studio shows
readable records, source and confidence, include/exclude controls, raw content,
and filtered provenance.

Review an output before exporting it. Reopening a completed draft returns the
recipe to review so the workflow cannot imply approval that no longer exists.

Exports are local under:

```text
<MIDDEN_HOME>\artifacts\
```

Every evidence-derived export has a provenance sidecar.

## CLI equivalent

The same basic path is available without the browser:

```powershell
.\bin\midden.exe scan --assay
.\bin\midden.exe catalog
.\bin\midden.exe reclaim --workspace <name> --days 7 --dry-run
.\bin\midden.exe reclaim --workspace <name> --days 7
.\bin\midden.exe nuggets
.\bin\midden.exe refine adr tsg --workspace <name> --dry-run
.\bin\midden.exe refine adr tsg --workspace <name>
.\bin\midden.exe artifacts
```

Run each command with `-h` before using optional flags.

## Stop safely

Press `Ctrl+C` in the terminal running `midden ui`. Midden closes the loopback
server and leaves its local state available for the next run.
