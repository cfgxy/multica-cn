package handler

// RUYI-420 rework: the consent screen's deny POST answers with the authorize
// URL re-presented carrying error=access_denied, and AuthorizeOAuth must hand
// that verdict to the validated client (RFC 6749 §4.1.2.1) instead of walking
// the browser into a fresh consent ticket. These tests pin that handshake at
// the authorize endpoint: the error is forwarded only after client and
// redirect_uri validate, the original state rides along, and a request
// without an error parameter keeps the pre-existing flow.

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/oauth"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// denyResumeHandler builds a Handler with only what the authorize endpoint's
// error handshake touches: a DB-backed client lookup, a signer so the surface
// is not "oauth disabled", and no Redis stores — the deny resume must answer
// before any of them would be consulted.
func denyResumeHandler(t *testing.T) *Handler {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate signing key: %v", err)
	}
	encoded := string(pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	}))
	signer, err := oauth.NewSigner(encoded, "https://deny-resume.test")
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	return &Handler{Queries: db.New(testPool), OAuthSigner: signer}
}

func seedDenyResumeClient(t *testing.T, clientID string, redirectURIs ...string) {
	t.Helper()
	secret, err := oauth.NewClientSecret()
	if err != nil {
		t.Fatalf("generate client secret: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO oauth_client (client_id, client_secret_hash, name, redirect_uris)
		VALUES ($1, $2, 'deny-resume fixture', $3)
	`, clientID, auth.HashToken(secret), redirectURIs); err != nil {
		t.Fatalf("seed oauth client: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM oauth_grant WHERE client_id = $1`, clientID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM oauth_client WHERE client_id = $1`, clientID)
	})
}

// oauthDenySessionCookie mints the HS256 session cookie oauthSessionUser
// verifies, so the test acts as a signed-in user without going through login.
func oauthDenySessionCookie(t *testing.T, userID string) *http.Cookie {
	t.Helper()
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": userID,
		"exp": time.Now().Add(time.Hour).Unix(),
	}).SignedString(auth.JWTSecret())
	if err != nil {
		t.Fatalf("sign session cookie: %v", err)
	}
	return &http.Cookie{Name: auth.AuthCookieName, Value: signed}
}

func denyResumeQuery(state bool) url.Values {
	q := url.Values{
		"client_id":             {"deny-resume-client"},
		"redirect_uri":          {"https://client.example.com/cb"},
		"response_type":         {"code"},
		"scope":                 {"mcp"},
		"code_challenge":        {"E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"},
		"code_challenge_method": {"S256"},
	}
	if state {
		q.Set("state", "st-123")
	}
	return q
}

func TestAuthorizeOAuthForwardsConsentDenyError(t *testing.T) {
	h := denyResumeHandler(t)
	seedDenyResumeClient(t, "deny-resume-client", "https://client.example.com/cb")

	t.Run("deny resume forwards access_denied and state to the registered redirect_uri", func(t *testing.T) {
		q := denyResumeQuery(true)
		q.Set("error", "access_denied")
		q.Set("error_description", "The user denied the authorization request.")
		req := newRequest("GET", "/auth/oauth/authorize?"+q.Encode(), nil)
		req.AddCookie(oauthDenySessionCookie(t, testUserID))

		resp := testutil.Call(t, h.AuthorizeOAuth, req).Want(http.StatusFound)
		target, err := url.Parse(resp.Header().Get("Location"))
		if err != nil {
			t.Fatalf("parse redirect target: %v", err)
		}
		if target.Scheme != "https" || target.Host != "client.example.com" || target.Path != "/cb" {
			t.Fatalf("deny resume landed on %q, want the registered redirect_uri", target)
		}
		got := target.Query()
		if got.Get("error") != "access_denied" {
			t.Errorf("error = %q, want access_denied", got.Get("error"))
		}
		if got.Get("error_description") != "The user denied the authorization request." {
			t.Errorf("error_description = %q, want the deny description", got.Get("error_description"))
		}
		if got.Get("state") != "st-123" {
			t.Errorf("state = %q, want the original st-123", got.Get("state"))
		}
		if got.Get("code") != "" {
			t.Errorf("deny resumed into a fresh authorization code %q", got.Get("code"))
		}
	})

	t.Run("deny resume without state omits the state parameter", func(t *testing.T) {
		q := denyResumeQuery(false)
		q.Set("error", "access_denied")
		req := newRequest("GET", "/auth/oauth/authorize?"+q.Encode(), nil)
		req.AddCookie(oauthDenySessionCookie(t, testUserID))

		resp := testutil.Call(t, h.AuthorizeOAuth, req).Want(http.StatusFound)
		target, err := url.Parse(resp.Header().Get("Location"))
		if err != nil {
			t.Fatalf("parse redirect target: %v", err)
		}
		if target.Host != "client.example.com" {
			t.Fatalf("deny resume landed on %q, want the registered redirect_uri", target)
		}
		if got := target.Query(); got.Get("error") != "access_denied" || got.Get("state") != "" {
			t.Errorf("error = %q state = %q, want access_denied with no state", got.Get("error"), got.Get("state"))
		}
	})

	t.Run("deny resume answers without a session", func(t *testing.T) {
		q := denyResumeQuery(true)
		q.Set("error", "access_denied")
		req := newRequest("GET", "/auth/oauth/authorize?"+q.Encode(), nil)

		resp := testutil.Call(t, h.AuthorizeOAuth, req).Want(http.StatusFound)
		target, err := url.Parse(resp.Header().Get("Location"))
		if err != nil {
			t.Fatalf("parse redirect target: %v", err)
		}
		if target.Host != "client.example.com" || target.Query().Get("error") != "access_denied" {
			t.Fatalf("deny resume without a session landed on %q, want the client callback", target)
		}
	})

	t.Run("unregistered redirect_uri never receives the error", func(t *testing.T) {
		q := denyResumeQuery(true)
		q.Set("redirect_uri", "https://evil.example.com/cb")
		q.Set("error", "access_denied")
		req := newRequest("GET", "/auth/oauth/authorize?"+q.Encode(), nil)
		req.AddCookie(oauthDenySessionCookie(t, testUserID))

		resp := testutil.Call(t, h.AuthorizeOAuth, req).Want(http.StatusBadRequest)
		if loc := resp.Header().Get("Location"); loc != "" {
			t.Fatalf("unregistered redirect_uri was rewarded with a redirect to %q", loc)
		}
	})

	t.Run("unknown client stays local", func(t *testing.T) {
		q := denyResumeQuery(true)
		q.Set("client_id", "no-such-client")
		q.Set("error", "access_denied")
		req := newRequest("GET", "/auth/oauth/authorize?"+q.Encode(), nil)
		req.AddCookie(oauthDenySessionCookie(t, testUserID))

		resp := testutil.Call(t, h.AuthorizeOAuth, req).Want(http.StatusBadRequest)
		if loc := resp.Header().Get("Location"); loc != "" {
			t.Fatalf("unknown client_id was rewarded with a redirect to %q", loc)
		}
	})

	t.Run("no error parameter keeps the normal flow", func(t *testing.T) {
		q := denyResumeQuery(true)
		req := newRequest("GET", "/auth/oauth/authorize?"+q.Encode(), nil)

		resp := testutil.Call(t, h.AuthorizeOAuth, req).Want(http.StatusFound)
		loc := resp.Header().Get("Location")
		target, err := url.Parse(loc)
		if err != nil {
			t.Fatalf("parse redirect target: %v", err)
		}
		// The deny-resume handling sits after the client/redirect_uri checks
		// and before the session step: a plain authorize still reaches the
		// login redirect here (the suite has no Redis consent store, so the
		// authenticated path cannot be exercised in-process).
		if target.Path != "/login" {
			t.Fatalf("plain authorize landed on %q, want the login redirect", loc)
		}
		if target.Query().Get("error") != "" {
			t.Errorf("plain authorize grew an error parameter: %q", loc)
		}
	})
}
