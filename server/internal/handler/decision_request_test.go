package handler

// Decision-request tests (RUYI-630). Coverage maps to the spec's column-1
// behaviors plus 方案甲's carrier assertions: cross-space dual-row sync,
// read-only origin projections, the target-space Owner's second
// confirmation, and a decision-center detail that never carries issue
// content. The executor tests pin the phase-1 whitelist (workspace_info_read
// + prompt_restore) including the hand-edit guard and the target-workspace
// containment check.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/testutil"
)

// ── helpers ─────────────────────────────────────────────────────────────────

func decisionRequestAuthHeaders(req *http.Request, agentID, taskID string) *http.Request {
	req.Header.Set("X-Actor-Source", "task_token")
	req.Header.Set("X-Agent-ID", agentID)
	req.Header.Set("X-Task-ID", taskID)
	return req
}

// createDecisionRequestFor posts to the agent-facing create endpoint.
// Multi-param requests use the package's withURLParams helper (daemon_test.go),
// which merges into one route context.
func createDecisionRequestFor(t *testing.T, agentID, taskID string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/workspaces/"+testWorkspaceID+"/decision-requests", body)
	req = withURLParams(req, "id", testWorkspaceID)
	req = decisionRequestAuthHeaders(req, agentID, taskID)
	testHandler.CreateDecisionRequest(w, req)
	return w
}

func decodeDecisionRequestDetail(t *testing.T, w *httptest.ResponseRecorder) DecisionRequestDetailResponse {
	t.Helper()
	var resp DecisionRequestDetailResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode decision request response: %v (%s)", err, w.Body.String())
	}
	return resp
}

// getDecisionRequestFor fetches one row's detail as a plain member of the
// given workspace.
func getDecisionRequestFor(t *testing.T, workspaceID, requestID string) (*httptest.ResponseRecorder, DecisionRequestDetailResponse) {
	t.Helper()
	w := httptest.NewRecorder()
	req := newRequest("GET", "/api/workspaces/"+workspaceID+"/decision-requests/"+requestID, nil)
	req = withURLParams(req, "id", workspaceID, "requestId", requestID)
	req.Header.Set("X-Workspace-ID", workspaceID)
	testHandler.GetDecisionRequest(w, req)
	if w.Code != http.StatusOK {
		return w, DecisionRequestDetailResponse{}
	}
	return w, decodeDecisionRequestDetail(t, w)
}

// answerDecisionRequestFor posts a member answer against a row in the given
// workspace, impersonating userID (empty = the fixture owner). The workspace
// header must follow the target row's space — resolution reads the header,
// not the URL param, outside the router.
func answerDecisionRequestFor(t *testing.T, workspaceID, requestID, userID, decision string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/workspaces/"+workspaceID+"/decision-requests/"+requestID+"/answer", map[string]any{
		"decision": decision,
	})
	req = withURLParams(req, "id", workspaceID, "requestId", requestID)
	req.Header.Set("X-Workspace-ID", workspaceID)
	if userID != "" && userID != testUserID {
		req.Header.Set("X-User-ID", userID)
	}
	testHandler.AnswerDecisionRequest(w, req)
	return w
}

func cancelDecisionRequestFor(t *testing.T, workspaceID, requestID string, memberUserID, agentID, taskID string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/workspaces/"+workspaceID+"/decision-requests/"+requestID+"/cancel", nil)
	req = withURLParams(req, "id", workspaceID, "requestId", requestID)
	req.Header.Set("X-Workspace-ID", workspaceID)
	if agentID != "" {
		req = decisionRequestAuthHeaders(req, agentID, taskID)
	} else if memberUserID != "" && memberUserID != testUserID {
		req.Header.Set("X-User-ID", memberUserID)
	}
	testHandler.CancelDecisionRequest(w, req)
	return w
}

var ruyi630WorkspaceSeq atomic.Int64

// secondWorkspaceWithOwner seeds another workspace with the main test user
// as its owner, so one human legitimately holds the second confirmation.
func secondWorkspaceWithOwner(t *testing.T) string {
	t.Helper()
	n := ruyi630WorkspaceSeq.Add(1)
	wsID := dbfx.Workspace(t, fmt.Sprintf("ruyi630-target-%d", n), fmt.Sprintf("ruyi630-target-%d", n))
	dbfx.Member(t, wsID, testUserID, "owner")
	return wsID
}

// seedPromptApplyState puts an agent into "marketplace applied" state so the
// restore executor has something to undo.
func seedPromptApplyState(t *testing.T, agentID, applied, previous string) {
	t.Helper()
	sum := sha256.Sum256([]byte(applied))
	state := fmt.Sprintf(`{"install_id":"i1","version_id":"v1","series_id":"s1","applied_version":1,"applied_content_sha256":%q,"previous_text":%q,"previous_updated_at":"","applied_by":"tester","applied_at":"2026-01-01T00:00:00Z","last_operation_id":"op1","has_restore_point":true,"restored":false}`,
		hex.EncodeToString(sum[:]), previous)
	dbfx.Exec(t, `UPDATE agent SET instructions = $2, marketplace_prompt_state = $3::jsonb WHERE id = $1`, agentID, applied, state)
}

