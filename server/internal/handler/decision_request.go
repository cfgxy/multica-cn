package handler

// Decision requests (RUYI-630): the Issue-independent authorization carrier
// for agent-initiated sensitive operations. An agent that cannot reach a
// human-gated endpoint raises a request instead; the humans the tiers name
// approve it in their own space's decision center (and, when an issue
// reference exists, on the issue thread's authorization link card); once
// every required step has approved, the platform executor runs the whitelisted
// action in an internal context and the terminal state is echoed back to the
// requesting run through the standard mention pipeline.
//
// Carrier layout (方案甲, Owner decision 5): one row PER INVOLVED SPACE,
// grouped by request_group_id. The target-space row is the operable second
// confirmation (decision 3B — operator tier forced to the target workspace
// Owner). The origin-space row is a read-only state projection: the origin
// step happens on the issue_decisions authorization link card when an issue
// reference exists, and on the origin row itself only when it does not
// (no-issue compatibility). Every state transition dual-writes the whole
// group in one transaction — clients never read across spaces.

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/logger"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Lifecycle statuses (mirrored by the migration CHECK).
const (
	decisionReqPending       = "pending"
	decisionReqApproved      = "approved"
	decisionReqDenied        = "denied"
	decisionReqExpired       = "expired"
	decisionReqRevoked       = "revoked"
	decisionReqExecuted      = "executed"
	decisionReqExecuteFailed = "execute_failed"
)

const (
	decisionReqRoleOrigin = "origin"
	decisionReqRoleTarget = "target"
)

// Answer sources. Only card_click reaches authorization rows/cards today;
// the question-card surface also records text_token and batch.
const (
	decisionSourceCardClick = "card_click"
	decisionSourceTextToken = "text_token"
	decisionSourceBatch     = "batch"
)

const (
	// Decision 4 (Owner): default validity 24h; the requester may declare
	// shorter, the platform caps at the same ceiling.
	decisionReqDefaultTTL = 24 * time.Hour
	decisionReqMaxTTL     = 24 * time.Hour
	decisionReqMinTTL     = 5 * time.Minute

	decisionReqMaxTitleLen  = 300
	decisionReqMaxDetailLen = 2000
	// Button label ceiling (规格第二节: 建议 ≤6 汉字, exact value left to
	// design; the server gate is deliberately a bit wider than the guideline).
	decisionReqMaxLabelLen    = 10
	decisionReqMaxNamedList   = 20
	decisionReqListDefaultLim = 200
	decisionReqListMaxLim     = 500
)

// DecisionRequestAction values — the phase-1 whitelist registry (decision 2:
// reads + restore as the first low-risk write; billing/payment classes never
// register). Risk tier is registry-derived and server-authoritative; the
// client cannot declare it.
const (
	DecisionActionWorkspaceInfoRead = "workspace_info_read"
	DecisionActionPromptRestore     = "prompt_restore"
)

const (
	decisionRiskRead     = "read"
	decisionRiskWriteLow = "write_low"
)

type decisionRequestActionDef struct {
	RiskTier string
	Summary  string
}

var decisionRequestActions = map[string]decisionRequestActionDef{
	DecisionActionWorkspaceInfoRead: {
		RiskTier: decisionRiskRead,
		Summary:  "Read the target workspace's basic profile (name, description).",
	},
	DecisionActionPromptRestore: {
		RiskTier: decisionRiskWriteLow,
		Summary:  "Undo the last applied marketplace prompt on an agent or squad (one-step restore, hand-edit guarded).",
	},
}

// errDecisionRequestValidation marks input-shape rejections (400).
type errDecisionRequestValidation struct{ msg string }

func (e errDecisionRequestValidation) Error() string { return e.msg }

type DecisionRequestResponse struct {
	ID             string          `json:"id"`
	RequestGroupID string          `json:"request_group_id"`
	WorkspaceID    string          `json:"workspace_id"`
	Role           string          `json:"role"`
	Operable       bool            `json:"operable"`
	Status         string          `json:"status"`
	ActionType     string          `json:"action_type"`
	ActionParams   json.RawMessage `json:"action_params"`
	RiskTier       string          `json:"risk_tier"`
	Title          string          `json:"title"`
	Detail         string          `json:"detail"`
	// Title-level origin references only — the detail view never carries the
	// origin issue's body or comments (决策中心点击不进 Issue).
	OriginWorkspaceID string          `json:"origin_workspace_id"`
	OriginAgentID     string          `json:"origin_agent_id"`
	OriginTaskID      *string         `json:"origin_task_id"`
	OriginIssueID     *string         `json:"origin_issue_id"`
	OriginIssueTitle  *string         `json:"origin_issue_title"`
	OperatorTier      string          `json:"operator_tier"`
	NamedApproverIDs  []string        `json:"named_approver_ids"`
	ApproveLabel      *string         `json:"approve_label"`
	DenyLabel         *string         `json:"deny_label"`
	ExpiresAt         time.Time       `json:"expires_at"`
	AnsweredByType    *string         `json:"answered_by_type"`
	AnsweredByID      *string         `json:"answered_by_id"`
	AnsweredAt        *time.Time      `json:"answered_at"`
	AnswerSource      *string         `json:"answer_source"`
	ExecutedAt        *time.Time      `json:"executed_at"`
	ExecutionResult   json.RawMessage `json:"execution_result,omitempty"`
	ExecutionError    *string         `json:"execution_error"`
	CreatedAt         time.Time       `json:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at"`
}

