package lark

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// fakeInstallerBinder records the BindInstallerTx calls so tests can
// assert that the OAuth callback wired the installer auto-bind into
// the install journey. Returning bindErr lets a test simulate
// "installer's open_id is already claimed by someone else" or
// "installer is not a workspace member" to confirm the OAuth callback
// surfaces those failures correctly.
type fakeInstallerBinder struct {
	called  int
	lastReq InstallerBindParams
	bindErr error
}

func (f *fakeInstallerBinder) BindInstallerTx(_ context.Context, _ *db.Queries, p InstallerBindParams) error {
	f.called++
	f.lastReq = p
	return f.bindErr
}

// failTxStarter satisfies the TxStarter interface for tests that reach
// the tx-open path but should not touch a real DB. Begin returns the
// configured error; tests assert it propagates correctly.
type failTxStarter struct {
	err error
}

func (f *failTxStarter) Begin(_ context.Context) (pgx.Tx, error) {
	return nil, f.err
}

func newOAuthService(t *testing.T, now func() time.Time, client APIClient) *OAuthService {
	t.Helper()
	return newOAuthServiceWithBinder(t, now, client, &fakeInstallerBinder{})
}

func newOAuthServiceWithBinder(t *testing.T, now func() time.Time, client APIClient, binder InstallerBinder) *OAuthService {
	t.Helper()
	cfg := OAuthConfig{
		AppID:              "cli_meta_app",
		AppSecret:          "shh",
		RedirectURI:        "https://multica.example/api/lark/install/callback",
		AuthorizeBaseURL:   "https://accounts.feishu.cn/open-apis/authen/v1/authorize",
		StateSigningSecret: "test-state-secret-32-bytes-of-rand!!",
		StateTTL:           10 * time.Minute,
		FrontendSuccessURL: "/settings?tab=lark",
		Now:                now,
	}
	// queries handle is non-nil but its DBTX is — every test below
	// must bail out BEFORE any query runs, otherwise this nil DBTX
	// would deopt at the first Exec. The failTxStarter guarantees
	// that: Begin returns immediately, so HandleCallback never even
	// reaches a qtx call.
	queries := db.New(nil)
	tx := &failTxStarter{err: errors.New("tx not available in this test")}
	svc, err := NewOAuthService(cfg, client, queries, tx, binder)
	if err != nil {
		t.Fatalf("NewOAuthService: %v", err)
	}
	return svc
}

func TestOAuthStartInstallBuildsSignedURL(t *testing.T) {
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	svc := newOAuthService(t, func() time.Time { return now }, NewStubAPIClient(newDiscardLogger()))

	res, err := svc.StartInstall(StartInstallParams{
		WorkspaceID: uuidFromString(t, "11111111-1111-1111-1111-111111111111"),
		AgentID:     uuidFromString(t, "22222222-2222-2222-2222-222222222222"),
		InitiatorID: uuidFromString(t, "33333333-3333-3333-3333-333333333333"),
	})
	if err != nil {
		t.Fatalf("StartInstall: %v", err)
	}
	u, err := url.Parse(res.URL)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	q := u.Query()
	if q.Get("app_id") != "cli_meta_app" {
		t.Fatalf("app_id not propagated: %s", q.Get("app_id"))
	}
	if q.Get("redirect_uri") != "https://multica.example/api/lark/install/callback" {
		t.Fatalf("redirect_uri not propagated: %s", q.Get("redirect_uri"))
	}
	if q.Get("state") == "" {
		t.Fatalf("state must be set")
	}
	if !strings.Contains(res.URL, "accounts.feishu.cn") {
		t.Fatalf("URL must point at Lark OAuth host: %s", res.URL)
	}
}

