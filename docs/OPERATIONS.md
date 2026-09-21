# Operation ownership

Core operations retrieve or manipulate session material under explicit scope.
They do not perform AI interpretation or require an editorial lifecycle.

| Operation | Effect |
|---|---|
| Inventory, show, brief, assay, recorded usage | Read source data and report facts/limits |
| Scan | Update the separate derived core cache |
| Read/search | Read a pinned source prefix and retain bounded source windows |
| Collect/select/merge/export | Create new portable material at an explicit destination |
| Asset extraction | Copy available selected assets under bounded confinement |
| Collection verification | Check structural/reference/hash correspondence |
| Explicit quote check | Check supplied words against a selected source window, not narrative truth |
| Legacy export | Read old working state and copy recoverable data/files without migrating it |
| Prune/archive | Separate explicit source-maintenance operations; not content-production steps |

The bundle and host agent own investigation strategy, audience, interpretation,
authoring, external rendering, inspection and revision. File edits and publication
use the host's normal interaction and permissions.

Read [Core](CORE.md) for concrete command contracts and
[Architecture](ARCHITECTURE.md) for the layer boundary.
