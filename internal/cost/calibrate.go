package cost

import (
	"fmt"
	"math"
)

// Estimate is a prediction with honest uncertainty.
//
// A single number implies precision that does not exist. Estimates here are
// ranges, and they carry the sample size behind them so a caller can see
// whether the prediction is grounded or a guess.
type Estimate struct {
	Op        string  `json:"op"`
	RawTokens int     `json:"raw_tokens"` // what Midden's own prompt weighs
	Low       int64   `json:"low"`
	Mid       int64   `json:"mid"`
	High      int64   `json:"high"`
	Factor    float64 `json:"factor"`  // learned multiplier over the raw estimate
	Samples   int     `json:"samples"` // runs the factor is based on
	Unit      string  `json:"unit,omitempty"`
	UnitMid   float64 `json:"unit_mid,omitempty"`
}

// Grounded reports whether the estimate is based on recorded history rather
// than a default.
func (e Estimate) Grounded() bool { return e.Samples > 0 }

// String renders the estimate for a human, never implying false precision.
func (e Estimate) String() string {
	base := fmt.Sprintf("%s–%s tokens", Compact(e.Low), Compact(e.High))
	if e.UnitMid > 0 && e.Unit != "" {
		base += fmt.Sprintf(" (~%.1f %s)", e.UnitMid, e.Unit)
	}
	if !e.Grounded() {
		return base + " [uncalibrated: no prior runs of this kind]"
	}
	return base + fmt.Sprintf(" [from %d prior run(s)]", e.Samples)
}

// defaultFactors are the starting multipliers before any history exists.
//
// They encode the reason the first estimates were so wrong: Midden's own
// prompt is a small fraction of what a CLI actually sends. The CLI adds its
// system prompt and tool definitions, and a staged prompt file costs an extra
// turn to read. These are deliberately pessimistic — an overestimate that
// stops a run is recoverable; an underestimate that empties a quota is not.
var defaultFactors = map[string]float64{
	"reclaim":  60,
	"refine":   90,
	"refinery": 90,
}

const fallbackFactor = 50

// Stats is the recorded history for one operation kind.
type Stats struct {
	Op         string
	Samples    int
	MeanFactor float64
	MinFactor  float64
	MaxFactor  float64
	MeanUnit   float64 // AIU or USD per run, whichever the backend reports
	UnitName   string
}

// Predict turns a raw prompt-size estimate into a calibrated range.
//
// With no history it falls back to a pessimistic default and says so. With
// history it uses the observed mean and widens the range to the observed
// spread, so the prediction converges on truth as the tool is used.
func Predict(op string, rawTokens int, s *Stats) Estimate {
	e := Estimate{Op: op, RawTokens: rawTokens}

	factor, ok := defaultFactors[op]
	if !ok {
		factor = fallbackFactor
	}
	spreadLow, spreadHigh := 0.6, 1.8

	if s != nil && s.Samples > 0 && s.MeanFactor > 0 {
		factor = s.MeanFactor
		e.Samples = s.Samples

		// Use the observed spread once there is enough history to trust it.
		if s.Samples >= 3 && s.MinFactor > 0 && s.MaxFactor > 0 {
			spreadLow = s.MinFactor / s.MeanFactor
			spreadHigh = s.MaxFactor / s.MeanFactor
		}
		if s.MeanUnit > 0 {
			e.Unit = s.UnitName
			e.UnitMid = s.MeanUnit
		}
	}

	e.Factor = factor
	e.Mid = int64(math.Round(float64(rawTokens) * factor))
	e.Low = int64(math.Round(float64(e.Mid) * spreadLow))
	e.High = int64(math.Round(float64(e.Mid) * spreadHigh))

	if e.Low < 0 {
		e.Low = 0
	}
	if e.High < e.Mid {
		e.High = e.Mid
	}
	return e
}

// PredictPerItem scales an estimate by how many items will be produced,
// which is how a caller decides whether a batch is affordable.
func PredictPerItem(e Estimate, items int) Estimate {
	if items <= 1 {
		return e
	}
	out := e
	out.Low *= int64(items)
	out.Mid *= int64(items)
	out.High *= int64(items)
	out.UnitMid *= float64(items)
	return out
}
