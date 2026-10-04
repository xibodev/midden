package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	fixtureSession = "0b5c2d1e-7a44-4f3e-9d61-2c8f5e7a9b10"
	resumeSession  = "5d0f9a2b-3c1e-4a8d-b7f6-1e2d3c4b5a69"
)

var (
	testCoreOnce sync.Once
	testCorePath string
	testCoreErr  error
)

// testCore builds the repository's core binary once per test run.
func testCore(t *testing.T) string {
	t.Helper()
	testCoreOnce.Do(func() { testCorePath, testCoreErr = buildTestCore() })
	if testCoreErr != nil {
		t.Fatal(testCoreErr)
	}
	return testCorePath
}

func buildTestCore() (string, error) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		return "", err
	}
	if _, err = os.Stat(filepath.Join(root, "cmd", "midden", "main.go")); err != nil {
		return "", fmt.Errorf("core sources not found: %w", err)
	}
	goTool, err := exec.LookPath("go")
	if err != nil {
		goTool = filepath.Join(runtime.GOROOT(), "bin", "go")
	}
	parent := filepath.Join(os.TempDir(), "midden-ui-test-core")
	if err = os.MkdirAll(parent, 0700); err != nil {
		return "", err
	}
	// Builds left by earlier runs are removed once clearly stale.
	if entries, err := os.ReadDir(parent); err == nil {
		for _, entry := range entries {
			if info, err := entry.Info(); err == nil && time.Since(info.ModTime()) > 6*time.Hour {
				os.RemoveAll(filepath.Join(parent, entry.Name()))
			}
		}
	}
	dir, err := os.MkdirTemp(parent, "build-")
	if err != nil {
		return "", err
	}
	name := "midden"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	binary := filepath.Join(dir, name)
	cmd := exec.Command(goTool, "build", "-o", binary, "./cmd/midden")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOWORK=off")
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("build core: %v\n%s", err, output)
	}
	return binary, nil
}

// coreApp is an app bound to the real core and a synthetic Claude store only.
func coreApp(t *testing.T) *App {
	t.Helper()
	core := testCore(t)
	root := t.TempDir()
	store := filepath.Join(root, "sources", "claude")
	project := filepath.Join(store, "projects", "synthetic-work")
	recorded := filepath.Join(root, "recorded-workspace")
	workspace := filepath.Join(root, "workspace")
	for _, dir := range []string{project, recorded, workspace} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	// A recorded directory outside temporary paths keeps the session visible
	// by default; it is only a recorded string and need not exist.
	synthetic := filepath.Join(filepath.VolumeName(root)+string(filepath.Separator), "synthetic-projects", "importer")
	writeClaudeSession(t, project, fixtureSession, synthetic, []string{
		"Improve the synthetic CSV importer so explicit zero values survive.",
		"Plan: use explicit presence checks in the mapper rather than blanket omission.",
		"The fixture check found that zero-valued quantities disappear during import.",
		"Correction: zero is a value; absence is a separate state. The local fixture checks now pass.",
	})
	writeClaudeSession(t, project, resumeSession, recorded, []string{
		"Continue the synthetic importer validation.",
		"Chunk boundaries now keep record identity.",
	})
	app, err := NewApp(Options{Workspace: workspace, State: filepath.Join(root, "state"), Core: core,
		SourceEnv: map[string]string{"MIDDEN_CLAUDE_ROOT": store}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Close)
	return app
}

func writeClaudeSession(t *testing.T, dir, id, cwd string, texts []string) {
	t.Helper()
	var lines bytes.Buffer
	for i, text := range texts {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		raw, err := json.Marshal(map[string]any{"type": role, "sessionId": id, "cwd": cwd,
			"timestamp": fmt.Sprintf("2026-01-%02dT12:00:00Z", i+1), "message": map[string]any{"role": role, "content": text}})
		if err != nil {
			t.Fatal(err)
		}
		lines.Write(append(raw, '\n'))
	}
	if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), lines.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
}

