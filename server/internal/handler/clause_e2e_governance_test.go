package handler

// Clause lifecycle end to end plus the governance views (RUYI-751 / ADR
// 007 §8–§9): revise / remove / move walk the real legislation API on top
// of an enacted clause and pin the carrier, binding and version effects;
// the legacy clause_name-only reference keeps resolving; the three
// governance endpoints answer over the same workspace; the reconciliation
// check catches a layer-set violation and all three drift kinds; and a
// degraded version window yields exactly one system-drafted rollback
// proposal that the overview lists.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/clausequalityrollup"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/pkg/clausemark"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// squadCarrierText is a squad instruction column the gate accepts, carrying
// the same target section as the workspace carrier so a move_clause has
// somewhere canonical to land.
const squadCarrierText = `# 小队作战手册

## 沟通规范

- **行文基调**：专业、正式、准确、简明扼要。

## 工作纪律

- **先测试后交付**：行为变更先写失败测试再实现。
`

// clauseOpBody builds a revise/remove/move proposal body referencing an
// enacted clause by id.
func clauseOpBody(wsID, changeKind, clauseID, clauseName, clauseText string, extra map[string]any) map[string]any {
	body := map[string]any{
		"carrier_scope":         "workspace",
		"carrier_scope_id":      wsID,
		"target_section":        "沟通规范",
		"change_kind":           changeKind,
		"clause_name":           clauseName,
		"clause_text":           clauseText,
		"gate_answer_layer":     "workspace=跨项目协作机制",
		"gate_answer_retention": "每轮输出都适用，留底座",
		"gate_answer_cost":      "约 60 字符常驻",
		"gate_answer_conflict":  "无同主题条款，无冲突",
		"gate_answer_dedup":     "已检索上下文与手册分册，无重复条款",
		"evidence_anchors":      []map[string]string{{"issue_id": "RUYI-000", "ref": "测试证据"}},
	}
	if clauseID != "" {
		body["clause_id"] = clauseID
	}
	for k, v := range extra {
		body[k] = v
	}
	return body
}

// submitClauseOp creates a proposal body as the member and walks it to
// pending_owner; the caller decides how the owner disposes of it.
func submitClauseOp(t *testing.T, f *clauseInjectionFixture, body map[string]any) string {
	t.Helper()
	code, respBody := legislationCall(t, testHandler.CreatePromptProposal,
		legislationReq(f.memberID, f.wsID, http.MethodPost, "/api/prompt-legislation/proposals", body))
	if code != http.StatusCreated {
		t.Fatalf("create %s draft: expected 201, got %d: %s", body["change_kind"], code, respBody)
	}
	var parsed struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(respBody), &parsed); err != nil {
		t.Fatalf("decode created proposal: %v (%s)", err, respBody)
	}
	submitDraft(t, f.wsID, f.memberID, parsed.ID)
	return parsed.ID
}

func TestClauseReviseEnactsNextVersion(t *testing.T) {
	f := newClauseInjectionFixture(t)
	clauseID := enactWorkspaceClause(t, f, "每日同步", cleanClauseText)

	proposed := "- **每日同步**：各成员开工前在任务单回报当日计划、阻塞与预计完成时间。"
	id := submitClauseOp(t, f, clauseOpBody(f.wsID, "revise_clause", clauseID, "每日同步", proposed, nil))
	code, body := legislationCall(t, testHandler.PreviewPromptProposal,
		legislationReq(f.memberID, f.wsID, http.MethodPost, "/api/prompt-legislation/proposals/"+id+"/preview", nil, "id", id))
	if code != http.StatusOK {
		t.Fatalf("revise preview: expected 200, got %d: %s", code, body)
	}
	var preview PromptProposalPreviewResponse
	if err := json.Unmarshal([]byte(body), &preview); err != nil {
		t.Fatalf("decode revise preview: %v (%s)", err, body)
	}
	var diff strings.Builder
	for _, line := range preview.Diff {
		diff.WriteString(line.Text)
		diff.WriteByte('\n')
	}
	if !strings.Contains(diff.String(), proposed) || strings.Contains(diff.String(), "修订请改用新增") {
		t.Fatalf("revise preview diff = %q, want proposed clause body and no static-title synthesis error", diff.String())
	}
	if code, body := ownerApprove(t, f.ownerID, f.wsID, id); code != http.StatusOK {
		t.Fatalf("revise approve: expected 200, got %d: %s", code, body)
	}

	var content string
	dbfx.QueryRow(t, `SELECT content FROM clause_version WHERE clause_id = $1 AND version = 2`, clauseID).Scan(&content)
	if content != "- **每日同步**：各成员开工前在任务单回报当日计划、阻塞与预计完成时间。" {
		t.Fatalf("clause v2 content = %q", content)
	}
	var since int32
	dbfx.QueryRow(t, `SELECT since_clause_version FROM clause_binding WHERE clause_id = $1 AND status = 'active'`, clauseID).Scan(&since)
	if since != 2 {
		t.Fatalf("binding since_clause_version = %d, want 2", since)
	}
	var carrierContext string
	dbfx.QueryRow(t, `SELECT context FROM workspace WHERE id = $1`, f.wsID).Scan(&carrierContext)
	if strings.Contains(carrierContext, "回报当日计划、阻塞与预计完成时间") ||
		strings.Contains(carrierContext, "clause:id=") {
		t.Fatalf("persisted carrier retained dynamic clause v2:\n%s", carrierContext)
	}
	var enactedVersion int
	dbfx.QueryRow(t, `SELECT enacted_version FROM prompt_proposal WHERE workspace_id = $1 AND id = $2`,
		f.wsID, id).Scan(&enactedVersion)
	if enactedVersion != 2 {
		t.Fatalf("proposal enacted_version = %d, want 2", enactedVersion)
	}

	taskID, claimedContext := enqueueAndClaim(t, f)
	if !strings.Contains(claimedContext, "rev=2") || !strings.Contains(claimedContext, "回报当日计划、阻塞与预计完成时间") {
		t.Fatalf("new claim did not receive revised clause v2:\n%s", claimedContext)
	}
	var injectedVersion int32
	var degraded bool
	if err := testPool.QueryRow(context.Background(),
		`SELECT clause_version, degraded FROM clause_injection WHERE task_id = $1 AND clause_id = $2`, taskID, clauseID).
		Scan(&injectedVersion, &degraded); err != nil {
		t.Fatalf("new claim left no clause_injection evidence: %v", err)
	}
	if injectedVersion != 2 || degraded {
		t.Fatalf("new claim injection = v%d degraded=%v, want v2 and non-degraded", injectedVersion, degraded)
	}
}