// seedQuestionCard opens a plain question card on the fixture issue.
func seedQuestionCard(t *testing.T, f decisionFixture) IssueDecisionResponse {
	t.Helper()
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues/"+f.IssueID+"/decisions", map[string]any{
		"question": "plain question?", "options": []string{"yes", "no"},
	})
	req = withURLParam(req, "id", f.IssueID)
	req = agentIdentityHeaders(req, f)
	testHandler.CreateIssueDecision(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("seed question card: %d %s", w.Code, w.Body.String())
	}
	return decodeDecision(t, w)
}

func listIssueCards(t *testing.T, issueID string) []map[string]any {
	t.Helper()
	w := httptest.NewRecorder()
	req := withURLParam(newRequest("GET", "/api/issues/"+issueID+"/decisions", nil), "id", issueID)
	testHandler.ListIssueDecisions(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list cards: %d %s", w.Code, w.Body.String())
	}
	var cards []map[string]any
	if err := json.NewDecoder(w.Body).Decode(&cards); err != nil {
		t.Fatalf("decode card list: %v", err)
	}
	return cards
}

func assertCardPending(t *testing.T, cards []map[string]any, cardID string) {
	t.Helper()
	for _, c := range cards {
		if c["id"] == cardID {
			if c["auth_state"] != "pending" || c["status"] != "open" {
				t.Fatalf("authorization card = %v/%v, want pending/open (文字回复与批量通道不得触达授权卡)", c["auth_state"], c["status"])
			}
			return
		}
	}
	t.Fatalf("authorization card %s missing from the issue list", cardID)
}

// ── creation surface ────────────────────────────────────────────────────────

