package handler

// Claim-time dynamic clause injection (RUYI-751 / ADR 007 §5–§6), driven
// through the real ClaimTaskByRuntime route against a dedicated workspace:
//
//   - the happy path injects the effective clause set into the claimed
//     workspace context and records one clause_injection row per
//     (task, clause, version) with the carrier version it rode on;
//   - clause injection rows remain idempotent, while each degraded claim
//     appends a carrier-level observation with its reason and timestamp;
//   - the global kill switch degrades to snapshot injection: the carrier
//     text passes through untouched and the rows land flagged degraded;
//   - a malformed carrier (hand-edited marker) degrades the same way, and
//     an unparseable snapshot is a counted miss, never a claim failure.
//
// claimTaskKind is a pure function and is pinned here without a database.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/featureflags"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/pkg/clausemark"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestClaimTaskKind(t *testing.T) {
	issueID := pgtype.UUID{Bytes: [16]byte{1}, Valid: true}
	chatID := pgtype.UUID{Bytes: [16]byte{2}, Valid: true}
	autopilotID := pgtype.UUID{Bytes: [16]byte{3}, Valid: true}

	cases := []struct {
		name string
		task db.AgentTaskQueue
		want string
	}{
		{"issue", db.AgentTaskQueue{IssueID: issueID}, "issue"},
		{"chat", db.AgentTaskQueue{ChatSessionID: chatID}, "chat"},
		{"autopilot", db.AgentTaskQueue{AutopilotRunID: autopilotID}, "autopilot"},
		{"quick_create", db.AgentTaskQueue{Context: []byte(
			fmt.Sprintf(`{"type":%q,"draft":{}}`, service.QuickCreateContextType))}, "quick_create"},
		{"unknown origin", db.AgentTaskQueue{}, ""},
		{"origin beats context", db.AgentTaskQueue{IssueID: issueID,
			Context: []byte(fmt.Sprintf(`{"type":%q}`, service.QuickCreateContextType))}, "issue"},
		{"non-quickcreate context", db.AgentTaskQueue{Context: []byte(`{"type":"other"}`)}, ""},
	}
	for _, tc := range cases {
		if got := claimTaskKind(&tc.task); got != tc.want {
			t.Errorf("%s: claimTaskKind = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestClauseBindingAllowsTaskKind(t *testing.T) {
	cases := []struct {
		name       string
		conditions []byte
		kind       string
		want       bool
	}{
		{name: "sql null", kind: "issue", want: true},
		{name: "non-object root", conditions: []byte(`[]`), kind: "issue", want: true},
		{name: "missing task kinds", conditions: []byte(`{"other":true}`), kind: "issue", want: true},
		{name: "null task kinds", conditions: []byte(`{"task_kinds":null}`), kind: "issue", want: true},
		{name: "non-array task kinds", conditions: []byte(`{"task_kinds":"issue"}`), kind: "issue", want: true},
		{name: "allowed kind", conditions: []byte(`{"task_kinds":["chat"]}`), kind: "chat", want: true},
		{name: "disallowed kind", conditions: []byte(`{"task_kinds":["chat"]}`), kind: "issue", want: false},
		{name: "non-string array member", conditions: []byte(`{"task_kinds":[null]}`), kind: "issue", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := clauseBindingAllowsTaskKind(tc.conditions, tc.kind); got != tc.want {
				t.Errorf("clauseBindingAllowsTaskKind(%s, %q) = %v, want %v", tc.conditions, tc.kind, got, tc.want)
			}
		})
	}
}

// clauseInjectionFixture is a dedicated workspace whose carrier is
// legislationCarrierText, with one owner, one member, and a runtime+agent
// pair bound to it so a claim can be driven end to end.
type clauseInjectionFixture struct {
	wsID      string
	ownerID   string
	memberID  string
	runtimeID string
	agentID   string
	issueID   string
}

var clauseInjectionSeq = time.Now().UnixNano()

func newClauseInjectionFixture(t *testing.T) *clauseInjectionFixture {
	t.Helper()
	if testHandler == nil {
		t.Skip("database not available")
	}
	clauseInjectionSeq++
	suffix := fmt.Sprintf("cinj%d", clauseInjectionSeq)
	f := &clauseInjectionFixture{}
	f.wsID = dbfx.Workspace(t, "clause injection ws "+suffix, "clause-injection-"+suffix,
		map[string]any{"context": legislationCarrierText})
	f.ownerID = dbfx.User(t, "Clause Injection Owner", "cinj-owner-"+suffix+"@multica.test")
	dbfx.Member(t, f.wsID, f.ownerID, "owner")
	f.memberID = dbfx.User(t, "Clause Injection Member", "cinj-member-"+suffix+"@multica.test")
	dbfx.Member(t, f.wsID, f.memberID, "member")
	f.runtimeID = dbfx.Runtime(t, "cinj runtime "+suffix, testutil.Cols{"workspace_id": f.wsID})
	f.agentID = dbfx.Agent(t, "cinj agent "+suffix, f.runtimeID, testutil.Cols{"workspace_id": f.wsID})

	dbfx.QueryRow(t, `
		INSERT INTO issue (workspace_id, title, status, priority, creator_id, creator_type, number, position)
		VALUES ($1, 'cinj issue', 'in_progress', 'none', $2, 'member',
			(SELECT COALESCE(MAX(number), 0) + 1 FROM issue WHERE workspace_id = $1), 0)
		RETURNING id`, f.wsID, f.ownerID).Scan(&f.issueID)

	// The clause family rows the enact writes; LIFO ahead of the fixture's
	// own workspace/user deletes (no FKs — the app owns dependent cleanup).
	t.Cleanup(func() {
		ctx := context.Background()
		for _, stmt := range []string{
			`DELETE FROM clause_injection_observation WHERE workspace_id = $1`,
			`DELETE FROM clause_injection WHERE workspace_id = $1`,
			`DELETE FROM prompt_proposal WHERE workspace_id = $1`,
			`DELETE FROM prompt_structure_baseline WHERE workspace_id = $1`,
			`DELETE FROM prompt_version WHERE workspace_id = $1`,
			`DELETE FROM clause_binding WHERE clause_id IN (SELECT id FROM clause WHERE workspace_id = $1)`,
			`DELETE FROM clause_version WHERE clause_id IN (SELECT id FROM clause WHERE workspace_id = $1)`,
			`DELETE FROM clause WHERE workspace_id = $1`,
		} {
			testPool.Exec(ctx, stmt, f.wsID)
		}
	})
	return f
}

// enactWorkspaceClause walks a clean add_clause proposal through the real
// legislation API and returns the clause id.
func enactWorkspaceClause(t *testing.T, f *clauseInjectionFixture, name, text string) string {
	t.Helper()
	id := createDraft(t, f.wsID, f.memberID, name, text)
	submitDraft(t, f.wsID, f.memberID, id)
	if code, body := ownerApprove(t, f.ownerID, f.wsID, id); code != http.StatusOK {
		t.Fatalf("enact %s: expected 200, got %d: %s", name, code, body)
	}
	var clauseID string
	dbfx.QueryRow(t, `SELECT clause_id::text FROM prompt_proposal WHERE workspace_id = $1 AND id = $2`,
		f.wsID, id).Scan(&clauseID)
	if clauseID == "" {
		t.Fatalf("enacted proposal has no clause_id")
	}
	return clauseID
}

// enqueueAndClaim queues one issue task for the fixture's agent and claims
// it through the real daemon route, returning the claim payload.
func enqueueAndClaim(t *testing.T, f *clauseInjectionFixture) (taskID, workspaceContext string) {
	t.Helper()
	claim := enqueueAndClaimResponse(t, f)
	return claim.taskID, claim.workspaceContext
}

type clauseInjectionClaim struct {
	taskID            string
	workspaceContext  string
	agentInstructions string
}

func enqueueAndClaimResponse(t *testing.T, f *clauseInjectionFixture) clauseInjectionClaim {
	t.Helper()
	var rawTaskID string
	dbfx.QueryRow(t, `
		INSERT INTO agent_task_queue (agent_id, runtime_id, issue_id, status, priority)
		VALUES ($1, $2, $3, 'queued', 0) RETURNING id`,
		f.agentID, f.runtimeID, f.issueID).Scan(&rawTaskID)

	req := newDaemonTokenRequest("POST", "/api/daemon/runtimes/"+f.runtimeID+"/tasks/claim", nil,
		f.wsID, "clause-injection-claim")
	req = withURLParam(req, "runtimeId", f.runtimeID)
	w := testutil.Call(t, testHandler.ClaimTaskByRuntime, req).Want(http.StatusOK)

	var resp struct {
		Task *struct {
			ID               string `json:"id"`
			WorkspaceContext string `json:"workspace_context"`
		} `json:"task"`
		Agent *struct {
			Instructions string `json:"instructions"`
		} `json:"agent"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode claim: %v (%s)", err, w.Body.String())
	}
	if resp.Task == nil {
		t.Fatalf("expected claim to succeed: %s", w.Body.String())
	}
	if resp.Task.ID != rawTaskID {
		t.Fatalf("claimed task id = %s, want %s", resp.Task.ID, rawTaskID)
	}
	if resp.Agent == nil {
		t.Fatalf("expected claimed agent payload: %s", w.Body.String())
	}
	return clauseInjectionClaim{
		taskID:            rawTaskID,
		workspaceContext:  resp.Task.WorkspaceContext,
		agentInstructions: resp.Agent.Instructions,
	}
}

func TestClaimDynamicClauseInjectionHappyPath(t *testing.T) {
	f := newClauseInjectionFixture(t)
	clauseID := enactWorkspaceClause(t, f, "每日同步", cleanClauseText)

	taskID, wsContext := enqueueAndClaim(t, f)

	var persistedCarrier string
	dbfx.QueryRow(t, `SELECT context FROM workspace WHERE id = $1`, f.wsID).Scan(&persistedCarrier)
	if strings.Contains(persistedCarrier, cleanClauseText) || strings.Contains(persistedCarrier, "clause:id=") {
		t.Fatalf("persisted carrier retained dynamic clause content:\n%s", persistedCarrier)
	}

	// The claimed workspace context carries the rendered clause region —
	// marker-wrapped, in the target section.
	if !strings.Contains(wsContext, "每日同步") ||
		!strings.Contains(wsContext, "<!-- clause:id=") ||
		!strings.Contains(wsContext, clausemark.CloseTag()) {
		t.Fatalf("claimed context missing the clause region:\n%s", wsContext)
	}

	// One injection row, keyed to the claim's task, riding carrier version 1
	// (the enact funneled the workspace carrier through prompt_version v1).
	var clauseIDGot, scope string
	var version int32
	var carrierVersion pgtype.Int4
	var degraded bool
	err := testPool.QueryRow(context.Background(),
		`SELECT clause_id::text, clause_version, carrier_scope, carrier_version, degraded
		   FROM clause_injection WHERE task_id = $1`, taskID).
		Scan(&clauseIDGot, &version, &scope, &carrierVersion, &degraded)
	if err != nil {
		t.Fatalf("claim left no injection row: %v", err)
	}
	if clauseIDGot != clauseID || version != 1 || scope != "workspace" || degraded {
		t.Fatalf("injection row = %s v%d %s degraded=%v, want %s v1 workspace false",
			clauseIDGot, version, scope, degraded, clauseID)
	}
	if !carrierVersion.Valid || carrierVersion.Int32 != 1 {
		t.Fatalf("carrier_version = %+v, want 1 (the enact's prompt_version)", carrierVersion)
	}

	// Injection rows are idempotent, while each degraded observation is an
	// append-only record of one degraded observation attempt.
	clauseUID := parseUUID(clauseID)
	testHandler.recordClauseInjectionEvents(context.Background(),
		parseUUID(f.wsID), parseUUID(taskID),
		map[string]string{"workspace": f.wsID},
		[]clauseInjectionRecord{{ClauseID: clauseUID, ClauseVersion: 1, Scope: "workspace"}},
		[]clauseInjectionObservation{{Scope: "workspace", ScopeID: parseUUID(f.wsID), Degraded: true, Reason: "kill_switch"}})
	var n int
	testPool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM clause_injection WHERE task_id = $1`, taskID).Scan(&n)
	if n != 1 {
		t.Fatalf("duplicate recording produced %d rows, want 1", n)
	}
	var reason string
	if err := testPool.QueryRow(context.Background(), `SELECT degradation_reason FROM clause_injection_observation WHERE task_id = $1`, taskID).Scan(&reason); err != nil {
		t.Fatalf("read degradation observation: %v", err)
	}
	if reason != "kill_switch" {
		t.Fatalf("observation reason = %q, want kill_switch", reason)
	}
	testHandler.recordClauseInjectionEvents(context.Background(), parseUUID(f.wsID), parseUUID(taskID),
		map[string]string{"workspace": f.wsID},
		[]clauseInjectionRecord{{ClauseID: clauseUID, ClauseVersion: 1, Scope: "workspace"}},
		[]clauseInjectionObservation{{Scope: "workspace", ScopeID: parseUUID(f.wsID), Degraded: true, Reason: "kill_switch"}})
	testHandler.recordClauseInjectionEvents(context.Background(), parseUUID(f.wsID), parseUUID(taskID),
		map[string]string{"workspace": f.wsID},
		[]clauseInjectionRecord{{ClauseID: clauseUID, ClauseVersion: 1, Scope: "workspace"}},
		[]clauseInjectionObservation{{Scope: "workspace", ScopeID: parseUUID(f.wsID), Degraded: true, Reason: "parse_error"}})
	var observationCount int
	if err := testPool.QueryRow(context.Background(), `SELECT COUNT(*) FROM clause_injection_observation WHERE task_id = $1`, taskID).Scan(&observationCount); err != nil {
		t.Fatalf("count per-attempt observations: %v", err)
	}
	if observationCount != 3 {
		t.Fatalf("observations across three recorder calls = %d, want 3", observationCount)
	}
	var observedReasons int
	if err := testPool.QueryRow(context.Background(), `SELECT COUNT(*) FROM clause_injection_observation WHERE task_id = $1 AND degradation_reason IN ('kill_switch', 'parse_error') AND created_at IS NOT NULL`, taskID).Scan(&observedReasons); err != nil {
		t.Fatalf("verify observation reasons and timestamps: %v", err)
	}
	if observedReasons != 3 {
		t.Fatalf("observations with reason and timestamp = %d, want 3", observedReasons)
	}
}

func TestClaimDynamicClauseInjectionRendersIntoEmptyCarrier(t *testing.T) {
	f := newClauseInjectionFixture(t)
	clauseID := enactWorkspaceClause(t, f, "每日同步", cleanClauseText)
	var slug string
	dbfx.QueryRow(t, `SELECT slug FROM clause WHERE id = $1`, clauseID).Scan(&slug)
	dbfx.Exec(t, `UPDATE workspace SET context = '' WHERE id = $1`, f.wsID)

	taskID, workspaceContext := enqueueAndClaim(t, f)
	if !strings.Contains(workspaceContext, slug) || !strings.Contains(workspaceContext, "每日同步") {
		t.Fatalf("empty carrier claim omitted active clause %s:\n%s", slug, workspaceContext)
	}
	var observed int
	if err := testPool.QueryRow(context.Background(), `SELECT COUNT(*) FROM clause_injection_observation WHERE task_id = $1`, taskID).Scan(&observed); err != nil {
		t.Fatalf("read empty-carrier observation: %v", err)
	}
	if observed != 0 {
		t.Fatalf("healthy empty-carrier claim created %d degraded observations, want 0", observed)
	}
}

func TestClaimDynamicClauseInjectionRendersIntoNullCarrier(t *testing.T) {
	f := newClauseInjectionFixture(t)
	clauseID := enactWorkspaceClause(t, f, "每日同步", cleanClauseText)
	var slug string
	dbfx.QueryRow(t, `SELECT slug FROM clause WHERE id = $1`, clauseID).Scan(&slug)
	dbfx.Exec(t, `UPDATE workspace SET context = NULL WHERE id = $1`, f.wsID)

	taskID, workspaceContext := enqueueAndClaim(t, f)
	if !strings.Contains(workspaceContext, slug) || !strings.Contains(workspaceContext, "每日同步") {
		t.Fatalf("NULL carrier claim omitted active clause %s:\n%s", slug, workspaceContext)
	}
	var observed int
	if err := testPool.QueryRow(context.Background(), `SELECT COUNT(*) FROM clause_injection_observation WHERE task_id = $1`, taskID).Scan(&observed); err != nil {
		t.Fatalf("read NULL-carrier observation: %v", err)
	}
	if observed != 0 {
		t.Fatalf("healthy NULL-carrier claim created %d degraded observations, want 0", observed)
	}
}

func TestClaimDynamicClauseInjectionFromNilWorkspaceContext(t *testing.T) {
	f := newClauseInjectionFixture(t)
	dbfx.Exec(t, `UPDATE workspace SET context = NULL WHERE id = $1`, f.wsID)
	clauseID := enactWorkspaceClause(t, f, "每日同步", cleanClauseText)

	var persistedCarrier pgtype.Text
	dbfx.QueryRow(t, `SELECT context FROM workspace WHERE id = $1`, f.wsID).Scan(&persistedCarrier)
	if persistedCarrier.Valid {
		t.Fatalf("persisted workspace context = %q, want NULL static carrier", persistedCarrier.String)
	}

	taskID, wsContext := enqueueAndClaim(t, f)
	if !strings.Contains(wsContext, cleanClauseText) ||
		!strings.Contains(wsContext, "<!-- clause:id=") ||
		!strings.Contains(wsContext, clausemark.CloseTag()) {
		t.Fatalf("claimed empty static carrier missing rendered clause:\n%s", wsContext)
	}

	var gotClauseID, scope string
	if err := testPool.QueryRow(context.Background(),
		`SELECT clause_id::text, carrier_scope FROM clause_injection WHERE task_id = $1`, taskID).
		Scan(&gotClauseID, &scope); err != nil {
		t.Fatalf("claim left no injection row: %v", err)
	}
	if gotClauseID != clauseID || scope != "workspace" {
		t.Fatalf("injection row = clause %s scope %s, want clause %s workspace", gotClauseID, scope, clauseID)
	}
}

func TestClaimDynamicClauseInjectionMikaAgent(t *testing.T) {
	f := newClauseInjectionFixture(t)
	clauseID := enactWorkspaceClause(t, f, "每日同步", cleanClauseText)
	dbfx.Exec(t, `UPDATE agent SET system_key = $2, instructions = '' WHERE id = $1`, f.agentID, service.MikaSystemKey)
	dbfx.Exec(t, `UPDATE clause_binding SET scope = 'agent', scope_id = $2 WHERE clause_id = $1`, clauseID, f.agentID)

	claim := enqueueAndClaimResponse(t, f)
	if got := strings.Count(claim.agentInstructions, cleanClauseText); got != 1 {
		t.Fatalf("Mika instructions contain the effective clause %d times, want once:\n%s", got, claim.agentInstructions)
	}
	if got := strings.Count(claim.agentInstructions, "<!-- clause:id="); got != 1 {
		t.Fatalf("Mika instructions contain %d clause markers, want one:\n%s", got, claim.agentInstructions)
	}

	var gotClauseID, scope string
	if err := testPool.QueryRow(context.Background(),
		`SELECT clause_id::text, carrier_scope FROM clause_injection WHERE task_id = $1`, claim.taskID).
		Scan(&gotClauseID, &scope); err != nil {
		t.Fatalf("claim left no Mika injection row: %v", err)
	}
	if gotClauseID != clauseID || scope != "agent" {
		t.Fatalf("Mika injection row = clause %s scope %s, want clause %s agent", gotClauseID, scope, clauseID)
	}
}

func TestClaimDynamicClauseInjectionKillSwitch(t *testing.T) {
	f := newClauseInjectionFixture(t)
	enactWorkspaceClause(t, f, "每日同步", cleanClauseText)

	// The switch-off claim returns the static carrier unchanged. New carriers
	// do not retain a legacy snapshot, so there is no degraded injection row.
	var carrierBefore string
	dbfx.QueryRow(t, `SELECT context FROM workspace WHERE id = $1`, f.wsID).Scan(&carrierBefore)

	withFeatureFlag(t, testHandler, featureflags.ClauseDynamicInjectionDisabled, true)

	taskID, wsContext := enqueueAndClaim(t, f)
	if wsContext != carrierBefore {
		t.Fatalf("kill-switch claim rewrote the carrier:\n--before--\n%s\n--claimed--\n%s",
			carrierBefore, wsContext)
	}

	var n int
	testPool.QueryRow(context.Background(), `SELECT COUNT(*) FROM clause_injection WHERE task_id = $1`, taskID).Scan(&n)
	if n != 0 {
		t.Fatalf("static carrier produced %d fallback injection rows, want 0", n)
	}
}

func TestClaimDynamicClauseInjectionBadCarrierDegrades(t *testing.T) {
	f := newClauseInjectionFixture(t)
	enactWorkspaceClause(t, f, "每日同步", cleanClauseText)

	// An Owner hand-edit that breaks the marker: the open tag's rev stops
	// being a number, so neither the fast path nor the snapshot parse can
	// read the carrier. The claim proceeds without injecting untrusted text.
	var carrier string
	dbfx.QueryRow(t, `SELECT context FROM workspace WHERE id = $1`, f.wsID).Scan(&carrier)
	broken := strings.Replace(carrier, "rev=1 -->", "rev=oops -->", 1)
	broken = strings.Replace(broken, clausemark.CloseTag(), "", 1)
	if broken == carrier {
		t.Fatalf("fixture carrier has no marker to break:\n%s", carrier)
	}
	dbfx.Exec(t, `UPDATE workspace SET context = $2 WHERE id = $1`, f.wsID, broken)

	taskID, wsContext := enqueueAndClaim(t, f)
	if wsContext != "" {
		t.Fatalf("degraded claim injected an unparseable carrier snapshot:\n%s", wsContext)
	}

	// The snapshot is unparseable too — a counted MISS: no rows, and the
	// claim row itself is what the reconciliation totals pick up.
	var n int
	testPool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM clause_injection WHERE task_id = $1`, taskID).Scan(&n)
	if n != 0 {
		t.Fatalf("unparseable snapshot produced %d injection rows, want 0 (counted miss)", n)
	}
	var observations int
	if err := testPool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM clause_injection_observation WHERE task_id = $1 AND degraded AND degradation_reason = 'parse_error'`, taskID).Scan(&observations); err != nil {
		t.Fatalf("read unparseable-snapshot observation: %v", err)
	}
	if observations != 1 {
		t.Fatalf("unparseable snapshot recorded %d degraded observations, want 1", observations)
	}
}

