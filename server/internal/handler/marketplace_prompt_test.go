package handler

// Publish-side tests for the prompt marketplace (RUYI-100).
//
// These drive the handlers directly rather than through the router, matching
// the rest of this suite: the routes are thin, and the interesting behaviour —
// authority re-checks, the secret gate, version freezing — lives in the
// handlers.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// promptSecretText is shaped like a GitHub token so the scanner fires. It is
// not a real credential, and the point of most assertions below is that it
// never appears in a response, a row or a log.
const promptSecretText = "ghp_abcdefghijklmnopqrstuvwxyz0123456789"

func promptTestAgent(t *testing.T, name, instructions string) string {
	t.Helper()
	return dbfx.Agent(t, name, handlerTestRuntimeID(t), map[string]any{
		"instructions": instructions,
	})
}

func promptTestSquad(t *testing.T, name, instructions string) string {
	t.Helper()
	leaderID := promptTestAgent(t, name+"-leader", "leader prompt")
	return dbfx.Squad(t, name, leaderID, map[string]any{
		"instructions": instructions,
	})
}

// callPrompt drives one handler with the marketplace flag on, applying any URL
// params the route would have bound.
func callPrompt(t *testing.T, h func(http.ResponseWriter, *http.Request), method, path string, body any, params map[string]string) (int, string) {
	t.Helper()
	withMarketplaceV1Flag(t, testHandler, true)

	req := newRequest(method, path, body)
	if len(params) > 0 {
		// withURLParams, not repeated withURLParam calls: the latter builds a
		// fresh route context each time and drops the previously set keys, so
		// a two-parameter route would only ever see the last one.
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

func decodePromptVersion(t *testing.T, raw string) PromptVersionResponse {
	t.Helper()
	var resp PromptVersionResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("decode version: %v (%s)", err, raw)
	}
	return resp
}

// createPromptDraft opens a draft from an agent and returns it.
func createPromptDraft(t *testing.T, agentID string, over map[string]any) PromptVersionResponse {
	t.Helper()
	body := map[string]any{
		"source_type":  "agent",
		"source_id":    agentID,
		"name":         "Careful Reviewer",
		"summary":      "A review prompt",
		"license_code": "cc0",
	}
	for k, v := range over {
		body[k] = v
	}
	code, raw := callPrompt(t, testHandler.CreatePromptVersion, http.MethodPost, "/api/marketplace/prompt-versions", body, nil)
	if code != http.StatusCreated {
		t.Fatalf("create draft: expected 201, got %d: %s", code, raw)
	}
	version := decodePromptVersion(t, raw)
	dbfx.Cleanup(t, `DELETE FROM marketplace_prompt_version WHERE series_id = $1`, version.SeriesID)
	return version
}

// publishPromptDraft publishes a draft publicly and returns the frozen version.
func publishPromptDraft(t *testing.T, versionID string) PromptVersionResponse {
	t.Helper()
	code, raw := callPrompt(t, testHandler.PublishPromptVersion, http.MethodPost,
		"/api/marketplace/prompt-versions/"+versionID+"/publish",
		map[string]any{"public": true}, map[string]string{"id": versionID})
	if code != http.StatusOK {
		t.Fatalf("publish: expected 200, got %d: %s", code, raw)
	}
	return decodePromptVersion(t, raw)
}

// publishedPrompt is the common setup: an agent with a prompt, published as v1.
func publishedPrompt(t *testing.T, name, instructions string) (agentID string, version PromptVersionResponse) {
	t.Helper()
	agentID = promptTestAgent(t, name, instructions)
	draft := createPromptDraft(t, agentID, nil)
	return agentID, publishPromptDraft(t, draft.ID)
}

// ── A1: publishing snapshots the source object's own text ────────────────────

func TestCreatePromptDraftSnapshotsSourceText(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	const instructions = "You are a careful reviewer. Always cite file and line."
	agentID := promptTestAgent(t, "prompt-draft-source", instructions)

	draft := createPromptDraft(t, agentID, nil)
	if draft.Content != instructions {
		t.Fatalf("draft content = %q, want the agent's own instructions", draft.Content)
	}
	if draft.State != promptStateDraft {
		t.Errorf("state = %q, want draft", draft.State)
	}
	// D1: a draft is private until an explicit publish.
	if draft.Visibility != promptVisibilityPrivate {
		t.Errorf("visibility = %q, want private — a draft must not be discoverable", draft.Visibility)
	}
	if draft.Version != nil {
		t.Errorf("version = %v, want nil until published", *draft.Version)
	}
}

// A client cannot supply prompt text: the server reads it from the source
// object, so a publish can never attach text the publisher does not control.
func TestCreatePromptDraftIgnoresClientSuppliedContent(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	const instructions = "the real prompt"
	agentID := promptTestAgent(t, "prompt-draft-nocontent", instructions)

	draft := createPromptDraft(t, agentID, map[string]any{
		"content": "text the client tried to inject",
	})
	if draft.Content != instructions {
		t.Fatalf("content = %q, want the source object's text", draft.Content)
	}
}

func TestCreatePromptDraftRejectsUnmanagedSource(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	otherUser := dbfx.User(t, "Prompt Outsider", "prompt-outsider@multica.ai")
	agentID := promptTestAgent(t, "prompt-draft-foreign", "private prompt")
	// Re-own the agent so the caller manages neither it nor the workspace role
	// that would otherwise cover it.
	dbfx.Exec(t, `UPDATE agent SET owner_id = $1, visibility = 'private' WHERE id = $2`, otherUser, agentID)
	dbfx.Exec(t, `UPDATE member SET role = 'member' WHERE workspace_id = $1 AND user_id = $2`,
		testWorkspaceID, testUserID)
	// Restore through the fixture rather than t.Cleanup + t.Context: the test
	// context is already cancelled by the time cleanups run, so a demotion
	// restored that way silently leaks into every later test in the package.
	dbfx.Cleanup(t, `UPDATE member SET role = 'owner' WHERE workspace_id = $1 AND user_id = $2`,
		testWorkspaceID, testUserID)

	code, raw := callPrompt(t, testHandler.CreatePromptVersion, http.MethodPost, "/api/marketplace/prompt-versions",
		map[string]any{
			"source_type": "agent", "source_id": agentID,
			"name": "Stolen", "license_code": "cc0",
		}, nil)
	if code != http.StatusForbidden && code != http.StatusNotFound {
		t.Fatalf("expected the publish to be refused, got %d: %s", code, raw)
	}
}

// ── A2 / D2: metadata and the license enum ──────────────────────────────────

func TestCreatePromptDraftRequiresLicense(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := promptTestAgent(t, "prompt-license", "some prompt")

	for _, tc := range []struct {
		name    string
		license any
	}{
		{"missing", nil},
		{"empty", ""},
		{"unknown", "mit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := map[string]any{"source_type": "agent", "source_id": agentID, "name": "X"}
			if tc.license != nil {
				body["license_code"] = tc.license
			}
			code, raw := callPrompt(t, testHandler.CreatePromptVersion, http.MethodPost,
				"/api/marketplace/prompt-versions", body, nil)
			if code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", code, raw)
			}
		})
	}
}

