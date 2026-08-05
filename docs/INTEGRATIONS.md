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

Open Notebook owns its own UI and data. Midden currently validates the
declarative contract—manifest shape, local probe, and declared OpenAPI
paths/verbs—but **does not yet push evidence, create notebooks, validate
multipart bodies, or construct deep links at runtime**. Those actions are P3:
they will be separately gated, preflighted, and tested against a live Open
Notebook instance before they become runnable.

The source endpoint has an unusual but verified wire contract:

- `POST /api/sources` is `multipart/form-data`, including plain text;
- `embed` and `async_processing` are **strings** (`"true"`), not JSON booleans;
- `notebooks` is a JSON string such as `["notebook:abc123"]`;
- a notebook deep link URL-encodes the colon in its ID.

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
