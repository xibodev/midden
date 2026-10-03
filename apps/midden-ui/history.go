package main

import (
	"fmt"
	"path/filepath"

	"github.com/xibodev/compa/pkg/memory"
	"github.com/xibodev/compa/pkg/session"
)

const kernelHistoryDir = "compa-history"

// openKernelHistory opens the kernel's JSONL conversation store in host state.
func openKernelHistory(state string) (session.SessionStore, error) {
	store, err := memory.NewJSONLStore(filepath.Join(state, kernelHistoryDir))
	if err != nil {
		return nil, fmt.Errorf("open kernel history: %w", err)
	}
	return session.NewJSONLBackend(store), nil
}
