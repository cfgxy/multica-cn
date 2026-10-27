package handler

// System Settings OAuth management (RUYI-420). Instance-level endpoints
// under /api/admin — the router group already applies the super-admin
// gate, so X-User-ID here is a live human admin and never an OAuth token
// (the auth middleware's grant gate rejects OAuth tokens on these paths
// before routing anyway).
//
// Secret discipline, the contract every handler here signs up to: the
// plaintext secret exists only inside the create and rotate response
// bodies, once. client_secret_hash never appears in any response, log
// line, or audit metadata — audit rows record THAT a rotation happened,
// never what was rotated.

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/oauth"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Audit vocabulary for the OAuth management surface, in the same action
// namespace as the RUYI-47 admin audit constants.
const (
	AuditActionOAuthClientCreate  = "oauth_client.create"
	AuditActionOAuthClientUpdate  = "oauth_client.update"
	AuditActionOAuthClientRotate  = "oauth_client.rotate"
	AuditActionOAuthClientDisable = "oauth_client.disable"
	AuditActionOAuthClientEnable  = "oauth_client.enable"
	AuditActionOAuthClientDelete  = "oauth_client.delete"
	AuditActionOAuthGrantRevoke   = "oauth_grant.revoke"

	AuditTargetTypeOAuthClient = "oauth_client"
	AuditTargetTypeOAuthGrant  = "oauth_grant"
)

const (
	oauthClientNameMaxLen = 200
	// mcpDiagTimeout bounds the server-to-Node probe behind the status
	// page. The diagnostic endpoint is a loopback static read; anything
	// slower is down, and the page must degrade fast rather than spin.
	mcpDiagTimeout = 3 * time.Second
)

// AdminOAuthClientResponse is the management-surface view of one client.
// There is no secret field by construction: the hash stays in the
// database, the plaintext existed only in the create/rotate response.
type AdminOAuthClientResponse struct {
	ID           string   `json:"id"`
	ClientID     string   `json:"client_id"`
	Name         string   `json:"name"`
	RedirectURIs []string `json:"redirect_uris"`
	CreatedBy    *string  `json:"created_by"`
	CreatedAt    string   `json:"created_at"`
	// SecretUpdatedAt is shown as "rotated at"; never the hash itself.
	SecretUpdatedAt *string `json:"secret_updated_at"`
	DisabledAt      *string `json:"disabled_at"`
	// Grant tallies for the list view: users who ever authorized, live
	// authorizations, and the most recent gate-confirmed use.
	GrantCount   int64   `json:"grant_count"`
	ActiveGrants int64   `json:"active_grants"`
	LastUsedAt   *string `json:"last_used_at"`
}

// AdminOAuthGrantResponse is one row of the admin grants directory.
type AdminOAuthGrantResponse struct {
	ID         string  `json:"id"`
	ClientID   string  `json:"client_id"`
	ClientName *string `json:"client_name"`
	UserID     string  `json:"user_id"`
	UserName   *string `json:"user_name"`
	UserEmail  *string `json:"user_email"`
	Scope      string  `json:"scope"`
	CreatedAt  string  `json:"created_at"`
	LastUsedAt *string `json:"last_used_at"`
	RevokedAt  *string `json:"revoked_at"`
}

// CreateOAuthClientResponse is the one response that ever carries the
// plaintext secret.
type CreateOAuthClientResponse struct {
	Client AdminOAuthClientResponse `json:"client"`
	// Secret is shown exactly once. It exists nowhere else — not the
	// database (hash only), not the audit log, not any later response.
	Secret string `json:"secret"`
}

// RotateOAuthClientResponse mirrors the create response's one-shot
// contract; SecretUpdatedAt lets the UI show "rotated just now" without a
// refetch.
type RotateOAuthClientResponse struct {
	Secret          string `json:"secret"`
	SecretUpdatedAt string `json:"secret_updated_at"`
}

func pgTimePtr(t pgtype.Timestamptz) *string {
	if !t.Valid {
		return nil
	}
	s := t.Time.UTC().Format(time.RFC3339)
	return &s
}

func pgTextPtr(t pgtype.Text) *string {
	if !t.Valid {
		return nil
	}
	s := t.String
	return &s
}

