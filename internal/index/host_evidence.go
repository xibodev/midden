package index

import (
	"strings"
)

// PutHostEvidence atomically stores a validated bounded host extraction.
// Stable content-derived IDs make retrying the same submission idempotent.
func (d *DB) PutHostEvidence(items []Nugget) (int, error) {
	tx, err := d.sql.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	stored := 0
	for _, n := range items {
		result, e := tx.Exec(`INSERT INTO nuggets(uid,tool,session_id,kind,title,body,tags,workspace,repo,confidence,model,redacted,turn_ref,created_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(uid) DO NOTHING`, n.UID, n.Tool, n.SessionID, n.Kind, n.Title, n.Body,
			strings.Join(n.Tags, ","), n.Workspace, n.Repo, n.Confidence, n.Model, n.Redacted, n.TurnRef, n.CreatedAt.Unix())
		if e != nil {
			return 0, e
		}
		count, e := result.RowsAffected()
		if e != nil {
			return 0, e
		}
		stored += int(count)
	}
	return stored, tx.Commit()
}
