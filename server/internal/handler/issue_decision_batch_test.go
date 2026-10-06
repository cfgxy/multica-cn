package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Batch decision answering (RUYI-471): one request answers several open
// cards, each through the same per-card CAS as the single endpoint; the
// winners share ONE summary echo comment (one mention pipeline pass, so one
// run wakes even when the cards share a creator); per-card failures are
// reported per card and never roll the batch back.

func createAgentCards(t *testing.T, f decisionFixture, n int, question string, options []string, multi bool) []IssueDecisionResponse {
	t.Helper()
	cards := make([]IssueDecisionResponse, 0, n)
	for i := 0; i < n; i++ {
		w := createDecisionCard(t, f, true, map[string]any{
			"question":     fmt.Sprintf("%s #%d", question, i+1),
			"options":      options,
			"multi_select": multi,
		})
		if w.Code != http.StatusCreated {
			t.Fatalf("setup create card %d: expected 201, got %d: %s", i+1, w.Code, w.Body.String())
		}
		cards = append(cards, decodeDecision(t, w))
	}
	return cards
}

func answerBatchRequest(t *testing.T, f decisionFixture, asAgent bool, answers []map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues/"+f.IssueID+"/decisions/answer-batch", map[string]any{"answers": answers})
	req = withURLParam(req, "id", f.IssueID)
	if asAgent {
		req = agentIdentityHeaders(req, f)
	}
	testHandler.AnswerIssueDecisionsBatch(w, req)
	return w
}

func decodeBatchResponse(t *testing.T, w *httptest.ResponseRecorder) BatchAnswerIssueDecisionsResponse {
	t.Helper()
	var resp BatchAnswerIssueDecisionsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode batch response: %v", err)
	}
	return resp
}

func openCardsOf(t *testing.T, issueID string) []db.IssueDecision {
	t.Helper()
	rows, err := testPool.Query(t.Context(),
		`SELECT id, status FROM issue_decisions WHERE issue_id = $1 ORDER BY created_at ASC`, parseUUID(issueID))
	if err != nil {
		t.Fatalf("query cards: %v", err)
	}
	defer rows.Close()
	var out []db.IssueDecision
	for rows.Next() {
		var d db.IssueDecision
		if err := rows.Scan(&d.ID, &d.Status); err != nil {
			t.Fatalf("scan card: %v", err)
		}
		out = append(out, d)
	}
	return out
}

func countComments(t *testing.T, issueID string) int {
	t.Helper()
	var n int
	if err := testPool.QueryRow(t.Context(),
		`SELECT count(*) FROM comment WHERE issue_id = $1`, parseUUID(issueID)).Scan(&n); err != nil {
		t.Fatalf("count comments: %v", err)
	}
	return n
}