// DecisionRequestDetailResponse is the authorization summary the decision
// center opens: this space's row plus the per-space step rows of the group,
// so a reader sees "origin approved, waiting on target" without any
// cross-space fetch of issue content.
type DecisionRequestDetailResponse struct {
	Request DecisionRequestResponse   `json:"request"`
	Steps   []DecisionRequestResponse `json:"steps"`
	Card    *IssueDecisionResponse    `json:"card,omitempty"`
}

type CreateDecisionRequestRequest struct {
	ActionType string          `json:"action_type"`
	Params     json.RawMessage `json:"params"`
	Title      string          `json:"title"`
	Detail     string          `json:"detail"`
	// Target workspace for the operation; empty = the caller's own space.
	TargetWorkspaceID string `json:"target_workspace_id,omitempty"`
	// Optional title-level issue reference in the origin space. When set,
	// the origin step is taken on the issue thread's authorization link
	// card; when absent, the origin row itself is operable.
	OriginIssueID string `json:"origin_issue_id,omitempty"`
	OriginTaskID  string `json:"origin_task_id,omitempty"`
	// Origin-space tier declarations for the link card (spec 第一节). Empty
	// fields take the fail-closed defaults.
	VisibleTier      string   `json:"visible_tier,omitempty"`
	OperatorTier     string   `json:"operator_tier,omitempty"`
	NamedApproverIDs []string `json:"named_approver_ids,omitempty"`
	ApproveLabel     string   `json:"approve_label,omitempty"`
	DenyLabel        string   `json:"deny_label,omitempty"`
	// Validity window in minutes; 0/absent = 24h; capped at 24h, floored at
	// 5 minutes.
	TTLMinutes int `json:"ttl_minutes,omitempty"`
}

type AnswerDecisionRequestRequest struct {
	// "approve" or "deny".
	Decision string `json:"decision"`
}

func decisionRequestToResponse(row db.DecisionRequest) DecisionRequestResponse {
	resp := DecisionRequestResponse{
		ID:               uuidToString(row.ID),
		RequestGroupID:   uuidToString(row.RequestGroupID),
		WorkspaceID:      uuidToString(row.WorkspaceID),
		Role:             row.Role,
		Operable:         row.Operable,
		Status:           row.Status,
		ActionType:       row.ActionType,
		ActionParams:     json.RawMessage(row.ActionParams),
		RiskTier:         row.RiskTier,
		Title:            row.Title,
		Detail:           row.Detail,
		OriginAgentID:    uuidToString(row.OriginAgentID),
		OperatorTier:     row.OperatorTier,
		NamedApproverIDs: parseUUIDJSONArray(row.NamedApproverIds),
		ExpiresAt:        row.ExpiresAt.Time,
		CreatedAt:        row.CreatedAt.Time,
		UpdatedAt:        row.UpdatedAt.Time,
	}
	resp.OriginWorkspaceID = uuidToString(row.OriginWorkspaceID)
	if row.OriginTaskID.Valid {
		v := uuidToString(row.OriginTaskID)
		resp.OriginTaskID = &v
	}
	if row.OriginIssueID.Valid {
		v := uuidToString(row.OriginIssueID)
		resp.OriginIssueID = &v
	}
	if row.OriginIssueTitle.Valid {
		v := row.OriginIssueTitle.String
		resp.OriginIssueTitle = &v
	}
	if row.ApproveLabel.Valid {
		v := row.ApproveLabel.String
		resp.ApproveLabel = &v
	}
	if row.DenyLabel.Valid {
		v := row.DenyLabel.String
		resp.DenyLabel = &v
	}
	if row.AnsweredByType.Valid {
		v := row.AnsweredByType.String
		resp.AnsweredByType = &v
	}
	if row.AnsweredByID.Valid {
		v := uuidToString(row.AnsweredByID)
		resp.AnsweredByID = &v
	}
	if row.AnsweredAt.Valid {
		v := row.AnsweredAt.Time
		resp.AnsweredAt = &v
	}
	if row.AnswerSource.Valid {
		v := row.AnswerSource.String
		resp.AnswerSource = &v
	}
	if row.ExecutedAt.Valid {
		v := row.ExecutedAt.Time
		resp.ExecutedAt = &v
	}
	if len(row.ExecutionResult) > 0 {
		resp.ExecutionResult = json.RawMessage(row.ExecutionResult)
	}
	if row.ExecutionError.Valid {
		v := row.ExecutionError.String
		resp.ExecutionError = &v
	}
	return resp
}

// parseUUIDJSONArray decodes a JSONB array of UUID strings; corrupt rows
// (hand-edited data) render as an empty list rather than a 500.
func parseUUIDJSONArray(raw []byte) []string {
	if len(raw) == 0 {
		return []string{}
	}
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil {
		return []string{}
	}
	return out
}

func containsUUIDString(list []string, id string) bool {
	for _, v := range list {
		if v == id {
			return true
		}
	}
	return false
}

