package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
)

// RUYI-433 config-management concurrency and catalog-gate tests.
//
// Two behaviours live here:
//  1. Optimistic-lock revisions on agent and execution_profile — a client
//     reads revision N, writes with expected_revision=N, and either lands on
//     N+1 or gets a structured 409 revision_conflict carrying the actual
//     revision. Entry writes guard the OWNING profile's revision.
//  2. The model catalog gate on explicit model writes: with a usable
//     runtime-catalog snapshot, a model the catalog misses AND the static
//     provider catalogs classify as incompatible is refused 400
//     unsupported_model before anything is written; custom strings and
//     catalog-cold runtimes keep the passthrough.

func patchAgentForRevisionTest(t *testing.T, agentID string, body map[string]any) *testutil.Response {
	t.Helper()
	req := testutil.WithURLParams(
		newRequest(http.MethodPatch, "/api/agents/"+agentID, body),
		"id", agentID)
	return testutil.Call(t, testHandler.UpdateAgent, req)
}

func agentModelForRevisionTest(t *testing.T, agentID string) string {
	t.Helper()
	var model *string
	dbfx.QueryRow(t, `SELECT model FROM agent WHERE id = $1`, agentID).Scan(&model)
	if model == nil {
		return ""
	}
	return *model
}

func TestAgentRevision_OptimisticLocking(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	runtimeID := createClaudeProviderRuntime(t)
	agentID := createAgentOnRuntime(t, "revision-lock-agent", runtimeID, "")

	var readBack map[string]any
	patchAgentForRevisionTest(t, agentID, map[string]any{"description": "baseline read"}).
		Want(http.StatusOK).JSON(&readBack)
	revision, ok := readBack["revision"].(float64)
	if !ok || revision < 1 {
		t.Fatalf("expected revision >= 1 on agent response, got %v", readBack["revision"])
	}

	t.Run("matching expected_revision lands and bumps", func(t *testing.T) {
		var updated map[string]any
		patchAgentForRevisionTest(t, agentID, map[string]any{
			"description":       "guarded write",
			"expected_revision": int64(revision),
		}).Want(http.StatusOK).JSON(&updated)
		if got := updated["revision"].(float64); got != revision+1 {
			t.Fatalf("expected revision %v after guarded write, got %v", revision+1, got)
		}
	})

	t.Run("stale expected_revision answers 409 with actual revision", func(t *testing.T) {
		w := patchAgentForRevisionTest(t, agentID, map[string]any{
			"description":       "stale write",
			"expected_revision": int64(revision), // already consumed above
		})
		w.Want(http.StatusConflict)
		var body map[string]any
		_ = json.NewDecoder(w.Body).Decode(&body)
		if body["code"] != "revision_conflict" {
			t.Fatalf("expected code revision_conflict, got %v", body["code"])
		}
		if got := body["actual_revision"].(float64); got != revision+1 {
			t.Fatalf("expected actual_revision %v, got %v", revision+1, got)
		}
		// The refused write must not have landed.
		if desc := agentDescriptionForRevisionTest(t, agentID); desc != "guarded write" {
			t.Fatalf("refused write changed the row: description=%q", desc)
		}
	})

	t.Run("non-positive expected_revision is a 400", func(t *testing.T) {
		patchAgentForRevisionTest(t, agentID, map[string]any{
			"description":       "bad revision",
			"expected_revision": int64(0),
		}).Want(http.StatusBadRequest)
	})
}

func agentDescriptionForRevisionTest(t *testing.T, agentID string) string {
	t.Helper()
	var description string
	dbfx.QueryRow(t, `SELECT description FROM agent WHERE id = $1`, agentID).Scan(&description)
	return description
}

