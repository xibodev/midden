# Midden 0.2.0 — Agentic CLI preview

**Recommended: install Midden into GitHub Copilot CLI, Claude Code, or OpenCode.**
The standalone browser application remains experimental.

- Script-owned interactive installation: choose hosts, personal/project scope,
  binary/state locations, optional Pandoc/D2, and PATH.
- Verified prebuilt headless downloads; Go is not required for installation.
- Ownership receipts, explicit upgrades/backups, verification, and uninstall that
  preserves recovered work and user files.
- Shared recovery skills and canonical guidance; the host owns models,
  authentication, and permissions.
- Retired the OpenMontage integration. Facet is a separately installed sister
  project for video creation.
- GitHub Actions gates publication on Go checks, native Windows/Linux/macOS
  installer tests, archive/checksum checks, and native artifact smoke tests.

## Install

Save `install.ps1` (Windows) or `install.sh` (Linux/macOS), `manifest.tsv`, and
`SHA256SUMS` together. Verify the script and manifest using
[the installation guide](https://github.com/xibodev/midden/blob/v0.2.0/docs/INSTALL.md).

Windows (PowerShell 7+): `pwsh -NoProfile -File ./install.ps1 -Version v0.2.0`

Linux/macOS: `bash ./install.sh --version v0.2.0`

Install and authenticate your CLI host first. The installer verifies the
platform archive before installing. Keep the script/manifest for lifecycle use.
Use an explicit version because preview releases are not GitHub's `latest`.

## Assets and limits

Headless and experimental standalone archives are available for Windows x64,
Linux x64/arm64, and macOS x64/arm64. `build-manifest.json` records the source
revision; `SHA256SUMS` covers all payload assets. Binaries are not OS code-signed.

Offline/native installer checks are separate from live host acceptance, which
remains in progress. Optional renderers and package-manager installation are
not bundled or certified by fixture tests. Review generated content and
provenance before publication. Standalone provider onboarding and complete
product validation remain incomplete.
