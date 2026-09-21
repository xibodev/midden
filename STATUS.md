# Release status

**Midden 0.3.0-dev: core/bundle architecture transition.**

The active product is a deterministic core CLI and a separately installable
agentic bundle. The host AI executes outcome playbooks; no AI runtime or
editorial approval engine is embedded in core.

The previous module, agent workflow API, Studio and provider integration are
retired from the active build. Existing files and legacy state are preserved;
recovery is explicit rather than an automatic migration.

GitHub Actions is the source of build/installer/package evidence for each
revision. Core unit/integration tests are not evidence that a bundle produced
good content. Bundle acceptance must inspect actual artifacts and distinguish
model-reported outcomes from demonstrated ones.

Source formats remain external/private implementation details and can change.
Credential filtering does not imply privacy clearance. Optional rendering
depends on the tools named by the selected bundle.

See [Core](docs/CORE.md), [Migration](docs/MIGRATION.md),
[Bundles](bundles/README.md) and [Acceptance](docs/ACCEPTANCE.md).
