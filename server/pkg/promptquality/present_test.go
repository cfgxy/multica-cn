package promptquality

import (
	"testing"

	"github.com/multica-ai/multica/server/pkg/promptdiscipline"
)

func full() Result {
	tok := int64(1200)
	med := int64(900)
	score := 88
	return Result{
		FinishedRuns:           10,
		InjectedTokens:         &tok,
		RunTokensMedian:        &med,
		DisciplineScoreMedian:  &score,
		DisciplineCoveredRuns:  9,
		ToolResultsMeasured:    40,
		ToolResultsError:       4,
		AttemptTotal:           13,
		RetriedRuns:            3,
		AttributableFailedRuns: 2,
		ExcludedFailedRuns:     1,
		FailureReasonCounts:    map[string]int{"iteration_limit": 2},
		FirstPassIssues:        6,
		ReviewedIssues:         8,
	}
}

func TestPresentReportsEveryDimensionWhenEverythingWasMeasured(t *testing.T) {
	got := Present(full())
	for name, m := range got.ByKey() {
		if m.State != StateOK {
			t.Errorf("%s state = %q, want ok", name, m.State)
		}
	}
}

// T5: a bucket whose runs predate the is_error column has nothing to divide.
// Reporting 0% would say "this prompt caused no tool failures", which is a
// claim the data cannot support.
func TestPresentReportsNoDataRatherThanZeroForAnUnmeasuredDimension(t *testing.T) {
	in := full()
	in.ToolResultsMeasured = 0
	in.ToolResultsError = 0

	got := Present(in)
	if got.ToolFailureRate.State != StateNoData {
		t.Fatalf("state = %q, want no_data", got.ToolFailureRate.State)
	}
	if got.ToolFailureRate.Value != nil {
		t.Errorf("Value = %v, want nil — an unmeasured ratio has no value at all", *got.ToolFailureRate.Value)
	}
	if got.ToolFailureRate.Reason == "" {
		t.Error("no_data without a reason phrase; the card would render an unexplained blank")
	}
}

// T7: a real but tiny sample is a third state. It is not "no data" (something
// was measured) and must not be rendered as a rate the user could act on.
func TestPresentMarksATinySampleInsteadOfPublishingItsRate(t *testing.T) {
	in := full()
	in.FinishedRuns = MinSampleRuns - 1
	in.AttemptTotal = 4
	in.RetriedRuns = 1

	got := Present(in)
	if got.RetryRate.State != StateInsufficientSample {
		t.Fatalf("state = %q, want insufficient_sample", got.RetryRate.State)
	}
	if got.RetryRate.Value != nil {
		t.Error("a below-threshold sample still published a rate")
	}
	if got.RetryRate.Sample != int64(MinSampleRuns-1) || got.RetryRate.Threshold != MinSampleRuns {
		t.Errorf("sample/threshold = %d/%d, want %d/%d — the badge needs both to explain itself",
			got.RetryRate.Sample, got.RetryRate.Threshold, MinSampleRuns-1, MinSampleRuns)
	}
}

// The sample that gates a dimension is that dimension's own denominator, not
// the bucket's run count. A bucket with plenty of runs but three measured tool
// results cannot report a tool failure rate.
func TestPresentGatesEachDimensionOnItsOwnDenominator(t *testing.T) {
	in := full()
	in.ToolResultsMeasured = 3
	in.ToolResultsError = 1

	got := Present(in)
	if got.ToolFailureRate.State != StateInsufficientSample {
		t.Errorf("tool failure state = %q, want insufficient_sample", got.ToolFailureRate.State)
	}
	if got.RetryRate.State != StateOK {
		t.Errorf("retry state = %q, want ok — its own denominator was fine", got.RetryRate.State)
	}
}

func TestPresentRatesAreNumeratorOverDenominatorNotOverFinishedRuns(t *testing.T) {
	got := Present(full())
	if v := *got.ToolFailureRate.Value; v != 0.1 {
		t.Errorf("tool failure rate = %v, want 4/40", v)
	}
	if v := *got.FirstPassRate.Value; v != 0.75 {
		t.Errorf("first-pass rate = %v, want 6/8 — reviewed issues, not finished runs", v)
	}
	if v := *got.RetryRate.Value; v != 0.3 {
		t.Errorf("retry rate = %v, want 3/10", v)
	}
}