type coreReply struct {
	status int
	header http.Header
	body   []byte
}

func callCore(t *testing.T, app *App, method, target string, body any) coreReply {
	t.Helper()
	var reader io.Reader
	switch value := body.(type) {
	case nil:
	case string:
		reader = strings.NewReader(value)
	default:
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	request := httptest.NewRequest(method, "http://127.0.0.1:18890"+target, reader)
	if method != http.MethodGet && method != http.MethodHead {
		request.Header.Set("X-Midden-CSRF", app.csrf)
	}
	response := httptest.NewRecorder()
	app.ServeHTTP(response, request)
	return coreReply{response.Code, response.Header(), response.Body.Bytes()}
}

// expectCore checks the status and JSON content type and decodes the object.
func expectCore(t *testing.T, app *App, method, target string, body any, status int) map[string]any {
	t.Helper()
	reply := callCore(t, app, method, target, body)
	if reply.status != status {
		t.Fatalf("%s %s: status %d, want %d: %s", method, target, reply.status, status, reply.body)
	}
	if kind := reply.header.Get("Content-Type"); kind != "application/json" {
		t.Fatalf("%s %s: content type %q", method, target, kind)
	}
	var value map[string]any
	if err := json.Unmarshal(reply.body, &value); err != nil {
		t.Fatalf("%s %s: not a JSON object: %s", method, target, reply.body)
	}
	return value
}

func list(t *testing.T, value map[string]any, key string) []map[string]any {
	t.Helper()
	items, ok := value[key].([]any)
	if !ok {
		t.Fatalf("%q is not a list: %v", key, value)
	}
	out := []map[string]any{}
	for _, item := range items {
		object, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("%q item is not an object: %v", key, item)
		}
		out = append(out, object)
	}
	return out
}

func sessionIDs(t *testing.T, inventory map[string]any) []string {
	ids := []string{}
	for _, session := range list(t, inventory, "sessions") {
		ids = append(ids, fmt.Sprint(session["id"]))
	}
	return ids
}

