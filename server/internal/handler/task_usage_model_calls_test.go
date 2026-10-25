package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// TestTaskUsageModelCallCountDBPath drives the daemon handler's durable
// evidence path. It proves replay safety, distinct retry accounting, failure
// accounting, old-daemon unknown behavior, and the known/unknown rollup split.
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
	dbfx.Cleanup(t, `DELETE FROM task_usage_model_call_evidence WHERE task_id = $1`, taskID)
	dbfx.Cleanup(t, `DELETE FROM task_usage WHERE task_id = $1`, taskID)

	queries := testHandler.Queries

	report := func(body string) {
		req := daemonTaskRequest(t, "/api/daemon/tasks/"+taskID+"/usage", taskID, json.RawMessage(body))
		testutil.Call(t, testHandler.ReportTaskUsage, req).Want(http.StatusOK)
	}

	// The evidence-only request persists while the task is still executing, before
	// any terminal usage aggregate exists.
	report(`{"model_call_evidence":[{"provider":"claude","model":"model-call-count-known","evidence_id":"success-1"}]}`)
	var liveEvidenceRows, liveUsageRows int
	if err := testPool.QueryRow(ctx, `
		SELECT
			(SELECT COUNT(*) FROM task_usage_model_call_evidence WHERE task_id = $1),
			(SELECT COUNT(*) FROM task_usage WHERE task_id = $1)`, taskID).Scan(&liveEvidenceRows, &liveUsageRows); err != nil {
		t.Fatalf("read live evidence state: %v", err)
	}
	if liveEvidenceRows != 1 || liveUsageRows != 0 {
		t.Fatalf("mid-run persisted evidence=%d usage rows=%d, want 1 / 0", liveEvidenceRows, liveUsageRows)
	}
	// Backdate the evidence to model a call that has since left the rolling
	// window. Terminal aggregate replay must not move its first-observed time.
	if _, err := testPool.Exec(ctx, `
		UPDATE task_usage_model_call_evidence
		SET created_at = now() - interval '2 hours'
		WHERE task_id = $1 AND evidence_id = 'success-1'`, taskID); err != nil {
		t.Fatalf("backdate evidence for replay assertion: %v", err)
	}

	// Replaying the terminal report must not add a second count or refresh the
	// first-observed timestamp; a later retry with a distinct provider ID adds one.
	report(`{"usage":[{"provider":"claude","model":"model-call-count-known","input_tokens":1000,"output_tokens":100,"model_call_evidence_complete":true,"model_call_evidence_ids":["success-1"]}]}`)
	report(`{"usage":[{"provider":"claude","model":"model-call-count-known","input_tokens":1000,"output_tokens":100,"model_call_evidence_complete":true,"model_call_evidence_ids":["success-1"]}]}`)
	report(`{"usage":[{"provider":"claude","model":"model-call-count-known","input_tokens":1200,"output_tokens":120,"model_call_evidence_complete":true,"model_call_evidence_ids":["success-2"]}]}`)

	// ReportTaskUsage runs before the terminal callback. A provider request that
	// was issued before a later task failure therefore remains billable when it
	// carries its original evidence ID.
	report(`{"usage":[{"provider":"claude","model":"model-call-count-failure","input_tokens":500,"output_tokens":50,"model_call_evidence_complete":true,"model_call_evidence_ids":["failure-1"]}]}`)

	// An old daemon aggregate and a transport failure without raw request
	// evidence both remain unknown. Neither may be interpreted as 0 or 42 calls.
	report(`{"usage":[{"provider":"claude","model":"model-call-count-unknown","input_tokens":250,"output_tokens":25,"model_call_count":42}]}`)

	rows, err := queries.GetTaskUsage(ctx, parseUUID(taskID))
	if err != nil {
		t.Fatalf("read task usage: %v", err)
	}
	byModel := make(map[string]db.TaskUsage, len(rows))
	for _, row := range rows {
		byModel[row.Model] = row
	}
	if got := byModel["model-call-count-known"].ModelCallCount; !got.Valid || got.Int64 != 2 {
		t.Errorf("known evidence count = %+v, want 2", got)
	}
	if got := byModel["model-call-count-failure"].ModelCallCount; !got.Valid || got.Int64 != 1 {
		t.Errorf("failure evidence count = %+v, want 1", got)
	}
	if got := byModel["model-call-count-unknown"].ModelCallCount; got.Valid {
		t.Errorf("aggregate-only row count = %+v, want NULL/unknown", got)
	}

	var evidenceRows int
	if err := testPool.QueryRow(ctx, `SELECT COUNT(*) FROM task_usage_model_call_evidence WHERE task_id = $1`, taskID).Scan(&evidenceRows); err != nil {
		t.Fatalf("count durable evidence: %v", err)
	}
	if evidenceRows != 3 {
		t.Errorf("durable evidence rows = %d, want 3 after one replay and three distinct IDs", evidenceRows)
	}
	var replayedAt time.Time
	if err := testPool.QueryRow(ctx, `
		SELECT created_at FROM task_usage_model_call_evidence
		WHERE task_id = $1 AND provider = 'claude' AND evidence_id = 'success-1'`, taskID).Scan(&replayedAt); err != nil {
		t.Fatalf("read replayed evidence timestamp: %v", err)
	}
	if age := time.Since(replayedAt); age < 2*time.Hour {
		t.Errorf("terminal replay refreshed first evidence timestamp; age=%s, want at least 2h", age)
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
	if hourlyKnown != 3 {
		t.Errorf("rollup known half = %d, want 3 (two success IDs plus one failure ID)", hourlyKnown)
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
	dashboardRows, err := queries.ListDashboardUsageDaily(ctx, db.ListDashboardUsageDailyParams{
		WorkspaceID: parseUUID(testWorkspaceID),
		Tz:          "UTC",
		Since:       since,
	})
	if err != nil {
		t.Fatalf("list dashboard daily: %v", err)
	}
	var seenKnown, seenUnknown bool
	for _, r := range dashboardRows {
		switch r.Model {
		case "model-call-count-known":
			seenKnown = true
			if r.ModelCallCount != 2 || r.ModelCallUnknownCount != 0 {
				t.Errorf("daily known row = calls %d unknown %d, want 2 / 0", r.ModelCallCount, r.ModelCallUnknownCount)
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