func TestOAuthDisabledWhenConfigMissing(t *testing.T) {
	queries := db.New(nil)
	tx := &failTxStarter{err: errors.New("tx not available")}
	svc, err := NewOAuthService(OAuthConfig{}, NewStubAPIClient(newDiscardLogger()), queries, tx, &fakeInstallerBinder{})
	if err != nil {
		t.Fatalf("NewOAuthService: %v", err)
	}
	_, err = svc.StartInstall(StartInstallParams{
		WorkspaceID: uuidFromString(t, "11111111-1111-1111-1111-111111111111"),
		AgentID:     uuidFromString(t, "22222222-2222-2222-2222-222222222222"),
		InitiatorID: uuidFromString(t, "33333333-3333-3333-3333-333333333333"),
	})
	if !errors.Is(err, ErrOAuthNotConfigured) {
		t.Fatalf("expected ErrOAuthNotConfigured, got %v", err)
	}
	_, err = svc.HandleCallback(context.Background(), CallbackParams{Code: "x", State: "y"})
	if !errors.Is(err, ErrOAuthNotConfigured) {
		t.Fatalf("expected ErrOAuthNotConfigured on callback, got %v", err)
	}
}

func TestOAuthCallbackRejectsInvalidState(t *testing.T) {
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	svc := newOAuthService(t, func() time.Time { return now }, NewStubAPIClient(newDiscardLogger()))
	_, err := svc.HandleCallback(context.Background(), CallbackParams{Code: "code", State: "not-a-real-state"})
	if !errors.Is(err, ErrInvalidState) {
		t.Fatalf("expected ErrInvalidState, got %v", err)
	}
}

func TestOAuthCallbackRejectsTamperedState(t *testing.T) {
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	svc := newOAuthService(t, func() time.Time { return now }, NewStubAPIClient(newDiscardLogger()))
	res, err := svc.StartInstall(StartInstallParams{
		WorkspaceID: uuidFromString(t, "11111111-1111-1111-1111-111111111111"),
		AgentID:     uuidFromString(t, "22222222-2222-2222-2222-222222222222"),
		InitiatorID: uuidFromString(t, "33333333-3333-3333-3333-333333333333"),
	})
	if err != nil {
		t.Fatalf("StartInstall: %v", err)
	}
	// Flip a single character of the signature — should fail HMAC.
	last := res.State[len(res.State)-1]
	tampered := res.State[:len(res.State)-1]
	if last == 'a' {
		tampered += "b"
	} else {
		tampered += "a"
	}
	_, err = svc.HandleCallback(context.Background(), CallbackParams{Code: "code", State: tampered})
	if !errors.Is(err, ErrInvalidState) {
		t.Fatalf("tampered state must be rejected as invalid, got %v", err)
	}
}

func TestOAuthCallbackRejectsExpiredState(t *testing.T) {
	mintAt := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	clock := mintAt
	svc := newOAuthService(t, func() time.Time { return clock }, NewStubAPIClient(newDiscardLogger()))

	res, err := svc.StartInstall(StartInstallParams{
		WorkspaceID: uuidFromString(t, "11111111-1111-1111-1111-111111111111"),
		AgentID:     uuidFromString(t, "22222222-2222-2222-2222-222222222222"),
		InitiatorID: uuidFromString(t, "33333333-3333-3333-3333-333333333333"),
	})
	if err != nil {
		t.Fatalf("StartInstall: %v", err)
	}

	// Advance the clock past the 10-minute TTL.
	clock = mintAt.Add(11 * time.Minute)
	_, err = svc.HandleCallback(context.Background(), CallbackParams{Code: "code", State: res.State})
	if !errors.Is(err, ErrStateExpired) {
		t.Fatalf("expected ErrStateExpired, got %v", err)
	}
}

// fakeOAuthAPIClient is a minimal APIClient that returns a canned
// OAuthExchangeResult and refuses every other transport call. Used to
// drive HandleCallback through to the installer-bind step without
// dragging the real Lark HTTP wire protocol in.
type fakeOAuthAPIClient struct {
	exch OAuthExchangeResult
	err  error
}

func (f *fakeOAuthAPIClient) IsConfigured() bool         { return true }
func (f *fakeOAuthAPIClient) SupportsOAuthInstall() bool { return true }

func (f *fakeOAuthAPIClient) SendInteractiveCard(_ context.Context, _ SendCardParams) (string, error) {
	return "", ErrAPIClientNotConfigured
}
func (f *fakeOAuthAPIClient) PatchInteractiveCard(_ context.Context, _ PatchCardParams) error {
	return ErrAPIClientNotConfigured
}
func (f *fakeOAuthAPIClient) SendBindingPromptCard(_ context.Context, _ BindingPromptParams) error {
	return ErrAPIClientNotConfigured
}
func (f *fakeOAuthAPIClient) ExchangeOAuthCode(_ context.Context, _ string, _ string) (OAuthExchangeResult, error) {
	return f.exch, f.err
}

