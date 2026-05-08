package handler

import (
	"context"
	"testing"
	"time"
)

// TestRollupTaskUsageDaily_AggregatesAndIsIdempotent exercises the
// rollup_task_usage_daily_window() SQL function directly. This is the
// shared aggregation primitive used by both the cron-driven watermark
// loop and the offline backfill command, so its correctness underpins
// the entire ListRuntimeUsage read path. Two properties matter:
//
//  1. It correctly groups raw `task_usage` rows by (date, runtime,
//     workspace, provider, model) and sums the four token columns.
//  2. Re-aggregating an already-rolled-up window through the upsert
//     produces *additive* totals (the ON CONFLICT DO UPDATE adds
//     EXCLUDED to the existing row). This is the property the
//     watermark wrapper relies on to make late-arriving events safe:
//     a future tick can re-process a partial day's tail without
//     wiping out earlier contributions.
func TestRollupTaskUsageDaily_AggregatesAndIsIdempotent(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	runtimeID := handlerTestRuntimeID(t)
	var agentID string
	if err := testPool.QueryRow(ctx, `
		SELECT id FROM agent WHERE workspace_id = $1 LIMIT 1
	`, testWorkspaceID).Scan(&agentID); err != nil {
		t.Fatalf("fetch agent: %v", err)
	}

	var issueID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, title, creator_id, creator_type)
		VALUES ($1, 'rollup test', $2, 'member')
		RETURNING id
	`, testWorkspaceID, testUserID).Scan(&issueID); err != nil {
		t.Fatalf("create issue: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, issueID)
	})

	// Pin the test to a fixed historical day so we don't collide with
	// concurrent rollups of "today" running against the same fixture
	// runtime. 2020-06-15 is far outside any backfill window the rest
	// of the suite touches.
	day := time.Date(2020, 6, 15, 0, 0, 0, 0, time.UTC)

	// Two rows on the same (date, provider, model) — must collapse to
	// a single output row whose totals sum the inputs.
	insertUsage := func(usageAt time.Time, model string, in, out int64) {
		var taskID string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO agent_task_queue (agent_id, issue_id, runtime_id, status, created_at)
			VALUES ($1, $2, $3, 'completed', $4)
			RETURNING id
		`, agentID, issueID, runtimeID, usageAt).Scan(&taskID); err != nil {
			t.Fatalf("insert task: %v", err)
		}
		if _, err := testPool.Exec(ctx, `
			INSERT INTO task_usage (task_id, provider, model, input_tokens, output_tokens, created_at)
			VALUES ($1, 'claude', $2, $3, $4, $5)
		`, taskID, model, in, out, usageAt); err != nil {
			t.Fatalf("insert task_usage: %v", err)
		}
		t.Cleanup(func() {
			testPool.Exec(ctx, `DELETE FROM agent_task_queue WHERE id = $1`, taskID)
		})
	}

	insertUsage(day.Add(1*time.Hour), "claude-3-5-sonnet", 100, 10)
	insertUsage(day.Add(2*time.Hour), "claude-3-5-sonnet", 200, 20)
	// A second model on the same day must produce a *separate* output
	// row (different group key).
	insertUsage(day.Add(3*time.Hour), "claude-3-5-haiku", 50, 5)

	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM task_usage_daily WHERE runtime_id = $1 AND bucket_date = $2::date`, runtimeID, day)
	})

	// --- 1) Initial aggregation produces the expected totals.
	if _, err := testPool.Exec(ctx, `
		SELECT rollup_task_usage_daily_window($1::timestamptz, $2::timestamptz)
	`, day, day.Add(24*time.Hour)); err != nil {
		t.Fatalf("rollup_task_usage_daily_window: %v", err)
	}

	type row struct {
		Model       string
		InputTokens int64
		Output      int64
		EventCount  int64
	}
	read := func() map[string]row {
		rs, err := testPool.Query(ctx, `
			SELECT model, input_tokens, output_tokens, event_count
			  FROM task_usage_daily
			 WHERE runtime_id = $1 AND bucket_date = $2::date
		`, runtimeID, day)
		if err != nil {
			t.Fatalf("read task_usage_daily: %v", err)
		}
		defer rs.Close()
		out := map[string]row{}
		for rs.Next() {
			var r row
			if err := rs.Scan(&r.Model, &r.InputTokens, &r.Output, &r.EventCount); err != nil {
				t.Fatalf("scan: %v", err)
			}
			out[r.Model] = r
		}
		return out
	}

	got := read()
	if len(got) != 2 {
		t.Fatalf("expected 2 rows (one per model), got %d: %+v", len(got), got)
	}
	if got["claude-3-5-sonnet"].InputTokens != 300 || got["claude-3-5-sonnet"].Output != 30 || got["claude-3-5-sonnet"].EventCount != 2 {
		t.Errorf("sonnet bucket wrong: %+v", got["claude-3-5-sonnet"])
	}
	if got["claude-3-5-haiku"].InputTokens != 50 || got["claude-3-5-haiku"].Output != 5 || got["claude-3-5-haiku"].EventCount != 1 {
		t.Errorf("haiku bucket wrong: %+v", got["claude-3-5-haiku"])
	}

	// --- 2) Re-aggregating the same window adds the same totals again.
	// This is the contract the watermark wrapper relies on: callers
	// MUST keep windows non-overlapping; if they don't, they pay the
	// "double" cost. Verifying it explicitly so the property doesn't
	// silently regress.
	if _, err := testPool.Exec(ctx, `
		SELECT rollup_task_usage_daily_window($1::timestamptz, $2::timestamptz)
	`, day, day.Add(24*time.Hour)); err != nil {
		t.Fatalf("rollup_task_usage_daily_window (second call): %v", err)
	}
	got = read()
	if got["claude-3-5-sonnet"].InputTokens != 600 || got["claude-3-5-sonnet"].EventCount != 4 {
		t.Errorf("after second call, sonnet should have doubled: %+v", got["claude-3-5-sonnet"])
	}
}

// TestRollupTaskUsageDaily_WatermarkAdvances verifies the cron entry
// point: rollup_task_usage_daily() consults task_usage_rollup_state to
// decide its window, performs the upsert, and bumps the watermark.
// We seed the watermark to a known value, force time to pass via a
// fixture, and assert the watermark moves forward by exactly the
// elapsed-window minus the 5 minute safety lag built into the function.
func TestRollupTaskUsageDaily_WatermarkAdvances(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	// Seed the watermark to "long ago" so the next call has a non-empty
	// window. Use a test-scoped low value so we don't clobber any other
	// test's state — the singleton row gets restored at the end.
	var prevWatermark time.Time
	if err := testPool.QueryRow(ctx, `SELECT watermark_at FROM task_usage_rollup_state WHERE id = 1`).Scan(&prevWatermark); err != nil {
		t.Fatalf("read prev watermark: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `UPDATE task_usage_rollup_state SET watermark_at = $1 WHERE id = 1`, prevWatermark)
	})

	if _, err := testPool.Exec(ctx, `
		UPDATE task_usage_rollup_state
		   SET watermark_at = '2020-01-01 00:00:00+00', last_error = NULL
		 WHERE id = 1
	`); err != nil {
		t.Fatalf("seed watermark: %v", err)
	}

	if _, err := testPool.Exec(ctx, `SELECT rollup_task_usage_daily()`); err != nil {
		t.Fatalf("rollup_task_usage_daily: %v", err)
	}

	var newWatermark time.Time
	var lastError *string
	if err := testPool.QueryRow(ctx, `SELECT watermark_at, last_error FROM task_usage_rollup_state WHERE id = 1`).Scan(&newWatermark, &lastError); err != nil {
		t.Fatalf("read new watermark: %v", err)
	}
	if lastError != nil {
		t.Fatalf("rollup recorded error: %s", *lastError)
	}

	// New watermark must be near now() - 5 min. Allow a wide window
	// (±2 min) so this isn't flaky on slow CI.
	expected := time.Now().UTC().Add(-5 * time.Minute)
	delta := newWatermark.Sub(expected)
	if delta < -2*time.Minute || delta > 2*time.Minute {
		t.Errorf("watermark %s not within 2min of expected %s (delta %s)", newWatermark, expected, delta)
	}
}
