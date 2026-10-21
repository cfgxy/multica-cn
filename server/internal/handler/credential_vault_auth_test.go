package handler

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/credentialcrypto"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

func TestCredentialWriteScopeAuthorization(t *testing.T) {
	userID := "00000000-0000-7000-8000-000000000001"
	otherID := "00000000-0000-7000-8000-000000000002"
	issue := dbIssueForCredentialAuth(userID)

	tests := []struct {
		name     string
		role     string
		assignee string
		scope    string
		want     bool
	}{
		{name: "owner issue", role: "owner", assignee: otherID, scope: "issue", want: true},
		{name: "owner single use", role: "owner", assignee: otherID, scope: "single_use", want: true},
		{name: "owner project", role: "owner", assignee: otherID, scope: "project", want: true},
		{name: "owner workspace", role: "owner", assignee: otherID, scope: "workspace", want: true},
		{name: "owner global", role: "owner", assignee: otherID, scope: "global", want: true},
		{name: "current direct assignee issue", role: "member", assignee: userID, scope: "issue", want: true},
		{name: "current direct assignee single use", role: "member", assignee: userID, scope: "single_use", want: true},
		{name: "former assignee after reassignment", role: "member", assignee: otherID, scope: "issue"},
		{name: "former owner after downgrade", role: "member", assignee: otherID, scope: "issue"},
		{name: "non-owner project", role: "member", assignee: userID, scope: "project"},
		{name: "non-owner workspace", role: "member", assignee: userID, scope: "workspace"},
		{name: "non-owner global", role: "member", assignee: userID, scope: "global"},
		{name: "admin is not owner for global", role: "admin", assignee: userID, scope: "global"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			current := issue
			current.AssigneeID = parseUUID(tt.assignee)
			member := dbMemberForCredentialAuth(tt.role, userID)
			if got := credentialCanWriteScope(member, current, userID, tt.scope); got != tt.want {
				t.Fatalf("credentialCanWriteScope(%q) = %t, want %t", tt.scope, got, tt.want)
			}
		})
	}
}

func dbIssueForCredentialAuth(assigneeID string) db.Issue {
	return db.Issue{
		ID:           parseUUID("00000000-0000-7000-8000-000000000003"),
		WorkspaceID:  parseUUID("00000000-0000-7000-8000-000000000004"),
		ProjectID:    parseUUID("00000000-0000-7000-8000-000000000005"),
		AssigneeType: pgtype.Text{String: "member", Valid: true},
		AssigneeID:   parseUUID(assigneeID),
	}
}

func dbMemberForCredentialAuth(role, userID string) db.Member {
	return db.Member{Role: role, UserID: parseUUID(userID)}
}

