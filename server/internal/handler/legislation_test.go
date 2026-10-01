package handler

// Tests for the prompt legislation API (RUYI-305 E2): the state machine's
// every path (draft → pending_owner → approved → enacted | gate_failed →
// draft; pending_owner → rejected → draft), the permission gates, the E5
// boundary (enacted_version stays NULL) and the carrier-rollback audit hook.
//
// Owner-only routes are driven through the real
// middleware.RequireWorkspaceRole chain from router.go — matching
// prompt_version_test.go — so the 403 assertions fail if that middleware is
// ever dropped from the route registration rather than exercising a
// handler-internal copy of the check.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/middleware"
)

// legislationCarrierText is the dedicated workspace carrier: a document the
// E4 gate accepts (safe headings, clean clause lines, the UUID section
// exempts its table from the table ban — same shape as the engine fixture).
const legislationCarrierText = `# 平台协作规范

## 沟通规范

- **行文基调**：专业、正式、准确、简明扼要。
- **先结论后依据**：第一句给出结论，依据随后。

## 成本纪律

- **合并取数**：能一条命令取得的情报不拆多轮。
- **禁止叙述轮**：工具编排期间不带工具调用的纯文字轮禁止出现。

## 成员 UUID 索引

| 成员 | UUID |
| --- | --- |
| 示例 | 00000000-0000-0000-0000-000000000000 |
`

// cleanClauseText is a clause the gate passes: no weak wording, no revision
// marks, no long inline enumeration.
const cleanClauseText = `- **每日同步**：各成员开工前在任务单回报当日计划与阻塞。`

// weakClauseText trips the weak-wording rule.
const weakClauseText = `- **弱表述条款**：原则上大家要写清楚。`

var legislationRunCounter = time.Now().UnixNano()

// legislationFixture builds a dedicated workspace whose carrier is
// legislationCarrierText, with one owner and one plain member. Legislation
// rows are removed by explicit cleanup in LIFO order ahead of the fixture's
// own workspace/user deletes (no FKs — the app layer owns dependent
// cleanup, so the test does too).
func legislationFixture(t *testing.T) (wsID, ownerID, memberID string) {
	t.Helper()
	if testHandler == nil {
		t.Skip("database not available")
	}
	legislationRunCounter++
	suffix := fmt.Sprintf("leg%d", legislationRunCounter)
	wsID = dbfx.Workspace(t, "legislation ws "+suffix, "legislation-"+suffix, map[string]any{"context": legislationCarrierText})
	ownerID = dbfx.User(t, "Legislation Owner", "legislation-owner-"+suffix+"@multica.test")
	dbfx.Member(t, wsID, ownerID, "owner")
	memberID = dbfx.User(t, "Legislation Member", "legislation-member-"+suffix+"@multica.test")
	dbfx.Member(t, wsID, memberID, "member")
	t.Cleanup(func() {
		ctx := context.Background()
		for _, stmt := range []string{
			`DELETE FROM prompt_proposal WHERE workspace_id = $1`,
			`DELETE FROM prompt_structure_baseline WHERE workspace_id = $1`,
			`DELETE FROM retrospective_issue_watermark WHERE workspace_id = $1`,
			`DELETE FROM retrospective_run WHERE workspace_id = $1`,
			`DELETE FROM retrospective_config WHERE workspace_id = $1`,
			`DELETE FROM prompt_version WHERE workspace_id = $1`,
		} {
			testPool.Exec(ctx, stmt, wsID)
		}
	})
	return wsID, ownerID, memberID
}

// legislationReq builds an authenticated request against the dedicated
// workspace (newRequest's shared-fixture headers are overridden).
func legislationReq(userID, wsID, method, path string, body any, params ...string) *http.Request {
	req := newRequest(method, path, body)
	req.Header.Set("X-User-ID", userID)
	req.Header.Set("X-Workspace-ID", wsID)
	if len(params) > 0 {
		req = withURLParams(req, params...)
	}
	return req
}

func legislationCall(t *testing.T, h http.HandlerFunc, req *http.Request) (int, string) {
	t.Helper()
	w := httptest.NewRecorder()
	h(w, req)
	return w.Code, w.Body.String()
}

// ownerRoute wraps a handler with the exact router.go owner middleware so
// the permission assertions exercise the real enforcement point.
func ownerRoute(h http.HandlerFunc) http.Handler {
	return middleware.RequireWorkspaceRole(testHandler.Queries, "owner")(h)
}

