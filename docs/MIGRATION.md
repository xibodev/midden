# Moving to the core/bundle architecture

This is a breaking change to unreleased integration and editorial interfaces,
not an in-place rewrite of user history.

## Preserved

- Original CLI session stores: investigation remains read-only.
- Existing drafts, exports, source packs and working directories.
- Legacy `index.db`: the core creates a separate `core-index.db`.
- Existing feature/release history: old interfaces remain recoverable from their
  matching revision, not through a permanent compatibility engine.

Back up the old state directory before changing an installation. Do not delete
it merely because the new core can read the original session stores.

## Explicit legacy export

```text
midden legacy export --from OLD_STATE --out NEW_DIRECTORY --json
```

The output directory must not exist; its parent must already exist, and the
destination must resolve outside the old store. The operation opens the old
database read-only, writes known working tables as JSONL, and copies available
recorded artifacts confined to the old state root.

Missing/outside artifacts are reported, not silently treated as recovered.
Per-file, aggregate-asset and per-table bounds prevent an unbounded export.
Review the report and actual files before retiring the old installation.

Legacy approval/status fields are historical data. Exporting them does not
create a new human approval, certify content, or publish anything.

## Retired surfaces

| Old surface | New ownership |
|---|---|
| `module` invocation/descriptors/grants | Ordinary core commands and direct data results |
| Mandatory `agent` workflow protocol | Host follows the separately installed bundle |
| `recipes`, editorial project and approval APIs | Working files, source collections and host/operator decisions |
| Core model execution (`ask`, `reclaim`, `refine`, provider runtime) | Existing AI CLI |
| Studio/kernel integration | Outside the active core release |
| Core article/deck type enum | Outcome guidance and tools in the bundle |
| Duplicated embedded skills | Canonical files under `bundles` |
| Legacy workflow MCP | Not required by the CLI-first architecture |

Useful deterministic source behavior was extracted rather than removed with
the old transport. `read`, `search`, `collect`, collection manipulation and
asset operations can be used directly without installing an AI host.

## Working files

Keep selected source material separate from interpretations and drafts. A small
workspace may contain `sources`, `brief.md`, `notes.md`, `draft.md` and `output`.
Use only what the outcome needs. A saved draft can be found as a file without a
project-table entry.
