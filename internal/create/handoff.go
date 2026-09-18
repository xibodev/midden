package create

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/mekjr1/midden/internal/index"
)

const HandoffSchema = "xibodev.midden.handoff/v1"

type HandoffOutput struct {
	ID             string `json:"id"`
	Kind           string `json:"kind"`
	Path           string `json:"path"`
	ProvenancePath string `json:"provenance_path"`
	ContentDigest  string `json:"content_digest"`
}

type HandoffManifest struct {
	Schema          string          `json:"schema"`
	Target          string          `json:"target"`
	Title           string          `json:"title"`
	ProjectID       string          `json:"project_id"`
	ProjectRevision int             `json:"project_revision"`
	ReviewState     string          `json:"review_state"`
	EditorialPath   string          `json:"editorial_path"`
	EditorialDigest string          `json:"editorial_digest"`
	Outputs         []HandoffOutput `json:"outputs"`
	Warnings        []string        `json:"warnings"`
}

type HandoffResult struct {
	Root           string `json:"root"`
	Path           string `json:"path"`
	ManifestPath   string `json:"manifest_path"`
	ManifestDigest string `json:"manifest_digest"`
	Target         string `json:"target"`
	ReviewState    string `json:"review_state"`
}

type ReviewedSnapshot struct {
	Output           index.RefineryOutput
	Body, Provenance []byte
}

// ReviewedSource verifies content, not just the saved output status.
func (w Workflow) ReviewedSource(id string) (ReviewedSnapshot, error) {
	var result ReviewedSnapshot
	o, err := w.DB.RefineryOutput(id)
	if err != nil {
		return result, err
	}
	if o.Status != "reviewed" && o.Status != "exported" {
		return result, fmt.Errorf("review output %s before handoff", id)
	}
	source, err := w.OwnedFile(o.Path)
	if err != nil {
		return result, err
	}
	prov, err := w.OwnedFile(o.ProvenancePath)
	if err != nil {
		return result, err
	}
	for _, path := range []string{source, prov} {
		info, e := os.Stat(path)
		if e != nil {
			return result, e
		}
		if info.Size() > 2<<20 {
			return result, fmt.Errorf("handoff source exceeds 2 MiB bound")
		}
	}
	body, err := os.ReadFile(source)
	if err != nil {
		return result, err
	}
	raw, err := os.ReadFile(prov)
	if err != nil {
		return result, err
	}
	var metadata struct {
		ContentDigest string `json:"content_digest"`
	}
	if err = json.Unmarshal(raw, &metadata); err != nil {
		return result, err
	}
	if metadata.ContentDigest == "" || metadata.ContentDigest != Digest(body) {
		return result, fmt.Errorf("reviewed bytes changed; review again before handoff")
	}
	return ReviewedSnapshot{Output: o, Body: body, Provenance: raw}, nil
}

