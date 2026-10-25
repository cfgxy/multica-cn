package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/credentialcrypto"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

const maxCredentialEnvelopeRequest = 2 << 20

type createIssueCredentialRequest struct {
	Name          string          `json:"name"`
	TargetService string          `json:"target_service"`
	ConnectorID   string          `json:"connector_id"`
	ConnectorRev  int64           `json:"connector_revision"`
	Purpose       string          `json:"purpose"`
	SubjectType   string          `json:"subject_type"`
	SubjectID     string          `json:"subject_id"`
	ResourceScope string          `json:"resource_scope"`
	ResourceID    string          `json:"resource_id,omitempty"`
	ExpiresAt     *string         `json:"expires_at"`
	Envelope      json.RawMessage `json:"envelope"`
}

var errInvalidCredentialRequest = errors.New("invalid encrypted credential request")

type credentialMetadataResponse struct {
	ID                string     `json:"id"`
	Name              string     `json:"name"`
	TargetService     string     `json:"target_service"`
	ConnectorID       string     `json:"connector_id"`
	ConnectorRevision int64      `json:"connector_revision"`
	Purpose           string     `json:"purpose"`
	Status            string     `json:"status"`
	ResourceScope     string     `json:"resource_scope"`
	SubjectType       string     `json:"subject_type"`
	SubjectID         string     `json:"subject_id"`
	ExpiresAt         *time.Time `json:"expires_at"`
	CreatedAt         time.Time  `json:"created_at"`
}

type credentialAuditResponse struct {
	ID           string    `json:"id"`
	CreatedAt    time.Time `json:"created_at"`
	CredentialID *string   `json:"credential_id"`
	EventType    string    `json:"event_type"`
	Result       string    `json:"result"`
	ReasonCode   string    `json:"reason_code"`
	ActorType    string    `json:"actor_type"`
	ActorID      string    `json:"actor_id"`
	ProjectID    *string   `json:"project_id"`
	ProjectTitle *string   `json:"project_title"`
	IssueID      *string   `json:"issue_id"`
	IssueTitle   *string   `json:"issue_title"`
	TaskID       *string   `json:"task_id"`
	AgentID      *string   `json:"agent_id"`
	Purpose      string    `json:"purpose"`
}

