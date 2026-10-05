package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// insertAdminUser writes a user row with the given admin state and
// registers cleanup. Emails are test-unique to stay safe on the shared
// database.
func insertAdminUser(t *testing.T, fx *testutil.Fixture, email string, isSuperAdmin bool, disabled bool) string {
	t.Helper()
	cols := testutil.Cols{
		"name":           email,
		"email":          email,
		"is_super_admin": isSuperAdmin,
	}
	if disabled {
		cols["disabled_at"] = "now()"
	}
	return fx.Insert(t, "user", cols)
}

func runSuperAdminGuard(t *testing.T, queries *db.Queries, userID string, impersonatorID string) *httptest.ResponseRecorder {
	t.Helper()
	mw := RequireSuperAdmin(queries)
	var called bool
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/admin/users", nil)
	req.Header.Set("X-User-ID", userID)
	if impersonatorID != "" {
		req.Header.Set("X-Impersonator-ID", impersonatorID)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if called && w.Code != http.StatusOK {
		t.Fatalf("next handler ran but wrote %d", w.Code)
	}
	return w
}

func TestRequireSuperAdmin(t *testing.T) {
	pool := openPool(t)
	queries := db.New(pool)
	fx := testutil.New(pool, "", "")

	admin := insertAdminUser(t, fx, "require-super-admin-admin@test.local", true, false)
	plain := insertAdminUser(t, fx, "require-super-admin-plain@test.local", false, false)
	disabledAdmin := insertAdminUser(t, fx, "require-super-admin-disabled@test.local", true, true)

	t.Run("super admin passes", func(t *testing.T) {
		if w := runSuperAdminGuard(t, queries, admin, ""); w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("non admin rejected", func(t *testing.T) {
		if w := runSuperAdminGuard(t, queries, plain, ""); w.Code != http.StatusForbidden {
			t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("disabled super admin rejected", func(t *testing.T) {
		if w := runSuperAdminGuard(t, queries, disabledAdmin, ""); w.Code != http.StatusForbidden {
			t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("unauthenticated rejected", func(t *testing.T) {
		if w := runSuperAdminGuard(t, queries, "", ""); w.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("missing user rejected", func(t *testing.T) {
		if w := runSuperAdminGuard(t, queries, "00000000-0000-0000-0000-000000000001", ""); w.Code != http.StatusForbidden {
			t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("malformed user id rejected", func(t *testing.T) {
		if w := runSuperAdminGuard(t, queries, "not-a-uuid", ""); w.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d: %s", w.Code, w.Body.String())
		}
	})

	// TestRequireSuperAdmin impersonation: an admin endpoint must never be
	// reachable through a shadow token — the request authenticates as the
	// impersonated user, and even a future relax of the target rules must
	// not open admin access through it.
	t.Run("impersonation session rejected outright", func(t *testing.T) {
		if w := runSuperAdminGuard(t, queries, admin, admin); w.Code != http.StatusForbidden {
			t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
		}
	})

}

// ---- dbDisabledLookup.IsDisabled (RUYI-429 fix D) ----
//
// These cases run against a stub DBTX instead of a live pool: the branch
// under test is error classification (no rows vs. real DB failure), which
// needs injected errors a real pool cannot produce on demand. pgx's own
// ErrNoRows plumbing for :one queries is covered end-to-end by the
// RequireSuperAdmin cases above, which hit the same GetUserAdminState
// statement through a real connection.

// stubAdminStateDB answers GetUserAdminState with a fixed row or error and
// counts how often the lookup actually reached the DB.
type stubAdminStateDB struct {
	db.DBTX
	state db.GetUserAdminStateRow
	err   error
	calls int
}

func (s *stubAdminStateDB) QueryRow(ctx context.Context, sql string, args ...interface{}) pgx.Row {
	s.calls++
	return &stubAdminStateRow{state: s.state, err: s.err}
}

// stubAdminStateRow mimics the field-by-field Scan the generated :one query
// performs (id, email, is_super_admin, disabled_at — in that order).
type stubAdminStateRow struct {
	pgx.Row
	state db.GetUserAdminStateRow
	err   error
}

func (r *stubAdminStateRow) Scan(dest ...interface{}) error {
	if r.err != nil {
		return r.err
	}
	*dest[0].(*pgtype.UUID) = r.state.ID
	*dest[1].(*string) = r.state.Email
	*dest[2].(*bool) = r.state.IsSuperAdmin
	*dest[3].(*pgtype.Timestamptz) = r.state.DisabledAt
	return nil
}

func newTestDisabledLookup(stub *stubAdminStateDB) auth.DisabledLookup {
	// nil Redis client → the cache degrades to its in-process map, keeping
	// the hit/miss contract under test without a Redis dependency.
	return NewDisabledLookup(db.New(stub), auth.NewUserStateCache(nil))
}

func TestDBDisabledLookup_NoRowsRejectedOnMissAndHit(t *testing.T) {
	stub := &stubAdminStateDB{err: pgx.ErrNoRows}
	lookup := newTestDisabledLookup(stub)
	userID := "9f1c3a2e-0000-4000-8000-000000000001"

	// Cache miss: the token names a user this database has never heard of.
	// RUYI-429: an unknown identity must be rejected, not waved through —
	// a token minted against a sibling database sharing the JWT_SECRET used
	// to authenticate as a ghost user here.
	if !lookup.IsDisabled(context.Background(), userID) {
		t.Fatal("no rows (cache miss): expected rejection, got fail-open")
	}

	// Cache hit: the rejection is cached like any other state, so a second
	// lookup behaves identically without consulting the DB again.
	if !lookup.IsDisabled(context.Background(), userID) {
		t.Fatal("no rows (cache hit): expected identical rejection, got fail-open")
	}
	if stub.calls != 1 {
		t.Fatalf("expected 1 DB round-trip (second lookup served from cache), got %d", stub.calls)
	}
}

func TestDBDisabledLookup_DBErrorStillFailsOpen(t *testing.T) {
	stub := &stubAdminStateDB{err: errors.New("connection refused")}
	lookup := newTestDisabledLookup(stub)

	// A dead dependency must not take down auth — the RUYI-47 fail-open
	// contract is deliberately kept for real errors; only no-rows tightens.
	if lookup.IsDisabled(context.Background(), "9f1c3a2e-0000-4000-8000-000000000002") {
		t.Fatal("real DB error: expected fail-open, got rejection")
	}
	// The failure is not cached: the next lookup must retry the DB.
	if lookup.IsDisabled(context.Background(), "9f1c3a2e-0000-4000-8000-000000000002") {
		t.Fatal("real DB error, second lookup: expected fail-open, got rejection")
	}
	if stub.calls != 2 {
		t.Fatalf("expected DB consulted on every failed lookup, got %d calls", stub.calls)
	}
}

func TestDBDisabledLookup_EnabledAndDisabledRowsCached(t *testing.T) {
	id, err := util.ParseUUID("9f1c3a2e-0000-4000-8000-000000000003")
	if err != nil {
		t.Fatalf("seed uuid: %v", err)
	}

	t.Run("enabled row allowed and cached", func(t *testing.T) {
		stub := &stubAdminStateDB{state: db.GetUserAdminStateRow{ID: id, Email: "u@test.local"}}
		lookup := newTestDisabledLookup(stub)
		uid := id.String()
		if lookup.IsDisabled(context.Background(), uid) {
			t.Fatal("enabled row: expected allowed")
		}
		if lookup.IsDisabled(context.Background(), uid) {
			t.Fatal("enabled row (cached): expected allowed")
		}
		if stub.calls != 1 {
			t.Fatalf("expected 1 DB round-trip, got %d", stub.calls)
		}
	})

	t.Run("disabled row rejected and cached", func(t *testing.T) {
		stub := &stubAdminStateDB{state: db.GetUserAdminStateRow{
			ID:         id,
			Email:      "u@test.local",
			DisabledAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
		}}
		lookup := newTestDisabledLookup(stub)
		uid := id.String()
		if !lookup.IsDisabled(context.Background(), uid) {
			t.Fatal("disabled row: expected rejected")
		}
		if !lookup.IsDisabled(context.Background(), uid) {
			t.Fatal("disabled row (cached): expected rejected")
		}
		if stub.calls != 1 {
			t.Fatalf("expected 1 DB round-trip, got %d", stub.calls)
		}
	})
}
