package index

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

func NewUID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("uid%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
func escapeLike(value string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(value)
}

// Artifact is a derived file index entry, not an editorial lifecycle object.
type Artifact struct {
	UID       string    `json:"uid"`
	Kind      string    `json:"kind"`
	Title     string    `json:"title"`
	Path      string    `json:"path"`
	Scope     string    `json:"scope"`
	CreatedAt time.Time `json:"created_at"`
}

func (d *DB) PutArtifact(a Artifact) error {
	if a.UID == "" {
		a.UID = NewUID()
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now()
	}
	tx, err := d.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if a.Path != "" {
		if _, err = tx.Exec(`DELETE FROM artifacts WHERE path=?`, a.Path); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(`INSERT INTO artifacts(uid,kind,title,path,scope,created_at) VALUES(?,?,?,?,?,?)`, a.UID, a.Kind, a.Title, a.Path, a.Scope, a.CreatedAt.Unix()); err != nil {
		return err
	}
	return tx.Commit()
}

func (d *DB) Artifacts(limit int) ([]Artifact, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		return nil, fmt.Errorf("artifact limit must be at most 1000")
	}
	rows, err := d.sql.Query(`SELECT uid,kind,COALESCE(title,''),COALESCE(path,''),COALESCE(scope,''),created_at FROM artifacts ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Artifact{}
	for rows.Next() {
		var a Artifact
		var created int64
		if err = rows.Scan(&a.UID, &a.Kind, &a.Title, &a.Path, &a.Scope, &created); err != nil {
			return nil, err
		}
		a.CreatedAt = time.Unix(created, 0)
		out = append(out, a)
	}
	return out, rows.Err()
}

type Operation struct {
	UID       string    `json:"uid"`
	Op        string    `json:"op"`
	Tool      string    `json:"tool"`
	SessionID string    `json:"session_id"`
	Before    int64     `json:"before"`
	After     int64     `json:"after"`
	Detail    string    `json:"detail"`
	OK        bool      `json:"ok"`
	CreatedAt time.Time `json:"created_at"`
}

func (d *DB) Operations(limit int) ([]Operation, error) {
	out, _, err := d.OperationsPage(limit, 0)
	return out, err
}
func (d *DB) OperationsPage(limit, offset int) ([]Operation, int, error) {
	if limit <= 0 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}
	if offset < 0 {
		return nil, 0, fmt.Errorf("offset must be non-negative")
	}
	var total int
	if err := d.sql.QueryRow(`SELECT COUNT(*) FROM operations`).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := d.sql.Query(`SELECT uid,op,COALESCE(tool,''),COALESCE(session_id,''),COALESCE(before,0),COALESCE(after,0),COALESCE(detail,''),COALESCE(ok,0),created_at FROM operations ORDER BY created_at DESC,rowid DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Operation{}
	for rows.Next() {
		var o Operation
		var ok int
		var created int64
		if err = rows.Scan(&o.UID, &o.Op, &o.Tool, &o.SessionID, &o.Before, &o.After, &o.Detail, &ok, &created); err != nil {
			return nil, 0, err
		}
		o.OK = ok == 1
		o.CreatedAt = time.Unix(created, 0)
		out = append(out, o)
	}
	return out, total, rows.Err()
}
