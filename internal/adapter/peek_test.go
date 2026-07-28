package adapter

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mekjr1/midden/internal/core"
)

// claudeFixture writes a minimal transcript and returns its path.
func claudeFixture(t *testing.T, root, project, id, cwd, summary string) string {
	t.Helper()

	dir := filepath.Join(root, "projects", project)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	line := `{"type":"summary","summary":"` + summary + `","cwd":"` + cwd +
		`","timestamp":"2026-07-01T10:00:00.000Z"}` + "\n"
	// Pad past the minimum-size filter so the session is not skipped as an
	// abandoned stub.
	pad := `{"type":"user","cwd":"` + cwd + `","message":{"content":"` +
		repeat("x", 3000) + `"}}` + "\n"

	path := filepath.Join(dir, id+".jsonl")
	if err := os.WriteFile(path, []byte(line+pad), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func repeat(s string, n int) string {
	out := make([]byte, 0, len(s)*n)
	for i := 0; i < n; i++ {
		out = append(out, s...)
	}
	return string(out)
}

// TestPeekCacheAvoidsReadingTranscripts is the regression guard for the defect
// that made every listing take minutes: describing a Claude session opened its
// transcript, and nothing reused the answer between runs.
//
// The cache is seeded with a title that does not appear in the file, so a hit
// is only possible if the file was not read.
func TestPeekCacheAvoidsReadingTranscripts(t *testing.T) {
	t.Cleanup(func() { SetPeekCache(nil) })

	root := t.TempDir()
	path := claudeFixture(t, root, "proj", "11111111-2222-3333-4444-555555555555",
		filepath.ToSlash(root), "title from file")

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	SetPeekCache(map[string]PeekEntry{
		path: {
			Cwd:     filepath.Join(root, "workdir"),
			Title:   "title from cache",
			Created: time.Unix(1_700_000_000, 0),
			Size:    fi.Size(),
			ModUnix: fi.ModTime().Unix(),
		},
	})

	c := &Claude{Root: root}
	got, err := c.Sessions(core.Scope{IncludeNoise: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 session, got %d", len(got))
	}
	if got[0].Title != "title from cache" {
		t.Errorf("cache ignored: title = %q, want the cached value", got[0].Title)
	}
	if got[0].Dir != filepath.Join(root, "workdir") {
		t.Errorf("cache ignored: dir = %q", got[0].Dir)
	}
}

// TestPeekCacheRejectsChangedFiles proves the cache cannot serve stale data:
// a transcript that has grown must be re-read, because its title and working
// directory may now differ.
func TestPeekCacheRejectsChangedFiles(t *testing.T) {
	t.Cleanup(func() { SetPeekCache(nil) })

	root := t.TempDir()
	path := claudeFixture(t, root, "proj", "11111111-2222-3333-4444-555555555555",
		filepath.ToSlash(root), "title from file")

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	for name, entry := range map[string]PeekEntry{
		"stale size":  {Cwd: root, Title: "cached", Size: fi.Size() - 1, ModUnix: fi.ModTime().Unix()},
		"stale mtime": {Cwd: root, Title: "cached", Size: fi.Size(), ModUnix: fi.ModTime().Unix() - 1},
		"no cwd":      {Cwd: "", Title: "cached", Size: fi.Size(), ModUnix: fi.ModTime().Unix()},
	} {
		t.Run(name, func(t *testing.T) {
			SetPeekCache(map[string]PeekEntry{path: entry})

			c := &Claude{Root: root}
			got, err := c.Sessions(core.Scope{IncludeNoise: true})
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 {
				t.Fatalf("want 1 session, got %d", len(got))
			}
			if got[0].Title != "title from file" {
				t.Errorf("served stale cache: title = %q, want the file's value", got[0].Title)
			}
		})
	}
}

// TestSessionsWithoutPeekCache confirms the cache is an optimisation and not a
// dependency: with none installed, the file is read and the result is the same.
func TestSessionsWithoutPeekCache(t *testing.T) {
	t.Cleanup(func() { SetPeekCache(nil) })
	SetPeekCache(nil)

	root := t.TempDir()
	claudeFixture(t, root, "proj", "11111111-2222-3333-4444-555555555555",
		filepath.ToSlash(root), "title from file")

	c := &Claude{Root: root}
	got, err := c.Sessions(core.Scope{IncludeNoise: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Title != "title from file" {
		t.Fatalf("got %+v", got)
	}
}
