package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/integrations/lark"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// LarkInstallationResponse is the wire shape for an installation row.
// `app_secret_encrypted` is INTENTIONALLY absent — the encrypted blob
// is server-internal and there is no product reason to expose it (the
// only consumer that needs the plaintext is the WS hub, which calls
// InstallationService.DecryptAppSecret server-side). Likewise, the WS
// lease columns are omitted; they are runtime state, not API surface.
type LarkInstallationResponse struct {
	ID              string  `json:"id"`
	WorkspaceID     string  `json:"workspace_id"`
	AgentID         string  `json:"agent_id"`
	AppID           string  `json:"app_id"`
	TenantKey       *string `json:"tenant_key,omitempty"`
	BotOpenID       string  `json:"bot_open_id"`
	InstallerUserID string  `json:"installer_user_id"`
	Status          string  `json:"status"`
	InstalledAt     string  `json:"installed_at"`
	CreatedAt       string  `json:"created_at"`
	UpdatedAt       string  `json:"updated_at"`
}

func larkInstallationToResponse(row db.LarkInstallation) LarkInstallationResponse {
	resp := LarkInstallationResponse{
		ID:              uuidToString(row.ID),
		WorkspaceID:     uuidToString(row.WorkspaceID),
		AgentID:         uuidToString(row.AgentID),
		AppID:           row.AppID,
		BotOpenID:       row.BotOpenID,
		InstallerUserID: uuidToString(row.InstallerUserID),
		Status:          row.Status,
		InstalledAt:     row.InstalledAt.Time.UTC().Format(time.RFC3339),
		CreatedAt:       row.CreatedAt.Time.UTC().Format(time.RFC3339),
		UpdatedAt:       row.UpdatedAt.Time.UTC().Format(time.RFC3339),
	}
	if row.TenantKey.Valid {
		tk := row.TenantKey.String
		resp.TenantKey = &tk
	}
	return resp
}

// CreateLarkInstallationRequest is the manual-install payload (admin
// pastes credentials from the Lark developer console). The OAuth
// callback path lands credentials via the same InstallationService
// pipeline, so adding it later does not change this contract.
type CreateLarkInstallationRequest struct {
	AgentID   string `json:"agent_id"`
	AppID     string `json:"app_id"`
	AppSecret string `json:"app_secret"`
	TenantKey string `json:"tenant_key,omitempty"`
	BotOpenID string `json:"bot_open_id"`
}

// CreateLarkInstallation (POST /api/workspaces/{id}/lark/installations)
// is admin-only at the router level. It performs the at-rest
// encryption of `app_secret` via InstallationService and refuses to
// fall back to plaintext storage when the master key is unset (503).
func (h *Handler) CreateLarkInstallation(w http.ResponseWriter, r *http.Request) {
	if h.LarkInstallations == nil {
		writeError(w, http.StatusServiceUnavailable, "lark integration not configured (MULTICA_LARK_SECRET_KEY)")
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "workspace id")
	if !ok {
		return
	}

	var req CreateLarkInstallationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	agentUUID, ok := parseUUIDOrBadRequest(w, req.AgentID, "agent_id")
	if !ok {
		return
	}
	// Validate the agent really belongs to this workspace before we
	// accept its credentials. Without this guard a workspace admin
	// could install Lark on an agent in a different workspace by
	// supplying that workspace's agent_id.
	if _, err := h.Queries.GetAgentInWorkspace(r.Context(), db.GetAgentInWorkspaceParams{
		ID:          agentUUID,
		WorkspaceID: wsUUID,
	}); err != nil {
		writeError(w, http.StatusNotFound, "agent not found in this workspace")
		return
	}
	installerUUID, ok := parseUUIDOrBadRequest(w, userID, "installer user id")
	if !ok {
		return
	}

	inst, err := h.LarkInstallations.Upsert(r.Context(), lark.InstallationParams{
		WorkspaceID:     wsUUID,
		AgentID:         agentUUID,
		AppID:           req.AppID,
		AppSecret:       req.AppSecret,
		TenantKey:       req.TenantKey,
		BotOpenID:       req.BotOpenID,
		InstallerUserID: installerUUID,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	resp := larkInstallationToResponse(inst)
	h.publish(protocol.EventLarkInstallationCreated, uuidToString(wsUUID), "user", userID, resp)
	writeJSON(w, http.StatusCreated, resp)
}

// ListLarkInstallations (GET /api/workspaces/{id}/lark/installations)
// is member-visible — the Integrations tab should not render blank
// for non-admins. Unlike the GitHub list, we do not strip any field
// here because no API surface column doubles as a management handle:
// revocation goes by the UUID id, which is meaningless without the
// admin route's authorization, so exposing it is harmless.
func (h *Handler) ListLarkInstallations(w http.ResponseWriter, r *http.Request) {
	if h.LarkInstallations == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"installations": []LarkInstallationResponse{},
			"configured":    false,
		})
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "workspace id")
	if !ok {
		return
	}
	rows, err := h.LarkInstallations.ListByWorkspace(r.Context(), wsUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list lark installations")
		return
	}
	out := make([]LarkInstallationResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, larkInstallationToResponse(row))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"installations": out,
		"configured":    true,
	})
}

