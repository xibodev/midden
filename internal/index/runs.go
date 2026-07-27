package index

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mekjr1/midden/internal/cost"
)

const runsSchema = `
-- Every operation that spent anything, with prediction and actual side by
-- side. This is what calibration learns from and what the cost ledger shows.
CREATE TABLE IF NOT EXISTS runs (
  uid           TEXT PRIMARY KEY,
  op            TEXT NOT NULL,
  scope         TEXT,
  backend       TEXT,
  cli_sessions  TEXT,
  est_tokens    INTEGER,
  items         INTEGER,
  model         TEXT,
  turns         INTEGER,
  input_tokens  INTEGER,
  output_tokens INTEGER,
  cache_read    INTEGER,
  cache_write   INTEGER,
  reasoning     INTEGER,
  aiu           REAL,
  usd           REAL,
  duration_ms   INTEGER,
  started_at    INTEGER,
  ended_at      INTEGER,
  ok            INTEGER,
  note          TEXT,
  -- Set once real usage has been read back out of the tool's own store.
  reconciled    INTEGER DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_runs_op    ON runs(op);
CREATE INDEX IF NOT EXISTS idx_runs_start ON runs(started_at DESC);
`

func (d *DB) migrateRuns() error {
	_, err := d.sql.Exec(runsSchema)
	return err
}

// PutRun records an operation. Usage may be empty at this point; call
// ReconcileRun once the backend has flushed its accounting to disk.
func (d *DB) PutRun(r cost.Run) error {
	if r.UID == "" {
		r.UID = NewUID()
	}
	sessions, _ := json.Marshal(r.CLISessions)
	okv := 0
	if r.OK {
		okv = 1
	}
	reconciled := 0
	if !r.Usage.Empty() {
		reconciled = 1
	}

	_, err := d.sql.Exec(`
		INSERT INTO runs (uid,op,scope,backend,cli_sessions,est_tokens,items,model,turns,
		  input_tokens,output_tokens,cache_read,cache_write,reasoning,aiu,usd,duration_ms,
		  started_at,ended_at,ok,note,reconciled)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(uid) DO UPDATE SET
		  items=excluded.items, model=excluded.model, turns=excluded.turns,
		  input_tokens=excluded.input_tokens, output_tokens=excluded.output_tokens,
		  cache_read=excluded.cache_read, cache_write=excluded.cache_write,
		  reasoning=excluded.reasoning, aiu=excluded.aiu, usd=excluded.usd,
		  duration_ms=excluded.duration_ms, ended_at=excluded.ended_at,
		  ok=excluded.ok, note=excluded.note, reconciled=excluded.reconciled`,
		r.UID, r.Op, r.Scope, r.Backend, string(sessions), r.EstTokens, r.Items,
		r.Usage.Model, r.Usage.Turns, r.Usage.InputTokens, r.Usage.OutputTokens,
		r.Usage.CacheRead, r.Usage.CacheWrite, r.Usage.Reasoning, r.Usage.AIU,
		r.Usage.USD, r.Usage.DurationMS, unix(r.StartedAt), unix(r.EndedAt),
		okv, r.Note, reconciled)
	return err
}

// Runs returns the ledger, newest first.
func (d *DB) Runs(limit int, op string) ([]cost.Run, error) {
	q := `SELECT uid,op,COALESCE(scope,''),COALESCE(backend,''),COALESCE(cli_sessions,'[]'),
	      COALESCE(est_tokens,0),COALESCE(items,0),COALESCE(model,''),COALESCE(turns,0),
	      COALESCE(input_tokens,0),COALESCE(output_tokens,0),COALESCE(cache_read,0),
	      COALESCE(cache_write,0),COALESCE(reasoning,0),COALESCE(aiu,0),COALESCE(usd,0),
	      COALESCE(duration_ms,0),COALESCE(started_at,0),COALESCE(ended_at,0),
	      COALESCE(ok,0),COALESCE(note,'')
	      FROM runs`
	var args []any
	if op != "" {
		q += " WHERE op = ?"
		args = append(args, op)
	}
	q += " ORDER BY started_at DESC"
	if limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", limit)
	}

	rows, err := d.sql.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []cost.Run
	for rows.Next() {
		var (
			r              cost.Run
			sessions       string
			started, ended int64
			okv            int
		)
		if err := rows.Scan(&r.UID, &r.Op, &r.Scope, &r.Backend, &sessions,
			&r.EstTokens, &r.Items, &r.Usage.Model, &r.Usage.Turns,
			&r.Usage.InputTokens, &r.Usage.OutputTokens, &r.Usage.CacheRead,
			&r.Usage.CacheWrite, &r.Usage.Reasoning, &r.Usage.AIU, &r.Usage.USD,
			&r.Usage.DurationMS, &started, &ended, &okv, &r.Note); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(sessions), &r.CLISessions)
		if started > 0 {
			r.StartedAt = time.Unix(started, 0)
		}
		if ended > 0 {
			r.EndedAt = time.Unix(ended, 0)
		}
		r.OK = okv == 1
		out = append(out, r)
	}
	return out, rows.Err()
}