// BuildHandoff writes a local portable source bundle; it never runs a renderer,
// invokes a consumer, installs dependencies, or publishes content.
func (w Workflow) BuildHandoff(projectID string, revision int, title, target string, ids []string, editorial json.RawMessage) (HandoffResult, error) {
	var result HandoffResult
	switch target {
	case "markdown", "quarto", "pandoc", "d2", "openmontage":
	default:
		return result, fmt.Errorf("unsupported handoff target %q", target)
	}
	if len(ids) < 1 || len(ids) > 100 || !json.Valid(editorial) {
		return result, fmt.Errorf("handoff needs 1..100 outputs and editorial metadata")
	}
	snapshots := make([]ReviewedSnapshot, 0, len(ids))
	totalBytes := len(editorial)
	for _, id := range ids {
		snapshot, err := w.ReviewedSource(id)
		if err != nil {
			return result, err
		}
		switch target {
		case "quarto", "pandoc", "markdown":
			if snapshot.Output.Format != "markdown" && snapshot.Output.Format != "marp" {
				return result, fmt.Errorf("%s requires Markdown sources", target)
			}
		case "d2":
			if snapshot.Output.Format != "d2" {
				return result, fmt.Errorf("D2 handoff requires diagram source")
			}
		case "openmontage":
			if snapshot.Output.Kind != "video_brief" && snapshot.Output.Kind != "slides" {
				return result, fmt.Errorf("video handoff requires a reviewed video brief or slides")
			}
		}
		totalBytes += len(snapshot.Body) + len(snapshot.Provenance)
		if totalBytes > 16<<20 {
			return result, fmt.Errorf("handoff exceeds 16 MiB bound; select fewer outputs")
		}
		snapshots = append(snapshots, snapshot)
	}
	base := filepath.Join(filepath.Dir(w.DB.Path()), "handoffs")
	if err := w.checkWritePath(base); err != nil {
		return result, err
	}
	if err := os.MkdirAll(base, 0700); err != nil {
		return result, err
	}
	stage, err := os.MkdirTemp(base, ".pending-")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(stage)
	if err = os.Mkdir(filepath.Join(stage, "sources"), 0700); err != nil {
		return result, err
	}
	manifest := HandoffManifest{Schema: HandoffSchema, Target: target, Title: title, ProjectID: projectID, ProjectRevision: revision,
		ReviewState: "unreviewed", EditorialPath: "editorial.json", EditorialDigest: Digest(editorial), Outputs: []HandoffOutput{},
		Warnings: []string{"Sources were reviewed; this assembled handoff and any rendered result still require operator inspection.",
			"No consumer was invoked. No external transfer or publishing was performed. Credential redaction is not privacy clearance.",
			"Asset locators are references only. Recover, inspect and license assets separately before using them."}}
	evidenceIDs := []string{}
	for i, snapshot := range snapshots {
		ext := ".md"
		if snapshot.Output.Format == "d2" {
			ext = ".d2"
		}
		path := fmt.Sprintf("sources/%02d%s", i+1, ext)
		prov := path + ".provenance.json"
		if err = os.WriteFile(filepath.Join(stage, filepath.FromSlash(path)), snapshot.Body, 0600); err != nil {
			return result, err
		}
		if err = os.WriteFile(filepath.Join(stage, filepath.FromSlash(prov)), snapshot.Provenance, 0600); err != nil {
			return result, err
		}
		manifest.Outputs = append(manifest.Outputs, HandoffOutput{ID: snapshot.Output.UID, Kind: snapshot.Output.Kind, Path: path, ProvenancePath: prov, ContentDigest: Digest(snapshot.Body)})
		evidenceIDs = append(evidenceIDs, snapshot.Output.EvidenceIDs...)
	}
	if err = os.WriteFile(filepath.Join(stage, "editorial.json"), editorial, 0600); err != nil {
		return result, err
	}
	instructions := "# " + title + "\n\nLocal source handoff; not a finished publication. Inspect manifest.json and editorial.json.\n\n"
	switch target {
	case "quarto":
		config := "project:\n  type: book\nbook:\n  title: " + strconv.Quote(title) + "\n  chapters:\n"
		for _, o := range manifest.Outputs {
			config += "    - " + strconv.Quote(o.Path) + "\n"
		}
		config += "format:\n  html: default\n"
		if err = os.WriteFile(filepath.Join(stage, "_quarto.yml"), []byte(config), 0600); err != nil {
			return result, err
		}
		instructions += "With separately installed Quarto, run `quarto render` here, then inspect the book. These are the selected reviewed chapters, not proof the whole project is complete.\n"
	case "pandoc":
		instructions += "With separately installed Pandoc, combine the listed Markdown files in order into the intended format, then inspect the result. No command has been executed.\n"
	case "d2":
		instructions += "Render each sources/*.d2 file with separately installed D2, then inspect the diagrams. Source approval is not visual verification.\n"
	case "openmontage":
		instructions += "Give this folder to an operator-controlled OpenMontage workspace. Start from the reviewed scene/script sources, resolve editorial gaps and supply approved assets. This is a file-based production brief, not a native OpenMontage project or a rendered video. No dependency, provider, upload, or paid generation is authorized by this handoff.\n"
	default:
		instructions += "The ordered Markdown files and provenance are editable sources. Re-review changes before publication.\n"
	}
	if err = os.WriteFile(filepath.Join(stage, "README.md"), []byte(instructions), 0600); err != nil {
		return result, err
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return result, err
	}
	if err = os.WriteFile(filepath.Join(stage, "manifest.json"), raw, 0600); err != nil {
		return result, err
	}
	id := index.NewUID()
	dest := filepath.Join(base, id)
	if err = w.checkWritePath(dest); err != nil {
		return result, err
	}
	if err = os.Rename(stage, dest); err != nil {
		return result, err
	}
	if err = w.DB.PutArtifact(index.Artifact{UID: id, Kind: "handoff:" + target, Title: title, Path: filepath.Join(dest, "manifest.json"),
		Scope: "project:" + projectID, NuggetIDs: uniqueStrings(evidenceIDs), Model: "deterministic"}); err != nil {
		cleanupErr := os.RemoveAll(dest)
		if cleanupErr != nil {
			return result, fmt.Errorf("record handoff: %v; cleanup: %w", err, cleanupErr)
		}
		return result, err
	}
	relative := filepath.ToSlash(filepath.Join("handoffs", id))
	return HandoffResult{Root: "midden_home", Path: relative, ManifestPath: relative + "/manifest.json", ManifestDigest: "sha256:" + Digest(raw), Target: target, ReviewState: "unreviewed"}, nil
}
