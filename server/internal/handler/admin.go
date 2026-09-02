package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/logger"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// This file backs the instance-level /api/admin routes (RUYI-47). Every
// handler sits behind middleware.RequireSuperAdmin, so the caller in
// X-User-ID is a live, enabled super admin. Handlers here only enforce the
// semantic guards (no self-lockout, at least one active super admin, no
// impersonating a super admin, no owner-role direct add) and write the
// admin_audit_log trail.

// ImpersonationTokenTTL bounds a shadow JWT. Deliberately far shorter than
// the 30-day login token: an impersonation session is a supervised,
// attributable window, not a second login. Expiry ends the session server
// side; the explicit stop endpoint records the audit end.
const ImpersonationTokenTTL = 30 * time.Minute

// Audit actions and target types. Kept as constants so the test suite and
// future log consumers reference one vocabulary.
const (
	AuditActionUserDisable      = "user.disable"
	AuditActionUserEnable       = "user.enable"
	AuditActionSuperAdminGrant  = "super_admin.grant"
	AuditActionSuperAdminRevoke = "super_admin.revoke"
	AuditActionMemberAdd        = "workspace_member.add"
	AuditActionImpersonateStart = "impersonation.start"
	AuditActionImpersonateStop  = "impersonation.stop"

	AuditTargetTypeUser      = "user"
	AuditTargetTypeWorkspace = "workspace"
)

// MaxAdminReasonLen caps the optional reason field (Q5=B: reason is
// optional). 500 chars is plenty for "investigating RUYI-###" and keeps a
// hostile payload from bloating audit rows.
const MaxAdminReasonLen = 500

// adminReason normalizes the optional reason: trimmed, bounded, empty
// stays NULL.
func adminReason(raw string) pgtype.Text {
	reason := strings.TrimSpace(raw)
	if reason == "" {
		return pgtype.Text{}
	}
	if len(reason) > MaxAdminReasonLen {
		reason = reason[:MaxAdminReasonLen]
	}
	return pgtype.Text{String: reason, Valid: true}
}

// writeAdminAudit records one instance-level admin action. Best-effort: an
// audit write failure is logged but does not roll the action back, because
// the alternative (failing the request after the state change committed)
// is worse for the operator than a missing audit row. Rows carry actor,
// action, target, timestamp, and metadata only — never credentials or
// session content.
func (h *Handler) writeAdminAudit(ctx context.Context, actorID, action, targetType string, targetID, workspaceID pgtype.UUID, reason pgtype.Text, metadata map[string]any) {
	meta := []byte("{}")
	if len(metadata) > 0 {
		if b, err := json.Marshal(metadata); err == nil {
			meta = b
		} else {
			slog.Warn("admin audit metadata marshal failed", "error", err)
		}
	}
	if err := h.Queries.CreateAdminAuditLog(ctx, db.CreateAdminAuditLogParams{
		ActorID:     parseUUID(actorID),
		Action:      action,
		TargetType:  targetType,
		TargetID:    targetID,
		WorkspaceID: workspaceID,
		Reason:      reason,
		Metadata:    meta,
	}); err != nil {
		slog.Warn("admin audit write failed", "action", action, "actor_id", actorID, "error", err)
	}
}

// ---------------------------------------------------------------------------
// User management
// ---------------------------------------------------------------------------

// AdminUserResponse is one row of the instance-wide user directory.
type AdminUserResponse struct {
	ID             string  `json:"id"`
	Name           string  `json:"name"`
	Email          string  `json:"email"`
	AvatarURL      *string `json:"avatar_url"`
	IsSuperAdmin   bool    `json:"is_super_admin"`
	DisabledAt     *string `json:"disabled_at"`
	WorkspaceCount int64   `json:"workspace_count"`
	CreatedAt      string  `json:"created_at"`
}

type AdminUserListResponse struct {
	Users []AdminUserResponse `json:"users"`
	Total int64               `json:"total"`
}

const (
	adminListDefaultLimit = 50
	adminListMaxLimit     = 200
)