// UnreconciledRuns returns runs whose real usage has not been read back yet.
func (d *DB) UnreconciledRuns() ([]cost.Run, error) {
	rows, err := d.sql.Query(`
		SELECT uid, op, COALESCE(backend,''), COALESCE(cli_sessions,'[]')
		FROM runs WHERE reconciled = 0 ORDER BY started_at DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []cost.Run
	for rows.Next() {
		var r cost.Run
		var sessions string
		if rows.Scan(&r.UID, &r.Op, &r.Backend, &sessions) != nil {
			continue
		}
		json.Unmarshal([]byte(sessions), &r.CLISessions)
		out = append(out, r)
	}
	return out, rows.Err()
}

// ReconcileRun writes real usage onto a recorded run.
func (d *DB) ReconcileRun(uid string, u cost.Usage) error {
	_, err := d.sql.Exec(`
		UPDATE runs SET model=?, turns=?, input_tokens=?, output_tokens=?,
		  cache_read=?, cache_write=?, reasoning=?, aiu=?, usd=?, duration_ms=?,
		  reconciled=1
		WHERE uid = ?`,
		u.Model, u.Turns, u.InputTokens, u.OutputTokens, u.CacheRead,
		u.CacheWrite, u.Reasoning, u.AIU, u.USD, u.DurationMS, uid)
	return err
}

// CalibrationFor returns the observed estimate-versus-actual statistics for an
// operation, which is what turns a guess into a grounded prediction.
func (d *DB) CalibrationFor(op string) (*cost.Stats, error) {
	rows, err := d.sql.Query(`
		SELECT est_tokens,
		       COALESCE(input_tokens,0)+COALESCE(output_tokens,0)+
		       COALESCE(cache_read,0)+COALESCE(cache_write,0) AS billable,
		       COALESCE(aiu,0), COALESCE(usd,0)
		FROM runs
		WHERE op = ? AND reconciled = 1 AND ok = 1 AND est_tokens > 0`, op)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	s := &cost.Stats{Op: op}
	var sumFactor, sumUnit float64
	for rows.Next() {
		var est, billable int64
		var aiu, usd float64
		if rows.Scan(&est, &billable, &aiu, &usd) != nil {
			continue
		}
		if est <= 0 || billable <= 0 {
			continue
		}
		f := float64(billable) / float64(est)

		s.Samples++
		sumFactor += f
		if s.MinFactor == 0 || f < s.MinFactor {
			s.MinFactor = f
		}
		if f > s.MaxFactor {
			s.MaxFactor = f
		}
		switch {
		case aiu > 0:
			sumUnit += aiu
			s.UnitName = "AIU"
		case usd > 0:
			sumUnit += usd
			s.UnitName = "USD"
		}
	}
	if s.Samples > 0 {
		s.MeanFactor = sumFactor / float64(s.Samples)
		s.MeanUnit = sumUnit / float64(s.Samples)
	}
	return s, rows.Err()
}

// CostTotals summarises spending across the ledger.
type CostTotals struct {
	Runs     int64              `json:"runs"`
	Items    int64              `json:"items"`
	Tokens   int64              `json:"tokens"`
	AIU      float64            `json:"aiu"`
	USD      float64            `json:"usd"`
	Duration int64              `json:"duration_ms"`
	ByOp     map[string]OpTotal `json:"by_op"`
}

// OpTotal is spending for one operation kind.
type OpTotal struct {
	Runs   int64   `json:"runs"`
	Items  int64   `json:"items"`
	Tokens int64   `json:"tokens"`
	AIU    float64 `json:"aiu"`
	USD    float64 `json:"usd"`
}

// PerItem is the unit cost, in credits when available.
func (o OpTotal) PerItem() float64 {
	if o.Items == 0 {
		return 0
	}
	if o.AIU > 0 {
		return o.AIU / float64(o.Items)
	}
	return float64(o.Tokens) / float64(o.Items)
}

// Costs aggregates the ledger.
func (d *DB) Costs() (CostTotals, error) {
	t := CostTotals{ByOp: map[string]OpTotal{}}

	rows, err := d.sql.Query(`
		SELECT op, COUNT(*), COALESCE(SUM(items),0),
		       COALESCE(SUM(input_tokens),0)+COALESCE(SUM(output_tokens),0)+
		       COALESCE(SUM(cache_read),0)+COALESCE(SUM(cache_write),0),
		       COALESCE(SUM(aiu),0), COALESCE(SUM(usd),0), COALESCE(SUM(duration_ms),0)
		FROM runs GROUP BY op`)
	if err != nil {
		return t, err
	}
	defer rows.Close()

	for rows.Next() {
		var op string
		var o OpTotal
		var dur int64
		if rows.Scan(&op, &o.Runs, &o.Items, &o.Tokens, &o.AIU, &o.USD, &dur) != nil {
			continue
		}
		t.ByOp[op] = o
		t.Runs += o.Runs
		t.Items += o.Items
		t.Tokens += o.Tokens
		t.AIU += o.AIU
		t.USD += o.USD
		t.Duration += dur
	}
	return t, rows.Err()
}

var _ = strings.TrimSpace
