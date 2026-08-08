# Human acceptance test

Run this after the automated build and desktop browser gates pass. It is the
final nondestructive operator check for a release.

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
- version prints `midden 2.2.0`;
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
- managed Open Notebook and OpenMontage settings remain editable.

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