// normalizeDecisionRequestTiers applies the spec's consistency rules: the
// operator scope must sit inside the visible scope, and a violating
// declaration converges to owner (fail-closed, never widened).
func normalizeDecisionRequestTiers(visible, operator string) (string, string, error) {
	if visible == "" {
		visible = "members"
	}
	if operator == "" {
		operator = "owner"
	}
	switch visible {
	case "members", "restricted":
	default:
		return "", "", errDecisionRequestValidation{"visible_tier must be one of members, restricted"}
	}
	switch operator {
	case "owner", "named", "members":
	default:
		return "", "", errDecisionRequestValidation{"operator_tier must be one of owner, named, members"}
	}
	if operator == "members" && visible == "restricted" {
		// 声明违反一致性时服务端收敛为仅 Owner。
		operator = "owner"
	}
	return visible, operator, nil
}

// validateDecisionRequestLabels applies the dual-button customization rules
// (规格第二节): both labels or neither, length-capped, never identical —
// identical labels are语义互换 in the eyes of an operator.
func validateDecisionRequestLabels(approve, deny string) (*string, *string, error) {
	approve = trimLabel(approve)
	deny = trimLabel(deny)
	if approve == "" && deny == "" {
		return nil, nil, nil
	}
	if approve == "" || deny == "" {
		return nil, nil, errDecisionRequestValidation{"approve_label and deny_label must be declared together"}
	}
	if len([]rune(approve)) > decisionReqMaxLabelLen || len([]rune(deny)) > decisionReqMaxLabelLen {
		return nil, nil, errDecisionRequestValidation{fmt.Sprintf("button labels must be at most %d characters", decisionReqMaxLabelLen)}
	}
	if approve == deny {
		return nil, nil, errDecisionRequestValidation{"button labels must not be identical"}
	}
	return &approve, &deny, nil
}

func trimLabel(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if r == 0 {
			continue
		}
		out = append(out, r)
	}
	return string(out)
}

// resolveDecisionRequestExpiry clamps the requested TTL into
// [5min, 24h]; 0 falls back to the 24h default (decision 4).
func resolveDecisionRequestExpiry(ttlMinutes int) time.Time {
	ttl := decisionReqDefaultTTL
	if ttlMinutes > 0 {
		ttl = time.Duration(ttlMinutes) * time.Minute
	}
	if ttl > decisionReqMaxTTL {
		ttl = decisionReqMaxTTL
	}
	if ttl < decisionReqMinTTL {
		ttl = decisionReqMinTTL
	}
	return time.Now().Add(ttl)
}

