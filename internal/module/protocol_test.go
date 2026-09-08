package module

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// TestProtocolIDIsFrozenValue guards the operator-ruled identifier against
// silent drift. Changing it is a deliberate cross-lane decision, never a
// refactor.
func TestProtocolIDIsFrozenValue(t *testing.T) {
	if ProtocolID != "xibodev.module/v1" {
		t.Fatalf("protocol ID drifted: got %q, want xibodev.module/v1", ProtocolID)
	}
	if ModuleID != "midden" {
		t.Fatalf("module ID drifted: got %q, want midden", ModuleID)
	}
}

// findNulls walks a decoded JSON document and reports the path of every null.
// Reporting the PATH rather than "there is a null somewhere" is what makes a
// failure actionable.
func findNulls(v any, path string, out *[]string) {
	switch t := v.(type) {
	case nil:
		*out = append(*out, path)
	case map[string]any:
		for k, val := range t {
			findNulls(val, path+"."+k, out)
		}
	case []any:
		for i, val := range t {
			findNulls(val, fmt.Sprintf("%s[%d]", path, i), out)
		}
	}
}

// costPaths are the only fields where null is correct: a nil cost means
// genuinely unknown and must never be normalized into zero.
func isCostPath(p string) bool {
	return strings.HasSuffix(p, ".estimated_cost") || strings.HasSuffix(p, ".actual_cost")
}

func decodeDoc(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var doc map[string]any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("envelope is not valid JSON: %v", err)
	}
	return doc
}

// TestDescribeEnvelopeHasNoNulls is the regression guard for the trap that
// Envelope.Normalize() cannot reach inside json.RawMessage: a descriptor
// marshalled before the envelope is normalized would ship nulls while every
// type check passed.
func TestDescribeEnvelopeHasNoNulls(t *testing.T) {
	d := Describe()
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal descriptor: %v", err)
	}
	env, err := NewResultEnvelope("describe", "", json.RawMessage(raw), LocalFree())
	if err != nil {
		t.Fatalf("build envelope: %v", err)
	}
	out, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}

	var nulls []string
	findNulls(decodeDoc(t, out), "$", &nulls)
	for _, p := range nulls {
		if !isCostPath(p) {
			t.Errorf("null at %s — protocol requires [] or {}", p)
		}
	}
}

// TestZeroDescriptorNormalizes proves the guard works on the worst case: a
// descriptor with every collection nil.
func TestZeroDescriptorNormalizes(t *testing.T) {
	var d Descriptor
	d.Normalize()
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var nulls []string
	findNulls(decodeDoc(t, raw), "$", &nulls)
	if len(nulls) > 0 {
		t.Errorf("zero descriptor emitted nulls at %v", nulls)
	}
}

// TestNormalizeNeverInventsACost is the counterpart guard: normalization must
// not turn unknown into free. An unpriced call rendering as $0 would slip past
// cost approval, which is the exact failure the pointer types prevent.
func TestNormalizeNeverInventsACost(t *testing.T) {
	env := NewErrorEnvelope(OpInvoke, "req-1", Error{
		Code:    ErrInternal,
		Message: "boom",
	}, UnknownCost())

	if env.Execution.EstimatedCost != nil {
		t.Error("normalization invented an estimated cost")
	}
	if env.Execution.ActualCost != nil {
		t.Error("normalization invented an actual cost")
	}

	raw, _ := json.Marshal(env)
	doc := decodeDoc(t, raw)
	exec := doc["execution"].(map[string]any)
	if got, ok := exec["estimated_cost"]; !ok || got != nil {
		t.Errorf("unknown estimated_cost must marshal as null, got %v", got)
	}
	if got, ok := exec["actual_cost"]; !ok || got != nil {
		t.Errorf("unknown actual_cost must marshal as null, got %v", got)
	}
}