func newAdminOAuthClientResponse(c db.OauthClient) AdminOAuthClientResponse {
	resp := AdminOAuthClientResponse{
		ID:              uuidToString(c.ID),
		ClientID:        c.ClientID,
		Name:            c.Name,
		RedirectURIs:    c.RedirectUris,
		CreatedAt:       c.CreatedAt.Time.UTC().Format(time.RFC3339),
		SecretUpdatedAt: pgTimePtr(c.SecretUpdatedAt),
		DisabledAt:      pgTimePtr(c.DisabledAt),
	}
	if c.CreatedBy.Valid {
		s := uuidToString(c.CreatedBy)
		resp.CreatedBy = &s
	}
	if c.RedirectUris == nil {
		resp.RedirectURIs = []string{}
	}
	return resp
}

func newAdminOAuthGrantRow(g db.ListOAuthGrantsRow) AdminOAuthGrantResponse {
	return AdminOAuthGrantResponse{
		ID:         uuidToString(g.ID),
		ClientID:   g.ClientID,
		ClientName: pgTextPtr(g.ClientName),
		UserID:     uuidToString(g.UserID),
		UserName:   pgTextPtr(g.UserName),
		UserEmail:  pgTextPtr(g.UserEmail),
		Scope:      g.Scope,
		CreatedAt:  g.CreatedAt.Time.UTC().Format(time.RFC3339),
		LastUsedAt: pgTimePtr(g.LastUsedAt),
		RevokedAt:  pgTimePtr(g.RevokedAt),
	}
}

func newAdminOAuthGrantOfClient(c db.OauthClient, g db.ListOAuthGrantsByClientRow) AdminOAuthGrantResponse {
	return AdminOAuthGrantResponse{
		ID:         uuidToString(g.ID),
		ClientID:   g.ClientID,
		ClientName: &c.Name,
		UserID:     uuidToString(g.UserID),
		UserName:   pgTextPtr(g.UserName),
		UserEmail:  pgTextPtr(g.UserEmail),
		Scope:      g.Scope,
		CreatedAt:  g.CreatedAt.Time.UTC().Format(time.RFC3339),
		LastUsedAt: pgTimePtr(g.LastUsedAt),
		RevokedAt:  pgTimePtr(g.RevokedAt),
	}
}

// loadOAuthClientByID addresses a client by row UUID (the management
// surface's stable key; the public client_id stays immutable).
func (h *Handler) loadOAuthClientByID(w http.ResponseWriter, r *http.Request) (db.OauthClient, bool) {
	id, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return db.OauthClient{}, false
	}
	client, err := h.Queries.GetOAuthClientByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "oauth client not found")
		return db.OauthClient{}, false
	}
	return client, true
}

// normalizeRedirectURIs trims and drops empty entries; validation itself
// is the shared oauth.ValidateRedirectURIs, so the UI and the CLI accept
// the same clients and both behave identically at authorize time.
func normalizeRedirectURIs(uris []string) []string {
	trimmed := make([]string, 0, len(uris))
	for _, uri := range uris {
		if uri = strings.TrimSpace(uri); uri != "" {
			trimmed = append(trimmed, uri)
		}
	}
	return trimmed
}

// AdminListOAuthClients GET /api/admin/oauth/clients
func (h *Handler) AdminListOAuthClients(w http.ResponseWriter, r *http.Request) {
	clients, err := h.Queries.ListOAuthClients(r.Context())
	if err != nil {
		slog.Error("admin: list oauth clients failed", "error", err)
		writeError(w, http.StatusInternalServerError, "could not list oauth clients")
		return
	}
	stats, err := h.Queries.CountOAuthGrantsByClient(r.Context())
	if err != nil {
		slog.Error("admin: oauth client grant stats failed", "error", err)
		// The directory renders without tallies rather than failing.
		stats = nil
	}
	type clientStat struct {
		count, active int64
		lastUsed      *string
	}
	byClient := make(map[string]clientStat, len(stats))
	for _, s := range stats {
		byClient[s.ClientID] = clientStat{count: s.GrantCount, active: s.ActiveGrantCount, lastUsed: pgTimePtr(s.LastUsedAt)}
	}
	resp := make([]AdminOAuthClientResponse, 0, len(clients))
	for _, c := range clients {
		out := newAdminOAuthClientResponse(c)
		if s, ok := byClient[c.ClientID]; ok {
			out.GrantCount = s.count
			out.ActiveGrants = s.active
			out.LastUsedAt = s.lastUsed
		}
		resp = append(resp, out)
	}
	writeJSON(w, http.StatusOK, map[string]any{"clients": resp})
}