func TestDecisionRequestCreateValidation(t *testing.T) {
	f := newDecisionFixture(t)

	t.Run("member actor refused — agents raise, humans answer", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := newRequest("POST", "/api/workspaces/"+testWorkspaceID+"/decision-requests", map[string]any{
			"action_type": "workspace_info_read", "title": "t",
		})
		req = withURLParam(req, "id", testWorkspaceID)
		testHandler.CreateDecisionRequest(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("member create: expected 403, got %d: %s", w.Code, w.Body.String())
		}
	})
	t.Run("unknown action type refused — registry is a closed set", func(t *testing.T) {
		w := createDecisionRequestFor(t, f.AgentID, f.TaskID, map[string]any{
			"action_type": "billing_payment", "title": "pay please",
		})
		if w.Code != http.StatusBadRequest {
			t.Fatalf("unknown action: expected 400, got %d: %s", w.Code, w.Body.String())
		}
	})
	t.Run("single-space create is operable with read risk tier", func(t *testing.T) {
		w := createDecisionRequestFor(t, f.AgentID, f.TaskID, map[string]any{
			"action_type": "workspace_info_read", "title": "read ws profile",
		})
		if w.Code != http.StatusCreated {
			t.Fatalf("create: expected 201, got %d: %s", w.Code, w.Body.String())
		}
		resp := decodeDecisionRequestDetail(t, w)
		if resp.Request.Operable != true || resp.Request.Role != "origin" || resp.Request.RiskTier != "read" {
			t.Fatalf("row = operable:%v role:%s risk:%s, want operable origin read", resp.Request.Operable, resp.Request.Role, resp.Request.RiskTier)
		}
		if len(resp.Steps) != 1 {
			t.Fatalf("steps = %d, want 1 (single-space)", len(resp.Steps))
		}
		if resp.Request.OperatorTier != "owner" {
			t.Fatalf("default operator tier = %s, want fail-closed owner", resp.Request.OperatorTier)
		}
	})
	t.Run("ttl clamps to the 24h ceiling and 5-minute floor", func(t *testing.T) {
		w := createDecisionRequestFor(t, f.AgentID, f.TaskID, map[string]any{
			"action_type": "workspace_info_read", "title": "ttl ceiling", "ttl_minutes": 1000000,
		})
		resp := decodeDecisionRequestDetail(t, w)
		if until := time.Until(resp.Request.ExpiresAt); until > 24*time.Hour+time.Minute {
			t.Fatalf("expires_at in %s, want <= 24h ceiling", until)
		}
		// Absent, zero, or negative declarations take the 24h default; only a
		// small positive declaration trips the floor.
		w = createDecisionRequestFor(t, f.AgentID, f.TaskID, map[string]any{
			"action_type": "workspace_info_read", "title": "ttl default", "ttl_minutes": -5,
		})
		if w.Code != http.StatusCreated {
			t.Fatalf("default create: expected 201, got %d", w.Code)
		}
		resp = decodeDecisionRequestDetail(t, w)
		if until := time.Until(resp.Request.ExpiresAt); until < 23*time.Hour {
			t.Fatalf("expires_at in %s, want the ~24h default for a non-positive ttl", until)
		}
		w = createDecisionRequestFor(t, f.AgentID, f.TaskID, map[string]any{
			"action_type": "workspace_info_read", "title": "ttl floor", "ttl_minutes": 2,
		})
		resp = decodeDecisionRequestDetail(t, w)
		if until := time.Until(resp.Request.ExpiresAt); until < 4*time.Minute || until > 6*time.Minute {
			t.Fatalf("expires_at in %s, want clamped to ~5m floor", until)
		}
	})
	t.Run("identical button labels refused", func(t *testing.T) {
		w := createDecisionRequestFor(t, f.AgentID, f.TaskID, map[string]any{
			"action_type": "workspace_info_read", "title": "t",
			"approve_label": "OK", "deny_label": "OK",
		})
		if w.Code != http.StatusBadRequest {
			t.Fatalf("identical labels: expected 400, got %d", w.Code)
		}
	})
	t.Run("cross-space create yields operable target row with owner tier", func(t *testing.T) {
		ws2 := secondWorkspaceWithOwner(t)
		w := createDecisionRequestFor(t, f.AgentID, f.TaskID, map[string]any{
			"action_type": "workspace_info_read", "title": "cross-space",
			"target_workspace_id": ws2,
		})
		if w.Code != http.StatusCreated {
			t.Fatalf("cross-space create: expected 201, got %d: %s", w.Code, w.Body.String())
		}
		resp := decodeDecisionRequestDetail(t, w)
		if len(resp.Steps) != 2 {
			t.Fatalf("steps = %d, want 2 (origin projection + target)", len(resp.Steps))
		}
		sawTarget := false
		for _, step := range resp.Steps {
			if step.Role == "target" {
				sawTarget = true
				if !step.Operable || step.OperatorTier != "owner" {
					t.Fatalf("target row operable:%v tier:%s, want operable owner (二次确认)", step.Operable, step.OperatorTier)
				}
			}
		}
		if !sawTarget {
			t.Fatal("no target step in the detail response")
		}
	})
	t.Run("issue reference mints an authorization link card and read-only projection", func(t *testing.T) {
		w := createDecisionRequestFor(t, f.AgentID, f.TaskID, map[string]any{
			"action_type": "workspace_info_read", "title": "card-backed",
			"origin_issue_id": f.IssueID,
			"approve_label":   "准了", "deny_label": "不行",
		})
		if w.Code != http.StatusCreated {
			t.Fatalf("create: expected 201, got %d: %s", w.Code, w.Body.String())
		}
		resp := decodeDecisionRequestDetail(t, w)
		if resp.Card == nil {
			t.Fatal("issue-referenced create: expected authorization link card in response")
		}
		if resp.Card.DecisionKind != "authorization" || resp.Card.AuthState == nil || *resp.Card.AuthState != "pending" {
			t.Fatalf("card kind/auth = %s/%v, want authorization/pending", resp.Card.DecisionKind, resp.Card.AuthState)
		}
		if len(resp.Card.Options) != 2 || resp.Card.Options[0].Label != "准了" || resp.Card.Options[1].Label != "不行" {
			t.Fatalf("card options = %+v, want custom labels baked in", resp.Card.Options)
		}
		if resp.Request.Operable {
			t.Fatal("origin row with issue reference must be a read-only projection (发起空间行只读)")
		}
	})
}

// ── answer gates (规格 column-1 items) ──────────────────────────────────────

