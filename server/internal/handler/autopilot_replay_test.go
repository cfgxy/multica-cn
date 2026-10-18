package handler

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/testutil"
)

func insertReplaySourceRun(t *testing.T, autopilotID string, payload string) string {
	t.Helper()
	return dbfx.Insert(t, "autopilot_run", testutil.Cols{
		"autopilot_id":    autopilotID,
		"source":          "manual",
		"status":          "failed",
		"trigger_payload": []byte(payload),
	})
}

func triggerReplay(t *testing.T, userID, autopilotID, sourceRunID, key string) *testutil.Response {
	t.Helper()
	req := newRequestAs(userID, http.MethodPost, "/api/autopilots/"+autopilotID+"/trigger?workspace_id="+testWorkspaceID, map[string]any{
		"mode":          "replay",
		"source_run_id": sourceRunID,
	})
	req.Header.Set("Idempotency-Key", key)
	return testutil.Call(t, testHandler.TriggerAutopilot, withURLParam(req, "id", autopilotID))
}

func TestTriggerAutopilotReplayIsScopedValidatedAndIdempotent(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	agentID := createWebhookTestAgent(t, "Replay Agent")
	autopilotID := createWebhookTestAutopilot(t, agentID, "active", "run_only")
	otherAutopilotID := createWebhookTestAutopilot(t, agentID, "active", "run_only")
	putAutopilotParams(t, autopilotID, []service.AutopilotParamInput{cityParam()})

	validSource := insertReplaySourceRun(t, autopilotID, `{"params":{"city":"Paris"}}`)
	foreignSource := insertReplaySourceRun(t, otherAutopilotID, `{"params":{"city":"Paris"}}`)
	invalidSource := insertReplaySourceRun(t, autopilotID, `{"params":{"legacy_city":"Paris"}}`)

	before := dbfx.Count(t, `SELECT count(*) FROM autopilot_run WHERE autopilot_id = $1`, autopilotID)
	triggerReplay(t, testUserID, autopilotID, foreignSource, "replay-foreign").Want(http.StatusNotFound)
	if got := dbfx.Count(t, `SELECT count(*) FROM autopilot_run WHERE autopilot_id = $1`, autopilotID); got != before {
		t.Fatalf("cross-autopilot replay created a run: before=%d after=%d", before, got)
	}

	triggerReplay(t, testUserID, autopilotID, invalidSource, "replay-invalid-current-config").Want(http.StatusBadRequest)
	if got := dbfx.Count(t, `SELECT count(*) FROM autopilot_run WHERE autopilot_id = $1`, autopilotID); got != before {
		t.Fatalf("invalid replay created a run: before=%d after=%d", before, got)
	}

	const key = "replay-idempotency-key"
	first := triggerReplay(t, testUserID, autopilotID, validSource, key).Want(http.StatusOK)
	var firstRun AutopilotRunResponse
	first.JSON(&firstRun)
	if firstRun.Mode != "replay" || firstRun.SourceRunID == nil || *firstRun.SourceRunID != validSource {
		t.Fatalf("replay response mode/source = %q/%v, want replay/%s", firstRun.Mode, firstRun.SourceRunID, validSource)
	}

	second := triggerReplay(t, testUserID, autopilotID, validSource, key).Want(http.StatusOK)
	var secondRun AutopilotRunResponse
	second.JSON(&secondRun)
	if secondRun.ID != firstRun.ID {
		t.Fatalf("same-key replay returned run %s after %s", secondRun.ID, firstRun.ID)
	}
	if got := dbfx.Count(t, `SELECT count(*) FROM autopilot_run WHERE autopilot_id = $1 AND trigger_payload->'_mcp_replay'->>'source_run_id' = $2`, autopilotID, validSource); got != 1 {
		t.Fatalf("runs created for same replay key/source = %d, want exactly one", got)
	}

	var storedSourcePayload []byte
	dbfx.QueryRow(t, `SELECT trigger_payload FROM autopilot_run WHERE id = $1`, validSource).Scan(&storedSourcePayload)
	var stored map[string]any
	if err := json.Unmarshal(storedSourcePayload, &stored); err != nil {
		t.Fatalf("decode original source payload: %v", err)
	}
	if _, changed := stored["_mcp_replay"]; changed {
		t.Fatalf("source run payload was modified by replay: %s", storedSourcePayload)
	}
}

func TestTriggerAutopilotReplayUsesCurrentCallerAuthorization(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	agentID := createWebhookTestAgent(t, "Replay authorization agent")
	autopilotID := createWebhookTestAutopilot(t, agentID, "active", "run_only")
	sourceRunID := insertReplaySourceRun(t, autopilotID, `{"params":{}}`)
	strangerID := dbfx.User(t, "Replay stranger", "replay-stranger@example.test")
	dbfx.Member(t, testWorkspaceID, strangerID, "member")

	before := dbfx.Count(t, `SELECT count(*) FROM autopilot_run WHERE autopilot_id = $1`, autopilotID)
	res := triggerReplay(t, strangerID, autopilotID, sourceRunID, "replay-unauthorized")
	res.Want(http.StatusForbidden)
	if got := dbfx.Count(t, `SELECT count(*) FROM autopilot_run WHERE autopilot_id = $1`, autopilotID); got != before {
		t.Fatalf("unauthorized replay created a run: before=%d after=%d", before, got)
	}
}
