package install

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/mekjr1/midden/internal/module"
)

const fakeHostEnv = "MIDDEN_FAKE_MODULE_HOST"

type fakeHostState struct {
	ModuleID string `json:"module_id"`
	Binary   string `json:"binary"`
	Enabled  bool   `json:"enabled"`
	Adds     int    `json:"adds"`
}

func TestFakeHostDetachedModuleInstallerLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the detached Midden binary")
	}
	home := t.TempDir()
	userFile := filepath.Join(home, "user-owned.txt")
	if err := os.WriteFile(userFile, []byte("preserve me"), 0o600); err != nil {
		t.Fatal(err)
	}

	moduleBinary := filepath.Join(t.TempDir(), "midden")
	if runtime.GOOS == "windows" {
		moduleBinary += ".exe"
	}
	build := exec.Command("go", "build", "-trimpath", "-o", moduleBinary, "../../cmd/midden")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build detached module: %v\n%s", err, out)
	}

	host := filepath.Join(t.TempDir(), "fakehost")
	if runtime.GOOS == "windows" {
		host += ".exe"
	}
	buildHost := exec.Command("go", "build", "-trimpath", "-o", host, "./testdata/fakehost")
	if out, err := buildHost.CombinedOutput(); err != nil {
		t.Fatalf("build fake host: %v\n%s", err, out)
	}
	t.Setenv(fakeHostEnv, home)
	target := Target{ID: "fake-studio", Name: "Fake Studio host", Kind: KindModule, BinaryPath: host, Detected: true}

	for i := 1; i <= 2; i++ {
		result, err := RegisterBinaryWithHost(target, moduleBinary)
		if err != nil {
			t.Fatalf("registration %d: %v (%s)", i, err, result.Output)
		}
	}
	state := inspectFakeHost(t, host)
	if state.ModuleID != "midden" || !state.Enabled || state.Adds != 2 {
		t.Fatalf("installed state = %#v", state)
	}
	if state.Binary == moduleBinary {
		t.Fatal("fake host referenced source binary instead of installing its own copy")
	}
	if _, err := os.Stat(state.Binary); err != nil {
		t.Fatalf("installed binary missing: %v", err)
	}
	descriptor := module.Describe()
	checkContent := func(path, digest string) {
		t.Helper()
		installedPath := filepath.Join(filepath.Dir(state.Binary), filepath.FromSlash(path))
		raw, err := os.ReadFile(installedPath)
		if err != nil {
			t.Fatalf("installed declared content %s missing: %v", path, err)
		}
		if actual := module.DigestSHA256(raw); actual != digest {
			t.Fatalf("installed declared content %s digest = %s, want %s", path, actual, digest)
		}
	}
	for _, content := range descriptor.AgentOverlays {
		checkContent(content.Path, content.Digest)
	}
	for _, content := range descriptor.Skills {
		checkContent(content.Path, content.Digest)
	}

	request := filepath.Join(t.TempDir(), "content-types.json")
	requestBody := fmt.Sprintf(`{"protocol":"xibodev.module/v1","capability":"content.types","request_id":"fake-host-invoke","input":{},"roots":{"midden_home":{"path":%q,"mode":"rw"}}}`, filepath.ToSlash(filepath.Join(home, "module-state")))
	if err := os.WriteFile(request, []byte(requestBody), 0o600); err != nil {
		t.Fatal(err)
	}
	invoke := exec.Command(state.Binary, "module", "invoke", "content.types", "--input", request)
	out, err := invoke.Output()
	if err != nil {
		t.Fatalf("invoke installed module: %v", err)
	}
	var envelope struct {
		Protocol  string `json:"protocol"`
		Module    string `json:"module"`
		Operation string `json:"operation"`
		OK        bool   `json:"ok"`
		Execution struct {
			Local         bool     `json:"local"`
			EstimatedCost *float64 `json:"estimated_cost"`
			ActualCost    *float64 `json:"actual_cost"`
			Artifacts     []any    `json:"artifacts"`
		} `json:"execution"`
	}
	if err := json.Unmarshal(out, &envelope); err != nil {
		t.Fatalf("installed module emitted invalid JSON: %v\n%s", err, out)
	}
	if !envelope.OK || envelope.Protocol != "xibodev.module/v1" || envelope.Module != "midden" || envelope.Operation != "invoke" {
		t.Fatalf("invalid invoke envelope: %#v", envelope)
	}
	if !envelope.Execution.Local || envelope.Execution.EstimatedCost == nil || *envelope.Execution.EstimatedCost != 0 || envelope.Execution.ActualCost == nil || *envelope.Execution.ActualCost != 0 || envelope.Execution.Artifacts == nil {
		t.Fatalf("invalid execution metadata: %#v", envelope.Execution)
	}

	runFakeHostCommand(t, host, "modules-disable", "midden")
	if inspectFakeHost(t, host).Enabled {
		t.Fatal("module remained enabled")
	}
	runFakeHostCommand(t, host, "modules-remove", "midden")
	if _, err := os.Stat(filepath.Join(home, "modules", "midden")); !os.IsNotExist(err) {
		t.Fatalf("module install survived removal: %v", err)
	}
	if got, err := os.ReadFile(userFile); err != nil || string(got) != "preserve me" {
		t.Fatalf("host lifecycle changed user-owned file: %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(home, "module-state", "index.db")); err != nil {
		t.Fatalf("module-owned state was removed with installation: %v", err)
	}
}

func inspectFakeHost(t *testing.T, host string) fakeHostState {
	t.Helper()
	out := runFakeHostCommand(t, host, "modules-list")
	var state fakeHostState
	if err := json.Unmarshal(out, &state); err != nil {
		t.Fatalf("decode host inspection: %v\n%s", err, out)
	}
	return state
}

func runFakeHostCommand(t *testing.T, host string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command(host, args...)
	cmd.Env = append(os.Environ(), fakeHostEnv+"="+os.Getenv(fakeHostEnv))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("fake host %v: %v\n%s", args, err, out)
	}
	return out
}
