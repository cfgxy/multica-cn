package clausequalityrollup

import (
	"math"
	"testing"
)

func w(finished, attributable int64) versionWindow {
	return versionWindow{FinishedRuns: finished, AttributableFails: attributable}
}

// A clearly degraded version against a healthy baseline must test
// significant at α=0.05; the mirror image must not.
func TestTwoProportionZDetectsDegradation(t *testing.T) {
	base := w(200, 10)  // 5% attributable failure rate
	cur := w(200, 30)   // 15%
	z, p := twoProportionZ(base, cur)
	if z <= 0 {
		t.Fatalf("degradation must give positive z, got %v", z)
	}
	if p >= Alpha {
		t.Fatalf("15%% vs 5%% over n=200 must be significant, p=%v", p)
	}

	// Mirror: the baseline being the worse one is improvement, z negative,
	// never flagged.
	z2, p2 := twoProportionZ(cur, base)
	if z2 >= 0 {
		t.Fatalf("improvement must give non-positive z, got %v", z2)
	}
	if p2 < Alpha {
		t.Fatalf("one-sided test must not flag improvement as degradation, p=%v", p2)
	}
}

// Identical rates — including the zero-variance corner — must never be
// called significant, and must not produce NaN.
func TestTwoProportionZIdenticalRates(t *testing.T) {
	cases := [][2]versionWindow{
		{w(100, 5), w(100, 5)},
		{w(100, 0), w(100, 0)},
		{w(100, 100), w(100, 100)},
		{w(30, 3), w(30, 3)},
	}
	for i, c := range cases {
		z, p := twoProportionZ(c[0], c[1])
		if math.IsNaN(z) || math.IsNaN(p) {
			t.Fatalf("case %d: NaN (z=%v p=%v)", i, z, p)
		}
		if z != 0 || p != 0.5 {
			t.Fatalf("case %d: identical rates must be neutral, got z=%v p=%v", i, z, p)
		}
	}
}

// Small but real-looking differences over the minimum sample must stay under
// the significance bar — the test must not turn noise into rollbacks.
func TestTwoProportionZNoiseStaysInsignificant(t *testing.T) {
	base := w(30, 3) // 10%
	cur := w(30, 5)  // ~16.7% — same magnitude noise the gate exists for
	_, p := twoProportionZ(base, cur)
	if p < Alpha {
		t.Fatalf("noise at n=30 must not be significant, p=%v", p)
	}
}

func TestRateZeroDenominator(t *testing.T) {
	if got := rate(versionWindow{}); got != 0 {
		t.Fatalf("empty window rate must be 0, got %v", got)
	}
}