func (h *Handler) CreateIssueCredential(w http.ResponseWriter, r *http.Request) {
	if len(h.CredentialTransportKeys.PrivateKeys) == 0 || len(h.CredentialStorageKeys.MasterKeys) == 0 {
		writeError(w, http.StatusServiceUnavailable, "credential encryption is unavailable")
		return
	}
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	body := http.MaxBytesReader(w, r.Body, maxCredentialEnvelopeRequest)
	data, err := readCredentialBody(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid encrypted credential request")
		return
	}
	req, envelope, err := decodeCreateIssueCredentialRequest(data)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid encrypted credential request")
		return
	}
	if !credentialRequestValid(req) {
		writeError(w, http.StatusBadRequest, "invalid credential metadata")
		return
	}
	requestConnectorID, requestUsesConnector, err := credentialRequestConnector(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid credential connector")
		return
	}
	if req.ResourceScope != "issue" && req.ResourceScope != "single_use" && req.ResourceScope != "project" &&
		req.ResourceScope != "workspace" && req.ResourceScope != "global" {
		writeError(w, http.StatusBadRequest, "unsupported credential resource scope")
		return
	}
	if req.SubjectType != "agent" && req.SubjectType != "squad" {
		writeError(w, http.StatusBadRequest, "invalid credential subject")
		return
	}
	var expiresAt *time.Time
	if req.ExpiresAt != nil {
		parsed, err := time.Parse(time.RFC3339, *req.ExpiresAt)
		if err != nil || !parsed.After(time.Now()) {
			writeError(w, http.StatusBadRequest, "invalid credential expiry")
			return
		}
		expiresAt = &parsed
	}
	if req.ResourceScope == "single_use" && expiresAt != nil {
		writeError(w, http.StatusBadRequest, "single-use credentials do not accept an explicit expiry")
		return
	}
	if (req.ResourceScope == "project" || req.ResourceScope == "workspace" || req.ResourceScope == "global") && expiresAt == nil {
		writeError(w, http.StatusBadRequest, "credential expiry is required for this resource scope")
		return
	}

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "credential storage failed")
		return
	}
	defer tx.Rollback(r.Context())
	authorizedIssue, member, err := lockCredentialWriteContext(r.Context(), tx, issue.ID, issue.WorkspaceID, userID)
	if err != nil {
		writeError(w, http.StatusNotFound, "issue not found")
		return
	}
	resourceID, resourceAADID, scopeAuthorized := credentialResource(authorizedIssue, member, userID, req.ResourceScope, req.ResourceID)
	if !scopeAuthorized {
		writeError(w, http.StatusForbidden, "credential authorization denied")
		return
	}
	if !h.validCredentialSubject(r, authorizedIssue, req.SubjectType, req.SubjectID) {
		writeError(w, http.StatusBadRequest, "invalid credential subject")
		return
	}
	if requestUsesConnector && !credentialConnectorActive(r.Context(), tx, requestConnectorID, authorizedIssue.WorkspaceID, req.ConnectorRev) {
		writeError(w, http.StatusBadRequest, "invalid credential connector")
		return
	}
	workspaceID := uuidToString(authorizedIssue.WorkspaceID)
	transportContext := credentialcrypto.Context{
		WorkspaceID: workspaceID, ResourceType: req.ResourceScope, ResourceID: resourceAADID,
		SubjectType: req.SubjectType, SubjectID: req.SubjectID, Purpose: "credential.create",
		Version: credentialcrypto.ProtocolVersion,
	}
	if requestUsesConnector {
		transportContext.ConnectorID = req.ConnectorID
		transportContext.ConnectorRev = req.ConnectorRev
	}
	credentialID := dbid.NewV7()
	credentialIDString := uuidToString(credentialID)
	storageContext := credentialcrypto.Context{
		WorkspaceID: workspaceID, ResourceType: "credential", ResourceID: credentialIDString,
		SubjectType: req.SubjectType, SubjectID: req.SubjectID, Purpose: "credential.storage",
		Version: credentialcrypto.ProtocolVersion,
	}
	connectorID := credentialID
	connectorRevision := int64(1)
	if requestUsesConnector {
		connectorID = requestConnectorID
		connectorRevision = req.ConnectorRev
		storageContext.ConnectorID = req.ConnectorID
		storageContext.ConnectorRev = req.ConnectorRev
	}
	storageAAD, err := storageContext.AssociatedData()
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid credential context")
		return
	}
	stored, err := credentialcrypto.ReencryptForStorage(
		h.CredentialTransportKeys,
		h.CredentialStorageKeys,
		envelope,
		transportContext,
		storageAAD,
	)
	if err != nil {
		writeError(w, http.StatusBadRequest, "encrypted credential could not be accepted")
		return
	}
	storedJSON, err := json.Marshal(stored)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "credential storage failed")
		return
	}

	grantID := dbid.NewV7()
	var expiry any
	if expiresAt != nil {
		expiry = *expiresAt
	}
	if _, err := tx.Exec(r.Context(), `
		INSERT INTO credential_vault (id, workspace_id, name, target_service, connector_id, connector_revision, purpose, credential_type, status, encrypted_value, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'machine', 'active', $8::jsonb, $9)`,
		credentialID, authorizedIssue.WorkspaceID, strings.TrimSpace(req.Name), credentialTargetService(req),
		connectorID, connectorRevision, strings.TrimSpace(req.Purpose), storedJSON, member.UserID); err != nil {
		writeError(w, http.StatusInternalServerError, "credential storage failed")
		return
	}
	if _, err := tx.Exec(r.Context(), `
		INSERT INTO credential_grant (id, credential_id, workspace_id, resource_scope, resource_id, subject_type, subject_id, effect, status, expires_at, granted_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'allow', 'active', $8, $9)`,
		grantID, credentialID, authorizedIssue.WorkspaceID, req.ResourceScope, resourceID, req.SubjectType,
		parseUUID(req.SubjectID), expiry, member.UserID); err != nil {
		writeError(w, http.StatusInternalServerError, "credential storage failed")
		return
	}
	if err := writeCredentialAudit(r.Context(), tx, authorizedIssue.WorkspaceID, credentialID, grantID,
		"credential.created", "recorded", "created", "member", uuidToString(member.UserID), "", pgtype.UUID{}, pgtype.UUID{}, authorizedIssue.ID, req.Purpose); err != nil {
		writeError(w, http.StatusInternalServerError, "credential storage failed")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "credential storage failed")
		return
	}
	writeJSON(w, http.StatusCreated, credentialMetadataResponse{
		ID: credentialIDString, Name: strings.TrimSpace(req.Name), TargetService: credentialTargetService(req),
		ConnectorID: uuidToString(connectorID), ConnectorRevision: connectorRevision,
		Purpose: strings.TrimSpace(req.Purpose), Status: "active", ResourceScope: req.ResourceScope,
		SubjectType: req.SubjectType, SubjectID: req.SubjectID, ExpiresAt: expiresAt, CreatedAt: time.Now().UTC(),
	})
}

