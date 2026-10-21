package handler

// Decision-request group advance + platform executor (RUYI-630).
//
// advanceDecisionRequestAfterAnswer is the single funnel every approval or
// denial goes through, whichever surface produced it (target-space row
// answer, origin authorization link card). It serializes racing final steps
// on a group-level FOR UPDATE lock, evaluates the step condition, and on
// full approval runs the whitelisted action in an INTERNAL execution
// context: the executor reuses the same sqlc queries and guards the human
// endpoints use, but no mat_ token is issued and nothing crosses the
// RequireHumanActor surface. Both authorization and execution steps land in
// the 926 audit log as agent.decision_* events.

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/logger"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const decisionRequestTriggerKind = "decision_request"

// auditEventForDecisionRequest builds an agent-domain audit event anchored
// on the request's creating agent (agent-domain rows require agent_id) with
// the group id as trigger ref, so one query reconstructs a request's whole
// authorization + execution chain.
func auditEventForDecisionRequest(eventType string, row db.DecisionRequest, details []byte) service.Event {
	event := service.AgentEvent(eventType, service.AuditActorAgent, row.CreatedByID, row.WorkspaceID, row.OriginAgentID, details)
	groupID := uuidToString(row.RequestGroupID)
	event.TriggerKind = strPtrOrNil(decisionRequestTriggerKind)
	event.TriggerRef = strPtrOrNil(groupID)
	return event
}

func decisionRequestAuditDetails(row db.DecisionRequest, extra map[string]any) []byte {
	details := map[string]any{
		"request_group_id": uuidToString(row.RequestGroupID),
		"action_type":      row.ActionType,
		"risk_tier":        row.RiskTier,
		"row_role":         row.Role,
	}
	if row.OriginIssueID.Valid {
		details["origin_issue_id"] = uuidToString(row.OriginIssueID)
	}
	for k, v := range extra {
		details[k] = v
	}
	b, err := json.Marshal(details)
	if err != nil {
		return nil
	}
	return b
}

// authorizationCardOptionsJSON builds the link card's two options: index 0
// = approve, index 1 = deny, custom labels baked in. Exactly two buttons —
// authorization is binary, multi-select stays false.
func authorizationCardOptionsJSON(approve, deny *string) []byte {
	approveLabel := "同意"
	denyLabel := "拒绝"
	if approve != nil && *approve != "" {
		approveLabel = *approve
	}
	if deny != nil && *deny != "" {
		denyLabel = *deny
	}
	b, _ := json.Marshal([]DecisionOption{{Label: approveLabel}, {Label: denyLabel}})
	return b
}

func textPtrFromStr(s *string) pgtype.Text {
	if s == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *s, Valid: true}
}

