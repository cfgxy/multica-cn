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
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/clausequalityrollup"
	"github.com/multica-ai/multica/server/internal/testutil"
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
		"carrier_scope":        "workspace",
		"carrier_scope_id":     wsID,
		"target_section":       "沟通规范",
		"change_kind":          changeKind,
		"clause_name":          clauseName,
		"clause_text":          clauseText,
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

	id := submitClauseOp(t, f, clauseOpBody(f.wsID, "revise_clause", clauseID, "每日同步",
		"- **每日同步**：各成员开工前在任务单回报当日计划、阻塞与预计完成时间。", nil))
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
	var context string
	dbfx.QueryRow(t, `SELECT context FROM workspace WHERE id = $1`, f.wsID).Scan(&context)
	if !strings.Contains(context, "回报当日计划、阻塞与预计完成时间") ||
		!strings.Contains(context, "rev=2") {
		t.Fatalf("carrier not re-rendered at rev 2:\n%s", context)
	}
	var enactedVersion int
	dbfx.QueryRow(t, `SELECT enacted_version FROM prompt_proposal WHERE workspace_id = $1 AND id = $2`,
		f.wsID, id).Scan(&enactedVersion)
	if enactedVersion != 2 {
		t.Fatalf("proposal enacted_version = %d, want 2", enactedVersion)
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
		"move_to_scope":   "squad",
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
	if !strings.Contains(squadInstructions, "每日同步") || !strings.Contains(squadInstructions, "<!-- clause:id=") {
		t.Fatalf("destination carrier missing the clause region:\n%s", squadInstructions)
	}
	var wsContext string
	dbfx.QueryRow(t, `SELECT context FROM workspace WHERE id = $1`, f.wsID).Scan(&wsContext)
	if strings.Contains(wsContext, "clause:id=") {
		t.Fatalf("source carrier still carries the clause region:\n%s", wsContext)
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
		Healthy     bool `json:"healthy"`
		Violations  []struct {
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

// Drift is observed per carrier: a stale rev, an orphan marker and a
// missing segment are each flagged against the active asset set.
func TestClauseReconciliationDriftKinds(t *testing.T) {
	t.Run("stale_rev", func(t *testing.T) {
		f := newClauseInjectionFixture(t)
		enactWorkspaceClause(t, f, "每日同步", cleanClauseText)
		dbfx.Exec(t, `UPDATE workspace SET context = replace(context, 'rev=1', 'rev=7') WHERE id = $1`, f.wsID)
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
	t.Run("missing_segment", func(t *testing.T) {
		f := newClauseInjectionFixture(t)
		enactWorkspaceClause(t, f, "每日同步", cleanClauseText)
		// The segment is gone but the clause is still bound — exactly what a
		// pre-marker restore of the column looks like.
		dbfx.Exec(t, `UPDATE workspace SET context = $2 WHERE id = $1`, f.wsID, legislationCarrierText)
		assertSingleDriftKind(t, f, "missing_segment")
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