func (h *Handler) ReplaceIssueCredential(w http.ResponseWriter, r *http.Request) {
	if len(h.CredentialTransportKeys.PrivateKeys) == 0 || len(h.CredentialStorageKeys.MasterKeys) == 0 {
		writeError(w, http.StatusServiceUnavailable, "credential encryption is unavailable")
		return
	}
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	credentialID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "credentialId"), "credential_id")
	if !ok {
		return
	}
	data, err := readCredentialBody(http.MaxBytesReader(w, r.Body, maxCredentialEnvelopeRequest))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid encrypted credential request")
		return
	}
	req, envelope, err := decodeCreateIssueCredentialRequest(data)
	if err != nil || !credentialRequestValid(req) {
		writeError(w, http.StatusBadRequest, "invalid encrypted credential request")
		return
	}
	requestConnectorID, requestUsesConnector, err := credentialRequestConnector(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid credential connector")
		return
	}
	if req.ResourceScope != "issue" && req.ResourceScope != "single_use" && req.ResourceScope != "project" &&
		req.ResourceScope != "workspace" && req.ResourceScope != "global" {
		writeError(w, http.StatusBadRequest, "unsupported credential resource scope")
		return
	}
	if req.SubjectType != "agent" && req.SubjectType != "squad" {
		writeError(w, http.StatusBadRequest, "invalid credential subject")
		return
	}
	var expiresAt *time.Time
	if req.ExpiresAt != nil {
		parsed, parseErr := time.Parse(time.RFC3339, *req.ExpiresAt)
		if parseErr != nil || !parsed.After(time.Now()) {
			writeError(w, http.StatusBadRequest, "invalid credential expiry")
			return
		}
		expiresAt = &parsed
	}
	if req.ResourceScope == "single_use" && expiresAt != nil {
		writeError(w, http.StatusBadRequest, "single-use credentials do not accept an explicit expiry")
		return
	}
	if (req.ResourceScope == "project" || req.ResourceScope == "workspace" || req.ResourceScope == "global") && expiresAt == nil {
		writeError(w, http.StatusBadRequest, "credential expiry is required for this resource scope")
		return
	}

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "credential replacement failed")
		return
	}
	defer tx.Rollback(r.Context())
	authorizedIssue, member, err := lockCredentialWriteContext(r.Context(), tx, issue.ID, issue.WorkspaceID, userID)
	if err != nil {
		writeError(w, http.StatusNotFound, "credential not found")
		return
	}
	resourceID, resourceAADID, scopeAuthorized := credentialResource(authorizedIssue, member, userID, req.ResourceScope, req.ResourceID)
	if !scopeAuthorized || !h.validCredentialSubject(r, authorizedIssue, req.SubjectType, req.SubjectID) {
		writeError(w, http.StatusForbidden, "credential authorization denied")
		return
	}
	var status string
	var createdBy pgtype.UUID
	var createdAt time.Time
	var existingConnectorID pgtype.UUID
	var existingConnectorRevision int64
	if err := tx.QueryRow(r.Context(), `SELECT status, created_by, created_at, connector_id, connector_revision
		FROM credential_vault WHERE id = $1 AND workspace_id = $2 FOR UPDATE`,
		credentialID, authorizedIssue.WorkspaceID).Scan(&status, &createdBy, &createdAt, &existingConnectorID, &existingConnectorRevision); err != nil {
		writeError(w, http.StatusNotFound, "credential not found")
		return
	}
	if status != "active" {
		writeError(w, http.StatusConflict, "credential is no longer active")
		return
	}
	rows, err := tx.Query(r.Context(), `SELECT id, workspace_id, resource_scope, resource_id, status, expires_at FROM credential_grant WHERE credential_id = $1 ORDER BY id FOR UPDATE`, credentialID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "credential replacement failed")
		return
	}
	var grantID pgtype.UUID
	var reachable, sameWorkspace bool = false, true
	var issueLocalOnly bool = true
	grantActiveAndUnexpired := true
	grantCount := 0
	for rows.Next() {
		var id, workspaceID, oldResourceID pgtype.UUID
		var oldScope string
		var oldStatus string
		var oldExpiresAt pgtype.Timestamptz
		if err := rows.Scan(&id, &workspaceID, &oldScope, &oldResourceID, &oldStatus, &oldExpiresAt); err != nil {
			rows.Close()
			writeError(w, http.StatusInternalServerError, "credential replacement failed")
			return
		}
		grantCount++
		grantID = id
		sameWorkspace = sameWorkspace && workspaceID == authorizedIssue.WorkspaceID
		reachable = reachable || credentialGrantMatchesIssue(authorizedIssue, oldScope, oldResourceID)
		issueLocalOnly = issueLocalOnly && (oldScope == "issue" || oldScope == "single_use") && oldResourceID.Valid && oldResourceID == authorizedIssue.ID
		grantActiveAndUnexpired = grantActiveAndUnexpired && oldStatus == "active" && (!oldExpiresAt.Valid || oldExpiresAt.Time.After(time.Now()))
	}
	rowsErr := rows.Err()
	rows.Close()
	if rowsErr != nil {
		writeError(w, http.StatusInternalServerError, "credential replacement failed")
		return
	}
	if !reachable || !sameWorkspace || grantCount != 1 {
		writeError(w, http.StatusNotFound, "credential not found")
		return
	}
	if !grantActiveAndUnexpired {
		writeError(w, http.StatusConflict, "credential grant is no longer active")
		return
	}
	if member.Role != "owner" && !(credentialIssueWriter(member, authorizedIssue, userID) && issueLocalOnly && createdBy.Valid && createdBy == member.UserID) {
		writeError(w, http.StatusForbidden, "credential authorization denied")
		return
	}
	workspaceID := uuidToString(authorizedIssue.WorkspaceID)
	credentialIDString := uuidToString(credentialID)
	transportContext := credentialcrypto.Context{WorkspaceID: workspaceID, ResourceType: req.ResourceScope, ResourceID: resourceAADID,
		SubjectType: req.SubjectType, SubjectID: req.SubjectID,
		Purpose: "credential.create", Version: credentialcrypto.ProtocolVersion}
	if requestUsesConnector {
		transportContext.ConnectorID = req.ConnectorID
		transportContext.ConnectorRev = req.ConnectorRev
	}
	storageContext := credentialcrypto.Context{WorkspaceID: workspaceID, ResourceType: "credential", ResourceID: credentialIDString,
		SubjectType: req.SubjectType, SubjectID: req.SubjectID,
		Purpose: "credential.storage", Version: credentialcrypto.ProtocolVersion}
	connectorID := credentialID
	connectorRevision := int64(1)
	if requestUsesConnector && !(requestConnectorID == credentialID && req.ConnectorRev == 1 && existingConnectorID == credentialID && existingConnectorRevision == 1) {
		if !credentialConnectorActive(r.Context(), tx, requestConnectorID, authorizedIssue.WorkspaceID, req.ConnectorRev) {
			writeError(w, http.StatusBadRequest, "invalid credential connector")
			return
		}
		connectorID = requestConnectorID
		connectorRevision = req.ConnectorRev
		storageContext.ConnectorID = req.ConnectorID
		storageContext.ConnectorRev = req.ConnectorRev
	}
	storageAAD, err := storageContext.AssociatedData()
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid credential context")
		return
	}
	stored, err := credentialcrypto.ReencryptForStorage(h.CredentialTransportKeys, h.CredentialStorageKeys, envelope, transportContext, storageAAD)
	if err != nil {
		writeError(w, http.StatusBadRequest, "encrypted credential could not be accepted")
		return
	}
	storedJSON, err := json.Marshal(stored)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "credential replacement failed")
		return
	}
	var expiry any
	if expiresAt != nil {
		expiry = *expiresAt
	}
	if _, err := tx.Exec(r.Context(), `UPDATE credential_vault SET name = $2, target_service = $3, connector_id = $4,
		connector_revision = $5, purpose = $6, encrypted_value = $7::jsonb, updated_at = now() WHERE id = $1`,
		credentialID, strings.TrimSpace(req.Name), credentialTargetService(req), connectorID, connectorRevision,
		strings.TrimSpace(req.Purpose), storedJSON); err != nil {
		writeError(w, http.StatusInternalServerError, "credential replacement failed")
		return
	}
	if _, err := tx.Exec(r.Context(), `UPDATE credential_grant SET resource_scope = $2, resource_id = $3, subject_type = $4,
		subject_id = $5, status = 'active', expires_at = $6, granted_by = $7, updated_at = now() WHERE id = $1`,
		grantID, req.ResourceScope, resourceID, req.SubjectType, parseUUID(req.SubjectID), expiry, member.UserID); err != nil {
		writeError(w, http.StatusInternalServerError, "credential replacement failed")
		return
	}
	if err := writeCredentialAudit(r.Context(), tx, authorizedIssue.WorkspaceID, credentialID, grantID,
		"credential.replaced", "recorded", "replaced", "member", userID, "", pgtype.UUID{}, pgtype.UUID{}, authorizedIssue.ID, req.Purpose); err != nil {
		writeError(w, http.StatusInternalServerError, "credential replacement failed")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "credential replacement failed")
		return
	}
	writeJSON(w, http.StatusOK, credentialMetadataResponse{ID: credentialIDString, Name: strings.TrimSpace(req.Name),
		TargetService: credentialTargetService(req), ConnectorID: uuidToString(connectorID), ConnectorRevision: connectorRevision,
		Purpose: strings.TrimSpace(req.Purpose), Status: "active", ResourceScope: req.ResourceScope,
		SubjectType: req.SubjectType, SubjectID: req.SubjectID, ExpiresAt: expiresAt, CreatedAt: createdAt})
}

