package handler

// Claim-time dynamic clause injection (RUYI-751 / ADR 007 §5–§6), driven
// through the real ClaimTaskByRuntime route against a dedicated workspace:
//
//   - the happy path injects the effective clause set into the claimed
//     workspace context and records one clause_injection row per
//     (task, clause, version) with the carrier version it rode on;
//   - re-recording the same claim's events is a no-op (the unique index —
//     a re-claimed task must not double-count);
//   - the global kill switch passes the carrier text through untouched; legacy
//     snapshots, when present, still record rows flagged degraded;
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

	// Re-recording the same claim's events is a no-op: the unique
	// (task_id, clause_id, clause_version) index absorbs the duplicate.
	clauseUID := parseUUID(clauseID)
	testHandler.recordClauseInjectionEvents(context.Background(),
		parseUUID(f.wsID), parseUUID(taskID),
		map[string]string{"workspace": f.wsID},
		[]clauseInjectionRecord{{ClauseID: clauseUID, ClauseVersion: 1, Scope: "workspace"}})
	var n int
	testPool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM clause_injection WHERE task_id = $1`, taskID).Scan(&n)
	if n != 1 {
		t.Fatalf("duplicate recording produced %d rows, want 1", n)
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

	// An Owner hand-edit adds a malformed legacy marker. Neither the normal
	// path nor the snapshot parser can read it, but the claim must still
	// succeed and inject the raw carrier verbatim.
	var carrier string
	dbfx.QueryRow(t, `SELECT context FROM workspace WHERE id = $1`, f.wsID).Scan(&carrier)
	broken := carrier + "\n<!-- clause:id=cls_aaa00001 rev=oops -->\n"
	dbfx.Exec(t, `UPDATE workspace SET context = $2 WHERE id = $1`, f.wsID, broken)

	taskID, wsContext := enqueueAndClaim(t, f)
	if wsContext != broken {
		t.Fatalf("degraded claim rewrote the malformed carrier:\n--seeded--\n%s\n--claimed--\n%s",
			broken, wsContext)
	}

	// The snapshot is unparseable too — a counted MISS: no rows, and the
	// claim row itself is what the reconciliation totals pick up.
	var n int
	testPool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM clause_injection WHERE task_id = $1`, taskID).Scan(&n)
	if n != 0 {
		t.Fatalf("unparseable snapshot produced %d injection rows, want 0 (counted miss)", n)
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
		parseUUID(f.wsID), promptVersionScopeWorkspace, parseUUID(f.wsID), legacySnapshot, "kill_switch")
	if len(records) != 1 {
		t.Fatalf("fallback records = %d, want 1", len(records))
	}
	if records[0].ClauseID != parseUUID(clauseID) || records[0].ClauseVersion != 1 || !records[0].Degraded {
		t.Fatalf("fallback record = %+v, want clause %s v1 degraded", records[0], clauseID)
	}
}
