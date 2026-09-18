// Package editorial validates and persists the host agent's editorial judgment.
// It owns neither a model runtime nor a second production/approval lifecycle.
package editorial

import "time"

const (
	Schema           = "xibodev.midden.editorial/v1"
	MaxEvidence      = 300
	MaxSources       = 25
	MaxDocumentBytes = 512 * 1024
)

type Source struct {
	Tool      string `json:"tool" enum:"copilot,claude,opencode"`
	SessionID string `json:"session_id"`
}

type Project struct {
	Schema         string      `json:"schema"`
	ID             string      `json:"id"`
	Title          string      `json:"title"`
	Goal           string      `json:"goal"`
	Revision       int         `json:"revision"`
	Sources        []Source    `json:"sources"`
	EvidenceIDs    []string    `json:"evidence_ids"`
	EvidenceDigest string      `json:"evidence_digest"`
	Analysis       *Analysis   `json:"analysis"`
	AnalysisStale  bool        `json:"analysis_stale"`
	Selections     []Selection `json:"selections"`
	CreatedAt      time.Time   `json:"created_at"`
	UpdatedAt      time.Time   `json:"updated_at"`
}

type Analysis struct {
	Arcs          []Arc         `json:"arcs"`
	Decisions     []Decision    `json:"decisions"`
	Claims        []Claim       `json:"claims"`
	Assets        []Asset       `json:"assets"`
	Gaps          []Gap         `json:"gaps"`
	Opportunities []Opportunity `json:"opportunities"`
	Chapters      []Chapter     `json:"chapters"`
}

type Arc struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Summary     string   `json:"summary"`
	EvidenceIDs []string `json:"evidence_ids"`
}

type Decision struct {
	ID          string   `json:"id"`
	Statement   string   `json:"statement"`
	Rationale   string   `json:"rationale"`
	Status      string   `json:"status" enum:"proposed,accepted,superseded,rejected,uncertain"`
	EvidenceIDs []string `json:"evidence_ids"`
	Supersedes  []string `json:"supersedes,omitempty"`
}

type Claim struct {
	ID               string   `json:"id"`
	Text             string   `json:"text"`
	Status           string   `json:"status" enum:"supported,contested,unverified"`
	SupportingIDs    []string `json:"supporting_ids"`
	ContradictingIDs []string `json:"contradicting_ids,omitempty"`
}

type Asset struct {
	ID         string `json:"id"`
	Label      string `json:"label"`
	Kind       string `json:"kind" enum:"image,code,document,diagram,video,other"`
	EvidenceID string `json:"evidence_id"`
	// Locator is an evidence-relative reference, never a path to read implicitly.
	Locator      string `json:"locator"`
	Availability string `json:"availability" enum:"referenced,missing"`
	Cluster      string `json:"cluster,omitempty"`
}

type Gap struct {
	ID          string   `json:"id"`
	Detail      string   `json:"detail"`
	Status      string   `json:"status" enum:"open,disclosed,resolved"`
	ClaimIDs    []string `json:"claim_ids,omitempty"`
	EvidenceIDs []string `json:"evidence_ids,omitempty"`
}

type Opportunity struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Hook        string   `json:"hook"`
	Audience    string   `json:"audience"`
	Purpose     string   `json:"purpose"`
	Formats     []string `json:"formats"`
	ArcIDs      []string `json:"arc_ids"`
	ClaimIDs    []string `json:"claim_ids"`
	DecisionIDs []string `json:"decision_ids,omitempty"`
	AssetIDs    []string `json:"asset_ids,omitempty"`
	GapIDs      []string `json:"gap_ids,omitempty"`
	Rationale   string   `json:"rationale"`
	Effort      string   `json:"effort" enum:"small,medium,large"`
	Risks       []string `json:"risks"`
}

type Chapter struct {
	ID            string   `json:"id"`
	Title         string   `json:"title"`
	OpportunityID string   `json:"opportunity_id"`
	DependsOn     []string `json:"depends_on,omitempty"`
	Status        string   `json:"status" enum:"planned,drafting,review,complete"`
	OutputID      string   `json:"output_id,omitempty"`
	Notes         string   `json:"notes,omitempty"`
}

type Selection struct {
	OpportunityID    string `json:"opportunity_id"`
	AnalysisRevision int    `json:"analysis_revision"`
	RecipeID         string `json:"recipe_id"`
}

type CreateRequest struct {
	Title       string   `json:"title"`
	Goal        string   `json:"goal"`
	Sources     []Source `json:"sources"`
	EvidenceIDs []string `json:"evidence_ids"`
}

type UpdateRequest struct {
	ProjectID        string   `json:"project_id"`
	ExpectedRevision int      `json:"expected_revision"`
	Title            string   `json:"title,omitempty"`
	Goal             string   `json:"goal,omitempty"`
	Sources          []Source `json:"sources"`
	EvidenceIDs      []string `json:"evidence_ids"`
}

type SelectRequest struct {
	ProjectID        string   `json:"project_id"`
	ExpectedRevision int      `json:"expected_revision"`
	OpportunityID    string   `json:"opportunity_id"`
	OutputKinds      []string `json:"output_kinds"`
	AcknowledgeGaps  bool     `json:"acknowledge_gaps,omitempty"`
}
