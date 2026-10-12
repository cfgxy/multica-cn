package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
)

// Workspace decision inbox (RUYI-494): the aggregation is one row PER CARD,
// issue fields ride along as context, membership gates the whole endpoint.
// AC9 is asserted with a positive control in the same test: a member reads
// the same workspace fine while an outsider is refused, so the denial is
// provably the membership gate (the query itself has no user dimension —
// removing the gate would flip the outsider case to 200).

// insertInboxCard writes one decision card directly. createdAgo controls the
// created_at ordering so "older open card" scenarios are deterministic.
func insertInboxCard(t *testing.T, workspaceID, issueID, question, status, createdAgo string, over ...testutil.Cols) string {
	t.Helper()
	cols := testutil.Cols{
		"workspace_id":        workspaceID,
		"issue_id":            issueID,
		"question":            question,
		"options":             testutil.Raw(`'[{"label":"A"},{"label":"B"}]'::jsonb`),
		"multi_select":        false,
		"recommended_indices": testutil.Raw(`'[]'::jsonb`),
		"status":              status,
		"created_by_type":     "member",
		"created_by_id":       testUserID,
	}
	if createdAgo != "" {
		cols["created_at"] = testutil.Raw(fmt.Sprintf("now() - interval '%s'", createdAgo))
	}
	for _, o := range over {
		for k, v := range o {
			cols[k] = v
		}
	}
	return dbfx.Insert(t, "issue_decisions", cols)
}

func getInbox(t *testing.T, workspaceID, query string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := withURLParam(newRequest("GET", "/api/workspaces/"+workspaceID+"/decision-inbox"+query, nil), "id", workspaceID)
	testHandler.ListWorkspaceDecisionInbox(w, req)
	return w
}

func decodeInbox(t *testing.T, w *httptest.ResponseRecorder) WorkspaceDecisionInboxResponse {
	t.Helper()
	var resp WorkspaceDecisionInboxResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode inbox response: %v", err)
	}
	return resp
}

