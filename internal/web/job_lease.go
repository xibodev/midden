package web

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// Owns proves this process still owns a claimed job immediately before work
// begins. Reading the durable lease avoids an in-memory-only bypass.
func (m *jobLeaseManager) Owns(jobID string) error {
	if m == nil {
		return fmt.Errorf("job ownership is unavailable")
	}
	if jobID == "" {
		return fmt.Errorf("job lease requires a job id")
	}
	if err := m.touch(); err != nil {
		return fmt.Errorf("heartbeat job owner: %w", err)
	}
	body, err := os.ReadFile(m.jobPath(jobID))
	if err != nil {
		return fmt.Errorf("read job owner: %w", err)
	}
	var record jobLeaseRecord
	if err := json.Unmarshal(body, &record); err != nil {
		return fmt.Errorf("decode job owner: %w", err)
	}
	if record.JobID != jobID || record.OwnerID != m.ownerID {
		return fmt.Errorf("job %s is not owned by this instance", jobID)
	}
	owner, err := os.Stat(m.instancePath())
	if err != nil {
		return fmt.Errorf("read job owner heartbeat: %w", err)
	}
	if time.Since(owner.ModTime()) > jobLeaseStaleAfter {
		return fmt.Errorf("job owner heartbeat is stale")
	}
	return nil
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
	healthy := make([]string, 0, len(entries))
	staleLeaseIDs := make([]string, 0)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(m.jobsDir(), entry.Name())
		body, readErr := os.ReadFile(path)
		var record jobLeaseRecord
		if readErr == nil {
			readErr = json.Unmarshal(body, &record)
		}
		if readErr != nil || record.JobID == "" || record.OwnerID == "" {
			// A malformed lease proves neither current ownership nor a usable
			// recovery path. Treat it as absent rather than refusing startup and
			// stranding every durable row indefinitely.
			staleLeaseIDs = append(staleLeaseIDs, strings.TrimSuffix(entry.Name(), ".json"))
			continue
		}
		owner, statErr := os.Stat(filepath.Join(m.instancesDir(), record.OwnerID+".lease"))
		if statErr == nil && time.Since(owner.ModTime()) <= jobLeaseStaleAfter {
			healthy = append(healthy, record.JobID)
			continue
		}
		staleLeaseIDs = append(staleLeaseIDs, record.JobID)
	}

	const detail = "Midden job owner stopped heartbeating"
	if _, err := m.db.ReconcileOrphanedJobsAndRuns(healthy, detail); err != nil {
		return fmt.Errorf("reconcile orphaned durable work: %w", err)
	}
	for _, id := range staleLeaseIDs {
		m.Release(id)
	}
	return nil
}
