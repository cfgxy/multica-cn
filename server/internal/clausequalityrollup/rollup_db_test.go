package clausequalityrollup

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// roll runs one tick and returns nothing; failures fail the test.
func roll(t *testing.T) {
	t.Helper()
	if _, err := (Runner{Queries: db.New(testPool)}).Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// dailyRow reads one (clause, version, day) bucket.
func dailyRow(t *testing.T, clauseID string, version int, day string) db.ClauseQualityDaily {
	t.Helper()
	var row db.ClauseQualityDaily
	err := testPool.QueryRow(context.Background(), `
		SELECT finished_runs, injected_tokens, attributable_failed_runs, excluded_failed_runs
		  FROM clause_quality_daily
		 WHERE clause_id = $1 AND clause_version = $2 AND day = $3`,
		clauseID, version, day).Scan(
		&row.FinishedRuns, &row.InjectedTokens,
		&row.AttributableFailedRuns, &row.ExcludedFailedRuns)
	if err != nil {
		t.Fatalf("read rolled bucket: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(),
			`DELETE FROM clause_quality_daily WHERE clause_id = $1`, clauseID)
	})
	return row
}

// The window key is the clause version at injection time and the aggregate
// day is the run's terminal day (ADR 007 §7): two runs at v2 land in the v2
// bucket on the day they finished, and a v1 row stays where it belongs.
func TestClauseRollupBucketsByVersionAndTerminalDay(t *testing.T) {
	c := newClauseFixture(t)
	finished := time.Now().UTC().Add(-time.Hour)
	// The watermark must predate the OLDEST injected run: the v1 window lives
	// two days back, and the dirty-bucket scan only sees completed_at newer
	// than the watermark.
	c.f.Exec(t, `UPDATE clause_quality_rollup_state SET watermark_at = $1 WHERE id = 1`,
		finished.Add(-72*time.Hour))

	c.inject(finished, 2, "completed")
	c.inject(finished, 2, "completed")
	c.inject(finished.Add(-48*time.Hour), 1, "failed")

	roll(t)

	day := finished.Format("2006-01-02")
	row := dailyRow(t, c.id, 2, day)
	if row.FinishedRuns != 2 {
		t.Errorf("v2 finished_runs = %d, want 2", row.FinishedRuns)
	}
	// D1's static half: the injected clause text's own token estimate — the
	// v2 content, non-NULL because the version row exists.
	if !row.InjectedTokens.Valid || row.InjectedTokens.Int64 <= 0 {
		t.Errorf("v2 injected_tokens = %+v, want the estimated clause size", row.InjectedTokens)
	}
	// The v1 failed run is a different window key on a different day.
	oldDay := finished.Add(-48 * time.Hour).Format("2006-01-02")
	old := dailyRow(t, c.id, 1, oldDay)
	if old.FinishedRuns != 1 || old.AttributableFailedRuns != 1 {
		t.Errorf("v1 bucket = %d runs / %d attributable, want 1/1",
			old.FinishedRuns, old.AttributableFailedRuns)
	}
}

// An injection whose task has not reached a terminal state stays out of every
// bucket until it does — the watermark contract.
func TestClauseRollupIgnoresRunningTasks(t *testing.T) {
	c := newClauseFixture(t)
	finished := time.Now().UTC().Add(-time.Hour)
	c.f.Exec(t, `UPDATE clause_quality_rollup_state SET watermark_at = $1 WHERE id = 1`,
		finished.Add(-24*time.Hour))

	c.inject(finished, 2, "running")

	roll(t)

	var n int
	err := testPool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM clause_quality_daily WHERE clause_id = $1`, c.id).Scan(&n)
	if err != nil {
		t.Fatalf("count daily rows: %v", err)
	}
	if n != 0 {
		t.Errorf("non-terminal run landed in %d buckets, want 0", n)
	}
}

// seedWindow writes one daily row per day for a version window, as if the
// rollup had recorded it.
func seedWindow(t *testing.T, clauseID string, version, runs, attributable int, days int) {
	t.Helper()
	for i := 0; i < days; i++ {
		day := time.Now().UTC().AddDate(0, 0, -i-1).Format("2006-01-02")
		_, err := testPool.Exec(context.Background(), `
			INSERT INTO clause_quality_daily (workspace_id, clause_id, clause_version, day,
				finished_runs, attributable_failed_runs, excluded_failed_runs,
				discipline_covered_runs, discipline_deductions, failure_reason_counts,
				tool_results_measured, tool_results_error, attempt_total, retried_runs,
				first_pass_issues, reviewed_issues)
			VALUES ($1, $2, $3, $4, $5, $6, 0, 0, '[]'::jsonb, '{}'::jsonb, 0, 0, $7, 0, 0, 0)
			ON CONFLICT (clause_id, clause_version, day) DO UPDATE
				SET finished_runs = EXCLUDED.finished_runs,
				    attributable_failed_runs = EXCLUDED.attributable_failed_runs`,
			testWorkspaceID, clauseID, version, day, runs, attributable, runs)
		if err != nil {
			t.Fatalf("seed window: %v", err)
		}
	}
}

func rollbackDraftCount(t *testing.T, clauseID string, version int) int {
	t.Helper()
	var n int
	err := testPool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM prompt_proposal WHERE clause_id = $1 AND rollback_of_version = $2`,
		clauseID, version).Scan(&n)
	if err != nil {
		t.Fatalf("count drafts: %v", err)
	}
	return n
}

