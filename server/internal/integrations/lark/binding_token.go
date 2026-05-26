package lark

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// BindingToken is the public shape of a freshly minted token. The raw
// token is returned to the caller exactly once — it is the unguessable
// secret embedded in the binding URL the Bot replies with. After this
// call returns, only the hash exists server-side; the raw value
// cannot be recovered from the DB.
type BindingToken struct {
	Raw       string
	ExpiresAt time.Time
}

// RedeemedBindingToken is the row returned to the caller after a
// successful redemption. The redemption path uses these fields to
// write the lark_user_binding row.
type RedeemedBindingToken struct {
	WorkspaceID    pgtype.UUID
	InstallationID pgtype.UUID
	LarkOpenID     OpenID
}

// BindingTokenService mints and redeems binding tokens for the
// "you're not bound yet, click here" flow. The TTL is fixed at
// BindingTokenTTL (15 min); the DB CHECK enforces the same cap so a
// misconfigured caller cannot quietly mint a longer-lived token.
type BindingTokenService struct {
	queries *db.Queries
	now     func() time.Time
}

// NewBindingTokenService constructs the default service. The clock
// is injectable so tests can pin time for deterministic expiry
// behavior; production callers use NewBindingTokenServiceWithClock
// with time.Now.
func NewBindingTokenService(queries *db.Queries) *BindingTokenService {
	return NewBindingTokenServiceWithClock(queries, time.Now)
}

// NewBindingTokenServiceWithClock is the seam for tests; production
// callers should use NewBindingTokenService.
func NewBindingTokenServiceWithClock(queries *db.Queries, now func() time.Time) *BindingTokenService {
	return &BindingTokenService{queries: queries, now: now}
}

// Mint creates a new single-use binding token and returns the raw
// secret + expiry. The raw value MUST be sent over a secure channel
// to the intended recipient — Lark DMs are encrypted in transit by
// the platform — and never logged. Mint is the only function in this
// package that produces a raw token; subsequent reads are by hash.
func (s *BindingTokenService) Mint(ctx context.Context, workspaceID, installationID pgtype.UUID, openID OpenID) (BindingToken, error) {
	raw, err := randomToken(32)
	if err != nil {
		return BindingToken{}, fmt.Errorf("generate token: %w", err)
	}
	hash := hashToken(raw)
	expiresAt := s.now().Add(BindingTokenTTL)

	if _, err := s.queries.CreateLarkBindingToken(ctx, db.CreateLarkBindingTokenParams{
		TokenHash:      hash,
		WorkspaceID:    workspaceID,
		InstallationID: installationID,
		LarkOpenID:     string(openID),
		ExpiresAt:      pgtype.Timestamptz{Time: expiresAt, Valid: true},
	}); err != nil {
		return BindingToken{}, fmt.Errorf("persist token: %w", err)
	}
	return BindingToken{Raw: raw, ExpiresAt: expiresAt}, nil
}

// Redeem consumes a raw token presented by the user in the binding
// URL. ErrBindingTokenInvalid covers all rejection paths — wrong
// hash, already consumed, expired — without leaking which: the
// caller's reply is the same for all three, and a precise error
// would let an attacker probe for token-replay races.
//
// The redemption itself is atomic at the DB layer: the underlying
// query updates `consumed_at` only when the row is unconsumed and
// unexpired, so two simultaneous redemptions of the same token
// cannot both succeed.
func (s *BindingTokenService) Redeem(ctx context.Context, raw string) (RedeemedBindingToken, error) {
	row, err := s.queries.ConsumeLarkBindingToken(ctx, hashToken(raw))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return RedeemedBindingToken{}, ErrBindingTokenInvalid
		}
		return RedeemedBindingToken{}, fmt.Errorf("consume token: %w", err)
	}
	return RedeemedBindingToken{
		WorkspaceID:    row.WorkspaceID,
		InstallationID: row.InstallationID,
		LarkOpenID:     OpenID(row.LarkOpenID),
	}, nil
}

// ErrBindingTokenInvalid is returned by Redeem for every rejection
// case. The caller must NOT distinguish "expired" from "already
// used" — that distinction enables timing oracles for token replay
// races and adds no product value (the user sees the same "link
// invalid or expired, please request a new one" copy either way).
var ErrBindingTokenInvalid = errors.New("binding token invalid or expired")

func randomToken(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	// URL-safe so the token embeds cleanly in the binding URL
	// without escaping. RawURLEncoding drops `=` padding which is
	// optional for decoders and would otherwise look ugly in
	// user-visible URLs.
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
