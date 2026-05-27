package lark

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// OAuthConfig captures the deployment-level OAuth knobs for the
// Multica-owned Lark app. Self-host operators set these via env vars
// when they want the Lark integration enabled. When AppID is empty
// the OAuth surface returns 503 — the manual-paste InstallationService
// path keeps working for operators that prefer that flow.
type OAuthConfig struct {
	// AppID is the Multica Lark app's app_id (the parent app users
	// install PersonalAgent bots from). Empty disables OAuth.
	AppID string

	// AppSecret authenticates Multica when exchanging the OAuth code
	// for installation credentials.
	AppSecret string

	// RedirectURI is the absolute URL Lark calls back after the user
	// authorizes the install. Must be registered in the Lark app
	// console. We do NOT derive it from request headers because a
	// reverse-proxy misconfiguration would let Lark land on the wrong
	// host.
	RedirectURI string

	// AuthorizeBaseURL is the Lark OAuth authorization endpoint
	// (https://accounts.feishu.cn/open-apis/authen/v1/authorize in
	// production). Configurable so dev / staging can point at a Lark
	// Beta endpoint.
	AuthorizeBaseURL string

	// StateSigningSecret is the HMAC key used to sign the OAuth state
	// token (binds workspace + agent into the callback). MUST be at
	// least 32 bytes. The token is opaque from the user's perspective.
	StateSigningSecret string

	// StateTTL caps how long an issued state token is valid. Default
	// 10 minutes; long enough for the user to walk through the Lark
	// authorize UI, short enough that an intercepted state cannot be
	// replayed days later.
	StateTTL time.Duration

	// FrontendSuccessURL is the post-install destination on the
	// Multica frontend. The callback redirects users here with
	// `?lark_installed=1&workspace=<slug>&installation=<id>` so the
	// settings/agent page can show confirmation copy without polling.
	// Empty defaults to "/settings?tab=lark".
	FrontendSuccessURL string

	// FrontendErrorURL is the post-failure destination. Empty defaults
	// to the same path as FrontendSuccessURL.
	FrontendErrorURL string

	// Now / Clock for tests.
	Now func() time.Time
}

