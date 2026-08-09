package web

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/mekjr1/midden/internal/index"
)

const (
	jobLeaseHeartbeat  = 5 * time.Second
	jobLeaseStaleAfter = 30 * time.Second
)

type jobLeaseRecord struct {
	JobID   string    `json:"job_id"`
	OwnerID string    `json:"owner_id"`
	Claimed time.Time `json:"claimed"`
}

// jobLeaseManager records which process owns a live background job. Leases are
// branch-local files in MIDDEN_HOME, so existing databases need no migration.
type jobLeaseManager struct {
	root    string
	ownerID string
	db      *index.DB
	stop    chan struct{}
	started sync.Once
	closed  sync.Once
	doneMu  sync.Mutex
	done    chan struct{}
}

func newJobLeaseManager(db *index.DB) (*jobLeaseManager, error) {
	m := &jobLeaseManager{root: filepath.Join(index.Dir(), "job-leases"), ownerID: index.NewUID(), db: db, stop: make(chan struct{})}
	if err := os.MkdirAll(m.jobsDir(), 0o700); err != nil {
		return nil, fmt.Errorf("create job lease directory: %w", err)
	}
	if err := os.MkdirAll(m.instancesDir(), 0o700); err != nil {
		return nil, fmt.Errorf("create job owner directory: %w", err)
	}
	if err := m.touch(); err != nil {
		return nil, fmt.Errorf("heartbeat job owner: %w", err)
	}
	return m, nil
}

func (m *jobLeaseManager) jobsDir() string      { return filepath.Join(m.root, "jobs") }
func (m *jobLeaseManager) instancesDir() string { return filepath.Join(m.root, "instances") }
func (m *jobLeaseManager) instancePath() string {
	return filepath.Join(m.instancesDir(), m.ownerID+".lease")
}
func (m *jobLeaseManager) jobPath(id string) string { return filepath.Join(m.jobsDir(), id+".json") }
func (m *jobLeaseManager) touch() error {
	return os.WriteFile(m.instancePath(), []byte(time.Now().UTC().Format(time.RFC3339Nano)), 0o600)
}

func (m *jobLeaseManager) startHeartbeat() {
	m.started.Do(func() {
		done := make(chan struct{})
		m.doneMu.Lock()
		m.done = done
		m.doneMu.Unlock()
		go func() {
			defer close(done)
			ticker := time.NewTicker(jobLeaseHeartbeat)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					_ = m.touch()
				case <-m.stop:
					return
				}
			}
		}()
	})
}

func (m *jobLeaseManager) Claim(jobID string) error {
	if jobID == "" {
		return fmt.Errorf("job lease requires a job id")
	}
	if err := m.touch(); err != nil {
		return fmt.Errorf("heartbeat job owner: %w", err)
	}
	body, err := json.Marshal(jobLeaseRecord{JobID: jobID, OwnerID: m.ownerID, Claimed: time.Now().UTC()})
	if err != nil {
		return err
	}
	if err := os.WriteFile(m.jobPath(jobID), body, 0o600); err != nil {
		return fmt.Errorf("record job owner: %w", err)
	}
	m.startHeartbeat()
	return nil
}

func (m *jobLeaseManager) Release(jobID string) {
	if jobID != "" {
		_ = os.Remove(m.jobPath(jobID))
	}
}

func (m *jobLeaseManager) Close() {
	m.closed.Do(func() {
		close(m.stop)
		m.doneMu.Lock()
		done := m.done
		m.doneMu.Unlock()
		if done != nil {
			<-done
		}
		_ = os.Remove(m.instancePath())
	})
}

func (m *jobLeaseManager) recoverStaleJobs() error {
	entries, err := os.ReadDir(m.jobsDir())
	if err != nil {
		return fmt.Errorf("read job leases: %w", err)
	}
	stale := make([]string, 0)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		body, err := os.ReadFile(filepath.Join(m.jobsDir(), entry.Name()))
		if err != nil {
			return fmt.Errorf("read job lease %s: %w", entry.Name(), err)
		}
		var record jobLeaseRecord
		if err := json.Unmarshal(body, &record); err != nil || record.JobID == "" || record.OwnerID == "" {
			return fmt.Errorf("invalid job lease %s", entry.Name())
		}
		owner, err := os.Stat(filepath.Join(m.instancesDir(), record.OwnerID+".lease"))
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("read job owner %s: %w", record.OwnerID, err)
		}
		if os.IsNotExist(err) || time.Since(owner.ModTime()) > jobLeaseStaleAfter {
			stale = append(stale, record.JobID)
		}
	}
	if len(stale) == 0 {
		return nil
	}
	const detail = "Midden job owner stopped heartbeating"
	if err := m.db.InterruptBackgroundJobsByID(stale, detail); err != nil {
		return fmt.Errorf("interrupt stale background jobs: %w", err)
	}
	if err := m.db.InterruptRecoveryRunsByJobID(stale, detail); err != nil {
		return fmt.Errorf("interrupt stale recovery runs: %w", err)
	}
	for _, id := range stale {
		m.Release(id)
	}
	return nil
}
