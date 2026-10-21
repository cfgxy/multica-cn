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

	"github.com/jackc/pgx/v5/pgtype"
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

func TestSelectTaskCredentialGrantsGroupsDeniesByCredentialAndConnector(t *testing.T) {
	connectorA := util.MustParseUUID("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	connectorB := util.MustParseUUID("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")
	credentialA := util.MustParseUUID("cccccccc-cccc-4ccc-8ccc-cccccccccccc")
	credentialB := util.MustParseUUID("dddddddd-dddd-4ddd-8ddd-dddddddddddd")
	credentialC := util.MustParseUUID("eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee")
	credentialD := util.MustParseUUID("ffffffff-ffff-4fff-8fff-ffffffffffff")
	issueID := util.MustParseUUID("11111111-1111-4111-8111-111111111111")
	workspaceID := util.MustParseUUID("22222222-2222-4222-8222-222222222222")
	agentID := util.MustParseUUID("33333333-3333-4333-8333-333333333333")
	base := credentialCapabilityGrant{
		WorkspaceID: workspaceID, IssueID: issueID, AgentID: agentID,
		ResourceScope: "issue", ResourceID: issueID, SubjectType: "agent", SubjectID: agentID,
		ConnectorRev: 1, ConnectorStatus: "active", TaskStatus: "dispatched", IssueCategory: "started",
	}
	grant := func(credentialID, connectorID pgtype.UUID, effect string) credentialCapabilityGrant {
		candidate := base
		candidate.CredentialID = credentialID
		candidate.ConnectorID = connectorID
		candidate.Effect = effect
		return candidate
	}
	selected := selectTaskCredentialGrants([]credentialCapabilityGrant{
		grant(credentialA, connectorA, "allow"),
		grant(credentialA, connectorA, "deny"),
		grant(credentialB, connectorB, "allow"),
		grant(credentialC, connectorB, "allow"),
		grant(credentialD, connectorB, "allow"),
		grant(credentialC, connectorB, "allow"),
	})
	if len(selected) != 0 {
		t.Fatalf("multiple credentials for one connector were selected: %+v", selected)
	}

	selected = selectTaskCredentialGrants([]credentialCapabilityGrant{
		grant(credentialA, connectorA, "allow"),
		grant(credentialA, connectorA, "deny"),
		grant(credentialB, connectorB, "allow"),
		grant(credentialD, connectorB, "allow"),
	})
	if len(selected) != 0 {
		t.Fatalf("two credentials for the same connector were not rejected: %+v", selected)
	}

	connectorC := util.MustParseUUID("44444444-4444-4444-8444-444444444444")
	connectorD := util.MustParseUUID("55555555-5555-4555-8555-555555555555")
	selected = selectTaskCredentialGrants([]credentialCapabilityGrant{
		grant(credentialA, connectorA, "deny"),
		grant(credentialB, connectorB, "allow"),
		grant(credentialC, connectorC, "allow"),
		grant(credentialD, connectorD, "allow"),
	})
	if len(selected) != 3 {
		t.Fatalf("selected %d capabilities, want three unaffected connector credentials", len(selected))
	}
	selectedByConnector := make(map[pgtype.UUID]pgtype.UUID, len(selected))
	for _, candidate := range selected {
		selectedByConnector[candidate.ConnectorID] = candidate.CredentialID
	}
	if _, ok := selectedByConnector[connectorA]; ok {
		t.Fatal("a credential with an applicable deny was selected")
	}
	if selectedByConnector[connectorB] != credentialB || selectedByConnector[connectorC] != credentialC || selectedByConnector[connectorD] != credentialD {
		t.Fatalf("available credentials were not selected independently: %+v", selectedByConnector)
	}
}

func TestFindTaskCredentialGrantsIsolatesConnectorDecisions(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler database is unavailable")
	}
	ctx := context.Background()
	runtimeID := createClaimReclaimRuntime(t, ctx, "Credential grouping runtime")
	dbfx.Exec(t, `UPDATE agent_runtime SET daemon_id = $2 WHERE id = $1`, runtimeID, batchClaimTestDaemonID)
	agentID, issueID := createClaimReclaimAgentAndIssue(t, ctx, runtimeID, "Credential grouping agent")
	taskID := seedQueuedIssueTask(t, ctx, agentID, runtimeID, issueID)
	dbfx.Exec(t, `UPDATE agent_task_queue SET status = 'dispatched' WHERE id = $1`, taskID)

	connectorDenied := insertCredentialConnectorForAuth(t, testWorkspaceID)
	connectorAvailableA := insertCredentialConnectorForAuth(t, testWorkspaceID)
	connectorConflict := insertCredentialConnectorForAuth(t, testWorkspaceID)
	connectorAvailableB := insertCredentialConnectorForAuth(t, testWorkspaceID)
	insertCredential := func(connectorID string, effects ...string) {
		t.Helper()
		credentialID := uuidToString(dbid.NewV7())
		dbfx.Insert(t, "credential_vault", testutil.Cols{
			"id": credentialID, "workspace_id": testWorkspaceID, "name": "synthetic grouping fixture",
			"target_service": "fixture", "purpose": "synthetic broker grouping test", "connector_id": connectorID,
			"connector_revision": 1, "credential_type": "machine", "status": "active",
			"encrypted_value": testutil.Raw("'{}'::jsonb"), "created_by": testUserID,
		})
		for _, effect := range effects {
			dbfx.Insert(t, "credential_grant", testutil.Cols{
				"id": uuidToString(dbid.NewV7()), "credential_id": credentialID, "workspace_id": testWorkspaceID,
				"resource_scope": "issue", "resource_id": issueID, "subject_type": "agent", "subject_id": agentID,
				"effect": effect, "status": "active", "granted_by": testUserID,
			})
		}
	}
	insertCredential(connectorDenied, "allow", "deny")
	insertCredential(connectorAvailableA, "allow")
	insertCredential(connectorConflict, "allow")
	insertCredential(connectorConflict, "allow")
	insertCredential(connectorAvailableB, "allow")

	tx, err := testHandler.TxStarter.Begin(ctx)
	if err != nil {
		t.Fatal("begin credential grouping query")
	}
	defer tx.Rollback(ctx)
	candidates, err := findTaskCredentialGrants(ctx, tx, taskID)
	if err != nil {
		t.Fatalf("read task credential grants: %v", err)
	}
	selected := selectTaskCredentialGrants(candidates)
	selectedConnectors := make(map[string]bool, len(selected))
	for _, grant := range selected {
		selectedConnectors[uuidToString(grant.ConnectorID)] = true
	}
	if len(selected) != 2 || !selectedConnectors[connectorAvailableA] || !selectedConnectors[connectorAvailableB] {
		t.Fatalf("selected connector grants = %v, want only the two unique allow connectors", selectedConnectors)
	}
	if selectedConnectors[connectorDenied] || selectedConnectors[connectorConflict] {
		t.Fatalf("deny or same-connector credential conflict was selected: %v", selectedConnectors)
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
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM credential_capability WHERE task_id = $1`, taskID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM credential_audit WHERE task_id = $1`, taskID)
	})
	dbfx.Insert(t, "credential_grant", testutil.Cols{
		"id": grantID, "credential_id": credentialID, "workspace_id": testWorkspaceID,
		"resource_scope": "issue", "resource_id": issueID, "subject_type": "agent", "subject_id": agentID,
		"effect": "allow", "status": "active", "granted_by": testUserID,
	})
	secondConnectorID := insertCredentialConnectorForAuth(t, testWorkspaceID)
	secondCredentialID := uuidToString(dbid.NewV7())
	secondStorageContext := credentialcrypto.Context{
		WorkspaceID: testWorkspaceID, ResourceType: "credential", ResourceID: secondCredentialID,
		SubjectType: "agent", SubjectID: agentID, ConnectorID: secondConnectorID, ConnectorRev: 1,
		Purpose: "credential.storage", Version: credentialcrypto.ProtocolVersion,
	}
	secondStorageAAD, err := secondStorageContext.AssociatedData()
	if err != nil {
		t.Fatal("build second storage AAD")
	}
	secondStorageEnvelope, err := storage.Seal([]byte("synthetic-second-broker-secret"), secondStorageAAD)
	if err != nil {
		t.Fatal("seal second storage fixture")
	}
	secondStoredJSON, err := json.Marshal(secondStorageEnvelope)
	if err != nil {
		t.Fatal("marshal second storage envelope")
	}
	dbfx.Insert(t, "credential_vault", testutil.Cols{
		"id": secondCredentialID, "workspace_id": testWorkspaceID, "name": "synthetic second connector fixture",
		"target_service": "fixture", "purpose": "second connector broker test", "connector_id": secondConnectorID,
		"connector_revision": 1, "credential_type": "machine", "status": "active",
		"encrypted_value": testutil.Raw(fmt.Sprintf("'%s'::jsonb", strings.ReplaceAll(string(secondStoredJSON), "'", "''"))),
		"created_by":      testUserID,
	})
	dbfx.Insert(t, "credential_grant", testutil.Cols{
		"id": uuidToString(dbid.NewV7()), "credential_id": secondCredentialID, "workspace_id": testWorkspaceID,
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
			ID                     string                   `json:"id"`
			CredentialCapabilities []credentialControlGrant `json:"credential_capabilities"`
		} `json:"tasks"`
	}
	if err := json.Unmarshal(body, &claimResponse); err != nil || len(claimResponse.Tasks) != 1 || claimResponse.Tasks[0].ID != taskID {
		t.Fatalf("claim response task mismatch: err=%v body=%s", err, body)
	}
	capabilities := claimResponse.Tasks[0].CredentialCapabilities
	if len(capabilities) != 2 {
		t.Fatalf("claim returned %d capabilities, want one for each connector: %+v", len(capabilities), capabilities)
	}
	var capability credentialControlGrant
	var secondCapability credentialControlGrant
	for _, candidate := range capabilities {
		if candidate.ConnectorID == connectorID {
			capability = candidate
		}
		if candidate.ConnectorID == secondConnectorID {
			secondCapability = candidate
		}
	}
	if capability.Capability == "" || capability.ConnectorRevision != 1 || secondCapability.Capability == "" {
		t.Fatalf("claim did not return the original connector capability: %+v", capabilities)
	}
	resolveBody, _ := json.Marshal(credentialResolveRequest{
		Capability: capability.Capability, ConnectorID: connectorID, ConnectorRevision: 1,
	})
	if code, _, resolveErr := h.DaemonRPCHandler(ctx,
		daemonws.ClientIdentity{DaemonID: "other-daemon"}, "credentials.resolve", resolveBody); code != http.StatusForbidden {
		t.Fatalf("wrong daemon resolve status=%d err=%v", code, resolveErr)
	}
	wrongConnectorBody, _ := json.Marshal(credentialResolveRequest{
		Capability: capability.Capability, ConnectorID: uuidToString(dbid.NewV7()), ConnectorRevision: 1,
	})
	if code, _, resolveErr := h.DaemonRPCHandler(ctx, identity, "credentials.resolve", wrongConnectorBody); code != http.StatusForbidden {
		t.Fatalf("wrong connector resolve status=%d err=%v", code, resolveErr)
	}
	otherIssueID := dbfx.Issue(t, "Credential broker swapped issue")
	dbfx.Exec(t, `UPDATE agent_task_queue SET issue_id = $2 WHERE id = $1`, taskID, otherIssueID)
	if code, _, resolveErr := h.DaemonRPCHandler(ctx, identity, "credentials.resolve", resolveBody); code != http.StatusForbidden {
		t.Fatalf("wrong task issue binding status=%d err=%v", code, resolveErr)
	}
	dbfx.Exec(t, `UPDATE agent_task_queue SET issue_id = $2 WHERE id = $1`, taskID, issueID)
	secondResolveBody, _ := json.Marshal(credentialResolveRequest{
		Capability: secondCapability.Capability, ConnectorID: secondConnectorID, ConnectorRevision: 1,
	})
	secondStatus, secondBody, secondResolveErr := h.DaemonRPCHandler(ctx, identity, "credentials.resolve", secondResolveBody)
	if secondStatus != http.StatusOK || secondResolveErr != nil {
		t.Fatalf("second connector resolve status=%d err=%v body=%s", secondStatus, secondResolveErr, secondBody)
	}
	var secondResolveResponse credentialResolveResponse
	if err := json.Unmarshal(secondBody, &secondResolveResponse); err != nil {
		t.Fatal("decode second connector execution envelope")
	}
	secondExecutionAAD, err := json.Marshal(struct {
		TaskID            string    `json:"task_id"`
		WorkspaceID       string    `json:"workspace_id"`
		DaemonID          string    `json:"daemon_id"`
		ConnectorID       string    `json:"connector_id"`
		ConnectorRevision int64     `json:"connector_revision"`
		ExpiresAt         time.Time `json:"expires_at"`
	}{taskID, testWorkspaceID, batchClaimTestDaemonID, secondConnectorID, 1, secondResolveResponse.ExpiresAt})
	if err != nil {
		t.Fatal("build second connector execution AAD")
	}
	secondPlaintext, err := credentialcrypto.OpenForSession(sessionKey, secondResolveResponse.Envelope, secondExecutionAAD)
	if err != nil || string(secondPlaintext) != "synthetic-second-broker-secret" {
		t.Fatalf("open second connector execution envelope = %q, %v", secondPlaintext, err)
	}
	zeroCredentialBytes(secondPlaintext)
	var deniedAudits int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM credential_audit WHERE task_id = $1 AND event_type = 'credential.broker_resolved' AND result = 'denied'`, taskID).Scan(&deniedAudits); err != nil {
		t.Fatal("count denied broker audits")
	}
	if deniedAudits < 3 {
		t.Fatalf("denied broker audit count = %d, want at least 3 for daemon, connector, and task binding mismatches", deniedAudits)
	}
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
	if code, _, resolveErr := h.DaemonRPCHandler(ctx, identity, "credentials.resolve", resolveBody); code != http.StatusForbidden {
		t.Fatalf("replay resolve status=%d err=%v", code, resolveErr)
	}

	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		t.Fatal("begin revocation-race capability transaction")
	}
	raceCapabilities, err := mintCredentialCapabilities(ctx, tx, batchClaimTestDaemonID, taskID, sessionKey.PublicKey().Bytes())
	if err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal("mint revocation-race capability")
	}
	if err := tx.Commit(ctx); err != nil || len(raceCapabilities) != 2 {
		t.Fatalf("commit revocation-race capabilities: count=%d err=%v", len(raceCapabilities), err)
	}
	var raceCapability credentialControlGrant
	for _, candidate := range raceCapabilities {
		if candidate.ConnectorID == connectorID {
			raceCapability = candidate
		}
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
