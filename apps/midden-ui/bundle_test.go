package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBundleMountKeepsCanonicalBytesAndLoadsAdvertisedNames(t *testing.T) {
	source, err := filepath.Abs(filepath.Join("..", "..", "bundles"))
	if err != nil {
		t.Fatal(err)
	}
	state := t.TempDir()
	target, skills, err := mountBundle(source, state)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 4 {
		t.Fatal("outcome skills missing")
	}
	for _, name := range []string{"article", "presentation", "investigation", "long-form"} {
		canonical, err := os.ReadFile(filepath.Join(source, name, "SKILL.md"))
		if err != nil {
			t.Fatal(err)
		}
		mounted, err := os.ReadFile(filepath.Join(target, "midden-"+name, "SKILL.md"))
		if err != nil {
			t.Fatal(err)
		}
		if string(canonical) != string(mounted) {
			t.Fatal("bundle guidance was rewritten")
		}
	}
	readme, err := os.ReadFile(filepath.Join(target, "midden-shared", "sources.md"))
	if err != nil || !strings.Contains(string(readme), "Source") {
		t.Fatal("shared resources missing")
	}
}

func TestModifiedMountedGuidanceIsNotSilentlyTrustedOnRestart(t *testing.T) {
	source, err := filepath.Abs(filepath.Join("..", "..", "bundles"))
	if err != nil {
		t.Fatal(err)
	}
	state := t.TempDir()
	target, _, err := mountBundle(source, state)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(target, "midden-article", "SKILL.md"), []byte("---\nname: midden-article\ndescription: changed\n---\nChanged instructions."), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err = mountBundle(source, state); err == nil {
		t.Fatal("modified mounted instructions were trusted under the canonical digest")
	}
}
