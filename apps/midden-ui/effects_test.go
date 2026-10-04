package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCoreEffectsFollowParsedArguments(t *testing.T) {
	view := "v-" + strings.Repeat("a", 64)
	record := "claude:s:1@0-10"
	cases := []struct {
		name string
		args []string
		want coreEffect
	}{
		{"list", []string{"ls", "--json"}, effectReadOnly},
		{"list alias", []string{"list", "--all", "--days", "7"}, effectReadOnly},
		{"find", []string{"find", "zero", "--scan=10", "--json"}, effectReadOnly},
		{"show", []string{"show", "--tool", "claude", "--session", "s", "--json"}, effectReadOnly},
		{"usage", []string{"usage", "--tool=claude", "--session=s"}, effectReadOnly},
		{"resume", []string{"resume", "s"}, effectReadOnly},
		{"help command", []string{"help"}, effectReadOnly},
		{"help flag command", []string{"--help"}, effectReadOnly},
		{"version", []string{"version"}, effectReadOnly},
		{"brief", []string{"brief", "--tool", "claude", "--session", "s", "--json"}, effectReadOnly},
		{"brief handoff", []string{"brief", "--tool", "claude", "--session", "s", "--handoff"}, effectWritesCache},
		{"brief handoff attached", []string{"brief", "--session=s", "--handoff=true"}, effectWritesCache},
		{"read pinned view", []string{"read", "--view", view, "--json"}, effectReadOnly},
		{"read pinned view attached", []string{"read", "--view=" + view, "--offset=24", "--limit=24"}, effectReadOnly},
		{"read pinned view context without records", []string{"read", "--view", view, "--before", "1"}, effectReadOnly},
		{"read view records", []string{"read", "--view", view, "--record", record}, effectWritesCache},
		{"read view records attached", []string{"read", "--record=" + record, "--view=" + view}, effectWritesCache},
		{"read view records single dash", []string{"read", "-view", view, "-record", record}, effectWritesCache},
		{"open exact source", []string{"read", "--tool", "claude", "--session", "s", "--json"}, effectWritesCache},
		{"read view to a file", []string{"read", "--view", view, "--out", "notes/view.json"}, effectWritesWorkspace},
		{"read view to a file attached", []string{"read", "--view", view, "--out=notes/view.json"}, effectWritesWorkspace},
		{"search", []string{"search", "zero value", "--view", view, "--json"}, effectWritesCache},
		{"assets of a view", []string{"assets", "--view", view, "--json"}, effectReadOnly},
		{"assets of an exact source", []string{"assets", "--tool", "claude", "--session", "s"}, effectWritesCache},
		{"assets copied out", []string{"assets", "--view", view, "--out", "assets"}, effectWritesWorkspace},
		{"assay", []string{"assay", "--session", "s", "--json"}, effectWritesCache},
		{"collect", []string{"collect", "--view", view, "--out", "sources", "--json"}, effectWritesWorkspace},
		{"collect without destination", []string{"collect", "--view", view}, effectWritesWorkspace},
		{"collection inspect", []string{"collection", "inspect", "sources", "--json"}, effectReadOnly},
		{"collection read", []string{"collection", "read", "sources", "--limit", "5"}, effectReadOnly},
		{"collection search", []string{"collection", "search", "sources", "--query", "zero"}, effectReadOnly},
		{"collection verify", []string{"collection", "verify", "sources", "--record", record, "--quote", "zero"}, effectReadOnly},
		{"collection select", []string{"collection", "select", "sources", "--record", record, "--out", "subset"}, effectWritesWorkspace},
		{"collection merge", []string{"collection", "merge", "a", "b", "--out", "merged"}, effectWritesWorkspace},
		{"collection export", []string{"collection", "export", "sources", "--format", "markdown", "--out", "notes.md"}, effectWritesWorkspace},
		{"collection usage", []string{"collection", "--help"}, effectReadOnly},
		{"out on any command", []string{"ls", "--out", "listing.json"}, effectWritesWorkspace},
		{"help does not run the command", []string{"collect", "--help"}, effectReadOnly},
		{"help never hides an output", []string{"collect", "-h", "--out", "sources"}, effectWritesWorkspace},
		{"query value spelled like out", []string{"collection", "search", "sources", "--query", "--out"}, effectReadOnly},
		{"filter value spelled like record", []string{"read", "--view", view, "--repo", "--record"}, effectReadOnly},
		{"view value spelled like help", []string{"collect", "--view", "--help"}, effectWritesWorkspace},
	}
	for _, tc := range cases {
		got, err := classifyCoreArgs(tc.args)
		if err != nil {
			t.Errorf("%s: %v rejected: %v", tc.name, tc.args, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s: %v classified %s, want %s", tc.name, tc.args, got, tc.want)
		}
	}
}

func TestCoreEffectsRejectUnparseableArguments(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"prune", "--execute"},
		{"scan"},
		{"doctor"},
		{"read", "--state", "elsewhere"},
		{"read", "--claude-root=elsewhere"},
		{"read", "--sources-only"},
		{"collection"},
		{"collection", "purge", "sources"},
		{"collection", "--json", "inspect", "sources"},
		{"read", "--", "--state=elsewhere"},
		{"read", "--unknown"},
		{"read", "--view"},
		{"read", "-t", "claude"},
		{"read", "--h=1"},
		{"resume", "s", "--with", "continue"},
		{"find", strings.Repeat("x", coreMaxArgBytes+1)},
	} {
		if effect, err := classifyCoreArgs(args); err == nil {
			t.Errorf("%v classified %s; want an error", args, effect)
		}
	}
}

func TestSharedValidatorChecksDestinationsAndCollectionPaths(t *testing.T) {
	opts := testOptions(t)
	opts.State = filepath.Join(opts.Workspace, "host-state")
	app, err := NewApp(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	if err = os.MkdirAll(filepath.Join(opts.Workspace, "taken"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"resume", "0b5c2d1e"},
		{"find", "state=ready", "--json"},
		{"collection", "inspect", "sources/a", "--json"},
		{"collect", "--view", "v", "--out", "sources/new", "--json"},
		{"collection", "export", "taken", "--out=notes/new.md", "--format", "markdown"},
	} {
		if err := app.validateCoreArgs(args); err != nil {
			t.Errorf("%v rejected: %v", args, err)
		}
	}
	for _, args := range [][]string{
		{"collect", "--view", "v", "--out", "taken"},
		{"collect", "--view", "v", "--out", "../outside"},
		{"collect", "--view", "v", "--out", filepath.Join(t.TempDir(), "elsewhere")},
		{"read", "--view", "v", "--out", ".git/view.json"},
		{"read", "--view", "v", "--out", "sessions/view.json"},
		{"read", "--view", "v", "--out", "host-state/view.json"},
		{"read", "--view", "v", "--out="},
		{"collection", "inspect", "../outside"},
		{"collection", "merge", "a", ".hidden", "--out", "merged"},
	} {
		if err := app.validateCoreArgs(args); err == nil {
			t.Errorf("%v accepted", args)
		}
	}
}
