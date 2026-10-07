# Development and releases

## Repository

| Path | What it holds |
|---|---|
| `cmd/midden/`, `internal/` | Core, the `midden` command (root Go module `github.com/xibodev/midden`). |
| `apps/midden-ui/` | The App, a separate Go module. `web/` is the browser UI (plain ES modules, embedded in the binary); `webtest/` tests it with Playwright against a mock server. |
| `bundles/` | The skills: `investigation`, `article`, `presentation`, `long-form` and `midden-shared`, installed as `midden-*`. |
| `install.ps1`, `install.sh` | The installers. |
| `scripts/` | Release tooling: `release_contract.py` (targets, pins, manifest and notices), `build-release.py`, `verify-release.py`, `smoke-bootstrap.py`, `fetch-compa.py`, `stage-site.py`. |
| `tests/` | `release/` (release tooling), `bootstrap/` (installers), `bundles/` (rendering and HTML inspection). |
| `site/` | The website page, published by `pages.yml`. |
| `docs/` | This documentation. |
| `licenses/` | License texts the release notices need. |

## Toolchain

- **Go** 1.26.6, the version in `apps/midden-ui/go.mod`, which CI uses for
  both modules. Builds need no cgo.
- **Python** 3.12 with only the standard library, for the scripts and tests.
  Run it with `-B`, as CI does: entry points must not write bytecode.
- **Playwright** 1.63.0 with Chromium, for the App's web tests and the
  bundle tests: `python -m pip install playwright==1.63.0` and
  `python -m playwright install --with-deps chromium --only-shell`.
- **Pandoc** 3.1 or later, for the bundle tests.
- **Git** on PATH.

Set `GOWORK=off` when you work in `apps/midden-ui`, as CI and the scripts
do. All Go modules come from the public Go proxy.

## Build

```sh
go build -trimpath -o midden ./cmd/midden      # Core; make build does the same
cd apps/midden-ui && GOWORK=off go build -trimpath -o midden-ui .
```

`midden-ui` expects `midden`, `skills/` and `app/compa-kernel` beside it;
otherwise pass `--core`, `--skills` and `--kernel`, and use `--data` to keep
test data away from your own.

To get a runnable App for this machine, with Core, the skills and the pinned
kernel:

```sh
python -B apps/midden-ui/package.py --out ../midden-app-test
```

Start it with the `start.ps1` or `start.sh` in that folder. `--downloads DIR`
keeps the downloaded Compa archives between runs.

## Test

| Suite | Command |
|---|---|
| Core | `go test ./... -count=1` and `go vet ./...` |
| App | in `apps/midden-ui`, with `GOWORK=off`: `go test ./... -count=1 -timeout=600s` and `go vet ./...` |
| App with the real kernel | set `MIDDEN_TEST_KERNEL` to a `compa-kernel` from `python -B scripts/fetch-compa.py --out DIR` (it prints the path first), then run the App tests |
| App web pages | `python -B -m unittest discover -s apps/midden-ui/webtest` |
| Release tooling | `python -B -m unittest discover -s tests/release -p "test_*.py"` |
| Windows installer | `python -B -m unittest discover -s tests/bootstrap -p test_powershell.py` |
| macOS and Linux installer | `python -B -m unittest discover -s tests/bootstrap -p test_unix.py` |
| Skills rendering | `python -B -m unittest discover -s tests/bundles -p "test_*.py"` |

The installer tests use synthetic releases and isolated profiles. The test
suites need no network; the build scripts download Compa, and
`smoke-bootstrap.py` downloads Pandoc.

CI also checks that Core depends only on approved modules (the list is in
`.github/workflows/validate.yml`), and that builds leave the working tree
unchanged.

## Release builds

```sh
python -B scripts/build-release.py --out DIR [--targets windows/amd64 ...] [--version X.Y.Z] [--downloads DIR]
python -B scripts/verify-release.py --directory DIR --commit SHA [--tag vX.Y.Z] [--smoke]
python -B scripts/smoke-bootstrap.py --directory DIR
```

- `build-release.py` needs a clean checkout and packages committed files,
  not the working tree. It builds Core, the App and the skills for the six
  targets (Windows, macOS and Linux on amd64 and arm64), stamps the version
  (default: the one in `internal/core/identity.go`), and writes the archives,
  both installers, `build-manifest.json`, `manifest.tsv` and `SHA256SUMS`.
- `verify-release.py` checks every file, checksum, pin and notice. With
  `--smoke` it also unpacks this machine's products into an isolated
  profile, runs Core, and starts the App and its kernel. It needs `go` on
  PATH.
- `smoke-bootstrap.py` installs, verifies, upgrades and removes each mode
  with the release's own installers, in a disposable profile.

## Releasing

1. Merge feature branches into `staging`. CI (`ci.yml`) runs on every push.
2. Dry run: run the Release workflow on `staging` with the version and
   `publish` off. It builds all six targets, verifies them, and runs both
   smoke scripts on Windows, macOS and Linux, on x64 and Arm64.
3. Merge `staging` into `main` with a commit titled "Midden X.Y.Z: ...".
4. Push an annotated tag `vX.Y.Z` on that commit. The Release workflow runs
   again and publishes the GitHub release with generated notes; a version
   with a `-` suffix is published as a prerelease.
5. For a stable release, publishing starts the Pages workflow, which puts
   the release's installers on the website.

Versions are `MAJOR.MINOR.PATCH` with an optional `-prerelease`, and the tag
adds a `v`. Source builds report the development version in
`internal/core/identity.go` and `apps/midden-ui/startup.go`; release builds
stamp the release version.

## Website

`site/` holds the page published at <https://xibodev.github.io/midden/>.
`pages.yml` runs when `site/` changes on `main`, after each stable release,
and on demand. It downloads the latest stable release's installers, checks
them against the release's checksums, and `stage-site.py` puts them beside
the page from `main`, filling in the release version. The page is published
only if that release's commit is part of `main`.

## Pins

- **Compa:** `COMPA_VERSION`, `COMPA_ARCHIVES` (archive and kernel
  checksums for each target) and `COMPA_TEXTS` in `scripts/release_contract.py`.
  To move to another Compa release, update them together, update the
  version the App's tests expect (`apps/midden-ui/kernel_test.go`), and run
  the App tests with that kernel (`MIDDEN_TEST_KERNEL`) and a release dry
  run. Compa is not a Go dependency, so `go.mod` doesn't change.
- **Pandoc:** `PANDOC_VERSION` and the archives in `release_contract.py`,
  with `tests/release/test_manifest.py` and the installer test fixtures.

Release notices are generated at build time from the compiled modules,
including the modules inside `compa-kernel`; none are committed. See
[NOTICE](../NOTICE).

## Conventions

- Builds and tests must not change tracked files, and scratch output goes
  outside the checkout.
- Line endings are LF, except `*.ps1`, which is CRLF.
- Never commit real session data; tests use synthetic sessions.
- A new skill folder needs changes in `release_contract.py`, both
  installers and the installer tests.
