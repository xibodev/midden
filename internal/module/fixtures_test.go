package module

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Cross-lane fixture conformance.
//
// The three lanes bind their implementations by CROSS-CHECKED FIXTURES rather
// than by sharing a Go package: modules are detached processes, so the contract
// is the JSON on the wire. This file is the second independent implementation
// that makes the host's fixtures meaningful — a fixture only one implementation
// has ever parsed proves nothing.
//
// The fixtures live in the host repository, which is a sibling checkout rather
// than a dependency. When it is absent these tests SKIP rather than fail:
// Midden must build and test standalone, and a missing sibling is not a defect
// in Midden.

const hostFixtureDir = `E:\open-source-projects\facet-studio\testdata\fixtures`

func fixturePath(t *testing.T, rel string) string {
	t.Helper()
	p := filepath.Join(hostFixtureDir, filepath.FromSlash(rel))
	if _, err := os.Stat(p); err != nil {
		t.Skipf("host fixture unavailable (%s); skipping cross-lane check", rel)
	}
	return p
}

func readFixture(t *testing.T, rel string) []byte {
	t.Helper()
	raw, err := os.ReadFile(fixturePath(t, rel))
	if err != nil {
		t.Fatalf("read fixture %s: %v", rel, err)
	}
	return raw
}

// ---------------------------------------------------------------------------
// The validator
// ---------------------------------------------------------------------------

// validateEnvelopeBytes applies the wire rules to raw module stdout, exactly as
// a host must before trusting module output. It returns the reasons the
// document is invalid; an empty slice means acceptable.
//
// It works on RAW BYTES, not a decoded struct, because several rules are about
// what is on the wire — trailing output, nulls, two documents — and are
// invisible once Go has unmarshalled them into typed zero values.
func validateEnvelopeBytes(raw []byte) []string {
	var bad []string

	if len(strings.TrimSpace(string(raw))) == 0 {
		return []string{"stdout is empty: a module must always emit an envelope, even on failure"}
	}

	// Exactly one JSON document, with nothing before or after it. Decoding the
	// first value and measuring the remainder catches BOTH a second envelope
	// and a trailing plain-text log line; checking only that the whole buffer
	// parses would miss the log line, which is the likelier real failure.
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		return []string{"stdout is not valid JSON: " + err.Error()}
	}
	rest := strings.TrimSpace(string(raw[dec.InputOffset():]))
	if rest != "" {
		bad = append(bad, "trailing output after the envelope: "+truncateForMsg(rest))
	}

	obj, ok := doc.(map[string]any)
	if !ok {
		return append(bad, "envelope is not a JSON object")
	}

	if p, _ := obj["protocol"].(string); p != ProtocolID {
		bad = append(bad, fmt.Sprintf("protocol %q is not %s", p, ProtocolID))
	}

	// ok must agree with exactly one of result/error.
	//
	// The ok:true-with-neither case is the one a real module hits by simply
	// forgetting to set Result: it decodes to a typed zero value rather than an
	// obvious error, so nothing upstream complains. My first version of this
	// switch checked only that ok:true did not carry an error, and therefore
	// ACCEPTED an ok:true envelope carrying nothing at all — caught by the
	// host's fixture, which is precisely what cross-checking is for.
	okFlag, hasOK := obj["ok"].(bool)
	_, hasResult := obj["result"]
	_, hasErr := obj["error"]
	switch {
	case !hasOK:
		bad = append(bad, "missing ok")
	case okFlag && hasErr:
		bad = append(bad, "ok:true carries an error")
	case okFlag && !hasResult:
		bad = append(bad, "ok:true carries no result; exactly one of result/error must be present")
	case !okFlag && !hasErr:
		bad = append(bad, "ok:false carries no error")
	case !okFlag && hasResult:
		bad = append(bad, "ok:false carries a result")
	}

	// Collections must be [] / {}, never null.
	for _, k := range []string{"warnings"} {
		if v, present := obj[k]; present && v == nil {
			bad = append(bad, k+" is null, must be []")
		}
	}
	exec, _ := obj["execution"].(map[string]any)
	if exec == nil {
		bad = append(bad, "missing execution")
		return bad
	}
	if v, present := exec["artifacts"]; present && v == nil {
		bad = append(bad, "execution.artifacts is null, must be []")
	}

	// Artifact confinement and digest format.
	if arts, ok := exec["artifacts"].([]any); ok {
		for i, a := range arts {
			am, ok := a.(map[string]any)
			if !ok {
				continue
			}
			p, _ := am["path"].(string)
			if filepath.IsAbs(p) || strings.HasPrefix(p, "/") || strings.Contains(p, "..") {
				bad = append(bad, fmt.Sprintf("artifact[%d].path %q is not relative to a declared root", i, p))
			}
			if d, _ := am["digest"].(string); d != "" && !ValidDigest(d) {
				bad = append(bad, fmt.Sprintf("artifact[%d].digest %q lacks the mandatory sha256: prefix or is malformed", i, d))
			}
		}
	}

	// Cost keys must be PRESENT. Absent is indistinguishable from unknown for a
	// host reading JSON, and silence about cost is what the pointer types exist
	// to prevent.
	for _, k := range []string{"estimated_cost", "actual_cost"} {
		if _, present := exec[k]; !present {
			bad = append(bad, "execution."+k+" is absent; it must be a number or explicit null")
		}
	}

	// operation names one of the two VERBS the host speaks, never a capability.
	// The capability travels in the request and is correlated by request_id;
	// duplicating it here would make the field mean different things depending
	// on which module answered. This assertion exists because the value was NOT
	// pinned by any fixture, and two lanes had already diverged on it unnoticed:
	// a shared field with no fixture pinning its value is where drift hides.
	switch op, _ := obj["operation"].(string); op {
	case OpDescribe, OpInvoke:
	default:
		bad = append(bad, fmt.Sprintf(
			"operation %q is not one of %q/%q; it names the verb, not the capability", op, OpDescribe, OpInvoke))
	}

	return bad
}

