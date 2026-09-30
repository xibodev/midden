package main

import (
	"os"
	"path/filepath"
	"testing"
)

func testOptions(t *testing.T) Options {
	t.Helper()
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	return Options{Workspace: workspace, State: filepath.Join(root, "state"), Core: "synthetic-core"}
}
