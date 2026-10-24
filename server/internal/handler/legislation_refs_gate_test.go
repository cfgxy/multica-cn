package handler

// RUYI-869: the clause reference contract enforced at the legislation gate
// (owner instruction 2026-10-24 — "结构化条款不得引用静态提示词，如果有这种
// 情况，说明条款没有提炼干净"). These tests drive the real API surface:
//
//   - a revision whose body cites its carrier's static section is parked in
//     gate_failed with the 反向静态引用 finding;
//   - an add whose body names a slug that is not in the active set is
//     gate_failed with the 缺失引用 finding;
//   - a remove that would leave a surviving ref is gate_failed;
//   - a revision that closes an A→B→A reference cycle is gate_failed with
//     the 循环引用 finding;
//   - and the happy path pins the deterministic ordering: the carrier's
//     rendered regions sit in ascending slug order, the total order the
//     effective-set SQL and clausemark.Render both emit.
//
// The pure-function matrix of the check itself lives in
// internal/legislation/refs_test.go; this file proves the gate wiring.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/pkg/clausemark"
)

// clauseSlugOfProposal reads the enacted clause slug of a proposal row; ""
// when the row did not enact (no clause_id yet).
func clauseSlugOfProposal(t *testing.T, wsID, proposalID string) string {
	t.Helper()
	var slug string
	if err := testPool.QueryRow(context.Background(),
		`SELECT c.slug FROM clause c JOIN prompt_proposal pp ON pp.clause_id = c.id
		  WHERE pp.workspace_id = $1 AND pp.id = $2`, wsID, proposalID).Scan(&slug); err != nil {
		t.Fatalf("read clause slug: %v", err)
	}
	return slug
}

// createProposal posts a raw proposal body (the shared createDraft helper
// only covers add_clause) and returns the created id.
func createProposal(t *testing.T, wsID, memberID string, body proposalReqBody) string {
	t.Helper()
	code, resp := legislationCall(t, testHandler.CreatePromptProposal,
		legislationReq(memberID, wsID, http.MethodPost, "/api/prompt-legislation/proposals", body))
	if code != http.StatusCreated {
		t.Fatalf("create proposal: expected 201, got %d: %s", code, resp)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(resp), &created); err != nil {
		t.Fatalf("decode created proposal: %s (%s)", err, resp)
	}
	return created.ID
}

// enactAddClause walks one add_clause through the real API and returns the
// proposal id.
func enactAddClause(t *testing.T, wsID, ownerID, memberID, name, text string) string {
	t.Helper()
	id := createDraft(t, wsID, memberID, name, text)
	submitDraft(t, wsID, memberID, id)
	if code, body := ownerApprove(t, ownerID, wsID, id); code != http.StatusOK {
		t.Fatalf("enact %s: expected 200, got %d: %s", name, code, body)
	}
	return id
}

// TestRefsGateReverseStaticRefRejected: a revision citing the carrier's
// static section fails the gate — the owner's contract that a clause
// pointing back at the static prompt means the rule was never extracted.
func TestRefsGateReverseStaticRefRejected(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	wsID, ownerID, memberID := legislationFixture(t)
	enactAddClause(t, wsID, ownerID, memberID, "每日同步", cleanClauseText)

	body := legislationProposalBody(wsID, "每日同步", "- **每日同步**：按 沟通规范 的第一条在任务单回报。")
	body.ChangeKind = "revise_clause"
	idR := createProposal(t, wsID, memberID, body)
	submitDraft(t, wsID, memberID, idR)
	code, resp := ownerApprove(t, ownerID, wsID, idR)
	if code != http.StatusConflict {
		t.Fatalf("reverse static ref: expected 409 gate-failed, got %d: %s", code, resp)
	}
	if !strings.Contains(resp, "gate_failed") {
		t.Fatalf("gate-failed response missing verdict: %s", resp)
	}
	errs := gateErrorsRow(t, wsID, idR)
	if !strings.Contains(errs, "反向静态引用") {
		t.Fatalf("gate_errors missing the 反向静态引用 finding: %s", errs)
	}
	if got := proposalStatus(t, wsID, idR); got != "gate_failed" {
		t.Fatalf("status = %q, want gate_failed", got)
	}
}

