package handler

// OAuth consent screen endpoints (RUYI-420).
//
// The authorize endpoint parks a validated request in the ConsentStore and
// sends the browser here when no live grant covers it. Three endpoints:
//
//	GET  /auth/oauth/consent/{id}          — what the page renders
//	POST /auth/oauth/consent/{id}/approve  — create/refresh the grant, resume authorize
//	POST /auth/oauth/consent/{id}/deny     — bounce back with access_denied
//
// Session posture matches AuthorizeOAuth itself: the session cookie is
// verified directly (oauthSessionUser) rather than behind middleware.Auth,
// because an unauthenticated visitor is a login redirect, not an error.
// The POSTs additionally enforce CSRF exactly the way middleware.Auth does
// for every other cookie-carried state change (auth.ValidateCSRF).
//
// The consent id is the only capability on these endpoints, and it is
// bound to the user who started the flow: a consent request loaded under a
// different session is not found. Logging discipline carries over from
// oauth.go — ids and client_id only, never tokens or verifiers.

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/oauth"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// OAuthConsentResponse is the payload the consent page renders. Scope keys
// are the stable tier identifiers; their human-readable descriptions live
// in the frontend's locale files, which is where display language belongs.
type OAuthConsentResponse struct {
	ClientID   string   `json:"client_id"`
	ClientName string   `json:"client_name"`
	Scopes     []string `json:"scopes"`
}

// redirectToConsent parks the validated authorization request and sends the
// browser to the consent screen. Every failure here is rendered as a
// protocol error back at the (already validated) redirect_uri: the client
// is the party that can still act on them.
func (h *Handler) redirectToConsent(w http.ResponseWriter, r *http.Request, req oauth.ConsentRequest) {
	if !h.OAuthConsents.Available() {
		slog.Error("oauth: consent store unavailable", "client_id", req.ClientID)
		h.redirectOAuthError(w, r, req.RedirectURI, req.State, "temporarily_unavailable", "The consent service is unavailable.")
		return
	}
	id, err := oauth.NewConsentRequestID()
	if err != nil {
		slog.Error("oauth: failed to generate consent request id", "client_id", req.ClientID, "error", err)
		h.redirectOAuthError(w, r, req.RedirectURI, req.State, "server_error", "Could not start the consent step.")
		return
	}
	if err := h.OAuthConsents.Save(r.Context(), id, req); err != nil {
		slog.Error("oauth: failed to store consent request", "client_id", req.ClientID, "error", err)
		h.redirectOAuthError(w, r, req.RedirectURI, req.State, "server_error", "Could not start the consent step.")
		return
	}
	target := h.oauthSiteRoot() + "/oauth/consent?request=" + url.QueryEscape(id)
	http.Redirect(w, r, target, http.StatusFound)
}

// loadConsentRequest resolves and authorizes the consent id for the current
// session. The bool is false with a response already written when anything
// is off — unknown id, expired, wrong user, or no session.
func (h *Handler) loadConsentRequest(w http.ResponseWriter, r *http.Request, consume bool) (oauth.ConsentRequest, bool) {
	reqID := strings.TrimSpace(chi.URLParam(r, "id"))
	if reqID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return oauth.ConsentRequest{}, false
	}
	userID, ok := h.oauthSessionUser(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not_signed_in"})
		return oauth.ConsentRequest{}, false
	}
	var (
		req oauth.ConsentRequest
		err error
	)
	if consume {
		req, err = h.OAuthConsents.Consume(r.Context(), reqID)
	} else {
		req, err = h.OAuthConsents.Load(r.Context(), reqID)
	}
	if err != nil {
		// Unknown and expired are the same answer (see ConsentStore), and a
		// request bound to another user is indistinguishable from one that
		// never existed — nothing about another user's pending consent may
		// leak through this endpoint.
		if errors.Is(err, oauth.ErrConsentNotFound) {
			slog.Warn("oauth: consent request not found or not yours", "user_id", userID)
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "consent_not_found"})
			return oauth.ConsentRequest{}, false
		}
		slog.Error("oauth: consent store read failed", "error", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "temporarily_unavailable"})
		return oauth.ConsentRequest{}, false
	}
	if req.UserID != userID {
		slog.Warn("oauth: consent request presented by a different session", "user_id", userID)
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "consent_not_found"})
		return oauth.ConsentRequest{}, false
	}
	return req, true
}

