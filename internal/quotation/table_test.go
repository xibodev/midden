package quotation

import "testing"

func TestTableFormattingPreservesWordBoundaries(t *testing.T) {
	source := "| Metric | Value |\n| --- | --- |\n| latency | 13 ms |\n"
	if !Matches(source, "latency 13 ms") {
		t.Fatal("faithful rendered table words were rejected")
	}
	if Matches(source, "MetricValuelatency13 ms") {
		t.Fatal("distinct words were concatenated into a false quotation")
	}
}
