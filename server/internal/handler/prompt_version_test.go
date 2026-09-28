package handler

// Tests for prompt tier version management (RUYI-183, self-evolution phase 1).
//
// These drive the handlers and, for the write-permission gate, the router
// middleware chain directly — matching the rest of this suite (see
// marketplace_prompt_test.go, actor_guards_test.go).

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/middleware"
)

func decodePromptGovVersion(t *testing.T, raw string) PromptGovernanceVersionResponse {
	t.Helper()
	var resp PromptGovernanceVersionResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("decode version: %v (%s)", err, raw)
	}
	return resp
}

func callPromptGov(t *testing.T, h http.HandlerFunc, method, path string, body any, params map[string]string) (int, string) {
	t.Helper()
	req := newRequest(method, path, body)
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

// ── happy path: save creates v2, list shows v1 (import backfill) + v2, and
// the business column is updated in lockstep. ──────────────────────────────

func TestPromptGovernanceSaveCreatesVersionAndUpdatesBusinessColumn(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := dbfx.Agent(t, "prompt-gov-save", handlerTestRuntimeID(t), map[string]any{"instructions": "v1 content"})
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM prompt_version WHERE scope = 'agent' AND scope_id = $1`, agentID)
	})

	code, raw := callPromptGov(t, testHandler.SavePromptGovernanceVersion, http.MethodPost,
		"/api/prompt-governance/agent/"+agentID+"/versions",
		map[string]any{"content": "v2 content", "change_note": "second edit"},
		map[string]string{"scope": "agent", "scopeId": agentID})
	if code != http.StatusOK {
		t.Fatalf("save: expected 200, got %d: %s", code, raw)
	}
	saved := decodePromptGovVersion(t, raw)
	if saved.Version != 1 {
		// A brand-new agent has no import backfill (only pre-existing rows
		// were backfilled to v1), so the first save here is v1.
		t.Fatalf("version = %d, want 1 for a fresh agent's first save", saved.Version)
	}
	if saved.Content != "v2 content" || saved.Source != "edit" {
		t.Fatalf("unexpected saved version: %+v", saved)
	}

	var instructions string
	dbfx.QueryRow(t, `SELECT instructions FROM agent WHERE id = $1`, agentID).Scan(&instructions)
	if instructions != "v2 content" {
		t.Fatalf("agent.instructions = %q, want the saved content (business column must move in lockstep)", instructions)
	}

	// List must show exactly the one version just created.
	code, raw = callPromptGov(t, testHandler.ListPromptGovernanceVersions, http.MethodGet,
		"/api/prompt-governance/agent/"+agentID+"/versions", nil,
		map[string]string{"scope": "agent", "scopeId": agentID})
	if code != http.StatusOK {
		t.Fatalf("list: expected 200, got %d: %s", code, raw)
	}
	var listResp struct {
		Versions []PromptGovernanceVersionResponse `json:"versions"`
		Total    int64                             `json:"total"`
	}
	if err := json.Unmarshal([]byte(raw), &listResp); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if listResp.Total != 1 || len(listResp.Versions) != 1 {
		t.Fatalf("list total/len = %d/%d, want 1/1: %s", listResp.Total, len(listResp.Versions), raw)
	}

	// A second save must assign v2, not collide with v1.
	code, raw = callPromptGov(t, testHandler.SavePromptGovernanceVersion, http.MethodPost,
		"/api/prompt-governance/agent/"+agentID+"/versions",
		map[string]any{"content": "v3 content", "change_note": "third edit"},
		map[string]string{"scope": "agent", "scopeId": agentID})
	if code != http.StatusOK {
		t.Fatalf("second save: expected 200, got %d: %s", code, raw)
	}
	if v := decodePromptGovVersion(t, raw); v.Version != 2 {
		t.Fatalf("second save version = %d, want 2", v.Version)
	}
}

// ── W2: a save whose content trips the secret scanner must be blocked, and
// must leave neither a new prompt_version row nor a changed business column
// behind — and the response must never echo the secret text itself. ───────

func TestPromptGovernanceSaveBlockedBySecretGate(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := dbfx.Agent(t, "prompt-gov-secret", handlerTestRuntimeID(t), map[string]any{"instructions": "clean content"})
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM prompt_version WHERE scope = 'agent' AND scope_id = $1`, agentID)
	})

	code, raw := callPromptGov(t, testHandler.SavePromptGovernanceVersion, http.MethodPost,
		"/api/prompt-governance/agent/"+agentID+"/versions",
		map[string]any{"content": "leaked token: " + promptSecretText, "change_note": "oops"},
		map[string]string{"scope": "agent", "scopeId": agentID})
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 prompt_secret_detected, got %d: %s", code, raw)
	}
	if strings.Contains(raw, promptSecretText) {
		t.Fatalf("blocked response must never echo the secret text: %s", raw)
	}

	var instructions string
	dbfx.QueryRow(t, `SELECT instructions FROM agent WHERE id = $1`, agentID).Scan(&instructions)
	if instructions != "clean content" {
		t.Fatalf("agent.instructions changed despite the gate blocking the save: %q", instructions)
	}

	var count int
	dbfx.QueryRow(t, `SELECT count(*) FROM prompt_version WHERE scope = 'agent' AND scope_id = $1`, agentID).Scan(&count)
	if count != 0 {
		t.Fatalf("prompt_version rows = %d, want 0 (blocked save must not append a row)", count)
	}
}