// advanceDecisionRequestAfterAnswer evaluates the group after a CAS answer
// won on any surface. Terminal outcomes propagate to every row and card in
// one transaction, execute the action on full approval, and post the
// once-only terminal callback to the requesting run.
func (h *Handler) advanceDecisionRequestAfterAnswer(r *http.Request, groupID pgtype.UUID, answererID string) {
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		slog.Warn("decision request advance begin failed", append(logger.RequestAttrs(r), "error", err)...)
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)

	rows, err := qtx.GetDecisionRequestGroupForUpdate(r.Context(), groupID)
	if err != nil {
		slog.Warn("decision request advance lock failed", append(logger.RequestAttrs(r), "error", err)...)
		return
	}
	if len(rows) == 0 {
		return
	}
	origin := rows[0]
	for _, row := range rows {
		if row.Role == decisionReqRoleOrigin {
			origin = row
		}
	}
	// The action lands in the target space when the group has one (方案甲:
	// the target row's workspace IS where the approvers' consent applies);
	// single-space groups fall back to the origin row. Execution anchored on
	// the origin row instead would silently read the requesting space's
	// profile and make every cross-space restore fail containment.
	landing := origin
	for _, row := range rows {
		if row.Role == decisionReqRoleTarget {
			landing = row
			break
		}
	}

	// A denial anywhere settles the whole group as denied. No "already
	// terminal" early return here: a deny that just won its CAS has NOT been
	// propagated yet — this loop is what propagates it. Re-entry safety comes
	// from the checks below: allApproved requires every row approved, so an
	// executed/denied/expired/revoked row keeps the execution path out, and
	// the group FOR UPDATE lock serializes concurrent advances. A duplicate
	// entry (deny landed on the card and the row surface in the same instant)
	// flips nothing — finalize skips its audit and card sync, and the
	// once-only callback stays with the transaction that did the flipping.
	for _, row := range rows {
		if row.Status == decisionReqDenied {
			ownsTerminal := h.finalizeDecisionRequestGroup(r, qtx, rows, decisionReqDenied)
			if err := tx.Commit(r.Context()); err != nil {
				slog.Warn("decision request advance commit failed", append(logger.RequestAttrs(r), "error", err, "group_id", uuidToString(groupID))...)
				return
			}
			if ownsTerminal {
				h.afterDecisionRequestTerminal(r, origin, decisionReqDenied, "", nil)
			}
			return
		}
	}

	if err := validateDecisionRequestGroupSpec(rows); err != nil {
		slog.Warn("decision request specification validation failed", append(logger.RequestAttrs(r), "group_id", uuidToString(groupID), "error", err)...)
		return
	}

	// Still waiting on another step — stay pending, publish, no callback.
	allApproved := true
	for _, row := range rows {
		if row.Status != decisionReqApproved {
			allApproved = false
			break
		}
	}
	if !allApproved {
		if err := tx.Commit(r.Context()); err != nil {
			slog.Warn("decision request advance commit failed", append(logger.RequestAttrs(r), "error", err, "group_id", uuidToString(groupID))...)
			return
		}
		for _, row := range rows {
			resp := decisionRequestToResponse(row)
			h.publish(protocol.EventDecisionRequestUpdated, uuidToString(row.WorkspaceID), "member", answererID, map[string]any{
				"request":          resp,
				"request_group_id": resp.RequestGroupID,
			})
		}
		return
	}

	// Every step approved: run the whitelisted action in this same
	// transaction (the group lock already serializes us against any other
	// final-step approval). The landing row names the workspace the action
	// executes in; its ActionParams are byte-identical to the origin row's.
	if err := qtx.CreateDecisionRequestActionSavepoint(r.Context()); err != nil {
		slog.Warn("decision request action savepoint failed", append(logger.RequestAttrs(r), "error", err, "group_id", uuidToString(groupID))...)
		return
	}
	resultJSON, execErr := h.executeDecisionRequestAction(r, qtx, landing)
	if execErr != nil {
		if err := qtx.RollbackDecisionRequestAction(r.Context()); err != nil {
			slog.Warn("decision request action rollback failed", append(logger.RequestAttrs(r), "error", err, "group_id", uuidToString(groupID))...)
			return
		}
	}
	if err := qtx.ReleaseDecisionRequestActionSavepoint(r.Context()); err != nil {
		slog.Warn("decision request action savepoint release failed", append(logger.RequestAttrs(r), "error", err, "group_id", uuidToString(groupID))...)
		return
	}
	status := decisionReqExecuted
	executedAt := pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}
	var resultBytes []byte
	var execErrText pgtype.Text
	if execErr != nil {
		status = decisionReqExecuteFailed
		execErrText = pgtype.Text{String: decisionRequestSafeErrorCode(landing.ActionType), Valid: true}
	}
	resultBytes = decisionRequestReceipt(landing.ActionType, resultJSON, execErr == nil, execErr != nil)

	updated, err := qtx.SetDecisionRequestExecution(r.Context(), db.SetDecisionRequestExecutionParams{
		Status:          status,
		ExecutedAt:      executedAt,
		ExecutionResult: resultBytes,
		ExecutionError:  execErrText,
		RequestGroupID:  groupID,
	})
	if err != nil {
		slog.Warn("decision request execution write failed", append(logger.RequestAttrs(r), "error", err, "group_id", uuidToString(groupID))...)
		return
	}
	if _, err := qtx.SetAuthorizationCardExecution(r.Context(), db.SetAuthorizationCardExecutionParams{
		AuthState:       status,
		ExecutedAt:      executedAt,
		ExecutionResult: resultBytes,
		ExecutionError:  execErrText,
		RequestGroupID:  groupID,
	}); err != nil {
		slog.Warn("decision card execution sync failed", append(logger.RequestAttrs(r), "error", err)...)
	}

	execRow := origin
	if len(updated) > 0 {
		execRow = updated[0]
	}
	eventType := service.AuditAgentDecisionExecuted
	if execErr != nil {
		eventType = service.AuditAgentDecisionExecuteFailed
	}
	auditDetails := map[string]any{"answered_by": answererID}
	if execErr == nil && landing.ActionType == DecisionActionProjectPromptSync {
		var summary struct {
			ContentSHA256 string           `json:"content_sha256"`
			Projects      []map[string]any `json:"projects"`
		}
		if json.Unmarshal(resultJSON, &summary) == nil {
			auditDetails["content_sha256"] = summary.ContentSHA256
			auditDetails["projects"] = summary.Projects
		}
	}
	service.TryAppendAuditEvents(r.Context(), qtx, auditEventForDecisionRequest(eventType, execRow, decisionRequestAuditDetails(execRow, auditDetails)))
	// The callback marker rides the execution transaction: the group lock
	// makes the locked-read marker state authoritative, so a concurrent
	// terminal path can never double-post the once-only callback.
	ownsTerminal := !origin.TerminalCallbackAt.Valid
	if ownsTerminal {
		if err := qtx.MarkDecisionRequestCallbackDone(r.Context(), groupID); err != nil {
			slog.Warn("decision request callback mark failed", append(logger.RequestAttrs(r), "error", err)...)
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		slog.Warn("decision request advance commit failed", append(logger.RequestAttrs(r), "error", err, "group_id", uuidToString(groupID))...)
		return
	}

	var errPtr *string
	if execErr != nil {
		s := decisionRequestSafeErrorCode(landing.ActionType)
		errPtr = &s
	}
	if ownsTerminal {
		h.afterDecisionRequestTerminal(r, execRow, status, string(resultJSON), errPtr)
		return
	}
	h.publishGroupUpdate(r, groupID)
}

