package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestEditorialSkillInstallsAndUninstallsWithOwnedBundle(t *testing.T) {
	target := skillsTarget(t)
	if _, err := InstallSkills(target, false); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(target.SkillsDir, "midden-editorial-production", "SKILL.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("primary editorial skill was not installed: %v", err)
	}
	parts := strings.SplitN(strings.ReplaceAll(string(raw), "\r\n", "\n"), "---", 3)
	if len(parts) < 3 {
		t.Fatal("installed skill has no frontmatter")
	}
	var front struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}
	if err = yaml.Unmarshal([]byte(parts[1]), &front); err != nil || front.Name != "midden-editorial-production" || front.Description == "" {
		t.Fatalf("undiscoverable skill: %+v %v", front, err)
	}
	if err = VerifySkillsInstall(target); err != nil {
		t.Fatal(err)
	}
	if _, err = UninstallSkills(target); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("owned editorial skill survived uninstall")
	}
}