// D6's denominator is the attributable side only. Environment failures were
// already excluded by Aggregate; including them here would quietly undo that.
func TestPresentFailureAttributionExcludesEnvironmentFailures(t *testing.T) {
	in := full()
	in.AttributableFailedRuns = 2
	in.ExcludedFailedRuns = 7

	got := Present(in)
	if got.FailureAttribution.Value == nil || *got.FailureAttribution.Value != 2 {
		t.Fatalf("value = %v, want the 2 attributable failures — the 7 environmental ones must not come back in",
			got.FailureAttribution.Value)
	}
	if got.FailureAttribution.Excluded != 7 {
		t.Errorf("Excluded = %d, want 7 surfaced so the card can say what it dropped", got.FailureAttribution.Excluded)
	}
}

// D6 with no attributable failure is a measured result, not a gap: the runs
// finished and none of them failed for a prompt-attributable reason.
func TestPresentFailureAttributionWithNoFailuresIsMeasured(t *testing.T) {
	in := full()
	in.AttributableFailedRuns = 0
	in.ExcludedFailedRuns = 0
	in.FailureReasonCounts = map[string]int{}

	got := Present(in)
	if got.FailureAttribution.State != StateOK {
		t.Errorf("state = %q, want ok — zero attributable failures is a measurement", got.FailureAttribution.State)
	}
}

func TestPresentInjectedTokensIsNoDataWhenTheVersionContentWasUnreadable(t *testing.T) {
	in := full()
	in.InjectedTokens = nil

	got := Present(in)
	if got.InjectedTokens.State != StateNoData {
		t.Errorf("state = %q, want no_data", got.InjectedTokens.State)
	}
}

func TestPresentDisciplineIsNoDataWhenNoRunIssuedAnInspectableCommand(t *testing.T) {
	in := full()
	in.DisciplineCoveredRuns = 0
	in.DisciplineScoreMedian = nil

	got := Present(in)
	if got.Discipline.State != StateNoData {
		t.Errorf("state = %q, want no_data", got.Discipline.State)
	}
}

func TestPresentAnEmptyBucketMeasuresNothingAnywhere(t *testing.T) {
	got := Present(Result{FailureReasonCounts: map[string]int{}})
	for name, m := range got.ByKey() {
		if m.State == StateOK {
			t.Errorf("%s reported a measurement for a bucket with no runs", name)
		}
		if m.Value != nil {
			t.Errorf("%s published a value for a bucket with no runs", name)
		}
	}
}

func TestByKeyCoversEveryDimensionTheCardGridRenders(t *testing.T) {
	keys := Present(full()).ByKey()
	for _, want := range []string{
		"injected_tokens", "run_tokens_median", "discipline",
		"tool_failure_rate", "retry_rate", "failure_attribution", "first_pass_rate",
	} {
		if _, ok := keys[want]; !ok {
			t.Errorf("ByKey is missing %q; the frontend grid would render an empty slot", want)
		}
	}
}

// The unit contract (RUYI-184 返工).
//
// These four tests exist because the frontend used to decide a dimension's
// unit from a hand-kept set of dimension names, and D2 — a 0..100 deduction
// score — sat in the set meant for 0..1 ratios, so a median of 90 rendered as
// 9,000%. The unit now travels with the measure, and these assertions pin what
// each dimension declares. Changing one of them changes what the card prints.

func TestPresentDisciplineDeclaresAHundredPointScoreNotARatio(t *testing.T) {
	got := Present(full())
	if got.Discipline.Unit != UnitScore {
		t.Errorf("discipline unit = %q, want %q — a 0..100 deduction score read as a ratio prints as 8,800%%",
			got.Discipline.Unit, UnitScore)
	}
	if v := *got.Discipline.Value; v != 88 {
		t.Errorf("discipline value = %v, want 88 — the score is published as points, not divided by 100", v)
	}
	// The scale the card prints against is promptdiscipline's own starting
	// score. Asserting against that constant rather than a literal 100 is the
	// point: a second copy of the scale is what let D2 drift in the first place,
	// and this fails if one is ever reintroduced here.
	if got.Discipline.ScoreMax != promptdiscipline.MaxScore {
		t.Errorf("discipline score_max = %d, want promptdiscipline.MaxScore (%d)",
			got.Discipline.ScoreMax, promptdiscipline.MaxScore)
	}
}

