package handler

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/credentialcrypto"
	"github.com/multica-ai/multica/server/internal/testutil"
)

type credentialDeliveryFixture struct {
	h         *Handler
	transport *rsa.PublicKey
	issueID   string
	agentID   string
	commentID string
}

func newCredentialDeliveryFixture(t *testing.T, status string) credentialDeliveryFixture {
	t.Helper()

	transportKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal("generate transport key")
	}
	h := *testHandler
	h.CredentialTransportKeys = credentialcrypto.TransportKeyring{
		CurrentKeyID: "test-key",
		PrivateKeys:  map[string]*rsa.PrivateKey{"test-key": transportKey},
	}
	h.CredentialStorageKeys = credentialcrypto.StorageKeyring{
		CurrentKeyID: "test-storage",
		MasterKeys:   map[string][]byte{"test-storage": make([]byte, 32)},
	}

	agentID := dbfx.Agent(t, "Credential delivery requester", handlerTestRuntimeID(t))
	issueID := dbfx.Issue(t, "Credential delivery request", testutil.Cols{"status": status})
	taskID := dbfx.Task(t, agentID, testutil.Cols{
		"runtime_id": handlerTestRuntimeID(t),
		"issue_id":   issueID,
	})
	commentID := dbfx.Comment(t, issueID, credentialRequestCommentMarker+"\nA credential is required to continue.", testutil.Cols{
		"author_type":    "agent",
		"author_id":      agentID,
		"source_task_id": taskID,
	})
	return credentialDeliveryFixture{
		h: &h, transport: &transportKey.PublicKey, issueID: issueID, agentID: agentID, commentID: commentID,
	}
}

func (f credentialDeliveryFixture) submit(t *testing.T, subjectID string) *httptest.ResponseRecorder {
	t.Helper()
	context := credentialcrypto.Context{
		WorkspaceID:  testWorkspaceID,
		ResourceType: "issue",
		ResourceID:   f.issueID,
		SubjectType:  "agent",
		SubjectID:    subjectID,
		Purpose:      "credential.create",
		Version:      credentialcrypto.ProtocolVersion,
	}
	body := map[string]any{
		"name":               "Delivery fixture",
		"purpose":            "Complete the requested work",
		"subject_type":       "agent",
		"subject_id":         subjectID,
		"resource_scope":     "issue",
		"request_comment_id": f.commentID,
		"envelope":           sealCredentialEnvelopeForHandlerTest(t, f.transport, context),
	}
	recorder := httptest.NewRecorder()
	request := newRequest(http.MethodPost, "/api/issues/"+f.issueID+"/credentials", body)
	request.Header.Set("X-User-ID", testUserID)
	f.h.CreateIssueCredential(recorder, withURLParam(request, "id", f.issueID))
	return recorder
}

func assertCredentialDeliveryWrites(t *testing.T, issueID string, want int) {
	t.Helper()
	for table, where := range map[string]string{
		"credential_vault": "workspace_id = $1 AND name = 'Delivery fixture'",
		"credential_grant": "workspace_id = $1 AND subject_type = 'agent'",
		"credential_audit": "workspace_id = $1 AND event_type = 'credential.created'",
		"activity_log":     "workspace_id = $1 AND issue_id = $2 AND action = 'credential_delivered'",
	} {
		args := []any{testWorkspaceID}
		if table == "activity_log" {
			args = append(args, issueID)
		}
		if got := dbfx.Count(t, "SELECT count(*) FROM "+table+" WHERE "+where, args...); got != want {
			t.Fatalf("%s writes = %d, want %d", table, got, want)
		}
	}
}

func TestCredentialDeliveryIsSingleUseAndPublishesSafeActivity(t *testing.T) {
	if testHandler == nil || testPool == nil || dbfx == nil {
		t.Skip("handler database is unavailable")
	}
	fixture := newCredentialDeliveryFixture(t, "todo")

	if response := fixture.submit(t, fixture.agentID); response.Code != http.StatusCreated {
		t.Fatalf("first delivery status = %d, want 201: %s", response.Code, response.Body.String())
	}
	if response := fixture.submit(t, fixture.agentID); response.Code != http.StatusConflict {
		t.Fatalf("repeat delivery status = %d, want 409: %s", response.Code, response.Body.String())
	}
	assertCredentialDeliveryWrites(t, fixture.issueID, 1)

	var rawDetails []byte
	dbfx.QueryRow(t, `SELECT details FROM activity_log
		WHERE workspace_id = $1 AND issue_id = $2 AND action = 'credential_delivered'`, testWorkspaceID, fixture.issueID).
		Scan(&rawDetails)
	var details map[string]json.RawMessage
	if err := json.Unmarshal(rawDetails, &details); err != nil {
		t.Fatalf("decode delivery activity details: %v", err)
	}
	allowed := map[string]bool{
		"credential_id": true, "grant_id": true, "request_comment_id": true, "requester_agent_id": true,
		"subject_type": true, "subject_id": true, "resource_scope": true, "resource_id": true,
	}
	if len(details) != len(allowed) {
		t.Fatalf("delivery activity details keys = %v, want only %v", details, allowed)
	}
	for key := range details {
		if !allowed[key] {
			t.Fatalf("delivery activity exposed prohibited detail %q", key)
		}
	}
}

