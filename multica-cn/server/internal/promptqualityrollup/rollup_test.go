package promptqualityrollup

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/taskfailure"
)

var (
	testPool        *pgxpool.Pool
	testWorkspaceID string
	testUserID      string
)

const (
	fixtureEmail = "promptqualityrollup-fixture@multica.ai"
	fixtureSlug  = "promptqualityrollup-fixtures"
)

// TestMain follows the same contract as internal/testutil's: without a reachable
// database the suite exits green rather than red, so a checkout without
// Postgres still runs the rest of the tree.
func TestMain(m *testing.M) {
	ctx := context.Background()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://multica:multica@localhost:5432/multica?sslmode=disable"
	}
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		fmt.Printf("Skipping rollup DB tests: could not connect: %v\n", err)
		os.Exit(m.Run())
	}
	if err := pool.Ping(ctx); err != nil {
		fmt.Printf("Skipping rollup DB tests: database not reachable: %v\n", err)
		pool.Close()
		os.Exit(m.Run())
	}
	if err := seed(ctx, pool); err != nil {
		fmt.Printf("Skipping rollup DB tests: seed failed: %v\n", err)
		pool.Close()
		os.Exit(m.Run())
	}
	testPool = pool
	code := m.Run()
	_, _ = pool.Exec(ctx, `DELETE FROM workspace WHERE slug = $1`, fixtureSlug)
	_, _ = pool.Exec(ctx, `DELETE FROM "user" WHERE email = $1`, fixtureEmail)
	pool.Close()
	os.Exit(code)
}

func seed(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, `DELETE FROM workspace WHERE slug = $1`, fixtureSlug); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, `DELETE FROM "user" WHERE email = $1`, fixtureEmail); err != nil {
		return err
	}
	if err := pool.QueryRow(ctx, `INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id`,
		"Prompt Quality Fixture", fixtureEmail).Scan(&testUserID); err != nil {
		return err
	}
	if err := pool.QueryRow(ctx, `INSERT INTO workspace (name, slug, issue_prefix) VALUES ($1, $2, $3) RETURNING id`,
		"Prompt Quality Fixtures", fixtureSlug, "PQF").Scan(&testWorkspaceID); err != nil {
		return err
	}
	_, err := pool.Exec(ctx, `INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'owner')`,
		testWorkspaceID, testUserID)
	return err
}

// scenario is one prepared bucket: an agent, an issue and however many runs a
// test queues against a single prompt version.
type scenario struct {
	f        *testutil.Fixture
	t        *testing.T
	agentID  string
	issueID  string
	version  int
	finished time.Time
}

func newScenario(t *testing.T) *scenario {
	t.Helper()
	if testPool == nil {
		t.Skip("database not available")
	}
	f := testutil.New(testPool, testWorkspaceID, testUserID)

	// The watermark is global and single-row, so a test must start from a
	// known point rather than whatever a previous tick left. Rewinding it and
	// restoring it afterwards keeps the suite serial-safe within one package.
	var saved time.Time
	f.QueryRow(t, `SELECT watermark_at FROM prompt_quality_rollup_state WHERE id = 1`).Scan(&saved)
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(),
			`UPDATE prompt_quality_rollup_state SET watermark_at = $1 WHERE id = 1`, saved)
	})

	runtimeID := f.Runtime(t, "pq-runtime")
	s := &scenario{
		f:        f,
		t:        t,
		agentID:  f.Agent(t, "pq-agent", runtimeID),
		issueID:  f.Issue(t, "pq issue"),
		version:  7,
		finished: time.Now().UTC().Add(-time.Hour),
	}
	f.Exec(t, `UPDATE prompt_quality_rollup_state SET watermark_at = $1 WHERE id = 1`,
		s.finished.Add(-24*time.Hour))
	return s
}

