# Human acceptance test

Run this only after the automated build and browser gates pass. It is the final
nondestructive operator check for a release.

## Test rules

- Start from a fresh clone.
- Use a new `MIDDEN_HOME`.
- Do not run prune, archive, delete, publish, install, upload, training, or
  shell actions.
- Use one small workspace for model-backed evidence extraction.
- Stop if any estimate is unacceptable.

## 1. Fresh clone and build

```powershell
git clone <repository-url> midden-human-test
Set-Location .\midden-human-test

go version
go test ./...

New-Item -ItemType Directory -Force .\bin | Out-Null
go build -trimpath -o .\bin\midden.exe .\cmd\midden
.\bin\midden.exe version

$env:MIDDEN_HOME = (Join-Path $PWD '.midden-human-test')
.\bin\midden.exe start
```

Pass if:

- the build requires no undocumented dependency;
- version prints `midden 2.1.0`;
- `start` explains what is present, what can spend, and what to do next;
- no existing Midden state was reused.

## 2. Launch

```powershell
.\bin\midden.exe ui
```

Pass if:

- a browser opens to a loopback URL;
- Home renders without an unexplained empty page;
- navigation labels and the next action are understandable;
- no horizontal scrolling is required at normal desktop width.

## 3. Mine

Open **Mine** and select **Scan and calculate yield**.

Pass if:

- the operation clearly says it is deterministic and free;
- progress is visible;
- source availability is understandable;
- assay-only state does not invent recommended assets;
- the evidence library explains why it is empty.

## 4. Extract a small evidence scope

Select **Extract evidence**, choose one workspace, a short time range, and
**Summary · cheapest**.

Pass if:

- the first action estimates only;
- backend, model, time, charge confidence, and external writes are visible;
- no model call begins before approval;
- the completed run reports how many evidence items were stored.

## 5. Test Conductor as a person

Open **Conductor** and send:

```text
hi
```

Pass if it replies conversationally and stays in Conductor.

Then send:

```text
I need help with my project.
```

Pass if it asks whether you want to search, create, or run rather than guessing.

Then send:

```text
What can I create from my evidence?
```

Pass if the answer is grounded in the visible evidence count and does not call
a model unnecessarily.

Then request:

```text
Create an ADR and troubleshooting guide from this workspace.
```

Pass if:

- a plan preview appears in the conversation;
- outputs and evidence count are visible;
- nothing is persisted before **Create this plan**;
- after confirmation, Midden says nothing has run or spent and opens Studio.

## 6. Studio evidence gate

Pass if:

- selected evidence is readable;
- low-confidence items are identifiable;
- search and review-only filtering work;
- selections can be changed and saved;
- production cannot run before **Approve evidence set**.

## 7. Production and review

After approving evidence, select **Preview cost and run**.

Pass if:

- the approval says drafts only and external writes none;
- the run has a durable, understandable timeline;
- generated outputs open inside Midden;
- structured packs are readable rather than raw JSONL walls;
- individual records can be included or excluded;
- provenance changes with the reviewed output;
- export remains unavailable until that output is reviewed.

## 8. Mobile and keyboard

Resize to a narrow phone-like width and complete basic navigation.

Pass if:

- the drawer opens, closes, and has a scrim;
- controls are comfortably tappable;
- no content is clipped horizontally.

Using only the keyboard:

- activate the skip link;
- move through navigation and session rows;
- open and close a dialog;
- confirm focus is trapped in the dialog and restored afterward.

## 9. Stop and report

Press `Ctrl+C` in the UI terminal.

Report:

- **PASS** or **FAIL**;
- the first step that failed;
- what you expected;
- what happened instead;
- a screenshot only if it contains no private session content.

A release is human-accepted only when all sections pass.
