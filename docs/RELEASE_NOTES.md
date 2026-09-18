# Midden 0.2.1 — Guided CLI installation

The installer now detects your CLI hosts, selects a single detected host
automatically, and offers numbered choices when several are available. Choose
optional rendering capabilities and confirm one summary; custom locations remain
available through flags and environment variables.

- Windows PowerShell 5.1 and PowerShell 7 support; no separate shell installation
  required on Windows.
- Concise progress, retrying downloads, and actionable errors.
- Broken unselected optional tools no longer block core recovery installation.
- Refresh Windows PATH after package installation; support bash, zsh, and fish
  profile entries on Unix. Detect an existing command shadowing Midden.
- Reuse the previous installation's scope/state on repeat runs. Interactive
  upgrades explicitly show replacement with backups in the confirmation summary.
- Exact host launch commands and a first recovery prompt at completion.
- Ownership, checksums, rollback, modified-file refusal, and recovery-data
  preservation remain release gates.

## Install

Save `install.ps1` (Windows) or `install.sh` (Linux/macOS), `manifest.tsv`, and
`SHA256SUMS` together from this release. Verify checksums using the
[installation guide](https://github.com/xibodev/midden/blob/v0.2.1/docs/INSTALL.md).

Windows: `powershell -NoProfile -File ./install.ps1 -Version v0.2.1`

Linux/macOS: `bash ./install.sh --version v0.2.1`

The [website](https://xibodev.github.io/midden/#install) provides checksum-verified
one-line entry points, promoted to this version after release publication.

## Availability and validation

Headless and experimental standalone archives: Windows x64, Linux x64/arm64,
macOS x64/arm64. The recommended route is an installed, authenticated Copilot CLI,
Claude Code, or OpenCode host. Windows ARM64 is not supported by this release.

Native CI covers installer mechanics and packaged-binary smoke tests, including
Windows PowerShell 5.1. Live host acceptance and real package-manager operations
are separate from fixture tests. Standalone remains experimental. Optional
Pandoc/D2 tools are not bundled; generated content needs editorial review.
