package handler

// Audit event query surface (RUYI-355 Phase 1). REST and the MCP
// search_audit_events tool both land here — the MCP client calls this
// endpoint, so one handler and one sqlc query serve both surfaces and a
// filter fix applies to both at once.

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const (
	auditEventsDefaultLimit = 50
	auditEventsMaxLimit     = 200
)

// auditEventDTO is the wire shape of one audit_event row. Nullable dimensions
// serialize as null so a timeline renders absence instead of zero UUIDs.
// occurred_at is RFC3339Nano: clients page with it as the keyset cursor, so it
// must keep the same sub-second precision as next_cursor.
type auditEventDTO struct {
	ID           string          `json:"id"`
	WorkspaceID  string          `json:"workspace_id"`
	Domain       string          `json:"domain"`
	EventType    string          `json:"event_type"`
	OccurredAt   *string         `json:"occurred_at"`
	ActorType    string          `json:"actor_type"`
	ActorID      *string         `json:"actor_id"`
	TriggerKind  *string         `json:"trigger_kind"`
	TriggerRef   *string         `json:"trigger_ref"`
	IssueID      *string         `json:"issue_id"`
	TaskID       *string         `json:"task_id"`
	AgentID      *string         `json:"agent_id"`
	RuntimeID    *string         `json:"runtime_id"`
	Reason       *string         `json:"reason"`
	InvocationID *string         `json:"invocation_id,omitempty"`
	Sensitive    bool            `json:"sensitive"`
	Details      json.RawMessage `json:"details"`
}

// optionalUUIDStr renders a nullable uuid dimension: invalid becomes null,
// valid becomes its string form.
func optionalUUIDStr(id pgtype.UUID) *string {
	if !id.Valid {
		return nil
	}
	s := uuidToString(id)
	return &s
}

func auditEventToDTO(e db.AuditEvent) auditEventDTO {
	details := json.RawMessage(e.Details)
	if len(details) == 0 {
		details = json.RawMessage("{}")
	}
	return auditEventDTO{
		ID:           uuidToString(e.ID),
		WorkspaceID:  uuidToString(e.WorkspaceID),
		Domain:       e.Domain,
		EventType:    e.EventType,
		OccurredAt:   timestampToNanoPtr(e.OccurredAt),
		ActorType:    e.ActorType,
		ActorID:      optionalUUIDStr(e.ActorID),
		TriggerKind:  textToPtr(e.TriggerKind),
		TriggerRef:   textToPtr(e.TriggerRef),
		IssueID:      optionalUUIDStr(e.IssueID),
		TaskID:       optionalUUIDStr(e.TaskID),
		AgentID:      optionalUUIDStr(e.AgentID),
		RuntimeID:    optionalUUIDStr(e.RuntimeID),
		Reason:       textToPtr(e.Reason),
		InvocationID: optionalUUIDStr(e.InvocationID),
		Sensitive:    e.Sensitive,
		Details:      details,
	}
}

// listAuditEventsParams carries the shared filter parsing for the workspace-
// and issue-level endpoints. Invalid values are rejected rather than skipped:
// a typo'd filter silently widening the result set is worse than a 400.
type listAuditEventsParams struct {
	domain       pgtype.Text
	eventType    pgtype.Text
	actorType    pgtype.Text
	actorID      pgtype.UUID
	issueID      pgtype.UUID
	taskID       pgtype.UUID
	agentID      pgtype.UUID
	runtimeID    pgtype.UUID
	reason       pgtype.Text
	invocationID pgtype.UUID
	timeBasis    pgtype.Text
	clientName   pgtype.Text
	clientVer    pgtype.Text
	toolName     pgtype.Text
	resultStatus pgtype.Text
	resultCode   pgtype.Text
	targetType   pgtype.Text
	targetID     pgtype.Text
	filterStart  bool
	since        pgtype.Timestamptz
	until        pgtype.Timestamptz
	limit        int32
	cursor       pgtype.Timestamptz
	cursorID     pgtype.UUID
}

func textParamIfSet(v string) pgtype.Text {
	if v == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: v, Valid: true}
}

func uuidParam(w http.ResponseWriter, name, raw string) (pgtype.UUID, bool) {
	if raw == "" {
		return pgtype.UUID{}, true
	}
	id, err := util.ParseUUID(raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid "+name+" parameter; expected uuid")
		return pgtype.UUID{}, false
	}
	return id, true
}

