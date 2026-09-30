# Development

Read `OBJECTIVE.md`. Keep deterministic core behavior, canonical outcome
guidance and host AI execution separate. The public entry points are documented
in [Installation](INSTALL.md); an end user does not need the source toolchain.

## Build the two executables

Use **Go 1.26.6** to build all components. The core module's floor is Go 1.26.5;
the app module requires Go 1.26.6. Python 3.9+ is used by the CLI installer and
packaging/check helpers, not by the packaged app.

The public module paths are `github.com/xibodev/midden` for core and
`github.com/xibodev/midden/apps/midden-ui` for the separate app module.

From the repository root, in PowerShell:

```powershell
go build -trimpath -o midden.exe .\cmd\midden
go test ./...
go vet ./...
```

The app is a separate Go module:

```powershell
Set-Location .\apps\midden-ui
go build -trimpath -o midden-ui.exe .
go test ./...
go vet ./...
```

Use native executable names/path separators on other platforms. The app embeds
Compa v1.0.0; core builds do not import that kernel, model/provider SDKs, the
retired module adapter or bundle content. The canonical bundle remains a
separate set of guidance and supporting files.

A source-built UI still needs the matching core and bundle in its intended
layout. Refer to the [host developer notes](../apps/midden-ui/README.md), not
the retired UI or module integration.

## Independent checks

From the repository root:

```powershell
python -B -m unittest discover -s tests\installer -p "test_*.py" -v
python -B -m unittest discover -s tests\release -p "test_*.py" -v
```

Bundle rendering checks need a separately provisioned renderer:

```powershell
$env:PANDOC = 'PATH_TO_PANDOC'
python -B -m unittest discover -s tests\bundles -p "test_*.py" -v
```

HTML inspection also needs the Python/Playwright environment and browser
installation described in the [bundle tool guide](../bundles/midden-shared/tools.md).
Use that existing interpreter for frontend browser tests:

```powershell
python -B -m unittest discover -s apps\midden-ui\webtest -v
```

Those browser tests use synthetic HTTP/SSE responses. They validate frontend
mechanics, not a live model account or content quality. Missing tools,
credentials and interactive checks not performed are limitations, not passes.

## Release inputs

Release builds inject the tag version into both executables from the same source
commit. The prerelease staging tag is `v0.3.1-rc.1`; the stable tag is `v0.3.1`.
Untagged local builds need not report a release version.

Use the checked-out release tooling's `--help` for its exact packaging and
verification arguments. Build into a new, external output directory rather than
mixing artifacts or private workspace data into the checkout.

The release includes app/core payloads, the canonical bundle, bootstrap scripts,
source/version metadata and SHA-256 checksums. Archives include generated
`THIRD_PARTY_NOTICES.txt` for compiled dependencies, with Compa attribution
retained. Only reviewed public inputs belong in an archive. Source/store data,
credentials, generated operator work and local settings are not release inputs.

Site bootstraps and release assets must come from the same release. Deploying
install links before their matching assets are available is not a complete
release. Current release binaries are unsigned; checksums do not authenticate
their publisher. Preserve licenses, copyright and accompanying notices.

## Responsibility boundaries

Core tests validate exact data behavior and source safety with synthetic stores.
Installer tests validate ownership, collisions, verification, upgrades and safe
removal. Runtime/browser fixtures validate host mechanics. Bundle evaluations
require ordinary goals and actual editable/rendered artifacts.

Do not infer successful authoring from a particular sequence of tool calls.
Do not weaken retained data/safety tests when retiring an interface. See
[Acceptance](ACCEPTANCE.md) for the separate evidence required.
