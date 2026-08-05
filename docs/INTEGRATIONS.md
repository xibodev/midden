# Integrations

## Set up a built-in integration

Open **Integrations** in `midden ui`. Midden always shows the built-in options,
even before anything is installed:

1. Read what the tool does, its cost class, and its upstream requirements.
2. Open the upstream GitHub setup guide or connect a tool you already installed.
3. Save its local address or folder in Midden's settings.
4. Explicitly test it when you are ready.

The settings screen writes `~/.midden/integrations.json`, a bounded,
credential-free file owned by Midden. You do **not** need to edit a source
file or YAML manifest to configure Open Notebook or OpenMontage. Saving or
enabling a setup never contacts it; only **Test connection** does.

Midden records when a test last succeeded or failed. **Connected** therefore
means "last verified", not a hidden background health check.

### Open Notebook

Open Notebook is an optional local knowledge workspace. Its upstream project is
[lfnovo/open-notebook](https://github.com/lfnovo/open-notebook) (MIT) and its
official quick start uses Docker Desktop and Docker Compose.

Before first use, follow its upstream guidance to change its encryption key.
The upstream Compose defaults expose the web UI and API beyond loopback, so
review and restrict those bindings on a shared or networked machine. Then use
Midden's setup form to enter the local API and web addresses. Midden accepts
only loopback targets.

If the Open Notebook instance uses a password, enable that setting. Midden
asks for the password only for a source-send action, keeps it in memory for
that action, and never writes it to its settings, index, manifests, or job
history.

### OpenMontage

OpenMontage is an optional local video-production capability. Its upstream
project is [calesthio/OpenMontage](https://github.com/calesthio/OpenMontage).
It is a host-side project, not a service Compose stack: its documented setup
needs a local repository, Python 3.10+, Node 18+, FFmpeg, and an agentic CLI.

Use the setup form to select the installed repository and CLI. A test reads
that folder only after you explicitly request it; this matters if the folder
is on a network mount. Its runs can spend through the selected CLI, so setup
is free but eventual production actions retain Midden's cost gates.

OpenMontage is licensed upstream under AGPLv3. Review its upstream licence
before redistributing or bundling it.

## Advanced manifests

Midden integrations can also be declared in manifests under
`~/.midden/plugins/`. This is an advanced escape hatch for custom integrations,
not the normal setup path. Existing `open-notebook.yaml` or
`openmontage.yaml` files remain usable; the UI can explicitly adopt a
supported legacy setup into Settings and retains a backup rather than
silently overwriting it.

If a migration is interrupted, Midden marks it as needing attention and shows
**Finish migration**. It will not use either configuration until that explicit
recovery step completes.

Advanced manifest discovery is deliberately **passive**: listing manifests
does not contact a service.

```powershell
midden plugins list                 # parse + validate; no network
midden plugins probe                # check loopback targets
midden plugins verify               # check declared service operations against OpenAPI
midden plugins probe --allow-network # explicit authorization for non-loopback targets
```

### Manifest contract

Every manifest requires:

```yaml
name: example
kind: service       # service | capability | coordinator
enabled: true
cost: free          # free | spends
probe: ...
```

`cost` is required even when the plugin is unavailable. A plugin that can
spend through an agentic CLI without declaring its cost class would violate
Midden's estimate-before-spend contract.

Unknown YAML keys are rejected. A typo such as `enable: false` must not quietly
become an enabled integration.

### Probe policy

An HTTP probe is bounded, credential-free, redirect-free, and loopback-only by
default. It never expands process environment variables in its URL, so a
manifest cannot turn `${AWS_SECRET_ACCESS_KEY}` into an outgoing request.

Directory probes may use `${NAME}` for paths such as
`${OPENMONTAGE_HOME}/pipeline_defs`; their status never echoes the expanded
path value. A directory can be an automount, junction, FUSE/NFS/CIFS mount, or
mapped drive, and discovering which can itself cause I/O. Therefore **all
directory probes require `--allow-network`**; a manifest cannot grant itself
that permission.

An unavailable target is normal. Midden renders its reason rather than showing
a runnable action that will fail later.

### Open Notebook wire contract

Open Notebook owns its own UI and data. The explicit **Send nuggets** action
requires an existing notebook ID, sends only stored nuggets after a second
redaction pass, creates one multipart text source, then opens Open Notebook's
own UI. It never sends raw transcripts, creates notebooks, mirrors content, or
waits long enough to call a destination-side async job failed.

Choose one workspace, or explicitly opt in to all workspaces. Each source is
bounded to the 100 newest matching nuggets.

The source endpoint has an unusual but verified wire contract:

- `POST /api/sources` is `multipart/form-data`, including plain text;
- `embed` and `async_processing` are **strings** (`"true"`), not JSON booleans;
- `notebooks` is a JSON string such as `["notebook:abc123"]`;
- a notebook deep link URL-encodes the colon in its ID.

When password protection is enabled, Midden asks for the password only in the
explicit export dialog and sends it only in that request's Authorization
header. `OPEN_NOTEBOOK_PASSWORD` remains an optional non-UI fallback for
existing advanced setups. Neither path stores the password in the index,
settings, manifests, rendered UI, or job history, and export requests never
use an environment-configured HTTP proxy.
After the source is accepted, the job reports **submitted** and returns the
notebook link. The manifest verifies the lightweight
`/sources/{source_id}/status` operation, but Midden does not wait for
destination-side processing; Open Notebook continues it in its own UI.

### OpenMontage execution model

OpenMontage is a capability integration, not a service client. Its own
architecture makes the coding agent the orchestration plane; Midden therefore
uses the same agentic CLI it already uses for `reclaim`, `refine`, and `ask`.

It publishes pipeline YAML, JSON Schemas, checkpoints, and
`estimate -> reserve -> reconcile` cost tracking. A future action view should
render its form from those schemas and show its checkpoint state, not accept
arbitrary plugin-supplied HTML or JavaScript.

## Coordinator safety boundary

A future coordinator chat is a separate trust domain. Before any shell-capable
session launches, its working directory must be rejected if it is inside:

- `~/.copilot`
- `~/.claude`
- `~/.local/share/opencode`
- `~/.midden`

This must resolve Windows junctions and symlinks, not compare strings.
