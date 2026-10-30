package handler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/credentialcrypto"
	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/internal/util"
	"github.com/multica-ai/multica/server/pkg/dbid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type failNthCredentialTxStarter struct {
	next   txStarter
	mu     sync.Mutex
	calls  int
	failOn int
}

func (s *failNthCredentialTxStarter) Begin(ctx context.Context) (pgx.Tx, error) {
	s.mu.Lock()
	s.calls++
	fail := s.calls == s.failOn
	s.mu.Unlock()
	if fail {
		return nil, errors.New("fault-injected credential capability transaction failure")
	}
	return s.next.Begin(ctx)
}

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

func TestCredentialCapabilityFirstUseDeadline(t *testing.T) {
	now := time.Date(2026, time.October, 26, 0, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name      string
		expiresAt time.Time
		expired   bool
	}{
		{name: "before deadline", expiresAt: now.Add(time.Nanosecond), expired: false},
		{name: "at deadline", expiresAt: now, expired: true},
		{name: "after deadline", expiresAt: now.Add(-time.Nanosecond), expired: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := credentialCapabilityExpired(now, test.expiresAt); got != test.expired {
				t.Fatalf("credentialCapabilityExpired(%s) = %t, want %t", test.expiresAt, got, test.expired)
			}
		})
	}
}