// TestLocalFreeIsExplicitZero distinguishes free from unknown. Deterministic
// local work is genuinely free and must say so, rather than leaving the host
// to demand approval for a scan that costs nothing.
func TestLocalFreeIsExplicitZero(t *testing.T) {
	exec := LocalFree()
	if exec.EstimatedCost == nil || *exec.EstimatedCost != 0 {
		t.Error("local free work must report an explicit zero estimated cost")
	}
	if exec.ActualCost == nil || *exec.ActualCost != 0 {
		t.Error("local free work must report an explicit zero actual cost")
	}
	if !exec.Local {
		t.Error("local free work must declare local:true")
	}
}

// TestDescriptorDeclaresHonestEffects checks the promise made to the host:
// the deterministic capabilities touch no network, no provider, and no
// subprocess. Declared effects must describe what the code path does.
func TestDescriptorDeclaresHonestEffects(t *testing.T) {
	d := Describe()
	if len(d.Capabilities) == 0 {
		t.Fatal("descriptor declares no capabilities")
	}
	for _, c := range d.Capabilities {
		if c.Effects.Network {
			t.Errorf("%s declares network, but no deterministic capability uses it", c.ID)
		}
		if c.Effects.Provider != "" {
			t.Errorf("%s declares provider %q, but calls no model", c.ID, c.Effects.Provider)
		}
		if c.Effects.ExternalWrites {
			t.Errorf("%s declares external writes; source stores are read-only", c.ID)
		}
		// cost_known is per-capability, not global. A capability that can
		// spend through the user's own AI CLI cannot price the call — Midden
		// never sees a bill — so declaring cost_known:false is the honest
		// answer and makes the host require approval.
		if !c.Effects.CostKnown && !capabilityMaySpend(c.ID) {
			t.Errorf("%s declares cost_known:false but cannot spend; a free capability must say so", c.ID)
		}
		if c.Effects.CostKnown && capabilityMaySpend(c.ID) {
			t.Errorf("%s can invoke a model but claims its cost is known; Midden never sees the bill", c.ID)
		}
		if c.RequestSchema == "" || c.ResultSchema == "" {
			t.Errorf("%s must reference both a request and a result schema", c.ID)
		}
		if _, ok := d.RequestSchemas[c.RequestSchema]; !ok {
			t.Errorf("%s references request schema %q that the descriptor does not declare", c.ID, c.RequestSchema)
		}
		if _, ok := d.ResultSchemas[c.ResultSchema]; !ok {
			t.Errorf("%s references result schema %q that the descriptor does not declare", c.ID, c.ResultSchema)
		}
		// A capability that is not long-running must not name a poll target.
		if !c.LongRunning && c.PollCapability != "" {
			t.Errorf("%s is not long-running but names poll capability %q", c.ID, c.PollCapability)
		}
	}
}

// TestPermissionsRequestNothingBeyondFilesystem records the claim made to the
// host: installing Midden grants nothing, and the deterministic set needs no
// network, credential, provider, publish or subprocess authority.
// capabilityMaySpend reports whether a capability can invoke a model through
// an authenticated CLI, and therefore cannot know its own cost.
func capabilityMaySpend(id string) bool { return id == CapContentProduce }

