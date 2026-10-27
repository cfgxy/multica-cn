package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// RUYI-391: a comment triggers an agent run unless the issue's status category
// refuses it. todo, in_progress, in_review, done and blocked admit runs —
// blocked stays wakeable through comments; backlog and cancelled do not. The
// gate is server-side and sits before every dispatch decision, so these
// tests drive the HTTP handlers (create, reply, preview) against a real
// database and assert on the task queue, not just the response payload.

func setIssueStatusForGateTest(t *testing.T, issueID, status string) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(),
		`UPDATE issue SET status = $2 WHERE id = $1`, issueID, status); err != nil {
		t.Fatalf("set issue status to %s: %v", status, err)
	}
}

func countIssueTasksForGateTest(t *testing.T, issueID string) int {
	t.Helper()
	var n int
	if err := testPool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM agent_task_queue WHERE issue_id = $1`, issueID).Scan(&n); err != nil {
		t.Fatalf("count tasks for issue: %v", err)
	}
	return n
}

func postCommentForGateTest(t *testing.T, issueID string, body map[string]any) (int, CommentResponse) {
	t.Helper()
	w := httptest.NewRecorder()
	r := withURLParam(newRequest(http.MethodPost, "/api/issues/"+issueID+"/comments", body), "id", issueID)
	testHandler.CreateComment(w, r)
	var resp CommentResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode comment response (status %d): %v", w.Code, err)
	}
	return w.Code, resp
}

func gateTestMentionContent(t *testing.T, agentID string) string {
	t.Helper()
	return fmt.Sprintf("[@Gate Agent](mention://agent/%s) please take a look", agentID)
}

// TestCommentStatusGate_ForbiddenStatusesDoNotDispatch covers the core
// acceptance rule: on backlog and cancelled issues a mention comment
// still saves (201) but creates no run, and the author gets an honest
// per-target blocked outcome instead of a silent no-op (MUL-4525 §2). blocked
// is deliberately absent here — it is on the admitted side (Owner-corrected
// semantics); the contrast with these two is pinned in
// TestCommentStatusGate_AdmittedStatusesDispatch and the transition test.
func TestCommentStatusGate_ForbiddenStatusesDoNotDispatch(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	for _, status := range []string{"backlog", "cancelled"} {
		t.Run(status, func(t *testing.T) {
			agentID := createHandlerTestAgent(t, "Gate Forbidden "+status, nil)
			issueID := createCommentTriggerPreviewIssue(t, "gate forbidden "+status, "", "")
			setIssueStatusForGateTest(t, issueID, status)

			code, resp := postCommentForGateTest(t, issueID, map[string]any{
				"content": gateTestMentionContent(t, agentID),
			})
			if code != http.StatusCreated {
				t.Fatalf("CreateComment: expected 201 (comment must save in %s), got %d: %s", status, code, resp.ID)
			}
			if resp.ID == "" {
				t.Fatalf("comment was not saved in %s", status)
			}

			outcome := findCommentOutcome(t, resp.TriggerOutcomes, agentID)
			if outcome.Status != DispatchBlocked || outcome.ReasonCode != ReasonIssueStatusNotDispatchable {
				t.Fatalf("outcome = %+v, want blocked/%s", outcome, ReasonIssueStatusNotDispatchable)
			}
			if n := countIssueTasksForGateTest(t, issueID); n != 0 {
				t.Fatalf("issue in %s has %d tasks after mention comment, want 0", status, n)
			}
		})
	}
}

// TestCommentStatusGate_ThreadReplyDoesNotDispatch extends the gate to the
// reply path: a mention inside a thread reply on a non-admitted issue must not
// dispatch either, while the same reply shape on an admitted issue still does.
func TestCommentStatusGate_ThreadReplyDoesNotDispatch(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "Gate Reply Agent", nil)

	issueID := createCommentTriggerPreviewIssue(t, "gate reply backlog", "", "")
	setIssueStatusForGateTest(t, issueID, "backlog")
	rootID := insertMemberRootCommentForTriggerPreviewTest(t, issueID, "root of the blocked thread")

	code, resp := postCommentForGateTest(t, issueID, map[string]any{
		"content":   gateTestMentionContent(t, agentID),
		"parent_id": rootID,
	})
	if code != http.StatusCreated || resp.ID == "" {
		t.Fatalf("reply comment must save (201), got %d id=%q", code, resp.ID)
	}
	outcome := findCommentOutcome(t, resp.TriggerOutcomes, agentID)
	if outcome.Status != DispatchBlocked || outcome.ReasonCode != ReasonIssueStatusNotDispatchable {
		t.Fatalf("reply outcome = %+v, want blocked/%s", outcome, ReasonIssueStatusNotDispatchable)
	}
	if n := countIssueTasksForGateTest(t, issueID); n != 0 {
		t.Fatalf("backlog reply produced %d tasks, want 0", n)
	}

	// Control: the identical reply on an admitted issue still dispatches, so
	// the assertion above is the gate's work, not the reply shape's.
	admittedID := createCommentTriggerPreviewIssue(t, "gate reply todo", "", "")
	admittedRootID := insertMemberRootCommentForTriggerPreviewTest(t, admittedID, "root of the allowed thread")
	code, resp = postCommentForGateTest(t, admittedID, map[string]any{
		"content":   gateTestMentionContent(t, agentID),
		"parent_id": admittedRootID,
	})
	if code != http.StatusCreated {
		t.Fatalf("admitted reply: expected 201, got %d", code)
	}
	outcome = findCommentOutcome(t, resp.TriggerOutcomes, agentID)
	if outcome.Status != DispatchQueued {
		t.Fatalf("admitted reply outcome = %+v, want queued", outcome)
	}
	if n := countIssueTasksForGateTest(t, admittedID); n != 1 {
		t.Fatalf("admitted reply produced %d tasks, want 1", n)
	}
}

// TestCommentStatusGate_AdmittedStatusesDispatch pins the whitelist: every
// admitted status — the execution statuses plus blocked, which must stay
// wakeable through comments (Owner-corrected semantics) — keeps dispatch
// working through an ordinary mention comment.
func TestCommentStatusGate_AdmittedStatusesDispatch(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	for _, status := range []string{"todo", "in_progress", "in_review", "done", "blocked"} {
		t.Run(status, func(t *testing.T) {
			agentID := createHandlerTestAgent(t, "Gate Admitted "+status, nil)
			issueID := createCommentTriggerPreviewIssue(t, "gate admitted "+status, "", "")
			setIssueStatusForGateTest(t, issueID, status)

			code, resp := postCommentForGateTest(t, issueID, map[string]any{
				"content": gateTestMentionContent(t, agentID),
			})
			if code != http.StatusCreated {
				t.Fatalf("CreateComment: expected 201, got %d: %s", code, resp.ID)
			}
			outcome := findCommentOutcome(t, resp.TriggerOutcomes, agentID)
			if outcome.Status != DispatchQueued {
				t.Fatalf("outcome = %+v, want queued in %s", outcome, status)
			}
			if n := countIssueTasksForGateTest(t, issueID); n != 1 {
				t.Fatalf("issue in %s has %d tasks after mention comment, want 1", status, n)
			}
		})
	}
}

// TestCommentStatusGate_TransitionTakesEffectImmediately is the regression
// pair from the acceptance criteria: the gate reads the status at dispatch
// time, so backlog -> todo starts dispatching and todo -> cancelled stops,
// each on the very next comment. The tail legs pin the Owner-corrected
// contrast on the SAME issue: -> blocked dispatches again (the unblock-by-
// comment channel), and -> backlog shuts it once more.
func TestCommentStatusGate_TransitionTakesEffectImmediately(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "Gate Transition Agent", nil)
	issueID := createCommentTriggerPreviewIssue(t, "gate transition", "", "")
	setIssueStatusForGateTest(t, issueID, "backlog")

	code, resp := postCommentForGateTest(t, issueID, map[string]any{
		"content": gateTestMentionContent(t, agentID),
	})
	if code != http.StatusCreated {
		t.Fatalf("backlog comment: expected 201, got %d", code)
	}
	if n := countIssueTasksForGateTest(t, issueID); n != 0 {
		t.Fatalf("backlog comment produced %d tasks, want 0", n)
	}

	setIssueStatusForGateTest(t, issueID, "todo")
	code, resp = postCommentForGateTest(t, issueID, map[string]any{
		"content": gateTestMentionContent(t, agentID),
	})
	if code != http.StatusCreated {
		t.Fatalf("todo comment: expected 201, got %d", code)
	}
	outcome := findCommentOutcome(t, resp.TriggerOutcomes, agentID)
	if outcome.Status != DispatchQueued {
		t.Fatalf("post-transition outcome = %+v, want queued", outcome)
	}
	if n := countIssueTasksForGateTest(t, issueID); n != 1 {
		t.Fatalf("todo comment produced %d tasks, want 1", n)
	}

	// Flip to cancelled without the issue handler's cascade (raw SQL fixture
	// write): the queued task from the todo comment survives, but the next
	// comment must create NO additional run and report the status block.
	setIssueStatusForGateTest(t, issueID, "cancelled")
	code, resp = postCommentForGateTest(t, issueID, map[string]any{
		"content": gateTestMentionContent(t, agentID),
	})
	if code != http.StatusCreated {
		t.Fatalf("cancelled comment: expected 201, got %d", code)
	}
	outcome = findCommentOutcome(t, resp.TriggerOutcomes, agentID)
	if outcome.Status != DispatchBlocked || outcome.ReasonCode != ReasonIssueStatusNotDispatchable {
		t.Fatalf("post-cancel outcome = %+v, want blocked/%s", outcome, ReasonIssueStatusNotDispatchable)
	}
	if n := countIssueTasksForGateTest(t, issueID); n != 1 {
		t.Fatalf("cancelled comment changed task count to %d, want 1 (no new run)", n)
	}

	// Same issue flips to blocked: the next mention comment dispatches again —
	// this is the unblock-by-comment channel the blocked status exists for.
	// The todo-era row is terminalized first (fixture write standing in for
	// the RUYI-384 cascade that a real -> cancelled flip runs), so the blocked
	// comment exercises a FRESH enqueue instead of coalescing into the
	// still-pending trigger from the todo leg.
	if _, err := testPool.Exec(context.Background(),
		`UPDATE agent_task_queue SET status = 'cancelled' WHERE issue_id = $1 AND status = 'queued'`, issueID); err != nil {
		t.Fatalf("terminalize todo-era task: %v", err)
	}
	setIssueStatusForGateTest(t, issueID, "blocked")
	code, resp = postCommentForGateTest(t, issueID, map[string]any{
		"content": gateTestMentionContent(t, agentID),
	})
	if code != http.StatusCreated {
		t.Fatalf("blocked comment: expected 201, got %d", code)
	}
	outcome = findCommentOutcome(t, resp.TriggerOutcomes, agentID)
	if outcome.Status != DispatchQueued {
		t.Fatalf("blocked-status outcome = %+v, want queued", outcome)
	}
	if n := countIssueTasksForGateTest(t, issueID); n != 2 {
		t.Fatalf("blocked comment changed task count to %d, want 2 (cancelled-era row + new run)", n)
	}

	// And back to backlog: the very next comment is refused again, so the
	// blocked admission above is the whitelist's work, not drift.
	setIssueStatusForGateTest(t, issueID, "backlog")
	code, resp = postCommentForGateTest(t, issueID, map[string]any{
		"content": gateTestMentionContent(t, agentID),
	})
	if code != http.StatusCreated {
		t.Fatalf("backlog-again comment: expected 201, got %d", code)
	}
	outcome = findCommentOutcome(t, resp.TriggerOutcomes, agentID)
	if outcome.Status != DispatchBlocked || outcome.ReasonCode != ReasonIssueStatusNotDispatchable {
		t.Fatalf("backlog-again outcome = %+v, want blocked/%s", outcome, ReasonIssueStatusNotDispatchable)
	}
	if n := countIssueTasksForGateTest(t, issueID); n != 2 {
		t.Fatalf("backlog-again comment changed task count to %d, want 2 (no new run)", n)
	}
}

// TestCommentStatusGate_CustomStatusFollowsCategory pins the category-based
// judgment: a custom status inherits its category's dispatch behavior, so the
// gate never needs to know custom keys.
func TestCommentStatusGate_CustomStatusFollowsCategory(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	for _, tc := range []struct {
		key, category string
		wantDispatch  bool
	}{
		{key: "gatepark", category: "backlog", wantDispatch: false},
		{key: "gatego", category: "todo", wantDispatch: true},
		// A custom status in the blocked category must dispatch like blocked
		// itself: the category, not the key, carries the wakeable semantics.
		{key: "gatewait", category: "blocked", wantDispatch: true},
	} {
		t.Run(tc.key, func(t *testing.T) {
			if _, err := testPool.Exec(ctx, `
				INSERT INTO issue_status (workspace_id, key, name, category, color, is_system, position)
				VALUES ($1, $2, $3, $4, '#058dfa', FALSE, 100)
			`, testWorkspaceID, tc.key, "Gate "+tc.key, tc.category); err != nil {
				t.Fatalf("insert custom status %s: %v", tc.key, err)
			}
			t.Cleanup(func() {
				testPool.Exec(context.Background(),
					`DELETE FROM issue_status WHERE workspace_id = $1 AND key = $2`, testWorkspaceID, tc.key)
			})

			agentID := createHandlerTestAgent(t, "Gate Custom "+tc.key, nil)
			issueID := createCommentTriggerPreviewIssue(t, "gate custom "+tc.key, "", "")
			setIssueStatusForGateTest(t, issueID, tc.key)

			code, resp := postCommentForGateTest(t, issueID, map[string]any{
				"content": gateTestMentionContent(t, agentID),
			})
			if code != http.StatusCreated {
				t.Fatalf("CreateComment: expected 201, got %d", code)
			}
			outcome := findCommentOutcome(t, resp.TriggerOutcomes, agentID)
			if n := countIssueTasksForGateTest(t, issueID); tc.wantDispatch {
				if outcome.Status != DispatchQueued || n != 1 {
					t.Fatalf("custom status %s (category %s): outcome %+v tasks %d, want queued/1", tc.key, tc.category, outcome, n)
				}
			} else {
				if outcome.Status != DispatchBlocked || outcome.ReasonCode != ReasonIssueStatusNotDispatchable || n != 0 {
					t.Fatalf("custom status %s (category %s): outcome %+v tasks %d, want blocked/0", tc.key, tc.category, outcome, n)
				}
			}
		})
	}
}

// TestPreviewCommentTriggers_StatusGate keeps the composer honest: on a
// non-admitted issue the preview shows the target as blocked with the status
// reason instead of listing it under agents, so the submit-time block is never
// a surprise.
func TestPreviewCommentTriggers_StatusGate(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "Gate Preview Agent", nil)
	issueID := createCommentTriggerPreviewIssue(t, "gate preview backlog", "", "")
	setIssueStatusForGateTest(t, issueID, "backlog")

	preview := previewCommentTriggersForTest(t, issueID, map[string]any{
		"content": gateTestMentionContent(t, agentID),
	})
	if len(preview.Agents) != 0 {
		t.Fatalf("backlog preview agents = %+v, want none", preview.Agents)
	}
	found := false
	for _, b := range preview.Blocked {
		if b.TargetID == agentID && b.Status == DispatchBlocked && b.ReasonCode == ReasonIssueStatusNotDispatchable {
			found = true
		}
	}
	if !found {
		t.Fatalf("backlog preview blocked = %+v, want agent %s blocked/%s", preview.Blocked, agentID, ReasonIssueStatusNotDispatchable)
	}

	setIssueStatusForGateTest(t, issueID, "todo")
	preview = previewCommentTriggersForTest(t, issueID, map[string]any{
		"content": gateTestMentionContent(t, agentID),
	})
	if len(preview.Agents) != 1 {
		t.Fatalf("todo preview agents = %+v, want the agent", preview.Agents)
	}

	// blocked must read the same on both sides of the submit boundary: the
	// preview lists the agent (the issue is wakeable, nothing to warn about)
	// and the submit really queues — preview and submit cannot disagree.
	setIssueStatusForGateTest(t, issueID, "blocked")
	preview = previewCommentTriggersForTest(t, issueID, map[string]any{
		"content": gateTestMentionContent(t, agentID),
	})
	if len(preview.Agents) != 1 {
		t.Fatalf("blocked preview agents = %+v, want the agent (blocked is wakeable)", preview.Agents)
	}
	if len(preview.Blocked) != 0 {
		t.Fatalf("blocked preview blocked = %+v, want none", preview.Blocked)
	}
	code, resp := postCommentForGateTest(t, issueID, map[string]any{
		"content": gateTestMentionContent(t, agentID),
	})
	if code != http.StatusCreated {
		t.Fatalf("blocked submit: expected 201, got %d", code)
	}
	if outcome := findCommentOutcome(t, resp.TriggerOutcomes, agentID); outcome.Status != DispatchQueued {
		t.Fatalf("blocked submit outcome = %+v, want queued (preview/submit consistency)", outcome)
	}
	if n := countIssueTasksForGateTest(t, issueID); n != 1 {
		t.Fatalf("blocked submit produced %d tasks, want 1", n)
	}
}

// TestCommentStatusGate_BlockedThreadReplyDispatches pins the wakeable path
// end to end: a mention inside a thread REPLY on a blocked issue dispatches
// exactly like on todo — unblocking through a corrective reply is part of
// what the blocked status is for, so the gate must not swallow it.
func TestCommentStatusGate_BlockedThreadReplyDispatches(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "Gate Blocked Reply Agent", nil)

	issueID := createCommentTriggerPreviewIssue(t, "gate reply blocked", "", "")
	setIssueStatusForGateTest(t, issueID, "blocked")
	rootID := insertMemberRootCommentForTriggerPreviewTest(t, issueID, "root of the blocked thread")

	code, resp := postCommentForGateTest(t, issueID, map[string]any{
		"content":   gateTestMentionContent(t, agentID),
		"parent_id": rootID,
	})
	if code != http.StatusCreated || resp.ID == "" {
		t.Fatalf("blocked reply comment must save (201), got %d id=%q", code, resp.ID)
	}
	outcome := findCommentOutcome(t, resp.TriggerOutcomes, agentID)
	if outcome.Status != DispatchQueued {
		t.Fatalf("blocked reply outcome = %+v, want queued", outcome)
	}
	if n := countIssueTasksForGateTest(t, issueID); n != 1 {
		t.Fatalf("blocked reply produced %d tasks, want 1", n)
	}
}
