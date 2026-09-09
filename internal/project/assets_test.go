package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEveryTargetGetsTheSameSemantics is the one-source property.
//
// Layout differs per driver because Claude, Copilot and OpenCode read different
// directories. The CONTENT must not: a skill that says something different to
// Copilot than to Claude is two products wearing one name.
func TestEveryTargetGetsTheSameSemantics(t *testing.T) {
	src := repoAssets(t)
	bodies := map[string]string{}

	for _, tg := range AssetTargets {
		root := t.TempDir()
		written, err := ProjectAssets(tg, src, root)
		if err != nil {
			t.Fatalf("%s: %v", tg.ID, err)
		}
		if len(written) == 0 {
			t.Fatalf("%s received no assets", tg.ID)
		}
		raw, err := os.ReadFile(filepath.Join(root, tg.SkillsSubdir, "midden-pipeline", "SKILL.md"))
		if err != nil {
			t.Fatalf("%s: pipeline skill missing: %v", tg.ID, err)
		}
		bodies[tg.ID] = string(raw)
	}

	var first, firstID string
	for id, body := range bodies {
		if first == "" {
			first, firstID = body, id
			continue
		}
		if body != first {
			t.Errorf("%s and %s received different pipeline content; semantics "+
				"are canonical and only layout may differ", firstID, id)
		}
	}
}

// TestProjectedAssetsCarryNoModuleVocabulary keeps a host concern out of
// product intelligence.
//
// A skill teaches what Midden DOES. How a host reaches it -- capabilities,
// envelopes, request files -- belongs to that host's own module registration
// and its agent-to-module wiring. A skill naming those is wrong in the two
// shapes that have no module host at all.
func TestProjectedAssetsCarryNoModuleVocabulary(t *testing.T) {
	src := repoAssets(t)
	root := t.TempDir()
	tg, _ := AssetTargetByID("claude-code")
	written, err := ProjectAssets(tg, src, root)
	if err != nil {
		t.Fatal(err)
	}

	banned := []string{"module invoke", "module describe", `"capability"`, "envelope"}
	for _, rel := range written {
		raw, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		body := string(raw)
		for _, b := range banned {
			if strings.Contains(body, b) {
				t.Errorf("%s teaches %q; that is the host's calling convention, "+
					"not Midden's behaviour", rel, b)
			}
		}
	}
}

// TestProjectedAssetsRunOnThisMachine guards the defect an agent actually hit.
//
// The previous skills taught `cat > /tmp/x.json <<'EOF'`, which mangles Windows
// backslashes into invalid JSON. The agent had to write requests in Python
// instead. An example that does not run on the reader's machine is worse than
// no example, and shipping it to three targets triples the defect.
func TestProjectedAssetsRunOnThisMachine(t *testing.T) {
	src := repoAssets(t)
	root := t.TempDir()
	tg, _ := AssetTargetByID("copilot-cli")
	written, err := ProjectAssets(tg, src, root)
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range written {
		raw, _ := os.ReadFile(filepath.Join(root, rel))
		body := string(raw)
		if strings.Contains(body, "<<'EOF'") || strings.Contains(body, "cat > /tmp/") {
			t.Errorf("%s teaches a bash heredoc writing to /tmp; it produces "+
				"invalid JSON on Windows and the reader cannot run it", rel)
		}
	}
}

// TestEmptySourceIsRefused: a projection that placed nothing looks identical to
// one that succeeded, and the target installs an empty directory.
func TestEmptySourceIsRefused(t *testing.T) {
	tg, _ := AssetTargetByID("claude-code")
	if _, err := ProjectAssets(tg, t.TempDir(), t.TempDir()); err == nil {
		t.Error("projecting from an empty source reported success")
	}
}

func repoAssets(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", "assets"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("canonical assets not present: %v", err)
	}
	return dir
}
