# Agentic bundles

These are the canonical outcome materials for an existing AI host. The host
reads the appropriate skill, executes core and external tools, inspects their
results, writes ordinary files, and revises the actual deliverable. The operator
supplies the goal and audience, not tool names.

| Intent | Skill | Typical result |
|---|---|---|
| "What is worth explaining from this work?" / "What actually happened?" | [Investigation](investigation/SKILL.md) | Grounded opportunities or findings, with source references and limits |
| "Make this useful to someone learning the technique" / "Revise my case study" | [Article](article/SKILL.md) | An editable article, tutorial, or case study |
| "Give me a ten-minute talk for new maintainers" | [Presentation](presentation/SKILL.md) | Editable slides and self-contained HTML |
| "Continue the handbook from its existing chapters" | [Long-form](long-form/SKILL.md) | Revised Markdown chapters and an HTML reading copy or requested EPUB |

Choose from the latest human intent and existing work. A discovery request ends
with grounded choices; it does not silently become an article. If the intended
medium is consequentially ambiguous, clarify it before producing an artifact.

Each skill uses the shared [source guide](midden-shared/sources.md) and
[dependency guide](midden-shared/tools.md). Keep these resources in the sibling
`midden-shared` directory. The installer prefixes outcome directories with
`midden-`; their relative references still resolve without claiming an unrelated
`shared` directory. This is one source tree, not four copies of the common
guidance. Hosts retain their own permissions, conversation, credentials, and
model configuration.

Working files may include a brief, notes, a draft, chapters, and an output
directory. Use only those that help the task. `sources` is a portable core
collection, not a project database. Local delivery does not authorize publishing.

See the [scoped installer](../installer/README.md) for checksum-verified core
and skill installation without global configuration.

## Mechanical checks

From the repository root:

```text
python -B -m unittest discover -s tests\bundles -p "test_*.py" -v
```

Use the host platform's path separator. Set `PANDOC` to a scoped executable's
absolute path if it is not on PATH. Browser-capture tests also need the
[HTML inspection toolkit](midden-shared/html-inspection.md): run them with its
interpreter or set `HTML_INSPECTION_PYTHON` to that interpreter. Use
`PLAYWRIGHT_BROWSERS_PATH` for scoped browser binaries, or
`HTML_INSPECTION_BROWSER` for an existing Chromium-family executable.
The tests render synthetic documents with
the shipped defaults and inspect the resulting artifacts; they do not install
tools, read sessions, call models, or establish agent behavioral effectiveness.
Natural-goal host acceptance is a separate check.

Keep fixtures and examples synthetic and generic. Store local evaluations,
logs, and generated artifacts outside this repository. Test scratch uses the
operating-system temporary directory and rejects locations inside the checkout.
Real session material and identifying details do not belong in public examples
or fixtures.
