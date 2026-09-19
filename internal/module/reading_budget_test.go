package module

import (
	"encoding/json"
	"testing"
)

func TestReadingBudgetIncreaseRequiresHostOperator(t *testing.T) {
	call, _ := packetCaller(t)
	packet := preparedPacket(t, call)
	env := call("evidence.extend_budget", map[string]any{"packet_id": packet["packet_id"], "limit_bytes": 262144})
	if env.OK || env.Error.Code != ErrOperatorConfirmation {
		t.Fatalf("budget change must require operator confirmation: %+v", env.Error)
	}
	var requestShape map[string]any
	if err := json.Unmarshal(Describe().RequestSchemas["xibodev.midden.evidence.extend_budget.request/v1"], &requestShape); err != nil {
		t.Fatal(err)
	}
}
