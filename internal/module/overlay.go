package module

import (
	"embed"
	"encoding/json"
	"fmt"
	"strings"
)

// Agent overlay and skills.
//
// They are EMBEDDED rather than read from disk so `module describe` cannot fail
// because an install is missing a file, and so a digest is a property of the
// binary rather than of whatever happens to be on disk beside it. The host
// verifies these digests before folding any of this into agent context, so a
// mismatch must be impossible rather than merely unlikely.
//
// The embedded copies live under content/ because go:embed cannot reach above
// the package directory. The repository root holds the same files at the paths
// the descriptor advertises, which is where a host reading from an installed
// module directory finds them. A test asserts the two are byte-identical, so
// the copy cannot silently drift from the published document.
//
//go:embed content/agents/midden-recovery.md content/skills/session-recovery/SKILL.md content/skills/evidence-selection/SKILL.md content/skills/content-seed/SKILL.md content/skills/editorial-production/SKILL.md
var overlayFS embed.FS

// Overlay and skill IDs.
const (
	OverlayRecovery = "midden.recovery"

	SkillSessionRecovery   = "midden.session-recovery"
	SkillEvidenceSelection = "midden.evidence-selection"
	SkillContentSeed       = "midden.content-seed"
	SkillEditorial         = "midden.editorial-production"
)

// Paths are relative to the module root, as the protocol requires: a module
// never hands the host an absolute path.
const (
	pathOverlayRecovery = "agents/midden-recovery.md"
	pathSkillRecovery   = "skills/session-recovery/SKILL.md"
	pathSkillEvidence   = "skills/evidence-selection/SKILL.md"
	pathSkillSeed       = "skills/content-seed/SKILL.md"
	pathSkillEditorial  = "skills/editorial-production/SKILL.md"
)

// embedPath maps a declared module-relative path to its embedded copy.
func embedPath(p string) string { return "content/" + p }

// mustRead returns embedded content. A failure here is a build-time mistake —
// the file is embedded, so it cannot go missing at runtime.
func mustRead(path string) []byte {
	raw, err := overlayFS.ReadFile(embedPath(path))
	if err != nil {
		panic("embedded module content missing: " + path)
	}
	return raw
}

// estimateTokens approximates context cost so the host can budget before
// loading. Deliberately crude: roughly four bytes per token is close enough to
// decide whether to load a document, and pretending to more precision would
// imply a measurement that was not made.
func estimateTokens(raw []byte) int {
	return len(raw) / 4
}

// overlays returns the agent overlay set, with digests computed over the exact
// bytes the host will read.
func overlays() []Overlay {
	raw := mustRead(pathOverlayRecovery)
	return []Overlay{{
		ID:     OverlayRecovery,
		Title:  "Midden session recovery",
		Path:   pathOverlayRecovery,
		Digest: DigestSHA256(raw),
		Tokens: estimateTokens(raw),
	}}
}

// skills returns the progressively loadable skill set.
//
// They are separate documents on purpose: the host loads only what the current
// request needs, so a session-recovery question does not cost the agent the
// seed contract as well.
func skills() []Skill {
	defs := []struct {
		id, title, summary, path string
	}{
		{
			SkillEditorial,
			"Editorial production",
			"Use when discovering stories, lessons, content opportunities, or long-form projects in agentic history.",
			pathSkillEditorial,
		},
		{
			SkillSessionRecovery,
			"Recovering a session",
			"Finding, assaying and carrying forward a session that cannot be resumed.",
			pathSkillRecovery,
		},
		{
			SkillEvidenceSelection,
			"Selecting evidence",
			"Reading assay classes and choosing the bounded slice worth keeping.",
			pathSkillEvidence,
		},
		{
			SkillContentSeed,
			"Content seeds",
			"Building and describing a portable xibodev.midden.seed/v1 bundle.",
			pathSkillSeed,
		},
	}

	out := make([]Skill, 0, len(defs))
	for _, d := range defs {
		raw := mustRead(d.path)
		out = append(out, Skill{
			ID:      d.id,
			Title:   d.title,
			Summary: d.summary,
			Path:    d.path,
			Digest:  DigestSHA256(raw),
			Tokens:  estimateTokens(raw),
		})
	}
	return out
}

// OverlayContent returns the bytes for an overlay or skill path, so a host that
// installed only the binary can still read what the descriptor advertises.
//
// It refuses any path the descriptor does not declare. Serving an arbitrary
// path would turn a content-lookup helper into a file-read primitive, which is
// exactly the kind of capability that must not appear by accident.
func OverlayContent(path string) ([]byte, bool) {
	switch strings.TrimSpace(path) {
	case pathOverlayRecovery, pathSkillRecovery, pathSkillEvidence, pathSkillSeed, pathSkillEditorial:
		raw, err := overlayFS.ReadFile(embedPath(path))
		if err != nil {
			return nil, false
		}
		return raw, true
	}
	return nil, false
}

