# Midden 0.2.2 — Interactive terminal setup

Midden now opens a script-owned terminal wizard with a branded header, three
visible stages, and keyboard-selectable choices.

- **Quick start** uses sensible defaults; **Custom setup** offers project scope,
  binary/data directories, and PATH preferences interactively.
- Up/Down moves the selection, Enter confirms, and Q cancels.
- Choose a detected CLI or all hosts, then core recovery or optional Pandoc/D2
  capabilities. Review one complete install plan before changes.
- Numbered plain-text fallback for redirected or limited terminals; `MIDDEN_PLAIN=1`
  forces plain mode and `NO_COLOR` disables colour.
- Release CI exercises actual Unix PTY and Windows ConPTY keyboard input in
  addition to lifecycle, checksum, ownership, cancellation, and artifact checks.

Installation remains owned by PowerShell/Bash scripts. The Go application owns
recovery and content operations; no extra terminal UI runtime is installed.

## Install

The [website](https://xibodev.github.io/midden/#install) provides the one-line
entry points, promoted after this release is published. Install and authenticate
Copilot CLI, Claude Code, or OpenCode first.

For manual download, save the platform installer, `manifest.tsv`, and
`SHA256SUMS` from this release and follow the
[installation guide](https://github.com/xibodev/midden/blob/v0.2.2/docs/INSTALL.md).

Windows: `powershell -NoProfile -File ./install.ps1 -Version v0.2.2`

Linux/macOS: `bash ./install.sh --version v0.2.2`

Windows PowerShell 5.1/7 and Bash 3.2+ are supported. Assets cover Windows x64,
Linux x64/arm64, and macOS x64/arm64, in headless and experimental standalone
variants. Live agent acceptance and real package-manager operations remain
separate from fixture tests. Review generated content before publication.
