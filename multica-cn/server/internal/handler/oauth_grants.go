package handler

// The signed-in user's own OAuth authorizations (RUYI-420): list and
// revoke from Settings → "My Authorizations". Scope is strictly personal —
// a user can only ever see and revoke grants where user_id matches their
// own session, and revoke answers 404 (not 403) for foreign ids so grant
// existence doesn't leak across users.

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// MyOAuthGrantResponse is one row of the "my authorizations" page: the
// client's display name and the grant's lifecycle. Management-side fields
// (created_by and friends) are not the user's business.
type MyOAuthGrantResponse struct {
	ID         string  `json:"id"`
	ClientID   string  `json:"client_id"`
	ClientName *string `json:"client_name"`
	Scope      string  `json:"scope"`
	CreatedAt  string  `json:"created_at"`
	LastUsedAt *string `json:"last_used_at"`
	RevokedAt  *string `json:"revoked_at"`
}

func newMyOAuthGrantResponse(g db.ListOAuthGrantsByUserRow) MyOAuthGrantResponse {
	return MyOAuthGrantResponse{
		ID:         uuidToString(g.ID),
		ClientID:   g.ClientID,
		ClientName: pgTextPtr(g.ClientName),
		Scope:      g.Scope,
		CreatedAt:  g.CreatedAt.Time.UTC().Format(time.RFC3339),
		LastUsedAt: pgTimePtr(g.LastUsedAt),
		RevokedAt:  pgTimePtr(g.RevokedAt),
	}
}

// ListMyOAuthGrants GET /api/oauth/grants
func (h *Handler) ListMyOAuthGrants(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	id, err := util.ParseUUID(userID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "user not authenticated")
		return
	}
	grants, err := h.Queries.ListOAuthGrantsByUser(r.Context(), id)
	if err != nil {
		slog.Error("list my oauth grants failed", "error", err)
		writeError(w, http.StatusInternalServerError, "could not list grants")
		return
	}
	resp := make([]MyOAuthGrantResponse, 0, len(grants))
	for _, g := range grants {
		resp = append(resp, newMyOAuthGrantResponse(g))
	}
	writeJSON(w, http.StatusOK, map[string]any{"grants": resp})
}

// RevokeMyOAuthGrant DELETE /api/oauth/grants/{id}
func (h *Handler) RevokeMyOAuthGrant(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	requester, err := util.ParseUUID(userID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "user not authenticated")
		return
	}
	id, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	// Ownership is checked BEFORE any write: a foreign id must be a pure
	// no-op, so the read-then-write order matters here even though the
	// final revoke stays a conditional UPDATE. Unknown id and someone
	// else's grant get the same 404.
	grant, err := h.Queries.GetOAuthGrantByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "grant not found")
			return
		}
		slog.Error("revoke my oauth grant failed", "error", err)
		writeError(w, http.StatusInternalServerError, "could not revoke grant")
		return
	}
	if grant.UserID != requester {
		writeError(w, http.StatusNotFound, "grant not found")
		return
	}
	if grant.RevokedAt.Valid {
		// Idempotent: revoking a live-revoked row again keeps its original
		// revoked_at and answers success.
		writeJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
		return
	}
	grant, err = h.Queries.RevokeOAuthGrantByID(r.Context(), id)
	if err != nil {
		// Lost a race with an admin revoke between the read and the write:
		// the conditional UPDATE matched nothing, the grant is dead either
		// way — success is the truthful answer.
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
			return
		}
		slog.Error("revoke my oauth grant failed", "error", err)
		writeError(w, http.StatusInternalServerError, "could not revoke grant")
		return
	}
	if h.OAuthGate != nil {
		h.OAuthGate.Invalidate(r.Context(), uuidToString(grant.ID))
	}
	slog.Info("oauth grant revoked by owner", "grant_id", uuidToString(grant.ID), "client_id", grant.ClientID)
	writeJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
}