// validateAgainstDeclaredEffects catches what the envelope alone cannot: an
// execution that over-reaches its capability's DECLARED effects. Cost-zero-
// instead-of-unknown is the headline case, because 0 is a structurally valid
// number and only the declaration says it is a lie.
//
// It takes the DESCRIPTOR and looks the capability up itself, rather than
// accepting a costKnown bool. An earlier version took that bool from the
// caller, which meant it ratified whatever the caller believed — a wrong
// belief would have produced a passing test. A validator must read the
// declaration, never be told it. (Same shape as a validator that accepts any
// verb a caller invents: both let the caller define the contract.)
//
// It checks the SAFE DIRECTION ONLY. Under-promising is fine — a capability
// that declares network and makes none is honest. Over-reaching is not.
func validateAgainstDeclaredEffects(raw []byte, d *Descriptor, capabilityID string) []string {
	var doc map[string]any
	if json.Unmarshal(raw, &doc) != nil {
		return nil
	}
	exec, _ := doc["execution"].(map[string]any)
	if exec == nil {
		return nil
	}

	// An undeclared capability is rejected, never skipped: an execution with no
	// declaration has no approval basis, and silently passing it would make the
	// check weakest exactly where it matters most.
	var cap *Capability
	for i := range d.Capabilities {
		if d.Capabilities[i].ID == capabilityID {
			cap = &d.Capabilities[i]
			break
		}
	}
	if cap == nil {
		return []string{fmt.Sprintf("capability %q is not declared by the descriptor; its execution has no approval basis", capabilityID)}
	}

	var bad []string
	if !cap.Effects.Network {
		if v, _ := exec["network"].(bool); v {
			bad = append(bad, "execution reports network for a capability declaring network:false")
		}
	}
	if !cap.Effects.ExternalWrites {
		if v, _ := exec["external_writes"].(bool); v {
			bad = append(bad, "execution reports external_writes for a capability declaring external_writes:false")
		}
	}
	if !cap.Effects.CostKnown {
		for _, k := range []string{"estimated_cost", "actual_cost"} {
			if v, present := exec[k]; present && v != nil {
				bad = append(bad, fmt.Sprintf(
					"execution.%s reports %v for a capability declaring cost_known:false; unknown cost must stay null, never 0", k, v))
			}
		}
	}

	// An artifact whose kind the capability never declared leaves the host with
	// no schema to validate or render it against.
	if arts, ok := exec["artifacts"].([]any); ok {
		for i, a := range arts {
			am, _ := a.(map[string]any)
			if am == nil {
				continue
			}
			kind, _ := am["kind"].(string)
			declared := false
			for _, k := range cap.ArtifactSchemas {
				if k == kind {
					declared = true
					break
				}
			}
			if !declared {
				bad = append(bad, fmt.Sprintf(
					"artifact[%d].kind %q is not in capability %q's declared artifact_schemas", i, kind, capabilityID))
			}
		}
	}

	return bad
}