func TestCredentialWritesUseCurrentRoleAndAssignee(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler database is unavailable")
	}

	formerOwnerID := dbfx.User(t, "Credential former owner", fmt.Sprintf("credential-former-owner-%s@multica.ai", handlerFixtureSuffix))
	memberID := dbfx.User(t, "Credential assignee", fmt.Sprintf("credential-assignee-%s@multica.ai", handlerFixtureSuffix))
	dbfx.Member(t, testWorkspaceID, formerOwnerID, "owner")
	dbfx.Member(t, testWorkspaceID, memberID, "member")
	projectID := dbfx.Project(t, "Credential authorization project")
	issueID := dbfx.Issue(t, "Credential authorization issue", testutil.Cols{
		"project_id":    projectID,
		"assignee_type": "member",
		"assignee_id":   formerOwnerID,
	})

	ownerProjectCredential := insertCredentialForAuth(t, projectID, "project", formerOwnerID)
	formerAssigneeCredential := insertCredentialForAuth(t, issueID, "issue", formerOwnerID)

	// The role change and reassignment happen before the request. The handler
	// must use the locked rows, not the creator/assignee snapshot from creation.
	dbfx.Exec(t, `UPDATE member SET role = 'member' WHERE workspace_id = $1 AND user_id = $2`, testWorkspaceID, formerOwnerID)
	dbfx.Exec(t, `UPDATE issue SET assignee_id = $2 WHERE id = $1`, issueID, memberID)

	for name, credentialID := range map[string]string{
		"downgraded former owner cannot revoke project credential":  ownerProjectCredential,
		"reassigned former assignee cannot revoke issue credential": formerAssigneeCredential,
	} {
		t.Run(name, func(t *testing.T) {
			w := revokeCredentialAs(t, testHandler, formerOwnerID, issueID, credentialID)
			if w.Code != http.StatusForbidden {
				t.Fatalf("revoke status = %d, want 403: %s", w.Code, w.Body.String())
			}
		})
	}

	// The still-current direct assignee may manage Issue and single-use grants,
	// but no non-owner may write a broader scope even when they created it.
	directIssue := dbfx.Issue(t, "Current credential assignee issue", testutil.Cols{
		"assignee_type": "member",
		"assignee_id":   memberID,
	})
	for _, scope := range []string{"issue", "single_use"} {
		credentialID := insertCredentialForAuth(t, directIssue, scope, memberID)
		w := revokeCredentialAs(t, testHandler, memberID, directIssue, credentialID)
		if w.Code != http.StatusOK {
			t.Fatalf("current assignee revoke %s status = %d, want 200: %s", scope, w.Code, w.Body.String())
		}
	}

	for _, scope := range []string{"project", "workspace", "global"} {
		credentialID := insertCredentialForAuth(t, map[string]string{
			"project": projectID, "workspace": testWorkspaceID, "global": "",
		}[scope], scope, memberID)
		w := revokeCredentialAs(t, testHandler, memberID, issueID, credentialID)
		if w.Code != http.StatusForbidden {
			t.Fatalf("non-owner revoke %s status = %d, want 403: %s", scope, w.Code, w.Body.String())
		}
		owner := revokeCredentialAs(t, testHandler, testUserID, issueID, credentialID)
		if owner.Code != http.StatusOK {
			t.Fatalf("current owner revoke %s status = %d, want 200: %s", scope, owner.Code, owner.Body.String())
		}
	}

	mixedCredential := insertCredentialForAuth(t, issueID, "issue", memberID)
	insertCredentialGrantForAuth(t, mixedCredential, "global", "", memberID)
	deniedMixedRevoke := revokeCredentialAs(t, testHandler, memberID, issueID, mixedCredential)
	if deniedMixedRevoke.Code != http.StatusForbidden {
		t.Fatalf("non-owner revoke mixed-scope credential status = %d, want 403: %s", deniedMixedRevoke.Code, deniedMixedRevoke.Body.String())
	}
	var mixedStatus string
	dbfx.QueryRow(t, `SELECT status FROM credential_vault WHERE id = $1`, mixedCredential).Scan(&mixedStatus)
	if mixedStatus != "active" {
		t.Fatalf("mixed-scope credential status after denied revoke = %q, want active", mixedStatus)
	}
	ownerMixedRevoke := revokeCredentialAs(t, testHandler, testUserID, issueID, mixedCredential)
	if ownerMixedRevoke.Code != http.StatusOK {
		t.Fatalf("current owner revoke mixed-scope credential status = %d, want 200: %s", ownerMixedRevoke.Code, ownerMixedRevoke.Body.String())
	}

	foreignWorkspaceID := dbfx.Workspace(t, "Credential foreign workspace", fmt.Sprintf("credential-foreign-%s", handlerFixtureSuffix))
	crossWorkspaceCredential := insertCredentialForAuth(t, issueID, "issue", memberID)
	insertCredentialGrantForWorkspaceAuth(t, crossWorkspaceCredential, foreignWorkspaceID, "global", "", memberID)
	crossWorkspaceRevoke := revokeCredentialAs(t, testHandler, memberID, issueID, crossWorkspaceCredential)
	if crossWorkspaceRevoke.Code != http.StatusNotFound {
		t.Fatalf("revoke credential with cross-workspace grant status = %d, want 404: %s", crossWorkspaceRevoke.Code, crossWorkspaceRevoke.Body.String())
	}
	dbfx.QueryRow(t, `SELECT status FROM credential_vault WHERE id = $1`, crossWorkspaceCredential).Scan(&mixedStatus)
	if mixedStatus != "active" {
		t.Fatalf("cross-workspace credential status after denied revoke = %q, want active", mixedStatus)
	}
	var crossGrantStatus string
	dbfx.QueryRow(t, `SELECT status FROM credential_grant WHERE credential_id = $1 AND workspace_id = $2`, crossWorkspaceCredential, foreignWorkspaceID).Scan(&crossGrantStatus)
	if crossGrantStatus != "active" {
		t.Fatalf("cross-workspace grant status after denied revoke = %q, want active", crossGrantStatus)
	}

	transportKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal("generate transport test key")
	}
	h := *testHandler
	h.CredentialTransportKeys = credentialcrypto.TransportKeyring{
		CurrentKeyID: "test-key", PrivateKeys: map[string]*rsa.PrivateKey{"test-key": transportKey},
	}
	h.CredentialStorageKeys = credentialcrypto.StorageKeyring{
		CurrentKeyID: "test-storage", MasterKeys: map[string][]byte{"test-storage": make([]byte, 32)},
	}
	agentID := dbfx.Agent(t, "Credential authorization subject", testRuntimeID)
	connectorID := insertCredentialConnectorForAuth(t, testWorkspaceID)
	requestCreate := func(userID, resourceScope string) *httptest.ResponseRecorder {
		t.Helper()
		body := map[string]any{
			"name": "fixture", "target_service": "fixture", "purpose": "synthetic authorization test",
			"connector_id": connectorID, "connector_revision": 1,
			"subject_type": "agent", "subject_id": agentID, "resource_scope": resourceScope,
			"envelope": map[string]any{
				"version": 1, "key_id": "test-key", "algorithm": credentialcrypto.TransportRSAOAEP,
				"wrapped_data_key": base64.RawURLEncoding.EncodeToString([]byte("not-a-key")),
				"nonce":            base64.RawURLEncoding.EncodeToString(make([]byte, 12)),
				"ciphertext":       base64.RawURLEncoding.EncodeToString([]byte("synthetic")),
			},
		}
		if resourceScope == "project" || resourceScope == "workspace" || resourceScope == "global" {
			body["expires_at"] = time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
		}
		w := httptest.NewRecorder()
		req := newRequest(http.MethodPost, "/api/issues/"+issueID+"/credentials", body)
		req.Header.Set("X-User-ID", userID)
		req = withURLParam(req, "id", issueID)
		h.CreateIssueCredential(w, req)
		return w
	}
	for _, scope := range []string{"issue", "single_use", "project", "workspace", "global"} {
		w := requestCreate(formerOwnerID, scope)
		if w.Code != http.StatusForbidden {
			t.Fatalf("former owner create %s status = %d, want 403: %s", scope, w.Code, w.Body.String())
		}
	}
	for _, scope := range []string{"project", "workspace", "global"} {
		w := requestCreate(memberID, scope)
		if w.Code != http.StatusForbidden {
			t.Fatalf("non-owner create %s status = %d, want 403: %s", scope, w.Code, w.Body.String())
		}
	}
	// A current assignee reaches envelope verification, proving the issue
	// scope check passed before the intentionally malformed synthetic envelope.
	currentAssigneeCreate := requestCreate(memberID, "single_use")
	if currentAssigneeCreate.Code != http.StatusBadRequest {
		t.Fatalf("current assignee single-use create status = %d, want envelope rejection 400: %s", currentAssigneeCreate.Code, currentAssigneeCreate.Body.String())
	}
}

