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
	"net/http"
	"strings"
	"testing"

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
