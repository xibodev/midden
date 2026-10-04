# Core commands and data contracts

Core is deterministic. It reads recorded material and performs explicit data
operations; it does not author articles, select models, certify editorial review,
or publish content.

## Session discovery

`ls`, `find`, `show`, `brief`, `doctor`, `scan` and `assay` retain their data-tool
roles. `resume` prints a command rather than launching another AI host. `usage`
reads usage already recorded by the source, not a Midden billing ledger.

Use exact source tool/session identifiers for investigation. Missing sources and
partial inventories are reported; a skipped session is not automatically related
to the selected one.

Claude discovery does not use transcript size as a usefulness threshold. Short,
identifiable sessions are included under the same scope/noise rules as larger
ones; `--all` includes sessions marked as noise. Empty files are ignored, while
nonempty transcripts that cannot be identified make the inventory partial.

## JSON output

For every command with a `--json` flag, a successful run writes exactly one JSON
document to stdout. Lists are `[]` when empty, never `null`. An empty store, an
empty scope, a search without matches or nothing to prune is a successful empty
result; `ls` fails when no source store in scope is readable at all. A failure,
including an exact session that does not exist, exits non-zero with a reason on
stderr and nothing on stdout. `collection verify` is the exception: it prints its
report and exits 1 when the collection or quotation does not verify.

`?` marks a field that may be absent.

| Command | Result |
|---|---|
| `ls` | `{sessions, total, matched, excluded_noise, offset, next_offset?, stores_read, warnings, partial}` |
| `find TEXT` | `{hits: [{session, matches, excerpt}], scanned, skipped, truncated}` |
| `show` | one session |
| `brief` | `{session, goal?, recent, last_assistant?, user_turns, total_records, truncated}`; a turn is `{index, role, text, time}` |
| `usage` | `{model?, turns, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, reasoning_tokens, aiu?, usd?, duration_ms}` |
| `doctor` | `{total, tracked_bytes, store_footprint, at_risk, dead_workspaces, live, by_tool, tool_tracked_bytes, tool_store_bytes}` |
| `scan` | `{indexed, assayed, skipped_unchanged, skipped_too_large, no_transcript, failed, bytes_assayed, reconciled, index_path}` |
| `assay` | stored totals `{sessions, assayed, bytes, signal, exhaust, artifact, bookkeeping, dup_bytes, images, clusters}`; `assayed` is 0 until `scan --assay` has run |
| `assay --live`, `--session ID` or `PREFIX` | one session's manifest, or a `(scope)` aggregate for several; each candidate's `class` is `signal`, `exhaust`, `artifact` or `bookkeeping` |
| `prune` | `[{session, plan, verification?, replaced, error?}]` |
| `archive` | `[{session, tool, bytes, target}]` |
| `ops` | `[{uid, op, tool, session_id, before, after, detail, ok, created_at}]` |

A session is `{tool, id, dir, title, repo?, created, updated, turns, bytes, live?,
noise, transcript_path?}`. Session lists (`ls`, `find`, `doctor`, `prune`) bound
titles to 160 characters, marked `[clipped]`; `show` and `brief` return the whole
title. A `find` excerpt is a single-line window around the first matching
transcript line. `read`, `search`, `assets`, `collect` and `collection` results
are described below.

Free text passes credential filtering in text and JSON output alike: session
titles, repository and live names, `find` excerpts, `brief` turns, `assay` titles
and candidate previews, and record text. A recognised credential becomes an
actionable placeholder such as `<SECRET — ask operator>`. Identifiers,
paths and numbers are unchanged. Filtering is not privacy clearance.

## Pinned reading

```text
midden read --tool TOOL --session ID --json
midden read --view VIEW_ID --record RECORD_ID --before 1 --after 1 --json
midden search "literal phrase" --view VIEW_ID --json
```

The result includes `view_id`, source identity, the observed time range, source
boundary/digest, selection information, record windows and warnings. Record IDs
are opaque, copyable source-window identities. Preserve them intact.

Records expose `id`, `source`, `kind`, `role`, `time`, `text`, `clipped`,
`redacted`, and a `reference` with source digest, ordinal and byte offsets.
Unknown timestamps remain unknown rather than fabricated.

File views pin a byte prefix; database views pin an ordered record prefix.
Further reads may observe that prefix while new records are appended. Changes
within it reject the old view. Run `read` with the exact source again to refresh
explicitly.

Reads are bounded: up to 256 selected records and 8,192 visible characters per
window. Inline record pages are limited to 16 KiB. Use smaller windows/pages or
`--out` to save a complete read result as a file. Bounds do not imply complete
semantic coverage.

Source locations normally resolve from the user profile. Material commands also
accept explicit `--copilot-root`, `--claude-root`, `--opencode-db` and
`--sources-only` for controlled stores. `--state` selects the cache location,
independently of those sources.

## Collections

```text
midden collect --view VIEW_ID --record RECORD_ID --out sources --json
midden collection inspect sources --json
midden collection read sources --offset 0 --limit 10 --json
midden collection search sources --query "literal phrase" --json
midden collection select sources --record RECORD_ID --out selected --json
midden collection merge sources-a sources-b --out combined --json
midden collection verify combined --json
midden collection export combined --format markdown --out source-notes.md
```

Repeat `--view` or `--record` for a larger explicit selection. Output paths must
be new; core does not silently overwrite existing work.

Portable collection files:

| File | Meaning |
|---|---|
| `manifest.json` | Schema, source identities and view boundaries, counts, digests, assets and limitations |
| `records.jsonl` | Selected recorded text and its provenance, distinct from host-authored interpretation |
| `assets` | Explicitly copied recorded assets, where available |

Collections support selection, combination and export without an editorial
project or recipe. Conflicting records are errors, not arbitrarily chosen
versions. Verification checks measurable integrity and references, not truth.
The supported collection data bound is 10,000 selected records / 32 MiB of record
data; split larger material deliberately.

## Assets

`assets --view VIEW_ID` lists recorded asset metadata without emitting base64
payloads; `--tool TOOL --session ID` can establish a fresh view directly.
Explicit record selection plus `--out NEW_DIRECTORY` extracts available
assets. Local paths must resolve inside supported source/workspace boundaries.
Remote references are not fetched automatically.

`collect --assets` carries available assets with explicitly selected source
records. Select, merge and directory export preserve their bytes and provenance.
Source-only JSONL/Markdown export rejects asset-bearing collections rather than
silently losing those files. Asset output parents must already exist.

An optional explicit quote check uses `collection verify PATH --record ID
--quote TEXT`. It checks supplied words, not every quoted phrase in a document.
Ambiguous IDs across source revisions must be narrowed before quote checking.

Missing, unsupported or oversized assets remain explicit omissions. An asset
reference is not proof that an image was inspected. Text credential redaction
does not sanitize image pixels.

## Source safety

Read operations never mutate source stores. `prune` and `archive`, if invoked,
are explicit maintenance commands with separate preview/execution behavior.
They are not steps in an article or presentation workflow.