func TestCreatePromptDraftRequiresName(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := promptTestAgent(t, "prompt-noname", "some prompt")
	code, raw := callPrompt(t, testHandler.CreatePromptVersion, http.MethodPost,
		"/api/marketplace/prompt-versions",
		map[string]any{"source_type": "agent", "source_id": agentID, "license_code": "cc0"}, nil)
	if code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", code, raw)
	}
}

// ── A3: the secret gate is fail-closed and leaks nothing ────────────────────

func TestPublishBlockedWhenPromptCarriesSecret(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := promptTestAgent(t, "prompt-secret", "Use "+promptSecretText+" to call the API.")
	draft := createPromptDraft(t, agentID, nil)

	code, raw := callPrompt(t, testHandler.PublishPromptVersion, http.MethodPost,
		"/api/marketplace/prompt-versions/"+draft.ID+"/publish",
		map[string]any{"public": true}, map[string]string{"id": draft.ID})

	if code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %s", code, raw)
	}
	// The response must name what fired and where, and carry none of the match.
	if !strings.Contains(raw, "prompt_secret_detected") {
		t.Errorf("response carries no machine-readable code: %s", raw)
	}
	if strings.Contains(raw, promptSecretText) {
		t.Fatal("the 422 body echoed the secret")
	}
	if strings.Contains(raw, promptSecretText[:8]) {
		t.Fatalf("the 422 body echoed a prefix of the secret: %s", raw)
	}

	// Fail-closed means the row did not go public.
	var state, visibility string
	dbfx.QueryRow(t, `SELECT state, visibility FROM marketplace_prompt_version WHERE id = $1`, draft.ID).
		Scan(&state, &visibility)
	if state != promptStateDraft {
		t.Errorf("state = %q, want the draft to stay unpublished", state)
	}
	if visibility == promptVisibilityPublic {
		t.Error("a blocked publish made the version public")
	}
}