func TestIssueDecisionAnswerBatch(t *testing.T) {
	f := newDecisionFixture(t)

	t.Run("agent identity is refused for batch answering", func(t *testing.T) {
		cards := createAgentCards(t, f, 2, "Agent gate", []string{"Alpha", "Beta"}, false)
		w := answerBatchRequest(t, f, true, []map[string]any{
			{"decision_id": cards[0].ID, "selected_indices": []int{0}},
			{"decision_id": cards[1].ID, "selected_indices": []int{1}},
		})
		if w.Code != http.StatusForbidden {
			t.Fatalf("agent batch: expected 403, got %d: %s", w.Code, w.Body.String())
		}
		for _, card := range openCardsOf(t, f.IssueID) {
			if card.Status != "open" {
				t.Fatalf("card %s moved to %s after refused agent batch, want all open", card.ID, card.Status)
			}
		}
	})

	t.Run("empty answers rejected", func(t *testing.T) {
		w := answerBatchRequest(t, f, false, []map[string]any{})
		if w.Code != http.StatusBadRequest {
			t.Fatalf("empty answers: expected 400, got %d", w.Code)
		}
	})

	t.Run("two cards answer with one shared echo and one trigger pass", func(t *testing.T) {
		cards := createAgentCards(t, f, 2, "Ship which", []string{"Alpha", "Beta"}, false)
		w := answerBatchRequest(t, f, false, []map[string]any{
			{"decision_id": cards[0].ID, "selected_indices": []int{0}},
			{"decision_id": cards[1].ID, "selected_indices": []int{1}},
		})
		if w.Code != http.StatusOK {
			t.Fatalf("batch answer: expected 200, got %d: %s", w.Code, w.Body.String())
		}
		resp := decodeBatchResponse(t, w)
		if len(resp.Results) != 2 {
			t.Fatalf("results = %d, want 2", len(resp.Results))
		}
		echoIDs := map[string]bool{}
		for i, r := range resp.Results {
			if r.Status != "answered" || r.Decision == nil {
				t.Fatalf("result %d = %s, want answered with decision", i, r.Status)
			}
			if r.Decision.AnswerCommentID == nil {
				t.Fatalf("result %d lacks echo comment link", i)
			}
			echoIDs[*r.Decision.AnswerCommentID] = true
		}
		if len(echoIDs) != 1 {
			t.Fatalf("cards link %d distinct echo comments, want exactly one shared echo", len(echoIDs))
		}
		if countComments(t, f.IssueID) != 1 {
			t.Fatalf("comment count = %d, want exactly the one shared echo", countComments(t, f.IssueID))
		}
		var content string
		for echoID := range echoIDs {
			if err := testPool.QueryRow(t.Context(),
				`SELECT content FROM comment WHERE id = $1`, parseUUID(echoID)).Scan(&content); err != nil {
				t.Fatalf("load echo: %v", err)
			}
		}
		if !strings.Contains(content, "mention://agent/"+f.AgentID) {
			t.Fatalf("echo lacks creator mention: %q", content)
		}
		// The fixture's earlier subtests leave two open cards ahead of this
		// batch, so the summary numbers these cards 3 and 4 — the same
		// created_at order the "1A 2B" text protocol binds against.
		if !strings.Contains(content, "决策 3 选 A、决策 4 选 B") {
			t.Fatalf("echo lacks compact summary line: %q", content)
		}
		if !strings.Contains(content, "Ship which #1") || !strings.Contains(content, "Ship which #2") {
			t.Fatalf("echo must list every answered card's question: %q", content)
		}
		if !strings.Contains(content, "Alpha") || !strings.Contains(content, "Beta") {
			t.Fatalf("echo must list every chosen option label: %q", content)
		}
		if len(resp.TriggerOutcomes) != 1 {
			t.Fatalf("trigger outcomes = %d, want exactly one wake for the shared echo", len(resp.TriggerOutcomes))
		}
	})

	t.Run("already-answered card conflicts per card without rolling back", func(t *testing.T) {
		cards := createAgentCards(t, f, 2, "Race", []string{"one", "two"}, false)
		// A concurrent single answer wins card #2 first.
		w := httptest.NewRecorder()
		req := newRequest("POST", "/api/issues/"+f.IssueID+"/decisions/"+cards[1].ID+"/answer", map[string]any{"selected_indices": []int{0}})
		req = withURLParams(req, "id", f.IssueID, "decisionId", cards[1].ID)
		testHandler.AnswerIssueDecision(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("setup single answer: expected 200, got %d", w.Code)
		}

		w = answerBatchRequest(t, f, false, []map[string]any{
			{"decision_id": cards[0].ID, "selected_indices": []int{1}},
			{"decision_id": cards[1].ID, "selected_indices": []int{1}},
		})
		if w.Code != http.StatusOK {
			t.Fatalf("partial batch: expected 200, got %d: %s", w.Code, w.Body.String())
		}
		resp := decodeBatchResponse(t, w)
		if resp.Results[0].Status != "answered" {
			t.Fatalf("card 1 status = %s, want answered", resp.Results[0].Status)
		}
		if resp.Results[1].Status != "conflict" {
			t.Fatalf("card 2 status = %s, want conflict", resp.Results[1].Status)
		}
		// The shared echo lists only the winner.
		if resp.Results[0].Decision == nil || resp.Results[0].Decision.AnswerCommentID == nil {
			t.Fatal("winner lacks echo link")
		}
		var content string
		if err := testPool.QueryRow(t.Context(),
			`SELECT content FROM comment WHERE id = $1`, parseUUID(*resp.Results[0].Decision.AnswerCommentID)).Scan(&content); err != nil {
			t.Fatalf("load echo: %v", err)
		}
		// Race #1 is open card 3 in creation order (the earlier subtests'
		// cards hold 1-2), so the summary line reads 决策 3 选 B.
		if !strings.Contains(content, "决策 3 选 B") || strings.Contains(content, "Race #2") {
			t.Fatalf("echo must summarize only the answered card: %q", content)
		}
	})

	t.Run("invalid selection is a per-card failure", func(t *testing.T) {
		cards := createAgentCards(t, f, 2, "Validate", []string{"one", "two"}, false)
		w := answerBatchRequest(t, f, false, []map[string]any{
			{"decision_id": cards[0].ID, "selected_indices": []int{0}},
			{"decision_id": cards[1].ID, "selected_indices": []int{5}},
		})
		if w.Code != http.StatusOK {
			t.Fatalf("batch with invalid card: expected 200 (per-card results), got %d", w.Code)
		}
		resp := decodeBatchResponse(t, w)
		if resp.Results[0].Status != "answered" {
			t.Fatalf("valid card status = %s, want answered", resp.Results[0].Status)
		}
		if resp.Results[1].Status != "invalid" {
			t.Fatalf("invalid card status = %s, want invalid", resp.Results[1].Status)
		}
		var stillOpen string
		if err := testPool.QueryRow(t.Context(),
			`SELECT status FROM issue_decisions WHERE id = $1`, parseUUID(cards[1].ID)).Scan(&stillOpen); err != nil {
			t.Fatalf("reload invalid card: %v", err)
		}
		if stillOpen != "open" {
			t.Fatalf("invalid card status = %s, want untouched open", stillOpen)
		}
	})

	t.Run("unknown or foreign card id reported per card", func(t *testing.T) {
		cards := createAgentCards(t, f, 1, "Known", []string{"one", "two"}, false)
		w := answerBatchRequest(t, f, false, []map[string]any{
			{"decision_id": cards[0].ID, "selected_indices": []int{0}},
			{"decision_id": "00000000-0000-0000-0000-000000000000", "selected_indices": []int{0}},
		})
		if w.Code != http.StatusOK {
			t.Fatalf("batch with unknown id: expected 200, got %d", w.Code)
		}
		resp := decodeBatchResponse(t, w)
		if resp.Results[1].Status != "not_found" {
			t.Fatalf("unknown card status = %s, want not_found", resp.Results[1].Status)
		}
	})
}

