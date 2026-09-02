package middleware

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// dbDisabledLookup implements auth.DisabledLookup against the persisted
// user.disabled_at column, fronted by the shared UserStateCache. This is
// what replaced the hardcoded temporary ban list: disable becomes a row
// write, and every auth path (JWT, PAT, task token, cloud PAT, WebSocket)
// sees it within at most one UserStateCacheTTL window — immediately on the
// next request when the acting node invalidates the entry itself.
//
// Failure semantics deliberately match the other auth caches: a Redis or DB
// error on a cache miss fails OPEN (log + allow), because a dead dependency
// must not take down every authenticated request. The authoritative reads
// happen on the login path, which has no cache in front of it.
type dbDisabledLookup struct {
	queries *db.Queries
	cache   *auth.UserStateCache
}

// NewDisabledLookup returns the production auth.DisabledLookup. Both
// dependencies come from the router: queries is the shared sqlc handle and
// cache the shared UserStateCache (nil-safe, degrades to per-request DB
// reads when Redis is not configured).
func NewDisabledLookup(queries *db.Queries, cache *auth.UserStateCache) auth.DisabledLookup {
	return &dbDisabledLookup{queries: queries, cache: cache}
}

func (l *dbDisabledLookup) IsDisabled(ctx context.Context, userID string) bool {
	if userID == "" {
		return false
	}
	if disabled, ok := l.cache.Get(ctx, userID); ok {
		return disabled
	}

	id, err := util.ParseUUID(userID)
	if err != nil {
		// A sub claim that is not a UUID can never map to a user row;
		// the token is invalid anyway and the JWT branch will have
		// rejected it before here in production shape.
		return false
	}
	state, err := l.queries.GetUserAdminState(ctx, id)
	if err != nil {
		slog.Warn("auth: user state lookup failed; failing open", "error", err)
		return false
	}

	disabled := state.DisabledAt.Valid
	l.cache.Set(ctx, userID, disabled)
	return disabled
}

// rejectDisabledUser writes the 403 and returns true when the account the
// request authenticates as is disabled. authPath labels the credential
// branch in the log line, mirroring the old hardcoded-list check.
func rejectDisabledUser(w http.ResponseWriter, r *http.Request, disabled auth.DisabledLookup, userID, authPath string) bool {
	if disabled == nil || !disabled.IsDisabled(r.Context(), userID) {
		return false
	}
	slog.Warn(
		"auth: disabled user rejected",
		"path", r.URL.Path,
		"user_id", userID,
		"auth_path", authPath,
	)
	writeError(w, http.StatusForbidden, auth.UserDisabledError)
	return true
}

// RequireSuperAdmin gates the instance-level /api/admin routes. It reads the
// X-User-ID the Auth middleware stamped, and re-checks the super-admin flag
// on every request straight from the DB — role authorization never trusts a
// cache, mirroring RequireWorkspaceRole.
//
// Impersonation sessions are rejected outright: a shadow token authenticates
// as the impersonated user, who is by construction not a super admin, and
// admin access must never travel through an impersonated identity even if a
// future change relaxes the impersonation target rules.
func RequireSuperAdmin(queries *db.Queries) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-Impersonator-ID") != "" {
				writeError(w, http.StatusForbidden, "impersonation session cannot access admin endpoints")
				return
			}
			userID := r.Header.Get("X-User-ID")
			if userID == "" {
				writeError(w, http.StatusUnauthorized, "user not authenticated")
				return
			}
			id, err := util.ParseUUID(userID)
			if err != nil {
				writeError(w, http.StatusUnauthorized, "user not authenticated")
				return
			}
			state, err := queries.GetUserAdminState(r.Context(), id)
			if err != nil {
				writeError(w, http.StatusForbidden, "super admin access required")
				return
			}
			if !state.IsSuperAdmin || state.DisabledAt.Valid {
				writeError(w, http.StatusForbidden, "super admin access required")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
