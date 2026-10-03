# Release status

**Midden 0.3: deterministic core, canonical bundle and a separate Compa-backed app.**

The core CLI and separately installable agentic bundle remain independent.
Use the bundle in an existing authenticated AI CLI, or use the Midden app with
a supported API-key/local compatible model connection. The host AI executes
outcome playbooks; no AI runtime or editorial approval engine is embedded in core.

Release automation builds core, bundle and UI packages from one source revision
and injects the requested release version. The public website and download
bootstraps are deployed only with matching stable-release assets.

GitHub Actions is the source of build/installer/package evidence for each
revision. Core unit/integration tests are not evidence that a bundle produced
good content. Bundle acceptance must inspect actual artifacts and distinguish
model-reported outcomes from demonstrated ones.

Source formats remain external/private implementation details and can change.
Credential filtering does not imply privacy clearance. Optional rendering
depends on the tools named by the selected bundle.

See [Core](docs/CORE.md), [Bundles](bundles/README.md) and
[Acceptance](docs/ACCEPTANCE.md).
