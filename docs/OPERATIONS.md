# Midden canonical Operation inventory

Layer 1 of Architecture v2.1. Derived by SEMANTIC CONTRACT, per operator
ruling 10: the six module capabilities, twenty-nine CLI verbs and nineteen
content types are EVIDENCE, not the inventory. A delivery surface must not
become Layer-1 product truth.

Status: derived, not yet declared in code. No wire change. `xibodev.module/v1`
is immutable legacy and carries none of this.

## How these were derived

A verb is an Operation only if it names a distinct semantic transformation that
the product owns. Two tests, both mechanical:

1. **Does it transform product material?** `version`, `help`, `ui`, `mcp`,
   `module`, `install`, `doctor` and `start` do not. They are Layer-3 adapters
   and Layer-4 shells wearing verb costumes.
2. **Do multiple faces converge on one implementation?** Measured by import
   graph: `internal/reclaim`, `internal/refinery` and `internal/assay` are each
   reached by three faces (CLI, module, web). Convergence is evidence that the
   semantic unit is the package, not the verb that calls it.

## The inventory

| Operation | Semantic contract | Deterministic | may_charge |
|---|---|---|---|
| `assay_session` | session scope -> evidence manifest | yes | false |
| `mine_evidence` | session scope -> stored evidence set | no (model) | true |
| `produce_content` | evidence set + kind -> written output | per kind | conditional on `kind` |
| `build_seed` | session scope + goal -> portable seed bundle | yes | false |

Four Operations. Six module capabilities, twenty-nine CLI verbs and nineteen
content types map ONTO these; none of those registries is itself the truth.

`sessions.list` and `content.types` are not Operations: they are registry reads
that report what exists, transforming no product material.

`produce_content` is ONE Operation whose chargeability depends on its `kind`
argument, not nineteen Operations. Splitting it would make a delivery surface
into Layer-1 truth, which ruling 10 forbids, and RFC v2 §3a exists to express
exactly this shape.

## Requirement strengths

No Operation declares a MANDATORY requirement for approval-for-irreversibility,
budget ceiling, or durable resume. Derived rather than assumed:

- **No irreversibility.** Zero capabilities declare `ExternalWrites`; every
  write lands inside the granted root. `produce_content` writes a fixed name per
  kind, so re-running overwrites its own output rather than accumulating.
- **No mandatory budget.** Recorded as `preferred`. No Operation's semantics
  require a ceiling; wanting one is not requiring one.
- **No mandatory resume.** `mine_evidence` was the only candidate, because it
  spends per session. Nugget identity now derives from the evidence, so
  re-mining lands on the same row instead of inserting duplicates: re-execution
  is safe, and an Operation whose re-execution is safe needs no resume facility.

That last line is operator ruling 2 working as intended. The requirement was a
defect in this product's idempotency wearing a requirement's clothes, and
repairing it locally dissolved a facility three lanes had been discussing.

## Artifact kinds

Per RFC v2 §7, and corrected against this tree rather than against how the
formats look:

| Emitted | Kind | Why |
|---|---|---|
| `text/markdown`, `text/plain`, `text/x-diff` | `text` | no validation contract |
| `application/x-ndjson`, `text/tab-separated-values` | `text` TODAY | structured, but NO validator exists |
| `application/json` | `text` TODAY | see below |

No output is `document` today. `xibodev.midden.content.output/v1` exists and is
a real JSON Schema, but its properties are `kind`, `format`, `review` -- it
validates the artifact RECORD, not the bytes of the file. Declaring the json
output `document` on the strength of that schema would name a validator that
does not validate the thing being declared.

RFC v2 §9 requires a `document` to name a validator that validates the
ARTIFACT'S OWN CONTENT, not a record about it, and to fail conformance
otherwise. A name-resolution check passes the declaration above; the intent
fails. TSV, NDJSON and JSON become `document` only when a
validator for their CONTENT is written and named.

A shape that looks validatable is not a validation contract, and neither is a
schema that validates something adjacent.