// CreateDecisionRequest handles POST /api/workspaces/{id}/decision-requests.
// Agent-only by definition: the channel exists because agent credentials
// cannot reach the sensitive endpoint, so a member raising one has nothing
// to authorize. The creation itself is permissionless by design — it asks
// for consent, it does not take it; the whitelisted registry and the tier
// checks on every answer are the security boundary.
func (h *Handler) CreateDecisionRequest(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID, ok := parseUUIDOrBadRequest(w, h.resolveWorkspaceID(r), "workspace id")
	if !ok {
		return
	}
	authorType, authorID := h.resolveActor(r, userID, uuidToString(workspaceID))
	if authorType != "agent" {
		writeError(w, http.StatusForbidden, "authorization requests are raised by agents")
		return
	}

	var req CreateDecisionRequestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	action, ok := decisionRequestActions[req.ActionType]
	if !ok {
		writeError(w, http.StatusBadRequest, "unknown action_type: the whitelist registry is the only door")
		return
	}
	title := sanitizeNullBytes(req.Title)
	if title == "" {
		writeError(w, http.StatusBadRequest, "title is required")
		return
	}
	if len([]rune(title)) > decisionReqMaxTitleLen {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("title must be at most %d characters", decisionReqMaxTitleLen))
		return
	}
	detail := sanitizeNullBytes(req.Detail)
	if len([]rune(detail)) > decisionReqMaxDetailLen {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("detail must be at most %d characters", decisionReqMaxDetailLen))
		return
	}
	if len(req.Params) == 0 {
		req.Params = []byte("{}")
	}
	if !json.Valid(req.Params) {
		writeError(w, http.StatusBadRequest, "params must be a JSON object")
		return
	}
	approveLabel, denyLabel, err := validateDecisionRequestLabels(req.ApproveLabel, req.DenyLabel)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	visibleTier, operatorTier, err := normalizeDecisionRequestTiers(req.VisibleTier, req.OperatorTier)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	namedIDs := make([]string, 0, len(req.NamedApproverIDs))
	for _, id := range req.NamedApproverIDs {
		if _, err := util.ParseUUID(id); err != nil {
			writeError(w, http.StatusBadRequest, "named_approver_ids contains a non-UUID value")
			return
		}
		namedIDs = append(namedIDs, id)
	}
	if len(namedIDs) > decisionReqMaxNamedList {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("named_approver_ids must contain at most %d entries", decisionReqMaxNamedList))
		return
	}
	namedJSON, err := json.Marshal(namedIDs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to encode named approvers")
		return
	}
	expiresAt := resolveDecisionRequestExpiry(req.TTLMinutes)

	// The origin task (when supplied) must exist so the terminal callback can
	// find its issue context; it is recorded, not authorized here.
	var originTaskID pgtype.UUID
	if req.OriginTaskID != "" {
		originTaskID, ok = parseUUIDOrBadRequest(w, req.OriginTaskID, "origin_task_id")
		if !ok {
			return
		}
	}

	// Optional origin issue reference: title-level only. The issue must live
	// in the caller's workspace; its body and comments never enter this
	// carrier.
	var originIssueID pgtype.UUID
	var originIssueTitle pgtype.Text
	if req.OriginIssueID != "" {
		originIssueID, ok = parseUUIDOrBadRequest(w, req.OriginIssueID, "origin_issue_id")
		if !ok {
			return
		}
		issue, err := h.Queries.GetIssueInWorkspace(r.Context(), db.GetIssueInWorkspaceParams{
			ID: originIssueID, WorkspaceID: workspaceID,
		})
		if err != nil {
			writeError(w, http.StatusNotFound, "origin issue not found in this workspace")
			return
		}
		originIssueTitle = pgtype.Text{String: issue.Title, Valid: true}
	}

	// Target space: default = the caller's own workspace (single-space
	// request). A different target turns the request cross-space.
	targetWorkspaceID := workspaceID
	if req.TargetWorkspaceID != "" && req.TargetWorkspaceID != uuidToString(workspaceID) {
		targetWorkspaceID, ok = parseUUIDOrBadRequest(w, req.TargetWorkspaceID, "target_workspace_id")
		if !ok {
			return
		}
		if _, err := h.Queries.GetWorkspace(r.Context(), targetWorkspaceID); err != nil {
			writeError(w, http.StatusNotFound, "target workspace not found")
			return
		}
	}
	crossSpace := targetWorkspaceID != workspaceID

	groupID := dbid.NewV7()
	agentUUID := parseUUID(authorID)
	expires := pgtype.Timestamptz{Time: expiresAt, Valid: true}

	originOperable := !originIssueID.Valid
	paramsCopy := append([]byte(nil), req.Params...)

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create decision request")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)

	originRow, err := qtx.CreateDecisionRequest(r.Context(), db.CreateDecisionRequestParams{
		ID:                dbid.NewV7(),
		RequestGroupID:    groupID,
		WorkspaceID:       workspaceID,
		Role:              decisionReqRoleOrigin,
		Operable:          originOperable,
		ActionType:        req.ActionType,
		ActionParams:      paramsCopy,
		RiskTier:          action.RiskTier,
		Title:             title,
		Detail:            detail,
		OriginWorkspaceID: workspaceID,
		OriginAgentID:     agentUUID,
		OriginTaskID:      originTaskID,
		OriginIssueID:     originIssueID,
		OriginIssueTitle:  originIssueTitle,
		OperatorTier:      operatorTier,
		NamedApproverIds:  namedJSON,
		ApproveLabel:      textPtrFromStr(approveLabel),
		DenyLabel:         textPtrFromStr(denyLabel),
		ExpiresAt:         expires,
		CreatedByID:       agentUUID,
	})
	if err != nil {
		slog.Warn("create decision request origin row failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to create decision request")
		return
	}

	rows := []db.DecisionRequest{originRow}

	// Cross-space: the target-space Owner owes the second confirmation
	// (decision 3B) — the target row is operable, owner-tier, and carries no
	// named list of its own.
	if crossSpace {
		targetRow, err := qtx.CreateDecisionRequest(r.Context(), db.CreateDecisionRequestParams{
			ID:                dbid.NewV7(),
			RequestGroupID:    groupID,
			WorkspaceID:       targetWorkspaceID,
			Role:              decisionReqRoleTarget,
			Operable:          true,
			ActionType:        req.ActionType,
			ActionParams:      paramsCopy,
			RiskTier:          action.RiskTier,
			Title:             title,
			Detail:            detail,
			OriginWorkspaceID: workspaceID,
			OriginAgentID:     agentUUID,
			OriginTaskID:      originTaskID,
			OriginIssueID:     originIssueID,
			OriginIssueTitle:  originIssueTitle,
			OperatorTier:      "owner",
			NamedApproverIds:  []byte("[]"),
			ApproveLabel:      textPtrFromStr(approveLabel),
			DenyLabel:         textPtrFromStr(denyLabel),
			ExpiresAt:         expires,
			CreatedByID:       agentUUID,
		})
		if err != nil {
			slog.Warn("create decision request target row failed", append(logger.RequestAttrs(r), "error", err)...)
			writeError(w, http.StatusInternalServerError, "failed to create decision request")
			return
		}
		rows = append(rows, targetRow)
	}

	// With an issue reference the origin step happens on the issue thread's
	// authorization link card (发起空间第一步授权走现有面); the origin row
	// stays a read-only projection. Without one the origin row is operable
	// (无 Issue 场景兼容).
	var card *db.IssueDecision
	if originIssueID.Valid {
		created, err := qtx.CreateAuthorizationDecisionCard(r.Context(), db.CreateAuthorizationDecisionCardParams{
			ID:               dbid.NewV7(),
			WorkspaceID:      workspaceID,
			IssueID:          originIssueID,
			SourceCommentID:  pgtype.UUID{},
			Question:         title,
			Options:          authorizationCardOptionsJSON(approveLabel, denyLabel),
			VisibleTier:      visibleTier,
			OperatorTier:     operatorTier,
			NamedApproverIds: namedJSON,
			ApproveLabel:     textPtrFromStr(approveLabel),
			DenyLabel:        textPtrFromStr(denyLabel),
			ExpiresAt:        expires,
			RequestGroupID:   groupID,
			ActionType:       pgtype.Text{String: req.ActionType, Valid: true},
			ActionParams:     paramsCopy,
			CreatedByType:    "agent",
			CreatedByID:      agentUUID,
		})
		if err != nil {
			slog.Warn("create authorization link card failed", append(logger.RequestAttrs(r), "error", err)...)
			writeError(w, http.StatusInternalServerError, "failed to create authorization link card")
			return
		}
		card = &created
	}

	// Audit step 0: the request itself (best-effort — a lost audit row must
	// not fail a creation that asked for nothing).
	serviceAudit := auditEventForDecisionRequest(service.AuditAgentDecisionRequested, originRow,
		decisionRequestAuditDetails(originRow, map[string]any{"created_by": authorID}))
	serviceAudit.TriggerKind = strPtrOrNil(decisionRequestTriggerKind)
	serviceAudit.TriggerRef = strPtrOrNil(uuidToString(groupID))
	service.TryAppendAuditEvents(r.Context(), qtx, serviceAudit)

	if err := tx.Commit(r.Context()); err != nil {
		slog.Warn("create decision request commit failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to create decision request")
		return
	}

	resp := DecisionRequestDetailResponse{
		Request: decisionRequestToResponse(originRow),
		Steps:   make([]DecisionRequestResponse, 0, len(rows)),
	}
	if card != nil {
		cardResp := decisionToResponse(*card)
		resp.Card = &cardResp
	}
	for _, row := range rows {
		resp.Steps = append(resp.Steps, decisionRequestToResponse(row))
	}
	// Fan the creation out to every row's own workspace channel: the target
	// space's decision center learns of its step in real time, and each
	// space's clients only ever receive their own row (no cross-space read).
	for _, row := range rows {
		rowResp := decisionRequestToResponse(row)
		h.publish(protocol.EventDecisionRequestUpdated, uuidToString(row.WorkspaceID), "agent", authorID, map[string]any{
			"request":          rowResp,
			"request_group_id": rowResp.RequestGroupID,
		})
	}
	slog.Info("decision request created", append(logger.RequestAttrs(r),
		"group_id", uuidToString(groupID), "action_type", req.ActionType,
		"cross_space", crossSpace, "created_by", authorID)...)
	writeJSON(w, http.StatusCreated, resp)
}

