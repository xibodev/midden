package index

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

const DefaultReadingBudget = 128 * 1024

type ReadingBudget struct {
	LimitBytes     int `json:"limit_bytes"`
	UsedBytes      int `json:"used_bytes"`
	RemainingBytes int `json:"remaining_bytes"`
}

type ReadingSpan struct{ Start, End int }

func (d *DB) migrateReading() error {
	_, err := d.sql.Exec(`
CREATE TABLE IF NOT EXISTS reading_investigations (
  id TEXT PRIMARY KEY,
  limit_bytes INTEGER NOT NULL,
  used_bytes INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS reading_packets (
  id TEXT PRIMARY KEY,
  digest TEXT NOT NULL UNIQUE,
  investigation_id TEXT NOT NULL REFERENCES reading_investigations(id),
  document TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS reading_spans (
  investigation_id TEXT NOT NULL REFERENCES reading_investigations(id),
  record_id TEXT NOT NULL,
  bytes INTEGER NOT NULL,
  PRIMARY KEY(investigation_id,record_id)
);
CREATE TABLE IF NOT EXISTS reading_windows (
  investigation_id TEXT NOT NULL REFERENCES reading_investigations(id),
  record_id TEXT NOT NULL,
  first_byte INTEGER NOT NULL,
  last_byte INTEGER NOT NULL,
  PRIMARY KEY(investigation_id,record_id,first_byte)
);`)
	return err
}

// PutReadingPacket charges only newly exposed excerpt bytes. Retrying or
// rereading an unchanged prefix does not consume the budget again.
func (d *DB) PutReadingPacket(id, digest, investigation string, doc json.RawMessage, spans map[string]int) (ReadingBudget, error) {
	ranges := map[string][]ReadingSpan{}
	for record, size := range spans {
		ranges[record] = []ReadingSpan{{Start: 0, End: size}}
	}
	return d.PutReadingPacketRanges(id, digest, investigation, doc, ranges)
}

