package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestKernelHistorySurvivesReopen(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state")
	key := "sk_v1_" + strings.Repeat("0", 64)
	store, err := openKernelHistory(state)
	if err != nil {
		t.Fatal(err)
	}
	store.AddMessage(key, "user", "fresh history")
	if err := store.Save(key); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = openKernelHistory(state)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if got := store.GetHistory(key); len(got) != 1 || got[0].Content != "fresh history" {
		t.Fatalf("history did not survive reopen: %+v", got)
	}
}
