# Inspect rendered HTML, then view the images

Use [inspect_html.py](inspect_html.py) on existing, self-contained HTML that the
host is authorized to open. It uses Playwright's browser, not a new renderer.
It does not call a model, collect sources, install tools, or assess factual or
visual quality.

Use an interpreter with **Python Playwright 1.48+** and an available Chromium
browser. `python` below means that interpreter, including a scoped virtual
environment when provided. Replace `INSPECTOR` with the helper's quoted absolute
path. For a request for five total slides, set the expected count from that brief:

```text
python INSPECTOR slides.html --mode slides --expected-slides 5 --out inspection
python INSPECTOR book.html --mode document --out book-inspection
```

Slides mode supports Pandoc dzslides. It counts every actual DOM slide, including
explicit covers and titles from other templates, selects each through the runtime,
and saves a PNG for each. The bundle's presentation defaults preserve the document
title without adding a cover; write an opening slide explicitly when wanted.
`--expected-slides N` compares the full rendered count with the requested total.
A mismatch returns `2` with all captures retained. Document mode takes one
full-page screenshot; expected-slide counts apply only to slides mode.
Additional states in custom interactive decks need separate host-browser inspection.

`--browser BROWSER_EXECUTABLE` selects an existing Chromium-family executable.
Alternatively, use an already provisioned Playwright browser; set
`PLAYWRIGHT_BROWSERS_PATH` for a scoped browser installation. Missing packages or
browsers are explicit failures. Probe availability first; provision dependencies
only with authority, into a scoped environment, never through global PATH or
profile changes.

Choose a **new output directory in the working delivery workspace**. The helper
preserves the input and refuses an existing output directory. It loads the HTML
bytes into an isolated blank page,
blocks network forwarding (including WebSockets), and blocks service workers.
External or nonembedded local resources may therefore be missing: the default
deliverable should carry its own CSS, runtime, and media.

`report.json` records the input hash, requested/actual counts, screenshot filenames,
possible overflow, missing images, browser errors, and attempted network access.
Exit `0` means capture completed without those structural findings; `2` means
findings were captured; `1` means incomplete capture or a dependency/input error.
The report always distinguishes capture from visual review.

**Open every saved PNG with an image-viewing tool**, then judge readability,
clipping, composition, and whether the visuals support the intended message.
Read the report too, but do not substitute it for seeing the images. If image
viewing is unavailable, state that layout remains unchecked. Revise the same
editable source/output, then capture and view a fresh set after changes.
