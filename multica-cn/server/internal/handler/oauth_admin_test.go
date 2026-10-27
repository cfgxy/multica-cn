package handler

// RUYI-420 handler tests: the OAuth management surface's security
// contracts, driven against the shared test database like the rest of the
// package (X-User-ID header + chi URL params; RequireSuperAdmin itself is
// middleware-covered). The contracts under test here are the ones the
// dispatch card calls out by name:
//   - the plaintext client secret appears exactly once, in the create and
//     rotate responses, and never again in any list/detail body;
//   - rotate replaces the verifiable hash in place (old secret stops
//     authenticating, no grace period);
//   - disable and delete revoke the client's live grants and audit rows
//     record the action, never the secret;
//   - the "my authorizations" endpoints are strictly owner-scoped, with
//     foreign ids answering 404 so grant existence does not leak.

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/testutil"
)

// createOAuthClientViaAPI drives POST /api/admin/oauth/clients as adminID
// and returns the row id, the public client_id and the one-shot plaintext
// secret. Cleanup removes the client's grant rows and the client row; the
// audit rows die with the admin fixture.
func createOAuthClientViaAPI(t *testing.T, adminID, name string) (rowID, clientID, secret string) {
	t.Helper()
	req := newRequest("POST", "/api/admin/oauth/clients", map[string]any{
		"name":          name,
		"redirect_uris": []string{"https://chatgpt.example.com/callback"},
	})
	req.Header.Set("X-User-ID", adminID)
	var created struct {
		Client struct {
			ID       string `json:"id"`
			ClientID string `json:"client_id"`
		} `json:"client"`
		Secret string `json:"secret"`
	}
	testutil.Call(t, testHandler.AdminCreateOAuthClient, req).Want(http.StatusCreated).JSON(&created)
	if created.Secret == "" || created.Client.ClientID == "" || created.Client.ID == "" {
		t.Fatalf("create response missing id/client_id/secret: %+v", created)
	}
	rowID, clientID, secret = created.Client.ID, created.Client.ClientID, created.Secret
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(),
			`DELETE FROM oauth_grant WHERE client_id = $1`, clientID)
		_, _ = testPool.Exec(context.Background(),
			`DELETE FROM oauth_client WHERE id = $1`, rowID)
	})
	return rowID, clientID, secret
}

// insertOAuthGrant inserts one oauth_grant row and returns its id.
func insertOAuthGrant(t *testing.T, clientID, userID, scope string, revokedAt *time.Time) string {
	t.Helper()
	cols := testutil.Cols{
		"client_id": clientID,
		"user_id":   userID,
		"scope":     scope,
	}
	if revokedAt != nil {
		cols["revoked_at"] = *revokedAt
	}
	return dbfx.Insert(t, "oauth_grant", cols)
}

func grantRevokedAt(t *testing.T, grantID string) *time.Time {
	t.Helper()
	var at *time.Time
	dbfx.QueryRow(t, `SELECT revoked_at FROM oauth_grant WHERE id = $1`, grantID).Scan(&at)
	return at
}