// TestRefsGateMissingRefRejected: an add whose body names a slug that is
// not in the active set fails the gate — the reference points at nothing
// the runtime will inject.
func TestRefsGateMissingRefRejected(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	wsID, ownerID, memberID := legislationFixture(t)

	dangling := "cls_deadbeef" // well-formed shape, no clause carries it
	body := legislationProposalBody(wsID, "引用纪律", "- **引用纪律**：归档须对照 "+dangling+" 的约定执行。")
	idN := createProposal(t, wsID, memberID, body)
	submitDraft(t, wsID, memberID, idN)
	code, resp := ownerApprove(t, ownerID, wsID, idN)
	if code != http.StatusConflict {
		t.Fatalf("missing ref: expected 409 gate-failed, got %d: %s", code, resp)
	}
	errs := gateErrorsRow(t, wsID, idN)
	if !strings.Contains(errs, "缺失引用") {
		t.Fatalf("gate_errors missing the 缺失引用 finding: %s", errs)
	}
}

// TestRefsGateRemoveLeavesDanglingRefRejected: removing a clause that other
// clause bodies reference is gate_failed — the survivor would point at
// nothing the runtime injects.
func TestRefsGateRemoveLeavesDanglingRefRejected(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	wsID, ownerID, memberID := legislationFixture(t)

	idA := enactAddClause(t, wsID, ownerID, memberID, "每日同步", cleanClauseText)
	slugA := clauseSlugOfProposal(t, wsID, idA)
	if slugA == "" {
		t.Fatal("enacted clause has no slug")
	}
	// B cites A by slug: the forward direction, gate-passing.
	enactAddClause(t, wsID, ownerID, memberID, "归档决策",
		"- **归档决策**：归档按 "+slugA+" 的约定执行。")

	body := legislationProposalBody(wsID, "每日同步", "")
	body.ChangeKind = "remove_clause"
	idR := createProposal(t, wsID, memberID, body)
	submitDraft(t, wsID, memberID, idR)
	code, resp := ownerApprove(t, ownerID, wsID, idR)
	if code != http.StatusConflict {
		t.Fatalf("remove with surviving ref: expected 409 gate-failed, got %d: %s", code, resp)
	}
	errs := gateErrorsRow(t, wsID, idR)
	if !strings.Contains(errs, "缺失引用") {
		t.Fatalf("gate_errors missing the 缺失引用 finding: %s", errs)
	}
	// Both clauses survive the rejected remove.
	if n := dbfx.Count(t, `SELECT count(*) FROM clause WHERE workspace_id = $1`, wsID); n != 2 {
		t.Fatalf("clauses after rejected remove = %d, want 2", n)
	}
}

// TestRefsGateCycleRejected: a revision closing an A→B→A reference cycle
// fails the gate — cyclic clauses re-introduce the circular loops the
// owner banned; references must form a DAG.
func TestRefsGateCycleRejected(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	wsID, ownerID, memberID := legislationFixture(t)

	idA := enactAddClause(t, wsID, ownerID, memberID, "每日同步", cleanClauseText)
	slugA := clauseSlugOfProposal(t, wsID, idA)
	idB := enactAddClause(t, wsID, ownerID, memberID, "归档决策",
		"- **归档决策**：归档按 "+slugA+" 的约定执行。")
	slugB := clauseSlugOfProposal(t, wsID, idB)

	body := legislationProposalBody(wsID, "每日同步",
		"- **每日同步**：执行前先核对 "+slugB+" 的归档要求。")
	body.ChangeKind = "revise_clause"
	idR := createProposal(t, wsID, memberID, body)
	submitDraft(t, wsID, memberID, idR)
	code, resp := ownerApprove(t, ownerID, wsID, idR)
	if code != http.StatusConflict {
		t.Fatalf("cycle: expected 409 gate-failed, got %d: %s", code, resp)
	}
	errs := gateErrorsRow(t, wsID, idR)
	if !strings.Contains(errs, "循环引用") {
		t.Fatalf("gate_errors missing the 循环引用 finding: %s", errs)
	}
}

