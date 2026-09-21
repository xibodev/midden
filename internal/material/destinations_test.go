package material

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/mekjr1/midden/internal/adapter"
)

func TestCheckDestinationProtectsStoresWithoutCreatingPaths(t *testing.T) {
	root := t.TempDir()
	copilot := filepath.Join(root, "copilot")
	claude := filepath.Join(root, "claude")
	service := Service{Roots: adapter.Roots{Copilot: copilot, Claude: claude, Strict: true}}
	for _, store := range []string{copilot, claude} {
		for _, destination := range []string{store, filepath.Join(store, "nested", "output.json")} {
			if err := service.CheckDestination(destination); err == nil {
				t.Fatalf("source-store destination accepted: %s", destination)
			}
		}
		if _, err := os.Stat(store); !os.IsNotExist(err) {
			t.Fatal("destination validation created a source-store directory")
		}
	}
	if err := service.CheckDestination(filepath.Join(root, "copilot-other", "output.json")); err != nil {
		t.Fatalf("a sibling path was mistaken for a source store: %v", err)
	}
	if err := service.CheckDestination(""); err == nil {
		t.Fatal("empty output destination was accepted")
	}
}

func TestCheckDestinationProtectsDatabaseFilesNotTheirWorkspace(t *testing.T) {
	workspace := t.TempDir()
	database := filepath.Join(workspace, "opencode.db")
	service := Service{Roots: adapter.Roots{Opencode: database, Strict: true}}
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		reserved := database + suffix
		if err := service.CheckDestination(reserved); err == nil {
			t.Fatalf("source database path accepted: %s", reserved)
		}
		if err := service.CheckDestination(filepath.Join(reserved, "cache", "view.json")); err == nil {
			t.Fatal("a reserved database filename could be replaced by a cache directory")
		}
		if _, err := os.Stat(reserved); !os.IsNotExist(err) {
			t.Fatal("checking a reserved database name created it")
		}
	}
	for _, destination := range []string{
		filepath.Join(workspace, "notes.json"),
		filepath.Join(workspace, "cache", "views", "view.json"),
		filepath.Join(workspace, "exports", "assets"),
		database + ".backup",
	} {
		if err := service.CheckDestination(destination); err != nil {
			t.Fatalf("ordinary workspace output rejected: %v", err)
		}
	}
}

func TestCheckDestinationResolvesSourceAliases(t *testing.T) {
	root := t.TempDir()
	store := filepath.Join(root, "source")
	if err := os.Mkdir(store, 0700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(store, "events.jsonl")
	if err := os.WriteFile(source, []byte("synthetic source"), 0600); err != nil {
		t.Fatal(err)
	}
	directoryAlias := filepath.Join(root, "store-alias")
	if err := os.Symlink(store, directoryAlias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	fileAlias := filepath.Join(root, "file-alias.jsonl")
	if err := os.Symlink(source, fileAlias); err != nil {
		t.Fatal(err)
	}
	service := Service{Roots: adapter.Roots{Copilot: store, Strict: true}}
	for _, destination := range []string{fileAlias, filepath.Join(directoryAlias, "new", "output.json")} {
		if err := service.CheckDestination(destination); err == nil {
			t.Fatal("a source-store alias bypassed destination validation")
		}
	}
	raw, err := os.ReadFile(source)
	if err != nil || string(raw) != "synthetic source" {
		t.Fatal("destination validation modified the source")
	}
	if _, err = os.Stat(filepath.Join(store, "new")); !os.IsNotExist(err) {
		t.Fatal("destination validation created source-store ancestors")
	}
}

func TestViewCacheCannotBeWrittenInsideSourceStore(t *testing.T) {
	for _, position := range []string{"store itself", "store child"} {
		t.Run(position, func(t *testing.T) {
			service, source, path := fixture(t)
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			service.State = service.Roots.Claude
			if position == "store child" {
				service.State = filepath.Join(service.State, "new-state")
			}
			if _, err = service.Open(source, ReadOptions{}); err == nil {
				t.Fatal("view cache was written inside a source store")
			}
			if _, err = os.Stat(filepath.Join(service.State, "views")); !os.IsNotExist(err) {
				t.Fatal("rejected cache destination created directories")
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("view creation modified the source transcript")
			}
		})
	}
}

func TestViewCacheCannotFollowAnAliasIntoSourceStore(t *testing.T) {
	service, source, _ := fixture(t)
	if err := os.Mkdir(service.State, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(service.Roots.Claude, filepath.Join(service.State, "views")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := service.Open(source, ReadOptions{}); err == nil {
		t.Fatal("view cache followed a source-store alias")
	}
	entries, err := os.ReadDir(service.Roots.Claude)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "projects" {
		t.Fatal("cache validation wrote files into the source store")
	}
}

func TestOpenCodeWorkspaceStillAllowsCacheCollectionsAndAssets(t *testing.T) {
	service, source, payload := nonTextOpencodeFixture(t)
	workspace := filepath.Dir(service.Roots.Opencode)
	service.State = filepath.Join(workspace, "midden-state")
	view, err := service.Open(source, ReadOptions{})
	if err != nil {
		t.Fatalf("cache beside a database was rejected: %v", err)
	}
	listed, err := service.Assets(view.ID, nil, "")
	if err != nil || len(listed.Assets) != 1 {
		t.Fatalf("list=%+v err=%v", listed, err)
	}
	extracted, err := service.Assets(view.ID, []string{listed.Assets[0].RecordID}, filepath.Join(workspace, "extracted"))
	if err != nil || extracted.CopiedCount != 1 {
		t.Fatalf("ordinary output beside the database was rejected: %+v %v", extracted, err)
	}
	out := filepath.Join(workspace, "collection")
	if _, err = service.Collect(CollectOptions{Views: []string{view.ID}, Records: []string{view.Records[0].ID}, Out: out, IncludeAssets: true}); err != nil {
		t.Fatalf("collection beside a database was rejected: %v", err)
	}
	assertCollectionAssets(t, out, 1, payload)
}

func TestViewCacheCannotClaimDatabaseSidecarNames(t *testing.T) {
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		t.Run(suffix, func(t *testing.T) {
			service, source, _ := nonTextOpencodeFixture(t)
			service.State = service.Roots.Opencode + suffix
			if _, err := service.Open(source, ReadOptions{}); err == nil {
				t.Fatal("view cache claimed a source database sidecar path")
			}
			if _, err := os.Stat(service.State); !os.IsNotExist(err) {
				t.Fatal("cache validation created a database sidecar")
			}
		})
	}
}
