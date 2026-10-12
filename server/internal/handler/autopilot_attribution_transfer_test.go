package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/service"
)

// These tests drive the REAL autopilot update handlers to prove the substantive /
// cosmetic edit boundary that decides when trigger_owner responsibility transfers
// (MUL-4302; Elon must-fix). They deliberately go through UpdateAutopilot /
// UpdateAutopilotTrigger rather than calling SetAutopilotTriggerPublisher* directly,
// because the bug Elon flagged lived in the handler's decision of WHEN to call the
// setter, not in the setter itself. testHandler / testUserID / testWorkspaceID /
// testPool are wired in TestMain (handler_test.go).

// seedTransferMember inserts a workspace member distinct from testUserID, modeling the
// CREATOR of an automation whose responsibility a later editor (testUserID) inherits.
func seedTransferMember(t *testing.T, label string) string {
	t.Helper()
	ctx := context.Background()
	var uid string
	if err := testPool.QueryRow(ctx, `INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id`,
		label, fmt.Sprintf("%s-%d@multica.test", label, time.Now().UnixNano())).Scan(&uid); err != nil {
		t.Fatalf("seed %s user: %v", label, err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, uid) })
	if _, err := testPool.Exec(ctx, `INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'member')`,
		testWorkspaceID, uid); err != nil {
		t.Fatalf("seed %s member: %v", label, err)
	}
	return uid
}

