package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/mekjr1/midden/internal/module"
)

func TestModulePackageMaterializesInstallableEmbeddedContent(t *testing.T) {
	bin := buildModuleBinary(t)
	outDir := filepath.Join(t.TempDir(), "package")
	cmd := commandWithEmptyEnv(bin, "module", "package", "--out", outDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("package: %v\n%s", err, out)
	}

	name := "midden"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if _, err := os.Stat(filepath.Join(outDir, name)); err != nil {
		t.Fatalf("packaged binary missing: %v", err)
	}
	d := module.Describe()
	checkContent := func(path, digest string) {
		t.Helper()
		got, err := os.ReadFile(filepath.Join(outDir, filepath.FromSlash(path)))
		if err != nil {
			t.Fatalf("packaged content %s missing: %v", path, err)
		}
		want, ok := module.OverlayContent(path)
		if !ok || string(got) != string(want) {
			t.Errorf("packaged content %s differs from embedded bytes", path)
		}
		if actual := module.DigestSHA256(got); actual != digest {
			t.Errorf("packaged content %s digest = %s, want %s", path, actual, digest)
		}
	}
	for _, content := range d.AgentOverlays {
		checkContent(content.Path, content.Digest)
	}
	for _, content := range d.Skills {
		checkContent(content.Path, content.Digest)
	}
}

func commandWithEmptyEnv(bin string, args ...string) *exec.Cmd {
	cmd := exec.Command(bin, args...)
	cmd.Env = []string{}
	return cmd
}
