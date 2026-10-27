package promptquiz

import (
	"math"
	"math/rand"
	"testing"
)

// measuredSample is the real run-token distribution the three baseline values
// were calibrated against: agent 14043839 v1, n=31, the largest same-version
// sample the shared stack held at calibration time. Frozen here so the
// calibration is replayable from the test alone.
var measuredSample = []float64{
	0, 65248, 79316, 83244, 85482, 89070, 89466, 104275, 104420, 106727,
	109556, 110169, 111349, 111553, 115856, 122899, 124321, 125551, 142012,
	143954, 150381, 155898, 178854, 181927, 228307, 235712, 239498, 246138,
	252725, 254138, 300526,
}

func draw(rng *rand.Rand, n int, scale float64) Sample {
	vals := make([]float64, n)
	for i := range vals {
		vals[i] = measuredSample[rng.Intn(len(measuredSample))] * scale
	}
	return Sample{Values: vals}
}

func rate(t *testing.T, seed int64, trials int, scale float64) float64 {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	hits := 0
	for i := 0; i < trials; i++ {
		v := Compare(draw(rng, BaselineSampleSize, 1.0), draw(rng, NewVersionSampleSize, scale)).Verdict
		if v == VerdictRegressed || v == VerdictImproved {
			hits++
		}
	}
	return float64(hits) / float64(trials)
}

// Summarize is the stored form of a baseline. Hand-checkable numbers, so a
// reader can confirm the quartile convention without running the calibration.
func TestSummarizeIsHandRecomputable(t *testing.T) {
	s := Summarize(Sample{Values: []float64{1, 2, 3, 4, 5, 6, 7, 8, 9}})
	if s.N != 9 {
		t.Fatalf("N = %d, want 9", s.N)
	}
	if s.Mean != 5 || s.Median != 5 {
		t.Fatalf("mean/median = %v/%v, want 5/5", s.Mean, s.Median)
	}
	// Inclusive linear interpolation over 1..9: Q1 = 3, Q3 = 7.
	if s.IQR != 4 {
		t.Fatalf("IQR = %v, want 4 (Q3=7 minus Q1=3)", s.IQR)
	}
	if s.Min != 1 || s.Max != 9 {
		t.Fatalf("min/max = %v/%v, want 1/9", s.Min, s.Max)
	}
}

// An unmeasured version must read as N=0, never as zero cost.
func TestSummarizeEmptySampleIsNotZeroCost(t *testing.T) {
	s := Summarize(Sample{})
	if s.N != 0 {
		t.Fatalf("N = %d, want 0", s.N)
	}
	if c := Compare(Sample{}, Sample{}); c.Verdict != VerdictInsufficient {
		t.Fatalf("verdict = %q, want %q — an unmeasured pair is not a clean bill of health", c.Verdict, VerdictInsufficient)
	}
}

// The dispersion ruling: the IQR is reported because one runaway measurement
// cannot move it, while it multiplies the standard deviation several-fold.
// These are the exact ratios cited in baseline.go.
func TestIQRSurvivesARunawayMeasurementAndStdDevDoesNot(t *testing.T) {
	clean := append([]float64(nil), measuredSample[1:]...) // drop the zero-token run
	base := Summarize(Sample{Values: clean})

	for _, mult := range []float64{3, 5, 10, 20} {
		polluted := append([]float64(nil), clean[:len(clean)-1]...)
		polluted = append(polluted, base.Max*mult)
		got := Summarize(Sample{Values: polluted})

		iqrRatio := got.IQR / base.IQR
		sdRatio := got.StdDev / base.StdDev
		if math.Abs(iqrRatio-1) > 1e-9 {
			t.Errorf("%vx runaway moved the IQR by %.2fx, want exactly 1.00x", mult, iqrRatio)
		}
		if sdRatio < 2 {
			t.Errorf("%vx runaway moved the std dev by only %.2fx — the premise for not using it does not hold", mult, sdRatio)
		}
	}
}

// The noise line's defining property: when both groups come from the SAME
// distribution, the alarm rate sits at the nominal alpha of the 1.96 sigma
// threshold rather than drifting with the sample size. This is what the
// scalar-plus-tolerance and median-plus-IQR rules failed to do.
func TestFalseAlarmRateMatchesTheNominalNoiseLine(t *testing.T) {
	got := rate(t, 20260926, 8000, 1.0)
	if got < 0.02 || got > 0.09 {
		t.Fatalf("false alarm rate = %.3f, want near the nominal 0.05 of a %.2f sigma two-sided line", got, NoiseSigma)
	}
}