func TestCredentialDeliveryRejectsTerminalIssuesWithoutWrites(t *testing.T) {
	if testHandler == nil || testPool == nil || dbfx == nil {
		t.Skip("handler database is unavailable")
	}

	for _, status := range []string{"done", "cancelled"} {
		t.Run(status, func(t *testing.T) {
			fixture := newCredentialDeliveryFixture(t, status)
			if response := fixture.submit(t, fixture.agentID); response.Code != http.StatusConflict {
				t.Fatalf("delivery on %s issue status = %d, want 409: %s", status, response.Code, response.Body.String())
			}
			assertCredentialDeliveryWrites(t, fixture.issueID, 0)
		})
	}

	for _, category := range []string{"done", "cancelled"} {
		t.Run("custom "+category, func(t *testing.T) {
			key := fmt.Sprintf("delivery_%s_%d", category, time.Now().UnixNano())
			dbfx.Insert(t, "issue_status", testutil.Cols{
				"workspace_id": testWorkspaceID,
				"key":          key,
				"name":         "Delivery terminal",
				"description":  "",
				"category":     category,
				"color":        "#00aa00",
				"position":     99,
			})
			fixture := newCredentialDeliveryFixture(t, key)
			if response := fixture.submit(t, fixture.agentID); response.Code != http.StatusConflict {
				t.Fatalf("delivery on custom %s issue status = %d, want 409: %s", category, response.Code, response.Body.String())
			}
			assertCredentialDeliveryWrites(t, fixture.issueID, 0)
		})
	}
}

func TestCredentialDeliveryRejectsCommentLineageMismatchWithoutWrites(t *testing.T) {
	if testHandler == nil || testPool == nil || dbfx == nil {
		t.Skip("handler database is unavailable")
	}

	tests := []struct {
		name    string
		prepare func(t *testing.T, fixture credentialDeliveryFixture) string
	}{
		{
			name: "member-authored comment",
			prepare: func(t *testing.T, fixture credentialDeliveryFixture) string {
				dbfx.Exec(t, `UPDATE comment SET author_type = 'member', author_id = $2 WHERE id = $1`, fixture.commentID, testUserID)
				return fixture.agentID
			},
		},
		{
			name: "reply comment",
			prepare: func(t *testing.T, fixture credentialDeliveryFixture) string {
				parentID := dbfx.Comment(t, fixture.issueID, "Earlier discussion")
				dbfx.Exec(t, `UPDATE comment SET parent_id = $2 WHERE id = $1`, fixture.commentID, parentID)
				return fixture.agentID
			},
		},
		{
			name: "comment without source task",
			prepare: func(t *testing.T, fixture credentialDeliveryFixture) string {
				dbfx.Exec(t, `UPDATE comment SET source_task_id = NULL WHERE id = $1`, fixture.commentID)
				return fixture.agentID
			},
		},
		{
			name: "task belongs to another issue",
			prepare: func(t *testing.T, fixture credentialDeliveryFixture) string {
				otherIssueID := dbfx.Issue(t, "Other credential delivery issue")
				dbfx.Exec(t, `UPDATE agent_task_queue SET issue_id = $2 WHERE id = (SELECT source_task_id FROM comment WHERE id = $1)`, fixture.commentID, otherIssueID)
				return fixture.agentID
			},
		},
		{
			name: "task belongs to another agent",
			prepare: func(t *testing.T, fixture credentialDeliveryFixture) string {
				otherAgentID := dbfx.Agent(t, "Other credential delivery requester", handlerTestRuntimeID(t))
				dbfx.Exec(t, `UPDATE agent_task_queue SET agent_id = $2 WHERE id = (SELECT source_task_id FROM comment WHERE id = $1)`, fixture.commentID, otherAgentID)
				return fixture.agentID
			},
		},
		{
			name: "request subject differs from comment author",
			prepare: func(t *testing.T, fixture credentialDeliveryFixture) string {
				return dbfx.Agent(t, "Mismatched credential delivery subject", handlerTestRuntimeID(t))
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCredentialDeliveryFixture(t, "todo")
			subjectID := test.prepare(t, fixture)
			if response := fixture.submit(t, subjectID); response.Code != http.StatusBadRequest {
				t.Fatalf("delivery status = %d, want 400: %s", response.Code, response.Body.String())
			}
			assertCredentialDeliveryWrites(t, fixture.issueID, 0)
		})
	}
}