func TestDecisionRequestAnswerGates(t *testing.T) {
	f := newDecisionFixture(t)

	makeRow := func(t *testing.T, targetWorkspace string) string {
		t.Helper()
		body := map[string]any{"action_type": "workspace_info_read", "title": "gates"}
		if targetWorkspace != "" {
			body["target_workspace_id"] = targetWorkspace
		}
		w := createDecisionRequestFor(t, f.AgentID, f.TaskID, body)
		if w.Code != http.StatusCreated {
			t.Fatalf("seed create: %d %s", w.Code, w.Body.String())
		}
		return decodeDecisionRequestDetail(t, w).Request.ID
	}

	t.Run("machine credential refused at the answer door", func(t *testing.T) {
		rowID := makeRow(t, "")
		w := httptest.NewRecorder()
		req := newRequest("POST", "/api/workspaces/"+testWorkspaceID+"/decision-requests/"+rowID+"/answer", map[string]any{"decision": "approve"})
		req = withURLParams(req, "id", testWorkspaceID, "requestId", rowID)
		req = decisionRequestAuthHeaders(req, f.AgentID, f.TaskID)
		testHandler.AnswerDecisionRequest(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("agent answer: expected 403, got %d: %s", w.Code, w.Body.String())
		}
	})
	t.Run("non-owner member refused on owner-tier row", func(t *testing.T) {
		user2 := dbfx.User(t, "ruyi630-member2", "ruyi630-member2@test.local")
		dbfx.Member(t, testWorkspaceID, user2, "member")
		rowID := makeRow(t, "")
		w := answerDecisionRequestFor(t, testWorkspaceID, rowID, user2, "approve")
		if w.Code != http.StatusForbidden {
			t.Fatalf("plain member answer on owner tier: expected 403, got %d: %s", w.Code, w.Body.String())
		}
	})
	t.Run("bad decision value refused", func(t *testing.T) {
		rowID := makeRow(t, "")
		w := answerDecisionRequestFor(t, testWorkspaceID, rowID, "", "maybe")
		if w.Code != http.StatusBadRequest {
			t.Fatalf("bad decision: expected 400, got %d", w.Code)
		}
	})
	t.Run("double answer loses the CAS", func(t *testing.T) {
		rowID := makeRow(t, "")
		if w := answerDecisionRequestFor(t, testWorkspaceID, rowID, "", "approve"); w.Code != http.StatusOK {
			t.Fatalf("first answer: expected 200, got %d: %s", w.Code, w.Body.String())
		}
		w := answerDecisionRequestFor(t, testWorkspaceID, rowID, "", "deny")
		if w.Code != http.StatusConflict {
			t.Fatalf("second answer: expected 409, got %d", w.Code)
		}
	})
	t.Run("single-space approval executes workspace_info_read", func(t *testing.T) {
		rowID := makeRow(t, "")
		w := answerDecisionRequestFor(t, testWorkspaceID, rowID, "", "approve")
		if w.Code != http.StatusOK {
			t.Fatalf("approve: expected 200, got %d: %s", w.Code, w.Body.String())
		}
		// The response reports the landed step; execution lands in the same
		// request before it returns, so the follow-up read sees the outcome.
		var step struct {
			Status string `json:"status"`
		}
		if err := json.NewDecoder(w.Body).Decode(&step); err != nil {
			t.Fatalf("decode answer response: %v", err)
		}
		if step.Status != "approved" {
			t.Fatalf("answer response status = %s, want approved (the landed step)", step.Status)
		}
		_, detail := getDecisionRequestFor(t, testWorkspaceID, rowID)
		if detail.Request.Status != "executed" {
			t.Fatalf("row after advance = %s, want executed", detail.Request.Status)
		}
		if detail.Request.ExecutedAt == nil || !strings.Contains(string(detail.Request.ExecutionResult), "workspace") {
			t.Fatalf("execution payload = %v / %s, want executed_at + workspace result",
				detail.Request.ExecutedAt, detail.Request.ExecutionResult)
		}
	})
	t.Run("read-only projection refuses the answer (发起空间行只读)", func(t *testing.T) {
		w := createDecisionRequestFor(t, f.AgentID, f.TaskID, map[string]any{
			"action_type": "workspace_info_read", "title": "projection",
			"origin_issue_id": f.IssueID,
		})
		resp := decodeDecisionRequestDetail(t, w)
		w2 := answerDecisionRequestFor(t, testWorkspaceID, resp.Request.ID, "", "approve")
		if w2.Code != http.StatusForbidden {
			t.Fatalf("projection answer: expected 403, got %d: %s", w2.Code, w2.Body.String())
		}
	})
}

// ── 方案甲: cross-space two-step flow ───────────────────────────────────────

