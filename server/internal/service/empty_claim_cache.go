package service

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// emptyClaimCacheKey holds a "no queued task" verdict tagged with the
// per-runtime version it was observed under. emptyClaimVersionKey is
// the per-runtime monotonic counter that any enqueue path bumps. The
// verdict is trusted only when its value equals the current version,
// which closes the race where a slow claim writes an empty verdict
// AFTER an enqueue has already invalidated it:
//
//	T1 claim:   v0 := GET version
//	            SELECT ... -> empty
//	            (slow, e.g. GC pause)
//	T2 enqueue: INSERT row
//	            INCR version  (-> v1)
//	            wakeup
//	T1 claim:   SET empty = v0
//	T3 claim:   v1' := GET version (== v1)
//	            GET empty (== v0) -> v0 != v1, treat as miss -> SELECT
//
// Without the version tag T3 would have hit the stale empty key and
// the just-queued task would sit idle until the empty key's TTL
// expired. With it, the only window left is one extra DB SELECT per
// runtime per concurrent enqueue, never a stalled task.
const (
	emptyClaimCachePrefix   = "mul:claim:runtime:empty:"
	emptyClaimVersionPrefix = "mul:claim:runtime:version:"
	batchClaimCursorPrefix  = "mul:claim:batch:cursor:"
)

// EmptyClaimCacheTTL bounds how long a cached "no queued task" verdict
// stays believable. Enqueue invalidates the verdict by bumping the
// per-runtime version before waking the daemon, so a longer TTL keeps
// the steady-state idle poll path off Postgres. The TTL remains the
// safety net for a missed invalidation, e.g. a transient Redis failure
// during Bump.
const EmptyClaimCacheTTL = 3 * time.Minute

// batchClaimCursorTTL bounds abandoned fairness state after a runtime set is
// removed. Version keys intentionally do not expire: their monotonic counter is
// the ABA guard for empty verdicts, so expiring and recreating a key could make
// an old version look current again.
const batchClaimCursorTTL = 24 * time.Hour

// emptyClaimRedisTimeout caps every Redis call from this cache. Enqueue
// paths use a background context so the cache outlives the request,
// but a wedged Redis must not stall enqueue indefinitely — bound the
// blast radius and degrade to "no cache" instead.
const emptyClaimRedisTimeout = 250 * time.Millisecond

var currentClaimVersionScript = redis.NewScript(`
local version = redis.call("GET", KEYS[1])
if not version then
  -- A missing version is an invalidation boundary. Remove any old verdict
  -- before recreating the counter so an evicted/deleted version can never
  -- make a surviving empty key look current again.
  redis.call("DEL", KEYS[2])
  redis.call("SET", KEYS[1], "0", "NX")
  version = redis.call("GET", KEYS[1])
end
return version
`)

var isEmptyClaimScript = redis.NewScript(`
local version = redis.call("GET", KEYS[2])
if not version then
  return 0
end
local empty = redis.call("GET", KEYS[1])
if empty ~= version then
  return 0
end
return 1
`)

var markEmptyClaimScript = redis.NewScript(`
local version = redis.call("GET", KEYS[2])
if not version or version ~= ARGV[1] then
  return 0
end
redis.call("SET", KEYS[1], ARGV[1], "EX", ARGV[2])
return 1
`)

var bumpClaimVersionScript = redis.NewScript(`
local version = redis.call("INCR", KEYS[1])
return version
`)

// EmptyClaimCache caches "this runtime currently has no queued task"
// so the daemon's poll-based claim path can short-circuit before
// hitting Postgres. Only the negative result is cached; positive
// results always re-check the DB so concurrent claimers race fairly
// in `ClaimAgentTask`'s `FOR UPDATE SKIP LOCKED`.
//
// The cache is invalidated synchronously on every enqueue (see
// TaskService.notifyTaskAvailable). A nil *EmptyClaimCache is safe to
// use — every method becomes a no-op or reports a cache miss, so
// single-node dev / tests with no REDIS_URL degrade cleanly to direct
// DB lookups.
type EmptyClaimCache struct {
	rdb *redis.Client
}

// NewEmptyClaimCache returns a cache backed by rdb. Pass nil to
// disable caching; the returned *EmptyClaimCache is safe to call but
// never hits Redis.
func NewEmptyClaimCache(rdb *redis.Client) *EmptyClaimCache {
	if rdb == nil {
		return nil
	}
	return &EmptyClaimCache{rdb: rdb}
}

func emptyClaimKey(runtimeID string) string        { return emptyClaimCachePrefix + runtimeID }
func emptyClaimVersion(runtimeID string) string    { return emptyClaimVersionPrefix + runtimeID }
func batchClaimCursorKey(runtimeSet string) string { return batchClaimCursorPrefix + runtimeSet }

func (c *EmptyClaimCache) bounded(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, emptyClaimRedisTimeout)
}