func parseListAuditEventsParams(w http.ResponseWriter, r *http.Request) (listAuditEventsParams, bool) {
	q := r.URL.Query()
	var p listAuditEventsParams

	p.domain = textParamIfSet(q.Get("domain"))
	p.eventType = textParamIfSet(q.Get("event_type"))
	p.actorType = textParamIfSet(q.Get("actor_type"))
	p.reason = textParamIfSet(q.Get("reason"))
	if raw := q.Get("time_basis"); raw != "" {
		if raw != "invocation_start" {
			writeError(w, http.StatusBadRequest, "invalid time_basis parameter")
			return p, false
		}
		p.timeBasis = pgtype.Text{String: raw, Valid: true}
	}
	for name, max := range map[string]int{"client_name": 128, "client_version": 64, "tool_name": 64} {
		value := q.Get(name)
		if len(value) > max || strings.IndexFunc(value, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
			writeError(w, http.StatusBadRequest, "invalid "+name+" parameter")
			return p, false
		}
	}
	p.clientName = textParamIfSet(q.Get("client_name"))
	p.clientVer = textParamIfSet(q.Get("client_version"))
	p.toolName = textParamIfSet(q.Get("tool_name"))
	p.resultStatus = textParamIfSet(q.Get("result_status"))
	p.resultCode = textParamIfSet(q.Get("result_error_code"))
	p.targetType = textParamIfSet(q.Get("target_type"))
	p.targetID = textParamIfSet(q.Get("target_id"))
	if p.resultStatus.Valid && p.resultStatus.String != "success" && p.resultStatus.String != "failure" && p.resultStatus.String != "refused" {
		writeError(w, http.StatusBadRequest, "invalid result_status parameter")
		return p, false
	}
	if p.resultCode.Valid && p.resultCode.String != "invalid_input" && p.resultCode.String != "not_found" &&
		p.resultCode.String != "permission_denied" && p.resultCode.String != "unavailable" &&
		p.resultCode.String != "timeout" && p.resultCode.String != "internal" {
		writeError(w, http.StatusBadRequest, "invalid result_error_code parameter")
		return p, false
	}
	if p.targetType.Valid && p.targetType.String != "workspace" && p.targetType.String != "issue" && p.targetType.String != "host" {
		writeError(w, http.StatusBadRequest, "invalid target_type parameter")
		return p, false
	}
	if len(q.Get("target_id")) > 128 || strings.IndexFunc(q.Get("target_id"), func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
		writeError(w, http.StatusBadRequest, "invalid target_id parameter")
		return p, false
	}
	if p.targetType.Valid != p.targetID.Valid {
		writeError(w, http.StatusBadRequest, "target_type and target_id must be provided together")
		return p, false
	}
	p.filterStart = p.clientName.Valid || p.clientVer.Valid || p.toolName.Valid || p.targetType.Valid

	var ok bool
	if p.actorID, ok = uuidParam(w, "actor_id", q.Get("actor_id")); !ok {
		return p, false
	}
	if p.issueID, ok = uuidParam(w, "issue_id", q.Get("issue_id")); !ok {
		return p, false
	}
	if p.taskID, ok = uuidParam(w, "task_id", q.Get("task_id")); !ok {
		return p, false
	}
	if p.agentID, ok = uuidParam(w, "agent_id", q.Get("agent_id")); !ok {
		return p, false
	}
	if p.runtimeID, ok = uuidParam(w, "runtime_id", q.Get("runtime_id")); !ok {
		return p, false
	}
	if p.invocationID, ok = uuidParam(w, "invocation_id", q.Get("invocation_id")); !ok {
		return p, false
	}

	if raw := q.Get("since"); raw != "" {
		t, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid since parameter; expected RFC3339 timestamp")
			return p, false
		}
		p.since = pgtype.Timestamptz{Time: t, Valid: true}
	}
	if raw := q.Get("until"); raw != "" {
		t, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid until parameter; expected RFC3339 timestamp")
			return p, false
		}
		p.until = pgtype.Timestamptz{Time: t, Valid: true}
	}
	if p.timeBasis.Valid && (p.since.Valid || p.until.Valid || p.actorID.Valid || p.actorType.Valid) {
		p.filterStart = true
	}

	p.limit = auditEventsDefaultLimit
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > auditEventsMaxLimit {
			writeError(w, http.StatusBadRequest, "invalid limit parameter; expected 1.."+strconv.Itoa(auditEventsMaxLimit))
			return p, false
		}
		p.limit = int32(n)
	}

	if raw := q.Get("cursor"); raw != "" {
		t, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid cursor parameter; expected RFC3339 timestamp")
			return p, false
		}
		p.cursor = pgtype.Timestamptz{Time: t, Valid: true}
	}
	// A cursor timestamp without its tiebreaker id silently skips or repeats
	// rows sharing that timestamp — reject the pair rather than guess.
	if raw := q.Get("cursor_id"); raw != "" {
		id, err := util.ParseUUID(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid cursor_id parameter; expected uuid")
			return p, false
		}
		if !p.cursor.Valid {
			writeError(w, http.StatusBadRequest, "cursor_id requires cursor")
			return p, false
		}
		p.cursorID = id
	} else if p.cursor.Valid {
		writeError(w, http.StatusBadRequest, "cursor requires cursor_id")
		return p, false
	}

	return p, true
}

