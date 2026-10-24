package handler

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// TestTaskUsageModelCallCountDBPath is the migration-987 database guard:
// the upsert stores a reported call count as a real value and an absent
// one as NULL (unknown, never a measured 0); the rollup window function
// splits the known and unknown halves into task_usage_hourly; and the
// dashboard read path surfaces both halves per (date, provider, model).
func TestTaskUsageModelCallCountDBPath(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	runtimeID := handlerTestRuntimeID(t)
	agentID := dbfx.Agent(t, "ModelCallCountDBAgent", runtimeID)
	issueID := dbfx.Issue(t, "model-call-count-issue")
	taskID := dbfx.Task(t, agentID, testutil.Cols{
		"issue_id":   issueID,
		"runtime_id": runtimeID,
		"status":     "completed",
	})
	dbfx.Cleanup(t, `DELETE FROM task_usage WHERE task_id = $1`, taskID)

	queries := testHandler.Queries

	// One row with provider call evidence, one without: the upsert must
	// store the count as a real value and the absence as NULL, never 0.
	if err := queries.UpsertTaskUsage(ctx, db.UpsertTaskUsageParams{
		TaskID:         parseUUID(taskID),
		Provider:       "claude",
		Model:          "model-call-count-known",
		InputTokens:    1000,
		OutputTokens:   100,
		ModelCallCount: authoritativeModelCallCount(42),
	}); err != nil {
		t.Fatalf("upsert known row: %v", err)
	}
	if err := queries.UpsertTaskUsage(ctx, db.UpsertTaskUsageParams{
		TaskID:       parseUUID(taskID),
		Provider:     "claude",
		Model:        "model-call-count-unknown",
		InputTokens:  500,
		OutputTokens: 50,
		// authoritativeModelCallCount(0) is the zero (NULL) Int8: an older
		// daemon that never carried call evidence.
		ModelCallCount: authoritativeModelCallCount(0),
	}); err != nil {
		t.Fatalf("upsert unknown row: %v", err)
	}

	var knownRows int
	if err := testPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM task_usage
		WHERE task_id = $1 AND model = 'model-call-count-known' AND model_call_count = 42
	`, taskID).Scan(&knownRows); err != nil {
		t.Fatalf("read known row: %v", err)
	}
	if knownRows != 1 {
		t.Errorf("known row: expected the count 42 stored, got %d matching rows", knownRows)
	}
	var unknownIsNull bool
	if err := testPool.QueryRow(ctx, `
		SELECT model_call_count IS NULL FROM task_usage
		WHERE task_id = $1 AND model = 'model-call-count-unknown'
	`, taskID).Scan(&unknownIsNull); err != nil {
		t.Fatalf("read unknown row: %v", err)
	}
	if !unknownIsNull {
		t.Error("unknown row: an absent count must store as NULL, not a real 0")
	}

	// Drive the rollup window function over the last hour — the production
	// cron does the same with a 5-minute lag — and assert the two halves
	// land in task_usage_hourly without bleeding into each other. The
	// bucket is the raw rows' hour, so key the read by the task's own
	// rows: the join carries the exact bucket set those rows rolled into.
	dbfx.Exec(t, `
		SELECT rollup_task_usage_hourly_window(now() - interval '1 hour', now() + interval '1 hour')
	`)
	var hourlyKnown, hourlyUnknown int64
	if err := testPool.QueryRow(ctx, `
		SELECT
			COALESCE(SUM(h.model_call_count), 0),
			COALESCE(SUM(h.model_call_unknown_count), 0)
		FROM task_usage_hourly h
		JOIN task_usage tu ON tu.model = h.model AND tu.task_id = $1
		WHERE h.runtime_id = (SELECT runtime_id FROM agent_task_queue WHERE id = $1)
			AND h.bucket_hour = date_trunc('hour', tu.created_at AT TIME ZONE 'UTC')
	`, taskID).Scan(&hourlyKnown, &hourlyUnknown); err != nil {
		t.Fatalf("rollup read: %v", err)
	}
	if hourlyKnown != 42 {
		t.Errorf("rollup known half = %d, want 42 (only the evidenced row)", hourlyKnown)
	}
	if hourlyUnknown != 1 {
		t.Errorf("rollup unknown half = %d, want 1 (the row without call evidence)", hourlyUnknown)
	}

	// The dashboard daily read path exposes both halves per bucket. Since is
	// a viewer-local start-of-day cutoff and Until is NULL (no future rows),
	// mirroring how the endpoint's legacy ?days= path bounds the query.
	since := pgtype.Timestamptz{
		Time:  time.Now().AddDate(0, 0, -31).Truncate(time.Hour),
		Valid: true,
	}
	rows, err := queries.ListDashboardUsageDaily(ctx, db.ListDashboardUsageDailyParams{
		WorkspaceID: parseUUID(testWorkspaceID),
		Tz:          "UTC",
		Since:       since,
	})
	if err != nil {
		t.Fatalf("list dashboard daily: %v", err)
	}
	var seenKnown, seenUnknown bool
	for _, r := range rows {
		switch r.Model {
		case "model-call-count-known":
			seenKnown = true
			if r.ModelCallCount != 42 || r.ModelCallUnknownCount != 0 {
				t.Errorf("daily known row = calls %d unknown %d, want 42 / 0", r.ModelCallCount, r.ModelCallUnknownCount)
			}
		case "model-call-count-unknown":
			seenUnknown = true
			if r.ModelCallCount != 0 || r.ModelCallUnknownCount != 1 {
				t.Errorf("daily unknown row = calls %d unknown %d, want 0 / 1", r.ModelCallCount, r.ModelCallUnknownCount)
			}
		}
	}
	if !seenKnown || !seenUnknown {
		t.Fatalf("daily rows missing the fixture buckets: known=%v unknown=%v", seenKnown, seenUnknown)
	}
}
