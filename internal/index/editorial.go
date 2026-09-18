package index

import (
	"encoding/json"
	"fmt"
	"time"
)

func (d *DB) migrateEditorial() error {
	_, err := d.sql.Exec(`
CREATE TABLE IF NOT EXISTS editorial_projects (
  id TEXT PRIMARY KEY,
  revision INTEGER NOT NULL,
  document TEXT NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS editorial_revisions (
  project_id TEXT NOT NULL REFERENCES editorial_projects(id),
  revision INTEGER NOT NULL,
  document TEXT NOT NULL,
  PRIMARY KEY(project_id, revision)
);`)
	return err
}

// SaveEditorial fences concurrent writers and links a selected recipe in the
// same transaction. A failed selection cannot leave an orphaned recipe.
func (d *DB) SaveEditorial(id string, expected int, document json.RawMessage, recipe *Recipe) error {
	if id == "" || expected < 0 || !json.Valid(document) {
		return fmt.Errorf("valid project id, revision and document are required")
	}
	tx, err := d.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if expected == 0 {
		_, err = tx.Exec("INSERT INTO editorial_projects(id,revision,document,updated_at) VALUES(?,?,?,?)", id, 1, string(document), time.Now().UnixNano())
	} else {
		result, e := tx.Exec("UPDATE editorial_projects SET revision=?,document=?,updated_at=? WHERE id=? AND revision=?",
			expected+1, string(document), time.Now().UnixNano(), id, expected)
		if e != nil {
			return e
		}
		n, e := result.RowsAffected()
		if e != nil {
			return e
		}
		if n != 1 {
			return fmt.Errorf("stale project revision; inspect the project before retrying")
		}
	}
	if err != nil {
		return err
	}
	if _, err = tx.Exec("INSERT INTO editorial_revisions(project_id,revision,document) VALUES(?,?,?)", id, expected+1, string(document)); err != nil {
		return err
	}
	if recipe != nil {
		outputs, e := json.Marshal(recipe.Outputs)
		if e != nil {
			return e
		}
		evidence, e := json.Marshal(recipe.EvidenceIDs)
		if e != nil {
			return e
		}
		_, err = tx.Exec(`INSERT INTO refinery_recipes(uid,title,workspace,request,status,outputs,evidence_ids,created_at,updated_at,approved_at)
VALUES(?,?,?,?,?,?,?,?,?,NULL)`, recipe.UID, recipe.Title, recipe.Workspace, recipe.Request, recipe.Status,
			string(outputs), string(evidence), recipe.CreatedAt.Unix(), recipe.UpdatedAt.Unix())
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (d *DB) Editorial(id string, revision int) (json.RawMessage, error) {
	var raw string
	var err error
	if revision == 0 {
		err = d.sql.QueryRow("SELECT document FROM editorial_projects WHERE id=?", id).Scan(&raw)
	} else {
		err = d.sql.QueryRow("SELECT document FROM editorial_revisions WHERE project_id=? AND revision=?", id, revision).Scan(&raw)
	}
	return json.RawMessage(raw), err
}

func (d *DB) EditorialList(limit, offset int) ([]json.RawMessage, int, error) {
	if limit < 1 || limit > 50 || offset < 0 {
		return nil, 0, fmt.Errorf("limit must be 1..50 and offset nonnegative")
	}
	var total int
	if err := d.sql.QueryRow("SELECT COUNT(*) FROM editorial_projects").Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := d.sql.Query("SELECT document FROM editorial_projects ORDER BY updated_at DESC,id LIMIT ? OFFSET ?", limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := make([]json.RawMessage, 0)
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			return nil, 0, err
		}
		out = append(out, json.RawMessage(raw))
	}
	return out, total, rows.Err()
}
