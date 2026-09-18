# Release status

**Midden 0.2.3 — preview**

The recommended delivery is the recovery bundle installed into GitHub Copilot
CLI, Claude Code, or OpenCode by `install.ps1` / `install.sh`. The standalone
browser application is experimental. Both forms share recovery operations,
evidence, content composition, review, export, and provenance.

The script installers own download verification, host skill registration,
optional dependencies, PATH, upgrade, and uninstall. Go owns product behavior.
Facet is a separately installed sister project; the old OpenMontage integration
has been retired without deleting users' legacy files.

## Release evidence

[GitHub Actions](https://github.com/xibodev/midden/actions) is the authoritative
record for each release revision: Go tests/vet, native Windows/Linux/macOS
installer lifecycle tests, archive/checksum verification, and native binary
smoke tests. See [Development](docs/DEVELOPMENT.md) for the release contract.

Offline installer tests do not establish live agent acceptance, provider login,
model quality, or functional access to every optional package manager. Those
checks remain separate. Generated work needs evidence and editorial review.

## Known limits

- Live acceptance across all three agentic CLI hosts is still in progress.
- Standalone provider onboarding and complete product validation are incomplete.
- Optional Pandoc/D2 capabilities depend on separately installed tools.
- Redaction reduces risk; it does not prove all private content is absent.
- Reliable live-session detection remains Claude-only.
- Recovery reads source stores; explicit archive/cleanup is a separate mutation.

Start with [Install](docs/INSTALL.md) and [Getting started](docs/GETTING_STARTED.md).