type proposalReqBody struct {
	CarrierScope        string              `json:"carrier_scope"`
	CarrierScopeID      string              `json:"carrier_scope_id"`
	TargetSection       string              `json:"target_section"`
	ChangeKind          string              `json:"change_kind"`
	ClauseName          string              `json:"clause_name"`
	ClauseText          string              `json:"clause_text"`
	GateAnswerLayer     string              `json:"gate_answer_layer"`
	GateAnswerRetention string              `json:"gate_answer_retention"`
	GateAnswerCost      string              `json:"gate_answer_cost"`
	GateAnswerConflict  string              `json:"gate_answer_conflict"`
	GateAnswerDedup     string              `json:"gate_answer_dedup"`
	EvidenceAnchors     []map[string]string `json:"evidence_anchors"`
}

func legislationProposalBody(wsID, clauseName, clauseText string) proposalReqBody {
	return proposalReqBody{
		CarrierScope:        "workspace",
		CarrierScopeID:      wsID,
		TargetSection:       "沟通规范",
		ChangeKind:          "add_clause",
		ClauseName:          clauseName,
		ClauseText:          clauseText,
		GateAnswerLayer:     "workspace=跨项目协作机制",
		GateAnswerRetention: "每轮输出都适用，留底座",
		GateAnswerCost:      "约 60 字符常驻",
		GateAnswerConflict:  "无同主题条款，无冲突",
		GateAnswerDedup:     "已检索上下文与手册分册，无重复条款",
		EvidenceAnchors:     []map[string]string{{"issue_id": "RUYI-000", "ref": "测试证据"}},
	}
}

// createDraft posts a draft as the member and returns the proposal id.
func createDraft(t *testing.T, wsID, memberID, clauseName, clauseText string) string {
	t.Helper()
	code, body := legislationCall(t, testHandler.CreatePromptProposal,
		legislationReq(memberID, wsID, http.MethodPost, "/api/prompt-legislation/proposals", legislationProposalBody(wsID, clauseName, clauseText)))
	if code != http.StatusCreated {
		t.Fatalf("create draft: expected 201, got %d: %s", code, body)
	}
	var resp struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("decode created proposal: %v (%s)", err, body)
	}
	return resp.ID
}

// submitDraft moves a draft to pending_owner.
func submitDraft(t *testing.T, wsID, memberID, id string) {
	t.Helper()
	code, body := legislationCall(t, testHandler.SubmitPromptProposal,
		legislationReq(memberID, wsID, http.MethodPost, "/api/prompt-legislation/proposals/"+id+"/submit", nil, "id", id))
	if code != http.StatusOK {
		t.Fatalf("submit: expected 200, got %d: %s", code, body)
	}
}

func proposalStatus(t *testing.T, wsID, id string) string {
	t.Helper()
	var status string
	if err := testPool.QueryRow(context.Background(),
		`SELECT status FROM prompt_proposal WHERE workspace_id = $1 AND id = $2`, wsID, id).Scan(&status); err != nil {
		t.Fatalf("read proposal status: %v", err)
	}
	return status
}

