package module

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mekjr1/midden/internal/confirmation"
)

func TestHostProbeReportsCancellationWithoutWorkflowState(t *testing.T) {
	req := Request{Capability: "host.status", Input: json.RawMessage(`{"probe_confirmation":true}`),
		ConfirmOperator: func(context.Context, confirmation.Request) (bool, error) {
			return false, confirmation.Failure{Code: "cancelled", Message: "Synthetic host cancellation"}
		}}
	env := InvokeAgent(req)
	if !env.OK {
		t.Fatal(env.Error)
	}
	var result struct {
		Available bool   `json:"confirmation_available"`
		Outcome   string `json:"confirmation_outcome"`
		Changed   bool   `json:"workflow_changed"`
	}
	json.Unmarshal(env.Result, &result)
	if !result.Available || result.Outcome != "cancelled" || result.Changed {
		t.Fatalf("misdiagnosed host response: %s", env.Result)
	}
}
