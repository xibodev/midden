package index

import (
	"encoding/json"
	"fmt"
	"time"
)

func (d *DB) migrateAgentViews() error {
	_, err := d.sql.Exec(`CREATE TABLE IF NOT EXISTS agent_views (
 id TEXT PRIMARY KEY, capability TEXT NOT NULL, document TEXT NOT NULL, created_at INTEGER NOT NULL
);`)
	return err
}

func (d *DB) PutAgentView(id, capability string, raw json.RawMessage) error {
	if len(raw) > 4<<20 || !json.Valid(raw) {
		return fmt.Errorf("result exceeds the 4 MiB inspectable-view bound")
	}
	tx, err := d.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("INSERT INTO agent_views(id,capability,document,created_at) VALUES(?,?,?,?) ON CONFLICT(id) DO NOTHING", id, capability, string(raw), time.Now().UnixNano()); err != nil {
		return err
	}
	if _, err = tx.Exec("DELETE FROM agent_views WHERE id NOT IN (SELECT id FROM agent_views ORDER BY created_at DESC,id LIMIT 128)"); err != nil {
		return err
	}
	return tx.Commit()
}

func (d *DB) AgentView(id string) (json.RawMessage, error) {
	var raw string
	err := d.sql.QueryRow("SELECT document FROM agent_views WHERE id=?", id).Scan(&raw)
	return json.RawMessage(raw), err
}
