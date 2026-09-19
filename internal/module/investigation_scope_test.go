package module

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestInvestigationRejectsAmbiguousScopeBeforeSourceAccess(t *testing.T) {
	for _, raw := range []string{
		`{"tool":"copilot","session_id":"one-session"}`,
		`{"tool":"copilot"}`,
		`{}`,
		`{"ids":[]}`,
		`{"ids":[" "]}`,
		`{"ids":["one-session"],"max_candiates":20}`,
		`{"ids":["one-session"]} {"ids":["another"]}`,
	} {
		t.Run(raw, func(t *testing.T) {
			env := Invoke(Request{Capability: CapSessionsAssay, Input: json.RawMessage(raw), ExplicitSourceRoots: true})
			if env.OK || env.Error.Code != ErrInvalidRequest {
				t.Fatalf("input must fail before probing unavailable source stores: %+v", env)
			}
			if strings.Contains(raw, "session_id") && (!strings.Contains(env.Error.Message, "session_id") || !strings.Contains(env.Error.Message, "ids")) {
				t.Fatalf("scope error needs a repair hint: %s", env.Error.Message)
			}
		})
	}
}

func TestExactAssayDoesNotIncludeNeighbourSessions(t *testing.T) {
	root := writeSyntheticClaudeStore(t)
	env := Invoke(Request{Capability: CapSessionsAssay,
		Input: json.RawMessage(`{"tool":"claude","ids":["11111111-2222-3333-4444-555555555555"]}`),
		Roots: map[string]Root{RootClaude: {Path: root, Mode: "ro"}}, ExplicitSourceRoots: true})
	if !env.OK {
		t.Fatal(env.Error)
	}
	var result AssayResult
	if err := json.Unmarshal(env.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.Assayed != 1 || len(result.Sessions) != 1 || result.Sessions[0].SessionID != "11111111-2222-3333-4444-555555555555" {
		t.Fatalf("exact scope widened: %+v", result)
	}
}
