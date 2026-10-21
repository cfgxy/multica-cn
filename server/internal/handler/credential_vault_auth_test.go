package handler

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
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

func TestGlobalCredentialResourceUsesGlobalEnvelopeSentinel(t *testing.T) {
	issue := dbIssueForCredentialAuth("00000000-0000-7000-8000-000000000001")
	owner := dbMemberForCredentialAuth("owner", "00000000-0000-7000-8000-000000000001")
	resourceID, aadID, ok := credentialResource(issue, owner, "00000000-0000-7000-8000-000000000001", "global", "global")
	if !ok || resourceID.Valid || aadID != "global" {
		t.Fatalf("global resource = (%v, %q, %t), want null storage ID and global AAD", resourceID, aadID, ok)
	}
	if _, _, ok := credentialResource(issue, owner, "00000000-0000-7000-8000-000000000001", "global", "workspace"); ok {
		t.Fatal("non-global resource ID was accepted for global scope")
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
	globalContext := credentialcrypto.Context{
		WorkspaceID: testWorkspaceID, ResourceType: "global", ResourceID: "global",
		SubjectType: "agent", SubjectID: agentID, ConnectorID: connectorID, ConnectorRev: 1,
		Purpose: "credential.create", Version: credentialcrypto.ProtocolVersion,
	}
	globalEnvelope := sealCredentialEnvelopeForHandlerTest(t, &transportKey.PublicKey, globalContext)
	globalBody := map[string]any{
		"name": "Global fixture", "target_service": "fixture", "purpose": "synthetic global authorization test",
		"connector_id": connectorID, "connector_revision": 1, "subject_type": "agent", "subject_id": agentID,
		"resource_scope": "global", "resource_id": "global",
		"expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339), "envelope": globalEnvelope,
	}
	globalRecorder := httptest.NewRecorder()
	globalRequest := newRequest(http.MethodPost, "/api/issues/"+issueID+"/credentials", globalBody)
	globalRequest.Header.Set("X-User-ID", testUserID)
	globalRequest = withURLParam(globalRequest, "id", issueID)
	h.CreateIssueCredential(globalRecorder, globalRequest)
	if globalRecorder.Code != http.StatusCreated {
		t.Fatalf("owner global credential submission status = %d, want 201: %s", globalRecorder.Code, globalRecorder.Body.String())
	}
	var globalResponse credentialMetadataResponse
	if err := json.Unmarshal(globalRecorder.Body.Bytes(), &globalResponse); err != nil {
		t.Fatal("global credential response was invalid")
	}
	if strings.Contains(globalRecorder.Body.String(), "synthetic-global-secret") {
		t.Fatal("global credential response disclosed the submitted value")
	}
	var globalResourceID *string
	if err := testPool.QueryRow(context.Background(), `SELECT resource_id::text FROM credential_grant WHERE credential_id = $1`, globalResponse.ID).Scan(&globalResourceID); err != nil {
		t.Fatal("global credential grant was not persisted")
	}
	if globalResourceID != nil {
		t.Fatal("global grant must persist a null resource_id")
	}
	globalListRequest := newRequest(http.MethodGet, "/api/issues/"+issueID+"/credentials", nil)
	globalListRequest.Header.Set("X-User-ID", testUserID)
	globalListRequest = withURLParam(globalListRequest, "id", issueID)
	globalListRecorder := httptest.NewRecorder()
	h.ListIssueCredentials(globalListRecorder, globalListRequest)
	var listedCredentials []credentialMetadataResponse
	if globalListRecorder.Code != http.StatusOK || json.Unmarshal(globalListRecorder.Body.Bytes(), &listedCredentials) != nil {
		t.Fatalf("global credential list status = %d: %s", globalListRecorder.Code, globalListRecorder.Body.String())
	}
	foundGlobal := false
	for _, item := range listedCredentials {
		foundGlobal = foundGlobal || item.ID == globalResponse.ID
	}
	if !foundGlobal {
		t.Fatal("global credential was not shown in the issue management list")
	}
	var globalAudit struct {
		Result, Reason, Purpose string
		IssueID                 pgtype.UUID
	}
	if err := testPool.QueryRow(context.Background(), `SELECT result, reason_code, purpose, issue_id FROM credential_audit WHERE credential_id = $1 AND event_type = 'credential.created'`, globalResponse.ID).
		Scan(&globalAudit.Result, &globalAudit.Reason, &globalAudit.Purpose, &globalAudit.IssueID); err != nil {
		t.Fatal("global credential creation audit was not persisted")
	}
	if globalAudit.Result != "recorded" || globalAudit.Reason != "created" || globalAudit.Purpose != "synthetic global authorization test" || globalAudit.IssueID != parseUUID(issueID) {
		t.Fatalf("global credential audit fields are incomplete: %+v", globalAudit)
	}
	auditRequest := newRequest(http.MethodGet, "/api/workspaces/"+testWorkspaceID+"/credential-audit", nil)
	auditRequest.Header.Set("X-User-ID", testUserID)
	auditRequest = withURLParam(auditRequest, "id", testWorkspaceID)
	auditRecorder := httptest.NewRecorder()
	h.ListCredentialAudit(auditRecorder, auditRequest)
	if auditRecorder.Code != http.StatusOK || !strings.Contains(auditRecorder.Body.String(), "synthetic global authorization test") || strings.Contains(auditRecorder.Body.String(), "synthetic-global-secret") {
		t.Fatalf("owner credential audit response status/body mismatch: %d %s", auditRecorder.Code, auditRecorder.Body.String())
	}
	deniedAuditRequest := newRequest(http.MethodGet, "/api/workspaces/"+testWorkspaceID+"/credential-audit", nil)
	deniedAuditRequest.Header.Set("X-User-ID", memberID)
	deniedAuditRequest = withURLParam(deniedAuditRequest, "id", testWorkspaceID)
	deniedAuditRecorder := httptest.NewRecorder()
	h.ListCredentialAudit(deniedAuditRecorder, deniedAuditRequest)
	if deniedAuditRecorder.Code != http.StatusForbidden {
		t.Fatalf("member credential audit status = %d, want 403: %s", deniedAuditRecorder.Code, deniedAuditRecorder.Body.String())
	}
	var originalCiphertext []byte
	if err := testPool.QueryRow(context.Background(), `SELECT encrypted_value FROM credential_vault WHERE id = $1`, globalResponse.ID).Scan(&originalCiphertext); err != nil {
		t.Fatal("could not read stored test ciphertext")
	}
	failedReplacementBody := map[string]any{
		"name": "Global fixture", "target_service": "fixture", "purpose": "synthetic replacement test",
		"connector_id": connectorID, "connector_revision": 1, "subject_type": "agent", "subject_id": agentID,
		"resource_scope": "global", "resource_id": "global",
		"expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		"envelope": map[string]any{
			"version": 1, "key_id": "test-key", "algorithm": credentialcrypto.TransportRSAOAEP,
			"wrapped_data_key": globalEnvelope.WrappedDataKey, "nonce": globalEnvelope.Nonce, "ciphertext": "invalid-envelope-ciphertext",
		},
	}
	failedReplacement := replaceCredentialAs(t, &h, testUserID, issueID, globalResponse.ID, failedReplacementBody)
	if failedReplacement.Code != http.StatusBadRequest {
		t.Fatalf("invalid replacement status = %d, want 400: %s", failedReplacement.Code, failedReplacement.Body.String())
	}
	var unchangedCiphertext []byte
	if err := testPool.QueryRow(context.Background(), `SELECT encrypted_value FROM credential_vault WHERE id = $1`, globalResponse.ID).Scan(&unchangedCiphertext); err != nil || !bytes.Equal(originalCiphertext, unchangedCiphertext) {
		t.Fatal("failed replacement changed the active ciphertext")
	}
	replacementContext := globalContext
	replacementContext.Purpose = "credential.create"
	replacementEnvelope := sealCredentialEnvelopeForHandlerTest(t, &transportKey.PublicKey, replacementContext)
	failedReplacementBody["envelope"] = replacementEnvelope
	const concurrentReplacements = 2
	var replacementWait sync.WaitGroup
	replacementResponses := make(chan int, concurrentReplacements)
	for i := 0; i < concurrentReplacements; i++ {
		replacementWait.Add(1)
		go func() {
			defer replacementWait.Done()
			response := replaceCredentialAs(t, &h, testUserID, issueID, globalResponse.ID, failedReplacementBody)
			replacementResponses <- response.Code
		}()
	}
	replacementWait.Wait()
	close(replacementResponses)
	for status := range replacementResponses {
		if status != http.StatusOK {
			t.Fatalf("concurrent replacement status = %d, want 200", status)
		}
	}
	var activeGrantCount int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM credential_grant WHERE credential_id = $1 AND status = 'active'`, globalResponse.ID).Scan(&activeGrantCount); err != nil || activeGrantCount != 1 {
		t.Fatalf("active grant count after concurrent replacement = %d, want 1", activeGrantCount)
	}
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

func sealCredentialEnvelopeForHandlerTest(t *testing.T, key *rsa.PublicKey, ctx credentialcrypto.Context) credentialcrypto.TransportEnvelope {
	t.Helper()
	aad, err := ctx.AssociatedData()
	if err != nil {
		t.Fatal("global credential context was invalid")
	}
	dataKey := make([]byte, 32)
	if _, err := rand.Read(dataKey); err != nil {
		t.Fatal("fixture key generation failed")
	}
	block, err := aes.NewCipher(dataKey)
	if err != nil {
		t.Fatal("fixture cipher setup failed")
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal("fixture AEAD setup failed")
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		t.Fatal("fixture nonce generation failed")
	}
	label := sha256.Sum256(aad)
	wrapped, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, key, dataKey, label[:])
	if err != nil {
		t.Fatal("fixture wrapping failed")
	}
	return credentialcrypto.TransportEnvelope{
		Version: credentialcrypto.ProtocolVersion, KeyID: "test-key", Algorithm: credentialcrypto.TransportRSAOAEP,
		WrappedDataKey: base64.RawURLEncoding.EncodeToString(wrapped),
		Nonce:          base64.RawURLEncoding.EncodeToString(nonce),
		Ciphertext:     base64.RawURLEncoding.EncodeToString(aead.Seal(nil, nonce, []byte("synthetic-global-secret"), aad)),
	}
}

func replaceCredentialAs(t *testing.T, h *Handler, userID, issueID, credentialID string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	req := newRequest(http.MethodPost, "/api/issues/"+issueID+"/credentials/"+credentialID+"/replace", body)
	req.Header.Set("X-User-ID", userID)
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("id", issueID)
	routeCtx.URLParams.Add("credentialId", credentialID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
	if got := chi.URLParam(req, "id"); got != issueID {
		t.Fatalf("replace route issue parameter = %q, want %q", got, issueID)
	}
	if got := chi.URLParam(req, "credentialId"); got != credentialID {
		t.Fatalf("replace route credential parameter = %q, want %q", got, credentialID)
	}
	h.ReplaceIssueCredential(recorder, req)
	return recorder
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
