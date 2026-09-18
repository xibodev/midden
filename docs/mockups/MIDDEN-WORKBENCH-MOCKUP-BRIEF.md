---
app: Midden
feature: Recovery workbench redesign
style: interactive full-journey prototype
target: desktop workbench, including compact desktop windows
output_file: MIDDEN-WORKBENCH-FULL-JOURNEYS.html
---

# Product intent

Midden is a local recovery workspace for discarded AI coding sessions.

The simplified journey is:

1. Recover sessions into scoped, durable mine runs.
2. Turn evidence into useful work inside one persistent Studio.
3. Review, render, download, and export outputs.
4. Reclaim storage only when recovery coverage proves it is safe.

# Information architecture

Primary destinations:

- Recover
  - session filters and selection;
  - new mine controls;
  - durable mine-run history;
  - evidence coverage and recovery status.
- Studio
  - permanent work-item list;
  - persistent Chat and optional Console;
  - output/file tabs;
  - type-aware preview, source, and provenance;
  - budget envelope rather than per-turn approvals.
- Library
  - all generated outputs;
  - content-type filters;
  - download, reveal, and export actions.
- Cleanup
  - explainable eligibility;
  - archive-before-purge lifecycle;
  - source fingerprint and recovery gates.
- Activity
  - background jobs;
  - progress, retry, cancel, and audit.
- Tools
  - runtime plugins;
  - agent skills;
  - callable tools;
  - viewers and destinations.

Knowledge, Agent Forge, Personalization, Conductor, Connections, and Operations
are no longer separate product worlds. They become output types, Studio
capabilities, contextual tools, or Activity filters.

# Design language

- Background: #08121f
- Workspace: #0c1726
- Surface: #111f31
- Elevated surface: #17283d
- Border: #29415a
- Primary: #78a6ff
- Primary strong: #4f83e8
- Success: #3bd3a1
- Warning: #f2ba63
- Danger: #f07888
- Text: #edf5ff
- Text dim: #93a8be
- Heading font: system sans-serif
- Utility font: system monospace
- Radius: 10-14px
- Density: compact operator workspace, not oversized marketing cards

# Desktop layout

- Stage fills the browser.
- Sidebar: 224px.
- Sticky top bar: 64px.
- Main content uses responsive grid panels.
- Studio:
  - work-item rail: 250px;
  - Chat/Console pane: minmax(360px, 0.95fr);
  - preview pane: minmax(420px, 1.25fr);
  - background-job dock anchored at bottom-right.

# Compact desktop layout

- Sidebar may become a drawer when the application window is narrow.
- Studio may move the preview below the work-item and conversation panes.
- Tables remain horizontally contained.
- This is window-resize resilience, not a phone or mobile product target.

# Interaction plan

## Recover

- Open and close the New mine builder.
- Filter by source, workspace, date range, status, and dormancy.
- Select exact sessions.
- Start a mine without blocking the UI.
- Watch progress in the global task dock.
- Completed run appears in history.

## Studio

- Select work items without leaving the Studio shell.
- Switch Chat and Console.
- Send a chat message and receive a simulated streamed answer.
- Run a controlled console command.
- Switch tutorial, diagram, and video outputs.
- Switch rendered preview, source, and provenance.
- Download or reveal outputs without requiring publication approval.

## Library

- Filter documents, visuals, video, data, and agent outputs.
- Download any owned draft.
- Open a work item in Studio.

## Cleanup

- Inspect why a session is eligible or held.
- Show source fingerprint, evidence coverage, output usage, dormancy, newer
  sessions, active references, and archive state.
- Queue reversible archive only; no destructive operation exists in the
  prototype.

## Activity

- Inspect jobs independently from the page that started them.
- Show progress, elapsed state, and completion.

## Tools

- Distinguish plugin, tool, skill, viewer, and destination.
- Show Promptfoo as missing with a contextual setup action.

# Key payoff

The central payoff is the Studio:

```text
work items/files | persistent chat or terminal | live type-aware preview
```

A task can continue in the background while the user navigates anywhere.
Generated source becomes a usable rendered artifact with download and export
choices. Completed recovery feeds explainable cleanup eligibility.
