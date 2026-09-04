package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
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
