package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mekjr1/midden/internal/seed"
)

type seedCLIInput struct {
	SelectionApproved    bool                `json:"selection_approved"`
	Goal                 string              `json:"goal"`
	Title                string              `json:"title,omitempty"`
	Summary              string              `json:"summary,omitempty"`
	KeyPoints            []string            `json:"key_points,omitempty"`
	SuggestedOutputTypes []string            `json:"suggested_output_types,omitempty"`
	Brief                string              `json:"brief,omitempty"`
	Evidence             []seed.SeedEvidence `json:"evidence,omitempty"`
	Sources              []seed.SeedSource   `json:"sources,omitempty"`
	Attach               []string            `json:"attach,omitempty"`
}

type seedCLIResult struct {
	Schema         string `json:"schema"`
	Path           string `json:"path"`
	ManifestPath   string `json:"manifest_path"`
	EvidenceDigest string `json:"evidence_digest"`
	EvidenceCount  int    `json:"evidence_count"`
	ReviewState    string `json:"review_state"`
	ModelUsed      bool   `json:"model_used"`
}

func cmdSeed(args []string) error {
	if len(args) == 0 || args[0] != "create" {
		return fmt.Errorf("usage: midden seed create --input <absolute-json> --out <absolute-directory>")
	}
	fs := flag.NewFlagSet("seed create", flag.ExitOnError)
	inputPath := fs.String("input", "", "absolute path to approved seed input JSON")
	outDir := fs.String("out", "", "absolute seed output directory")
	if err := fs.Parse(reorderArgs(fs, args[1:])); err != nil {
		return err
	}
	if !filepath.IsAbs(*inputPath) || !filepath.IsAbs(*outDir) {
		return fmt.Errorf("--input and --out must both be absolute paths")
	}
	if _, err := os.Stat(*outDir); err == nil {
		return fmt.Errorf("output directory already exists: %s", *outDir)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect output directory: %w", err)
	}

	raw, err := os.ReadFile(*inputPath)
	if err != nil {
		return fmt.Errorf("read seed input: %w", err)
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	var in seedCLIInput
	if err := dec.Decode(&in); err != nil {
		return fmt.Errorf("decode seed input: %w", err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("decode seed input: trailing JSON content")
	}
	if !in.SelectionApproved {
		return fmt.Errorf("seed input must explicitly set selection_approved to true")
	}
	if strings.TrimSpace(in.Goal) == "" {
		return fmt.Errorf("seed input goal is required")
	}
	for _, path := range in.Attach {
		if !filepath.IsAbs(path) {
			return fmt.Errorf("attachment path must be absolute: %q", path)
		}
	}

	manifest, err := seed.WriteSeed(*outDir, seed.SeedInput{
		Goal:                 in.Goal,
		Title:                in.Title,
		Summary:              in.Summary,
		KeyPoints:            in.KeyPoints,
		SuggestedOutputTypes: in.SuggestedOutputTypes,
		Brief:                in.Brief,
		Evidence:             in.Evidence,
		Sources:              in.Sources,
		Attach:               in.Attach,
	})
	if err != nil {
		return err
	}
	return emitJSON(seedCLIResult{
		Schema:         manifest.Schema,
		Path:           *outDir,
		ManifestPath:   filepath.Join(*outDir, seed.SeedManifestFile),
		EvidenceDigest: manifest.EvidenceDigest,
		EvidenceCount:  manifest.EvidenceCount,
		ReviewState:    seed.ReviewUnreviewed,
		ModelUsed:      false,
	})
}
