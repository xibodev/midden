package material

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mekjr1/midden/internal/adapter"
)

func TestCheckDestinationProtectsResolvedDatabaseCompanions(t *testing.T) {
	root := t.TempDir()
	database := filepath.Join(root, "physical.db")
	if err := os.WriteFile(database, []byte("synthetic database"), 0600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "configured.db")
	if err := os.Symlink(database, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	service := Service{Roots: adapter.Roots{Opencode: alias, Strict: true}}
	for _, base := range []string{alias, database} {
		for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
			if err := service.CheckDestination(base + suffix); err == nil {
				t.Fatalf("database alias or companion was writable: %s", base+suffix)
			}
		}
	}
}

func TestAssetDestinationStillRequiresExistingParent(t *testing.T) {
	service, view, _, _ := materialAssetFixture(t)
	parent := filepath.Join(t.TempDir(), "missing")
	out := filepath.Join(parent, "assets")
	if _, err := service.Assets(view.ID, []string{view.Records[1].ID}, out); err == nil {
		t.Fatal("asset extraction created a missing output parent")
	}
	if _, err := os.Stat(parent); !os.IsNotExist(err) {
		t.Fatal("asset extraction changed its existing-parent contract")
	}
}