// RevokeLarkInstallation (DELETE /api/workspaces/{id}/lark/installations/{installationId})
// flips status to 'revoked' so the WS hub drops the connection on its
// next sweep. The row itself is preserved for audit; a re-install via
// CreateLarkInstallation flips status back to 'active' atomically.
func (h *Handler) RevokeLarkInstallation(w http.ResponseWriter, r *http.Request) {
	if h.LarkInstallations == nil {
		writeError(w, http.StatusServiceUnavailable, "lark integration not configured")
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "workspace id")
	if !ok {
		return
	}
	instUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "installationId"), "installation id")
	if !ok {
		return
	}
	// Workspace-scoped lookup ensures one workspace cannot revoke
	// another's installation by guessing the UUID.
	if _, err := h.LarkInstallations.GetInWorkspace(r.Context(), instUUID, wsUUID); err != nil {
		if errors.Is(err, lark.ErrInstallationNotFound) {
			writeError(w, http.StatusNotFound, "lark installation not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load installation")
		return
	}
	if err := h.LarkInstallations.Revoke(r.Context(), instUUID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to revoke installation")
		return
	}
	h.publish(protocol.EventLarkInstallationRevoked, uuidToString(wsUUID), "user", userID, map[string]any{
		"id": uuidToString(instUUID),
	})
	w.WriteHeader(http.StatusNoContent)
}

// RedeemLarkBindingTokenRequest carries the raw token the user
// clicked through from the Bot's "you need to bind" reply card.
type RedeemLarkBindingTokenRequest struct {
	Token string `json:"token"`
}

// RedeemLarkBindingTokenResponse is the post-redemption shape. We
// echo the workspace/installation/open_id so the frontend can render
// "you are now bound to <workspace> via <agent>" without a second
// fetch.
type RedeemLarkBindingTokenResponse struct {
	WorkspaceID    string `json:"workspace_id"`
	InstallationID string `json:"installation_id"`
	LarkOpenID     string `json:"lark_open_id"`
}

// RedeemLarkBindingToken (POST /api/lark/binding/redeem) is the only
// path that writes a lark_user_binding row from user-driven action.
// The redeemer's identity is taken from the session, not the token,
// so a stolen token cannot bind a Lark open_id to an attacker's
// Multica account. The token only proves "this open_id requested
// binding" — combining it with the logged-in user is what creates
// the (open_id ↔ user) mapping.
func (h *Handler) RedeemLarkBindingToken(w http.ResponseWriter, r *http.Request) {
	if h.LarkBindingTokens == nil {
		writeError(w, http.StatusServiceUnavailable, "lark integration not configured")
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	var req RedeemLarkBindingTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Token == "" {
		writeError(w, http.StatusBadRequest, "token is required")
		return
	}
	userUUID, ok := parseUUIDOrBadRequest(w, userID, "user id")
	if !ok {
		return
	}

	redeemed, err := h.LarkBindingTokens.Redeem(r.Context(), req.Token)
	if err != nil {
		if errors.Is(err, lark.ErrBindingTokenInvalid) {
			writeError(w, http.StatusGone, "binding token invalid or expired")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to redeem token")
		return
	}

	// CreateLarkUserBinding's composite FK to member(workspace_id, user_id)
	// will fail with a foreign-key violation if the redeemer is NOT a
	// member of the token's workspace. We surface that as 403 so a
	// non-member who stole the link cannot silently complete binding.
	if _, err := h.Queries.CreateLarkUserBinding(r.Context(), db.CreateLarkUserBindingParams{
		WorkspaceID:    redeemed.WorkspaceID,
		MulticaUserID:  userUUID,
		InstallationID: redeemed.InstallationID,
		LarkOpenID:     string(redeemed.LarkOpenID),
	}); err != nil {
		writeError(w, http.StatusForbidden, "binding refused (are you a workspace member?)")
		return
	}

	writeJSON(w, http.StatusOK, RedeemLarkBindingTokenResponse{
		WorkspaceID:    uuidToString(redeemed.WorkspaceID),
		InstallationID: uuidToString(redeemed.InstallationID),
		LarkOpenID:     string(redeemed.LarkOpenID),
	})
}
