package index

import (
	"os"
	"path/filepath"
	"strings"
)

// BackfillArtifacts records content files that exist on disk but not in the
// library.
//
// Documents produced before the module face recorded artifact rows are real
// files a user paid for, yet invisible to every listing. Deleting them or
// leaving them unlisted are both wrong: the honest repair is to adopt them.
//
// It lives in the core, not in a face, because the CLI (`midden artifacts`)
// and the standalone UI (/api/artifacts) read the same table -- a repair
// reachable from only one of them would leave the other still wrong.
//
// Adopted rows carry no nugget ids and no model: the evidence behind a file
// written before it was recorded is genuinely unknown, and inventing a
// provenance link would be worse than admitting the gap. Front matter supplies
// the kind and title when present, because that is what the producer actually
// wrote, rather than a guess from the filename.
func (d *DB) BackfillArtifacts(dir string) (int, error) {
	contentDir := filepath.Join(dir, "content")
	entries, err := os.ReadDir(contentDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}

	known := map[string]bool{}
	existing, err := d.Artifacts(0)
	if err != nil {
		return 0, err
	}
	for _, a := range existing {
		if a.Path != "" {
			known[strings.ToLower(filepath.Clean(a.Path))] = true
		}
	}

	adopted := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		abs := filepath.Join(contentDir, e.Name())
		if known[strings.ToLower(filepath.Clean(abs))] {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		kind, title := frontMatterKindTitle(abs)
		if title == "" {
			title = strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		}
		if kind == "" {
			kind = "content"
		}
		if err := d.PutArtifact(Artifact{
			Kind:      kind,
			Title:     title,
			Path:      abs,
			CreatedAt: info.ModTime(),
		}); err != nil {
			return adopted, err
		}
		adopted++
	}
	return adopted, nil
}

// frontMatterKindTitle reads the recipe and title a producer wrote into a
// document's front matter. Absence is normal: a hand-written file has none.
func frontMatterKindTitle(path string) (kind, title string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", ""
	}
	body := string(raw)
	if !strings.HasPrefix(body, "---") {
		return "", ""
	}
	end := strings.Index(body[3:], "\n---")
	if end < 0 {
		return "", ""
	}
	for _, line := range strings.Split(body[3:end+3], "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		v = strings.Trim(strings.TrimSpace(v), `"`)
		switch strings.TrimSpace(k) {
		case "midden_recipe":
			kind = strings.TrimPrefix(v, "module-")
		case "title":
			title = v
		}
	}
	return kind, title
}
