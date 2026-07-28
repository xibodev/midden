package index

import (
	"time"

	"github.com/mekjr1/midden/internal/adapter"
)

// PeekCache returns transcript metadata recorded by earlier scans, keyed by
// transcript path, so adapters can avoid re-opening files that have not
// changed.
//
// Only rows that carry a transcript path are useful here; stores that keep
// their metadata in a database describe themselves cheaply already.
func (d *DB) PeekCache() map[string]adapter.PeekEntry {
	rows, err := d.sql.Query(`
		SELECT transcript, COALESCE(dir,''), COALESCE(title,''),
		       COALESCE(created,0), COALESCE(bytes,0),
		       COALESCE(updated,0), COALESCE(noise,0)
		FROM sessions
		WHERE COALESCE(transcript,'') != '' AND COALESCE(dir,'') != ''`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	out := map[string]adapter.PeekEntry{}
	for rows.Next() {
		var (
			path, dir, title string
			created, bytes   int64
			updated          int64
			noise            int
		)
		if rows.Scan(&path, &dir, &title, &created, &bytes, &updated, &noise) != nil {
			continue
		}
		e := adapter.PeekEntry{
			Cwd:     dir,
			Title:   title,
			Noise:   noise == 1,
			Size:    bytes,
			ModUnix: updated,
		}
		if created > 0 {
			e.Created = time.Unix(created, 0)
		}
		out[path] = e
	}
	return out
}

// WarmPeekCache installs the index's transcript metadata into the adapter
// layer. It is best-effort: a missing or empty index only costs speed.
func WarmPeekCache() {
	db, err := Open()
	if err != nil {
		return
	}
	defer db.Close()

	if m := db.PeekCache(); len(m) > 0 {
		adapter.SetPeekCache(m)
	}
}
