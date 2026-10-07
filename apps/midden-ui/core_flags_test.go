package main

import "testing"

func TestCoreValidatorPreservesDataOnlyScanAndQuoteOptions(t *testing.T) {
	app := newTestApp(t)
	for _, args := range [][]string{
		{"find", "needle", "--scan", "10", "--json"},
		{"find", "needle", "--scan=10", "--json"},
		{"collection", "verify", "sources", "--record", "record", "--quote", "recorded words"},
		{"collection", "verify", "sources", "--record=record", "--quote=recorded words"},
	} {
		if err := app.validateCoreArgs(args); err != nil {
			t.Fatalf("valid data arguments rejected: %v: %v", args, err)
		}
	}
}