// TestLegislationStateMachineHappyPath walks draft → pending_owner →
// preview → approve → enacted and pins the E5 boundary: the business column
// is untouched and enacted_version stays NULL — the carrier write belongs
// to RUYI-285.
func TestLegislationStateMachineHappyPath(t *testing.T) {
	wsID, ownerID, memberID := legislationFixture(t)

	var contextBefore string
	dbfx.QueryRow(t, `SELECT context FROM workspace WHERE id = $1`, wsID).Scan(&contextBefore)

	id := createDraft(t, wsID, memberID, "每日同步", cleanClauseText)
	if got := proposalStatus(t, wsID, id); got != "draft" {
		t.Fatalf("after create: status = %q, want draft", got)
	}

	// Preview: the diff must contain the added clause line; no baseline
	// exists yet, so the first gate will bootstrap one.
	code, body := legislationCall(t, testHandler.PreviewPromptProposal,
		legislationReq(memberID, wsID, http.MethodPost, "/api/prompt-legislation/proposals/"+id+"/preview", nil, "id", id))
	if code != http.StatusOK {
		t.Fatalf("preview: expected 200, got %d: %s", code, body)
	}
	if !strings.Contains(body, "每日同步") {
		t.Fatalf("preview diff missing the added clause: %s", body)
	}

	submitDraft(t, wsID, memberID, id)
	if got := proposalStatus(t, wsID, id); got != "pending_owner" {
		t.Fatalf("after submit: status = %q, want pending_owner", got)
	}

	// The injection defence: approval without the preview-confirmed flag is
	// rejected before any transaction runs.
	code, body = legislationCall(t, testHandler.ApprovePromptProposal,
		legislationReq(ownerID, wsID, http.MethodPost, "/api/prompt-legislation/proposals/"+id+"/approve", map[string]any{"confirm_diff_previewed": false}, "id", id))
	if code != http.StatusBadRequest {
		t.Fatalf("approve without confirm: expected 400, got %d: %s", code, body)
	}
	if got := proposalStatus(t, wsID, id); got != "pending_owner" {
		t.Fatalf("status changed by rejected approve: %q", got)
	}

	ownerApprove := ownerRoute(http.HandlerFunc(testHandler.ApprovePromptProposal))
	code, body = legislationCall(t, ownerApprove.ServeHTTP,
		legislationReq(ownerID, wsID, http.MethodPost, "/api/prompt-legislation/proposals/"+id+"/approve", map[string]any{"confirm_diff_previewed": true}, "id", id))
	if code != http.StatusOK {
		t.Fatalf("approve: expected 200, got %d: %s", code, body)
	}
	if got := proposalStatus(t, wsID, id); got != "enacted" {
		t.Fatalf("after approve: status = %q, want enacted: %s", got, body)
	}

	var enactedVersion *int
	var gateErrors string
	dbfx.QueryRow(t, `SELECT enacted_version, gate_errors::text FROM prompt_proposal WHERE workspace_id = $1 AND id = $2`, wsID, id).Scan(&enactedVersion, &gateErrors)
	if enactedVersion != nil {
		t.Fatalf("E5 boundary broken: enacted_version = %v, want NULL", *enactedVersion)
	}
	if gateErrors != "[]" {
		t.Fatalf("enacted row kept gate errors: %s", gateErrors)
	}
	var contextAfter string
	dbfx.QueryRow(t, `SELECT context FROM workspace WHERE id = $1`, wsID).Scan(&contextAfter)
	if contextAfter != contextBefore {
		t.Fatalf("E5 boundary broken: workspace carrier content changed on enact")
	}

	// The gate rebuilt the structure baseline from the synthesized text —
	// the new clause is part of it.
	var clauses string
	dbfx.QueryRow(t, `SELECT clauses::text FROM prompt_structure_baseline WHERE carrier_scope = 'workspace' AND carrier_scope_id = $1`, wsID).Scan(&clauses)
	if !strings.Contains(clauses, "每日同步") {
		t.Fatalf("baseline rebuild missing the enacted clause: %s", clauses)
	}
}

// TestLegislationGateFailedAndRework covers approved → gate_failed → draft:
// a weak-wording clause is blocked, the engine report is persisted for the
// UI, and rework returns the row to draft with the report cleared on the
// next pass.
func TestLegislationGateFailedAndRework(t *testing.T) {
	wsID, ownerID, memberID := legislationFixture(t)

	id := createDraft(t, wsID, memberID, "弱表述条款", weakClauseText)
	submitDraft(t, wsID, memberID, id)

	ownerApprove := ownerRoute(http.HandlerFunc(testHandler.ApprovePromptProposal))
	code, body := legislationCall(t, ownerApprove.ServeHTTP,
		legislationReq(ownerID, wsID, http.MethodPost, "/api/prompt-legislation/proposals/"+id+"/approve", map[string]any{"confirm_diff_previewed": true}, "id", id))
	if code != http.StatusConflict {
		t.Fatalf("gate-failed approve: expected 409, got %d: %s", code, body)
	}
	if !strings.Contains(body, "gate_failed") {
		t.Fatalf("gate-failed response without verdict: %s", body)
	}
	if got := proposalStatus(t, wsID, id); got != "gate_failed" {
		t.Fatalf("after gate-failed approve: status = %q, want gate_failed", got)
	}
	var gateErrors string
	dbfx.QueryRow(t, `SELECT gate_errors::text FROM prompt_proposal WHERE workspace_id = $1 AND id = $2`, wsID, id).Scan(&gateErrors)
	if !strings.Contains(gateErrors, "弱表述") {
		t.Fatalf("gate_errors missing the weak-wording finding: %s", gateErrors)
	}

	// gate_failed → draft via rework (the creator's own row; member route).
	code, body = legislationCall(t, testHandler.ReworkPromptProposal,
		legislationReq(memberID, wsID, http.MethodPost, "/api/prompt-legislation/proposals/"+id+"/rework", nil, "id", id))
	if code != http.StatusOK {
		t.Fatalf("rework: expected 200, got %d: %s", code, body)
	}
	if got := proposalStatus(t, wsID, id); got != "draft" {
		t.Fatalf("after rework: status = %q, want draft", got)
	}

	// The creator fixes the clause and re-runs the whole path to enacted.
	code, body = legislationCall(t, testHandler.UpdatePromptProposal,
		legislationReq(memberID, wsID, http.MethodPatch, "/api/prompt-legislation/proposals/"+id,
			legislationProposalBody(wsID, "每日同步", cleanClauseText), "id", id))
	if code != http.StatusOK {
		t.Fatalf("patch draft: expected 200, got %d: %s", code, body)
	}
	submitDraft(t, wsID, memberID, id)
	code, body = legislationCall(t, ownerApprove.ServeHTTP,
		legislationReq(ownerID, wsID, http.MethodPost, "/api/prompt-legislation/proposals/"+id+"/approve", map[string]any{"confirm_diff_previewed": true}, "id", id))
	if code != http.StatusOK {
		t.Fatalf("second approve: expected 200, got %d: %s", code, body)
	}
	if got := proposalStatus(t, wsID, id); got != "enacted" {
		t.Fatalf("after re-approve: status = %q, want enacted: %s", got, body)
	}
}

