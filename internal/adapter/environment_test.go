package adapter

import "testing"

func TestExplicitEnvironmentRootsDoNotReadUnrelatedStores(t *testing.T) {
	t.Setenv("MIDDEN_CLAUDE_ROOT", t.TempDir())
	t.Setenv("MIDDEN_COPILOT_ROOT", "")
	t.Setenv("MIDDEN_OPENCODE_DB", "")
	adapters := All()
	if len(adapters) != 1 || adapters[0].Tool() != "claude" {
		t.Fatal("explicit source configuration widened into ambient stores")
	}
}
