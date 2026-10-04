package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/pkg/dbid"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Unified audit event contract (RUYI-355 Phase 1).
//
// Every cross-domain operational event lands in the append-only audit_event
// table through THIS file and nowhere else: the required-field validation
// below is the single enforcement point for the write contract, and the
// partial indexes in migration 922 assume its invariants hold. Never INSERT
// into audit_event from a call site; construct an Event with one of the typed
// constructors and append it through AppendAuditEvents (same-transaction
// paths) or TryAppendAuditEvents (best-effort paths).
//
// Causal chains are rebuilt from shared object dimensions (issue_id / task_id
// / agent_id / runtime_id) + trigger_kind/trigger_ref + occurred_at ordering
// — there is no explicit parent pointer, matching the approved design.
//
// Security: payloads must never carry environment-variable values or any
// credential material; env-domain events record key NAMES and counts only
// (same redaction the activity_log writes have always applied).

// Audit domains. The audit_event.event_type CHECK requires
// "<domain>.<action>" naming; keep every event type prefixed with its domain.
const (
	AuditDomainIssue   = "issue"
	AuditDomainRun     = "run"
	AuditDomainAgent   = "agent"
	AuditDomainRuntime = "runtime"
	AuditDomainOps     = "ops"
)

// Actor types (superset of the activity_log vocabulary).
const (
	AuditActorMember = "member"
	AuditActorAgent  = "agent"
	AuditActorSystem = "system"
	AuditActorDaemon = "daemon"
)

// Phase 1 event types. The vocabulary is open by design (new types need no
// migration); these constants cover the approved Phase 1 coverage matrix.
// The design table's "server.started" is normalized to ops.server_started so
// every event type stays domain-prefixed (the CHECK requires it).
const (
	// Run domain: the full run lifecycle.
	AuditRunQueued                = "run.queued"
	AuditRunDispatched            = "run.dispatched"
	AuditRunStarted               = "run.started"
	AuditRunWaitingLocalDirectory = "run.waiting_local_directory"
	AuditRunCancelRequested       = "run.cancel_requested"
	AuditRunCompleted             = "run.completed"
	AuditRunFailed                = "run.failed"
	AuditRunCancelled             = "run.cancelled"
	AuditRunRetried               = "run.retried"
	AuditRunRerun                 = "run.rerun"

	// Runtime domain: daemon connection lifecycle and sweeper verdicts.
	AuditRuntimeConnected          = "runtime.connected"
	AuditRuntimeDisconnected       = "runtime.disconnected"
	AuditRuntimeOfflineDetected    = "runtime.offline_detected"
	AuditRuntimeGC                 = "runtime.gc"
	AuditRuntimeReconnectExhausted = "runtime.reconnect_exhausted"

	// Agent domain: agent-scoped operational and security events.
	AuditAgentRunsCancelled           = "agent.runs_cancelled"
	AuditAgentEnvRevealed             = "agent.env_revealed"
	AuditAgentEnvUpdated              = "agent.env_updated"
	AuditAgentExecutionProfileActived = "agent.execution_profile_activated"

	// Issue domain: mirror of the activity_log writes (dual-write until
	// Phase 2 switches the timeline data source), plus the audit-only direct
	// writes that previously had no queryable surface.
	AuditIssueCreated              = "issue.created"
	AuditIssueStatusChanged        = "issue.status_changed"
	AuditIssuePriorityChanged      = "issue.priority_changed"
	AuditIssueAssigneeChanged      = "issue.assignee_changed"
	AuditIssueStartDateChanged     = "issue.start_date_changed"
	AuditIssueDueDateChanged       = "issue.due_date_changed"
	AuditIssueTitleChanged         = "issue.title_changed"
	AuditIssueDescriptionUpdated   = "issue.description_updated"
	AuditIssueTaskCompleted        = "issue.task_completed"
	AuditIssueTaskFailed           = "issue.task_failed"
	AuditIssueSquadLeaderEvaluated = "issue.squad_leader_evaluated"
	AuditIssueRunSuppressed        = "issue.run_suppressed"

	// Ops domain: deployment anchoring.
	AuditOpsServerStarted = "ops.server_started"
)