func TestPermissionsRequestNothingBeyondFilesystem(t *testing.T) {
	p := Describe().Permissions
	if len(p.Network) != 0 {
		t.Errorf("declared network permissions %v", p.Network)
	}
	if len(p.Credentials) != 0 {
		t.Errorf("declared credential permissions %v", p.Credentials)
	}
	if len(p.PaidProviders) != 0 {
		t.Errorf("declared paid providers %v", p.PaidProviders)
	}
	// Subprocess IS declared, because content.produce shells out to an AI CLI
	// the user is already signed in to. Midden holds no API key and never
	// calls a provider directly, so this is subprocess authority rather than
	// network or credential authority — the distinction the host's permission
	// model depends on.
	want := map[string]bool{"copilot": true, "claude": true, "opencode": true}
	for _, b := range p.Subprocess {
		if !want[b] {
			t.Errorf("declared subprocess %q, which no capability invokes", b)
		}
		if strings.ContainsAny(b, `/\`) {
			t.Errorf("subprocess %q is a path, not a bare name; a module must not nominate an arbitrary executable", b)
		}
	}
	if p.Publish {
		t.Error("declared publish authority")
	}
	if len(p.FilesystemRead) != 3 {
		t.Errorf("expected three read-only source-store roots, got %v", p.FilesystemRead)
	}
}

// TestSchemasAreValidJSON catches a malformed embedded schema at test time
// rather than when a host tries to validate against it.
func TestSchemasAreValidJSON(t *testing.T) {
	d := Describe()
	all := map[string]json.RawMessage{}
	for k, v := range d.RequestSchemas {
		all["request:"+k] = v
	}
	for k, v := range d.ResultSchemas {
		all["result:"+k] = v
	}
	if len(all) == 0 {
		t.Fatal("descriptor declares no schemas")
	}
	for name, raw := range all {
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Errorf("%s is not valid JSON: %v", name, err)
			continue
		}
		if doc["$id"] == nil {
			t.Errorf("%s has no $id", name)
		}
	}
}

// TestSchemaKeysMatchDocumentIDs closes a gap the existing checks leave open:
// they prove a capability's schema reference RESOLVES to a map entry, and that
// each document has an $id — but not that the two agree.
//
// A descriptor can therefore key its schemas by one naming scheme while the
// documents declare another, and every reference still "resolves". The failure
// mode this guards against was found live in a sibling lane: capabilities
// referencing `x.request` while the schema map was keyed by tool name, giving
// an empty intersection and a host unable to validate any request or result.
func TestSchemaKeysMatchDocumentIDs(t *testing.T) {
	d := Describe()
	check := func(mapName string, m map[string]json.RawMessage) {
		for key, raw := range m {
			var doc map[string]any
			if err := json.Unmarshal(raw, &doc); err != nil {
				continue // covered by TestSchemasAreValidJSON
			}
			// An ABSENT $id is legal: the map key identifies the schema, and
			// requiring $id would reject otherwise-valid descriptors. Only a
			// present-and-DISAGREEING $id is a violation, because that is the
			// case where two identities contradict each other.
			//
			// Midden separately requires $id on its OWN schemas
			// (TestSchemasAreValidJSON). That is a house rule about what this
			// module publishes, not a rule about what the protocol accepts —
			// conflating the two would make Midden reject a legal descriptor
			// written by another module.
			id, present := doc["$id"]
			if !present {
				continue
			}
			if s, _ := id.(string); s != key {
				t.Errorf("%s[%q] declares $id %q; when both are present they must agree, "+
					"or a reference can resolve to a schema describing something else", mapName, key, s)
			}
		}
	}
	check("request_schemas", d.RequestSchemas)
	check("result_schemas", d.ResultSchemas)
	check("artifact_schemas", d.ArtifactSchemas)
}

// TestNestedCollectionsAreNeverNull guards the shape that has now appeared in
// three separate lanes: the top-level document is checked and found clean while
// the NESTED collections marshal as null.
//
// It walks the marshalled descriptor rather than inspecting the struct, because
// that is where the difference shows: a nil slice is invisible in Go and only
// becomes `null` on the wire.
func TestNestedCollectionsAreNeverNull(t *testing.T) {
	// A descriptor built the way a careless author would: outer collections
	// populated, inner ones left to Go's zero values.
	d := Descriptor{
		Module:           ModuleID,
		ProtocolVersions: []string{ProtocolID},
		Capabilities: []Capability{
			{ID: "x.one"}, // ArtifactSchemas and Skills deliberately nil
			{ID: "x.two"},
		},
	}
	d.Normalize()

	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var nulls []string
	findNulls(decodeDoc(t, raw), "$", &nulls)
	if len(nulls) > 0 {
		t.Errorf("nested collections marshalled as null at %v; Normalize must recurse", nulls)
	}

	// And prove the guard is load-bearing: the same descriptor WITHOUT
	// Normalize must produce nulls, otherwise this test would pass whether or
	// not Normalize did anything.
	bare := Descriptor{Capabilities: []Capability{{ID: "x.one"}}}
	rawBare, err := json.Marshal(bare)
	if err != nil {
		t.Fatalf("marshal bare: %v", err)
	}
	var bareNulls []string
	findNulls(decodeDoc(t, rawBare), "$", &bareNulls)
	if len(bareNulls) == 0 {
		t.Error("un-normalized descriptor produced no nulls; this test cannot detect a regression")
	}
}

// TestDeclaredSubprocessMatchesReality guards the declared-vs-actual gap found
// live in a sibling lane, where a renderer invoked `node` directly while the
// descriptor's subprocess list — derived from a probe table — never mentioned
// it.
//
// Midden's deterministic capabilities shell out to nothing, so the honest
// declaration is empty. When a model-backed capability is added it must declare
// the CLI names it actually invokes, and this test must be extended alongside
// it rather than deleted.
// TestDeclaredSubprocessMatchesReality guards the declared-vs-actual gap found
// live in a sibling lane, where a renderer invoked `node` while the descriptor
// — derived from a probe table — never mentioned it.
//
// The declaration must be derived from what the code INVOKES. Midden's
// model-backed content path runs exactly the three AI CLIs the exec runner
// knows about, so those three and no others.
func TestDeclaredSubprocessMatchesReality(t *testing.T) {
	declared := map[string]bool{}
	for _, b := range Describe().Permissions.Subprocess {
		declared[b] = true
	}
	// The exec layer's backends are the ground truth for what can be run.
	for _, b := range []string{"copilot", "claude", "opencode"} {
		if !declared[b] {
			t.Errorf("the exec runner can invoke %q but the descriptor does not declare it; "+
				"a host would refuse to grant a binary the module never asked for", b)
		}
		delete(declared, b)
	}
	for b := range declared {
		t.Errorf("descriptor declares subprocess %q that no code path invokes; "+
			"over-declaring is as dishonest as under-reporting", b)
	}
}

// TestUnknownCapabilityIsStructured proves a bad capability produces a routable
// envelope rather than a crash or a bare error string.
func TestUnknownCapabilityIsStructured(t *testing.T) {
	env := Invoke(Request{Protocol: ProtocolID, Capability: "sessions.nope", RequestID: "r1"})
	if env.OK {
		t.Fatal("unknown capability reported ok")
	}
	if env.Error == nil || env.Error.Code != ErrUnknownCapability {
		t.Fatalf("expected %s, got %+v", ErrUnknownCapability, env.Error)
	}
	if env.RequestID != "r1" {
		t.Errorf("request_id must be echoed verbatim, got %q", env.RequestID)
	}
	if env.Protocol != ProtocolID {
		t.Errorf("error envelope must still declare the protocol, got %q", env.Protocol)
	}

	raw, _ := json.Marshal(env)
	var nulls []string
	findNulls(decodeDoc(t, raw), "$", &nulls)
	for _, p := range nulls {
		if !isCostPath(p) {
			t.Errorf("error envelope has null at %s", p)
		}
	}
}

// TestProtocolMismatchIsRefused proves a host speaking another protocol gets a
// stable code rather than a best-effort attempt.
func TestProtocolMismatchIsRefused(t *testing.T) {
	env := Invoke(Request{Protocol: "some.other/v9", Capability: CapSessionsList})
	if env.OK {
		t.Fatal("mismatched protocol was accepted")
	}
	if env.Error == nil || env.Error.Code != ErrUnsupportedProtocol {
		t.Fatalf("expected %s, got %+v", ErrUnsupportedProtocol, env.Error)
	}
}

// TestInvalidToolIsRejected proves scope validation produces a structured
// error rather than silently widening to every tool.
func TestInvalidToolIsRejected(t *testing.T) {
	in, _ := json.Marshal(AssayRequest{Tool: "emacs"})
	env := Invoke(Request{
		Protocol:   ProtocolID,
		Capability: CapSessionsAssay,
		Input:      in,
	})
	if env.OK {
		t.Fatal("unknown tool was accepted")
	}
	if env.Error == nil || env.Error.Code != ErrInvalidRequest {
		t.Fatalf("expected %s, got %+v", ErrInvalidRequest, env.Error)
	}
}

// TestAssayResultNormalizes proves the payload normalizes its own nested maps.
// This is the case Envelope.Normalize() provably cannot reach.
func TestAssayResultNormalizes(t *testing.T) {
	r := &AssayResult{Sessions: []AssaySessionResult{{SessionID: "s1", Tool: "claude"}}}
	env, err := NewResultEnvelope(OpInvoke, "r1", r, LocalFree())
	if err != nil {
		t.Fatalf("build envelope: %v", err)
	}
	raw, _ := json.Marshal(env)
	var nulls []string
	findNulls(decodeDoc(t, raw), "$", &nulls)
	for _, p := range nulls {
		if !isCostPath(p) {
			t.Errorf("null at %s — nested result collections must normalize", p)
		}
	}
}

// TestClampBoundsWork proves an unbounded request cannot ask for unbounded
// work: the ceiling holds even when the caller asks for more.
func TestClampBoundsWork(t *testing.T) {
	cases := []struct{ in, def, ceiling, want int }{
		{0, 25, 200, 25},
		{10, 25, 200, 10},
		{9999, 25, 200, 200},
		{-5, 25, 200, 25},
	}
	for _, c := range cases {
		if got := clamp(c.in, c.def, c.ceiling); got != c.want {
			t.Errorf("clamp(%d,%d,%d) = %d, want %d", c.in, c.def, c.ceiling, got, c.want)
		}
	}
}

// TestDigestFormat pins the cross-lane digest convention: "sha256:" plus
// lowercase hex, prefix mandatory at the module boundary.
func TestDigestFormat(t *testing.T) {
	// sha256 of empty input — the value an empty evidence set produces, which
	// is a valid seed rather than an error.
	got := DigestSHA256(nil)
	want := "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if got != want {
		t.Errorf("DigestSHA256(nil) = %q, want %q", got, want)
	}
	if !ValidDigest(got) {
		t.Error("DigestSHA256 produced a value ValidDigest rejects")
	}

	bad := []string{
		"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",        // bare hex
		"sha256:E3B0C44298FC1C149AFBF4C8996FB92427AE41E4649B934CA495991B7852B855", // uppercase
		"sha256:abc", // truncated
		"md5:abcd",   // wrong algorithm
		"",           // empty
		"sha256:",    // prefix only
	}
	for _, b := range bad {
		if ValidDigest(b) {
			t.Errorf("ValidDigest(%q) = true, want false", b)
		}
	}
}

// TestSummariesDoNotDenyWhatTheCodeDoes guards a defect found through a live
// cockpit: content.produce's summary still said model-backed types were "not
// yet producible" after they had been shipped and verified with a real ADR.
//
// The summary is the ONE line an agent reads before deciding whether to call a
// capability. A summary that denies a working feature removes it from reach as
// effectively as deleting the code, and nothing fails — the agent simply never
// tries. That is the same silent-wrong-answer shape as every other defect this
// project has produced.
func TestSummariesDoNotDenyWhatTheCodeDoes(t *testing.T) {
	// Phrases that assert a capability CANNOT do something. If the code can,
	// the summary is lying to the only reader that matters.
	denials := []string{
		"not yet producible",
		"not yet exposed",
		"not yet supported",
		"cannot currently",
		"is not implemented",
	}
	for _, c := range Describe().Capabilities {
		lower := strings.ToLower(c.Summary)
		for _, d := range denials {
			if strings.Contains(lower, d) {
				t.Errorf("capability %s summary contains %q. If that is still true, say so; "+
					"if the code has since gained the ability, the summary is hiding a working "+
					"feature from the agent that reads it.", c.ID, d)
			}
		}
	}
}

// TestContentProduceSummaryNamesTheModelSplit is the positive half: an agent
// choosing a kind needs to know that some cost money before it picks one.
func TestContentProduceSummaryNamesTheModelSplit(t *testing.T) {
	for _, c := range Describe().Capabilities {
		if c.ID != CapContentProduce {
			continue
		}
		lower := strings.ToLower(c.Summary)
		if !strings.Contains(lower, "free") {
			t.Error("content.produce summary does not tell an agent that some kinds are free")
		}
		if !strings.Contains(lower, "model") {
			t.Error("content.produce summary does not tell an agent that some kinds call a model")
		}
		return
	}
	t.Fatal("content.produce is not declared")
}
