package auth

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// UserStateCacheTTL bounds how long a per-user disabled flag stays cached
// before the auth path goes back to Postgres. This is the revocation
// latency contract for account disable: an existing browser session or PAT
// keeps working for at most one TTL window after the disable lands (the
// same trade-off MembershipCache (5 min) and PATCache (10 min) already
// make); new logins are always rejected immediately because the login path
// reads the user row directly.
const UserStateCacheTTL = 60 * time.Second

// userStateCachePrefix namespaces user-state keys alongside the other
// auth caches (mul:auth:*).
const userStateCachePrefix = "mul:auth:ustate:"

// userStateDisabled / userStateEnabled are the two cached values. Only
// one byte is stored; the key namespace already scopes it to "is this
// account disabled".
const (
	userStateDisabled = "1"
	userStateEnabled  = "0"
)

// UserStateCache caches the per-user disabled flag so the auth middleware
// does not add a DB round-trip to every request. Backed by Redis when one
// is configured; otherwise a small in-process map with the same TTL, so
// single-node deployments without Redis keep identical semantics (their
// invalidation scope is just one node).
//
// A nil *UserStateCache is safe to use — every method reports a miss or
// no-ops, and callers degrade to direct DB lookups.
type UserStateCache struct {
	rdb *redis.Client

	// In-process fallback used when rdb is nil. Entries expire lazily.
	mu      sync.RWMutex
	local   map[string]userStateEntry
	nowFunc func() time.Time
}

type userStateEntry struct {
	disabled  bool
	expiresAt time.Time
}

// NewUserStateCache returns a cache backed by rdb. Pass a nil client to use
// the in-process fallback.
func NewUserStateCache(rdb *redis.Client) *UserStateCache {
	return &UserStateCache{
		rdb:     rdb,
		local:   make(map[string]userStateEntry),
		nowFunc: time.Now,
	}
}

func userStateKey(userID string) string {
	return userStateCachePrefix + userID
}

// Get returns the cached disabled flag for the user. ok=false on miss or any
// Redis error — a dead Redis must not take down auth; the caller falls back
// to the authoritative DB read.
func (c *UserStateCache) Get(ctx context.Context, userID string) (disabled, ok bool) {
	if c == nil || userID == "" {
		return false, false
	}
	if c.rdb != nil {
		v, err := c.rdb.Get(ctx, userStateKey(userID)).Result()
		if err != nil {
			if !errors.Is(err, redis.Nil) {
				slog.Warn("user_state_cache: get failed; falling back to DB", "error", err)
			}
			return false, false
		}
		return v == userStateDisabled, true
	}

	c.mu.RLock()
	entry, hit := c.local[userID]
	c.mu.RUnlock()
	if !hit || !c.nowFunc().Before(entry.expiresAt) {
		return false, false
	}
	return entry.disabled, true
}

// Set caches the disabled flag for one TTL window. Errors are logged and
// swallowed — a cache write failure is not a request failure.
func (c *UserStateCache) Set(ctx context.Context, userID string, disabled bool) {
	if c == nil || userID == "" {
		return
	}
	if c.rdb != nil {
		v := userStateEnabled
		if disabled {
			v = userStateDisabled
		}
		if err := c.rdb.Set(ctx, userStateKey(userID), v, UserStateCacheTTL).Err(); err != nil {
			slog.Warn("user_state_cache: set failed", "error", err)
		}
		return
	}

	c.mu.Lock()
	c.local[userID] = userStateEntry{disabled: disabled, expiresAt: c.nowFunc().Add(UserStateCacheTTL)}
	c.mu.Unlock()
}

// Invalidate drops the cached flag so the next auth path re-reads the DB.
// Called when the disabled or super-admin state changes.
func (c *UserStateCache) Invalidate(ctx context.Context, userID string) {
	if c == nil || userID == "" {
		return
	}
	if c.rdb != nil {
		if err := c.rdb.Del(ctx, userStateKey(userID)).Err(); err != nil {
			slog.Warn("user_state_cache: invalidate failed; entry will expire on TTL", "error", err)
		}
		return
	}

	c.mu.Lock()
	delete(c.local, userID)
	c.mu.Unlock()
}
