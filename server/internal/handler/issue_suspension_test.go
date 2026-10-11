package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// RUYI-690: the issue suspension feature. These tests drive the HTTP
// handlers against a real database and assert on persisted state (the
// issue_suspension rows, the issue.suspended_at snapshot, the task queue the
// gate and the wake act on) — not just response payloads. Suspension is
// orthogonal to the lifecycle status: every test here keeps the issue's
// status untouched and expects dispatch behavior to flip with the hold.

func suspendIssueForTest(t *testing.T, issueID string, body map[string]any) (int, IssueSuspensionMutationResponse) {
	t.Helper()
	w := httptest.NewRecorder()
	req := withURLParam(newRequest(http.MethodPost, "/api/issues/"+issueID+"/suspensions", body), "id", issueID)
	testHandler.SuspendIssue(w, req)
	var resp IssueSuspensionMutationResponse
	if w.Code == http.StatusCreated {
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decode suspend response: %v", err)
		}
	}
	return w.Code, resp
}

func releaseSuspensionForTest(t *testing.T, issueID, suspensionID, arm string, body map[string]any) int {
	t.Helper()
	w := httptest.NewRecorder()
	req := newRequest(http.MethodPost, "/api/issues/"+issueID+"/suspensions/"+suspensionID+"/"+arm, body)
	req = withURLParams(req, "id", issueID, "suspensionId", suspensionID)
	if arm == "resume" {
		testHandler.ResumeIssueSuspension(w, req)
	} else {
		testHandler.CancelIssueSuspension(w, req)
	}
	return w.Code
}

// issueSuspendedAtForTest reads the read-side snapshot straight from the
// issue row so the assertions judge the persisted state.
func issueSuspendedAtForTest(t *testing.T, issueID string) bool {
	t.Helper()
	var at *time.Time
	if err := testPool.QueryRow(context.Background(),
		`SELECT suspended_at FROM issue WHERE id = $1`, issueID).Scan(&at); err != nil {
		t.Fatalf("load suspended_at: %v", err)
	}
	return at != nil
}

// suspensionStateForTest reads (status, resumed_by_predicate) for one row.
func suspensionStateForTest(t *testing.T, suspensionID string) (string, string) {
	t.Helper()
	var status string
	var predicate []byte
	if err := testPool.QueryRow(context.Background(),
		`SELECT status, COALESCE(resumed_by_predicate, '{}'::jsonb)::text FROM issue_suspension WHERE id = $1`, suspensionID,
	).Scan(&status, &predicate); err != nil {
		t.Fatalf("load suspension state: %v", err)
	}
	return status, string(predicate)
}

func countSuspensionRowsForTest(t *testing.T, issueID string) int {
	t.Helper()
	var n int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM issue_suspension WHERE issue_id = $1`, issueID).Scan(&n); err != nil {
		t.Fatalf("count suspensions: %v", err)
	}
	return n
}

func countSuspensionActivitiesForTest(t *testing.T, issueID, action string) int {
	t.Helper()
	var n int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM activity_log WHERE issue_id = $1 AND action = $2`, issueID, action,
	).Scan(&n); err != nil {
		t.Fatalf("count %s activities: %v", action, err)
	}
	return n
}