// TestOAuthCallbackInstallerMissingOpenID pins the safety net for
// when /authen/v1/user_info returns without an open_id. The callback
// must surface this as a typed error rather than silently proceed
// without the auto-bind.
func TestOAuthCallbackInstallerMissingOpenID(t *testing.T) {
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	stub := &fakeOAuthAPIClient{
		exch: OAuthExchangeResult{
			InstallerOpenID: "", // missing — must surface as a distinct error
		},
	}
	binder := &fakeInstallerBinder{}
	svc := newOAuthServiceWithBinder(t, func() time.Time { return now }, stub, binder)

	res, err := svc.StartInstall(StartInstallParams{
		WorkspaceID: uuidFromString(t, "11111111-1111-1111-1111-111111111111"),
		AgentID:     uuidFromString(t, "22222222-2222-2222-2222-222222222222"),
		InitiatorID: uuidFromString(t, "33333333-3333-3333-3333-333333333333"),
	})
	if err != nil {
		t.Fatalf("StartInstall: %v", err)
	}
	_, err = svc.HandleCallback(context.Background(), CallbackParams{Code: "ok", State: res.State})
	if !errors.Is(err, ErrExchangeMissingInstallerOpenID) {
		t.Fatalf("expected ErrExchangeMissingInstallerOpenID, got %v", err)
	}
	if binder.called != 0 {
		t.Fatalf("binder must not run when installer open_id is missing")
	}
}

func TestOAuthCallbackPropagatesExchangeError(t *testing.T) {
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	stub := NewStubAPIClient(newDiscardLogger()) // returns ErrAPIClientNotConfigured
	svc := newOAuthService(t, func() time.Time { return now }, stub)

	res, err := svc.StartInstall(StartInstallParams{
		WorkspaceID: uuidFromString(t, "11111111-1111-1111-1111-111111111111"),
		AgentID:     uuidFromString(t, "22222222-2222-2222-2222-222222222222"),
		InitiatorID: uuidFromString(t, "33333333-3333-3333-3333-333333333333"),
	})
	if err != nil {
		t.Fatalf("StartInstall: %v", err)
	}
	_, err = svc.HandleCallback(context.Background(), CallbackParams{Code: "code", State: res.State})
	if !errors.Is(err, ErrAPIClientNotConfigured) {
		t.Fatalf("expected stub-client error to propagate, got %v", err)
	}
}

// TestOAuthCallbackOpensTxAfterValidExchange pins the post-must-fix
// flow: once the OAuth exchange succeeds AND the state is valid AND
// the installer's open_id is present, HandleCallback opens a DB
// transaction so the installation lookup + installer bind commit
// together. With a fake TxStarter that errors at Begin, that error
// surfaces — proving the tx-open path runs in this order, BEFORE any
// non-transactional lookup or bind could leak a partial state.
func TestOAuthCallbackOpensTxAfterValidExchange(t *testing.T) {
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	stub := &fakeOAuthAPIClient{
		exch: OAuthExchangeResult{InstallerOpenID: "ou_installer"},
	}
	binder := &fakeInstallerBinder{}

	cfg := OAuthConfig{
		AppID:              "cli_meta_app",
		AppSecret:          "shh",
		RedirectURI:        "https://multica.example/api/lark/install/callback",
		AuthorizeBaseURL:   "https://accounts.feishu.cn/open-apis/authen/v1/authorize",
		StateSigningSecret: "test-state-secret-32-bytes-of-rand!!",
		StateTTL:           10 * time.Minute,
		FrontendSuccessURL: "/settings?tab=lark",
		Now:                func() time.Time { return now },
	}
	queries := db.New(nil)
	beginErr := errors.New("synthetic tx open failure")
	tx := &failTxStarter{err: beginErr}

	svc, err := NewOAuthService(cfg, stub, queries, tx, binder)
	if err != nil {
		t.Fatalf("NewOAuthService: %v", err)
	}
	res, err := svc.StartInstall(StartInstallParams{
		WorkspaceID: uuidFromString(t, "11111111-1111-1111-1111-111111111111"),
		AgentID:     uuidFromString(t, "22222222-2222-2222-2222-222222222222"),
		InitiatorID: uuidFromString(t, "33333333-3333-3333-3333-333333333333"),
	})
	if err != nil {
		t.Fatalf("StartInstall: %v", err)
	}
	_, err = svc.HandleCallback(context.Background(), CallbackParams{Code: "ok", State: res.State})
	if err == nil || !errors.Is(err, beginErr) {
		t.Fatalf("expected Begin error to propagate, got %v", err)
	}
	if binder.called != 0 {
		t.Fatalf("binder must not run when the tx never opens")
	}
}

