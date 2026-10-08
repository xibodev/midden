package main

import (
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestFreshDataHasTheNewLayout(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Midden")
	paths, notice, err := prepareData(root)
	if err != nil || notice != "" {
		t.Fatalf("prepare = %v %q", err, notice)
	}
	for _, dir := range []string{paths.App, paths.Kernel, paths.Workspace, paths.Files} {
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			t.Fatalf("%s is missing", dir)
		}
	}
	if paths.Files != filepath.Join(root, "workspace", "files") || paths.Kernel != filepath.Join(root, "kernel") || paths.App != filepath.Join(root, "app") {
		t.Fatalf("paths = %+v", paths)
	}
	if readTestFile(t, filepath.Join(paths.App, layoutMarker)) != layoutVersion {
		t.Fatal("the layout was not recorded")
	}
}

func TestEarlierAppDataMovesOnce(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "workspace", "notes", "draft.md"), "my draft")
	writeTestFile(t, filepath.Join(root, "workspace", "files", "named-files.txt"), "a folder the person called files")
	writeTestFile(t, filepath.Join(root, "host-state", "sessions.json"), `{"s":{"id":"s","title":"Earlier","updated":"2026-01-01T00:00:00Z","messages":[]}}`)
	writeTestFile(t, filepath.Join(root, "host-state", "model-checks.json"), `{"fixture":{}}`)
	writeTestFile(t, filepath.Join(root, "host-state", "core", "views", "v.json"), "core state")
	old := filepath.Join(root, "host-state", "kernel")
	writeTestFile(t, filepath.Join(old, "config.json"), `{
  "agents": {"defaults": {"model_name": "fixture/model", "workspace": "/old/compa-workspace", "max_tokens": 8192}},
  "channel_list": {"midden-ui": {"enabled": true, "type": "midden-ui", "settings": {"streaming": {"enabled": true}}}},
  "hooks": {"defaults": {"approval_timeout_ms": 300000}},
  "provider_instances": [{"id": "fixture", "provider_kind": "custom_openai", "adapter": "openai-compatible", "protocol": "openai",
    "endpoint": "http://127.0.0.1:9/v1", "auth_connection_ref": "credential:midden-fixture", "state": "enabled"}],
  "active_models": ["fixture/model"]
}`)
	writeTestFile(t, filepath.Join(old, "auth.json"), `{"credentials":{"midden-fixture":{"access_token":"kept-key","provider":"custom_openai","auth_method":"api_key"}}}`)
	writeTestFile(t, filepath.Join(old, "model_catalogs.json"), `{"entries":{"fixture":{"id":"fixture","instance_id":"fixture","provider":"custom_openai","api_base":"http://127.0.0.1:9/v1","models":[{"id":"model"}],"fetched_at":"t"}}}`)

	paths, notice, err := prepareData(root)
	if err != nil || notice != "" {
		t.Fatalf("migrate = %v %q", err, notice)
	}
	if readTestFile(t, filepath.Join(paths.Files, "notes", "draft.md")) != "my draft" ||
		readTestFile(t, filepath.Join(paths.Files, "files", "named-files.txt")) != "a folder the person called files" {
		t.Fatal("the person's files did not move into files")
	}
	if entries, _ := os.ReadDir(paths.Workspace); len(entries) != 1 {
		t.Fatalf("the kernel's workspace holds more than the files folder: %v", entries)
	}
	if !strings.Contains(readTestFile(t, filepath.Join(paths.App, "sessions.json")), "Earlier") {
		t.Fatal("conversations did not move")
	}
	cfg, err := loadKernelConfig(paths.Kernel)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultModel() != "fixture/model" || cfg.instance("fixture") == nil || len(cfg.ActiveModels) != 1 {
		t.Fatalf("model settings did not move: %+v", cfg)
	}
	if _, ok := cfg.other["channel_list"]; ok {
		t.Fatal("a setting Compa 3 no longer reads moved with the models")
	}
	if secret, err := resolveCredentialRef(paths.Kernel, "credential:midden-fixture"); err != nil || secret != "kept-key" {
		t.Fatal("the stored key did not move", err)
	}
	if status := selectionStatus(paths.Kernel, cfg); !status.Configured {
		t.Fatalf("the moved default model does not resolve: %+v", status)
	}
	if readTestFile(t, filepath.Join(root, "host-state", "core", "views", "v.json")) != "core state" {
		t.Fatal("host-state was changed")
	}

	if _, err := os.Stat(movingFiles(paths)); !os.IsNotExist(err) {
		t.Fatal("the files were left set aside")
	}
	// A second start moves nothing again.
	writeTestFile(t, filepath.Join(paths.Files, "new.md"), "after the move")
	if _, notice, err := prepareData(root); err != nil || notice != "" {
		t.Fatalf("second prepare = %v %q", err, notice)
	}
	if readTestFile(t, filepath.Join(paths.Files, "new.md")) != "after the move" {
		t.Fatal("a second start moved the files again")
	}
}