// loadDecisionRequestForWorkspace fetches a request row scoped to the
// workspace in the URL — the tenant boundary for every read and write.
func (h *Handler) loadDecisionRequestForWorkspace(w http.ResponseWriter, r *http.Request, workspaceID pgtype.UUID) (db.DecisionRequest, bool) {
	requestID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "requestId"), "request id")
	if !ok {
		return db.DecisionRequest{}, false
	}
	row, err := h.Queries.GetDecisionRequest(r.Context(), db.GetDecisionRequestParams{
		ID: requestID, WorkspaceID: workspaceID,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "decision request not found")
			return db.DecisionRequest{}, false
		}
		writeError(w, http.StatusInternalServerError, "failed to load decision request")
		return db.DecisionRequest{}, false
	}
	return row, true
}

// GetDecisionRequest handles GET
// /api/workspaces/{id}/decision-requests/{requestId} — the authorization
// summary detail. Members of the workspace and the creating agent (CLI status
// polling for the no-issue flow) may read; the payload carries the group's
// step rows and never any issue body or comments.
func (h *Handler) GetDecisionRequest(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID, ok := parseUUIDOrBadRequest(w, h.resolveWorkspaceID(r), "workspace id")
	if !ok {
		return
	}
	row, ok := h.loadDecisionRequestForWorkspace(w, r, workspaceID)
	if !ok {
		return
	}
	authorType, authorID := h.resolveActor(r, userID, uuidToString(workspaceID))
	if authorType != "member" && uuidToString(row.CreatedByID) != authorID {
		writeError(w, http.StatusForbidden, "only workspace members or the creating agent can view this request")
		return
	}

	h.sweepDueDecisionRequests(r)

	group, err := h.Queries.GetDecisionRequestGroup(r.Context(), row.RequestGroupID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load decision request")
		return
	}
	resp := DecisionRequestDetailResponse{
		Request: decisionRequestToResponse(row),
		Steps:   make([]DecisionRequestResponse, 0, len(group)),
	}
	for _, step := range group {
		resp.Steps = append(resp.Steps, decisionRequestToResponse(step))
	}
	if row.OriginIssueID.Valid && uuidToString(row.WorkspaceID) == uuidToString(row.OriginWorkspaceID) {
		if card, err := h.Queries.GetAuthorizationCardForGroup(r.Context(), db.GetAuthorizationCardForGroupParams{
			RequestGroupID: row.RequestGroupID,
			WorkspaceID:    row.WorkspaceID,
		}); err == nil {
			cardResp := decisionToResponse(card)
			resp.Card = &cardResp
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// ListDecisionRequests handles GET /api/workspaces/{id}/decision-requests.
// Member-only: agents poll their own request by id instead. Runs the lazy
// expiry sweep first so due groups render their terminal state.
func (h *Handler) ListDecisionRequests(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := parseUUIDOrBadRequest(w, h.resolveWorkspaceID(r), "workspace id")
	if !ok {
		return
	}
	if _, ok := h.workspaceMember(w, r, uuidToString(workspaceID)); !ok {
		return
	}
	h.sweepDueDecisionRequests(r)

	limit := decisionReqListDefaultLim
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > decisionReqListMaxLim {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("limit must be between 1 and %d", decisionReqListMaxLim))
			return
		}
		limit = n
	}
	var status pgtype.Text
	if raw := r.URL.Query().Get("status"); raw != "" {
		switch raw {
		case decisionReqPending, decisionReqApproved, decisionReqDenied,
			decisionReqExpired, decisionReqRevoked, decisionReqExecuted, decisionReqExecuteFailed:
			status = pgtype.Text{String: raw, Valid: true}
		default:
			writeError(w, http.StatusBadRequest, "unknown status filter")
			return
		}
	}

	rows, err := h.Queries.ListDecisionRequestsForWorkspace(r.Context(), db.ListDecisionRequestsForWorkspaceParams{
		WorkspaceID: workspaceID,
		Limit:       int32(limit),
		Status:      status,
	})
	if err != nil {
		slog.Warn("decision request list failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to list decision requests")
		return
	}
	counts, err := h.Queries.CountWorkspaceDecisionRequestsByStatus(r.Context(), workspaceID)
	if err != nil {
		slog.Warn("decision request counts failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to count decision requests")
		return
	}
	items := make([]DecisionRequestResponse, 0, len(rows))
	for _, row := range rows {
		items = append(items, decisionRequestToResponse(row))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": items,
		"counts": map[string]int{
			"pending":  int(counts.PendingCount),
			"closed":   int(counts.ClosedCount),
			"executed": int(counts.ExecutedCount),
		},
	})
}

