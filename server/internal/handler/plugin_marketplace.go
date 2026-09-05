package handler

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
)

type marketplacePluginListingRequest struct {
	VersionID string `json:"version_id"`
}

// ListMarketplacePlugins — GET /api/workspaces/{id}/marketplace/plugins
func (h *Handler) ListMarketplacePlugins(w http.ResponseWriter, r *http.Request) {
	if !h.requirePluginMarketplace(w, r) {
		return
	}
	workspaceID, ok := parseUUIDOrBadRequest(w, workspaceIDFromURL(r, "id"), "workspace_id")
	if !ok {
		return
	}
	plugins, err := h.PluginService.ListMarketplacePlugins(r.Context(), workspaceID)
	if err != nil {
		writePluginError(w, err, "failed to list Plugin marketplace")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"plugins": plugins})
}

// ListPluginPackageInMarketplace — PUT /api/workspaces/{id}/plugins/packages/{packageId}/marketplace
func (h *Handler) ListPluginPackageInMarketplace(w http.ResponseWriter, r *http.Request) {
	if !h.requirePluginMarketplace(w, r) {
		return
	}
	workspaceIDString := workspaceIDFromURL(r, "id")
	workspaceID, ok := parseUUIDOrBadRequest(w, workspaceIDString, "workspace_id")
	if !ok {
		return
	}
	member, ok := h.workspaceMember(w, r, workspaceIDString)
	if !ok {
		return
	}
	var req marketplacePluginListingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	err := h.PluginService.ListPackageInMarketplace(
		r.Context(), workspaceID, member.UserID, chi.URLParam(r, "packageId"), req.VersionID,
	)
	if err != nil {
		writePluginError(w, err, "failed to list Plugin in marketplace")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// UnlistPluginPackageFromMarketplace — DELETE /api/workspaces/{id}/plugins/packages/{packageId}/marketplace
func (h *Handler) UnlistPluginPackageFromMarketplace(w http.ResponseWriter, r *http.Request) {
	if !h.requirePluginMarketplace(w, r) {
		return
	}
	workspaceID, ok := parseUUIDOrBadRequest(w, workspaceIDFromURL(r, "id"), "workspace_id")
	if !ok {
		return
	}
	if err := h.PluginService.UnlistPackageFromMarketplace(r.Context(), workspaceID, chi.URLParam(r, "packageId")); err != nil {
		writePluginError(w, err, "failed to remove Plugin from marketplace")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
