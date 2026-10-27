package oauth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"strings"
)

// MethodS256 is the only code_challenge_method this server accepts.
//
// "plain" is deliberately absent: OpenAI's MCP connector requires S256 and
// documents servers without it as unsupported, so accepting plain would only
// widen the attack surface for clients that could already do better. RFC 7636
// §4.2 permits a server to support S256 alone.
const MethodS256 = "S256"

var (
	// ErrUnsupportedChallengeMethod is returned for any method other than S256.
	ErrUnsupportedChallengeMethod = errors.New("unsupported code_challenge_method")
	// ErrVerifierMismatch is returned when the verifier does not hash to the
	// challenge recorded at authorization time.
	ErrVerifierMismatch = errors.New("code_verifier does not match code_challenge")
	// ErrVerifierLength is returned when the verifier is outside the length
	// range RFC 7636 §4.1 mandates.
	ErrVerifierLength = errors.New("code_verifier length out of range")
)

// DeriveChallenge returns the S256 challenge for verifier: the
// base64url-without-padding encoding of its SHA-256 digest (RFC 7636 §4.2).
func DeriveChallenge(verifier string) string {
	digest := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

// VerifyPKCE checks a presented code_verifier against the code_challenge the
// authorization request recorded.
//
// The comparison is constant-time. Both values are attacker-supplied in the
// failure cases this guards — a stolen authorization code without the matching
// verifier is exactly the interception attack PKCE exists to stop — so a
// length-dependent early exit would leak how much of the challenge matched.
func VerifyPKCE(challenge, method, verifier string) error {
	if method != MethodS256 {
		return ErrUnsupportedChallengeMethod
	}
	if len(verifier) < 43 || len(verifier) > 128 {
		return ErrVerifierLength
	}
	// The stored challenge is normalized to unpadded base64url at
	// authorization time; strip padding defensively so a client that padded
	// its challenge is not rejected for a purely encoding difference.
	expected := strings.TrimRight(challenge, "=")
	if subtle.ConstantTimeCompare([]byte(DeriveChallenge(verifier)), []byte(expected)) != 1 {
		return ErrVerifierMismatch
	}
	return nil
}
