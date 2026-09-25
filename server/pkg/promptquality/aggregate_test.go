package promptquality

import (
	"testing"

	"github.com/multica-ai/multica/server/pkg/promptdiscipline"
	"github.com/multica-ai/multica/server/pkg/taskfailure"
)

func run(taskID, issueID, status string, opts ...func(*Run)) Run {
	r := Run{TaskID: taskID, IssueID: issueID, Status: status, Attempt: 1, UsageMeasured: true}
	for _, o := range opts {
		o(&r)
	}
	return r
}

func tokens(n int64) func(*Run) { return func(r *Run) { r.RunTokens = n } }
func attempt(n int) func(*Run)  { return func(r *Run) { r.Attempt = n } }
func reason(s taskfailure.Reason) func(*Run) {
	return func(r *Run) { r.FailureReason = string(s) }
}
func unmeasuredUsage(r *Run) { r.UsageMeasured = false; r.RunTokens = 0 }

// ---------------------------------------------------------------- D1

func TestAggregateRunTokensMedianIgnoresUnmeasuredRuns(t *testing.T) {
	got := Aggregate(Input{Runs: []Run{
		run("t1", "i1", "completed", tokens(100)),
		run("t2", "i1", "completed", tokens(300)),
		run("t3", "i1", "completed", unmeasuredUsage),
	}})
	if got.RunTokensMedian == nil {
		t.Fatal("two measured runs must produce a median")
	}
	// A run whose usage was never reported must not be read as 0 tokens: with
	// it the median would be 100, which understates the cost of every version
	// whose daemon dropped a usage report.
	if *got.RunTokensMedian != 200 {
		t.Errorf("median = %d, want 200 (measured 100 and 300 only)", *got.RunTokensMedian)
	}
}

func TestAggregateRunTokensMedianIsNilWhenNothingMeasured(t *testing.T) {
	got := Aggregate(Input{Runs: []Run{
		run("t1", "i1", "completed", unmeasuredUsage),
	}})
	if got.RunTokensMedian != nil {
		t.Errorf("median = %v, want nil — no usage was reported, which is not zero cost", *got.RunTokensMedian)
	}
	if got.FinishedRuns != 1 {
		t.Errorf("FinishedRuns = %d, want 1: the run still finished", got.FinishedRuns)
	}
}

// ---------------------------------------------------------------- D2

func TestAggregateDisciplineMedianCountsOnlyCoveredRuns(t *testing.T) {
	got := Aggregate(Input{
		Runs: []Run{
			run("t1", "i1", "completed"),
			run("t2", "i1", "completed"),
			run("t3", "i1", "completed"),
		},
		Discipline: map[string]promptdiscipline.Result{
			"t1": {Covered: true, Score: 100},
			"t2": {Covered: true, Score: 80},
			// t3 made no inspectable command — unmeasured, not perfect.
			"t3": {Covered: false},
		},
	})
	if got.DisciplineCoveredRuns != 2 {
		t.Errorf("DisciplineCoveredRuns = %d, want 2", got.DisciplineCoveredRuns)
	}
	if got.DisciplineScoreMedian == nil || *got.DisciplineScoreMedian != 90 {
		t.Errorf("median = %v, want 90 (100 and 80); an uncovered run must not be scored 100", got.DisciplineScoreMedian)
	}
}

func TestAggregateDisciplineMedianIsNilWithNoCoverage(t *testing.T) {
	got := Aggregate(Input{
		Runs:       []Run{run("t1", "i1", "completed")},
		Discipline: map[string]promptdiscipline.Result{"t1": {Covered: false}},
	})
	if got.DisciplineScoreMedian != nil {
		t.Errorf("median = %v, want nil", *got.DisciplineScoreMedian)
	}
	if got.DisciplineCoveredRuns != 0 {
		t.Errorf("DisciplineCoveredRuns = %d, want 0", got.DisciplineCoveredRuns)
	}
}

