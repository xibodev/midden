# Optional integrations

Midden is complete without integrations. Connections add local makers or
destinations after the core scan, evidence, recipe, review, and export workflow
is working.

The normal setup path is **Connections** in `midden ui`. Advanced YAML
manifests are an escape hatch, not a prerequisite.

## Managed settings

Open **Connections** to:

1. Read what an integration does and whether later actions can spend.
2. Open the upstream installation guide.
3. Install and configure the upstream project yourself.
4. Save a local URL, path, or backend in Midden.
5. Select **Test connection** when the upstream tool is running.

Managed settings are written to:

```text
<MIDDEN_HOME>\integrations.json
```

Saving or enabling a connection is passive. It does not contact the target.
**Connected** means the last explicit test succeeded; Midden does not run a
hidden health-check loop.

The settings file is bounded and credential-free.

## Detected local makers

Connections can report locally installed tools such as:

- Pandoc
- Quarto
- Marp
- D2
- Typst
- Promptfoo
- GitHub CLI

Midden does not install them. Their availability affects optional output
rendering or handoff, not core evidence extraction and review.

## Open Notebook

[Open Notebook](https://github.com/lfnovo/open-notebook) is an optional local
knowledge workspace.

### Upstream preparation

Follow the upstream Docker Desktop and Docker Compose instructions. Before
first use:

- change the upstream encryption key;
- review its UI and API bind addresses;
- restrict them appropriately on shared or networked machines;
- start the service before testing it from Midden.

Midden does not install or start Open Notebook.

### Midden setup

In **Connections → Open Notebook**, configure:

- API URL, normally `http://127.0.0.1:5055/api`;
- UI URL, normally `http://127.0.0.1:8502`;
- whether the instance requires a password.

Only loopback URLs are accepted.

If a password is required, Midden requests it for the explicit send action,
keeps it in memory for that request, and does not write it to settings, the
index, a manifest, rendered UI, or job history.

`OPEN_NOTEBOOK_PASSWORD` is an optional current-process fallback for advanced
setups. Do not commit it.

### Send behavior

**Send nuggets**:

- requires an existing notebook ID;
- sends stored nuggets, not raw transcripts;
- applies a second redaction pass;
- chooses one workspace unless all workspaces are explicitly selected;
- limits the source to the 100 newest matching nuggets;
- creates one multipart text source;
- returns the notebook link after the source is accepted;
- does not create a notebook or wait for downstream processing to finish.

The verified upstream wire contract uses:

- `POST /api/sources`;
- `multipart/form-data`;
- string values for `embed` and `async_processing`;
- a JSON string for the `notebooks` field;
- URL encoding for the colon in notebook IDs.

Export requests ignore environment-configured HTTP proxies.

## OpenMontage

[OpenMontage](https://github.com/calesthio/OpenMontage) is an optional
agent-driven local video-production project.

Its upstream setup requires:

- a local OpenMontage repository;
- Python 3.10 or newer;
- Node 18 or newer;
- FFmpeg;
- an authenticated agentic CLI.

In **Connections → OpenMontage**, configure:

- the absolute path to the checked-out repository;
- `copilot`, `claude`, or `opencode` as its backend.

Midden saves the path and backend only. It does not install dependencies.
Testing the connection reads the configured folder only after an explicit
request.

OpenMontage actions can spend through the selected CLI, so eventual production
retains Midden's preview and approval gates.

OpenMontage is licensed upstream under AGPLv3. Review that license before
redistributing or bundling it.

## Advanced manifests

Custom integration manifests live under:

```text
<MIDDEN_HOME>\plugins\
```

Commands:

```powershell
midden plugins list
midden plugins probe
midden plugins verify
midden plugins probe --allow-network
```

`list` parses and validates without contacting a target. `probe` and `verify`
perform explicit checks.

### Required fields

```yaml
name: example
kind: service       # service | capability | coordinator
enabled: true
cost: free          # free | spends
probe: ...
```

Unknown YAML keys are rejected. A misspelled safety field must not silently
change behavior.

`cost` is mandatory even for unavailable integrations. A plugin that can spend
through an agentic CLI must declare that fact before it becomes actionable.

### Probe policy

HTTP probes are:

- bounded;
- credential-free;
- redirect-free;
- loopback-only by default.

A manifest cannot expand environment variables into an HTTP URL.

Directory paths may contain environment variables for local installation
paths, but every directory probe requires `--allow-network` because a path can
resolve to a mapped drive, automount, junction, or network filesystem.

An unavailable target is a normal visible state, not a runnable action that
fails later.

## Legacy manifest migration

Existing `open-notebook.yaml` and `openmontage.yaml` manifests remain usable.
The UI can explicitly adopt a supported advanced setup into managed Settings.

Migration:

- never silently overwrites the manifest;
- retains a backup;
- records interrupted state;
- requires **Finish migration** if interrupted;
- refuses to use conflicting managed and advanced configurations.

## Conductor and shell boundary

Conductor can search reclaimed evidence, answer evidence questions, design a
recipe, and run an approved production. Shell execution is disabled.

Any future shell-capable feature is a separate trust domain. It must reject a
working directory inside:

- `~\.copilot`
- `~\.claude`
- `~\.local\share\opencode`
- `~\.midden`

Path checks must resolve Windows junctions and symbolic links rather than
compare strings.
