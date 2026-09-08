package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The host runs modules with an EMPTY environment (cmd.Env = []string{}), so
// that everything a module may touch arrives explicitly in the request rather
// than ambiently from the host's process. These tests run the REAL binary that
// way, because the failure they guard against is invisible in-process: the test
// harness inherits an environment, so a unit test would pass while the module
// broke under the host.

func buildModuleBinary(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping subprocess test in short mode")
	}
	name := "midden-envtest"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	bin := filepath.Join(t.TempDir(), name)
	cmd := exec.Command("go", "build", "-o", bin, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build module binary: %v\n%s", err, out)
	}
	return bin
}

// runDetached runs the binary the way the host does: empty environment,
// separated streams.
func runDetached(t *testing.T, bin string, args ...string) (stdout, stderr []byte, err error) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = []string{} // exactly what the host supplies
	var so, se strings.Builder
	cmd.Stdout = &so
	cmd.Stderr = &se
	err = cmd.Run()
	return []byte(so.String()), []byte(se.String()), err
}

// TestDescribeUnderEmptyEnvironment proves discovery works with nothing
// ambient. Describe must never depend on state, an environment variable, or a
// reachable store: a host has to be able to enumerate a module's capabilities
// before granting it anything.
func TestDescribeUnderEmptyEnvironment(t *testing.T) {
	bin := buildModuleBinary(t)

	stdout, stderr, err := runDetached(t, bin, "module", "describe", "--json")
	if err != nil {
		t.Fatalf("describe failed under empty environment: %v\nstderr: %s", err, stderr)
	}
	if len(stderr) != 0 {
		t.Errorf("describe wrote %d bytes to stderr; expected none", len(stderr))
	}

	var env map[string]any
	if err := json.Unmarshal(stdout, &env); err != nil {
		t.Fatalf("stdout is not one JSON document: %v", err)
	}
	if ok, _ := env["ok"].(bool); !ok {
		t.Error("describe must succeed with no environment")
	}
	if got := env["operation"]; got != "describe" {
		t.Errorf("operation = %v, want describe", got)
	}
}

// TestAssayUnderEmptyEnvironmentFailsLoudly is the regression guard for a
// silent-failure mode that shipped and was caught only by running the binary
// the way the host does.
//
// Midden's adapters resolve source stores from the user profile, and the
// helper that does it swallows the error and returns "". Under an empty
// environment every store path silently became relative, matched nothing, and
// sessions.assay returned ok:true with zero sessions and ZERO WARNINGS.
//
// That is the worst available answer: an empty success is indistinguishable
// from "you genuinely have no sessions", so the host agent would truthfully
// report to a user that their recovery scope is empty when in fact the module
// was blind. A module that cannot see its inputs must say so.
func TestAssayUnderEmptyEnvironmentFailsLoudly(t *testing.T) {
	bin := buildModuleBinary(t)

	req := filepath.Join(t.TempDir(), "req.json")
	body := `{"protocol":"xibodev.module/v1","capability":"sessions.assay",` +
		`"request_id":"req-env-guard","input":{"days":30,"max_sessions":3},` +
		`"roots":{},"grants":{},"deadline_ms":60000,"max_output_bytes":1048576}`
	if err := os.WriteFile(req, []byte(body), 0o600); err != nil {
		t.Fatalf("write request: %v", err)
	}

	stdout, _, _ := runDetached(t, bin, "module", "invoke", "sessions.assay", "--input", req)

	var env map[string]any
	if err := json.Unmarshal(stdout, &env); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\ngot: %s", err, stdout)
	}

	ok, _ := env["ok"].(bool)
	if ok {
		result, _ := env["result"].(map[string]any)
		warnings, _ := env["warnings"].([]any)
		// The precise shape of the bug: success, nothing found, nothing said.
		if result != nil && len(warnings) == 0 {
			t.Fatalf("sessions.assay reported SUCCESS with no findings and no warnings under an empty "+
				"environment; an unreachable store must not look like an empty one.\nresult: %v", result)
		}
		t.Fatalf("expected a structured failure, got ok:true\n%s", stdout)
	}

	errObj, _ := env["error"].(map[string]any)
	if errObj == nil {
		t.Fatal("ok:false envelope carries no error object")
	}
	if code, _ := errObj["code"].(string); code != "no_source_stores" {
		t.Errorf("error code = %q, want no_source_stores", code)
	}
	// Retryable: supplying the environment or roots makes the same request
	// succeed, so this is a setup problem rather than a permanent failure.
	if retryable, _ := errObj["retryable"].(bool); !retryable {
		t.Error("a missing precondition the caller can supply must be retryable")
	}
	if env["request_id"] != "req-env-guard" {
		t.Errorf("request_id = %v, want it echoed verbatim", env["request_id"])
	}
}

// TestStdoutIsExactlyOneDocument proves the property the whole contract rests
// on, measured on the real binary rather than asserted: one JSON document,
// nothing before or after it.
func TestStdoutIsExactlyOneDocument(t *testing.T) {
	bin := buildModuleBinary(t)

	stdout, _, err := runDetached(t, bin, "module", "describe", "--json")
	if err != nil {
		t.Fatalf("describe: %v", err)
	}

	dec := json.NewDecoder(strings.NewReader(string(stdout)))
	var doc any
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("stdout is not valid JSON: %v", err)
	}
	if rest := strings.TrimSpace(string(stdout[dec.InputOffset():])); rest != "" {
		t.Errorf("trailing output after the envelope: %q", rest)
	}
}
