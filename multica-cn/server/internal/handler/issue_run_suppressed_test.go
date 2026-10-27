package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// runSuppressedSnapshot reads the snapshot columns straight from the issue row
// so the assertions judge the persisted state, not just the response payload.
func runSuppressedSnapshot(t *testing.T, issueID string) (suppressed bool, hasAt bool) {
	t.Helper()
	var at *time.Time
	if err := testPool.QueryRow(context.Background(),
		`SELECT run_suppressed, run_suppressed_at FROM issue WHERE id = $1`, issueID,
	).Scan(&suppressed, &at); err != nil {
		t.Fatalf("load run_suppressed snapshot: %v", err)
	}
	return suppressed, at != nil
}

// countRunSuppressedActivities counts the append-only `run_suppressed` audit
// events for an issue.
func countRunSuppressedActivities(t *testing.T, issueID string) int {
	t.Helper()
	var n int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM activity_log WHERE issue_id = $1 AND action = 'run_suppressed'`, issueID,
	).Scan(&n); err != nil {
		t.Fatalf("count run_suppressed activities: %v", err)
	}
	return n
}

func updateIssueForTest(t *testing.T, issueID string, body map[string]any) IssueResponse {
	t.Helper()
	w := httptest.NewRecorder()
	req := withURLParam(newRequest("PUT", "/api/issues/"+issueID, body), "id", issueID)
	testHandler.UpdateIssue(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateIssue: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp IssueResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode issue: %v", err)
	}
	return resp
}

// seedTodoIssueWithAgent creates an active (non-backlog) issue so the very
// first assign can produce a run the suppress can cancel — the assign source
// never fires on backlog rows (the parking lot, MUL-6463).
func seedTodoIssueWithAgent(t *testing.T) (issue IssueResponse, agentID string) {
	t.Helper()
	agentID = seededReadyAgentID(t)
	issue = createIssueForTest(t, map[string]any{"title": "RUYI-275 seed", "status": "todo"})
	return issue, agentID
}

// placeHold assigns the ready agent with suppress_run=true, the "暂不开始"
// write this whole feature is about.
func placeHold(t *testing.T, issueID, agentID string) IssueResponse {
	t.Helper()
	return updateIssueForTest(t, issueID, map[string]any{
		"assignee_type": "agent",
		"assignee_id":   agentID,
		"suppress_run":  true,
	})
}

// AC1: an honored suppress flips the read-side snapshot and appends exactly
// one run_suppressed activity whose details carry the v1 WARN's five fields
// verbatim.
func TestHonoredSuppressSetsSnapshotAndWritesEvent(t *testing.T) {
	issue, agentID := seedTodoIssueWithAgent(t)

	resp := placeHold(t, issue.ID, agentID)

	if !resp.RunSuppressed || resp.RunSuppressedAt == nil {
		t.Fatalf("response snapshot not set: run_suppressed=%v run_suppressed_at=%v", resp.RunSuppressed, resp.RunSuppressedAt)
	}
	suppressed, hasAt := runSuppressedSnapshot(t, issue.ID)
	if !suppressed || !hasAt {
		t.Fatalf("persisted snapshot not set: run_suppressed=%v run_suppressed_at_present=%v", suppressed, hasAt)
	}
	if n := countRunSuppressedActivities(t, issue.ID); n != 1 {
		t.Fatalf("expected exactly 1 run_suppressed activity, got %d", n)
	}

	var actorType, actorID string
	var rawDetails []byte
	if err := testPool.QueryRow(context.Background(), `
		SELECT actor_type, actor_id, details FROM activity_log
		WHERE issue_id = $1 AND action = 'run_suppressed'
		ORDER BY created_at DESC LIMIT 1
	`, issue.ID).Scan(&actorType, &actorID, &rawDetails); err != nil {
		t.Fatalf("load run_suppressed activity: %v", err)
	}
	if actorType != "member" || actorID != testUserID {
		t.Fatalf("event actor mismatch: actor_type=%q actor_id=%q (want member/%s)", actorType, actorID, testUserID)
	}
	var details map[string]any
	if err := json.Unmarshal(rawDetails, &details); err != nil {
		t.Fatalf("decode details: %v", err)
	}
	// The five fields mirror the v1 WARN log line field by field (RUYI-275).
	want := map[string]string{
		"issue_id":       issue.ID,
		"actor_type":     "member",
		"actor_id":       testUserID,
		"trigger_source": "assign",
	}
	for k, v := range want {
		if details[k] != v {
			t.Fatalf("details[%q] = %v, want %q", k, details[k], v)
		}
	}
	if details["target_status"] != resp.Status {
		t.Fatalf("details[target_status] = %v, want current status %q", details["target_status"], resp.Status)
	}
}

// AC2 negative: a run-starting write WITHOUT suppress_run leaves no snapshot
// and no event — "suppressed" must mean suppressed, not "any assign".
func TestUnsuppressedWriteSetsNothing(t *testing.T) {
	issue, agentID := seedTodoIssueWithAgent(t)

	resp := updateIssueForTest(t, issue.ID, map[string]any{
		"assignee_type": "agent",
		"assignee_id":   agentID,
	})
	if resp.RunSuppressed {
		t.Fatalf("unsuppressed assign must not set run_suppressed")
	}
	if suppressed, hasAt := runSuppressedSnapshot(t, issue.ID); suppressed || hasAt {
		t.Fatalf("persisted snapshot polluted: suppressed=%v has_at=%v", suppressed, hasAt)
	}
	if n := countRunSuppressedActivities(t, issue.ID); n != 0 {
		t.Fatalf("unsuppressed write must write no event, got %d", n)
	}
}

// Keep semantics: a write that starts no run (title edit) leaves the snapshot
// untouched and writes no second event.
func TestNonTriggeringWriteHoldsSnapshot(t *testing.T) {
	issue, agentID := seedTodoIssueWithAgent(t)
	placeHold(t, issue.ID, agentID)

	resp := updateIssueForTest(t, issue.ID, map[string]any{"title": "renamed while on hold"})
	if !resp.RunSuppressed {
		t.Fatalf("title edit must keep run_suppressed")
	}
	if suppressed, _ := runSuppressedSnapshot(t, issue.ID); !suppressed {
		t.Fatalf("title edit must keep persisted snapshot")
	}
	if n := countRunSuppressedActivities(t, issue.ID); n != 1 {
		t.Fatalf("title edit must not append events, got %d (want 1)", n)
	}
}

// Clear semantics, run path: the next write that truly starts a run resets
// both snapshot fields — the "立即开始" escape hatch from the UX spec.
func TestRunStartingWriteClearsSnapshot(t *testing.T) {
	issue, agentID := seedTodoIssueWithAgent(t)
	placeHold(t, issue.ID, agentID)
	if suppressed, _ := runSuppressedSnapshot(t, issue.ID); !suppressed {
		t.Fatalf("fixture setup: hold not placed")
	}

	// Park the issue, then promote it WITHOUT suppress_run: the promotion
	// (backlog -> todo) truly starts a run and lifts the hold.
	updateIssueForTest(t, issue.ID, map[string]any{"status": "backlog"})
	resp := updateIssueForTest(t, issue.ID, map[string]any{"status": "todo"})

	if resp.RunSuppressed {
		t.Fatalf("run-starting promotion must clear run_suppressed")
	}
	if suppressed, hasAt := runSuppressedSnapshot(t, issue.ID); suppressed || hasAt {
		t.Fatalf("run-starting promotion must reset both fields: suppressed=%v has_at=%v", suppressed, hasAt)
	}
}

// Clear semantics, unassign path: removing the assignee lifts the hold — a
// hold with no active holder means nothing.
func TestAssigneeClearanceClearsSnapshot(t *testing.T) {
	issue, agentID := seedTodoIssueWithAgent(t)
	placeHold(t, issue.ID, agentID)

	resp := updateIssueForTest(t, issue.ID, map[string]any{
		"assignee_type": nil,
		"assignee_id":   nil,
	})
	if resp.RunSuppressed {
		t.Fatalf("unassign must clear run_suppressed")
	}
	if suppressed, hasAt := runSuppressedSnapshot(t, issue.ID); suppressed || hasAt {
		t.Fatalf("unassign must reset both fields: suppressed=%v has_at=%v", suppressed, hasAt)
	}
	if n := countRunSuppressedActivities(t, issue.ID); n != 1 {
		t.Fatalf("unassign must not append events, got %d (want 1)", n)
	}
}

// Batch parity: the batch endpoint shares the single enqueue predicate and
// must maintain the snapshot the same way (suppress_run applies batch-wide).
func TestBatchHonoredSuppressSetsSnapshotAndEvent(t *testing.T) {
	issue, agentID := seedTodoIssueWithAgent(t)

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues/batch-update", map[string]any{
		"issue_ids": []string{issue.ID},
		"updates": map[string]any{
			"assignee_type": "agent",
			"assignee_id":   agentID,
			"suppress_run":  true,
		},
	})
	testHandler.BatchUpdateIssues(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("BatchUpdateIssues: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	if suppressed, hasAt := runSuppressedSnapshot(t, issue.ID); !suppressed || !hasAt {
		t.Fatalf("batch suppress must set persisted snapshot: suppressed=%v has_at=%v", suppressed, hasAt)
	}
	if n := countRunSuppressedActivities(t, issue.ID); n != 1 {
		t.Fatalf("expected exactly 1 run_suppressed activity from batch, got %d", n)
	}
}
