package promptquality

import "testing"

func day(finished int, over ...func(*Result)) Result {
	r := Result{FinishedRuns: finished, FailureReasonCounts: map[string]int{}}
	for _, o := range over {
		o(&r)
	}
	return r
}

func TestCombineSumsEveryCountedDimension(t *testing.T) {
	a := day(4, func(r *Result) {
		r.ToolResultsMeasured, r.ToolResultsError = 20, 3
		r.AttemptTotal, r.RetriedRuns = 5, 1
		r.AttributableFailedRuns, r.ExcludedFailedRuns = 1, 2
		r.FailureReasonCounts["iteration_limit"] = 1
		r.FirstPassIssues, r.ReviewedIssues = 2, 3
	})
	b := day(6, func(r *Result) {
		r.ToolResultsMeasured, r.ToolResultsError = 10, 1
		r.AttemptTotal, r.RetriedRuns = 8, 2
		r.AttributableFailedRuns = 3
		r.FailureReasonCounts["iteration_limit"] = 2
		r.FailureReasonCounts["tool_error"] = 1
		r.FirstPassIssues, r.ReviewedIssues = 4, 5
	})

	got := Combine([]Result{a, b})
	for _, c := range []struct {
		name string
		got  int
		want int
	}{
		{"finished_runs", got.FinishedRuns, 10},
		{"tool_results_measured", int(got.ToolResultsMeasured), 30},
		{"tool_results_error", int(got.ToolResultsError), 4},
		{"attempt_total", got.AttemptTotal, 13},
		{"retried_runs", got.RetriedRuns, 3},
		{"attributable_failed_runs", got.AttributableFailedRuns, 4},
		{"excluded_failed_runs", got.ExcludedFailedRuns, 2},
		{"first_pass_issues", got.FirstPassIssues, 6},
		{"reviewed_issues", got.ReviewedIssues, 8},
	} {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d", c.name, c.got, c.want)
		}
	}
	if got.FailureReasonCounts["iteration_limit"] != 3 || got.FailureReasonCounts["tool_error"] != 1 {
		t.Errorf("failure reasons = %v, want the two days added per reason", got.FailureReasonCounts)
	}
}

// A window with no days measured nothing; it did not measure zero.
func TestCombineOfNothingMeasuresNothing(t *testing.T) {
	got := Combine(nil)
	if got.RunTokensMedian != nil || got.DisciplineScoreMedian != nil || got.InjectedTokens != nil {
		t.Error("an empty window produced a value")
	}
	if got.FinishedRuns != 0 {
		t.Errorf("FinishedRuns = %d, want 0", got.FinishedRuns)
	}
}

// The window median must lean towards the day that actually carried the runs.
func TestCombineWeightsTheMedianByRunsBehindEachDay(t *testing.T) {
	quiet := day(1, func(r *Result) { v := int64(10_000); r.RunTokensMedian = &v })
	busy := day(99, func(r *Result) { v := int64(900); r.RunTokensMedian = &v })

	got := Combine([]Result{quiet, busy})
	if got.RunTokensMedian == nil {
		t.Fatal("no median for a window with measured days")
	}
	if *got.RunTokensMedian != 900 {
		t.Errorf("median = %d, want 900 — one outlier day must not outweigh 99 runs", *got.RunTokensMedian)
	}
}

// A day whose runs were never measured contributes no median at all, rather
// than contributing a zero that would drag the window down.
func TestCombineIgnoresDaysWithNoMeasuredMedian(t *testing.T) {
	measured := day(5, func(r *Result) { v := int64(700); r.RunTokensMedian = &v })
	unmeasured := day(5)

	got := Combine([]Result{measured, unmeasured})
	if got.RunTokensMedian == nil || *got.RunTokensMedian != 700 {
		t.Errorf("median = %v, want the one day that was measured", got.RunTokensMedian)
	}
}

// Every day of a version injects the same prompt, so D1's static half is that
// number and not a sum over the window.
func TestCombineDoesNotAddInjectedTokensAcrossDays(t *testing.T) {
	v := int64(1200)
	a := day(3, func(r *Result) { n := v; r.InjectedTokens = &n })
	b := day(3, func(r *Result) { n := v; r.InjectedTokens = &n })

	got := Combine([]Result{a, b})
	if got.InjectedTokens == nil || *got.InjectedTokens != v {
		t.Errorf("injected tokens = %v, want %d — the same prompt counted once", got.InjectedTokens, v)
	}
}

// Discipline is weighted by covered runs, which is not the bucket's run count.
func TestCombineWeightsDisciplineByCoveredRunsNotFinishedRuns(t *testing.T) {
	// A day with many runs but only one inspectable command must not decide
	// the window's discipline score.
	noisy := day(100, func(r *Result) {
		s := 20
		r.DisciplineScoreMedian = &s
		r.DisciplineCoveredRuns = 1
	})
	solid := day(10, func(r *Result) {
		s := 90
		r.DisciplineScoreMedian = &s
		r.DisciplineCoveredRuns = 10
	})

	got := Combine([]Result{noisy, solid})
	if got.DisciplineScoreMedian == nil || *got.DisciplineScoreMedian != 90 {
		t.Errorf("discipline = %v, want 90 — weighted by covered runs", got.DisciplineScoreMedian)
	}
	if got.DisciplineCoveredRuns != 11 {
		t.Errorf("covered runs = %d, want 11", got.DisciplineCoveredRuns)
	}
}