// The counter-experiment that justifies rejecting a scalar baseline: mean plus
// two baseline standard deviations gets WORSE at detecting a real shift as N
// grows, because the larger sample widens the very band the shift must clear.
// If this ever stopped holding, the distribution ruling would need re-arguing.
func TestScalarToleranceRuleDegradesWithSampleSize(t *testing.T) {
	meanPlus2SD := func(base, cur Sample) bool {
		b, c := Summarize(base), Summarize(cur)
		return b.StdDev > 0 && math.Abs(c.Mean-b.Mean) > 2*b.StdDev
	}
	detect := func(n int) float64 {
		rng := rand.New(rand.NewSource(20260926))
		hits := 0
		for i := 0; i < 4000; i++ {
			if meanPlus2SD(draw(rng, n, 1.0), draw(rng, n, 1.30)) {
				hits++
			}
		}
		return float64(hits) / 4000
	}
	small, large := detect(5), detect(30)
	if large >= small {
		t.Fatalf("scalar rule detection at N=30 (%.3f) did not fall below N=5 (%.3f); the scalar-baseline rejection rests on it doing so", large, small)
	}
}

// N and the asymmetric group sizes: the configured pair must actually catch the
// shifts they were chosen for, and must beat the symmetric alternative that
// costs the same number of new-version runs.
func TestConfiguredSampleSizesDetectRealShifts(t *testing.T) {
	if got := rate(t, 20260927, 4000, 1.50); got < 0.60 {
		t.Fatalf("+50%% detection = %.3f at base=%d new=%d, want >= 0.60", got, BaselineSampleSize, NewVersionSampleSize)
	}
	if got := rate(t, 20260928, 4000, 2.00); got < 0.90 {
		t.Fatalf("+100%% detection = %.3f, want >= 0.90", got)
	}

	// Symmetric 12 x 12 is the alternative the asymmetry was chosen over.
	rng := rand.New(rand.NewSource(20260929))
	hits := 0
	for i := 0; i < 4000; i++ {
		if math.Abs(mannWhitneyZ(draw(rng, NewVersionSampleSize, 1.0).Values, draw(rng, NewVersionSampleSize, 1.5).Values)) > NoiseSigma {
			hits++
		}
	}
	symmetric := float64(hits) / 4000
	asymmetric := rate(t, 20260927, 4000, 1.50)
	if asymmetric <= symmetric {
		t.Fatalf("asymmetric %d x %d (%.3f) did not beat symmetric %d x %d (%.3f) at +50%%; accumulating the baseline would buy nothing",
			BaselineSampleSize, NewVersionSampleSize, asymmetric, NewVersionSampleSize, NewVersionSampleSize, symmetric)
	}
}

// Direction matters: a cheaper version must not be reported as a regression.
func TestCompareReportsDirection(t *testing.T) {
	rng := rand.New(rand.NewSource(20260930))
	base := draw(rng, BaselineSampleSize, 1.0)

	if v := Compare(base, draw(rng, NewVersionSampleSize, 4.0)).Verdict; v != VerdictRegressed {
		t.Fatalf("4x more expensive reported as %q, want %q", v, VerdictRegressed)
	}
	if v := Compare(base, draw(rng, NewVersionSampleSize, 0.25)).Verdict; v != VerdictImproved {
		t.Fatalf("4x cheaper reported as %q, want %q", v, VerdictImproved)
	}
}

// Below either required size the comparison is refused rather than guessed.
func TestCompareRefusesUndersizedGroups(t *testing.T) {
	rng := rand.New(rand.NewSource(20261001))
	full := draw(rng, BaselineSampleSize, 1.0)

	if v := Compare(draw(rng, BaselineSampleSize-1, 1.0), draw(rng, NewVersionSampleSize, 1.0)).Verdict; v != VerdictInsufficient {
		t.Fatalf("baseline of %d gave %q, want %q", BaselineSampleSize-1, v, VerdictInsufficient)
	}
	if v := Compare(full, draw(rng, NewVersionSampleSize-1, 1.0)).Verdict; v != VerdictInsufficient {
		t.Fatalf("current group of %d gave %q, want %q", NewVersionSampleSize-1, v, VerdictInsufficient)
	}
}

// Ties are pervasive here — identical token counts repeat across measurements —
// so the tie correction has to be present, not assumed away.
func TestMannWhitneyHandlesTiesWithoutDividingByZero(t *testing.T) {
	same := make([]float64, 20)
	for i := range same {
		same[i] = 100000
	}
	if z := mannWhitneyZ(same, append([]float64(nil), same...)); z != 0 {
		t.Fatalf("z = %v for two identical all-tied groups, want 0", z)
	}
}
