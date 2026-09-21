# Installation

Core and the outcome bundle are separate artifacts. Use a matching pair from a
verified release or staging build. Verify the archive checksums before extracting.
The core archive contains only the executable, license and core documentation.

The core runs directly; no AI host, Python runtime, model configuration or
special headless build is required.

Bundle installation needs Python 3.9+ and filesystem hard-link support. It
registers canonical skills/resources for the selected host, copies a
checksum-verified core binary to a scoped location, and records ownership for
verification, upgrade and removal. It does not alter global PATH, models, MCP
configuration, permissions or source data.

From the extracted bundle artifact:

```powershell
.\install.ps1 -CoreBinary CORE_BINARY -CoreSha256 BINARY_SHA256 -BundleRoot BUNDLE_ROOT -ProjectDir PROJECT
```

`BINARY_SHA256` is the extracted executable's trusted digest, not the archive's.
Project scope and Copilot CLI are the defaults. Claude and generic `.agents`
destinations are also available as file layouts; actual host discovery is
validated separately.

See the authoritative [installer guide](../installer/README.md) for exact options,
PowerShell/Bash invocation, custom paths, receipt verification, upgrades,
uninstallation and recovery behavior.

Start a fresh host conversation in the work directory and state a normal goal.
Do not enable unrelated global instructions or providers just to use Midden.
External renderers are needed only for the corresponding bundle outcomes.

For an old installation, first read [Migration](MIGRATION.md). Old receipts and
private working data are not automatically removed or rewritten.