// The gate covers every free-text field, not just the prompt body: a credential
// pasted into usage notes reaches the catalog just as surely.
func TestPublishBlockedWhenMetadataCarriesSecret(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := promptTestAgent(t, "prompt-secret-meta", "a clean prompt")
	draft := createPromptDraft(t, agentID, map[string]any{
		"usage_notes": "authenticate with " + promptSecretText,
	})

	code, raw := callPrompt(t, testHandler.PublishPromptVersion, http.MethodPost,
		"/api/marketplace/prompt-versions/"+draft.ID+"/publish",
		map[string]any{"public": true}, map[string]string{"id": draft.ID})
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for a secret in metadata, got %d: %s", code, raw)
	}
	if strings.Contains(raw, promptSecretText) {
		t.Fatal("the 422 body echoed the secret")
	}
}

// There is no override: the gate has no "publish anyway" parameter, and one
// would guarantee a credential eventually reaches the catalog.
func TestPublishSecretGateHasNoOverride(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := promptTestAgent(t, "prompt-secret-override", "token "+promptSecretText)
	draft := createPromptDraft(t, agentID, nil)

	for _, body := range []map[string]any{
		{"public": true, "force": true},
		{"public": true, "ignore_findings": true},
		{"public": true, "acknowledge_secrets": true},
	} {
		code, raw := callPrompt(t, testHandler.PublishPromptVersion, http.MethodPost,
			"/api/marketplace/prompt-versions/"+draft.ID+"/publish", body,
			map[string]string{"id": draft.ID})
		if code != http.StatusUnprocessableEntity {
			t.Fatalf("override %v bypassed the gate: %d %s", body, code, raw)
		}
	}
}

// ── A4: publishing freezes an immutable version ─────────────────────────────

func TestPublishFreezesVersionAndAssignsNumber(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	const instructions = "v1 of the prompt"
	agentID, published := publishedPrompt(t, "prompt-freeze", instructions)

	if published.State != promptStatePublished {
		t.Fatalf("state = %q, want published", published.State)
	}
	if published.Version == nil || *published.Version != 1 {
		t.Fatalf("version = %v, want 1", published.Version)
	}
	if published.Content != instructions {
		t.Errorf("content = %q, want the published snapshot", published.Content)
	}

	// Editing the agent afterwards must not rewrite the published snapshot:
	// that is what "immutable" has to mean for a consumer who installed it.
	dbfx.Exec(t, `UPDATE agent SET instructions = $1 WHERE id = $2`, "edited after publish", agentID)

	code, raw := callPrompt(t, testHandler.GetPromptVersion, http.MethodGet,
		"/api/marketplace/prompt-versions/"+published.ID, nil, map[string]string{"id": published.ID})
	if code != http.StatusOK {
		t.Fatalf("read back: expected 200, got %d: %s", code, raw)
	}
	if got := decodePromptVersion(t, raw); got.Content != instructions {
		t.Fatalf("published content changed to %q after the source was edited", got.Content)
	}
}

func TestPublishedVersionCannotBeEdited(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	_, published := publishedPrompt(t, "prompt-immutable", "frozen text")

	code, raw := callPrompt(t, testHandler.UpdatePromptVersion, http.MethodPut,
		"/api/marketplace/prompt-versions/"+published.ID,
		map[string]any{"name": "Renamed", "license_code": "cc0"},
		map[string]string{"id": published.ID})
	if code != http.StatusConflict {
		t.Fatalf("expected 409 editing a published version, got %d: %s", code, raw)
	}
}