// seedScheduleTriggerPublishedBy inserts a schedule trigger whose responsible publisher
// is memberID, so a later handler edit can be observed to transfer (or not) responsibility.
func seedScheduleTriggerPublishedBy(t *testing.T, apID, memberID string) string {
	t.Helper()
	var id string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO autopilot_trigger (autopilot_id, kind, enabled, cron_expression, published_by_type, published_by_id)
		VALUES ($1, 'schedule', true, '0 * * * *', 'member', $2) RETURNING id`,
		apID, memberID).Scan(&id); err != nil {
		t.Fatalf("seed schedule trigger: %v", err)
	}
	return id
}

// triggerPublisherMember returns the member id currently recorded as the trigger's
// responsible publisher, or "" if it is not a member.
func triggerPublisherMember(t *testing.T, triggerID string) string {
	t.Helper()
	row, err := testHandler.Queries.GetAutopilotTrigger(context.Background(), parseUUID(triggerID))
	if err != nil {
		t.Fatalf("load trigger: %v", err)
	}
	if row.PublishedByType.Valid && row.PublishedByType.String == "member" && row.PublishedByID.Valid {
		return uuidToString(row.PublishedByID)
	}
	return ""
}

func patchAutopilot(t *testing.T, apID string, body map[string]any) {
	t.Helper()
	w := httptest.NewRecorder()
	req := newRequest("PATCH", "/api/autopilots/"+apID+"?workspace_id="+testWorkspaceID, body)
	req = withURLParam(req, "id", apID)
	testHandler.UpdateAutopilot(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateAutopilot: expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func patchTrigger(t *testing.T, apID, triggerID string, body map[string]any) {
	t.Helper()
	w := httptest.NewRecorder()
	req := newRequest("PATCH", "/api/autopilots/"+apID+"/triggers/"+triggerID, body)
	req = withURLParams(req, "id", apID, "triggerId", triggerID)
	testHandler.UpdateAutopilotTrigger(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateAutopilotTrigger: expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

// TestUpdateAutopilot_PromptEditTransfersAllTriggers: the autopilot description IS the
// run prompt (task instruction), so editing it transfers accountability for EVERY
// trigger to the editor — the exact gap Elon flagged (description was omitted from the
// substantive predicate, so a prompt rewrite left runs attributed to the creator).
func TestUpdateAutopilot_PromptEditTransfersAllTriggers(t *testing.T) {
	creatorA := seedTransferMember(t, "prompt-creator")
	agentID := createWebhookTestAgent(t, "PromptEdit Agent")
	apID := createWebhookTestAutopilot(t, agentID, "active", "run_only")
	trig1 := seedScheduleTriggerPublishedBy(t, apID, creatorA)
	trig2 := seedScheduleTriggerPublishedBy(t, apID, creatorA)

	patchAutopilot(t, apID, map[string]any{"description": "Rewritten prompt: do the new thing"})

	if got := triggerPublisherMember(t, trig1); got != testUserID {
		t.Errorf("trigger1 publisher = %s, want editor %s (prompt edit must transfer)", got, testUserID)
	}
	if got := triggerPublisherMember(t, trig2); got != testUserID {
		t.Errorf("trigger2 publisher = %s, want editor %s (prompt edit bumps all triggers)", got, testUserID)
	}
}

// TestUpdateAutopilot_CosmeticTitleEditDoesNotTransfer: the title is a display label,
// not an instruction — editing it alone must NOT move responsibility.
func TestUpdateAutopilot_CosmeticTitleEditDoesNotTransfer(t *testing.T) {
	creatorA := seedTransferMember(t, "title-creator")
	agentID := createWebhookTestAgent(t, "TitleEdit Agent")
	apID := createWebhookTestAutopilot(t, agentID, "active", "run_only")
	trig := seedScheduleTriggerPublishedBy(t, apID, creatorA)

	patchAutopilot(t, apID, map[string]any{"title": "Renamed automation"})

	if got := triggerPublisherMember(t, trig); got != creatorA {
		t.Errorf("trigger publisher = %s, want unchanged creator %s (cosmetic title edit must not transfer)", got, creatorA)
	}
}

// TestUpdateAutopilotTrigger_SubstantiveEditTransfersOnlyThatTrigger: a cron/enabled
// edit changes what/when THIS trigger fires, so it transfers only this trigger's
// responsibility; a sibling trigger is untouched (per-firing-trigger granularity).
func TestUpdateAutopilotTrigger_SubstantiveEditTransfersOnlyThatTrigger(t *testing.T) {
	creatorA := seedTransferMember(t, "trig-creator")
	agentID := createWebhookTestAgent(t, "TrigEdit Agent")
	apID := createWebhookTestAutopilot(t, agentID, "active", "run_only")
	trig1 := seedScheduleTriggerPublishedBy(t, apID, creatorA)
	trig2 := seedScheduleTriggerPublishedBy(t, apID, creatorA)

	patchTrigger(t, apID, trig1, map[string]any{"enabled": false})

	if got := triggerPublisherMember(t, trig1); got != testUserID {
		t.Errorf("trigger1 publisher = %s, want editor %s (substantive edit must transfer)", got, testUserID)
	}
	if got := triggerPublisherMember(t, trig2); got != creatorA {
		t.Errorf("trigger2 publisher = %s, want unchanged creator %s (editing a sibling must not transfer)", got, creatorA)
	}
}

// TestUpdateAutopilotTrigger_LabelOnlyEditDoesNotTransfer: label is cosmetic, so a
// label-only PATCH must NOT transfer responsibility — the over-transfer Elon flagged
// (any PATCH used to unconditionally re-stamp the publisher).
func TestUpdateAutopilotTrigger_LabelOnlyEditDoesNotTransfer(t *testing.T) {
	creatorA := seedTransferMember(t, "label-creator")
	agentID := createWebhookTestAgent(t, "LabelEdit Agent")
	apID := createWebhookTestAutopilot(t, agentID, "active", "run_only")
	trig := seedScheduleTriggerPublishedBy(t, apID, creatorA)

	patchTrigger(t, apID, trig, map[string]any{"label": "just a nickname"})

	if got := triggerPublisherMember(t, trig); got != creatorA {
		t.Errorf("trigger publisher = %s, want unchanged creator %s (label-only edit must not transfer)", got, creatorA)
	}
}

// TestUpdateAutopilotTrigger_NoOpEditDoesNotTransfer: a PATCH that re-sends the trigger's
// current values changes nothing, so responsibility must stay put.
func TestUpdateAutopilotTrigger_NoOpEditDoesNotTransfer(t *testing.T) {
	creatorA := seedTransferMember(t, "noop-creator")
	agentID := createWebhookTestAgent(t, "NoOpEdit Agent")
	apID := createWebhookTestAutopilot(t, agentID, "active", "run_only")
	trig := seedScheduleTriggerPublishedBy(t, apID, creatorA)

	// Re-send the same cron the row already has.
	patchTrigger(t, apID, trig, map[string]any{"cron_expression": "0 * * * *"})

	if got := triggerPublisherMember(t, trig); got != creatorA {
		t.Errorf("trigger publisher = %s, want unchanged creator %s (no-op edit must not transfer)", got, creatorA)
	}
}

// seedAutopilotCreatedBy inserts an autopilot whose creator is creatorID — the
// attribution tests need a non-fixture creator to observe responsibility and
// run attribution away from the suite-default user.
func seedAutopilotCreatedBy(t *testing.T, agentID, status, mode, creatorID string) string {
	t.Helper()
	var apID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO autopilot (
			workspace_id, title, assignee_id, status, execution_mode,
			created_by_type, created_by_id
		) VALUES ($1, $2, $3, $4, $5, 'member', $6)
		RETURNING id
	`, testWorkspaceID, "PAT attribution "+status, agentID, status, mode, creatorID).Scan(&apID); err != nil {
		t.Fatalf("create autopilot: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM autopilot WHERE id = $1`, apID)
	})
	return apID
}