// Structured cancel/failure reasons. Cancel-class events (run.cancelled) MUST
// carry one of these; failure-class events (run.failed, run.cancel_requested)
// carry the reason that names the cause.
const (
	AuditReasonIssueDeleted           = "issue_deleted"
	AuditReasonIssueCancelled         = "issue_cancelled"
	AuditReasonReassignmentCleanup    = "reassignment_cleanup"
	AuditReasonTriggerCommentDeleted  = "trigger_comment_deleted"
	AuditReasonSupersededByRetry      = "superseded_by_retry"
	AuditReasonAgentStopped           = "agent_stopped"
	AuditReasonAgentArchived          = "agent_archived"
	AuditReasonUserRequested          = "user_requested"
	AuditReasonServerRepair           = "server_repair"
	AuditReasonChatSessionDeleted     = "chat_session_deleted"
	AuditReasonWorkspaceTeardown      = "workspace_teardown"
	AuditReasonRuntimeOfflineConverge = "runtime_offline_converged"
	AuditReasonRuntimeTeardown        = "runtime_teardown"
	AuditReasonConsumedByRunningTask  = "consumed_by_running_task"
	AuditReasonEscalationAcknowledged = "escalation_acknowledged"
	AuditReasonRuntimeOffline         = "runtime_offline"
	AuditReasonReconnectExhausted     = "reconnect_exhausted"
	AuditReasonOpsSweep               = "ops_sweep"
	// AuditReasonAgentReported is the run.failed fallback when the agent's own
	// report carries no structured failure_reason; the raw error text stays on
	// the task row, never duplicated into audit details.
	AuditReasonAgentReported = "agent_reported"
)

var auditDomains = map[string]bool{
	AuditDomainIssue: true, AuditDomainRun: true, AuditDomainAgent: true,
	AuditDomainRuntime: true, AuditDomainOps: true,
}

var auditActorTypes = map[string]bool{
	AuditActorMember: true, AuditActorAgent: true, AuditActorSystem: true, AuditActorDaemon: true,
}

// Event is one audit_event row pending insert. Construct via the typed
// constructors below so required fields start populated; AppendAuditEvents
// validates before writing regardless.
type Event struct {
	ID          pgtype.UUID
	WorkspaceID pgtype.UUID
	Domain      string
	EventType   string
	OccurredAt  time.Time
	ActorType   string
	ActorID     pgtype.UUID // invalid only for system actors
	TriggerKind *string
	TriggerRef  *string
	IssueID     pgtype.UUID
	TaskID      pgtype.UUID
	AgentID     pgtype.UUID
	RuntimeID   pgtype.UUID
	Reason      *string
	Details     []byte // JSONB; empty becomes '{}'
}

// Validate enforces the write contract. Rules mirror the audit_event column
// design (migration 922) and the approved coverage matrix:
//   - workspace/domain/event_type/occurred_at/actor_type required (id is
//     minted by AppendAuditEvents when absent)
//   - event_type must be "<domain>.<action>" (also enforced by a CHECK, but
//     contract violations should fail here with an actionable message)
//   - actor_id required unless the actor is the system
//   - run events carry task_id; runtime events carry runtime_id; issue events
//     carry issue_id; agent events carry agent_id
//   - cancel/failure class events carry a structured reason
func (e Event) Validate() error {
	switch {
	case !e.WorkspaceID.Valid:
		return errors.New("audit event: workspace_id is required")
	case !auditDomains[e.Domain]:
		return fmt.Errorf("audit event: unknown domain %q", e.Domain)
	case e.EventType == "":
		return errors.New("audit event: event_type is required")
	case len(e.EventType) <= len(e.Domain)+1 ||
		e.EventType[:len(e.Domain)] != e.Domain ||
		e.EventType[len(e.Domain)] != '.':
		return fmt.Errorf("audit event: event_type %q must be prefixed with domain %q", e.EventType, e.Domain)
	case e.OccurredAt.IsZero():
		return errors.New("audit event: occurred_at is required")
	case !auditActorTypes[e.ActorType]:
		return fmt.Errorf("audit event: unknown actor_type %q", e.ActorType)
	case !e.ActorID.Valid && e.ActorType != AuditActorSystem:
		return fmt.Errorf("audit event %s: actor_id is required for actor_type %q", e.EventType, e.ActorType)
	}
	if e.Domain == AuditDomainRun && !e.TaskID.Valid {
		return fmt.Errorf("audit event %s: task_id is required", e.EventType)
	}
	if e.Domain == AuditDomainRuntime && !e.RuntimeID.Valid {
		return fmt.Errorf("audit event %s: runtime_id is required", e.EventType)
	}
	if e.Domain == AuditDomainIssue && !e.IssueID.Valid {
		return fmt.Errorf("audit event %s: issue_id is required", e.EventType)
	}
	if e.Domain == AuditDomainAgent && !e.AgentID.Valid {
		return fmt.Errorf("audit event %s: agent_id is required", e.EventType)
	}
	switch e.EventType {
	case AuditRunCancelled, AuditRunFailed, AuditRunCancelRequested:
		if e.Reason == nil || *e.Reason == "" {
			return fmt.Errorf("audit event %s: reason is required", e.EventType)
		}
	}
	return nil
}

