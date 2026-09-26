package handler

// MCP OAuth authorization server (RUYI-209).
//
// Scope, and why it is this small: ChatGPT needs an OAuth 2.0 authorization
// endpoint it can drive with PKCE, and the only resource behind it is the MCP
// endpoint at /api/mcp. So this file implements the authorization-code grant
// with S256 and nothing else — no DCR, no refresh tokens, no scope catalogue,
// no consent screen beyond the login the user already has. Each of those is a
// separate decision the first version deliberately defers; see
// docs/adr/001-mcp-oauth-behind-nextjs-proxy.md §3.6-§3.7.
//
// The whole surface is switched off unless OAUTH_SIGNING_KEY is configured:
// every endpoint here returns 501 when h.OAuthSigner is nil, and the router
// does not publish the discovery documents. That is the rollback path — clearing
// one environment variable returns the deployment to pure-PAT behaviour without
// reverting code (ADR §7).
//
// Logging discipline: an authorization failure records the error type and, when
// known, the client_id. The token, the authorization code, the code_verifier,
// the client_secret and the raw Authorization header never reach a log line.

import (
	"crypto/subtle"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/oauth"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// oauthDisabled answers every OAuth endpoint when no signing key is configured.
//
// 501 rather than 404: the endpoint exists in this build, it is the deployment
// that has not enabled it, and an operator reading the response should be able
// to tell those apart. ADR §5 lists "silent 404" as the failure mode to avoid.
func (h *Handler) oauthDisabled(w http.ResponseWriter) bool {
	if h.OAuthSigner != nil {
		return false
	}
	writeJSON(w, http.StatusNotImplemented, map[string]string{
		"error":             "oauth_not_configured",
		"error_description": "This deployment has no OAUTH_SIGNING_KEY configured, so the MCP authorization server is disabled.",
	})
	return true
}

// GetOAuthProtectedResourceMetadata serves the RFC 9728 document.
//
// Registered as a wildcard so it answers both the bare path and the
// path-insertion form (/.well-known/oauth-protected-resource/api/mcp): which
// one ChatGPT probes is not documented, and serving both costs one route.
func (h *Handler) GetOAuthProtectedResourceMetadata(w http.ResponseWriter, r *http.Request) {
	if h.oauthDisabled(w) {
		return
	}
	writeJSON(w, http.StatusOK, oauth.ProtectedResourceMetadata(h.oauthSiteRoot()))
}

// GetOAuthAuthorizationServerMetadata serves the RFC 8414 document.
func (h *Handler) GetOAuthAuthorizationServerMetadata(w http.ResponseWriter, r *http.Request) {
	if h.oauthDisabled(w) {
		return
	}
	writeJSON(w, http.StatusOK, oauth.AuthorizationServerMetadata(h.oauthSiteRoot()))
}

// GetOAuthJWKS publishes the public half of the access-token signing key.
func (h *Handler) GetOAuthJWKS(w http.ResponseWriter, r *http.Request) {
	if h.oauthDisabled(w) {
		return
	}
	writeJSON(w, http.StatusOK, h.OAuthSigner.JWKS())
}

// oauthSiteRoot is the origin every issued URL and the token `iss` are built
// from. It comes from configuration (MULTICA_APP_URL / FRONTEND_ORIGIN), never
// from the request's Host header: a client reads these documents to decide
// where to send its credentials, so a spoofed Host must not be able to point it
// somewhere else.
func (h *Handler) oauthSiteRoot() string {
	return strings.TrimRight(h.cfg.AppURL, "/")
}

// AuthorizeOAuth is the authorization endpoint.
//
// It runs in the user's browser, so its two failure modes are different in
// kind: a broken request from the client is rendered here (returning it to an
// unvalidated redirect_uri would make this an open redirector), while a failure
// the validated client should learn about goes back to redirect_uri as an
// `error` parameter, per RFC 6749 §4.1.2.1.
func (h *Handler) AuthorizeOAuth(w http.ResponseWriter, r *http.Request) {
	if h.oauthDisabled(w) {
		return
	}
	q := r.URL.Query()
	clientID := strings.TrimSpace(q.Get("client_id"))
	redirectURI := strings.TrimSpace(q.Get("redirect_uri"))

	client, ok := h.loadOAuthClient(r, clientID)
	if !ok {
		// Rendered locally on purpose: with an unknown client_id there is no
		// registered redirect_uri to trust with the error.
		h.renderOAuthError(w, http.StatusBadRequest, "invalid_client", "Unknown client_id.")
		return
	}
	if !redirectURIAllowed(client.RedirectUris, redirectURI) {
		// Also local: bouncing this to the submitted URI is exactly the open
		// redirect the registered list exists to prevent.
		slog.Warn("oauth: authorize rejected unregistered redirect_uri", "client_id", clientID)
		h.renderOAuthError(w, http.StatusBadRequest, "invalid_request", "redirect_uri is not registered for this client.")
		return
	}

	// From here the redirect target is trusted, so protocol errors travel back
	// to the client where it can act on them.
	if q.Get("response_type") != "code" {
		h.redirectOAuthError(w, r, redirectURI, q.Get("state"), "unsupported_response_type", "Only response_type=code is supported.")
		return
	}
	challenge := strings.TrimSpace(q.Get("code_challenge"))
	method := strings.TrimSpace(q.Get("code_challenge_method"))
	if challenge == "" {
		// PKCE is mandatory, not negotiated: this server has one grant and one
		// client class, and a public client without PKCE has no protection at
		// all against a stolen code.
		h.redirectOAuthError(w, r, redirectURI, q.Get("state"), "invalid_request", "code_challenge is required (PKCE S256).")
		return
	}
	if method != oauth.MethodS256 {
		h.redirectOAuthError(w, r, redirectURI, q.Get("state"), "invalid_request", "code_challenge_method must be S256.")
		return
	}

	resource := strings.TrimSpace(q.Get("resource"))
	if resource != "" && !h.oauthResourceAccepted(resource) {
		h.redirectOAuthError(w, r, redirectURI, q.Get("state"), "invalid_target", "resource does not name this server's MCP endpoint.")
		return
	}

	userID, ok := h.oauthSessionUser(r)
	if !ok {
		// Not signed in. Send the browser to the login page with `next` set to
		// this exact authorization request, so consent resumes after login
		// instead of the client seeing a failure it cannot fix.
		h.redirectToLogin(w, r)
		return
	}

	if !h.OAuthCodes.Available() {
		slog.Error("oauth: authorization code store unavailable", "client_id", clientID)
		h.redirectOAuthError(w, r, redirectURI, q.Get("state"), "temporarily_unavailable", "The authorization code store is unavailable.")
		return
	}
	code, err := oauth.NewAuthorizationCode()
	if err != nil {
		slog.Error("oauth: failed to generate authorization code", "client_id", clientID, "error", err)
		h.redirectOAuthError(w, r, redirectURI, q.Get("state"), "server_error", "Could not issue an authorization code.")
		return
	}
	if err := h.OAuthCodes.Save(r.Context(), code, oauth.AuthorizationCode{
		ClientID:            clientID,
		UserID:              userID,
		RedirectURI:         redirectURI,
		CodeChallenge:       challenge,
		CodeChallengeMethod: method,
		Resource:            resource,
		Scope:               oauth.ScopeMCP,
	}); err != nil {
		slog.Error("oauth: failed to store authorization code", "client_id", clientID, "error", err)
		h.redirectOAuthError(w, r, redirectURI, q.Get("state"), "server_error", "Could not issue an authorization code.")
		return
	}

	target, err := url.Parse(redirectURI)
	if err != nil {
		h.renderOAuthError(w, http.StatusBadRequest, "invalid_request", "redirect_uri is not a valid URL.")
		return
	}
	params := target.Query()
	params.Set("code", code)
	if state := q.Get("state"); state != "" {
		params.Set("state", state)
	}
	target.RawQuery = params.Encode()
	http.Redirect(w, r, target.String(), http.StatusFound)
}

// TokenOAuth is the token endpoint: it exchanges a single-use authorization
// code for an access token.
func (h *Handler) TokenOAuth(w http.ResponseWriter, r *http.Request) {
	if h.oauthDisabled(w) {
		return
	}
	if err := r.ParseForm(); err != nil {
		writeOAuthTokenError(w, http.StatusBadRequest, "invalid_request", "Could not parse the request body.")
		return
	}
	if r.PostForm.Get("grant_type") != "authorization_code" {
		writeOAuthTokenError(w, http.StatusBadRequest, "unsupported_grant_type", "Only grant_type=authorization_code is supported.")
		return
	}

	clientID, clientSecret := oauthClientCredentials(r)
	client, ok := h.loadOAuthClient(r, clientID)
	if !ok {
		writeOAuthTokenError(w, http.StatusUnauthorized, "invalid_client", "Unknown client_id.")
		return
	}
	// Constant-time so a caller cannot learn the secret one byte at a time from
	// response timing.
	if subtle.ConstantTimeCompare([]byte(auth.HashToken(clientSecret)), []byte(client.ClientSecretHash)) != 1 {
		slog.Warn("oauth: token request presented a wrong client secret", "client_id", clientID)
		writeOAuthTokenError(w, http.StatusUnauthorized, "invalid_client", "Client authentication failed.")
		return
	}

	if !h.OAuthCodes.Available() {
		slog.Error("oauth: authorization code store unavailable", "client_id", clientID)
		writeOAuthTokenError(w, http.StatusServiceUnavailable, "temporarily_unavailable", "The authorization code store is unavailable.")
		return
	}
	// Consuming before validating is intentional: the code is spent by the
	// attempt, so a failed exchange cannot be retried with a different verifier.
	// That is what makes a brute-force search over verifiers impossible rather
	// than merely slow.
	stored, err := h.OAuthCodes.Consume(r.Context(), strings.TrimSpace(r.PostForm.Get("code")))
	if err != nil {
		if errors.Is(err, oauth.ErrCodeNotFound) {
			slog.Warn("oauth: token request presented an unknown or spent code", "client_id", clientID)
			writeOAuthTokenError(w, http.StatusBadRequest, "invalid_grant", "The authorization code is invalid, expired, or already used.")
			return
		}
		slog.Error("oauth: failed to read authorization code", "client_id", clientID, "error", err)
		writeOAuthTokenError(w, http.StatusServiceUnavailable, "temporarily_unavailable", "Could not read the authorization code.")
		return
	}

	// The code is bound to the client that requested it: one registered client
	// must not be able to redeem another's code.
	if stored.ClientID != clientID {
		slog.Warn("oauth: token request redeemed another client's code", "client_id", clientID)
		writeOAuthTokenError(w, http.StatusBadRequest, "invalid_grant", "The authorization code was not issued to this client.")
		return
	}
	// RFC 6749 §4.1.3 requires the same redirect_uri as the authorization step.
	if redirectURI := strings.TrimSpace(r.PostForm.Get("redirect_uri")); redirectURI != stored.RedirectURI {
		slog.Warn("oauth: token request redirect_uri did not match the authorization request", "client_id", clientID)
		writeOAuthTokenError(w, http.StatusBadRequest, "invalid_grant", "redirect_uri does not match the authorization request.")
		return
	}
	// The resource indicator must match too (RFC 8707 §2.2): otherwise a client
	// could authorize for one resource and mint a token addressed to another.
	requestedResource := strings.TrimSpace(r.PostForm.Get("resource"))
	if requestedResource != "" && requestedResource != stored.Resource {
		slog.Warn("oauth: token request resource did not match the authorization request", "client_id", clientID)
		writeOAuthTokenError(w, http.StatusBadRequest, "invalid_target", "resource does not match the authorization request.")
		return
	}

	if err := oauth.VerifyPKCE(stored.CodeChallenge, stored.CodeChallengeMethod, r.PostForm.Get("code_verifier")); err != nil {
		// Error type only. The verifier is the secret this check exists to
		// protect; logging it would defeat the mechanism.
		slog.Warn("oauth: PKCE verification failed", "client_id", clientID, "reason", pkceFailureReason(err))
		writeOAuthTokenError(w, http.StatusBadRequest, "invalid_grant", "code_verifier does not match the code_challenge.")
		return
	}

	accessToken, expiresAt, err := h.OAuthSigner.MintAccessToken(stored.UserID, stored.Resource, stored.Scope, time.Now())
	if err != nil {
		slog.Error("oauth: failed to mint access token", "client_id", clientID, "error", err)
		writeOAuthTokenError(w, http.StatusInternalServerError, "server_error", "Could not issue an access token.")
		return
	}

	// No refresh_token: the first version issues none (ADR §3.5), and returning
	// the field empty would tell a client to attempt a grant that does not exist.
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token": accessToken,
		"token_type":   "Bearer",
		"expires_in":   int(time.Until(expiresAt).Seconds()),
		"scope":        stored.Scope,
	})
}