// run queues one finished run attributed to the agent-scope prompt version.
func (s *scenario) run(status string, over ...testutil.Cols) string {
	s.t.Helper()
	cols := testutil.Cols{
		"issue_id":        s.issueID,
		"status":          status,
		"attempt":         1,
		"completed_at":    s.finished,
		"prompt_versions": testutil.Raw(fmt.Sprintf(`'{"agent": %d}'::jsonb`, s.version)),
	}
	for _, o := range over {
		for k, v := range o {
			cols[k] = v
		}
	}
	return s.f.Task(s.t, s.agentID, cols)
}

func (s *scenario) usage(taskID string, input, output int64) {
	s.t.Helper()
	s.f.Insert(s.t, "task_usage", testutil.Cols{
		"task_id": taskID, "model": "test-model",
		"input_tokens": input, "output_tokens": output,
	})
}

func (s *scenario) toolResult(taskID string, seq int, isError any) {
	s.t.Helper()
	s.f.Insert(s.t, "task_message", testutil.Cols{
		"task_id": taskID, "seq": seq, "type": "tool_result", "is_error": isError,
	})
}

func (s *scenario) toolUse(taskID string, seq int, tool, command string) {
	s.t.Helper()
	input, err := json.Marshal(map[string]any{"command": command})
	if err != nil {
		s.t.Fatalf("marshal input: %v", err)
	}
	s.f.Insert(s.t, "task_message", testutil.Cols{
		"task_id": taskID, "seq": seq, "type": "tool_use", "tool": tool,
		"input": string(input),
	})
}

func (s *scenario) roll() db.PromptQualityDaily {
	s.t.Helper()
	q := db.New(testPool)
	if _, err := (Runner{Queries: q}).Run(context.Background()); err != nil {
		s.t.Fatalf("Run: %v", err)
	}
	day := s.finished.Format("2006-01-02")
	var row db.PromptQualityDaily
	err := testPool.QueryRow(context.Background(), `
		SELECT finished_runs, injected_tokens, run_tokens_median,
		       discipline_score_median, discipline_covered_runs,
		       tool_results_measured, tool_results_error,
		       attempt_total, retried_runs,
		       attributable_failed_runs, excluded_failed_runs, failure_reason_counts,
		       first_pass_issues, reviewed_issues
		  FROM prompt_quality_daily
		 WHERE scope = 'agent' AND scope_id = $1 AND version = $2 AND day = $3`,
		s.agentID, s.version, day).Scan(
		&row.FinishedRuns, &row.InjectedTokens, &row.RunTokensMedian,
		&row.DisciplineScoreMedian, &row.DisciplineCoveredRuns,
		&row.ToolResultsMeasured, &row.ToolResultsError,
		&row.AttemptTotal, &row.RetriedRuns,
		&row.AttributableFailedRuns, &row.ExcludedFailedRuns, &row.FailureReasonCounts,
		&row.FirstPassIssues, &row.ReviewedIssues)
	if err != nil {
		s.t.Fatalf("read rolled bucket: %v", err)
	}
	s.t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(),
			`DELETE FROM prompt_quality_daily WHERE scope = 'agent' AND scope_id = $1`, s.agentID)
	})
	return row
}

// ---------------------------------------------------------------- D1

func TestRollupKeepsUnreportedUsageOutOfTheMedian(t *testing.T) {
	s := newScenario(t)
	a := s.run("completed")
	b := s.run("completed")
	s.run("completed") // no task_usage row at all
	s.usage(a, 100, 0)
	s.usage(b, 300, 0)

	row := s.roll()
	if !row.RunTokensMedian.Valid || row.RunTokensMedian.Int64 != 200 {
		t.Errorf("run_tokens_median = %+v, want 200: a run the daemon never reported is not a 0-token run",
			row.RunTokensMedian)
	}
	if row.FinishedRuns != 3 {
		t.Errorf("finished_runs = %d, want 3", row.FinishedRuns)
	}
}

