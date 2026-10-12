package handler

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// RUYI-719 feature tests: decision-card answer echoes carry typed answer
// context into the continuation run's claim payload. The echo comment stays
// the human-readable record; claim-time injection adds the question text and
// picked options read from issue_decisions, so the woken run knows the answer
// semantics without re-reading the thread. Batch echoes (RUYI-471) must
// inject EVERY winner; a missing echo pointer degrades explicitly to the
// pre-RUYI-719 behavior.

// continuationTaskForEcho loads the queued task the echo's mention pipeline
// enqueued for the card-creating agent (trigger_comment_id = echo id).
func continuationTaskForEcho(t *testing.T, issueID, echoID string) db.AgentTaskQueue {
	t.Helper()
	var taskID string
	err := testPool.QueryRow(t.Context(), `
		SELECT id FROM agent_task_queue
		WHERE issue_id = $1 AND trigger_comment_id = $2
		ORDER BY created_at DESC
		LIMIT 1
	`, parseUUID(issueID), parseUUID(echoID)).Scan(&taskID)
	if err != nil {
		t.Fatalf("load continuation task for echo %s: %v", echoID, err)
	}
	task, err := testHandler.Queries.GetAgentTask(t.Context(), parseUUID(taskID))
	if err != nil {
		t.Fatalf("GetAgentTask: %v", err)
	}
	return task
}

// claimFixtureTask builds the claim payload for the given task on the task
// agent's own runtime, the same assembly the daemon claim endpoint runs.
func claimFixtureTask(t *testing.T, task db.AgentTaskQueue) AgentTaskResponse {
	t.Helper()
	var runtimeID string
	if err := testPool.QueryRow(t.Context(), `SELECT runtime_id FROM agent WHERE id = $1`, task.AgentID).Scan(&runtimeID); err != nil {
		t.Fatalf("load task agent runtime: %v", err)
	}
	runtime, err := testHandler.Queries.GetAgentRuntimeForWorkspace(t.Context(), db.GetAgentRuntimeForWorkspaceParams{
		ID:          parseUUID(runtimeID),
		WorkspaceID: parseUUID(testWorkspaceID),
	})
	if err != nil {
		t.Fatalf("load runtime: %v", err)
	}
	req := newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+runtimeID+"/tasks/claim", nil, testWorkspaceID, "decision-answer-context")
	resp, _, _, _, failure := testHandler.buildClaimedTaskResponse(req, &task, runtime, runtimeID, testWorkspaceID)
	if failure != nil {
		t.Fatalf("buildClaimedTaskResponse failure: %+v", failure)
	}
	return resp
}