// TestRefsGateDeterministicOrdering pins the ordering invariant end to
// end: after two enacts, the carrier's marker regions sit in ascending
// slug order — the total order the effective-set SQL
// (ORDER BY scope_rank, c.slug) and clausemark.Render both emit, and the
// only order under which re-rendering stays byte-stable.
func TestRefsGateDeterministicOrdering(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	wsID, ownerID, memberID := legislationFixture(t)
	enactAddClause(t, wsID, ownerID, memberID, "每日同步", cleanClauseText)
	enactAddClause(t, wsID, ownerID, memberID, "归档决策", "- **归档决策**：归档须留痕。")

	var ctx string
	if err := testPool.QueryRow(context.Background(),
		`SELECT context FROM workspace WHERE id = $1`, wsID).Scan(&ctx); err != nil {
		t.Fatalf("read carrier context: %v", err)
	}
	refs := clausemark.ScanMarkers(ctx)
	if len(refs) != 2 {
		t.Fatalf("carrier carries %d regions, want 2:\n%s", len(refs), ctx)
	}
	for i := 1; i < len(refs); i++ {
		if refs[i].Slug < refs[i-1].Slug {
			t.Fatalf("regions out of ascending order: %s before %s:\n%s",
				refs[i-1].Slug, refs[i].Slug, ctx)
		}
	}
	// Render idempotence: re-rendering the same set converges byte for byte.
	clauses := []clausemark.RenderedClause{
		{Slug: refs[0].Slug, Version: refs[0].Rev, Body: "body-a"},
		{Slug: refs[1].Slug, Version: refs[1].Rev, Body: "body-b"},
	}
	once := clausemark.Render(ctx, clauses)
	twice := clausemark.Render(once, clauses)
	if once != twice {
		t.Fatalf("render not byte-stable:\n--once--\n%s\n--twice--\n%s", once, twice)
	}
}

func callRoute(t *testing.T, route http.Handler, req *http.Request) *testutil.Response {
	t.Helper()
	return testutil.Call(t, http.HandlerFunc(route.ServeHTTP), req)
}