func insertCredentialForAuth(t *testing.T, resourceID, scope, creatorID string) string {
	t.Helper()
	credentialID := uuidToString(dbid.NewV7())
	connectorID := insertCredentialConnectorForAuth(t, testWorkspaceID)
	dbfx.Insert(t, "credential_vault", testutil.Cols{
		"id": credentialID, "workspace_id": testWorkspaceID, "name": "fixture",
		"target_service": "fixture", "purpose": "synthetic authorization test",
		"connector_id": connectorID, "connector_revision": 1,
		"credential_type": "machine", "status": "active", "encrypted_value": testutil.Raw("'{}'::jsonb"),
		"created_by": creatorID,
	})
	insertCredentialGrantForAuth(t, credentialID, scope, resourceID, creatorID)
	return credentialID
}

func insertCredentialConnectorForAuth(t *testing.T, workspaceID string) string {
	t.Helper()
	id := uuidToString(dbid.NewV7())
	dbfx.Insert(t, "credential_connector", testutil.Cols{
		"id": id, "workspace_id": workspaceID, "name": "synthetic connector",
		"kind": credentialConnectorKind, "target_url": "https://connector.example/mcp",
		"adapter_policy": credentialConnectorPolicy, "auth_field": credentialConnectorAuth,
		"status": "active", "revision": 1, "created_by": testUserID,
	})
	return id
}

func insertCredentialGrantForAuth(t *testing.T, credentialID, scope, resourceID, creatorID string) {
	t.Helper()
	insertCredentialGrantForWorkspaceAuth(t, credentialID, testWorkspaceID, scope, resourceID, creatorID)
}

func insertCredentialGrantForWorkspaceAuth(t *testing.T, credentialID, workspaceID, scope, resourceID, creatorID string) {
	t.Helper()
	grantID := uuidToString(dbid.NewV7())
	var resource any
	if resourceID != "" {
		resource = resourceID
	}
	dbfx.Insert(t, "credential_grant", testutil.Cols{
		"id": grantID, "credential_id": credentialID, "workspace_id": workspaceID,
		"resource_scope": scope, "resource_id": resource, "subject_type": "agent",
		"subject_id": testUserID, "effect": "allow", "status": "active", "granted_by": creatorID,
	})
}

func revokeCredentialAs(t *testing.T, h *Handler, userID, issueID, credentialID string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := newRequest(http.MethodPost, "/api/issues/"+issueID+"/credentials/"+credentialID+"/revoke", nil)
	req.Header.Set("X-User-ID", userID)
	req = withURLParams(req, "id", issueID, "credentialId", credentialID)
	h.RevokeIssueCredential(w, req)
	return w
}