// A version whose attributable failure rate is significantly worse than its
// predecessor's — both windows past the sample gate — gets exactly one
// system-drafted rollback proposal, and the latch keeps it at one.
func TestRollbackSuggestionGeneratedOnce(t *testing.T) {
	c := newClauseFixture(t)
	seedWindow(t, c.id, 1, 10, 3, 5)  // v1: 50 runs, 15 attributable — 30%
	seedWindow(t, c.id, 2, 10, 15, 5) // v2: 50 runs, 75 attributable — 60%

	checker := RollbackChecker{Queries: db.New(testPool)}
	out := checker.EvaluateClauses(context.Background(), nil) // nil candidate list is a no-op
	if out.Evaluated != 0 || len(out.Failures) != 0 {
		t.Fatalf("nil candidates evaluated: %+v", out)
	}

	out = checker.EvaluateClauses(context.Background(), []pgtype.UUID{clauseUID(t, c.id)})
	if out.Evaluated != 1 || len(out.Failures) != 0 {
		t.Fatalf("evaluate: %+v", out)
	}
	if n := rollbackDraftCount(t, c.id, 2); n != 1 {
		t.Fatalf("rollback drafts = %d, want 1", n)
	}
	var source, changeKind, clauseText string
	var rollbackOf int
	err := testPool.QueryRow(context.Background(), `
		SELECT source, change_kind, clause_text, rollback_of_version
		  FROM prompt_proposal WHERE clause_id = $1 AND rollback_of_version = 2`,
		c.id).Scan(&source, &changeKind, &clauseText, &rollbackOf)
	if err != nil {
		t.Fatalf("read draft: %v", err)
	}
	if source != "system" || changeKind != "revise_clause" || rollbackOf != 2 {
		t.Errorf("draft = %s/%s/rollback_of=%d, want system/revise_clause/2", source, changeKind, rollbackOf)
	}
	if want := "- **条款测试**：基线版本文本。"; clauseText != want {
		t.Errorf("draft clause_text = %q, want the v1 content %q", clauseText, want)
	}

	// The planned comparison runs once per version: a second pass — even
	// with new data — must not regenerate.
	seedWindow(t, c.id, 2, 10, 20, 5)
	out = checker.EvaluateClauses(context.Background(), []pgtype.UUID{clauseUID(t, c.id)})
	if len(out.Failures) != 0 {
		t.Fatalf("re-evaluate: %+v", out.Failures)
	}
	if n := rollbackDraftCount(t, c.id, 2); n != 1 {
		t.Fatalf("latch broken: rollback drafts = %d, want 1", n)
	}
}

// clauseUID converts the fixture's clause id for the checker's signature.
func clauseUID(t *testing.T, id string) pgtype.UUID {
	t.Helper()
	u, err := uuid.Parse(id)
	if err != nil {
		t.Fatalf("parse clause id: %v", err)
	}
	return pgtype.UUID{Bytes: u, Valid: true}
}

// Below the sample gate neither window may yield a percentage conclusion
// ("样本不足"), so no draft appears even at a glaring rate gap.
func TestRollbackSuggestionRespectsSampleGate(t *testing.T) {
	c := newClauseFixture(t)
	seedWindow(t, c.id, 1, 5, 1, 2)  // 10 runs — under the gate
	seedWindow(t, c.id, 2, 5, 4, 2)  // 10 runs, 80% vs 10%

	checker := RollbackChecker{Queries: db.New(testPool)}
	out := checker.EvaluateClauses(context.Background(), []pgtype.UUID{clauseUID(t, c.id)})
	if len(out.Failures) != 0 {
		t.Fatalf("evaluate: %+v", out.Failures)
	}
	if n := rollbackDraftCount(t, c.id, 2); n != 0 {
		t.Fatalf("draft created below the sample gate: %d", n)
	}
}

// A version performing better than — or statistically indistinguishable
// from — its predecessor is never flagged, and a first version has no
// predecessor to compare against at all.
func TestRollbackSuggestionNeedsDegradationAndPredecessor(t *testing.T) {
	c := newClauseFixture(t)
	seedWindow(t, c.id, 1, 10, 15, 5) // v1: 30%
	seedWindow(t, c.id, 2, 10, 3, 5)  // v2: 10% — improvement

	checker := RollbackChecker{Queries: db.New(testPool)}
	out := checker.EvaluateClauses(context.Background(), []pgtype.UUID{clauseUID(t, c.id)})
	if len(out.Failures) != 0 || rollbackDraftCount(t, c.id, 2) != 0 {
		t.Fatalf("improvement flagged: %+v, drafts=%d", out.Failures, rollbackDraftCount(t, c.id, 2))
	}

	// A fresh clause with only version 1 is skipped before any window math.
	fresh := newClauseFixture(t)
	fresh.f.Exec(t, `DELETE FROM clause_version WHERE clause_id = $1 AND version = 2`, fresh.id)
	fresh.f.Exec(t, `DELETE FROM clause_binding WHERE clause_id = $1`, fresh.id)
	out = checker.EvaluateClauses(context.Background(), []pgtype.UUID{clauseUID(t, fresh.id)})
	if len(out.Failures) != 0 {
		t.Fatalf("first-version evaluation failed: %+v", out.Failures)
	}
	if n := rollbackDraftCount(t, fresh.id, 1); n != 0 {
		t.Fatalf("first version drafted: %d", n)
	}
}