func waitForClauseReferenceLockWaiters(wsID string, want int) error {
	ctx := context.Background()
	var key int64
	if err := testPool.QueryRow(ctx, `SELECT hashtextextended($1::text, 869)`, wsID).Scan(&key); err != nil {
		return fmt.Errorf("read workspace advisory lock key: %w", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var count int
		if err := testPool.QueryRow(ctx, `
			SELECT count(*)
			FROM pg_locks
			WHERE locktype = 'advisory' AND NOT granted AND objsubid = 1
			  AND classid = (($1::bigint >> 32) & 4294967295)::oid
			  AND objid = ($1::bigint & 4294967295)::oid`, key).Scan(&count); err != nil {
			return fmt.Errorf("read workspace advisory lock waiters: %w", err)
		}
		if count >= want {
			return nil
		}
		time.Sleep(5 * time.Millisecond)
	}
	return fmt.Errorf("workspace advisory lock waiters did not reach %d", want)
}

func TestClauseRemovalRacesWithUntouchedCarrierStaticWrite(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	wsID, ownerID, memberID := legislationFixture(t)
	proposalID := enactAddClause(t, wsID, ownerID, memberID, "每日同步", cleanClauseText)
	slug := clauseSlugOfProposal(t, wsID, proposalID)
	agentID := dbfx.Agent(t, "clause-ref-race", "", map[string]any{
		"workspace_id": wsID,
		"instructions": "existing static instructions",
	})

	removeBody := legislationProposalBody(wsID, "每日同步", "")
	removeBody.ChangeKind = "remove_clause"
	removeID := createProposal(t, wsID, memberID, removeBody)
	submitDraft(t, wsID, memberID, removeID)

	approveRoute := ownerRoute(http.HandlerFunc(testHandler.ApprovePromptProposal))
	saveRoute := ownerRoute(http.HandlerFunc(testHandler.SavePromptGovernanceVersion))
	approveReq := legislationReq(ownerID, wsID, http.MethodPost,
		"/api/prompt-legislation/proposals/"+removeID+"/approve",
		map[string]any{"confirm_diff_previewed": true}, "id", removeID)
	saveReq := legislationReq(ownerID, wsID, http.MethodPost,
		"/api/prompt-governance/agent/"+agentID+"/versions",
		map[string]any{"content": "Keep the static reference " + slug, "change_note": "race"},
		"scope", "agent", "scopeId", agentID)

	blocker, err := testPool.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin advisory lock barrier: %v", err)
	}
	defer blocker.Rollback(context.Background())
	if _, err := blocker.Exec(context.Background(),
		`SELECT pg_advisory_xact_lock(hashtextextended($1::text, 869))`, wsID); err != nil {
		t.Fatalf("hold workspace advisory lock: %v", err)
	}

	type result struct {
		name string
		code int
		body string
	}
	results := make(chan result, 2)
	start := make(chan struct{})
	go func() {
		<-start
		resp := callRoute(t, approveRoute, approveReq)
		results <- result{name: "remove", code: resp.Code, body: resp.Text()}
	}()
	go func() {
		<-start
		resp := callRoute(t, saveRoute, saveReq)
		results <- result{name: "static write", code: resp.Code, body: resp.Text()}
	}()
	close(start)
	waitErr := waitForClauseReferenceLockWaiters(wsID, 2)
	if err := blocker.Commit(context.Background()); err != nil {
		t.Fatalf("release workspace advisory lock: %v", err)
	}

	got := map[string]result{}
	for range 2 {
		res := <-results
		got[res.name] = res
	}
	if waitErr != nil {
		t.Fatalf("requests did not contend on the workspace lock: %v; remove=%d %s, static write=%d %s",
			waitErr, got["remove"].code, got["remove"].body, got["static write"].code, got["static write"].body)
	}
	if got["remove"].code == got["static write"].code ||
		(got["remove"].code != http.StatusOK && got["remove"].code != http.StatusConflict) ||
		(got["static write"].code != http.StatusOK && got["static write"].code != http.StatusConflict) {
		t.Fatalf("concurrent outcomes must be one commit and one conflict; remove=%d %s, static write=%d %s",
			got["remove"].code, got["remove"].body, got["static write"].code, got["static write"].body)
	}

	activeClauses := dbfx.Count(t, `SELECT count(*) FROM clause WHERE workspace_id = $1 AND slug = $2 AND status = 'active'`, wsID, slug)
	var instructions string
	if err := testPool.QueryRow(context.Background(), `SELECT instructions FROM agent WHERE id = $1`, agentID).Scan(&instructions); err != nil {
		t.Fatalf("read agent instructions after race: %v", err)
	}
	if got["remove"].code == http.StatusOK {
		if activeClauses != 0 || instructions != "existing static instructions" {
			t.Fatalf("remove won but state is inconsistent: active clauses=%d, instructions=%q", activeClauses, instructions)
		}
	} else if activeClauses != 1 || instructions != "Keep the static reference "+slug {
		t.Fatalf("static write won but state is inconsistent: active clauses=%d, instructions=%q", activeClauses, instructions)
	}
}

