package promptquiz

import (
	"math"
	"sort"
)

// The regression baseline is a DISTRIBUTION, not a scalar: a version's reading
// is "mean + dispersion + N", and the verdict is whether the new version's
// sample GROUP differs from the baseline sample GROUP by more than the noise
// two groups of the same version show against each other.
//
// The three values below are not judgement calls. They were calibrated by
// resampling the real run-token distribution measured on this deployment
// (agent 14043839 v1, n=31, the largest same-version sample the shared stack
// held), 20000 trials per cell, seed 20260926. Null trials draw both groups
// from the same distribution, so every alarm is a false positive; alternative
// trials shift one group by a known multiplier, so every silence is a missed
// regression. baseline_test.go replays the decisive cells against a frozen
// sample so the numbers below can be re-derived, not taken on trust.

// NewVersionSampleSize is N: how many graded measurements a new version needs
// before it is comparable.
//
// WHY 12. On the measured distribution, detection of a +50% shift runs
// 0.391 at N=8, 0.567 at N=12 and 0.689 at N=16 — 12 is where the
// cost/power curve turns over, since the last step buys 0.12 more power for
// 33% more runs. Below 8 the test is too blind to be worth running at all.
const NewVersionSampleSize = 12

// BaselineSampleSize is the size the baseline group is accumulated to.
//
// WHY ASYMMETRIC. The baseline group does not have to be re-measured: it
// accumulates across the version's whole life, so its size is free while the
// new version's 12 runs are the entire cost. Paying for it works:
// base=30 x new=12 detects +50% at 0.733 and +100% at 0.968, against
// 0.567 / 0.885 for a symmetric 12 x 12, and base=60 x new=12 reaches
// 0.809 / 0.986. 30 is the point past which further accumulation adds less
// than it costs to wait for.
const BaselineSampleSize = 30

// NoiseSigma is the noise line: how many standard deviations of the U
// statistic a group difference must exceed to be called a change. 1.96 is the
// two-sided alpha = 0.05 critical value of the normal approximation.
//
// WHY A RANK TEST AND NOT mean +/- k*sd. The scalar-plus-tolerance rule was
// measured and is not merely weaker — it is inverted. Its detection of a real
// +30% shift FALLS as N grows, 0.161 at N=5 down to 0.002 at N=30, because the
// larger sample also estimates the baseline's own sd larger and widens the very
// band the shift has to clear. The median +/- IQR family fails differently: its
// false-alarm rate slides from 0.271 at N=5 to 0.006 at N=30, so the same
// alarm means something different at every sample size. Mann-Whitney's false
// alarm rate holds at its nominal value across the whole grid (0.044 to 0.051),
// which is what makes "this alarm means a 5% chance of being noise" a true
// statement rather than a label.
//
// WHY NOT 2.58 (alpha = 0.01). At N=12 it detects a +50% shift only 0.195 of
// the time — too deaf to a regression that is already large enough to notice by
// hand.
const NoiseSigma = 1.96

// Dispersion is reported as the interquartile range, and long-tail runs are
// KEPT rather than trimmed or dropped.
//
// WHY. Measured on the same sample, replacing one run with a 10x runaway
// multiplies the standard deviation by 8.16 (64394 -> 525735) and leaves the
// IQR untouched at 1.00x; at 20x, sd goes 16.66x and IQR still 1.00x. The
// production data says the same thing directly: agent f65fc145 has
// sd/mean = 0.930 but IQR/median = 0.249 with max/median = 5.03 — one runaway
// owns its standard deviation.
//
// Dropping those runs was rejected: "this version runs away more often" is a
// real property of the version and the single most useful thing a quiz can
// surface. Truncating was rejected for the same reason plus a second one — a
// cap is another arbitrary constant to defend. Keeping the sample and reporting
// a statistic the tail cannot move gets both.

// Sample is one version's measurement group.
type Sample struct {
	// Values are the per-measurement readings, one per graded repeat.
	Values []float64
	// Scores are the graded readings of the SAME rows Values was built from
	// (RUYI-286): an item id plus its weighted pass ratio, present only when
	// the row was graded. Same cohort filter, so "comparable and graded" —
	// a score that a bank edit or a runtime switch made incomparable stays
	// out, exactly where its token sibling stays out.
	Scores []ScoredReading
}

// ScoredReading is one graded measurement reduced to what score aggregation
// needs. No answer text, no evidence: those live in score_detail and stay
// behind the Owner-only read that returns them.
type ScoredReading struct {
	ItemID string
	Score  float64
}

// Summary is the stored form of a baseline: mean, dispersion, N.
type Summary struct {
	N      int     `json:"n"`
	Mean   float64 `json:"mean"`
	Median float64 `json:"median"`
	// IQR is the dispersion measure of record. Standard deviation is reported
	// alongside it for operators who ask, never used for a decision.
	IQR    float64 `json:"iqr"`
	StdDev float64 `json:"std_dev"`
	Min    float64 `json:"min"`
	Max    float64 `json:"max"`
}

