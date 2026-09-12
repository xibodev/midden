// Package viewdef provides the product-owned view definition for Midden.
//
// Identical definitions are consumed by Midden standalone (M-APP) and
// the Midden Studio module (M-MOD), ensuring byte-for-byte digest parity.
package viewdef

import "github.com/xibodev/facet-studio/pkg/view"

const (
	MiddenViewID = "midden.content_workbench"
	Version      = "1.0.0"
)

// MiddenWorkbenchView returns the canonical declarative workbench view for Midden.
func MiddenWorkbenchView() *view.ViewDefinition {
	return &view.ViewDefinition{
		ID:             MiddenViewID,
		SchemaVersion:  Version,
		Title:          "Midden Content Workbench",
		Module:         "midden",
		ArtifactSchema: "xibodev.midden.content.output/v1",
		Sections: []view.SectionDefinition{
			{
				ID:        "evidence_view",
				Title:     "Reclaimed Evidence",
				Type:      "tab",
				Primitive: view.Table,
				Binding:   "evidence.nuggets",
				EmptyText: "No evidence mined yet. Scope and assay sessions to extract evidence.",
			},
			{
				ID:        "article_view",
				Title:     "Article & Tutorial",
				Type:      "tab",
				Primitive: view.Markdown,
				Binding:   "content.article",
				EmptyText: "No article draft produced yet.",
			},
			{
				ID:        "slides_view",
				Title:     "Presentation Deck",
				Type:      "tab",
				Primitive: view.Slides,
				Binding:   "content.slides",
				EmptyText: "No presentation slides produced yet.",
			},
			{
				ID:        "provenance_view",
				Title:     "Provenance & Citations",
				Type:      "tab",
				Primitive: view.Document,
				Binding:   "provenance.manifest",
				EmptyText: "No provenance manifest recorded yet.",
			},
		},
		Actions: []view.ActionDefinition{
			{
				ID:    "mine_evidence",
				Label: "Mine Evidence",
				Tool:  "midden_evidence_extract",
			},
			{
				ID:    "produce_article",
				Label: "Produce Article",
				Tool:  "midden_content_produce",
			},
			{
				ID:    "produce_slides",
				Label: "Produce Slides",
				Tool:  "midden_content_produce",
			},
			{
				ID:               "export_bundle",
				Label:            "Export Verified Seed",
				Tool:             "midden_seed_create",
				RequiresApproval: true,
			},
		},
	}
}