func TestAdminOAuthClientSecretAppearsExactlyOnce(t *testing.T) {
	admin := insertAdminTestUser(t, "oauth-secret-once@test.local", "OAuth Secret Once", true)
	rowID, _, oldSecret := createOAuthClientViaAPI(t, admin, "Secret Once Client")

	// List and detail must never carry the plaintext or the hash key.
	var list struct {
		Clients []struct {
			ID     string `json:"id"`
			Secret string `json:"secret"`
		} `json:"clients"`
	}
	listReq := newRequest("GET", "/api/admin/oauth/clients", nil)
	listReq.Header.Set("X-User-ID", admin)
	listResp := testutil.Call(t, testHandler.AdminListOAuthClients, listReq).Want(http.StatusOK)
	listResp.JSON(&list)
	if body := listResp.Text(); strings.Contains(body, oldSecret) || strings.Contains(body, "client_secret_hash") {
		t.Fatal("client list leaks the plaintext secret or the hash key")
	}

	detailReq := newRequest("GET", "/api/admin/oauth/clients/"+rowID, nil)
	detailReq.Header.Set("X-User-ID", admin)
	detailReq = withURLParam(detailReq, "id", rowID)
	detailResp := testutil.Call(t, testHandler.AdminGetOAuthClient, detailReq).Want(http.StatusOK)
	if body := detailResp.Text(); strings.Contains(body, oldSecret) || strings.Contains(body, "client_secret_hash") {
		t.Fatal("client detail leaks the plaintext secret or the hash key")
	}

	// Update (rename) is likewise silent about secrets.
	patchReq := newRequest("PATCH", "/api/admin/oauth/clients/"+rowID, map[string]any{
		"name":          "Secret Once Client Renamed",
		"redirect_uris": []string{"https://chatgpt.example.com/callback"},
	})
	patchReq.Header.Set("X-User-ID", admin)
	patchReq = withURLParam(patchReq, "id", rowID)
	patchResp := testutil.Call(t, testHandler.AdminUpdateOAuthClient, patchReq).Want(http.StatusOK)
	if body := patchResp.Text(); strings.Contains(body, oldSecret) {
		t.Fatal("client update leaks the plaintext secret")
	}

	// Rotate answers the NEW plaintext exactly once; the old one is dead.
	rotateReq := newRequest("POST", "/api/admin/oauth/clients/"+rowID+"/rotate", nil)
	rotateReq.Header.Set("X-User-ID", admin)
	rotateReq = withURLParam(rotateReq, "id", rowID)
	rotateResp := testutil.Call(t, testHandler.AdminRotateOAuthClientSecret, rotateReq).Want(http.StatusOK)
	var rotated struct {
		Secret string `json:"secret"`
	}
	rotateResp.JSON(&rotated)
	if rotated.Secret == "" || rotated.Secret == oldSecret {
		t.Fatalf("rotate response secret = %q, want a fresh value distinct from the create secret", rotated.Secret)
	}
	if body := listResp.Text(); strings.Contains(body, rotated.Secret) {
		t.Fatal("list response (fetched before rotate) unexpectedly knows the new secret")
	}
}

func TestAdminRotateReplacesTheVerifiableHashInPlace(t *testing.T) {
	admin := insertAdminTestUser(t, "oauth-rotate-hash@test.local", "OAuth Rotate Hash", true)
	rowID, _, oldSecret := createOAuthClientViaAPI(t, admin, "Rotate Hash Client")

	rotateReq := newRequest("POST", "/api/admin/oauth/clients/"+rowID+"/rotate", nil)
	rotateReq.Header.Set("X-User-ID", admin)
	rotateReq = withURLParam(rotateReq, "id", rowID)
	var rotated struct {
		Secret string `json:"secret"`
	}
	testutil.Call(t, testHandler.AdminRotateOAuthClientSecret, rotateReq).Want(http.StatusOK).JSON(&rotated)

	var storedHash string
	dbfx.QueryRow(t, `SELECT client_secret_hash FROM oauth_client WHERE id = $1`, rowID).Scan(&storedHash)
	if storedHash == auth.HashToken(oldSecret) {
		t.Fatal("rotate left the create-time hash in place; the old secret would still authenticate")
	}
	if storedHash != auth.HashToken(rotated.Secret) {
		t.Fatal("rotate stored a hash that does not verify the returned new secret")
	}

	// Audit records THAT the rotation happened, never the secret material.
	countReq := countAdminAuditRows(t, AuditActionOAuthClientRotate, admin, rowID)
	if countReq != 1 {
		t.Fatalf("oauth_client.rotate audit rows = %d, want 1", countReq)
	}
}