// TestLegislationRejectKeepsRecordAndRestore pins the B3 semantics: rejected
// rows keep their record (readable in the pool, reason stored) and re-enter
// only through the audited owner restore.
func TestLegislationRejectKeepsRecordAndRestore(t *testing.T) {
	wsID, ownerID, memberID := legislationFixture(t)

	id := createDraft(t, wsID, memberID, "每日同步", cleanClauseText)
	submitDraft(t, wsID, memberID, id)

	ownerReject := ownerRoute(http.HandlerFunc(testHandler.RejectPromptProposal))
	code, body := legislationCall(t, ownerReject.ServeHTTP,
		legislationReq(ownerID, wsID, http.MethodPost, "/api/prompt-legislation/proposals/"+id+"/reject", map[string]any{"reason": "本轮范围外"}, "id", id))
	if code != http.StatusOK {
		t.Fatalf("reject: expected 200, got %d: %s", code, body)
	}
	if got := proposalStatus(t, wsID, id); got != "rejected" {
		t.Fatalf("after reject: status = %q, want rejected", got)
	}
	var reason, audit string
	dbfx.QueryRow(t, `SELECT rollback_reason, audit_log::text FROM prompt_proposal WHERE workspace_id = $1 AND id = $2`, wsID, id).Scan(&reason, &audit)
	if reason != "本轮范围外" {
		t.Fatalf("rejected row lost the reason: %q", reason)
	}
	if !strings.Contains(audit, "rejected") {
		t.Fatalf("rejected row missing the audit entry: %s", audit)
	}

	// The row stays in the pool (留档), listed with its status.
	code, body = legislationCall(t, testHandler.ListPromptProposals,
		legislationReq(memberID, wsID, http.MethodGet, "/api/prompt-legislation/proposals?status=rejected", nil))
	if code != http.StatusOK || !strings.Contains(body, id) {
		t.Fatalf("rejected row not listed: %d %s", code, body)
	}

	// Only the owner's audited restore re-enters the pool.
	ownerRestore := ownerRoute(http.HandlerFunc(testHandler.RestorePromptProposal))
	code, body = legislationCall(t, ownerRestore.ServeHTTP,
		legislationReq(ownerID, wsID, http.MethodPost, "/api/prompt-legislation/proposals/"+id+"/restore", nil, "id", id))
	if code != http.StatusOK {
		t.Fatalf("restore: expected 200, got %d: %s", code, body)
	}
	if got := proposalStatus(t, wsID, id); got != "draft" {
		t.Fatalf("after restore: status = %q, want draft", got)
	}
}