// TestCommentTextDecisionAnswer drives the member comment path: a comment
// whose ENTIRE content is a compact "1A 2B" answer applies to the open cards
// through the batch pipeline. The identity and exact-match filters are the
// misfire guards — each negative case below fails (cards would move) if its
// filter were removed, which is what makes the assertions live.
func TestCommentTextDecisionAnswer(t *testing.T) {
	postComment := func(t *testing.T, f decisionFixture, asAgent bool, content string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		req := newRequest("POST", "/api/issues/"+f.IssueID+"/comments", map[string]any{"content": content})
		req = withURLParam(req, "id", f.IssueID)
		if asAgent {
			req = agentIdentityHeaders(req, f)
		}
		testHandler.CreateComment(w, req)
		return w
	}
	cardStatuses := func(t *testing.T, f decisionFixture) string {
		t.Helper()
		var statuses []string
		for _, c := range openCardsOf(t, f.IssueID) {
			statuses = append(statuses, c.Status)
		}
		return strings.Join(statuses, ",")
	}

	t.Run("member 1A 2B answers both cards with one shared echo", func(t *testing.T) {
		f := newDecisionFixture(t)
		createAgentCards(t, f, 2, "Text path", []string{"Alpha", "Beta"}, false)
		before := countComments(t, f.IssueID)

		w := postComment(t, f, false, "1A 2B")
		if w.Code != http.StatusCreated {
			t.Fatalf("text answer comment: expected 201, got %d: %s", w.Code, w.Body.String())
		}
		if got := cardStatuses(t, f); got != "answered,answered" {
			t.Fatalf("card statuses = %q, want both answered", got)
		}
		echoCount := countComments(t, f.IssueID) - before - 1 // minus the member comment itself
		if echoCount != 1 {
			t.Fatalf("echo comments = %d, want exactly one summary echo", echoCount)
		}
		var echo string
		if err := testPool.QueryRow(t.Context(),
			`SELECT content FROM comment WHERE issue_id = $1 AND author_type = 'member' AND content LIKE '%mention://agent%' ORDER BY created_at DESC LIMIT 1`,
			parseUUID(f.IssueID)).Scan(&echo); err != nil {
			t.Fatalf("load echo: %v", err)
		}
		if !strings.Contains(echo, "Text path #1") || !strings.Contains(echo, "Text path #2") {
			t.Fatalf("echo must bind visibly per card: %q", echo)
		}
		var linked int
		if err := testPool.QueryRow(t.Context(),
			`SELECT count(*) FROM issue_decisions WHERE issue_id = $1 AND answer_comment_id IS NOT NULL`,
			parseUUID(f.IssueID)).Scan(&linked); err != nil {
			t.Fatalf("count linked cards: %v", err)
		}
		if linked != 2 {
			t.Fatalf("cards linked to echo = %d, want 2", linked)
		}
	})

	t.Run("agent-authored token comment never answers (identity filter)", func(t *testing.T) {
		f := newDecisionFixture(t)
		createAgentCards(t, f, 2, "Agent token", []string{"Alpha", "Beta"}, false)

		w := postComment(t, f, true, "1A 2B")
		if w.Code != http.StatusCreated {
			t.Fatalf("agent comment: expected 201, got %d", w.Code)
		}
		if got := cardStatuses(t, f); got != "open,open" {
			t.Fatalf("card statuses = %q after agent token comment, want both open", got)
		}
		if n := countComments(t, f.IssueID); n != 1 {
			t.Fatalf("comment count = %d, want just the agent comment (no echo)", n)
		}
	})

	t.Run("mixed prose never answers (exact match filter)", func(t *testing.T) {
		f := newDecisionFixture(t)
		createAgentCards(t, f, 2, "Prose guard", []string{"Alpha", "Beta"}, false)

		for _, content := range []string{"1A 2B 加急", "决策 1 选 A、决策 2 选 B", "1A 2B（推荐）"} {
			w := postComment(t, f, false, content)
			if w.Code != http.StatusCreated {
				t.Fatalf("prose comment %q: expected 201, got %d", content, w.Code)
			}
		}
		if got := cardStatuses(t, f); got != "open,open" {
			t.Fatalf("card statuses = %q after prose comments, want both open", got)
		}
	})

	t.Run("partial coverage over three open cards refuses the whole line", func(t *testing.T) {
		f := newDecisionFixture(t)
		createAgentCards(t, f, 3, "Coverage", []string{"Alpha", "Beta"}, false)

		w := postComment(t, f, false, "1A 2B")
		if w.Code != http.StatusCreated {
			t.Fatalf("comment: expected 201, got %d", w.Code)
		}
		if got := cardStatuses(t, f); got != "open,open,open" {
			t.Fatalf("card statuses = %q, want all untouched open", got)
		}
		if n := countComments(t, f.IssueID); n != 1 {
			t.Fatalf("comment count = %d, want no echo", n)
		}
	})

	t.Run("no open cards: token-shaped comment is an ordinary comment", func(t *testing.T) {
		f := newDecisionFixture(t)
		w := postComment(t, f, false, "1A 2B")
		if w.Code != http.StatusCreated {
			t.Fatalf("comment: expected 201, got %d", w.Code)
		}
		if n := countComments(t, f.IssueID); n != 1 {
			t.Fatalf("comment count = %d, want just the comment (silent ignore)", n)
		}
	})

	t.Run("multi-select letters answer a multi card", func(t *testing.T) {
		f := newDecisionFixture(t)
		single := createAgentCards(t, f, 1, "Single", []string{"Alpha", "Beta"}, false)
		multi := createAgentCards(t, f, 1, "Multi", []string{"one", "two", "three"}, true)

		w := postComment(t, f, false, "1A 2BC")
		if w.Code != http.StatusCreated {
			t.Fatalf("comment: expected 201, got %d", w.Code)
		}
		var gotMulti, gotSingle []int
		if err := testPool.QueryRow(t.Context(),
			`SELECT selected_indices FROM issue_decisions WHERE id = $1`, parseUUID(multi[0].ID)).Scan(&gotMulti); err != nil {
			t.Fatalf("load multi card: %v", err)
		}
		if !slices.Equal(gotMulti, []int{1, 2}) {
			t.Fatalf("multi selected_indices = %v, want [1 2] (letters B,C)", gotMulti)
		}
		if err := testPool.QueryRow(t.Context(),
			`SELECT selected_indices FROM issue_decisions WHERE id = $1`, parseUUID(single[0].ID)).Scan(&gotSingle); err != nil {
			t.Fatalf("load single card: %v", err)
		}
		if !slices.Equal(gotSingle, []int{0}) {
			t.Fatalf("single selected_indices = %v, want [0]", gotSingle)
		}
	})

	t.Run("member comment does not wake the assignee when the text answer applies", func(t *testing.T) {
		f := newDecisionFixture(t)
		createAgentCards(t, f, 2, "Single run", []string{"Alpha", "Beta"}, false)
		// Assign the issue to a SECOND agent: an unmodified comment would wake
		// it through the assignee fallback; the text answer must not.
		assignee := createHandlerTestAgent(t, "text-answer-assignee", nil)
		testPool.Exec(t.Context(),
			`UPDATE issue SET assignee_type = 'agent', assignee_id = $1 WHERE id = $2`,
			parseUUID(assignee), parseUUID(f.IssueID))
		queued := func() int {
			t.Helper()
			var n int
			testPool.QueryRow(t.Context(),
				`SELECT count(*) FROM agent_task_queue WHERE issue_id = $1 AND agent_id = $2`,
				parseUUID(f.IssueID), parseUUID(assignee)).Scan(&n)
			return n
		}

		// Control: an ordinary member comment DOES reach the assignee, which
		// is what makes the suppression assertion below meaningful.
		w := postComment(t, f, false, "please take a look")
		if w.Code != http.StatusCreated {
			t.Fatalf("control comment: expected 201, got %d", w.Code)
		}
		if n := queued(); n == 0 {
			t.Fatal("control: assignee fallback did not enqueue for a plain comment; the suppression assertion would be vacuous")
		}

		// The token comment applies the answers and must NOT wake the
		// assignee — the only wake is the echo's creator mention.
		w = postComment(t, f, false, "1A 2B")
		if w.Code != http.StatusCreated {
			t.Fatalf("token comment: expected 201, got %d", w.Code)
		}
		if got := cardStatuses(t, f); !strings.Contains(got, "answered") {
			t.Fatalf("card statuses = %q, want answers applied", got)
		}
		if n := queued(); n != 1 {
			t.Fatalf("assignee queue entries = %d, want exactly the control run (token comment must not wake the assignee)", n)
		}
	})
}