func TestClauseReviseKeepsCarrierStaticWithoutMarker(t *testing.T) {
	f := newClauseInjectionFixture(t)
	clauseID := enactWorkspaceClause(t, f, "每日同步", cleanClauseText)

	var before string
	dbfx.QueryRow(t, `SELECT context FROM workspace WHERE id = $1`, f.wsID).Scan(&before)
	if strings.Contains(before, "clause:id=") {
		t.Fatalf("enacted carrier unexpectedly contains a marker:\n%s", before)
	}

	proposed := "- **每日同步**：缺失 marker 时仍由资产集重建此条款。"
	id := submitClauseOp(t, f, clauseOpBody(f.wsID, "revise_clause", clauseID, "每日同步", proposed, nil))
	code, body := legislationCall(t, testHandler.PreviewPromptProposal,
		legislationReq(f.memberID, f.wsID, http.MethodPost, "/api/prompt-legislation/proposals/"+id+"/preview", nil, "id", id))
	if code != http.StatusOK {
		t.Fatalf("static-carrier preview: expected 200, got %d: %s", code, body)
	}
	if !strings.Contains(body, proposed) {
		t.Fatalf("static-carrier preview omitted proposed clause: %s", body)
	}
	if code, body := ownerApprove(t, f.ownerID, f.wsID, id); code != http.StatusOK {
		t.Fatalf("missing-marker approve: expected 200, got %d: %s", code, body)
	}

	var after string
	dbfx.QueryRow(t, `SELECT context FROM workspace WHERE id = $1`, f.wsID).Scan(&after)
	if strings.Contains(after, proposed) || strings.Contains(after, "clause:id=") {
		t.Fatalf("revision persisted dynamic clause content:\n%s", after)
	}
	if strings.Contains(after, cleanClauseText) {
		t.Fatalf("revision retained the stale dynamic clause text:\n%s", after)
	}
	if !strings.Contains(after, "## 沟通规范") || !strings.Contains(after, "行文基调") {
		t.Fatalf("missing-marker revision did not preserve static context:\n%s", after)
	}
}

func directClauseRevisionRoute(h http.HandlerFunc) http.Handler {
	return RequireHumanOrOAuthActor(middleware.RequireWorkspaceRole(testHandler.Queries, "owner")(h))
}

func TestDirectClauseRevisionUsesPreviewTokensAndCreatesNoProposal(t *testing.T) {
	f := newClauseInjectionFixture(t)
	clauseID := enactWorkspaceClause(t, f, "每日同步", cleanClauseText)

	var currentContent, currentHash, currentCarrier string
	dbfx.QueryRow(t, `SELECT content, content_sha256 FROM clause_version WHERE clause_id = $1 AND version = 1`, clauseID).
		Scan(&currentContent, &currentHash)
	dbfx.QueryRow(t, `SELECT context FROM workspace WHERE id = $1`, f.wsID).Scan(&currentCarrier)
	currentCarrier = clausemark.StripMarkers(currentCarrier)
	if _, err := testPool.Exec(context.Background(), `UPDATE workspace SET context = $1 WHERE id = $2`, currentCarrier, f.wsID); err != nil {
		t.Fatalf("remove marker from direct-revision fixture carrier: %v", err)
	}
	proposed := "- **每日同步**：Owner 确认后由 MCP 直接写入完整最终条文。"
	request := map[string]any{
		"content":                        proposed,
		"expected_clause_version":        1,
		"expected_clause_content_sha256": currentHash,
		"expected_carrier_sha256":        sha256Hex(currentCarrier),
	}
	route := directClauseRevisionRoute(testHandler.DirectReviseClause)
	previewRoute := directClauseRevisionRoute(testHandler.PreviewDirectClauseRevision)
	previewReq := legislationReq(f.ownerID, f.wsID, http.MethodPost,
		"/api/prompt-legislation/governance/clauses/"+clauseID+"/direct-revision-preview", request, "clauseId", clauseID)
	previewReq.Header.Set("X-Actor-Source", "oauth")
	code, body := legislationCall(t, previewRoute.ServeHTTP, previewReq)
	if code != http.StatusOK {
		t.Fatalf("direct revision preview: expected 200, got %d: %s", code, body)
	}
	if !strings.Contains(body, proposed) || !strings.Contains(body, `"next_clause_version":2`) {
		t.Fatalf("direct revision preview omitted canonical diff/version: %s", body)
	}
	var preview DirectClauseRevisionPreviewResponse
	if err := json.Unmarshal([]byte(body), &preview); err != nil {
		t.Fatalf("decode direct revision preview: %v (%s)", err, body)
	}
	if len(preview.PreviewSHA256) != 64 {
		t.Fatalf("direct revision preview token = %q, want SHA256", preview.PreviewSHA256)
	}

	mismatched := make(map[string]any, len(request)+2)
	for key, value := range request {
		mismatched[key] = value
	}
	mismatched["content"] = "- **每日同步**：未经 Owner 预览的替换正文。"
	mismatched["preview_sha256"] = preview.PreviewSHA256
	mismatched["confirm_previewed"] = true
	mismatchReq := legislationReq(f.ownerID, f.wsID, http.MethodPost,
		"/api/prompt-legislation/governance/clauses/"+clauseID+"/direct-revision", mismatched, "clauseId", clauseID)
	mismatchReq.Header.Set("X-Actor-Source", "oauth")
	code, body = legislationCall(t, route.ServeHTTP, mismatchReq)
	if code != http.StatusConflict || !strings.Contains(body, "预览与提交内容不一致") {
		t.Fatalf("mismatched direct revision = %d: %s, want preview conflict", code, body)
	}

	request["preview_sha256"] = preview.PreviewSHA256
	request["confirm_previewed"] = true
	writeReq := legislationReq(f.ownerID, f.wsID, http.MethodPost,
		"/api/prompt-legislation/governance/clauses/"+clauseID+"/direct-revision", request, "clauseId", clauseID)
	writeReq.Header.Set("X-Actor-Source", "oauth")
	code, body = legislationCall(t, route.ServeHTTP, writeReq)
	if code != http.StatusOK {
		t.Fatalf("direct revision write: expected 200, got %d: %s", code, body)
	}
	var result DirectClauseRevisionResponse
	if err := json.Unmarshal([]byte(body), &result); err != nil {
		t.Fatalf("decode direct revision result: %v (%s)", err, body)
	}

	var versionCount, proposalCount int
	var versionContent, audit, persistedCarrier string
	dbfx.QueryRow(t, `SELECT COUNT(*) FROM clause_version WHERE clause_id = $1`, clauseID).Scan(&versionCount)
	dbfx.QueryRow(t, `SELECT content FROM clause_version WHERE clause_id = $1 AND version = 2`, clauseID).Scan(&versionContent)
	dbfx.QueryRow(t, `SELECT COUNT(*) FROM prompt_proposal WHERE workspace_id = $1`, f.wsID).Scan(&proposalCount)
	dbfx.QueryRow(t, `SELECT audit_log::text FROM clause_binding WHERE clause_id = $1 AND status = 'active'`, clauseID).Scan(&audit)
	dbfx.QueryRow(t, `SELECT context FROM workspace WHERE id = $1`, f.wsID).Scan(&persistedCarrier)
	if versionCount != 2 || versionContent != proposed {
		t.Fatalf("direct revision versions = %d / %q, want v2 %q", versionCount, versionContent, proposed)
	}
	if proposalCount != 1 {
		t.Fatalf("direct revision created a proposal: proposal count = %d, want original enact only", proposalCount)
	}
	if !strings.Contains(audit, "owner_direct_revision") {
		t.Fatalf("direct revision binding audit missing action: %s", audit)
	}
	if strings.Contains(persistedCarrier, proposed) || strings.Contains(persistedCarrier, currentContent) || strings.Contains(persistedCarrier, "clause:id=") {
		t.Fatalf("direct revision persisted dynamic clause content:\n%s", persistedCarrier)
	}
	if result.CarrierContentSHA256 != sha256Hex(persistedCarrier) {
		t.Fatalf("direct revision response carrier hash = %s, persisted hash = %s", result.CarrierContentSHA256, sha256Hex(persistedCarrier))
	}

	staleReq := legislationReq(f.ownerID, f.wsID, http.MethodPost,
		"/api/prompt-legislation/governance/clauses/"+clauseID+"/direct-revision", request, "clauseId", clauseID)
	staleReq.Header.Set("X-Actor-Source", "oauth")
	code, body = legislationCall(t, route.ServeHTTP, staleReq)
	if code != http.StatusConflict || !strings.Contains(body, "changed from preview") {
		t.Fatalf("stale direct revision = %d: %s, want version conflict", code, body)
	}
	dbfx.QueryRow(t, `SELECT COUNT(*) FROM clause_version WHERE clause_id = $1`, clauseID).Scan(&versionCount)
	if versionCount != 2 {
		t.Fatalf("stale direct revision changed version history: %d versions", versionCount)
	}

	taskTokenReq := legislationReq(f.ownerID, f.wsID, http.MethodPost,
		"/api/prompt-legislation/governance/clauses/"+clauseID+"/direct-revision", request, "clauseId", clauseID)
	taskTokenReq.Header.Set("X-Actor-Source", "task_token")
	code, body = legislationCall(t, route.ServeHTTP, taskTokenReq)
	if code != http.StatusForbidden {
		t.Fatalf("task-token direct revision = %d: %s, want 403", code, body)
	}

	cloudPATReq := legislationReq(f.ownerID, f.wsID, http.MethodPost,
		"/api/prompt-legislation/governance/clauses/"+clauseID+"/direct-revision", request, "clauseId", clauseID)
	cloudPATReq.Header.Set("X-Actor-Source", "cloud_pat")
	code, body = legislationCall(t, route.ServeHTTP, cloudPATReq)
	if code != http.StatusForbidden {
		t.Fatalf("cloud-PAT direct revision = %d: %s, want 403", code, body)
	}

	nonOwnerReq := legislationReq(f.memberID, f.wsID, http.MethodPost,
		"/api/prompt-legislation/governance/clauses/"+clauseID+"/direct-revision", request, "clauseId", clauseID)
	nonOwnerReq.Header.Set("X-Actor-Source", "oauth")
	code, body = legislationCall(t, route.ServeHTTP, nonOwnerReq)
	if code != http.StatusForbidden {
		t.Fatalf("non-owner OAuth direct revision = %d: %s, want 403", code, body)
	}
}