func decodeCreateIssueCredentialRequest(data []byte) (createIssueCredentialRequest, credentialcrypto.TransportEnvelope, error) {
	if len(data) == 0 || len(data) > maxCredentialEnvelopeRequest || credentialcrypto.RejectDuplicateJSONKeys(data) != nil {
		return createIssueCredentialRequest{}, credentialcrypto.TransportEnvelope{}, errInvalidCredentialRequest
	}
	var req createIssueCredentialRequest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		return createIssueCredentialRequest{}, credentialcrypto.TransportEnvelope{}, errInvalidCredentialRequest
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return createIssueCredentialRequest{}, credentialcrypto.TransportEnvelope{}, errInvalidCredentialRequest
	}
	envelope, err := credentialcrypto.DecodeTransportEnvelope(req.Envelope)
	if err != nil {
		return createIssueCredentialRequest{}, credentialcrypto.TransportEnvelope{}, errInvalidCredentialRequest
	}
	return req, envelope, nil
}

func (h *Handler) ListIssueCredentials(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	member, err := h.getWorkspaceMember(r.Context(), userID, uuidToString(issue.WorkspaceID))
	if err != nil {
		writeError(w, http.StatusNotFound, "issue not found")
		return
	}
	rows, err := h.DB.Query(r.Context(), `
		SELECT c.id::text, c.name, c.target_service, c.connector_id::text, c.connector_revision, c.purpose, c.status,
		       g.resource_scope, g.subject_type, g.subject_id::text, g.expires_at, c.created_at
		FROM credential_vault c JOIN credential_grant g ON g.credential_id = c.id
		WHERE c.workspace_id = $1 AND (
			(g.resource_scope IN ('issue', 'single_use') AND g.resource_id = $2) OR
			(g.resource_scope = 'project' AND g.resource_id = $3) OR
			(g.resource_scope = 'workspace' AND g.resource_id = $1) OR
			g.resource_scope = 'global'
		)
		  AND (c.created_by = $4 OR $5 = 'owner' OR ($6 AND g.resource_scope IN ('issue', 'single_use')))
		ORDER BY c.created_at DESC`, issue.WorkspaceID, issue.ID, issue.ProjectID, member.UserID,
		member.Role, credentialIssueWriter(member, issue, userID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list credentials")
		return
	}
	defer rows.Close()
	items := make([]credentialMetadataResponse, 0)
	for rows.Next() {
		var item credentialMetadataResponse
		var subjectID string
		if err := rows.Scan(&item.ID, &item.Name, &item.TargetService, &item.ConnectorID, &item.ConnectorRevision, &item.Purpose, &item.Status,
			&item.ResourceScope, &item.SubjectType, &subjectID, &item.ExpiresAt, &item.CreatedAt); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list credentials")
			return
		}
		item.SubjectID = subjectID
		items = append(items, item)
	}
	if rows.Err() != nil {
		writeError(w, http.StatusInternalServerError, "failed to list credentials")
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (h *Handler) RevokeIssueCredential(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	credentialID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "credentialId"), "credential_id")
	if !ok {
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "credential revocation failed")
		return
	}
	defer tx.Rollback(r.Context())
	authorizedIssue, member, err := lockCredentialWriteContext(r.Context(), tx, issue.ID, issue.WorkspaceID, userID)
	if err != nil {
		writeError(w, http.StatusNotFound, "credential not found")
		return
	}
	var status string
	if err := tx.QueryRow(r.Context(), `
		SELECT status FROM credential_vault
		WHERE id = $1 AND workspace_id = $2
		FOR UPDATE`, credentialID, authorizedIssue.WorkspaceID).Scan(&status); err != nil {
		writeError(w, http.StatusNotFound, "credential not found")
		return
	}
	rows, err := tx.Query(r.Context(), `
		SELECT id, workspace_id, resource_scope, resource_id
		FROM credential_grant WHERE credential_id = $1
		ORDER BY id FOR UPDATE`, credentialID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "credential revocation failed")
		return
	}
	var grantID pgtype.UUID
	grantReachableFromIssue := false
	grantWorkspaceConsistent := true
	allGrantsIssueLocal := true
	for rows.Next() {
		var candidateID, grantWorkspaceID, resourceID pgtype.UUID
		var scope string
		if err := rows.Scan(&candidateID, &grantWorkspaceID, &scope, &resourceID); err != nil {
			rows.Close()
			writeError(w, http.StatusInternalServerError, "credential revocation failed")
			return
		}
		if grantWorkspaceID != authorizedIssue.WorkspaceID {
			grantWorkspaceConsistent = false
		}
		if credentialGrantMatchesIssue(authorizedIssue, scope, resourceID) {
			grantReachableFromIssue = true
			if !grantID.Valid {
				grantID = candidateID
			}
		}
		if (scope != "issue" && scope != "single_use") || !resourceID.Valid || resourceID != authorizedIssue.ID {
			allGrantsIssueLocal = false
		}
	}
	rowsErr := rows.Err()
	rows.Close()
	if rowsErr != nil {
		writeError(w, http.StatusInternalServerError, "credential revocation failed")
		return
	}
	if !grantReachableFromIssue || !grantWorkspaceConsistent {
		writeError(w, http.StatusNotFound, "credential not found")
		return
	}
	if member.Role != "owner" && !(credentialIssueWriter(member, authorizedIssue, userID) && allGrantsIssueLocal) {
		writeError(w, http.StatusForbidden, "credential authorization denied")
		return
	}
	if status == "revoked" || status == "expired" || status == "exhausted" {
		writeJSON(w, http.StatusOK, map[string]string{"status": status})
		return
	}
	if _, err := tx.Exec(r.Context(), `UPDATE credential_vault SET status = 'revoked', encrypted_value = '{}'::jsonb, updated_at = now() WHERE id = $1`, credentialID); err != nil {
		writeError(w, http.StatusInternalServerError, "credential revocation failed")
		return
	}
	if _, err := tx.Exec(r.Context(), `UPDATE credential_grant SET status = 'revoked', updated_at = now() WHERE credential_id = $1 AND status = 'active'`, credentialID); err != nil {
		writeError(w, http.StatusInternalServerError, "credential revocation failed")
		return
	}
	if err := writeCredentialAudit(r.Context(), tx, authorizedIssue.WorkspaceID, credentialID, grantID,
		"credential.revoked", "recorded", "revoked", "member", userID, "", pgtype.UUID{}, pgtype.UUID{}, authorizedIssue.ID, ""); err != nil {
		writeError(w, http.StatusInternalServerError, "credential revocation failed")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "credential revocation failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
}

