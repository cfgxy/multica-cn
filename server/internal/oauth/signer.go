package oauth

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// AccessTokenTTL is how long a minted access token stays valid.
//
// 90 days matches the initial signing window of a Multica PAT
// (handler.PATRenewExtension), which is the credential an OAuth access token
// stands in for here. The first version issues no refresh token, so this is
// also the interval at which a ChatGPT connection has to be re-authorized.
// docs/adr/001-mcp-oauth-behind-nextjs-proxy.md §5 records this as the decision
// most worth revisiting.
const AccessTokenTTL = 90 * 24 * time.Hour

// ScopeMCP is the only scope the first version issues.
const ScopeMCP = "mcp"

// ErrNoSigningKey reports that OAUTH_SIGNING_KEY is unset or unusable, which
// is what keeps the whole OAuth surface switched off.
var ErrNoSigningKey = errors.New("OAUTH_SIGNING_KEY is not configured")

// Signer mints and verifies the RS256 access tokens of the MCP authorization
// server. It holds the private key read from OAUTH_SIGNING_KEY at boot; the
// public half is published through /.well-known/jwks.json so a client or a
// future third-party resource server can verify without a shared secret.
type Signer struct {
	key      *rsa.PrivateKey
	keyID    string
	issuer   string
	tokenTTL time.Duration
}

// NewSigner parses a PEM-encoded RSA private key and binds it to an issuer.
//
// The PEM text is never echoed into the returned error: a parse failure reports
// only that the value could not be read as an RSA private key, because this
// error reaches the boot log.
func NewSigner(pemKey, issuer string) (*Signer, error) {
	pemKey = strings.TrimSpace(pemKey)
	if pemKey == "" {
		return nil, ErrNoSigningKey
	}
	block, _ := pem.Decode([]byte(pemKey))
	if block == nil {
		return nil, fmt.Errorf("%w: value is not PEM-encoded", ErrNoSigningKey)
	}
	key, err := parseRSAPrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrNoSigningKey, err.Error())
	}
	return &Signer{
		key:      key,
		keyID:    keyIDFor(&key.PublicKey),
		issuer:   strings.TrimRight(strings.TrimSpace(issuer), "/"),
		tokenTTL: AccessTokenTTL,
	}, nil
}

func parseRSAPrivateKey(der []byte) (*rsa.PrivateKey, error) {
	// PKCS#1 ("BEGIN RSA PRIVATE KEY") and PKCS#8 ("BEGIN PRIVATE KEY") are
	// both shapes `openssl genrsa` and `openssl genpkey` produce, and an
	// operator has no reason to know which one this server wants.
	if key, err := x509.ParsePKCS1PrivateKey(der); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, errors.New("not an RSA private key in PKCS#1 or PKCS#8 form")
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("key is not RSA")
	}
	return key, nil
}

// keyIDFor derives a stable kid from the public key, so JWKS and the token
// header agree without persisting anything.
func keyIDFor(pub *rsa.PublicKey) string {
	digest := sha256.Sum256(append(pub.N.Bytes(), big.NewInt(int64(pub.E)).Bytes()...))
	return hex.EncodeToString(digest[:8])
}

// Issuer returns the site root this signer stamps into `iss`.
func (s *Signer) Issuer() string { return s.issuer }

// KeyID returns the kid published in JWKS and stamped into token headers.
func (s *Signer) KeyID() string { return s.keyID }

// MintAccessToken signs an access token for userID.
//
// audience is the `resource` value the client sent through both the
// authorization and the token request (RFC 8707). It is echoed into `aud`
// unchanged rather than replaced by a server-side constant: the resource
// indicator is what binds the token to one MCP endpoint, so a token minted for
// resource A must not verify at resource B. An empty audience falls back to the
// issuer, which keeps a client that omitted the optional parameter working
// while still producing an audience-bound token.
func (s *Signer) MintAccessToken(userID, audience, scope string, now time.Time) (string, time.Time, error) {
	if s == nil || s.key == nil {
		return "", time.Time{}, ErrNoSigningKey
	}
	if audience == "" {
		audience = s.issuer
	}
	if scope == "" {
		scope = ScopeMCP
	}
	expiresAt := now.Add(s.tokenTTL)
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss":   s.issuer,
		"sub":   userID,
		"aud":   audience,
		"scope": scope,
		"iat":   now.Unix(),
		"exp":   expiresAt.Unix(),
	})
	token.Header["kid"] = s.keyID
	signed, err := token.SignedString(s.key)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("sign access token: %w", err)
	}
	return signed, expiresAt, nil
}