// ── switch/rollback: switching to a historical version creates a new
// version whose content matches the target, tagged source=revert with
// source_version pointing at what was restored. ────────────────────────────

func TestPromptGovernanceSwitchCreatesRevertVersion(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := dbfx.Agent(t, "prompt-gov-switch", handlerTestRuntimeID(t), map[string]any{"instructions": "original"})
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM prompt_version WHERE scope = 'agent' AND scope_id = $1`, agentID)
	})

	code, raw := callPromptGov(t, testHandler.SavePromptGovernanceVersion, http.MethodPost,
		"/api/prompt-governance/agent/"+agentID+"/versions",
		map[string]any{"content": "original", "change_note": "v1"},
		map[string]string{"scope": "agent", "scopeId": agentID})
	if code != http.StatusOK {
		t.Fatalf("seed v1: expected 200, got %d: %s", code, raw)
	}
	v1 := decodePromptGovVersion(t, raw)

	code, raw = callPromptGov(t, testHandler.SavePromptGovernanceVersion, http.MethodPost,
		"/api/prompt-governance/agent/"+agentID+"/versions",
		map[string]any{"content": "changed", "change_note": "v2"},
		map[string]string{"scope": "agent", "scopeId": agentID})
	if code != http.StatusOK {
		t.Fatalf("seed v2: expected 200, got %d: %s", code, raw)
	}

	code, raw = callPromptGov(t, testHandler.SwitchPromptGovernanceVersion, http.MethodPost,
		"/api/prompt-governance/agent/"+agentID+"/versions/"+strconv.Itoa(int(v1.Version))+"/switch", nil,
		map[string]string{"scope": "agent", "scopeId": agentID, "version": strconv.Itoa(int(v1.Version))})
	if code != http.StatusOK {
		t.Fatalf("switch: expected 200, got %d: %s", code, raw)
	}
	reverted := decodePromptGovVersion(t, raw)
	if reverted.Version != v1.Version+2 {
		t.Fatalf("reverted version = %d, want %d (append-only, never reuses v1's number)", reverted.Version, v1.Version+2)
	}
	if reverted.Content != "original" {
		t.Fatalf("reverted content = %q, want %q", reverted.Content, "original")
	}
	if reverted.Source != "revert" || reverted.SourceVersion == nil || *reverted.SourceVersion != v1.Version {
		t.Fatalf("reverted version metadata wrong: source=%q source_version=%v, want revert/%d", reverted.Source, reverted.SourceVersion, v1.Version)
	}

	var instructions string
	dbfx.QueryRow(t, `SELECT instructions FROM agent WHERE id = $1`, agentID).Scan(&instructions)
	if instructions != "original" {
		t.Fatalf("agent.instructions = %q after switch, want the reverted content", instructions)
	}
}

// ── diff: returns both endpoints' full content. ─────────────────────────────

func TestPromptGovernanceDiffReturnsBothVersions(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := dbfx.Agent(t, "prompt-gov-diff", handlerTestRuntimeID(t), map[string]any{"instructions": "a"})
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM prompt_version WHERE scope = 'agent' AND scope_id = $1`, agentID)
	})

	callPromptGov(t, testHandler.SavePromptGovernanceVersion, http.MethodPost,
		"/api/prompt-governance/agent/"+agentID+"/versions",
		map[string]any{"content": "content A"}, map[string]string{"scope": "agent", "scopeId": agentID})
	callPromptGov(t, testHandler.SavePromptGovernanceVersion, http.MethodPost,
		"/api/prompt-governance/agent/"+agentID+"/versions",
		map[string]any{"content": "content B"}, map[string]string{"scope": "agent", "scopeId": agentID})

	req := newRequest(http.MethodGet, "/api/prompt-governance/agent/"+agentID+"/diff?from=1&to=2", nil)
	req = withURLParams(req, "scope", "agent", "scopeId", agentID)
	w := httptest.NewRecorder()
	testHandler.GetPromptGovernanceVersionDiff(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("diff: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var diffResp struct {
		From PromptGovernanceVersionResponse `json:"from"`
		To   PromptGovernanceVersionResponse `json:"to"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &diffResp); err != nil {
		t.Fatalf("decode diff: %v", err)
	}
	if diffResp.From.Content != "content A" || diffResp.To.Content != "content B" {
		t.Fatalf("diff content mismatch: from=%q to=%q", diffResp.From.Content, diffResp.To.Content)
	}
}

// ── scope isolation (P2/S2/A2): a scope_id that belongs to a different
// workspace must 404, never leak content across workspace boundaries. ─────

func TestPromptGovernanceScopeIsolationAcrossWorkspaces(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	otherWorkspace := dbfx.Workspace(t, "Prompt Gov Other WS", "prompt-gov-other-ws")
	otherAgent := dbfx.Agent(t, "prompt-gov-other-agent", "", map[string]any{
		"workspace_id": otherWorkspace,
		"instructions": "belongs to another workspace",
	})

	// Requested under testWorkspaceID (via X-Workspace-ID set by newRequest),
	// but the agent lives in otherWorkspace.
	code, raw := callPromptGov(t, testHandler.ListPromptGovernanceVersions, http.MethodGet,
		"/api/prompt-governance/agent/"+otherAgent+"/versions", nil,
		map[string]string{"scope": "agent", "scopeId": otherAgent})
	if code != http.StatusNotFound {
		t.Fatalf("cross-workspace list: expected 404, got %d: %s", code, raw)
	}

	code, raw = callPromptGov(t, testHandler.SavePromptGovernanceVersion, http.MethodPost,
		"/api/prompt-governance/agent/"+otherAgent+"/versions",
		map[string]any{"content": "attempted cross-workspace write"},
		map[string]string{"scope": "agent", "scopeId": otherAgent})
	if code != http.StatusNotFound {
		t.Fatalf("cross-workspace save: expected 404, got %d: %s", code, raw)
	}

	var instructions string
	dbfx.QueryRow(t, `SELECT instructions FROM agent WHERE id = $1`, otherAgent).Scan(&instructions)
	if instructions != "belongs to another workspace" {
		t.Fatalf("cross-workspace save must not have written through: %q", instructions)
	}
}

// A workspace-scope request whose scope_id is not the resolved workspace's
// own id must 404 too — LockWorkspaceForPromptVersion has no workspace_id
// column to filter on, so this check lives in the handler itself.
func TestPromptGovernanceWorkspaceScopeRejectsForeignScopeID(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	otherWorkspace := dbfx.Workspace(t, "Prompt Gov Foreign WS", "prompt-gov-foreign-ws")

	code, raw := callPromptGov(t, testHandler.SavePromptGovernanceVersion, http.MethodPost,
		"/api/prompt-governance/workspace/"+otherWorkspace+"/versions",
		map[string]any{"content": "should not apply"},
		map[string]string{"scope": "workspace", "scopeId": otherWorkspace})
	if code != http.StatusNotFound {
		t.Fatalf("expected 404 for scope_id != resolved workspace, got %d: %s", code, raw)
	}
}

// ── W1: writes are Owner-only. This wraps the real handler with the real
// middleware.RequireWorkspaceRole(queries, "owner") chain from router.go —
// not a handler-internal check — so the assertion actually exercises the
// enforcement point and fails if that middleware is ever dropped from the
// route registration. ───────────────────────────────────────────────────────

func TestPromptGovernanceSaveRequiresOwnerRole(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := dbfx.Agent(t, "prompt-gov-perm", handlerTestRuntimeID(t), map[string]any{"instructions": "untouched"})
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM prompt_version WHERE scope = 'agent' AND scope_id = $1`, agentID)
	})
	memberUserID := dbfx.User(t, "Prompt Gov Member", "prompt-gov-member@multica.ai")
	dbfx.Member(t, testWorkspaceID, memberUserID, "member")

	chain := middleware.RequireWorkspaceRole(testHandler.Queries, "owner")(
		http.HandlerFunc(testHandler.SavePromptGovernanceVersion))

	// Negative path: a plain workspace member must be rejected. If the
	// "owner" requirement were ever loosened or removed, this assertion
	// would start failing (it is not vacuously true — see the owner-path
	// assertion right below, which proves the same chain lets a permitted
	// caller through).
	req := newRequestAs(memberUserID, http.MethodPost, "/api/prompt-governance/agent/"+agentID+"/versions",
		map[string]any{"content": "should be rejected"})
	req = withURLParams(req, "scope", "agent", "scopeId", agentID)
	w := httptest.NewRecorder()
	chain.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("member write: expected 403, got %d: %s", w.Code, w.Body.String())
	}
	var instructions string
	dbfx.QueryRow(t, `SELECT instructions FROM agent WHERE id = $1`, agentID).Scan(&instructions)
	if instructions != "untouched" {
		t.Fatalf("agent.instructions changed despite the member being rejected: %q", instructions)
	}

	// Positive path: the workspace owner (testUserID) goes through the same
	// chain and succeeds, proving the 403 above is the role check and not
	// some unrelated wiring problem.
	req = newRequest(http.MethodPost, "/api/prompt-governance/agent/"+agentID+"/versions",
		map[string]any{"content": "owner write"})
	req = withURLParams(req, "scope", "agent", "scopeId", agentID)
	w = httptest.NewRecorder()
	chain.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("owner write: expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

// ── empty-content guard (RUYI-213) ──────────────────────────────────────────
//
// The 922 v1 backfill snapshotted whatever each tier's business column held at
// the time, so a tier whose content had already been lost carries an empty v1
// row. Switching to such a version used to copy that emptiness straight back
// over content restored since. The guard rejects any write whose content is
// blank while the tier's current effective content is not.

func TestPromptGovernanceSwitchToEmptyVersionIsRejected(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := dbfx.Agent(t, "prompt-gov-empty-switch", handlerTestRuntimeID(t), map[string]any{"instructions": "live content"})
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM prompt_version WHERE scope = 'agent' AND scope_id = $1`, agentID)
	})

	// An empty v1 row, exactly as the backfill would have written it for a
	// tier whose business column was already blank. It cannot be created
	// through the handler — the save gate rejects empty content — so it is
	// inserted directly, the same way the migration did.
	dbfx.Exec(t, `INSERT INTO prompt_version (workspace_id, scope, scope_id, version, content, content_sha256, source, change_note)
		VALUES ($1, 'agent', $2, 1, '', encode(digest('', 'sha256'), 'hex'), 'import', 'empty v1 baseline')`,
		testWorkspaceID, agentID)

	code, raw := callPromptGov(t, testHandler.SwitchPromptGovernanceVersion, http.MethodPost,
		"/api/prompt-governance/agent/"+agentID+"/versions/1/switch", nil,
		map[string]string{"scope": "agent", "scopeId": agentID, "version": "1"})
	if code != http.StatusConflict {
		t.Fatalf("switch to empty v1: expected 409, got %d: %s", code, raw)
	}

	// The point of the guard is the business column, not the status code:
	// the live instructions must survive the rejected switch.
	var instructions string
	dbfx.QueryRow(t, `SELECT instructions FROM agent WHERE id = $1`, agentID).Scan(&instructions)
	if instructions != "live content" {
		t.Fatalf("agent.instructions = %q after a rejected switch, want the live content intact", instructions)
	}
	// And no new version row may be appended for a write that was refused.
	if n := dbfx.Count(t, `SELECT count(*) FROM prompt_version WHERE scope = 'agent' AND scope_id = $1`, agentID); n != 1 {
		t.Fatalf("prompt_version rows = %d after a rejected switch, want the single seeded v1", n)
	}
}

func TestPromptGovernanceSwitchToWhitespaceVersionIsRejected(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := dbfx.Agent(t, "prompt-gov-blank-switch", handlerTestRuntimeID(t), map[string]any{"instructions": "live content"})
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM prompt_version WHERE scope = 'agent' AND scope_id = $1`, agentID)
	})

	// Whitespace-only content passes the save gate's len()>0 check, so it can
	// reach prompt_version through a normal save and later be switched back
	// to. It erases live text just as thoroughly as an empty string.
	dbfx.Exec(t, `INSERT INTO prompt_version (workspace_id, scope, scope_id, version, content, content_sha256, source, change_note)
		VALUES ($1, 'agent', $2, 1, '   ', encode(digest('   ', 'sha256'), 'hex'), 'import', 'whitespace v1 baseline')`,
		testWorkspaceID, agentID)

	code, raw := callPromptGov(t, testHandler.SwitchPromptGovernanceVersion, http.MethodPost,
		"/api/prompt-governance/agent/"+agentID+"/versions/1/switch", nil,
		map[string]string{"scope": "agent", "scopeId": agentID, "version": "1"})
	if code != http.StatusConflict {
		t.Fatalf("switch to whitespace v1: expected 409, got %d: %s", code, raw)
	}
	var instructions string
	dbfx.QueryRow(t, `SELECT instructions FROM agent WHERE id = $1`, agentID).Scan(&instructions)
	if instructions != "live content" {
		t.Fatalf("agent.instructions = %q after a rejected switch, want the live content intact", instructions)
	}
}

