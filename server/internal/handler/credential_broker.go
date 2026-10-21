package handler

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/credentialcrypto"
	"github.com/multica-ai/multica/server/internal/util"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

const credentialCapabilityTTL = 5 * time.Minute

type credentialCapabilityGrant struct {
	CredentialID     pgtype.UUID
	GrantID          pgtype.UUID
	WorkspaceID      pgtype.UUID
	IssueID          pgtype.UUID
	AgentID          pgtype.UUID
	ProjectID        pgtype.UUID
	SquadID          pgtype.UUID
	ResourceScope    string
	ResourceID       pgtype.UUID
	SubjectType      string
	SubjectID        pgtype.UUID
	Effect           string
	Purpose          string
	CredentialStatus string
	ExpiresAt        pgtype.Timestamptz
	ConnectorID      pgtype.UUID
	ConnectorRev     int64
	ConnectorStatus  string
	ConnectorTarget  string
	EncryptedValue   []byte
	TaskStatus       string
	IssueCategory    string
	DaemonID         string
}

type credentialControlGrant struct {
	Capability        string    `json:"capability"`
	ConnectorID       string    `json:"connector_id"`
	ConnectorRevision int64     `json:"connector_revision"`
	TargetURL         string    `json:"target_url"`
	ExpiresAt         time.Time `json:"expires_at"`
}

type credentialResolveRequest struct {
	Capability        string `json:"capability"`
	ConnectorID       string `json:"connector_id"`
	ConnectorRevision int64  `json:"connector_revision"`
}

type credentialResolveResponse struct {
	Envelope  credentialcrypto.ExecutionEnvelope `json:"envelope"`
	ExpiresAt time.Time                          `json:"expires_at"`
}

