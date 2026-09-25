package promptquality

import "testing"

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
	if got.FailureAttribution.Denominator == nil || *got.FailureAttribution.Denominator != 2 {
		t.Fatalf("denominator = %v, want the 2 attributable failures", got.FailureAttribution.Denominator)
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