// Deductions are flattened with their task id so the drill-down can go back to
// the transcript, and they never carry command text.
func TestAggregateFlattensDeductionsWithTaskID(t *testing.T) {
	got := Aggregate(Input{
		Runs: []Run{run("t1", "i1", "completed")},
		Discipline: map[string]promptdiscipline.Result{
			"t1": {Covered: true, Score: 75, Deductions: []promptdiscipline.Deduction{
				{Rule: promptdiscipline.RuleBareGitStash, Points: 20, Seq: 4, Detail: "x"},
			}},
		},
	})
	if len(got.Deductions) != 1 {
		t.Fatalf("Deductions = %+v, want one", got.Deductions)
	}
	if got.Deductions[0].TaskID != "t1" || got.Deductions[0].Seq != 4 {
		t.Errorf("deduction lost its pointer back to the transcript: %+v", got.Deductions[0])
	}
}

// ---------------------------------------------------------------- D4

func TestAggregateToolResultsPassThroughUnmeasuredAsZeroMeasured(t *testing.T) {
	got := Aggregate(Input{
		Runs:        []Run{run("t1", "i1", "completed")},
		ToolResults: ToolResultCounts{Measured: 0, Errored: 0},
	})
	// measured = 0 is the "this bucket predates the is_error column" state.
	// The aggregate must carry it through untouched so the read path can say
	// "no data" instead of "0% failure" (T5).
	if got.ToolResultsMeasured != 0 || got.ToolResultsError != 0 {
		t.Errorf("tool results = %d/%d, want 0/0", got.ToolResultsError, got.ToolResultsMeasured)
	}
}

// ---------------------------------------------------------------- D5

func TestAggregateRetryCounts(t *testing.T) {
	got := Aggregate(Input{Runs: []Run{
		run("t1", "i1", "completed", attempt(1)),
		run("t2", "i1", "completed", attempt(3)),
		run("t3", "i1", "failed", attempt(2)),
	}})
	if got.AttemptTotal != 6 {
		t.Errorf("AttemptTotal = %d, want 6", got.AttemptTotal)
	}
	if got.RetriedRuns != 2 {
		t.Errorf("RetriedRuns = %d, want 2 (attempt > 1)", got.RetriedRuns)
	}
}

// ---------------------------------------------------------------- D6

// The acceptance criterion names ReasonRuntimeOffline explicitly: a run that
// failed because the runtime went offline must land in excluded_failed_runs,
// must not appear in failure_reason_counts, and must not move the
// prompt-attributable numerator.
func TestAggregateSplitsEnvironmentFailuresOutOfPromptAttribution(t *testing.T) {
	got := Aggregate(Input{Runs: []Run{
		run("t1", "i1", "failed", reason(taskfailure.ReasonRuntimeOffline)),
		run("t2", "i1", "failed", reason(taskfailure.ReasonAgentProviderServerError)),
		run("t3", "i1", "failed", reason(taskfailure.ReasonIterationLimit)),
		run("t4", "i1", "completed"),
	}})
	if got.ExcludedFailedRuns != 2 {
		t.Errorf("ExcludedFailedRuns = %d, want 2", got.ExcludedFailedRuns)
	}
	if got.AttributableFailedRuns != 1 {
		t.Errorf("AttributableFailedRuns = %d, want 1", got.AttributableFailedRuns)
	}
	if _, present := got.FailureReasonCounts[string(taskfailure.ReasonRuntimeOffline)]; present {
		t.Errorf("runtime_offline leaked into the prompt-attributable reason breakdown: %v", got.FailureReasonCounts)
	}
	if got.FailureReasonCounts[string(taskfailure.ReasonIterationLimit)] != 1 {
		t.Errorf("FailureReasonCounts = %v, want iteration_limit: 1", got.FailureReasonCounts)
	}
}