// sweepDueDecisionRequests runs the lazy expiry sweep (groups then their
// link cards) and posts at most one terminal callback per affected group.
// Best-effort everywhere: expiry converges on the next read even if the
// callback post fails.
func (h *Handler) sweepDueDecisionRequests(r *http.Request) {
	flipped, err := h.Queries.ExpireDueDecisionRequests(r.Context())
	if err != nil {
		slog.Warn("decision request expiry sweep failed", append(logger.RequestAttrs(r), "error", err)...)
		return
	}
	if _, err := h.Queries.ExpireDueDecisionCards(r.Context()); err != nil {
		slog.Warn("decision card expiry sweep failed", append(logger.RequestAttrs(r), "error", err)...)
	}
	if len(flipped) == 0 {
		return
	}
	seenGroups := make(map[pgtype.UUID]db.DecisionRequest, len(flipped))
	for _, row := range flipped {
		if row.Role == decisionReqRoleOrigin && !row.TerminalCallbackAt.Valid {
			seenGroups[row.RequestGroupID] = row
		}
	}
	for groupID, origin := range seenGroups {
		h.postDecisionRequestCallback(r, origin, decisionReqExpired, "", nil)
		h.Queries.MarkDecisionRequestCallbackDone(r.Context(), groupID)
		h.publishGroupUpdate(r, groupID)
	}
}

// publishGroupUpdate fans the freshest group state to both workspaces' WS
// channels (one event per row) — clients never read across spaces.
func (h *Handler) publishGroupUpdate(r *http.Request, groupID pgtype.UUID) {
	rows, err := h.Queries.GetDecisionRequestGroup(r.Context(), groupID)
	if err != nil {
		return
	}
	for _, row := range rows {
		resp := decisionRequestToResponse(row)
		h.publish(protocol.EventDecisionRequestUpdated, uuidToString(row.WorkspaceID), "system", "", map[string]any{
			"request":          resp,
			"request_group_id": resp.RequestGroupID,
		})
	}
}

// AnswerDecisionRequest handles POST
// /api/workspaces/{id}/decision-requests/{requestId}/answer. Human members
// only (route gate + handler re-check); the operable flag is the carrier's
// read-only-projection guard, and the tier check is the per-row gate.
func (h *Handler) AnswerDecisionRequest(w http.ResponseWriter, r *http.Request) {
	if isMachineCredentialActor(r) {
		writeError(w, http.StatusForbidden, "authorization requests can only be answered by human members")
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID, ok := parseUUIDOrBadRequest(w, h.resolveWorkspaceID(r), "workspace id")
	if !ok {
		return
	}
	authorType, authorID := h.resolveActor(r, userID, uuidToString(workspaceID))
	if authorType != "member" {
		writeError(w, http.StatusForbidden, "authorization requests can only be answered by human members")
		return
	}
	row, ok := h.loadDecisionRequestForWorkspace(w, r, workspaceID)
	if !ok {
		return
	}

	var req AnswerDecisionRequestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	var decision string
	switch req.Decision {
	case "approve":
		decision = decisionReqApproved
	case "deny":
		decision = decisionReqDenied
	default:
		writeError(w, http.StatusBadRequest, "decision must be approve or deny")
		return
	}

	// 只读投影拒绝作答：origin 行在跨空间与有 Issue 引用两种形态下都只是
	// 状态投影，授权动作只发生在声明档位内的 operable 行上。
	if !row.Operable {
		writeError(w, http.StatusForbidden, "this space's row is a read-only projection")
		return
	}
	member, ok := h.workspaceMember(w, r, uuidToString(workspaceID))
	if !ok {
		return
	}
	if !decisionRequestTierAllows(row.OperatorTier, parseUUIDJSONArray(row.NamedApproverIds), member.Role, userID) {
		writeError(w, http.StatusForbidden, "your role does not meet this request's operator tier")
		return
	}

	answered, err := h.Queries.AnswerDecisionRequest(r.Context(), db.AnswerDecisionRequestParams{
		ID:           row.ID,
		WorkspaceID:  workspaceID,
		Decision:     decision,
		AnsweredByID: parseUUID(userID),
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusConflict, "decision request is no longer actionable (already answered, expired, or revoked)")
			return
		}
		slog.Warn("answer decision request failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to answer decision request")
		return
	}

	// Audit the step that just landed (decision 2's two-step audit trail:
	// authorization events are separate from the execution events the
	// advance pass writes).
	service.TryAppendAuditEvents(r.Context(), h.Queries, auditEventForDecisionRequest(
		service.AuditAgentDecisionAuthorized, answered,
		decisionRequestAuditDetails(answered, map[string]any{
			"answered_by":   authorID,
			"answer_source": decisionSourceCardClick,
			"decision":      decision,
		})))

	// The response reports the step the caller just landed; the group advance
	// (and, on the final step, the whitelisted execution) runs after the body
	// is written — clients follow the group through decision_request:updated
	// WS events and the detail endpoint.
	writeJSON(w, http.StatusOK, decisionRequestToResponse(answered))

	h.advanceDecisionRequestAfterAnswer(r, answered.RequestGroupID, authorID)
}

