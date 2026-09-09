package project

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Asset projection.
//
// One canonical pipeline definition, rendered per target. The semantics are
// Midden's and identical everywhere; the LAYOUT and the invocation idiom differ
// because Claude, Copilot and OpenCode read different directories and describe
// commands differently.
//
// Two rules the canonical source must keep, both learned from watching a cold
// agent use these:
//
//  1. NO MODULE VOCABULARY. A skill teaches what Midden does, not how a host
//     reaches it. When Midden ships as a module, that host's registration and
//     its own agent-to-module wiring own the calling convention entirely. A
//     skill that names capabilities or envelopes leaks a host concern into
//     product intelligence and is wrong in the other two shapes.
//
//  2. NO SHELL-SPECIFIC EXAMPLES. The previous skills taught
//     `cat > /tmp/x.json <<'EOF'`, which mangles Windows paths into invalid
//     JSON. An agent hit exactly that and had to write requests in Python
//     instead. An example that does not run on the reader's machine is worse
//     than no example.

// AssetTarget describes where one driver reads its assets from.
type AssetTarget struct {
	ID string

	// SkillsSubdir and AgentsSubdir are relative to the target's own root.
	SkillsSubdir string
	AgentsSubdir string
}

// AssetTargets is the set Midden projects onto.
var AssetTargets = []AssetTarget{
	{ID: "claude-code", SkillsSubdir: ".claude/skills", AgentsSubdir: ".claude/agents"},
	{ID: "copilot-cli", SkillsSubdir: ".copilot/skills", AgentsSubdir: ".copilot/agents"},
	{ID: "opencode", SkillsSubdir: ".config/opencode/skills", AgentsSubdir: ".config/opencode/agents"},
	// Standalone embeds its assets rather than placing them where another
	// harness scans, so it has no subdirectory of its own.
	{ID: "standalone", SkillsSubdir: "skills", AgentsSubdir: "agents"},
}

// AssetTargetByID returns a target and whether it exists.
func AssetTargetByID(id string) (AssetTarget, bool) {
	for _, t := range AssetTargets {
		if t.ID == id {
			return t, true
		}
	}
	return AssetTarget{}, false
}

// ProjectAssets renders the canonical assets for one target beneath root.
//
// Returns the relative paths written, so a caller can report what a target
// actually received rather than asserting that it received something.
func ProjectAssets(t AssetTarget, sourceDir, root string) ([]string, error) {
	var written []string

	for _, group := range []struct{ src, dst string }{
		{filepath.Join(sourceDir, "skills"), t.SkillsSubdir},
		{filepath.Join(sourceDir, "agents"), t.AgentsSubdir},
	} {
		paths, err := copyTree(group.src, filepath.Join(root, group.dst), group.dst)
		if err != nil {
			return nil, err
		}
		written = append(written, paths...)
	}

	if len(written) == 0 {
		// A projection that placed nothing looks identical to one that
		// succeeded, and the target would install an empty directory while
		// reporting success.
		return nil, fmt.Errorf("no assets found under %s; the projection would "+
			"place nothing while reporting success", sourceDir)
	}
	return written, nil
}

func copyTree(src, dst, relPrefix string) ([]string, error) {
	entries, err := os.ReadDir(src)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		s := filepath.Join(src, e.Name())
		d := filepath.Join(dst, e.Name())
		if e.IsDir() {
			nested, err := copyTree(s, d, relPrefix+"/"+e.Name())
			if err != nil {
				return nil, err
			}
			out = append(out, nested...)
			continue
		}
		raw, err := os.ReadFile(s)
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(d), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(d, raw, 0o644); err != nil {
			return nil, err
		}
		out = append(out, strings.TrimPrefix(relPrefix+"/"+e.Name(), "/"))
	}
	return out, nil
}