func (c OAuthConfig) withDefaults() OAuthConfig {
	if c.StateTTL == 0 {
		c.StateTTL = 10 * time.Minute
	}
	if c.AuthorizeBaseURL == "" {
		c.AuthorizeBaseURL = "https://accounts.feishu.cn/open-apis/authen/v1/authorize"
	}
	if c.FrontendSuccessURL == "" {
		c.FrontendSuccessURL = "/settings?tab=lark"
	}
	if c.FrontendErrorURL == "" {
		c.FrontendErrorURL = c.FrontendSuccessURL
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	return c
}

// Enabled reports whether OAuth installation is configured for this
// deployment. The HTTP layer uses this for the 503 short-circuit so
// manual-install operators are not forced to also configure OAuth.
func (c OAuthConfig) Enabled() bool {
	return c.AppID != "" && c.AppSecret != "" && c.RedirectURI != "" && c.StateSigningSecret != ""
}

// InstallerBinder is the narrow surface OAuthService needs to record
// the installer's lark_user_binding row in the same business step as
// the installation lookup. Without this step the first inbound
// message from the installer would be dropped as `unbound_user`
// (and the Bot would reply "you're not bound, click here…" to the
// person who just clicked "authorize" seconds ago).
//
// Implementations MUST be idempotent on (installation_id, lark_open_id):
// a re-authorize by the same user should not error.
//
// `qtx` is the *db.Queries handle to run the bind against. The
// OAuth service opens a transaction so the installation read and the
// binding write commit together; nil means "use the service's
// own (non-transactional) queries handle".
type InstallerBinder interface {
	BindInstallerTx(ctx context.Context, qtx *db.Queries, p InstallerBindParams) error
}

// InstallerBindParams carries the inputs InstallerBinder needs. Kept
// as a struct so adding union_id (Phase 2) does not break callers.
type InstallerBindParams struct {
	WorkspaceID    pgtype.UUID
	InstallationID pgtype.UUID
	MulticaUserID  pgtype.UUID // the installer's Multica account
	LarkOpenID     OpenID      // the installer's per-installation open_id
}

// OAuthService coordinates the install start / callback dance.
//
// Scope after the MVP must-fix round: the OAuth flow is identity-only.
// The v2 user-OAuth chain (/authen/v2/oauth/token + /authen/v1/user_info)
// delivers the installer's open_id but NOT per-installation bot
// credentials, so HandleCallback no longer writes lark_installation
// rows. It binds the installer onto an installation already
// provisioned via the manual-paste POST /lark/installations route.
// When the PersonalAgent install API is integrated in a follow-up
// PR, this surface can grow back to single-step scan-to-install
// without a schema change.
//
// The lookup-and-bind run inside a single DB transaction so a
// concurrent revoke / re-provision cannot land between the read and
// the binding insert.
type OAuthService struct {
	cfg     OAuthConfig
	client  APIClient
	queries *db.Queries
	tx      TxStarter
	binder  InstallerBinder
}

// NewOAuthService constructs an OAuthService. cfg may be the zero
// value — Enabled() will simply return false and StartInstall/Callback
// will surface ErrOAuthNotConfigured. queries / tx / binder are
// required; the OAuth install journey reads + writes inside a single
// transaction, so refusing to construct without those is the safer
// default than allowing a silent regression.
func NewOAuthService(cfg OAuthConfig, client APIClient, queries *db.Queries, tx TxStarter, binder InstallerBinder) (*OAuthService, error) {
	cfg = cfg.withDefaults()
	if client == nil {
		return nil, errors.New("lark oauth: APIClient is required")
	}
	if queries == nil {
		return nil, errors.New("lark oauth: queries is required")
	}
	if tx == nil {
		return nil, errors.New("lark oauth: TxStarter is required")
	}
	if binder == nil {
		return nil, errors.New("lark oauth: InstallerBinder is required")
	}
	return &OAuthService{cfg: cfg, client: client, queries: queries, tx: tx, binder: binder}, nil
}

// StartInstallParams carries the workspace + agent the install will
// bind to. The handler sources both from the URL path (workspace) and
// the query (agent) and has already validated workspace membership
// (admin-only at the router) and agent ↔ workspace ownership.
type StartInstallParams struct {
	WorkspaceID pgtype.UUID
	AgentID     pgtype.UUID
	InitiatorID pgtype.UUID
}

// StartInstallResult is what StartInstall returns to the handler.
// `URL` is the absolute Lark authorization URL the frontend should
// open in a new tab or display as a QR. `State` is exposed for tests
// and for handlers that want to log the binding (do NOT echo to the
// user).
type StartInstallResult struct {
	URL   string
	State string
}

// StartInstall builds a signed-state OAuth URL the user opens to
// authorize the install. The state token binds the workspace, agent,
// and initiating user so the callback can persist the installation
// against the correct rows without trusting query params.
func (s *OAuthService) StartInstall(p StartInstallParams) (StartInstallResult, error) {
	if !s.cfg.Enabled() {
		return StartInstallResult{}, ErrOAuthNotConfigured
	}
	if !p.WorkspaceID.Valid || !p.AgentID.Valid || !p.InitiatorID.Valid {
		return StartInstallResult{}, errors.New("workspace, agent, and initiator are required")
	}
	state, err := s.signState(uuidString(p.WorkspaceID), uuidString(p.AgentID), uuidString(p.InitiatorID))
	if err != nil {
		return StartInstallResult{}, fmt.Errorf("sign state: %w", err)
	}
	u := s.buildAuthorizeURL(state)
	return StartInstallResult{URL: u, State: state}, nil
}

// CallbackParams is what the handler hands to HandleCallback after
// pulling values out of the query string.
type CallbackParams struct {
	Code  string
	State string
}

// CallbackResult is what HandleCallback returns: the persisted
// installation row plus a redirect URL the handler should bounce the
// browser to.
type CallbackResult struct {
	WorkspaceID    pgtype.UUID
	AgentID        pgtype.UUID
	InstallationID pgtype.UUID
	InstallerOpenID OpenID
	RedirectURL    string
}

// HandleCallback finishes the OAuth identity-bind step: verify state
// → exchange code for installer identity → look up the
// already-provisioned lark_installation by (workspace, agent) →
// bind the installer onto it. The lookup and bind run in a single DB
// transaction so a concurrent revoke / re-provision cannot land
// between read and bind.
//
// `ErrInstallationNotProvisioned` is returned when no installation
// exists yet for (workspace, agent) — the callback handler maps it
// to the `installation_not_provisioned` reason so the UI can ask
// the admin to provision via the manual-paste route first.
//
// The handler is responsible for the HTTP-side redirect using the
// returned URL; this keeps the service HTTP-free for tests.
func (s *OAuthService) HandleCallback(ctx context.Context, p CallbackParams) (CallbackResult, error) {
	if !s.cfg.Enabled() {
		return CallbackResult{}, ErrOAuthNotConfigured
	}
	if strings.TrimSpace(p.Code) == "" {
		return CallbackResult{}, ErrMissingCode
	}
	if strings.TrimSpace(p.State) == "" {
		return CallbackResult{}, ErrInvalidState
	}
	binding, ok := s.verifyState(p.State)
	if !ok {
		return CallbackResult{}, ErrInvalidState
	}
	if s.cfg.Now().After(binding.ExpiresAt) {
		return CallbackResult{}, ErrStateExpired
	}

	exch, err := s.client.ExchangeOAuthCode(ctx, p.Code, s.cfg.RedirectURI)
	if err != nil {
		return CallbackResult{}, fmt.Errorf("exchange oauth code: %w", err)
	}
	if err := validateExchangeResult(exch); err != nil {
		return CallbackResult{}, err
	}

	// Single transaction so the installation-state read and the
	// binding insert commit together. If a concurrent admin
	// revokes the installation between read and bind, the bind
	// either commits against the live row or is rolled back as a
	// whole — never half-applied.
	tx, err := s.tx.Begin(ctx)
	if err != nil {
		return CallbackResult{}, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)
	qtx := s.queries.WithTx(tx)

	inst, err := qtx.GetLarkInstallationByAgent(ctx, db.GetLarkInstallationByAgentParams{
		WorkspaceID: binding.WorkspaceID,
		AgentID:     binding.AgentID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return CallbackResult{}, ErrInstallationNotProvisioned
		}
		return CallbackResult{}, fmt.Errorf("lookup installation: %w", err)
	}
	if InstallationStatus(inst.Status) != InstallationActive {
		return CallbackResult{}, ErrInstallationRevoked
	}

	if err := s.binder.BindInstallerTx(ctx, qtx, InstallerBindParams{
		WorkspaceID:    binding.WorkspaceID,
		InstallationID: inst.ID,
		MulticaUserID:  binding.InitiatorID,
		LarkOpenID:     exch.InstallerOpenID,
	}); err != nil {
		return CallbackResult{}, fmt.Errorf("bind installer: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return CallbackResult{}, fmt.Errorf("commit: %w", err)
	}

	return CallbackResult{
		WorkspaceID:     binding.WorkspaceID,
		AgentID:         binding.AgentID,
		InstallationID:  inst.ID,
		InstallerOpenID: exch.InstallerOpenID,
		RedirectURL:     s.cfg.FrontendSuccessURL,
	}, nil
}

// ErrorRedirect returns the URL the handler should bounce to when
// HandleCallback fails. Centralizing this lets us preserve the
// frontend-success path for the success case and a single error
// destination for every failure mode (with a code query param so the
// UI can pick the right copy).
func (s *OAuthService) ErrorRedirect(reason string) string {
	base := s.cfg.FrontendErrorURL
	sep := "?"
	if strings.Contains(base, "?") {
		sep = "&"
	}
	return base + sep + "lark_error=" + url.QueryEscape(reason)
}

func (s *OAuthService) buildAuthorizeURL(state string) string {
	q := url.Values{}
	q.Set("app_id", s.cfg.AppID)
	q.Set("redirect_uri", s.cfg.RedirectURI)
	q.Set("state", state)
	q.Set("response_type", "code")
	q.Set("scope", "personal_agent:install")
	sep := "?"
	if strings.Contains(s.cfg.AuthorizeBaseURL, "?") {
		sep = "&"
	}
	return s.cfg.AuthorizeBaseURL + sep + q.Encode()
}

// stateBinding is the unpacked, verified state token. The handler
// trusts these fields once verifyState returns ok = true.
type stateBinding struct {
	WorkspaceID pgtype.UUID
	AgentID     pgtype.UUID
	InitiatorID pgtype.UUID
	ExpiresAt   time.Time
}

// signState produces a token of the form:
//
//	workspaceID.agentID.initiatorID.expiresAtUnix.nonceHex.sigHex
//
// signed with HMAC-SHA256(StateSigningSecret). The HMAC covers the
// concatenated payload (no length-prefix needed because every field is
// fixed-width except nonceHex, which is consumed last before the sig).
func (s *OAuthService) signState(workspaceID, agentID, initiatorID string) (string, error) {
	nonce := make([]byte, 12)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	expires := s.cfg.Now().Add(s.cfg.StateTTL).Unix()
	payload := fmt.Sprintf("%s.%s.%s.%d.%s",
		workspaceID, agentID, initiatorID, expires, hex.EncodeToString(nonce))
	mac := hmac.New(sha256.New, []byte(s.cfg.StateSigningSecret))
	mac.Write([]byte(payload))
	sig := hex.EncodeToString(mac.Sum(nil))
	return payload + "." + sig, nil
}

func (s *OAuthService) verifyState(token string) (stateBinding, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 6 {
		return stateBinding{}, false
	}
	workspaceIDStr, agentIDStr, initiatorIDStr, expiresStr, nonceHex, sigHex :=
		parts[0], parts[1], parts[2], parts[3], parts[4], parts[5]
	payload := strings.Join(parts[:5], ".")
	mac := hmac.New(sha256.New, []byte(s.cfg.StateSigningSecret))
	mac.Write([]byte(payload))
	expected := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(sigHex)) {
		return stateBinding{}, false
	}
	_ = nonceHex // included in the signature, no further use
	var workspaceID, agentID, initiatorID pgtype.UUID
	if err := workspaceID.Scan(workspaceIDStr); err != nil {
		return stateBinding{}, false
	}
	if err := agentID.Scan(agentIDStr); err != nil {
		return stateBinding{}, false
	}
	if err := initiatorID.Scan(initiatorIDStr); err != nil {
		return stateBinding{}, false
	}
	var expiresUnix int64
	if _, err := fmt.Sscanf(expiresStr, "%d", &expiresUnix); err != nil {
		return stateBinding{}, false
	}
	return stateBinding{
		WorkspaceID: workspaceID,
		AgentID:     agentID,
		InitiatorID: initiatorID,
		ExpiresAt:   time.Unix(expiresUnix, 0),
	}, true
}

func validateExchangeResult(r OAuthExchangeResult) error {
	if r.InstallerOpenID == "" {
		// Installer auto-binding (see HandleCallback) requires the
		// installer's per-app open_id; without it we cannot bind
		// the installer onto the existing installation row.
		return ErrExchangeMissingInstallerOpenID
	}
	return nil
}

// Public sentinels so handlers can map service errors to HTTP status
// codes without parsing strings.

// ErrOAuthNotConfigured is returned when StartInstall / HandleCallback
// is called against a deployment that has not set up the Multica Lark
// app credentials (AppID / AppSecret / RedirectURI / StateSigningSecret).
// Handlers translate this to 503.
var ErrOAuthNotConfigured = errors.New("lark oauth: not configured")

// ErrMissingCode means the callback fired without a `code` param —
// either the user denied the install or Lark malformed the redirect.
var ErrMissingCode = errors.New("lark oauth: missing code")

// ErrInvalidState means the state token was missing, malformed, or
// failed HMAC verification. Could be a replay against a different
// signing secret (key rotation) or an attempt to forge a callback;
// both surface the same opaque error.
var ErrInvalidState = errors.New("lark oauth: invalid state")

// ErrStateExpired means the state token is well-formed but its TTL
// has elapsed. The user should restart the install from the agent
// detail page.
var ErrStateExpired = errors.New("lark oauth: state expired")

// ErrExchangeMissingInstallerOpenID surfaces the rare case where Lark's
// /authen/v1/user_info returned a response without the installer's
// open_id. The stubAPIClient returns ErrAPIClientNotConfigured before
// this can fire; the real client validates upstream.
var ErrExchangeMissingInstallerOpenID = errors.New("lark oauth: exchange response missing installer open_id")

// ErrInstallationNotProvisioned is returned by HandleCallback when no
// lark_installation row exists yet for (workspace, agent). Until the
// PersonalAgent install API is integrated, installations must be
// provisioned out-of-band via the manual-paste POST /lark/installations
// route; the OAuth flow only binds the installer's identity onto an
// existing installation. Mapped to the `installation_not_provisioned`
// reason at the HTTP boundary so the UI can guide the admin.
var ErrInstallationNotProvisioned = errors.New("lark oauth: installation not provisioned for this agent")

// ErrInstallationRevoked is returned by HandleCallback when the
// installation row exists but its status is no longer `active`. The
// UI surfaces this distinctly so the user knows to re-provision
// rather than just re-scan.
var ErrInstallationRevoked = errors.New("lark oauth: installation revoked")