// decisionRequestTierAllows applies the operator tier against the workspace
// member. Owner always may (spec's hard floor); named adds the explicit
// approver list; members lets any workspace member through.
func decisionRequestTierAllows(tier string, named []string, role, userID string) bool {
	if role == "owner" {
		return true
	}
	switch tier {
	case "owner":
		return false
	case "named":
		return containsUUIDString(named, userID)
	case "members":
		return true
	}
	return false
}

// CancelDecisionRequest handles POST
// /api/workspaces/{id}/decision-requests/{requestId}/cancel. The creating
// agent may retract its own pending request; a workspace Owner (of the space
// this row belongs to) may revoke it — 防僵尸卡。
func (h *Handler) CancelDecisionRequest(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID, ok := parseUUIDOrBadRequest(w, h.resolveWorkspaceID(r), "workspace id")
	if !ok {
		return
	}
	authorType, authorID := h.resolveActor(r, userID, uuidToString(workspaceID))
	row, ok := h.loadDecisionRequestForWorkspace(w, r, workspaceID)
	if !ok {
		return
	}

	allowed := authorType == "agent" && uuidToString(row.CreatedByID) == authorID
	if !allowed {
		if authorType == "member" {
			if member, err := h.getWorkspaceMember(r.Context(), userID, uuidToString(workspaceID)); err == nil && roleAllowed(member.Role, "owner") {
				allowed = true
			}
		}
	}
	if !allowed {
		writeError(w, http.StatusForbidden, "only the creating agent or a workspace owner can cancel this request")
		return
	}

	h.sweepDueDecisionRequests(r)

	revoked, err := h.Queries.CancelDecisionRequestGroup(r.Context(), row.RequestGroupID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to cancel decision request")
		return
	}
	if len(revoked) == 0 {
		writeError(w, http.StatusConflict, "decision request is no longer revocable")
		return
	}

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to cancel decision request")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)
	if _, err := qtx.SyncDecisionCardsForGroup(r.Context(), db.SyncDecisionCardsForGroupParams{
		AuthState:      decisionReqRevoked,
		RequestGroupID: row.RequestGroupID,
	}); err != nil {
		slog.Warn("sync authorization cards on revoke failed", append(logger.RequestAttrs(r), "error", err)...)
	}
	service.TryAppendAuditEvents(r.Context(), qtx, auditEventForDecisionRequest(
		service.AuditAgentDecisionRevoked, row,
		decisionRequestAuditDetails(row, map[string]any{"revoked_by": authorID, "revoked_by_type": authorType})))
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to cancel decision request")
		return
	}

	origin := revoked[0]
	for _, row := range revoked {
		if row.Role == decisionReqRoleOrigin {
			origin = row
		}
	}
	if !origin.TerminalCallbackAt.Valid {
		h.postDecisionRequestCallback(r, origin, decisionReqRevoked, "", nil)
		h.Queries.MarkDecisionRequestCallbackDone(r.Context(), origin.RequestGroupID)
	}
	h.publishGroupUpdate(r, row.RequestGroupID)
	writeJSON(w, http.StatusOK, map[string]any{"status": decisionReqRevoked})
}