// AppendAuditEvents validates and batch-inserts events inside the caller's
// transaction (q is typically a qtx). A contract violation or write failure
// returns an error so the surrounding transaction rolls back — use this for
// paths where the audit row is part of the business guarantee (server-side
// cancel attribution, fail-closed env audits).
func AppendAuditEvents(ctx context.Context, q *db.Queries, events ...Event) error {
	if len(events) == 0 {
		return nil
	}
	params := make([]db.CreateAuditEventsParams, 0, len(events))
	for i := range events {
		e := events[i]
		if err := e.Validate(); err != nil {
			return err
		}
		details := e.Details
		if len(details) == 0 {
			details = []byte("{}")
		}
		params = append(params, db.CreateAuditEventsParams{
			ID:          e.ID,
			WorkspaceID: e.WorkspaceID,
			Domain:      e.Domain,
			EventType:   e.EventType,
			OccurredAt:  pgtype.Timestamptz{Time: e.OccurredAt, Valid: true},
			ActorType:   e.ActorType,
			ActorID:     e.ActorID,
			TriggerKind: textPtr(e.TriggerKind),
			TriggerRef:  textPtr(e.TriggerRef),
			IssueID:     e.IssueID,
			TaskID:      e.TaskID,
			AgentID:     e.AgentID,
			RuntimeID:   e.RuntimeID,
			Reason:      textPtr(e.Reason),
			Details:     details,
		})
	}
	if _, err := q.CreateAuditEvents(ctx, params); err != nil {
		return fmt.Errorf("audit event batch insert: %w", err)
	}
	return nil
}

// TryAppendAuditEvents is the best-effort variant for paths OUTSIDE a business
// transaction (sweeper verdicts, daemon register, server startup): a failure
// is logged loudly but never propagates. Any path where losing the audit row
// must abort the operation belongs in the caller's tx via AppendAuditEvents.
func TryAppendAuditEvents(ctx context.Context, q *db.Queries, events ...Event) {
	if len(events) == 0 {
		return
	}
	if err := AppendAuditEvents(ctx, q, events...); err != nil {
		slog.Warn("audit event write failed (best-effort path)", "count", len(events), "error", err)
	}
}

func textPtr(s *string) pgtype.Text {
	if s == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *s, Valid: true}
}

// ---- Typed constructors -------------------------------------------------
//
// One constructor family per Phase 1 coverage-matrix row. Callers pass the
// row they just wrote (task/runtime snapshot) so object dimensions can never
// drift from the business row the event describes.