func TestAgentModelCatalogGate_RejectsKnownIncompatible(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	runtimeID := createClaudeProviderRuntime(t)
	// A real discovery result: two Claude models the daemon advertised.
	if err := testHandler.ModelCatalogCache.Put(ctx, runtimeID, []ModelEntry{
		{ID: "claude-opus-5"},
		{ID: "claude-sonnet-5"},
	}, nil, true); err != nil {
		t.Fatalf("seed catalog: %v", err)
	}
	agentID := createAgentOnRuntime(t, "catalog-gate-agent", runtimeID, "")

	t.Run("known cross-family model is refused before writing", func(t *testing.T) {
		w := patchAgentForRevisionTest(t, agentID, map[string]any{"model": "gpt-5.2"})
		w.Want(http.StatusBadRequest)
		var body map[string]any
		_ = json.NewDecoder(w.Body).Decode(&body)
		if body["code"] != "unsupported_model" {
			t.Fatalf("expected code unsupported_model, got %v (%s)", body["code"], w.Body.String())
		}
		// 无半配置状态：拒绝路径不落库。
		if model := agentModelForRevisionTest(t, agentID); model != "" {
			t.Fatalf("refused model write landed on the row: model=%q", model)
		}
	})

	t.Run("catalog-listed model passes", func(t *testing.T) {
		patchAgentForRevisionTest(t, agentID, map[string]any{"model": "claude-opus-5"}).
			Want(http.StatusOK)
		if model := agentModelForRevisionTest(t, agentID); model != "claude-opus-5" {
			t.Fatalf("expected catalog model to land, got %q", model)
		}
	})

	t.Run("claude context-window variant matches its base entry", func(t *testing.T) {
		patchAgentForRevisionTest(t, agentID, map[string]any{"model": "claude-opus-5[1m]"}).
			Want(http.StatusOK)
	})

	t.Run("unknown custom string keeps the manual-input passthrough", func(t *testing.T) {
		patchAgentForRevisionTest(t, agentID, map[string]any{"model": "my-proxy-model"}).
			Want(http.StatusOK)
	})
}

func TestAgentModelCatalogGate_CatalogMissPasses(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	// A second runtime with NO cached catalog: an offline / never-probed
	// runtime has no authoritative list to enforce, so the write passes
	// (决策 2：目录未命中放行).
	runtimeID := createClaudeProviderRuntime(t)
	agentID := createAgentOnRuntime(t, "catalog-cold-agent", runtimeID, "")

	patchAgentForRevisionTest(t, agentID, map[string]any{"model": "gpt-5.2"}).
		Want(http.StatusOK)
}

func getExecutionProfileForRevisionTest(t *testing.T, profileID string) ExecutionProfileResponse {
	t.Helper()
	req := testutil.WithURLParams(
		newRequest(http.MethodGet, "/api/workspaces/"+testWorkspaceID+"/execution-profiles/"+profileID, nil),
		"id", testWorkspaceID, "profileId", profileID)
	var resp ExecutionProfileResponse
	testutil.Call(t, testHandler.GetExecutionProfile, req).Want(http.StatusOK).JSON(&resp)
	return resp
}

func patchExecutionProfileForRevisionTest(t *testing.T, profileID string, body map[string]any) *testutil.Response {
	t.Helper()
	req := testutil.WithURLParams(
		newRequest(http.MethodPatch, "/api/workspaces/"+testWorkspaceID+"/execution-profiles/"+profileID, body),
		"id", testWorkspaceID, "profileId", profileID)
	return testutil.Call(t, testHandler.UpdateExecutionProfile, req)
}