// ListAuditEvents serves GET /api/workspaces/{id}/audit-events — the
// workspace-scoped audit search. The route sits behind the workspace member
// middleware; every audit row is workspace-scoped by construction.
func (h *Handler) ListAuditEvents(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := parseUUIDOrBadRequest(w, workspaceIDFromURL(r, "id"), "workspace id")
	if !ok {
		return
	}
	member, ok := h.requireWorkspaceMember(w, r, uuidToString(workspaceID), "workspace not found")
	if !ok {
		return
	}
	p, ok := parseListAuditEventsParams(w, r)
	if !ok {
		return
	}

	rows, err := h.Queries.ListAuditEvents(r.Context(), db.ListAuditEventsParams{
		WorkspaceID:     workspaceID,
		Limit:           p.limit,
		Since:           p.since,
		Until:           p.until,
		FilterDomain:    p.domain,
		FilterEventType: p.eventType,
		FilterActorType: p.actorType,
		FilterActorID:   p.actorID,
		// The MCP search_audit_events tool passes issue_id through this
		// endpoint, so the filter must bite here — leaving it open silently
		// widened the MCP result to the whole workspace trail.
		FilterIssueID:         p.issueID,
		FilterTaskID:          p.taskID,
		FilterAgentID:         p.agentID,
		FilterRuntimeID:       p.runtimeID,
		FilterReason:          p.reason,
		FilterInvocationID:    p.invocationID,
		TimeBasis:             p.timeBasis,
		FilterClientName:      p.clientName,
		FilterClientVersion:   p.clientVer,
		FilterToolName:        p.toolName,
		FilterResultStatus:    p.resultStatus,
		FilterResultCode:      p.resultCode,
		FilterTargetType:      p.targetType,
		FilterTargetID:        p.targetID,
		FilterInvocationStart: p.filterStart,
		IncludeSensitive:      roleAllowed(member.Role, "owner", "admin"),
		CursorAt:              p.cursor,
		CursorID:              p.cursorID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list audit events")
		return
	}
	writeAuditEventsPage(w, rows, p.limit)
}

// ListIssueAuditEvents is the issue-level thin wrapper over the same query —
// issue_id pinned, everything else identical. The route owns the issue
// dimension: a stray issue_id query param is validated by the shared parser
// but the pin wins.
func (h *Handler) ListIssueAuditEvents(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	member, ok := h.requireWorkspaceMember(w, r, uuidToString(issue.WorkspaceID), "issue not found")
	if !ok {
		return
	}
	p, ok := parseListAuditEventsParams(w, r)
	if !ok {
		return
	}

	rows, err := h.Queries.ListAuditEvents(r.Context(), db.ListAuditEventsParams{
		WorkspaceID:           issue.WorkspaceID,
		Limit:                 p.limit,
		Since:                 p.since,
		Until:                 p.until,
		FilterDomain:          p.domain,
		FilterEventType:       p.eventType,
		FilterActorType:       p.actorType,
		FilterActorID:         p.actorID,
		FilterIssueID:         issue.ID,
		FilterTaskID:          p.taskID,
		FilterAgentID:         p.agentID,
		FilterRuntimeID:       p.runtimeID,
		FilterReason:          p.reason,
		FilterInvocationID:    p.invocationID,
		TimeBasis:             p.timeBasis,
		FilterClientName:      p.clientName,
		FilterClientVersion:   p.clientVer,
		FilterToolName:        p.toolName,
		FilterResultStatus:    p.resultStatus,
		FilterResultCode:      p.resultCode,
		FilterTargetType:      p.targetType,
		FilterTargetID:        p.targetID,
		FilterInvocationStart: p.filterStart,
		IncludeSensitive:      roleAllowed(member.Role, "owner", "admin"),
		CursorAt:              p.cursor,
		CursorID:              p.cursorID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list audit events")
		return
	}
	writeAuditEventsPage(w, rows, p.limit)
}

// writeAuditEventsPage renders one page plus the keyset cursor for the next
// one when the page came back full (there may or may not be more; the next
// call answers that).
func writeAuditEventsPage(w http.ResponseWriter, rows []db.AuditEvent, limit int32) {
	events := make([]auditEventDTO, len(rows))
	for i, row := range rows {
		events[i] = auditEventToDTO(row)
	}
	resp := struct {
		Events       []auditEventDTO `json:"events"`
		NextCursor   *string         `json:"next_cursor"`
		NextCursorID *string         `json:"next_cursor_id"`
	}{Events: events}
	if int32(len(rows)) == limit && len(rows) > 0 {
		last := rows[len(rows)-1]
		ts := last.OccurredAt.Time.UTC().Format(time.RFC3339Nano)
		id := uuidToString(last.ID)
		resp.NextCursor = &ts
		resp.NextCursorID = &id
	}
	writeJSON(w, http.StatusOK, resp)
}