// TestLegislationOwnerPermissions drives every owner-only route through the
// real router middleware: a plain member is 403 on approve, batch approve,
// reject, restore, enact and both retrospective writes; the owner passes the
// same chain. Missing the confirm flag is a 400 from the handler, not a 403
// from the route — proving the chain let the owner through.
func TestLegislationOwnerPermissions(t *testing.T) {
	wsID, ownerID, memberID := legislationFixture(t)

	id := createDraft(t, wsID, memberID, "每日同步", cleanClauseText)
	submitDraft(t, wsID, memberID, id)

	ownerOnly := map[string]func() http.Handler{
		"approve":       func() http.Handler { return ownerRoute(http.HandlerFunc(testHandler.ApprovePromptProposal)) },
		"approve-batch": func() http.Handler { return ownerRoute(http.HandlerFunc(testHandler.BatchApprovePromptProposals)) },
		"reject":        func() http.Handler { return ownerRoute(http.HandlerFunc(testHandler.RejectPromptProposal)) },
		"restore":       func() http.Handler { return ownerRoute(http.HandlerFunc(testHandler.RestorePromptProposal)) },
		"enact":         func() http.Handler { return ownerRoute(http.HandlerFunc(testHandler.EnactPromptProposal)) },
		"retro-config":  func() http.Handler { return ownerRoute(http.HandlerFunc(testHandler.UpdateRetrospectiveConfig)) },
		"retro-run":     func() http.Handler { return ownerRoute(http.HandlerFunc(testHandler.TriggerRetrospectiveRun)) },
	}
	cases := []struct {
		name   string
		method string
		path   string
		body   any
		params []string
	}{
		{"approve", http.MethodPost, "/api/prompt-legislation/proposals/" + id + "/approve", map[string]any{"confirm_diff_previewed": true}, []string{"id", id}},
		{"approve-batch", http.MethodPost, "/api/prompt-legislation/proposals/approve-batch", map[string]any{"ids": []string{id}, "confirm_diff_previewed": true}, nil},
		{"reject", http.MethodPost, "/api/prompt-legislation/proposals/" + id + "/reject", map[string]any{"reason": "x"}, []string{"id", id}},
		{"restore", http.MethodPost, "/api/prompt-legislation/proposals/" + id + "/restore", nil, []string{"id", id}},
		{"enact", http.MethodPost, "/api/prompt-legislation/proposals/" + id + "/enact", nil, []string{"id", id}},
		{"retro-config", http.MethodPut, "/api/retrospective/config", map[string]any{"enabled": true}, nil},
		{"retro-run", http.MethodPost, "/api/retrospective/run", nil, nil},
	}
	for _, tc := range cases {
		t.Run("member forbidden on "+tc.name, func(t *testing.T) {
			code, body := legislationCall(t, ownerOnly[tc.name]().ServeHTTP,
				legislationReq(memberID, wsID, tc.method, tc.path, tc.body, tc.params...))
			if code != http.StatusForbidden {
				t.Fatalf("member: expected 403, got %d: %s", code, body)
			}
		})
	}
	// The gate-failed/enacted state was never reached — the member's 403s
	// must have left the pending row untouched.
	if got := proposalStatus(t, wsID, id); got != "pending_owner" {
		t.Fatalf("member requests moved the row: %q", got)
	}

	// Positive control: the owner passes the identical chain. Approve hits
	// the handler (a clean clause enacts — 200); retro-config writes (200);
	// retro-run reaches the LLM check (409 while the test fixture has no
	// LLM configured).
	code, body := legislationCall(t, ownerOnly["retro-config"]().ServeHTTP,
		legislationReq(ownerID, wsID, http.MethodPut, "/api/retrospective/config", map[string]any{"enabled": false, "window_days": 7}))
	if code != http.StatusOK {
		t.Fatalf("owner retro-config: expected 200, got %d: %s", code, body)
	}
	code, body = legislationCall(t, ownerOnly["retro-run"]().ServeHTTP,
		legislationReq(ownerID, wsID, http.MethodPost, "/api/retrospective/run", nil))
	if code != http.StatusConflict {
		t.Fatalf("owner retro-run without LLM: expected 409, got %d: %s", code, body)
	}
	code, body = legislationCall(t, ownerOnly["approve"]().ServeHTTP,
		legislationReq(ownerID, wsID, http.MethodPost, "/api/prompt-legislation/proposals/"+id+"/approve", map[string]any{"confirm_diff_previewed": true}, "id", id))
	if code != http.StatusOK {
		t.Fatalf("owner approve: expected 200, got %d: %s", code, body)
	}
}