func decisionRequestSafeErrorCode(actionType string) string {
	switch actionType {
	case DecisionActionProjectPromptSync:
		return "PROMPT_SYNC_FAILED"
	case DecisionActionPromptRestore:
		return "PROMPT_RESTORE_FAILED"
	case DecisionActionWorkspaceInfoRead:
		return "WORKSPACE_READ_FAILED"
	default:
		return "EXECUTION_FAILED"
	}
}

func decisionRequestReceipt(actionType string, result []byte, completed, failed bool) []byte {
	var receipt map[string]any
	if json.Unmarshal(result, &receipt) != nil || receipt == nil {
		receipt = map[string]any{}
	}
	sideEffect := "no_side_effect"
	stepStatus := "completed"
	if completed && actionType != DecisionActionWorkspaceInfoRead {
		sideEffect = "completed"
	}
	if failed {
		stepStatus = "failed"
	}
	receipt["receipt_version"] = 1
	receipt["side_effect_status"] = sideEffect
	receipt["steps"] = []map[string]string{{
		"handler":            actionType,
		"status":             stepStatus,
		"side_effect_status": sideEffect,
	}}
	encoded, err := json.Marshal(receipt)
	if err != nil {
		return []byte(`{"receipt_version":1,"side_effect_status":"unknown","steps":[]}`)
	}
	return encoded
}

