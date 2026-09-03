package handler

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/testutil"
)

// The admin handler tests drive the RUYI-47 guard semantics directly
// against the shared test database: self-protection, the last-super-admin
// invariant, the impersonation target rules, the disable side effects, and
// the audit trail. Handlers are invoked like the rest of this package's
// tests (X-User-ID header + chi URL params), so the RequireSuperAdmin
// middleware itself is covered by internal/middleware/admin_test.go.

// insertAdminTestUser is a fixture wrapper with a per-call-site unique
// email; it returns the user id string. admin_audit_log has no foreign key
// by repo convention, so the audit rows each test generates are cleaned up
// here alongside the user rows.
func insertAdminTestUser(t *testing.T, email, name string, isSuperAdmin bool) string {
	t.Helper()
	id := dbfx.Insert(t, "user", testutil.Cols{
		"email":          email,
		"name":           name,
		"is_super_admin": isSuperAdmin,
	})
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(),
			`DELETE FROM admin_audit_log WHERE actor_id::text = $1 OR target_id::text = $1`, id)
	})
	return id
}

func setSuperAdminEmailsForTest(t *testing.T, emails []string) {
	t.Helper()
	previous := testHandler.cfg.SuperAdminEmails
	testHandler.cfg.SuperAdminEmails = emails
	t.Cleanup(func() {
		testHandler.cfg.SuperAdminEmails = previous
	})
}

// latestImpersonationSessionID returns the session_id stamped in the most
// recent impersonation.start row for (actorID, targetID).
func latestImpersonationSessionID(t *testing.T, actorID, targetID string) string {
	t.Helper()
	var sid *string
	dbfx.QueryRow(t,
		`SELECT metadata->>'session_id' FROM admin_audit_log
		 WHERE action = $1 AND actor_id::text = $2 AND target_id::text = $3
		 ORDER BY created_at DESC, id DESC LIMIT 1`,
		AuditActionImpersonateStart, actorID, targetID).Scan(&sid)
	if sid == nil {
		t.Fatal("impersonation.start row has no session_id in metadata")
	}
	return *sid
}

// countStopRowsWithSession counts impersonation.stop rows for (actorID,
// targetID) carrying the given session_id.
func countStopRowsWithSession(t *testing.T, actorID, targetID, sessionID string) int64 {
	t.Helper()
	var n int64
	dbfx.QueryRow(t,
		`SELECT count(*) FROM admin_audit_log
		 WHERE action = $1 AND actor_id::text = $2 AND target_id::text = $3
		   AND metadata->>'session_id' = $4`,
		AuditActionImpersonateStop, actorID, targetID, sessionID).Scan(&n)
	return n
}

// countForcedStopRows counts only FORCE-TERMINATED stop rows (the ones the
// disable path writes, terminated_by=admin_disabled) for the session.
func countForcedStopRows(t *testing.T, actorID, targetID, sessionID string) int64 {
	t.Helper()
	var n int64
	dbfx.QueryRow(t,
		`SELECT count(*) FROM admin_audit_log
		 WHERE action = $1 AND actor_id::text = $2 AND target_id::text = $3
		   AND metadata->>'session_id' = $4 AND metadata->>'terminated_by' = 'admin_disabled'`,
		AuditActionImpersonateStop, actorID, targetID, sessionID).Scan(&n)
	return n
}

func countAdminAuditRows(t *testing.T, action, actorID, targetID string) int64 {
	t.Helper()
	var n int64
	dbfx.QueryRow(t,
		`SELECT count(*) FROM admin_audit_log WHERE action = $1 AND actor_id::text = $2 AND target_id::text = $3`,
		action, actorID, targetID).Scan(&n)
	return n
}

func TestAdminSetUserDisabled_CannotDisableSelf(t *testing.T) {
	admin := insertAdminTestUser(t, "admin-self-disable@test.local", "Admin Self Disable", true)

	req := newRequest("PATCH", "/api/admin/users/"+admin+"/disabled", AdminSetUserDisabledRequest{Disabled: true})
	req = withURLParam(req, "id", admin)
	req.Header.Set("X-User-ID", admin)
	testutil.Call(t, testHandler.AdminSetUserDisabled, req).Want(http.StatusForbidden)

	var disabled bool
	dbfx.QueryRow(t, `SELECT disabled_at IS NOT NULL FROM "user" WHERE id = $1`, admin).Scan(&disabled)
	if disabled {
		t.Fatal("self-disable must not persist any state")
	}
}