// seedAgentOwnedBy inserts a private cloud agent owned by ownerID. The PAT
// attribution test needs the assignee agent owned by the PAT owner: manual
// trigger admission is gated by the CLICKER's invocation access to the
// assignee agent (MUL-3963/4525), so a fixture-default-owned private agent
// would skip the dispatch before any run attribution happens.
func seedAgentOwnedBy(t *testing.T, name, ownerID string) string {
	t.Helper()
	var agentID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent (
			workspace_id, name, description, runtime_mode, runtime_config,
			runtime_id, visibility, max_concurrent_tasks, owner_id,
			instructions, custom_env, custom_args, mcp_config
		)
		VALUES ($1, $2, '', 'cloud', '{}'::jsonb, $3, 'private', 1, $4, '', '{}'::jsonb, '[]'::jsonb, '{}'::jsonb)
		RETURNING id
	`, testWorkspaceID, name, testRuntimeID, ownerID).Scan(&agentID); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, agentID)
	})
	return agentID
}

// TestTriggerAutopilotWithParamsAttributesPATOwner (RUYI-641 stage B) pins run
// attribution for the API-ification path: a `mul_` PAT reaching /trigger with
// params must produce a run attributed to the PAT OWNER. A PAT presents exactly
// one identity signal to handlers — the auth middleware resolves the token to
// its owner and stamps X-User-ID, stripping any client-supplied actor headers
// (the mul_ branch in internal/middleware/auth.go; the translation itself is
// covered there by TestAuth_InvalidPAT / TestAuth_StripsClientSuppliedActorSource /
// TestAuth_PATCacheHit). This test drives the handler with that wire shape via
// newRequestAs (X-User-ID = owner, no X-Actor-Source, no agent headers) for a
// member who is neither the fixture default user nor an agent, and asserts the
// manual-dispatch rule: the caller becomes originator == accountable, source
// direct_human (MUL-4302 §4), with the stage-A params snapshot persisted.
//
// Reuse note (per stage B dispatch card): this asserts behavior already in
// force — middleware PAT translation, manual attribution, and stage-A param
// resolution — so no red-first was staged; the test guards the composed
// contract against future drift.
func TestTriggerAutopilotWithParamsAttributesPATOwner(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	patOwner := seedTransferMember(t, "pat-owner")
	agentID := seedAgentOwnedBy(t, "PAT Params Agent", patOwner)
	apID := seedAutopilotCreatedBy(t, agentID, "active", "run_only", patOwner)
	cleanupAutopilotParams(t, apID)
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM autopilot_run WHERE autopilot_id = $1`, apID)
	})

	// The PAT owner defines the calling contract on their own automation.
	w := httptest.NewRecorder()
	req := newRequestAs(patOwner, "PUT", "/api/autopilots/"+apID+"/params", map[string]any{
		"params": []service.AutopilotParamInput{cityParam()},
	})
	req = withURLParam(req, "id", apID)
	testHandler.ReplaceAutopilotParams(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT params as PAT owner: expected 200, got %d body=%s", w.Code, w.Body.String())
	}

	// ...and triggers it with params through the API surface.
	w = httptest.NewRecorder()
	req = newRequestAs(patOwner, "POST", "/api/autopilots/"+apID+"/trigger", map[string]any{
		"params": map[string]any{"city": "上海"},
	})
	req = withURLParam(req, "id", apID)
	testHandler.TriggerAutopilot(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("trigger as PAT owner: expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var runResp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &runResp); err != nil {
		t.Fatalf("decode run: %v", err)
	}
	runID, _ := runResp["id"].(string)
	if runID == "" {
		t.Fatalf("run response missing id: %#v", runResp)
	}

	// autopilot_run carries no attribution columns — the run's principal lands
	// on the agent_task_queue row dispatch creates, via the run's task_id.
	var taskID string
	if err := testPool.QueryRow(context.Background(),
		`SELECT task_id FROM autopilot_run WHERE id = $1 AND task_id IS NOT NULL`, runID,
	).Scan(&taskID); err != nil {
		t.Fatalf("load run task_id: %v", err)
	}
	var originator, accountable, source string
	if err := testPool.QueryRow(context.Background(), `
		SELECT originator_user_id::text, accountable_user_id::text, originator_source
		FROM agent_task_queue WHERE id = $1`, taskID,
	).Scan(&originator, &accountable, &source); err != nil {
		t.Fatalf("load task attribution: %v", err)
	}
	if originator != patOwner || accountable != patOwner {
		t.Fatalf("run attribution = originator %s / accountable %s, want PAT owner %s", originator, accountable, patOwner)
	}
	if source != "direct_human" {
		t.Fatalf("originator_source = %q, want direct_human", source)
	}
	var payload []byte
	if err := testPool.QueryRow(context.Background(),
		`SELECT trigger_payload FROM autopilot_run WHERE id = $1`, runID).Scan(&payload); err != nil {
		t.Fatalf("load run payload: %v", err)
	}
	var snap struct {
		Params map[string]any `json:"params"`
	}
	if err := json.Unmarshal(payload, &snap); err != nil {
		t.Fatalf("decode snapshot: %v body=%s", err, string(payload))
	}
	if snap.Params["city"] != "上海" {
		t.Fatalf("snapshot city = %#v, want 上海", snap.Params["city"])
	}
}
