package main

import (
	"bufio"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mekjr1/midden/internal/confirmation"
)

func TestHostConfirmationReportsDistinctOutcomes(t *testing.T) {
	for _, tc := range []struct{ reply, want string }{
		{`{"action":"accept","content":{"approved":true}}`, "accepted"},
		{`{"action":"accept","content":{"approved":false}}`, "not_approved"},
		{`{"action":"decline"}`, "declined"},
		{`{"action":"cancel"}`, "cancelled"},
		{`{"action":"accept","content":{}}`, "invalid_response"},
		{`{"action":"unexpected"}`, "invalid_response"},
	} {
		var output strings.Builder
		input := bufio.NewScanner(strings.NewReader(`{"jsonrpc":"2.0","id":"midden-confirmation-1","result":` + tc.reply + "}\n"))
		connection := mcpConnection{in: input, out: json.NewEncoder(&output)}
		accepted, err := connection.confirm(context.Background(), confirmation.Request{Action: "probe", SubjectID: "synthetic", Digest: "probe", Message: "Synthetic confirmation test; no workflow changes."})
		if got := confirmation.Outcome(accepted, err); got != tc.want {
			t.Errorf("%s: got %s, want %s", tc.reply, got, tc.want)
		}
	}
}
