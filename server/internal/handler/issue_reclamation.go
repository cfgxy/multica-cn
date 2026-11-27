package handler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func (h *Handler) BeginIssueReclamation(w http.ResponseWriter, r *http.Request) {
	h.issueReclamation(w, r, false)
}

func (h *Handler) FinishIssueReclamation(w http.ResponseWriter, r *http.Request) {
	h.issueReclamation(w, r, true)
}

func (h *Handler) issueReclamation(w http.ResponseWriter, r *http.Request, finish bool) {
	ctx := r.Context()
	daemonID := middleware.DaemonIDFromContext(ctx)
	daemonWorkspace := middleware.DaemonWorkspaceIDFromContext(ctx)
	owner, ownerErr := util.ParseUUID(middleware.DaemonTokenOwnerIDFromContext(ctx))
	if middleware.DaemonAuthPathFromContext(ctx) != middleware.DaemonAuthPathDaemonToken ||
		daemonID == "" || daemonWorkspace == "" || ownerErr != nil || !owner.Valid {
		writeError(w, http.StatusUnauthorized, "owner-bound daemon token authentication is required")
		return
	}
	workspace, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "workspaceId"), "workspace_id")
	if !ok {
		return
	}
	if uuidToString(workspace) != daemonWorkspace {
		writeError(w, http.StatusNotFound, "issue not found")
		return
	}
	issue, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "issueId"), "issue_id")
	if !ok {
		return
	}
	var req struct {
		Token string `json:"token"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	raw, err := hex.DecodeString(req.Token)
	if err != nil || len(raw) != 32 || hex.EncodeToString(raw) != req.Token {
		writeError(w, http.StatusBadRequest, "token must be 64 lowercase hexadecimal characters")
		return
	}
	digest := sha256.Sum256(raw)
	result, err := h.Queries.ReserveIssueReclamation(ctx, db.ReserveIssueReclamationParams{
		WorkspaceID: workspace,
		IssueID:     issue,
		DaemonID:    daemonID,
		OwnerID:     owner,
		TokenHash:   digest[:],
		Finish:      finish,
	})
	if err != nil {
		slog.Warn("issue reclamation query failed", "issue_id", uuidToString(issue), "finish", finish, "error", err)
	}
	switch {
	case err == nil && result == "ok":
		w.WriteHeader(http.StatusNoContent)
	case err == nil && result == "not_found":
		writeError(w, http.StatusNotFound, "issue not found")
	case err == nil && result == "conflict":
		writeError(w, http.StatusConflict, "issue reclamation request conflicts with current state")
	default:
		writeError(w, http.StatusInternalServerError, "issue reclamation request failed")
	}
}

func writeIssueReclamationConflict(w http.ResponseWriter, err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "MC001" {
		writeError(w, http.StatusConflict, "issue worktree reclamation is in progress; retry after it finishes")
		return true
	}
	return false
}