// The snapshot fallback resolves each rendered segment back to its clause
// asset and flags the row degraded — the kill-switch path's accounting.
func TestSnapshotFallbackInjectionsResolveSegments(t *testing.T) {
	f := newClauseInjectionFixture(t)
	clauseID := enactWorkspaceClause(t, f, "每日同步", cleanClauseText)

	var carrier string
	dbfx.QueryRow(t, `SELECT context FROM workspace WHERE id = $1`, f.wsID).Scan(&carrier)
	var slug string
	dbfx.QueryRow(t, `SELECT slug FROM clause WHERE id = $1`, clauseID).Scan(&slug)
	legacySnapshot := clausemark.Render(carrier, []clausemark.RenderedClause{{
		Slug: slug, Version: 1, Body: cleanClauseText,
	}})

	records := testHandler.snapshotFallbackInjections(context.Background(),
		parseUUID(f.wsID), promptVersionScopeWorkspace, parseUUID(f.wsID), "issue", legacySnapshot, "kill_switch")
	if len(records) != 1 {
		t.Fatalf("fallback records = %d, want 1", len(records))
	}
	if records[0].ClauseID != parseUUID(clauseID) || records[0].ClauseVersion != 1 || !records[0].Degraded {
		t.Fatalf("fallback record = %+v, want clause %s v1 degraded", records[0], clauseID)
	}
}