// loadOAuthClient reads a registered client. A blank client_id is rejected
// without a query.
func (h *Handler) loadOAuthClient(r *http.Request, clientID string) (db.OauthClient, bool) {
	if clientID == "" || h.Queries == nil {
		return db.OauthClient{}, false
	}
	client, err := h.Queries.GetOAuthClientByClientID(r.Context(), clientID)
	if err != nil {
		slog.Warn("oauth: unknown client_id", "client_id", clientID)
		return db.OauthClient{}, false
	}
	return client, true
}

// oauthClientCredentials reads client authentication from either of the two
// methods the metadata document advertises: client_secret_post (form fields)
// and client_secret_basic (the Authorization header). The raw header value is
// never logged.
func oauthClientCredentials(r *http.Request) (clientID, clientSecret string) {
	if id, secret, ok := r.BasicAuth(); ok {
		return strings.TrimSpace(id), secret
	}
	return strings.TrimSpace(r.PostForm.Get("client_id")), r.PostForm.Get("client_secret")
}

// redirectURIAllowed matches the submitted redirect_uri against the registered
// list exactly. No prefix or wildcard matching: a prefix match on an
// attacker-chosen suffix is a classic redirect-hijack vector, and the client
// list is operator-maintained, so an exact value is always available.
func redirectURIAllowed(registered []string, candidate string) bool {
	if candidate == "" {
		return false
	}
	for _, allowed := range registered {
		if allowed == candidate {
			return true
		}
	}
	return false
}