func truncateForMsg(s string) string {
	if len(s) > 60 {
		return s[:60] + "…"
	}
	return s
}

// ---------------------------------------------------------------------------
// Positive fixtures: every one must be ACCEPTED
// ---------------------------------------------------------------------------

func TestHostPositiveFixturesAreAccepted(t *testing.T) {
	positives := []string{
		"describe/descriptor-full.json",
		"describe/descriptor-minimal.json",
		"invoke/success-local-deterministic.json",
		"invoke/success-with-artifact.json",
		"invoke/estimate-known-free.json",
		"invoke/estimate-unknown-cost.json",
		"invoke/long-running-job-handle.json",
		"invoke/error-invalid-request.json",
		"invoke/error-unknown-capability.json",
		"invoke/error-permission-denied.json",
		"invoke/error-path-outside-root.json",
		"invoke/error-missing-requirement.json",
		"invoke/error-provider-failure-unknown-cost.json",
	}
	for _, rel := range positives {
		t.Run(rel, func(t *testing.T) {
			raw := readFixture(t, rel)
			if bad := validateEnvelopeBytes(raw); len(bad) > 0 {
				t.Errorf("host fixture REJECTED by Midden's validator — drift signal:\n  %s",
					strings.Join(bad, "\n  "))
			}
		})
	}
}

// TestDescriptorMinimalDecodesIntoMiddenTypes is the strongest cross-lane
// check: the host's smallest legal descriptor must decode into Midden's OWN
// independently written types, and every collection must survive as empty
// rather than null. Two implementations agreeing on one document is what makes
// the fixture meaningful.
func TestDescriptorMinimalDecodesIntoMiddenTypes(t *testing.T) {
	raw := readFixture(t, "describe/descriptor-minimal.json")

	var env Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("host envelope does not decode into Midden's Envelope: %v", err)
	}
	if env.Protocol != ProtocolID {
		t.Errorf("protocol %q != %q", env.Protocol, ProtocolID)
	}
	if !env.OK {
		t.Error("descriptor fixture is not ok")
	}
	if env.Warnings == nil {
		t.Error("warnings decoded as nil; fixture must carry []")
	}

	var d Descriptor
	if err := json.Unmarshal(env.Result, &d); err != nil {
		t.Fatalf("host descriptor does not decode into Midden's Descriptor: %v", err)
	}
	if len(d.ProtocolVersions) != 1 || d.ProtocolVersions[0] != ProtocolID {
		t.Errorf("protocol_versions = %v", d.ProtocolVersions)
	}
	if len(d.Capabilities) != 1 {
		t.Fatalf("expected exactly one capability, got %d", len(d.Capabilities))
	}

	// The point of the minimal fixture: empty collections are [] / {}, so a
	// consumer never has to distinguish absent from empty.
	if d.RequestSchemas == nil || d.ResultSchemas == nil || d.ArtifactSchemas == nil {
		t.Error("schema maps decoded as nil; the minimal fixture must carry {}")
	}
	if d.AgentOverlays == nil || d.Skills == nil || d.Requirements == nil {
		t.Error("collections decoded as nil; the minimal fixture must carry []")
	}
	if d.Permissions.FilesystemRead == nil || d.Permissions.Subprocess == nil {
		t.Error("permission slices decoded as nil; must carry []")
	}
	if d.Capabilities[0].ArtifactSchemas == nil || d.Capabilities[0].Skills == nil {
		t.Error("capability collections decoded as nil; must carry []")
	}

	// Re-marshalling through Midden's types must not reintroduce nulls.
	d.Normalize()
	out, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("re-marshal: %v", err)
	}
	var nulls []string
	findNulls(decodeDoc(t, out), "$", &nulls)
	if len(nulls) > 0 {
		t.Errorf("round-trip through Midden's types produced nulls at %v", nulls)
	}
}

