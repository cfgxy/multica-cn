package oauth

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const testIssuer = "https://multica.example.com"

func testSigningKeyPEM(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate test RSA key: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	}))
}

func testSigner(t *testing.T) *Signer {
	t.Helper()
	signer, err := NewSigner(testSigningKeyPEM(t), testIssuer)
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	return signer
}

func TestNewSignerRejectsMissingKey(t *testing.T) {
	for _, raw := range []string{"", "   "} {
		if _, err := NewSigner(raw, testIssuer); !errors.Is(err, ErrNoSigningKey) {
			t.Fatalf("NewSigner(%q) error = %v, want ErrNoSigningKey", raw, err)
		}
	}
}

func TestNewSignerRejectsNonPEMValueWithoutEchoingIt(t *testing.T) {
	// The error text reaches the boot log, so it must never quote the value it
	// was handed — a mis-set OAUTH_SIGNING_KEY is still a secret.
	const bogus = "definitely-not-a-key-but-secret-shaped"
	_, err := NewSigner(bogus, testIssuer)
	if !errors.Is(err, ErrNoSigningKey) {
		t.Fatalf("NewSigner(non-PEM) error = %v, want ErrNoSigningKey", err)
	}
	if strings.Contains(err.Error(), bogus) {
		t.Fatalf("NewSigner error echoed the configured key material: %v", err)
	}
}

func TestNewSignerAcceptsPKCS8(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal PKCS#8: %v", err)
	}
	pkcs8 := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))

	if _, err := NewSigner(pkcs8, testIssuer); err != nil {
		t.Fatalf("NewSigner rejected a PKCS#8 key openssl genpkey produces: %v", err)
	}
}

// The `resource` indicator the client sent must land in `aud` verbatim: that
// binding is what stops a token minted for one MCP endpoint from being replayed
// at another.
func TestMintAccessTokenMapsResourceToAudience(t *testing.T) {
	signer := testSigner(t)
	const resource = "https://multica.example.com/api/mcp"
	now := time.Now()

	token, expiresAt, err := signer.MintAccessToken("user-1", resource, ScopeMCP, now)
	if err != nil {
		t.Fatalf("MintAccessToken: %v", err)
	}

	claims := parseClaimsUnverified(t, token)
	if got := claims["aud"]; got != resource {
		t.Fatalf("aud = %v, want the resource indicator %q", got, resource)
	}
	if got := claims["iss"]; got != testIssuer {
		t.Fatalf("iss = %v, want %q", got, testIssuer)
	}
	if got := claims["sub"]; got != "user-1" {
		t.Fatalf("sub = %v, want user-1", got)
	}
	if got := claims["scope"]; got != ScopeMCP {
		t.Fatalf("scope = %v, want %q", got, ScopeMCP)
	}
	if want := now.Add(AccessTokenTTL); !expiresAt.Equal(want) {
		t.Fatalf("expiresAt = %v, want %v (90-day TTL)", expiresAt, want)
	}
}

func TestMintAccessTokenFallsBackToIssuerAudience(t *testing.T) {
	// `resource` is optional in RFC 8707; a client that omits it must still get
	// an audience-bound token rather than one with an empty aud.
	signer := testSigner(t)
	token, _, err := signer.MintAccessToken("user-1", "", "", time.Now())
	if err != nil {
		t.Fatalf("MintAccessToken: %v", err)
	}
	claims := parseClaimsUnverified(t, token)
	if got := claims["aud"]; got != testIssuer {
		t.Fatalf("aud = %v, want the issuer %q as the fallback", got, testIssuer)
	}
}

func TestVerifyAccessTokenRoundTrip(t *testing.T) {
	signer := testSigner(t)
	const resource = "https://multica.example.com/api/mcp"
	token, _, err := signer.MintAccessToken("user-7", resource, ScopeMCP, time.Now())
	if err != nil {
		t.Fatalf("MintAccessToken: %v", err)
	}

	claims, err := signer.VerifyAccessToken(token)
	if err != nil {
		t.Fatalf("VerifyAccessToken rejected a token it just minted: %v", err)
	}
	if claims.Subject != "user-7" {
		t.Fatalf("Subject = %q, want user-7", claims.Subject)
	}
	if len(claims.Audience) != 1 || claims.Audience[0] != resource {
		t.Fatalf("Audience = %v, want [%q]", claims.Audience, resource)
	}
}