// finalizeDecisionRequestGroup propagates a terminal state (denied today;
// expiry and revoke converge on the same shape) onto every row and pending
// card of the group, with the audit row and the once-only callback marker,
// inside the caller's transaction. Returns false for a duplicate finalize:
// the callback marker is written inside the terminal-flipping transaction,
// so a set marker on the locked read means another path already settled the
// group — audit, card sync and callback stay with that transaction. (The
// propagate row count cannot be the signal: on the single-space card-deny
// path the origin row is already denied by the answer-transaction sync, and
// the primary finalize legitimately flips zero rows.)
func (h *Handler) finalizeDecisionRequestGroup(r *http.Request, qtx *db.Queries, rows []db.DecisionRequest, status string) bool {
	for _, row := range rows {
		if row.TerminalCallbackAt.Valid {
			return false
		}
	}
	if _, err := qtx.PropagateDecisionRequestGroup(r.Context(), db.PropagateDecisionRequestGroupParams{
		Status:         status,
		RequestGroupID: rows[0].RequestGroupID,
	}); err != nil {
		slog.Warn("decision request propagation failed", append(logger.RequestAttrs(r), "error", err, "status", status)...)
		return false
	}
	if err := qtx.MarkDecisionRequestCallbackDone(r.Context(), rows[0].RequestGroupID); err != nil {
		slog.Warn("decision request callback mark failed", append(logger.RequestAttrs(r), "error", err)...)
	}
	if _, err := qtx.SyncDecisionCardsForGroup(r.Context(), db.SyncDecisionCardsForGroupParams{
		AuthState:      status,
		RequestGroupID: rows[0].RequestGroupID,
	}); err != nil {
		slog.Warn("decision card sync failed", append(logger.RequestAttrs(r), "error", err, "status", status)...)
	}
	eventType := service.AuditAgentDecisionDenied
	if status == decisionReqRevoked {
		eventType = service.AuditAgentDecisionRevoked
	}
	service.TryAppendAuditEvents(r.Context(), qtx, auditEventForDecisionRequest(eventType, rows[0], decisionRequestAuditDetails(rows[0], nil)))
	return true
}

// afterDecisionRequestTerminal posts the once-only callback to the
// requesting run and fans the fresh group state out over both workspaces'
// WS channels. The caller owns callback uniqueness — it invokes this only
// when its transaction is the one that flipped the group terminal (the
// marker was written inside that same transaction, so racing paths see it
// under the group lock). Callback = system comment on the origin issue
// mentioning the creating agent, run through the standard mention pipeline
// with the agent's owner as the effective human — the consent the group
// captured was that owner's, so the wake acts for them. No issue reference
// means no thread: the run polls GET /decision-requests/{id} (creator
// allowed) and the WS event is the push signal.
func (h *Handler) afterDecisionRequestTerminal(r *http.Request, origin db.DecisionRequest, status, resultJSON string, execErr *string) {
	h.postDecisionRequestCallback(r, origin, status, resultJSON, execErr)
	h.publishGroupUpdate(r, origin.RequestGroupID)
}

// postDecisionRequestCallback writes the terminal-state echo comment and
// wakes the creating agent through the mention pipeline. Best-effort: the
// group state is already committed, a lost callback leaves the state
// readable by polling.
func (h *Handler) postDecisionRequestCallback(r *http.Request, origin db.DecisionRequest, status, resultJSON string, execErr *string) {
	if !origin.OriginIssueID.Valid {
		return
	}
	issue, err := h.Queries.GetIssueInWorkspace(r.Context(), db.GetIssueInWorkspaceParams{
		ID: origin.OriginIssueID, WorkspaceID: origin.OriginWorkspaceID,
	})
	if err != nil {
		slog.Warn("decision request callback issue missing", append(logger.RequestAttrs(r),
			"error", err, "issue_id", uuidToString(origin.OriginIssueID))...)
		return
	}

	content := h.buildDecisionRequestCallbackContent(r, origin, status, resultJSON, execErr)
	if content == "" {
		return
	}
	created, err := h.Queries.CreateComment(r.Context(), db.CreateCommentParams{
		ID:          dbid.NewV7(),
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
		AuthorType:  "system",
		AuthorID:    pgtype.UUID{Valid: true},
		Content:     content,
		Type:        "system",
		ParentID:    pgtype.UUID{Valid: false},
	})
	if err != nil {
		slog.Warn("decision request callback comment failed", append(logger.RequestAttrs(r),
			"error", err, "group_id", uuidToString(origin.RequestGroupID))...)
		return
	}
	comment := created.Comment()
	h.publish(protocol.EventCommentCreated, uuidToString(issue.WorkspaceID), "system", "", map[string]any{
		"comment":             commentToResponse(comment, nil, nil),
		"issue_title":         issue.Title,
		"issue_assignee_type": textToPtr(issue.AssigneeType),
		"issue_assignee_id":   uuidToPtr(issue.AssigneeID),
		"issue_status":        issue.Status,
		"issue_revision":      created.IssueRevision,
	})

	// Wake the creator through the standard pipeline, acting for the agent's
	// owner — the human whose consent this whole channel encodes. A deleted
	// or owner-less agent simply loses the wake; state stays pollable.
	ownerID := ""
	if agent, err := h.Queries.GetAgent(r.Context(), origin.OriginAgentID); err == nil {
		ownerID = uuidToString(agent.OwnerID)
	}
	if ownerID != "" {
		h.triggerTasksForComment(r.Context(), issue, comment, nil, "member", ownerID, ownerID, nil)
	}
}