// AdminCreateOAuthClient POST /api/admin/oauth/clients
// Body: { name, redirect_uris }.
func (h *Handler) AdminCreateOAuthClient(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name         string   `json:"name"`
		RedirectURIs []string `json:"redirect_uris"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" || len(name) > oauthClientNameMaxLen {
		writeError(w, http.StatusBadRequest, "name is required (max 200 chars)")
		return
	}
	uris := normalizeRedirectURIs(body.RedirectURIs)
	if err := oauth.ValidateRedirectURIs(uris); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	actorID := r.Header.Get("X-User-ID")

	clientID, err := oauth.NewClientSecret()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not generate client_id")
		return
	}
	secret, err := oauth.NewClientSecret()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not generate client secret")
		return
	}
	client, err := h.Queries.CreateOAuthClient(r.Context(), db.CreateOAuthClientParams{
		ClientID:         clientID,
		ClientSecretHash: auth.HashToken(secret),
		Name:             name,
		RedirectUris:     uris,
		CreatedBy:        parseUUID(actorID),
	})
	if err != nil {
		slog.Error("admin: create oauth client failed", "error", err)
		writeError(w, http.StatusInternalServerError, "could not create oauth client")
		return
	}
	h.writeAdminAudit(r.Context(), actorID, AuditActionOAuthClientCreate, AuditTargetTypeOAuthClient,
		client.ID, pgtype.UUID{}, adminReason(r.URL.Query().Get("reason")), map[string]any{
			"client_id": client.ClientID,
			"name":      client.Name,
		})
	slog.Info("admin: oauth client created", "client_id", client.ClientID)
	writeJSON(w, http.StatusCreated, CreateOAuthClientResponse{
		Client: newAdminOAuthClientResponse(client),
		Secret: secret,
	})
}

// AdminGetOAuthClient GET /api/admin/oauth/clients/{id} — the client plus
// its grants directory slice.
func (h *Handler) AdminGetOAuthClient(w http.ResponseWriter, r *http.Request) {
	client, ok := h.loadOAuthClientByID(w, r)
	if !ok {
		return
	}
	grants, err := h.Queries.ListOAuthGrantsByClient(r.Context(), client.ClientID)
	if err != nil {
		slog.Error("admin: oauth client grants failed", "error", err)
		writeError(w, http.StatusInternalServerError, "could not list grants")
		return
	}
	resp := struct {
		Client AdminOAuthClientResponse  `json:"client"`
		Grants []AdminOAuthGrantResponse `json:"grants"`
	}{Client: newAdminOAuthClientResponse(client), Grants: make([]AdminOAuthGrantResponse, 0, len(grants))}
	for _, g := range grants {
		resp.Grants = append(resp.Grants, newAdminOAuthGrantOfClient(client, g))
	}
	writeJSON(w, http.StatusOK, resp)
}

// AdminUpdateOAuthClient PATCH /api/admin/oauth/clients/{id}
// Body: { name, redirect_uris } — both fields replace in full.
func (h *Handler) AdminUpdateOAuthClient(w http.ResponseWriter, r *http.Request) {
	client, ok := h.loadOAuthClientByID(w, r)
	if !ok {
		return
	}
	var body struct {
		Name         string   `json:"name"`
		RedirectURIs []string `json:"redirect_uris"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" || len(name) > oauthClientNameMaxLen {
		writeError(w, http.StatusBadRequest, "name is required (max 200 chars)")
		return
	}
	uris := normalizeRedirectURIs(body.RedirectURIs)
	if err := oauth.ValidateRedirectURIs(uris); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	updated, err := h.Queries.UpdateOAuthClient(r.Context(), db.UpdateOAuthClientParams{
		ID:           client.ID,
		Name:         name,
		RedirectUris: uris,
	})
	if err != nil {
		slog.Error("admin: update oauth client failed", "error", err)
		writeError(w, http.StatusInternalServerError, "could not update oauth client")
		return
	}
	h.writeAdminAudit(r.Context(), r.Header.Get("X-User-ID"), AuditActionOAuthClientUpdate, AuditTargetTypeOAuthClient,
		client.ID, pgtype.UUID{}, adminReason(r.URL.Query().Get("reason")), map[string]any{
			"client_id":     client.ClientID,
			"name":          name,
			"redirect_uris": uris,
		})
	writeJSON(w, http.StatusOK, map[string]any{"client": newAdminOAuthClientResponse(updated)})
}