func TestClauseReviseExtendsCurrentV2(t *testing.T) {
	f := newClauseInjectionFixture(t)
	clauseID := enactWorkspaceClause(t, f, "每日同步", cleanClauseText)
	v2 := "- **每日同步**：成员开工前回报计划与阻塞。"
	first := submitClauseOp(t, f, clauseOpBody(f.wsID, "revise_clause", clauseID, "每日同步", v2, nil))
	if code, body := ownerApprove(t, f.ownerID, f.wsID, first); code != http.StatusOK {
		t.Fatalf("revise to v2: expected 200, got %d: %s", code, body)
	}

	v3 := "- **每日同步**：成员开工前回报计划、阻塞和预计完成时间。"
	second := submitClauseOp(t, f, clauseOpBody(f.wsID, "revise_clause", clauseID, "每日同步", v3, nil))
	if code, body := ownerApprove(t, f.ownerID, f.wsID, second); code != http.StatusOK {
		t.Fatalf("revise from v2: expected 200, got %d: %s", code, body)
	}

	var v2Content, v3Content string
	dbfx.QueryRow(t, `SELECT content FROM clause_version WHERE clause_id = $1 AND version = 2`, clauseID).Scan(&v2Content)
	dbfx.QueryRow(t, `SELECT content FROM clause_version WHERE clause_id = $1 AND version = 3`, clauseID).Scan(&v3Content)
	if v2Content != v2 || v3Content != v3 {
		t.Fatalf("version history = v2 %q, v3 %q; want both sequential revisions preserved", v2Content, v3Content)
	}
}

func TestClauseReviseRejectsStaleConcurrentProposal(t *testing.T) {
	f := newClauseInjectionFixture(t)
	clauseID := enactWorkspaceClause(t, f, "每日同步", cleanClauseText)
	firstText := "- **每日同步**：先批准的修订内容。"
	secondText := "- **每日同步**：基于旧版本提交的并发修订。"
	first := submitClauseOp(t, f, clauseOpBody(f.wsID, "revise_clause", clauseID, "每日同步", firstText, nil))
	second := submitClauseOp(t, f, clauseOpBody(f.wsID, "revise_clause", clauseID, "每日同步", secondText, nil))

	if code, body := ownerApprove(t, f.ownerID, f.wsID, first); code != http.StatusOK {
		t.Fatalf("first concurrent revision: expected 200, got %d: %s", code, body)
	}
	code, preview := legislationCall(t, testHandler.PreviewPromptProposal,
		legislationReq(f.memberID, f.wsID, http.MethodPost, "/api/prompt-legislation/proposals/"+second+"/preview", nil, "id", second))
	if code != http.StatusConflict || !strings.Contains(preview, "版本") {
		t.Fatalf("stale concurrent preview = %d: %s, want conflict mentioning version", code, preview)
	}
	if code, body := ownerApprove(t, f.ownerID, f.wsID, second); code == http.StatusOK || !strings.Contains(body, "版本") {
		t.Fatalf("stale concurrent approval = %d: %s, want a version conflict", code, body)
	}

	var count int
	var latest string
	dbfx.QueryRow(t, `SELECT COUNT(*) FROM clause_version WHERE clause_id = $1`, clauseID).Scan(&count)
	dbfx.QueryRow(t, `SELECT content FROM clause_version WHERE clause_id = $1 ORDER BY version DESC LIMIT 1`, clauseID).Scan(&latest)
	if count != 2 || latest != firstText {
		t.Fatalf("stale proposal changed clause history: versions=%d latest=%q", count, latest)
	}
}

