package handler

// Cross-workspace authority tests for the prompt marketplace (RUYI-100).
//
// The finding these pin: h.workspaceMember returns whatever member the
// middleware put on the request context WITHOUT checking which workspace that
// member belongs to. Every prompt path that took a workspace id from an object
// the caller named — a squad's own workspace — and then asked workspaceMember
// about it was really asking "what is this caller's role in the workspace they
// came from", and answering for a squad somewhere else entirely.
//
// So the setup here is deliberately the escalation shape: testUserID is an
// OWNER of the test workspace and not a member of the foreign one at all, and
// the request carries a context member exactly as the middleware would build
// it. Before the fix that owner row was applied to a foreign squad and
// canManageSquad said yes. Every case below must be 404 — not 403, because a
// caller who cannot see the workspace must not learn that the id exists.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// foreignPromptWorkspace builds a whole second workspace: its own user, its own
// runtime, its own agent, and a squad with prompt text worth stealing.
func foreignPromptWorkspace(t *testing.T, slug string) (workspaceID, userID, squadID string) {
	t.Helper()

	userID = dbfx.User(t, "Foreign Owner "+slug, slug+"@prompt-xws.test")
	workspaceID = dbfx.Workspace(t, "Foreign "+slug, slug, testutil.Cols{
		"issue_prefix": "",
	})
	dbfx.Member(t, workspaceID, userID, "owner")

	runtimeID := dbfx.Runtime(t, "foreign runtime "+slug, testutil.Cols{
		"workspace_id": workspaceID,
		"owner_id":     userID,
	})
	leaderID := dbfx.Agent(t, "foreign-leader-"+slug, runtimeID, testutil.Cols{
		"workspace_id": workspaceID,
		"owner_id":     userID,
		"instructions": "the foreign leader prompt",
	})
	squadID = dbfx.Squad(t, "foreign-squad-"+slug, leaderID, testutil.Cols{
		"workspace_id": workspaceID,
		"creator_id":   userID,
		"instructions": "the foreign squad's own carefully tuned prompt",
	})
	return workspaceID, userID, squadID
}

// callPromptAsContextOwner drives a handler the way the router would: with the
// caller's OWN workspace member row already on the context, which is precisely
// the value the vulnerable helper returned without checking.
func callPromptAsContextOwner(t *testing.T, h func(http.ResponseWriter, *http.Request), method, path string, body any, params map[string]string) (int, string) {
	t.Helper()
	withMarketplaceV1Flag(t, testHandler, true)

	memberRow, err := testHandler.Queries.GetMemberByUserAndWorkspace(t.Context(),
		db.GetMemberByUserAndWorkspaceParams{
			UserID:      util.MustParseUUID(testUserID),
			WorkspaceID: util.MustParseUUID(testWorkspaceID),
		})
	if err != nil {
		t.Fatalf("load the caller's own member row: %v", err)
	}
	if memberRow.Role != "owner" {
		t.Fatalf("this test needs the caller to be an owner of their own workspace, got %q", memberRow.Role)
	}

	req := newRequest(method, path, body)
	req = req.WithContext(middleware.SetMemberContext(req.Context(), testWorkspaceID, memberRow))
	if len(params) > 0 {
		kv := make([]string, 0, len(params)*2)
		for k, v := range params {
			kv = append(kv, k, v)
		}
		req = withURLParams(req, kv...)
	}
	w := httptest.NewRecorder()
	h(w, req)
	return w.Code, w.Body.String()
}

// A workspace-A owner must not be able to publish workspace-B's squad prompt.
// This is the leak with the widest blast radius: publishing copies the source
// object's live text into a catalog other workspaces can read.
func TestPublishRejectsForeignWorkspaceSquad(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	_, _, foreignSquadID := foreignPromptWorkspace(t, "prompt-xws-publish")

	code, raw := callPromptAsContextOwner(t, testHandler.CreatePromptVersion, http.MethodPost,
		"/api/marketplace/prompt-versions", map[string]any{
			"source_type":  "squad",
			"source_id":    foreignSquadID,
			"name":         "Stolen Prompt",
			"summary":      "published from a workspace the caller has no part in",
			"license_code": "cc0",
		}, nil)
	if code != http.StatusNotFound {
		t.Fatalf("expected 404 publishing a foreign squad, got %d: %s", code, raw)
	}
	// The response must not carry the prompt either: a 4xx that echoes the text
	// would leak exactly what the 404 is there to protect.
	if containsForeignPrompt(raw) {
		t.Fatalf("the rejection leaked the foreign squad's prompt: %s", raw)
	}

	count := dbfx.Count(t, `SELECT count(*) FROM marketplace_prompt_version WHERE source_id = $1`, foreignSquadID)
	if count != 0 {
		t.Fatalf("a rejected publish still wrote %d version rows", count)
	}
}

