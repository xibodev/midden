# Outcome-scoped dependencies

Use the host's existing file and shell tools. Check the specific capability
before promising its output. Report a missing dependency, the affected format,
and what can still be delivered. Installation, downloads, uploads, and
publishing require the operator's authority; do not silently perform them.

| Outcome | Required capability |
|---|---|
| Session investigation or collection work | Core `midden` CLI with the commands in the [source guide](sources.md) |
| Markdown article, slide source, or chapters | Host file editing; core only when using its sources |
| Self-contained HTML slides | Pandoc 3.1+ with the `dzslides` writer and `--embed-resources` |
| Long-form HTML / EPUB | Pandoc 3.1+ with `html5` / `epub3` respectively |
| Reusable HTML screenshot capture | Python Playwright 1.48+ in an available interpreter and a Chromium-family browser; see [HTML inspection](html-inspection.md) |
| PDF | A host-accessible browser with local print-to-PDF, or another explicitly available renderer |
| PowerPoint | Pandoc's `pptx` writer plus a viewer or converter capable of checking the requested presentation |

`pandoc --version` and `pandoc --list-output-formats` establish the installed
capabilities. A scoped Pandoc executable is sufficient; no system-wide install,
TeX distribution, JavaScript package stack, or new AI runtime is required for
the default HTML and EPUB outcomes.

Run Pandoc from the content directory, with an absolute path to the selected
bundle defaults file and explicit input/output paths. The slide defaults resolve
their CSS relative to themselves. Long-form input order is supplied explicitly
by the host, not a wildcard. The defaults do not select or overwrite a draft.

Use reviewed local media and system fonts. The slide CSS avoids the stock
dzslides template's remote font; Pandoc embeds CSS, media, and its slide runtime.
HTML defaults embed resources; EPUB packages local media. Retain attribution
links as links, not remotely loaded images, scripts, stylesheets, or fonts.
Avoid remote math/rendering services. Capture generated HTML offline and inspect
its resource findings before describing it as self-contained. Open the actual
screenshots before claiming visual review; source CSS or a structural report
alone does not show how the result looks.

Treat a nonzero exit or rendering warning as unfinished work. Inspect the
artifact itself, not just a filename or successful process. Revise the editable
source and rerender the same agreed target. If the requested renderer or viewer
is unavailable, label a saved source draft or unchecked conversion accurately;
another format is not fulfillment of a specifically requested PDF or PowerPoint.