// A failed run whose reason was never classified is attributable and countable
// under an explicit bucket — dropping it would shrink the denominator and make
// the prompt look better than the data says.
func TestAggregateUnclassifiedFailureStaysAttributable(t *testing.T) {
	got := Aggregate(Input{Runs: []Run{run("t1", "i1", "failed")}})
	if got.AttributableFailedRuns != 1 {
		t.Errorf("AttributableFailedRuns = %d, want 1", got.AttributableFailedRuns)
	}
	if got.FailureReasonCounts[ReasonUnclassified] != 1 {
		t.Errorf("FailureReasonCounts = %v, want %s: 1", got.FailureReasonCounts, ReasonUnclassified)
	}
}

// Cancelled runs are terminal but are not failures: a human or a supersede
// stopped them, which says nothing about the prompt.
func TestAggregateCancelledRunIsNeitherAttributableNorExcluded(t *testing.T) {
	got := Aggregate(Input{Runs: []Run{run("t1", "i1", "cancelled")}})
	if got.AttributableFailedRuns != 0 || got.ExcludedFailedRuns != 0 {
		t.Errorf("cancelled run counted as a failure: %+v", got)
	}
	if got.FinishedRuns != 1 {
		t.Errorf("FinishedRuns = %d, want 1", got.FinishedRuns)
	}
}

// ---------------------------------------------------------------- D7

func TestAggregateFirstPassCountsOnlyReviewedIssues(t *testing.T) {
	got := Aggregate(Input{
		Runs: []Run{
			run("t1", "i1", "completed"),
			run("t2", "i2", "completed"),
			run("t3", "i3", "completed"),
		},
		Reviews: []IssueReview{
			{IssueID: "i1", EnteredReview: 1, SentBack: 0},
			{IssueID: "i2", EnteredReview: 2, SentBack: 1},
			// i3 never reached review — not judged yet, so it is in neither count.
		},
	})
	if got.ReviewedIssues != 2 {
		t.Errorf("ReviewedIssues = %d, want 2 — an issue that never reached review is not a verdict", got.ReviewedIssues)
	}
	if got.FirstPassIssues != 1 {
		t.Errorf("FirstPassIssues = %d, want 1", got.FirstPassIssues)
	}
}

func TestAggregateDeduplicatesIssuesAcrossRuns(t *testing.T) {
	got := Aggregate(Input{
		Runs: []Run{
			run("t1", "i1", "completed"),
			run("t2", "i1", "completed"),
		},
		Reviews: []IssueReview{{IssueID: "i1", EnteredReview: 1}},
	})
	if got.ReviewedIssues != 1 {
		t.Errorf("ReviewedIssues = %d, want 1: two runs on one issue are one review outcome", got.ReviewedIssues)
	}
}

// ---------------------------------------------------------------- shared

func TestAggregateEmptyBucketMeasuresNothing(t *testing.T) {
	got := Aggregate(Input{})
	if got.FinishedRuns != 0 {
		t.Errorf("FinishedRuns = %d, want 0", got.FinishedRuns)
	}
	if got.RunTokensMedian != nil || got.DisciplineScoreMedian != nil {
		t.Errorf("an empty bucket must produce no medians, got %+v", got)
	}
	if got.FailureReasonCounts == nil {
		t.Error("FailureReasonCounts must be an empty map, not nil — it is serialised into a NOT NULL jsonb column")
	}
}

// The injected-token figure is a property of the version and travels through
// untouched, including its absence: a scope whose content could not be read
// must persist NULL, never 0.
func TestAggregatePassesInjectedTokensThroughIncludingAbsence(t *testing.T) {
	n := int64(4200)
	if got := Aggregate(Input{InjectedTokens: &n}); got.InjectedTokens == nil || *got.InjectedTokens != 4200 {
		t.Errorf("InjectedTokens = %v, want 4200", got.InjectedTokens)
	}
	if got := Aggregate(Input{}); got.InjectedTokens != nil {
		t.Errorf("InjectedTokens = %v, want nil", *got.InjectedTokens)
	}
}
