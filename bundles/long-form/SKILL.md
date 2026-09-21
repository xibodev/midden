---
name: midden-long-form
description: Use when an operator wants a handbook, book, multi-chapter guide, sustained report, or incremental continuation or revision of existing long-form work.
---

# Grow a coherent work in useful increments

Deliver editable Markdown chapters and a readable edition. Default to a
self-contained HTML reading copy; use EPUB when requested. Honor narrower
chapter-only requests. A partial increment is not a finished book.

Read the shared [source guide](../midden-shared/sources.md) and
[dependency guide](../midden-shared/tools.md).

## Resume the real work

Inspect the named folder's brief, outline, chapters, notes, and source
collections. Infer current coverage from actual files, not a project catalog.
Read neighboring chapters and the existing terminology before changing a
chapter. Preserve useful human edits and revise the agreed files.

Keep chapter order, coverage, and the next increment in an outline; evidence gaps
in notes. These host-authored aids stay separate from the raw `sources`
collection, without a special project format.

## Develop the chosen increment

Choose scope from the human goal and available evidence. Run targeted core
reads or collection searches within the source guide's bounds, including later
outcomes and corrections. Preserve existing collections when adding evidence.
Use exact-locator source notes or explicit collection pointers; keep opaque IDs
and digests intact. A participant's success report remains reported, not an
independently executed check. Preserve planned, attempted, reported, and verified
scope rather than inventing an ending.

Write substantive prose and useful examples, not only an outline. Reproduce
examples safely where appropriate and distinguish observed output from
untested instructions. Keep vocabulary, cross-references, audience assumptions,
and level of detail consistent with neighboring chapters.

When evidence changes, revise affected passages and notes, and state which other
chapters need reconciliation. Save this increment before expanding scope.

## Assemble and inspect the reading copy

Keep title and language metadata with the manuscript. Supply chapter paths in
the intended order, never by an unchecked wildcard. From the content directory,
replace `HTML_DEFAULTS` or `EPUB_DEFAULTS` with the quoted absolute path to
[templates/html.yaml](templates/html.yaml) or
[templates/epub.yaml](templates/epub.yaml):

```text
pandoc --defaults HTML_DEFAULTS 01-introduction.md 02-method.md --output book.html
pandoc --defaults EPUB_DEFAULTS 01-introduction.md 02-method.md --output book.epub
```

Run the requested conversion, inspect the result, and open the actual artifact.
Check chapter order, navigation, cross-references, code, citations, and local
media offline. For EPUB, inspect its packaged resources and reading order too;
a ZIP file alone does not establish a readable edition. If no EPUB viewer is
available, distinguish package checks from visual review.

## Deliver a workspace increment

The chosen working-project/workspace directory holds the edited chapters,
requested editions, and durable factual source collection or complete pointers.
When delivering HTML, fill a **visual-review slot**: rendered screenshots actually
viewed, their report paths and coverage, or **not visually reviewed** with the
missing capability or reason. Use the [HTML inspector](../midden-shared/html-inspection.md) in document
mode, then open its images; structural checks alone do not fill this slot.

Revise and rerender the same target. Return workspace paths, changes, and remaining
gaps. Session temp is scratch, not the sole final home. Keep publication separate.