func TestAdminDisableClientRevokesLiveGrantsAndAudits(t *testing.T) {
	admin := insertAdminTestUser(t, "oauth-disable@test.local", "OAuth Disable", true)
	rowID, clientID, _ := createOAuthClientViaAPI(t, admin, "Disable Client")
	userA := insertAdminTestUser(t, "oauth-disable-a@test.local", "Disable User A", false)
	userB := insertAdminTestUser(t, "oauth-disable-b@test.local", "Disable User B", false)
	userC := insertAdminTestUser(t, "oauth-disable-c@test.local", "Disable User C", false)

	liveA := insertOAuthGrant(t, clientID, userA, "mcp:read mcp:write", nil)
	liveB := insertOAuthGrant(t, clientID, userB, "mcp", nil)
	// One grant per (client, user), so the already-revoked row belongs to a
	// third user. Truncated to microseconds: that is timestamptz storage
	// precision, and the preserved-value comparison below reads back what
	// the column stored.
	preRevokedAt := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	preRevoked := insertOAuthGrant(t, clientID, userC, "mcp:read", &preRevokedAt)

	disableReq := newRequest("PATCH", "/api/admin/oauth/clients/"+rowID+"/disabled", map[string]any{
		"disabled": true,
		"reason":   "compromised",
	})
	disableReq.Header.Set("X-User-ID", admin)
	disableReq = withURLParam(disableReq, "id", rowID)
	testutil.Call(t, testHandler.AdminSetOAuthClientDisabled, disableReq).Want(http.StatusOK)

	var disabledAt *time.Time
	dbfx.QueryRow(t, `SELECT disabled_at FROM oauth_client WHERE id = $1`, rowID).Scan(&disabledAt)
	if disabledAt == nil {
		t.Fatal("disable left disabled_at NULL")
	}
	if at := grantRevokedAt(t, liveA); at == nil {
		t.Fatal("disable left user A's live grant unrevoked")
	}
	if at := grantRevokedAt(t, liveB); at == nil {
		t.Fatal("disable left user B's live grant unrevoked")
	}
	if at := grantRevokedAt(t, preRevoked); at == nil || !at.Equal(preRevokedAt) {
		t.Fatalf("disable disturbed an already-revoked grant: got %v, want preserved %v", at, preRevokedAt)
	}
	if n := countAdminAuditRows(t, AuditActionOAuthClientDisable, admin, rowID); n != 1 {
		t.Fatalf("oauth_client.disable audit rows = %d, want 1", n)
	}

	// Re-enable clears the soft-disable state (revoked grants stay revoked:
	// users must re-consent, they are not silently un-revoked).
	enableReq := newRequest("PATCH", "/api/admin/oauth/clients/"+rowID+"/disabled", map[string]any{
		"disabled": false,
	})
	enableReq.Header.Set("X-User-ID", admin)
	enableReq = withURLParam(enableReq, "id", rowID)
	testutil.Call(t, testHandler.AdminSetOAuthClientDisabled, enableReq).Want(http.StatusOK)
	dbfx.QueryRow(t, `SELECT disabled_at FROM oauth_client WHERE id = $1`, rowID).Scan(&disabledAt)
	if disabledAt != nil {
		t.Fatal("enable left disabled_at set")
	}
	if n := countAdminAuditRows(t, AuditActionOAuthClientEnable, admin, rowID); n != 1 {
		t.Fatalf("oauth_client.enable audit rows = %d, want 1", n)
	}
}

func TestAdminDeleteClientRevokesGrantsButKeepsTheirRows(t *testing.T) {
	admin := insertAdminTestUser(t, "oauth-delete@test.local", "OAuth Delete", true)
	rowID, clientID, _ := createOAuthClientViaAPI(t, admin, "Delete Client")
	userA := insertAdminTestUser(t, "oauth-delete-a@test.local", "Delete User A", false)
	grantID := insertOAuthGrant(t, clientID, userA, "mcp:read", nil)

	deleteReq := newRequest("DELETE", "/api/admin/oauth/clients/"+rowID, nil)
	deleteReq.Header.Set("X-User-ID", admin)
	deleteReq = withURLParam(deleteReq, "id", rowID)
	testutil.Call(t, testHandler.AdminDeleteOAuthClient, deleteReq).Want(http.StatusOK)

	var clients int64
	dbfx.QueryRow(t, `SELECT count(*) FROM oauth_client WHERE id = $1`, rowID).Scan(&clients)
	if clients != 0 {
		t.Fatal("delete left the client row behind")
	}
	// The grant row survives (revoked, not deleted) so the user's
	// "my authorizations" history and the audit trail outlive the client.
	var grants int64
	dbfx.QueryRow(t, `SELECT count(*) FROM oauth_grant WHERE id = $1`, grantID).Scan(&grants)
	if grants != 1 {
		t.Fatal("delete removed the grant row; history must be kept, only revoked")
	}
	if at := grantRevokedAt(t, grantID); at == nil {
		t.Fatal("delete left the client's grant live")
	}
	var revokedMeta string
	dbfx.QueryRow(t,
		`SELECT metadata->>'grants_revoked' FROM admin_audit_log
		 WHERE action = $1 AND target_id::text = $2`,
		AuditActionOAuthClientDelete, rowID).Scan(&revokedMeta)
	if revokedMeta != "1" {
		t.Fatalf("delete audit grants_revoked = %q, want %q", revokedMeta, "1")
	}
}

