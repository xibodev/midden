package index

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// NewUID returns a short random identifier for a stored row.
func NewUID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("uid%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// Nugget is a unit of reclaimed value.
//
// Provenance is mandatory: every nugget names the session it came from and the
// model that produced it, so a weak-model extraction can be identified and
// re-run later rather than silently trusted.
type Nugget struct {
	UID        string    `json:"uid"`
	Tool       string    `json:"tool"`
	SessionID  string    `json:"session_id"`
	Kind       string    `json:"kind"`
	Title      string    `json:"title"`
	Body       string    `json:"body"`
	Tags       []string  `json:"tags,omitempty"`
	Workspace  string    `json:"workspace,omitempty"`
	Repo       string    `json:"repo,omitempty"`
	Confidence float64   `json:"confidence"`
	Model      string    `json:"model"`
	Redacted   bool      `json:"redacted"`
	TurnRef    string    `json:"turn_ref,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// NuggetKinds are the extraction targets. Each is a thing that is expensive to
// rediscover and cheap to store.
var NuggetKinds = []string{
	"decision",  // a choice made, with the alternatives rejected
	"error_fix", // a failure and what actually resolved it
	"command",   // an invocation that worked, with its context
	"gotcha",    // surprising behaviour worth remembering
	"dead_end",  // an approach tried and abandoned, and why
	"artifact",  // a produced thing and what it demonstrates
	"brief",     // distilled session state for handoff
}

// ValidKind reports whether a nugget kind is recognised.
func ValidKind(k string) bool {
	for _, v := range NuggetKinds {
		if v == k {
			return true
		}
	}
	return false
}

// PutNuggets stores nuggets in one transaction.
func (d *DB) PutNuggets(ns []Nugget) error {
	tx, err := d.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO nuggets (uid,tool,session_id,kind,title,body,tags,workspace,repo,
		  confidence,model,redacted,turn_ref,created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, n := range ns {
		if n.UID == "" {
			n.UID = NewUID()
		}
		if n.CreatedAt.IsZero() {
			n.CreatedAt = time.Now()
		}
		red := 0
		if n.Redacted {
			red = 1
		}
		if _, err := stmt.Exec(n.UID, n.Tool, n.SessionID, n.Kind, n.Title, n.Body,
			strings.Join(n.Tags, ","), n.Workspace, n.Repo, n.Confidence, n.Model,
			red, n.TurnRef, n.CreatedAt.Unix()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// NuggetQuery narrows a nugget lookup.
type NuggetQuery struct {
	Kind      string
	SessionID string
	Workspace string
	Search    string
	Limit     int
}

// Nuggets returns stored nuggets matching a query, newest first.
func (d *DB) Nuggets(q NuggetQuery) ([]Nugget, error) {
	var (
		where []string
		args  []any
	)
	if q.Kind != "" {
		where = append(where, "kind = ?")
		args = append(args, q.Kind)
	}
	if q.SessionID != "" {
		where = append(where, "session_id LIKE ?")
		args = append(args, q.SessionID+"%")
	}
	if q.Workspace != "" {
		where = append(where, "workspace LIKE ?")
		args = append(args, "%"+q.Workspace+"%")
	}
	if q.Search != "" {
		where = append(where, "(title LIKE ? OR body LIKE ?)")
		args = append(args, "%"+q.Search+"%", "%"+q.Search+"%")
	}

	sqlStr := `SELECT uid,tool,session_id,kind,COALESCE(title,''),body,COALESCE(tags,''),
	           COALESCE(workspace,''),COALESCE(repo,''),COALESCE(confidence,0),
	           COALESCE(model,''),COALESCE(redacted,0),COALESCE(turn_ref,''),created_at
	           FROM nuggets`
	if len(where) > 0 {
		sqlStr += " WHERE " + strings.Join(where, " AND ")
	}
	sqlStr += " ORDER BY created_at DESC"
	if q.Limit > 0 {
		sqlStr += fmt.Sprintf(" LIMIT %d", q.Limit)
	}

	rows, err := d.sql.Query(sqlStr, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Nugget
	for rows.Next() {
		var n Nugget
		var tags string
		var red int
		var created int64
		if err := rows.Scan(&n.UID, &n.Tool, &n.SessionID, &n.Kind, &n.Title, &n.Body,
			&tags, &n.Workspace, &n.Repo, &n.Confidence, &n.Model, &red,
			&n.TurnRef, &created); err != nil {
			return nil, err
		}
		if tags != "" {
			n.Tags = strings.Split(tags, ",")
		}
		n.Redacted = red == 1
		n.CreatedAt = time.Unix(created, 0)
		out = append(out, n)
	}
	return out, rows.Err()
}

// NuggetCounts returns how many nuggets exist per kind.
func (d *DB) NuggetCounts() (map[string]int64, error) {
	rows, err := d.sql.Query(`SELECT kind, COUNT(*) FROM nuggets GROUP BY kind`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]int64{}
	for rows.Next() {
		var k string
		var n int64
		if rows.Scan(&k, &n) == nil {
			out[k] = n
		}
	}
	return out, rows.Err()
}

// Artifact is a generated document derived from nuggets.
type Artifact struct {
	UID       string    `json:"uid"`
	Kind      string    `json:"kind"`
	Title     string    `json:"title"`
	Path      string    `json:"path"`
	Scope     string    `json:"scope"`
	NuggetIDs []string  `json:"nugget_ids"`
	Model     string    `json:"model"`
	CreatedAt time.Time `json:"created_at"`
}

// PutArtifact records a generated artifact and the nuggets behind it.
func (d *DB) PutArtifact(a Artifact) error {
	if a.UID == "" {
		a.UID = NewUID()
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now()
	}
	ids, _ := json.Marshal(a.NuggetIDs)
	_, err := d.sql.Exec(`
		INSERT INTO artifacts (uid,kind,title,path,scope,nugget_ids,model,created_at)
		VALUES (?,?,?,?,?,?,?,?)`,
		a.UID, a.Kind, a.Title, a.Path, a.Scope, string(ids), a.Model, a.CreatedAt.Unix())
	return err
}

// Artifacts lists generated artifacts, newest first.
func (d *DB) Artifacts(limit int) ([]Artifact, error) {
	q := `SELECT uid,kind,COALESCE(title,''),COALESCE(path,''),COALESCE(scope,''),
	      COALESCE(nugget_ids,'[]'),COALESCE(model,''),created_at
	      FROM artifacts ORDER BY created_at DESC`
	if limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", limit)
	}
	rows, err := d.sql.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Artifact
	for rows.Next() {
		var a Artifact
		var ids string
		var created int64
		if err := rows.Scan(&a.UID, &a.Kind, &a.Title, &a.Path, &a.Scope, &ids,
			&a.Model, &created); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(ids), &a.NuggetIDs)
		a.CreatedAt = time.Unix(created, 0)
		out = append(out, a)
	}
	return out, rows.Err()
}

// Operation is one audited mutating action.
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

// Operations returns the audit log, newest first.
func (d *DB) Operations(limit int) ([]Operation, error) {
	q := `SELECT uid,op,COALESCE(tool,''),COALESCE(session_id,''),
	      COALESCE(before,0),COALESCE(after,0),COALESCE(detail,''),COALESCE(ok,0),created_at
	      FROM operations ORDER BY created_at DESC`
	if limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", limit)
	}
	rows, err := d.sql.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Operation
	for rows.Next() {
		var o Operation
		var ok int
		var created int64
		if err := rows.Scan(&o.UID, &o.Op, &o.Tool, &o.SessionID, &o.Before,
			&o.After, &o.Detail, &ok, &created); err != nil {
			return nil, err
		}
		o.OK = ok == 1
		o.CreatedAt = time.Unix(created, 0)
		out = append(out, o)
	}
	return out, rows.Err()
}
