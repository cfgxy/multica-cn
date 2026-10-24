package handler

// Tests for the prompt legislation API (RUYI-305 E2): the state machine's
// every path (draft → pending_owner → approved → enacted | gate_failed →
// draft; pending_owner → rejected → draft), the permission gates, the ADR
// 007 §8 enact contract (clause asset write + carrier re-render in one
// transaction) and the carrier-rollback audit hook.
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
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/pkg/taskadaptation"
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
			`DELETE FROM clause_binding WHERE clause_id IN (SELECT id FROM clause WHERE workspace_id = $1)`,
			`DELETE FROM clause_version WHERE clause_id IN (SELECT id FROM clause WHERE workspace_id = $1)`,
			`DELETE FROM clause WHERE workspace_id = $1`,
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
	MoveToScope         string              `json:"move_to_scope"`
	MoveToScopeID       string              `json:"move_to_scope_id"`
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
// preview → approve → enacted and pins the ADR 007 §8 enact contract: one
// transaction writes the clause asset, normalizes the carrier's static
// business column through the version funnel, rebuilds the baseline and lands
// the row with clause_id + enacted_version.
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

	// The proposal row lands with the clause reference and the enacted
	// clause version (ADR 007 §8 — the old NULL boundary is replaced).
	var clauseID string
	var enactedVersion int
	var gateErrors string
	dbfx.QueryRow(t, `SELECT clause_id::text, enacted_version, gate_errors::text FROM prompt_proposal WHERE workspace_id = $1 AND id = $2`, wsID, id).Scan(&clauseID, &enactedVersion, &gateErrors)
	if clauseID == "" || enactedVersion != 1 {
		t.Fatalf("enacted row = clause_id %q / version %d, want a clause reference at version 1", clauseID, enactedVersion)
	}
	if gateErrors != "[]" {
		t.Fatalf("enacted row kept gate errors: %s", gateErrors)
	}

	// The clause asset trio: version 1 holds the drafted text, actively
	// bound to the workspace carrier.
	var content string
	dbfx.QueryRow(t, `SELECT content FROM clause_version WHERE clause_id = $1 AND version = 1`, clauseID).Scan(&content)
	if content != cleanClauseText {
		t.Fatalf("clause v1 content = %q, want the drafted text %q", content, cleanClauseText)
	}
	var bindingScope, bindingStatus string
	dbfx.QueryRow(t, `SELECT scope, status FROM clause_binding WHERE clause_id = $1 AND status = 'active'`, clauseID).Scan(&bindingScope, &bindingStatus)
	if bindingScope != "workspace" || bindingStatus != "active" {
		t.Fatalf("clause binding = %s/%s, want active workspace binding", bindingScope, bindingStatus)
	}

	// The carrier business column now keeps only static context. The clause
	// remains in the asset library for claim-time composition.
	var contextAfter string
	dbfx.QueryRow(t, `SELECT context FROM workspace WHERE id = $1`, wsID).Scan(&contextAfter)
	if contextAfter == contextBefore {
		t.Fatalf("enact contract broken: workspace carrier content unchanged")
	}
	if strings.Contains(contextAfter, "每日同步") || strings.Contains(contextAfter, "<!-- clause:id=") {
		t.Fatalf("carrier retained dynamic clause content: %.200s", contextAfter)
	}

	// The write went through the prompt_version funnel, not a bare column
	// update: the ledger holds the rendered carrier.
	var versions int
	dbfx.QueryRow(t, `SELECT COUNT(*) FROM prompt_version WHERE workspace_id = $1 AND scope = 'workspace' AND scope_id = $2`, wsID, wsID).Scan(&versions)
	if versions == 0 {
		t.Fatalf("carrier write bypassed the prompt_version funnel")
	}

	// The gate rebuilt the structure baseline from static text only.
	var clauses string
	dbfx.QueryRow(t, `SELECT clauses::text FROM prompt_structure_baseline WHERE carrier_scope = 'workspace' AND carrier_scope_id = $1`, wsID).Scan(&clauses)
	if strings.Contains(clauses, "每日同步") {
		t.Fatalf("baseline retained a dynamic clause: %s", clauses)
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
	useRetrospectiveRunner(t)

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
	// retro-run with the runner wired but no config row answers 400
	// (ErrNoConfig) — the route let the owner through to the handler.
	code, body := legislationCall(t, ownerOnly["retro-config"]().ServeHTTP,
		legislationReq(ownerID, wsID, http.MethodPut, "/api/retrospective/config", map[string]any{"enabled": false, "window_days": 7}))
	if code != http.StatusOK {
		t.Fatalf("owner retro-config: expected 200, got %d: %s", code, body)
	}
	code, body = legislationCall(t, ownerOnly["retro-run"]().ServeHTTP,
		legislationReq(ownerID, wsID, http.MethodPost, "/api/retrospective/run", nil))
	if code != http.StatusBadRequest {
		t.Fatalf("owner retro-run without config: expected 400, got %d: %s", code, body)
	}
	code, body = legislationCall(t, ownerOnly["approve"]().ServeHTTP,
		legislationReq(ownerID, wsID, http.MethodPost, "/api/prompt-legislation/proposals/"+id+"/approve", map[string]any{"confirm_diff_previewed": true}, "id", id))
	if code != http.StatusOK {
		t.Fatalf("owner approve: expected 200, got %d: %s", code, body)
	}
	var audit []struct {
		Action string `json:"action"`
	}
	var auditJSON []byte
	dbfx.QueryRow(t, `SELECT audit_log FROM prompt_proposal WHERE workspace_id = $1 AND id = $2`, wsID, id).Scan(&auditJSON)
	if err := json.Unmarshal(auditJSON, &audit); err != nil {
		t.Fatalf("decode approved proposal audit: %v", err)
	}
	approvedCount, enactedCount := 0, 0
	for _, entry := range audit {
		if entry.Action == "approved" {
			approvedCount++
		}
		if entry.Action == "enacted" {
			enactedCount++
		}
	}
	if approvedCount != 1 || enactedCount != 1 {
		t.Fatalf("single approve should append one approved and one enacted audit entry, got approved=%d enacted=%d audit=%s", approvedCount, enactedCount, auditJSON)
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

func TestLegislationUpdateValidatesAdaptationCandidateEvidence(t *testing.T) {
	wsID, _, memberID := legislationFixture(t)
	id := createDraft(t, wsID, memberID, "候选证据更新", cleanClauseText)
	var originalAnchors []byte
	if err := testPool.QueryRow(context.Background(), `SELECT evidence_anchors FROM prompt_proposal WHERE workspace_id = $1 AND id = $2`, wsID, id).Scan(&originalAnchors); err != nil {
		t.Fatalf("read original proposal anchors: %v", err)
	}

	agentID := dbfx.Agent(t, "legislation-candidate", handlerTestRuntimeID(t), testutil.Cols{"workspace_id": wsID})
	taskIDs := []string{"31a16b2c-e1a0-4a44-8a8f-6a17f51d0001", "31a16b2c-e1a0-4a44-8a8f-6a17f51d0002"}
	snapshot := `"policy_id":"policy-a","policy_version":"v2","scope":"current_task","trigger":"long implementation task","current_strategy":"one large pass","suggested_strategy":"use small verified steps","allowed_actions":["split into checkpoints"]`
	anchors := make([]map[string]string, 0, len(taskIDs))
	for _, taskID := range taskIDs {
		ctxJSON := `{"capability_adaptation":{` + snapshot + `}}`
		resultJSON := `{"capability_adaptation_result":{"task_id":"` + taskID + `","policy_id":"policy-a","policy_version":"v2","status":"applied"}}`
		dbfx.Task(t, agentID, testutil.Cols{
			"id": taskID, "status": "completed", "completed_at": testutil.Raw("now()"),
			"context": testutil.Raw("'" + ctxJSON + "'::jsonb"), "result": testutil.Raw("'" + resultJSON + "'::jsonb"),
		})
		anchors = append(anchors, map[string]string{"task_id": taskID})
	}
	candidateID := taskadaptation.CandidateID(&taskadaptation.Snapshot{
		PolicyID: "policy-a", PolicyVersion: "v2", Scope: "current_task", Trigger: "long implementation task",
		CurrentStrategy: "one large pass", SuggestedStrategy: "use small verified steps", AllowedActions: []string{"split into checkpoints"},
	})
	for _, anchor := range anchors {
		anchor["ref"] = adaptationCandidateRef + candidateID
		anchor["note"] = "policy-a@v2; applied; scope=current_task; evidence=repeated_verified_task_receipts; limitation=repeated_pattern_not_approved_workspace_policy"
	}

	invalidCases := []struct {
		name    string
		anchors []map[string]string
	}{
		{name: "forged source task", anchors: []map[string]string{{"task_id": "11111111-1111-4111-8111-111111111111", "ref": adaptationCandidateRef + candidateID, "note": anchors[0]["note"]}, anchors[1]}},
		{name: "single receipt", anchors: anchors[:1]},
		{name: "mismatched fingerprint", anchors: []map[string]string{{"task_id": taskIDs[0], "ref": adaptationCandidateRef + strings.Repeat("0", 64), "note": anchors[0]["note"]}, {"task_id": taskIDs[1], "ref": adaptationCandidateRef + strings.Repeat("0", 64), "note": anchors[1]["note"]}}},
	}
	for _, tc := range invalidCases {
		t.Run(tc.name, func(t *testing.T) {
			body := legislationProposalBody(wsID, "候选证据更新", cleanClauseText)
			body.EvidenceAnchors = tc.anchors
			code, response := legislationCall(t, testHandler.UpdatePromptProposal,
				legislationReq(memberID, wsID, http.MethodPatch, "/api/prompt-legislation/proposals/"+id, body, "id", id))
			if code != http.StatusBadRequest {
				t.Fatalf("patch invalid candidate evidence: expected 400, got %d: %s", code, response)
			}
			var got []byte
			if err := testPool.QueryRow(context.Background(), `SELECT evidence_anchors FROM prompt_proposal WHERE workspace_id = $1 AND id = $2`, wsID, id).Scan(&got); err != nil {
				t.Fatalf("read proposal anchors after rejected patch: %v", err)
			}
			if string(got) != string(originalAnchors) {
				t.Fatalf("rejected patch changed stored evidence: got %s, want %s", got, originalAnchors)
			}
		})
	}

	body := legislationProposalBody(wsID, "候选证据更新", cleanClauseText)
	body.EvidenceAnchors = anchors
	code, response := legislationCall(t, testHandler.UpdatePromptProposal,
		legislationReq(memberID, wsID, http.MethodPatch, "/api/prompt-legislation/proposals/"+id, body, "id", id))
	if code != http.StatusOK {
		t.Fatalf("patch valid candidate evidence: expected 200, got %d: %s", code, response)
	}
	var stored []map[string]string
	if err := testPool.QueryRow(context.Background(), `SELECT evidence_anchors FROM prompt_proposal WHERE workspace_id = $1 AND id = $2`, wsID, id).Scan(&stored); err != nil {
		t.Fatalf("read updated proposal anchors: %v", err)
	}
	if len(stored) != 2 || stored[0]["ref"] != adaptationCandidateRef+candidateID || stored[1]["ref"] != adaptationCandidateRef+candidateID {
		t.Fatalf("valid candidate evidence was not persisted: %+v", stored)
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
	var failedAuditJSON []byte
	dbfx.QueryRow(t, `SELECT audit_log FROM prompt_proposal WHERE workspace_id = $1 AND id = $2`, wsID, badID).Scan(&failedAuditJSON)
	var failedAudit []struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(failedAuditJSON, &failedAudit); err != nil {
		t.Fatalf("decode gate-failed proposal audit: %v", err)
	}
	approvedCount, gateFailedCount := 0, 0
	for _, entry := range failedAudit {
		if entry.Action == "approved" {
			approvedCount++
		}
		if entry.Action == "gate_failed" {
			gateFailedCount++
		}
	}
	if approvedCount != 1 || gateFailedCount != 1 {
		t.Fatalf("single gate-failed approval should append one approved and one gate_failed audit entry, got approved=%d gate_failed=%d audit=%s", approvedCount, gateFailedCount, failedAuditJSON)
	}
}

// cleanClauseFor returns gate-passing clause text whose first-line label IS
// the clause name — the engine consistency rule requires the two to match
// verbatim for add/revise drafts, and the body stays free of weak wording.
func cleanClauseFor(name string) string {
	return "- **" + name + "**：各成员开工前在任务单回报当日计划与阻塞。"
}

// TestLegislationEnactmentTargets (RUYI-858): the owner's landing target is
// decided at approval and persisted on the row. clause_library (the default,
// sent or not) writes the clause assets; static_prompt lands the gate
// synthesis through the version funnel WITHOUT clause asset rows; move/migrate
// kinds reject the static target with a 400; the /enact recovery path enacts
// the persisted target; the batch endpoint carries per-id targets.
func TestLegislationEnactmentTargets(t *testing.T) {
	wsID, ownerID, memberID := legislationFixture(t)

	var contextBefore string
	dbfx.QueryRow(t, `SELECT context FROM workspace WHERE id = $1`, wsID).Scan(&contextBefore)

	// Default (no target in the body): the library path writes clause assets
	// and normalizes the carrier's static content.
	libID := createDraft(t, wsID, memberID, "默认入条款库", cleanClauseFor("默认入条款库"))
	submitDraft(t, wsID, memberID, libID)
	ownerApprove := ownerRoute(http.HandlerFunc(testHandler.ApprovePromptProposal))
	code, body := legislationCall(t, ownerApprove.ServeHTTP,
		legislationReq(ownerID, wsID, http.MethodPost, "/api/prompt-legislation/proposals/"+libID+"/approve",
			map[string]any{"confirm_diff_previewed": true}, "id", libID))
	if code != http.StatusOK {
		t.Fatalf("library default approve: expected 200, got %d: %s", code, body)
	}
	var libTarget, libClause string
	var libVersion int
	dbfx.QueryRow(t, `SELECT enactment_target, COALESCE(clause_id::text, ''), enacted_version FROM prompt_proposal WHERE id = $1`, libID).
		Scan(&libTarget, &libClause, &libVersion)
	if libTarget != "clause_library" || libClause == "" || libVersion != 1 {
		t.Fatalf("library row = target %q / clause %q / version %d, want clause_library with a clause at version 1", libTarget, libClause, libVersion)
	}

	// static_prompt: the same gate-passing clause lands in the carrier text
	// ONLY — the version funnel records a carrier write, but no clause
	// asset rows and no clause reference on the row.
	staticID := createDraft(t, wsID, memberID, "静态落地", cleanClauseFor("静态落地"))
	submitDraft(t, wsID, memberID, staticID)
	code, body = legislationCall(t, ownerApprove.ServeHTTP,
		legislationReq(ownerID, wsID, http.MethodPost, "/api/prompt-legislation/proposals/"+staticID+"/approve",
			map[string]any{"confirm_diff_previewed": true, "enactment_target": "static_prompt"}, "id", staticID))
	if code != http.StatusOK {
		t.Fatalf("static approve: expected 200, got %d: %s", code, body)
	}
	var staticTarget, staticClause string
	var staticVersion int
	dbfx.QueryRow(t, `SELECT enactment_target, COALESCE(clause_id::text, ''), enacted_version FROM prompt_proposal WHERE id = $1`, staticID).
		Scan(&staticTarget, &staticClause, &staticVersion)
	if staticTarget != "static_prompt" || staticClause != "" {
		t.Fatalf("static row = target %q / clause %q, want static_prompt with no clause reference", staticTarget, staticClause)
	}
	if staticVersion == 0 || staticVersion < libVersion {
		t.Fatalf("static row enacted_version = %d, want a carrier version written by the funnel", staticVersion)
	}
	// No clause asset row exists for the static landing.
	var clauseRows int
	dbfx.QueryRow(t, `SELECT COUNT(*) FROM clause c WHERE c.workspace_id = $1`, wsID).Scan(&clauseRows)
	if clauseRows != 1 {
		t.Fatalf("clause rows = %d, want exactly the library clause (the static landing writes none)", clauseRows)
	}
	// The carrier column carries the synthesized text through the funnel.
	var contextAfter string
	dbfx.QueryRow(t, `SELECT context FROM workspace WHERE id = $1`, wsID).Scan(&contextAfter)
	if contextAfter == contextBefore || !strings.Contains(contextAfter, "静态落地") {
		t.Fatalf("static landing did not write the carrier content")
	}
	var staticVersions int
	dbfx.QueryRow(t, `SELECT COUNT(*) FROM prompt_version WHERE workspace_id = $1 AND scope = 'workspace' AND scope_id = $2`, wsID, wsID).Scan(&staticVersions)
	if staticVersions < 2 {
		t.Fatalf("static landing bypassed the prompt_version funnel: %d carrier versions", staticVersions)
	}

	// The recovery path enacts the PERSISTED target: a static row parked in
	// approved must still land statically when /enact runs later.
	restID := createDraft(t, wsID, memberID, "恢复路径静态", cleanClauseFor("恢复路径静态"))
	submitDraft(t, wsID, memberID, restID)
	dbfx.Exec(t, `UPDATE prompt_proposal SET status = 'approved', enactment_target = 'static_prompt' WHERE workspace_id = $1 AND id = $2`, wsID, restID)
	ownerEnact := ownerRoute(http.HandlerFunc(testHandler.EnactPromptProposal))
	code, body = legislationCall(t, ownerEnact.ServeHTTP,
		legislationReq(ownerID, wsID, http.MethodPost, "/api/prompt-legislation/proposals/"+restID+"/enact", nil, "id", restID))
	if code != http.StatusOK {
		t.Fatalf("recovery enact on static row: expected 200, got %d: %s", code, body)
	}
	if got := proposalStatus(t, wsID, restID); got != "enacted" {
		t.Fatalf("recovery enact: status %q, want enacted", got)
	}
	var restTarget string
	dbfx.QueryRow(t, `SELECT enactment_target FROM prompt_proposal WHERE id = $1`, restID).Scan(&restTarget)
	if restTarget != "static_prompt" {
		t.Fatalf("recovery enact rewrote the persisted target: %q", restTarget)
	}

	// A mid-flight crash on a DEFAULT row (approved, no explicit target
	// column value — migration default) must land as clause_library.
	restLibID := createDraft(t, wsID, memberID, "恢复路径默认", cleanClauseFor("恢复路径默认"))
	submitDraft(t, wsID, memberID, restLibID)
	approveDefault := ownerRoute(http.HandlerFunc(testHandler.ApprovePromptProposal))
	code, body = legislationCall(t, approveDefault.ServeHTTP,
		legislationReq(ownerID, wsID, http.MethodPost, "/api/prompt-legislation/proposals/"+restLibID+"/approve",
			map[string]any{"confirm_diff_previewed": true}, "id", restLibID))
	if code != http.StatusOK {
		t.Fatalf("default recovery approve: expected 200, got %d: %s", code, body)
	}
	var defaultTarget string
	dbfx.QueryRow(t, `SELECT enactment_target FROM prompt_proposal WHERE id = $1`, restLibID).Scan(&defaultTarget)
	if defaultTarget != "clause_library" {
		t.Fatalf("default approve persisted target %q, want clause_library", defaultTarget)
	}

	// Move/migrate kinds reject the static target up front.
	moveDraft := legislationProposalBody(wsID, "移动条款", "")
	moveDraft.ChangeKind = "move_clause"
	moveDraft.MoveToScope = "project"
	// A distinct, well-formed UUID keeps the "destination differs" rule
	// satisfied; the destination project need not exist — the target-400
	// fires in normalizeEnactmentTarget before any carrier read on approve.
	moveDraft.MoveToScopeID = "00000000-0000-0000-0000-000000000000"
	// Park the row at pending_owner the way a real move proposal would.
	code, body = legislationCall(t, testHandler.CreatePromptProposal,
		legislationReq(memberID, wsID, http.MethodPost, "/api/prompt-legislation/proposals", moveDraft))
	if code != http.StatusCreated {
		t.Fatalf("create move draft: expected 201, got %d: %s", code, body)
	}
	var moveResp struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(body), &moveResp); err != nil {
		t.Fatalf("decode move id: %v (%s)", err, body)
	}
	moveID := moveResp.ID
	dbfx.Exec(t, `UPDATE prompt_proposal SET status = 'pending_owner' WHERE id = $1`, moveID)
	code, body = legislationCall(t, ownerApprove.ServeHTTP,
		legislationReq(ownerID, wsID, http.MethodPost, "/api/prompt-legislation/proposals/"+moveID+"/approve",
			map[string]any{"confirm_diff_previewed": true, "enactment_target": "static_prompt"}, "id", moveID))
	if code != http.StatusBadRequest {
		t.Fatalf("static target on move_clause: expected 400, got %d: %s", code, body)
	}
	if got := proposalStatus(t, wsID, moveID); got != "pending_owner" {
		t.Fatalf("rejected static target changed the status: %q", got)
	}

	// Unknown targets are rejected too, without status movement.
	otherID := createDraft(t, wsID, memberID, "未知目标", cleanClauseFor("未知目标"))
	submitDraft(t, wsID, memberID, otherID)
	code, body = legislationCall(t, ownerApprove.ServeHTTP,
		legislationReq(ownerID, wsID, http.MethodPost, "/api/prompt-legislation/proposals/"+otherID+"/approve",
			map[string]any{"confirm_diff_previewed": true, "enactment_target": "carrier_override"}, "id", otherID))
	if code != http.StatusBadRequest {
		t.Fatalf("unknown target: expected 400, got %d: %s", code, body)
	}

	// The batch endpoint carries per-id landing targets: the clean row lands
	// statically, the sibling keeps the default library path.
	batchA := createDraft(t, wsID, memberID, "批量静态", cleanClauseFor("批量静态"))
	batchB := createDraft(t, wsID, memberID, "批量默认", cleanClauseFor("批量默认"))
	submitDraft(t, wsID, memberID, batchA)
	submitDraft(t, wsID, memberID, batchB)
	batch := ownerRoute(http.HandlerFunc(testHandler.BatchApprovePromptProposals))
	code, body = legislationCall(t, batch.ServeHTTP,
		legislationReq(ownerID, wsID, http.MethodPost, "/api/prompt-legislation/proposals/approve-batch",
			map[string]any{
				"ids":                    []string{batchA, batchB},
				"confirm_diff_previewed": true,
				"enactment_targets":      map[string]string{batchA: "static_prompt"},
			}))
	if code != http.StatusOK {
		t.Fatalf("batch with per-id targets: expected 200, got %d: %s", code, body)
	}
	var targetA, targetB, clauseB string
	dbfx.QueryRow(t, `SELECT enactment_target FROM prompt_proposal WHERE id = $1`, batchA).Scan(&targetA)
	dbfx.QueryRow(t, `SELECT enactment_target, COALESCE(clause_id::text, '') FROM prompt_proposal WHERE id = $1`, batchB).Scan(&targetB, &clauseB)
	if targetA != "static_prompt" || targetB != "clause_library" || clauseB == "" {
		t.Fatalf("batch targets: A=%q B=%q clauseB=%q, want A static_prompt / B clause_library with clause", targetA, targetB, clauseB)
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

// TestListPromptProposalsByRun (RUYI-673): the run_id filter selects the
// proposals a retrospective run created (audit_log's created entry carries
// the run id as actor_id) or merged into (merged_from cites the run id);
// the unfiltered listing is untouched.
func TestListPromptProposalsByRun(t *testing.T) {
	wsID, ownerID, memberID := legislationFixture(t)

	run1 := "aaaaaaaa-0000-4000-8000-000000000001"
	run2 := "bbbbbbbb-0000-4000-8000-000000000002"

	retroAudit := func(run string) string {
		audit, _ := json.Marshal([]map[string]any{{
			"action": "created", "actor_type": "system", "actor_id": run,
			"at": "2026-10-11T00:00:00Z",
		}})
		return string(audit)
	}

	idRun1 := createDraft(t, wsID, memberID, "按run过滤一", cleanClauseText)
	idRun2 := createDraft(t, wsID, memberID, "按run过滤二", cleanClauseText)
	idPlain := createDraft(t, wsID, memberID, "按run过滤普通", cleanClauseText)
	dbfx.Exec(t, `UPDATE prompt_proposal SET audit_log = $2::jsonb WHERE id = $1`, idRun1, retroAudit(run1))
	dbfx.Exec(t, `UPDATE prompt_proposal SET audit_log = $2::jsonb WHERE id = $1`, idRun2, retroAudit(run2))

	// A merged-into row: a regular draft whose pool merge cites run2.
	idMerged := createDraft(t, wsID, memberID, "按run过滤合并", cleanClauseText)
	merged, _ := json.Marshal([]map[string]string{{"issue_id": wsID, "run_id": run2}})
	dbfx.Exec(t, `UPDATE prompt_proposal SET merged_from = $2::jsonb WHERE id = $1`, idMerged, merged)

	list := func(query string) map[string]bool {
		t.Helper()
		code, body := legislationCall(t, testHandler.ListPromptProposals,
			legislationReq(ownerID, wsID, http.MethodGet, "/api/prompt-legislation/proposals"+query, nil))
		if code != http.StatusOK {
			t.Fatalf("list %q: expected 200, got %d: %s", query, code, body)
		}
		ids := map[string]bool{}
		for _, id := range []string{idRun1, idRun2, idPlain, idMerged} {
			if strings.Contains(body, id) {
				ids[id] = true
			}
		}
		return ids
	}

	if got := list("?run_id=" + run1); !got[idRun1] || len(got) != 1 {
		t.Fatalf("run1 filter returned %v, want only %s", got, idRun1)
	}
	if got := list("?run_id=" + run2); !got[idRun2] || !got[idMerged] || len(got) != 2 {
		t.Fatalf("run2 filter returned %v, want %s and %s", got, idRun2, idMerged)
	}
	if got := list(""); len(got) != 4 {
		t.Fatalf("unfiltered list returned %v, want all four", got)
	}
}