func TestAdminRevokeGrantIsIdempotentAndAudited(t *testing.T) {
	admin := insertAdminTestUser(t, "oauth-grant-revoke@test.local", "OAuth Grant Revoke", true)
	_, clientID, _ := createOAuthClientViaAPI(t, admin, "Grant Revoke Client")
	userA := insertAdminTestUser(t, "oauth-grant-a@test.local", "Grant User A", false)
	grantID := insertOAuthGrant(t, clientID, userA, "mcp:read", nil)

	req := newRequest("DELETE", "/api/admin/oauth/grants/"+grantID, nil)
	req.Header.Set("X-User-ID", admin)
	req = withURLParam(req, "id", grantID)
	testutil.Call(t, testHandler.AdminRevokeOAuthGrant, req).Want(http.StatusOK)

	first := grantRevokedAt(t, grantID)
	if first == nil {
		t.Fatal("revoke left revoked_at NULL")
	}
	if n := countAdminAuditRows(t, AuditActionOAuthGrantRevoke, admin, grantID); n != 1 {
		t.Fatalf("oauth_grant.revoke audit rows = %d, want 1", n)
	}

	// Revoking again is a no-op success that keeps the original revoked_at.
	req2 := newRequest("DELETE", "/api/admin/oauth/grants/"+grantID, nil)
	req2.Header.Set("X-User-ID", admin)
	req2 = withURLParam(req2, "id", grantID)
	testutil.Call(t, testHandler.AdminRevokeOAuthGrant, req2).Want(http.StatusOK)
	if again := grantRevokedAt(t, grantID); !again.Equal(*first) {
		t.Fatalf("second revoke moved revoked_at: %v -> %v", first, again)
	}
	if n := countAdminAuditRows(t, AuditActionOAuthGrantRevoke, admin, grantID); n != 1 {
		t.Fatalf("idempotent re-revoke wrote another audit row: %d, want 1", n)
	}

	// Unknown id: 404.
	unknown := newRequest("DELETE", "/api/admin/oauth/grants/"+uuid.NewString(), nil)
	unknown.Header.Set("X-User-ID", admin)
	unknown = withURLParam(unknown, "id", uuid.NewString())
	testutil.Call(t, testHandler.AdminRevokeOAuthGrant, unknown).Want(http.StatusNotFound)
}