func TestClauseReviseRollsBackWhenCarrierRerenderFails(t *testing.T) {
	f := newClauseInjectionFixture(t)
	clauseID := enactWorkspaceClause(t, f, "每日同步", cleanClauseText)
	var carrierBefore string
	dbfx.QueryRow(t, `SELECT context FROM workspace WHERE id = $1`, f.wsID).Scan(&carrierBefore)
	proposal := submitClauseOp(t, f, clauseOpBody(f.wsID, "revise_clause", clauseID, "每日同步",
		"- **每日同步**：触发重渲染失败的修订。", nil))

	suffix := fmt.Sprintf("cinj%d", clauseInjectionSeq)
	functionName := "ruyi880_fail_prompt_version_" + suffix
	triggerName := "ruyi880_fail_prompt_version_trg_" + suffix
	if _, err := testPool.Exec(context.Background(), fmt.Sprintf(`
CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $fn$
BEGIN
	    IF NEW.workspace_id = '%s'::uuid AND NEW.change_note = '条款静态载体规范化' THEN
        RAISE EXCEPTION 'injected clause carrier re-render failure';
    END IF;
    RETURN NEW;
END
$fn$;
CREATE TRIGGER %s BEFORE INSERT ON prompt_version
FOR EACH ROW EXECUTE FUNCTION %s();`, functionName, f.wsID, triggerName, functionName)); err != nil {
		t.Fatalf("install isolated re-render failure trigger: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = testPool.Exec(ctx, fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON prompt_version`, triggerName))
		_, _ = testPool.Exec(ctx, fmt.Sprintf(`DROP FUNCTION IF EXISTS %s()`, functionName))
	})

	if code, body := ownerApprove(t, f.ownerID, f.wsID, proposal); code == http.StatusOK {
		t.Fatalf("approval succeeded despite injected post-asset failure: %s", body)
	}
	var versionCount, activeVersion int
	var carrierAfter string
	dbfx.QueryRow(t, `SELECT COUNT(*) FROM clause_version WHERE clause_id = $1`, clauseID).Scan(&versionCount)
	dbfx.QueryRow(t, `SELECT since_clause_version FROM clause_binding WHERE clause_id = $1 AND status = 'active'`, clauseID).Scan(&activeVersion)
	dbfx.QueryRow(t, `SELECT context FROM workspace WHERE id = $1`, f.wsID).Scan(&carrierAfter)
	if versionCount != 1 || activeVersion != 1 || carrierAfter != carrierBefore {
		t.Fatalf("failed re-render left partial state: versions=%d active_version=%d carrier_changed=%v",
			versionCount, activeVersion, carrierAfter != carrierBefore)
	}
	if status := proposalStatus(t, f.wsID, proposal); status != "pending_owner" {
		t.Fatalf("proposal status after rolled-back write = %q, want pending_owner", status)
	}
}

func TestClauseReviseEnactsNextVersionForSquadBinding(t *testing.T) {
	f := newClauseInjectionFixture(t)
	squadID := dbfx.Squad(t, "revision target squad", f.agentID,
		testutil.Cols{"workspace_id": f.wsID, "instructions": squadCarrierText})

	create := clauseOpBody(f.wsID, "add_clause", "", "小队交接", "- **小队交接**：交接时说明现状。", nil)
	create["carrier_scope"] = "squad"
	create["carrier_scope_id"] = squadID
	createID := submitClauseOp(t, f, create)
	if code, body := ownerApprove(t, f.ownerID, f.wsID, createID); code != http.StatusOK {
		t.Fatalf("squad clause create: expected 200, got %d: %s", code, body)
	}
	var clauseID string
	dbfx.QueryRow(t, `SELECT clause_id::text FROM prompt_proposal WHERE workspace_id = $1 AND id = $2`, f.wsID, createID).Scan(&clauseID)
	if clauseID == "" {
		t.Fatal("squad clause create did not persist clause_id")
	}

	proposed := "- **小队交接**：交接时同步当前进度、未决风险和下一步责任人。"
	revise := clauseOpBody(f.wsID, "revise_clause", clauseID, "小队交接", proposed, nil)
	revise["carrier_scope"] = "squad"
	revise["carrier_scope_id"] = squadID
	reviseID := submitClauseOp(t, f, revise)
	code, previewBody := legislationCall(t, testHandler.PreviewPromptProposal,
		legislationReq(f.memberID, f.wsID, http.MethodPost, "/api/prompt-legislation/proposals/"+reviseID+"/preview", nil, "id", reviseID))
	if code != http.StatusOK || !strings.Contains(previewBody, "下一步责任人") {
		t.Fatalf("squad clause preview: expected revised diff, got %d: %s", code, previewBody)
	}
	if code, body := ownerApprove(t, f.ownerID, f.wsID, reviseID); code != http.StatusOK {
		t.Fatalf("squad clause revise: expected 200, got %d: %s", code, body)
	}

	var since int32
	var instructions string
	dbfx.QueryRow(t, `SELECT since_clause_version FROM clause_binding WHERE clause_id = $1 AND status = 'active'`, clauseID).Scan(&since)
	dbfx.QueryRow(t, `SELECT instructions FROM squad WHERE id = $1`, squadID).Scan(&instructions)
	if since != 2 || strings.Contains(instructions, proposed) || strings.Contains(instructions, "clause:id=") {
		t.Fatalf("squad revision = binding v%d, instructions:\n%s", since, instructions)
	}
}