func credentialIssueWriter(member db.Member, issue db.Issue, userID string) bool {
	return member.Role == "owner" ||
		(issue.AssigneeType.Valid && issue.AssigneeType.String == "member" &&
			issue.AssigneeID.Valid && uuidToString(issue.AssigneeID) == userID)
}

func credentialCanWriteScope(member db.Member, issue db.Issue, userID, scope string) bool {
	if scope == "project" || scope == "workspace" || scope == "global" {
		return member.Role == "owner"
	}
	return (scope == "issue" || scope == "single_use") && credentialIssueWriter(member, issue, userID)
}

func lockCredentialWriteContext(ctx context.Context, tx pgx.Tx, issueID, workspaceID pgtype.UUID, userID string) (db.Issue, db.Member, error) {
	var issue db.Issue
	if err := tx.QueryRow(ctx, `
		SELECT id, workspace_id, project_id, assignee_type, assignee_id
		FROM issue WHERE id = $1 AND workspace_id = $2
		FOR SHARE`, issueID, workspaceID).Scan(
		&issue.ID, &issue.WorkspaceID, &issue.ProjectID, &issue.AssigneeType, &issue.AssigneeID); err != nil {
		return db.Issue{}, db.Member{}, err
	}
	var member db.Member
	if err := tx.QueryRow(ctx, `
		SELECT user_id, workspace_id, role
		FROM member WHERE user_id = $1 AND workspace_id = $2
		FOR SHARE`, parseUUID(userID), issue.WorkspaceID).Scan(
		&member.UserID, &member.WorkspaceID, &member.Role); err != nil {
		return db.Issue{}, db.Member{}, err
	}
	return issue, member, nil
}