func TestSnapshotFallbackSkipsDeprecatedClauseAtHistoricalCarrier(t *testing.T) {
	f := newClauseInjectionFixture(t)
	clauseID := enactWorkspaceClause(t, f, "每日同步", cleanClauseText)
	projectA := dbfx.Project(t, "clause fallback historical project A", testutil.Cols{
		"workspace_id": f.wsID,
		"instructions": "# Project A\n\n## Project rules\n",
	})
	projectB := dbfx.Project(t, "clause fallback current project B", testutil.Cols{
		"workspace_id": f.wsID,
		"instructions": "# Project B\n\n## Project rules\n",
	})
	var slug string
	dbfx.QueryRow(t, `SELECT slug FROM clause WHERE id = $1`, clauseID).Scan(&slug)
	body := "- **每日同步**：成员开工前回报当日计划与阻塞。"
	projectABase := "# Project A\n\n## Project rules\n"
	projectBBase := "# Project B\n\n## Project rules\n"
	projectASnapshot := clausemark.Render(projectABase, []clausemark.RenderedClause{{Slug: slug, Version: 1, Body: body}})
	projectBSnapshot := clausemark.Render(projectBBase, []clausemark.RenderedClause{{Slug: slug, Version: 1, Body: body}})
	dbfx.Exec(t, `UPDATE project SET instructions = $2 WHERE id = $1`, projectA, projectASnapshot)
	dbfx.Exec(t, `UPDATE project SET instructions = $2 WHERE id = $1`, projectB, projectBSnapshot)

	// Model a move: A retains its historical marker while B owns the active binding.
	dbfx.Exec(t, `UPDATE clause_binding SET scope = 'project', scope_id = $2, status = 'superseded' WHERE clause_id = $1 AND status = 'active'`, clauseID, projectA)
	dbfx.Exec(t, `INSERT INTO clause_binding (clause_id, scope, scope_id, status, conditions, since_clause_version, audit_log)
		VALUES ($1, 'project', $2, 'active', '{"task_kinds":["chat"]}'::jsonb, 1, '[]'::jsonb)`, clauseID, projectB)

	withFeatureFlag(t, testHandler, featureflags.ClauseDynamicInjectionDisabled, true)
	activeAtB := testHandler.applyDynamicClauses(context.Background(), parseUUID(f.wsID), promptVersionScopeProject,
		parseUUID(projectA), "issue", projectASnapshot)
	if strings.Contains(activeAtB.Text, "clause:id=") || strings.Contains(activeAtB.Text, "每日同步") || len(activeAtB.Injections) != 0 {
		t.Fatalf("fallback injected a clause active on project B from historical project A:\ntext:\n%s\ninjections: %+v", activeAtB.Text, activeAtB.Injections)
	}
	issueAtB := testHandler.applyDynamicClauses(context.Background(), parseUUID(f.wsID), promptVersionScopeProject,
		parseUUID(projectB), "issue", projectBSnapshot)
	if strings.Contains(issueAtB.Text, "clause:id=") || strings.Contains(issueAtB.Text, "每日同步") || len(issueAtB.Injections) != 0 {
		t.Fatalf("fallback injected a chat-only clause into an issue task:\ntext:\n%s\ninjections: %+v", issueAtB.Text, issueAtB.Injections)
	}
	chatAtB := testHandler.applyDynamicClauses(context.Background(), parseUUID(f.wsID), promptVersionScopeProject,
		parseUUID(projectB), "chat", projectBSnapshot)
	if !strings.Contains(chatAtB.Text, "每日同步") || len(chatAtB.Injections) != 1 {
		t.Fatalf("fallback omitted a clause allowed for chat:\ntext:\n%s\ninjections: %+v", chatAtB.Text, chatAtB.Injections)
	}

	code, response := legislationCall(t, testHandler.DeactivateClause,
		legislationReq(f.ownerID, f.wsID, http.MethodPost, "/api/clauses/"+clauseID+"/deactivate", nil, "clauseId", clauseID))
	if code != http.StatusNoContent {
		t.Fatalf("deactivate moved clause: expected 204, got %d: %s", code, response)
	}
	var historicalSnapshot string
	dbfx.QueryRow(t, `SELECT instructions FROM project WHERE id = $1`, projectA).Scan(&historicalSnapshot)
	if historicalSnapshot != projectASnapshot {
		t.Fatalf("deactivation unexpectedly changed historical carrier A:\n--- before ---\n%s\n--- after ---\n%s", projectASnapshot, historicalSnapshot)
	}

	result := testHandler.applyDynamicClauses(context.Background(), parseUUID(f.wsID), promptVersionScopeProject,
		parseUUID(projectA), "issue", historicalSnapshot)
	if strings.Contains(result.Text, "clause:id=") || strings.Contains(result.Text, "每日同步") {
		t.Fatalf("fallback injected deprecated clause from historical carrier:\n%s", result.Text)
	}
	if len(result.Injections) != 0 {
		t.Fatalf("fallback recorded %d injections for a deprecated historical clause, want 0", len(result.Injections))
	}
	if !strings.Contains(result.Text, "# Project A") {
		t.Fatalf("fallback did not preserve stable carrier text:\n%s", result.Text)
	}
}