func TestPresentRatesDeclareTheRatioUnit(t *testing.T) {
	got := Present(full())
	for name, m := range map[string]Measure{
		"tool_failure_rate": got.ToolFailureRate,
		"retry_rate":        got.RetryRate,
		"first_pass_rate":   got.FirstPassRate,
	} {
		if m.Unit != UnitRatio {
			t.Errorf("%s unit = %q, want %q", name, m.Unit, UnitRatio)
		}
	}
}

func TestPresentTokenMeasuresDeclareTheCountUnit(t *testing.T) {
	got := Present(full())
	for name, m := range map[string]Measure{
		"injected_tokens":   got.InjectedTokens,
		"run_tokens_median": got.RunTokensMedian,
	} {
		if m.Unit != UnitCount {
			t.Errorf("%s unit = %q, want %q — a token count read as a ratio prints as 120,000%%",
				name, m.Unit, UnitCount)
		}
	}
}

// D6 is a count of attributable failures. It used to set Numerator and
// Denominator to the same number, so the card printed "2 / 2" — a ratio of a
// value against itself — on top of rendering the count as a percentage.
func TestPresentFailureAttributionIsACountWithNoSelfReferentialRatio(t *testing.T) {
	in := full()
	in.AttributableFailedRuns = 2
	in.ExcludedFailedRuns = 7

	got := Present(in)
	if got.FailureAttribution.Unit != UnitCount {
		t.Errorf("unit = %q, want %q — 2 attributable failures printed as 200%%",
			got.FailureAttribution.Unit, UnitCount)
	}
	if v := *got.FailureAttribution.Value; v != 2 {
		t.Errorf("value = %v, want 2", v)
	}
	if got.FailureAttribution.Denominator != nil {
		t.Errorf("denominator = %d, want nil — a count has no denominator, and pointing it at the numerator made the card print \"2 / 2\"",
			*got.FailureAttribution.Denominator)
	}
	if got.FailureAttribution.Numerator != nil {
		t.Errorf("numerator = %d, want nil — the ratio line must not render for a count",
			*got.FailureAttribution.Numerator)
	}
	if got.FailureAttribution.Sample != int64(in.FinishedRuns) {
		t.Errorf("sample = %d, want %d finished runs — this is what the card counts the failures over",
			got.FailureAttribution.Sample, in.FinishedRuns)
	}
	if got.FailureAttribution.Excluded != 7 {
		t.Errorf("Excluded = %d, want 7", got.FailureAttribution.Excluded)
	}
}

// Sample is what the card's "over N runs" line reads. An ok measure that has no
// run sample at all must report 0 so the card can omit the line rather than
// print "over 0 runs" under a measured value.
func TestPresentOkMeasuresCarryEitherARatioOrANonZeroSample(t *testing.T) {
	got := Present(full())
	for name, m := range got.ByKey() {
		if m.State != StateOK {
			t.Fatalf("%s state = %q, want ok", name, m.State)
		}
		hasRatio := m.Numerator != nil && m.Denominator != nil
		if !hasRatio && m.Sample == 0 && name != "injected_tokens" && name != "run_tokens_median" {
			t.Errorf("%s has neither a ratio nor a sample; the card would print \"over 0 runs\" under a real value", name)
		}
	}
	// D1's two cards are the deliberate exception: a static token estimate and
	// a weighted median of medians are not counted over a run set the card
	// could name, so they declare no sample and the line is not rendered.
	if got.InjectedTokens.Sample != 0 || got.RunTokensMedian.Sample != 0 {
		t.Errorf("injected/run token samples = %d/%d, want 0/0 — a non-zero here would put a run count the number was not measured over on the card",
			got.InjectedTokens.Sample, got.RunTokensMedian.Sample)
	}
}
