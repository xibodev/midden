package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Contract tests run the real command line in a child process, so they see
// exactly what a caller sees: stdout, stderr and the exit code.
const runMainEnv = "MIDDEN_TEST_RUN_MAIN"

func TestMain(m *testing.M) {
	if os.Getenv(runMainEnv) == "1" {
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

type cliResult struct {
	stdout, stderr string
	code           int
}

func runCLI(t *testing.T, args ...string) cliResult {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Env = append(os.Environ(), runMainEnv+"=1")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	result := cliResult{stdout: stdout.String(), stderr: stderr.String()}
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		result.code = exit.ExitCode()
	case err != nil:
		t.Fatalf("midden %s: %v", strings.Join(args, " "), err)
	}
	return result
}

// Synthetic credentials in shapes the credential filter recognises, built at
// run time so no credential-shaped literal sits in the source. Each starts with
// a marker that appears nowhere else, so a clipped fragment is caught too.
var (
	synthToken        = "ghp_" + strings.Repeat("Z", 36)
	synthAccess       = "AKIA" + "SYNTHETICEXAMPLE"
	synthValue        = "qzx" + "assignedvalue42"
	credentialMarkers = []string{"ghp_", "AKIA", "qzx"}
)

// fixtureCwd is outside temporary directories, which noise rules hide.
const fixtureCwd = "/srv/midden-contract-fixture"

type contractFixture struct {
	home, work string
}

// newContractFixture isolates child processes: a synthetic Claude store, an
// empty profile and private state. No real store is ever read.
func newContractFixture(t *testing.T, populate bool) contractFixture {
	t.Helper()
	home := t.TempDir()
	store := filepath.Join(home, "claude")
	project := filepath.Join(store, "projects", "synthetic")
	work := filepath.Join(home, "work")
	for _, dir := range []string{project, work} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("MIDDEN_HOME", filepath.Join(home, "state"))
	t.Setenv("MIDDEN_CLAUDE_ROOT", store)
	t.Setenv("MIDDEN_COPILOT_ROOT", "")
	t.Setenv("MIDDEN_OPENCODE_DB", "")
	t.Setenv("NO_COLOR", "1")
	if !populate {
		return contractFixture{home: home, work: work}
	}
	writeTranscript(t, project, "contract-alpha",
		message("user", 1, map[string]any{"content": "Rotate the deploy token " + synthToken + " before the release"}),
		message("assistant", 2, map[string]any{
			"model": "synthetic-model",
			"usage": map[string]int{"input_tokens": 12, "output_tokens": 7},
			"content": []map[string]string{{"type": "text", "text": "The needle-contract setting uses access key " +
				synthAccess + ` and export API_KEY="` + synthValue + `" in the shell.`}},
		}),
		message("user", 3, map[string]any{"content": `Confirm the needle-contract rollout without echoing API_KEY="` + synthValue + `"`}),
		message("assistant", 4, map[string]any{"content": "Confirmed. Nothing else changed."}),
	)
	writeTranscript(t, project, "contract-beta",
		message("user", 5, map[string]any{"content": "Plan the synthetic importer cleanup"}),
		message("assistant", 6, map[string]any{"content": "Plan recorded; nothing was run."}),
	)
	// A session without conversation turns: its brief has no goal and no
	// recent turns.
	writeTranscript(t, project, "contract-quiet",
		map[string]any{"type": "summary", "summary": "Quiet synthetic summary", "timestamp": "2026-01-02T10:07:00Z"},
		message("system", 8, map[string]any{"content": "Synthetic bookkeeping."}),
	)
	return contractFixture{home: home, work: work}
}

func message(role string, minute int, body map[string]any) map[string]any {
	body["role"] = role
	return map[string]any{"type": role, "timestamp": fmt.Sprintf("2026-01-02T10:%02d:00Z", minute), "message": body}
}

func writeTranscript(t *testing.T, dir, id string, records ...map[string]any) {
	t.Helper()
	var data []byte
	for _, record := range records {
		record["sessionId"], record["cwd"] = id, fixtureCwd
		raw, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		data = append(append(data, raw...), '\n')
	}
	if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// contractCase is one command line. A successful case prints exactly one JSON
// document without a null anywhere; a failure prints nothing on stdout.
type contractCase struct {
	name    string
	args    []string // {name} expands from vars, including values captured by checks
	exit    int
	failure bool
	arrays  []string // dotted paths that must be JSON arrays; "" is the root
	check   func(t *testing.T, doc any)
}

func runContract(t *testing.T, cases []contractCase, vars map[string]string) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := make([]string, len(tc.args))
			for i, arg := range tc.args {
				for key, value := range vars {
					arg = strings.ReplaceAll(arg, "{"+key+"}", value)
				}
				args[i] = arg
			}
			res := runCLI(t, args...)
			assertNoCredential(t, res.stdout+res.stderr)
			if res.code != tc.exit {
				t.Fatalf("exit %d, want %d\nstdout: %s\nstderr: %s", res.code, tc.exit, res.stdout, res.stderr)
			}
			if tc.failure {
				if strings.TrimSpace(res.stdout) != "" {
					t.Fatalf("a failure printed to stdout: %s", res.stdout)
				}
				if strings.TrimSpace(res.stderr) == "" {
					t.Fatal("a failure gave no reason on stderr")
				}
				return
			}
			doc := decodeOne(t, res.stdout)
			assertNoNull(t, doc, "$")
			for _, path := range tc.arrays {
				if _, ok := lookup(t, doc, path).([]any); !ok {
					t.Errorf("%q is not a JSON array: %s", path, res.stdout)
				}
			}
			if tc.check != nil {
				tc.check(t, doc)
			}
		})
	}
}