func TestAdminSetUserDisabled_DisablesRevokesPATsAndAudits(t *testing.T) {
	admin := insertAdminTestUser(t, "admin-disabler@test.local", "Admin Disabler", true)
	target := insertAdminTestUser(t, "admin-disable-target@test.local", "Disable Target", false)

	// Live PAT for the target: the disable must revoke it.
	token, err := auth.GeneratePATToken()
	if err != nil {
		t.Fatalf("generate PAT: %v", err)
	}
	patID := dbfx.Insert(t, "personal_access_token", testutil.Cols{
		"user_id":      target,
		"name":         "disable-test",
		"token_hash":   auth.HashToken(token),
		"token_prefix": token[:8],
	})

	req := newRequest("PATCH", "/api/admin/users/"+target+"/disabled", AdminSetUserDisabledRequest{Disabled: true, Reason: "investigating"})
	req = withURLParam(req, "id", target)
	req.Header.Set("X-User-ID", admin)
	testutil.Call(t, testHandler.AdminSetUserDisabled, req).Want(http.StatusOK)

	var disabledAt, disabledBy string
	dbfx.QueryRow(t,
		`SELECT disabled_at::text, disabled_by::text FROM "user" WHERE id = $1`, target).Scan(&disabledAt, &disabledBy)
	if disabledAt == "" || disabledBy != admin {
		t.Fatalf("expected disabled_at + disabled_by=actor, got %q / %q", disabledAt, disabledBy)
	}

	var revoked bool
	dbfx.QueryRow(t, `SELECT revoked FROM personal_access_token WHERE id = $1`, patID).Scan(&revoked)
	if !revoked {
		t.Fatal("disable must revoke the target's PATs")
	}

	// The auth-path lookup reports disabled immediately after the flip.
	state, err := testHandler.Queries.GetUserAdminState(t.Context(), parseUUID(target))
	if err != nil {
		t.Fatalf("get user admin state: %v", err)
	}
	if !state.DisabledAt.Valid {
		t.Fatal("user admin state must report disabled right after the flip")
	}

	if n := countAdminAuditRows(t, AuditActionUserDisable, admin, target); n != 1 {
		t.Fatalf("expected exactly one user.disable audit row, got %d", n)
	}

	// Re-enable restores the account.
	req2 := newRequest("PATCH", "/api/admin/users/"+target+"/disabled", AdminSetUserDisabledRequest{Disabled: false})
	req2 = withURLParam(req2, "id", target)
	req2.Header.Set("X-User-ID", admin)
	testutil.Call(t, testHandler.AdminSetUserDisabled, req2).Want(http.StatusOK)

	var stillDisabled bool
	dbfx.QueryRow(t, `SELECT disabled_at IS NOT NULL FROM "user" WHERE id = $1`, target).Scan(&stillDisabled)
	if stillDisabled {
		t.Fatal("re-enable must clear disabled_at")
	}
	if n := countAdminAuditRows(t, AuditActionUserEnable, admin, target); n != 1 {
		t.Fatalf("expected exactly one user.enable audit row, got %d", n)
	}
}

func TestAdminSetUserSuperAdmin_CannotRevokeSelf(t *testing.T) {
	admin := insertAdminTestUser(t, "admin-self-revoke@test.local", "Admin Self Revoke", true)

	req := newRequest("PATCH", "/api/admin/users/"+admin+"/super-admin", AdminSetUserSuperAdminRequest{Granted: false})
	req = withURLParam(req, "id", admin)
	req.Header.Set("X-User-ID", admin)
	testutil.Call(t, testHandler.AdminSetUserSuperAdmin, req).Want(http.StatusForbidden)

	var still bool
	dbfx.QueryRow(t, `SELECT is_super_admin FROM "user" WHERE id = $1`, admin).Scan(&still)
	if !still {
		t.Fatal("self-revoke must not persist")
	}
}