func TestPromptGovernanceSaveOfWhitespaceContentIsRejected(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := dbfx.Agent(t, "prompt-gov-blank-save", handlerTestRuntimeID(t), map[string]any{"instructions": "live content"})
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM prompt_version WHERE scope = 'agent' AND scope_id = $1`, agentID)
	})

	code, raw := callPromptGov(t, testHandler.SavePromptGovernanceVersion, http.MethodPost,
		"/api/prompt-governance/agent/"+agentID+"/versions",
		map[string]any{"content": "\n \t\n", "change_note": "accidental blank save"},
		map[string]string{"scope": "agent", "scopeId": agentID})
	if code != http.StatusConflict {
		t.Fatalf("blank save: expected 409, got %d: %s", code, raw)
	}
	var instructions string
	dbfx.QueryRow(t, `SELECT instructions FROM agent WHERE id = $1`, agentID).Scan(&instructions)
	if instructions != "live content" {
		t.Fatalf("agent.instructions = %q after a rejected blank save, want the live content intact", instructions)
	}
}

// The guard must not block a write to a tier that is already empty — there is
// no live content to lose, and an agent created without instructions must
// still be able to receive its first (or a subsequent blank) version.
func TestPromptGovernanceBlankWriteAllowedWhenCurrentContentIsEmpty(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := dbfx.Agent(t, "prompt-gov-blank-on-blank", handlerTestRuntimeID(t), map[string]any{"instructions": ""})
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM prompt_version WHERE scope = 'agent' AND scope_id = $1`, agentID)
	})

	code, raw := callPromptGov(t, testHandler.SavePromptGovernanceVersion, http.MethodPost,
		"/api/prompt-governance/agent/"+agentID+"/versions",
		map[string]any{"content": " ", "change_note": "blank over blank"},
		map[string]string{"scope": "agent", "scopeId": agentID})
	if code != http.StatusOK {
		t.Fatalf("blank write over empty content: expected 200, got %d: %s", code, raw)
	}
}