func credentialGrantMatchesIssue(issue db.Issue, scope string, resourceID pgtype.UUID) bool {
	switch scope {
	case "issue", "single_use":
		return resourceID.Valid && resourceID == issue.ID
	case "project":
		return issue.ProjectID.Valid && resourceID.Valid && resourceID == issue.ProjectID
	case "workspace":
		return resourceID.Valid && resourceID == issue.WorkspaceID
	case "global":
		return !resourceID.Valid
	default:
		return false
	}
}

func credentialResource(issue db.Issue, member db.Member, userID, scope, requestedID string) (pgtype.UUID, string, bool) {
	if !credentialCanWriteScope(member, issue, userID, scope) {
		return pgtype.UUID{}, "", false
	}
	workspaceID := uuidToString(issue.WorkspaceID)
	issueID := uuidToString(issue.ID)
	switch scope {
	case "issue", "single_use":
		if requestedID != "" && requestedID != issueID {
			return pgtype.UUID{}, "", false
		}
		return issue.ID, issueID, true
	case "project":
		if !issue.ProjectID.Valid || (requestedID != "" && requestedID != uuidToString(issue.ProjectID)) {
			return pgtype.UUID{}, "", false
		}
		return issue.ProjectID, uuidToString(issue.ProjectID), true
	case "workspace":
		if requestedID != "" && requestedID != workspaceID {
			return pgtype.UUID{}, "", false
		}
		return issue.WorkspaceID, workspaceID, true
	case "global":
		if requestedID != "" && requestedID != "global" {
			return pgtype.UUID{}, "", false
		}
		return pgtype.UUID{}, "global", true
	default:
		return pgtype.UUID{}, "", false
	}
}

