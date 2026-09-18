# Getting started

## Recommended: inside your agentic CLI

Follow [Install](INSTALL.md), restart your selected host, and ask it to use
`midden-editorial-production`:

> Investigate these sessions. Compare the stories worth telling, their audiences,
> evidence, corrections, gaps, and risks. Help me choose one before drafting.

Follow the [agent-first editorial workflow](EDITORIAL_WORKFLOW.md) for exact
tools, host-authored extraction, project checkpoints, review, and delivery.
Use `midden-session-recovery` when the goal is only to recover lost context.

Start with one closed session or a narrow project scope. Review the proposed
evidence, redact anything private, and approve only claims the sources support.
Have the host compose content through Midden's recipe operations, review the
draft and provenance, then render or export locally. Pandoc is optional for
editable PowerPoint/HTML; D2 is optional for SVG diagrams. Your host owns model
access and permissions. A discovered skill is not proof of end-to-end acceptance.

The installed guidance binds the binary to your chosen recovery state directory;
source session stores remain read-only during discovery, assay, and extraction.

## Experimental standalone walkthrough

The remainder of this guide covers the experimental browser application, from
an empty Midden index to a reviewed output. Use a standalone archive or source
build, not the headless archive installed for CLI hosts.

## The workflow

**Recover → Studio → Library → Cleanup.**

Source sessions remain read-only throughout.

## 1. Choose a clean Midden data directory

For a disposable first run from the repository root:

```powershell
$env:MIDDEN_HOME = (Join-Path $PWD '.midden')
```

This isolates Midden's index, jobs, conversations, and outputs. It does not
change where Copilot, Claude, or OpenCode store their sessions.

If `.midden` already exists, use a new directory rather than deleting it:

```powershell
$env:MIDDEN_HOME = (Join-Path $PWD '.midden-first-run')
```

## 2. Confirm and launch

```powershell
.\bin\midden.exe version
.\bin\midden.exe start
.\bin\midden.exe ui
```

Midden binds only to `127.0.0.1` and normally opens
`http://127.0.0.1:7777`. Keep the terminal open and press `Ctrl+C` when done.

## 3. Recover one small scope

Open **Recover**.

The session inventory is consequence-sorted and can be filtered by title,
workspace, source, and age. Select one exact closed session for the first run.

Select **Mine 1 selected**. This immediately starts the free exact-session
assay, confirms that it is running, and records the result in Activity. You can
navigate elsewhere while it runs.

Use **New mine** instead when you need a configurable source, workspace, time
range, depth, or evidence-extraction backend.

Every mine records:

- exact scope;
- depth;
- source fingerprints;
- session, assay, evidence, failure, and byte counts;
- start/end state;
- durable job history.

## 4. Extract evidence

After the assay, choose **Extract evidence** for the same exact session or a
small workspace scope.

1. Choose Summary, Deep, or X-ray. These depths use different evidence record
   limits.
2. Select an already authenticated AI CLI, or leave auto-detection enabled.
3. Review the long-running estimate.
4. Start the background extraction only if the estimate is acceptable.

The selected CLI receives a bounded, redacted evidence slice rather than the
raw transcript corpus.

## 5. Create a Studio work item

Open **Studio** and select **New work item**.

Choose:

- an evidenced workspace;
- a clear finished outcome;
- an optional output starter such as tutorial + diagram.

The work item remains visible in Studio's left rail. Selecting Studio later
does not trap you inside one item; the complete list remains available.

## 6. Approve evidence and run

Open **Review evidence**.

1. Read every selected item.
2. Remove unrelated or weak evidence.
3. Save while iterating.
4. Approve only when the allowed claims are correct.

After approval, select **Preview cost and run**. The confirmation applies to
the long-running production, not to ordinary chat turns.

The production writes local drafts and stops before publishing, installation,
upload, training, or unrestricted shell execution.

## 7. Use persistent Studio chat

Studio chat is tied to the work item:

- its messages are stored in Midden's SQLite index;
- conversation and tools run through the embedded Facet Studio kernel;
- later turns retain the work-item context;
- one visible budget envelope covers routine turns;
- exceeding the envelope is blocked explicitly;
- the user can navigate while a turn runs as a background job.

Ask questions or request revisions normally. There is no estimate dialog on
every turn.

Studio's **Console** is a controlled diagnostic surface, not a host terminal.
Supported commands include:

```text
help
status
files
evidence
runs
```

## 8. Preview, edit, and download

Select an output in Studio.

- **Rendered** shows formatted Markdown, structured JSON/JSONL, media, or an
  optional renderer result.
- **Source** provides an editable source file with draft/review decisions.
- **Provenance** shows the exact evidence behind the output.
- **Download** always returns the owned source file.
- Reviewed outputs may also be exported locally.

If a renderer such as D2 is absent, Midden states that honestly and keeps the
source editable and downloadable rather than pretending a rendered diagram
exists.

## 9. Use Library and Cleanup

**Library** lists all outputs independently from their work item. Filter by
document, visual, video, data, or agent output and download directly.

**Cleanup** explains whether a session is:

- eligible;
- held;
- protected.

Eligibility requires a closed dormant source, an unchanged fingerprint,
recovered evidence, reviewed output references, and no active work-item
dependency. Archive is reversible and remains separate from permanent purge.

## CLI equivalent

```powershell
.\bin\midden.exe scan --assay
.\bin\midden.exe reclaim --workspace <name> --days 7 --dry-run
.\bin\midden.exe reclaim --workspace <name> --days 7
.\bin\midden.exe catalog
.\bin\midden.exe refine adr tsg --workspace <name> --dry-run
.\bin\midden.exe refine adr tsg --workspace <name>
.\bin\midden.exe artifacts
```

Run each command with `-h` before using optional flags.