// TestHostRequestDecodesIntoMiddenRequest proves Midden can consume what the
// host actually sends, including canonicalized roots with access modes.
func TestHostRequestDecodesIntoMiddenRequest(t *testing.T) {
	raw := readFixture(t, "invoke/request-example.json")

	var req Request
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatalf("host request does not decode into Midden's Request: %v", err)
	}
	if req.Protocol != ProtocolID {
		t.Errorf("protocol %q != %q", req.Protocol, ProtocolID)
	}
	if req.RequestID == "" {
		t.Error("request carries no request_id to echo")
	}
	if req.DeadlineMS <= 0 {
		t.Error("request carries no deadline")
	}
	if req.MaxOutputBytes <= 0 {
		t.Error("request carries no output ceiling")
	}
	if len(req.Roots) == 0 {
		t.Fatal("request carries no roots")
	}

	for name, r := range req.Roots {
		if r.Mode != "ro" && r.Mode != "rw" {
			t.Errorf("root %q has mode %q, want ro or rw", name, r.Mode)
		}
		if !filepath.IsAbs(r.Path) && !strings.HasPrefix(r.Path, "/") {
			t.Errorf("root %q path %q is not absolute; the host must supply canonicalized paths", name, r.Path)
		}
	}

	// A staged seed root is read-only: a consumer must not be able to mutate
	// the producer's output.
	if seed, ok := req.Roots["seed_in"]; ok && seed.Mode != "ro" {
		t.Errorf("seed_in root is %q, must be ro", seed.Mode)
	}
}

// ---------------------------------------------------------------------------
// Negative fixtures: every one must be REJECTED
// ---------------------------------------------------------------------------

func TestHostMalformedFixturesAreRejected(t *testing.T) {
	// Every file the host says a module must never produce, with the property
	// that catches it. Accepting any of these is the drift signal.
	negatives := []string{
		"malformed/two-json-documents.txt",
		"malformed/progress-line-before-envelope.txt",
		"malformed/trailing-log-line.txt",
		"malformed/empty-stdout.txt",
		"malformed/not-json.txt",
		"malformed/null-collections.txt",
		"malformed/wrong-protocol.txt",
		"malformed/ok-true-with-error.txt",
		"malformed/ok-true-with-neither-result-nor-error.txt",
		"malformed/operation-is-capability-not-verb.txt",
		"malformed/absolute-artifact-path.txt",
		"malformed/bare-hex-digest.txt",
	}
	for _, rel := range negatives {
		t.Run(rel, func(t *testing.T) {
			raw := readFixture(t, rel)
			if bad := validateEnvelopeBytes(raw); len(bad) == 0 {
				t.Error("malformed fixture ACCEPTED by Midden's validator — drift signal")
			}
		})
	}
}

// TestCostZeroInsteadOfUnknownNeedsDeclaredEffects records the host's own
// caveat honestly: this fixture is NOT structurally detectable. A 0 is a valid
// number, so the envelope alone cannot condemn it. It is caught only by
// comparing against the capability's declared cost_known.
func TestCostZeroInsteadOfUnknownNeedsDeclaredEffects(t *testing.T) {
	raw := readFixture(t, "malformed/cost-zero-instead-of-unknown.txt")

	if bad := validateEnvelopeBytes(raw); len(bad) != 0 {
		t.Errorf("structural validation was expected to PASS this fixture, but reported: %v", bad)
	}

	// A synthetic descriptor declaring the capability as UNPRICED. The check
	// must read this, not be told the answer.
	unpriced := &Descriptor{Capabilities: []Capability{{
		ID:      "fake.echo",
		Effects: Effects{Local: false, Network: true, CostKnown: false},
	}}}
	bad := validateAgainstDeclaredEffects(raw, unpriced, "fake.echo")
	if len(bad) == 0 {
		t.Error("effects-aware validation failed to catch 0-instead-of-null; this is the expensive one")
	}

	// MUTATION GUARD, inverse direction: a capability that genuinely knows its
	// cost MAY report 0. Without this the check could degenerate into a blanket
	// ban on zero and would prove nothing.
	priced := &Descriptor{Capabilities: []Capability{{
		ID:      "fake.echo",
		Effects: Effects{Local: false, Network: true, CostKnown: true},
	}}}
	if bad := validateAgainstDeclaredEffects(raw, priced, "fake.echo"); len(bad) != 0 {
		t.Errorf("a capability declaring cost_known:true may report 0, but validation rejected it: %v", bad)
	}

	// An undeclared capability is rejected, not skipped: no declaration means
	// no approval basis.
	if bad := validateAgainstDeclaredEffects(raw, priced, "never.declared"); len(bad) == 0 {
		t.Error("an undeclared capability must be rejected, not silently skipped")
	}
}

