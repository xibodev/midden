package index

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

func (d *DB) migrateHostReviews() error {
	_, err := d.sql.Exec(`CREATE TABLE IF NOT EXISTS host_reviews (
 action TEXT NOT NULL, subject_id TEXT NOT NULL, digest TEXT NOT NULL,
 confirmed_at INTEGER NOT NULL, PRIMARY KEY(action,subject_id,digest)
);`)
	return err
}

// RecordHostReview is an audit receipt for an existing workflow transition,
// not an alternative state machine. The module calls it only after host consent.
func (d *DB) RecordHostReview(action, id, digest string) error {
	if action == "" || id == "" || digest == "" {
		return fmt.Errorf("confirmation action, subject and digest are required")
	}
	_, err := d.sql.Exec("INSERT INTO host_reviews(action,subject_id,digest,confirmed_at) VALUES(?,?,?,?) ON CONFLICT DO NOTHING", action, id, digest, time.Now().UnixNano())
	return err
}

func (d *DB) HasHostReview(action, id, digest string) (bool, error) {
	var found int
	err := d.sql.QueryRow("SELECT 1 FROM host_reviews WHERE action=? AND subject_id=? AND digest=?", action, id, digest).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return found == 1, err
}