func TestClauseRemoveRetiresClauseAndCarrier(t *testing.T) {
	f := newClauseInjectionFixture(t)
	clauseID := enactWorkspaceClause(t, f, "每日同步", cleanClauseText)

	id := submitClauseOp(t, f, clauseOpBody(f.wsID, "remove_clause", clauseID, "每日同步", "", nil))
	if code, body := ownerApprove(t, f.ownerID, f.wsID, id); code != http.StatusOK {
		t.Fatalf("remove approve: expected 200, got %d: %s", code, body)
	}

	var status string
	dbfx.QueryRow(t, `SELECT status FROM clause WHERE id = $1`, clauseID).Scan(&status)
	if status != "deprecated" {
		t.Fatalf("clause status = %q, want deprecated", status)
	}
	var active int
	dbfx.QueryRow(t, `SELECT COUNT(*) FROM clause_binding WHERE clause_id = $1 AND status = 'active'`, clauseID).Scan(&active)
	if active != 0 {
		t.Fatalf("%d active bindings left, want 0", active)
	}
	var context string
	dbfx.QueryRow(t, `SELECT context FROM workspace WHERE id = $1`, f.wsID).Scan(&context)
	if strings.Contains(context, "clause:id=") || strings.Contains(context, "每日同步") {
		t.Fatalf("carrier still carries the removed clause:\n%s", context)
	}
}

func TestClauseMoveBetweenCarriers(t *testing.T) {
	f := newClauseInjectionFixture(t)
	squadID := dbfx.Squad(t, "move target squad", f.agentID,
		testutil.Cols{"workspace_id": f.wsID, "instructions": squadCarrierText})
	clauseID := enactWorkspaceClause(t, f, "每日同步", cleanClauseText)

	id := submitClauseOp(t, f, clauseOpBody(f.wsID, "move_clause", clauseID, "每日同步", "", map[string]any{
		"move_to_scope":    "squad",
		"move_to_scope_id": squadID,
	}))
	if code, body := ownerApprove(t, f.ownerID, f.wsID, id); code != http.StatusOK {
		t.Fatalf("move approve: expected 200, got %d: %s", code, body)
	}

	// The source binding is superseded, the destination is active, and the
	// clause itself stays active (a move relocates, it does not retire).
	var superseded, active int
	dbfx.QueryRow(t, `SELECT COUNT(*) FROM clause_binding WHERE clause_id = $1 AND status = 'superseded'`, clauseID).Scan(&superseded)
	dbfx.QueryRow(t, `SELECT COUNT(*) FROM clause_binding WHERE clause_id = $1 AND status = 'active' AND scope = 'squad' AND scope_id = $2`, clauseID, squadID).Scan(&active)
	if superseded != 1 || active != 1 {
		t.Fatalf("bindings after move: superseded=%d active-squad=%d, want 1/1", superseded, active)
	}
	var clauseStatus string
	dbfx.QueryRow(t, `SELECT status FROM clause WHERE id = $1`, clauseID).Scan(&clauseStatus)
	if clauseStatus != "active" {
		t.Fatalf("clause status after move = %q, want active", clauseStatus)
	}

	var squadInstructions string
	dbfx.QueryRow(t, `SELECT instructions FROM squad WHERE id = $1`, squadID).Scan(&squadInstructions)
	if strings.Contains(squadInstructions, "每日同步") || strings.Contains(squadInstructions, "<!-- clause:id=") {
		t.Fatalf("destination carrier retained dynamic clause content:\n%s", squadInstructions)
	}
	var wsContext string
	dbfx.QueryRow(t, `SELECT context FROM workspace WHERE id = $1`, f.wsID).Scan(&wsContext)
	if strings.Contains(wsContext, "clause:id=") || strings.Contains(wsContext, "每日同步") {
		t.Fatalf("source carrier retained dynamic clause content:\n%s", wsContext)
	}
}

// Legacy name-only proposals keep working: an unqualified clause_name
// resolves to the single clause asset at creation (ADR 007 §8 migration
// path); resolution is re-checked fail-closed at enact.
func TestProposalLegacyNameReferenceStillCreates(t *testing.T) {
	f := newClauseInjectionFixture(t)
	clauseID := enactWorkspaceClause(t, f, "每日同步", cleanClauseText)

	id := submitClauseOp(t, f, clauseOpBody(f.wsID, "revise_clause", "", "每日同步",
		"- **每日同步**：各成员开工前在任务单回报当日计划与阻塞（更新）。", nil))
	var resolved string
	dbfx.QueryRow(t, `SELECT clause_id::text FROM prompt_proposal WHERE workspace_id = $1 AND id = $2`,
		f.wsID, id).Scan(&resolved)
	if resolved != clauseID {
		t.Fatalf("name-only draft resolved to %q, want %q", resolved, clauseID)
	}
}

func TestClauseGovernanceOverviewAndDetail(t *testing.T) {
	f := newClauseInjectionFixture(t)
	clauseID := enactWorkspaceClause(t, f, "每日同步", cleanClauseText)

	code, body := legislationCall(t, testHandler.ListClauseGovernance,
		legislationReq(f.memberID, f.wsID, http.MethodGet, "/api/prompt-legislation/governance/clauses", nil))
	if code != http.StatusOK {
		t.Fatalf("overview: expected 200, got %d: %s", code, body)
	}
	var overview struct {
		Clauses []struct {
			ID            string `json:"id"`
			Slug          string `json:"slug"`
			LatestVersion int32  `json:"latest_version"`
			BoundScope    string `json:"bound_scope"`
			TokensPending bool   `json:"tokens_pending"`
		} `json:"clauses"`
		StatusCounts map[string]int64 `json:"status_counts"`
		Migration    struct {
			TotalCarriers int64 `json:"total_carriers"`
			BoundCarriers int64 `json:"bound_carriers"`
		} `json:"migration"`
		RollbackSuggestions []map[string]any `json:"rollback_suggestions"`
	}
	if err := json.Unmarshal([]byte(body), &overview); err != nil {
		t.Fatalf("decode overview: %v (%s)", err, body)
	}
	if len(overview.Clauses) != 1 || overview.Clauses[0].ID != clauseID ||
		overview.Clauses[0].LatestVersion != 1 || overview.Clauses[0].BoundScope != "workspace" {
		t.Fatalf("overview clauses = %+v", overview.Clauses)
	}
	if !overview.Clauses[0].TokensPending {
		t.Fatalf("no rollup rows yet, tokens_pending must be true")
	}
	if overview.StatusCounts["active"] != 1 || overview.Migration.BoundCarriers != 1 {
		t.Fatalf("totals = counts %+v bound %d", overview.StatusCounts, overview.Migration.BoundCarriers)
	}
	if len(overview.RollbackSuggestions) != 0 {
		t.Fatalf("unexpected rollback suggestions: %s", body)
	}

	req := legislationReq(f.memberID, f.wsID, http.MethodGet, "/api/prompt-legislation/governance/clauses/"+clauseID, nil)
	code, body = legislationCall(t, testHandler.GetClauseGovernanceDetail, withURLParams(req, "clauseId", clauseID))
	if code != http.StatusOK {
		t.Fatalf("detail: expected 200, got %d: %s", code, body)
	}
	var detail struct {
		ID       string `json:"id"`
		Versions []struct {
			Version int32 `json:"version"`
		} `json:"versions"`
		Bindings []struct {
			Scope              string `json:"scope"`
			Status             string `json:"status"`
			SinceClauseVersion int32  `json:"since_clause_version"`
		} `json:"bindings"`
	}
	if err := json.Unmarshal([]byte(body), &detail); err != nil {
		t.Fatalf("decode detail: %v (%s)", err, body)
	}
	if detail.ID != clauseID || len(detail.Versions) != 1 || detail.Versions[0].Version != 1 {
		t.Fatalf("detail versions = %+v", detail.Versions)
	}
	if len(detail.Bindings) != 1 || detail.Bindings[0].Scope != "workspace" || detail.Bindings[0].Status != "active" {
		t.Fatalf("detail bindings = %+v", detail.Bindings)
	}

	// A clause from another workspace does not resolve here.
	other := newClauseInjectionFixture(t)
	req = legislationReq(f.memberID, f.wsID, http.MethodGet, "/api/prompt-legislation/governance/clauses/x", nil)
	code, _ = legislationCall(t, testHandler.GetClauseGovernanceDetail, withURLParams(req, "clauseId", other.mustEnact(t)))
	if code != http.StatusNotFound {
		t.Fatalf("cross-workspace detail: expected 404, got %d", code)
	}
}

