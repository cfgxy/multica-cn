package handler

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/credentialcrypto"
	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/internal/util"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

func TestCredentialGrantMatchesTaskRequiresBothDimensions(t *testing.T) {
	issueID := util.MustParseUUID("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	workspaceID := util.MustParseUUID("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")
	agentID := util.MustParseUUID("cccccccc-cccc-4ccc-8ccc-cccccccccccc")
	projectID := util.MustParseUUID("dddddddd-dddd-4ddd-8ddd-dddddddddddd")
	squadID := util.MustParseUUID("eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee")
	base := credentialCapabilityGrant{
		IssueID: issueID, WorkspaceID: workspaceID, AgentID: agentID, ProjectID: projectID,
		SquadID: squadID, ResourceScope: "issue", ResourceID: issueID,
		SubjectType: "agent", SubjectID: agentID, TaskStatus: "dispatched", IssueCategory: "started",
	}
	if !credentialGrantMatchesTask(base) {
		t.Fatal("matching issue and agent grant was rejected")
	}

	cases := []struct {
		name   string
		mutate func(*credentialCapabilityGrant)
	}{
		{"wrong issue resource", func(g *credentialCapabilityGrant) { g.ResourceID = projectID }},
		{"wrong subject", func(g *credentialCapabilityGrant) { g.SubjectID = projectID }},
		{"wrong workspace", func(g *credentialCapabilityGrant) { g.ResourceScope = "workspace"; g.ResourceID = projectID }},
		{"terminal issue", func(g *credentialCapabilityGrant) { g.IssueCategory = "done" }},
		{"terminal task", func(g *credentialCapabilityGrant) { g.TaskStatus = "completed" }},
		{"unassigned squad subject", func(g *credentialCapabilityGrant) { g.SubjectType = "squad"; g.SquadID.Valid = false }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			candidate := base
			test.mutate(&candidate)
			if credentialGrantMatchesTask(candidate) {
				t.Fatal("mismatched grant was accepted")
			}
		})
	}
}