// parseAdminListParams reads the shared ?query=&limit=&offset= pagination
// for the admin list endpoints, clamping limit into a sane band. Invalid or
// missing values fall back to the defaults, matching the autopilot list
// handlers.
func parseAdminListParams(r *http.Request) (query string, limit, offset int32) {
	query = strings.TrimSpace(r.URL.Query().Get("query"))

	limit = adminListDefaultLimit
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 {
		limit = int32(min(v, adminListMaxLimit)) //nolint:gosec // clamped to adminListMaxLimit above
	}

	if v, err := strconv.Atoi(r.URL.Query().Get("offset")); err == nil && v > 0 {
		offset = int32(v) //nolint:gosec // negatives rejected; overflow inputs fail Atoi
	}
	return query, limit, offset
}

func (h *Handler) adminUserToResponse(row db.ListAllUsersRow) AdminUserResponse {
	var avatarURL *string
	if row.AvatarUrl.Valid && row.AvatarUrl.String != "" {
		u := row.AvatarUrl.String
		avatarURL = &u
	}
	var disabledAt *string
	if row.DisabledAt.Valid {
		t := timestampToString(row.DisabledAt)
		disabledAt = &t
	}
	return AdminUserResponse{
		ID:             uuidToString(row.ID),
		Name:           row.Name,
		Email:          row.Email,
		AvatarURL:      avatarURL,
		IsSuperAdmin:   row.IsSuperAdmin,
		DisabledAt:     disabledAt,
		WorkspaceCount: row.WorkspaceCount,
		CreatedAt:      timestampToString(row.CreatedAt),
	}
}

// AdminListUsers handles GET /api/admin/users?query=&limit=&offset=.
func (h *Handler) AdminListUsers(w http.ResponseWriter, r *http.Request) {
	query, limit, offset := parseAdminListParams(r)

	rows, err := h.Queries.ListAllUsers(r.Context(), db.ListAllUsersParams{
		Query:  query,
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		slog.Warn("admin list users failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to list users")
		return
	}

	total, err := h.Queries.CountAllUsers(r.Context(), query)
	if err != nil {
		slog.Warn("admin count users failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to list users")
		return
	}

	users := make([]AdminUserResponse, 0, len(rows))
	for _, row := range rows {
		users = append(users, h.adminUserToResponse(row))
	}

	writeJSON(w, http.StatusOK, AdminUserListResponse{Users: users, Total: total})
}

type AdminSetUserDisabledRequest struct {
	Disabled bool   `json:"disabled"`
	Reason   string `json:"reason"`
}

// AdminSetUserDisabled handles PATCH /api/admin/users/{id}/disabled.
//
// Side effects of a disable, in order: row state flip, every live PAT of
// the account revoked (their cache entries dropped), the user-state cache
// invalidated so this node re-reads the DB on the next request. New logins
// are rejected immediately (the login path reads the row); already-issued
// browser sessions and daemon/CLI tokens lose access within
// UserStateCacheTTL.
func (h *Handler) AdminSetUserDisabled(w http.ResponseWriter, r *http.Request) {
	actorID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	targetID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "user id")
	if !ok {
		return
	}

	var req AdminSetUserDisabledRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Self-protection guard (Owner requirement): an admin cannot disable
	// their own live account through the API. It would also trip the
	// last-active-super-admin invariant, but the explicit check gives a
	// precise error regardless of role.
	if targetID == parseUUID(actorID) {
		writeError(w, http.StatusForbidden, "cannot disable your own account")
		return
	}

	target, err := h.Queries.GetUser(r.Context(), targetID)
	if err != nil {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}

	now := time.Now()
	// The id is written on BOTH paths — the WHERE clause rides params.ID.
	// Disabling additionally stamps the actor + timestamp; enabling leaves
	// the narg pair NULL so the same UPDATE clears both columns.
	params := db.SetUserDisabledParams{ID: targetID}
	if req.Disabled {
		params.DisabledAt = pgtype.Timestamptz{Time: now, Valid: true}
		params.DisabledBy = parseUUID(actorID)
	}

	if _, err := h.Queries.SetUserDisabled(r.Context(), params); err != nil {
		slog.Warn("admin set user disabled failed", append(logger.RequestAttrs(r), "error", err, "target_id", uuidToString(targetID))...)
		writeError(w, http.StatusInternalServerError, "failed to update user")
		return
	}

	if req.Disabled {
		// Machine credentials die with the account: revoke every live
		// PAT and drop their cache entries so CLI/daemon traffic stops
		// at the next request instead of at AuthCacheTTL.
		hashes, err := h.Queries.RevokeAllPersonalAccessTokensByUser(r.Context(), targetID)
		if err != nil {
			slog.Warn("admin revoke PATs failed", append(logger.RequestAttrs(r), "error", err, "target_id", uuidToString(targetID))...)
		}
		for _, hash := range hashes {
			h.PATCache.Invalidate(r.Context(), hash)
		}
	}

	h.UserStateCache.Invalidate(r.Context(), uuidToString(targetID))

	action := AuditActionUserEnable
	if req.Disabled {
		action = AuditActionUserDisable
	}
	h.writeAdminAudit(r.Context(), actorID, action, AuditTargetTypeUser, targetID,
		pgtype.UUID{}, adminReason(req.Reason), map[string]any{
			"target_email": target.Email,
		})

	updated, err := h.Queries.GetUser(r.Context(), targetID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load user")
		return
	}
	writeJSON(w, http.StatusOK, h.userToResponse(updated))
}

