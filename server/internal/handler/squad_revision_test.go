package handler

// RUYI-666: squad revision (optimistic lock) contract.
//
// squad.instructions is written concurrently by Leader runs and humans. Same
// contract as the workspace/issue/comment/project/agent revision columns
// (migrations 351/920/928/944): responses carry revision, a matching
// expected_revision updates and increments, a stale one answers 409
// revision_conflict without mutating the row, and omitting expected_revision
// keeps the legacy unconditional behavior (opt-in).
//
// Conflict bodies must expose revision metadata only — never the losing
// writer's candidate text.

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// updateSquadForRevision drives UpdateSquad with the chi params the handler
// reads, as the workspace owner.
func updateSquadForRevision(t *testing.T, squadID string, body map[string]any) *testutil.Response {
	t.Helper()
	r := squadScopeReq("", "PUT", "/api/squads/"+squadID, body, map[string]string{"id": squadID})
	return testutil.Call(t, testHandler.UpdateSquad, r)
}

// A matching expected_revision updates instructions and increments the
// revision.
func TestSquadRevisionLifecycle(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler suite fixture unavailable")
	}
	leaderID := createHandlerTestAgent(t, "squad-revision-leader", nil)
	squad := createSquadAs(t, "", "Revision Lifecycle Squad", leaderID)
	if squad.Revision != 1 {
		t.Fatalf("expected created squad revision 1, got %d", squad.Revision)
	}

	w := updateSquadForRevision(t, squad.ID, map[string]any{
		"instructions":      "lifecycle instructions v2",
		"expected_revision": squad.Revision,
	}).Want(http.StatusOK)

	var resp SquadResponse
	w.JSON(&resp)
	if resp.Revision != squad.Revision+1 {
		t.Errorf("expected revision %d after guarded update, got %d", squad.Revision+1, resp.Revision)
	}
	if resp.Instructions != "lifecycle instructions v2" {
		t.Errorf("guarded write did not land: instructions %q", resp.Instructions)
	}
}

// A stale expected_revision answers 409 revision_conflict with the expected
// and actual revisions, leaves the row untouched, and never echoes the
// losing writer's candidate text back.
func TestUpdateSquadStaleRevisionConflict(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler suite fixture unavailable")
	}
	leaderID := createHandlerTestAgent(t, "squad-revision-conflict-leader", nil)
	squad := createSquadAs(t, "", "Revision Conflict Squad", leaderID)

	// Advance the real revision so the held value is genuinely stale.
	advanced := updateSquadForRevision(t, squad.ID, map[string]any{
		"instructions": "conflict baseline",
	}).Want(http.StatusOK)
	var advancedResp SquadResponse
	advanced.JSON(&advancedResp)

	const candidateText = "CONCURRENT-CANDIDATE-TEXT must never be echoed"
	w := updateSquadForRevision(t, squad.ID, map[string]any{
		"instructions":      candidateText,
		"expected_revision": advancedResp.Revision + 5,
	}).Want(http.StatusConflict)

	var conflict struct {
		Error            string `json:"error"`
		Code             string `json:"code"`
		ResourceType     string `json:"resource_type"`
		ResourceID       string `json:"resource_id"`
		ExpectedRevision int64  `json:"expected_revision"`
		ActualRevision   int64  `json:"actual_revision"`
	}
	w.JSON(&conflict)
	if conflict.Code != "revision_conflict" {
		t.Errorf("expected code revision_conflict, got %q", conflict.Code)
	}
	if conflict.ResourceType != "squad" || conflict.ResourceID != squad.ID {
		t.Errorf("unexpected resource identity: %s %s", conflict.ResourceType, conflict.ResourceID)
	}
	if conflict.ExpectedRevision != advancedResp.Revision+5 {
		t.Errorf("expected_revision %d, got %d", advancedResp.Revision+5, conflict.ExpectedRevision)
	}
	if conflict.ActualRevision != advancedResp.Revision {
		t.Errorf("actual_revision %d, got %d", advancedResp.Revision, conflict.ActualRevision)
	}
	if strings.Contains(w.Body.String(), candidateText) {
		t.Errorf("conflict body leaked the losing writer's candidate text: %s", w.Body.String())
	}

	var instructions *string
	dbfx.QueryRow(t, `SELECT instructions FROM squad WHERE id = $1`, squad.ID).Scan(&instructions)
	if instructions == nil || *instructions != "conflict baseline" {
		t.Errorf("conflicting write mutated the row: instructions %v", instructions)
	}
}

// The lock must not over-reject: a matching value lands, and a non-positive
// one is a clean 400 like the issue/project contracts.
func TestUpdateSquadExpectedRevisionValidation(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler suite fixture unavailable")
	}
	leaderID := createHandlerTestAgent(t, "squad-revision-validation-leader", nil)
	squad := createSquadAs(t, "", "Revision Validation Squad", leaderID)

	updateSquadForRevision(t, squad.ID, map[string]any{
		"instructions":      "validation v2",
		"expected_revision": 0,
	}).Want(http.StatusBadRequest)

	var resp SquadResponse
	updateSquadForRevision(t, squad.ID, map[string]any{
		"instructions":      "validation v3",
		"expected_revision": squad.Revision,
	}).Want(http.StatusOK).JSON(&resp)
	if resp.Revision != squad.Revision+1 {
		t.Errorf("expected revision %d after guarded update, got %d", squad.Revision+1, resp.Revision)
	}
}

// Opt-in regression: a request without expected_revision keeps the legacy
// unconditional behavior — it lands and bumps the revision, never 409s.
func TestUpdateSquadWithoutExpectedRevisionUnchanged(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler suite fixture unavailable")
	}
	leaderID := createHandlerTestAgent(t, "squad-revision-optout-leader", nil)
	squad := createSquadAs(t, "", "Revision OptOut Squad", leaderID)

	for i := int64(2); i <= 3; i++ {
		var resp SquadResponse
		updateSquadForRevision(t, squad.ID, map[string]any{"instructions": "legacy write"}).
			Want(http.StatusOK).JSON(&resp)
		if resp.Revision != i {
			t.Errorf("legacy write: expected revision %d, got %d", i, resp.Revision)
		}
	}
}

// The prompt-version activation/rollback write is also an instruction write:
// it must bump the revision so a client holding an expected_revision from
// before the rollback loses the race instead of overwriting the restored
// instructions (same pattern as the project contract from RUYI-354).
func TestPromptVersionSquadInstructionsWriteBumpsRevision(t *testing.T) {
	if testPool == nil {
		t.Skip("handler suite fixture unavailable")
	}
	ctx := context.Background()
	leaderID := createHandlerTestAgent(t, "squad-revision-pv-leader", nil)
	squad := createSquadAs(t, "", "Revision PromptVersion Squad", leaderID)

	squadUUID := parseUUID(squad.ID)
	before, err := db.New(testPool).GetSquad(ctx, squadUUID)
	if err != nil {
		t.Fatalf("get squad: %v", err)
	}

	updated, err := db.New(testPool).UpdateSquadInstructionsForPromptVersion(ctx,
		db.UpdateSquadInstructionsForPromptVersionParams{ID: squadUUID, Instructions: "restored instructions"})
	if err != nil {
		t.Fatalf("prompt-version instructions write: %v", err)
	}
	if updated.Revision != before.Revision+1 {
		t.Errorf("expected revision %d after prompt-version write, got %d", before.Revision+1, updated.Revision)
	}
}