// oauthResourceAccepted reports whether a resource indicator names something
// this deployment actually serves.
func (h *Handler) oauthResourceAccepted(resource string) bool {
	root := h.oauthSiteRoot()
	return resource == root+oauth.MCPResourcePath || resource == root
}

// oauthSessionUser resolves the signed-in user from the session cookie.
//
// The authorization endpoint runs in a browser, so the session cookie is the
// only credential available. It is the same HS256 cookie the rest of the app
// uses, verified here directly rather than behind middleware.Auth because an
// unauthenticated visit is not an error — it redirects to login.
func (h *Handler) oauthSessionUser(r *http.Request) (string, bool) {
	cookie, err := r.Cookie(auth.AuthCookieName)
	if err != nil || cookie.Value == "" {
		return "", false
	}
	token, err := jwt.Parse(cookie.Value, func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, jwt.ErrSignatureInvalid
		}
		return auth.JWTSecret(), nil
	}, jwt.WithValidMethods([]string{"HS256"}))
	if err != nil || !token.Valid {
		return "", false
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return "", false
	}
	subject, _ := claims["sub"].(string)
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return "", false
	}
	// A deleted or disabled account must not be able to mint a 90-day token.
	if _, err := util.ParseUUID(subject); err != nil {
		return "", false
	}
	return subject, true
}