// AdminSetOAuthClientDisabled PATCH /api/admin/oauth/clients/{id}/disabled
// Body: { disabled, reason? }. Disabling revokes every live grant of the
// client (explicit application-layer cleanup, no DB cascade) and drops
// their gate-cache entries, so already-issued tokens stop passing within
// one write instead of one TTL.
func (h *Handler) AdminSetOAuthClientDisabled(w http.ResponseWriter, r *http.Request) {
	client, ok := h.loadOAuthClientByID(w, r)
	if !ok {
		return
	}
	var body struct {
		Disabled bool   `json:"disabled"`
		Reason   string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	actorID := r.Header.Get("X-User-ID")
	action := AuditActionOAuthClientEnable
	var at pgtype.Timestamptz
	var by pgtype.UUID
	if body.Disabled {
		action = AuditActionOAuthClientDisable
		at = pgtype.Timestamptz{Time: time.Now(), Valid: true}
		by = parseUUID(actorID)
	}
	updated, err := h.Queries.SetOAuthClientDisabled(r.Context(), db.SetOAuthClientDisabledParams{
		ID:         client.ID,
		DisabledAt: at,
		DisabledBy: by,
	})
	if err != nil {
		slog.Error("admin: set oauth client disabled failed", "error", err)
		writeError(w, http.StatusInternalServerError, "could not update oauth client")
		return
	}
	revokedCount := h.revokeGrantsForClient(r, client.ClientID)
	h.writeAdminAudit(r.Context(), actorID, action, AuditTargetTypeOAuthClient,
		client.ID, pgtype.UUID{}, adminReason(body.Reason), map[string]any{
			"client_id":      client.ClientID,
			"disabled":       body.Disabled,
			"grants_revoked": revokedCount,
		})
	slog.Info("admin: oauth client disabled state changed", "client_id", client.ClientID, "disabled", body.Disabled, "grants_revoked", revokedCount)
	writeJSON(w, http.StatusOK, map[string]any{"client": newAdminOAuthClientResponse(updated)})
}

// revokeGrantsForClient revokes every live grant of a client and drops the
// gate-cache entries. Best effort on the cache: a missed invalidation
// expires within the gate TTL. Returns the number revoked for audit.
func (h *Handler) revokeGrantsForClient(r *http.Request, clientID string) int {
	revoked, err := h.Queries.RevokeOAuthGrantsByClient(r.Context(), clientID)
	if err != nil {
		slog.Error("admin: revoke client grants failed", "client_id", clientID, "error", err)
		return 0
	}
	for _, id := range revoked {
		if h.OAuthGate != nil {
			h.OAuthGate.Invalidate(r.Context(), uuidToString(id))
		}
	}
	return len(revoked)
}

// AdminRotateOAuthClientSecret POST /api/admin/oauth/clients/{id}/rotate
// The new hash replaces the old one in place: the old secret stops
// authenticating at the token endpoint on its next presentation (no
// dual-hash grace period, per the RUYI-420 design). Response carries the
// plaintext once.
func (h *Handler) AdminRotateOAuthClientSecret(w http.ResponseWriter, r *http.Request) {
	client, ok := h.loadOAuthClientByID(w, r)
	if !ok {
		return
	}
	secret, err := oauth.NewClientSecret()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not generate client secret")
		return
	}
	updated, err := h.Queries.RotateOAuthClientSecret(r.Context(), db.RotateOAuthClientSecretParams{
		ID:               client.ID,
		ClientSecretHash: auth.HashToken(secret),
	})
	if err != nil {
		slog.Error("admin: rotate oauth client secret failed", "error", err)
		writeError(w, http.StatusInternalServerError, "could not rotate secret")
		return
	}
	h.writeAdminAudit(r.Context(), r.Header.Get("X-User-ID"), AuditActionOAuthClientRotate, AuditTargetTypeOAuthClient,
		client.ID, pgtype.UUID{}, adminReason(r.URL.Query().Get("reason")), map[string]any{
			"client_id": client.ClientID,
		})
	slog.Info("admin: oauth client secret rotated", "client_id", client.ClientID)
	writeJSON(w, http.StatusOK, RotateOAuthClientResponse{
		Secret:          secret,
		SecretUpdatedAt: updated.SecretUpdatedAt.Time.UTC().Format(time.RFC3339),
	})
}