func assertNoCredential(t *testing.T, output string) {
	t.Helper()
	for _, marker := range credentialMarkers {
		if strings.Contains(output, marker) {
			t.Fatalf("output carries a synthetic credential or a fragment of one:\n%s", output)
		}
	}
}

// decodeOne requires stdout to hold exactly one JSON document.
func decodeOne(t *testing.T, stdout string) any {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(stdout))
	decoder.UseNumber()
	var doc any
	if err := decoder.Decode(&doc); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout)
	}
	if _, err := decoder.Token(); err != io.EOF {
		t.Fatalf("stdout holds more than one JSON document:\n%s", stdout)
	}
	return doc
}

func assertNoNull(t *testing.T, value any, path string) {
	t.Helper()
	switch v := value.(type) {
	case nil:
		t.Errorf("%s is null", path)
	case map[string]any:
		for key, child := range v {
			assertNoNull(t, child, path+"."+key)
		}
	case []any:
		for i, child := range v {
			assertNoNull(t, child, fmt.Sprintf("%s[%d]", path, i))
		}
	}
}

// lookup resolves a dotted path; numeric parts index arrays.
func lookup(t *testing.T, doc any, path string) any {
	t.Helper()
	if path == "" {
		return doc
	}
	current := doc
	for _, part := range strings.Split(path, ".") {
		switch v := current.(type) {
		case map[string]any:
			child, ok := v[part]
			if !ok {
				t.Fatalf("missing %q in %v", path, doc)
			}
			current = child
		case []any:
			i, err := strconv.Atoi(part)
			if err != nil || i < 0 || i >= len(v) {
				t.Fatalf("missing %q in %v", path, doc)
			}
			current = v[i]
		default:
			t.Fatalf("missing %q in %v", path, doc)
		}
	}
	return current
}

func text(t *testing.T, doc any, path string) string {
	t.Helper()
	s, ok := lookup(t, doc, path).(string)
	if !ok {
		t.Fatalf("%q is not a string", path)
	}
	return s
}

