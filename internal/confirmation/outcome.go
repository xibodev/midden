package confirmation

import (
	"context"
	"errors"
)

type Failure struct{ Code, Message string }

func (e Failure) Error() string { return e.Message }

func Outcome(accepted bool, err error) string {
	if accepted && err == nil {
		return "accepted"
	}
	var failure Failure
	if errors.As(err, &failure) {
		return failure.Code
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	if err != nil {
		return "unavailable"
	}
	return "not_approved"
}
