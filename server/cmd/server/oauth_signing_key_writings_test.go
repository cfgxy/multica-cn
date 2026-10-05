package main

// Both writings of OAUTH_SIGNING_KEY a `.env` file can carry must produce the
// same signer (RUYI-216). The recommended writing is one physical line with
// `\n` escapes, because `Makefile` reads the same file through `include` and
// GNU make fails every target on a value that spans lines; the real multi-line
// writing is what deployments configured before this change have on disk, so it
// has to keep working.

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/oauth"
)

// signingKeyPEM mints a throwaway RSA-2048 key at run time. No key material is
// ever committed as a fixture, not even one that protects nothing.
func signingKeyPEM(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	der := x509.MarshalPKCS1PrivateKey(key)
	return strings.TrimSpace(string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: der})))
}

func TestNewOAuthSignerAcceptsBothPEMWritings(t *testing.T) {
	multiline := signingKeyPEM(t)
	escaped := strings.ReplaceAll(multiline, "\n", `\n`)
	if strings.Contains(escaped, "\n") {
		t.Fatalf("test setup: the escaped writing must be a single physical line")
	}
	const siteRoot = "https://signing-key-writings.test"

	keyIDs := map[string]string{}
	for _, tc := range []struct {
		name  string
		value string
	}{
		{"real newlines", multiline},
		{"escaped newlines on one line", escaped},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("OAUTH_SIGNING_KEY", tc.value)

			signer := newOAuthSigner(siteRoot)
			if signer == nil {
				t.Fatal("signer is nil: this writing of the PEM was not accepted, so the OAuth surface would be off")
			}

			token, _, err := signer.MintAccessToken("user-42", "", oauth.ScopeMCP, "", "", time.Now())
			if err != nil {
				t.Fatalf("mint access token: %v", err)
			}
			claims, err := signer.VerifyAccessToken(token)
			if err != nil {
				t.Fatalf("verify access token: %v", err)
			}
			if claims.Subject != "user-42" {
				t.Errorf("sub = %q, want user-42", claims.Subject)
			}
			if claims.Scope != oauth.ScopeMCP {
				t.Errorf("scope = %q, want %q", claims.Scope, oauth.ScopeMCP)
			}
			keyIDs[tc.name] = signer.KeyID()
		})
	}

	// Same key material through two writings has to yield the same JWKS key id.
	// A different kid would mean one writing was silently mangled into some
	// other key rather than rejected.
	if a, b := keyIDs["real newlines"], keyIDs["escaped newlines on one line"]; a == "" || a != b {
		t.Errorf("key id differs between writings: %q vs %q", a, b)
	}
}

// A value that is neither writing must still leave the OAuth surface off rather
// than boot a half-configured signer, and the warning must not carry the value.
func TestNewOAuthSignerRejectsUnparsableKey(t *testing.T) {
	t.Setenv("OAUTH_SIGNING_KEY", `-----BEGIN RSA PRIVATE KEY-----\nnot-base64\n-----END RSA PRIVATE KEY-----`)
	if signer := newOAuthSigner("https://signing-key-writings.test"); signer != nil {
		t.Error("an unparsable key must disable the OAuth surface, not produce a signer")
	}
}
