package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// --- pure validation -------------------------------------------------------

func TestValidateDecisionInput(t *testing.T) {
	t.Run("accepts two options", func(t *testing.T) {
		options, recs, err := validateDecisionInput("Which DB?", []string{"Postgres", "SQLite"}, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(options) != 2 || recs == nil || len(recs) != 0 {
			t.Fatalf("got options=%v recs=%v, want 2 options and empty recs", options, recs)
		}
	})
	t.Run("accepts four options with recommendations", func(t *testing.T) {
		_, recs, err := validateDecisionInput("Pick", []string{"a", "b", "c", "d"}, []int{1, 3})
		if err != nil || len(recs) != 2 {
			t.Fatalf("got recs=%v err=%v, want 2 recommendations accepted", recs, err)
		}
	})
	t.Run("rejects fewer than two options", func(t *testing.T) {
		for _, n := range []int{0, 1} {
			labels := make([]string, n)
			for i := range labels {
				labels[i] = fmt.Sprintf("opt%d", i)
			}
			if _, _, err := validateDecisionInput("q", labels, nil); err == nil {
				t.Fatalf("%d options: expected rejection", n)
			}
		}
	})
	t.Run("rejects more than four options", func(t *testing.T) {
		if _, _, err := validateDecisionInput("q", []string{"a", "b", "c", "d", "e"}, nil); err == nil {
			t.Fatal("5 options: expected rejection")
		}
	})
	t.Run("rejects empty question", func(t *testing.T) {
		if _, _, err := validateDecisionInput("  ", []string{"a", "b"}, nil); err == nil {
			t.Fatal("empty question: expected rejection")
		}
	})
	t.Run("rejects empty and duplicate labels", func(t *testing.T) {
		if _, _, err := validateDecisionInput("q", []string{"a", "  "}, nil); err == nil {
			t.Fatal("blank label: expected rejection")
		}
		if _, _, err := validateDecisionInput("q", []string{"a", "a"}, nil); err == nil {
			t.Fatal("duplicate labels: expected rejection")
		}
	})
	t.Run("rejects out-of-range recommendation", func(t *testing.T) {
		if _, _, err := validateDecisionInput("q", []string{"a", "b"}, []int{2}); err == nil {
			t.Fatal("recommendation index 2 of 2 options: expected rejection")
		}
	})
	t.Run("question is sanitized of NUL bytes", func(t *testing.T) {
		options, _, err := validateDecisionInput("q\x00?", []string{"a", "b"}, nil)
		if err != nil || options == nil {
			t.Fatalf("NUL-containing question rejected: %v", err)
		}
	})
}

func TestValidateDecisionAnswer(t *testing.T) {
	t.Run("empty selection rejected", func(t *testing.T) {
		if err := validateDecisionAnswer(2, false, nil); err == nil {
			t.Fatal("empty selection: expected rejection")
		}
	})
	t.Run("single-select takes exactly one", func(t *testing.T) {
		if err := validateDecisionAnswer(3, false, []int{0, 1}); err == nil {
			t.Fatal("single-select with two picks: expected rejection")
		}
		if err := validateDecisionAnswer(3, false, []int{1}); err != nil {
			t.Fatalf("single-select with one pick: unexpected error %v", err)
		}
	})
	t.Run("multi-select takes one up to all", func(t *testing.T) {
		if err := validateDecisionAnswer(3, true, []int{0, 2}); err != nil {
			t.Fatalf("multi-select two picks: unexpected error %v", err)
		}
		if err := validateDecisionAnswer(3, true, []int{0, 1, 2}); err != nil {
			t.Fatalf("multi-select all picks: unexpected error %v", err)
		}
	})
	t.Run("out-of-range and duplicate rejected", func(t *testing.T) {
		if err := validateDecisionAnswer(2, true, []int{2}); err == nil {
			t.Fatal("out-of-range index: expected rejection")
		}
		if err := validateDecisionAnswer(3, true, []int{1, 1}); err == nil {
			t.Fatal("duplicate indices: expected rejection")
		}
		if err := validateDecisionAnswer(2, true, []int{-1}); err == nil {
			t.Fatal("negative index: expected rejection")
		}
	})
}

// --- handler lifecycle (DB-backed) ------------------------------------------

// decisionFixture seeds an issue plus a workspace-invocable agent with a
// running task, so a task-token request classifies as the agent actor
// (resolveActor's task_token branch) and a bare request classifies as the
// member actor.
type decisionFixture struct {
	IssueID string
	AgentID string
	TaskID  string
}

func newDecisionFixture(t *testing.T) decisionFixture {
	t.Helper()
	issueID := dbfx.Issue(t, "decision card fixture")
	agentID := createHandlerTestAgent(t, "decision-card-agent", nil)
	taskID := createHandlerTestTaskForAgentOnIssue(t, agentID, issueID)
	t.Cleanup(func() {
		// issue_decisions has no FK (repo rule), and the echo comment's
		// trigger may enqueue task rows — sweep both before the issue goes.
		testPool.Exec(context.Background(), `DELETE FROM issue_decisions WHERE issue_id = $1`, issueID)
		testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE issue_id = $1`, issueID)
	})
	return decisionFixture{IssueID: issueID, AgentID: agentID, TaskID: taskID}
}

// agentIdentityHeaders mirror what the auth middleware stamps from a mat_
// task token; resolveActor's task_token branch trusts exactly this triple.
func agentIdentityHeaders(req *http.Request, f decisionFixture) *http.Request {
	req.Header.Set("X-Actor-Source", "task_token")
	req.Header.Set("X-Agent-ID", f.AgentID)
	req.Header.Set("X-Task-ID", f.TaskID)
	return req
}

func createDecisionCard(t *testing.T, f decisionFixture, asAgent bool, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues/"+f.IssueID+"/decisions", body)
	req = withURLParam(req, "id", f.IssueID)
	if asAgent {
		req = agentIdentityHeaders(req, f)
	}
	testHandler.CreateIssueDecision(w, req)
	return w
}

func decodeDecision(t *testing.T, w *httptest.ResponseRecorder) IssueDecisionResponse {
	t.Helper()
	var resp IssueDecisionResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode decision response: %v", err)
	}
	return resp
}

func TestIssueDecisionCreateValidation(t *testing.T) {
	f := newDecisionFixture(t)

	t.Run("fewer than two options rejected over the API", func(t *testing.T) {
		w := createDecisionCard(t, f, false, map[string]any{"question": "q", "options": []string{"only"}})
		if w.Code != http.StatusBadRequest {
			t.Fatalf("1 option: expected 400, got %d: %s", w.Code, w.Body.String())
		}
	})
	t.Run("more than four options rejected over the API", func(t *testing.T) {
		w := createDecisionCard(t, f, false, map[string]any{"question": "q", "options": []string{"a", "b", "c", "d", "e"}})
		if w.Code != http.StatusBadRequest {
			t.Fatalf("5 options: expected 400, got %d: %s", w.Code, w.Body.String())
		}
	})
	t.Run("agent identity recorded on create", func(t *testing.T) {
		w := createDecisionCard(t, f, true, map[string]any{
			"question": "Ship which day?",
			"options":  []string{"Monday", "Tuesday"},
		})
		if w.Code != http.StatusCreated {
			t.Fatalf("agent create: expected 201, got %d: %s", w.Code, w.Body.String())
		}
		card := decodeDecision(t, w)
		if card.Status != "open" || card.CreatedByType != "agent" || card.CreatedByID != f.AgentID {
			t.Fatalf("card = %+v, want open card created by agent %s", card, f.AgentID)
		}
		if len(card.Options) != 2 || card.Options[0].Label != "Monday" {
			t.Fatalf("options = %+v, want Monday/Tuesday", card.Options)
		}
	})
	t.Run("list returns created cards", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := withURLParam(newRequest("GET", "/api/issues/"+f.IssueID+"/decisions", nil), "id", f.IssueID)
		testHandler.ListIssueDecisions(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("list: expected 200, got %d: %s", w.Code, w.Body.String())
		}
		var cards []IssueDecisionResponse
		if err := json.NewDecoder(w.Body).Decode(&cards); err != nil {
			t.Fatalf("decode list: %v", err)
		}
		if len(cards) < 1 {
			t.Fatal("list returned no cards")
		}
	})
}

func TestIssueDecisionAnswerAuthorization(t *testing.T) {
	f := newDecisionFixture(t)

	w := createDecisionCard(t, f, true, map[string]any{
		"question": "Which option ships?",
		"options":  []string{"Alpha", "Beta", "Gamma"},
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("setup create: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	card := decodeDecision(t, w)

	answer := func(asAgent bool, indices []int) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		req := newRequest("POST", "/api/issues/"+f.IssueID+"/decisions/"+card.ID+"/answer", map[string]any{
			"selected_indices": indices,
		})
		req = withURLParams(req, "id", f.IssueID, "decisionId", card.ID)
		if asAgent {
			req = agentIdentityHeaders(req, f)
		}
		testHandler.AnswerIssueDecision(w, req)
		return w
	}

	t.Run("agent identity is refused for answering", func(t *testing.T) {
		w := answer(true, []int{0})
		if w.Code != http.StatusForbidden {
			t.Fatalf("agent answer: expected 403, got %d: %s", w.Code, w.Body.String())
		}
		var still db.IssueDecision
		if err := testPool.QueryRow(t.Context(), `SELECT status FROM issue_decisions WHERE id = $1`, parseUUID(card.ID)).Scan(&still.Status); err != nil {
			t.Fatalf("reload card: %v", err)
		}
		if still.Status != "open" {
			t.Fatalf("card status = %q after refused agent answer, want open", still.Status)
		}
	})

	t.Run("single-select rejects two picks and out-of-range", func(t *testing.T) {
		if w := answer(false, []int{0, 1}); w.Code != http.StatusBadRequest {
			t.Fatalf("two picks on single-select: expected 400, got %d", w.Code)
		}
		if w := answer(false, []int{5}); w.Code != http.StatusBadRequest {
			t.Fatalf("out-of-range pick: expected 400, got %d", w.Code)
		}
	})

	t.Run("member answer succeeds and echoes a mention comment", func(t *testing.T) {
		w := answer(false, []int{1})
		if w.Code != http.StatusOK {
			t.Fatalf("member answer: expected 200, got %d: %s", w.Code, w.Body.String())
		}
		got := decodeDecision(t, w)
		if got.Status != "answered" || got.AnsweredByType == nil || *got.AnsweredByType != "member" || got.AnsweredByID == nil {
			t.Fatalf("card after answer = %+v, want answered by member", got)
		}
		if len(got.SelectedIndices) != 1 || got.SelectedIndices[0] != 1 {
			t.Fatalf("selected_indices = %v, want [1]", got.SelectedIndices)
		}
		if got.AnswerCommentID == nil {
			t.Fatal("answer comment id not linked")
		}
		var content string
		if err := testPool.QueryRow(t.Context(), `SELECT content FROM comment WHERE id = $1`, parseUUID(*got.AnswerCommentID)).Scan(&content); err != nil {
			t.Fatalf("load echo comment: %v", err)
		}
		if !strings.Contains(content, "mention://agent/"+f.AgentID) {
			t.Fatalf("echo comment lacks agent mention: %q", content)
		}
		if !strings.Contains(content, "Option 2: Beta") {
			t.Fatalf("echo comment lacks chosen option line: %q", content)
		}
	})

	t.Run("second answer conflicts", func(t *testing.T) {
		if w := answer(false, []int{2}); w.Code != http.StatusConflict {
			t.Fatalf("re-answer: expected 409, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("multi-select card accepts multiple picks", func(t *testing.T) {
		w := createDecisionCard(t, f, false, map[string]any{
			"question":     "Which modules?",
			"options":      []string{"API", "Web", "CLI"},
			"multi_select": true,
		})
		if w.Code != http.StatusCreated {
			t.Fatalf("multi create: expected 201, got %d", w.Code)
		}
		multi := decodeDecision(t, w)
		w = httptest.NewRecorder()
		req := newRequest("POST", "/api/issues/"+f.IssueID+"/decisions/"+multi.ID+"/answer", map[string]any{
			"selected_indices": []int{0, 2},
		})
		req = withURLParams(req, "id", f.IssueID, "decisionId", multi.ID)
		testHandler.AnswerIssueDecision(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("multi answer: expected 200, got %d: %s", w.Code, w.Body.String())
		}
		got := decodeDecision(t, w)
		if len(got.SelectedIndices) != 2 || got.SelectedIndices[0] != 0 || got.SelectedIndices[1] != 2 {
			t.Fatalf("multi selected_indices = %v, want [0 2]", got.SelectedIndices)
		}
	})
}

func TestIssueDecisionCancel(t *testing.T) {
	f := newDecisionFixture(t)

	w := createDecisionCard(t, f, false, map[string]any{
		"question": "Cancel me",
		"options":  []string{"yes", "no"},
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("setup create: expected 201, got %d", w.Code)
	}
	card := decodeDecision(t, w)

	cancel := func(asAgent bool) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		req := newRequest("POST", "/api/issues/"+f.IssueID+"/decisions/"+card.ID+"/cancel", map[string]any{})
		req = withURLParams(req, "id", f.IssueID, "decisionId", card.ID)
		if asAgent {
			req = agentIdentityHeaders(req, f)
		}
		testHandler.CancelIssueDecision(w, req)
		return w
	}

	if w := cancel(true); w.Code != http.StatusForbidden {
		t.Fatalf("agent cancel: expected 403, got %d", w.Code)
	}
	if w := cancel(false); w.Code != http.StatusOK {
		t.Fatalf("member cancel: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if w := cancel(false); w.Code != http.StatusConflict {
		t.Fatalf("re-cancel: expected 409, got %d", w.Code)
	}
}

// answerEchoForMemberCard keeps a member-created card mention-free: no agent
// to wake, so the echo is a plain record.
func TestAnswerEchoForMemberCardHasNoMention(t *testing.T) {
	f := newDecisionFixture(t)

	w := createDecisionCard(t, f, false, map[string]any{
		"question": "Member card",
		"options":  []string{"one", "two"},
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("setup create: expected 201, got %d", w.Code)
	}
	card := decodeDecision(t, w)
	if card.CreatedByType != "member" {
		t.Fatalf("created_by_type = %q, want member", card.CreatedByType)
	}

	w = httptest.NewRecorder()
	req := newRequest("POST", "/api/issues/"+f.IssueID+"/decisions/"+card.ID+"/answer", map[string]any{
		"selected_indices": []int{0},
	})
	req = withURLParams(req, "id", f.IssueID, "decisionId", card.ID)
	testHandler.AnswerIssueDecision(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("answer: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	got := decodeDecision(t, w)
	var content string
	if err := testPool.QueryRow(t.Context(), `SELECT content FROM comment WHERE id = $1`, parseUUID(*got.AnswerCommentID)).Scan(&content); err != nil {
		t.Fatalf("load echo comment: %v", err)
	}
	if strings.Contains(content, "mention://agent/") {
		t.Fatalf("member-card echo must not mention an agent: %q", content)
	}
}
