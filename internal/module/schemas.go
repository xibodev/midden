package module

// JSON Schema documents referenced by the descriptor.
//
// They are embedded as constants rather than loaded from disk so `module
// describe` cannot fail because a file is missing from an install, and so the
// descriptor is a pure function of the binary.

const scopeRequestSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "xibodev.midden.sessions.list.request/v1",
  "title": "Midden session scope",
  "description": "An exact scope over read-only source stores. The zero value matches everything, so callers should narrow deliberately.",
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "tool": {
      "type": "string",
      "enum": ["copilot", "claude", "opencode"],
      "description": "Limit to one source store."
    },
    "days": {
      "type": "integer",
      "minimum": 0,
      "description": "Only sessions touched in the last n calendar days. 0 is unbounded."
    },
    "workspace": {"type": "string", "description": "Substring match against the session directory."},
    "repo": {"type": "string", "description": "Match repository."},
    "ids": {
      "type": "array",
      "items": {"type": "string"},
      "description": "Exact session identifiers. Preferred over inferred scope."
    },
    "id_prefix": {"type": "string", "description": "Match a session id prefix."},
    "include_noise": {"type": "boolean", "description": "Include automated or trivial sessions."},
    "max_sessions": {
      "type": "integer",
      "minimum": 0,
      "maximum": 200,
      "description": "Bound on sessions returned. 0 selects the default of 25."
    }
  }
}`

const sessionsListResultSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "xibodev.midden.sessions.list.result/v1",
  "title": "Midden session inventory",
  "type": "object",
  "additionalProperties": false,
  "required": ["sessions", "total", "truncated"],
  "properties": {
    "sessions": {
      "type": "array",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["session_id", "tool"],
        "properties": {
          "session_id": {"type": "string"},
          "tool": {"type": "string", "enum": ["copilot", "claude", "opencode"]},
          "title": {"type": "string"},
          "workspace": {"type": "string"},
          "repo": {"type": "string"},
          "created": {"type": "string", "format": "date-time"},
          "updated": {"type": "string", "format": "date-time"},
          "turns": {"type": "integer"},
          "bytes": {"type": "integer"},
          "risk": {"type": "string"},
          "workspace_exists": {"type": "boolean"}
        }
      }
    },
    "total": {"type": "integer"},
    "truncated": {"type": "boolean"}
  }
}`

const assayRequestSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "xibodev.midden.sessions.assay.request/v1",
  "title": "Midden assay request",
  "description": "Deterministic classification of session records. Never calls a model.",
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "tool": {"type": "string", "enum": ["copilot", "claude", "opencode"]},
    "days": {"type": "integer", "minimum": 0},
    "workspace": {"type": "string"},
    "repo": {"type": "string"},
    "ids": {"type": "array", "items": {"type": "string"}},
    "id_prefix": {"type": "string"},
    "include_noise": {"type": "boolean"},
    "max_sessions": {
      "type": "integer",
      "minimum": 0,
      "maximum": 200,
      "description": "Bound on sessions assayed. 0 selects the default of 25."
    },
    "max_candidates": {
      "type": "integer",
      "minimum": 0,
      "maximum": 500,
      "description": "Bound on candidate records tracked per session. 0 selects the default of 40."
    }
  }
}`

const assayResultSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "xibodev.midden.sessions.assay.result/v1",
  "title": "Midden assay result",
  "description": "Per-session composition and reclaimable yield. Candidate record previews are drawn from private transcripts and are deliberately not returned.",
  "type": "object",
  "additionalProperties": false,
  "required": ["sessions", "assayed", "failed", "truncated", "total_bytes", "signal_bytes", "reclaimable_bytes"],
  "properties": {
    "sessions": {
      "type": "array",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["session_id", "tool", "total_records", "total_bytes", "counts", "bytes", "by_kind"],
        "properties": {
          "session_id": {"type": "string"},
          "tool": {"type": "string", "enum": ["copilot", "claude", "opencode"]},
          "title": {"type": "string"},
          "total_records": {"type": "integer"},
          "total_bytes": {"type": "integer"},
          "counts": {
            "type": "object",
            "additionalProperties": {"type": "integer"},
            "description": "Record counts by class: signal, exhaust, artifact, bookkeeping."
          },
          "bytes": {
            "type": "object",
            "additionalProperties": {"type": "integer"},
            "description": "Byte totals by class."
          },
          "by_kind": {
            "type": "object",
            "additionalProperties": {"type": "integer"},
            "description": "Tool-native record kind breakdown, which is what makes a pruning decision defensible."
          },
          "signal_bytes": {"type": "integer"},
          "reclaimable_bytes": {"type": "integer"},
          "signal_share": {"type": "number", "minimum": 0, "maximum": 1},
          "compression": {"type": "number"},
          "candidate_count": {"type": "integer"},
          "slice_bytes": {"type": "integer"},
          "est_slice_tokens": {"type": "integer"},
          "duplicate_reads": {"type": "integer"},
          "duplicate_bytes": {"type": "integer"},
          "image_count": {"type": "integer"},
          "image_clusters": {"type": "integer"}
        }
      }
    },
    "assayed": {"type": "integer"},
    "failed": {"type": "integer"},
    "truncated": {"type": "boolean"},
    "total_bytes": {"type": "integer"},
    "signal_bytes": {"type": "integer"},
    "reclaimable_bytes": {"type": "integer"}
  }
}`

const seedCreateRequestSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "xibodev.midden.seed.create.request/v1",
  "title": "Midden seed creation request",
  "description": "Build a portable content seed from an exact session scope. Deterministic; never calls a model. Requires the midden_home write root.",
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "tool": {"type": "string", "enum": ["copilot", "claude", "opencode"]},
    "days": {"type": "integer", "minimum": 0},
    "workspace": {"type": "string"},
    "repo": {"type": "string"},
    "ids": {
      "type": "array",
      "items": {"type": "string"},
      "description": "Exact session identifiers. Strongly preferred over an inferred scope: a seed built from the wrong sessions is worse than no seed."
    },
    "id_prefix": {"type": "string"},
    "include_noise": {"type": "boolean"},
    "max_sessions": {"type": "integer", "minimum": 0, "maximum": 200},
    "max_candidates": {"type": "integer", "minimum": 0, "maximum": 500},
    "max_evidence": {
      "type": "integer",
      "minimum": 0,
      "maximum": 500,
      "description": "Bound on evidence records carried by the seed. 0 selects the default of 40."
    },
    "goal": {"type": "string", "description": "What the seed is for. The one field a useful seed should always carry."},
    "title": {"type": "string"},
    "summary": {"type": "string"},
    "key_points": {"type": "array", "items": {"type": "string"}},
    "suggested_output_types": {
      "type": "array",
      "items": {"type": "string"},
      "description": "Advisory hint. Deliberately not enumerated here: the consuming creative lane owns this vocabulary, so enumerating it in Midden's schema would make every new output type a breaking change in the wrong repository."
    },
    "brief": {"type": "string", "description": "Override the generated prose brief. Optional."},
    "name": {
      "type": "string",
      "maxLength": 100,
      "pattern": "^[A-Za-z0-9_-]+$",
      "description": "Seed directory name, a single safe path segment. Generated when absent."
    }
  }
}`

const seedCreateResultSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "xibodev.midden.seed.create.result/v1",
  "title": "Midden seed creation result",
  "type": "object",
  "additionalProperties": false,
  "required": ["schema", "root", "path", "manifest_path", "evidence_digest", "evidence_count", "session_count"],
  "properties": {
    "schema": {"const": "xibodev.midden.seed/v1"},
    "root": {"type": "string", "description": "Logical root name the path is relative to."},
    "path": {"type": "string", "description": "Seed directory, RELATIVE to the named root. Never absolute: an absolute path would be unverifiable by the host and meaningless to a consumer reading a staged copy."},
    "manifest_path": {"type": "string", "description": "Entry file, relative to the same root."},
    "evidence_digest": {
      "type": "string",
      "pattern": "^[0-9a-f]{64}$",
      "description": "sha256 over evidence.jsonl, bare lowercase hex. Scoped to the evidence set, not the bundle, so regenerating brief prose does not invalidate a provenance claim."
    },
    "evidence_count": {"type": "integer", "minimum": 0, "description": "Zero is valid: a goal with no recovered material is a legitimate seed."},
    "session_count": {"type": "integer", "minimum": 0}
  }
}`

const seedManifestSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "xibodev.midden.seed/v1",
  "title": "Midden content seed manifest",
  "description": "Entry file of a seed bundle directory containing manifest.json, brief.md, evidence.jsonl, provenance.json and attachments/. Portable: readable with no Midden binary, index, or environment. Every internal path is relative to the seed root, and the digest rather than the path is the stable identity.",
  "type": "object",
  "required": ["schema", "evidence_digest", "evidence_count"],
  "properties": {
    "schema": {"const": "xibodev.midden.seed/v1"},
    "goal": {"type": "string"},
    "title": {"type": "string"},
    "summary": {"type": "string"},
    "key_points": {"type": "array", "items": {"type": "string"}},
    "attachments": {
      "type": "array",
      "items": {"type": "string"},
      "description": "Paths relative to the seed root. Never absolute, never referencing Midden internals."
    },
    "suggested_output_types": {
      "type": "array",
      "items": {"type": "string"},
      "description": "Advisory. An unrecognised value degrades to no suggestion rather than failing the seed."
    },
    "evidence_digest": {"type": "string", "pattern": "^[0-9a-f]{64}$"},
    "evidence_count": {"type": "integer", "minimum": 0},
    "created_at": {"type": "string", "format": "date-time"}
  }
}`
