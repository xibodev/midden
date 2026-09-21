# Troubleshooting

| Symptom | Check |
|---|---|
| No session matched | Confirm the exact source tool/ID, source roots and inventory warnings. Do not widen silently. |
| Some sources skipped | Read the reason. Inventory omissions do not prove those sessions are related to your target. |
| Source view changed | Appends should be safe. Earlier edits, truncation or record reordering require an explicit new view. |
| Missing view | Check `MIDDEN_HOME`/`--state`. Portable collections do not depend on that cache. |
| JSON result too large | Narrow `--limit`/`--chars`, page records, or save the complete result with `--out` where supported. |
| Asset unavailable | Distinguish a reference, embedded data, missing file, external URL and an unsafe location. Missing is not copied. |
| Existing output path | Choose a new destination. Core collection/export does not silently overwrite user work. |
| Draft not found | Inspect the working directory and its files. Do not equate an empty index with no drafts. |
| Rendering unavailable | Follow the selected bundle's external-tool prerequisites. Report missing dependencies explicitly. |
| Old module/Studio command rejected | This interface is retired. Use the core CLI and separate bundle; see Migration. |

The core cannot prove a narrative is true merely because its source references
are valid. Revisit the source and involve the operator when meaning is uncertain.
