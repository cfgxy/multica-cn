package handler

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/credentialcrypto"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

const (
	credentialConnectorKind   = "mcp_http_bearer"
	credentialConnectorPolicy = "mcp_http_v1"
	credentialConnectorAuth   = "authorization_bearer"
)

type credentialConnectorRequest struct {
	Name     string `json:"name"`
	Target   string `json:"target_url"`
	Status   string `json:"status"`
	Revision int64  `json:"revision"`
}

type credentialConnectorResponse struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Kind          string `json:"kind"`
	TargetURL     string `json:"target_url"`
	AdapterPolicy string `json:"adapter_policy"`
	AuthField     string `json:"auth_field"`
	Status        string `json:"status"`
	Revision      int64  `json:"revision"`
}

func validCredentialConnectorTarget(raw string) bool {
	if raw == "" || raw != strings.TrimSpace(raw) || strings.ContainsAny(raw, "#?") {
		return false
	}
	parsed, err := url.ParseRequestURI(raw)
	return err == nil && parsed.Scheme == "https" && parsed.Hostname() != "" &&
		parsed.User == nil && parsed.Fragment == "" && parsed.RawQuery == ""
}

func decodeCredentialConnectorRequest(w http.ResponseWriter, r *http.Request, out *credentialConnectorRequest) bool {
	body := http.MaxBytesReader(w, r.Body, 16<<10)
	data, err := io.ReadAll(body)
	if err != nil || len(data) == 0 || credentialcrypto.RejectDuplicateJSONKeys(data) != nil {
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(out) != nil {
		return false
	}
	var trailing any
	return decoder.Decode(&trailing) == io.EOF
}

func (h *Handler) ListCredentialConnectors(w http.ResponseWriter, r *http.Request) {
	workspaceID := workspaceIDFromURL(r, "id")
	if _, ok := h.requireWorkspaceRole(w, r, workspaceID, "workspace not found", "owner"); !ok {
		return
	}
	rows, err := h.DB.Query(r.Context(), `
		SELECT id::text, name, kind, target_url, adapter_policy, auth_field, status, revision
		FROM credential_connector WHERE workspace_id = $1 ORDER BY created_at, id`, parseUUID(workspaceID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list credential connectors")
		return
	}
	defer rows.Close()
	items := make([]credentialConnectorResponse, 0)
	for rows.Next() {
		var item credentialConnectorResponse
		if err := rows.Scan(&item.ID, &item.Name, &item.Kind, &item.TargetURL, &item.AdapterPolicy, &item.AuthField, &item.Status, &item.Revision); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list credential connectors")
			return
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		writeError(w, http.StatusInternalServerError, "failed to list credential connectors")
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (h *Handler) CreateCredentialConnector(w http.ResponseWriter, r *http.Request) {
	workspaceID := workspaceIDFromURL(r, "id")
	member, ok := h.requireWorkspaceRole(w, r, workspaceID, "workspace not found", "owner")
	if !ok {
		return
	}
	var req credentialConnectorRequest
	if !decodeCredentialConnectorRequest(w, r, &req) || strings.TrimSpace(req.Name) == "" || len(req.Name) > 200 || !validCredentialConnectorTarget(req.Target) {
		writeError(w, http.StatusBadRequest, "invalid credential connector")
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "credential connector storage failed")
		return
	}
	defer tx.Rollback(r.Context())
	id := dbid.NewV7()
	workspaceUUID := parseUUID(workspaceID)
	if _, err := tx.Exec(r.Context(), `
		INSERT INTO credential_connector (id, workspace_id, name, kind, target_url, adapter_policy, auth_field, status, revision, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'active', 1, $8)`, id, workspaceUUID,
		strings.TrimSpace(req.Name), credentialConnectorKind, strings.TrimSpace(req.Target), credentialConnectorPolicy, credentialConnectorAuth, member.UserID); err != nil {
		writeError(w, http.StatusInternalServerError, "credential connector storage failed")
		return
	}
	if err := writeCredentialAudit(r.Context(), tx, workspaceUUID, pgtype.UUID{}, pgtype.UUID{},
		"credential.connector_created", "recorded", "created", "member", uuidToString(member.UserID), "",
		pgtype.UUID{}, pgtype.UUID{}, pgtype.UUID{}, ""); err != nil {
		writeError(w, http.StatusInternalServerError, "credential connector storage failed")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "credential connector storage failed")
		return
	}
	writeJSON(w, http.StatusCreated, credentialConnectorResponse{
		ID: uuidToString(id), Name: strings.TrimSpace(req.Name), Kind: credentialConnectorKind,
		TargetURL: strings.TrimSpace(req.Target), AdapterPolicy: credentialConnectorPolicy,
		AuthField: credentialConnectorAuth, Status: "active", Revision: 1,
	})
}

func (h *Handler) UpdateCredentialConnector(w http.ResponseWriter, r *http.Request) {
	workspaceID := workspaceIDFromURL(r, "id")
	member, ok := h.requireWorkspaceRole(w, r, workspaceID, "workspace not found", "owner")
	if !ok {
		return
	}
	connectorID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "connectorId"), "connector_id")
	if !ok {
		return
	}
	var req credentialConnectorRequest
	if !decodeCredentialConnectorRequest(w, r, &req) || strings.TrimSpace(req.Name) == "" || len(req.Name) > 200 ||
		!validCredentialConnectorTarget(req.Target) || (req.Status != "active" && req.Status != "disabled") || req.Revision < 1 {
		writeError(w, http.StatusBadRequest, "invalid credential connector")
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "credential connector update failed")
		return
	}
	defer tx.Rollback(r.Context())
	var currentRevision int64
	if err := tx.QueryRow(r.Context(), `SELECT revision FROM credential_connector WHERE id = $1 AND workspace_id = $2 FOR UPDATE`, connectorID, parseUUID(workspaceID)).Scan(&currentRevision); err != nil {
		writeError(w, http.StatusNotFound, "credential connector not found")
		return
	}
	if req.Revision != currentRevision {
		writeError(w, http.StatusConflict, "credential connector revision changed")
		return
	}
	var updated credentialConnectorResponse
	if err := tx.QueryRow(r.Context(), `
		UPDATE credential_connector SET name = $3, target_url = $4, status = $5, revision = revision + 1, updated_at = now()
		WHERE id = $1 AND workspace_id = $2
		RETURNING id::text, name, kind, target_url, adapter_policy, auth_field, status, revision`,
		connectorID, parseUUID(workspaceID), strings.TrimSpace(req.Name), strings.TrimSpace(req.Target), req.Status).
		Scan(&updated.ID, &updated.Name, &updated.Kind, &updated.TargetURL, &updated.AdapterPolicy, &updated.AuthField, &updated.Status, &updated.Revision); err != nil {
		writeError(w, http.StatusInternalServerError, "credential connector update failed")
		return
	}
	if err := writeCredentialAudit(r.Context(), tx, parseUUID(workspaceID), pgtype.UUID{}, pgtype.UUID{},
		"credential.connector_updated", "recorded", "updated", "member", uuidToString(member.UserID), "",
		pgtype.UUID{}, pgtype.UUID{}, pgtype.UUID{}, ""); err != nil {
		writeError(w, http.StatusInternalServerError, "credential connector update failed")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "credential connector update failed")
		return
	}
	writeJSON(w, http.StatusOK, updated)
}