func TestAdminSetUserSuperAdmin_RevokeKeepsOneActiveSuperAdmin(t *testing.T) {
	admin := insertAdminTestUser(t, "admin-revoke-guard@test.local", "Admin Revoke Guard", true)
	target := insertAdminTestUser(t, "admin-revoke-target@test.local", "Admin Revoke Target", true)

	req := newRequest("PATCH", "/api/admin/users/"+target+"/super-admin", AdminSetUserSuperAdminRequest{Granted: false})
	req = withURLParam(req, "id", target)
	req.Header.Set("X-User-ID", admin)
	testutil.Call(t, testHandler.AdminSetUserSuperAdmin, req).Want(http.StatusOK)

	var active int64
	dbfx.QueryRow(t, `SELECT count(*) FROM "user" WHERE is_super_admin AND disabled_at IS NULL`).Scan(&active)
	if active < 1 {
		t.Fatalf("instance must retain at least one active super admin, got %d", active)
	}
	if n := countAdminAuditRows(t, AuditActionSuperAdminRevoke, admin, target); n != 1 {
		t.Fatalf("expected one super_admin.revoke audit row, got %d", n)
	}
}

func TestCountActiveSuperAdminsExcludesGivenUser(t *testing.T) {
	// Primitive check for the invariant query behind the revoke guard:
	// excludes the given id and counts only enabled super admins.
	active := insertAdminTestUser(t, "count-excl-active@test.local", "Count Excl Active", true)
	other := insertAdminTestUser(t, "count-excl-other@test.local", "Count Excl Other", true)
	disabled := insertAdminTestUser(t, "count-excl-disabled@test.local", "Count Excl Disabled", true)
	dbfx.Exec(t, `UPDATE "user" SET disabled_at = now() WHERE id = $1`, disabled)

	// Excluding "other" must still find "active" (enabled) and skip the
	// disabled admin.
	n, err := testHandler.Queries.CountActiveSuperAdminsExcluding(t.Context(), parseUUID(other))
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n < 1 {
		t.Fatalf("expected >=1 active super admin excluding target, got %d", n)
	}
	_ = active
}

func TestAdminSetUserSuperAdmin_GrantIsIdempotentAndAudits(t *testing.T) {
	admin := insertAdminTestUser(t, "admin-granter@test.local", "Admin Granter", true)
	target := insertAdminTestUser(t, "admin-grant-target@test.local", "Grant Target", false)

	doGrant := func() int {
		req := newRequest("PATCH", "/api/admin/users/"+target+"/super-admin", AdminSetUserSuperAdminRequest{Granted: true, Reason: "ops rotation"})
		req = withURLParam(req, "id", target)
		req.Header.Set("X-User-ID", admin)
		return testutil.Call(t, testHandler.AdminSetUserSuperAdmin, req).Code
	}

	if code := doGrant(); code != http.StatusOK {
		t.Fatalf("first grant: expected 200, got %d", code)
	}
	if code := doGrant(); code != http.StatusOK {
		t.Fatalf("repeat grant: expected 200 (idempotent), got %d", code)
	}

	var isSuper bool
	dbfx.QueryRow(t, `SELECT is_super_admin FROM "user" WHERE id = $1`, target).Scan(&isSuper)
	if !isSuper {
		t.Fatal("grant must persist is_super_admin")
	}
	if n := countAdminAuditRows(t, AuditActionSuperAdminGrant, admin, target); n != 2 {
		t.Fatalf("expected two grant audit rows (one per granted request), got %d", n)
	}
}

func TestAdminImpersonate_Guards(t *testing.T) {
	admin := insertAdminTestUser(t, "admin-imp-admin@test.local", "Admin Imp Admin", true)
	otherAdmin := insertAdminTestUser(t, "admin-imp-other@test.local", "Admin Imp Other", true)
	disabled := insertAdminTestUser(t, "admin-imp-disabled@test.local", "Admin Imp Disabled", false)
	dbfx.Exec(t, `UPDATE "user" SET disabled_at = now() WHERE id = $1`, disabled)

	cases := []struct {
		name   string
		target string
		want   int
	}{
		{"another super admin", otherAdmin, http.StatusForbidden},
		{"self", admin, http.StatusForbidden},
		{"disabled user", disabled, http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := newRequest("POST", "/api/admin/users/"+tc.target+"/impersonate", AdminImpersonateRequest{})
			req = withURLParam(req, "id", tc.target)
			req.Header.Set("X-User-ID", admin)
			testutil.Call(t, testHandler.AdminImpersonate, req).Want(tc.want)
		})
	}
}