func TestExecutionProfileRevision_GuardsProfileAndEntryWrites(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	runtimeID := createClaudeProviderRuntime(t)
	agentID := createAgentOnRuntime(t, "profile-revision-agent", runtimeID, "")

	body := map[string]any{"name": "profile-revision-test"}
	req := testutil.WithURLParams(
		newRequest(http.MethodPost, "/api/workspaces/"+testWorkspaceID+"/execution-profiles", body),
		"id", testWorkspaceID)
	var created ExecutionProfileResponse
	testutil.Call(t, testHandler.CreateExecutionProfile, req).Want(http.StatusCreated).JSON(&created)
	profileID := created.ID
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM execution_profile_entry WHERE profile_id = $1`, profileID)
		testPool.Exec(context.Background(), `DELETE FROM execution_profile WHERE id = $1`, profileID)
	})
	if created.Revision != 1 {
		t.Fatalf("expected a fresh profile at revision 1, got %d", created.Revision)
	}

	t.Run("rename with matching revision lands and bumps", func(t *testing.T) {
		var updated ExecutionProfileResponse
		patchExecutionProfileForRevisionTest(t, profileID, map[string]any{
			"name":              "profile-revision-test-renamed",
			"expected_revision": int64(1),
		}).Want(http.StatusOK).JSON(&updated)
		if updated.Revision != 2 {
			t.Fatalf("expected revision 2 after rename, got %d", updated.Revision)
		}
	})

	t.Run("stale rename answers 409 with actual revision", func(t *testing.T) {
		w := patchExecutionProfileForRevisionTest(t, profileID, map[string]any{
			"name":              "profile-revision-test-stale",
			"expected_revision": int64(1),
		})
		w.Want(http.StatusConflict)
		var resp map[string]any
		_ = json.NewDecoder(w.Body).Decode(&resp)
		if resp["code"] != "revision_conflict" || resp["actual_revision"].(float64) != 2 {
			t.Fatalf("expected revision_conflict/actual 2, got %v", resp)
		}
	})

	putEntryWithRevision := func(t *testing.T, expected *int64) *testutil.Response {
		t.Helper()
		body := map[string]any{
			"agent_id":   agentID,
			"runtime_id": runtimeID,
			"model":      "claude-opus-5",
		}
		if expected != nil {
			body["expected_revision"] = *expected
		}
		return putEntry(t, profileID, body)
	}

	t.Run("entry upsert with matching revision lands and bumps", func(t *testing.T) {
		expected := int64(2)
		putEntryWithRevision(t, &expected).Want(http.StatusOK)
		if got := getExecutionProfileForRevisionTest(t, profileID).Revision; got != 3 {
			t.Fatalf("expected revision 3 after entry upsert, got %d", got)
		}
	})

	t.Run("entry upsert with stale revision answers 409", func(t *testing.T) {
		expected := int64(2)
		w := putEntryWithRevision(t, &expected)
		w.Want(http.StatusConflict)
		var resp map[string]any
		_ = json.NewDecoder(w.Body).Decode(&resp)
		if resp["code"] != "revision_conflict" {
			t.Fatalf("expected revision_conflict, got %v", resp)
		}
	})

	t.Run("entry delete guards the profile revision too", func(t *testing.T) {
		delReq := testutil.WithURLParams(
			newRequest(http.MethodDelete,
				"/api/workspaces/"+testWorkspaceID+"/execution-profiles/"+profileID+"/entries/"+agentID+"?expected_revision=3", nil),
			"id", testWorkspaceID, "profileId", profileID, "agentId", agentID)
		testutil.Call(t, testHandler.DeleteExecutionProfileEntry, delReq).Want(http.StatusNoContent)
		if got := getExecutionProfileForRevisionTest(t, profileID).Revision; got != 4 {
			t.Fatalf("expected revision 4 after entry delete, got %d", got)
		}

		staleDel := testutil.WithURLParams(
			newRequest(http.MethodDelete,
				"/api/workspaces/"+testWorkspaceID+"/execution-profiles/"+profileID+"/entries/"+agentID+"?expected_revision=3", nil),
			"id", testWorkspaceID, "profileId", profileID, "agentId", agentID)
		w := testutil.Call(t, testHandler.DeleteExecutionProfileEntry, staleDel)
		w.Want(http.StatusConflict)
		var resp map[string]any
		_ = json.NewDecoder(w.Body).Decode(&resp)
		if resp["code"] != "revision_conflict" {
			t.Fatalf("expected revision_conflict, got %v", resp)
		}
	})

	t.Run("activation bumps both the profile and its agents", func(t *testing.T) {
		expected := int64(4)
		putEntryWithRevision(t, &expected).Want(http.StatusOK)

		var before AgentResponse
		getReq := testutil.WithURLParams(
			newRequest(http.MethodGet, "/api/agents/"+agentID, nil), "id", agentID)
		testutil.Call(t, testHandler.GetAgent, getReq).Want(http.StatusOK).JSON(&before)

		activate(t, profileID).Want(http.StatusOK)
		clearActiveProfile(t)

		profile := getExecutionProfileForRevisionTest(t, profileID)
		if profile.Revision != 6 { // 5 after entry upsert, 6 after activation bump
			t.Fatalf("expected profile revision 6 after activation, got %d", profile.Revision)
		}

		var after AgentResponse
		testutil.Call(t, testHandler.GetAgent, getReq).Want(http.StatusOK).JSON(&after)
		if after.Revision != before.Revision+1 {
			t.Fatalf("expected agent revision %d after activation, got %d", before.Revision+1, after.Revision)
		}
		if after.Model != "claude-opus-5" {
			t.Fatalf("expected activation to write the entry model, got %q", after.Model)
		}
	})
}
