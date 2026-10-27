package promptquality

// The presentation half of the package: it is the only place a division
// happens, and it is where the three states the dashboard can render are
// decided (RUYI-184 T5/T7).
//
// Aggregate deliberately stores numerators and denominators and never divides,
// so that "not measured" survives all the way to the API. This file spends
// that separation: each dimension is turned into a Measure that is either a
// value, an explicit "no data", or an explicit "insufficient sample" — never a
// zero standing in for the last two. The frontend renders the state; it has no
// fallback path that would invent a 0%.
//
// D3 (rule perplexity) is not here. It has its own table and its own scoring
// pipeline (pkg/promptperplexity), because it is a model judgement about the
// prompt text rather than a fold of the run stream.

import "github.com/multica-ai/multica/server/pkg/promptdiscipline"

// State is what the card renders.
type State string

const (
	// StateOK: the dimension has a real value.
	StateOK State = "ok"
	// StateNoData: nothing was measured. The card shows a reason phrase, not
	// a number.
	StateNoData State = "no_data"
	// StateInsufficientSample: something was measured, but too little of it to
	// state a rate. The card shows a badge and no directional wording.
	StateInsufficientSample State = "insufficient_sample"
)

// Unit says how a measured value must be read, and it is the whole contract
// between this package and the cards.
//
// It exists because the unit used to be inferred on the far side: the frontend
// kept a set of dimension names it considered ratios, and D2 — a 0..100
// deduction score — was in it, so a median of 90 was formatted as 9,000%. A set
// of names cannot be checked against the numbers this file emits, and nothing
// fails when the two drift. Travelling with the value, the unit is decided once
// here, next to the arithmetic that produced it.
type Unit string

const (
	// UnitRatio: a fraction in 0..1 produced by rate(). Rendered as a
	// percentage.
	UnitRatio Unit = "ratio"
	// UnitCount: an absolute tally or a token figure. Rendered as a plain
	// number.
	UnitCount Unit = "count"
	// UnitScore: a 0..100 point score (promptdiscipline's deduction scale).
	// Rendered as points out of the maximum, never as a percentage of one.
	UnitScore Unit = "score"
)

// ScoreMax is the top of the UnitScore scale. It is promptdiscipline's own
// starting score, referenced rather than restated: D2 is that package's number,
// and a literal 100 here would be a second definition of the same scale — the
// class of drift this Unit contract exists to remove. Published with the
// measure so the card need not hold a third copy.
const ScoreMax = promptdiscipline.MaxScore

// MinSampleRuns is the declared threshold below which a rate is not published.
//
// Ten is chosen so a single outcome cannot move the displayed rate by more than
// ten points. Below that a "25% retry rate" is one retried run out of four, and
// reading a trend off it is reading noise. The number is returned in the
// response alongside the sample so the badge can state the rule it applied
// rather than merely hiding the value.
const MinSampleRuns = 10

// MinSampleIssues is the same floor for the one dimension counted per issue
// rather than per run (D7, first-pass rate). It is lower because the unit is
// coarser: a bucket with fifty runs routinely covers a handful of issues, and
// holding issue-counted D7 to the run floor would leave it permanently
// unreported on exactly the buckets where the other six cards are dense.
const MinSampleIssues = 5

// Reason phrases. They are keys, not sentences: the four locale files own the
// wording, and this package must not decide what language the dashboard speaks.
const (
	ReasonNoFinishedRuns   = "no_finished_runs"
	ReasonNotInstrumented  = "not_instrumented"
	ReasonNoCoveredRuns    = "no_covered_runs"
	ReasonNoVersionContent = "no_version_content"
	ReasonNoReviewedIssues = "no_reviewed_issues"
	ReasonBelowSampleFloor = "below_sample_floor"
)

// Measure is one dimension as the API returns it.
//
// Value is a pointer and Denominator is a pointer for the same reason the
// Result fields are: this struct is serialised straight into a JSON response,
// and a zero here would be indistinguishable from a measured zero.
type Measure struct {
	State State `json:"state"`

	// Unit is how Value must be read. Always set, including on the two states
	// that carry no value: a card renders its unit-dependent wording before it
	// knows whether a number arrived.
	Unit Unit `json:"unit"`

	// Value is the rate, the count or the score. Nil unless State is ok.
	Value *float64 `json:"value"`

	// ScoreMax is the top of the scale for UnitScore, so the card can print
	// "90 / 100". Zero for every other unit.
	ScoreMax int `json:"score_max,omitempty"`

	// Numerator and Denominator back the value so the UI can show "4 / 40"
	// rather than only a percentage. Both nil unless the value is a ratio of
	// the two — a count has no denominator, and pointing one at the numerator
	// made the D6 card print "2 / 2".
	Numerator   *int64 `json:"numerator"`
	Denominator *int64 `json:"denominator"`

	// Sample and Threshold explain an insufficient_sample badge, and Sample is
	// also what an ok card's "over N runs" line reads. Sample is the
	// dimension's own denominator, which is not always the bucket's run count,
	// and it is 0 when the value was not counted over a run set the card could
	// name — D1's static token estimate and its median of medians. The card
	// omits the line at 0 rather than claiming a measured value rests on zero
	// runs.
	Sample    int64 `json:"sample"`
	Threshold int   `json:"threshold"`

	// Reason names why the state is not ok. Empty when it is.
	Reason string `json:"reason,omitempty"`

	// Excluded is D6-only: failures dropped from the attribution denominator
	// because their reason was environmental. Surfaced so the card can say
	// what it left out rather than silently shrinking.
	Excluded int `json:"excluded,omitempty"`
}