// TestOAuthRequiresInstallerBinder pins the constructor-misuse rule:
// the OAuth install journey REQUIRES installer auto-binding, so a nil
// binder must fail at construction time rather than slip past and
// produce a callback flow whose installer is left in an unbound state.
func TestOAuthRequiresInstallerBinder(t *testing.T) {
	queries := db.New(nil)
	tx := &failTxStarter{err: errors.New("tx not available")}
	_, err := NewOAuthService(OAuthConfig{}, NewStubAPIClient(newDiscardLogger()), queries, tx, nil)
	if err == nil {
		t.Fatal("expected nil InstallerBinder to be rejected at construction")
	}
	if !strings.Contains(err.Error(), "InstallerBinder") {
		t.Fatalf("expected InstallerBinder error, got %v", err)
	}
}

// TestOAuthRequiresQueriesAndTx pins the rest of the constructor's
// must-be-non-nil contract. Without queries or tx the OAuth callback
// cannot open the lookup+bind transaction, so refusing nil here is
// the safer default than allowing a silent regression at runtime.
func TestOAuthRequiresQueriesAndTx(t *testing.T) {
	tx := &failTxStarter{err: errors.New("tx not available")}
	if _, err := NewOAuthService(OAuthConfig{}, NewStubAPIClient(newDiscardLogger()), nil, tx, &fakeInstallerBinder{}); err == nil {
		t.Fatal("expected nil queries to be rejected at construction")
	}
	queries := db.New(nil)
	if _, err := NewOAuthService(OAuthConfig{}, NewStubAPIClient(newDiscardLogger()), queries, nil, &fakeInstallerBinder{}); err == nil {
		t.Fatal("expected nil TxStarter to be rejected at construction")
	}
}

func TestValidateExchangeResult(t *testing.T) {
	good := OAuthExchangeResult{InstallerOpenID: "ou_installer"}
	if err := validateExchangeResult(good); err != nil {
		t.Fatalf("valid result rejected: %v", err)
	}
	if err := validateExchangeResult(OAuthExchangeResult{}); !errors.Is(err, ErrExchangeMissingInstallerOpenID) {
		t.Fatalf("missing installer_open_id: %v", err)
	}
}

// TestVerifyStateRoundTripsAllFields is a sanity check that pgtype.UUID
// round-trips through the state token; if Scan fails on the parsed
// substring the verifyState branch silently rejects valid tokens.
func TestVerifyStateRoundTripsAllFields(t *testing.T) {
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	svc := newOAuthService(t, func() time.Time { return now }, NewStubAPIClient(newDiscardLogger()))

	wsID := uuidFromString(t, "11111111-1111-1111-1111-111111111111")
	agentID := uuidFromString(t, "22222222-2222-2222-2222-222222222222")
	initiatorID := uuidFromString(t, "33333333-3333-3333-3333-333333333333")
	res, err := svc.StartInstall(StartInstallParams{
		WorkspaceID: wsID,
		AgentID:     agentID,
		InitiatorID: initiatorID,
	})
	if err != nil {
		t.Fatalf("StartInstall: %v", err)
	}
	binding, ok := svc.verifyState(res.State)
	if !ok {
		t.Fatalf("verifyState rejected freshly-signed token")
	}
	if !uuidEqual(binding.WorkspaceID, wsID) ||
		!uuidEqual(binding.AgentID, agentID) ||
		!uuidEqual(binding.InitiatorID, initiatorID) {
		t.Fatalf("round-trip mismatch: %+v", binding)
	}
}

func uuidEqual(a, b pgtype.UUID) bool {
	return a.Valid == b.Valid && a.Bytes == b.Bytes
}
