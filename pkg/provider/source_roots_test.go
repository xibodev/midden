package provider

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mekjr1/midden/internal/module"
	"github.com/xibodev/facet-studio/pkg/agent"
)

func TestExplicitSourceRootsForwardOnlyAvailableStoresReadOnly(t *testing.T) {
	base := t.TempDir()
	claude := filepath.Join(base, "claude")
	copilot := filepath.Join(base, "copilot")
	opencode := filepath.Join(base, "opencode.db")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(copilot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(claude, "projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(copilot, "session-store.db"), []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(opencode, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}

	roots := (SourceRoots{
		Claude:   claude,
		Copilot:  copilot,
		Opencode: opencode,
	}).moduleRoots()
	for name, want := range map[string]string{
		module.RootClaude: claude, module.RootCopilot: copilot, module.RootOpencode: opencode,
	} {
		root, ok := roots[name]
		if !ok {
			t.Errorf("explicit root %s was omitted", name)
			continue
		}
		if root.Mode != "ro" {
			t.Errorf("root %s mode = %q, want ro", name, root.Mode)
		}
		if root.Path != filepath.Clean(want) {
			t.Errorf("root %s path = %q, want %q", name, root.Path, want)
		}
	}
}

func TestExplicitSourceRootsOmitMissingAndWrongShapes(t *testing.T) {
	base := t.TempDir()
	wrongClaude := filepath.Join(base, "claude-file")
	wrongOpencode := filepath.Join(base, "opencode-dir")
	if err := os.WriteFile(wrongClaude, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(wrongOpencode, 0o755); err != nil {
		t.Fatal(err)
	}

	roots := (SourceRoots{
		Claude:   wrongClaude,
		Copilot:  filepath.Join(base, "missing"),
		Opencode: wrongOpencode,
	}).moduleRoots()
	if len(roots) != 0 {
		t.Fatalf("invalid or unavailable roots were forwarded: %#v", roots)
	}
}

func TestRegisteredToolsCaptureExplicitRootsAndSeparateState(t *testing.T) {
	base := t.TempDir()
	workspace := filepath.Join(base, "workspace")
	claude := filepath.Join(base, "claude")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(claude, "projects"), 0o755); err != nil {
		t.Fatal(err)
	}

	p := NewMiddenToolProvider(WithSourceRoots(SourceRoots{
		Claude:  claude,
		Copilot: filepath.Join(base, "missing-copilot"),
	}))
	var registered *middenTool
	p.RegisterTools(workspace, func(tool agent.Tool) {
		if tool.Name() == "midden_sessions_list" {
			registered, _ = tool.(*middenTool)
		}
	})
	if registered == nil {
		t.Fatal("sessions.list tool was not registered")
	}
	if !registered.strictRoots {
		t.Fatal("explicit source configuration did not make registered tool fail closed")
	}
	if len(registered.sourceRoots) != 1 {
		t.Fatalf("registered roots = %#v, want only Claude", registered.sourceRoots)
	}
	root, ok := registered.sourceRoots[module.RootClaude]
	if !ok || root.Path != filepath.Clean(claude) || root.Mode != "ro" {
		t.Fatalf("registered Claude root = %#v, want %q read-only", root, claude)
	}
	if _, ok := registered.sourceRoots[module.RootCopilot]; ok {
		t.Fatal("missing Copilot root was invented or forwarded")
	}
	if registered.stateRoot == workspace {
		t.Fatal("Midden state root equals host workspace")
	}
	wantState, err := ModuleStateRoot(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if registered.stateRoot != wantState {
		t.Fatalf("registered state root = %q, want %q", registered.stateRoot, wantState)
	}
}