func TestDecisionRequestCrossSpaceTwoStep(t *testing.T) {
	f := newDecisionFixture(t)
	ws2 := secondWorkspaceWithOwner(t)

	seed := func(t *testing.T) (originID, targetID string) {
		t.Helper()
		w := createDecisionRequestFor(t, f.AgentID, f.TaskID, map[string]any{
			"action_type": "workspace_info_read", "title": "two-step",
			"target_workspace_id": ws2,
		})
		if w.Code != http.StatusCreated {
			t.Fatalf("seed: %d %s", w.Code, w.Body.String())
		}
		resp := decodeDecisionRequestDetail(t, w)
		for _, step := range resp.Steps {
			if step.Role == "target" {
				targetID = step.ID
			} else {
				originID = step.ID
			}
		}
		return originID, targetID
	}

	t.Run("origin approval alone does not execute — target owner owes the second confirmation", func(t *testing.T) {
		originID, targetID := seed(t)
		if w := answerDecisionRequestFor(t, testWorkspaceID, originID, "", "approve"); w.Code != http.StatusOK {
			t.Fatalf("origin approve: %d %s", w.Code, w.Body.String())
		}
		_, detail := getDecisionRequestFor(t, ws2, targetID)
		if detail.Request.Status != "pending" {
			t.Fatalf("target status after origin approval = %s, want pending", detail.Request.Status)
		}
		if detail.Request.ExecutedAt != nil {
			t.Fatalf("target executed_at = %v, want nil before second confirmation", detail.Request.ExecutedAt)
		}
	})
	t.Run("target approval completes the group and dual-writes execution truth", func(t *testing.T) {
		originID, targetID := seed(t)
		if w := answerDecisionRequestFor(t, testWorkspaceID, originID, "", "approve"); w.Code != http.StatusOK {
			t.Fatalf("origin approve: %d", w.Code)
		}
		if w := answerDecisionRequestFor(t, ws2, targetID, "", "approve"); w.Code != http.StatusOK {
			t.Fatalf("target approve: %d %s", w.Code, w.Body.String())
		}
		// Same-transaction dual-write: BOTH spaces' rows land on executed
		// with the same execution payload (同事务双写同步).
		for _, tc := range []struct{ ws, id, label string }{
			{testWorkspaceID, originID, "origin"},
			{ws2, targetID, "target"},
		} {
			_, detail := getDecisionRequestFor(t, tc.ws, tc.id)
			if detail.Request.Status != "executed" || detail.Request.ExecutedAt == nil || len(detail.Request.ExecutionResult) == 0 {
				t.Fatalf("%s row status=%s executed_at=%v result=%s, want executed with payload",
					tc.label, detail.Request.Status, detail.Request.ExecutedAt, detail.Request.ExecutionResult)
			}
		}
	})
	t.Run("a denial anywhere settles the whole group as denied", func(t *testing.T) {
		originID, targetID := seed(t)
		if w := answerDecisionRequestFor(t, ws2, targetID, "", "deny"); w.Code != http.StatusOK {
			t.Fatalf("target deny: %d", w.Code)
		}
		_, detail := getDecisionRequestFor(t, testWorkspaceID, originID)
		if detail.Request.Status != "denied" {
			t.Fatalf("origin row status after target denial = %s, want denied (group propagation)", detail.Request.Status)
		}
	})
	t.Run("detail carries title-level references only — never issue content", func(t *testing.T) {
		w := createDecisionRequestFor(t, f.AgentID, f.TaskID, map[string]any{
			"action_type": "workspace_info_read", "title": "detail shape",
			"origin_issue_id": f.IssueID,
		})
		resp := decodeDecisionRequestDetail(t, w)
		raw, err := json.Marshal(resp)
		if err != nil {
			t.Fatal(err)
		}
		var shaped map[string]any
		if err := json.Unmarshal(raw, &shaped); err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"issue_description", "comments", "issue_body", "comment"} {
			if _, ok := shaped[forbidden]; ok {
				t.Fatalf("detail response carries %q — the decision-center detail must never enter Issue content", forbidden)
			}
		}
		if resp.Request.OriginIssueID == nil || *resp.Request.OriginIssueID != f.IssueID {
			t.Fatalf("origin_issue_id = %v, want the title-level reference", resp.Request.OriginIssueID)
		}
	})
}

// ── 文字回复不构成授权 + 批量不含授权卡 ─────────────────────────────────────