func number(t *testing.T, doc any, path string) int64 {
	t.Helper()
	n, ok := lookup(t, doc, path).(json.Number)
	if !ok {
		t.Fatalf("%q is not a number", path)
	}
	v, err := n.Int64()
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func items(t *testing.T, doc any, path string) []any {
	t.Helper()
	list, ok := lookup(t, doc, path).([]any)
	if !ok {
		t.Fatalf("%q is not an array", path)
	}
	return list
}

func wantLen(path string, n int) func(*testing.T, any) {
	return func(t *testing.T, doc any) {
		t.Helper()
		if got := len(items(t, doc, path)); got != n {
			t.Errorf("%q has %d items, want %d", path, got, n)
		}
	}
}

func wantFiltered(t *testing.T, doc any, path string) {
	t.Helper()
	if s := text(t, doc, path); !strings.Contains(s, "ask operator") {
		t.Errorf("%q shows no filter placeholder: %q", path, s)
	}
}

var reconciledLists = []string{"reconciled.ghost_sessions", "reconciled.stale_manifests", "reconciled.orphan_manifests"}

func TestJSONContractOnSyntheticStore(t *testing.T) {
	fixture := newContractFixture(t, true)
	vars := map[string]string{
		"sources":  filepath.Join(fixture.work, "sources"),
		"selected": filepath.Join(fixture.work, "selected"),
		"combined": filepath.Join(fixture.work, "combined"),
		"export":   filepath.Join(fixture.work, "export.jsonl"),
	}
	runContract(t, []contractCase{
		{name: "ls", args: []string{"ls", "--json"}, arrays: []string{"sessions", "stores_read", "warnings"},
			check: func(t *testing.T, doc any) {
				wantLen("sessions", 3)(t, doc)
				for i := range 3 {
					if path := fmt.Sprintf("sessions.%d", i); text(t, doc, path+".id") == "contract-alpha" {
						wantFiltered(t, doc, path+".title")
					}
				}
			}},
		{name: "find hit", args: []string{"find", "needle-contract", "--json"}, arrays: []string{"hits"},
			check: func(t *testing.T, doc any) {
				wantLen("hits", 1)(t, doc)
				if text(t, doc, "hits.0.session.id") != "contract-alpha" || number(t, doc, "hits.0.matches") != 2 {
					t.Errorf("wrong hit: %v", doc)
				}
				if !strings.Contains(text(t, doc, "hits.0.excerpt"), "needle-contract") {
					t.Error("excerpt does not show the match")
				}
				wantFiltered(t, doc, "hits.0.excerpt")
				wantFiltered(t, doc, "hits.0.session.title")
				if number(t, doc, "scanned") != 3 || lookup(t, doc, "truncated") != false {
					t.Errorf("scan accounting: %v", doc)
				}
			}},
		{name: "find nothing", args: []string{"find", "absent-needle-phrase", "--json"}, arrays: []string{"hits"},
			check: wantLen("hits", 0)},
		{name: "show", args: []string{"show", "--tool", "claude", "--session", "contract-alpha", "--json"},
			check: func(t *testing.T, doc any) {
				if !strings.Contains(text(t, doc, "title"), "<GITHUB_TOKEN — ask operator>") {
					t.Errorf("title not filtered: %v", doc)
				}
			}},
		{name: "usage", args: []string{"usage", "--tool", "claude", "--session", "contract-alpha", "--json"},
			check: func(t *testing.T, doc any) {
				if number(t, doc, "turns") != 1 || text(t, doc, "model") != "synthetic-model" {
					t.Errorf("usage: %v", doc)
				}
			}},
		{name: "brief", args: []string{"brief", "--tool", "claude", "--session", "contract-alpha", "--json"}, arrays: []string{"recent"},
			check: func(t *testing.T, doc any) {
				wantLen("recent", 4)(t, doc)
				wantFiltered(t, doc, "session.title")
				wantFiltered(t, doc, "goal.text")
				wantFiltered(t, doc, "recent.1.text")
				wantFiltered(t, doc, "recent.2.text")
				if text(t, doc, "last_assistant.text") != "Confirmed. Nothing else changed." {
					t.Errorf("last assistant turn: %v", doc)
				}
			}},
		{name: "brief without turns", args: []string{"brief", "--tool", "claude", "--session", "contract-quiet", "--json"}, arrays: []string{"recent"},
			check: func(t *testing.T, doc any) {
				wantLen("recent", 0)(t, doc)
				if _, ok := doc.(map[string]any)["goal"]; ok {
					t.Error("a brief without turns reported a goal")
				}
			}},
		{name: "doctor", args: []string{"doctor", "--json"}, arrays: []string{"at_risk", "dead_workspaces", "live"},
			check: func(t *testing.T, doc any) {
				wantLen("at_risk", 0)(t, doc)
				wantLen("dead_workspaces", 3)(t, doc)
				for i := range 3 {
					if path := fmt.Sprintf("dead_workspaces.%d", i); text(t, doc, path+".id") == "contract-alpha" {
						wantFiltered(t, doc, path+".title")
					}
				}
			}},
		{name: "scan", args: []string{"scan", "--json"}, arrays: reconciledLists,
			check: func(t *testing.T, doc any) {
				if number(t, doc, "indexed") != 3 {
					t.Errorf("scan: %v", doc)
				}
			}},
		// A narrowed scan does not reconcile; its report still lists [].
		{name: "scan narrowed", args: []string{"scan", "--days", "1", "--json"}, arrays: reconciledLists},
		{name: "scan assay", args: []string{"scan", "--assay", "--json"}, arrays: reconciledLists,
			check: func(t *testing.T, doc any) {
				if number(t, doc, "assayed") != 3 {
					t.Errorf("scan --assay: %v", doc)
				}
			}},
		{name: "assay stored", args: []string{"assay", "--json"}, check: func(t *testing.T, doc any) {
			wantTotals(t, doc)
			if number(t, doc, "assayed") != 3 || number(t, doc, "signal") == 0 {
				t.Errorf("stored assay: %v", doc)
			}
		}},
		{name: "assay session", args: []string{"assay", "--session", "contract-alpha", "--json"}, arrays: []string{"candidates"},
			check: func(t *testing.T, doc any) {
				if text(t, doc, "session_id") != "contract-alpha" || text(t, doc, "tool") != "claude" {
					t.Errorf("a single-session assay must identify its session: %v", doc)
				}
				wantFiltered(t, doc, "title")
				candidates := items(t, doc, "candidates")
				filtered := 0
				for i := range candidates {
					wantClassLabel(t, doc, fmt.Sprintf("candidates.%d.class", i))
					if strings.Contains(text(t, doc, fmt.Sprintf("candidates.%d.preview", i)), "ask operator") {
						filtered++
					}
				}
				if len(candidates) != 4 || filtered != 3 {
					t.Errorf("candidates = %d, filtered previews = %d; want 4 and 3", len(candidates), filtered)
				}
			}},
		{name: "assay live scope", args: []string{"assay", "--live", "--json"}, arrays: []string{"candidates"},
			check: func(t *testing.T, doc any) {
				if text(t, doc, "session_id") != "(scope)" || number(t, doc, "total_records") == 0 {
					t.Errorf("scope assay: %v", doc)
				}
				for i := range items(t, doc, "candidates") {
					wantClassLabel(t, doc, fmt.Sprintf("candidates.%d.class", i))
				}
			}},
		{name: "assay unknown session", args: []string{"assay", "--session", "no-such-session", "--json"}, exit: 1, failure: true},
		{name: "prune without targets", args: []string{"prune", "--json"}, arrays: []string{""}, check: wantLen("", 0)},
		{name: "prune estimate", args: []string{"prune", "--min-session", "0", "--json"}, arrays: []string{""},
			check: func(t *testing.T, doc any) {
				wantLen("", 3)(t, doc)
				for i := range 3 {
					if path := fmt.Sprintf("%d.session", i); text(t, doc, path+".id") == "contract-alpha" {
						wantFiltered(t, doc, path+".title")
					}
				}
			}},
		{name: "archive preview", args: []string{"archive", "--json"}, arrays: []string{""},
			check: func(t *testing.T, doc any) {
				wantLen("", 3)(t, doc)
				for _, key := range []string{"session", "tool", "bytes", "target"} {
					lookup(t, doc, "0."+key)
				}
			}},
		{name: "archive no match", args: []string{"archive", "no-such-prefix", "--json"}, arrays: []string{""}, check: wantLen("", 0)},
		{name: "ops", args: []string{"ops", "--json"}, arrays: []string{""}, check: wantLen("", 0)},
		{name: "read source", args: []string{"read", "--tool", "claude", "--session", "contract-alpha", "--json"}, arrays: []string{"records", "warnings"},
			check: func(t *testing.T, doc any) {
				vars["view"] = text(t, doc, "view_id")
				vars["record"] = text(t, doc, "records.0.id")
				wantFiltered(t, doc, "title")
			}},
		{name: "search hit", args: []string{"search", "needle-contract", "--view", "{view}", "--json"}, arrays: []string{"records", "warnings"},
			check: wantLen("records", 2)},
		{name: "search nothing", args: []string{"search", "absent phrase here", "--view", "{view}", "--json"}, arrays: []string{"records", "warnings"},
			check: wantLen("records", 0)},
		{name: "read view", args: []string{"read", "--view", "{view}", "--json"}, arrays: []string{"records", "warnings"}},
		{name: "assets", args: []string{"assets", "--view", "{view}", "--json"}, arrays: []string{"assets", "omissions", "paths", "limitations"},
			check: wantLen("assets", 0)},
		{name: "collect", args: []string{"collect", "--view", "{view}", "--record", "{record}", "--out", "{sources}", "--json"},
			check: func(t *testing.T, doc any) {
				if number(t, doc, "record_count") != 1 {
					t.Errorf("collect: %v", doc)
				}
			}},
		{name: "collection inspect", args: []string{"collection", "inspect", "{sources}", "--json"}, arrays: []string{"warnings"}},
		{name: "collection read", args: []string{"collection", "read", "{sources}", "--json"}, arrays: []string{"records"}, check: wantLen("records", 1)},
		{name: "collection search nothing", args: []string{"collection", "search", "{sources}", "--query", "absent phrase here", "--json"}, arrays: []string{"records"},
			check: wantLen("records", 0)},
		{name: "collection verify", args: []string{"collection", "verify", "{sources}", "--json"}, arrays: []string{"problems"},
			check: func(t *testing.T, doc any) {
				if lookup(t, doc, "valid") != true {
					t.Errorf("verify: %v", doc)
				}
			}},
		{name: "collection select", args: []string{"collection", "select", "{sources}", "--record", "{record}", "--out", "{selected}", "--json"}},
		{name: "collection merge", args: []string{"collection", "merge", "{sources}", "{selected}", "--out", "{combined}", "--json"}},
		{name: "collection export", args: []string{"collection", "export", "{combined}", "--out", "{export}", "--format", "jsonl", "--json"}},
		{name: "show unknown session", args: []string{"show", "--tool", "claude", "--session", "no-such-session", "--json"}, exit: 1, failure: true},
		{name: "find without query", args: []string{"find", "--json"}, exit: 1, failure: true},
	}, vars)
}

func TestJSONContractOnEmptyStore(t *testing.T) {
	newContractFixture(t, false)
	runContract(t, []contractCase{
		{name: "ls", args: []string{"ls", "--json"}, arrays: []string{"sessions", "stores_read", "warnings"}, check: wantLen("sessions", 0)},
		{name: "find", args: []string{"find", "anything", "--json"}, arrays: []string{"hits"}, check: wantLen("hits", 0)},
		{name: "doctor", args: []string{"doctor", "--json"}, arrays: []string{"at_risk", "dead_workspaces", "live"},
			check: func(t *testing.T, doc any) {
				for _, path := range []string{"at_risk", "dead_workspaces", "live"} {
					wantLen(path, 0)(t, doc)
				}
			}},
		{name: "scan", args: []string{"scan", "--json"}, arrays: reconciledLists},
		{name: "scan assay", args: []string{"scan", "--assay", "--json"}, arrays: reconciledLists},
		{name: "assay stored", args: []string{"assay", "--json"}, check: func(t *testing.T, doc any) {
			wantTotals(t, doc)
			if number(t, doc, "assayed") != 0 {
				t.Errorf("empty store assayed sessions: %v", doc)
			}
		}},
		{name: "assay live scope", args: []string{"assay", "--live", "--json"}, arrays: []string{"candidates"}, check: wantLen("candidates", 0)},
		{name: "prune", args: []string{"prune", "--json"}, arrays: []string{""}, check: wantLen("", 0)},
		{name: "prune any size", args: []string{"prune", "--min-session", "0", "--json"}, arrays: []string{""}, check: wantLen("", 0)},
		{name: "archive", args: []string{"archive", "--json"}, arrays: []string{""}, check: wantLen("", 0)},
		{name: "ops", args: []string{"ops", "--json"}, arrays: []string{""}, check: wantLen("", 0)},
		{name: "show", args: []string{"show", "--tool", "claude", "--session", "missing", "--json"}, exit: 1, failure: true},
		{name: "brief", args: []string{"brief", "--tool", "claude", "--session", "missing", "--json"}, exit: 1, failure: true},
		{name: "usage", args: []string{"usage", "--tool", "claude", "--session", "missing", "--json"}, exit: 1, failure: true},
		{name: "assay session", args: []string{"assay", "--session", "missing", "--json"}, exit: 1, failure: true},
	}, map[string]string{})
}

// Terminal output carries the same filtered text as JSON.
func TestTextOutputIsFiltered(t *testing.T) {
	fixture := newContractFixture(t, true)
	for _, tc := range []struct {
		args        []string
		placeholder bool
	}{
		{[]string{"ls"}, true},
		{[]string{"show", "--tool", "claude", "--session", "contract-alpha"}, true},
		{[]string{"find", "needle-contract"}, true},
		{[]string{"brief", "--tool", "claude", "--session", "contract-alpha"}, true},
		{[]string{"brief", "--tool", "claude", "--session", "contract-alpha", "--handoff"}, true},
		{[]string{"assay", "--session", "contract-alpha"}, true},
		{[]string{"doctor"}, false},
		{[]string{"prune", "--min-session", "0"}, false},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			res := runCLI(t, tc.args...)
			if res.code != 0 {
				t.Fatalf("exit %d\nstdout: %s\nstderr: %s", res.code, res.stdout, res.stderr)
			}
			assertNoCredential(t, res.stdout+res.stderr)
			if tc.placeholder && !strings.Contains(res.stdout, "ask operator") {
				t.Errorf("filtered text shows no placeholder:\n%s", res.stdout)
			}
		})
	}
	// The saved handoff is the same filtered text.
	saved, err := filepath.Glob(filepath.Join(fixture.home, "state", "artifacts", "handoff-*.md"))
	if err != nil || len(saved) != 1 {
		t.Fatalf("saved handoff: %v %v", saved, err)
	}
	raw, err := os.ReadFile(saved[0])
	if err != nil {
		t.Fatal(err)
	}
	assertNoCredential(t, string(raw))
}

