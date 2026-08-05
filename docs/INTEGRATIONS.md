# Integrations

Midden integrations are declarative manifests under `~/.midden/plugins/`.
They are discovered dynamically, but discovery is deliberately **passive**:
listing manifests does not contact a service.

```powershell
midden plugins list                 # parse + validate; no network
midden plugins probe                # check loopback targets
midden plugins verify               # check declared service operations against OpenAPI
midden plugins probe --allow-network # explicit authorization for non-loopback targets
```

## Manifest contract

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

## Probe policy

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

## Open Notebook

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

If `OPEN_NOTEBOOK_PASSWORD` is configured, Midden sends the configured
password only in the Authorization header of the explicit export request. It
is never stored in the index, rendered in the UI, used during passive
listing/probes, or sent through an environment-configured HTTP proxy.
After the source is accepted, the job reports **submitted** and returns the
notebook link. The manifest verifies the lightweight
`/sources/{source_id}/status` operation, but Midden does not wait for
destination-side processing; Open Notebook continues it in its own UI.

The configured manifest can be copied from the user's local
`~/.midden/plugins/open-notebook.yaml` once Open Notebook is installed.

## OpenMontage

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