// AuditWorkspaceIDForTask resolves the workspace an agent_task_queue row
// belongs to. The run ledger carries no workspace column; the resolution
// chain mirrors ResolveTaskWorkspaceID (issue → chat session → autopilot →
// linkless context kinds) but returns pgtype and works on a qtx so callers
// inside a transaction stay on their own snapshot. Invalid return means the
// row's anchors are gone — the write contract then rejects the event rather
// than writing an unqueryable row.
func AuditWorkspaceIDForTask(ctx context.Context, q *db.Queries, task db.AgentTaskQueue) pgtype.UUID {
	if task.IssueID.Valid {
		if issue, err := q.GetIssue(ctx, task.IssueID); err == nil {
			return issue.WorkspaceID
		}
	}
	if task.ChatSessionID.Valid {
		if cs, err := q.GetChatSession(ctx, task.ChatSessionID); err == nil {
			return cs.WorkspaceID
		}
	}
	if task.AutopilotRunID.Valid {
		if run, err := q.GetAutopilotRun(ctx, task.AutopilotRunID); err == nil {
			if ap, err := q.GetAutopilot(ctx, run.AutopilotID); err == nil {
				return ap.WorkspaceID
			}
		}
	}
	// The executing agent's home workspace is the strongest linkless anchor:
	// every task runs under exactly one agent and agents belong to exactly
	// one workspace, so this holds even when the context JSONB carries a
	// placeholder or stale workspace_id.
	if task.AgentID.Valid {
		if agent, err := q.GetAgent(ctx, task.AgentID); err == nil {
			return agent.WorkspaceID
		}
	}
	// Linkless kinds carry their workspace in the context JSONB:
	// quick-create payloads name workspace_id directly; quiz-run payloads
	// name a quiz item whose row knows the workspace (the same authority the
	// sweep tick and batch endpoint already use).
	if len(task.Context) > 0 {
		var payload struct {
			WorkspaceID string `json:"workspace_id"`
			QuizItemID  string `json:"quiz_item_id"`
		}
		if err := json.Unmarshal(task.Context, &payload); err == nil {
			if payload.WorkspaceID != "" {
				if wsID, err := pgtypeUUIDFromString(payload.WorkspaceID); err == nil {
					return wsID
				}
			}
			if payload.QuizItemID != "" {
				if itemID, err := pgtypeUUIDFromString(payload.QuizItemID); err == nil {
					if wsID, err := q.GetPromptQuizItemWorkspace(ctx, itemID); err == nil {
						return wsID
					}
				}
			}
		}
	}
	return pgtype.UUID{}
}

func pgtypeUUIDFromString(s string) (pgtype.UUID, error) {
	var u pgtype.UUID
	if err := u.Scan(s); err != nil {
		return pgtype.UUID{}, err
	}
	return u, nil
}

// RunEventFromTask builds a run-domain event from an agent_task_queue row the
// writer just persisted. workspace/issue/task/agent/runtime dimensions come
// from the row (workspace resolved per AuditWorkspaceIDForTask); an
// unresolvable workspace yields an event the contract will reject at write
// time — callers on best-effort paths will see a loud warning, never a silent
// unqueryable row.
func RunEventFromTask(ctx context.Context, q *db.Queries, eventType, reason, actorType string, actorID pgtype.UUID, task db.AgentTaskQueue) Event {
	return runEventFromDims(AuditWorkspaceIDForTask(ctx, q, task), task.IssueID, task.ID, task.AgentID, task.RuntimeID,
		eventType, reason, actorType, actorID)
}

// runEventFromDims is the dimension-explicit core behind RunEventFromTask;
// callers holding a sqlc Row shape (not the full AgentTaskQueue) use it
// directly with their own workspace resolution.
func runEventFromDims(workspaceID, issueID, taskID, agentID, runtimeID pgtype.UUID, eventType, reason, actorType string, actorID pgtype.UUID) Event {
	e := Event{
		ID:          dbid.NewV7(),
		WorkspaceID: workspaceID,
		Domain:      AuditDomainRun,
		EventType:   eventType,
		OccurredAt:  time.Now().UTC(),
		ActorType:   actorType,
		ActorID:     actorID,
		IssueID:     issueID,
		TaskID:      taskID,
		AgentID:     agentID,
		RuntimeID:   runtimeID,
	}
	if reason != "" {
		r := reason
		e.Reason = &r
	}
	return e
}

// TaskCancelledEvent builds the run.cancelled event for one task row a cancel
// statement just terminalized. Same-transaction callers pass their qtx so the
// workspace resolution rides the cancel's own snapshot; reason names WHY the
// run ended and actorType/actorID name WHO ended it (audit reason vocabulary).
func TaskCancelledEvent(ctx context.Context, q *db.Queries, task db.AgentTaskQueue, reason, actorType string, actorID pgtype.UUID, extraDetails map[string]any) Event {
	ev := RunEventFromTask(ctx, q, AuditRunCancelled, reason, actorType, actorID, task)
	if len(extraDetails) > 0 {
		if d, err := json.Marshal(extraDetails); err == nil {
			ev = ev.WithDetails(d)
		}
	}
	return ev
}