func TestBatchClaimRequeuesDirectCredentialWhenCapabilityMintFails(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	runtimeID := createClaimReclaimRuntime(t, ctx, "Direct credential mint failure runtime")
	dbfx.Exec(t, `UPDATE agent_runtime SET daemon_id = $2 WHERE id = $1`, runtimeID, batchClaimTestDaemonID)
	agentID, issueID := createClaimReclaimAgentAndIssue(t, ctx, runtimeID, "Direct credential mint failure agent")
	taskID := seedQueuedIssueTask(t, ctx, agentID, runtimeID, issueID)
	credentialID := uuidToString(dbid.NewV7())
	dbfx.Insert(t, "credential_vault", testutil.Cols{
		"id": credentialID, "workspace_id": testWorkspaceID, "name": "direct capability fixture",
		"target_service": "fixture", "purpose": "fail closed on mint failure", "connector_id": credentialID,
		"connector_revision": 1, "credential_type": "machine", "status": "active",
		"encrypted_value": testutil.Raw("'{}'::jsonb"), "created_by": testUserID,
	})
	dbfx.Insert(t, "credential_grant", testutil.Cols{
		"id": uuidToString(dbid.NewV7()), "credential_id": credentialID, "workspace_id": testWorkspaceID,
		"resource_scope": "issue", "resource_id": issueID, "subject_type": "agent", "subject_id": agentID,
		"effect": "allow", "status": "active", "granted_by": testUserID,
	})
	sessionKey, err := credentialcrypto.NewSessionKey()
	if err != nil {
		t.Fatal("generate credential session key")
	}
	starter := &failNthCredentialTxStarter{next: testHandler.TxStarter, failOn: 2}
	h := *testHandler
	h.TxStarter = starter
	body, err := json.Marshal(map[string]any{
		"daemon_id": batchClaimTestDaemonID, "runtime_ids": []string{runtimeID}, "max_tasks": 1,
		"session_public_key": base64.RawURLEncoding.EncodeToString(sessionKey.PublicKey().Bytes()),
	})
	if err != nil {
		t.Fatal("marshal batch claim")
	}
	status, response, err := h.DaemonRPCHandler(ctx, daemonws.ClientIdentity{
		DaemonID: batchClaimTestDaemonID, UserID: testUserID, WorkspaceID: testWorkspaceID,
		WorkspaceIDs: []string{testWorkspaceID}, RuntimeIDs: []string{runtimeID},
		Capabilities: protocol.DaemonCapabilityCredentialMCPV1,
	}, "tasks.claim", body)
	if err != nil || status != http.StatusOK {
		t.Fatalf("claim status=%d err=%v body=%s", status, err, response)
	}
	var claim struct {
		Tasks []json.RawMessage `json:"tasks"`
	}
	if err := json.Unmarshal(response, &claim); err != nil || len(claim.Tasks) != 0 {
		t.Fatalf("mint failure returned credential-bound task: err=%v body=%s", err, response)
	}
	if starter.calls != 2 {
		t.Fatalf("credential capability transactions = %d, want 2", starter.calls)
	}
	var taskStatus string
	if err := testPool.QueryRow(ctx, `SELECT status FROM agent_task_queue WHERE id = $1`, taskID).Scan(&taskStatus); err != nil {
		t.Fatal("read task status after failed capability mint")
	}
	if taskStatus != "queued" {
		t.Fatalf("task status after failed capability mint = %q, want queued", taskStatus)
	}
	var capabilityCount int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM credential_capability WHERE task_id = $1`, taskID).Scan(&capabilityCount); err != nil {
		t.Fatal("count capability rows")
	}
	if capabilityCount != 0 {
		t.Fatalf("failed capability mint persisted %d capability rows", capabilityCount)
	}
}

func TestBatchClaimRequeuesDirectCredentialWithoutCredentialMCPCapability(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	runtimeID := createClaimReclaimRuntime(t, ctx, "Direct credential capability gate runtime")
	dbfx.Exec(t, `UPDATE agent_runtime SET daemon_id = $2 WHERE id = $1`, runtimeID, batchClaimTestDaemonID)
	agentID, issueID := createClaimReclaimAgentAndIssue(t, ctx, runtimeID, "Direct credential capability gate agent")
	taskID := seedQueuedIssueTask(t, ctx, agentID, runtimeID, issueID)
	credentialID := uuidToString(dbid.NewV7())
	dbfx.Insert(t, "credential_vault", testutil.Cols{
		"id": credentialID, "workspace_id": testWorkspaceID, "name": "direct capability gate fixture",
		"target_service": "fixture", "purpose": "require credential MCP capability", "connector_id": credentialID,
		"connector_revision": 1, "credential_type": "machine", "status": "active",
		"encrypted_value": testutil.Raw("'{}'::jsonb"), "created_by": testUserID,
	})
	dbfx.Insert(t, "credential_grant", testutil.Cols{
		"id": uuidToString(dbid.NewV7()), "credential_id": credentialID, "workspace_id": testWorkspaceID,
		"resource_scope": "issue", "resource_id": issueID, "subject_type": "agent", "subject_id": agentID,
		"effect": "allow", "status": "active", "granted_by": testUserID,
	})
	sessionKey, err := credentialcrypto.NewSessionKey()
	if err != nil {
		t.Fatal("generate credential session key")
	}
	body, err := json.Marshal(map[string]any{
		"daemon_id": batchClaimTestDaemonID, "runtime_ids": []string{runtimeID}, "max_tasks": 1,
		"session_public_key": base64.RawURLEncoding.EncodeToString(sessionKey.PublicKey().Bytes()),
	})
	if err != nil {
		t.Fatal("marshal batch claim")
	}
	status, response, err := testHandler.DaemonRPCHandler(ctx, daemonws.ClientIdentity{
		DaemonID: batchClaimTestDaemonID, UserID: testUserID, WorkspaceID: testWorkspaceID,
		WorkspaceIDs: []string{testWorkspaceID}, RuntimeIDs: []string{runtimeID},
	}, "tasks.claim", body)
	if err != nil || status != http.StatusOK {
		t.Fatalf("claim status=%d err=%v body=%s", status, err, response)
	}
	var claim struct {
		Tasks []json.RawMessage `json:"tasks"`
	}
	if err := json.Unmarshal(response, &claim); err != nil || len(claim.Tasks) != 0 {
		t.Fatalf("daemon without credential-mcp-v1 received direct credential task: err=%v body=%s", err, response)
	}
	var taskStatus string
	if err := testPool.QueryRow(ctx, `SELECT status FROM agent_task_queue WHERE id = $1`, taskID).Scan(&taskStatus); err != nil {
		t.Fatal("read task status after capability gate")
	}
	if taskStatus != "queued" {
		t.Fatalf("task status after capability gate = %q, want queued", taskStatus)
	}
	var capabilityCount int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM credential_capability WHERE task_id = $1`, taskID).Scan(&capabilityCount); err != nil {
		t.Fatal("count capability rows")
	}
	if capabilityCount != 0 {
		t.Fatalf("daemon without credential-mcp-v1 minted %d credential capabilities", capabilityCount)
	}
}

