package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	redismock "github.com/go-redis/redismock/v9"
)

func testCode() AuthorizationCode {
	return AuthorizationCode{
		ClientID:            "chatgpt",
		UserID:              "11111111-2222-3333-4444-555555555555",
		RedirectURI:         "https://chat.openai.com/aip/callback",
		CodeChallenge:       DeriveChallenge(testVerifier),
		CodeChallengeMethod: MethodS256,
		Resource:            "https://multica.example.com/api/mcp",
		Scope:               ScopeMCP,
	}
}

func TestSaveUsesTheSixtySecondTTL(t *testing.T) {
	client, mock := redismock.NewClientMock()
	t.Cleanup(func() { client.Close() })

	data := testCode()
	payload, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	mock.ExpectSet(codeKeyPrefix+"code-1", payload, AuthorizationCodeTTL).SetVal("OK")

	if err := NewCodeStore(client).Save(context.Background(), "code-1", data); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("Save did not issue SET with the expected key and TTL: %v", err)
	}
}

// GETDEL rather than GET is what makes a code single-use: the read and the
// delete have to be one round trip, or two token requests racing on one stolen
// code could both observe it.
func TestConsumeDeletesInTheSameRoundTrip(t *testing.T) {
	client, mock := redismock.NewClientMock()
	t.Cleanup(func() { client.Close() })

	data := testCode()
	payload, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	mock.ExpectGetDel(codeKeyPrefix + "code-1").SetVal(string(payload))

	got, err := NewCodeStore(client).Consume(context.Background(), "code-1")
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("Consume did not issue GETDEL: %v", err)
	}
	if got.Resource != data.Resource || got.CodeChallenge != data.CodeChallenge || got.UserID != data.UserID {
		t.Fatalf("Consume returned %+v, want the saved state %+v", *got, data)
	}
}

// A second redemption of the same code finds nothing. The error must not
// distinguish "consumed" from "never existed".
func TestConsumeReportsMissingCodeUniformly(t *testing.T) {
	client, mock := redismock.NewClientMock()
	t.Cleanup(func() { client.Close() })

	mock.ExpectGetDel(codeKeyPrefix + "code-1").RedisNil()

	if _, err := NewCodeStore(client).Consume(context.Background(), "code-1"); !errors.Is(err, ErrCodeNotFound) {
		t.Fatalf("Consume of an absent code error = %v, want ErrCodeNotFound", err)
	}
}

func TestCodeStoreWithoutRedisFailsClosed(t *testing.T) {
	// A deployment without Redis must refuse the grant rather than mint codes
	// that can never be redeemed.
	store := NewCodeStore(nil)
	if store.Available() {
		t.Fatal("Available() = true for a store without a Redis client")
	}
	if err := store.Save(context.Background(), "code-1", testCode()); !errors.Is(err, ErrCodeStoreUnavailable) {
		t.Fatalf("Save error = %v, want ErrCodeStoreUnavailable", err)
	}
	if _, err := store.Consume(context.Background(), "code-1"); !errors.Is(err, ErrCodeStoreUnavailable) {
		t.Fatalf("Consume error = %v, want ErrCodeStoreUnavailable", err)
	}
}

func TestCodeStoreKeysAreNamespaced(t *testing.T) {
	// The store shares Redis with sessions, PAT caches and the realtime relay;
	// an unprefixed code key could collide with any of them.
	if codeKeyPrefix == "" || codeKeyPrefix[len(codeKeyPrefix)-1] != ':' {
		t.Fatalf("codeKeyPrefix = %q, want a colon-terminated namespace", codeKeyPrefix)
	}
}
