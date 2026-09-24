# Installation

Core and the outcome bundle are separate artifacts. Use a matching pair from a
trusted release or staging build. The core archive contains only the executable,
license and core documentation.

The core runs directly; no AI host, Python runtime, model configuration or
special headless build is required.

Bundle installation needs Python 3.9+ and filesystem hard-link support. It
registers canonical skills/resources for the selected host, copies a
checksum-verified core binary to a scoped location, and records ownership for
verification, upgrade and removal. It does not alter global PATH, models, MCP
configuration, permissions or source data.

From a reviewed source checkout or a verified, extracted bundle artifact, point
at the local build directory containing `build-manifest.json`, `SHA256SUMS` and
the native core plus universal bundle archives. The project must already exist:

```powershell
.\install.ps1 -DistributionDir 'C:\midden-release' -ProjectDir 'C:\work\example' -DryRun
.\install.ps1 -DistributionDir 'C:\midden-release' -ProjectDir 'C:\work\example'
```

This selects the matching native pair, verifies archive and binary digests,
extracts into external temporary storage and checks the core's reported version
before applying the existing receipt-owned installation. It does not download
anything or require manual core extraction, binary hash lookup or bundle-root
selection. The launcher itself must already be trusted; there is no automatic
bootstrap from unopened archives.

`-DryRun` (`--dry-run` / `--plan`) prints a read-only JSON plan with bindings,
exact destinations/actions and dependencies. It checks inputs and ownership but
does not extract, write files or execute the core. Executable compatibility and
filesystem writability/hard-link support remain unprobed.

**Integrity against local checksums is not cryptographic authenticity.** A
payload and replaced local checksums can agree. This mode does not authenticate
the builder or validate signatures.

The existing explicit route remains available for separately extracted inputs:

```powershell
.\install.ps1 -CoreBinary CORE_BINARY -CoreSha256 BINARY_SHA256 -BundleRoot BUNDLE_ROOT -ProjectDir PROJECT
```

`BINARY_SHA256` is the extracted executable's trusted digest, not the archive's.
Project scope and Copilot CLI are the defaults. Claude and generic `.agents`
destinations are also available as file layouts; actual host discovery is
validated separately.

See the authoritative [installer guide](../installer/README.md) for exact options,
PowerShell/Bash invocation, distribution limits, custom paths, receipt
verification, upgrades, uninstallation and recovery behavior. Modified owned
files block upgrade/removal rather than losing edits; unowned files and state
remain untouched. No source archives are needed to verify or uninstall.

Start a fresh host conversation in the work directory and state a normal goal.
Do not enable unrelated global instructions or providers just to use Midden.
External renderers are needed only for the corresponding bundle outcomes.

For an old installation, first read [Migration](MIGRATION.md). Old receipts and
private working data are not automatically removed or rewritten.
