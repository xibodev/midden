// Package confirmation describes the trusted host interaction boundary.
// It does not implement an approval workflow or accept wire-level assertions.
package confirmation

import "context"

type Request struct {
	Action    string `json:"action"`
	SubjectID string `json:"subject_id"`
	Digest    string `json:"digest"`
	Message   string `json:"message"`
}

// Handler must be supplied by a host UI, never deserialized from model input.
// A missing handler, decline, cancellation or absent operator is not approval.
type Handler func(context.Context, Request) (bool, error)
