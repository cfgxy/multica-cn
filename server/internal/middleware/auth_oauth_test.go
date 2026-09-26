package middleware

// The OAuth branch of Auth (RUYI-209). What these tests are for is less the
// happy path than the blast radius: the branch was added to a middleware that
// already dispatches four credential shapes, and the acceptance criterion for
// the change is that none of the other four notices.

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/oauth"
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
	called bool
	userID string
}

func (c *capturingHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c.called = true
	c.userID = r.Header.Get("X-User-ID")
}

func TestAuthAcceptsOAuthAccessToken(t *testing.T) {
	signer := testOAuthSigner(t)
	token, _, err := signer.MintAccessToken("user-42", oauthTestIssuer+oauth.MCPResourcePath, oauth.ScopeMCP, time.Now())
	if err != nil {
		t.Fatalf("MintAccessToken: %v", err)
	}

	next := &capturingHandler{}
	req := httptest.NewRequest(http.MethodPost, "/api/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	Auth(nil, nil, nil, nil, signer)(next).ServeHTTP(rec, req)

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

// A client cannot promote itself by sending its own X-User-ID alongside a valid
// token: the middleware strips caller-supplied identity headers before any
// branch runs, and the OAuth branch sets the value from the verified subject.
func TestAuthOverwritesClientSuppliedUserIDOnTheOAuthPath(t *testing.T) {
	signer := testOAuthSigner(t)
	token, _, err := signer.MintAccessToken("real-user", oauthTestIssuer+oauth.MCPResourcePath, oauth.ScopeMCP, time.Now())
	if err != nil {
		t.Fatalf("MintAccessToken: %v", err)
	}

	next := &capturingHandler{}
	req := httptest.NewRequest(http.MethodPost, "/api/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-User-ID", "somebody-else")
	rec := httptest.NewRecorder()
	Auth(nil, nil, nil, nil, signer)(next).ServeHTTP(rec, req)

	if next.userID != "real-user" {
		t.Fatalf("X-User-ID = %q, want the verified subject rather than the client's value", next.userID)
	}
}

// A token addressed to another resource is refused here, not just inside the
// signer: this is the path a stolen token would actually arrive on.
func TestAuthRejectsOAuthTokenForAnotherResource(t *testing.T) {
	signer := testOAuthSigner(t)
	token, _, err := signer.MintAccessToken("user-42", "https://attacker.example.com/api/mcp", oauth.ScopeMCP, time.Now())
	if err != nil {
		t.Fatalf("MintAccessToken: %v", err)
	}

	next := &capturingHandler{}
	req := httptest.NewRequest(http.MethodPost, "/api/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	Auth(nil, nil, nil, nil, signer)(next).ServeHTTP(rec, req)

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
	token, _, err := foreign.MintAccessToken("user-42", oauthTestIssuer+oauth.MCPResourcePath, oauth.ScopeMCP, time.Now())
	if err != nil {
		t.Fatalf("MintAccessToken: %v", err)
	}

	next := &capturingHandler{}
	req := httptest.NewRequest(http.MethodPost, "/api/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	Auth(nil, nil, nil, nil, testOAuthSigner(t))(next).ServeHTTP(rec, req)

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
	token, _, err := signer.MintAccessToken("user-42", oauthTestIssuer+oauth.MCPResourcePath, oauth.ScopeMCP, time.Now())
	if err != nil {
		t.Fatalf("MintAccessToken: %v", err)
	}

	next := &capturingHandler{}
	req := httptest.NewRequest(http.MethodPost, "/api/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	Auth(nil, nil, nil, nil, nil)(next).ServeHTTP(rec, req)

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
	t.Setenv("JWT_SECRET", "test-secret-value-for-oauth-branch-regression")
	token := generateToken(validClaims(), []byte("test-secret-value-for-oauth-branch-regression"))

	next := &capturingHandler{}
	req := httptest.NewRequest(http.MethodGet, "/api/issues", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	Auth(nil, nil, nil, nil, testOAuthSigner(t))(next).ServeHTTP(rec, req)

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
	rs256, _, err := signer.MintAccessToken("user-42", oauthTestIssuer+oauth.MCPResourcePath, oauth.ScopeMCP, time.Now())
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
