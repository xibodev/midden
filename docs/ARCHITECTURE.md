# Core and outcome bundles

Midden is not an agent. Its two deliverables have a one-way dependency:

```text
Human operator <-> existing AI CLI
                         |
                  follows the bundle
                         |
               executes core + host/external tools
                         |
                ordinary working files
```

## Core

The default Go binary contains deterministic source adapters, normalized session
records, scoped discovery, stable source views, measurements, bounded reads,
asset handling, collection transforms and export. It never invokes an AI model.

`internal/material` owns source views and portable collections. It depends on
the source adapters, data types and explicit filesystem operations, not module
envelopes or an editorial lifecycle. `internal/index` is a derived cache;
`core-index.db` is separate from old working data. `internal/legacy` provides
explicit read-only recovery of old tables and files.

The ordinary CLI is the reference interface. A transport adapter, if added later,
must invoke these operations without introducing a second lifecycle or store.
The prior module/runtime/Studio interfaces are not active compatibility layers.

## Bundle

Canonical materials under `bundles` describe investigation, articles,
presentations and incremental long-form work. The host agent reasons, creates
files, runs tools, inspects results and revises with the operator.

The bundle does not execute an AI loop. Helpers perform concrete reusable
mechanics; external rendering belongs to existing tools such as Pandoc and the
host's browser. The operator supplies intent and judgment, not API choreography.

## Data and working surface

Source records and available assets remain distinguishable from interpretations
and authored outputs. A portable collection contains a versioned manifest,
selected record data, asset metadata/bytes and integrity information. Source
views detect earlier changes while tolerating appends beyond their boundary.

Briefs, notes, drafts, chapters and rendered outputs are ordinary files. Their
existence does not depend on an editorial project table. Revisions should make
the current deliverable unambiguous.

Credential redaction is not privacy review. References prove origin, not
semantic truth. The host controls permissions and interaction; core enforces
source scope, read-only behavior, bounds and safe explicit destinations.

## Independent acceptance

Core tests establish deterministic correctness and source safety. Bundle
acceptance establishes useful outcomes from ordinary requests, with actual
editable/rendered artifacts. Neither result substitutes for the other.

Public fixtures are synthetic. Packaging includes only reviewed product
materials; private evaluations and session history remain outside the repository.
