package handler

// RUYI-397 multi-runtime preference routing: a runtime whose host reported
// memory backpressure is passed over at claim time. The stored-report parser
// semantics are pinned without a database; the endpoint tests prove the hold
// actually keeps tasks queued (never failed) and decays with freshness.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestRuntimeHeldByBackpressure_StoredReportSemantics(t *testing.T) {
	now := time.Now()
	rt := func(metadata string) db.AgentRuntime {
		return db.AgentRuntime{
			ID:       mustParseUUID(t, "6f9619ff-8b86-d011-b42d-00c04fc964ff"),
			Metadata: []byte(metadata),
		}
	}
	fresh := now.Add(-30 * time.Second).Format(time.RFC3339)
	stale := now.Add(-3 * time.Minute).Format(time.RFC3339)

	cases := []struct {
		name     string
		metadata string
		want     bool
	}{
		{name: "no backpressure key", metadata: `{"other": 1}`, want: false},
		{name: "malformed metadata", metadata: `{not json`, want: false},
		{name: "inactive report", metadata: `{"backpressure": {"active": false, "recorded_at": "` + fresh + `"}}`, want: false},
		{name: "fresh active report holds", metadata: `{"backpressure": {"active": true, "recorded_at": "` + fresh + `"}}`, want: true},
		{name: "report older than the TTL releases", metadata: `{"backpressure": {"active": true, "recorded_at": "` + stale + `"}}`, want: false},
		{name: "active report without recorded_at releases", metadata: `{"backpressure": {"active": true}}`, want: false},
		{name: "active report with unreadable recorded_at releases", metadata: `{"backpressure": {"active": true, "recorded_at": "not-a-time"}}`, want: false},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := runtimeHeldByBackpressure(rt(tt.metadata), now); got != tt.want {
				t.Fatalf("runtimeHeldByBackpressure = %v, want %v", got, tt.want)
			}
		})
	}
}

// seedStoredBackpressure writes the runtime's stored backpressure report the
// same way SetAgentRuntimeBackpressure does (report merged under the
// `backpressure` key, recorded_at stamped server-side), with a caller-chosen
// age so TTL decay is testable without sleeping.
func seedStoredBackpressure(t *testing.T, ctx context.Context, runtimeID, age string) {
	t.Helper()
	report := `{"active": true, "reason": "mem", "mem_available_pct": 7.5, "swap_used_pct": 40}`
	if _, err := testPool.Exec(ctx,
		`UPDATE agent_runtime SET metadata = metadata || jsonb_build_object(
			'backpressure', $1::jsonb || jsonb_build_object('recorded_at', now() - ($2::interval)))
		WHERE id = $3`,
		report, age, runtimeID,
	); err != nil {
		t.Fatalf("seed stored backpressure: %v", err)
	}
}

func clearStoredBackpressure(t *testing.T, ctx context.Context, runtimeID string) {
	t.Helper()
	if _, err := testPool.Exec(ctx,
		`UPDATE agent_runtime SET metadata = metadata - 'backpressure' WHERE id = $1`, runtimeID,
	); err != nil {
		t.Fatalf("clear stored backpressure: %v", err)
	}
}

func queueStatus(t *testing.T, ctx context.Context, taskID string) string {
	t.Helper()
	var status string
	if err := testPool.QueryRow(ctx,
		`SELECT status FROM agent_task_queue WHERE id = $1`, taskID,
	).Scan(&status); err != nil {
		t.Fatalf("read task %s status: %v", taskID, err)
	}
	return status
}

func decodeBatchClaim(t *testing.T, w *httptest.ResponseRecorder) batchClaimResponse {
	t.Helper()
	var resp batchClaimResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode batch claim response: %v (%s)", err, w.Body.String())
	}
	return resp
}

