package main

// Account-level routes vs. the MCP OAuth access token (RUYI-209).
//
// The token this deployment mints for ChatGPT is a full user credential in
// every respect except one: the user never holds it — it is stored by an
// external service. Its only revocation boundary is the 90-day expiry, and
// POST /api/tokens would let a holder step straight out of that boundary by
// minting a mul_ PAT with an expiry of its own choosing. /api/admin is the same
// shape one level up: RequireSuperAdmin alone would carry instance-wide
// administration into that external store whenever the authorizing user is a
// super admin.
//
// These assertions run against a real router (not the middleware in isolation)
// because the defect being guarded is a missing r.Use: the guard can be
// correct and the route still unprotected.

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/analytics"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/oauth"
	"github.com/multica-ai/multica/server/internal/realtime"
)

const oauthGuardIssuer = "https://oauth-guard.example.com"

// newOAuthEnabledServer builds a router from an OAUTH_SIGNING_KEY generated in
// the test, and returns it alongside a signer holding the same key so the test
// can mint a token this server will accept. The ambient environment has no
// signing key (see oauth_disabled_test.go), so the enabled shape has to be
// constructed here rather than assumed.
func newOAuthEnabledServer(t *testing.T) (*httptest.Server, *oauth.Signer) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	encoded := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))

	t.Setenv("OAUTH_SIGNING_KEY", encoded)
	t.Setenv("MULTICA_APP_URL", oauthGuardIssuer)

	hub := realtime.NewHub()
	go hub.Run()
	server := httptest.NewServer(NewRouter(testPool, hub, events.New(), analytics.NoopClient{}, nil))
	t.Cleanup(server.Close)

	signer, err := oauth.NewSigner(encoded, oauthGuardIssuer)
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	return server, signer
}

func mintGuardToken(t *testing.T, signer *oauth.Signer) string {
	t.Helper()
	token, _, err := signer.MintAccessToken(testUserID, oauthGuardIssuer+oauth.MCPResourcePath, oauth.ScopeMCP, time.Now())
	if err != nil {
		t.Fatalf("MintAccessToken: %v", err)
	}
	return token
}

func callWithBearer(t *testing.T, server *httptest.Server, method, path, bearer, body string) (int, []byte) {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, server.URL+path, reader)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+bearer)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, payload
}

// The two account-level entry points must refuse the OAuth credential. 403
// rather than 401: the token authenticated fine, it is the actor kind that is
// wrong, and the distinction is what tells an operator reading logs apart from
// a forged token.
func TestOAuthTokenIsRefusedByAccountLevelRoutes(t *testing.T) {
	server, signer := newOAuthEnabledServer(t)
	token := mintGuardToken(t, signer)

	// The status code alone is not enough on /api/admin: RequireSuperAdmin
	// also answers 403, and the test user is not a super admin, so a missing
	// RequireHumanActor there would still produce a green 403. Assert on the
	// guard's own message so each case pins the guard it is about.
	const humanActorMessage = "this endpoint is only available to human actors"

	cases := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		// The escalation this guard exists for: one call turns a 90-day
		// externally-held token into a PAT with no such window.
		{name: "create PAT", method: http.MethodPost, path: "/api/tokens", body: `{"name":"ruyi209-escalation-probe"}`},
		{name: "list PATs", method: http.MethodGet, path: "/api/tokens"},
		{name: "admin list users", method: http.MethodGet, path: "/api/admin/users"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, body := callWithBearer(t, server, tc.method, tc.path, token, tc.body)
			if status != http.StatusForbidden {
				t.Fatalf("%s %s = %d, want 403; body=%s", tc.method, tc.path, status, body)
			}
			if !bytes.Contains(body, []byte(humanActorMessage)) {
				t.Fatalf("%s %s refused by the wrong guard: body=%s, want the actor-source message", tc.method, tc.path, body)
			}
		})
	}
}

// No PAT was created as a side effect. A 403 from a handler that had already
// written the row would still leave the escalation in place, and the status
// code alone cannot see that.
func TestOAuthTokenCreatesNoPATRow(t *testing.T) {
	server, signer := newOAuthEnabledServer(t)
	token := mintGuardToken(t, signer)

	const probeName = "ruyi209-escalation-side-effect-probe"
	t.Cleanup(func() {
		_, _ = testPool.Exec(t.Context(), `DELETE FROM personal_access_token WHERE name = $1`, probeName)
	})

	if status, body := callWithBearer(t, server, http.MethodPost, "/api/tokens", token, `{"name":"`+probeName+`"}`); status != http.StatusForbidden {
		t.Fatalf("POST /api/tokens = %d, want 403; body=%s", status, body)
	}

	var count int
	if err := testPool.QueryRow(t.Context(), `SELECT count(*) FROM personal_access_token WHERE name = $1`, probeName).Scan(&count); err != nil {
		t.Fatalf("count PATs: %v", err)
	}
	if count != 0 {
		t.Fatalf("POST /api/tokens created %d token row(s) despite the 403", count)
	}
}

// The regression side: the guard rejects on the actor source, and mul_ PATs
// and session JWTs carry none. Both must still reach /api/tokens — the daemon's
// renewal loop and `multica login` run on exactly these two credentials.
func TestPATAndSessionKeepAccessToTokenRoutes(t *testing.T) {
	server, _ := newOAuthEnabledServer(t)

	const patToken = "mul_ruyi209_actor_guard_regression"
	var patID string
	if err := testPool.QueryRow(t.Context(), `
		INSERT INTO personal_access_token (user_id, name, token_hash, token_prefix)
		VALUES ($1, $2, $3, $4)
		RETURNING id
	`, testUserID, "ruyi209 actor-guard regression", auth.HashToken(patToken), "mul_ruyi2").Scan(&patID); err != nil {
		t.Fatalf("insert PAT: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(t.Context(), `DELETE FROM personal_access_token WHERE id = $1`, patID)
	})

	for _, tc := range []struct {
		name   string
		bearer string
	}{
		{name: "mul_ PAT", bearer: patToken},
		{name: "session JWT", bearer: testToken},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if status, body := callWithBearer(t, server, http.MethodGet, "/api/tokens", tc.bearer, ""); status != http.StatusOK {
				t.Fatalf("GET /api/tokens = %d, want 200; body=%s", status, body)
			}
		})
	}
}

// The token is not broken in general — it still reaches the surface it was
// minted for. Without this the previous tests would also pass on a token the
// server rejects outright.
func TestOAuthTokenStillAuthenticatesOnWorkspaceRoutes(t *testing.T) {
	server, signer := newOAuthEnabledServer(t)
	token := mintGuardToken(t, signer)

	if status, body := callWithBearer(t, server, http.MethodGet, "/api/workspaces", token, ""); status != http.StatusOK {
		t.Fatalf("GET /api/workspaces = %d, want 200; body=%s", status, body)
	}
}
