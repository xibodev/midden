package create

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Bundle returns the exact current source, its provenance, and any verified
// rendering. Downloading a draft does not mark it reviewed or published.
func (w Workflow) Bundle(id string) ([]byte, error) {
	o, err := w.DB.RefineryOutput(id)
	if err != nil {
		return nil, err
	}
	source, err := w.OwnedFile(o.Path)
	if err != nil {
		return nil, err
	}
	provenance, err := w.OwnedFile(o.ProvenancePath)
	if err != nil {
		return nil, err
	}
	paths := []string{source, provenance}
	for _, format := range []string{"pptx", "html"} {
		if delivery, err := w.DeliveryPath(id, format); err == nil {
			paths = append(paths, delivery, delivery+".provenance.json")
		}
	}
	var buffer bytes.Buffer
	z := zip.NewWriter(&buffer)
	files := []map[string]any{}
	total := int64(0)
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		total += info.Size()
		if total > 32<<20 {
			return nil, fmt.Errorf("delivery bundle exceeds 32 MiB")
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		name := filepath.Base(path)
		entry, err := z.Create(name)
		if err != nil {
			return nil, err
		}
		if _, err = entry.Write(raw); err != nil {
			return nil, err
		}
		files = append(files, map[string]any{"name": name, "bytes": len(raw), "sha256": Digest(raw)})
	}
	manifest, err := json.MarshalIndent(map[string]any{"output_id": id, "title": o.Title, "review_state": o.Status, "published": false, "files": files}, "", "  ")
	if err != nil {
		return nil, err
	}
	entry, err := z.Create("delivery-manifest.json")
	if err != nil {
		return nil, err
	}
	if _, err = entry.Write(manifest); err != nil {
		return nil, err
	}
	if err = z.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}