// TaskCancelAttribution maps the single-task cancel option shape onto the
// audit reason + actor pair. User-initiated cancels attribute to the acting
// member; everything else is a server decision and attributes to system.
func TaskCancelAttribution(userInitiated bool) (reason, actorType string) {
	if userInitiated {
		return AuditReasonUserRequested, AuditActorMember
	}
	return AuditReasonServerRepair, AuditActorSystem
}

// BulkTaskCancelledEvents builds one run.cancelled event per cancelled row.
// Same-transaction callers pass their qtx; pair with AppendAuditEvents to
// commit attribution with the flip.
func BulkTaskCancelledEvents(ctx context.Context, q *db.Queries, cancelled []db.AgentTaskQueue, reason, actorType string, actorID pgtype.UUID, extraDetails map[string]any) []Event {
	events := make([]Event, 0, len(cancelled))
	for _, t := range cancelled {
		events = append(events, TaskCancelledEvent(ctx, q, t, reason, actorType, actorID, extraDetails))
	}
	return events
}

// appendTaskCancelledAudits writes one run.cancelled event per row inside the
// caller's cancel transaction — attribution commits with the flip or not at
// all. reason names the server-side decision; actorID stays zero for system
// actors and carries the acting member/agent otherwise.
func appendTaskCancelledAudits(ctx context.Context, qtx *db.Queries, cancelled []db.AgentTaskQueue, reason, actorType string, actorID pgtype.UUID, extraDetails map[string]any) error {
	if len(cancelled) == 0 {
		return nil
	}
	return AppendAuditEvents(ctx, qtx, BulkTaskCancelledEvents(ctx, qtx, cancelled, reason, actorType, actorID, extraDetails)...)
}

// appendTaskFailedAudits writes one run.failed event per row inside the
// caller's terminal-transaction. Sweeper/repair paths only — user-visible
// failures go through FailTask, which writes its own event with richer
// details.
func appendTaskFailedAudits(ctx context.Context, qtx *db.Queries, rows []db.AgentTaskQueue, reason string) error {
	if len(rows) == 0 {
		return nil
	}
	events := make([]Event, 0, len(rows))
	for _, t := range rows {
		events = append(events, RunEventFromTask(ctx, qtx, AuditRunFailed, reason, AuditActorSystem, pgtype.UUID{}, t))
	}
	return AppendAuditEvents(ctx, qtx, events...)
}

// jsonDetails marshals a details payload, returning '{}' on failure — details
// are supplementary and must never block an event.
func jsonDetails(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte("{}")
	}
	return b
}

// JSONDetails is the exported form of jsonDetails for other packages that
// build audit events through the typed constructors.
func JSONDetails(v any) []byte { return jsonDetails(v) }

// memberOrSystemActor attributes an action to the acting member when one is
// known, else to the system.
func memberOrSystemActor(userID pgtype.UUID) (string, pgtype.UUID) {
	if userID.Valid {
		return AuditActorMember, userID
	}
	return AuditActorSystem, pgtype.UUID{}
}

// AuditActorFor maps a caller-supplied actor onto the contract's vocabulary.
// Unknown types, or a known type with no id, degrade to system — the contract
// rejects unattributed non-system actors, and a mirror write must never fail
// validation on actor shape.
func AuditActorFor(actorType string, actorID pgtype.UUID) (string, pgtype.UUID) {
	switch actorType {
	case AuditActorMember, AuditActorAgent, AuditActorDaemon:
		if actorID.Valid {
			return actorType, actorID
		}
	}
	return AuditActorSystem, pgtype.UUID{}
}