// TestLegislationDraftOwnership: a member can edit and submit only their own
// drafts; another member's row is 403 (creator-or-owner rule inside the
// member route group).
func TestLegislationDraftOwnership(t *testing.T) {
	wsID, _, memberID := legislationFixture(t)
	otherID := dbfx.User(t, "Legislation Other", "legislation-other-x@multica.test")
	dbfx.Member(t, wsID, otherID, "member")

	id := createDraft(t, wsID, memberID, "每日同步", cleanClauseText)

	code, body := legislationCall(t, testHandler.UpdatePromptProposal,
		legislationReq(otherID, wsID, http.MethodPatch, "/api/prompt-legislation/proposals/"+id,
			legislationProposalBody(wsID, "每日同步", weakClauseText), "id", id))
	if code != http.StatusForbidden {
		t.Fatalf("other member patch: expected 403, got %d: %s", code, body)
	}
	code, body = legislationCall(t, testHandler.SubmitPromptProposal,
		legislationReq(otherID, wsID, http.MethodPost, "/api/prompt-legislation/proposals/"+id+"/submit", nil, "id", id))
	if code != http.StatusForbidden {
		t.Fatalf("other member submit: expected 403, got %d: %s", code, body)
	}
	if got := proposalStatus(t, wsID, id); got != "draft" {
		t.Fatalf("row moved by a non-creator: %q", got)
	}
}

// TestLegislationCreateValidation: unknown scope and a scope_id that does
// not exist in the workspace are rejected on the write path.
func TestLegislationCreateValidation(t *testing.T) {
	wsID, _, memberID := legislationFixture(t)

	body := legislationProposalBody(wsID, "每日同步", cleanClauseText)
	body.CarrierScope = "galaxy"
	code, respBody := legislationCall(t, testHandler.CreatePromptProposal,
		legislationReq(memberID, wsID, http.MethodPost, "/api/prompt-legislation/proposals", body))
	if code != http.StatusBadRequest {
		t.Fatalf("unknown scope: expected 400, got %d: %s", code, respBody)
	}

	body = legislationProposalBody(wsID, "每日同步", cleanClauseText)
	body.CarrierScope = "project"
	body.CarrierScopeID = wsID // exists but is not a project
	code, respBody = legislationCall(t, testHandler.CreatePromptProposal,
		legislationReq(memberID, wsID, http.MethodPost, "/api/prompt-legislation/proposals", body))
	if code != http.StatusNotFound {
		t.Fatalf("non-project scope id: expected 404, got %d: %s", code, respBody)
	}
}

// TestLegislationEnactRecoveryPath: an `approved` row stranded by a crash is
// re-gated and enacted by the owner-only enact endpoint; a pending_owner row
// is refused there (approve is the normal path).
func TestLegislationEnactRecoveryPath(t *testing.T) {
	wsID, ownerID, memberID := legislationFixture(t)

	id := createDraft(t, wsID, memberID, "每日同步", cleanClauseText)
	submitDraft(t, wsID, memberID, id)

	ownerEnact := ownerRoute(http.HandlerFunc(testHandler.EnactPromptProposal))
	// pending_owner cannot go through enact.
	code, body := legislationCall(t, ownerEnact.ServeHTTP,
		legislationReq(ownerID, wsID, http.MethodPost, "/api/prompt-legislation/proposals/"+id+"/enact", nil, "id", id))
	if code != http.StatusConflict {
		t.Fatalf("enact on pending_owner: expected 409, got %d: %s", code, body)
	}

	// Simulate the mid-flight crash: approved, audited, not yet gated.
	dbfx.Exec(t, `UPDATE prompt_proposal SET status = 'approved' WHERE workspace_id = $1 AND id = $2`, wsID, id)
	code, body = legislationCall(t, ownerEnact.ServeHTTP,
		legislationReq(ownerID, wsID, http.MethodPost, "/api/prompt-legislation/proposals/"+id+"/enact", nil, "id", id))
	if code != http.StatusOK {
		t.Fatalf("enact recovery: expected 200, got %d: %s", code, body)
	}
	if got := proposalStatus(t, wsID, id); got != "enacted" {
		t.Fatalf("after recovery enact: status = %q, want enacted", got)
	}
}