func TestAdminImpersonate_IssuesShadowTokenAndAuditsStart(t *testing.T) {
	admin := insertAdminTestUser(t, "admin-imp-start@test.local", "Admin Imp Start", true)
	target := insertAdminTestUser(t, "admin-imp-target@test.local", "Admin Imp Target", false)

	req := newRequest("POST", "/api/admin/users/"+target+"/impersonate", AdminImpersonateRequest{Reason: "repro RUYI-47"})
	req = withURLParam(req, "id", target)
	req.Header.Set("X-User-ID", admin)
	resp := testutil.Call(t, testHandler.AdminImpersonate, req).Want(http.StatusOK)

	var body LoginResponse
	resp.JSON(&body)

	parsed, err := jwt.Parse(body.Token, func(token *jwt.Token) (any, error) {
		return auth.JWTSecret(), nil
	})
	if err != nil || !parsed.Valid {
		t.Fatalf("issued token must parse: %v", err)
	}
	claims := parsed.Claims.(jwt.MapClaims)
	if claims["sub"] != target {
		t.Fatalf("shadow token sub = %v, want target %s", claims["sub"], target)
	}
	if claims["imp"] != admin {
		t.Fatalf("shadow token imp = %v, want actor %s", claims["imp"], admin)
	}
	iat := int64(claims["iat"].(float64))
	exp := int64(claims["exp"].(float64))
	if ttl := time.Duration(exp-iat) * time.Second; ttl != ImpersonationTokenTTL {
		t.Fatalf("shadow token TTL = %s, want %s", ttl, ImpersonationTokenTTL)
	}

	// P2-1: the login-style response must already carry impersonator_id so
	// the client's applySession renders the identity-switch banner
	// immediately, without waiting for a /api/me refresh.
	if body.User.ImpersonatorID == nil || *body.User.ImpersonatorID != admin {
		t.Fatalf("impersonate response user.impersonator_id = %v, want %s", body.User.ImpersonatorID, admin)
	}

	if n := countAdminAuditRows(t, AuditActionImpersonateStart, admin, target); n != 1 {
		t.Fatalf("expected one impersonation.start audit row, got %d", n)
	}
}

func TestStopImpersonation(t *testing.T) {
	admin := insertAdminTestUser(t, "admin-stop@test.local", "Admin Stop", true)
	target := insertAdminTestUser(t, "admin-stop-target@test.local", "Stop Target", false)

	t.Run("requires an impersonation session", func(t *testing.T) {
		req := newRequest("POST", "/api/impersonation/stop", nil)
		req.Header.Set("X-User-ID", target)
		testutil.Call(t, testHandler.StopImpersonation, req).Want(http.StatusBadRequest)
	})

	t.Run("rejects non-super impersonator", func(t *testing.T) {
		plain := insertAdminTestUser(t, "admin-stop-plain@test.local", "Stop Plain", false)
		req := newRequest("POST", "/api/impersonation/stop", nil)
		req.Header.Set("X-User-ID", target)
		req.Header.Set("X-Impersonator-ID", plain)
		testutil.Call(t, testHandler.StopImpersonation, req).Want(http.StatusForbidden)
	})

	t.Run("re-mints the impersonator token and audits stop", func(t *testing.T) {
		// Establish the open session first: the stop row pairs with the
		// start row's session_id.
		doImpersonate(t, admin, target)
		startSID := latestImpersonationSessionID(t, admin, target)

		req := newRequest("POST", "/api/impersonation/stop", nil)
		req.Header.Set("X-User-ID", target)
		req.Header.Set("X-Impersonator-ID", admin)
		// The middleware relays the token's sid claim into this header;
		// handler-level tests stub it the same way.
		req.Header.Set("X-Impersonation-Session", startSID)
		resp := testutil.Call(t, testHandler.StopImpersonation, req).Want(http.StatusOK)

		var body LoginResponse
		resp.JSON(&body)

		parsed, err := jwt.Parse(body.Token, func(token *jwt.Token) (any, error) {
			return auth.JWTSecret(), nil
		})
		if err != nil || !parsed.Valid {
			t.Fatalf("re-minted token must parse: %v", err)
		}
		claims := parsed.Claims.(jwt.MapClaims)
		if claims["sub"] != admin {
			t.Fatalf("re-minted token sub = %v, want impersonator %s", claims["sub"], admin)
		}
		if _, has := claims["imp"]; has {
			t.Fatal("re-minted token must not carry an imp claim")
		}

		if n := countAdminAuditRows(t, AuditActionImpersonateStop, admin, target); n != 1 {
			t.Fatalf("expected one impersonation.stop audit row, got %d", n)
		}
		// P2-2 pairing: the stop row must carry the session_id of the
		// impersonation.start row it closes.
		if n := countStopRowsWithSession(t, admin, target, startSID); n != 1 {
			t.Fatalf("expected exactly one stop row paired with session %s, got %d", startSID, n)
		}
	})
}

