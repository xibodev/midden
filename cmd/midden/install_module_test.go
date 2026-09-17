package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/mekjr1/midden/internal/module"
)

func TestNormalInstallCommandRegistersPackagedStudioModule(t *testing.T) {
	bin := buildModuleBinary(t)
	host := filepath.Join(t.TempDir(), "fakehost")
	if runtime.GOOS == "windows" {
		host += ".exe"
	}
	buildHost := exec.Command("go", "build", "-trimpath", "-o", host, "../../internal/install/testdata/fakehost")
	if out, err := buildHost.CombinedOutput(); err != nil {
		t.Fatalf("build fake host: %v\n%s", err, out)
	}

	hostHome := t.TempDir()
	cmd := exec.Command(bin, "install", "--only", "facet-studio", "--host", host)
	cmd.Env = append(os.Environ(), "MIDDEN_FAKE_MODULE_HOST="+hostHome)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("normal Studio install: %v\n%s", err, out)
	}

	var state struct {
		ModuleID string `json:"module_id"`
		Binary   string `json:"binary"`
		Enabled  bool   `json:"enabled"`
	}
	rawState, err := os.ReadFile(filepath.Join(hostHome, "host-state.json"))
	if err != nil {
		t.Fatalf("read fake host state: %v", err)
	}
	if err := json.Unmarshal(rawState, &state); err != nil {
		t.Fatalf("decode fake host state: %v", err)
	}
	if state.ModuleID != module.ModuleID || !state.Enabled || state.Binary == "" {
		t.Fatalf("installed state = %#v", state)
	}

	descriptor := module.Describe()
	checkContent := func(path, digest string) {
		t.Helper()
		installedPath := filepath.Join(filepath.Dir(state.Binary), filepath.FromSlash(path))
		raw, err := os.ReadFile(installedPath)
		if err != nil {
			t.Fatalf("normal install omitted %s: %v", path, err)
		}
		if actual := module.DigestSHA256(raw); actual != digest {
			t.Fatalf("normal install %s digest = %s, want %s", path, actual, digest)
		}
	}
	for _, content := range descriptor.AgentOverlays {
		checkContent(content.Path, content.Digest)
	}
	for _, content := range descriptor.Skills {
		checkContent(content.Path, content.Digest)
	}
}