// The `aud` check is the other half of the resource binding: minting a token
// with the client's resource indicator means nothing unless the resource server
// refuses tokens addressed elsewhere.
func TestVerifyAccessTokenRejectsForeignAudience(t *testing.T) {
	signer := testSigner(t)
	token, _, err := signer.MintAccessToken("user-1", "https://attacker.example.com/api/mcp", ScopeMCP, time.Now())
	if err != nil {
		t.Fatalf("MintAccessToken: %v", err)
	}
	if _, err := signer.VerifyAccessToken(token); !errors.Is(err, ErrAudienceMismatch) {
		t.Fatalf("VerifyAccessToken accepted a token addressed to another resource: err = %v", err)
	}
}

func TestVerifyAccessTokenAcceptsThisResource(t *testing.T) {
	signer := testSigner(t)
	for _, resource := range []string{testIssuer + MCPResourcePath, testIssuer} {
		token, _, err := signer.MintAccessToken("user-1", resource, ScopeMCP, time.Now())
		if err != nil {
			t.Fatalf("MintAccessToken: %v", err)
		}
		if _, err := signer.VerifyAccessToken(token); err != nil {
			t.Fatalf("VerifyAccessToken rejected aud=%q, which names this deployment: %v", resource, err)
		}
	}
}

func TestVerifyAccessTokenRejectsForeignKey(t *testing.T) {
	minter := testSigner(t)
	verifier := testSigner(t) // different key pair, same issuer

	token, _, err := minter.MintAccessToken("user-1", "", ScopeMCP, time.Now())
	if err != nil {
		t.Fatalf("MintAccessToken: %v", err)
	}
	if _, err := verifier.VerifyAccessToken(token); !errors.Is(err, ErrInvalidAccessToken) {
		t.Fatalf("VerifyAccessToken accepted a token signed by another key: err = %v", err)
	}
}

func TestVerifyAccessTokenRejectsExpiredToken(t *testing.T) {
	signer := testSigner(t)
	token, _, err := signer.MintAccessToken("user-1", "", ScopeMCP, time.Now().Add(-2*AccessTokenTTL))
	if err != nil {
		t.Fatalf("MintAccessToken: %v", err)
	}
	if _, err := signer.VerifyAccessToken(token); !errors.Is(err, ErrInvalidAccessToken) {
		t.Fatalf("VerifyAccessToken accepted an expired token: err = %v", err)
	}
}

func TestVerifyAccessTokenRejectsForeignIssuer(t *testing.T) {
	keyPEM := testSigningKeyPEM(t)
	other, err := NewSigner(keyPEM, "https://someone-else.example.com")
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	ours, err := NewSigner(keyPEM, testIssuer)
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}

	// Same key, different issuer: only the iss check can reject this, which is
	// what stops one deployment's token from being replayed at another that
	// happens to share key material.
	token, _, err := other.MintAccessToken("user-1", "", ScopeMCP, time.Now())
	if err != nil {
		t.Fatalf("MintAccessToken: %v", err)
	}
	if _, err := ours.VerifyAccessToken(token); !errors.Is(err, ErrInvalidAccessToken) {
		t.Fatalf("VerifyAccessToken accepted a token from another issuer: err = %v", err)
	}
}

// An unsigned "alg: none" token is the classic JWT confusion attack.
func TestVerifyAccessTokenRejectsUnsignedToken(t *testing.T) {
	signer := testSigner(t)
	unsigned, err := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.MapClaims{
		"iss": testIssuer,
		"sub": "attacker",
		"exp": time.Now().Add(time.Hour).Unix(),
	}).SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("build unsigned token: %v", err)
	}
	if _, err := signer.VerifyAccessToken(unsigned); !errors.Is(err, ErrInvalidAccessToken) {
		t.Fatalf("VerifyAccessToken accepted an alg=none token: err = %v", err)
	}
}