type AdminSetUserSuperAdminRequest struct {
	Granted bool   `json:"granted"`
	Reason  string `json:"reason"`
}

// AdminSetUserSuperAdmin handles PATCH /api/admin/users/{id}/super-admin.
// Guards: an admin cannot revoke their own flag (Q4=A), and revocation
// must never leave the instance without an enabled super admin.
func (h *Handler) AdminSetUserSuperAdmin(w http.ResponseWriter, r *http.Request) {
	actorID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	targetID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "user id")
	if !ok {
		return
	}

	var req AdminSetUserSuperAdminRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	target, err := h.Queries.GetUserAdminState(r.Context(), targetID)
	if err != nil {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}

	if !req.Granted {
		if targetID == parseUUID(actorID) {
			writeError(w, http.StatusForbidden, "cannot revoke your own super admin role")
			return
		}
		// Last-admin guard only bites when the target is currently an
		// enabled super admin; revoking a disabled admin's flag cannot
		// reduce the active count.
		if target.IsSuperAdmin && !target.DisabledAt.Valid {
			remaining, err := h.Queries.CountActiveSuperAdminsExcluding(r.Context(), targetID)
			if err != nil {
				slog.Warn("admin count super admins failed", append(logger.RequestAttrs(r), "error", err)...)
				writeError(w, http.StatusInternalServerError, "failed to update user")
				return
			}
			if remaining < 1 {
				writeError(w, http.StatusConflict, "at least one active super admin must remain")
				return
			}
		}
	}

	if _, err := h.Queries.SetUserSuperAdmin(r.Context(), db.SetUserSuperAdminParams{
		ID:           targetID,
		IsSuperAdmin: req.Granted,
	}); err != nil {
		slog.Warn("admin set super admin failed", append(logger.RequestAttrs(r), "error", err, "target_id", uuidToString(targetID))...)
		writeError(w, http.StatusInternalServerError, "failed to update user")
		return
	}

	h.UserStateCache.Invalidate(r.Context(), uuidToString(targetID))

	action := AuditActionSuperAdminRevoke
	if req.Granted {
		action = AuditActionSuperAdminGrant
	}
	h.writeAdminAudit(r.Context(), actorID, action, AuditTargetTypeUser, targetID,
		pgtype.UUID{}, adminReason(req.Reason), nil)

	updated, err := h.Queries.GetUser(r.Context(), targetID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load user")
		return
	}
	writeJSON(w, http.StatusOK, h.userToResponse(updated))
}

// ---------------------------------------------------------------------------
// Workspace management
// ---------------------------------------------------------------------------

// AdminWorkspaceResponse is one row of the instance-wide workspace
// directory. Owner is the longest-standing owner-role member.
type AdminWorkspaceResponse struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Slug        string  `json:"slug"`
	OwnerID     *string `json:"owner_id"`
	OwnerName   *string `json:"owner_name"`
	OwnerEmail  *string `json:"owner_email"`
	MemberCount int64   `json:"member_count"`
	CreatedAt   string  `json:"created_at"`
}

type AdminWorkspaceListResponse struct {
	Workspaces []AdminWorkspaceResponse `json:"workspaces"`
	Total      int64                    `json:"total"`
}

