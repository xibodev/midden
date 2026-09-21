# Development

Read `OBJECTIVE.md`. Keep deterministic core behavior separate from outcome
guidance and the host AI's execution.

## Build and test

Requires Go 1.26.5. Python 3 is used for packaging and bundle/installer checks.

```powershell
go build -trimpath -o midden.exe .\cmd\midden
go test ./...
go vet ./...
python -B -m unittest discover -s tests\release -p "test_*.py" -v
```

There is no `headless` variant or embedded AI runtime. Core builds must not pull
in model/provider SDKs, Studio, the retired module adapter, or bundle content.

Bundle rendering tests use a separately provisioned Pandoc:

```powershell
$env:PANDOC = 'PATH_TO_PANDOC'
python -B -m unittest discover -s tests\bundles -p "test_*.py" -v
```

Use the exact executable path for your isolated tool installation. A missing
renderer is a failed prerequisite, not a passing rendering test.
HTML inspection tests also require the scoped Python/Playwright environment and
browser installation described in the shared bundle tools guide. Run those tests
with that interpreter; they must inspect rendered pages rather than infer layout
from CSS.

## Distribution

From a clean checkout:

```powershell
python scripts\build-release.py --out ABSOLUTE_EMPTY_DIRECTORY --targets windows/amd64
python scripts\verify-release.py --directory ABSOLUTE_EMPTY_DIRECTORY --commit COMMIT_SHA --smoke
```

The artifacts are independent:

- Native core archive: executable, license and core documentation.
- Universal bundle archive: canonical bundle material and installation tooling.
- Build manifest and SHA-256 checksums: source revision, version and target list.

Release tooling packages only allowlisted public source files. It does not bundle
state, private source material, generated acceptance outputs or credentials.
Bundle inputs come from the reviewed Git commit, not recursive directory scans.
Ignored local settings are excluded even when the checkout reports clean. Archive
verification checks the exact member set against that source revision.

## Test responsibilities

Core tests validate exact data behavior and source safety with synthetic stores.
Bundle tests render real artifacts and inspect their structure/appearance. Live
agent evaluations check outcomes from natural prompts; do not infer successful
authoring from a particular sequence of tool calls.

Do not weaken a test merely to match a refactor. When retiring a responsibility,
retire its old integration tests and preserve relevant data/safety coverage at
the new owner. Keep private acceptance records outside the repository.