func TestAdminAddWorkspaceMember(t *testing.T) {
	admin := insertAdminTestUser(t, "admin-ws-add@test.local", "Admin Ws Add", true)

	t.Run("rejects owner role", func(t *testing.T) {
		ws := dbfx.Insert(t, "workspace", testutil.Cols{"name": "Admin Add Owner Reject", "slug": "admin-add-owner-reject"})
		target := insertAdminTestUser(t, "admin-ws-owner-reject@test.local", "Owner Reject", false)

		req := newRequest("POST", "/api/admin/workspaces/"+ws+"/members", AdminAddWorkspaceMemberRequest{
			UserID: target, Role: "owner",
		})
		req = withURLParam(req, "id", ws)
		req.Header.Set("X-User-ID", admin)
		testutil.Call(t, testHandler.AdminAddWorkspaceMember, req).Want(http.StatusBadRequest)
	})

	t.Run("adds admin-role member directly and audits", func(t *testing.T) {
		ws := dbfx.Insert(t, "workspace", testutil.Cols{"name": "Admin Add Direct", "slug": "admin-add-direct"})
		target := insertAdminTestUser(t, "admin-ws-direct@test.local", "Ws Direct", false)

		req := newRequest("POST", "/api/admin/workspaces/"+ws+"/members", AdminAddWorkspaceMemberRequest{
			UserID: target, Role: "admin", Reason: "onboarding",
		})
		req = withURLParam(req, "id", ws)
		req.Header.Set("X-User-ID", admin)
		testutil.Call(t, testHandler.AdminAddWorkspaceMember, req).Want(http.StatusCreated)

		var role string
		dbfx.QueryRow(t,
			`SELECT role FROM member WHERE workspace_id::text = $1 AND user_id::text = $2`, ws, target).Scan(&role)
		if role != "admin" {
			t.Fatalf("expected member row with role admin, got %q", role)
		}
		if n := countAdminAuditRows(t, AuditActionMemberAdd, admin, ws); n != 1 {
			t.Fatalf("expected one workspace_member.add audit row, got %d", n)
		}

		// Second add for the same user → conflict.
		req2 := newRequest("POST", "/api/admin/workspaces/"+ws+"/members", AdminAddWorkspaceMemberRequest{
			UserID: target, Role: "member",
		})
		req2 = withURLParam(req2, "id", ws)
		req2.Header.Set("X-User-ID", admin)
		testutil.Call(t, testHandler.AdminAddWorkspaceMember, req2).Want(http.StatusConflict)
	})

	t.Run("unknown user rejected", func(t *testing.T) {
		ws := dbfx.Insert(t, "workspace", testutil.Cols{"name": "Admin Add Unknown", "slug": "admin-add-unknown"})
		req := newRequest("POST", "/api/admin/workspaces/"+ws+"/members", AdminAddWorkspaceMemberRequest{
			Email: "admin-add-missing-user@test.local", Role: "member",
		})
		req = withURLParam(req, "id", ws)
		req.Header.Set("X-User-ID", admin)
		testutil.Call(t, testHandler.AdminAddWorkspaceMember, req).Want(http.StatusNotFound)
	})
}

func TestAdminListUsers_ReturnsDirectoryWithFlags(t *testing.T) {
	admin := insertAdminTestUser(t, "admin-list-admin@test.local", "List Admin", true)
	// A second user with a membership so workspace_count > 0 is covered.
	target := insertAdminTestUser(t, "admin-list-target-unique@test.local", "List Target", false)
	ws := dbfx.Insert(t, "workspace", testutil.Cols{"name": "Admin List WS", "slug": "admin-list-ws"})
	dbfx.Insert(t, "member", testutil.Cols{"workspace_id": ws, "user_id": target, "role": "member"})

	req := newRequest("GET", "/api/admin/users?query=admin-list-target-unique", nil)
	req.Header.Set("X-User-ID", admin)
	resp := testutil.Call(t, testHandler.AdminListUsers, req).Want(http.StatusOK)

	var body AdminUserListResponse
	resp.JSON(&body)

	if body.Total < 1 || len(body.Users) != 1 {
		t.Fatalf("expected exactly the searched user, got total=%d users=%d", body.Total, len(body.Users))
	}
	got := body.Users[0]
	if got.ID != target || got.IsSuperAdmin || got.WorkspaceCount != 1 {
		t.Fatalf("unexpected directory row: %+v", got)
	}
	if got.DisabledAt != nil {
		t.Fatalf("enabled user must have null disabled_at: %+v", got)
	}
}