// answerSingleCard answers one open card as a member through the single
// endpoint and returns the refreshed card (with the echo pointer filled).
func answerSingleCard(t *testing.T, f decisionFixture, decisionID string, indices []int) IssueDecisionResponse {
	t.Helper()
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues/"+f.IssueID+"/decisions/"+decisionID+"/answer", map[string]any{
		"selected_indices": indices,
	})
	req = withURLParams(req, "id", f.IssueID, "decisionId", decisionID)
	testHandler.AnswerIssueDecision(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("answer: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	return decodeDecision(t, w)
}

// postMemberComment posts a comment as the fixture member.
func postMemberComment(t *testing.T, f decisionFixture, content string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues/"+f.IssueID+"/comments", map[string]any{"content": content})
	req = withURLParam(req, "id", f.IssueID)
	testHandler.CreateComment(w, req)
	return w
}

func TestClaimInjectsSingleDecisionAnswerContext(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	f := newDecisionFixture(t)

	w := createDecisionCard(t, f, true, map[string]any{
		"question": "Ship which day?",
		"options":  []string{"Monday", "Tuesday", "Wednesday"},
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("setup create: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	card := decodeDecision(t, w)
	answered := answerSingleCard(t, f, card.ID, []int{1})
	if answered.AnswerCommentID == nil || *answered.AnswerCommentID == "" {
		t.Fatal("answer response must carry the echo pointer before the claim can locate the card")
	}

	resp := claimFixtureTask(t, continuationTaskForEcho(t, f.IssueID, *answered.AnswerCommentID))

	if len(resp.DecisionAnswers) != 1 {
		t.Fatalf("decision answers = %d entries, want exactly the one answered card", len(resp.DecisionAnswers))
	}
	da := resp.DecisionAnswers[0]
	if da.DecisionID != card.ID {
		t.Errorf("decision_id = %q, want %q", da.DecisionID, card.ID)
	}
	if da.Question != "Ship which day?" {
		t.Errorf("question = %q, want the original question text", da.Question)
	}
	if da.AnswerSource != "card_click" {
		t.Errorf("answer_source = %q, want card_click", da.AnswerSource)
	}
	if len(da.Selected) != 1 {
		t.Fatalf("selected = %d entries, want 1", len(da.Selected))
	}
	if da.Selected[0].Position != 2 || da.Selected[0].Label != "Tuesday" {
		t.Errorf("selected = %+v, want position 2 (Tuesday)", da.Selected[0])
	}
}

func TestClaimInjectsBatchDecisionAnswerContexts(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	f := newDecisionFixture(t)
	cards := createAgentCards(t, f, 2, "Batch pick", []string{"Alpha", "Beta"}, false)

	w := answerBatchRequest(t, f, false, []map[string]any{
		{"decision_id": cards[0].ID, "selected_indices": []int{0}},
		{"decision_id": cards[1].ID, "selected_indices": []int{1}},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("batch answer: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	batch := decodeBatchResponse(t, w)
	if batch.EchoCommentID == "" {
		t.Fatal("batch answer must share one echo comment")
	}

	// One shared echo, two winners: the claim payload must cover BOTH cards —
	// a single-entry payload here would mean silent card loss (RUYI-471).
	resp := claimFixtureTask(t, continuationTaskForEcho(t, f.IssueID, batch.EchoCommentID))
	if len(resp.DecisionAnswers) != 2 {
		t.Fatalf("decision answers = %d entries, want both batch winners", len(resp.DecisionAnswers))
	}
	byID := make(map[string]DecisionAnswerContextData, len(resp.DecisionAnswers))
	for _, da := range resp.DecisionAnswers {
		byID[da.DecisionID] = da
	}
	for i, want := range []struct {
		cardID   string
		question string
		position int
		label    string
	}{{cards[0].ID, "Batch pick #1", 1, "Alpha"}, {cards[1].ID, "Batch pick #2", 2, "Beta"}} {
		da, ok := byID[want.cardID]
		if !ok {
			t.Fatalf("card %d (%s) missing from injected decision answers", i+1, want.cardID)
		}
		if da.Question != want.question {
			t.Errorf("card %d question = %q, want %q", i+1, da.Question, want.question)
		}
		if len(da.Selected) != 1 || da.Selected[0].Position != want.position || da.Selected[0].Label != want.label {
			t.Errorf("card %d selected = %+v, want position %d (%s)", i+1, da.Selected, want.position, want.label)
		}
	}
}

func TestClaimInjectsTextTokenDecisionAnswerContexts(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	f := newDecisionFixture(t)
	// The "1A 2B" binding is positional over the open cards (created_at ASC),
	// so the two cards created here are answered by sequence, not by id.
	createAgentCards(t, f, 2, "Token pick", []string{"Alpha", "Beta"}, false)

	w := postMemberComment(t, f, "1A 2B")
	if w.Code != http.StatusCreated {
		t.Fatalf("token comment: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var echoID string
	if err := testPool.QueryRow(t.Context(), `
		SELECT answer_comment_id FROM issue_decisions
		WHERE issue_id = $1 AND status = 'answered' AND answer_comment_id IS NOT NULL
		ORDER BY created_at ASC LIMIT 1
	`, parseUUID(f.IssueID)).Scan(&echoID); err != nil {
		t.Fatalf("text-token answer left no echo pointer: %v", err)
	}

	resp := claimFixtureTask(t, continuationTaskForEcho(t, f.IssueID, echoID))
	if len(resp.DecisionAnswers) != 2 {
		t.Fatalf("decision answers = %d entries, want both text-token winners", len(resp.DecisionAnswers))
	}
	for _, da := range resp.DecisionAnswers {
		if da.AnswerSource != "text_token" {
			t.Errorf("card %s answer_source = %q, want text_token", da.DecisionID, da.AnswerSource)
		}
	}
}

func TestClaimDecisionAnswerContextDegradesWithoutEchoPointer(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	f := newDecisionFixture(t)

	w := createDecisionCard(t, f, true, map[string]any{
		"question": "Pointer loss drill",
		"options":  []string{"Keep", "Drop"},
	})
	card := decodeDecision(t, w)
	answered := answerSingleCard(t, f, card.ID, []int{0})
	if answered.AnswerCommentID == nil {
		t.Fatal("expected a filled echo pointer before the simulated linkage failure")
	}

	// Simulate the best-effort linkage losing the race (SetIssueDecisionAnswerComment
	// failure): the card is answered, the echo exists, the pointer is gone. The
	// claim must degrade explicitly to the pre-RUYI-719 behavior — no typed
	// answers injected, echo text still delivered as the trigger content.
	testPool.Exec(t.Context(), `UPDATE issue_decisions SET answer_comment_id = NULL WHERE id = $1`, parseUUID(card.ID))

	resp := claimFixtureTask(t, continuationTaskForEcho(t, f.IssueID, *answered.AnswerCommentID))
	if len(resp.DecisionAnswers) != 0 {
		t.Fatalf("decision answers = %d entries, want the explicit empty degradation", len(resp.DecisionAnswers))
	}
	if !strings.Contains(resp.TriggerCommentContent, "Pointer loss drill") {
		t.Errorf("echo text must still reach the run as the trigger content, got:\n%s", resp.TriggerCommentContent)
	}
}

func TestClaimDecisionAnswerContextIsRetryStable(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	f := newDecisionFixture(t)

	w := createDecisionCard(t, f, true, map[string]any{
		"question": "Retry same answer",
		"options":  []string{"No", "Yes"},
	})
	card := decodeDecision(t, w)
	answered := answerSingleCard(t, f, card.ID, []int{1})

	task := continuationTaskForEcho(t, f.IssueID, *answered.AnswerCommentID)
	first := claimFixtureTask(t, task)
	second := claimFixtureTask(t, task)
	if !reflect.DeepEqual(first.DecisionAnswers, second.DecisionAnswers) {
		t.Errorf("retry claim must yield the identical decision answer context, got %+v vs %+v",
			first.DecisionAnswers, second.DecisionAnswers)
	}
}

func TestClaimDecisionAnswerContextExcludesAuthorizationCards(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	f := newDecisionFixture(t)

	w := createDecisionCard(t, f, true, map[string]any{
		"question": "Plain question",
		"options":  []string{"Left", "Right"},
	})
	card := decodeDecision(t, w)
	answered := answerSingleCard(t, f, card.ID, []int{1})

	// An authorization-kind card must never leak into this question-card
	// surface, even if a row ever carried the echo pointer (its answers have
	// their own RUYI-630 callback semantics).
	authCardID := dbfxInsertAuthorizationCard(t, f, *answered.AnswerCommentID)

	resp := claimFixtureTask(t, continuationTaskForEcho(t, f.IssueID, *answered.AnswerCommentID))
	if len(resp.DecisionAnswers) != 1 {
		t.Fatalf("decision answers = %d entries, want only the question card", len(resp.DecisionAnswers))
	}
	if resp.DecisionAnswers[0].DecisionID == authCardID {
		t.Fatal("authorization card leaked into the question-card answer context")
	}
	if resp.DecisionAnswers[0].Question != "Plain question" {
		t.Errorf("question = %q, want the question card's text", resp.DecisionAnswers[0].Question)
	}
}

// dbfxInsertAuthorizationCard seeds an authorization-kind card row whose
// answer_comment_id points at the given echo, simulating a row shape the
// injection query must refuse by kind.
func dbfxInsertAuthorizationCard(t *testing.T, f decisionFixture, echoID string) string {
	t.Helper()
	var authID string
	err := testPool.QueryRow(t.Context(), `
		INSERT INTO issue_decisions (
			id, workspace_id, issue_id, question, options,
			status, selected_indices, answer_comment_id,
			decision_kind, visible_tier, operator_tier,
			created_by_type, created_by_id
		) VALUES (
			gen_random_uuid(), $1, $2, 'Authorize deploy?', '[]'::jsonb,
			'answered', '[0]'::jsonb, $3,
			'authorization', 'members', 'owner',
			'agent', $4
		) RETURNING id
	`, parseUUID(testWorkspaceID), parseUUID(f.IssueID), parseUUID(echoID), parseUUID(f.AgentID)).Scan(&authID)
	if err != nil {
		t.Fatalf("seed authorization card: %v", err)
	}
	return authID
}
