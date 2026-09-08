package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/pkg/plugincontract"
)

// Publishing endpoints.
//
// An author uploads an artifact bundle and Multica stores it. There is no
// install-by-URL any more, and no second way for plugin code to reach a reader's
// browser: the settings page publishes here, and installing names a version this
// endpoint created.

// ListPluginPackages — GET /api/workspaces/{id}/plugins/packages
//
// Ungated: see the cleanup note above requirePluginsV1. A publisher who
// disabled the feature still has to be able to see and delete what they
// published.
func (h *Handler) ListPluginPackages(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := parseUUIDOrBadRequest(w, workspaceIDFromURL(r, "id"), "workspace_id")
	if !ok {
		return
	}
	packages, err := h.PluginService.ListPackages(r.Context(), workspaceID)
	if err != nil {
		writePluginError(w, err, "failed to list published Plugins")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"packages": packages})
}

// PublishPluginPackage — POST /api/workspaces/{id}/plugins/packages
//
// multipart/form-data with a `bundle` file: a zip holding the manifest and every
// file it names. Validated in full before anything is stored, so a surface whose
// entry is missing fails here rather than in a reader's browser weeks later.
func (h *Handler) PublishPluginPackage(w http.ResponseWriter, r *http.Request) {
	if !h.requirePluginsV1(w, r) {
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

	// Bounded before the multipart reader sees it: ParseMultipartForm would
	// otherwise spool an unbounded upload to a temp file first.
	r.Body = http.MaxBytesReader(w, r.Body, plugincontract.MaxBundleSize+multipartOverheadBytes)
	if err := r.ParseMultipartForm(plugincontract.MaxBundleSize + multipartOverheadBytes); err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "the Plugin package upload is too large or malformed")
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()

	file, _, err := r.FormFile("bundle")
	if err != nil {
		writeError(w, http.StatusBadRequest, "a Plugin package file is required")
		return
	}
	defer file.Close()

	archive, err := io.ReadAll(io.LimitReader(file, plugincontract.MaxBundleSize+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read the Plugin package")
		return
	}
	if len(archive) > plugincontract.MaxBundleSize {
		writeError(w, http.StatusRequestEntityTooLarge, "the Plugin package is too large")
		return
	}

	published, err := h.PluginService.PublishBundle(r.Context(), workspaceID, member.UserID, archive)
	if err != nil {
		writePluginError(w, err, "failed to publish the Plugin")
		return
	}
	writeJSON(w, http.StatusCreated, published)
}

// multipartOverheadBytes is headroom for the form's own boundaries and headers,
// so a bundle at exactly the size limit is not rejected by the envelope around
// it.
const multipartOverheadBytes = 64 * 1024

type publishLocalPluginRequest struct {
	// Name is a directory under MULTICA_PLUGIN_DIR.
	Name string `json:"name"`
}

// PublishLocalPluginPackage — POST /api/workspaces/{id}/plugins/packages/local
//
// The development channel: publish straight from a directory the operator
// already hosts, so iterating on a surface does not mean zipping and uploading
// after every edit. It produces an ordinary immutable version — there is no
// live-reload path that would make "is this code frozen?" depend on how the
// plugin was installed.
func (h *Handler) PublishLocalPluginPackage(w http.ResponseWriter, r *http.Request) {
	if !h.requirePluginsV1(w, r) {
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
	var req publishLocalPluginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	published, err := h.PluginService.PublishLocalBundle(r.Context(), workspaceID, member.UserID, strings.TrimSpace(req.Name))
	if err != nil {
		writePluginError(w, err, "failed to publish the Plugin")
		return
	}
	writeJSON(w, http.StatusCreated, published)
}

// ListPublicPluginPackages — GET /api/workspaces/{id}/plugins/directory
//
// The instance directory. Membership of the workspace in the URL is what
// authorizes the read — the listing spans workspaces, so anonymous access would
// publish an inventory of the instance's plugins to anyone who can reach it.
func (h *Handler) ListPublicPluginPackages(w http.ResponseWriter, r *http.Request) {
	if !h.requirePluginsV1(w, r) {
		return
	}
	workspaceIDString := workspaceIDFromURL(r, "id")
	workspaceID, ok := parseUUIDOrBadRequest(w, workspaceIDString, "workspace_id")
	if !ok {
		return
	}
	if _, ok := h.workspaceMember(w, r, workspaceIDString); !ok {
		return
	}
	packages, err := h.PluginService.ListPublicPackages(r.Context(), workspaceID)
	if err != nil {
		writePluginError(w, err, "failed to list Plugins on the directory")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"packages": packages})
}

type pluginVisibilityRequest struct {
	// Public rather than a free-text visibility: the column has two states and
	// a bool cannot carry a third one that the CHECK constraint would then
	// reject with a database error instead of a validation message.
	Public bool `json:"public"`
}

// SetPluginPackageVisibility — PUT /api/workspaces/{id}/plugins/packages/{packageId}/visibility
func (h *Handler) SetPluginPackageVisibility(w http.ResponseWriter, r *http.Request) {
	if !h.requirePluginsV1(w, r) {
		return
	}
	workspaceIDString := workspaceIDFromURL(r, "id")
	workspaceID, ok := parseUUIDOrBadRequest(w, workspaceIDString, "workspace_id")
	if !ok {
		return
	}
	if _, ok := h.workspaceMember(w, r, workspaceIDString); !ok {
		return
	}
	var req pluginVisibilityRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	updated, err := h.PluginService.SetPackageVisibility(r.Context(), workspaceID, chi.URLParam(r, "packageId"), req.Public)
	if err != nil {
		writePluginError(w, err, "failed to update the Plugin listing")
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

type pluginWithdrawalRequest struct {
	Withdrawn bool `json:"withdrawn"`
}

// SetPluginVersionWithdrawn — PUT /api/workspaces/{id}/plugins/versions/{versionId}/withdrawn
func (h *Handler) SetPluginVersionWithdrawn(w http.ResponseWriter, r *http.Request) {
	if !h.requirePluginsV1(w, r) {
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
	var req pluginWithdrawalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	updated, err := h.PluginService.SetVersionWithdrawn(r.Context(), workspaceID, member.UserID, chi.URLParam(r, "versionId"), req.Withdrawn)
	if err != nil {
		writePluginError(w, err, "failed to update the Plugin version listing")
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// DeletePluginPackage — DELETE /api/workspaces/{id}/plugins/packages/{packageId}
//
// Ungated: see the cleanup note above requirePluginsV1. The install-count guard
// inside DeletePackage still applies, so this cannot break a workspace that is
// still running one of these versions.
func (h *Handler) DeletePluginPackage(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := parseUUIDOrBadRequest(w, workspaceIDFromURL(r, "id"), "workspace_id")
	if !ok {
		return
	}
	if err := h.PluginService.DeletePackage(r.Context(), workspaceID, chi.URLParam(r, "packageId")); err != nil {
		writePluginError(w, err, "failed to delete the published Plugin")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