// CurrentVersion returns the runtime's current invalidation version.
// Callers MUST read this BEFORE the DB SELECT they are about to cache, then
// pass it back to MarkEmpty so a concurrent Bump invalidates the would-be
// cache write. The Redis script initializes a fresh version key atomically;
// version keys never expire, which prevents an old version from becoming valid
// again after an expiry-and-recreate ABA sequence.
func (c *EmptyClaimCache) CurrentVersion(ctx context.Context, runtimeID string) int64 {
	if c == nil || runtimeID == "" {
		return 0
	}
	bctx, cancel := c.bounded(ctx)
	defer cancel()
	version, err := currentClaimVersionScript.Run(
		bctx,
		c.rdb,
		[]string{emptyClaimVersion(runtimeID), emptyClaimKey(runtimeID)},
	).Text()
	if err != nil {
		if !errors.Is(err, redis.Nil) {
			slog.Warn("empty_claim_cache: version get failed; falling back to DB", "error", err)
		}
		return 0
	}
	value, err := strconv.ParseInt(version, 10, 64)
	if err != nil {
		slog.Warn("empty_claim_cache: invalid version value; falling back to DB", "value", version, "error", err)
		return 0
	}
	return value
}

// IsEmpty returns true only when an empty verdict and its version are read
// atomically and the version key still exists. Missing versions are always a
// cache miss; they must never be interpreted as version zero.
func (c *EmptyClaimCache) IsEmpty(ctx context.Context, runtimeID string) bool {
	if c == nil || runtimeID == "" {
		return false
	}
	bctx, cancel := c.bounded(ctx)
	defer cancel()
	matched, err := isEmptyClaimScript.Run(
		bctx,
		c.rdb,
		[]string{emptyClaimKey(runtimeID), emptyClaimVersion(runtimeID)},
	).Int()
	if err != nil {
		slog.Warn("empty_claim_cache: version check failed; falling back to DB", "error", err)
		return false
	}
	return matched == 1
}

// MarkEmpty stores the empty verdict tagged with observedVersion. The
// verdict is later trusted only if observedVersion still equals the
// current version (see IsEmpty). Pass the value returned by
// CurrentVersion BEFORE the SELECT that confirmed the runtime was
// empty; a concurrent Bump between the two will make the next reader
// reject this entry, forcing a fresh DB check.
//
// Errors are logged and swallowed — a cache write failure is not a
// request failure.
func (c *EmptyClaimCache) MarkEmpty(ctx context.Context, runtimeID string, observedVersion int64) {
	if c == nil || runtimeID == "" {
		return
	}
	bctx, cancel := c.bounded(ctx)
	defer cancel()
	matched, err := markEmptyClaimScript.Run(
		bctx,
		c.rdb,
		[]string{emptyClaimKey(runtimeID), emptyClaimVersion(runtimeID)},
		strconv.FormatInt(observedVersion, 10),
		int64(EmptyClaimCacheTTL/time.Second),
	).Int()
	if err != nil {
		slog.Warn("empty_claim_cache: set failed", "error", err)
		return
	}
	if matched != 1 {
		// A concurrent Bump changed the version after the SELECT. Do not
		// install an empty verdict for that stale observation.
		slog.Debug("empty_claim_cache: stale empty verdict rejected", "runtime_id", runtimeID, "version", observedVersion)
	}
}

// Bump increments the runtime's invalidation version. Called from
// every enqueue path BEFORE the daemon WS wakeup so any verdict
// written under the previous version is rejected on the next read,
// without needing a separate DEL on the empty key.
//
// Errors are logged and swallowed — a Redis hiccup must not stop a
// legitimate enqueue. The empty key still expires on its own TTL so
// the worst-case stall is bounded.
func (c *EmptyClaimCache) Bump(ctx context.Context, runtimeID string) {
	if c == nil || runtimeID == "" {
		return
	}
	bctx, cancel := c.bounded(ctx)
	defer cancel()
	if _, err := bumpClaimVersionScript.Run(
		bctx,
		c.rdb,
		[]string{emptyClaimVersion(runtimeID)},
	).Int64(); err != nil {
		slog.Warn("empty_claim_cache: bump failed; entry will expire on TTL", "error", err)
	}
}

// LastBatchClaimAgent reads the stable Agent identity served most recently for
// this runtime set. The boolean is true when Redis was reachable, including a
// missing key; callers can then distinguish a fresh set from a Redis failure.
func (c *EmptyClaimCache) LastBatchClaimAgent(ctx context.Context, runtimeSet string) (agentID string, available bool) {
	if c == nil || runtimeSet == "" {
		return "", false
	}
	bctx, cancel := c.bounded(ctx)
	defer cancel()
	value, err := c.rdb.Get(bctx, batchClaimCursorKey(runtimeSet)).Result()
	if errors.Is(err, redis.Nil) {
		return "", true
	}
	if err != nil {
		slog.Warn("empty_claim_cache: batch fairness state read failed; falling back locally", "error", err)
		return "", false
	}
	return value, true
}

// RememberBatchClaimAgent stores the stable Agent identity served most recently
// for this runtime set. A false result tells the caller to use local fallback.
func (c *EmptyClaimCache) RememberBatchClaimAgent(ctx context.Context, runtimeSet, agentID string) bool {
	if c == nil || runtimeSet == "" || agentID == "" {
		return false
	}
	bctx, cancel := c.bounded(ctx)
	defer cancel()
	if err := c.rdb.Set(bctx, batchClaimCursorKey(runtimeSet), agentID, batchClaimCursorTTL).Err(); err != nil {
		slog.Warn("empty_claim_cache: batch fairness state write failed; falling back locally", "error", err)
		return false
	}
	return true
}