// Summarize folds a sample into its stored form. An empty sample yields a zero
// Summary with N=0, which every caller must read as "not measured" rather than
// as zero cost.
func Summarize(s Sample) Summary {
	n := len(s.Values)
	if n == 0 {
		return Summary{}
	}
	xs := append([]float64(nil), s.Values...)
	sort.Float64s(xs)

	var total float64
	for _, v := range xs {
		total += v
	}
	mean := total / float64(n)

	out := Summary{
		N:      n,
		Mean:   mean,
		Median: quantile(xs, 0.5),
		IQR:    quantile(xs, 0.75) - quantile(xs, 0.25),
		Min:    xs[0],
		Max:    xs[n-1],
	}
	if n > 1 {
		var ss float64
		for _, v := range xs {
			d := v - mean
			ss += d * d
		}
		out.StdDev = math.Sqrt(ss / float64(n-1))
	}
	return out
}

// Verdict is the outcome of comparing two sample groups.
type Verdict string

const (
	// VerdictInsufficient means the comparison was not made: one of the groups
	// is below its required size. It is NOT "no change" — reporting it as such
	// would turn an unmeasured version into a clean bill of health.
	VerdictInsufficient Verdict = "insufficient"
	// VerdictSteady means the groups differ by no more than same-version noise.
	VerdictSteady Verdict = "steady"
	// VerdictImproved means the new group is significantly lower (cheaper).
	VerdictImproved Verdict = "improved"
	// VerdictRegressed means the new group is significantly higher.
	VerdictRegressed Verdict = "regressed"
)

// Comparison is a full, re-derivable account of one decision. Every input to
// the verdict is carried on it so a reader can recompute the call by hand.
type Comparison struct {
	Baseline Summary `json:"baseline"`
	Current  Summary `json:"current"`
	// Z is the Mann-Whitney U statistic in units of its own standard
	// deviation, signed: positive when the current group ranks higher.
	Z         float64 `json:"z"`
	Threshold float64 `json:"threshold"`
	Verdict   Verdict `json:"verdict"`
}

// Compare decides whether the current group differs from the baseline group by
// more than noise.
//
// This function is the whole reason the quiz exists, and it deliberately
// returns a LABEL. Nothing in the publish path calls it: a regressed verdict is
// a reading, never a gate (Owner Q10). See TestQuizNeverBlocksPromptPublish.
func Compare(baseline, current Sample) Comparison {
	b, c := Summarize(baseline), Summarize(current)
	out := Comparison{Baseline: b, Current: c, Threshold: NoiseSigma, Verdict: VerdictInsufficient}
	if b.N < BaselineSampleSize || c.N < NewVersionSampleSize {
		return out
	}
	out.Z = mannWhitneyZ(baseline.Values, current.Values)
	switch {
	case math.Abs(out.Z) <= NoiseSigma:
		out.Verdict = VerdictSteady
	case out.Z > 0:
		out.Verdict = VerdictRegressed
	default:
		out.Verdict = VerdictImproved
	}
	return out
}

// mannWhitneyZ is the two-sided Mann-Whitney U statistic under the normal
// approximation, signed so the caller can tell direction, with ties given
// average ranks and the variance corrected for them.
//
// Rank-based by design: one runaway measurement moves the statistic by a single
// rank rather than by its magnitude, which is the property the measured long
// tail demands.
func mannWhitneyZ(base, cur []float64) float64 {
	n1, n2 := len(base), len(cur)
	if n1 == 0 || n2 == 0 {
		return 0
	}
	type entry struct {
		v   float64
		cur bool
	}
	merged := make([]entry, 0, n1+n2)
	for _, v := range base {
		merged = append(merged, entry{v, false})
	}
	for _, v := range cur {
		merged = append(merged, entry{v, true})
	}
	sort.Slice(merged, func(i, j int) bool { return merged[i].v < merged[j].v })

	ranks := make([]float64, len(merged))
	var tieCorrection float64
	for i := 0; i < len(merged); {
		j := i
		for j+1 < len(merged) && merged[j+1].v == merged[i].v {
			j++
		}
		avg := float64(i+j)/2 + 1
		for k := i; k <= j; k++ {
			ranks[k] = avg
		}
		if t := float64(j - i + 1); t > 1 {
			tieCorrection += t*t*t - t
		}
		i = j + 1
	}

	var rankSumCur float64
	for i, e := range merged {
		if e.cur {
			rankSumCur += ranks[i]
		}
	}
	nc, nb := float64(n2), float64(n1)
	u := rankSumCur - nc*(nc+1)/2
	mu := nb * nc / 2
	n := nb + nc
	variance := nb * nc / 12 * (n + 1 - tieCorrection/(n*(n-1)))
	if variance <= 0 {
		return 0
	}
	return (u - mu) / math.Sqrt(variance)
}

// quantile is the inclusive linear-interpolation definition, matching the
// method the calibration script used so the Go implementation and the recorded
// simulation numbers describe the same statistic.
func quantile(sorted []float64, p float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if n == 1 {
		return sorted[0]
	}
	pos := p * float64(n-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	if lo == hi {
		return sorted[lo]
	}
	return sorted[lo] + (pos-float64(lo))*(sorted[hi]-sorted[lo])
}
