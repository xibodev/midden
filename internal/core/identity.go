package core

// Product identity is independent of host adapters and outcome bundles.
const (
	// ModuleID is retained for portable legacy data readers.
	ModuleID = "midden"
)

// Version is set by release builds; ordinary source builds retain a development label.
var Version = "0.3.0-dev"
