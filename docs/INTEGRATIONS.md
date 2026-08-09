# Optional integrations

Midden is complete without integrations. Tools adds local makers or
destinations after the core scan, evidence, recipe, review, and export workflow
is working.

The normal setup path is **Tools** in `midden ui`. Advanced YAML
manifests are an escape hatch, not a prerequisite.

## Managed settings

Open **Tools** to:

1. Read what an integration does and whether later actions can spend.
2. Open the upstream installation guide.
3. Follow complete setup steps for that connection type.
4. Browse for a local repository folder or enter loopback URLs.
5. Save the local URL, path, or backend in Midden.
6. Select **Save & test** to verify the real service or runtime prerequisites.
7. Open the connected UI directly from the capability card where supported.

Managed settings are written to:

```text
<MIDDEN_HOME>\integrations.json
```

Saving or enabling a connection is passive. It does not contact the target.
**Connected** means the last explicit test succeeded; Midden does not run a
hidden health-check loop.

The settings file is bounded and credential-free.

## Detected local makers

Tools can report locally installed capabilities such as:

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

In **Tools → Open Notebook**, configure:

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

In **Tools → OpenMontage**:

1. Follow the displayed prerequisite and clone instructions.
2. Select **Browse folder** and choose the checked-out repository root.
3. Choose `copilot`, `claude`, or `opencode` as the agentic backend.
4. Select **Save & test**.

Midden saves the path and backend only. Testing is explicit and verifies:

- the OpenMontage repository contract;
- pipeline definitions;
- the Backlot UI;
- a usable Python environment;
- Node.js and FFmpeg;
- the selected authenticated AI CLI.

After a successful test, **Open Backlot** launches OpenMontage's local living
storyboard. Studio's **Create video** action starts the selected pipeline in the
persistent workspace-agent conversation. Required production decisions and
spend approvals remain visible in chat. Approved MP4/WebM files copied to the
Midden delivery directory are imported as draft outputs and preview inline.

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

## Studio workspace-agent and Console boundary

Studio continues one evidence-grounded AI CLI conversation with local tools
enabled. It selects a normal project workspace when one is safe and otherwise
uses a Midden-owned work area. It rejects a working directory inside:

- `~\.copilot`
- `~\.claude`
- `~\.local\share\opencode`
- `~\.midden`

Path checks must resolve Windows junctions and symbolic links rather than
compare strings.

The workspace agent is instructed to ask before destructive or irreversible
actions, publishing, credential changes, external uploads, and unapproved paid
provider calls. Final files must be copied to the dedicated per-work-item
delivery directory before Midden imports or previews them.

Console remains a separate allowlisted diagnostics surface for status, files,
evidence, runs, and OpenMontage status. It is not a host shell.