// answerAuthorizationCard is the origin step's card-click surface: tier
// gate, same-transaction card CAS + origin projection-row sync, an echo
// that deliberately does NOT mention the agent (the requesting run wakes
// once, at terminal), then the group advance which evaluates the step
// condition and possibly executes.
func (h *Handler) answerAuthorizationCard(w http.ResponseWriter, r *http.Request, issue db.Issue, card db.IssueDecision, selected []int, authorID string) {
	if len(selected) != 1 || (selected[0] != 0 && selected[0] != 1) {
		writeError(w, http.StatusBadRequest, "authorization cards take exactly one of the two buttons (approve or deny)")
		return
	}
	decision := decisionReqApproved
	authState := "approved"
	if selected[0] == 1 {
		decision = decisionReqDenied
		authState = "denied"
	}

	member, ok := h.workspaceMember(w, r, uuidToString(issue.WorkspaceID))
	if !ok {
		return
	}
	if !decisionRequestTierAllows(card.OperatorTier, parseUUIDJSONArray(card.NamedApproverIds), member.Role, authorID) {
		writeError(w, http.StatusForbidden, "your role does not meet this authorization's operator tier")
		return
	}

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to answer authorization")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)

	selectedJSON, err := json.Marshal([]int{selected[0]})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to encode answer")
		return
	}
	answered, err := qtx.AnswerAuthorizationDecisionCard(r.Context(), db.AnswerAuthorizationDecisionCardParams{
		ID:              card.ID,
		WorkspaceID:     issue.WorkspaceID,
		AuthState:       authState,
		SelectedIndices: selectedJSON,
		AnsweredByID:    parseUUID(authorID),
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusConflict, "authorization window closed or already answered")
			return
		}
		slog.Warn("answer authorization card failed", append(logger.RequestAttrs(r), "error", err, "decision_id", uuidToString(card.ID))...)
		writeError(w, http.StatusInternalServerError, "failed to answer authorization")
		return
	}

	// Mirror the step onto the group's origin projection row in the SAME
	// transaction — card and row are one truth; a row already settled
	// (expired/revoked in a race) rolls the card answer back.
	synced, err := qtx.SyncOriginDecisionRequestRowAnswer(r.Context(), db.SyncOriginDecisionRequestRowAnswerParams{
		RequestGroupID: card.PendingRequestGroupID,
		Decision:       decision,
		AnsweredByID:   parseUUID(authorID),
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusConflict, "the authorization request is no longer pending")
			return
		}
		slog.Warn("sync origin row from card answer failed", append(logger.RequestAttrs(r), "error", err, "group_id", uuidToString(card.PendingRequestGroupID))...)
		writeError(w, http.StatusInternalServerError, "failed to answer authorization")
		return
	}

	if decision == decisionReqApproved {
		// Per-step authorization audit; the deny path's group-level
		// decision_denied event is written by the advance pass.
		service.TryAppendAuditEvents(r.Context(), qtx, auditEventForDecisionRequest(
			service.AuditAgentDecisionAuthorized, synced,
			decisionRequestAuditDetails(synced, map[string]any{
				"answered_by":   authorID,
				"answer_source": decisionSourceCardClick,
				"decision":      decision,
			})))
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to answer authorization")
		return
	}

	resp := decisionToResponse(answered)
	h.publish(protocol.EventDecisionUpdated, uuidToString(issue.WorkspaceID), "member", authorID, map[string]any{
		"decision":    resp,
		"issue_id":    uuidToString(issue.ID),
		"issue_title": issue.Title,
	})

	// Step echo: human-readable record, no mention, no trigger pass — the
	// requesting run must not wake before the terminal callback.
	h.postAuthorizationStepEcho(r, issue, card, decision, authorID)

	h.advanceDecisionRequestAfterAnswer(r, card.PendingRequestGroupID, authorID)
	writeJSON(w, http.StatusOK, resp)
}

// postAuthorizationStepEcho records the human's step decision on the thread
// as a plain member comment. Best-effort.
func (h *Handler) postAuthorizationStepEcho(r *http.Request, issue db.Issue, card db.IssueDecision, decision, authorID string) {
	label := card.ApproveLabel.String
	if decision == decisionReqDenied {
		label = card.DenyLabel.String
	}
	if label == "" {
		label = "同意"
		if decision == decisionReqDenied {
			label = "拒绝"
		}
	}
	content := fmt.Sprintf("授权请求已作答：「%s」→ %s", card.Question, label)
	created, err := h.Queries.CreateComment(r.Context(), db.CreateCommentParams{
		ID:          dbid.NewV7(),
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
		AuthorType:  "member",
		AuthorID:    parseUUID(authorID),
		Content:     content,
		Type:        "comment",
	})
	if err != nil {
		slog.Warn("authorization step echo failed", append(logger.RequestAttrs(r),
			"error", err, "decision_id", uuidToString(card.ID))...)
		return
	}
	comment := created.Comment()
	if _, err := h.Queries.SetIssueDecisionAnswerComment(r.Context(), db.SetIssueDecisionAnswerCommentParams{
		ID:              card.ID,
		WorkspaceID:     issue.WorkspaceID,
		AnswerCommentID: comment.ID,
	}); err != nil {
		slog.Warn("authorization step echo link failed", append(logger.RequestAttrs(r), "error", err)...)
	}
	commentResp := commentToResponse(comment, nil, nil)
	commentResp.IssueRevision = created.IssueRevision
	h.publish(protocol.EventCommentCreated, uuidToString(issue.WorkspaceID), "member", authorID, map[string]any{
		"comment":             commentResp,
		"issue_title":         issue.Title,
		"issue_assignee_type": textToPtr(issue.AssigneeType),
		"issue_assignee_id":   uuidToPtr(issue.AssigneeID),
		"issue_status":        issue.Status,
		"issue_revision":      created.IssueRevision,
	})
}
