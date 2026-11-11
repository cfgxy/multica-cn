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

	runtimeID := dbfx.Runtime(t, "ModelCallCountDBRuntime", testutil.Cols{
		"daemon_id":    "model-call-count-daemon",
		"runtime_mode": "local",
	})
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

	// A successful provider response with stable evidence is known. Replaying the
	// daemon report must not add a second count; a later retry with a distinct
	// provider ID must add one.
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

	liveEvidence := func() {
		req := modelCallEvidenceRequest(t, runtimeID, taskID,
			`{"provider":"claude","model":"model-call-count-live","evidence_id":"live-call-1"}`)
		testutil.Call(t, testHandler.ReportTaskModelCallEvidence, req).Want(http.StatusOK)
	}
	if _, err := testPool.Exec(ctx, `UPDATE agent_task_queue SET status = 'running' WHERE id = $1`, taskID); err != nil {
		t.Fatalf("mark task running before live evidence: %v", err)
	}
	liveEvidence()
	if _, err := testPool.Exec(ctx, `
		UPDATE task_usage_model_call_evidence SET created_at = now() - INTERVAL '2 hours'
		WHERE task_id = $1 AND provider = 'claude' AND evidence_id = 'live-call-1'`, taskID); err != nil {
		t.Fatalf("age evidence timestamp fixture: %v", err)
	}
	var agedEvidenceAt time.Time
	if err := testPool.QueryRow(ctx, `
		SELECT created_at FROM task_usage_model_call_evidence
		WHERE task_id = $1 AND provider = 'claude' AND evidence_id = 'live-call-1'`, taskID).Scan(&agedEvidenceAt); err != nil {
		t.Fatalf("read aged evidence timestamp: %v", err)
	}
	liveEvidence()
	var replayedEvidenceAt time.Time
	if err := testPool.QueryRow(ctx, `
		SELECT created_at FROM task_usage_model_call_evidence
		WHERE task_id = $1 AND provider = 'claude' AND evidence_id = 'live-call-1'`, taskID).Scan(&replayedEvidenceAt); err != nil {
		t.Fatalf("read replayed evidence timestamp: %v", err)
	}
	if !replayedEvidenceAt.Equal(agedEvidenceAt) {
		t.Fatalf("replay changed first evidence timestamp: got %s, want %s", replayedEvidenceAt, agedEvidenceAt)
	}
	report(`{"usage":[{"provider":"claude","model":"model-call-count-live","input_tokens":10,"model_call_evidence_complete":true,"model_call_evidence_ids":["live-call-1"]}]}`)
	var liveCalls int64
	if err := testPool.QueryRow(ctx, `
		SELECT model_call_count FROM task_usage
		WHERE task_id = $1 AND provider = 'claude' AND model = 'model-call-count-live'`, taskID).Scan(&liveCalls); err != nil {
		t.Fatalf("read replayed terminal usage count: %v", err)
	}
	if liveCalls != 1 {
		t.Fatalf("terminal replay call count = %d, want 1", liveCalls)
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
	if hourlyKnown != 4 {
		t.Errorf("rollup known half = %d, want 4 (two success IDs, one failure ID, and one live ID)", hourlyKnown)
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

// TestTaskUsageModelCallEvidenceCorrectionRehomesDurableUsage proves that a
// provider correction for the same call identity moves the durable usage row to
// the corrected model without duplicating its tokens or call count.
func TestTaskUsageModelCallEvidenceCorrectionRehomesDurableUsage(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	runtimeID := dbfx.Runtime(t, "ModelCallCorrectionRuntime", testutil.Cols{
		"daemon_id":    "model-call-correction-daemon",
		"runtime_mode": "local",
	})
	agentID := dbfx.Agent(t, "ModelCallCorrectionAgent", runtimeID)
	issueID := dbfx.Issue(t, "model-call-correction-issue")
	taskID := dbfx.Task(t, agentID, testutil.Cols{
		"issue_id":   issueID,
		"runtime_id": runtimeID,
		"status":     "completed",
	})
	dbfx.Cleanup(t, `DELETE FROM task_usage_model_call_evidence WHERE task_id = $1`, taskID)
	dbfx.Cleanup(t, `DELETE FROM task_usage WHERE task_id = $1`, taskID)

	report := func(body string) {
		req := daemonTaskRequest(t, "/api/daemon/tasks/"+taskID+"/usage", taskID, json.RawMessage(body))
		testutil.Call(t, testHandler.ReportTaskUsage, req).Want(http.StatusOK)
	}

	report(`{"usage":[{"provider":"claude","model":"model-call-correction-old","input_tokens":100,"output_tokens":10,"model_call_evidence_complete":true,"model_call_evidence_ids":["correction-1"]}]}`)
	report(`{"usage":[{"provider":"claude","model":"model-call-correction-new","input_tokens":100,"output_tokens":10,"model_call_evidence_complete":true,"model_call_evidence_ids":["correction-1"]}]}`)

	rows, err := testHandler.Queries.GetTaskUsage(ctx, parseUUID(taskID))
	if err != nil {
		t.Fatalf("read corrected task usage: %v", err)
	}
	byModel := make(map[string]db.TaskUsage, len(rows))
	for _, row := range rows {
		byModel[row.Model] = row
	}
	if _, ok := byModel["model-call-correction-old"]; ok {
		t.Fatal("superseded model usage row remains after evidence correction")
	}
	corrected, ok := byModel["model-call-correction-new"]
	if !ok {
		t.Fatalf("corrected model usage row missing: %#v", byModel)
	}
	if corrected.InputTokens != 100 || corrected.OutputTokens != 10 {
		t.Fatalf("corrected tokens = input %d output %d, want 100 / 10", corrected.InputTokens, corrected.OutputTokens)
	}
	if !corrected.ModelCallCount.Valid || corrected.ModelCallCount.Int64 != 1 {
		t.Fatalf("corrected call count = %+v, want 1", corrected.ModelCallCount)
	}

	var evidenceModel string
	if err := testPool.QueryRow(ctx, `
		SELECT model FROM task_usage_model_call_evidence
		WHERE task_id = $1 AND provider = 'claude' AND evidence_id = 'correction-1'`, taskID).Scan(&evidenceModel); err != nil {
		t.Fatalf("read corrected evidence: %v", err)
	}
	if evidenceModel != "model-call-correction-new" {
		t.Fatalf("evidence model = %q, want corrected model", evidenceModel)
	}
}

// TestTaskUsageModelCallEvidencePartialCorrectionRecountsOldModel proves that
// moving only part of a model's evidence also refreshes the old durable count.
func TestTaskUsageModelCallEvidencePartialCorrectionRecountsOldModel(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	runtimeID := dbfx.Runtime(t, "ModelCallPartialCorrectionRuntime", testutil.Cols{
		"daemon_id":    "model-call-partial-correction-daemon",
		"runtime_mode": "local",
	})
	agentID := dbfx.Agent(t, "ModelCallPartialCorrectionAgent", runtimeID)
	issueID := dbfx.Issue(t, "model-call-partial-correction-issue")
	taskID := dbfx.Task(t, agentID, testutil.Cols{
		"issue_id":   issueID,
		"runtime_id": runtimeID,
		"status":     "completed",
	})
	dbfx.Cleanup(t, `DELETE FROM task_usage_model_call_evidence WHERE task_id = $1`, taskID)
	dbfx.Cleanup(t, `DELETE FROM task_usage WHERE task_id = $1`, taskID)

	report := func(body string) {
		req := daemonTaskRequest(t, "/api/daemon/tasks/"+taskID+"/usage", taskID, json.RawMessage(body))
		testutil.Call(t, testHandler.ReportTaskUsage, req).Want(http.StatusOK)
	}

	report(`{"usage":[{"provider":"claude","model":"model-call-partial-old","input_tokens":100,"output_tokens":10,"model_call_evidence_complete":true,"model_call_evidence_ids":["partial-1","partial-2"]}]}`)
	report(`{"usage":[{"provider":"claude","model":"model-call-partial-new","input_tokens":40,"output_tokens":4,"model_call_evidence_complete":true,"model_call_evidence_ids":["partial-1"]}]}`)

	rows, err := testHandler.Queries.GetTaskUsage(ctx, parseUUID(taskID))
	if err != nil {
		t.Fatalf("read partially corrected task usage: %v", err)
	}
	byModel := make(map[string]db.TaskUsage, len(rows))
	for _, row := range rows {
		byModel[row.Model] = row
	}
	old, ok := byModel["model-call-partial-old"]
	if !ok || !old.ModelCallCount.Valid || old.ModelCallCount.Int64 != 1 {
		t.Fatalf("old model usage = %+v, want one remaining call", old)
	}
	newUsage, ok := byModel["model-call-partial-new"]
	if !ok || !newUsage.ModelCallCount.Valid || newUsage.ModelCallCount.Int64 != 1 {
		t.Fatalf("new model usage = %+v, want one corrected call", newUsage)
	}

	var oldTotal, newTotal int64
	if err := testPool.QueryRow(ctx, `
		SELECT COALESCE(SUM(model_call_count) FILTER (WHERE model = 'model-call-partial-old'), 0),
		       COALESCE(SUM(model_call_count) FILTER (WHERE model = 'model-call-partial-new'), 0)
		FROM task_usage_model_call_totals
		WHERE task_id = $1 AND provider = 'claude'`, taskID).Scan(&oldTotal, &newTotal); err != nil {
		t.Fatalf("read partially corrected totals: %v", err)
	}
	if oldTotal != 1 || newTotal != 1 {
		t.Fatalf("partial correction totals = old %d new %d, want 1 / 1", oldTotal, newTotal)
	}
}
