package adapter

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"

	"github.com/mekjr1/midden/internal/assay"
)

func readFileView(path string, scanner *assay.Scanner, observe func([]byte) bool) (*assay.Manifest, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	limit := info.Size()
	if view := scanner.ViewBoundary(); view != nil {
		if view.Kind != "file-prefix-v1" {
			return nil, fmt.Errorf("unsupported saved file view; prepare a new orientation")
		}
		if info.Size() < view.Bytes {
			return nil, fmt.Errorf("source view was truncated; the saved prefix is no longer available")
		}
		limit = view.Bytes
	}
	digest := sha256.New()
	reader := io.TeeReader(io.LimitReader(f, limit), digest)
	if err = eachLineReader(reader, observe); err != nil {
		return nil, err
	}
	m := scanner.Manifest()
	m.SourceDigest = "sha256:" + hex.EncodeToString(digest.Sum(nil))
	m.SourceView = assay.SourceView{Kind: "file-prefix-v1", Bytes: limit, Records: m.TotalRecords}
	return m, nil
}
