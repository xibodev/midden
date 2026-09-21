---
name: midden-presentation
description: Use when an operator wants a talk, slide deck, briefing, presentation revision, or a specifically requested PDF or PowerPoint presentation from existing work or evidence.
---

# Deliver a presentation the audience can use

Deliver editable source and the requested artifact; default to self-contained
HTML. Discovery-only requests use investigation.

Read the shared [source guide](../midden-shared/sources.md) and
[dependency guide](../midden-shared/tools.md).

## Shape the talk around intent

Use the requested audience, duration, purpose, and format. Inspect existing
decks, notes, and collections; revise the agreed files.

Ground claims in core reads, including later outcomes and reversals. Map factual
slides to supporting records, not just an
illustration. Collect exact claim, outcome/context, and asset record IDs together
with `--assets`. Reuse existing collections; collect/merge additions into new
outputs rather than deleting `sources`. Source notes retain full identifiers
or explicit collection pointers. An inspected success report remains reported.
Keep notes separate from raw evidence, and open assets before selecting them.

Use one point per slide, readable text, accessible local visuals, and notes for
detail. Keep unresolved outcomes visible.

## Render the actual slides

Author `slides.md` with one level-one heading per slide. Title metadata names the
browser document; the defaults add no automatic cover. Write an opening slide
explicitly when wanted, optionally using `{.title}` styling, and count it in the
requested total. Use reviewed local media and system fonts.

From the chosen workspace delivery directory, replace `PRESENTATION_DEFAULTS` with the quoted
absolute path to [templates/html.yaml](templates/html.yaml):

```text
pandoc --defaults PRESENTATION_DEFAULTS slides.md --output slides.html
```

Use the [HTML inspector](../midden-shared/html-inspection.md) with its scoped
Playwright interpreter. Set `--expected-slides` from the request. For a five-slide
brief, replace `INSPECTOR` with the helper's absolute path and run:

```text
python INSPECTOR slides.html --mode slides --expected-slides 5 --out inspection
```

The inspector counts every rendered slide and fails a mismatch while retaining
the screenshots and report.

**Open every screenshot with an image-viewing tool.** Check readability,
clipping, visuals, citations, and the conclusion. HTML/CSS/report text alone is
not visual inspection; label layout unchecked when images cannot be viewed.
After edits, rerender the same target and capture/view a fresh set.

For requested PDF, use an available browser print capability and inspect every
page, count, clipping, and links. For requested PowerPoint, use a `pptx` writer
and inspect layout, media, count, and requested editability in a compatible
viewer or converter. An extension or ZIP signature alone is not verification.

## Deliver a workspace bundle

The chosen working-project/workspace directory holds these delivery slots:

| Slot | Artifact |
|---|---|
| Editable source | `slides.md` and any useful notes |
| Requested format | The rendered HTML, PDF, or PowerPoint file |
| Factual support | Durable source collection or complete pointers, mapped to factual slides |
| Inspection | Report, rendered screenshots, and the scope actually viewed or still unchecked |

Reopen final files there and return their paths. Session temp is scratch, not the
sole final home. Label incomplete scope or unavailable format checks. Publication
remains separate from local delivery.