// mustEnact is the fixture-scoped form of enactWorkspaceClause returning the
// fresh clause id (the helper takes the fixture as its receiver-space twin).
func (f *clauseInjectionFixture) mustEnact(t *testing.T) string {
	t.Helper()
	return enactWorkspaceClause(t, f, "每日同步", cleanClauseText)
}

// The 层集合 ⊆ invariant: an injection attributed to a carrier scope whose
// key is missing from the task's prompt_versions attribution is a violation
// the reconciliation view must surface.
func TestClauseReconciliationLayerViolation(t *testing.T) {
	f := newClauseInjectionFixture(t)
	clauseID := enactWorkspaceClause(t, f, "每日同步", cleanClauseText)

	code, body := legislationCall(t, testHandler.GetClauseReconciliation,
		legislationReq(f.memberID, f.wsID, http.MethodGet, "/api/prompt-legislation/governance/reconciliation", nil))
	if code != http.StatusOK {
		t.Fatalf("reconciliation: expected 200, got %d: %s", code, body)
	}
	var before struct {
		Healthy bool `json:"healthy"`
	}
	json.Unmarshal([]byte(body), &before)
	if !before.Healthy {
		t.Fatalf("fresh workspace not healthy: %s", body)
	}

	// A squad-scoped injection whose task attribution only carries the
	// workspace key — the write path cannot produce this (it records every
	// injected scope), so the row models attribution drift after the fact.
	var taskID string
	dbfx.QueryRow(t, `
		INSERT INTO agent_task_queue (agent_id, issue_id, status, priority, attempt, completed_at, prompt_versions)
		VALUES ($1, $2, 'completed', 0, 1, now(), $3::jsonb) RETURNING id`,
		f.agentID, f.issueID, `{"workspace": "`+f.wsID+`"}`).Scan(&taskID)
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM clause_injection WHERE task_id = $1`, taskID) })
	dbfx.Exec(t, `
		INSERT INTO clause_injection (workspace_id, task_id, clause_id, clause_version, carrier_scope)
		VALUES ($1, $2, $3, 1, 'squad')`, f.wsID, taskID, clauseID)

	code, body = legislationCall(t, testHandler.GetClauseReconciliation,
		legislationReq(f.memberID, f.wsID, http.MethodGet, "/api/prompt-legislation/governance/reconciliation", nil))
	if code != http.StatusOK {
		t.Fatalf("reconciliation 2: expected 200, got %d: %s", code, body)
	}
	var after struct {
		Healthy    bool `json:"healthy"`
		Violations []struct {
			TaskID       string `json:"task_id"`
			CarrierScope string `json:"carrier_scope"`
		} `json:"layer_set_violations"`
	}
	if err := json.Unmarshal([]byte(body), &after); err != nil {
		t.Fatalf("decode reconciliation: %v (%s)", err, body)
	}
	if after.Healthy || len(after.Violations) != 1 || after.Violations[0].CarrierScope != "squad" {
		t.Fatalf("violations = %+v healthy=%v, want one squad violation", after.Violations, after.Healthy)
	}
}

// Drift observes only legacy marker data. Active clauses are assembled from
// the asset library, so a static carrier has no missing-segment condition.
func TestClauseReconciliationDriftKinds(t *testing.T) {
	t.Run("stale_rev", func(t *testing.T) {
		f := newClauseInjectionFixture(t)
		enactWorkspaceClause(t, f, "每日同步", cleanClauseText)
		dbfx.Exec(t, `UPDATE workspace SET context = context || $2 WHERE id = $1`, f.wsID,
			"\n<!-- clause:id=cls_aaa00001 rev=7 -->\n")
		assertSingleDriftKind(t, f, "stale_rev")
	})
	t.Run("orphan_marker", func(t *testing.T) {
		f := newClauseInjectionFixture(t)
		enactWorkspaceClause(t, f, "每日同步", cleanClauseText)
		// A hand-pasted marker referencing no live clause (or any clause).
		dbfx.Exec(t, `UPDATE workspace SET context = context || $2 WHERE id = $1`, f.wsID,
			"\n<!-- clause:id=cls_zzzzzzzz rev=1 -->\n")
		assertSingleDriftKind(t, f, "orphan_marker")
	})
	t.Run("static_carrier", func(t *testing.T) {
		f := newClauseInjectionFixture(t)
		enactWorkspaceClause(t, f, "每日同步", cleanClauseText)
		recon := getReconciliation(t, f)
		if !recon.Healthy || len(recon.DriftCarriers) != 0 {
			t.Fatalf("static carrier was reported as drift: %+v", recon.DriftCarriers)
		}
	})
}

func TestExpectedClausesByCarrierKeepsProjectBindingsSeparate(t *testing.T) {
	boundID := pgtype.UUID{Bytes: [16]byte{1}, Valid: true}
	unboundID := pgtype.UUID{Bytes: [16]byte{2}, Valid: true}
	expected := expectedClausesByCarrier([]db.ListActiveWorkspaceClausesRow{{
		Scope: "project", ScopeID: boundID, Slug: "project-clause", Version: 1,
	}})

	if got := expected[clauseCarrierKey{Scope: "project", ScopeID: boundID}]["project-clause"]; got != 1 {
		t.Fatalf("bound project clause version = %d, want 1", got)
	}
	if _, found := expected[clauseCarrierKey{Scope: "project", ScopeID: unboundID}]["project-clause"]; found {
		t.Fatal("project clause leaked to an unbound project")
	}
}

// A clause bound to one project is not expected in every project. The drift
// scan must use both the carrier scope and its ID when matching bindings.
func TestClauseReconciliationScopesExpectedClausesToCarrier(t *testing.T) {
	f := newClauseInjectionFixture(t)
	boundProjectID := dbfx.Project(t, "bound project", testutil.Cols{
		"workspace_id": f.wsID,
		"instructions": "# Bound project\n\n## Project rules\n",
	})
	dbfx.Project(t, "unbound project", testutil.Cols{
		"workspace_id": f.wsID,
		"instructions": "# Unbound project\n\n## Project rules\n",
	})

	body := legislationProposalBody(f.wsID, "project clause", cleanClauseText)
	body.CarrierScope = "project"
	body.CarrierScopeID = boundProjectID
	body.TargetSection = "Project rules"
	body.GateAnswerLayer = "project=项目事实"
	code, response := legislationCall(t, testHandler.CreatePromptProposal,
		legislationReq(f.memberID, f.wsID, http.MethodPost, "/api/prompt-legislation/proposals", body))
	if code != http.StatusCreated {
		t.Fatalf("create project clause draft: expected 201, got %d: %s", code, response)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(response), &created); err != nil {
		t.Fatalf("decode project clause draft: %v (%s)", err, response)
	}
	submitDraft(t, f.wsID, f.memberID, created.ID)
	if code, response = ownerApprove(t, f.ownerID, f.wsID, created.ID); code != http.StatusOK {
		t.Fatalf("approve project clause: expected 200, got %d: %s", code, response)
	}

	code, response = legislationCall(t, testHandler.GetClauseReconciliation,
		legislationReq(f.memberID, f.wsID, http.MethodGet, "/api/prompt-legislation/governance/reconciliation", nil))
	if code != http.StatusOK {
		t.Fatalf("reconciliation: expected 200, got %d: %s", code, response)
	}
	var reconciliation struct {
		Healthy bool `json:"healthy"`
		Drift   []struct {
			Scope   string `json:"scope"`
			ScopeID string `json:"scope_id"`
		} `json:"drift"`
	}
	if err := json.Unmarshal([]byte(response), &reconciliation); err != nil {
		t.Fatalf("decode reconciliation: %v (%s)", err, response)
	}
	if !reconciliation.Healthy || len(reconciliation.Drift) != 0 {
		t.Fatalf("project-scoped clause leaked to another carrier: %s", response)
	}
}

func assertSingleDriftKind(t *testing.T, f *clauseInjectionFixture, kind string) {
	t.Helper()
	code, body := legislationCall(t, testHandler.GetClauseReconciliation,
		legislationReq(f.memberID, f.wsID, http.MethodGet, "/api/prompt-legislation/governance/reconciliation", nil))
	if code != http.StatusOK {
		t.Fatalf("reconciliation: expected 200, got %d: %s", code, body)
	}
	var resp struct {
		Healthy bool `json:"healthy"`
		Drift   []struct {
			Scope   string `json:"scope"`
			Entries []struct {
				Kind string `json:"kind"`
				Slug string `json:"slug"`
			} `json:"entries"`
		} `json:"drift"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if resp.Healthy {
		t.Fatalf("%s: reconciliation claims healthy: %s", kind, body)
	}
	found := false
	for _, c := range resp.Drift {
		for _, e := range c.Entries {
			if e.Kind == kind {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("%s: drift = %s", kind, body)
	}
}

// A version pair past the sample gate with a significant regression gets
// exactly one system-drafted rollback proposal, and the governance overview
// lists it (ADR 007 §7 — the statistics to governance-view seam).
func TestRollbackSuggestionSurfacesInGovernance(t *testing.T) {
	f := newClauseInjectionFixture(t)
	clauseID := enactWorkspaceClause(t, f, "每日同步", cleanClauseText)
	reviseID := submitClauseOp(t, f, clauseOpBody(f.wsID, "revise_clause", clauseID, "每日同步",
		"- **每日同步**：各成员开工前在任务单回报当日计划、阻塞与预计完成时间。", nil))
	if code, body := ownerApprove(t, f.ownerID, f.wsID, reviseID); code != http.StatusOK {
		t.Fatalf("revise approve: expected 200, got %d: %s", code, body)
	}

	// Two full windows: v1 at 30% attributable, v2 at 60% — significant.
	seedClauseWindow(t, f.wsID, clauseID, 1, 10, 3, 5)
	seedClauseWindow(t, f.wsID, clauseID, 2, 10, 15, 5)

	checker := clausequalityrollup.RollbackChecker{Queries: db.New(testPool)}
	out := checker.EvaluateClauses(context.Background(), []pgtype.UUID{parseUUID(clauseID)})
	if len(out.Failures) != 0 {
		t.Fatalf("evaluate: %+v", out.Failures)
	}

	var drafts int
	dbfx.QueryRow(t, `SELECT COUNT(*) FROM prompt_proposal
		WHERE clause_id = $1 AND rollback_of_version = 2 AND source = 'system'`, clauseID).Scan(&drafts)
	if drafts != 1 {
		t.Fatalf("rollback drafts = %d, want 1", drafts)
	}

	code, body := legislationCall(t, testHandler.ListClauseGovernance,
		legislationReq(f.memberID, f.wsID, http.MethodGet, "/api/prompt-legislation/governance/clauses", nil))
	if code != http.StatusOK {
		t.Fatalf("overview: expected 200, got %d: %s", code, body)
	}
	var overview struct {
		RollbackSuggestions []map[string]any `json:"rollback_suggestions"`
	}
	json.Unmarshal([]byte(body), &overview)
	if len(overview.RollbackSuggestions) != 1 {
		t.Fatalf("overview rollback_suggestions = %d, want 1: %s", len(overview.RollbackSuggestions), body)
	}
}

// TestClauseGovernanceSearchFiltersOverviewRows (RUYI-868): the keyword
// endpoint filters the overview's own rows by slug/title and exact status,
// and it is workspace-scoped like the overview is.
func TestClauseGovernanceSearchFiltersOverviewRows(t *testing.T) {
	f := newClauseInjectionFixture(t)
	enactWorkspaceClause(t, f, "每日同步", cleanClauseText)
	// The gate checks the clause text's first-line label against the clause
	// name, so the second clause carries its own labelled text.
	const detailClauseText = `- **每日同步详细版**：各成员开工前在任务单回报当日计划、阻塞与预计完成时间。`
	enactWorkspaceClause(t, f, "每日同步详细版", detailClauseText)

	// q=每日 matches both clauses by title; the detailed variant is
	// reachable only through its longer title.
	code, body := legislationCall(t, testHandler.SearchClauseGovernance,
		legislationReq(f.memberID, f.wsID, http.MethodGet, "/api/prompt-legislation/governance/clauses/search?q=每日", nil))
	if code != http.StatusOK {
		t.Fatalf("search q=每日: expected 200, got %d: %s", code, body)
	}
	var out struct {
		Matched int `json:"matched"`
		Clauses []struct {
			ID    string `json:"id"`
			Slug  string `json:"slug"`
			Title string `json:"title"`
		} `json:"clauses"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode search: %v (%s)", err, body)
	}
	if out.Matched != 2 || len(out.Clauses) != 2 {
		t.Fatalf("q=每日 matched = %d, want 2: %s", out.Matched, body)
	}

	code, body = legislationCall(t, testHandler.SearchClauseGovernance,
		legislationReq(f.memberID, f.wsID, http.MethodGet, "/api/prompt-legislation/governance/clauses/search?q=详细&status=active", nil))
	if code != http.StatusOK {
		t.Fatalf("search q=详细: expected 200, got %d: %s", code, body)
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode search: %v (%s)", err, body)
	}
	if out.Matched != 1 || out.Clauses[0].Title != "每日同步详细版" {
		t.Fatalf("q=详细&status=active = %+v, want exactly the detailed clause", out.Clauses)
	}

	// The status filter alone narrows without a keyword.
	code, body = legislationCall(t, testHandler.SearchClauseGovernance,
		legislationReq(f.memberID, f.wsID, http.MethodGet, "/api/prompt-legislation/governance/clauses/search?status=active", nil))
	if code != http.StatusOK {
		t.Fatalf("search status=active: expected 200, got %d: %s", code, body)
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode search: %v (%s)", err, body)
	}
	if out.Matched != 2 {
		t.Fatalf("status=active matched = %d, want 2: %s", out.Matched, body)
	}

	// No filter is a misuse: the overview is the list view.
	code, body = legislationCall(t, testHandler.SearchClauseGovernance,
		legislationReq(f.memberID, f.wsID, http.MethodGet, "/api/prompt-legislation/governance/clauses/search", nil))
	if code != http.StatusBadRequest {
		t.Fatalf("search without q/status: expected 400, got %d: %s", code, body)
	}

	// Workspace isolation: a clause that exists in another workspace is
	// invisible to this workspace's search, not merely absent.
	other := newClauseInjectionFixture(t)
	otherID := enactWorkspaceClause(t, other, "每日同步", cleanClauseText)
	code, body = legislationCall(t, testHandler.SearchClauseGovernance,
		legislationReq(f.memberID, f.wsID, http.MethodGet, "/api/prompt-legislation/governance/clauses/search?q=每日", nil))
	if code != http.StatusOK {
		t.Fatalf("search after second workspace: expected 200, got %d: %s", code, body)
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode search: %v (%s)", err, body)
	}
	if out.Matched != 2 {
		t.Fatalf("search leaked or lost clauses: matched = %d, want 2 (own workspace only)", out.Matched)
	}
	for _, c := range out.Clauses {
		if c.ID == otherID {
			t.Fatalf("search returned clause %s from another workspace", otherID)
		}
	}
}

// TestRequireHumanOrOAuthActorGatesLegislationRouter (RUYI-868): the router
// wiring for the opened surface — the unscoped machine credentials are
// rejected at the guard, and a plain member request passes the whole chain
// including workspace context resolution.
func TestRequireHumanOrOAuthActorGatesLegislationRouter(t *testing.T) {
	f := newClauseInjectionFixture(t)

	// The router.go order for the opened group: guard outside, the
	// workspace-member middleware inside.
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		testHandler.ListClauseGovernance(w, r)
	})
	chain := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Serve the composed guard → member chain directly, in the router.go
		// order.
		RequireHumanOrOAuthActor(
			middleware.RequireWorkspaceMember(testHandler.Queries)(inner),
		).ServeHTTP(w, r)
	})

	req := legislationReq(f.memberID, f.wsID, http.MethodGet, "/api/prompt-legislation/governance/clauses", nil)
	req.Header.Set("X-Actor-Source", "task_token")
	if code, body := legislationCall(t, chain, req); code != http.StatusForbidden {
		t.Fatalf("task_token: expected 403 at the guard, got %d: %s", code, body)
	}

	req = legislationReq(f.memberID, f.wsID, http.MethodGet, "/api/prompt-legislation/governance/clauses", nil)
	req.Header.Set("X-Actor-Source", "cloud_pat")
	if code, body := legislationCall(t, chain, req); code != http.StatusForbidden {
		t.Fatalf("cloud_pat: expected 403 at the guard, got %d: %s", code, body)
	}

	req = legislationReq(f.memberID, f.wsID, http.MethodGet, "/api/prompt-legislation/governance/clauses", nil)
	req.Header.Set("X-Actor-Source", "oauth")
	if code, body := legislationCall(t, chain, req); code != http.StatusOK {
		t.Fatalf("oauth member: expected 200, got %d: %s", code, body)
	}

	req = legislationReq(f.memberID, f.wsID, http.MethodGet, "/api/prompt-legislation/governance/clauses", nil)
	if code, body := legislationCall(t, chain, req); code != http.StatusOK {
		t.Fatalf("human member: expected 200, got %d: %s", code, body)
	}
}

// seedClauseWindow writes one daily row per day for a version window, as if
// the clause quality rollup had recorded it.
func seedClauseWindow(t *testing.T, wsID, clauseID string, version, runs, attributable, days int) {
	t.Helper()
	for i := 0; i < days; i++ {
		dbfx.Exec(t, `
			INSERT INTO clause_quality_daily (workspace_id, clause_id, clause_version, day,
				finished_runs, attributable_failed_runs, excluded_failed_runs,
				discipline_covered_runs, discipline_deductions, failure_reason_counts,
				tool_results_measured, tool_results_error, attempt_total, retried_runs,
				first_pass_issues, reviewed_issues)
			VALUES ($1, $2, $3, (now() AT TIME ZONE 'UTC')::date - $4::int, $5, $6, 0,
				0, '[]'::jsonb, '{}'::jsonb, 0, 0, $5, 0, 0, 0)
			ON CONFLICT (clause_id, clause_version, day) DO UPDATE
				SET finished_runs = EXCLUDED.finished_runs,
				    attributable_failed_runs = EXCLUDED.attributable_failed_runs`,
			wsID, clauseID, version, i+1, runs, attributable)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(),
			`DELETE FROM clause_quality_daily WHERE clause_id = $1`, clauseID)
	})
}