func TestClaimTasksByRuntime_PrefersHealthyRuntime(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	heldRT := createClaimReclaimRuntime(t, ctx, "BP held rt")
	healthyRT := createClaimReclaimRuntime(t, ctx, "BP healthy rt")
	heldAgent, heldIssue := createClaimReclaimAgentAndIssue(t, ctx, heldRT, "BP held agent")
	healthyAgent, healthyIssue := createClaimReclaimAgentAndIssue(t, ctx, healthyRT, "BP healthy agent")
	heldTask := seedQueuedIssueTask(t, ctx, heldAgent, heldRT, heldIssue)
	healthyTask := seedQueuedIssueTask(t, ctx, healthyAgent, healthyRT, healthyIssue)

	// The held runtime carries a fresh ACTIVE report; the batch claim must
	// pass over it and serve the healthy runtime instead.
	seedStoredBackpressure(t, ctx, heldRT, "10 seconds")

	w := postBatchClaim(t, testWorkspaceID, []string{heldRT, healthyRT}, 5)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	resp := decodeBatchClaim(t, w)
	if len(resp.Tasks) != 1 || resp.Tasks[0].RuntimeID != healthyRT || resp.Tasks[0].ID != healthyTask {
		t.Fatalf("claimed %+v, want only the healthy runtime's task %s", resp.Tasks, healthyTask)
	}
	if got := queueStatus(t, ctx, heldTask); got != "queued" {
		t.Fatalf("held task status = %q, want queued (the hold delays, never fails)", got)
	}

	// Age the stored report past the hold TTL: the runtime releases and its
	// queued task is served on the next claim.
	seedStoredBackpressure(t, ctx, heldRT, "3 minutes")
	w = postBatchClaim(t, testWorkspaceID, []string{heldRT}, 5)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	resp = decodeBatchClaim(t, w)
	if len(resp.Tasks) != 1 || resp.Tasks[0].ID != heldTask {
		t.Fatalf("aged-out hold must release the runtime, claimed %+v want %s", resp.Tasks, heldTask)
	}
}