func TestCredentialBrokerCapabilityIsControlBoundSingleUse(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler database is unavailable")
	}
	ctx := context.Background()
	runtimeID := createClaimReclaimRuntime(t, ctx, "Credential broker runtime")
	dbfx.Exec(t, `UPDATE agent_runtime SET daemon_id = $2 WHERE id = $1`, runtimeID, batchClaimTestDaemonID)
	agentID, issueID := createClaimReclaimAgentAndIssue(t, ctx, runtimeID, "Credential broker agent")
	taskID := seedQueuedIssueTask(t, ctx, agentID, runtimeID, issueID)
	connectorID := insertCredentialConnectorForAuth(t, testWorkspaceID)
	credentialID := uuidToString(dbid.NewV7())
	key := make([]byte, 32)
	for index := range key {
		key[index] = byte(index + 1)
	}
	storage := credentialcrypto.StorageKeyring{CurrentKeyID: "broker-test", MasterKeys: map[string][]byte{"broker-test": key}}
	storageContext := credentialcrypto.Context{
		WorkspaceID: testWorkspaceID, ResourceType: "credential", ResourceID: credentialID,
		SubjectType: "agent", SubjectID: agentID, ConnectorID: connectorID, ConnectorRev: 1,
		Purpose: "credential.storage", Version: credentialcrypto.ProtocolVersion,
	}
	storageAAD, err := storageContext.AssociatedData()
	if err != nil {
		t.Fatal("build storage AAD")
	}
	storageEnvelope, err := storage.Seal([]byte("synthetic-broker-secret"), storageAAD)
	if err != nil {
		t.Fatal("seal storage fixture")
	}
	storedJSON, err := json.Marshal(storageEnvelope)
	if err != nil {
		t.Fatal("marshal storage envelope")
	}
	dbfx.Insert(t, "credential_vault", testutil.Cols{
		"id": credentialID, "workspace_id": testWorkspaceID, "name": "synthetic broker fixture",
		"target_service": "fixture", "purpose": "synthetic broker test", "connector_id": connectorID,
		"connector_revision": 1, "credential_type": "machine", "status": "active",
		"encrypted_value": testutil.Raw(fmt.Sprintf("'%s'::jsonb", strings.ReplaceAll(string(storedJSON), "'", "''"))),
		"created_by":      testUserID,
	})
	grantID := uuidToString(dbid.NewV7())
	dbfx.Insert(t, "credential_grant", testutil.Cols{
		"id": grantID, "credential_id": credentialID, "workspace_id": testWorkspaceID,
		"resource_scope": "issue", "resource_id": issueID, "subject_type": "agent", "subject_id": agentID,
		"effect": "allow", "status": "active", "granted_by": testUserID,
	})
	h := *testHandler
	h.CredentialStorageKeys = storage
	sessionKey, err := credentialcrypto.NewSessionKey()
	if err != nil {
		t.Fatal("generate daemon session key")
	}
	identity := daemonws.ClientIdentity{DaemonID: batchClaimTestDaemonID, UserID: testUserID,
		WorkspaceID: testWorkspaceID, WorkspaceIDs: []string{testWorkspaceID}, RuntimeIDs: []string{runtimeID}}
	claimBody, err := json.Marshal(map[string]any{
		"daemon_id": batchClaimTestDaemonID, "runtime_ids": []string{runtimeID}, "max_tasks": 1,
		"session_public_key": base64.RawURLEncoding.EncodeToString(sessionKey.PublicKey().Bytes()),
	})
	if err != nil {
		t.Fatal("marshal claim request")
	}
	status, body, err := h.DaemonRPCHandler(ctx, identity, "tasks.claim", claimBody)
	if err != nil || status != http.StatusOK {
		t.Fatalf("claim status=%d err=%v body=%s", status, err, body)
	}
	var claimResponse struct {
		Tasks []struct {
			ID                   string                 `json:"id"`
			CredentialCapability credentialControlGrant `json:"credential_capability"`
		} `json:"tasks"`
	}
	if err := json.Unmarshal(body, &claimResponse); err != nil || len(claimResponse.Tasks) != 1 || claimResponse.Tasks[0].ID != taskID {
		t.Fatalf("claim response task mismatch: err=%v body=%s", err, body)
	}
	capability := claimResponse.Tasks[0].CredentialCapability
	if capability.Capability == "" || capability.ConnectorID != connectorID || capability.ConnectorRevision != 1 {
		t.Fatalf("claim did not return a bound capability: %+v", capability)
	}
	resolveBody, _ := json.Marshal(credentialResolveRequest{
		Capability: capability.Capability, ConnectorID: connectorID, ConnectorRevision: 1,
	})
	if code, _, resolveErr := h.DaemonRPCHandler(ctx,
		daemonws.ClientIdentity{DaemonID: "other-daemon"}, "credentials.resolve", resolveBody); code != http.StatusForbidden || resolveErr == nil {
		t.Fatalf("wrong daemon resolve status=%d err=%v", code, resolveErr)
	}
	wrongConnectorBody, _ := json.Marshal(credentialResolveRequest{
		Capability: capability.Capability, ConnectorID: uuidToString(dbid.NewV7()), ConnectorRevision: 1,
	})
	if code, _, resolveErr := h.DaemonRPCHandler(ctx, identity, "credentials.resolve", wrongConnectorBody); code != http.StatusForbidden || resolveErr == nil {
		t.Fatalf("wrong connector resolve status=%d err=%v", code, resolveErr)
	}
	otherIssueID := dbfx.Issue(t, "Credential broker swapped issue")
	dbfx.Exec(t, `UPDATE agent_task_queue SET issue_id = $2 WHERE id = $1`, taskID, otherIssueID)
	if code, _, resolveErr := h.DaemonRPCHandler(ctx, identity, "credentials.resolve", resolveBody); code != http.StatusForbidden || resolveErr == nil {
		t.Fatalf("wrong task issue binding status=%d err=%v", code, resolveErr)
	}
	dbfx.Exec(t, `UPDATE agent_task_queue SET issue_id = $2 WHERE id = $1`, taskID, issueID)
	type resolveResult struct {
		status int
		body   json.RawMessage
		err    error
	}
	results := make(chan resolveResult, 2)
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			code, response, resolveErr := h.DaemonRPCHandler(ctx, identity, "credentials.resolve", resolveBody)
			results <- resolveResult{status: code, body: response, err: resolveErr}
		}()
	}
	wait.Wait()
	close(results)
	var success *resolveResult
	successes := 0
	for result := range results {
		if result.status == http.StatusOK && result.err == nil {
			successes++
			success = &result
		}
	}
	if successes != 1 || success == nil {
		t.Fatalf("concurrent resolutions succeeded %d times, want exactly once", successes)
	}
	var envelope credentialResolveResponse
	if err := json.Unmarshal(success.body, &envelope); err != nil {
		t.Fatal("decode execution envelope")
	}
	expiresAt := envelope.ExpiresAt
	executionAAD, err := json.Marshal(struct {
		TaskID            string    `json:"task_id"`
		WorkspaceID       string    `json:"workspace_id"`
		DaemonID          string    `json:"daemon_id"`
		ConnectorID       string    `json:"connector_id"`
		ConnectorRevision int64     `json:"connector_revision"`
		ExpiresAt         time.Time `json:"expires_at"`
	}{taskID, testWorkspaceID, batchClaimTestDaemonID, connectorID, 1, expiresAt})
	if err != nil {
		t.Fatal("build execution AAD")
	}
	plaintext, err := credentialcrypto.OpenForSession(sessionKey, envelope.Envelope, executionAAD)
	if err != nil || string(plaintext) != "synthetic-broker-secret" {
		t.Fatalf("open execution envelope = %q, %v", plaintext, err)
	}
	zeroCredentialBytes(plaintext)
	if code, _, resolveErr := h.DaemonRPCHandler(ctx, identity, "credentials.resolve", resolveBody); code != http.StatusForbidden || resolveErr == nil {
		t.Fatalf("replay resolve status=%d err=%v", code, resolveErr)
	}

	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		t.Fatal("begin revocation-race capability transaction")
	}
	raceCapability, err := mintCredentialCapability(ctx, tx, batchClaimTestDaemonID, taskID, sessionKey.PublicKey().Bytes())
	if err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal("mint revocation-race capability")
	}
	if err := tx.Commit(ctx); err != nil || raceCapability == nil {
		t.Fatalf("commit revocation-race capability: capability=%v err=%v", raceCapability != nil, err)
	}
	raceBody, _ := json.Marshal(credentialResolveRequest{
		Capability: raceCapability.Capability, ConnectorID: connectorID, ConnectorRevision: 1,
	})
	started := make(chan struct{})
	raced := make(chan int, 1)
	revoked := make(chan error, 1)
	go func() {
		<-started
		code, _, _ := h.DaemonRPCHandler(ctx, identity, "credentials.resolve", raceBody)
		raced <- code
	}()
	go func() {
		<-started
		_, err := testPool.Exec(ctx, `UPDATE credential_grant SET status = 'revoked', updated_at = now() WHERE id = $1`, grantID)
		revoked <- err
	}()
	close(started)
	resolveStatus := <-raced
	if err := <-revoked; err != nil {
		t.Fatalf("revoke grant during resolution: %v", err)
	}
	if resolveStatus != http.StatusOK && resolveStatus != http.StatusForbidden {
		t.Fatalf("grant-revocation race status=%d, want 200 or fail-closed 403", resolveStatus)
	}
	raceHash := sha256.Sum256([]byte(raceCapability.Capability))
	var consumed bool
	if err := testPool.QueryRow(ctx, `SELECT consumed_at IS NOT NULL FROM credential_capability WHERE capability_hash = $1`, raceHash[:]).Scan(&consumed); err != nil {
		t.Fatal("read revocation-race capability state")
	}
	if (resolveStatus == http.StatusOK) != consumed {
		t.Fatalf("revocation-race status=%d consumed=%t: expected success and consumption to linearize together", resolveStatus, consumed)
	}
}