// buildDecisionRequestCallbackContent renders the structured terminal-state
// card the requesting run reads: machine-parseable head line + human
// summary lines. The answer never arrives as free text.
func (h *Handler) buildDecisionRequestCallbackContent(r *http.Request, origin db.DecisionRequest, status, resultJSON string, execErr *string) string {
	head := ""
	if agent, err := h.Queries.GetAgent(r.Context(), origin.OriginAgentID); err == nil && agent.Name != "" {
		head = fmt.Sprintf("[@%s](mention://agent/%s) ", agent.Name, uuidToString(origin.OriginAgentID))
	}
	head += fmt.Sprintf("授权请求终态 [%s]（decision_request:%s，action=%s）", status, uuidToString(origin.RequestGroupID), origin.ActionType)

	var body strings.Builder
	body.WriteString(head + "\n")
	switch status {
	case decisionReqExecuted:
		body.WriteString("- 执行结果：已由平台代执行完成\n")
		if resultJSON != "" && len(resultJSON) < 800 {
			body.WriteString("- 详情：" + resultJSON + "\n")
		}
	case decisionReqExecuteFailed:
		body.WriteString("- 执行结果：平台代执行失败\n")
		if execErr != nil && *execErr != "" {
			body.WriteString("- 失败原因：" + *execErr + "\n")
		}
	case decisionReqDenied:
		body.WriteString("- 结果：授权被拒绝，未执行\n")
	case decisionReqExpired:
		body.WriteString("- 结果：授权窗口已过期，未执行\n")
	case decisionReqRevoked:
		body.WriteString("- 结果：授权已被撤回，未执行\n")
	default:
		return ""
	}
	return body.String()
}

// ── Platform executor ───────────────────────────────────────────────────────

// decisionRestoreParams is prompt_restore's parameter contract.
type decisionRestoreParams struct {
	TargetType string `json:"target_type"`
	TargetID   string `json:"target_id"`
}

// decisionWorkspaceInfoParams is workspace_info_read's parameter contract.
// Phase 1 carries no selectors — the read is the target workspace's own
// profile. The type exists so a later widening is a registry change, not a
// protocol one.
type decisionWorkspaceInfoParams struct {
	IncludeCounts bool `json:"include_counts,omitempty"`
}

const maxProjectPromptSyncTargets = 50

type decisionProjectPromptSyncParams struct {
	ProjectIDs    []string `json:"project_ids"`
	Content       string   `json:"content"`
	ContentSHA256 string   `json:"content_sha256"`
}

func normalizeProjectPromptSyncParams(raw json.RawMessage) (json.RawMessage, error) {
	var params decisionProjectPromptSyncParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, errors.New("project_prompt_sync params must be an object")
	}
	if len(params.ProjectIDs) == 0 || len(params.ProjectIDs) > maxProjectPromptSyncTargets {
		return nil, fmt.Errorf("project_ids must contain between 1 and %d projects", maxProjectPromptSyncTargets)
	}
	if len([]rune(params.Content)) > maxProjectInstructionsLen {
		return nil, fmt.Errorf("content exceeds the maximum length of %d characters", maxProjectInstructionsLen)
	}
	if params.ContentSHA256 == "" || params.ContentSHA256 != sha256Hex(params.Content) {
		return nil, errors.New("content_sha256 does not match content")
	}
	ids := make([]string, 0, len(params.ProjectIDs))
	seen := make(map[string]struct{}, len(params.ProjectIDs))
	for _, rawID := range params.ProjectIDs {
		id, err := util.ParseUUID(rawID)
		if err != nil {
			return nil, errors.New("project_ids must contain only UUIDs")
		}
		canonical := uuidToString(id)
		if _, ok := seen[canonical]; ok {
			return nil, errors.New("project_ids must not contain duplicates")
		}
		seen[canonical] = struct{}{}
		ids = append(ids, canonical)
	}
	sort.Strings(ids)
	params.ProjectIDs = ids
	return json.Marshal(params)
}