// TryAuditTaskQueued writes the run.queued event for a freshly created task
// row (best-effort, outside the insert transaction). A deferred row still
// logs run.queued with its fire time in details so a timeline can tell
// "waiting for a slot" from "waiting for a timer".
func TryAuditTaskQueued(ctx context.Context, q *db.Queries, task db.AgentTaskQueue, actorType string, actorID pgtype.UUID) {
	ev := RunEventFromTask(ctx, q, AuditRunQueued, "", actorType, actorID, task)
	if task.Status == "deferred" && task.FireAt.Valid {
		ev = ev.WithDetails(jsonDetails(map[string]string{"fire_at": task.FireAt.Time.UTC().Format(time.RFC3339Nano)}))
	}
	TryAppendAuditEvents(ctx, q, ev)
}

// RuntimeEventFromRow builds a runtime-domain event from an agent_runtime row.
// agentID optionally names the owning user agent for reconnect/GC events;
// pass pgtype.UUID{} where not applicable.
func RuntimeEventFromRow(eventType, actorType string, runtime db.AgentRuntime) Event {
	e := Event{
		ID:          dbid.NewV7(),
		WorkspaceID: runtime.WorkspaceID,
		Domain:      AuditDomainRuntime,
		EventType:   eventType,
		OccurredAt:  time.Now().UTC(),
		ActorType:   actorType,
		RuntimeID:   runtime.ID,
	}
	// Daemon events attribute to the runtime's own id — the contract rejects
	// a non-system actor with no id, and the runtime IS the acting daemon.
	if actorType == AuditActorDaemon {
		e.ActorID = runtime.ID
	}
	return e
}

// RuntimeEventFromDims builds a runtime-domain event when the caller holds
// only the runtime's identity columns (sweeper RETURNING shapes, GC paths)
// rather than a full row. reason optionally names the sweeper verdict.
func RuntimeEventFromDims(eventType, reason, actorType string, actorID, workspaceID, runtimeID pgtype.UUID) Event {
	e := Event{
		ID:          dbid.NewV7(),
		WorkspaceID: workspaceID,
		Domain:      AuditDomainRuntime,
		EventType:   eventType,
		OccurredAt:  time.Now().UTC(),
		ActorType:   actorType,
		ActorID:     actorID,
		RuntimeID:   runtimeID,
	}
	if reason != "" {
		r := reason
		e.Reason = &r
	}
	return e
}

// IssueEvent builds an issue-domain event (dual-write mirror of activity_log).
func IssueEvent(eventType, actorType string, actorID, workspaceID, issueID pgtype.UUID, details []byte) Event {
	return Event{
		ID:          dbid.NewV7(),
		WorkspaceID: workspaceID,
		Domain:      AuditDomainIssue,
		EventType:   eventType,
		OccurredAt:  time.Now().UTC(),
		ActorType:   actorType,
		ActorID:     actorID,
		IssueID:     issueID,
		Details:     details,
	}
}

// AgentEvent builds an agent-domain event.
func AgentEvent(eventType, actorType string, actorID, workspaceID, agentID pgtype.UUID, details []byte) Event {
	return Event{
		ID:          dbid.NewV7(),
		WorkspaceID: workspaceID,
		Domain:      AuditDomainAgent,
		EventType:   eventType,
		OccurredAt:  time.Now().UTC(),
		ActorType:   actorType,
		ActorID:     actorID,
		AgentID:     agentID,
		Details:     details,
	}
}

// OpsServerStartedEvent anchors a deployment: version + commit go into
// details so "what changed around this timestamp" has a queryable answer.
// Written once per known workspace from the startup hook (server.started is
// workspace-scoped because every audit query is).
func OpsServerStartedEvent(workspaceID pgtype.UUID, version, commit string) Event {
	details, _ := json.Marshal(map[string]string{"version": version, "commit": commit})
	return Event{
		ID:          dbid.NewV7(),
		WorkspaceID: workspaceID,
		Domain:      AuditDomainOps,
		EventType:   AuditOpsServerStarted,
		OccurredAt:  time.Now().UTC(),
		ActorType:   AuditActorSystem,
		Details:     details,
	}
}

// WithTrigger attaches causal-upstream evidence (semantics reused from
// agent_task_queue.trigger_evidence_kind/ref_id).
func (e Event) WithTrigger(kind, ref string) Event {
	k, r := kind, ref
	e.TriggerKind = &k
	e.TriggerRef = &r
	return e
}

// WithDetails sets/overrides the JSONB payload.
func (e Event) WithDetails(details []byte) Event {
	e.Details = details
	return e
}