func (h *Handler) ListCredentialAudit(w http.ResponseWriter, r *http.Request) {
	workspaceID := workspaceIDFromURL(r, "id")
	if _, ok := h.requireWorkspaceRole(w, r, workspaceID, "workspace not found", "owner"); !ok {
		return
	}
	rows, err := h.DB.Query(r.Context(), `
		SELECT a.id::text, a.created_at, a.credential_id::text, a.event_type, a.result, a.reason_code,
		       a.actor_type, a.actor_id, p.id::text, p.title, i.id::text, i.title,
		       a.task_id::text, a.agent_id::text, a.purpose
		FROM credential_audit a
		LEFT JOIN issue i ON i.id = a.issue_id AND i.workspace_id = a.workspace_id
		LEFT JOIN project p ON p.id = i.project_id AND p.workspace_id = a.workspace_id
		WHERE a.workspace_id = $1
		ORDER BY a.created_at DESC, a.id DESC
		LIMIT 500`, parseUUID(workspaceID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list credential audit")
		return
	}
	defer rows.Close()
	items := make([]credentialAuditResponse, 0)
	for rows.Next() {
		var item credentialAuditResponse
		var credentialID, projectID, projectTitle, issueID, issueTitle, taskID, agentID pgtype.Text
		if err := rows.Scan(&item.ID, &item.CreatedAt, &credentialID, &item.EventType, &item.Result, &item.ReasonCode,
			&item.ActorType, &item.ActorID, &projectID, &projectTitle, &issueID, &issueTitle,
			&taskID, &agentID, &item.Purpose); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list credential audit")
			return
		}
		item.CredentialID = credentialNullableText(credentialID)
		item.ProjectID = credentialNullableText(projectID)
		item.ProjectTitle = credentialNullableText(projectTitle)
		item.IssueID = credentialNullableText(issueID)
		item.IssueTitle = credentialNullableText(issueTitle)
		item.TaskID = credentialNullableText(taskID)
		item.AgentID = credentialNullableText(agentID)
		items = append(items, item)
	}
	if rows.Err() != nil {
		writeError(w, http.StatusInternalServerError, "failed to list credential audit")
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func credentialNullableText(value pgtype.Text) *string {
	if !value.Valid {
		return nil
	}
	return &value.String
}

func (h *Handler) validCredentialSubject(r *http.Request, issue db.Issue, subjectType, subjectID string) bool {
	if subjectType == "agent" {
		return h.isWorkspaceEntity(r.Context(), subjectType, subjectID, uuidToString(issue.WorkspaceID))
	}
	if subjectType != "squad" {
		return false
	}
	id, err := util.ParseUUID(subjectID)
	if err != nil {
		return false
	}
	var exists bool
	if err := h.DB.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM squad WHERE id = $1 AND workspace_id = $2)`, id, issue.WorkspaceID).Scan(&exists); err != nil {
		return false
	}
	return exists
}

func credentialRequestValid(req createIssueCredentialRequest) bool {
	return len(strings.TrimSpace(req.Name)) > 0 && len(req.Name) <= 200 &&
		len(strings.TrimSpace(req.Purpose)) > 0 && len(req.Purpose) <= 2000 &&
		((strings.TrimSpace(req.ConnectorID) == "" && req.ConnectorRev == 0) ||
			(strings.TrimSpace(req.ConnectorID) != "" && req.ConnectorRev > 0))
}

func credentialRequestConnector(req createIssueCredentialRequest) (pgtype.UUID, bool, error) {
	if strings.TrimSpace(req.ConnectorID) == "" {
		return pgtype.UUID{}, false, nil
	}
	connectorID, err := util.ParseUUID(req.ConnectorID)
	if err != nil {
		return pgtype.UUID{}, false, err
	}
	return connectorID, true, nil
}

func credentialConnectorActive(ctx context.Context, tx pgx.Tx, connectorID, workspaceID pgtype.UUID, revision int64) bool {
	var status string
	err := tx.QueryRow(ctx, `
		SELECT status FROM credential_connector
		WHERE id = $1 AND workspace_id = $2 AND revision = $3
		FOR SHARE`, connectorID, workspaceID, revision).Scan(&status)
	return err == nil && status == "active"
}

func credentialTargetService(req createIssueCredentialRequest) string {
	if targetService := strings.TrimSpace(req.TargetService); targetService != "" {
		return targetService
	}
	return strings.TrimSpace(req.Name)
}

func readCredentialBody(r io.Reader) ([]byte, error) {
	return io.ReadAll(r)
}

func writeCredentialAudit(ctx context.Context, tx pgx.Tx, workspaceID, credentialID, grantID pgtype.UUID,
	event, result, reason, actorType, actorID, daemonID string,
	taskID, agentID, issueID pgtype.UUID, purpose string) error {
	var daemon any
	if daemonID != "" {
		daemon = daemonID
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO credential_audit
		(id, workspace_id, credential_id, grant_id, event_type, result, reason_code, actor_type, actor_id, daemon_id, task_id, agent_id, issue_id, purpose)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)`,
		dbid.NewV7(), workspaceID, credentialID, grantID, event, result, reason, actorType, actorID,
		daemon, taskID, agentID, issueID, purpose)
	return err
}