// A second publication of the same asset becomes v2 in the same series, which
// is what makes an update offer possible for consumers.
func TestSecondPublishIncrementsVersionInSeries(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID, v1 := publishedPrompt(t, "prompt-v2", "first text")

	dbfx.Exec(t, `UPDATE agent SET instructions = $1 WHERE id = $2`, "second text", agentID)
	draft2 := createPromptDraft(t, agentID, map[string]any{"series_id": v1.SeriesID})
	v2 := publishPromptDraft(t, draft2.ID)

	if v2.SeriesID != v1.SeriesID {
		t.Fatalf("series changed: %s -> %s", v1.SeriesID, v2.SeriesID)
	}
	if v2.Version == nil || *v2.Version != 2 {
		t.Fatalf("version = %v, want 2", v2.Version)
	}
	if v2.Content != "second text" {
		t.Errorf("content = %q, want the new snapshot", v2.Content)
	}
}

// ── D4: attribution never names the source workspace ────────────────────────

func TestPublishedVersionNeverExposesSourceWorkspace(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	_, published := publishedPrompt(t, "prompt-attribution", "attributed prompt")

	if published.PublisherDisplayName != handlerTestName {
		t.Errorf("publisher = %q, want the publisher's display name %q",
			published.PublisherDisplayName, handlerTestName)
	}

	code, raw := callPrompt(t, testHandler.ListPromptVersions, http.MethodGet,
		"/api/marketplace/prompt-versions", nil, nil)
	if code != http.StatusOK {
		t.Fatalf("discovery: expected 200, got %d: %s", code, raw)
	}
	// The source workspace id must not travel on any prompt-market surface.
	if strings.Contains(raw, testWorkspaceID) {
		t.Fatalf("the catalog leaked the source workspace id: %s", raw)
	}
	if strings.Contains(raw, "source_workspace") {
		t.Fatalf("the catalog carries a source_workspace field: %s", raw)
	}
}

// ── A5: discovery shows published public versions only ──────────────────────

func TestDiscoveryExcludesDraftsAndWithdrawn(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	draftAgent := promptTestAgent(t, "prompt-discovery-draft", "draft only prompt")
	draft := createPromptDraft(t, draftAgent, map[string]any{"name": "DraftOnlyAsset"})

	_, published := publishedPrompt(t, "prompt-discovery-live", "live prompt")

	code, raw := callPrompt(t, testHandler.ListPromptVersions, http.MethodGet,
		"/api/marketplace/prompt-versions", nil, nil)
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", code, raw)
	}
	if strings.Contains(raw, draft.ID) {
		t.Error("discovery surfaced a draft")
	}
	if !strings.Contains(raw, published.ID) {
		t.Error("discovery omitted a published public version")
	}

	// Withdrawing removes it from discovery.
	code, raw = callPrompt(t, testHandler.WithdrawPromptVersion, http.MethodPost,
		"/api/marketplace/prompt-versions/"+published.ID+"/withdraw", nil,
		map[string]string{"id": published.ID})
	if code != http.StatusOK {
		t.Fatalf("withdraw: expected 200, got %d: %s", code, raw)
	}
	code, raw = callPrompt(t, testHandler.ListPromptVersions, http.MethodGet,
		"/api/marketplace/prompt-versions", nil, nil)
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", code, raw)
	}
	if strings.Contains(raw, published.ID) {
		t.Error("discovery still surfaces a withdrawn version")
	}
}

// The discovery listing carries metadata, not prompt bodies: it is the one
// surface that fans out across every workspace, so it carries the least it can.
func TestDiscoveryOmitsPromptBody(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	const body = "the-distinctive-prompt-body-marker"
	publishedPrompt(t, "prompt-listing-body", body)

	code, raw := callPrompt(t, testHandler.ListPromptVersions, http.MethodGet,
		"/api/marketplace/prompt-versions", nil, nil)
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", code, raw)
	}
	if strings.Contains(raw, body) {
		t.Fatal("the discovery listing carried the prompt body")
	}
}

func TestDiscoveryFiltersByKind(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	_, agentVersion := publishedPrompt(t, "prompt-kind-agent", "agent kind prompt")

	code, raw := callPrompt(t, testHandler.ListPromptVersions, http.MethodGet,
		"/api/marketplace/prompt-versions?kind=squad_prompt", nil, nil)
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", code, raw)
	}
	if strings.Contains(raw, agentVersion.ID) {
		t.Error("kind=squad_prompt returned an agent prompt")
	}

	code, raw = callPrompt(t, testHandler.ListPromptVersions, http.MethodGet,
		"/api/marketplace/prompt-versions?kind=nonsense", nil, nil)
	if code != http.StatusBadRequest {
		t.Fatalf("expected 400 for an unknown kind, got %d: %s", code, raw)
	}
}

