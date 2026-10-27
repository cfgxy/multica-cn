package oauth

import (
	"errors"
	"strings"
	"testing"
)

// The verifier is a fixed 43-character value (the RFC 7636 minimum) so the
// challenge below stays a constant the test can assert against by identity.
const testVerifier = "abcdefghijklmnopqrstuvwxyz0123456789-._~ABC"

func TestDeriveChallengeIsUnpaddedBase64URL(t *testing.T) {
	challenge := DeriveChallenge(testVerifier)
	if strings.Contains(challenge, "=") {
		t.Fatalf("challenge carries base64 padding, which RFC 7636 §4.2 forbids: %q", challenge)
	}
	if strings.ContainsAny(challenge, "+/") {
		t.Fatalf("challenge uses standard base64 alphabet instead of base64url: %q", challenge)
	}
	// A SHA-256 digest is 32 bytes, which is 43 unpadded base64 characters.
	if len(challenge) != 43 {
		t.Fatalf("challenge length = %d, want 43 (32-byte digest, unpadded)", len(challenge))
	}
}

func TestVerifyPKCEAcceptsMatchingVerifier(t *testing.T) {
	if err := VerifyPKCE(DeriveChallenge(testVerifier), MethodS256, testVerifier); err != nil {
		t.Fatalf("VerifyPKCE rejected the verifier its own challenge was derived from: %v", err)
	}
}

// The negative case PKCE exists for: an attacker redeems an intercepted
// authorization code while holding a verifier the legitimate client never sent.
func TestVerifyPKCERejectsMismatchedVerifier(t *testing.T) {
	challenge := DeriveChallenge(testVerifier)
	attackerVerifier := strings.Repeat("z", 43)

	err := VerifyPKCE(challenge, MethodS256, attackerVerifier)
	if !errors.Is(err, ErrVerifierMismatch) {
		t.Fatalf("VerifyPKCE(challenge, S256, wrong-verifier) error = %v, want ErrVerifierMismatch", err)
	}
}

// A verifier that is a prefix of the real one must not pass either: this is the
// shape a length- or prefix-sensitive comparison would let through.
func TestVerifyPKCERejectsTruncatedVerifier(t *testing.T) {
	challenge := DeriveChallenge(testVerifier)
	truncated := testVerifier[:len(testVerifier)-1] + "X"

	if err := VerifyPKCE(challenge, MethodS256, truncated); !errors.Is(err, ErrVerifierMismatch) {
		t.Fatalf("VerifyPKCE with a one-character-off verifier error = %v, want ErrVerifierMismatch", err)
	}
}

func TestVerifyPKCERejectsPlainMethod(t *testing.T) {
	// "plain" would make the challenge equal to the verifier, so an attacker
	// holding only an intercepted code could satisfy the check.
	err := VerifyPKCE(testVerifier, "plain", testVerifier)
	if !errors.Is(err, ErrUnsupportedChallengeMethod) {
		t.Fatalf("VerifyPKCE with method=plain error = %v, want ErrUnsupportedChallengeMethod", err)
	}
}

func TestVerifyPKCERejectsOutOfRangeVerifierLength(t *testing.T) {
	short := strings.Repeat("a", 42)
	long := strings.Repeat("a", 129)

	if err := VerifyPKCE(DeriveChallenge(short), MethodS256, short); !errors.Is(err, ErrVerifierLength) {
		t.Fatalf("42-character verifier error = %v, want ErrVerifierLength", err)
	}
	if err := VerifyPKCE(DeriveChallenge(long), MethodS256, long); !errors.Is(err, ErrVerifierLength) {
		t.Fatalf("129-character verifier error = %v, want ErrVerifierLength", err)
	}
}

func TestVerifyPKCEToleratesPaddedStoredChallenge(t *testing.T) {
	// Some clients pad their challenge. Rejecting for that alone would fail a
	// verifier that actually matches.
	padded := DeriveChallenge(testVerifier) + "="
	if err := VerifyPKCE(padded, MethodS256, testVerifier); err != nil {
		t.Fatalf("VerifyPKCE rejected a padded but correct challenge: %v", err)
	}
}
