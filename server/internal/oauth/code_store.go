package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// AuthorizationCodeTTL bounds how long a code stays redeemable.
//
// 60s is the window RFC 6749 §4.1.2 recommends. The code travels through a
// browser redirect straight into the client's back-channel token call, so a
// longer window buys nothing and only widens the replay surface for a code
// captured from a referrer log or a shared browser history.
const AuthorizationCodeTTL = 60 * time.Second

const codeKeyPrefix = "mul:oauth:code:"

// ErrCodeNotFound reports that an authorization code is unknown, expired, or
// already consumed. The three are deliberately one error: telling a caller which
// of them applied would confirm that a given code once existed.
var ErrCodeNotFound = errors.New("authorization code not found")

// ErrCodeStoreUnavailable reports that no Redis client is configured, so the
// authorization-code grant cannot run.
var ErrCodeStoreUnavailable = errors.New("authorization code store unavailable")

// AuthorizationCode is the state carried from /authorize to /token.
type AuthorizationCode struct {
	ClientID            string `json:"client_id"`
	UserID              string `json:"user_id"`
	RedirectURI         string `json:"redirect_uri"`
	CodeChallenge       string `json:"code_challenge"`
	CodeChallengeMethod string `json:"code_challenge_method"`
	// Resource is the RFC 8707 resource indicator from the authorization
	// request. The token request must present the same value, and it becomes
	// the minted token's `aud`.
	Resource string `json:"resource"`
	Scope    string `json:"scope"`
}

// CodeStore holds authorization codes in Redis.
//
// Redis rather than a table: a code lives 60 seconds and is consumed once, so a
// row would be pure write amplification plus a reaper. The consume path is a
// single GETDEL, which makes single-use enforcement atomic across API replicas
// without a lock.
type CodeStore struct {
	rdb *redis.Client
}

// NewCodeStore returns a store over rdb. A nil rdb yields a store whose
// operations fail with ErrCodeStoreUnavailable, which is the correct behaviour
// for a deployment without Redis: failing closed beats minting codes that can
// never be redeemed.
func NewCodeStore(rdb *redis.Client) *CodeStore {
	return &CodeStore{rdb: rdb}
}

// Available reports whether the store can actually serve the grant.
func (s *CodeStore) Available() bool {
	return s != nil && s.rdb != nil
}

// Save stores code with the standard TTL.
func (s *CodeStore) Save(ctx context.Context, code string, data AuthorizationCode) error {
	if !s.Available() {
		return ErrCodeStoreUnavailable
	}
	payload, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal authorization code: %w", err)
	}
	if err := s.rdb.Set(ctx, codeKeyPrefix+code, payload, AuthorizationCodeTTL).Err(); err != nil {
		return fmt.Errorf("store authorization code: %w", err)
	}
	return nil
}

// Consume returns the stored state and deletes it in the same round trip.
//
// GETDEL is what makes the code single-use: two token requests racing on one
// stolen code cannot both read it, because only one GETDEL observes a value.
func (s *CodeStore) Consume(ctx context.Context, code string) (*AuthorizationCode, error) {
	if !s.Available() {
		return nil, ErrCodeStoreUnavailable
	}
	raw, err := s.rdb.GetDel(ctx, codeKeyPrefix+code).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, ErrCodeNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("consume authorization code: %w", err)
	}
	var data AuthorizationCode
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("decode authorization code: %w", err)
	}
	return &data, nil
}
