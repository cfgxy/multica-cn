package handler

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
)

// Execution profile handler tests (RUYI-57).
//
// The activation path is what these mostly pin down: it writes to N agents in
// one transaction and reports per-member outcomes, so the interesting cases are
// the partial ones — an archived member, a member of another workspace, and the
// all-failed case where nothing at all may be recorded.

func newExecutionProfile(t *testing.T, name string) string {
	t.Helper()
	body := map[string]any{"name": name}
	req := testutil.WithURLParams(newRequest("POST", "/api/workspaces/"+testWorkspaceID+"/execution-profiles", body),
		"id", testWorkspaceID)
	var resp ExecutionProfileResponse
	testutil.Call(t, testHandler.CreateExecutionProfile, req).Want(http.StatusCreated).JSON(&resp)
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM execution_profile_entry WHERE profile_id = $1`, resp.ID)
		testPool.Exec(context.Background(), `DELETE FROM execution_profile WHERE id = $1`, resp.ID)
	})
	return resp.ID
}

func putEntry(t *testing.T, profileID string, body map[string]any) *testutil.Response {
	t.Helper()
	req := testutil.WithURLParams(
		newRequest("PUT", "/api/workspaces/"+testWorkspaceID+"/execution-profiles/"+profileID+"/entries", body),
		"id", testWorkspaceID, "profileId", profileID)
	return testutil.Call(t, testHandler.UpsertExecutionProfileEntry, req)
}

func activate(t *testing.T, profileID string) *testutil.Response {
	t.Helper()
	req := testutil.WithURLParams(
		newRequest("POST", "/api/workspaces/"+testWorkspaceID+"/execution-profiles/"+profileID+"/activate", nil),
		"id", testWorkspaceID, "profileId", profileID)
	return testutil.Call(t, testHandler.ActivateExecutionProfile, req)
}

func activeProfileID(t *testing.T) string {
	t.Helper()
	var active *string
	dbfx.QueryRow(t, `SELECT active_execution_profile_id::text FROM workspace WHERE id = $1`, testWorkspaceID).Scan(&active)
	if active == nil {
		return ""
	}
	return *active
}

// clearActiveProfile restores the workspace pointer so a test that activates
// does not leak an active profile into the next one.
func clearActiveProfile(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		testPool.Exec(context.Background(),
			`UPDATE workspace SET active_execution_profile_id = NULL WHERE id = $1`, testWorkspaceID)
	})
}

// TestUpsertExecutionProfileEntry_RejectsIncompleteAndForeignReferences is the
// guard for the core rule: a stored entry must always be activatable. An entry
// missing a model, or naming an agent/runtime from another workspace, is
// rejected at write time rather than persisted for the activation path to trip
// over.
func TestUpsertExecutionProfileEntry_RejectsIncompleteAndForeignReferences(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	runtimeID := dbfx.Runtime(t, "EP Entry Runtime")
	agentID := dbfx.Agent(t, "EP Entry Agent", runtimeID)
	profileID := newExecutionProfile(t, "EP Entry Validation")

	// Missing model: the half-configured entry the UI's per-field save could
	// otherwise produce.
	putEntry(t, profileID, map[string]any{
		"agent_id": agentID, "runtime_id": runtimeID, "model": "",
	}).Want(http.StatusBadRequest)

	// Agent from another workspace.
	foreignWS := dbfx.Workspace(t, "EP Foreign WS", "ep-foreign-ws")
	foreignAgent := dbfx.Agent(t, "EP Foreign Agent", runtimeID, testutil.Cols{
		"workspace_id": foreignWS, "owner_id": nil,
	})
	putEntry(t, profileID, map[string]any{
		"agent_id": foreignAgent, "runtime_id": runtimeID, "model": "gpt-5",
	}).Want(http.StatusBadRequest)

	// Runtime from another workspace.
	foreignRuntime := dbfx.Insert(t, "agent_runtime", testutil.Cols{
		"workspace_id": foreignWS,
		"daemon_id":    nil,
		"name":         "EP Foreign Runtime",
		"runtime_mode": "cloud",
		"provider":     "handler_test_runtime",
		"status":       "online",
		"device_info":  "",
		"metadata":     testutil.Raw("'{}'::jsonb"),
		"last_seen_at": testutil.Raw("now()"),
		"visibility":   "private",
		"owner_id":     testUserID,
	})
	putEntry(t, profileID, map[string]any{
		"agent_id": agentID, "runtime_id": foreignRuntime, "model": "gpt-5",
	}).Want(http.StatusBadRequest)

	// A thinking level the runtime's provider does not know. The fixture
	// provider has no reasoning control at all, so any token is invalid.
	putEntry(t, profileID, map[string]any{
		"agent_id": agentID, "runtime_id": runtimeID, "model": "gpt-5",
		"thinking_level": "high",
	}).Want(http.StatusBadRequest)

	// Nothing above may have landed.
	if got := dbfx.Count(t, `SELECT count(*) FROM execution_profile_entry WHERE profile_id = $1`, profileID); got != 0 {
		t.Fatalf("expected no entries stored, got %d", got)
	}

	// The valid shape succeeds and replaces rather than duplicates.
	putEntry(t, profileID, map[string]any{
		"agent_id": agentID, "runtime_id": runtimeID, "model": "gpt-5",
	}).Want(http.StatusOK)
	putEntry(t, profileID, map[string]any{
		"agent_id": agentID, "runtime_id": runtimeID, "model": "gpt-5-codex",
	}).Want(http.StatusOK)
	if got := dbfx.Count(t, `SELECT count(*) FROM execution_profile_entry WHERE profile_id = $1`, profileID); got != 1 {
		t.Fatalf("expected upsert to keep one entry, got %d", got)
	}
	var model string
	dbfx.QueryRow(t, `SELECT model FROM execution_profile_entry WHERE profile_id = $1`, profileID).Scan(&model)
	if model != "gpt-5-codex" {
		t.Fatalf("expected upsert to replace the model, got %q", model)
	}
}

// TestActivateExecutionProfile_AppliesOnlyNamedAgents pins the headline
// behaviour: named members are overwritten, and a member the profile does not
// name keeps exactly what it had.
func TestActivateExecutionProfile_AppliesOnlyNamedAgents(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	clearActiveProfile(t)

	fromRuntime := dbfx.Runtime(t, "EP From Runtime")
	toRuntime := dbfx.Runtime(t, "EP To Runtime")
	named := dbfx.Agent(t, "EP Named Agent", fromRuntime, testutil.Cols{"model": "old-model"})
	untouched := dbfx.Agent(t, "EP Untouched Agent", fromRuntime, testutil.Cols{"model": "keep-me"})

	profileID := newExecutionProfile(t, "EP Apply Profile")
	putEntry(t, profileID, map[string]any{
		"agent_id": named, "runtime_id": toRuntime, "model": "new-model",
	}).Want(http.StatusOK)

	var out ExecutionProfileActivationResponse
	activate(t, profileID).Want(http.StatusOK).JSON(&out)

	if out.Applied != 1 || out.Skipped != 0 || out.Failed != 0 {
		t.Fatalf("expected 1 applied, got %+v", out)
	}
	if !out.Profile.IsActive {
		t.Fatalf("expected the activated profile to report is_active")
	}

	var gotRuntime, gotModel string
	dbfx.QueryRow(t, `SELECT runtime_id::text, model FROM agent WHERE id = $1`, named).Scan(&gotRuntime, &gotModel)
	if gotRuntime != toRuntime || gotModel != "new-model" {
		t.Fatalf("named agent not reconfigured: runtime=%s model=%s", gotRuntime, gotModel)
	}

	var keptRuntime, keptModel string
	dbfx.QueryRow(t, `SELECT runtime_id::text, model FROM agent WHERE id = $1`, untouched).Scan(&keptRuntime, &keptModel)
	if keptRuntime != fromRuntime || keptModel != "keep-me" {
		t.Fatalf("agent outside the profile was modified: runtime=%s model=%s", keptRuntime, keptModel)
	}

	if got := activeProfileID(t); got != profileID {
		t.Fatalf("expected workspace pointer %s, got %s", profileID, got)
	}

	// Decision B: the overwritten configuration is recoverable from the audit
	// log rather than a backup profile.
	if got := dbfx.Count(t,
		`SELECT count(*) FROM activity_log
		 WHERE workspace_id = $1 AND action = 'execution_profile_activated'
		   AND details->>'agent_id' = $2 AND details->>'from_model' = 'old-model'`,
		testWorkspaceID, named); got != 1 {
		t.Fatalf("expected one audit row carrying the previous model, got %d", got)
	}
}

// TestActivateExecutionProfile_PartialSuccessReportsEachMember: an archived
// member must not abort the activation for everyone else, and must come back
// named so the result dialog can say which member did not move.
func TestActivateExecutionProfile_PartialSuccessReportsEachMember(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	clearActiveProfile(t)

	fromRuntime := dbfx.Runtime(t, "EP Partial From")
	toRuntime := dbfx.Runtime(t, "EP Partial To")
	live := dbfx.Agent(t, "EP Partial Live", fromRuntime, testutil.Cols{"model": "old"})
	archived := dbfx.Agent(t, "EP Partial Archived", fromRuntime, testutil.Cols{"model": "old"})

	profileID := newExecutionProfile(t, "EP Partial Profile")
	for _, id := range []string{live, archived} {
		putEntry(t, profileID, map[string]any{
			"agent_id": id, "runtime_id": toRuntime, "model": "new",
		}).Want(http.StatusOK)
	}
	// Archive after the entry was stored — exactly the drift the activation
	// path re-validates for.
	dbfx.Exec(t, `UPDATE agent SET archived_at = now() WHERE id = $1`, archived)

	var out ExecutionProfileActivationResponse
	activate(t, profileID).Want(http.StatusOK).JSON(&out)

	if out.Applied != 1 || out.Skipped != 1 || out.Failed != 0 {
		t.Fatalf("expected 1 applied / 1 skipped, got %+v", out)
	}
	byAgent := map[string]ExecutionProfileActivationResult{}
	for _, r := range out.Results {
		byAgent[r.AgentID] = r
	}
	if byAgent[live].Status != executionProfileEntryApplied {
		t.Fatalf("live agent should be applied, got %+v", byAgent[live])
	}
	if byAgent[archived].Status != executionProfileEntrySkipped || byAgent[archived].Reason != "agent_archived" {
		t.Fatalf("archived agent should be skipped with a reason, got %+v", byAgent[archived])
	}

	var archivedModel string
	dbfx.QueryRow(t, `SELECT model FROM agent WHERE id = $1`, archived).Scan(&archivedModel)
	if archivedModel != "old" {
		t.Fatalf("archived agent must not be reconfigured, got %q", archivedModel)
	}
	// One success is enough to record the activation.
	if got := activeProfileID(t); got != profileID {
		t.Fatalf("expected pointer to move on partial success, got %q", got)
	}
}

// TestActivateExecutionProfile_AllFailedWritesNothing is the transaction guard:
// when no member could be updated, neither the agents, the pointer, nor
// last_activated_at may record the attempt.
func TestActivateExecutionProfile_AllFailedWritesNothing(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	clearActiveProfile(t)

	fromRuntime := dbfx.Runtime(t, "EP AllFail From")
	toRuntime := dbfx.Runtime(t, "EP AllFail To")
	agentID := dbfx.Agent(t, "EP AllFail Agent", fromRuntime, testutil.Cols{"model": "old"})

	profileID := newExecutionProfile(t, "EP AllFail Profile")
	putEntry(t, profileID, map[string]any{
		"agent_id": agentID, "runtime_id": toRuntime, "model": "new",
	}).Want(http.StatusOK)
	dbfx.Exec(t, `UPDATE agent SET archived_at = now() WHERE id = $1`, agentID)

	var out ExecutionProfileActivationResponse
	activate(t, profileID).Want(http.StatusOK).JSON(&out)

	if out.Applied != 0 {
		t.Fatalf("expected nothing applied, got %+v", out)
	}
	if out.Profile.IsActive {
		t.Fatalf("a profile that changed no agent must not report is_active")
	}
	if got := activeProfileID(t); got != "" {
		t.Fatalf("pointer must not move when nothing applied, got %q", got)
	}
	var lastActivated *string
	dbfx.QueryRow(t, `SELECT last_activated_at::text FROM execution_profile WHERE id = $1`, profileID).Scan(&lastActivated)
	if lastActivated != nil {
		t.Fatalf("last_activated_at must stay null when nothing applied, got %v", *lastActivated)
	}
	var model string
	dbfx.QueryRow(t, `SELECT model FROM agent WHERE id = $1`, agentID).Scan(&model)
	if model != "old" {
		t.Fatalf("agent must be untouched, got %q", model)
	}
}

// TestActivateExecutionProfile_EmptyProfileRefused: an empty profile would be
// "active" while having configured nothing, so it is refused outright.
func TestActivateExecutionProfile_EmptyProfileRefused(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	clearActiveProfile(t)

	profileID := newExecutionProfile(t, "EP Empty Profile")
	activate(t, profileID).Want(http.StatusBadRequest)
	if got := activeProfileID(t); got != "" {
		t.Fatalf("pointer must not move for an empty profile, got %q", got)
	}
}

// TestDeleteExecutionProfile_ActiveOnlyClearsPointer: deleting the active
// profile clears the marker but leaves the agents on the configuration it
// wrote — rolling them back would move agents nobody asked to move.
func TestDeleteExecutionProfile_ActiveOnlyClearsPointer(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	clearActiveProfile(t)

	fromRuntime := dbfx.Runtime(t, "EP Delete From")
	toRuntime := dbfx.Runtime(t, "EP Delete To")
	agentID := dbfx.Agent(t, "EP Delete Agent", fromRuntime, testutil.Cols{"model": "old"})

	profileID := newExecutionProfile(t, "EP Delete Profile")
	putEntry(t, profileID, map[string]any{
		"agent_id": agentID, "runtime_id": toRuntime, "model": "new",
	}).Want(http.StatusOK)
	activate(t, profileID).Want(http.StatusOK)

	// A second, unrelated profile must keep its rows through the delete.
	otherID := newExecutionProfile(t, "EP Delete Other")

	req := testutil.WithURLParams(
		newRequest("DELETE", "/api/workspaces/"+testWorkspaceID+"/execution-profiles/"+profileID, nil),
		"id", testWorkspaceID, "profileId", profileID)
	testutil.Call(t, testHandler.DeleteExecutionProfile, req).Want(http.StatusNoContent)

	if got := activeProfileID(t); got != "" {
		t.Fatalf("expected the pointer cleared, got %q", got)
	}
	var runtimeID, model string
	dbfx.QueryRow(t, `SELECT runtime_id::text, model FROM agent WHERE id = $1`, agentID).Scan(&runtimeID, &model)
	if runtimeID != toRuntime || model != "new" {
		t.Fatalf("agent configuration must survive the delete: runtime=%s model=%s", runtimeID, model)
	}
	if got := dbfx.Count(t, `SELECT count(*) FROM execution_profile_entry WHERE profile_id = $1`, profileID); got != 0 {
		t.Fatalf("expected entries cascaded, got %d", got)
	}
	if got := dbfx.Count(t, `SELECT count(*) FROM execution_profile WHERE id = $1`, otherID); got != 1 {
		t.Fatalf("expected the unrelated profile to survive")
	}
}

// TestDeleteExecutionProfile_OtherProfileKeepsPointer: the pointer clear is
// guarded on the profile id, so deleting a different profile must not
// deactivate the active one.
func TestDeleteExecutionProfile_OtherProfileKeepsPointer(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	clearActiveProfile(t)

	runtimeID := dbfx.Runtime(t, "EP Guard Runtime")
	agentID := dbfx.Agent(t, "EP Guard Agent", runtimeID)

	activeID := newExecutionProfile(t, "EP Guard Active")
	putEntry(t, activeID, map[string]any{
		"agent_id": agentID, "runtime_id": runtimeID, "model": "m",
	}).Want(http.StatusOK)
	activate(t, activeID).Want(http.StatusOK)

	victimID := newExecutionProfile(t, "EP Guard Victim")
	req := testutil.WithURLParams(
		newRequest("DELETE", "/api/workspaces/"+testWorkspaceID+"/execution-profiles/"+victimID, nil),
		"id", testWorkspaceID, "profileId", victimID)
	testutil.Call(t, testHandler.DeleteExecutionProfile, req).Want(http.StatusNoContent)

	if got := activeProfileID(t); got != activeID {
		t.Fatalf("deleting another profile cleared the pointer: %q", got)
	}
}

// TestExecutionProfile_DuplicateNameConflicts: the unique index surfaces as a
// 409 the rename form can show inline, not a 500.
func TestExecutionProfile_DuplicateNameConflicts(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	newExecutionProfile(t, "EP Duplicate Name")

	body := map[string]any{"name": "EP Duplicate Name"}
	req := testutil.WithURLParams(newRequest("POST", "/api/workspaces/"+testWorkspaceID+"/execution-profiles", body),
		"id", testWorkspaceID)
	testutil.Call(t, testHandler.CreateExecutionProfile, req).Want(http.StatusConflict)

	// Blank names are a field error, not a constraint violation.
	blank := testutil.WithURLParams(
		newRequest("POST", "/api/workspaces/"+testWorkspaceID+"/execution-profiles", map[string]any{"name": "   "}),
		"id", testWorkspaceID)
	testutil.Call(t, testHandler.CreateExecutionProfile, blank).Want(http.StatusBadRequest)
}

// TestExecutionProfile_WorkspaceIsolation: a profile is only reachable through
// its own workspace, on read and on activate.
func TestExecutionProfile_WorkspaceIsolation(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	profileID := newExecutionProfile(t, "EP Isolation Profile")
	foreignWS := dbfx.Workspace(t, "EP Isolation WS", "ep-isolation-ws")

	get := testutil.WithURLParams(
		newRequest("GET", "/api/workspaces/"+foreignWS+"/execution-profiles/"+profileID, nil),
		"id", foreignWS, "profileId", profileID)
	testutil.Call(t, testHandler.GetExecutionProfile, get).Want(http.StatusNotFound)

	act := testutil.WithURLParams(
		newRequest("POST", "/api/workspaces/"+foreignWS+"/execution-profiles/"+profileID+"/activate", nil),
		"id", foreignWS, "profileId", profileID)
	testutil.Call(t, testHandler.ActivateExecutionProfile, act).Want(http.StatusNotFound)

	list := testutil.WithURLParams(
		newRequest("GET", "/api/workspaces/"+foreignWS+"/execution-profiles", nil), "id", foreignWS)
	body := testutil.Call(t, testHandler.ListExecutionProfiles, list).Want(http.StatusOK).Text()
	if strings.Contains(body, profileID) {
		t.Fatalf("profile leaked into another workspace's list: %s", body)
	}
}

// TestActivateExecutionProfile_EmptyThinkingLevelClearsTheAgent is the guard
// for the three-field overwrite promise. An entry saved with an explicit empty
// thinking level means "runtime default", so activation must CLEAR the agent's
// level — not leave a stale `high` standing beside a freshly written runtime
// and model, which would carry the old provider's reasoning setting into every
// task the new runtime runs.
func TestActivateExecutionProfile_EmptyThinkingLevelClearsTheAgent(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	clearActiveProfile(t)

	fromRuntime := dbfx.Runtime(t, "EP Thinking From", testutil.Cols{"provider": "claude"})
	toRuntime := dbfx.Runtime(t, "EP Thinking To", testutil.Cols{"provider": "claude"})
	agentID := dbfx.Agent(t, "EP Thinking Agent", fromRuntime,
		testutil.Cols{"model": "old", "thinking_level": "high"})

	profileID := newExecutionProfile(t, "EP Thinking Profile")
	// "" is the drawer's "runtime default" choice: an opinion, not silence.
	putEntry(t, profileID, map[string]any{
		"agent_id": agentID, "runtime_id": toRuntime, "model": "new", "thinking_level": "",
	}).Want(http.StatusOK)

	var out ExecutionProfileActivationResponse
	activate(t, profileID).Want(http.StatusOK).JSON(&out)
	if out.Applied != 1 {
		t.Fatalf("expected the entry to apply, got %+v", out)
	}

	var level *string
	var model string
	dbfx.QueryRow(t, `SELECT thinking_level, model FROM agent WHERE id = $1`, agentID).Scan(&level, &model)
	if model != "new" {
		t.Fatalf("model should have been overwritten, got %q", model)
	}
	if level != nil {
		t.Fatalf("thinking_level must be cleared by an explicit empty entry, got %q", *level)
	}
}

// TestActivateExecutionProfile_OmittedThinkingLevelKeepsTheAgentValue is the
// other half of the tri-state: an entry that never expressed an opinion must
// leave the agent's level alone, so "clear" and "no opinion" stay distinct.
func TestActivateExecutionProfile_OmittedThinkingLevelKeepsTheAgentValue(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	clearActiveProfile(t)

	fromRuntime := dbfx.Runtime(t, "EP Keep From", testutil.Cols{"provider": "claude"})
	toRuntime := dbfx.Runtime(t, "EP Keep To", testutil.Cols{"provider": "claude"})
	agentID := dbfx.Agent(t, "EP Keep Agent", fromRuntime,
		testutil.Cols{"model": "old", "thinking_level": "high"})

	profileID := newExecutionProfile(t, "EP Keep Profile")
	// thinking_level omitted entirely.
	putEntry(t, profileID, map[string]any{
		"agent_id": agentID, "runtime_id": toRuntime, "model": "new",
	}).Want(http.StatusOK)

	var out ExecutionProfileActivationResponse
	activate(t, profileID).Want(http.StatusOK).JSON(&out)
	if out.Applied != 1 {
		t.Fatalf("expected the entry to apply, got %+v", out)
	}

	var level *string
	dbfx.QueryRow(t, `SELECT thinking_level FROM agent WHERE id = $1`, agentID).Scan(&level)
	if level == nil || *level != "high" {
		t.Fatalf("an entry with no opinion must not touch thinking_level, got %v", level)
	}
}

// TestUpsertExecutionProfileEntry_AppliesRuntimeThinkingCapability: the Profile
// path must hold the same capability line as UpdateAgent. `hermes` covers two
// binaries and only the discovered catalog says which one answered, so a
// runtime with no catalog cannot be handed a level here when the single-agent
// API would refuse it.
func TestUpsertExecutionProfileEntry_AppliesRuntimeThinkingCapability(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	runtimeID := dbfx.Runtime(t, "EP Capability Runtime", testutil.Cols{"provider": "hermes"})
	agentID := dbfx.Agent(t, "EP Capability Agent", runtimeID)
	profileID := newExecutionProfile(t, "EP Capability Profile")

	// No catalog has been reported for this runtime: ambiguous provider, so
	// the capability answer is "unknown" and the level is refused.
	putEntry(t, profileID, map[string]any{
		"agent_id": agentID, "runtime_id": runtimeID, "model": "hermes-4",
		"thinking_level": "high",
	}).Want(http.StatusBadRequest)

	// Same entry without a level is fine — the gate is on the level, not on
	// the runtime.
	putEntry(t, profileID, map[string]any{
		"agent_id": agentID, "runtime_id": runtimeID, "model": "hermes-4",
	}).Want(http.StatusOK)
}

// TestActivateExecutionProfile_RechecksPrivateRuntimePermission is the
// permission guard. canUseRuntimeForAgent has no admin override (MUL-6126): a
// private runtime is someone's own machine, credentials and files. The entry
// was authorised when it was saved, but a runtime can be flipped to private
// afterwards, so activation must re-authorise against the CURRENT caller and
// the runtime's CURRENT visibility rather than trusting the stored entry.
func TestActivateExecutionProfile_RechecksPrivateRuntimePermission(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	clearActiveProfile(t)

	otherUser := dbfx.User(t, "EP Runtime Owner", "ep-runtime-owner@example.com")
	fromRuntime := dbfx.Runtime(t, "EP Perm From")
	// Public at save time, so the entry is legitimately storable.
	toRuntime := dbfx.Runtime(t, "EP Perm To", testutil.Cols{"visibility": "public"})
	agentID := dbfx.Agent(t, "EP Perm Agent", fromRuntime, testutil.Cols{"model": "old"})

	profileID := newExecutionProfile(t, "EP Perm Profile")
	putEntry(t, profileID, map[string]any{
		"agent_id": agentID, "runtime_id": toRuntime, "model": "new",
	}).Want(http.StatusOK)

	// The owner takes their machine back: public -> private, owned by someone
	// who is not the caller.
	dbfx.Exec(t, `UPDATE agent_runtime SET visibility = 'private', owner_id = $1 WHERE id = $2`,
		otherUser, toRuntime)

	var out ExecutionProfileActivationResponse
	activate(t, profileID).Want(http.StatusOK).JSON(&out)

	if out.Applied != 0 || out.Failed != 1 {
		t.Fatalf("expected the entry to be refused, got %+v", out)
	}
	if len(out.Results) != 1 || out.Results[0].Reason != "runtime_forbidden" {
		t.Fatalf("expected a runtime_forbidden reason the dialog can name, got %+v", out.Results)
	}

	var runtime, model string
	dbfx.QueryRow(t, `SELECT runtime_id::text, model FROM agent WHERE id = $1`, agentID).Scan(&runtime, &model)
	if runtime != fromRuntime || model != "old" {
		t.Fatalf("agent must not be bound to a runtime the caller may not use: runtime=%s model=%s", runtime, model)
	}
	if got := activeProfileID(t); got != "" {
		t.Fatalf("nothing applied, so the pointer must not move, got %q", got)
	}
}
