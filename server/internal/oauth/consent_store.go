package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// ConsentRequestTTL bounds how long a pending consent screen stays
// redeemable. It only has to outlive a human reading one page; ten minutes
// is generous and keeps abandoned tabs from holding redirectable state.
const ConsentRequestTTL = 10 * time.Minute

const consentKeyPrefix = "mul:oauth:consent:"

// ErrConsentNotFound reports that a consent request is unknown or expired.
// The two are deliberately one error — an expired request and a forged id
// deserve the same treatment (restarting the authorize flow).
var ErrConsentNotFound = errors.New("consent request not found")

// ConsentRequest is the authorization request parked while the user looks
// at the consent screen. It is the validated tail of an /authorize call —
// response_type, PKCE and redirect_uri have already been checked — so the
// approval path can rebuild the redirect without re-parsing query strings.
type ConsentRequest struct {
	ClientID            string `json:"client_id"`
	UserID              string `json:"user_id"`
	RedirectURI         string `json:"redirect_uri"`
	Scope               string `json:"scope"`
	State               string `json:"state,omitempty"`
	Resource            string `json:"resource,omitempty"`
	CodeChallenge       string `json:"code_challenge"`
	CodeChallengeMethod string `json:"code_challenge_method"`
}

// ConsentStore holds pending consent requests in Redis. Same shape and
// failure posture as CodeStore: a nil Redis fails the consent flow closed.
type ConsentStore struct {
	rdb *redis.Client
}

// NewConsentStore returns a store over rdb.
func NewConsentStore(rdb *redis.Client) *ConsentStore {
	return &ConsentStore{rdb: rdb}
}

func consentKey(id string) string { return consentKeyPrefix + id }

// NewConsentRequestID returns a fresh opaque consent request id.
func NewConsentRequestID() (string, error) {
	return NewAuthorizationCode()
}

// Available reports whether the backing Redis answers PINGs.
func (s *ConsentStore) Available() bool {
	if s == nil || s.rdb == nil {
		return false
	}
	return s.rdb.Ping(context.Background()).Err() == nil
}

// Save parks a consent request under id.
func (s *ConsentStore) Save(ctx context.Context, id string, req ConsentRequest) error {
	if s == nil || s.rdb == nil {
		return ErrCodeStoreUnavailable
	}
	raw, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal consent request: %w", err)
	}
	return s.rdb.Set(ctx, consentKey(id), raw, ConsentRequestTTL).Err()
}

// Load reads a pending request without consuming it, so the consent page
// can re-render after a refresh. The id stays valid until TTL.
func (s *ConsentStore) Load(ctx context.Context, id string) (ConsentRequest, error) {
	if s == nil || s.rdb == nil {
		return ConsentRequest{}, ErrCodeStoreUnavailable
	}
	raw, err := s.rdb.Get(ctx, consentKey(id)).Result()
	if errors.Is(err, redis.Nil) {
		return ConsentRequest{}, ErrConsentNotFound
	}
	if err != nil {
		return ConsentRequest{}, err
	}
	var req ConsentRequest
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		return ConsentRequest{}, err
	}
	return req, nil
}

// Consume atomically reads and deletes a pending request. Approval and
// denial are both single-shot: the id cannot be replayed into a second
// grant decision.
func (s *ConsentStore) Consume(ctx context.Context, id string) (ConsentRequest, error) {
	if s == nil || s.rdb == nil {
		return ConsentRequest{}, ErrCodeStoreUnavailable
	}
	raw, err := s.rdb.GetDel(ctx, consentKey(id)).Result()
	if errors.Is(err, redis.Nil) {
		return ConsentRequest{}, ErrConsentNotFound
	}
	if err != nil {
		return ConsentRequest{}, err
	}
	var req ConsentRequest
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		return ConsentRequest{}, err
	}
	return req, nil
}

// NewClientSecret returns a fresh client secret: 64 hex chars of CSPRNG
// output, the same 32-byte strength authorization codes use. Callers hash
// it with auth.HashToken before storage; the plaintext exists only in the
// return value and the single response that carries it.
func NewClientSecret() (string, error) {
	return NewAuthorizationCode()
}
