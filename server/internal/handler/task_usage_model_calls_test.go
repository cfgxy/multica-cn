package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	obsmetrics "github.com/multica-ai/multica/server/internal/metrics"
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

	// A live evidence row can arrive before the terminal usage report. The
	// Prometheus source query must expose that call even before hourly rollup.
	liveOnlyTaskID := dbfx.Task(t, agentID, testutil.Cols{
		"issue_id":   issueID,
		"runtime_id": runtimeID,
		"status":     "running",
	})
	liveOnlyReq := modelCallEvidenceRequest(t, runtimeID, liveOnlyTaskID,
		`{"provider":"claude","model":"model-call-count-live-only","evidence_id":"live-only-1"}`)
	testutil.Call(t, testHandler.ReportTaskModelCallEvidence, liveOnlyReq).Want(http.StatusOK)
	metricsRows, err := queries.SumTaskUsageTotalsForMetrics(ctx)
	if err != nil {
		t.Fatalf("sum usage metrics: %v", err)
	}
	var liveOnlyFound bool
	for _, row := range metricsRows {
		if row.RuntimeID == runtimeID && row.Provider == "claude" && row.Model == "model-call-count-live-only" {
			liveOnlyFound = true
			if row.ModelCallCount != 1 || row.InputTokens != 0 || row.OutputTokens != 0 {
				t.Fatalf("live-only metrics row = %+v, want one call and zero tokens", row)
			}
		}
	}
	if !liveOnlyFound {
		t.Fatalf("live-only evidence missing from usage metrics source")
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
		t.Errorf("rollup known half = %d, want 4 (two success IDs, one failure ID, and the live evidence call)", hourlyKnown)
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

func TestUsageMetricsDeltaAndModelKeyDrift(t *testing.T) {
	if testHandler == nil || testPool == nil || testHandler.TaskService == nil {
		t.Skip("database or task service not available")
	}
	ctx := context.Background()

	runtimeID := dbfx.Runtime(t, "UsageMetricsDeltaRuntime", testutil.Cols{
		"daemon_id":    "usage-metrics-delta-daemon",
		"runtime_mode": "local",
	})
	agentID := dbfx.Agent(t, "UsageMetricsDeltaAgent", runtimeID)
	issueID := dbfx.Issue(t, "usage-metrics-delta-issue")
	taskID := dbfx.Task(t, agentID, testutil.Cols{
		"issue_id":   issueID,
		"runtime_id": runtimeID,
		"status":     "running",
	})
	dbfx.Cleanup(t, `DELETE FROM task_usage_model_call_evidence WHERE task_id = $1`, taskID)
	dbfx.Cleanup(t, `DELETE FROM task_usage WHERE task_id = $1`, taskID)

	metrics := obsmetrics.NewBusinessMetrics()
	previousMetrics := testHandler.TaskService.Metrics
	testHandler.TaskService.Metrics = metrics
	t.Cleanup(func() { testHandler.TaskService.Metrics = previousMetrics })

	report := func(body string) {
		req := daemonTaskRequest(t, "/api/daemon/tasks/"+taskID+"/usage", taskID, json.RawMessage(body))
		testutil.Call(t, testHandler.ReportTaskUsage, req).Want(http.StatusOK)
	}

	// A corrected report can move the provider's stable call identity to a new
	// model key. Its cumulative totals must contribute only the increment beyond
	// the old key, and a replay must not increment the process metrics again.
	report(`{"usage":[{"provider":"claude","model":"usage-metrics-old","input_tokens":100,"output_tokens":10,"model_call_evidence_complete":true,"model_call_evidence_ids":["model-drift-1"]}]}`)
	report(`{"usage":[{"provider":"claude","model":"usage-metrics-new","input_tokens":125,"output_tokens":15,"model_call_evidence_complete":true,"model_call_evidence_ids":["model-drift-1"]}]}`)
	report(`{"usage":[{"provider":"claude","model":"usage-metrics-new","input_tokens":125,"output_tokens":15,"model_call_evidence_complete":true,"model_call_evidence_ids":["model-drift-1"]}]}`)

	rows, err := testHandler.Queries.GetTaskUsage(ctx, parseUUID(taskID))
	if err != nil {
		t.Fatalf("read task usage: %v", err)
	}
	if len(rows) != 1 || rows[0].Model != "usage-metrics-new" {
		t.Fatalf("usage rows after model drift = %+v, want only usage-metrics-new", rows)
	}
	if !rows[0].ModelCallCount.Valid || rows[0].ModelCallCount.Int64 != 1 {
		t.Fatalf("new model call count = %+v, want 1", rows[0].ModelCallCount)
	}

	// The same model reports cumulative totals twice. Only the 200+20 increase
	// is observable in Prometheus, while replaying the second report is inert.
	report(`{"usage":[{"provider":"claude","model":"usage-metrics-same","input_tokens":1000,"output_tokens":100,"model_call_evidence_complete":true,"model_call_evidence_ids":["same-model-1"]}]}`)
	report(`{"usage":[{"provider":"claude","model":"usage-metrics-same","input_tokens":1200,"output_tokens":120,"model_call_evidence_complete":true,"model_call_evidence_ids":["same-model-1"]}]}`)
	report(`{"usage":[{"provider":"claude","model":"usage-metrics-same","input_tokens":1200,"output_tokens":120,"model_call_evidence_complete":true,"model_call_evidence_ids":["same-model-1"]}]}`)

	// A new evidence identity with no token totals still represents one provider
	// request, but it must not move any token counter.
	report(`{"usage":[{"provider":"claude","model":"usage-metrics-evidence-only","model_call_evidence_complete":true,"model_call_evidence_ids":["evidence-only-1"]}]}`)
	report(`{"usage":[{"provider":"claude","model":"usage-metrics-evidence-only","model_call_evidence_complete":true,"model_call_evidence_ids":["evidence-only-1"]}]}`)
	beforeUnknown := counterValue(t, metrics, "multica_llm_request_total")
	unknownReq := modelCallEvidenceRequest(t, runtimeID, taskID,
		`{"provider":"claude","model":"usage-metrics-unknown","incomplete":true}`)
	testutil.Call(t, testHandler.ReportTaskModelCallEvidence, unknownReq).Want(http.StatusOK)
	if got := counterValue(t, metrics, "multica_llm_request_total"); got != beforeUnknown {
		t.Fatalf("incomplete evidence changed request counter: got %v, want %v", got, beforeUnknown)
	}

	if got := counterValue(t, metrics, "multica_llm_unpriced_tokens_total"); got != 1460 {
		t.Fatalf("captured unpriced token delta = %v, want 1460 (model drift, same-model delta, and no evidence-only tokens)", got)
	}
	if got := counterValue(t, metrics, "multica_llm_request_total"); got != 5 {
		t.Fatalf("captured usage reports = %v, want 5 after replay and evidence-only retry", got)
	}

	// A single terminal report can contain an incomplete old model row and a
	// complete corrected row for the same provider call. The old row remains
	// unknown because its token/cost totals are not proven to be replaced.
	mixedTaskID := dbfx.Task(t, agentID, testutil.Cols{
		"issue_id":   issueID,
		"runtime_id": runtimeID,
		"status":     "completed",
	})
	dbfx.Cleanup(t, `DELETE FROM task_usage_model_call_evidence WHERE task_id = $1`, mixedTaskID)
	dbfx.Cleanup(t, `DELETE FROM task_usage WHERE task_id = $1`, mixedTaskID)
	mixedReport := func(body string) {
		req := daemonTaskRequest(t, "/api/daemon/tasks/"+mixedTaskID+"/usage", mixedTaskID, json.RawMessage(body))
		testutil.Call(t, testHandler.ReportTaskUsage, req).Want(http.StatusOK)
	}
	mixedReport(`{"usage":[{"provider":"claude","model":"mixed-old","input_tokens":10,"model_call_evidence_complete":true,"model_call_evidence_ids":["mixed-evidence-1"]}]}`)
	mixedReport(`{"usage":[{"provider":"claude","model":"mixed-old","input_tokens":20},{"provider":"claude","model":"mixed-new","input_tokens":30,"model_call_evidence_complete":true,"model_call_evidence_ids":["mixed-evidence-1"]}]}`)
	mixedRows, err := testHandler.Queries.GetTaskUsage(ctx, parseUUID(mixedTaskID))
	if err != nil {
		t.Fatalf("read mixed model usage: %v", err)
	}
	var mixedOldUnknown, mixedNewKnown bool
	for _, row := range mixedRows {
		switch row.Model {
		case "mixed-old":
			mixedOldUnknown = !row.ModelCallCount.Valid
		case "mixed-new":
			mixedNewKnown = row.ModelCallCount.Valid && row.ModelCallCount.Int64 == 1
		}
	}
	if !mixedOldUnknown || !mixedNewKnown {
		t.Fatalf("mixed model usage = %+v, want old unknown and new known", mixedRows)
	}
}