func TestNilSignerFailsClosed(t *testing.T) {
	// A deployment without OAUTH_SIGNING_KEY holds a nil signer; every method
	// must report the missing key rather than panic.
	var signer *Signer
	if _, _, err := signer.MintAccessToken("user-1", "", ScopeMCP, time.Now()); !errors.Is(err, ErrNoSigningKey) {
		t.Fatalf("nil signer MintAccessToken error = %v, want ErrNoSigningKey", err)
	}
	if _, err := signer.VerifyAccessToken("anything"); !errors.Is(err, ErrNoSigningKey) {
		t.Fatalf("nil signer VerifyAccessToken error = %v, want ErrNoSigningKey", err)
	}
	if keys, ok := signer.JWKS()["keys"].([]any); !ok || len(keys) != 0 {
		t.Fatalf("nil signer JWKS = %v, want an empty key set", signer.JWKS())
	}
}

func TestJWKSPublishesTheSigningKey(t *testing.T) {
	signer := testSigner(t)
	keys, ok := signer.JWKS()["keys"].([]any)
	if !ok || len(keys) != 1 {
		t.Fatalf("JWKS keys = %v, want exactly one entry", signer.JWKS()["keys"])
	}
	entry, ok := keys[0].(map[string]any)
	if !ok {
		t.Fatalf("JWKS entry has type %T, want a JSON object", keys[0])
	}
	for field, want := range map[string]string{
		"kty": "RSA",
		"use": "sig",
		"alg": "RS256",
		"kid": signer.KeyID(),
	} {
		if got := entry[field]; got != want {
			t.Fatalf("JWKS %s = %v, want %q", field, got, want)
		}
	}
	for _, field := range []string{"n", "e"} {
		if value, _ := entry[field].(string); value == "" {
			t.Fatalf("JWKS %s is empty; a client cannot verify without it", field)
		}
	}
	// The private half must never appear in a published document.
	for _, forbidden := range []string{"d", "p", "q", "dp", "dq", "qi"} {
		if _, present := entry[forbidden]; present {
			t.Fatalf("JWKS leaks private key parameter %q", forbidden)
		}
	}
}

func TestMintedTokenCarriesKeyIDHeader(t *testing.T) {
	// Without kid a client holding a rotated JWKS cannot tell which key to use.
	signer := testSigner(t)
	token, _, err := signer.MintAccessToken("user-1", "", ScopeMCP, time.Now())
	if err != nil {
		t.Fatalf("MintAccessToken: %v", err)
	}
	parsed, _, err := jwt.NewParser().ParseUnverified(token, jwt.MapClaims{})
	if err != nil {
		t.Fatalf("ParseUnverified: %v", err)
	}
	if got := parsed.Header["kid"]; got != signer.KeyID() {
		t.Fatalf("token kid = %v, want %q", got, signer.KeyID())
	}
	if got := parsed.Header["alg"]; got != "RS256" {
		t.Fatalf("token alg = %v, want RS256", got)
	}
}

func TestNewAuthorizationCodeIsUniqueAndOpaque(t *testing.T) {
	seen := make(map[string]struct{}, 32)
	for range 32 {
		code, err := NewAuthorizationCode()
		if err != nil {
			t.Fatalf("NewAuthorizationCode: %v", err)
		}
		if len(code) != 64 {
			t.Fatalf("code length = %d, want 64 hex characters (32 bytes)", len(code))
		}
		if _, duplicate := seen[code]; duplicate {
			t.Fatalf("NewAuthorizationCode returned a duplicate: %q", code)
		}
		seen[code] = struct{}{}
	}
}

func parseClaimsUnverified(t *testing.T, token string) jwt.MapClaims {
	t.Helper()
	claims := jwt.MapClaims{}
	if _, _, err := jwt.NewParser().ParseUnverified(token, claims); err != nil {
		t.Fatalf("ParseUnverified: %v", err)
	}
	return claims
}
