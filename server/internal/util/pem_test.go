package util

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"strings"
	"testing"
)

// testKeyPEM mints a throwaway RSA-2048 key at run time and returns its PKCS#1
// PEM encoding. Key material is never checked in: a fixture PEM in the tree
// would be a committed private key regardless of what it protects.
func testKeyPEM(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	der := x509.MarshalPKCS1PrivateKey(key)
	return string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: der}))
}

// The escaped single-line writing an operator uses to keep `.env` parsable by
// `include` must decode to exactly the same text as the real multi-line block,
// so both writings reach crypto/x509 identically.
func TestNormalizePEMKeyDecodesEscapedNewlines(t *testing.T) {
	multiline := testKeyPEM(t)
	singleLine := strings.ReplaceAll(strings.TrimSpace(multiline), "\n", `\n`)

	if strings.Contains(singleLine, "\n") {
		t.Fatalf("test setup: the escaped writing must be one physical line")
	}

	got := NormalizePEMKey(singleLine)
	if want := strings.TrimSpace(multiline); got != want {
		t.Errorf("escaped writing did not decode to the multi-line block:\ngot %d lines, want %d lines", strings.Count(got, "\n")+1, strings.Count(want, "\n")+1)
	}
	if block, _ := pem.Decode([]byte(got)); block == nil {
		t.Error("normalized escaped writing is not PEM-decodable")
	}
}

// The real multi-line writing is what every existing deployment has in its
// `.env` today, so normalization must leave it byte-identical apart from
// surrounding whitespace.
func TestNormalizePEMKeyKeepsRealNewlines(t *testing.T) {
	multiline := testKeyPEM(t)

	got := NormalizePEMKey("\n  " + multiline + "  \n")
	if want := strings.TrimSpace(multiline); got != want {
		t.Errorf("multi-line writing was mutated by normalization")
	}
	if block, _ := pem.Decode([]byte(got)); block == nil {
		t.Error("normalized multi-line writing is not PEM-decodable")
	}
}

// CRLF-escaped values come from operators editing `.env` on Windows or pasting
// out of a Windows editor; they must decode to LF, not to a literal `\r`.
func TestNormalizePEMKeyDecodesEscapedCRLF(t *testing.T) {
	multiline := strings.TrimSpace(testKeyPEM(t))
	singleLine := strings.ReplaceAll(multiline, "\n", `\r\n`)

	got := NormalizePEMKey(singleLine)
	if got != multiline {
		t.Errorf("escaped CRLF writing did not decode to LF-separated PEM")
	}
	if strings.Contains(got, "\r") {
		t.Error("normalized value still carries a carriage return")
	}
}

// The recommended writing is quoted (the shell splits an unquoted PEM header on
// its spaces), and make's `include` hands the quotes through to the process, so
// they must be stripped rather than reaching the PEM parser.
func TestNormalizePEMKeyStripsSurroundingQuotes(t *testing.T) {
	multiline := strings.TrimSpace(testKeyPEM(t))
	escaped := strings.ReplaceAll(multiline, "\n", `\n`)

	for _, writing := range []string{
		`"` + escaped + `"`,
		`'` + escaped + `'`,
		`"` + multiline + `"`,
	} {
		got := NormalizePEMKey(writing)
		if got != multiline {
			t.Errorf("quoted writing %.30q… did not normalize to the bare PEM block", writing)
		}
		if block, _ := pem.Decode([]byte(got)); block == nil {
			t.Errorf("quoted writing %.30q… is not PEM-decodable after normalization", writing)
		}
	}
}

func TestNormalizePEMKeyEmptyStaysEmpty(t *testing.T) {
	for _, raw := range []string{"", "   ", "\n\t "} {
		if got := NormalizePEMKey(raw); got != "" {
			t.Errorf("NormalizePEMKey(%q) = %q, want empty (callers read empty as \"not configured\")", raw, got)
		}
	}
}
