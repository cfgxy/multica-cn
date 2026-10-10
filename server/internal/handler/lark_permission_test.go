package handler

// Tests for the RUYI-400 capability endpoints: the static permission
// catalog behind the bind dialog's upfront declaration, and the
// recheck-permissions probe (persistence + honest tri-state + the
// revoke-style authorization).

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/integrations/lark"
)

func TestGetLarkPermissionCatalog_ServesVerifiedCatalog(t *testing.T) {
	h := &Handler{}
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/x/lark/permission-catalog", nil)
	w := httptest.NewRecorder()
	h.GetLarkPermissionCatalog(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Capabilities []struct {
			ID        string     `json:"id"`
			Probeable bool       `json:"probeable"`
			Scopes    [][]string `json:"scopes"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Capabilities) != 7 {
		t.Fatalf("expected 7 catalog entries, got %d", len(resp.Capabilities))
	}
	byID := map[string]struct {
		probeable bool
		scopes    [][]string
	}{}
	for _, c := range resp.Capabilities {
		byID[c.ID] = struct {
			probeable bool
			scopes    [][]string
		}{c.Probeable, c.Scopes}
	}
	for _, id := range []string{"receive_messages", "send_messages", "read_history", "media_resources", "contact_lookup", "drive_file_links", "wiki_doc_links"} {
		if _, ok := byID[id]; !ok {
			t.Fatalf("catalog missing %q", id)
		}
	}
	if byID["receive_messages"].probeable {
		t.Error("receive_messages must be marked not probeable (event push has no REST probe)")
	}
	if !byID["read_history"].probeable {
		t.Error("read_history must be probeable")
	}
	// RUYI-572 share-link capabilities are synthetic-REST-probeable.
	if !byID["drive_file_links"].probeable || !byID["wiki_doc_links"].probeable {
		t.Error("drive_file_links/wiki_doc_links must be probeable")
	}
	// The AND-of-OR shape must survive serialization, group-exact:
	// read_history needs any base read scope AND im:message.group_msg.
	// im:message.history:readonly grants the conversation LIST but not
	// the single-message GET, so it must NOT satisfy group 1 (RUYI-546).
	want := [][]string{
		{"im:message", "im:message:readonly"},
		{"im:message.group_msg"},
	}
	got := byID["read_history"].scopes
	if len(got) != len(want) {
		t.Fatalf("read_history scopes = %v, want %v", got, want)
	}
	for i := range want {
		if !slices.Equal(got[i], want[i]) {
			t.Fatalf("read_history group %d = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestRecheckLarkPermissions_NotConfigured(t *testing.T) {
	h := &Handler{}
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/x/lark/installations/y/recheck-permissions", nil)
	w := httptest.NewRecorder()
	h.RecheckLarkPermissions(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

// recheckStubClient fails exactly one API surface with a canned error so
// the tri-state assertions are deterministic. Only the four probed
// methods are implemented; the nil-embedded interface panics on anything
// else, which is the desired canary for unexpected fan-out.
type recheckStubClient struct {
	lark.APIClient
	getMessageErr error
}

func (s *recheckStubClient) IsConfigured() bool { return true }
func (s *recheckStubClient) GetMessage(context.Context, lark.InstallationCredentials, string) ([]lark.LarkMessage, error) {
	return nil, s.getMessageErr
}
func (s *recheckStubClient) SendTextMessage(context.Context, lark.SendTextParams) (string, error) {
	return "", nil
}
func (s *recheckStubClient) DownloadMessageResource(context.Context, lark.InstallationCredentials, lark.DownloadResourceParams) (lark.DownloadedResource, error) {
	return lark.DownloadedResource{}, nil
}
func (s *recheckStubClient) GetUserName(context.Context, lark.InstallationCredentials, string) (string, error) {
	return "", nil
}

// wireRecheckStub attaches a programmable APIClient to the shared test
// handler, restoring the previous value on cleanup. Requires
// wireLarkInstallServices to have run (it supplies LarkInstallations).
func wireRecheckStub(t *testing.T, client *recheckStubClient) {
	t.Helper()
	prev := testHandler.LarkAPIClient
	testHandler.LarkAPIClient = client
	t.Cleanup(func() { testHandler.LarkAPIClient = prev })
}

func recheckAs(userID, instID string) *httptest.ResponseRecorder {
	req := newRequestAs(userID, http.MethodPost,
		"/api/workspaces/"+testWorkspaceID+"/lark/installations/"+instID+"/recheck-permissions", nil)
	req = withURLParams(req, "id", testWorkspaceID, "installationId", instID)
	w := httptest.NewRecorder()
	testHandler.RecheckLarkPermissions(w, req)
	return w
}

func TestRecheckLarkPermissions_PersistsHonestTriState(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	wireLarkInstallServices(t)
	agentID, ownerID, memberID := privateAgentTestFixture(t)
	wireRecheckStub(t, &recheckStubClient{
		// The canonical no-permission verdict: read_history must come
		// back missing while the untouched surfaces stay granted.
		getMessageErr: &lark.APIError{Op: "get message", Code: 99991672, Msg: "no permission"},
	})

	instID, instUUID := seedActiveInstallationForRecheck(t, agentID, ownerID)

	// Unrelated plain member: forbidden (canManageAgent).
	if code := recheckAs(memberID, instID).Code; code != http.StatusForbidden {
		t.Fatalf("recheck as unrelated member: want 403, got %d", code)
	}

	w := recheckAs(ownerID, instID)
	if w.Code != http.StatusOK {
		t.Fatalf("recheck as agent owner: want 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp RecheckLarkPermissionsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Capabilities) != 7 {
		t.Fatalf("expected 7 verdicts, got %d", len(resp.Capabilities))
	}
	status := map[string]string{}
	for _, c := range resp.Capabilities {
		status[c.Capability] = c.Status
	}
	want := map[string]string{
		"receive_messages": "unknown", // never probeable over REST
		"send_messages":    "granted",
		"read_history":     "missing", // 99991672 from the stub
		"media_resources":  "granted",
		"contact_lookup":   "granted",
		// RUYI-572: the stub predates the ShareLinkClient surface, so the
		// share-link probes degrade to honest unknown instead of faking a
		// verdict — the degradation the install panel shows pre-regrant.
		"drive_file_links": "unknown",
		"wiki_doc_links":   "unknown",
	}
	for id, s := range want {
		if status[id] != s {
			t.Errorf("capability %q status = %q, want %q", id, status[id], s)
		}
	}
	// missing verdict names the scopes to add — the actionable 差集.
	for _, c := range resp.Capabilities {
		if c.Capability == "read_history" && len(c.RequiredScopes) == 0 {
			t.Error("read_history missing verdict must carry required_scopes")
		}
	}

	// Verdicts are persisted exactly once per capability (upsert, not
	// accumulate), and a second recheck overwrites in place.
	n := capabilityStateCount(t, instUUID)
	if n != 7 {
		t.Fatalf("expected 7 persisted rows, got %d", n)
	}
	if w := recheckAs(testUserID, instID); w.Code != http.StatusOK {
		t.Fatalf("second recheck as workspace owner: want 200, got %d", w.Code)
	}
	if n := capabilityStateCount(t, instUUID); n != 7 {
		t.Fatalf("recheck must upsert, not accumulate: got %d rows", n)
	}
}

func TestRecheckLarkPermissions_RevokedInstallationConflict(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	wireLarkInstallServices(t)
	agentID, ownerID, _ := privateAgentTestFixture(t)
	wireRecheckStub(t, &recheckStubClient{})

	instID, _ := seedActiveInstallationForRecheck(t, agentID, ownerID)
	if _, err := testPool.Exec(context.Background(),
		`UPDATE channel_installation SET status = 'revoked' WHERE id = $1`, instID); err != nil {
		t.Fatalf("revoke installation: %v", err)
	}

	if code := recheckAs(ownerID, instID).Code; code != http.StatusConflict {
		t.Fatalf("recheck on revoked installation: want 409, got %d", code)
	}
}

// seedActiveInstallationForRecheck inserts an ACTIVE feishu installation
// for agentID through InstallationService (so app_secret is properly
// sealed and recheck's decrypt succeeds) and cleans up after the test.
func seedActiveInstallationForRecheck(t *testing.T, agentID, installerID string) (string, pgtype.UUID) {
	t.Helper()
	agentUUID := parseUUID(agentID)
	installerUUID := parseUUID(installerID)
	wsUUID := parseUUID(testWorkspaceID)
	inst, err := testHandler.LarkInstallations.Upsert(context.Background(), lark.InstallationParams{
		WorkspaceID:     wsUUID,
		AgentID:         agentUUID,
		AppID:           "cli_recheck_test",
		AppSecret:       "recheck-test-secret",
		BotOpenID:       "ou_recheck_bot",
		InstallerUserID: installerUUID,
		Region:          lark.RegionFeishu,
	})
	if err != nil {
		t.Fatalf("seed installation: %v", err)
	}
	id := inst.ID.String()
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM channel_capability_state WHERE installation_id = $1`, inst.ID)
		testPool.Exec(context.Background(), `DELETE FROM channel_installation WHERE id = $1`, inst.ID)
	})
	return id, inst.ID
}

func capabilityStateCount(t *testing.T, instUUID pgtype.UUID) int {
	t.Helper()
	var n int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM channel_capability_state WHERE installation_id = $1`, instUUID).Scan(&n); err != nil {
		t.Fatalf("count capability states: %v", err)
	}
	return n
}
