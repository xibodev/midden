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

The derived `core-index.db` does not migrate old editorial state. See
[Migration](MIGRATION.md) for explicit legacy recovery.