func TestRollupLeavesRunTokensNullWhenNothingWasReported(t *testing.T) {
	s := newScenario(t)
	s.run("completed")

	if row := s.roll(); row.RunTokensMedian.Valid {
		t.Errorf("run_tokens_median = %d, want NULL", row.RunTokensMedian.Int64)
	}
}

// ---------------------------------------------------------------- D2

func TestRollupScoresDisciplinePerRunAndCountsCoverage(t *testing.T) {
	s := newScenario(t)
	clean := s.run("completed")
	sloppy := s.run("completed")
	s.run("completed") // text-only run: nothing the rule table can read

	s.toolUse(clean, 1, "Bash", "multica issue comment add X --content-file ./reply.md")
	s.toolUse(sloppy, 1, "Bash", "git stash pop")

	row := s.roll()
	if row.DisciplineCoveredRuns != 2 {
		t.Errorf("discipline_covered_runs = %d, want 2: the run with no inspectable command is unmeasured, not perfect",
			row.DisciplineCoveredRuns)
	}
	if !row.DisciplineScoreMedian.Valid {
		t.Fatal("discipline_score_median is NULL but two runs were covered")
	}
	median, err := row.DisciplineScoreMedian.Float64Value()
	if err != nil {
		t.Fatalf("decode median: %v", err)
	}
	if median.Float64 != 90 {
		t.Errorf("discipline_score_median = %v, want 90 (100 and 80)", median.Float64)
	}
}

// ---------------------------------------------------------------- D4 (T5)

// The acceptance criterion: a run predating the is_error column must read as
// "no data" on D4, never as a 0% tool failure rate. The rollup expresses that
// by leaving the measured denominator at 0.
func TestRollupReportsNoDataWhenIsErrorWasNeverRecorded(t *testing.T) {
	s := newScenario(t)
	task := s.run("completed")
	s.toolResult(task, 1, nil)
	s.toolResult(task, 2, nil)

	row := s.roll()
	if row.ToolResultsMeasured != 0 {
		t.Errorf("tool_results_measured = %d, want 0: NULL is_error is not a measurement", row.ToolResultsMeasured)
	}
	if row.ToolResultsError != 0 {
		t.Errorf("tool_results_error = %d, want 0", row.ToolResultsError)
	}
}

func TestRollupCountsMeasuredToolResultsSeparatelyFromErrors(t *testing.T) {
	s := newScenario(t)
	task := s.run("completed")
	s.toolResult(task, 1, false)
	s.toolResult(task, 2, true)
	s.toolResult(task, 3, nil) // still unmeasured, even beside measured ones

	row := s.roll()
	if row.ToolResultsMeasured != 2 || row.ToolResultsError != 1 {
		t.Errorf("tool results = %d errored of %d measured, want 1 of 2",
			row.ToolResultsError, row.ToolResultsMeasured)
	}
}

// ---------------------------------------------------------------- D5

func TestRollupSumsAttemptsAndCountsRetries(t *testing.T) {
	s := newScenario(t)
	s.run("completed")
	s.run("completed", testutil.Cols{"attempt": 3})

	row := s.roll()
	if row.AttemptTotal != 4 {
		t.Errorf("attempt_total = %d, want 4", row.AttemptTotal)
	}
	if row.RetriedRuns != 1 {
		t.Errorf("retried_runs = %d, want 1", row.RetriedRuns)
	}
}

// ---------------------------------------------------------------- D6 (T4)