func countSuspensionAuditForTest(t *testing.T, issueID, eventType string) int {
	t.Helper()
	var n int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_event WHERE issue_id = $1 AND event_type = $2`, issueID, eventType,
	).Scan(&n); err != nil {
		t.Fatalf("count %s audit events: %v", eventType, err)
	}
	return n
}

// AC (partial-unique index): at most one ACTIVE suspension per issue. A
// second suspend conflicts with 409; after release, suspending again is a new
// history row. Validation failures never create rows.
func TestSuspendSetsSnapshotSingleActiveAndValidation(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	issue := createIssueForTest(t, map[string]any{"title": "suspension seed", "status": "todo"})
	target := createIssueForTest(t, map[string]any{"title": "suspension target", "status": "todo"})

	// Validation: unknown waiting_on_type, missing target id, external wait
	// without a described release condition — all 400, no rows written.
	for name, body := range map[string]map[string]any{
		"bad waiting_on_type": {"reason": "r", "waiting_on_type": "someday"},
		"missing issue id":    {"reason": "r", "waiting_on_type": "issue"},
		"self wait":           {"reason": "r", "waiting_on_type": "issue", "waiting_on_issue_id": issue.ID},
		"external no cond":    {"reason": "r", "waiting_on_type": "external"},
	} {
		code, _ := suspendIssueForTest(t, issue.ID, body)
		if code != http.StatusBadRequest {
			t.Fatalf("%s: expected 400, got %d", name, code)
		}
	}
	if n := countSuspensionRowsForTest(t, issue.ID); n != 0 {
		t.Fatalf("validation failures wrote %d rows, want 0", n)
	}

	// First suspend: 201, snapshot set, dual audit trail (activity + audit
	// twin), waiting target recorded.
	code, resp := suspendIssueForTest(t, issue.ID, map[string]any{
		"reason":             "waiting for the schema decision",
		"waiting_on_type":    "issue",
		"waiting_on_issue_id": target.ID,
	})
	if code != http.StatusCreated {
		t.Fatalf("suspend: expected 201, got %d", code)
	}
	if !issueSuspendedAtForTest(t, issue.ID) {
		t.Fatal("suspend did not set issue.suspended_at")
	}
	if resp.Suspension.Status != "active" || resp.Suspension.WaitingOnType != "issue" {
		t.Fatalf("suspension row = %+v", resp.Suspension)
	}
	if resp.Issue.SuspendedAt == nil {
		t.Fatal("response issue snapshot missing suspended_at")
	}
	if n := countSuspensionActivitiesForTest(t, issue.ID, "issue.suspended"); n != 1 {
		t.Fatalf("issue.suspended activity rows = %d, want 1", n)
	}
	if n := countSuspensionAuditForTest(t, issue.ID, "issue.suspended"); n != 1 {
		t.Fatalf("issue.suspended audit rows = %d, want 1", n)
	}

	// Second active suspension loses to the partial unique index: 409, no
	// new row, snapshot untouched.
	code, _ = suspendIssueForTest(t, issue.ID, map[string]any{
		"reason":             "second hold",
		"waiting_on_type":    "external",
		"resume_condition":   "whenever",
	})
	if code != http.StatusConflict {
		t.Fatalf("double suspend: expected 409, got %d", code)
	}
	if n := countSuspensionRowsForTest(t, issue.ID); n != 1 {
		t.Fatalf("active rows after conflict = %d, want 1", n)
	}

	// Cancel the hold: snapshot cleared, row kept as history.
	if code := releaseSuspensionForTest(t, issue.ID, resp.Suspension.ID, "cancel", map[string]any{"note": "obsolete"}); code != http.StatusOK {
		t.Fatalf("cancel: expected 200, got %d", code)
	}
	if issueSuspendedAtForTest(t, issue.ID) {
		t.Fatal("cancel did not clear issue.suspended_at")
	}
	status, _ := suspensionStateForTest(t, resp.Suspension.ID)
	if status != "cancelled" {
		t.Fatalf("row status after cancel = %q, want cancelled", status)
	}

	// A released hold frees the slot: suspend again creates a NEW row.
	code, resp2 := suspendIssueForTest(t, issue.ID, map[string]any{
		"reason":           "second hold for real",
		"waiting_on_type":  "external",
		"resume_condition": "vendor replies",
	})
	if code != http.StatusCreated {
		t.Fatalf("re-suspend: expected 201, got %d", code)
	}
	if resp2.Suspension.ID == resp.Suspension.ID {
		t.Fatal("re-suspend reused the cancelled row")
	}
}

// AC (trigger gate): while suspended, normal comments and @mentions save but
// never queue runs — outcome blocked with the issue_suspended reason, preview
// stops promising the run. Release admits dispatch again.
func TestSuspensionGateHoldsCommentDispatchUntilRelease(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	agentID := seededReadyAgentID(t)
	issueID := createCommentTriggerPreviewIssue(t, "gate suspension seed", "", "")
	targetID := createCommentTriggerPreviewIssue(t, "gate suspension target", "", "")

	code, resp := suspendIssueForTest(t, issueID, map[string]any{
		"reason":             "parked on the dependency",
		"waiting_on_type":    "issue",
		"waiting_on_issue_id": targetID,
	})
	if code != http.StatusCreated {
		t.Fatalf("suspend: expected 201, got %d", code)
	}

	mention := fmt.Sprintf("[@Gate Agent](mention://agent/%s) please take a look", agentID)
	cCode, cResp := postCommentForGateTest(t, issueID, map[string]any{"content": mention})
	if cCode != http.StatusCreated || cResp.ID == "" {
		t.Fatalf("comment must save while suspended (201), got %d id=%q", cCode, cResp.ID)
	}
	outcome := findCommentOutcome(t, cResp.TriggerOutcomes, agentID)
	if outcome.Status != DispatchBlocked || outcome.ReasonCode != ReasonIssueSuspended {
		t.Fatalf("outcome = %+v, want blocked/%s", outcome, ReasonIssueSuspended)
	}
	if n := countIssueTasksForGateTest(t, issueID); n != 0 {
		t.Fatalf("suspended issue queued %d tasks, want 0", n)
	}

	preview := previewCommentTriggersForTest(t, issueID, map[string]any{"content": mention})
	if len(preview.Agents) != 0 {
		t.Fatalf("preview promised %d agents while suspended, want 0", len(preview.Agents))
	}
	found := false
	for _, b := range preview.Blocked {
		if b.TargetID == agentID && b.ReasonCode == ReasonIssueSuspended {
			found = true
		}
	}
	if !found {
		t.Fatalf("preview blocked outcomes missing %s: %+v", ReasonIssueSuspended, preview.Blocked)
	}

	// Release: the issue returns to normal scheduling — the same mention now
	// dispatches (auto-resume wake comes from the release path too, so the
	// count is at least the manual comment's; the gate's job is admitting).
	if c := releaseSuspensionForTest(t, issueID, resp.Suspension.ID, "resume", nil); c != http.StatusOK {
		t.Fatalf("resume: expected 200, got %d", c)
	}
	_, cResp2 := postCommentForGateTest(t, issueID, map[string]any{"content": mention})
	outcome2 := findCommentOutcome(t, cResp2.TriggerOutcomes, agentID)
	if outcome2.Status != DispatchQueued && outcome2.Status != DispatchCoalesced {
		t.Fatalf("outcome after release = %+v, want queued/coalesced", outcome2)
	}
}

// AC (machine predicate issue_done): an issue suspended on a target resumes
// automatically — row → resumed with the firing predicate denormalized,
// snapshot cleared, and (decision 3) the agent assignee woken back to work.
func TestAutoResumeOnWaitingIssueDone(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	agentID := seededReadyAgentID(t)
	waitingID := createCommentTriggerPreviewIssue(t, "auto resume waiting", "agent", agentID)
	target := createIssueForTest(t, map[string]any{"title": "auto resume target", "status": "todo"})

	code, resp := suspendIssueForTest(t, waitingID, map[string]any{
		"reason":             "waiting on the blocker",
		"waiting_on_type":    "issue",
		"waiting_on_issue_id": target.ID,
	})
	if code != http.StatusCreated {
		t.Fatalf("suspend: expected 201, got %d", code)
	}

	updateIssueForTest(t, target.ID, map[string]any{"status": "done"})

	status, predicate := suspensionStateForTest(t, resp.Suspension.ID)
	if status != "resumed" {
		t.Fatalf("row status after target done = %q, want resumed", status)
	}
	var p map[string]any
	if err := json.Unmarshal([]byte(predicate), &p); err != nil {
		t.Fatalf("decode resumed_by_predicate: %v", err)
	}
	if p["kind"] != "issue_done" || p["issue_id"] != target.ID {
		t.Fatalf("predicate = %v, want issue_done/%s", p, target.ID)
	}
	if issueSuspendedAtForTest(t, waitingID) {
		t.Fatal("auto-resume did not clear the snapshot")
	}
	// The system actor records the resume on the audit twin only; member
	// actors are the ones who land in activity_log.
	if n := countSuspensionAuditForTest(t, waitingID, "issue.suspension_resumed"); n != 1 {
		t.Fatalf("issue.suspension_resumed audit rows = %d, want 1", n)
	}
	// Decision 3: auto-resume also auto-restarts scheduling — the agent
	// assignee is woken directly.
	if n := taskCountFor(t, waitingID, agentID); n != 1 {
		t.Fatalf("wake tasks after auto-resume = %d, want 1", n)
	}
}

// AC (machine predicates are a closed set): an external wait NEVER
// auto-resumes — the target completing does not satisfy a wait that was not
// declared on it.
func TestExternalWaitNeverAutoResumes(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	issueID := createCommentTriggerPreviewIssue(t, "external wait seed", "", "")
	unrelated := createIssueForTest(t, map[string]any{"title": "unrelated target", "status": "todo"})

	code, resp := suspendIssueForTest(t, issueID, map[string]any{
		"reason":           "waiting on the vendor",
		"waiting_on_type":  "external",
		"resume_condition": "vendor replies with the quota",
	})
	if code != http.StatusCreated {
		t.Fatalf("suspend: expected 201, got %d", code)
	}

	updateIssueForTest(t, unrelated.ID, map[string]any{"status": "done"})

	status, predicate := suspensionStateForTest(t, resp.Suspension.ID)
	if status != "active" {
		t.Fatalf("external wait moved to %q, want active (never auto-resumes)", status)
	}
	if predicate != "{}" {
		t.Fatalf("external wait grew a predicate %q, want none", predicate)
	}
	if !issueSuspendedAtForTest(t, issueID) {
		t.Fatal("snapshot cleared without a release")
	}
}

// AC (machine predicate decision_answered): answering the card resumes the
// waiting issue; CANCELLING the card never does — a withdrawn question
// grants nothing.
func TestDecisionAnswerResumesCancelDoesNot(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	// Two independent waiting issues: one waits on a card that gets
	// CANCELLED (must stay suspended), one on a card that gets ANSWERED
	// (must auto-resume). A cancelled card withdraws the question without
	// granting it, so it never fires decision_answered.
	cancelWaitID := createCommentTriggerPreviewIssue(t, "decision cancel wait", "", "")
	answerWaitID := createCommentTriggerPreviewIssue(t, "decision answer wait", "", "")
	cardIssue := newDecisionFixture(t)

	w := createDecisionCard(t, cardIssue, true, map[string]any{
		"question": "which storage?",
		"options":  []string{"postgres", "sqlite"},
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create card: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	card := decodeDecision(t, w)

	code, resp := suspendIssueForTest(t, cancelWaitID, map[string]any{
		"reason":                 "waiting on the card",
		"waiting_on_type":        "decision",
		"waiting_on_decision_id": card.ID,
	})
	if code != http.StatusCreated {
		t.Fatalf("suspend: expected 201, got %d", code)
	}

	// Cancel arm: the row must stay active.
	cw := httptest.NewRecorder()
	creq := newRequest(http.MethodPost, "/api/issues/"+cardIssue.IssueID+"/decisions/"+card.ID+"/cancel", nil)
	creq = withURLParams(creq, "id", cardIssue.IssueID, "decisionId", card.ID)
	testHandler.CancelIssueDecision(cw, creq)
	if cw.Code != http.StatusOK {
		t.Fatalf("cancel card: expected 200, got %d", cw.Code)
	}
	status, _ := suspensionStateForTest(t, resp.Suspension.ID)
	if status != "active" {
		t.Fatalf("cancelled card resumed the wait (status %q), want active", status)
	}
	if !issueSuspendedAtForTest(t, cancelWaitID) {
		t.Fatal("cancel-arm snapshot cleared without a release")
	}

	// Answer arm on the second waiting issue: answering resumes.
	w2 := createDecisionCard(t, cardIssue, true, map[string]any{
		"question": "which retry policy?",
		"options":  []string{"exponential", "fixed"},
	})
	if w2.Code != http.StatusCreated {
		t.Fatalf("create second card: expected 201, got %d", w2.Code)
	}
	card2 := decodeDecision(t, w2)
	code, resp2 := suspendIssueForTest(t, answerWaitID, map[string]any{
		"reason":                 "waiting on the second card",
		"waiting_on_type":        "decision",
		"waiting_on_decision_id": card2.ID,
	})
	if code != http.StatusCreated {
		t.Fatalf("re-suspend: expected 201, got %d", code)
	}

	aw := httptest.NewRecorder()
	areq := newRequest(http.MethodPost, "/api/issues/"+cardIssue.IssueID+"/decisions/"+card2.ID+"/answer", map[string]any{
		"selected_indices": []int{0},
	})
	areq = withURLParams(areq, "id", cardIssue.IssueID, "decisionId", card2.ID)
	testHandler.AnswerIssueDecision(aw, areq)
	if aw.Code != http.StatusOK {
		t.Fatalf("answer card: expected 200, got %d: %s", aw.Code, aw.Body.String())
	}

	status, predicate := suspensionStateForTest(t, resp2.Suspension.ID)
	if status != "resumed" {
		t.Fatalf("row status after answer = %q, want resumed", status)
	}
	var p map[string]any
	if err := json.Unmarshal([]byte(predicate), &p); err != nil {
		t.Fatalf("decode predicate: %v", err)
	}
	if p["kind"] != "decision_answered" || p["decision_id"] != card2.ID {
		t.Fatalf("predicate = %v, want decision_answered/%s", p, card2.ID)
	}
	if issueSuspendedAtForTest(t, answerWaitID) {
		t.Fatal("decision auto-resume did not clear the snapshot")
	}
	if !issueSuspendedAtForTest(t, cancelWaitID) {
		t.Fatal("cancel-arm wait lost its snapshot")
	}
}

// AC (manual arms): manual resume wakes the assignee (decision 3), cancel
// does not (nothing granted → nothing resumed); a second release on the same
// row loses the CAS with 409.
func TestManualResumeWakesCancelDoesNotCASConflicts(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	agentID := seededReadyAgentID(t)
	wakeID := createCommentTriggerPreviewIssue(t, "manual wake seed", "agent", agentID)
	cancelID := createCommentTriggerPreviewIssue(t, "manual cancel seed", "agent", agentID)

	// Wake arm: resume enqueues exactly one task for the idle assignee.
	code, resp := suspendIssueForTest(t, wakeID, map[string]any{
		"reason":           "holding pattern",
		"waiting_on_type":  "manual",
		"resume_condition": "owner says go",
	})
	if code != http.StatusCreated {
		t.Fatalf("suspend: expected 201, got %d", code)
	}
	if c := releaseSuspensionForTest(t, wakeID, resp.Suspension.ID, "resume", map[string]any{"note": "go"}); c != http.StatusOK {
		t.Fatalf("resume: expected 200, got %d", c)
	}
	if n := taskCountFor(t, wakeID, agentID); n != 1 {
		t.Fatalf("wake tasks after manual resume = %d, want 1", n)
	}
	// CAS loser: resuming the same (now resumed) row conflicts.
	if c := releaseSuspensionForTest(t, wakeID, resp.Suspension.ID, "resume", nil); c != http.StatusConflict {
		t.Fatalf("double resume: expected 409, got %d", c)
	}

	// Cancel arm: no wake.
	code, resp2 := suspendIssueForTest(t, cancelID, map[string]any{
		"reason":           "mistaken hold",
		"waiting_on_type":  "manual",
		"resume_condition": "never mind",
	})
	if code != http.StatusCreated {
		t.Fatalf("suspend: expected 201, got %d", code)
	}
	if c := releaseSuspensionForTest(t, cancelID, resp2.Suspension.ID, "cancel", nil); c != http.StatusOK {
		t.Fatalf("cancel: expected 200, got %d", c)
	}
	if n := taskCountFor(t, cancelID, agentID); n != 0 {
		t.Fatalf("cancel woke the assignee (%d tasks), want 0", n)
	}
}

// AC (workspace deletion): the app-layer sweep removes the issue's
// suspension rows with the issue (no FK — the CTE in DeleteIssue).
func TestDeleteIssueSweepsSuspensions(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	issue := createIssueForTest(t, map[string]any{"title": "sweep seed", "status": "todo"})
	target := createIssueForTest(t, map[string]any{"title": "sweep target", "status": "todo"})

	code, _ := suspendIssueForTest(t, issue.ID, map[string]any{
		"reason":             "will be swept",
		"waiting_on_type":    "issue",
		"waiting_on_issue_id": target.ID,
	})
	if code != http.StatusCreated {
		t.Fatalf("suspend: expected 201, got %d", code)
	}

	w := httptest.NewRecorder()
	req := withURLParam(newRequest(http.MethodDelete, "/api/issues/"+issue.ID, nil), "id", issue.ID)
	testHandler.DeleteIssue(w, req)
	if w.Code != http.StatusOK && w.Code != http.StatusNoContent {
		t.Fatalf("DeleteIssue: expected 200/204, got %d: %s", w.Code, w.Body.String())
	}
	if n := countSuspensionRowsForTest(t, issue.ID); n != 0 {
		t.Fatalf("issue deletion left %d suspension rows, want 0", n)
	}
}

// AC (aggregate view): the workspace list defaults to active rows, accepts
// ?status=all, and denormalizes the waiting target's current state.
func TestWorkspaceSuspensionAggregate(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	a := createIssueForTest(t, map[string]any{"title": "aggregate active", "status": "todo"})
	b := createIssueForTest(t, map[string]any{"title": "aggregate resumed", "status": "todo"})
	target := createIssueForTest(t, map[string]any{"title": "aggregate target", "status": "todo"})

	suspendIssueForTest(t, a.ID, map[string]any{
		"reason":             "still waiting",
		"waiting_on_type":    "issue",
		"waiting_on_issue_id": target.ID,
	})
	_, respB := suspendIssueForTest(t, b.ID, map[string]any{
		"reason":             "already released",
		"waiting_on_type":    "issue",
		"waiting_on_issue_id": target.ID,
	})
	if c := releaseSuspensionForTest(t, b.ID, respB.Suspension.ID, "resume", nil); c != http.StatusOK {
		t.Fatalf("resume b: expected 200, got %d", c)
	}

	fetch := func(query string) []WorkspaceIssueSuspensionResponse {
		t.Helper()
		w := httptest.NewRecorder()
		req := newRequest(http.MethodGet, "/api/workspaces/"+testWorkspaceID+"/suspensions"+query, nil)
		req = withURLParam(req, "id", testWorkspaceID)
		testHandler.ListWorkspaceIssueSuspensions(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("aggregate list: expected 200, got %d: %s", w.Code, w.Body.String())
		}
		var body struct {
			Suspensions []WorkspaceIssueSuspensionResponse `json:"suspensions"`
			Total       int                                `json:"total"`
		}
		if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
			t.Fatalf("decode aggregate: %v", err)
		}
		return body.Suspensions
	}

	active := fetch("")
	sawA := false
	for _, row := range active {
		if row.IssueID == a.ID {
			sawA = true
			if row.WaitingOnTarget == nil || row.WaitingOnTarget.IssueID != target.ID {
				t.Fatalf("active row missing denormalized target: %+v", row.WaitingOnTarget)
			}
			if row.IssueIdentifier == "" || row.IssueTitle != "aggregate active" {
				t.Fatalf("row issue coordinates = %q/%q", row.IssueIdentifier, row.IssueTitle)
			}
		}
		if row.IssueID == b.ID {
			t.Fatalf("resumed row leaked into the default (active) list: %+v", row)
		}
	}
	if !sawA {
		t.Fatal("active suspension missing from the default list")
	}

	all := fetch("?status=all")
	sawB := false
	for _, row := range all {
		if row.IssueID == b.ID && row.Status == "resumed" {
			sawB = true
		}
	}
	if !sawB {
		t.Fatal("resumed row missing from ?status=all")
	}
}