// A collection manifest may carry no warnings; inspect still reports [].
func TestCollectionInspectReportsEmptyWarningsAsArray(t *testing.T) {
	dir := t.TempDir()
	manifest := `{"schema":"midden.collection/v1","record_file":"records.jsonl","record_count":0,"records_digest":"","sources":[],"assets":[],"asset_omissions":[],"warnings":null}`
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runMaterial("collection", []string{"inspect", dir, "--json"}, &out); err != nil {
		t.Fatal(err)
	}
	doc := decodeOne(t, out.String())
	assertNoNull(t, doc, "$")
	wantLen("warnings", 0)(t, doc)
}

func wantTotals(t *testing.T, doc any) {
	t.Helper()
	object, ok := doc.(map[string]any)
	if !ok {
		t.Fatalf("totals are not an object: %v", doc)
	}
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	want := "artifact,assayed,bookkeeping,bytes,clusters,dup_bytes,exhaust,images,sessions,signal"
	if got := strings.Join(keys, ","); got != want {
		t.Errorf("totals keys = %s, want %s", got, want)
	}
}

func wantClassLabel(t *testing.T, doc any, path string) {
	t.Helper()
	switch label := text(t, doc, path); label {
	case "signal", "exhaust", "artifact", "bookkeeping":
	default:
		t.Errorf("%q = %q, want a class label", path, label)
	}
}