func TestPersonDoorReadsSessionsAndPinsViews(t *testing.T) {
	app := coreApp(t)
	all := sessionIDs(t, expectCore(t, app, "GET", "/api/core/sessions?all=1&tool=claude&limit=10", nil, 200))
	if !slices.Contains(all, fixtureSession) || !slices.Contains(all, resumeSession) {
		t.Fatalf("listing misses synthetic sessions: %v", all)
	}
	if visible := sessionIDs(t, expectCore(t, app, "GET", "/api/core/sessions?days=0&offset=0", nil, 200)); !slices.Contains(visible, fixtureSession) {
		t.Fatalf("default listing misses the recorded session: %v", visible)
	}
	reply := callCore(t, app, "GET", "/api/core/find?q=zero&tool=claude&limit=5", nil)
	if reply.status != 200 || !json.Valid(reply.body) || !bytes.Contains(reply.body, []byte(fixtureSession)) {
		t.Fatalf("find: %d %s", reply.status, reply.body)
	}
	if show := expectCore(t, app, "GET", "/api/core/show?tool=claude&id="+fixtureSession, nil, 200); show["id"] != fixtureSession {
		t.Fatalf("show: %v", show)
	}
	if usage := expectCore(t, app, "GET", "/api/core/usage?tool=claude&id="+fixtureSession, nil, 200); usage["input_tokens"] == nil {
		t.Fatalf("usage: %v", usage)
	}
	brief := expectCore(t, app, "GET", "/api/core/brief?tool=claude&id="+fixtureSession, nil, 200)
	if session, _ := brief["session"].(map[string]any); session["id"] != fixtureSession {
		t.Fatalf("brief: %v", brief)
	}
	resume := expectCore(t, app, "GET", "/api/core/resume?id="+resumeSession, nil, 200)
	if command, _ := resume["command"].(string); len(resume) != 1 || !strings.Contains(command, "claude --resume "+resumeSession) {
		t.Fatalf("resume: %v", resume)
	}
	missing := expectCore(t, app, "GET", "/api/core/show?tool=claude&id=00000000-0000-4000-8000-000000000000", nil, 422)
	if message, _ := missing["error"].(string); !strings.HasPrefix(message, "session is unavailable") {
		t.Fatalf("core failure not reported by its first stderr line: %v", missing)
	}

	opened := expectCore(t, app, "POST", "/api/core/views", map[string]any{"tool": "claude", "session": fixtureSession, "limit": 10, "chars": 400}, 200)
	view, _ := opened["view_id"].(string)
	records := list(t, opened, "records")
	if opened["schema"] != "midden.source-view/v1" || view == "" || len(records) != 4 {
		t.Fatalf("opened view: %v", opened)
	}
	page := expectCore(t, app, "GET", "/api/core/views/"+view+"?offset=1&limit=2&chars=400&includeTools=0", nil, 200)
	if page["view_id"] != view || page["offset"] != float64(1) || len(list(t, page, "records")) != 2 || page["next_offset"] != float64(3) {
		t.Fatalf("view page: %v", page)
	}
	searched := expectCore(t, app, "POST", "/api/core/views/"+view+"/search", map[string]any{"query": "zero", "limit": 5}, 200)
	if searched["selection"] != "search" || searched["view_id"] == view {
		t.Fatalf("search did not pin a new view: %v", searched)
	}
	focused := expectCore(t, app, "POST", "/api/core/views/"+view+"/context", map[string]any{"records": []string{fmt.Sprint(records[1]["id"])}, "before": 1, "after": 1}, 200)
	if focused["selection"] != "context" {
		t.Fatalf("context: %v", focused)
	}
	if assets := expectCore(t, app, "GET", "/api/core/views/"+view+"/assets?offset=0&limit=10", nil, 200); assets["asset_count"] != float64(0) || assets["view_id"] != view {
		t.Fatalf("assets: %v", assets)
	}
	pinned, _ := filepath.Glob(filepath.Join(app.opts.State, "core", "views", "v-*.json"))
	if len(pinned) < 3 {
		t.Fatalf("views were not pinned in the core cache: %v", pinned)
	}
	if entries, _ := os.ReadDir(app.opts.Workspace); len(entries) != 0 {
		t.Fatalf("reads changed the workspace: %v", entries)
	}
}