func TestClaimTasksByRuntime_RequestReportHoldsWholeMachine(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	rt1 := createClaimReclaimRuntime(t, ctx, "BP req rt1")
	rt2 := createClaimReclaimRuntime(t, ctx, "BP req rt2")
	a1, i1 := createClaimReclaimAgentAndIssue(t, ctx, rt1, "BP req agent1")
	a2, i2 := createClaimReclaimAgentAndIssue(t, ctx, rt2, "BP req agent2")
	task1 := seedQueuedIssueTask(t, ctx, a1, rt1, i1)
	task2 := seedQueuedIssueTask(t, ctx, a2, rt2, i2)

	// An ACTIVE report on the request itself is machine-level and freshest —
	// every runtime on the daemon shares the host, so nothing is claimed.
	w := httptest.NewRecorder()
	req := newDaemonTokenRequest("POST", "/api/daemon/tasks/claim",
		map[string]any{"daemon_id": batchClaimTestDaemonID, "runtime_ids": []string{rt1, rt2},
			"max_tasks": 5,
			"backpressure": map[string]any{"active": true, "reason": "mem",
				"mem_available_pct": 6.2, "swap_used_pct": 30}},
		testWorkspaceID, batchClaimTestDaemonID)
	testHandler.ClaimTasksByRuntime(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if resp := decodeBatchClaim(t, w); len(resp.Tasks) != 0 {
		t.Fatalf("active request report must hold the whole machine, claimed %+v", resp.Tasks)
	}
	for id, name := range map[string]string{task1: "task1", task2: "task2"} {
		if got := queueStatus(t, ctx, id); got != "queued" {
			t.Fatalf("%s status = %q, want queued", name, got)
		}
	}

	// A healthy report on the request is equally authoritative: it releases
	// the machine even though a stored ACTIVE hold exists on rt1.
	seedStoredBackpressure(t, ctx, rt1, "10 seconds")
	w = httptest.NewRecorder()
	req = newDaemonTokenRequest("POST", "/api/daemon/tasks/claim",
		map[string]any{"daemon_id": batchClaimTestDaemonID, "runtime_ids": []string{rt1, rt2},
			"max_tasks":    5,
			"backpressure": map[string]any{"active": false, "reason": "", "mem_available_pct": 41, "swap_used_pct": 12}},
		testWorkspaceID, batchClaimTestDaemonID)
	testHandler.ClaimTasksByRuntime(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	resp := decodeBatchClaim(t, w)
	if len(resp.Tasks) != 2 {
		t.Fatalf("healthy request report must override the stored hold and serve both runtimes, claimed %+v", resp.Tasks)
	}
}

func TestClaimTaskByRuntime_StoredHoldKeepsTaskQueued(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	runtimeID := createClaimReclaimRuntime(t, ctx, "BP singular rt")
	agentID, issueID := createClaimReclaimAgentAndIssue(t, ctx, runtimeID, "BP singular agent")
	queuedID := seedQueuedIssueTask(t, ctx, agentID, runtimeID, issueID)

	seedStoredBackpressure(t, ctx, runtimeID, "10 seconds")
	task, body := claimTaskByRuntimeForTest(t, runtimeID)
	if task != nil {
		t.Fatalf("held runtime claimed task %s, want nil (%s)", task.ID, body)
	}
	if got := queueStatus(t, ctx, queuedID); got != "queued" {
		t.Fatalf("held task status = %q, want queued", got)
	}

	clearStoredBackpressure(t, ctx, runtimeID)
	task, body = claimTaskByRuntimeForTest(t, runtimeID)
	if task == nil || task.ID != queuedID {
		t.Fatalf("after clearing the hold the claim must serve %s, got %+v (%s)", queuedID, task, body)
	}
}

// TestAgentTaskResponse_QueuedReasonOptional pins the wire contract for the
// RUYI-397 admission code: `queued_reason` is omitempty decoration on the
// task-run row, so payloads from backends/clients that never knew about it
// round-trip unchanged and the key only exists while a reason is set.
func TestAgentTaskResponse_QueuedReasonOptional(t *testing.T) {
	empty, err := json.Marshal(AgentTaskResponse{})
	if err != nil {
		t.Fatalf("marshal zero response: %v", err)
	}
	if bytes.Contains(empty, []byte("queued_reason")) {
		t.Fatalf("zero-value response carries queued_reason: %s", empty)
	}

	withReason, err := json.Marshal(AgentTaskResponse{QueuedReason: "runtime_backpressure"})
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	if !bytes.Contains(withReason, []byte(`"queued_reason":"runtime_backpressure"`)) {
		t.Fatalf("queued_reason missing from marshaled response: %s", withReason)
	}
}

// TestListTasksByIssue_QueuedReasonBackpressure proves the query-side half of
// the admission code: the execution-history read stamps `queued_reason` on
// queued rows whose agent's bound runtime sits under a fresh backpressure
// hold, and drops the field the moment the hold decays or clears — while
// already-dispatched rows never carry it, whatever the runtime reports.
func TestListTasksByIssue_QueuedReasonBackpressure(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler tests require a database")
	}
	ctx := context.Background()

	rtHeld := createClaimReclaimRuntime(t, ctx, "bp-queued-reason-rt")
	queuedAgentID, issueID := createClaimReclaimAgentAndIssue(t, ctx, rtHeld, "bp-queued-reason-agent")
	// One pending row per (issue, agent) (idx_one_pending_task_per_issue_agent_v2),
	// so the dispatched contrast row belongs to a second agent pinned to the
	// same held runtime — which also exercises the multi-agent binding path.
	runningAgentID := dbfx.Agent(t, "bp-queued-reason-runner", rtHeld)
	queuedID := seedQueuedIssueTask(t, ctx, queuedAgentID, rtHeld, issueID)
	runningID := createDispatchedClaimFixtureTask(t, ctx, runningAgentID, rtHeld, issueID, "1 minute", true)

	runs := func() map[string]AgentTaskResponse {
		resp := testutil.Call(t, testHandler.ListTasksByIssue,
			withURLParam(newRequest(http.MethodGet, "/api/issues/"+issueID+"/task-runs", nil), "id", issueID),
		).Want(http.StatusOK)
		var out []AgentTaskResponse
		resp.JSON(&out)
		byID := make(map[string]AgentTaskResponse, len(out))
		for _, row := range out {
			byID[row.ID] = row
		}
		return byID
	}

	// Fresh active hold: the queued row names the reason; the dispatched row
	// on the same held runtime does not.
	seedStoredBackpressure(t, ctx, rtHeld, "30 seconds")
	byID := runs()
	if got := byID[queuedID].QueuedReason; got != "runtime_backpressure" {
		t.Fatalf("queued row queued_reason = %q, want runtime_backpressure", got)
	}
	if got := byID[runningID].QueuedReason; got != "" {
		t.Fatalf("dispatched row carries queued_reason %q", got)
	}

	// Same report aged past the hold TTL — the field disappears (fail-open).
	seedStoredBackpressure(t, ctx, rtHeld, "3 minutes")
	byID = runs()
	if got := byID[queuedID].QueuedReason; got != "" {
		t.Fatalf("aged-out hold still reports queued_reason %q", got)
	}

	// Report cleared (daemon recovered) — the field stays gone.
	clearStoredBackpressure(t, ctx, rtHeld)
	byID = runs()
	if got := byID[queuedID].QueuedReason; got != "" {
		t.Fatalf("recovered runtime still reports queued_reason %q", got)
	}
}
