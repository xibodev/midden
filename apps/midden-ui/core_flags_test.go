package main

import "testing"

func TestCoreWrapperPreservesDataOnlyScanAndQuoteOptions(t *testing.T) {
	app, err := NewApp(testOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	tool := coreTool{app}
	for _, args := range [][]string{
		{"find", "needle", "--scan", "10", "--json"},
		{"find", "needle", "--scan=10", "--json"},
		{"collection", "verify", "sources", "--record", "record", "--quote", "recorded words"},
		{"collection", "verify", "sources", "--record=record", "--quote=recorded words"},
	} {
		if err := tool.validate(args); err != nil {
			t.Fatalf("valid data arguments rejected: %v: %v", args, err)
		}
	}
}