// Presentation is the seven-card payload for one bucket.
type Presentation struct {
	InjectedTokens     Measure `json:"injected_tokens"`
	RunTokensMedian    Measure `json:"run_tokens_median"`
	Discipline         Measure `json:"discipline"`
	ToolFailureRate    Measure `json:"tool_failure_rate"`
	RetryRate          Measure `json:"retry_rate"`
	FailureAttribution Measure `json:"failure_attribution"`
	FirstPassRate      Measure `json:"first_pass_rate"`
}

// ByKey exposes the same measures keyed by their JSON name, for tests and for
// callers that iterate the grid rather than naming each card.
func (p Presentation) ByKey() map[string]Measure {
	return map[string]Measure{
		"injected_tokens":     p.InjectedTokens,
		"run_tokens_median":   p.RunTokensMedian,
		"discipline":          p.Discipline,
		"tool_failure_rate":   p.ToolFailureRate,
		"retry_rate":          p.RetryRate,
		"failure_attribution": p.FailureAttribution,
		"first_pass_rate":     p.FirstPassRate,
	}
}

// Present turns one aggregated bucket into the seven cards.
func Present(r Result) Presentation {
	return Presentation{
		InjectedTokens:  count(r.InjectedTokens, 0, ReasonNoVersionContent),
		RunTokensMedian: count(r.RunTokensMedian, 0, ReasonNoFinishedRuns),
		// D2 is the median of promptdiscipline's 0..100 deduction score, not a
		// share of compliant runs. It declares UnitScore so the card prints
		// "90 / 100" instead of dividing it by nothing and calling it 9,000%.
		Discipline: score(int64PtrFromInt(r.DisciplineScoreMedian), int64(r.DisciplineCoveredRuns), ReasonNoCoveredRuns),
		// D4's denominator is measured tool results, not runs: the whole point
		// of the is_error column is that a run before it was added contributes
		// nothing here instead of contributing a success.
		ToolFailureRate:    rate(r.ToolResultsError, r.ToolResultsMeasured, MinSampleRuns, ReasonNotInstrumented),
		RetryRate:          rate(int64(r.RetriedRuns), int64(r.FinishedRuns), MinSampleRuns, ReasonNoFinishedRuns),
		FailureAttribution: failureAttribution(r),
		// D7 is counted per issue, so it is gated on the issue floor.
		FirstPassRate: rate(int64(r.FirstPassIssues), int64(r.ReviewedIssues), MinSampleIssues, ReasonNoReviewedIssues),
	}
}

// failureAttribution is D6, and it is a count rather than a rate: the card
// shows how the prompt-attributable failures break down by reason, over a
// denominator that environment and provider failures were already removed from
// (taskfailure.PromptAttributable, applied in Aggregate).
//
// Zero attributable failures is a measurement — the runs finished and none of
// them failed for a reason the prompt could own — so the only "no data" state
// is a bucket with no finished runs at all. Excluded travels with it so the
// card can say what it dropped instead of appearing to have seen fewer
// failures than the run list shows.
func failureAttribution(r Result) Measure {
	if r.FinishedRuns == 0 {
		return Measure{State: StateNoData, Unit: UnitCount, Threshold: MinSampleRuns, Reason: ReasonNoFinishedRuns}
	}
	m := count(int64Ptr(int64(r.AttributableFailedRuns)), int64(r.FinishedRuns), ReasonNoFinishedRuns)
	m.Excluded = r.ExcludedFailedRuns
	return m
}

// rate is the only division in the package, and it refuses twice before doing
// it: once when the denominator is zero (nothing measured) and once when it is
// below the declared floor (measured, but not enough to state).
//
// floor is passed in rather than read from a package constant because the
// dimensions do not all count the same unit; see MinSampleIssues.
func rate(num, den int64, floor int, emptyReason string) Measure {
	if den == 0 {
		return Measure{State: StateNoData, Unit: UnitRatio, Threshold: floor, Reason: emptyReason}
	}
	if den < int64(floor) {
		return Measure{
			State:     StateInsufficientSample,
			Unit:      UnitRatio,
			Sample:    den,
			Threshold: floor,
			Reason:    ReasonBelowSampleFloor,
		}
	}
	v := float64(num) / float64(den)
	n, d := num, den
	return Measure{
		State:       StateOK,
		Unit:        UnitRatio,
		Value:       &v,
		Numerator:   &n,
		Denominator: &d,
		Sample:      den,
		Threshold:   floor,
	}
}

// count carries a measurement that is a tally or a figure rather than a ratio.
// There is no sample floor: a median of nine runs is still that median, and
// hiding it would be a different lie than publishing a rate off four.
//
// sample is the run set the figure was counted over, or 0 when there is none
// the card could name — see Measure.Sample. Numerator and Denominator stay nil
// either way: a count is not a ratio, and filling them made the D6 card print
// its value over itself.
func count(v *int64, sample int64, emptyReason string) Measure {
	if v == nil {
		return Measure{State: StateNoData, Unit: UnitCount, Threshold: MinSampleRuns, Reason: emptyReason}
	}
	f := float64(*v)
	return Measure{State: StateOK, Unit: UnitCount, Value: &f, Sample: sample, Threshold: MinSampleRuns}
}

// score carries a 0..100 point score. Same no-floor reasoning as count; the
// separate constructor exists so ScoreMax travels with it and the card never
// has to guess the top of the scale.
func score(v *int64, sample int64, emptyReason string) Measure {
	m := count(v, sample, emptyReason)
	m.Unit = UnitScore
	m.ScoreMax = ScoreMax
	return m
}

func int64Ptr(v int64) *int64 { return &v }

func int64PtrFromInt(v *int) *int64 {
	if v == nil {
		return nil
	}
	out := int64(*v)
	return &out
}
