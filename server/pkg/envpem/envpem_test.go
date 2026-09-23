package envpem

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"strings"
	"testing"

	"github.com/golang-jwt/jwt/v5"
)

// testKeyPEM generates a fresh RSA key and returns its PEM encoding plus the
// parsed key, so round-trip assertions run against real key material.
func testKeyPEM(t *testing.T) (string, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	der := x509.MarshalPKCS1PrivateKey(key)
	block := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: der}
	// pem.EncodeToMemory emits a trailing newline; NormalizePrivateKey
	// trims surrounding whitespace, so the canonical form has none.
	return strings.TrimRight(string(pem.EncodeToMemory(block)), "\n"), key
}

func TestNormalizePrivateKey(t *testing.T) {
	pemText, _ := testKeyPEM(t)
	// The make-compatible .env form: quotes kept by Compose, newlines as
	// literal escapes. This is the exact string observed in the wild.
	escaped := `"` + strings.ReplaceAll(strings.TrimSpace(pemText), "\n", `\n`) + `"`

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"multi-line unchanged", pemText, pemText},
		{"multi-line quoted", `"` + pemText + `"`, pemText},
		{"single-line escaped", strings.ReplaceAll(pemText, "\n", `\n`), pemText},
		{"single-line escaped and quoted", escaped, pemText},
		{"single-quoted wrapper", "'" + pemText + "'", pemText},
		{"surrounding whitespace", "  " + pemText + "\t", pemText},
		{"empty stays empty", "", ""},
		{"quotes only", `""`, ""},
		{"unbalanced quote kept", `"-----BEGIN`, `"-----BEGIN`},
		{"non-PEM text untouched", "not-a-key", "not-a-key"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizePrivateKey(tt.in); got != tt.want {
				t.Errorf("NormalizePrivateKey() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestNormalizedKeyParses proves the wild form round-trips through the same
// parser the production readers use, not merely through string comparison.
func TestNormalizedKeyParses(t *testing.T) {
	pemText, want := testKeyPEM(t)
	escaped := `"` + strings.ReplaceAll(strings.TrimSpace(pemText), "\n", `\n`) + `"`

	got, err := jwt.ParseRSAPrivateKeyFromPEM([]byte(NormalizePrivateKey(escaped)))
	if err != nil {
		t.Fatalf("parse normalized key: %v", err)
	}
	if got.N.Cmp(want.N) != 0 || got.E != want.E {
		t.Error("normalized key parses to a different key than generated")
	}
}