func TestWorkspaceDecisionInboxList(t *testing.T) {
	issueA := dbfx.Issue(t, "Inbox issue A")
	issueB := dbfx.Issue(t, "Inbox issue B")

	insertInboxCard(t, testWorkspaceID, issueA, "oldest open", "open", "3 hours")
	insertInboxCard(t, testWorkspaceID, issueA, "newer open", "open", "2 hours")
	insertInboxCard(t, testWorkspaceID, issueB, "answered card", "answered", "1 hour",
		testutil.Cols{"selected_indices": testutil.Raw(`'[0]'::jsonb`)})
	insertInboxCard(t, testWorkspaceID, issueB, "cancelled card", "cancelled", "30 minutes")

	w := getInbox(t, testWorkspaceID, "")
	if w.Code != http.StatusOK {
		t.Fatalf("list: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	resp := decodeInbox(t, w)

	if len(resp.Items) != 4 {
		t.Fatalf("items = %d, want 4 (one row per card)", len(resp.Items))
	}
	if resp.Counts.Open != 2 || resp.Counts.Answered != 1 || resp.Counts.Cancelled != 1 {
		t.Fatalf("counts = %+v, want open=2 answered=1 cancelled=1", resp.Counts)
	}

	// AC1/AC11: every row carries the human-readable identifier and title.
	byQuestion := map[string]WorkspaceDecisionInboxItem{}
	for _, item := range resp.Items {
		if item.IssueIdentifier == "" || item.IssueTitle == "" {
			t.Fatalf("card %q: identifier=%q title=%q, want both populated", item.Question, item.IssueIdentifier, item.IssueTitle)
		}
		if item.IssueNumber < 1 {
			t.Fatalf("card %q: issue_number = %d, want a real number", item.Question, item.IssueNumber)
		}
		byQuestion[item.Question] = item
	}
	if got := byQuestion["newer open"]; got.IssueTitle != "Inbox issue A" || got.IssueID != issueA {
		t.Fatalf("newer open card = %+v, want issue A context", got)
	}

	// Newest-first ordering: the 30-minute card leads, the 3-hour card trails.
	if resp.Items[0].Question != "cancelled card" || resp.Items[len(resp.Items)-1].Question != "oldest open" {
		t.Fatalf("ordering broken: first=%q last=%q, want newest→oldest",
			resp.Items[0].Question, resp.Items[len(resp.Items)-1].Question)
	}
}

func TestWorkspaceDecisionInboxSameIssueThreeCards(t *testing.T) {
	// AC2: one issue, three cards, the two oldest still open. Every card is
	// an independent row — the newer answered card must not hide the older
	// open ones.
	issue := dbfx.Issue(t, "Three-card issue")
	insertInboxCard(t, testWorkspaceID, issue, "decision 1 (old open)", "open", "3 hours")
	insertInboxCard(t, testWorkspaceID, issue, "decision 2 (old open)", "open", "2 hours")
	insertInboxCard(t, testWorkspaceID, issue, "decision 3 (new answered)", "answered", "1 hour",
		testutil.Cols{"selected_indices": testutil.Raw(`'[1]'::jsonb`)})

	resp := decodeInbox(t, getInbox(t, testWorkspaceID, ""))

	sameIssue := 0
	openQuestions := map[string]bool{}
	for _, item := range resp.Items {
		if item.IssueID != issue {
			continue
		}
		sameIssue++
		if item.Status == "open" {
			openQuestions[item.Question] = true
		}
	}
	if sameIssue != 3 {
		t.Fatalf("cards for the issue = %d, want 3 independent rows", sameIssue)
	}
	if !openQuestions["decision 1 (old open)"] || !openQuestions["decision 2 (old open)"] {
		t.Fatalf("old open cards missing from aggregation: %v", openQuestions)
	}
}

func TestWorkspaceDecisionInboxStatusFilter(t *testing.T) {
	issue := dbfx.Issue(t, "Filtered issue")
	insertInboxCard(t, testWorkspaceID, issue, "filter open", "open", "1 hour")
	insertInboxCard(t, testWorkspaceID, issue, "filter answered", "answered", "2 hours",
		testutil.Cols{"selected_indices": testutil.Raw(`'[0]'::jsonb`)})

	w := getInbox(t, testWorkspaceID, "?status=open")
	if w.Code != http.StatusOK {
		t.Fatalf("status=open: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	resp := decodeInbox(t, w)
	if len(resp.Items) != 1 || resp.Items[0].Status != "open" {
		t.Fatalf("status=open items = %+v, want only the open card", resp.Items)
	}
	// Counts stay the workspace totals so section headers render real numbers
	// while the list window is filtered.
	if resp.Counts.Open != 1 || resp.Counts.Answered != 1 {
		t.Fatalf("counts = %+v, want totals independent of the filter", resp.Counts)
	}

	if w := getInbox(t, testWorkspaceID, "?status=exploded"); w.Code != http.StatusBadRequest {
		t.Fatalf("invalid status: expected 400, got %d", w.Code)
	}
	if w := getInbox(t, testWorkspaceID, "?limit=0"); w.Code != http.StatusBadRequest {
		t.Fatalf("limit=0: expected 400, got %d", w.Code)
	}
	if w := getInbox(t, testWorkspaceID, "?limit=501"); w.Code != http.StatusBadRequest {
		t.Fatalf("limit=501: expected 400, got %d", w.Code)
	}

	w = getInbox(t, testWorkspaceID, "?limit=1")
	if w.Code != http.StatusOK {
		t.Fatalf("limit=1: expected 200, got %d", w.Code)
	}
	if resp := decodeInbox(t, w); len(resp.Items) != 1 {
		t.Fatalf("limit=1 items = %d, want 1", len(resp.Items))
	}
}

func TestWorkspaceDecisionInboxAnswerSyncsAggregation(t *testing.T) {
	// AC4/AC5 at the data layer: answering through the existing issue-scoped
	// endpoint flips the card in the workspace aggregation too — one status
	// truth, two windows onto it.
	f := newDecisionFixture(t)
	w := createDecisionCard(t, f, false, map[string]any{
		"question": "inbox sync check",
		"options":  []string{"yes", "no"},
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create card: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	card := decodeDecision(t, w)
	if card.Status != "open" {
		t.Fatalf("pre-answer card status = %s, want open", card.Status)
	}

	before := decodeInbox(t, getInbox(t, testWorkspaceID, ""))
	foundOpen := false
	for _, item := range before.Items {
		if item.ID == card.ID {
			foundOpen = item.Status == "open"
		}
	}
	if !foundOpen {
		t.Fatal("fresh card not open in the workspace aggregation")
	}

	// Answer it through the issue-scoped member path.
	w = httptest.NewRecorder()
	req := withURLParams(newRequest("POST", "/api/issues/"+f.IssueID+"/decisions/"+card.ID+"/answer",
		map[string]any{"selected_indices": []int{0}}), "id", f.IssueID, "decisionId", card.ID)
	testHandler.AnswerIssueDecision(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("answer: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	after := decodeInbox(t, getInbox(t, testWorkspaceID, ""))
	synced := false
	for _, item := range after.Items {
		if item.ID != card.ID {
			continue
		}
		synced = true
		if item.Status != "answered" || len(item.SelectedIndices) != 1 || item.SelectedIndices[0] != 0 {
			t.Fatalf("answered card in aggregation = status %s selected %v, want answered [0]", item.Status, item.SelectedIndices)
		}
	}
	if !synced {
		t.Fatal("answered card missing from the aggregation")
	}
	if after.Counts.Open != before.Counts.Open-1 || after.Counts.Answered != before.Counts.Answered+1 {
		t.Fatalf("counts did not move with the answer: before=%+v after=%+v", before.Counts, after.Counts)
	}
}

func TestWorkspaceDecisionInboxMembershipGate(t *testing.T) {
	issue := dbfx.Issue(t, "Membership gate issue")
	insertInboxCard(t, testWorkspaceID, issue, "members only card", "open", "")

	// Positive control: the fixture member reads the same workspace fine.
	if w := getInbox(t, testWorkspaceID, ""); w.Code != http.StatusOK {
		t.Fatalf("member: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// AC9: an authenticated non-member gets the same not-found a missing
	// workspace would produce — no membership, no aggregation, no leak.
	outsider := dbfx.User(t, "Inbox Outsider", "inbox-outsider@example.test")
	w := httptest.NewRecorder()
	req := withURLParam(newRequest("GET", "/api/workspaces/"+testWorkspaceID+"/decision-inbox", nil), "id", testWorkspaceID)
	req.Header.Set("X-User-ID", outsider)
	testHandler.ListWorkspaceDecisionInbox(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("outsider: expected 404, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]string
	_ = json.NewDecoder(w.Body).Decode(&body)
	if body["error"] != "workspace not found" {
		t.Fatalf("outsider error = %q, want workspace not found", body["error"])
	}
}

// Guard the workspace scoping of both queries at the SQL layer: a card from
// another workspace must never appear in this workspace's rows. The
// falsifiable half of AC9 — dropping workspace_id from either query fails
// here.
func TestWorkspaceDecisionInboxNoCrossWorkspaceBleed(t *testing.T) {
	otherWs := dbfx.Workspace(t, "Inbox Other WS", "inbox-other-ws")
	otherIssue := dbfx.Issue(t, "Other workspace issue", testutil.Cols{"workspace_id": otherWs})
	insertInboxCard(t, otherWs, otherIssue, "foreign card", "open", "")

	resp := decodeInbox(t, getInbox(t, testWorkspaceID, ""))
	for _, item := range resp.Items {
		if item.WorkspaceID != testWorkspaceID {
			t.Fatalf("cross-workspace leak: card %q from %s in %s inbox", item.Question, item.WorkspaceID, testWorkspaceID)
		}
	}
}