func (d *DB) PutReadingPacketRanges(id, digest, investigation string, doc json.RawMessage, spans map[string][]ReadingSpan) (ReadingBudget, error) {
	var budget ReadingBudget
	if id == "" || digest == "" || investigation == "" || !json.Valid(doc) {
		return budget, fmt.Errorf("packet identity and valid document are required")
	}
	tx, err := d.sql.Begin()
	if err != nil {
		return budget, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("INSERT INTO reading_investigations(id,limit_bytes) VALUES(?,?) ON CONFLICT(id) DO NOTHING", investigation, DefaultReadingBudget); err != nil {
		return budget, err
	}
	if err = tx.QueryRow("SELECT limit_bytes,used_bytes FROM reading_investigations WHERE id=?", investigation).Scan(&budget.LimitBytes, &budget.UsedBytes); err != nil {
		return budget, err
	}
	delta := 0
	merged := map[string][]ReadingSpan{}
	for record, windows := range spans {
		if record == "" {
			return budget, fmt.Errorf("invalid reading span")
		}
		for _, window := range windows {
			if window.Start < 0 || window.End < window.Start {
				return budget, fmt.Errorf("invalid source byte offsets")
			}
		}
		previous := []ReadingSpan{}
		rows, e := tx.Query("SELECT first_byte,last_byte FROM reading_windows WHERE investigation_id=? AND record_id=? ORDER BY first_byte", investigation, record)
		if e != nil {
			return budget, e
		}
		for rows.Next() {
			var span ReadingSpan
			if e = rows.Scan(&span.Start, &span.End); e != nil {
				rows.Close()
				return budget, e
			}
			previous = append(previous, span)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return budget, e
		}
		if len(previous) == 0 {
			var oldPrefix int
			e = tx.QueryRow("SELECT bytes FROM reading_spans WHERE investigation_id=? AND record_id=?", investigation, record).Scan(&oldPrefix)
			if e != nil && !errors.Is(e, sql.ErrNoRows) {
				return budget, e
			}
			if oldPrefix > 0 {
				previous = append(previous, ReadingSpan{0, oldPrefix})
			}
		}
		combined := mergeReadingSpans(append(append([]ReadingSpan(nil), previous...), windows...))
		delta += spanBytes(combined) - spanBytes(previous)
		merged[record] = combined
	}
	if budget.UsedBytes+delta > budget.LimitBytes {
		return budget, fmt.Errorf("reading budget exceeded: used=%d additional=%d limit=%d; obtain operator confirmation before increasing it", budget.UsedBytes, delta, budget.LimitBytes)
	}
	if _, err = tx.Exec("UPDATE reading_investigations SET used_bytes=used_bytes+? WHERE id=?", delta, investigation); err != nil {
		return budget, err
	}
	for record, windows := range merged {
		if _, err = tx.Exec(`INSERT INTO reading_spans(investigation_id,record_id,bytes) VALUES(?,?,?)
ON CONFLICT(investigation_id,record_id) DO UPDATE SET bytes=excluded.bytes`, investigation, record, spanBytes(windows)); err != nil {
			return budget, err
		}
		if _, err = tx.Exec("DELETE FROM reading_windows WHERE investigation_id=? AND record_id=?", investigation, record); err != nil {
			return budget, err
		}
		for _, window := range windows {
			if _, err = tx.Exec("INSERT INTO reading_windows(investigation_id,record_id,first_byte,last_byte) VALUES(?,?,?,?)", investigation, record, window.Start, window.End); err != nil {
				return budget, err
			}
		}
	}

	if _, err = tx.Exec("INSERT INTO reading_packets(id,digest,investigation_id,document) VALUES(?,?,?,?) ON CONFLICT(id) DO NOTHING",
		id, digest, investigation, string(doc)); err != nil {
		return budget, err
	}
	if err = tx.Commit(); err != nil {
		return budget, err
	}
	budget.UsedBytes += delta
	budget.RemainingBytes = budget.LimitBytes - budget.UsedBytes
	return budget, nil
}

func mergeReadingSpans(spans []ReadingSpan) []ReadingSpan {
	sort.Slice(spans, func(i, j int) bool { return spans[i].Start < spans[j].Start })
	out := []ReadingSpan{}
	for _, span := range spans {
		if span.End == span.Start {
			continue
		}
		if len(out) == 0 || span.Start > out[len(out)-1].End {
			out = append(out, span)
		} else if span.End > out[len(out)-1].End {
			out[len(out)-1].End = span.End
		}
	}
	return out
}

func spanBytes(spans []ReadingSpan) int {
	total := 0
	for _, span := range spans {
		total += span.End - span.Start
	}
	return total
}

func (d *DB) ReadingPacket(key string) (json.RawMessage, error) {
	var raw string
	err := d.sql.QueryRow("SELECT document FROM reading_packets WHERE id=? OR digest=?", key, key).Scan(&raw)
	return json.RawMessage(raw), err
}

func (d *DB) ReadingBudget(id string) (ReadingBudget, error) {
	var b ReadingBudget
	err := d.sql.QueryRow("SELECT limit_bytes,used_bytes FROM reading_investigations WHERE id=?", id).Scan(&b.LimitBytes, &b.UsedBytes)
	b.RemainingBytes = b.LimitBytes - b.UsedBytes
	return b, err
}

func (d *DB) ExtendReadingBudget(id string, expected, newLimit int) error {
	if newLimit <= expected || newLimit > 1024*1024 {
		return fmt.Errorf("new reading limit must increase and remain at most 1 MiB")
	}
	r, err := d.sql.Exec("UPDATE reading_investigations SET limit_bytes=? WHERE id=? AND limit_bytes=?", newLimit, id, expected)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("reading budget changed; inspect it before confirming an increase")
	}
	return nil
}