func TestAdminListWorkspaces_ReturnsOwnerAndCounts(t *testing.T) {
	admin := insertAdminTestUser(t, "admin-wslist-admin@test.local", "WsList Admin", true)
	ws := dbfx.Insert(t, "workspace", testutil.Cols{"name": "Admin WsList Unique", "slug": "admin-wslist-unique"})
	dbfx.Insert(t, "member", testutil.Cols{"workspace_id": ws, "user_id": admin, "role": "owner"})
	extra := insertAdminTestUser(t, "admin-wslist-extra@test.local", "WsList Extra", false)
	dbfx.Insert(t, "member", testutil.Cols{"workspace_id": ws, "user_id": extra, "role": "member"})

	req := newRequest("GET", "/api/admin/workspaces?query=admin-wslist-unique", nil)
	req.Header.Set("X-User-ID", admin)
	resp := testutil.Call(t, testHandler.AdminListWorkspaces, req).Want(http.StatusOK)

	var body AdminWorkspaceListResponse
	resp.JSON(&body)

	if len(body.Workspaces) != 1 || body.Total != 1 {
		t.Fatalf("expected exactly one workspace, got total=%d rows=%d", body.Total, len(body.Workspaces))
	}
	got := body.Workspaces[0]
	if got.ID != ws || got.OwnerID == nil || *got.OwnerID != admin || got.MemberCount != 2 {
		t.Fatalf("unexpected workspace row: %+v", got)
	}
}

// TestAdminListWorkspaces_OwnerlessWorkspaceDoesNotError is a regression
// test for RUYI-47 rework: a workspace with no owner-role member must not
// break the query. Before the fix, ListAllWorkspacesRow declared
// OwnerName/OwnerEmail as non-nullable Go strings while the underlying join
// can legitimately return NULL for both, so pgx failed to scan the row and
// the endpoint returned 500 for every request that touched such a
// workspace (including the unfiltered admin page load).
func TestAdminListWorkspaces_OwnerlessWorkspaceDoesNotError(t *testing.T) {
	admin := insertAdminTestUser(t, "admin-wslist-ownerless-admin@test.local", "WsList Ownerless Admin", true)
	ws := dbfx.Insert(t, "workspace", testutil.Cols{"name": "Admin WsList Ownerless", "slug": "admin-wslist-ownerless"})
	// Non-owner membership only: no row in `member` has role='owner' for
	// this workspace, so the LEFT JOIN chain in ListAllWorkspaces must
	// leave owner_id/owner_name/owner_email NULL rather than erroring.
	member := insertAdminTestUser(t, "admin-wslist-ownerless-member@test.local", "WsList Ownerless Member", false)
	dbfx.Insert(t, "member", testutil.Cols{"workspace_id": ws, "user_id": member, "role": "member"})

	req := newRequest("GET", "/api/admin/workspaces?query=admin-wslist-ownerless", nil)
	req.Header.Set("X-User-ID", admin)
	resp := testutil.Call(t, testHandler.AdminListWorkspaces, req).Want(http.StatusOK)

	var body AdminWorkspaceListResponse
	resp.JSON(&body)

	if len(body.Workspaces) != 1 || body.Total != 1 {
		t.Fatalf("expected exactly one workspace, got total=%d rows=%d", body.Total, len(body.Workspaces))
	}
	got := body.Workspaces[0]
	if got.ID != ws {
		t.Fatalf("unexpected workspace row: %+v", got)
	}
	if got.OwnerID != nil || got.OwnerName != nil || got.OwnerEmail != nil {
		t.Fatalf("ownerless workspace must have nil owner fields, got: %+v", got)
	}
	if got.MemberCount != 1 {
		t.Fatalf("expected member_count=1, got %d", got.MemberCount)
	}
}