// TestRequestIDNotEchoedNeedsTheRequest is the same class of honesty: a module
// inventing its own request_id is only detectable against the id the host sent.
func TestRequestIDNotEchoedNeedsTheRequest(t *testing.T) {
	reqRaw := readFixture(t, "invoke/request-example.json")
	respRaw := readFixture(t, "malformed/request-id-not-echoed.txt")

	var req Request
	if err := json.Unmarshal(reqRaw, &req); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	var resp Envelope
	if err := json.Unmarshal(respRaw, &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.RequestID == req.RequestID {
		t.Fatal("fixture is supposed to carry a non-echoed request_id")
	}
	if len(validateEnvelopeBytes(respRaw)) != 0 {
		t.Log("note: this fixture also fails structurally, but correlation is the property that matters")
	}
}

// TestOversizedInlinePayloadIsNotStructurallyDetectable records the third
// honest limit in the negative set. A 4KB inline payload is a well-formed
// envelope: nothing in the document itself is wrong, so structural validation
// MUST pass it. It is caught by the host enforcing max_output_bytes at read
// time, not by inspecting the JSON.
//
// The test asserts the PASS deliberately. If someone later "hardens" the
// validator to reject large results, this fails and explains that the rule
// lives in the host's read path — and that the real fix for a module is to
// return an artifact pointer rather than an inline payload.
func TestOversizedInlinePayloadIsNotStructurallyDetectable(t *testing.T) {
	raw := readFixture(t, "malformed/oversized-inline-payload.txt")

	if bad := validateEnvelopeBytes(raw); len(bad) != 0 {
		t.Errorf("structural validation was expected to PASS this well-formed envelope, but reported: %v", bad)
	}

	// What DOES catch it: the host's declared ceiling. A module handed this
	// budget should have produced an artifact pointer instead.
	const hostCeiling = 1024
	if len(raw) <= hostCeiling {
		t.Fatalf("fixture is %d bytes; it must exceed the modelled ceiling to be meaningful", len(raw))
	}

	// And the envelope proves the module had the alternative available and
	// did not use it: artifacts is empty while the payload is inline.
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	exec, _ := doc["execution"].(map[string]any)
	arts, _ := exec["artifacts"].([]any)
	if len(arts) != 0 {
		t.Error("fixture should model the wrong behaviour: inline payload with NO artifact pointer")
	}
}

// ---------------------------------------------------------------------------
// Midden's own output, through the host's rules
// ---------------------------------------------------------------------------

// TestMiddenOutputPassesHostValidation closes the loop: Midden's real envelopes
// are validated by the same function that judges the host's fixtures. A rule
// that only ever runs against someone else's documents proves nothing about us.
func TestMiddenOutputPassesHostValidation(t *testing.T) {
	// describe
	d := Describe()
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal descriptor: %v", err)
	}
	env, err := NewResultEnvelope("describe", "", json.RawMessage(raw), LocalFree())
	if err != nil {
		t.Fatalf("build describe envelope: %v", err)
	}
	out, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal describe envelope: %v", err)
	}
	if bad := validateEnvelopeBytes(out); len(bad) > 0 {
		t.Errorf("Midden's describe envelope fails host validation:\n  %s", strings.Join(bad, "\n  "))
	}

	// A structured error envelope must satisfy the same rules.
	errEnv := NewErrorEnvelope(OpInvoke, "req-1", Error{
		Code:      ErrInvalidRequest,
		Message:   "unknown tool",
		Retryable: false,
	}, LocalFree())
	errOut, err := json.Marshal(errEnv)
	if err != nil {
		t.Fatalf("marshal error envelope: %v", err)
	}
	if bad := validateEnvelopeBytes(errOut); len(bad) > 0 {
		t.Errorf("Midden's error envelope fails host validation:\n  %s", strings.Join(bad, "\n  "))
	}

	// And a result payload with nested collections.
	res := &AssayResult{Sessions: []AssaySessionResult{{SessionID: "s1", Tool: "claude"}}}
	okEnv, err := NewResultEnvelope(OpInvoke, "req-2", res, LocalFree())
	if err != nil {
		t.Fatalf("build result envelope: %v", err)
	}
	okOut, err := json.Marshal(okEnv)
	if err != nil {
		t.Fatalf("marshal result envelope: %v", err)
	}
	if bad := validateEnvelopeBytes(okOut); len(bad) > 0 {
		t.Errorf("Midden's assay envelope fails host validation:\n  %s", strings.Join(bad, "\n  "))
	}

	// Midden's deterministic capabilities declare cost_known:true and no
	// network or external writes, so the effects-aware check must also pass
	// against Midden's OWN descriptor — read, not asserted.
	d2 := Describe()
	if bad := validateAgainstDeclaredEffects(okOut, &d2, CapSessionsAssay); len(bad) > 0 {
		t.Errorf("effects-aware validation failed against Midden's own descriptor: %v", bad)
	}
}
