package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestReadFileOutputCannotWriteInsideASourceStore(t *testing.T) {
	store, view := scopeFixture(t)
	destination := filepath.Join(store, "new-output", "view.json")
	var output bytes.Buffer
	if runMaterial("read", []string{"--view", view, "--out", destination, "--json"}, &output) == nil {
		t.Fatal("read output was written inside source store")
	}
	if _, err := os.Stat(filepath.Dir(destination)); !os.IsNotExist(err) {
		t.Fatal("rejected read output created a source directory")
	}
}