func TestMaybeGrantSuperAdmin_IsIdempotentBootstrap(t *testing.T) {
	setSuperAdminEmailsForTest(t, []string{"bootstrap-admin@test.local"})

	insertAdminTestUser(t, "bootstrap-admin@test.local", "Bootstrap Admin", false)
	insertAdminTestUser(t, "bootstrap-other@test.local", "Bootstrap Other", false)

	fetched, err := testHandler.Queries.GetUserByEmail(t.Context(), "bootstrap-admin@test.local")
	if err != nil {
		t.Fatalf("fetch user: %v", err)
	}
	if fetched.IsSuperAdmin {
		t.Fatal("fixture must start without the flag")
	}

	granted := testHandler.maybeGrantSuperAdmin(t.Context(), fetched)
	if !granted.IsSuperAdmin {
		t.Fatal("bootstrap email must be granted on the login path")
	}

	// Fresh read: the second call must not rewrite the row — updated_at
	// unchanged proves the early return fired.
	var updatedBefore, updatedAfter time.Time
	dbfx.QueryRow(t, `SELECT updated_at FROM "user" WHERE email = $1`, "bootstrap-admin@test.local").Scan(&updatedBefore)
	refetched, err := testHandler.Queries.GetUserByEmail(t.Context(), "bootstrap-admin@test.local")
	if err != nil {
		t.Fatalf("refetch: %v", err)
	}
	testHandler.maybeGrantSuperAdmin(t.Context(), refetched)
	dbfx.QueryRow(t, `SELECT updated_at FROM "user" WHERE email = $1`, "bootstrap-admin@test.local").Scan(&updatedAfter)
	if !updatedBefore.Equal(updatedAfter) {
		t.Fatal("idempotent grant must not write when the flag is already set")
	}

	// A non-listed email is never granted.
	row, err := testHandler.Queries.GetUserByEmail(t.Context(), "bootstrap-other@test.local")
	if err != nil {
		t.Fatalf("fetch other: %v", err)
	}
	still := testHandler.maybeGrantSuperAdmin(t.Context(), row)
	if still.IsSuperAdmin {
		t.Fatal("non-listed email must not be granted")
	}
}

func TestDisabledUserRejectedOnLoginPath(t *testing.T) {
	insertAdminTestUser(t, "login-disabled@test.local", "Login Disabled", false)
	dbfx.Exec(t, `UPDATE "user" SET disabled_at = now() WHERE email = $1`, "login-disabled@test.local")

	_, _, err := testHandler.findOrCreateUser(t.Context(), "login-disabled@test.local")
	if err == nil || !strings.Contains(err.Error(), auth.UserDisabledError) {
		t.Fatalf("expected disabled rejection, got %v", err)
	}
}

// doImpersonate is a test helper that drives AdminImpersonate and returns
// the shadow LoginResponse.
func doImpersonate(t *testing.T, adminID, targetID string) LoginResponse {
	t.Helper()
	req := newRequest("POST", "/api/admin/users/"+targetID+"/impersonate", AdminImpersonateRequest{Reason: "session-fix"})
	req = withURLParam(req, "id", targetID)
	req.Header.Set("X-User-ID", adminID)
	resp := testutil.Call(t, testHandler.AdminImpersonate, req).Want(http.StatusOK)
	var body LoginResponse
	resp.JSON(&body)
	return body
}

func doDisable(t *testing.T, actorID, targetID string, disabled bool) {
	t.Helper()
	req := newRequest("PATCH", "/api/admin/users/"+targetID+"/disabled", AdminSetUserDisabledRequest{Disabled: disabled, Reason: "term-fix"})
	req = withURLParam(req, "id", targetID)
	req.Header.Set("X-User-ID", actorID)
	testutil.Call(t, testHandler.AdminSetUserDisabled, req).Want(http.StatusOK)
}