func TestPersonDoorCollectsListsAndVerifiesSources(t *testing.T) {
	app := coreApp(t)
	opened := expectCore(t, app, "POST", "/api/core/views", map[string]any{"tool": "claude", "session": fixtureSession}, 200)
	view := fmt.Sprint(opened["view_id"])
	records := list(t, opened, "records")
	plan, correction := fmt.Sprint(records[1]["id"]), fmt.Sprint(records[3]["id"])

	selected := expectCore(t, app, "POST", "/api/core/collect", map[string]any{"views": []string{view}, "records": []string{plan, correction}, "out": "sources/selected"}, 200)
	if selected["record_count"] != float64(2) {
		t.Fatalf("collect: %v", selected)
	}
	if all := expectCore(t, app, "POST", "/api/core/collect", map[string]any{"views": []string{view}, "out": "sources/all"}, 200); all["record_count"] != float64(4) {
		t.Fatalf("collect all: %v", all)
	}
	taken := expectCore(t, app, "POST", "/api/core/collect", map[string]any{"views": []string{view}, "out": "sources/selected"}, 400)
	if message, _ := taken["error"].(string); !strings.Contains(message, "already exists") {
		t.Fatalf("existing destination: %v", taken)
	}

	manifest, err := os.ReadFile(filepath.Join(app.opts.Workspace, "sources", "all", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	for rel, content := range map[string][]byte{
		"deep/b/c/d/e/manifest.json":     manifest,
		"too/deep/c/d/e/f/manifest.json": manifest,
		".hidden/copy/manifest.json":     manifest,
		"node_modules/pkg/manifest.json": manifest,
		"sessions/copy/manifest.json":    manifest,
		"notes/manifest.json":            []byte(`{"schema":"another/v1"}`),
	} {
		path := filepath.Join(app.opts.Workspace, filepath.FromSlash(rel))
		if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, content, 0600); err != nil {
			t.Fatal(err)
		}
	}
	paths := []string{}
	for _, info := range list(t, expectCore(t, app, "GET", "/api/core/collections", nil, 200), "collections") {
		if info["schema"] != "midden.collection/v1" {
			t.Fatalf("listed collection without inspection: %v", info)
		}
		paths = append(paths, fmt.Sprint(info["path"]))
	}
	if want := []string{"deep/b/c/d/e", "sources/all", "sources/selected"}; !slices.Equal(paths, want) {
		t.Fatalf("collections %v, want %v", paths, want)
	}

	if info := expectCore(t, app, "GET", "/api/core/collection?path=sources/selected", nil, 200); info["record_count"] != float64(2) || info["path"] != "sources/selected" {
		t.Fatalf("inspect: %v", info)
	}
	page := expectCore(t, app, "GET", "/api/core/collection/records?path=sources/selected&offset=0&limit=1", nil, 200)
	if page["total"] != float64(2) || page["next_offset"] != float64(1) || len(list(t, page, "records")) != 1 {
		t.Fatalf("records: %v", page)
	}
	hits := expectCore(t, app, "POST", "/api/core/collection/search", map[string]any{"path": "sources/selected", "query": "zero", "limit": 10}, 200)
	if hits["total"] != float64(1) || list(t, hits, "records")[0]["id"] != correction {
		t.Fatalf("collection search: %v", hits)
	}
	if verified := expectCore(t, app, "POST", "/api/core/collection/verify", map[string]any{"path": "sources/selected"}, 200); verified["valid"] != true {
		t.Fatalf("verify: %v", verified)
	}
	quote := map[string]any{"path": "sources/selected", "record": correction, "quote": "zero is a value"}
	if matched := expectCore(t, app, "POST", "/api/core/collection/verify", quote, 200); matched["matched"] != true {
		t.Fatalf("quote: %v", matched)
	}
	quote["quote"] = "zero is never a value"
	if unmatched := expectCore(t, app, "POST", "/api/core/collection/verify", quote, 200); unmatched["matched"] != false || unmatched["record_id"] != correction {
		t.Fatalf("unmatched quote: %v", unmatched)
	}

	exported := expectCore(t, app, "POST", "/api/core/collection/export", map[string]any{"path": "sources/selected", "out": "notes/selected.md", "format": "markdown"}, 200)
	if exported["path"] != "notes/selected.md" || exported["format"] != "markdown" {
		t.Fatalf("export: %v", exported)
	}
	notes, err := os.ReadFile(filepath.Join(app.opts.Workspace, "notes", "selected.md"))
	if err != nil || !strings.Contains(string(notes), "zero is a value") {
		t.Fatalf("export file: %v %s", err, notes)
	}
	merged := expectCore(t, app, "POST", "/api/core/collection/merge", map[string]any{"paths": []string{"sources/selected", "sources/all"}, "out": "sources/merged"}, 200)
	if merged["record_count"] != float64(4) {
		t.Fatalf("merge: %v", merged)
	}

	// A damaged collection still returns its verification report.
	damaged, err := os.OpenFile(filepath.Join(app.opts.Workspace, "sources", "all", "records.jsonl"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	damaged.WriteString("{\"id\":\"synthetic\"}\n")
	damaged.Close()
	report := expectCore(t, app, "POST", "/api/core/collection/verify", map[string]any{"path": "sources/all"}, 200)
	if report["valid"] != false || len(report["problems"].([]any)) == 0 {
		t.Fatalf("damaged collection report: %v", report)
	}
}

func TestAgentDoorRunsTheSameCore(t *testing.T) {
	app := coreApp(t)
	tool := coreTool{app}
	result := tool.Execute(context.Background(), map[string]any{"args": []any{"ls", "--all", "--json"}})
	if result.IsError || !strings.Contains(result.ForLLM, fixtureSession) {
		t.Fatalf("agent listing failed: %s", result.ForLLM)
	}
	result = tool.Execute(context.Background(), map[string]any{"args": []any{"show", "--tool", "claude", "--session", "missing", "--json"}})
	if !result.IsError || !strings.Contains(result.ForLLM, "Midden failed") {
		t.Fatalf("core failure not reported to the agent: %s", result.ForLLM)
	}
	if result = tool.Execute(context.Background(), map[string]any{"args": []any{"prune"}}); !result.IsError {
		t.Fatal("agent ran a maintenance command")
	}
}

func TestPersonDoorWritesRequireTheSessionToken(t *testing.T) {
	app, err := NewApp(testOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	for _, token := range []string{"", "wrong-token"} {
		for _, target := range []string{"/api/core/views", "/api/core/collect", "/api/core/collection/export", "/api/core/collection/verify"} {
			request := httptest.NewRequest("POST", "http://127.0.0.1:18890"+target, strings.NewReader(`{}`))
			if token != "" {
				request.Header.Set("X-Midden-CSRF", token)
			}
			response := httptest.NewRecorder()
			app.ServeHTTP(response, request)
			if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "CSRF") {
				t.Fatalf("POST %s with token %q: %d %s", target, token, response.Code, response.Body)
			}
		}
	}
}

func TestPersonDoorRejectsBadInputsBeforeRunningTheCore(t *testing.T) {
	// The synthetic core path cannot run: any 400 proves validation came first.
	app, err := NewApp(testOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	view := "v-" + strings.Repeat("0", 64)
	cases := []struct {
		method, target string
		body           any
		status         int
	}{
		{"GET", "/api/core/sessions?tool=other", nil, 400},
		{"GET", "/api/core/sessions?limit=0", nil, 400},
		{"GET", "/api/core/sessions?limit=101", nil, 400},
		{"GET", "/api/core/sessions?offset=-1", nil, 400},
		{"GET", "/api/core/sessions?days=many", nil, 400},
		{"GET", "/api/core/sessions?all=maybe", nil, 400},
		{"GET", "/api/core/sessions?unknown=1", nil, 400},
		{"GET", "/api/core/sessions?tool=claude&tool=copilot", nil, 400},
		{"GET", "/api/core/sessions?q=%zz", nil, 400},
		{"GET", "/api/core/find", nil, 400},
		{"GET", "/api/core/find?q=%20%20", nil, 400},
		{"GET", "/api/core/find?q=-flag", nil, 400},
		{"GET", "/api/core/find?q=" + strings.Repeat("x", 301), nil, 400},
		{"GET", "/api/core/show?tool=claude", nil, 400},
		{"GET", "/api/core/show?id=" + fixtureSession, nil, 400},
		{"GET", "/api/core/usage?tool=claude&id=-x", nil, 400},
		{"GET", "/api/core/brief?tool=claude&id=a,b", nil, 400},
		{"GET", "/api/core/brief?tool=claude&id=" + strings.Repeat("a", 257), nil, 400},
		{"GET", "/api/core/brief?tool=claude&id=a%20b", nil, 400},
		{"GET", "/api/core/resume", nil, 400},
		{"POST", "/api/core/views", map[string]any{"tool": "claude"}, 400},
		{"POST", "/api/core/views", map[string]any{"tool": "other", "session": "s"}, 400},
		{"POST", "/api/core/views", map[string]any{"tool": "claude", "session": "s", "limit": 0}, 400},
		{"POST", "/api/core/views", map[string]any{"tool": "claude", "session": "s", "chars": 79}, 400},
		{"POST", "/api/core/views", map[string]any{"tool": "claude", "session": "s", "unknown": true}, 400},
		{"POST", "/api/core/views", `{"tool":"claude"`, 400},
		{"POST", "/api/core/views?tool=claude", map[string]any{"tool": "claude", "session": "s"}, 400},
		{"GET", "/api/core/views/", nil, 400},
		{"GET", "/api/core/views/bad,id", nil, 400},
		{"GET", "/api/core/views/" + view + "?limit=257", nil, 400},
		{"GET", "/api/core/views/" + view + "?chars=8193", nil, 400},
		{"GET", "/api/core/views/" + view + "?includeTools=yes", nil, 400},
		{"POST", "/api/core/views/" + view + "/search", map[string]any{"query": "ab"}, 400},
		{"POST", "/api/core/views/" + view + "/search", map[string]any{"query": "-flag value"}, 400},
		{"POST", "/api/core/views/" + view + "/search", map[string]any{"query": strings.Repeat("x", 301)}, 400},
		{"POST", "/api/core/views/" + view + "/context", map[string]any{"records": []string{}}, 400},
		{"POST", "/api/core/views/" + view + "/context", map[string]any{"records": slices.Repeat([]string{"claude:s:1"}, 17)}, 400},
		{"POST", "/api/core/views/" + view + "/context", map[string]any{"records": []string{"claude:s:1"}, "before": 6}, 400},
		{"GET", "/api/core/views/" + view + "/assets?limit=101", nil, 400},
		{"POST", "/api/core/collect", map[string]any{"views": []string{}, "out": "sources"}, 400},
		{"POST", "/api/core/collect", map[string]any{"views": []string{view}}, 400},
		{"POST", "/api/core/collect", map[string]any{"views": []string{view}, "assets": true, "out": "sources"}, 400},
		{"POST", "/api/core/collect", map[string]any{"views": slices.Repeat([]string{view}, 26), "out": "sources"}, 400},
		{"GET", "/api/core/collection", nil, 400},
		{"GET", "/api/core/collection?path=..", nil, 400},
		{"GET", "/api/core/collection?path=a/../b", nil, 400},
		{"GET", "/api/core/collection?path=/abs", nil, 400},
		{"GET", "/api/core/collection?path=C:/sources", nil, 400},
		{"GET", "/api/core/collection?path=.", nil, 400},
		{"GET", "/api/core/collection?path=-sources", nil, 400},
		{"GET", "/api/core/collection?path=.hidden/sources", nil, 400},
		{"GET", "/api/core/collection/records?path=sources&limit=257", nil, 400},
		{"POST", "/api/core/collection/search", map[string]any{"path": "sources", "query": " "}, 400},
		{"POST", "/api/core/collection/verify", map[string]any{"path": "sources", "record": "claude:s:1"}, 400},
		{"POST", "/api/core/collection/verify", map[string]any{"path": "sources", "quote": "words"}, 400},
		{"POST", "/api/core/collection/verify", map[string]any{"path": "sources", "record": "claude:s:1", "quote": strings.Repeat("x", 16385)}, 400},
		{"POST", "/api/core/collection/export", map[string]any{"path": "sources", "out": "notes.pdf", "format": "pdf"}, 400},
		{"POST", "/api/core/collection/merge", map[string]any{"paths": []string{}, "out": "merged"}, 400},
		{"POST", "/api/core/collection/merge", map[string]any{"paths": slices.Repeat([]string{"sources"}, 26), "out": "merged"}, 400},
		{"POST", "/api/core/sessions", map[string]any{}, 405},
		{"GET", "/api/core/collect", nil, 405},
		{"DELETE", "/api/core/views/" + view, nil, 405},
		{"POST", "/api/core/collections", map[string]any{}, 405},
		{"HEAD", "/api/core/show?tool=claude&id=s", nil, 405},
		{"GET", "/api/core/unknown", nil, 404},
		{"GET", "/api/core/views/" + view + "/unknown", nil, 404},
	}
	if filepath.Separator != '\\' {
		cases = append(cases, struct {
			method, target string
			body           any
			status         int
		}{"GET", "/api/core/collection?path=" + url.QueryEscape(`sources\a`), nil, 400})
	}
	for _, tc := range cases {
		reply := callCore(t, app, tc.method, tc.target, tc.body)
		if reply.status != tc.status {
			t.Errorf("%s %s: status %d, want %d: %s", tc.method, tc.target, reply.status, tc.status, reply.body)
			continue
		}
		if tc.method == "HEAD" {
			continue
		}
		var failure struct {
			Error string `json:"error"`
		}
		if err := json.Unmarshal(reply.body, &failure); err != nil || failure.Error == "" {
			t.Errorf("%s %s: not a JSON error: %s", tc.method, tc.target, reply.body)
		}
		if tc.status == 405 && reply.header.Get("Allow") == "" {
			t.Errorf("%s %s: 405 without Allow", tc.method, tc.target)
		}
	}
}

func TestPersonDoorRejectsWritesOutsideTheWorkspace(t *testing.T) {
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
	outside := filepath.Join(filepath.Dir(opts.Workspace), "outside")
	view := "v-" + strings.Repeat("0", 64)
	destinations := []string{"../outside", outside, filepath.ToSlash(outside), "taken", "host-state/collection", ".midden/collection", "sessions/collection", "notes/../../outside"}
	if err = os.Symlink(filepath.Dir(opts.Workspace), filepath.Join(opts.Workspace, "link")); err == nil {
		destinations = append(destinations, "link/outside")
	}
	for _, out := range destinations {
		for _, call := range []struct {
			target string
			body   map[string]any
		}{
			{"/api/core/collect", map[string]any{"views": []string{view}, "out": out}},
			{"/api/core/collection/export", map[string]any{"path": "taken", "out": out, "format": "markdown"}},
			{"/api/core/collection/merge", map[string]any{"paths": []string{"taken"}, "out": out}},
		} {
			if reply := callCore(t, app, "POST", call.target, call.body); reply.status != http.StatusBadRequest {
				t.Errorf("%s accepted destination %q: %d %s", call.target, out, reply.status, reply.body)
			}
		}
	}
	if _, err = os.Lstat(outside); !os.IsNotExist(err) {
		t.Fatal("a write escaped the workspace")
	}
}

func TestCollectionWalkSkipsHostStateAndHiddenTrees(t *testing.T) {
	opts := testOptions(t)
	opts.State = filepath.Join(opts.Workspace, "host-state")
	app, err := NewApp(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	manifest := []byte(`{"schema":"midden.collection/v1","record_file":"records.jsonl"}`)
	for _, rel := range []string{"a/manifest.json", "host-state/b/manifest.json", ".git/c/manifest.json", "node_modules/d/manifest.json",
		"sessions/e/manifest.json", "f/g/h/i/j/manifest.json", "k/l/m/n/o/p/manifest.json", "a/nested/manifest.json"} {
		path := filepath.Join(opts.Workspace, filepath.FromSlash(rel))
		if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, manifest, 0600); err != nil {
			t.Fatal(err)
		}
	}
	found, err := app.findCollections(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a", "f/g/h/i/j"}; !slices.Equal(found, want) {
		t.Fatalf("collections %v, want %v", found, want)
	}
	for i := range collectionLimit + 5 {
		path := filepath.Join(opts.Workspace, "many", fmt.Sprintf("c%03d", i), "manifest.json")
		if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, manifest, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if found, err = app.findCollections(context.Background()); err != nil || len(found) != collectionLimit {
		t.Fatalf("walk returned %d collections (%v), want the %d limit", len(found), err, collectionLimit)
	}
}
