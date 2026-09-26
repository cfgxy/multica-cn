package main

// The "OAuth disabled" shape (RUYI-209). A deployment without OAUTH_SIGNING_KEY
// must degrade in one specific way and no other: the discovery documents are
// absent (a client must not be pointed at an authorization server that cannot
// mint anything), the endpoints that still exist answer 501 rather than 404 so
// an operator can tell "not built" from "not enabled", startup says so once in
// the log, and the PAT credential path is untouched.
//
// testServer is built by TestMain from the ambient environment, which carries
// no OAUTH_SIGNING_KEY — that is exactly the configuration under test here.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/auth"
)

// requireOAuthDisabled skips when the ambient environment DOES configure a
// signing key: these assertions describe the disabled deployment, and running
// them against an enabled one would report a configuration difference as a
// product defect.
func requireOAuthDisabled(t *testing.T) {
	t.Helper()
	if strings.TrimSpace(os.Getenv("OAUTH_SIGNING_KEY")) != "" {
		t.Skip("OAUTH_SIGNING_KEY is configured in this environment; the disabled shape is not under test")
	}
}

// Evidence 1: the two discovery documents are not published.
func TestOAuthDiscoveryDocumentsAbsentWhenSigningKeyUnset(t *testing.T) {
	requireOAuthDisabled(t)

	for _, path := range []string{
		"/.well-known/oauth-protected-resource",
		"/.well-known/oauth-protected-resource/api/mcp",
		"/.well-known/oauth-authorization-server",
		"/.well-known/jwks.json",
	} {
		resp, err := http.Get(testServer.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404 (document must not be published while the signer is nil); body=%s", path, resp.StatusCode, body)
		}
		// A 404 whose body still carries the metadata would defeat the point:
		// the absence has to be real, not just a status code.
		for _, leak := range []string{"authorization_endpoint", "code_challenge_methods_supported", "resource", "keys"} {
			if bytes.Contains(body, []byte(`"`+leak+`"`)) {
				t.Errorf("GET %s leaked discovery field %q in its 404 body: %s", path, leak, body)
			}
		}
	}
}

// Evidence 2: /auth/oauth/* answers 501, not 404 and not a redirect.
func TestOAuthEndpointsReturn501WhenSigningKeyUnset(t *testing.T) {
	requireOAuthDisabled(t)

	cases := []struct {
		name   string
		method string
		path   string
	}{
		{"authorize", http.MethodGet, "/auth/oauth/authorize?client_id=x&redirect_uri=https%3A%2F%2Fexample.com%2Fcb&response_type=code"},
		{"token", http.MethodPost, "/auth/oauth/token"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(tc.method, testServer.URL+tc.path, strings.NewReader("grant_type=authorization_code"))
			if err != nil {
				t.Fatalf("new request: %v", err)
			}
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("%s %s: %v", tc.method, tc.path, err)
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()

			if resp.StatusCode != http.StatusNotImplemented {
				t.Fatalf("%s %s = %d, want 501; body=%s", tc.method, tc.path, resp.StatusCode, body)
			}
			var payload struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(body, &payload); err != nil {
				t.Fatalf("decode body %s: %v", body, err)
			}
			if payload.Error != "oauth_not_configured" {
				t.Errorf("error = %q, want %q", payload.Error, "oauth_not_configured")
			}
		})
	}
}

// Evidence 3: startup says the feature is off, once, without the key material.
func TestNewOAuthSignerWarnsAndReturnsNilWithoutKey(t *testing.T) {
	t.Setenv("OAUTH_SIGNING_KEY", "")

	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
	defer slog.SetDefault(previous)

	if signer := newOAuthSigner("https://multica.example.com"); signer != nil {
		t.Fatal("newOAuthSigner returned a signer with OAUTH_SIGNING_KEY unset")
	}
	if got := logs.String(); !strings.Contains(got, "mcp oauth disabled: OAUTH_SIGNING_KEY not configured") {
		t.Fatalf("startup warning missing from log output: %q", got)
	}
}

// Evidence 4: the PAT path is untouched. A mul_ token still authenticates and a
// forged one is still rejected — the OAuth branch sits after this one in
// middleware.Auth and must not shadow it when the signer is nil.
func TestPATAuthUnaffectedWhileOAuthDisabled(t *testing.T) {
	requireOAuthDisabled(t)

	ctx := context.Background()
	const token = "mul_ruyi209_disabled_shape_probe"
	var patID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO personal_access_token (user_id, name, token_hash, token_prefix)
		VALUES ($1, $2, $3, $4)
		RETURNING id
	`, testUserID, "ruyi209 oauth-disabled probe", auth.HashToken(token), "mul_ruyi2").Scan(&patID); err != nil {
		t.Fatalf("insert PAT: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM personal_access_token WHERE id = $1`, patID)
	})

	call := func(bearer string) (int, []byte) {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, testServer.URL+"/api/workspaces", nil)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+bearer)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET /api/workspaces: %v", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, body
	}

	if status, body := call(token); status != http.StatusOK {
		t.Fatalf("valid PAT = %d, want 200; body=%s", status, body)
	}
	if status, _ := call("mul_this_value_was_never_issued"); status != http.StatusUnauthorized {
		t.Fatalf("unissued PAT = %d, want 401", status)
	}
}