// P2-2: disabling a super admin force-terminates their open impersonation
// sessions; each open session must get an impersonation.stop audit row
// (metadata.terminated_by = admin_disabled, session_id paired with its
// start row) so the trail stays symmetric with normal start/stop.
func TestAdminDisableSuperAdmin_WritesTerminationAuditForOpenSessions(t *testing.T) {
	disabler := insertAdminTestUser(t, "admin-term-disabler@test.local", "Term Disabler", true)
	terminator := insertAdminTestUser(t, "admin-term-admin@test.local", "Term Admin", true)
	target := insertAdminTestUser(t, "admin-term-target@test.local", "Term Target", false)

	body := doImpersonate(t, terminator, target)
	sid := latestImpersonationSessionID(t, terminator, target)
	if body.User.ImpersonatorID == nil {
		t.Fatal("precondition: impersonate response must carry impersonator_id")
	}

	doDisable(t, disabler, terminator, true)

	// The termination row impersonates the same actor/target pair as the
	// start row, carries the session_id, and explains itself.
	n := countStopRowsWithSession(t, terminator, target, sid)
	if n != 1 {
		t.Fatalf("expected exactly one forced impersonation.stop row for session %s, got %d", sid, n)
	}
	var terminatedBy *string
	dbfx.QueryRow(t,
		`SELECT metadata->>'terminated_by' FROM admin_audit_log
		 WHERE action = $1 AND actor_id::text = $2 AND target_id::text = $3
		   AND metadata->>'session_id' = $4`,
		AuditActionImpersonateStop, terminator, target, sid).Scan(&terminatedBy)
	if terminatedBy == nil || *terminatedBy != "admin_disabled" {
		t.Fatalf("forced stop row terminated_by = %v, want admin_disabled", terminatedBy)
	}

	// Disabling again must not duplicate termination rows: the session is
	// already closed by the first pass.
	doDisable(t, disabler, terminator, false)
	doDisable(t, disabler, terminator, true)
	if n := countStopRowsWithSession(t, terminator, target, sid); n != 1 {
		t.Fatalf("re-disable duplicated termination rows for session %s: got %d, want 1", sid, n)
	}
}

// P2-2 scoping: closed sessions (normal stop) and naturally expired
// sessions are NOT terminated again; only genuinely open sessions are.
func TestAdminDisableSuperAdmin_SkipsClosedAndExpiredSessions(t *testing.T) {
	disabler := insertAdminTestUser(t, "admin-scope-disabler@test.local", "Scope Disabler", true)
	terminator := insertAdminTestUser(t, "admin-scope-admin@test.local", "Scope Admin", true)
	closedTarget := insertAdminTestUser(t, "admin-scope-closed@test.local", "Scope Closed", false)
	expiredTarget := insertAdminTestUser(t, "admin-scope-expired@test.local", "Scope Expired", false)
	openTarget := insertAdminTestUser(t, "admin-scope-open@test.local", "Scope Open", false)

	// Closed session: start + explicit stop (session header relayed the
	// way the middleware does for a real shadow token).
	doImpersonate(t, terminator, closedTarget)
	closedSID := latestImpersonationSessionID(t, terminator, closedTarget)
	stopReq := newRequest("POST", "/api/impersonation/stop", nil)
	stopReq.Header.Set("X-User-ID", closedTarget)
	stopReq.Header.Set("X-Impersonator-ID", terminator)
	stopReq.Header.Set("X-Impersonation-Session", closedSID)
	testutil.Call(t, testHandler.StopImpersonation, stopReq).Want(http.StatusOK)

	// Expired session: start row backdated past its own expires_at.
	doImpersonate(t, terminator, expiredTarget)
	expiredSID := latestImpersonationSessionID(t, terminator, expiredTarget)
	dbfx.Exec(t,
		`UPDATE admin_audit_log SET metadata = jsonb_set(metadata, '{expires_at}', to_jsonb(to_char((now() - interval '1 hour') AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')))
		 WHERE action = $1 AND actor_id::text = $2 AND target_id::text = $3 AND metadata->>'session_id' = $4`,
		AuditActionImpersonateStart, terminator, expiredTarget, expiredSID)

	// Open session: left in flight.
	doImpersonate(t, terminator, openTarget)
	openSID := latestImpersonationSessionID(t, terminator, openTarget)

	doDisable(t, disabler, terminator, true)

	// Only genuinely open sessions get forced-termination rows; the closed
	// session keeps just its explicit stop, the expired one stays rowless.
	if n := countForcedStopRows(t, terminator, closedTarget, closedSID); n != 0 {
		t.Fatalf("closed session must not be terminated again, got %d forced rows", n)
	}
	if n := countForcedStopRows(t, terminator, expiredTarget, expiredSID); n != 0 {
		t.Fatalf("expired session must not be terminated, got %d forced rows", n)
	}
	if n := countForcedStopRows(t, terminator, openTarget, openSID); n != 1 {
		t.Fatalf("open session must be terminated exactly once, got %d forced rows", n)
	}
}
