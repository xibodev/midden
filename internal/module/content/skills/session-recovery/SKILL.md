---
name: midden-session-recovery
description: Recover context from an agentic-CLI session that is too large to resume. Use when a session will not resume, resume "did nothing" and started fresh, or context was lost and work must continue elsewhere. Covers finding the session, measuring what it is made of with a free deterministic assay, and packaging the part worth keeping. Requires the midden binary.
---

# Session recovery

Recovering a session that cannot be resumed.

## When this applies

A user says a session is too large to resume, that resume "did nothing", or
that they lost context and want to continue elsewhere.

Copilot's `--resume` fails silently above roughly 680 MiB and starts a NEW
session instead of reporting an error. The user experiences this as amnesia,
not as a failure. If someone describes that symptom, size is the first thing
to check.

## Sequence

1. **Find the session.** `sessions.list` with the tightest scope you can
   justify — `ids` when the user named one, otherwise `workspace` or `days`.
   Confirm the match with the user before going further if the scope was
   inferred rather than given.

2. **Assay it.** `sessions.assay` on that exact session. Free, deterministic,
   no model. Read `signal_share`, `reclaimable_bytes`, and `compression`.

3. **Say what is there.** Report composition before proposing action:
   "2.2 MB, 70% signal, 330 KB reclaimable" is a fact the user can act on.
   "You should prune this" is a recommendation they did not ask for.

4. **Carry it forward.** `seed.create` packages the selected evidence with
   provenance into a portable bundle another module or a fresh session can use.

## Judgement

**A dead workspace is why resume fails, not just size.** `workspace_exists:
false` means the directory the session was keyed to is gone. Resuming into it
cannot work regardless of transcript size. Check this before blaming bytes.

**High exhaust is not a problem to fix.** A session that is 90% tool output
was working normally — tool results are large. It means the recoverable slice
is small and cheap, which is good news, not a defect.

**Do not propose deletion.** Midden measures and packages. Pruning and
archiving are separate, explicitly gated decisions the user makes with full
information. Reporting `reclaimable_bytes` is informing them; recommending
they reclaim it is not your call.

**Truncation is not failure.** `truncated: true` means the scope matched more
sessions than the bound. Say so plainly and offer to narrow the scope, rather
than presenting a partial list as complete.

## Invoking Midden

Midden is a local binary. Every capability is one command; stdout carries a
single JSON envelope and diagnostics go to stderr, so parse stdout alone.

```bash
# What can this Midden do?
midden module describe --json

# Inventory sessions. Write the request to a file first.
cat > /tmp/req.json <<'EOF'
{"protocol":"xibodev.module/v1","capability":"sessions.list",
 "request_id":"r1","input":{"tool":"claude","days":7,"max_sessions":10}}
EOF
midden module invoke sessions.list --input /tmp/req.json

# Assay one exact session.
cat > /tmp/assay.json <<'EOF'
{"protocol":"xibodev.module/v1","capability":"sessions.assay",
 "request_id":"r2","input":{"ids":["<session-id>"],"max_candidates":40}}
EOF
midden module invoke sessions.assay --input /tmp/assay.json
```

Read `ok` first. On `ok:false` route on `error.code`, never on message text.
`no_source_stores` means Midden could not see the stores at all — that is not
"the user has no sessions".

Midden also has a human-facing CLI (`midden ls`, `midden doctor`,
`midden brief <id>`) which is often the faster answer for a person.
