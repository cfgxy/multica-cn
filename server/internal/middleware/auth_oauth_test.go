package middleware

// The OAuth branch of Auth (RUYI-209). What these tests are for is less the
// happy path than the blast radius: the branch was added to a middleware that
// already dispatches four credential shapes, and the acceptance criterion for
// the change is that none of the other four notices.

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/oauth"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const oauthTestIssuer = "https://multica.example.com"

func testOAuthSigner(t *testing.T) *oauth.Signer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	encoded := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	signer, err := oauth.NewSigner(string(encoded), oauthTestIssuer)
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	return signer
}

// capturingHandler records the identity headers Auth set, so a test can assert
// what the downstream handler would see.
type capturingHandler struct {
	called      bool
	userID      string
	actorSource string
}

func (c *capturingHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c.called = true
	c.userID = r.Header.Get("X-User-ID")
	c.actorSource = r.Header.Get("X-Actor-Source")
}

func TestAuthAcceptsOAuthAccessToken(t *testing.T) {
	signer := testOAuthSigner(t)
	token, _, err := signer.MintAccessToken("user-42", oauthTestIssuer+oauth.MCPResourcePath, oauth.ScopeMCP, "", "", time.Now())
	if err != nil {
		t.Fatalf("MintAccessToken: %v", err)
	}

	next := &capturingHandler{}
	req := httptest.NewRequest(http.MethodPost, "/api/issues", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	Auth(nil, nil, nil, nil, signer, nil)(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if !next.called {
		t.Fatal("next handler was not reached")
	}
	if next.userID != "user-42" {
		t.Fatalf("X-User-ID = %q, want the token subject", next.userID)
	}
}

// The OAuth branch must classify its credential as machine-held. The token is
// minted for an external MCP client and stored there, so handler.RequireHumanActor
// has to be able to tell it apart from the human's own session — without that
// stamp the token reaches /api/tokens and can mint a PAT that outlives its own
// 90-day window.
func TestAuthStampsOAuthActorSource(t *testing.T) {
	signer := testOAuthSigner(t)
	token, _, err := signer.MintAccessToken("user-42", oauthTestIssuer+oauth.MCPResourcePath, oauth.ScopeMCP, "", "", time.Now())
	if err != nil {
		t.Fatalf("MintAccessToken: %v", err)
	}

	next := &capturingHandler{}
	req := httptest.NewRequest(http.MethodPost, "/api/issues", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	Auth(nil, nil, nil, nil, signer, nil)(next).ServeHTTP(rec, req)

	if next.actorSource != "oauth" {
		t.Fatalf("X-Actor-Source = %q, want %q", next.actorSource, "oauth")
	}
}

// The session JWT is the credential the OAuth stamp must not spread to: a
// human browsing the app keeps an empty actor source and therefore keeps
// access to the account-level routes.
func TestAuthLeavesSessionJWTActorSourceEmptyWithSignerPresent(t *testing.T) {
	// Same memoization caveat as TestAuthSessionJWTUnaffectedByTheOAuthBranch.
	token := generateToken(validClaims(), auth.JWTSecret())

	next := &capturingHandler{}
	req := httptest.NewRequest(http.MethodGet, "/api/tokens", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	Auth(nil, nil, nil, nil, testOAuthSigner(t), nil)(next).ServeHTTP(rec, req)

	if next.actorSource != "" {
		t.Fatalf("X-Actor-Source = %q, want empty for a session JWT", next.actorSource)
	}
}

// A client cannot promote itself by sending its own X-User-ID alongside a valid
// token: the middleware strips caller-supplied identity headers before any
// branch runs, and the OAuth branch sets the value from the verified subject.
func TestAuthOverwritesClientSuppliedUserIDOnTheOAuthPath(t *testing.T) {
	signer := testOAuthSigner(t)
	token, _, err := signer.MintAccessToken("real-user", oauthTestIssuer+oauth.MCPResourcePath, oauth.ScopeMCP, "", "", time.Now())
	if err != nil {
		t.Fatalf("MintAccessToken: %v", err)
	}

	next := &capturingHandler{}
	req := httptest.NewRequest(http.MethodPost, "/api/issues", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-User-ID", "somebody-else")
	rec := httptest.NewRecorder()
	Auth(nil, nil, nil, nil, signer, nil)(next).ServeHTTP(rec, req)

	if next.userID != "real-user" {
		t.Fatalf("X-User-ID = %q, want the verified subject rather than the client's value", next.userID)
	}
}

// A token addressed to another resource is refused here, not just inside the
// signer: this is the path a stolen token would actually arrive on.
func TestAuthRejectsOAuthTokenForAnotherResource(t *testing.T) {
	signer := testOAuthSigner(t)
	token, _, err := signer.MintAccessToken("user-42", "https://attacker.example.com/api/mcp", oauth.ScopeMCP, "", "", time.Now())
	if err != nil {
		t.Fatalf("MintAccessToken: %v", err)
	}

	next := &capturingHandler{}
	req := httptest.NewRequest(http.MethodPost, "/api/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	Auth(nil, nil, nil, nil, signer, nil)(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if next.called {
		t.Fatal("next handler ran for a token addressed to another resource")
	}
}

// A token signed by a different key is a forgery even though its header says
// RS256 and its claims look right.
func TestAuthRejectsOAuthTokenFromAnotherKey(t *testing.T) {
	foreign := testOAuthSigner(t)
	token, _, err := foreign.MintAccessToken("user-42", oauthTestIssuer+oauth.MCPResourcePath, oauth.ScopeMCP, "", "", time.Now())
	if err != nil {
		t.Fatalf("MintAccessToken: %v", err)
	}

	next := &capturingHandler{}
	req := httptest.NewRequest(http.MethodPost, "/api/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	Auth(nil, nil, nil, nil, testOAuthSigner(t), nil)(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if next.called {
		t.Fatal("next handler ran for a token signed by a foreign key")
	}
}

// With no signer configured — the shape of a deployment without
// OAUTH_SIGNING_KEY — an OAuth token is not half-accepted: it falls through to
// the HMAC branch, which rejects it. The surface is absent, not open.
func TestAuthWithoutSignerRejectsOAuthAccessToken(t *testing.T) {
	signer := testOAuthSigner(t)
	token, _, err := signer.MintAccessToken("user-42", oauthTestIssuer+oauth.MCPResourcePath, oauth.ScopeMCP, "", "", time.Now())
	if err != nil {
		t.Fatalf("MintAccessToken: %v", err)
	}

	next := &capturingHandler{}
	req := httptest.NewRequest(http.MethodPost, "/api/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	Auth(nil, nil, nil, nil, nil, nil)(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if next.called {
		t.Fatal("next handler ran with no OAuth signer configured")
	}
}

// The regression the OAuth branch has to not cause: a Multica session JWT is
// HS256, so it must still be verified by the HMAC branch even when a signer is
// present. Same assertions as TestAuth_ValidToken, with a signer wired in.
func TestAuthSessionJWTUnaffectedByTheOAuthBranch(t *testing.T) {
	// auth.JWTSecret() memoizes via sync.Once, so a per-test t.Setenv would
	// only take effect for whichever test ran first in the package. Sign with
	// the resolved secret, as TestAuth_ValidToken does.
	token := generateToken(validClaims(), auth.JWTSecret())

	next := &capturingHandler{}
	req := httptest.NewRequest(http.MethodGet, "/api/issues", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	Auth(nil, nil, nil, nil, testOAuthSigner(t), nil)(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if next.userID != "test-user-id" {
		t.Fatalf("X-User-ID = %q, want the session subject", next.userID)
	}
}

// isRS256Token only chooses a branch, so it must not be fooled into choosing
// the OAuth one for a session JWT — and must not panic on a malformed value.
func TestIsRS256TokenSelectsOnlyRS256JWS(t *testing.T) {
	signer := testOAuthSigner(t)
	rs256, _, err := signer.MintAccessToken("user-42", oauthTestIssuer+oauth.MCPResourcePath, oauth.ScopeMCP, "", "", time.Now())
	if err != nil {
		t.Fatalf("MintAccessToken: %v", err)
	}
	hs256 := generateToken(validClaims(), []byte("secret"))

	cases := []struct {
		name  string
		token string
		want  bool
	}{
		{"rs256 access token", rs256, true},
		{"hs256 session token", hs256, false},
		{"pat", "mul_" + "0123456789abcdef0123456789abcdef01234567", false},
		{"empty", "", false},
		{"two segments", "aaa.bbb", false},
		{"header is not base64", "!!!.bbb.ccc", false},
		{"header is not json", "YWJj.bbb.ccc", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isRS256Token(tc.token); got != tc.want {
				t.Fatalf("isRS256Token = %v, want %v", got, tc.want)
			}
		})
	}
}

// ——— grant gate (RUYI-420) ———

const gateTestGrantID = "514492f7-b30f-4147-bd33-c0e8ce5d6d4f"

// deadDB satisfies db.DBTX and always fails. Gate tests only need the
// middleware's background last_used touch to not crash the test process;
// the goroutine ignores the error, exactly as production does.
type deadDB struct{}

func (deadDB) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errors.New("auth_oauth_test: no database")
}
func (deadDB) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("auth_oauth_test: no database")
}
func (deadDB) QueryRow(context.Context, string, ...any) pgx.Row {
	return nil
}

func mintGateToken(t *testing.T, signer *oauth.Signer, scope string) string {
	t.Helper()
	token, _, err := signer.MintAccessToken("user-42", oauthTestIssuer+oauth.MCPResourcePath, scope, "client-1", gateTestGrantID, time.Now())
	if err != nil {
		t.Fatalf("MintAccessToken: %v", err)
	}
	return token
}

// A token whose grant the gate reports as revoked (or whose client is
// disabled or deleted) is dead the moment the gate says so — 401, downstream
// never runs, and this holds while the JWT itself is still well within its
// 90 days. That is the whole point of the gate.
func TestAuthOAuthTokenWithDeadGrantRejected(t *testing.T) {
	cases := map[string]auth.OAuthGrantState{
		"revoked":        {ClientFound: true, GrantRevoked: true},
		"clientdisabled": {ClientFound: true, ClientDisabled: true},
		"clientdeleted":  {ClientFound: false},
	}
	for name, state := range cases {
		t.Run(name, func(t *testing.T) {
			signer := testOAuthSigner(t)
			gate := auth.NewOAuthGate(nil, func(ctx context.Context, grantID string) (auth.OAuthGrantState, bool, error) {
				if grantID != gateTestGrantID {
					t.Fatalf("gate consulted for %q, want %q", grantID, gateTestGrantID)
				}
				return state, true, nil
			})
			token := mintGateToken(t, signer, oauth.ScopeMCP)

			next := &capturingHandler{}
			req := httptest.NewRequest(http.MethodPost, "/api/issues", nil)
			req.Header.Set("Authorization", "Bearer "+token)
			rec := httptest.NewRecorder()
			Auth(db.New(deadDB{}), nil, nil, nil, signer, gate)(next).ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401; body = %s", rec.Code, rec.Body.String())
			}
			if next.called {
				t.Fatal("next handler ran behind a dead grant")
			}
		})
	}
}

// The live-grant path: the same token shape passes when the gate says the
// authorization still stands — signature + gate both green, downstream runs.
// The queries stub is the deadDB above: the fire-and-forget last_used touch
// must not take the request down when it fails.
func TestAuthOAuthTokenWithLiveGrantPasses(t *testing.T) {
	signer := testOAuthSigner(t)
	gate := auth.NewOAuthGate(nil, func(ctx context.Context, grantID string) (auth.OAuthGrantState, bool, error) {
		return auth.OAuthGrantState{ClientFound: true}, true, nil
	})
	token := mintGateToken(t, signer, oauth.ScopeMCP)

	next := &capturingHandler{}
	req := httptest.NewRequest(http.MethodPost, "/api/issues", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	Auth(db.New(deadDB{}), nil, nil, nil, signer, gate)(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if !next.called || next.userID != "user-42" {
		t.Fatalf("downstream = %v, userID = %q; want called as user-42", next.called, next.userID)
	}
}

// Fail-closed: a token carrying a grant id but wired without a gate must be
// rejected, not waved through. A deployment that forgot the gate goes dark
// for gid tokens instead of silently losing its revocation promise.
func TestAuthOAuthTokenWithGrantIDAndNoGateRejected(t *testing.T) {
	signer := testOAuthSigner(t)
	token := mintGateToken(t, signer, oauth.ScopeMCP)

	next := &capturingHandler{}
	req := httptest.NewRequest(http.MethodPost, "/api/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	Auth(db.New(deadDB{}), nil, nil, nil, signer, nil)(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

// A gid claim that is not a UUID was not minted by this server's token
// endpoint — reject at the gate, before any lookup.
func TestAuthOAuthTokenWithMalformedGrantIDRejected(t *testing.T) {
	signer := testOAuthSigner(t)
	token, _, err := signer.MintAccessToken("user-42", oauthTestIssuer+oauth.MCPResourcePath, oauth.ScopeMCP, "client-1", "not-a-uuid", time.Now())
	if err != nil {
		t.Fatalf("MintAccessToken: %v", err)
	}

	next := &capturingHandler{}
	req := httptest.NewRequest(http.MethodPost, "/api/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	Auth(db.New(deadDB{}), nil, nil, nil, signer, auth.NewOAuthGate(nil, func(ctx context.Context, grantID string) (auth.OAuthGrantState, bool, error) {
		t.Fatal("gate must not be consulted for a malformed grant id")
		return auth.OAuthGrantState{}, false, nil
	}))(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

// ——— scope surface (RUYI-420) ———

// A read-scoped token is valid but not authorized for writes: 403 with
// insufficient_scope, never 401 — the credential is fine, the authority is
// not.
func TestAuthOAuthScopeReadTokenCannotWrite(t *testing.T) {
	signer := testOAuthSigner(t)
	token, _, err := signer.MintAccessToken("user-42", oauthTestIssuer+oauth.MCPResourcePath, oauth.ScopeRead, "", "", time.Now())
	if err != nil {
		t.Fatalf("MintAccessToken: %v", err)
	}

	next := &capturingHandler{}
	req := httptest.NewRequest(http.MethodPost, "/api/issues", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	Auth(nil, nil, nil, nil, signer, nil)(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("WWW-Authenticate"); !strings.Contains(got, "insufficient_scope") {
		t.Fatalf("WWW-Authenticate = %q, want an insufficient_scope challenge", got)
	}
	if next.called {
		t.Fatal("next handler ran behind an insufficient scope")
	}
}

// The run tier is the one mcp:write must not cover: dispatch and run
// steering consume the owner's quota, so they sit above plain writes.
func TestAuthOAuthWriteTokenCannotRun(t *testing.T) {
	signer := testOAuthSigner(t)
	token, _, err := signer.MintAccessToken("user-42", oauthTestIssuer+oauth.MCPResourcePath, oauth.ScopeWrite, "", "", time.Now())
	if err != nil {
		t.Fatalf("MintAccessToken: %v", err)
	}

	for _, path := range []string{
		"/api/issues/quick-create",
		"/api/issues/i-1/tasks/r-1/cancel",
	} {
		next := &capturingHandler{}
		req := httptest.NewRequest(http.MethodPost, path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		Auth(nil, nil, nil, nil, signer, nil)(next).ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Fatalf("POST %s: status = %d, want 403", path, rec.Code)
		}
		if next.called {
			t.Fatalf("POST %s: next handler ran behind an insufficient scope", path)
		}
	}
}

// Fail-closed outside the MCP surface: even the legacy full-access scope
// cannot reach account-level routes through an OAuth token — the token
// authorizes the MCP service, not the user's whole API.
func TestAuthOAuthFullScopeStillRejectedOffSurface(t *testing.T) {
	signer := testOAuthSigner(t)
	token, _, err := signer.MintAccessToken("user-42", oauthTestIssuer+oauth.MCPResourcePath, oauth.ScopeMCP, "", "", time.Now())
	if err != nil {
		t.Fatalf("MintAccessToken: %v", err)
	}

	for _, path := range []string{"/api/tokens", "/api/admin/users", "/api/members"} {
		next := &capturingHandler{}
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		Auth(nil, nil, nil, nil, signer, nil)(next).ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Fatalf("GET %s: status = %d, want 403", path, rec.Code)
		}
		if next.called {
			t.Fatalf("GET %s: next handler ran off the MCP surface", path)
		}
	}
}
