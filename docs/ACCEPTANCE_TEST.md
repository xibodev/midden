# Optional manual desktop walkthrough

This walkthrough is for subjective usability feedback and real-person product
critique. It is not required to complete the build, tests, or engineering
release gate.

## Test rules

- Start from a fresh clone.
- Use a new `MIDDEN_HOME`.
- Do not prune, archive, publish, install, upload, train, or purge.
- Use one small exact session for model-backed evidence extraction.
- Stop if any long-running estimate is unacceptable.
- Midden is a desktop workbench. Test a normal desktop window and a compact
  desktop window; phone/mobile behavior is not a release requirement.

## 1. Fresh clone and build

```powershell
git clone <repository-url> midden-human-test
Set-Location .\midden-human-test

go test ./...
New-Item -ItemType Directory -Force .\bin | Out-Null
go build -trimpath -o .\bin\midden.exe .\cmd\midden
.\bin\midden.exe version

$env:MIDDEN_HOME = (Join-Path $PWD '.midden-human-test')
.\bin\midden.exe start
```

Pass if:

- the build requires no undocumented dependency;
- version prints `midden 0.2.2`;
- no existing Midden state is reused.

## 2. Launch and navigation

```powershell
.\bin\midden.exe ui
```

Pass if:

- the browser opens to a loopback URL;
- Recover explains the next action;
- Recover, Studio, Library, Cleanup, Activity, and Tools all open;
- there is no body-level horizontal scrolling at approximately 1440×900 or
  1024×768.

## 3. Exact-session mine

In **Recover**:

1. Filter and select one small closed session.
2. Open **New mine**.
3. Choose **Assay only · free**.
4. Start the mine.
5. Navigate to another view while it runs.

Pass if:

- no execution modal blocks the application;
- the background dock and Activity show progress;
- the completed mine appears in durable history with its exact session scope;
- source file size and modification time remain unchanged.

## 4. Evidence extraction

For the same exact session:

1. Choose **Extract evidence**.
2. Compare Summary and Deep previews.
3. Confirm their record limits differ.
4. Approve one small extraction.

Pass if:

- preview does not invoke a model;
- backend, depth, scope, estimate, and time are visible;
- extraction runs in the background;
- the result reports stored evidence.

## 5. Studio work item

Create a work item from the recovered evidence.

Pass if:

- it appears in the permanent work-item rail;
- selecting Studio again keeps the full list visible;
- evidence can be inspected, changed, saved, and approved;
- production remains blocked until evidence approval.

## 6. Persistent chat

Send two concise messages in Studio.

Pass if:

- no cost dialog appears for each routine turn;
- both turns run under the visible work-item budget;
- messages survive a browser refresh;
- the second turn resumes the same AI CLI session;
- navigation remains available while the turn runs.

Use Console `status` and `files`.

Pass if:

- the commands return work-item diagnostics;
- arbitrary shell commands are rejected.

Studio **Chat** is different from Console:

- clicking **Send** starts a durable background agent job;
- a narrow command is executed as written rather than replaced by the
  background work-item goal;
- tool traces stay out of the saved chat answer;
- the agent cannot use `.copilot`, `.claude`, OpenCode, or Midden state as its
  working directory;
- a finished file placed in the per-work-item handoff directory appears as a
  draft output in Studio.

## 7. Production, preview, and download

Run a small approved output plan.

Pass if:

- the long-running production gets one explicit estimate/approval;
- generated outputs become local drafts;
- Markdown renders as a formatted document;
- JSONL renders as readable records;
- Source is editable;
- Provenance identifies supporting evidence;
- Download returns the owned source without navigating away;
- an absent optional renderer produces an honest fallback rather than a fake
  rendered result.

## 8. Library, Cleanup, Activity, and Tools

Pass if:

- Library filters outputs and downloads them independently of Studio;
- Cleanup explains every eligibility gate;
- protected or held sessions cannot be archived through the UI;
- Activity retains jobs after browser refresh;
- Tools distinguishes plugins, tools, skills, viewers, and destinations;
- managed Open Notebook settings remain editable;
- imported MP4/WebM outputs preview without reading the binary into the JSON API.

## 9. Keyboard and compact desktop

Using only the keyboard:

- activate the skip link;
- navigate the sidebar;
- open and close a dialog and drawer;
- confirm focus remains contained and is restored.

Resize to a compact desktop window around 1024×768. Pass if the workbench
remains usable without body-level horizontal overflow.

## 10. Stop and report

Press `Ctrl+C` in the UI terminal.

Report:

- **PASS** or **FAIL**;
- the first failed step;
- expected versus actual behavior;
- a screenshot only if it contains no private session content.

Automated engineering completion does not depend on this walkthrough.