// AdminListWorkspaces handles GET /api/admin/workspaces?query=&limit=&offset=.
func (h *Handler) AdminListWorkspaces(w http.ResponseWriter, r *http.Request) {
	query, limit, offset := parseAdminListParams(r)

	rows, err := h.Queries.ListAllWorkspaces(r.Context(), db.ListAllWorkspacesParams{
		Query:  query,
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		slog.Warn("admin list workspaces failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to list workspaces")
		return
	}

	total, err := h.Queries.CountAllWorkspaces(r.Context(), query)
	if err != nil {
		slog.Warn("admin count workspaces failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to list workspaces")
		return
	}

	workspaces := make([]AdminWorkspaceResponse, 0, len(rows))
	for _, row := range rows {
		ws := AdminWorkspaceResponse{
			ID:          uuidToString(row.ID),
			Name:        row.Name,
			Slug:        row.Slug,
			MemberCount: row.MemberCount,
			CreatedAt:   timestampToString(row.CreatedAt),
		}
		if row.OwnerID.Valid {
			id := uuidToString(row.OwnerID)
			ws.OwnerID = &id
			name := row.OwnerName
			ws.OwnerName = &name
			email := row.OwnerEmail
			ws.OwnerEmail = &email
		}
		workspaces = append(workspaces, ws)
	}

	writeJSON(w, http.StatusOK, AdminWorkspaceListResponse{Workspaces: workspaces, Total: total})
}

type AdminAddWorkspaceMemberRequest struct {
	Email  string `json:"email"`
	UserID string `json:"user_id"`
	Role   string `json:"role"`
	Reason string `json:"reason"`
}

// AdminAddWorkspaceMember handles POST /api/admin/workspaces/{id}/members —
// the invite-free direct add. Roles are bounded to member/admin (Q3=B):
// owner involves workspace ownership transfer semantics that stay inside
// the workspace's own management flow. The user must already exist; adding
// a not-yet-registered email is what the normal invitation flow is for.
func (h *Handler) AdminAddWorkspaceMember(w http.ResponseWriter, r *http.Request) {
	actorID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "workspace id")
	if !ok {
		return
	}

	var req AdminAddWorkspaceMemberRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	role, valid := normalizeMemberRole(req.Role)
	if !valid {
		writeError(w, http.StatusBadRequest, "invalid member role")
		return
	}
	if role == "owner" {
		writeError(w, http.StatusBadRequest, "cannot add a member as owner")
		return
	}

	var user db.User
	switch {
	case strings.TrimSpace(req.UserID) != "":
		id, ok := parseUUIDOrBadRequest(w, req.UserID, "user_id")
		if !ok {
			return
		}
		u, err := h.Queries.GetUser(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		user = u
	case strings.TrimSpace(req.Email) != "":
		u, err := h.Queries.GetUserByEmail(r.Context(), strings.ToLower(strings.TrimSpace(req.Email)))
		if err != nil {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		user = u
	default:
		writeError(w, http.StatusBadRequest, "email or user_id is required")
		return
	}

	// Idempotency: already a member → conflict, mirroring the invitation
	// handler's behaviour for existing members.
	if _, err := h.Queries.GetMemberByUserAndWorkspace(r.Context(), db.GetMemberByUserAndWorkspaceParams{
		UserID:      user.ID,
		WorkspaceID: workspaceID,
	}); err == nil {
		writeError(w, http.StatusConflict, "user is already a member")
		return
	}

	if _, err := h.Queries.GetWorkspace(r.Context(), workspaceID); err != nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}

	member, err := h.Queries.CreateMember(r.Context(), db.CreateMemberParams{
		WorkspaceID: workspaceID,
		UserID:      user.ID,
		Role:        role,
	})
	if err != nil {
		slog.Warn("admin add workspace member failed", append(logger.RequestAttrs(r), "error", err, "workspace_id", uuidToString(workspaceID))...)
		writeError(w, http.StatusInternalServerError, "failed to add member")
		return
	}

	h.MembershipCache.Invalidate(r.Context(), uuidToString(user.ID), uuidToString(workspaceID))

	h.writeAdminAudit(r.Context(), actorID, AuditActionMemberAdd, AuditTargetTypeWorkspace, workspaceID,
		workspaceID, adminReason(req.Reason), map[string]any{
			"member_id": uuidToString(member.ID),
			"role":      role,
			"user_id":   uuidToString(user.ID),
		})

	writeJSON(w, http.StatusCreated, map[string]any{
		"id":           uuidToString(member.ID),
		"workspace_id": uuidToString(member.WorkspaceID),
		"user_id":      uuidToString(member.UserID),
		"role":         member.Role,
		"created_at":   timestampToString(member.CreatedAt),
	})
}

// ---------------------------------------------------------------------------
// Impersonation (Q2=B: all content-side access by a super admin travels
// through an attributable impersonation session; no invisible read path.)
// ---------------------------------------------------------------------------

type AdminImpersonateRequest struct {
	Reason string `json:"reason"`
}

// AdminImpersonate handles POST /api/admin/users/{id}/impersonate. Issues a
// short-TTL shadow JWT (sub = target, imp = acting super admin) and records
// the session start. Machine credentials (mat_/mdt_/mcn_/mul_) are never
// touched — only a browser session token is issued. Guard: targets that
// are super admins (including the caller) are rejected, so admin access can
// never be gained through an impersonated identity.
func (h *Handler) AdminImpersonate(w http.ResponseWriter, r *http.Request) {
	actorID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	targetID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "user id")
	if !ok {
		return
	}

	var req AdminImpersonateRequest
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
	}

	target, err := h.Queries.GetUser(r.Context(), targetID)
	if err != nil {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	if target.IsSuperAdmin {
		writeError(w, http.StatusForbidden, "cannot impersonate a super admin")
		return
	}
	if target.DisabledAt.Valid {
		writeError(w, http.StatusForbidden, "cannot impersonate a disabled user")
		return
	}

	now := time.Now()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub":   uuidToString(target.ID),
		"email": target.Email,
		"name":  target.Name,
		"imp":   actorID,
		"iat":   now.Unix(),
		"exp":   now.Add(ImpersonationTokenTTL).Unix(),
	})
	tokenString, err := token.SignedString(auth.JWTSecret())
	if err != nil {
		slog.Warn("impersonation token signing failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to generate token")
		return
	}

	if err := auth.SetAuthCookies(w, tokenString); err != nil {
		slog.Warn("failed to set impersonation cookies", "error", err)
	}

	h.writeAdminAudit(r.Context(), actorID, AuditActionImpersonateStart, AuditTargetTypeUser, targetID,
		pgtype.UUID{}, adminReason(req.Reason), map[string]any{
			"expires_at": now.Add(ImpersonationTokenTTL).UTC().Format(time.RFC3339),
		})

	slog.Info("impersonation session started",
		append(logger.RequestAttrs(r),
			"actor_id", actorID,
			"target_id", uuidToString(target.ID),
			"ttl_seconds", int(ImpersonationTokenTTL.Seconds()))...)

	writeJSON(w, http.StatusOK, LoginResponse{
		Token: tokenString,
		User:  h.userToResponse(target),
	})
}