// The same authority governs applying INTO a squad: a prompt written into a
// foreign squad would replace text the caller has no standing to touch.
func TestApplyRejectsForeignWorkspaceSquadTarget(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	_, _, foreignSquadID := foreignPromptWorkspace(t, "prompt-xws-apply")

	// A legitimately published, legitimately installed squad prompt in the
	// caller's OWN workspace — so the only thing wrong with the request is the
	// target.
	squadID := promptTestSquad(t, "prompt-xws-source", "the source squad prompt")
	draft := createPromptDraft(t, squadID, map[string]any{
		"source_type": "squad", "source_id": squadID,
	})
	published := publishPromptDraft(t, draft.ID)
	install := installPrompt(t, published.ID)

	const before = "the foreign squad's own carefully tuned prompt"

	code, raw := callPromptAsContextOwner(t, testHandler.PreviewPromptApply, http.MethodPost,
		"/api/marketplace/prompt-installations/"+install.ID+"/preview",
		map[string]any{"target_type": "squad", "target_id": foreignSquadID},
		map[string]string{"id": install.ID})
	if code != http.StatusNotFound {
		t.Fatalf("expected 404 previewing into a foreign squad, got %d: %s", code, raw)
	}
	if containsForeignPrompt(raw) {
		t.Fatalf("the preview rejection leaked the foreign squad's prompt: %s", raw)
	}

	code, raw = callPromptAsContextOwner(t, testHandler.ApplyPrompt, http.MethodPost,
		"/api/marketplace/prompt-installations/"+install.ID+"/apply",
		map[string]any{
			"target_type": "squad", "target_id": foreignSquadID,
			"strategy": "replace", "operation_id": "op-xws-apply",
		}, map[string]string{"id": install.ID})
	if code != http.StatusNotFound {
		t.Fatalf("expected 404 applying into a foreign squad, got %d: %s", code, raw)
	}

	var after string
	dbfx.QueryRow(t, `SELECT instructions FROM squad WHERE id = $1`, foreignSquadID).Scan(&after)
	if after != before {
		t.Fatalf("the foreign squad's prompt was rewritten: %q", after)
	}
}

// Restore writes to the target too, so it takes the same gate. A restore that
// escaped it would overwrite a foreign squad with whatever text the attacker's
// own apply state happened to hold.
func TestRestoreRejectsForeignWorkspaceSquadTarget(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	_, _, foreignSquadID := foreignPromptWorkspace(t, "prompt-xws-restore")

	code, raw := callPromptAsContextOwner(t, testHandler.RestorePrompt, http.MethodPost,
		"/api/marketplace/prompt-targets/squad/"+foreignSquadID+"/restore",
		map[string]any{"operation_id": "op-xws-restore"},
		map[string]string{"type": "squad", "id": foreignSquadID})
	if code != http.StatusNotFound {
		t.Fatalf("expected 404 restoring a foreign squad, got %d: %s", code, raw)
	}

	code, raw = callPromptAsContextOwner(t, testHandler.GetPromptTargetState, http.MethodGet,
		"/api/marketplace/prompt-targets/squad/"+foreignSquadID, nil,
		map[string]string{"type": "squad", "id": foreignSquadID})
	if code != http.StatusNotFound {
		t.Fatalf("expected 404 reading a foreign squad's prompt state, got %d: %s", code, raw)
	}
	if containsForeignPrompt(raw) {
		t.Fatalf("the state rejection leaked the foreign squad's prompt: %s", raw)
	}
}

// Withdrawal authority belongs to the publishing workspace. A foreign caller
// pulling someone else's published asset is a denial of service against every
// workspace that installed it, so it takes the same lookup — and a private
// version must not be readable to them either.
func TestWithdrawAndPrivateReadRejectForeignWorkspaceVersion(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	foreignWorkspaceID, foreignUserID, foreignSquadID := foreignPromptWorkspace(t, "prompt-xws-withdraw")

	const foreignContent = "the foreign squad's own carefully tuned prompt"
	versionID := dbfx.Insert(t, "marketplace_prompt_version", testutil.Cols{
		"series_id":              foreignSquadID,
		"kind":                   "squad_prompt",
		"version":                1,
		"source_workspace_id":    foreignWorkspaceID,
		"source_type":            "squad",
		"source_id":              foreignSquadID,
		"publisher_user_id":      foreignUserID,
		"publisher_display_name": "Foreign Owner",
		"content":                foreignContent,
		"content_sha256":         sha256Hex(foreignContent),
		"name":                   "Foreign Asset",
		"summary":                "published by a workspace the caller has no part in",
		"license_code":           "cc0",
		"state":                  "published",
		// Private: the discovery path must not answer for it, so the only way
		// to read it is the publisher check.
		"visibility":       "private",
		"scanner_revision": "test",
		"scanned_at":       testutil.Raw("now()"),
		"published_at":     testutil.Raw("now()"),
	})

	code, raw := callPromptAsContextOwner(t, testHandler.GetPromptVersion, http.MethodGet,
		"/api/marketplace/prompt-versions/"+versionID, nil,
		map[string]string{"id": versionID})
	if code != http.StatusNotFound {
		t.Fatalf("expected 404 reading a foreign private version, got %d: %s", code, raw)
	}
	if containsForeignPrompt(raw) {
		t.Fatalf("the rejected read returned the prompt body: %s", raw)
	}

	code, raw = callPromptAsContextOwner(t, testHandler.WithdrawPromptVersion, http.MethodPost,
		"/api/marketplace/prompt-versions/"+versionID+"/withdraw", nil,
		map[string]string{"id": versionID})
	if code != http.StatusNotFound {
		t.Fatalf("expected 404 withdrawing a foreign version, got %d: %s", code, raw)
	}

	var state string
	dbfx.QueryRow(t, `SELECT state FROM marketplace_prompt_version WHERE id = $1`, versionID).Scan(&state)
	if state != promptStatePublished {
		t.Fatalf("the foreign version was withdrawn anyway: state = %q", state)
	}
}

// containsForeignPrompt looks for the distinctive phrase in the foreign squad's
// text. Every rejection above asserts on it: a 404 that still echoes the prompt
// body would leak exactly what the 404 is protecting.
func containsForeignPrompt(body string) bool {
	return strings.Contains(body, "carefully tuned prompt")
}