// GetOAuthConsent renders the payload for the consent screen.
func (h *Handler) GetOAuthConsent(w http.ResponseWriter, r *http.Request) {
	if h.oauthDisabled(w) {
		return
	}
	req, ok := h.loadConsentRequest(w, r, false)
	if !ok {
		return
	}
	client, clientOK := h.loadOAuthClient(r, req.ClientID)
	if !clientOK {
		// The client vanished between authorize and consent. Fail the
		// pending request rather than rendering a screen for a ghost.
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "consent_not_found"})
		return
	}
	writeJSON(w, http.StatusOK, OAuthConsentResponse{
		ClientID:   req.ClientID,
		ClientName: client.Name,
		Scopes:     oauth.ScopeTiersOf(req.Scope),
	})
}

// ApproveOAuthConsent consumes the pending request, upserts the grant and
// answers with the authorize URL the browser resumes. Single-shot: the
// consume makes approve/deny replay-proof.
func (h *Handler) ApproveOAuthConsent(w http.ResponseWriter, r *http.Request) {
	if h.oauthDisabled(w) {
		return
	}
	if !auth.ValidateCSRF(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "csrf_validation_failed"})
		return
	}
	req, ok := h.loadConsentRequest(w, r, true)
	if !ok {
		return
	}
	userUUID, err := util.ParseUUID(req.UserID)
	if err != nil {
		slog.Error("oauth: consent user id is not a UUID", "client_id", req.ClientID)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "server_error"})
		return
	}
	grant, err := h.Queries.UpsertOAuthGrant(r.Context(), db.UpsertOAuthGrantParams{
		ClientID: req.ClientID,
		UserID:   userUUID,
		Scope:    req.Scope,
	})
	if err != nil {
		slog.Error("oauth: failed to persist grant", "client_id", req.ClientID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "server_error"})
		return
	}
	// A revived grant may still sit in the gate cache under its pre-revoke
	// (dead) state; drop the entry so the resumed authorize mints a token
	// the gate lets through immediately.
	h.invalidateOAuthGate(r.Context(), util.UUIDToString(grant.ID))

	slog.Info("oauth: consent approved", "client_id", req.ClientID, "grant_id", util.UUIDToString(grant.ID))
	writeJSON(w, http.StatusOK, map[string]string{
		"redirect": h.rebuildAuthorizeURL(req, ""),
	})
}

// DenyOAuthConsent consumes the pending request and answers with the
// authorize URL carrying the standard access_denied error.
func (h *Handler) DenyOAuthConsent(w http.ResponseWriter, r *http.Request) {
	if h.oauthDisabled(w) {
		return
	}
	if !auth.ValidateCSRF(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "csrf_validation_failed"})
		return
	}
	req, ok := h.loadConsentRequest(w, r, true)
	if !ok {
		return
	}
	slog.Info("oauth: consent denied", "client_id", req.ClientID)
	writeJSON(w, http.StatusOK, map[string]string{
		"redirect": h.rebuildAuthorizeURL(req, "access_denied"),
	})
}

// rebuildAuthorizeURL reconstructs the original authorize request from the
// parked consent request, optionally adding an OAuth error. The stored copy
// is authoritative — rebuilding from the browser's current query string
// would let a tampered tab smuggle in different parameters.
func (h *Handler) rebuildAuthorizeURL(req oauth.ConsentRequest, oauthErr string) string {
	params := url.Values{}
	params.Set("client_id", req.ClientID)
	params.Set("redirect_uri", req.RedirectURI)
	params.Set("response_type", "code")
	params.Set("scope", req.Scope)
	params.Set("code_challenge", req.CodeChallenge)
	params.Set("code_challenge_method", req.CodeChallengeMethod)
	if req.Resource != "" {
		params.Set("resource", req.Resource)
	}
	if req.State != "" {
		params.Set("state", req.State)
	}
	if oauthErr != "" {
		params.Set("error", oauthErr)
		params.Set("error_description", "The user denied the authorization request.")
	}
	return h.oauthSiteRoot() + "/auth/oauth/authorize?" + params.Encode()
}

// invalidateOAuthGate drops one grant's cached gate state. Nil-safe: a
// deployment without a gate has nothing cached.
func (h *Handler) invalidateOAuthGate(ctx context.Context, grantID string) {
	if h.OAuthGate != nil {
		h.OAuthGate.Invalidate(ctx, grantID)
	}
}