// AccessTokenClaims is the verified subset the auth middleware consumes.
type AccessTokenClaims struct {
	Subject  string
	Audience []string
	Scope    string
}

// ErrInvalidAccessToken reports that a presented token is not a valid access
// token minted by this server.
var ErrInvalidAccessToken = errors.New("invalid oauth access token")

// ErrAudienceMismatch reports that a structurally valid token names a resource
// other than this deployment's MCP endpoint.
var ErrAudienceMismatch = errors.New("oauth access token audience does not name this resource")

// acceptedAudiences lists the `aud` values this deployment answers to: the MCP
// endpoint URL, and the issuer for a client that omitted the optional RFC 8707
// `resource` parameter.
func (s *Signer) acceptedAudiences() []string {
	return []string{s.issuer + MCPResourcePath, s.issuer}
}

// VerifyAccessToken parses and validates an access token.
//
// Only RS256 is accepted: allowing the algorithm in the token header to select
// the verification method is the classic JWT confusion bug, and this server
// mints exactly one algorithm. `iss` is checked against the configured site
// root so a token minted by another deployment cannot be replayed here, and
// `aud` against this deployment's resource identifiers — the resource indicator
// is only a binding if the resource server enforces it, otherwise a token the
// client requested for some other endpoint replays here unchallenged.
func (s *Signer) VerifyAccessToken(tokenString string) (*AccessTokenClaims, error) {
	if s == nil || s.key == nil {
		return nil, ErrNoSigningKey
	}
	parsed, err := jwt.Parse(tokenString, func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, jwt.ErrSignatureInvalid
		}
		return &s.key.PublicKey, nil
	}, jwt.WithValidMethods([]string{"RS256"}), jwt.WithIssuer(s.issuer))
	if err != nil || !parsed.Valid {
		return nil, ErrInvalidAccessToken
	}
	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		return nil, ErrInvalidAccessToken
	}
	subject, _ := claims["sub"].(string)
	if strings.TrimSpace(subject) == "" {
		return nil, ErrInvalidAccessToken
	}
	scope, _ := claims["scope"].(string)
	audience := audienceValues(claims["aud"])
	if !audienceMatches(audience, s.acceptedAudiences()) {
		return nil, ErrAudienceMismatch
	}
	return &AccessTokenClaims{
		Subject:  subject,
		Audience: audience,
		Scope:    scope,
	}, nil
}

func audienceMatches(presented, accepted []string) bool {
	for _, value := range presented {
		for _, want := range accepted {
			if value == want {
				return true
			}
		}
	}
	return false
}

// audienceValues normalizes the `aud` claim, which RFC 7519 §4.1.3 allows to be
// either a single string or an array of them.
func audienceValues(raw any) []string {
	switch value := raw.(type) {
	case string:
		if value == "" {
			return nil
		}
		return []string{value}
	case []any:
		out := make([]string, 0, len(value))
		for _, item := range value {
			if s, ok := item.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

// JWKS renders the public half of the signing key as a JWK set.
func (s *Signer) JWKS() map[string]any {
	if s == nil || s.key == nil {
		return map[string]any{"keys": []any{}}
	}
	pub := &s.key.PublicKey
	return map[string]any{
		"keys": []any{
			map[string]any{
				"kty": "RSA",
				"use": "sig",
				"alg": "RS256",
				"kid": s.keyID,
				"n":   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
				"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
			},
		},
	}
}

// NewAuthorizationCode returns a fresh opaque authorization code.
func NewAuthorizationCode() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate authorization code: %w", err)
	}
	return hex.EncodeToString(buf), nil
}