// TestLegislationBatchApprove: per-id independent transactions — the clean
// proposal enacts, the weak one gate-fails, and neither outcome blocks the
// other; results come back in input order.
func TestLegislationBatchApprove(t *testing.T) {
	wsID, ownerID, memberID := legislationFixture(t)

	goodID := createDraft(t, wsID, memberID, "每日同步", cleanClauseText)
	badID := createDraft(t, wsID, memberID, "弱表述条款", weakClauseText)
	submitDraft(t, wsID, memberID, goodID)
	submitDraft(t, wsID, memberID, badID)

	batch := ownerRoute(http.HandlerFunc(testHandler.BatchApprovePromptProposals))
	code, body := legislationCall(t, batch.ServeHTTP,
		legislationReq(ownerID, wsID, http.MethodPost, "/api/prompt-legislation/proposals/approve-batch",
			map[string]any{"ids": []string{goodID, badID}, "confirm_diff_previewed": true}))
	if code != http.StatusOK {
		t.Fatalf("batch approve: expected 200, got %d: %s", code, body)
	}
	var resp struct {
		Results []struct {
			ID     string `json:"id"`
			Status int    `json:"status"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("decode batch result: %v (%s)", err, body)
	}
	if len(resp.Results) != 2 || resp.Results[0].ID != goodID || resp.Results[1].ID != badID {
		t.Fatalf("batch results out of input order: %s", body)
	}
	if resp.Results[0].Status != http.StatusOK || resp.Results[1].Status != http.StatusConflict {
		t.Fatalf("batch per-id statuses wrong: %s", body)
	}
	if got := proposalStatus(t, wsID, goodID); got != "enacted" {
		t.Fatalf("clean proposal: %q, want enacted", got)
	}
	if got := proposalStatus(t, wsID, badID); got != "gate_failed" {
		t.Fatalf("weak proposal: %q, want gate_failed", got)
	}
}

// TestPromptVersionRevertMarksEnactedProposals pins the rollback audit hook:
// a prompt_version switch (source='revert') marks every enacted proposal on
// that carrier inside the same transaction — reason and carrier_rollback
// audit entry on the proposal line.
func TestPromptVersionRevertMarksEnactedProposals(t *testing.T) {
	wsID, ownerID, memberID := legislationFixture(t)

	id := createDraft(t, wsID, memberID, "每日同步", cleanClauseText)
	submitDraft(t, wsID, memberID, id)
	ownerApprove := ownerRoute(http.HandlerFunc(testHandler.ApprovePromptProposal))
	code, body := legislationCall(t, ownerApprove.ServeHTTP,
		legislationReq(ownerID, wsID, http.MethodPost, "/api/prompt-legislation/proposals/"+id+"/approve", map[string]any{"confirm_diff_previewed": true}, "id", id))
	if code != http.StatusOK {
		t.Fatalf("approve: expected 200, got %d: %s", code, body)
	}

	// Seed a prompt_version line (v1) and then switch to it — the switch is
	// the source='revert' write the hook rides on.
	saveReq := legislationReq(ownerID, wsID, http.MethodPost,
		"/api/prompt-governance/workspace/"+wsID+"/versions", map[string]any{"content": legislationCarrierText, "change_note": "v1 快照"})
	code, body = legislationCall(t, testHandler.SavePromptGovernanceVersion, withURLParams(saveReq, "scope", "workspace", "scopeId", wsID))
	if code != http.StatusOK {
		t.Fatalf("save v1: expected 200, got %d: %s", code, body)
	}
	switchReq := legislationReq(ownerID, wsID, http.MethodPost,
		"/api/prompt-governance/workspace/"+wsID+"/versions/1/switch", nil)
	code, body = legislationCall(t, testHandler.SwitchPromptGovernanceVersion, withURLParams(switchReq, "scope", "workspace", "scopeId", wsID, "version", "1"))
	if code != http.StatusOK {
		t.Fatalf("switch to v1: expected 200, got %d: %s", code, body)
	}

	var reason, audit string
	dbfx.QueryRow(t, `SELECT rollback_reason, audit_log::text FROM prompt_proposal WHERE workspace_id = $1 AND id = $2`, wsID, id).Scan(&reason, &audit)
	if reason == "" {
		t.Fatalf("enacted proposal not marked on carrier rollback")
	}
	if !strings.Contains(audit, "carrier_rollback") {
		t.Fatalf("rollback audit entry missing: %s", audit)
	}
	if got := proposalStatus(t, wsID, id); got != "enacted" {
		t.Fatalf("rollback must not change the proposal status: %q", got)
	}
}
