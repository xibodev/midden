package main

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestAssetInventoryAcceptsAnExactSourceSelector(t *testing.T) {
	_, _ = scopeFixture(t)
	var output bytes.Buffer
	if err := runMaterial("assets", []string{"--tool", "claude", "--session", "fixture", "--json"}, &output); err != nil {
		t.Fatal(err)
	}
	var result struct {
		View  string `json:"view_id"`
		Count int    `json:"asset_count"`
	}
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.View == "" || result.Count != 0 {
		t.Fatal("asset inventory result has the wrong source")
	}
}
