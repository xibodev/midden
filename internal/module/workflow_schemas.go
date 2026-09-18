package module

import (
	"encoding/json"
	"reflect"

	"github.com/mekjr1/midden/internal/cost"
	"github.com/mekjr1/midden/internal/index"
	"github.com/mekjr1/midden/internal/refinery"
)

type recipeResult struct {
	Recipe index.Recipe `json:"recipe"`
}
type designResult struct {
	Recipe         index.Recipe            `json:"recipe"`
	EvidenceReport refinery.EvidenceReport `json:"evidence_report"`
	Estimate       cost.Estimate           `json:"estimate"`
	Preview        bool                    `json:"preview"`
}
type inspectRecipeResult struct {
	Recipe         index.Recipe            `json:"recipe"`
	Evidence       []index.Nugget          `json:"evidence"`
	Outputs        []index.RefineryOutput  `json:"outputs"`
	Runs           []index.RefineryRun     `json:"runs"`
	Estimate       cost.Estimate           `json:"estimate"`
	EvidenceReport refinery.EvidenceReport `json:"evidence_report"`
}
type evidenceReviewResult struct {
	Recipe         index.Recipe            `json:"recipe"`
	EvidenceReport refinery.EvidenceReport `json:"evidence_report"`
}
type productionResult struct {
	Recipe         index.Recipe           `json:"recipe"`
	Run            index.RefineryRun      `json:"run"`
	Outputs        []index.RefineryOutput `json:"outputs"`
	ReviewRequired bool                   `json:"review_required"`
}
type inspectOutputResult struct {
	Output        index.RefineryOutput `json:"output"`
	Body          string               `json:"body"`
	Provenance    string               `json:"provenance"`
	ContentDigest string               `json:"content_digest"`
}
type reviewOutputResult struct {
	Output        index.RefineryOutput `json:"output"`
	ContentDigest string               `json:"content_digest"`
}
type exportOutputResult struct {
	Output         index.RefineryOutput `json:"output"`
	Path           string               `json:"path"`
	ProvenancePath string               `json:"provenance_path"`
	Destination    string               `json:"destination"`
}
type renderOutputResult struct {
	OutputID       string `json:"output_id"`
	Format         string `json:"format"`
	Path           string `json:"path"`
	Bytes          int    `json:"bytes"`
	Slides         int    `json:"slides"`
	SourceDigest   string `json:"source_digest"`
	RenderedDigest string `json:"rendered_digest"`
	ProvenancePath string `json:"provenance_path"`
	ReviewState    string `json:"review_state"`
}
type evidenceListResult struct {
	Evidence          []index.Nugget `json:"evidence"`
	Limit             int            `json:"limit"`
	Offset            int            `json:"offset"`
	PossiblyTruncated bool           `json:"possibly_truncated"`
}
type recipeListResult struct {
	Recipes           []index.Recipe `json:"recipes"`
	Limit             int            `json:"limit"`
	PossiblyTruncated bool           `json:"possibly_truncated"`
}

func workflowResultSchema(id, cap string) json.RawMessage {
	kinds := map[string]reflect.Type{
		"recipes.compose": shape[productionResult](), "recipes.produce": shape[productionResult](),
		"evidence.list": shape[evidenceListResult](), "recipes.list": shape[recipeListResult](),
		"recipes.inspect": shape[inspectRecipeResult](), "recipes.preview": shape[designResult](), "recipes.design": shape[designResult](),
		"recipes.update": shape[recipeResult](), "recipes.evidence": shape[evidenceReviewResult](),
		"outputs.inspect": shape[inspectOutputResult](), "outputs.review": shape[reviewOutputResult](),
		"outputs.export": shape[exportOutputResult](), "outputs.render": shape[renderOutputResult](),
	}
	return typedSchema(id, kinds[cap])
}
