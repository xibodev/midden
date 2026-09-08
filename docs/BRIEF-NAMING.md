# `brief` — five meanings, one word

Required by the operator before any renaming. Rule applied:

> Preserve published wire names. Disambiguate semantic/domain names.

## Enumeration

| # | Current name / location | Semantic meaning | Owner | Public? | Canonical internal name |
|---|---|---|---|---|---|
| 1 | `SeedBriefFile = "brief.md"` (`internal/seed/seed.go:55`) | prose a host agent READS to understand a seed | Midden | **frozen wire** — `seed/v1` layout, stated normative to Facet | **seed brief** |
| 2 | `midden brief` (CLI verb) | deterministic handoff that lets a fresh session RESUME work | Midden | public CLI surface | **resume brief** |
| 3 | `video_brief` (content kind) | markdown PRODUCED from evidence for a video producer | Midden | public content kind, declared by `content.types` | **video brief** |
| 4 | `brief` (seed.create request field) | operator-supplied override for #1 | Midden | **frozen wire** — in the `seed.create` request schema | **seed brief override** |
| 5 | `brief.schema.json` | JSON an agent AUTHORS into a Facet project | **Facet** | Facet's input contract | not mine to name |

Four are mine. The fifth belongs to the sibling lane and is enumerated only
because the collision crosses the boundary.

## Why nothing is renamed

Every Midden occurrence is already public: two are in the frozen `seed/v1` wire
contract, one is a CLI verb users type, one is a content kind `content.types`
publishes. Under the operator's rule, all four are retained at the boundary.

So the disambiguation is **documentary, not mechanical**. There is no local-only
`brief` left to rename — which is itself the finding. The word became ambiguous
by accumulating public surfaces, and by the time the ambiguity was visible every
occurrence had been published.

## Why there is no canonical `Brief` type

The five differ in who acts and in which direction:

- #1 is READ by a consumer;
- #2 is PRODUCED to resume the producer's own work;
- #3 is PRODUCED for a third party;
- #4 is SUPPLIED by an operator as input;
- #5 is AUTHORED by an agent into another product.

A shared type would unify five verbs on the strength of a shared noun. That is
the collision, not the cure.

## The bite, recorded rather than fixed

A consumer holding both a Midden seed's `brief.md` and a Facet `brief.schema.json`
has two "briefs" in scope with no shared shape. Latent today: Facet does not parse
`video_brief` by agreement, and its schema is never applied to Midden output.

Facet will state the shape `brief.schema.json` lands in before relocating it.