// executeDecisionRequestAction dispatches the whitelisted action inside the
// caller's transaction. The operable row names the action, its params, and
// the workspace the action lands in; the registry's closed set is the only
// door.
func (h *Handler) executeDecisionRequestAction(r *http.Request, qtx *db.Queries, row db.DecisionRequest) ([]byte, error) {
	switch row.ActionType {
	case DecisionActionWorkspaceInfoRead:
		return h.executeWorkspaceInfoRead(r, qtx, row)
	case DecisionActionPromptRestore:
		return h.executePromptRestore(r, qtx, row)
	case DecisionActionProjectPromptSync:
		return h.executeProjectPromptSync(r, qtx, row)
	default:
		return nil, fmt.Errorf("action %q is not in the whitelist registry", row.ActionType)
	}
}

func (h *Handler) executeProjectPromptSync(r *http.Request, qtx *db.Queries, row db.DecisionRequest) ([]byte, error) {
	raw, err := normalizeProjectPromptSyncParams(row.ActionParams)
	if err != nil {
		return nil, err
	}
	var params decisionProjectPromptSyncParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, err
	}
	scan, ok, badReason := promptGovernanceGate(params.Content)
	if badReason != "" {
		return nil, fmt.Errorf("prompt rejected: %s", badReason)
	}
	if !ok {
		return nil, errors.New("prompt rejected by the secret scanner")
	}

	projectIDs := make([]pgtype.UUID, len(params.ProjectIDs))
	for i, id := range params.ProjectIDs {
		projectIDs[i], err = util.ParseUUID(id)
		if err != nil {
			return nil, errors.New("project_ids must contain only UUIDs")
		}
		if _, err := qtx.LockProjectForPromptVersion(r.Context(), db.LockProjectForPromptVersionParams{
			ID: projectIDs[i], WorkspaceID: row.WorkspaceID,
		}); err != nil {
			return nil, fmt.Errorf("project %s not found in the target workspace", id)
		}
	}

	gateResult, err := json.Marshal(scan.Findings)
	if err != nil {
		return nil, errors.New("failed to encode prompt scan result")
	}
	results := make([]map[string]any, 0, len(projectIDs))
	for i, projectID := range projectIDs {
		next, err := qtx.NextPromptVersion(r.Context(), db.NextPromptVersionParams{
			Scope: string(promptVersionScopeProject), ScopeID: projectID,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to allocate prompt version for project %s", params.ProjectIDs[i])
		}
		if _, err := qtx.CreatePromptVersion(r.Context(), db.CreatePromptVersionParams{
			WorkspaceID:       row.WorkspaceID,
			Scope:             string(promptVersionScopeProject),
			ScopeID:           projectID,
			Version:           next,
			Content:           params.Content,
			ContentSha256:     params.ContentSHA256,
			Source:            "edit",
			ChangeNote:        "跨空间 Owner 授权的项目提示词同步",
			ScannerRevision:   scan.Revision,
			GateResult:        gateResult,
			AuthorUserID:      row.AnsweredByID,
			AuthorNoteIssueID: pgtype.UUID{},
		}); err != nil {
			return nil, fmt.Errorf("failed to record prompt version for project %s", params.ProjectIDs[i])
		}
		if _, err := qtx.UpdateProjectInstructionsForPromptVersion(r.Context(), db.UpdateProjectInstructionsForPromptVersionParams{
			ID: projectID, Instructions: strToText(params.Content),
		}); err != nil {
			return nil, fmt.Errorf("failed to update project %s", params.ProjectIDs[i])
		}
		results = append(results, map[string]any{
			"project_id":     params.ProjectIDs[i],
			"status":         "updated",
			"version":        next,
			"content_sha256": params.ContentSHA256,
		})
	}
	return json.Marshal(map[string]any{
		"action":         DecisionActionProjectPromptSync,
		"content_sha256": params.ContentSHA256,
		"projects":       results,
	})
}

