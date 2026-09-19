package create

import (
	"encoding/json"
	"fmt"
	"os"
)

func (w Workflow) AuditOutput(c Change) (DraftAudit, error) {
	var empty DraftAudit
	o, err := w.DB.RefineryOutput(c.OutputID)
	if err != nil {
		return empty, err
	}
	path, err := w.OwnedFile(o.Path)
	if err != nil {
		return empty, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return empty, err
	}
	if info.Size() > 2<<20 || len(c.Body) > 2<<20 {
		return empty, fmt.Errorf("output exceeds the audit text bound")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return empty, err
	}
	if c.ExpectedDigest != "" && c.ExpectedDigest != Digest(raw) {
		return empty, fmt.Errorf("output changed since inspection")
	}
	body := c.Body
	if body == "" {
		body = string(raw)
	}
	body = NormalizeSourceMetadata(body)
	ids := c.EvidenceIDs
	if ids == nil {
		ids = o.EvidenceIDs
	}
	allowed := map[string]bool{}
	for _, id := range o.EvidenceIDs {
		allowed[id] = true
	}
	for _, id := range ids {
		if !allowed[id] {
			return empty, fmt.Errorf("evidence %s is outside the output scope", id)
		}
	}
	evidence, err := w.DB.NuggetsByIDs(ids)
	if err != nil {
		return empty, err
	}
	if len(evidence) != len(ids) {
		return empty, fmt.Errorf("source evidence is missing")
	}
	provPath, err := w.OwnedFile(o.ProvenancePath)
	if err != nil {
		return empty, err
	}
	prov, err := os.ReadFile(provPath)
	if err != nil {
		return empty, err
	}
	var manifest struct {
		Keys             map[string]string `json:"citation_keys"`
		Model            string            `json:"model"`
		GenerationDigest string            `json:"generation_digest"`
	}
	if err = json.Unmarshal(prov, &manifest); err != nil {
		return empty, err
	}
	if o.Kind == "notebook_pack" && manifest.Model == "deterministic" && manifest.GenerationDigest == Digest([]byte(body)) {
		return DraftAudit{ContentDigest: manifest.GenerationDigest, MechanicalValid: true, SemanticReview: "required",
			Findings: []AuditFinding{}, Passages: []CitedPassage{}}, nil
	}
	if o.Format != "markdown" && o.Format != "marp" {
		return DraftAudit{ContentDigest: Digest([]byte(body)), MechanicalValid: false, Blocked: false, SemanticReview: "required",
			Findings: []AuditFinding{{Code: "manual_format_review", Message: "Paragraph/quotation auditing is unavailable for this source format; inspect it manually before approving"}},
			Passages: []CitedPassage{}}, nil
	}
	return AuditDraft(w.DB, body, evidence, manifest.Keys)
}