func TestProjectMetadataUpdatePreservesConcurrentClauseRender(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	wsID, ownerID, _ := legislationFixture(t)
	initialInstructions := "original project instructions"
	createReq := legislationReq(ownerID, wsID, http.MethodPost, "/api/projects",
		CreateProjectRequest{Title: "clause render race", Instructions: &initialInstructions})
	created := testutil.Call(t, testHandler.CreateProject, createReq).Want(http.StatusCreated)
	var project struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(created.Text()), &project); err != nil {
		t.Fatalf("decode created project: %v (%s)", err, created.Text())
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(),
			`DELETE FROM project WHERE workspace_id = $1 AND id = $2`, wsID, project.ID)
	})

	const renderedInstructions = "original project instructions\n<!-- clause: cls_abcdefgh -->\n- **已生效条款**：保留"
	blocker, err := testPool.Acquire(context.Background())
	if err != nil {
		t.Fatalf("acquire advisory lock connection: %v", err)
	}
	defer blocker.Release()
	unlock := func() {
		_, _ = blocker.Exec(context.Background(),
			`SELECT pg_advisory_unlock(hashtextextended($1::text, 869))`, wsID)
	}
	if _, err := blocker.Exec(context.Background(),
		`SELECT pg_advisory_lock(hashtextextended($1::text, 869))`, wsID); err != nil {
		unlock()
		t.Fatalf("hold workspace advisory lock: %v", err)
	}
	defer unlock()

	if _, err := testPool.Exec(context.Background(),
		`UPDATE project SET instructions = $1 WHERE workspace_id = $2 AND id = $3`,
		renderedInstructions, wsID, project.ID); err != nil {
		t.Fatalf("simulate committed clause render under workspace lock: %v", err)
	}

	type result struct {
		code int
		body string
	}
	results := make(chan result, 1)
	go func() {
		req := legislationReq(ownerID, wsID, http.MethodPut, "/api/projects/"+project.ID,
			map[string]any{"title": "updated without prompt"}, "id", project.ID)
		resp := testutil.Call(t, testHandler.UpdateProject, req)
		results <- result{code: resp.Code, body: resp.Text()}
	}()

	var got result
	select {
	case got = <-results:
	case <-time.After(5 * time.Second):
		unlock()
		select {
		case got = <-results:
		case <-time.After(5 * time.Second):
			t.Fatal("metadata update remained blocked after releasing workspace advisory lock")
		}
		t.Fatalf("metadata update blocked while workspace advisory lock was held: %d %s", got.code, got.body)
	}
	if got.code != http.StatusOK {
		t.Fatalf("metadata update: expected 200, got %d: %s", got.code, got.body)
	}
	var updated struct {
		Instructions *string `json:"instructions"`
	}
	if err := json.Unmarshal([]byte(got.body), &updated); err != nil {
		t.Fatalf("decode metadata update: %v (%s)", err, got.body)
	}
	if updated.Instructions == nil || *updated.Instructions != renderedInstructions {
		t.Fatalf("metadata response lost clause render: got %v, want %q", updated.Instructions, renderedInstructions)
	}

	var instructions string
	if err := testPool.QueryRow(context.Background(),
		`SELECT instructions FROM project WHERE workspace_id = $1 AND id = $2`, wsID, project.ID).Scan(&instructions); err != nil {
		t.Fatalf("read project instructions after metadata update: %v", err)
	}
	if instructions != renderedInstructions {
		t.Fatalf("metadata update overwrote clause render: got %q, want %q", instructions, renderedInstructions)
	}
}

func TestPromptGovernanceSaveRejectsReferenceAfterClauseRemoval(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	wsID, ownerID, memberID := legislationFixture(t)
	proposalID := enactAddClause(t, wsID, ownerID, memberID, "每日同步", cleanClauseText)
	slug := clauseSlugOfProposal(t, wsID, proposalID)
	agentID := dbfx.Agent(t, "clause-ref-after-remove", "", map[string]any{
		"workspace_id": wsID,
		"instructions": "clean instructions",
	})

	removeBody := legislationProposalBody(wsID, "每日同步", "")
	removeBody.ChangeKind = "remove_clause"
	removeID := createProposal(t, wsID, memberID, removeBody)
	submitDraft(t, wsID, memberID, removeID)
	if code, body := ownerApprove(t, ownerID, wsID, removeID); code != http.StatusOK {
		t.Fatalf("remove clause before static write: expected 200, got %d: %s", code, body)
	}

	route := ownerRoute(http.HandlerFunc(testHandler.SavePromptGovernanceVersion))
	req := legislationReq(ownerID, wsID, http.MethodPost,
		"/api/prompt-governance/agent/"+agentID+"/versions",
		map[string]any{"content": "Dangling static reference " + slug, "change_note": "should be rejected"},
		"scope", "agent", "scopeId", agentID)
	resp := callRoute(t, route, req).Want(http.StatusConflict)
	if !strings.Contains(resp.Text(), slug) {
		t.Fatalf("conflict response does not identify the rejected clause slug %q: %s", slug, resp.Text())
	}
	if got := dbfx.Count(t, `SELECT count(*) FROM prompt_version WHERE workspace_id = $1 AND scope = 'agent' AND scope_id = $2`, wsID, agentID); got != 0 {
		t.Fatalf("rejected static write created %d prompt versions, want 0", got)
	}
	var instructions string
	if err := testPool.QueryRow(context.Background(), `SELECT instructions FROM agent WHERE id = $1`, agentID).Scan(&instructions); err != nil {
		t.Fatalf("read agent instructions: %v", err)
	}
	if instructions != "clean instructions" {
		t.Fatalf("rejected static write changed instructions to %q", instructions)
	}
}

