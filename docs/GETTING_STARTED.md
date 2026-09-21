# Getting started

Use the core directly when you need session data. Add an outcome bundle when
you want an existing AI CLI to investigate and create something from it.

1. Build or obtain the core executable and run `midden help`.
2. Locate a session with `midden ls --days 7 --json`.
3. Read an exact source with `midden read --tool TOOL --session ID --json`.
4. Search/read further context through the returned `view_id`.
5. Collect useful records to an ordinary `sources` directory.

The source tool and exact identifier are separate. Do not turn a bad selector
into an all-history scan. Read the output's scope, clipping and inventory warnings.

For AI-assisted work, install the matching bundle and start a fresh conversation
in the work directory. Give an ordinary goal, for example:

> Investigate this session and show me what is worth developing.

> Develop the strongest idea as a short internal article.

The agent should discover the appropriate guidance and execute the tools. A
draft must exist as an editable file. A presentation must be rendered and
inspected, not merely described in chat.

For existing work, point the host at its working directory. Notes, collections
and drafts are files; their existence is not conditional on a database project.

See [Core](CORE.md), [Installation](INSTALL.md), [Bundles](../bundles/README.md)
and [Acceptance](ACCEPTANCE.md).