func TestBatchClaimDoesNotMintDirectCredentialWithoutRuntimeOwner(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	runtimeID := createClaimReclaimRuntime(t, ctx, "Direct credential missing owner runtime")
	dbfx.Exec(t, `UPDATE agent_runtime SET daemon_id = $2, owner_id = NULL WHERE id = $1`, runtimeID, batchClaimTestDaemonID)
	agentID, issueID := createClaimReclaimAgentAndIssue(t, ctx, runtimeID, "Direct credential missing owner agent")
	taskID := seedQueuedIssueTask(t, ctx, agentID, runtimeID, issueID)
	credentialID := uuidToString(dbid.NewV7())
	dbfx.Insert(t, "credential_vault", testutil.Cols{
		"id": credentialID, "workspace_id": testWorkspaceID, "name": "direct owner fence fixture",
		"target_service": "fixture", "purpose": "require runtime owner before capability mint", "connector_id": credentialID,
		"connector_revision": 1, "credential_type": "machine", "status": "active",
		"encrypted_value": testutil.Raw("'{}'::jsonb"), "created_by": testUserID,
	})
	dbfx.Insert(t, "credential_grant", testutil.Cols{
		"id": uuidToString(dbid.NewV7()), "credential_id": credentialID, "workspace_id": testWorkspaceID,
		"resource_scope": "issue", "resource_id": issueID, "subject_type": "agent", "subject_id": agentID,
		"effect": "allow", "status": "active", "granted_by": testUserID,
	})
	sessionKey, err := credentialcrypto.NewSessionKey()
	if err != nil {
		t.Fatal("generate credential session key")
	}
	body, err := json.Marshal(map[string]any{
		"daemon_id": batchClaimTestDaemonID, "runtime_ids": []string{runtimeID}, "max_tasks": 1,
		"session_public_key": base64.RawURLEncoding.EncodeToString(sessionKey.PublicKey().Bytes()),
	})
	if err != nil {
		t.Fatal("marshal batch claim")
	}
	status, response, err := testHandler.DaemonRPCHandler(ctx, daemonws.ClientIdentity{
		DaemonID: batchClaimTestDaemonID, UserID: testUserID, WorkspaceID: testWorkspaceID,
		WorkspaceIDs: []string{testWorkspaceID}, RuntimeIDs: []string{runtimeID},
		Capabilities: protocol.DaemonCapabilityCredentialMCPV1,
	}, "tasks.claim", body)
	if err != nil || status != http.StatusOK {
		t.Fatalf("claim status=%d err=%v body=%s", status, err, response)
	}
	var claim struct {
		Tasks []json.RawMessage `json:"tasks"`
	}
	if err := json.Unmarshal(response, &claim); err != nil || len(claim.Tasks) != 0 {
		t.Fatalf("missing runtime owner returned credential-bound task: err=%v body=%s", err, response)
	}
	var taskStatus string
	if err := testPool.QueryRow(ctx, `SELECT status FROM agent_task_queue WHERE id = $1`, taskID).Scan(&taskStatus); err != nil {
		t.Fatal("read task status")
	}
	if taskStatus != "cancelled" {
		t.Fatalf("task status after missing runtime owner = %q, want cancelled", taskStatus)
	}
	var capabilityCount int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM credential_capability WHERE task_id = $1`, taskID).Scan(&capabilityCount); err != nil {
		t.Fatal("count capability rows")
	}
	if capabilityCount != 0 {
		t.Fatalf("missing runtime owner minted %d credential capabilities", capabilityCount)
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

	directA := util.MustParseUUID("66666666-6666-4666-8666-666666666666")
	directB := util.MustParseUUID("77777777-7777-4777-8777-777777777777")
	direct := func(credentialID pgtype.UUID) credentialCapabilityGrant {
		candidate := grant(credentialID, credentialID, "allow")
		candidate.DirectDelivery = true
		candidate.ConnectorStatus = ""
		return candidate
	}
	selected = selectTaskCredentialGrants([]credentialCapabilityGrant{direct(directA), direct(directB)})
	if len(selected) != 2 {
		t.Fatalf("selected %d direct credentials, want two", len(selected))
	}
	for _, candidate := range selected {
		if !candidate.DirectDelivery || candidate.CredentialID != candidate.ConnectorID {
			t.Fatalf("selected direct capability = %+v", candidate)
		}
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
	var capturedLogs bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&capturedLogs, nil)))
	defer slog.SetDefault(previousLogger)
	ctx := context.Background()
	runtimeID := createClaimReclaimRuntime(t, ctx, "Credential broker runtime")
	dbfx.Exec(t, `UPDATE agent_runtime SET daemon_id = $2 WHERE id = $1`, runtimeID, batchClaimTestDaemonID)
	agentID, issueID := createClaimReclaimAgentAndIssue(t, ctx, runtimeID, "Credential broker agent")
	taskID := seedQueuedIssueTask(t, ctx, agentID, runtimeID, issueID)
	connectorID := insertCredentialConnectorForAuth(t, testWorkspaceID)
	credentialID := uuidToString(dbid.NewV7())
	marker := "synthetic-credential-marker-" + uuidToString(dbid.NewV7())
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
	storageEnvelope, err := storage.Seal([]byte(marker), storageAAD)
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
	secondGrantID := uuidToString(dbid.NewV7())
	dbfx.Insert(t, "credential_grant", testutil.Cols{
		"id": secondGrantID, "credential_id": secondCredentialID, "workspace_id": testWorkspaceID,
		"resource_scope": "issue", "resource_id": issueID, "subject_type": "agent", "subject_id": agentID,
		"effect": "allow", "status": "active", "granted_by": testUserID,
	})
	directCredentialID := uuidToString(dbid.NewV7())
	directStorageContext := credentialcrypto.Context{
		WorkspaceID: testWorkspaceID, ResourceType: "credential", ResourceID: directCredentialID,
		SubjectType: "agent", SubjectID: agentID, Purpose: "credential.storage", Version: credentialcrypto.ProtocolVersion,
	}
	directStorageAAD, err := directStorageContext.AssociatedData()
	if err != nil {
		t.Fatal("build direct storage AAD")
	}
	directStorageEnvelope, err := storage.Seal([]byte("synthetic-direct-broker-secret"), directStorageAAD)
	if err != nil {
		t.Fatal("seal direct storage fixture")
	}
	directStoredJSON, err := json.Marshal(directStorageEnvelope)
	if err != nil {
		t.Fatal("marshal direct storage envelope")
	}
	dbfx.Insert(t, "credential_vault", testutil.Cols{
		"id": directCredentialID, "workspace_id": testWorkspaceID, "name": "synthetic direct broker fixture",
		"target_service": "fixture", "purpose": "direct broker test", "connector_id": directCredentialID,
		"connector_revision": 1, "credential_type": "machine", "status": "active",
		"encrypted_value": testutil.Raw(fmt.Sprintf("'%s'::jsonb", strings.ReplaceAll(string(directStoredJSON), "'", "''"))),
		"created_by":      testUserID,
	})
	dbfx.Insert(t, "credential_grant", testutil.Cols{
		"id": uuidToString(dbid.NewV7()), "credential_id": directCredentialID, "workspace_id": testWorkspaceID,
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
		WorkspaceID: testWorkspaceID, WorkspaceIDs: []string{testWorkspaceID}, RuntimeIDs: []string{runtimeID},
		Capabilities: "credential-mcp-v1"}
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
	if len(capabilities) != 3 {
		t.Fatalf("claim returned %d capabilities, want one for each credential: %+v", len(capabilities), capabilities)
	}
	var capability credentialControlGrant
	var secondCapability credentialControlGrant
	var directCapability credentialControlGrant
	for _, candidate := range capabilities {
		if candidate.ConnectorID == connectorID {
			capability = candidate
		}
		if candidate.ConnectorID == secondConnectorID {
			secondCapability = candidate
		}
		if candidate.CredentialID == directCredentialID {
			directCapability = candidate
		}
	}
	if capability.Capability == "" || capability.ConnectorRevision != 1 || secondCapability.Capability == "" ||
		directCapability.Capability == "" || directCapability.ConnectorID != "" {
		t.Fatalf("claim did not return expected credential capabilities: %+v", capabilities)
	}
	directCapabilityHash := sha256.Sum256([]byte(directCapability.Capability))
	var directCapabilityExpiresAt time.Time
	if err := testPool.QueryRow(ctx, `SELECT expires_at FROM credential_capability WHERE capability_hash = $1`, directCapabilityHash[:]).Scan(&directCapabilityExpiresAt); err != nil {
		t.Fatal("read direct credential capability expiry")
	}
	if remaining := time.Until(directCapabilityExpiresAt); remaining > credentialCapabilityFirstUseTTL || remaining < credentialCapabilityFirstUseTTL-time.Minute {
		t.Fatalf("direct capability remaining lifetime = %v, want approximately %v", remaining, credentialCapabilityFirstUseTTL)
	}
	resolveBody, _ := json.Marshal(credentialResolveRequest{
		Capability: capability.Capability, ConnectorID: connectorID, ConnectorRevision: 1,
	})

	// Hold the grant row while resolve starts before expiry, then release it
	// only after expiry. Authorization must use wall-clock time after waiting.
	if _, err := testPool.Exec(ctx, `UPDATE credential_grant SET expires_at = now() + interval '1 hour' WHERE id = $1`, grantID); err != nil {
		t.Fatal("set grant expiry after lock observation")
	}
	grantLock, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatal("begin grant-expiry lock")
	}
	if err := grantLock.QueryRow(ctx, `SELECT id FROM credential_grant WHERE id = $1 FOR UPDATE`, grantID).Scan(new(string)); err != nil {
		_ = grantLock.Rollback(ctx)
		t.Fatal("lock grant for expiry race")
	}
	expiryResolve := make(chan int, 1)
	go func() {
		code, _, _ := h.DaemonRPCHandler(ctx, identity, "credentials.resolve", resolveBody)
		expiryResolve <- code
	}()
	waitForCredentialResolveLock(t, ctx)
	var grantStillValid bool
	if err := testPool.QueryRow(ctx, `SELECT expires_at > clock_timestamp() FROM credential_grant WHERE id = $1`, grantID).Scan(&grantStillValid); err != nil {
		t.Fatal("check grant expiry before advancing deadline")
	}
	if !grantStillValid {
		t.Fatal("resolve did not reach the authorization lock before the grant deadline")
	}
	if _, err := grantLock.Exec(ctx, `UPDATE credential_grant SET expires_at = clock_timestamp() - interval '1 millisecond' WHERE id = $1`, grantID); err != nil {
		_ = grantLock.Rollback(ctx)
		t.Fatal("expire grant while resolve is waiting")
	}
	if err := grantLock.Commit(ctx); err != nil {
		t.Fatal("release grant-expiry lock")
	}
	if code := <-expiryResolve; code != http.StatusForbidden {
		t.Fatalf("resolve waiting across grant expiry returned %d, want 403", code)
	}
	dbfx.Exec(t, `UPDATE credential_grant SET expires_at = NULL WHERE id = $1`, grantID)

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

	// Resolve must reject after the issue reaches a terminal state, before
	// releasing the sealed value.
	var originalIssueStatus string
	if err := testPool.QueryRow(ctx, `SELECT status FROM issue WHERE id = $1`, issueID).Scan(&originalIssueStatus); err != nil {
		t.Fatal("read issue status before terminal resolve check")
	}
	issueLock, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatal("begin terminal issue lock")
	}
	if err := issueLock.QueryRow(ctx, `SELECT status FROM issue WHERE id = $1 FOR UPDATE`, issueID).Scan(&originalIssueStatus); err != nil {
		_ = issueLock.Rollback(ctx)
		t.Fatal("lock issue for terminal resolve race")
	}
	terminalResolve := make(chan struct {
		code     int
		response json.RawMessage
		err      error
	}, 1)
	go func() {
		code, response, resolveErr := h.DaemonRPCHandler(ctx, identity, "credentials.resolve", resolveBody)
		terminalResolve <- struct {
			code     int
			response json.RawMessage
			err      error
		}{code, response, resolveErr}
	}()
	waitForCredentialResolveLock(t, ctx)
	if _, err := issueLock.Exec(ctx, `UPDATE issue SET status = 'done' WHERE id = $1`, issueID); err != nil {
		_ = issueLock.Rollback(ctx)
		t.Fatal("mark issue terminal during resolve")
	}
	if err := issueLock.Commit(ctx); err != nil {
		t.Fatal("commit terminal issue transition")
	}
	terminalResult := <-terminalResolve
	if terminalResult.code != http.StatusForbidden || bytes.Contains(terminalResult.response, []byte(marker)) {
		t.Fatalf("terminal issue race status=%d err=%v markerVisible=%t, want denial without value", terminalResult.code, terminalResult.err, bytes.Contains(terminalResult.response, []byte(marker)))
	}
	dbfx.Exec(t, `UPDATE issue SET status = $2 WHERE id = $1`, issueID, originalIssueStatus)

	// A capability minted against an active connector must not survive either
	// deactivation or a revision change in the database.
	dbfx.Exec(t, `UPDATE credential_connector SET status = 'disabled' WHERE id = $1`, connectorID)
	if code, _, resolveErr := h.DaemonRPCHandler(ctx, identity, "credentials.resolve", resolveBody); code != http.StatusForbidden {
		t.Fatalf("disabled connector resolve status=%d err=%v, want denial", code, resolveErr)
	}
	dbfx.Exec(t, `UPDATE credential_connector SET status = 'active', revision = revision + 1 WHERE id = $1`, connectorID)
	if code, _, resolveErr := h.DaemonRPCHandler(ctx, identity, "credentials.resolve", resolveBody); code != http.StatusForbidden {
		t.Fatalf("changed connector revision resolve status=%d err=%v, want denial", code, resolveErr)
	}
	dbfx.Exec(t, `UPDATE credential_connector SET revision = 1 WHERE id = $1`, connectorID)
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
	if err != nil || string(plaintext) != marker {
		t.Fatalf("open execution envelope failed or returned unexpected synthetic value (err=%v)", err)
	}
	zeroCredentialBytes(plaintext)
	var persistedAudit []byte
	if err := testPool.QueryRow(ctx, `SELECT COALESCE(jsonb_agg(to_jsonb(a)), '[]'::jsonb) FROM credential_audit a WHERE task_id = $1`, taskID).Scan(&persistedAudit); err != nil {
		t.Fatal("read persisted credential audit rows")
	}
	if bytes.Contains(persistedAudit, []byte(marker)) {
		t.Fatal("synthetic credential marker was persisted in credential audit")
	}
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
	if err := tx.Commit(ctx); err != nil || len(raceCapabilities) != 3 {
		t.Fatalf("commit revocation-race capabilities: count=%d err=%v", len(raceCapabilities), err)
	}
	var raceCapability credentialControlGrant
	var expiredRaceCapability credentialControlGrant
	for _, candidate := range raceCapabilities {
		if candidate.ConnectorID == connectorID {
			raceCapability = candidate
		}
		if candidate.ConnectorID == secondConnectorID {
			expiredRaceCapability = candidate
		}
	}
	if raceCapability.Capability == "" || expiredRaceCapability.Capability == "" {
		t.Fatal("minted capabilities did not cover both connectors")
	}
	expiredRaceHash := sha256.Sum256([]byte(expiredRaceCapability.Capability))
	var capabilityExpiry time.Time
	if err := testPool.QueryRow(ctx, `UPDATE credential_capability SET expires_at = now() + interval '8 seconds' WHERE capability_hash = $1 RETURNING expires_at`, expiredRaceHash[:]).Scan(&capabilityExpiry); err != nil {
		t.Fatal("set capability expiry after lock observation budget")
	}
	expiredRaceBody, _ := json.Marshal(credentialResolveRequest{
		Capability: expiredRaceCapability.Capability, ConnectorID: secondConnectorID, ConnectorRevision: 1,
	})
	capabilityExpiryLock, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatal("begin capability-expiry lock")
	}
	if err := capabilityExpiryLock.QueryRow(ctx, `SELECT id FROM credential_grant WHERE id = $1 FOR UPDATE`, secondGrantID).Scan(new(string)); err != nil {
		_ = capabilityExpiryLock.Rollback(ctx)
		t.Fatal("lock grant for capability expiry race")
	}
	capabilityExpiryResolve := make(chan int, 1)
	go func() {
		code, _, _ := h.DaemonRPCHandler(ctx, identity, "credentials.resolve", expiredRaceBody)
		capabilityExpiryResolve <- code
	}()
	waitForCredentialResolveLock(t, ctx)
	if !time.Now().Before(capabilityExpiry) {
		t.Fatal("resolve did not reach the authorization lock before the capability deadline")
	}
	time.Sleep(time.Until(capabilityExpiry) + 25*time.Millisecond)
	if err := capabilityExpiryLock.Commit(ctx); err != nil {
		t.Fatal("release capability-expiry lock")
	}
	if code := <-capabilityExpiryResolve; code != http.StatusForbidden {
		t.Fatalf("resolve waiting across capability expiry returned %d, want 403", code)
	}
	var capabilityConsumed bool
	if err := testPool.QueryRow(ctx, `SELECT consumed_at IS NOT NULL FROM credential_capability WHERE capability_hash = $1`, expiredRaceHash[:]).Scan(&capabilityConsumed); err != nil {
		t.Fatal("read expired capability consumption")
	}
	if capabilityConsumed {
		t.Fatal("capability consumed after first-use deadline")
	}
	var expiredAuditCount int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM credential_audit WHERE task_id = $1 AND event_type = 'credential.broker_resolved' AND result = 'denied' AND reason_code = 'capability_expired'`, taskID).Scan(&expiredAuditCount); err != nil {
		t.Fatal("count expired capability audits")
	}
	if expiredAuditCount != 1 {
		t.Fatalf("expired capability audit count = %d, want 1", expiredAuditCount)
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
	if bytes.Contains(capturedLogs.Bytes(), []byte(marker)) {
		t.Fatal("synthetic credential marker appeared in service logs")
	}
}

func waitForCredentialResolveLock(t *testing.T, ctx context.Context) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var waiting bool
		if err := testPool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM pg_stat_activity
				WHERE pid <> pg_backend_pid() AND wait_event_type = 'Lock'
				  AND query LIKE '%credential_grant%'
			)`).Scan(&waiting); err != nil {
			t.Fatal("observe credential resolve lock wait")
		}
		if waiting {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("credential resolve did not block on the locked grant row")
}