// executeWorkspaceInfoRead answers with the target workspace's basic
// profile — the same fields any member of that space can already see.
func (h *Handler) executeWorkspaceInfoRead(r *http.Request, qtx *db.Queries, row db.DecisionRequest) ([]byte, error) {
	var params decisionWorkspaceInfoParams
	if len(row.ActionParams) > 0 {
		if err := json.Unmarshal(row.ActionParams, &params); err != nil {
			return nil, fmt.Errorf("invalid action params: %w", err)
		}
	}
	ws, err := qtx.GetWorkspace(r.Context(), row.WorkspaceID)
	if err != nil {
		return nil, fmt.Errorf("target workspace not found: %w", err)
	}
	return json.Marshal(map[string]any{
		"workspace": map[string]any{
			"id":          uuidToString(ws.ID),
			"name":        ws.Name,
			"description": textToPtr(ws.Description),
		},
	})
}

// executePromptRestore mirrors the human restore endpoint's core — lock the
// target FOR UPDATE, refuse on the hand-edit guard, write content and apply
// state atomically — but in the platform's internal context: no HTTP
// surface, no mat_ token, authorized solely by this approved request. The
// target must live in the workspace the request names.
func (h *Handler) executePromptRestore(r *http.Request, qtx *db.Queries, row db.DecisionRequest) ([]byte, error) {
	var params decisionRestoreParams
	if err := json.Unmarshal(row.ActionParams, &params); err != nil {
		return nil, fmt.Errorf("invalid action params: %w", err)
	}
	targetType := strings.TrimSpace(params.TargetType)
	if targetType != promptSourceAgent && targetType != promptSourceSquad {
		return nil, fmt.Errorf("target_type must be %q or %q", promptSourceAgent, promptSourceSquad)
	}
	targetID, err := util.ParseUUID(params.TargetID)
	if err != nil {
		return nil, errors.New("target_id must be a UUID")
	}

	locked, ok := h.lockPromptTarget(r, qtx, targetType, targetID)
	if !ok {
		return nil, errors.New("prompt target not found")
	}
	if uuidToString(locked.workspaceID) != uuidToString(row.WorkspaceID) {
		return nil, errors.New("prompt target belongs to a different workspace than the request")
	}
	state := locked.state
	if state.AppliedContentSha256 == "" || !state.canRestore() {
		return nil, errors.New("there is no applied marketplace prompt to undo on this target")
	}
	// The hand-edit guard — identical to the human endpoint: restoring would
	// silently discard a post-apply manual edit, so it refuses instead.
	currentHash := sha256Hex(locked.content)
	if currentHash != state.AppliedContentSha256 {
		return nil, errors.New("prompt target was modified after the marketplace apply; refusing to overwrite")
	}

	restored := promptApplyState{
		InstallID:            state.InstallID,
		VersionID:            state.VersionID,
		SeriesID:             state.SeriesID,
		AppliedVersion:       state.AppliedVersion,
		AppliedContentSha256: sha256Hex(state.PreviousText),
		PreviousText:         "",
		PreviousUpdatedAt:    "",
		HasRestorePoint:      false,
		AppliedBy:            state.AppliedBy,
		AppliedAt:            state.AppliedAt,
		LastOperationID:      "decision_request:" + uuidToString(row.RequestGroupID),
		Restored:             true,
	}
	blob, err := encodePromptApplyState(restored)
	if err != nil {
		return nil, fmt.Errorf("failed to encode restored state: %w", err)
	}
	if !h.writePromptToTarget(r, qtx, targetType, targetID, state.PreviousText, blob) {
		return nil, errors.New("failed to write restored prompt")
	}
	slog.Info("decision request executed prompt restore", append(logger.RequestAttrs(r),
		"group_id", uuidToString(row.RequestGroupID),
		"target_type", targetType, "target_id", uuidToString(targetID))...)
	return json.Marshal(map[string]any{
		"action":         DecisionActionPromptRestore,
		"target_type":    targetType,
		"target_id":      uuidToString(targetID),
		"content_sha256": restored.AppliedContentSha256,
		"restored":       true,
	})
}
