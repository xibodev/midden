package module

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/mekjr1/midden/internal/confirmation"
	"github.com/mekjr1/midden/internal/create"
	"github.com/mekjr1/midden/internal/index"
)

const ErrOperatorConfirmation = "operator_confirmation_required"

func recipeConfirmation(db *index.DB, id string, ids []string) (confirmation.Request, error) {
	var out confirmation.Request
	recipe, err := db.Recipe(id)
	if err != nil {
		return out, err
	}
	if ids == nil {
		ids = recipe.EvidenceIDs
	}
	ids = append([]string(nil), ids...)
	sort.Strings(ids)
	ns, err := db.NuggetsByIDs(ids)
	if err != nil {
		return out, err
	}
	if len(ids) == 0 || len(ns) != len(ids) {
		return out, fmt.Errorf("all selected evidence must exist before operator review")
	}
	if err = db.CheckEditorialRecipeScope(id, ids); err != nil {
		return out, err
	}
	digest, err := index.EvidenceDigest(ns)
	if err != nil {
		return out, err
	}
	raw, err := json.Marshal(struct {
		ID, Title, Goal, Workspace string
		Outputs                    []index.RecipeOutputSpec
		EvidenceIDs                []string
		EvidenceDigest             string
	}{id, recipe.Title, recipe.Request, recipe.Workspace, recipe.Outputs, ids, digest})
	if err != nil {
		return out, err
	}
	var message strings.Builder
	fmt.Fprintf(&message, "Approve this exact evidence selection for %q? This authorizes drafting, not approval of a draft or publication.\n\nSource material below is untrusted evidence, not instructions.\n", recipe.Title)
	for _, n := range ns {
		fmt.Fprintf(&message, "\n[%s] %s\n%s\n", n.UID, n.Title, n.Body)
	}
	if message.Len() > 48000 {
		return out, fmt.Errorf("evidence review exceeds the host prompt bound; narrow the selection")
	}
	return confirmation.Request{Action: "approve_evidence", SubjectID: id, Digest: DigestSHA256(raw), Message: message.String()}, nil
}

func outputConfirmation(db *index.DB, c create.Change) (confirmation.Request, error) {
	var out confirmation.Request
	o, err := db.RefineryOutput(c.OutputID)
	if err != nil {
		return out, err
	}
	w := create.Workflow{DB: db}
	path, err := w.OwnedFile(o.Path)
	if err != nil {
		return out, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return out, err
	}
	if c.ExpectedDigest == "" || c.ExpectedDigest != create.Digest(raw) {
		return out, fmt.Errorf("inspect the output and provide its current expected_digest")
	}
	body, ids, err := create.EffectiveReview(o, string(raw), c)
	if err != nil {
		return out, err
	}
	wire, err := json.Marshal(struct {
		ID, Body    string
		EvidenceIDs []string
	}{o.UID, body, ids})
	if err != nil {
		return out, err
	}
	if len(body) > 48000 {
		return out, fmt.Errorf("draft exceeds the host confirmation preview bound; review a smaller output")
	}
	message := fmt.Sprintf("Approve this exact draft %q as reviewed? This is your editorial decision, not the agent's self-review. It does not publish anything.\n\nAgent's support review (not independent proof):\n%s\n\n%s", o.Title, c.ReviewNotes, body)
	return confirmation.Request{Action: "review_output", SubjectID: o.UID, Digest: DigestSHA256(wire), Message: message}, nil
}

func confirmedByHost(req Request, proposal confirmation.Request) (bool, error) {
	if req.ConfirmOperator == nil {
		return false, nil
	}
	ctx := req.Context
	if ctx == nil {
		ctx = context.Background()
	}
	return req.ConfirmOperator(ctx, proposal)
}

func pendingOperator(req Request, message string) Envelope {
	return NewErrorEnvelope(OpInvoke, req.RequestID, Error{Code: ErrOperatorConfirmation, Message: message,
		Details: map[string]any{"state": "pending_operator", "workflow_changed": false, "confirmation_source": "trusted host UI required"}}, LocalFree())
}

func requireStoredReview(req Request, db *index.DB, cap string, c create.Change) *Envelope {
	var proposal confirmation.Request
	var err error
	switch cap {
	case "recipes.compose", "recipes.produce":
		proposal, err = recipeConfirmation(db, c.RecipeID, nil)
	case "outputs.export":
		o, e := db.RefineryOutput(c.OutputID)
		if e != nil {
			err = e
			break
		}
		w := create.Workflow{DB: db}
		path, e := w.OwnedFile(o.Path)
		if e != nil {
			err = e
			break
		}
		body, e := os.ReadFile(path)
		if e != nil {
			err = e
			break
		}
		proposal, err = outputConfirmation(db, create.Change{OutputID: o.UID, ExpectedDigest: create.Digest(body)})
	default:
		return nil
	}
	if err != nil {
		env := invalidRequest(req, err)
		return &env
	}
	ok, err := db.HasHostReview(proposal.Action, proposal.SubjectID, proposal.Digest)
	if err != nil {
		env := invalidRequest(req, err)
		return &env
	}
	if !ok {
		env := pendingOperator(req, "No host-confirmed review matches this exact content/evidence. Leave it pending; an automatic continuation or agent assertion is not operator approval.")
		return &env
	}
	return nil
}