func mintCredentialCapabilities(ctx context.Context, tx pgx.Tx, daemonID, taskID string, sessionPublicKey []byte) ([]credentialControlGrant, error) {
	if daemonID == "" || len(sessionPublicKey) != 32 {
		return nil, nil
	}
	if _, err := tx.Exec(ctx, `DELETE FROM credential_capability WHERE task_id = $1`, parseUUID(taskID)); err != nil {
		return nil, err
	}
	grants, err := findTaskCredentialGrants(ctx, tx, taskID)
	if err != nil {
		return nil, err
	}
	selected := selectTaskCredentialGrants(grants)
	capabilities := make([]credentialControlGrant, 0, len(selected))
	for _, grant := range selected {
		if grant.DaemonID != daemonID || grant.ConnectorStatus != "active" || grant.ConnectorRev < 1 || grant.TaskStatus != "dispatched" {
			continue
		}
		capabilityBytes := make([]byte, 32)
		if _, err := rand.Read(capabilityBytes); err != nil {
			return nil, errors.New("credential capability generation failed")
		}
		capability := base64.RawURLEncoding.EncodeToString(capabilityBytes)
		zeroCredentialBytes(capabilityBytes)
		hash := sha256.Sum256([]byte(capability))
		nonce := make([]byte, 32)
		if _, err := rand.Read(nonce); err != nil {
			return nil, errors.New("credential capability generation failed")
		}
		expiresAt := time.Now().UTC().Add(credentialCapabilityTTL)
		_, err = tx.Exec(ctx, `
			INSERT INTO credential_capability
			(id, capability_hash, daemon_id, task_id, agent_id, issue_id, workspace_id, connector_id, connector_revision, session_public_key, nonce, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
			dbid.NewV7(), hash[:], daemonID, parseUUID(taskID), grant.AgentID, grant.IssueID,
			grant.WorkspaceID, grant.ConnectorID, grant.ConnectorRev, sessionPublicKey, nonce, expiresAt)
		if err != nil {
			return nil, err
		}
		capabilities = append(capabilities, credentialControlGrant{Capability: capability,
			ConnectorID: uuidToString(grant.ConnectorID), ConnectorRevision: grant.ConnectorRev,
			TargetURL: grant.ConnectorTarget, ExpiresAt: expiresAt})
	}
	return capabilities, nil
}

// findTaskCredentialGrants evaluates both authorization dimensions from the
// task and grant rows. Credential identifiers and adapter fields are never
// accepted from the daemon as selectors.
func findTaskCredentialGrants(ctx context.Context, tx pgx.Tx, taskID string) ([]credentialCapabilityGrant, error) {
	taskUUID := parseUUID(taskID)
	query := `
		SELECT c.id, g.id, i.workspace_id, q.issue_id, q.agent_id, i.project_id, q.squad_id,
		       g.resource_scope, g.resource_id, g.subject_type, g.subject_id, g.effect, c.purpose,
		       c.status, g.expires_at, c.connector_id, c.connector_revision, cc.status, cc.target_url,
		       c.encrypted_value, q.status, COALESCE(s.category, i.status), COALESCE(rt.daemon_id, '')
		FROM agent_task_queue q
		JOIN issue i ON i.id = q.issue_id
		LEFT JOIN issue_status s ON s.workspace_id = i.workspace_id AND s.key = i.status
		JOIN agent_runtime rt ON rt.id = q.runtime_id AND rt.workspace_id = i.workspace_id
		JOIN credential_grant g ON g.workspace_id = i.workspace_id
		JOIN credential_vault c ON c.id = g.credential_id AND c.workspace_id = i.workspace_id
		JOIN credential_connector cc ON cc.id = c.connector_id AND cc.workspace_id = c.workspace_id
		WHERE q.id = $1 AND q.status IN ('dispatched', 'running')
		  AND g.status = 'active' AND c.status = 'active'
		  AND (g.expires_at IS NULL OR g.expires_at > now())
		  AND c.connector_revision = cc.revision AND cc.status = 'active'
		  AND (COALESCE(rt.daemon_id, '') <> '')`
	rows, err := tx.Query(ctx, query+` ORDER BY c.id, g.id FOR SHARE OF q, i, rt, g, c, cc`, taskUUID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var candidates []credentialCapabilityGrant
	for rows.Next() {
		var candidate credentialCapabilityGrant
		if err := rows.Scan(&candidate.CredentialID, &candidate.GrantID, &candidate.WorkspaceID,
			&candidate.IssueID, &candidate.AgentID, &candidate.ProjectID, &candidate.SquadID,
			&candidate.ResourceScope, &candidate.ResourceID, &candidate.SubjectType, &candidate.SubjectID,
			&candidate.Effect, &candidate.Purpose, &candidate.CredentialStatus, &candidate.ExpiresAt, &candidate.ConnectorID,
			&candidate.ConnectorRev, &candidate.ConnectorStatus, &candidate.ConnectorTarget, &candidate.EncryptedValue,
			&candidate.TaskStatus, &candidate.IssueCategory, &candidate.DaemonID); err != nil {
			return nil, err
		}
		if !credentialGrantMatchesTask(candidate) {
			continue
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return candidates, nil
}

func selectTaskCredentialGrants(candidates []credentialCapabilityGrant) []credentialCapabilityGrant {
	type credentialKey struct {
		credentialID pgtype.UUID
		connectorID  pgtype.UUID
		revision     int64
	}
	type connectorKey struct {
		connectorID pgtype.UUID
		revision    int64
	}
	type credentialDecision struct {
		grant   credentialCapabilityGrant
		allowed bool
		denied  bool
	}
	credentials := make(map[credentialKey]credentialDecision)
	for _, candidate := range candidates {
		key := credentialKey{candidate.CredentialID, candidate.ConnectorID, candidate.ConnectorRev}
		decision := credentials[key]
		if candidate.Effect == "deny" {
			decision.denied = true
		} else if candidate.Effect == "allow" {
			if !decision.allowed {
				decision.grant = candidate
			}
			decision.allowed = true
		}
		credentials[key] = decision
	}
	byConnector := make(map[connectorKey][]credentialCapabilityGrant)
	for key, decision := range credentials {
		if decision.allowed && !decision.denied {
			connector := connectorKey{key.connectorID, key.revision}
			byConnector[connector] = append(byConnector[connector], decision.grant)
		}
	}
	connectors := make([]connectorKey, 0, len(byConnector))
	for connector := range byConnector {
		connectors = append(connectors, connector)
	}
	sort.Slice(connectors, func(i, j int) bool {
		if idI, idJ := uuidToString(connectors[i].connectorID), uuidToString(connectors[j].connectorID); idI != idJ {
			return idI < idJ
		}
		return connectors[i].revision < connectors[j].revision
	})
	selected := make([]credentialCapabilityGrant, 0, len(connectors))
	for _, connector := range connectors {
		if len(byConnector[connector]) == 1 {
			selected = append(selected, byConnector[connector][0])
		}
	}
	return selected
}

func credentialGrantMatchesTask(g credentialCapabilityGrant) bool {
	if g.TaskStatus != "dispatched" && g.TaskStatus != "running" || g.IssueCategory == "done" || g.IssueCategory == "cancelled" {
		return false
	}
	subjectMatches := g.SubjectType == "agent" && g.SubjectID == g.AgentID ||
		g.SubjectType == "squad" && g.SquadID.Valid && g.SubjectID == g.SquadID
	if !subjectMatches {
		return false
	}
	switch g.ResourceScope {
	case "single_use", "issue":
		return g.ResourceID.Valid && g.ResourceID == g.IssueID
	case "project":
		return g.ProjectID.Valid && g.ResourceID.Valid && g.ResourceID == g.ProjectID
	case "workspace":
		return g.ResourceID.Valid && g.ResourceID == g.WorkspaceID
	case "global":
		return !g.ResourceID.Valid
	default:
		return false
	}
}

func (h *Handler) resolveCredentialCapability(ctx context.Context, daemonID string, req credentialResolveRequest) (credentialResolveResponse, error) {
	var rejected credentialResolveResponse
	capabilityBytes, decodeErr := base64.RawURLEncoding.Strict().DecodeString(req.Capability)
	if daemonID == "" || decodeErr != nil || len(capabilityBytes) != 32 || req.ConnectorRevision < 1 {
		return rejected, errors.New("credential capability rejected")
	}
	defer zeroCredentialBytes(capabilityBytes)
	connectorUUID, err := parseUUIDString(req.ConnectorID)
	if err != nil {
		return rejected, errors.New("credential capability rejected")
	}
	hash := sha256.Sum256([]byte(req.Capability))
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return rejected, errors.New("credential capability rejected")
	}
	defer tx.Rollback(ctx)
	var taskID, workspaceID, agentID, issueID pgtype.UUID
	var capabilityDaemonID string
	var sessionPublicKey []byte
	var capabilityConnector pgtype.UUID
	var capabilityRevision int64
	var expiresAt time.Time
	var consumedAt pgtype.Timestamptz
	err = tx.QueryRow(ctx, `
		SELECT daemon_id, task_id, workspace_id, agent_id, issue_id, connector_id, connector_revision,
		       session_public_key, expires_at, consumed_at
		FROM credential_capability WHERE capability_hash = $1 FOR UPDATE`, hash[:]).
		Scan(&capabilityDaemonID, &taskID, &workspaceID, &agentID, &issueID, &capabilityConnector, &capabilityRevision,
			&sessionPublicKey, &expiresAt, &consumedAt)
	if err != nil {
		return rejected, errors.New("credential capability rejected")
	}
	deny := func(reason string, credentialID, grantID pgtype.UUID, purpose string) (credentialResolveResponse, error) {
		if err := writeCredentialAudit(ctx, tx, workspaceID, credentialID, grantID,
			"credential.broker_resolved", "denied", reason, "daemon", daemonID,
			daemonID, taskID, agentID, issueID, purpose); err != nil {
			return rejected, errors.New("credential capability rejected")
		}
		if err := tx.Commit(ctx); err != nil {
			return rejected, errors.New("credential capability rejected")
		}
		return rejected, errors.New("credential capability rejected")
	}
	if capabilityDaemonID != daemonID {
		return deny("daemon_mismatch", pgtype.UUID{}, pgtype.UUID{}, "")
	}
	if consumedAt.Valid {
		return deny("capability_replayed", pgtype.UUID{}, pgtype.UUID{}, "")
	}
	if !time.Now().Before(expiresAt) {
		return deny("capability_expired", pgtype.UUID{}, pgtype.UUID{}, "")
	}
	if capabilityConnector != connectorUUID || capabilityRevision != req.ConnectorRevision {
		return deny("connector_mismatch", pgtype.UUID{}, pgtype.UUID{}, "")
	}
	candidates, err := findTaskCredentialGrants(ctx, tx, uuidToString(taskID))
	if err != nil {
		return rejected, errors.New("credential capability rejected")
	}
	var grant credentialCapabilityGrant
	matches := 0
	for _, candidate := range selectTaskCredentialGrants(candidates) {
		if candidate.ConnectorID == capabilityConnector && candidate.ConnectorRev == capabilityRevision {
			grant = candidate
			matches++
		}
	}
	if matches != 1 {
		return deny("grant_missing_or_ambiguous", pgtype.UUID{}, pgtype.UUID{}, "")
	}
	if grant.DaemonID != daemonID || grant.WorkspaceID != workspaceID || grant.AgentID != agentID || grant.IssueID != issueID ||
		grant.ConnectorID != capabilityConnector || grant.ConnectorID != connectorUUID || grant.ConnectorRev != capabilityRevision ||
		grant.ConnectorStatus != "active" || (grant.TaskStatus != "dispatched" && grant.TaskStatus != "running") {
		return deny("task_or_connector_binding_mismatch", grant.CredentialID, grant.GrantID, grant.Purpose)
	}
	if len(h.CredentialStorageKeys.MasterKeys) == 0 {
		return deny("storage_key_unavailable", grant.CredentialID, grant.GrantID, grant.Purpose)
	}
	var stored credentialcrypto.StorageEnvelope
	if json.Unmarshal(grant.EncryptedValue, &stored) != nil {
		return deny("stored_envelope_invalid", grant.CredentialID, grant.GrantID, grant.Purpose)
	}
	storageContext := credentialcrypto.Context{
		WorkspaceID: uuidToString(workspaceID), ResourceType: "credential", ResourceID: uuidToString(grant.CredentialID),
		SubjectType: grant.SubjectType, SubjectID: uuidToString(grant.SubjectID), ConnectorID: req.ConnectorID,
		ConnectorRev: capabilityRevision, Purpose: "credential.storage", Version: credentialcrypto.ProtocolVersion,
	}
	storageAAD, err := storageContext.AssociatedData()
	if err != nil {
		return deny("storage_context_invalid", grant.CredentialID, grant.GrantID, grant.Purpose)
	}
	plaintext, err := h.CredentialStorageKeys.Open(stored, storageAAD)
	if err != nil {
		return deny("stored_envelope_rejected", grant.CredentialID, grant.GrantID, grant.Purpose)
	}
	defer zeroCredentialBytes(plaintext)
	consumed, err := tx.Exec(ctx, `UPDATE credential_capability SET consumed_at = now() WHERE capability_hash = $1 AND consumed_at IS NULL`, hash[:])
	if err != nil {
		return rejected, errors.New("credential capability rejected")
	}
	if consumed.RowsAffected() != 1 {
		return deny("capability_replayed", grant.CredentialID, grant.GrantID, grant.Purpose)
	}
	if grant.ResourceScope == "single_use" {
		if _, err := tx.Exec(ctx, `UPDATE credential_grant SET status = 'exhausted', updated_at = now() WHERE id = $1`, grant.GrantID); err != nil {
			return rejected, errors.New("credential capability rejected")
		}
		if _, err := tx.Exec(ctx, `UPDATE credential_vault SET status = 'exhausted', encrypted_value = '{}'::jsonb, updated_at = now() WHERE id = $1`, grant.CredentialID); err != nil {
			return rejected, errors.New("credential capability rejected")
		}
	}
	if err := writeCredentialAudit(ctx, tx, workspaceID, grant.CredentialID, grant.GrantID,
		"credential.broker_resolved", "allowed", "capability_consumed", "daemon", daemonID,
		daemonID, taskID, agentID, issueID, grant.Purpose); err != nil {
		return rejected, errors.New("credential capability rejected")
	}
	envelopeExpiresAt := time.Now().UTC().Add(30 * time.Second)
	aad, err := json.Marshal(struct {
		TaskID            string    `json:"task_id"`
		WorkspaceID       string    `json:"workspace_id"`
		DaemonID          string    `json:"daemon_id"`
		ConnectorID       string    `json:"connector_id"`
		ConnectorRevision int64     `json:"connector_revision"`
		ExpiresAt         time.Time `json:"expires_at"`
	}{uuidToString(taskID), uuidToString(workspaceID), daemonID, req.ConnectorID, capabilityRevision, envelopeExpiresAt})
	if err != nil {
		return deny("execution_context_invalid", grant.CredentialID, grant.GrantID, grant.Purpose)
	}
	envelope, err := credentialcrypto.SealForSession(sessionPublicKey, plaintext, aad)
	if err != nil {
		return deny("execution_envelope_failed", grant.CredentialID, grant.GrantID, grant.Purpose)
	}
	if err := tx.Commit(ctx); err != nil {
		return rejected, errors.New("credential capability rejected")
	}
	return credentialResolveResponse{Envelope: envelope, ExpiresAt: envelopeExpiresAt}, nil
}

func zeroCredentialBytes(value []byte) {
	for i := range value {
		value[i] = 0
	}
}

func parseUUIDString(value string) (pgtype.UUID, error) {
	return util.ParseUUID(strings.TrimSpace(value))
}

func (h *Handler) ResolveCredentialCapability(w http.ResponseWriter, r *http.Request, daemonID string, body json.RawMessage) {
	if len(body) == 0 || credentialcrypto.RejectDuplicateJSONKeys(body) != nil {
		writeError(w, http.StatusBadRequest, "invalid credential resolve request")
		return
	}
	var req credentialResolveRequest
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&req) != nil {
		writeError(w, http.StatusBadRequest, "invalid credential resolve request")
		return
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid credential resolve request")
		return
	}
	response, err := h.resolveCredentialCapability(r.Context(), daemonID, req)
	if err != nil {
		writeError(w, http.StatusForbidden, "credential capability rejected")
		return
	}
	writeJSON(w, http.StatusOK, response)
}
