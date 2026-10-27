package auth

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"

	"github.com/redis/go-redis/v9"
)

// OAuthGrantState is the revocation-relevant state of one OAuth grant and
// its client, resolved by the auth middleware after signature verification.
// All three fields are facts about the world, not a verdict — the gate
// derives the verdict (any of them true/false in the fatal direction means
// "reject").
type OAuthGrantState struct {
	// GrantRevoked: the user (or an admin) revoked this authorization.
	GrantRevoked bool `json:"grant_revoked"`
	// ClientDisabled: an operator soft-disabled the client.
	ClientDisabled bool `json:"client_disabled"`
	// ClientFound: the client row still exists. A deleted client's tokens
	// must die, but a LEFT JOIN cannot distinguish "deleted" from
	// "enabled" — the lookup folds that into this explicit flag.
	ClientFound bool `json:"client_found"`
}

// Live reports whether a token carrying this grant may pass.
func (s OAuthGrantState) Live() bool {
	return s.ClientFound && !s.GrantRevoked && !s.ClientDisabled
}

// OAuthGrantLookup resolves the gate state for a grant id. found=false
// means the grant row itself is gone (hard-deleted or never existed) —
// both are fatal for the token. Returned by the wiring layer as a closure
// over the sqlc query, so this package keeps its zero-generated-code
// discipline (same shape as DisabledLookup).
type OAuthGrantLookup func(ctx context.Context, grantID string) (state OAuthGrantState, found bool, err error)

// oauthGrantCachePrefix namespaces the grant gate in Redis next to the PAT
// cache (mul:auth:*).
const oauthGrantCachePrefix = "mul:auth:oauthgrant:"

// ErrGateUnavailable reports that the gate cannot answer (Redis down AND
// the backing lookup failed). The middleware treats this as reject: for a
// revocation gate, "cannot check" must not mean "assume alive".
var ErrGateUnavailable = errors.New("oauth grant gate unavailable")

// OAuthGate answers "may this grant id still act?" with a Redis-cached
// lookup, the same revocation-latency contract as PATCache: a revoke takes
// effect immediately when the acting write path invalidates the entry, and
// at most AuthCacheTTL later if that invalidation is missed. A nil gate is
// safe — Check reports a miss-lookup with a nil lookup as unavailable.
type OAuthGate struct {
	rdb    *redis.Client
	lookup OAuthGrantLookup
}

// NewOAuthGate returns a gate over rdb and lookup. Either may be nil: a nil
// Redis disables caching (every Check goes to the lookup), a nil lookup
// makes Check report ErrGateUnavailable — the wiring layer passes both or
// neither.
func NewOAuthGate(rdb *redis.Client, lookup OAuthGrantLookup) *OAuthGate {
	return &OAuthGate{rdb: rdb, lookup: lookup}
}

func oauthGrantCacheKey(grantID string) string { return oauthGrantCachePrefix + grantID }

// Check resolves the gate state for grantID. fresh reports that the answer
// came from a live lookup (not the cache) — the caller uses it to decide
// whether to refresh last_used_at, mirroring the PAT cache-miss throttle.
//
// The error return is ErrGateUnavailable, never the underlying error: the
// middleware logs reason strings, not lookup internals.
func (g *OAuthGate) Check(ctx context.Context, grantID string) (state OAuthGrantState, found, fresh bool, err error) {
	if g == nil || g.lookup == nil {
		return OAuthGrantState{}, false, false, ErrGateUnavailable
	}
	if g.rdb != nil {
		if raw, rerr := g.rdb.Get(ctx, oauthGrantCacheKey(grantID)).Result(); rerr == nil {
			var cached OAuthGrantState
			if jerr := json.Unmarshal([]byte(raw), &cached); jerr == nil {
				return cached, true, false, nil
			}
			// Corrupt entry: fall through to the lookup and overwrite it.
			slog.Warn("oauth_gate: corrupt cache entry; re-reading from DB")
		} else if !errors.Is(rerr, redis.Nil) {
			// Redis down: degrade to direct lookups, same as PATCache.
			slog.Warn("oauth_gate: get failed; falling back to DB", "error", rerr)
		}
	}

	state, found, err = g.lookup(ctx, grantID)
	if err != nil {
		return OAuthGrantState{}, false, false, ErrGateUnavailable
	}
	if !found {
		// Do not cache absence: a grant row materializing later (should
		// not happen — rows are soft-revoked, never deleted while live
		// tokens reference them) must not be shadowed by a stale miss.
		return OAuthGrantState{}, false, true, nil
	}
	g.set(ctx, grantID, state)
	return state, true, true, nil
}

func (g *OAuthGate) set(ctx context.Context, grantID string, state OAuthGrantState) {
	if g.rdb == nil {
		return
	}
	raw, err := json.Marshal(state)
	if err != nil {
		slog.Warn("oauth_gate: marshal failed", "error", err)
		return
	}
	if err := g.rdb.Set(ctx, oauthGrantCacheKey(grantID), raw, AuthCacheTTL).Err(); err != nil {
		slog.Warn("oauth_gate: set failed", "error", err)
	}
}

// Invalidate drops the cached state for grantID. Called on the write path
// (grant revoke, grant upsert, client disable/delete) so the change is
// effective immediately rather than at TTL. Errors are logged and
// swallowed: the entry expires within AuthCacheTTL regardless.
func (g *OAuthGate) Invalidate(ctx context.Context, grantID string) {
	if g == nil || g.rdb == nil {
		return
	}
	if err := g.rdb.Del(ctx, oauthGrantCacheKey(grantID)).Err(); err != nil {
		slog.Warn("oauth_gate: invalidate failed; entry will expire on TTL", "error", err)
	}
}

// InvalidateClient drops every cached grant state of one client. Grant ids
// are supplied by the caller (the revoke query returns them); this shape
// keeps the gate free of any query dependency.
func (g *OAuthGate) InvalidateClient(ctx context.Context, grantIDs []string) {
	for _, id := range grantIDs {
		g.Invalidate(ctx, id)
	}
}

// OAuthGateTTL exposes the contract for tests and the ADR: gate answers are
// at most this stale after a missed invalidation.
var OAuthGateTTL = AuthCacheTTL