func TestAMoveCutShortIsFinishedAndNeverRepeated(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "host-state", "sessions.json"), "{}")
	paths := dataPaths(root)
	// The earlier start set the workspace aside, recorded it, and stopped.
	writeTestFile(t, filepath.Join(movingFiles(paths), "files", "only.txt"), "the person's only folder is called files")
	writeTestFile(t, filepath.Join(paths.App, "files-set-aside"), "done\n")
	if _, _, err := prepareData(root); err != nil {
		t.Fatal(err)
	}
	if readTestFile(t, filepath.Join(paths.Files, "files", "only.txt")) != "the person's only folder is called files" {
		t.Fatal("the set-aside files did not reach the files folder")
	}
	if _, _, err := prepareData(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(paths.Files, "files", "files")); !os.IsNotExist(err) {
		t.Fatal("the move was repeated")
	}
}

func TestUnreadableEarlierModelsAskForReconnection(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "host-state", "kernel", "config.json"),
		`{"provider_instances":[{"id":"Bad Id","provider_kind":"x","adapter":"y","protocol":"z","state":"enabled"}]}`)
	paths, notice, err := prepareData(root)
	if err != nil || !strings.Contains(notice, "Connect your models again") {
		t.Fatalf("migrate = %v %q", err, notice)
	}
	if _, err := os.Stat(kernelConfigPath(paths.Kernel)); !os.IsNotExist(err) {
		t.Fatal("unreadable model settings were carried over")
	}
}

func TestAgentInstructionsAreRewrittenAtEachStart(t *testing.T) {
	app := newTestApp(t)
	path := filepath.Join(app.paths.Workspace, "AGENT.md")
	if text := readTestFile(t, path); !strings.HasPrefix(text, "---\nname: Midden\n") || !strings.Contains(text, "files/") {
		t.Fatalf("AGENT.md = %q", text)
	}
	writeTestFile(t, path, "changed by someone")
	again, err := NewApp(app.opts)
	if err != nil {
		t.Fatal(err)
	}
	again.Close()
	if readTestFile(t, path) != agentInstructions(runtime.GOOS) {
		t.Fatal("AGENT.md was not rewritten")
	}
}

func TestAgentInstructionsSayWhereAndInWhichShellCommandsRun(t *testing.T) {
	windows, unix := agentInstructions("windows"), agentInstructions("darwin")
	for _, text := range []string{windows, unix} {
		if !strings.Contains(text, "exec tool's cwd set to files") {
			t.Fatalf("the instructions do not say where commands run: %q", text)
		}
	}
	if !strings.Contains(windows, "Windows PowerShell") || strings.Contains(unix, "PowerShell") || !strings.Contains(unix, "Commands run in sh.") {
		t.Fatalf("shell: windows %q, other %q", windows, unix)
	}
}

func TestSkillsAreListedFromTheirFrontmatter(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "midden-b", "SKILL.md"), "---\nname: midden-b\ndescription: \"Use when B.\"\n---\n# B\n")
	writeTestFile(t, filepath.Join(dir, "midden-a", "SKILL.md"), "---\r\nname: midden-a\r\ndescription: Use when A.\r\n---\r\n")
	writeTestFile(t, filepath.Join(dir, "midden-shared", "sources.md"), "shared guidance")
	writeTestFile(t, filepath.Join(dir, "broken", "SKILL.md"), "# no frontmatter")
	skills := listSkills(dir)
	if len(skills) != 2 || skills[0] != (skillInfo{"midden-a", "Use when A."}) || skills[1] != (skillInfo{"midden-b", "Use when B."}) {
		t.Fatalf("skills = %+v", skills)
	}
	if len(listSkills(filepath.Join(dir, "missing"))) != 0 {
		t.Fatal("a missing skills folder listed skills")
	}
}

func TestASecondLaunchFindsTheRunningApp(t *testing.T) {
	root := t.TempDir()
	release, err := lockData(root)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := lockData(root); !errors.Is(err, errAlreadyRunning) {
		t.Fatalf("a second lock = %v", err)
	}
	app := newTestApp(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: app}
	go server.Serve(listener)
	defer server.Close()
	if _, ok := runningLaunchAddress(app.paths); ok {
		t.Fatal("an address was found before one was written")
	}
	address := app.launchAddress(listener.Addr().String())
	if err := writeLaunchAddress(app.paths, address); err != nil {
		t.Fatal(err)
	}
	if found, ok := runningLaunchAddress(app.paths); !ok || found != address {
		t.Fatalf("running address = %q %v", found, ok)
	}
	writeTestFile(t, launchFilePath(app.paths), strings.Replace(address, app.key, "stale-key", 1))
	if _, ok := runningLaunchAddress(app.paths); ok {
		t.Fatal("a stale address was taken for the running App")
	}
	removeLaunchAddress(app.paths, address)
	if _, err := os.Stat(launchFilePath(app.paths)); err != nil {
		t.Fatal("another App's address was removed")
	}
}