// AdminDeleteOAuthClient DELETE /api/admin/oauth/clients/{id}
// Hard delete, matching the CLI verb; the UI confirms loudly. Grants are
// revoked (not deleted) first, so the audit trail and the "my
// authorizations" history survive the client.
func (h *Handler) AdminDeleteOAuthClient(w http.ResponseWriter, r *http.Request) {
	client, ok := h.loadOAuthClientByID(w, r)
	if !ok {
		return
	}
	revokedCount := h.revokeGrantsForClient(r, client.ClientID)
	if err := h.Queries.DeleteOAuthClientByID(r.Context(), client.ID); err != nil {
		slog.Error("admin: delete oauth client failed", "error", err)
		writeError(w, http.StatusInternalServerError, "could not delete oauth client")
		return
	}
	h.writeAdminAudit(r.Context(), r.Header.Get("X-User-ID"), AuditActionOAuthClientDelete, AuditTargetTypeOAuthClient,
		client.ID, pgtype.UUID{}, adminReason(r.URL.Query().Get("reason")), map[string]any{
			"client_id":      client.ClientID,
			"name":           client.Name,
			"grants_revoked": revokedCount,
		})
	slog.Info("admin: oauth client deleted", "client_id", client.ClientID, "grants_revoked", revokedCount)
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// AdminListOAuthGrants GET /api/admin/oauth/grants — the instance-wide
// directory of user → client authorizations.
func (h *Handler) AdminListOAuthGrants(w http.ResponseWriter, r *http.Request) {
	grants, err := h.Queries.ListOAuthGrants(r.Context())
	if err != nil {
		slog.Error("admin: list oauth grants failed", "error", err)
		writeError(w, http.StatusInternalServerError, "could not list grants")
		return
	}
	resp := make([]AdminOAuthGrantResponse, 0, len(grants))
	for _, g := range grants {
		resp = append(resp, newAdminOAuthGrantRow(g))
	}
	writeJSON(w, http.StatusOK, map[string]any{"grants": resp})
}

// AdminRevokeOAuthGrant DELETE /api/admin/oauth/grants/{id}
// Idempotent: the revoke UPDATE skips already-revoked rows, so an empty
// result means "unknown id" (404) or "already revoked" (idempotent success
// that leaves the original revoked_at alone).
func (h *Handler) AdminRevokeOAuthGrant(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	grant, err := h.Queries.RevokeOAuthGrantByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// The conditional UPDATE matched nothing. Grants are never
			// hard-deleted, so a row that still exists is already revoked —
			// answer success rather than 404 so double-revoking in the
			// directory stays a no-op instead of an error toast.
			if _, gerr := h.Queries.GetOAuthGrantByID(r.Context(), id); gerr == nil {
				writeJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
				return
			}
			writeError(w, http.StatusNotFound, "grant not found")
			return
		}
		slog.Error("admin: revoke oauth grant failed", "error", err)
		writeError(w, http.StatusInternalServerError, "could not revoke grant")
		return
	}
	if h.OAuthGate != nil {
		h.OAuthGate.Invalidate(r.Context(), uuidToString(grant.ID))
	}
	h.writeAdminAudit(r.Context(), r.Header.Get("X-User-ID"), AuditActionOAuthGrantRevoke, AuditTargetTypeOAuthGrant,
		grant.ID, pgtype.UUID{}, adminReason(r.URL.Query().Get("reason")), map[string]any{
			"client_id": grant.ClientID,
			"user_id":   uuidToString(grant.UserID),
		})
	slog.Info("admin: oauth grant revoked", "grant_id", uuidToString(grant.ID), "client_id", grant.ClientID)
	writeJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
}

// AdminMCPServerStatusResponse is the MCP Server page payload: the
// authorization server half (this process) plus the Node process half.
type AdminMCPServerStatusResponse struct {
	OAuth   AdminMCPOAuthStatus   `json:"oauth"`
	MCP     AdminMCPProcessStatus `json:"mcp"`
	Clients AdminCounts           `json:"clients"`
	Grants  AdminCounts           `json:"grants"`
}