func TestMyOAuthGrantsAreScopedToTheirOwner(t *testing.T) {
	admin := insertAdminTestUser(t, "oauth-mine-admin@test.local", "OAuth Mine Admin", true)
	userA := insertAdminTestUser(t, "oauth-mine-a@test.local", "OAuth Mine A", false)
	userB := insertAdminTestUser(t, "oauth-mine-b@test.local", "OAuth Mine B", false)
	_, client1, _ := createOAuthClientViaAPI(t, admin, "Mine Client One")
	_, client2, _ := createOAuthClientViaAPI(t, admin, "Mine Client Two")

	grantA1 := insertOAuthGrant(t, client1, userA, "mcp:read", nil)
	grantA2 := insertOAuthGrant(t, client2, userA, "mcp:read mcp:write", nil)
	grantB1 := insertOAuthGrant(t, client1, userB, "mcp:run", nil)

	listReq := newRequest("GET", "/api/oauth/grants", nil)
	listReq.Header.Set("X-User-ID", userA)
	var mine struct {
		Grants []struct {
			ID         string  `json:"id"`
			ClientName *string `json:"client_name"`
			Scope      string  `json:"scope"`
		} `json:"grants"`
	}
	testutil.Call(t, testHandler.ListMyOAuthGrants, listReq).Want(http.StatusOK).JSON(&mine)
	seen := map[string]string{}
	for _, g := range mine.Grants {
		seen[g.ID] = g.Scope
	}
	if len(seen) != 2 || seen[grantA1] != "mcp:read" || seen[grantA2] != "mcp:read mcp:write" {
		t.Fatalf("my grants = %v, want exactly the two owned by user A", seen)
	}
	if _, foreign := seen[grantB1]; foreign {
		t.Fatal("my grants list leaked another user's grant")
	}

	// Revoking a foreign id answers 404 (not 403 — existence must not leak)
	// and leaves the grant untouched.
	foreignReq := newRequest("DELETE", "/api/oauth/grants/"+grantB1, nil)
	foreignReq.Header.Set("X-User-ID", userA)
	foreignReq = withURLParam(foreignReq, "id", grantB1)
	testutil.Call(t, testHandler.RevokeMyOAuthGrant, foreignReq).Want(http.StatusNotFound)
	if at := grantRevokedAt(t, grantB1); at != nil {
		t.Fatal("a foreign revoke modified the grant")
	}

	// Own grant: revoke, then re-revoke idempotently.
	ownReq := newRequest("DELETE", "/api/oauth/grants/"+grantA1, nil)
	ownReq.Header.Set("X-User-ID", userA)
	ownReq = withURLParam(ownReq, "id", grantA1)
	testutil.Call(t, testHandler.RevokeMyOAuthGrant, ownReq).Want(http.StatusOK)
	first := grantRevokedAt(t, grantA1)
	if first == nil {
		t.Fatal("owner revoke left revoked_at NULL")
	}
	ownReq2 := newRequest("DELETE", "/api/oauth/grants/"+grantA1, nil)
	ownReq2.Header.Set("X-User-ID", userA)
	ownReq2 = withURLParam(ownReq2, "id", grantA1)
	testutil.Call(t, testHandler.RevokeMyOAuthGrant, ownReq2).Want(http.StatusOK)
	if again := grantRevokedAt(t, grantA1); !again.Equal(*first) {
		t.Fatalf("idempotent re-revoke moved revoked_at: %v -> %v", first, again)
	}
}

func TestAdminMCPServerStatusDegradesWithoutDependencies(t *testing.T) {
	admin := insertAdminTestUser(t, "oauth-status@test.local", "OAuth Status", true)

	// Without MCP_URL: the process half reports unconfigured, the page
	// still answers 200.
	t.Setenv("MCP_URL", "")
	req := newRequest("GET", "/api/admin/mcp/status", nil)
	req.Header.Set("X-User-ID", admin)
	var status struct {
		OAuth struct {
			Enabled bool   `json:"enabled"`
			Issuer  string `json:"issuer"`
		} `json:"oauth"`
		MCP struct {
			URLConfigured bool   `json:"url_configured"`
			Reachable     bool   `json:"reachable"`
			Version       string `json:"version"`
			ToolCount     int64  `json:"tool_count"`
			Tools         []any  `json:"tools"`
		} `json:"mcp"`
	}
	testutil.Call(t, testHandler.AdminMCPServerStatus, req).Want(http.StatusOK).JSON(&status)
	if status.MCP.URLConfigured || status.MCP.Reachable || len(status.MCP.Tools) != 0 {
		t.Fatalf("unconfigured MCP_URL reported as %+v, want unconfigured and unreachable", status.MCP)
	}

	// With a dead MCP_URL: configured but unreachable — the diagnostic is
	// the degraded page, not a 500.
	t.Setenv("MCP_URL", "http://127.0.0.1:9")
	req2 := newRequest("GET", "/api/admin/mcp/status", nil)
	req2.Header.Set("X-User-ID", admin)
	testutil.Call(t, testHandler.AdminMCPServerStatus, req2).Want(http.StatusOK).JSON(&status)
	if !status.MCP.URLConfigured {
		t.Fatal("MCP_URL set but url_configured = false")
	}
	if status.MCP.Reachable {
		t.Fatal("reachable = true against a closed port")
	}
}