func TestCreateProjectRejectsDanglingInitialStaticReference(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	wsID, ownerID, _ := legislationFixture(t)
	initialInstructions := "Reference missing clause cls_deadbeef"
	title := fmt.Sprintf("dangling initial prompt %d", time.Now().UnixNano())
	before := dbfx.Count(t, `SELECT count(*) FROM project WHERE workspace_id = $1 AND title = $2`, wsID, title)

	req := legislationReq(ownerID, wsID, http.MethodPost, "/api/projects",
		CreateProjectRequest{Title: title, Instructions: &initialInstructions})
	resp := testutil.Call(t, testHandler.CreateProject, req).Want(http.StatusConflict)
	if !strings.Contains(resp.Text(), "cls_deadbeef") {
		t.Fatalf("conflict response does not identify the dangling initial reference: %s", resp.Text())
	}
	after := dbfx.Count(t, `SELECT count(*) FROM project WHERE workspace_id = $1 AND title = $2`, wsID, title)
	if after != before {
		t.Fatalf("rejected project create left %d project rows (before=%d)", after, before)
	}
}

func TestCreateAgentRejectsDanglingInitialStaticReference(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	wsID, ownerID, _ := legislationFixture(t)
	runtimeID := dbfx.Runtime(t, "clause-ref-create-runtime", map[string]any{
		"workspace_id": wsID,
		"owner_id":     ownerID,
	})
	name := fmt.Sprintf("dangling initial agent %d", time.Now().UnixNano())
	initialInstructions := "Reference missing clause cls_deadbeef"
	before := dbfx.Count(t, `SELECT count(*) FROM agent WHERE workspace_id = $1 AND name = $2`, wsID, name)

	req := legislationReq(ownerID, wsID, http.MethodPost, "/api/agents",
		map[string]any{"name": name, "runtime_id": runtimeID, "instructions": initialInstructions, "max_concurrent_tasks": 1, "resource_weight": 1})
	resp := testutil.Call(t, testHandler.CreateAgent, req).Want(http.StatusConflict)
	if !strings.Contains(resp.Text(), "cls_deadbeef") {
		t.Fatalf("conflict response does not identify the dangling initial reference: %s", resp.Text())
	}
	after := dbfx.Count(t, `SELECT count(*) FROM agent WHERE workspace_id = $1 AND name = $2`, wsID, name)
	if after != before {
		t.Fatalf("rejected agent create left %d agents (before=%d)", after, before)
	}
}

func TestCreateWorkspaceRejectsDanglingInitialStaticReference(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	_, ownerID, _ := legislationFixture(t)
	slug := fmt.Sprintf("dangling-context-%d", time.Now().UnixNano())
	initialContext := "Reference missing clause cls_deadbeef"
	before := dbfx.Count(t, `SELECT count(*) FROM workspace WHERE slug = $1`, slug)

	req := legislationReq(ownerID, "", http.MethodPost, "/api/workspaces",
		CreateWorkspaceRequest{Name: "Dangling context", Slug: slug, Context: &initialContext})
	resp := testutil.Call(t, testHandler.CreateWorkspace, req).Want(http.StatusConflict)
	if !strings.Contains(resp.Text(), "cls_deadbeef") {
		t.Fatalf("conflict response does not identify the dangling initial reference: %s", resp.Text())
	}
	after := dbfx.Count(t, `SELECT count(*) FROM workspace WHERE slug = $1`, slug)
	if after != before {
		t.Fatalf("rejected workspace create left %d workspaces (before=%d)", after, before)
	}
}