// The acceptance criterion names ReasonRuntimeOffline: a run that failed
// because the runtime went offline stays out of the prompt-attribution
// denominator and out of the reason breakdown.
func TestRollupExcludesRuntimeOfflineFromPromptAttribution(t *testing.T) {
	s := newScenario(t)
	s.run("failed", testutil.Cols{"failure_reason": string(taskfailure.ReasonRuntimeOffline)})
	s.run("failed", testutil.Cols{"failure_reason": string(taskfailure.ReasonIterationLimit)})
	s.run("completed")

	row := s.roll()
	if row.ExcludedFailedRuns != 1 {
		t.Errorf("excluded_failed_runs = %d, want 1", row.ExcludedFailedRuns)
	}
	if row.AttributableFailedRuns != 1 {
		t.Errorf("attributable_failed_runs = %d, want 1", row.AttributableFailedRuns)
	}
	var reasons map[string]int
	if err := json.Unmarshal(row.FailureReasonCounts, &reasons); err != nil {
		t.Fatalf("decode failure_reason_counts: %v", err)
	}
	if _, present := reasons[string(taskfailure.ReasonRuntimeOffline)]; present {
		t.Errorf("runtime_offline leaked into the breakdown: %v", reasons)
	}
	if reasons[string(taskfailure.ReasonIterationLimit)] != 1 {
		t.Errorf("failure_reason_counts = %v, want iteration_limit: 1", reasons)
	}
}

// ---------------------------------------------------------------- D7

func TestRollupCountsFirstPassOnlyForIssuesThatReachedReview(t *testing.T) {
	s := newScenario(t)
	s.run("completed")
	s.f.Insert(t, "activity_log", testutil.Cols{
		"workspace_id": testWorkspaceID, "issue_id": s.issueID, "action": "status_changed",
		"details": testutil.Raw(`'{"from": "in_progress", "to": "in_review"}'::jsonb`),
	})

	row := s.roll()
	if row.ReviewedIssues != 1 || row.FirstPassIssues != 1 {
		t.Errorf("first_pass/reviewed = %d/%d, want 1/1", row.FirstPassIssues, row.ReviewedIssues)
	}
}

func TestRollupLeavesUnreviewedIssueOutOfBothCounts(t *testing.T) {
	s := newScenario(t)
	s.run("completed")

	row := s.roll()
	if row.ReviewedIssues != 0 || row.FirstPassIssues != 0 {
		t.Errorf("first_pass/reviewed = %d/%d, want 0/0: an issue that never reached review has no verdict",
			row.FirstPassIssues, row.ReviewedIssues)
	}
}

// ---------------------------------------------------------------- convergence

// Rewriting rather than incrementing is what lets this rollup run off a bare
// watermark. If a second pass over the same bucket doubled its counts, the
// no-dirty-queue design in migration 925 would be wrong.
func TestRollupRescanningABucketConverges(t *testing.T) {
	s := newScenario(t)
	s.run("completed")
	s.run("failed", testutil.Cols{"failure_reason": string(taskfailure.ReasonIterationLimit)})

	first := s.roll()
	s.f.Exec(t, `UPDATE prompt_quality_rollup_state SET watermark_at = $1 WHERE id = 1`,
		s.finished.Add(-24*time.Hour))
	second := s.roll()

	if first.FinishedRuns != second.FinishedRuns || first.AttributableFailedRuns != second.AttributableFailedRuns {
		t.Errorf("rescan changed the bucket: %d/%d then %d/%d",
			first.FinishedRuns, first.AttributableFailedRuns,
			second.FinishedRuns, second.AttributableFailedRuns)
	}
}

// A tick that finds nothing must not move the watermark forward past runs it
// never read.
func TestRollupWithNoDirtyBucketsLeavesWatermarkAlone(t *testing.T) {
	s := newScenario(t)
	s.f.Exec(t, `UPDATE prompt_quality_rollup_state SET watermark_at = now() WHERE id = 1`)

	var before time.Time
	s.f.QueryRow(t, `SELECT watermark_at FROM prompt_quality_rollup_state WHERE id = 1`).Scan(&before)
	out, err := (Runner{Queries: db.New(testPool)}).Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Buckets != 0 {
		t.Errorf("Buckets = %d, want 0", out.Buckets)
	}
	var after time.Time
	s.f.QueryRow(t, `SELECT watermark_at FROM prompt_quality_rollup_state WHERE id = 1`).Scan(&after)
	if !after.Equal(before) {
		t.Errorf("watermark moved from %s to %s on an empty tick", before, after)
	}
}