// WorkflowGuidance is delivery-neutral knowledge shared by the CLI bundle and
// native prompt contributor. Capability names are stable; hosts adapt invocation.
func WorkflowGuidance() string {
	var b strings.Builder
	b.WriteString("\n## Evidence-to-output workflow\n\nInspect existing evidence and projects before re-extracting. Establish the goal and exact source scope; discover worthwhile stories before forcing a format. For open-ended content use midden-editorial-production: evidence.prepare/compose, projects.create/inspect, editorial.prepare/analyze, and editorial.select. The host authors semantic analysis; Midden validates and persists it without a nested model. Selection creates a draft recipe, not approval. A preview, saved recipe, approved evidence set, draft, reviewed output and exported file are different states.\n\n")
	for _, cap := range workflowCapabilities {
		fmt.Fprintf(&b, "- `%s`: %s\n", cap.ID, cap.Summary)
	}
	b.WriteString("\nUse recipes.preview to discuss a plan; recipes.design saves it. recipes.evidence records the user's exact evidence selection and review. recipes.produce starts only from an approved plan. Read outputs.inspect and its content_digest before outputs.review; send that digest as expected_digest. Revisions invalidate earlier review. Export only reviewed, unchanged bytes with provenance. A model's self-review is not a human's editorial approval. Never claim a seed or a source document is a finished rendered deliverable.\n")
	b.WriteString("\nWhen the host agent authors the content itself, recipes.compose accepts drafts keyed by output kind and saves them into the same approved-plan lifecycle without a nested model call. Use outputs.render for editable PPTX from slides or standalone HTML from Markdown. Pandoc must be installed; surface missing renderer errors. A source preview is not a visual verification of PowerPoint rendering.\n")
	return b.String()
}

// CLISkillContent includes the canonical recovery overlay in the entry skill so
// installing skills alone does not silently omit the agent's steering guidance.
func CLISkillContent(path string) ([]byte, bool) {
	raw, ok := OverlayContent(path)
	if !ok {
		return nil, false
	}
	out := append([]byte(nil), raw...)
	if path == pathSkillRecovery || path == pathSkillEditorial {
		out = append(out, []byte("\n\n## Recovery capability guidance\n\n")...)
		out = append(out, mustRead(pathOverlayRecovery)...)
	}
	out = append(out, []byte(WorkflowGuidance())...)
	return out, true
}

// SelfCheck reports problems in Midden's own descriptor.
//
// It exists because of a question a host could not answer from outside: a
// module that reports nothing because it checked, and one that reports nothing
// because it does not look, are indistinguishable on a page listing warnings.
// Midden was the second kind. Silence is only meaningful if something looked.
//
// Every check here is about what THIS BINARY declares versus what it can
// actually serve, so it needs no filesystem, no roots, and no host — it is
// answerable at describe time and cannot fail for an environmental reason.
func SelfCheck() []string { return selfCheckOf(Describe()) }

// selfCheckOf is the testable seam: it takes a descriptor rather than building
// one, so a test can mutate a field and prove the check detects it.
func selfCheckOf(d Descriptor) []string {
	var out []string

	declaredSkills := map[string]bool{}
	for _, s := range d.Skills {
		declaredSkills[s.ID] = true
	}

	for _, c := range d.Capabilities {
		// A capability referencing a schema the descriptor does not declare
		// leaves a host with nothing to validate its requests against. The
		// reference resolves in the code and dangles on the wire.
		if _, ok := d.RequestSchemas[c.RequestSchema]; c.RequestSchema != "" && !ok {
			out = append(out, fmt.Sprintf(
				"capability %s references request schema %q that the descriptor does not declare",
				c.ID, c.RequestSchema))
		}
		if _, ok := d.ResultSchemas[c.ResultSchema]; c.ResultSchema != "" && !ok {
			out = append(out, fmt.Sprintf(
				"capability %s references result schema %q that the descriptor does not declare",
				c.ID, c.ResultSchema))
		}
		for _, k := range c.ArtifactSchemas {
			if _, ok := d.ArtifactSchemas[k]; !ok {
				out = append(out, fmt.Sprintf(
					"capability %s may produce artifact %q that the descriptor does not declare, "+
						"so a host has no schema to validate or render it", c.ID, k))
			}
		}
		for _, id := range c.Skills {
			if !declaredSkills[id] {
				out = append(out, fmt.Sprintf(
					"capability %s references skill %q that the descriptor does not declare", c.ID, id))
			}
		}
		// A capability that is not long-running must not name a poll target:
		// a host would offer polling for something that never returns a handle.
		if !c.LongRunning && c.PollCapability != "" {
			out = append(out, fmt.Sprintf(
				"capability %s is not long-running but names poll capability %q", c.ID, c.PollCapability))
		}
	}

	// Overlay and skill content must be servable and match its declared digest.
	// A host refuses a mismatch, so this is the module reporting the same
	// refusal before the host has to.
	check := func(kind, id, path, digest string) {
		raw, ok := OverlayContent(path)
		if !ok {
			out = append(out, fmt.Sprintf("%s %s declares path %q that this binary cannot serve", kind, id, path))
			return
		}
		if got := DigestSHA256(raw); got != digest {
			out = append(out, fmt.Sprintf(
				"%s %s at %s does not match its declared digest and would be REFUSED when loaded "+
					"into agent context (declared %s, embedded %s)", kind, id, path, digest, got))
		}
	}
	for _, o := range d.AgentOverlays {
		check("overlay", o.ID, o.Path, o.Digest)
	}
	for _, s := range d.Skills {
		check("skill", s.ID, s.Path, s.Digest)
	}

	// Every embedded schema must be valid JSON with an $id that agrees with
	// its map key, or a reference resolves to a schema describing something
	// else. An ABSENT $id is legal — the key is the identity.
	for name, raw := range allSchemas(d) {
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			out = append(out, fmt.Sprintf("schema %q is not valid JSON: %v", name, err))
			continue
		}
		if id, present := doc["$id"]; present {
			if s, _ := id.(string); s != name {
				out = append(out, fmt.Sprintf(
					"schema %q declares $id %q; when both are present they must agree", name, s))
			}
		}
	}

	return out
}

func allSchemas(d Descriptor) map[string]json.RawMessage {
	all := map[string]json.RawMessage{}
	for k, v := range d.RequestSchemas {
		all[k] = v
	}
	for k, v := range d.ResultSchemas {
		all[k] = v
	}
	for k, v := range d.ArtifactSchemas {
		all[k] = v
	}
	return all
}
