package cost

import (
	"strings"
	"testing"
	"time"
)

func TestUsageBillableCountsEveryTokenThatMoved(t *testing.T) {
	// Cache reads and writes are real spending. An accounting that ignores
	// them understates cost by most of it: in one measured session cache
	// writes alone were ~55% of the charge.
	u := Usage{InputTokens: 100, OutputTokens: 20, CacheRead: 500, CacheWrite: 300}
	if got := u.Billable(); got != 920 {
		t.Errorf("Billable() = %d, want 920", got)
	}
}

func TestUsagePrefersSubscriptionUnits(t *testing.T) {
	// Under a seat the scarce resource is credits, not dollars.
	if got := (Usage{AIU: 81.4, InputTokens: 400000}).Unit(); got != "81.4 AIU" {
		t.Errorf("Unit() = %q, want AIU", got)
	}
	if got := (Usage{USD: 4.59}).Unit(); got != "$4.59" {
		t.Errorf("Unit() = %q, want dollars", got)
	}
	if got := (Usage{InputTokens: 2000}).Unit(); !strings.Contains(got, "tok") {
		t.Errorf("Unit() = %q, want a token count fallback", got)
	}
}

func TestUsageAddAccumulates(t *testing.T) {
	var total Usage
	total.Add(Usage{Turns: 1, InputTokens: 10, AIU: 1.5, Model: "opus"})
	total.Add(Usage{Turns: 2, InputTokens: 20, AIU: 2.5})

	if total.Turns != 3 || total.InputTokens != 30 || total.AIU != 4 {
		t.Errorf("Add did not accumulate: %+v", total)
	}
	if total.Model != "opus" {
		t.Errorf("model should be retained, got %q", total.Model)
	}
}

func TestRunAccuracyExposesBadEstimates(t *testing.T) {
	// The first implementation predicted 3,069 tokens for a run that consumed
	// 219,178. Nothing surfaced that, so a "budget" was enforced against a
	// number 70x too small. Accuracy makes the error visible.
	r := Run{EstTokens: 3069, Usage: Usage{InputTokens: 219178}}
	if got := r.Accuracy(); got < 70 || got > 72 {
		t.Errorf("Accuracy() = %.1f, want ~71", got)
	}

	// No estimate or no usage means no claim.
	if (Run{}).Accuracy() != 0 {
		t.Error("empty run should report no accuracy rather than a fake one")
	}
}

func TestRunPerItem(t *testing.T) {
	r := Run{Items: 6, Usage: Usage{AIU: 81.4}}
	if got := r.PerItem(); got < 13.5 || got > 13.7 {
		t.Errorf("PerItem() = %.2f, want ~13.6 AIU", got)
	}
	if (Run{Items: 0}).PerItem() != 0 {
		t.Error("zero items should not divide by zero")
	}
}

func TestPredictIsPessimisticWithoutHistory(t *testing.T) {
	// An overestimate that stops a run is recoverable; an underestimate that
	// empties a quota is not.
	e := Predict("reclaim", 1000, nil)

	if e.Grounded() {
		t.Error("should not claim to be grounded with no samples")
	}
	if e.Mid <= 1000 {
		t.Errorf("uncalibrated estimate must exceed the raw prompt size, got %d", e.Mid)
	}
	if !strings.Contains(e.String(), "uncalibrated") {
		t.Errorf("must say it is uncalibrated: %q", e.String())
	}
	if e.Low > e.Mid || e.High < e.Mid {
		t.Errorf("range is inverted: %d-%d-%d", e.Low, e.Mid, e.High)
	}
}

func TestPredictUsesRecordedHistory(t *testing.T) {
	s := &Stats{Op: "reclaim", Samples: 4, MeanFactor: 184,
		MinFactor: 150, MaxFactor: 220, MeanUnit: 81.4, UnitName: "AIU"}
	e := Predict("reclaim", 1000, s)

	if !e.Grounded() || e.Samples != 4 {
		t.Errorf("should be grounded in 4 samples: %+v", e)
	}
	if e.Mid != 184000 {
		t.Errorf("Mid = %d, want 184000", e.Mid)
	}
	if e.Low >= e.Mid || e.High <= e.Mid {
		t.Errorf("observed spread should widen the range: %d-%d-%d", e.Low, e.Mid, e.High)
	}
	if !strings.Contains(e.String(), "AIU") {
		t.Errorf("should surface the charge unit: %q", e.String())
	}
	if !strings.Contains(e.String(), "4 prior run") {
		t.Errorf("should cite its sample size: %q", e.String())
	}
}

func TestPredictNarrowSpreadUntilEnoughSamples(t *testing.T) {
	// One outlier should not define the range.
	s := &Stats{Op: "refine", Samples: 1, MeanFactor: 100, MinFactor: 100, MaxFactor: 100}
	e := Predict("refine", 1000, s)
	if e.Low == e.Mid && e.High == e.Mid {
		t.Error("a single sample should still produce a range, not a point")
	}
}

func TestPredictPerItemScales(t *testing.T) {
	base := Predict("refine", 1000, nil)
	scaled := PredictPerItem(base, 3)

	if scaled.Mid != base.Mid*3 {
		t.Errorf("Mid did not scale: %d vs %d", scaled.Mid, base.Mid*3)
	}
	if PredictPerItem(base, 1).Mid != base.Mid {
		t.Error("a single item should not scale")
	}
}

func TestCompact(t *testing.T) {
	cases := map[int64]string{
		0: "0", 999: "999", 1500: "2k", 439000: "439k", 9204089: "9.2M",
	}
	for in, want := range cases {
		if got := Compact(in); got != want {
			t.Errorf("Compact(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestRunDurationFallsBackToRecordedTime(t *testing.T) {
	r := Run{Usage: Usage{DurationMS: 70000}}
	if got := r.Duration(); got != 70*time.Second {
		t.Errorf("Duration() = %v, want 70s", got)
	}

	start := time.Now().Add(-2 * time.Minute)
	r2 := Run{StartedAt: start, EndedAt: start.Add(90 * time.Second)}
	if got := r2.Duration(); got != 90*time.Second {
		t.Errorf("Duration() = %v, want 90s", got)
	}
}