func TestDecisionRequestCardChannels(t *testing.T) {
	f := newDecisionFixture(t)

	w := createDecisionRequestFor(t, f.AgentID, f.TaskID, map[string]any{
		"action_type": "workspace_info_read", "title": "auth card",
		"origin_issue_id": f.IssueID,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("seed auth request: %d %s", w.Code, w.Body.String())
	}
	authDetail := decodeDecisionRequestDetail(t, w)
	if authDetail.Card == nil {
		t.Fatal("seed: expected authorization link card")
	}

	t.Run("batch endpoint cannot answer an authorization card", func(t *testing.T) {
		question := seedQuestionCard(t, f)
		w := httptest.NewRecorder()
		req := newRequest("POST", "/api/issues/"+f.IssueID+"/decisions/answer-batch", map[string]any{
			"answers": []map[string]any{
				{"decision_id": question.ID, "selected_indices": []int{0}},
			},
		})
		req = withURLParam(req, "id", f.IssueID)
		testHandler.AnswerIssueDecisionsBatch(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("batch: %d %s", w.Code, w.Body.String())
		}
		var resp BatchAnswerIssueDecisionsResponse
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatal(err)
		}
		if len(resp.Results) != 1 || resp.Results[0].Status != "answered" {
			t.Fatalf("batch results = %+v, want the question card answered", resp.Results)
		}
		if resp.Results[0].Decision.AnswerSource == nil || *resp.Results[0].Decision.AnswerSource != "batch" {
			t.Fatalf("answer_source = %v, want batch", resp.Results[0].Decision.AnswerSource)
		}
		assertCardPending(t, listIssueCards(t, f.IssueID), authDetail.Card.ID)
	})
	t.Run("text reply never constitutes an authorization", func(t *testing.T) {
		// A fresh question card keeps the open set exactly one card, so "1A"
		// is a complete binding for THAT card — and still never touches the
		// authorization card, which is excluded from the text open set.
		seedQuestionCard(t, f)
		w := httptest.NewRecorder()
		req := newRequest("POST", "/api/issues/"+f.IssueID+"/comments", map[string]any{
			"content": "1A", "type": "comment",
		})
		req = withURLParam(req, "id", f.IssueID)
		testHandler.CreateComment(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("comment: %d %s", w.Code, w.Body.String())
		}
		assertCardPending(t, listIssueCards(t, f.IssueID), authDetail.Card.ID)
	})
	t.Run("card click answers the authorization card and drives execution", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := newRequest("POST", "/api/issues/"+f.IssueID+"/decisions/"+authDetail.Card.ID+"/answer", map[string]any{
			"selected_indices": []int{0},
		})
		req = withURLParams(req, "id", f.IssueID, "decisionId", authDetail.Card.ID)
		testHandler.AnswerIssueDecision(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("card answer: %d %s", w.Code, w.Body.String())
		}
		var card IssueDecisionResponse
		if err := json.NewDecoder(w.Body).Decode(&card); err != nil {
			t.Fatal(err)
		}
		if card.AuthState == nil || *card.AuthState != "approved" {
			t.Fatalf("auth_state = %v, want approved", card.AuthState)
		}
		if card.AnswerSource == nil || *card.AnswerSource != "card_click" {
			t.Fatalf("answer_source = %v, want card_click", card.AnswerSource)
		}
		// Single-space + issue reference: the card click IS the origin step,
		// so the advance pass executes synchronously in-request.
		_, detail := getDecisionRequestFor(t, testWorkspaceID, authDetail.Request.ID)
		if detail.Request.Status != "executed" {
			t.Fatalf("origin row after card answer = %s, want executed", detail.Request.Status)
		}
		if detail.Request.ExecutionResult == nil || !strings.Contains(string(detail.Request.ExecutionResult), "workspace") {
			t.Fatalf("execution_result = %s, want workspace payload", detail.Request.ExecutionResult)
		}
	})
}

// ── expiry + cancel lifecycle ───────────────────────────────────────────────

func TestDecisionRequestExpiryAndCancel(t *testing.T) {
	f := newDecisionFixture(t)

	t.Run("lazy expiry flips the group and its authorization card", func(t *testing.T) {
		w := createDecisionRequestFor(t, f.AgentID, f.TaskID, map[string]any{
			"action_type": "workspace_info_read", "title": "expire me",
			"origin_issue_id": f.IssueID,
		})
		detail := decodeDecisionRequestDetail(t, w)
		dbfx.Exec(t, `UPDATE decision_requests SET expires_at = now() - interval '1 minute' WHERE request_group_id = $1`, detail.Request.RequestGroupID)
		dbfx.Exec(t, `UPDATE issue_decisions SET expires_at = now() - interval '1 minute' WHERE pending_request_group_id = $1`, detail.Request.RequestGroupID)

		listW := httptest.NewRecorder()
		req := withURLParam(newRequest("GET", "/api/workspaces/"+testWorkspaceID+"/decision-requests", nil), "id", testWorkspaceID)
		testHandler.ListDecisionRequests(listW, req)
		if listW.Code != http.StatusOK {
			t.Fatalf("list: %d %s", listW.Code, listW.Body.String())
		}

		_, after := getDecisionRequestFor(t, testWorkspaceID, detail.Request.ID)
		if after.Request.Status != "expired" {
			t.Fatalf("row after sweep = %s, want expired", after.Request.Status)
		}
		var callbackAt *time.Time
		dbfx.QueryRow(t, `SELECT terminal_callback_at FROM decision_requests WHERE id = $1`, detail.Request.ID).Scan(&callbackAt)
		if callbackAt == nil {
			t.Fatal("terminal_callback_at not set — expiry callback must fire at most once and be marked")
		}
	})
	t.Run("creator agent cancel revokes the whole group; plain member cannot", func(t *testing.T) {
		user2 := dbfx.User(t, "ruyi630-member3", "ruyi630-member3@test.local")
		dbfx.Member(t, testWorkspaceID, user2, "member")
		w := createDecisionRequestFor(t, f.AgentID, f.TaskID, map[string]any{
			"action_type": "workspace_info_read", "title": "cancel me",
		})
		detail := decodeDecisionRequestDetail(t, w)

		if cw := cancelDecisionRequestFor(t, testWorkspaceID, detail.Request.ID, user2, "", ""); cw.Code != http.StatusForbidden {
			t.Fatalf("plain member cancel: expected 403, got %d", cw.Code)
		}
		if cw := cancelDecisionRequestFor(t, testWorkspaceID, detail.Request.ID, "", f.AgentID, f.TaskID); cw.Code != http.StatusOK {
			t.Fatalf("creator cancel: expected 200, got %d: %s", cw.Code, cw.Body.String())
		}
		if cw := cancelDecisionRequestFor(t, testWorkspaceID, detail.Request.ID, "", f.AgentID, f.TaskID); cw.Code != http.StatusConflict {
			t.Fatalf("second cancel: expected 409, got %d", cw.Code)
		}
		var status string
		dbfx.QueryRow(t, `SELECT status FROM decision_requests WHERE id = $1`, detail.Request.ID).Scan(&status)
		if status != "revoked" {
			t.Fatalf("row status after cancel = %s, want revoked", status)
		}
	})
}