// AdminMCPOAuthStatus describes the authorization server. Endpoint URLs
// derive from the same site root the discovery documents use, so the page
// always agrees with what clients actually fetch.
type AdminMCPOAuthStatus struct {
	Enabled               bool   `json:"enabled"`
	Issuer                string `json:"issuer"`
	KeyID                 string `json:"key_id"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	JWKSURL               string `json:"jwks_url"`
	ProtectedResourceURL  string `json:"protected_resource_url"`
}

// AdminMCPProcessStatus describes the Node MCP process. When MCP_URL is
// unset or the process unreachable, Reachable=false and the page says so —
// that is the diagnostic, not an error.
type AdminMCPProcessStatus struct {
	URLConfigured bool               `json:"url_configured"`
	Reachable     bool               `json:"reachable"`
	Version       string             `json:"version"`
	ToolCount     int64              `json:"tool_count"`
	Tools         []AdminMCPToolInfo `json:"tools"`
}

type AdminMCPToolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type AdminCounts struct {
	Total  int64 `json:"total"`
	Active int64 `json:"active"`
}

// AdminMCPServerStatus GET /api/admin/mcp/status — the MCP Server page's
// read model. Read-only; nothing here can start an agent run.
func (h *Handler) AdminMCPServerStatus(w http.ResponseWriter, r *http.Request) {
	resp := AdminMCPServerStatusResponse{
		OAuth: AdminMCPOAuthStatus{Enabled: h.OAuthSigner != nil},
		MCP:   AdminMCPProcessStatus{URLConfigured: os.Getenv("MCP_URL") != ""},
	}
	root := h.oauthSiteRoot()
	resp.OAuth.Issuer = root
	resp.OAuth.ProtectedResourceURL = root + oauth.MCPResourcePath
	if h.OAuthSigner != nil {
		resp.OAuth.KeyID = h.OAuthSigner.KeyID()
		resp.OAuth.AuthorizationEndpoint = root + "/auth/oauth/authorize"
		resp.OAuth.TokenEndpoint = root + "/auth/oauth/token"
		resp.OAuth.JWKSURL = root + "/.well-known/jwks.json"
	}
	if clients, err := h.Queries.CountOAuthClients(r.Context()); err == nil {
		resp.Clients = AdminCounts{Total: clients.Total, Active: clients.Active}
	}
	if grants, err := h.Queries.CountOAuthGrants(r.Context()); err == nil {
		resp.Grants = AdminCounts{Total: grants.Total, Active: grants.Active}
	}
	// The Node process owns the tool catalogue; reach it only when a URL is
	// configured. Any failure degrades the page, never fails the request.
	if url := strings.TrimSpace(os.Getenv("MCP_URL")); url != "" {
		resp.MCP = h.fetchMCPDiagnostics(r.Context(), url, resp.MCP)
	} else {
		resp.MCP.Tools = []AdminMCPToolInfo{}
	}
	writeJSON(w, http.StatusOK, resp)
}

// fetchMCPDiagnostics probes the Node MCP process's loopback-only
// diagnostic endpoint. Every failure mode collapses into
// Reachable=false — the page shows degraded state, and no error here is
// the caller's fault.
func (h *Handler) fetchMCPDiagnostics(ctx context.Context, baseURL string, base AdminMCPProcessStatus) AdminMCPProcessStatus {
	ctx, cancel := context.WithTimeout(ctx, mcpDiagTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/diag", nil)
	if err != nil {
		return base
	}
	httpResp, err := http.DefaultClient.Do(req)
	if err != nil {
		return base
	}
	defer httpResp.Body.Close()
	if httpResp.StatusCode != http.StatusOK {
		return base
	}
	var diag struct {
		Version string `json:"version"`
		Tools   []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"tools"`
	}
	if err := json.NewDecoder(httpResp.Body).Decode(&diag); err != nil {
		return base
	}
	base.Reachable = true
	base.Version = diag.Version
	base.ToolCount = int64(len(diag.Tools))
	base.Tools = make([]AdminMCPToolInfo, 0, len(diag.Tools))
	for _, t := range diag.Tools {
		base.Tools = append(base.Tools, AdminMCPToolInfo{Name: t.Name, Description: t.Description})
	}
	return base
}
