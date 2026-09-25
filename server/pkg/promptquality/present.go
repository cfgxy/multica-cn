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

	// Value is the rate or the absolute figure. Nil unless State is ok.
	Value *float64 `json:"value"`

	// Numerator and Denominator back the value so the UI can show "4 / 40"
	// rather than only a percentage. Nil when nothing was measured.
	Numerator   *int64 `json:"numerator"`
	Denominator *int64 `json:"denominator"`

	// Sample and Threshold explain an insufficient_sample badge. Sample is the
	// dimension's own denominator, which is not always the bucket's run count.
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
		InjectedTokens:  absolute(r.InjectedTokens, ReasonNoVersionContent),
		RunTokensMedian: absolute(r.RunTokensMedian, ReasonNoFinishedRuns),
		Discipline: func() Measure {
			m := absolute(int64PtrFromInt(r.DisciplineScoreMedian), ReasonNoCoveredRuns)
			m.Sample = int64(r.DisciplineCoveredRuns)
			return m
		}(),
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
		return Measure{State: StateNoData, Threshold: MinSampleRuns, Reason: ReasonNoFinishedRuns}
	}
	n := int64(r.AttributableFailedRuns)
	v := float64(n)
	return Measure{
		State:       StateOK,
		Value:       &v,
		Numerator:   &n,
		Denominator: &n,
		Sample:      int64(r.FinishedRuns),
		Threshold:   MinSampleRuns,
		Excluded:    r.ExcludedFailedRuns,
	}
}

// rate is the only division in the package, and it refuses twice before doing
// it: once when the denominator is zero (nothing measured) and once when it is
// below the declared floor (measured, but not enough to state).
//
// floor is passed in rather than read from a package constant because the
// dimensions do not all count the same unit; see MinSampleIssues.
func rate(num, den int64, floor int, emptyReason string) Measure {
	if den == 0 {
		return Measure{State: StateNoData, Threshold: floor, Reason: emptyReason}
	}
	if den < int64(floor) {
		return Measure{
			State:     StateInsufficientSample,
			Sample:    den,
			Threshold: floor,
			Reason:    ReasonBelowSampleFloor,
		}
	}
	v := float64(num) / float64(den)
	n, d := num, den
	return Measure{
		State:       StateOK,
		Value:       &v,
		Numerator:   &n,
		Denominator: &d,
		Sample:      den,
		Threshold:   floor,
	}
}

// absolute carries a measurement that is a figure rather than a ratio. There is
// no sample floor: a median of nine runs is still that median, and hiding it
// would be a different lie than publishing a rate off four.
func absolute(v *int64, emptyReason string) Measure {
	if v == nil {
		return Measure{State: StateNoData, Threshold: MinSampleRuns, Reason: emptyReason}
	}
	f := float64(*v)
	n := *v
	return Measure{State: StateOK, Value: &f, Numerator: &n, Threshold: MinSampleRuns}
}

func int64PtrFromInt(v *int) *int64 {
	if v == nil {
		return nil
	}
	out := int64(*v)
	return &out
}