// ── platform executor: prompt_restore ───────────────────────────────────────

func TestDecisionRequestExecutorPromptRestore(t *testing.T) {
	f := newDecisionFixture(t)

	t.Run("approval restores the previous prompt in the internal context", func(t *testing.T) {
		seedPromptApplyState(t, f.AgentID, "marketplace v2 text", "original human text")
		w := createDecisionRequestFor(t, f.AgentID, f.TaskID, map[string]any{
			"action_type": "prompt_restore", "title": "undo last apply",
			"params": map[string]any{"target_type": "agent", "target_id": f.AgentID},
		})
		if w.Code != http.StatusCreated {
			t.Fatalf("create: %d %s", w.Code, w.Body.String())
		}
		detail := decodeDecisionRequestDetail(t, w)
		if detail.Request.RiskTier != "write_low" {
			t.Fatalf("risk tier = %s, want write_low", detail.Request.RiskTier)
		}
		if aw := answerDecisionRequestFor(t, testWorkspaceID, detail.Request.ID, "", "approve"); aw.Code != http.StatusOK {
			t.Fatalf("approve: %d %s", aw.Code, aw.Body.String())
		}
		var instructions string
		dbfx.QueryRow(t, `SELECT instructions FROM agent WHERE id = $1`, f.AgentID).Scan(&instructions)
		if instructions != "original human text" {
			t.Fatalf("instructions after restore = %q, want the previous text", instructions)
		}
		var restored, hasRestorePoint bool
		dbfx.QueryRow(t, `SELECT (marketplace_prompt_state::jsonb->>'restored')::bool, COALESCE((marketplace_prompt_state::jsonb->>'has_restore_point')::bool, false) FROM agent WHERE id = $1`, f.AgentID).Scan(&restored, &hasRestorePoint)
		if !restored || hasRestorePoint {
			t.Fatalf("state restored=%v has_restore_point=%v, want restored=true spent=false", restored, hasRestorePoint)
		}
		_, detail = getDecisionRequestFor(t, testWorkspaceID, detail.Request.ID)
		if detail.Request.Status != "executed" || !strings.Contains(string(detail.Request.ExecutionResult), "content_sha256") {
			t.Fatalf("row = %s / %s, want executed with content_sha256 result", detail.Request.Status, detail.Request.ExecutionResult)
		}
	})
	t.Run("hand-edit guard fails execution without touching the prompt", func(t *testing.T) {
		seedPromptApplyState(t, f.AgentID, "marketplace v3 text", "original")
		dbfx.Exec(t, `UPDATE agent SET instructions = 'hand edited after apply' WHERE id = $1`, f.AgentID)
		w := createDecisionRequestFor(t, f.AgentID, f.TaskID, map[string]any{
			"action_type": "prompt_restore", "title": "undo over an edit",
			"params": map[string]any{"target_type": "agent", "target_id": f.AgentID},
		})
		detail := decodeDecisionRequestDetail(t, w)
		aw := answerDecisionRequestFor(t, testWorkspaceID, detail.Request.ID, "", "approve")
		if aw.Code != http.StatusOK {
			t.Fatalf("approve: %d", aw.Code)
		}
		_, after := getDecisionRequestFor(t, testWorkspaceID, detail.Request.ID)
		if after.Request.Status != "execute_failed" || after.Request.ExecutionError == nil || !strings.Contains(*after.Request.ExecutionError, "modified") {
			t.Fatalf("row status=%s err=%v, want execute_failed with the hand-edit refusal", after.Request.Status, after.Request.ExecutionError)
		}
		var instructions string
		dbfx.QueryRow(t, `SELECT instructions FROM agent WHERE id = $1`, f.AgentID).Scan(&instructions)
		if instructions != "hand edited after apply" {
			t.Fatalf("instructions = %q, want untouched by the failed execution", instructions)
		}
	})
	t.Run("executor refuses a target outside the request's workspace", func(t *testing.T) {
		ws2 := secondWorkspaceWithOwner(t)
		seedPromptApplyState(t, f.AgentID, "marketplace text", "original")
		// A second-space agent as the restore target: the request executes on
		// the origin row's workspace, so the containment check must refuse.
		ws2Agent := dbfx.Agent(t, "ruyi630-ws2-agent", "", testutil.Cols{"workspace_id": ws2})
		w := createDecisionRequestFor(t, f.AgentID, f.TaskID, map[string]any{
			"action_type": "prompt_restore", "title": "cross-space probe",
			"target_workspace_id": ws2,
			"params": map[string]any{"target_type": "agent", "target_id": ws2Agent},
		})
		detail := decodeDecisionRequestDetail(t, w)
		originID := detail.Request.ID
		var targetID string
		for _, step := range detail.Steps {
			if step.Role == "target" {
				targetID = step.ID
			}
		}
		if w := answerDecisionRequestFor(t, testWorkspaceID, originID, "", "approve"); w.Code != http.StatusOK {
			t.Fatalf("origin approve: %d", w.Code)
		}
		if w := answerDecisionRequestFor(t, ws2, targetID, "", "approve"); w.Code != http.StatusOK {
			t.Fatalf("target approve: %d", w.Code)
		}
		var status string
		var execErr *string
		dbfx.QueryRow(t, `SELECT status, execution_error FROM decision_requests WHERE request_group_id = $1 AND role = 'target'`, detail.Request.RequestGroupID).Scan(&status, &execErr)
		if status != "execute_failed" || execErr == nil || !strings.Contains(*execErr, "different workspace") {
			t.Fatalf("target row status=%s err=%v, want execute_failed on workspace containment", status, execErr)
		}
	})
}