// ── S1: squad prompts are first-class ───────────────────────────────────────

func TestSquadPromptPublishesAndFreezes(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	const instructions = "Route work to the right specialist."
	squadID := promptTestSquad(t, "prompt-squad", instructions)

	code, raw := callPrompt(t, testHandler.CreatePromptVersion, http.MethodPost,
		"/api/marketplace/prompt-versions", map[string]any{
			"source_type": "squad", "source_id": squadID,
			"name": "Squad Router", "license_code": "cc-by-4.0",
		}, nil)
	if code != http.StatusCreated {
		t.Fatalf("squad draft: expected 201, got %d: %s", code, raw)
	}
	draft := decodePromptVersion(t, raw)
	dbfx.Cleanup(t, `DELETE FROM marketplace_prompt_version WHERE series_id = $1`, draft.SeriesID)
	if draft.Kind != promptKindSquad {
		t.Fatalf("kind = %q, want %q", draft.Kind, promptKindSquad)
	}
	if draft.Content != instructions {
		t.Fatalf("content = %q, want the squad's instructions", draft.Content)
	}

	published := publishPromptDraft(t, draft.ID)
	if published.Version == nil || *published.Version != 1 {
		t.Fatalf("version = %v, want 1", published.Version)
	}
}

// ── A6: withdrawal ──────────────────────────────────────────────────────────

func TestWithdrawnVersionCannotBeInstalled(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	_, published := publishedPrompt(t, "prompt-withdraw-install", "withdrawn prompt")

	code, raw := callPrompt(t, testHandler.WithdrawPromptVersion, http.MethodPost,
		"/api/marketplace/prompt-versions/"+published.ID+"/withdraw", nil,
		map[string]string{"id": published.ID})
	if code != http.StatusOK {
		t.Fatalf("withdraw: expected 200, got %d: %s", code, raw)
	}

	code, raw = callPrompt(t, testHandler.InstallPrompt, http.MethodPost,
		"/api/marketplace/prompt-installations",
		map[string]any{"version_id": published.ID}, nil)
	if code != http.StatusNotFound {
		t.Fatalf("expected a withdrawn version to be uninstallable, got %d: %s", code, raw)
	}
}

// ── P-series: the flag closes every prompt route ────────────────────────────

func TestPromptRoutesClosedWhenFlagOff(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := promptTestAgent(t, "prompt-flag-off", "a prompt")
	withMarketplaceV1Flag(t, testHandler, false)

	cases := []struct {
		name   string
		invoke func() *httptest.ResponseRecorder
	}{
		{"list", func() *httptest.ResponseRecorder {
			w := httptest.NewRecorder()
			testHandler.ListPromptVersions(w, newRequest(http.MethodGet, "/api/marketplace/prompt-versions", nil))
			return w
		}},
		{"create", func() *httptest.ResponseRecorder {
			w := httptest.NewRecorder()
			testHandler.CreatePromptVersion(w, newRequest(http.MethodPost, "/api/marketplace/prompt-versions",
				map[string]any{"source_type": "agent", "source_id": agentID, "name": "X", "license_code": "cc0"}))
			return w
		}},
		{"install", func() *httptest.ResponseRecorder {
			w := httptest.NewRecorder()
			testHandler.InstallPrompt(w, newRequest(http.MethodPost, "/api/marketplace/prompt-installations",
				map[string]any{"version_id": agentID}))
			return w
		}},
		{"installs", func() *httptest.ResponseRecorder {
			w := httptest.NewRecorder()
			testHandler.ListPromptInstalls(w, newRequest(http.MethodGet, "/api/marketplace/prompt-installations", nil))
			return w
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := tc.invoke()
			if w.Code != http.StatusServiceUnavailable {
				t.Fatalf("expected 503 with the flag off, got %d: %s", w.Code, w.Body.String())
			}
		})
	}

	// The capability the marketplace merely drives stays reachable: the agent's
	// own prompt is untouched and still editable by the existing entry point.
	var instructions string
	dbfx.QueryRow(t, `SELECT instructions FROM agent WHERE id = $1`, agentID).Scan(&instructions)
	if instructions != "a prompt" {
		t.Fatalf("instructions = %q; turning the flag off must not disturb existing prompts", instructions)
	}
}