// StopImpersonation handles POST /api/impersonation/stop from the
// user-scoped route group (NOT /api/admin — the caller authenticates as the
// impersonated user). Verifies the shadow-token pair stamped by the
// middleware, re-mints a fresh login token for the impersonator, and
// records the session end. A target identity can never mint itself a token
// for the impersonator: the impersonator ID comes from the signed imp
// claim, and the re-mint re-reads the impersonator row to confirm they are
// still an enabled super admin.
func (h *Handler) StopImpersonation(w http.ResponseWriter, r *http.Request) {
	targetID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	impersonatorID := r.Header.Get("X-Impersonator-ID")
	if impersonatorID == "" {
		writeError(w, http.StatusBadRequest, "not in an impersonation session")
		return
	}

	impersonator, err := h.Queries.GetUser(r.Context(), parseUUID(impersonatorID))
	if err != nil {
		writeError(w, http.StatusForbidden, "impersonator account is unavailable")
		return
	}
	if !impersonator.IsSuperAdmin || impersonator.DisabledAt.Valid {
		writeError(w, http.StatusForbidden, "impersonator account is unavailable")
		return
	}

	tokenString, err := h.issueJWT(impersonator)
	if err != nil {
		slog.Warn("stop impersonation token mint failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to generate token")
		return
	}

	if err := auth.SetAuthCookies(w, tokenString); err != nil {
		slog.Warn("failed to set auth cookies", "error", err)
	}

	h.writeAdminAudit(r.Context(), impersonatorID, AuditActionImpersonateStop, AuditTargetTypeUser,
		parseUUID(targetID), pgtype.UUID{}, pgtype.Text{}, nil)

	writeJSON(w, http.StatusOK, LoginResponse{
		Token: tokenString,
		User:  h.userToResponse(impersonator),
	})
}