// ── audit chain ─────────────────────────────────────────────────────────────

func TestDecisionRequestAuditChain(t *testing.T) {
	f := newDecisionFixture(t)
	w := createDecisionRequestFor(t, f.AgentID, f.TaskID, map[string]any{
		"action_type": "workspace_info_read", "title": "audited",
	})
	detail := decodeDecisionRequestDetail(t, w)
	if aw := answerDecisionRequestFor(t, testWorkspaceID, detail.Request.ID, "", "approve"); aw.Code != http.StatusOK {
		t.Fatalf("approve: %d", aw.Code)
	}
	var n int
	dbfx.QueryRow(t, `
		SELECT count(*) FROM audit_event
		WHERE trigger_kind = 'decision_request' AND trigger_ref = $1
		  AND event_type IN ('agent.decision_requested','agent.decision_authorized','agent.decision_executed')
	`, detail.Request.RequestGroupID).Scan(&n)
	if n != 3 {
		t.Fatalf("audit events = %d, want the requested/authorized/executed chain of 3", n)
	}
}

// ── agent polling access ────────────────────────────────────────────────────

func TestDecisionRequestAgentPolling(t *testing.T) {
	f := newDecisionFixture(t)
	w := createDecisionRequestFor(t, f.AgentID, f.TaskID, map[string]any{
		"action_type": "workspace_info_read", "title": "poll me",
	})
	detail := decodeDecisionRequestDetail(t, w)

	t.Run("creating agent reads its request without issue reference", func(t *testing.T) {
		req := newRequest("GET", "/api/workspaces/"+testWorkspaceID+"/decision-requests/"+detail.Request.ID, nil)
		req = withURLParams(req, "id", testWorkspaceID, "requestId", detail.Request.ID)
		req = decisionRequestAuthHeaders(req, f.AgentID, f.TaskID)
		w := httptest.NewRecorder()
		testHandler.GetDecisionRequest(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("agent poll: expected 200, got %d: %s", w.Code, w.Body.String())
		}
	})
	t.Run("unrelated agent refused", func(t *testing.T) {
		otherAgent := createHandlerTestAgent(t, "decision-request-outsider", nil)
		otherTask := createHandlerTestTaskForAgent(t, otherAgent)
		req := newRequest("GET", "/api/workspaces/"+testWorkspaceID+"/decision-requests/"+detail.Request.ID, nil)
		req = withURLParams(req, "id", testWorkspaceID, "requestId", detail.Request.ID)
		req = decisionRequestAuthHeaders(req, otherAgent, otherTask)
		w := httptest.NewRecorder()
		testHandler.GetDecisionRequest(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("unrelated agent poll: expected 403, got %d", w.Code)
		}
	})
}

// ── workspace teardown ──────────────────────────────────────────────────────

func TestDecisionRequestWorkspaceTeardown(t *testing.T) {
	f := newDecisionFixture(t)
	w := createDecisionRequestFor(t, f.AgentID, f.TaskID, map[string]any{
		"action_type": "workspace_info_read", "title": "swept with the space",
	})
	detail := decodeDecisionRequestDetail(t, w)

	// Mirror DeleteWorkspaceLeafData's decision_requests CTE (repo rule: no
	// FK, teardown is explicit).
	dbfx.Exec(t, `DELETE FROM decision_requests WHERE workspace_id = $1`, testWorkspaceID)
	var n int
	dbfx.QueryRow(t, `SELECT count(*) FROM decision_requests WHERE id = $1`, detail.Request.ID).Scan(&n)
	if n != 0 {
		t.Fatal("rows survived the teardown delete, want 0")
	}
}
