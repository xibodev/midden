package module

import (
	"embed"
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
//go:embed content/agents/midden-recovery.md content/skills/session-recovery/SKILL.md content/skills/evidence-selection/SKILL.md content/skills/content-seed/SKILL.md
var overlayFS embed.FS

// Overlay and skill IDs.
const (
	OverlayRecovery = "midden.recovery"

	SkillSessionRecovery   = "midden.session-recovery"
	SkillEvidenceSelection = "midden.evidence-selection"
	SkillContentSeed       = "midden.content-seed"
)

// Paths are relative to the module root, as the protocol requires: a module
// never hands the host an absolute path.
const (
	pathOverlayRecovery = "agents/midden-recovery.md"
	pathSkillRecovery   = "skills/session-recovery/SKILL.md"
	pathSkillEvidence   = "skills/evidence-selection/SKILL.md"
	pathSkillSeed       = "skills/content-seed/SKILL.md"
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
	case pathOverlayRecovery, pathSkillRecovery, pathSkillEvidence, pathSkillSeed:
		raw, err := overlayFS.ReadFile(embedPath(path))
		if err != nil {
			return nil, false
		}
		return raw, true
	}
	return nil, false
}