// redirectToLogin sends an unauthenticated visitor to the login page with
// `next` pointing back at this authorization request.
//
// `next` carries only the path and query, never an absolute URL: the login page
// sanitizes it to a same-origin path (packages/core/auth/utils.ts
// sanitizeNextUrl), and handing it anything else would simply be dropped.
func (h *Handler) redirectToLogin(w http.ResponseWriter, r *http.Request) {
	next := r.URL.Path
	if r.URL.RawQuery != "" {
		next += "?" + r.URL.RawQuery
	}
	login := h.oauthSiteRoot() + "/login?next=" + url.QueryEscape(next)
	http.Redirect(w, r, login, http.StatusFound)
}

// redirectOAuthError returns a protocol error to a validated redirect_uri.
func (h *Handler) redirectOAuthError(w http.ResponseWriter, r *http.Request, redirectURI, state, code, description string) {
	target, err := url.Parse(redirectURI)
	if err != nil {
		h.renderOAuthError(w, http.StatusBadRequest, "invalid_request", "redirect_uri is not a valid URL.")
		return
	}
	params := target.Query()
	params.Set("error", code)
	params.Set("error_description", description)
	if state != "" {
		params.Set("state", state)
	}
	target.RawQuery = params.Encode()
	http.Redirect(w, r, target.String(), http.StatusFound)
}

// renderOAuthError answers the browser directly, for the failures that must not
// be forwarded to a redirect_uri.
func (h *Handler) renderOAuthError(w http.ResponseWriter, status int, code, description string) {
	writeJSON(w, status, map[string]string{
		"error":             code,
		"error_description": description,
	})
}

func writeOAuthTokenError(w http.ResponseWriter, status int, code, description string) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, status, map[string]string{
		"error":             code,
		"error_description": description,
	})
}

// pkceFailureReason maps a PKCE error to a short label safe to log.
func pkceFailureReason(err error) string {
	switch {
	case errors.Is(err, oauth.ErrUnsupportedChallengeMethod):
		return "unsupported_challenge_method"
	case errors.Is(err, oauth.ErrVerifierLength):
		return "verifier_length"
	case errors.Is(err, oauth.ErrVerifierMismatch):
		return "verifier_mismatch"
	default:
		return "invalid_verifier"
	}
}
