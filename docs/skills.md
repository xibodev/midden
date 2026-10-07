# The skills

The Bundle is four skills that teach your AI agent to work from your recorded
sessions with Core. You ask for an outcome; the agent reads the skill that
fits and follows it.

| Skill | Ask for | You get |
|---|---|---|
| `midden-investigation` | What happened in some work, what is worth developing, where a draft or collection is, which evidence supports an outcome | An answer, or a short list of opportunities, each with an angle, audience, evidence, open gap and likely format. Optionally a `sources` collection. |
| `midden-article` | An article, tutorial, case study or explanatory post, or a revision of one | A Markdown file, or another format you ask for, with citations to session records |
| `midden-presentation` | A talk, slide deck or briefing | `slides.md` and self-contained HTML slides; PDF or PowerPoint when you ask; screenshots of the rendered slides |
| `midden-long-form` | A handbook, book, multi-chapter guide or long report, written a part at a time | Markdown chapters, an outline and notes, and an HTML reading copy, or EPUB when you ask |

A fifth folder, `midden-shared`, holds the guidance the four share: how to
read and cite sources (`sources.md`), which tools each format needs
(`dependencies.md`), and how to check rendered HTML (`html-inspection.md`
and `inspect_html.py`). The skills link to it, so keep it beside them.

## Install

The installer copies the skills, unchanged, into your agent's skills folder:
`~/.claude/skills`, `~/.copilot/skills` or `~/.agents/skills`, or a
project's folder. See [the skills for your agent](install.md#the-skills-for-your-agent).
The skills run `midden`, so the installer installs Core with them.

The App's assistant has the same skills built in; you don't install them
for it.

## Examples

- "Investigate my sessions on this repository from the last two weeks and
  suggest what is worth writing up."
- "Which sessions show how we fixed the Windows installer?"
- "Write a tutorial from the sessions where I set up the release pipeline."
- "Turn the investigation into a 10-minute talk as HTML slides."
- "Start a handbook about our release process; write the first chapter."

## How they work

- **From the records.** The skills list, read and search sessions with
  `midden`, read on past an opening sample to the later outcome and
  corrections, and say what they read, what was clipped and what is still
  unknown, instead of filling gaps.
- **Evidence beside the work.** Records that support the work are collected
  into a `sources` collection next to it (`manifest.json`, `records.jsonl`,
  `assets/`), and citations keep the exact record IDs. A collection is
  added to and merged, not deleted and recreated.
- **Reported, not proven.** A session saying that checks passed is
  reported as such; it isn't verified by being read.
- **Files you can edit.** The result is ordinary files where you asked for
  them, revised in place rather than copied. The skills open the saved
  files and give you their paths.
- **Checked output.** Rendered HTML is captured and its screenshots are
  looked at before the layout is described as checked. Anything not checked
  is labelled.
- **Your agent's rules.** Approvals and permissions are your agent's. The
  skills never install, download, upload or publish without your say-so.
  Session text is treated as data, not instructions.
- **Care with what you share.** Core hides recognisable credentials, but
  the skills still keep secrets and unneeded personal details out of what
  they write.

## Tools for each format

| Outcome | Needs |
|---|---|
| Investigation, collections | Core (`midden`) |
| Markdown article, slide source, chapters | Your agent's file editing; Core for sources |
| HTML slides | Pandoc 3.1 or later |
| Long-form HTML or EPUB | Pandoc 3.1 or later |
| Screenshots of HTML | A Chromium-family browser, such as Edge or Chrome. Python with Playwright 1.48 or later adds a structural report. |
| PDF | A browser that can print to PDF |
| PowerPoint | Pandoc's `pptx` writer, and a viewer or converter to check the result |

The skills check for a tool before promising a format. When one is missing
they say which format is affected and what they can still deliver, and they
don't install it without your say-so. In the App, Pandoc 3.12 is already
available to the assistant.

## Templates

| Template | What it makes |
|---|---|
| `midden-presentation/templates/html.yaml` | Pandoc dzslides: one slide per level-one heading, no automatic cover slide, everything embedded in one file, system fonts, and one slide per page when printed to PDF |
| `midden-long-form/templates/html.yaml` | One HTML file with a table of contents and numbered sections, everything embedded |
| `midden-long-form/templates/epub.yaml` | EPUB 3 with a table of contents and numbered sections |

All three stop on any Pandoc warning, so a missing image fails the render
instead of being dropped.
