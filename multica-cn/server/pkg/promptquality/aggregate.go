// Package promptquality folds one bucket of finished runs — all runs that a
// single prompt (scope, scope_id, version) was injected into on a single UTC
// day — into the row the quality dashboard reads (RUYI-184).
//
// The one rule the whole package is built around: a ratio cannot say "not
// measured". Every dimension here stores its numerator and its denominator
// separately and never divides. A version whose daemon never reported usage,
// whose runs predate the is_error column, or whose runs issued no inspectable
// command, must reach the dashboard as "no data" — not as 0 tokens, 0% tool
// failures, or a perfect discipline score.
package promptquality

import (
	"sort"

	"github.com/multica-ai/multica/server/pkg/promptdiscipline"
	"github.com/multica-ai/multica/server/pkg/taskfailure"
)

// ReasonUnclassified buckets failed runs whose failure_reason was never
// written. They stay in the D6 denominator — see taskfailure.PromptAttributable
// for why dropping an unknown failure is the unsafe direction — so they need a
// visible key rather than being folded into an existing reason.
const ReasonUnclassified = "unclassified"

// Terminal statuses. Only these three reach a bucket; the SQL filters the rest.
const (
	statusCompleted = "completed"
	statusFailed    = "failed"
	statusCancelled = "cancelled"
)

// Run is one finished run in the bucket.
type Run struct {
	TaskID  string
	IssueID string
	Status  string

	// FailureReason is the raw taskfailure.Reason string, empty when the run
	// did not fail or the reason was never classified.
	FailureReason string

	// Attempt is agent_task_queue.attempt: 1 for a run that was never retried.
	Attempt int

	// UsageMeasured distinguishes "the daemon reported no usage rows" from
	// "the run genuinely burned zero tokens". RunTokens is meaningless when
	// this is false.
	UsageMeasured bool
	RunTokens     int64
}

// ToolResultCounts is D4's pair. Measured counts tool results whose is_error
// is non-NULL; Errored counts those where it is true. Measured == 0 is the
// pre-instrumentation state and is the only correct way to express it.
type ToolResultCounts struct {
	Measured int64
	Errored  int64
}

// IssueReview is D7's per-issue review history, derived from activity_log
// status transitions.
type IssueReview struct {
	IssueID       string
	EnteredReview int
	SentBack      int
}

// Input is everything one bucket needs. The caller fetches each part with its
// own query; folding them is pure so the rules above are testable without a
// database.
type Input struct {
	Runs []Run

	// Discipline is keyed by Run.TaskID. A run missing from the map is treated
	// the same as an uncovered one: unmeasured, not perfect.
	Discipline map[string]promptdiscipline.Result

	ToolResults ToolResultCounts
	Reviews     []IssueReview

	// InjectedTokens is the static half of D1 — the estimated size of the
	// prompt content itself. Nil when the version's content could not be read.
	InjectedTokens *int64
}

// Deduction is a promptdiscipline.Deduction carrying the run it came from, so
// the drill-down can walk back to the transcript. It still carries no command
// text: this value is serialised into a column the dashboard scans in bulk.
type Deduction struct {
	TaskID string `json:"task_id"`
	Rule   string `json:"rule"`
	Points int    `json:"points"`
	Seq    int    `json:"seq"`
}

// Result mirrors the prompt_quality_daily row. Every nilable field is nilable
// because NULL and 0 mean different things in that column.
type Result struct {
	FinishedRuns int

	// D1
	InjectedTokens  *int64
	RunTokensMedian *int64

	// D2
	DisciplineScoreMedian *int
	DisciplineCoveredRuns int
	Deductions            []Deduction

	// D4
	ToolResultsMeasured int64
	ToolResultsError    int64

	// D5
	AttemptTotal int
	RetriedRuns  int

	// D6
	AttributableFailedRuns int
	ExcludedFailedRuns     int
	FailureReasonCounts    map[string]int

	// D7
	FirstPassIssues int
	ReviewedIssues  int
}

// Aggregate folds one bucket. It never divides and never substitutes a zero
// for an absent measurement.
func Aggregate(in Input) Result {
	out := Result{
		InjectedTokens:      in.InjectedTokens,
		ToolResultsMeasured: in.ToolResults.Measured,
		ToolResultsError:    in.ToolResults.Errored,
		FailureReasonCounts: map[string]int{},
	}

	var (
		runTokens        []int64
		disciplineScores []int
	)

	for _, r := range in.Runs {
		out.FinishedRuns++

		if r.UsageMeasured {
			runTokens = append(runTokens, r.RunTokens)
		}

		if d, ok := in.Discipline[r.TaskID]; ok && d.Covered {
			out.DisciplineCoveredRuns++
			disciplineScores = append(disciplineScores, d.Score)
			for _, ded := range d.Deductions {
				out.Deductions = append(out.Deductions, Deduction{
					TaskID: r.TaskID,
					Rule:   string(ded.Rule),
					Points: ded.Points,
					Seq:    ded.Seq,
				})
			}
		}

		out.AttemptTotal += r.Attempt
		if r.Attempt > 1 {
			out.RetriedRuns++
		}

		// Only failures are split. A cancelled run is terminal but says
		// nothing about the prompt, so it belongs on neither side.
		if r.Status == statusFailed {
			if taskfailure.PromptAttributable(taskfailure.Reason(r.FailureReason)) {
				out.AttributableFailedRuns++
				reason := r.FailureReason
				if reason == "" {
					reason = ReasonUnclassified
				}
				out.FailureReasonCounts[reason]++
			} else {
				out.ExcludedFailedRuns++
			}
		}
	}

	out.RunTokensMedian = medianInt64(runTokens)
	out.DisciplineScoreMedian = medianInt(disciplineScores)

	// D7 is per issue, not per run: several runs on one issue are one verdict.
	// An issue that never reached review has not been judged, so it counts on
	// neither side.
	seen := map[string]bool{}
	for _, r := range in.Runs {
		seen[r.IssueID] = true
	}
	for _, rev := range in.Reviews {
		if !seen[rev.IssueID] || rev.EnteredReview == 0 {
			continue
		}
		out.ReviewedIssues++
		if rev.SentBack == 0 {
			out.FirstPassIssues++
		}
	}

	return out
}

// medianInt64 returns nil for an empty sample: the absence of a measurement,
// not a zero measurement.
func medianInt64(v []int64) *int64 {
	if len(v) == 0 {
		return nil
	}
	s := append([]int64(nil), v...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	mid := len(s) / 2
	var m int64
	if len(s)%2 == 1 {
		m = s[mid]
	} else {
		m = (s[mid-1] + s[mid]) / 2
	}
	return &m
}

func medianInt(v []int) *int {
	if len(v) == 0 {
		return nil
	}
	s := append([]int(nil), v...)
	sort.Ints(s)
	mid := len(s) / 2
	var m int
	if len(s)%2 == 1 {
		m = s[mid]
	} else {
		m = (s[mid-1] + s[mid]) / 2
	}
	return &m
}
