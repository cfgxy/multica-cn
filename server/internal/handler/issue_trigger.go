package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	obsmetrics "github.com/multica-ai/multica/server/internal/metrics"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	agentver "github.com/multica-ai/multica/server/pkg/agent"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

// maxPreviewTriggerIssues caps a single preview request so a pathological
// selection cannot fan out into thousands of readiness probes.
const maxPreviewTriggerIssues = 500

// suppressWarnMessage is the single log message for an honored `suppress_run`.
// It is a constant so operators grep one string and the tests assert the same
// one the handlers emit.
const suppressWarnMessage = "issue write suppressed an agent run"

// issueTriggerWriteProbe builds the probe the write paths feed to
// WillEnqueueRun. The private-agent gate is already enforced at the HTTP
// boundary (validateAssigneePair on assign) and inside enqueueSquadLeaderTask
// (canEnqueueSquadLeader), so a write must NOT re-run or sink it — it passes
// allow-all. The self-loop check needs the request's X-Task-ID header.
func (h *Handler) issueTriggerWriteProbe(r *http.Request, actorType, actorID string, issue db.Issue) service.IssueTriggerProbe {
	return service.IssueTriggerProbe{
		CanAccessAgent: nil, // allow-all; gate lives at the write boundary
		IsSelfLoop: func() bool {
			return h.isAgentRunningOnIssue(r, actorType, issue)
		},
		SuppressActiveSelfAssignment: func(agentID pgtype.UUID) bool {
			suppress := h.shouldSuppressActiveSelfAssignment(r.Context(), actorType, actorID, issue.ID, agentID)
			if suppress {
				slog.Info("suppressing duplicate self-assignment enqueue",
					"issue_id", uuidToString(issue.ID),
					"agent_id", uuidToString(agentID),
				)
			}
			return suppress
		},
	}
}

// issueTriggerPreviewProbe mirrors the real write-time gates for the read-only
// preview: the private-agent gate (so preview never leaks a private agent's
// readiness to a member who cannot see it — matching validateAssigneePair /
// canEnqueueSquadLeader) and the same self-loop guard.
func (h *Handler) issueTriggerPreviewProbe(r *http.Request, actorType, actorID, workspaceID string, issue db.Issue) service.IssueTriggerProbe {
	originatorUserID := h.invokeOriginatorFromRequest(r, actorType, actorID)
	return service.IssueTriggerProbe{
		CanAccessAgent: func(agent db.Agent) bool {
			return h.canInvokeAgent(r.Context(), agent, actorType, actorID, originatorUserID, workspaceID)
		},
		IsSelfLoop: func() bool {
			return h.isAgentRunningOnIssue(r, actorType, issue)
		},
		SuppressActiveSelfAssignment: func(agentID pgtype.UUID) bool {
			return h.shouldSuppressActiveSelfAssignment(r.Context(), actorType, actorID, issue.ID, agentID)
		},
	}
}

// shouldSuppressActiveSelfAssignment prevents a trusted task-scoped agent
// actor from creating another run for the target pair merely to claim issue
// ownership. It intentionally checks the TARGET pair, not whether the actor is
// busy anywhere: cross-issue self handoffs are a supported workflow and must
// still enqueue when the target has no active run. Query errors fail closed
// against the external enqueue side effect while leaving the ownership write
// itself intact. The API still returns success for that ownership write; only
// the server log exposes the failed advisory lookup, because enqueue is the
// optional side effect and suppressing it is safer than risking duplicate work.
func (h *Handler) shouldSuppressActiveSelfAssignment(ctx context.Context, actorType, actorID string, issueID, targetAgentID pgtype.UUID) bool {
	if actorType != "agent" || actorID == "" || actorID != uuidToString(targetAgentID) {
		return false
	}
	active, err := h.hasActiveTaskForIssueAndAgent(ctx, issueID, targetAgentID)
	return active || err != nil
}

// recordSuppressedIssueRun leaves the durable trace an honored `suppress_run`
// produces. The write itself succeeds and no task row appears, so without this
// nothing on the server distinguishes "a human deliberately parked the run"
// from "the run was lost" — RUYI-248 had to be reconstructed from agent
// session transcripts for exactly that reason. The ids go in the log; the
// counter carries only the two closed enums (RUYI-252).
//
// RUYI-275 adds two product-visible traces beside the WARN + metric: an
// append-only `run_suppressed` activity_log event whose details carry the
// same five fields as this log line (issue_id, actor_type, actor_id,
// trigger_source, target_status — the audit trail), while the issue row's
// run_suppressed snapshot (maintained by setRunSuppressedState before the
// response was built) answers "is the issue held right now". The event
// insert is best-effort: the write has already committed, and failing it
// must not fail the request — the WARN + metric are the fallback trail.
func (h *Handler) recordSuppressedIssueRun(ctx context.Context, issue db.Issue, trigger service.IssueRunTrigger, actorType, actorID string) {
	slog.Warn(suppressWarnMessage,
		"issue_id", uuidToString(issue.ID),
		"actor_type", actorType,
		"actor_id", actorID,
		"trigger_source", string(trigger.Source),
		"target_status", issue.Status,
	)
	h.Metrics.RecordIssueRunSuppressed(string(trigger.Source), actorType)

	details, err := json.Marshal(map[string]any{
		"issue_id":       uuidToString(issue.ID),
		"actor_type":     actorType,
		"actor_id":       actorID,
		"trigger_source": string(trigger.Source),
		"target_status":  issue.Status,
	})
	if err != nil {
		slog.Warn("marshal run_suppressed activity details", "issue_id", uuidToString(issue.ID), "error", err)
		return
	}
	actorUUID, err := util.ParseUUID(actorID)
	if err != nil {
		// actorID comes from resolveActor and is always a UUID; reaching this
		// means an unexpected actor shape — keep the log+metric trail, skip
		// the event rather than insert a row that violates the FK shape.
		slog.Warn("run_suppressed actor id is not a uuid", "issue_id", uuidToString(issue.ID), "actor_id", actorID)
		return
	}
	if _, err := h.Queries.CreateActivity(ctx, db.CreateActivityParams{
		ID:          dbid.NewV7(),
		WorkspaceID: issue.WorkspaceID,
		IssueID:     issue.ID,
		ActorType:   pgtype.Text{String: actorType, Valid: true},
		ActorID:     actorUUID,
		Action:      suppressedActivityAction,
		Details:     details,
	}); err != nil {
		slog.Warn("insert run_suppressed activity", "issue_id", uuidToString(issue.ID), "error", err)
	}
}

// suppressedActivityAction is the activity_log action for an honored
// `suppress_run` (RUYI-275). The details payload mirrors the WARN log line's
// five fields verbatim, so an operator can join the two trails by content.
const suppressedActivityAction = "run_suppressed"

// setRunSuppressedState maintains the issue row's run_suppressed snapshot
// (RUYI-275): set when a write's run was honored-suppressed, cleared when the
// write truly starts a run or removes the assignee, untouched otherwise. It
// deliberately rides outside UpdateIssue's CTE: the disposition is only known
// after WillEnqueueRun evaluates the already-updated row, and a second
// targeted UPDATE keeps the big write query from growing trigger-probe
// columns. Best-effort — on error the stale snapshot persists and the next
// disposition-bearing write repairs it; the caller returns the refreshed row
// so the WS payload and HTTP response flip the badge in the same event that
// reports this write.
func (h *Handler) setRunSuppressedState(ctx context.Context, issue db.Issue, suppressed bool) db.Issue {
	var at pgtype.Timestamptz
	if suppressed {
		at = pgtype.Timestamptz{Time: time.Now(), Valid: true}
	}
	updated, err := h.Queries.SetIssueRunSuppressed(ctx, db.SetIssueRunSuppressedParams{
		ID:              issue.ID,
		RunSuppressed:   suppressed,
		RunSuppressedAt: at,
	})
	if err != nil {
		slog.Warn("maintain run_suppressed snapshot", "issue_id", uuidToString(issue.ID), "suppressed", suppressed, "error", err)
		return issue
	}
	return updated
}

// suppressesRun decides whether a requested suppress_run actually cancels the
// enqueue this write would otherwise perform.
//
// A member actor is the human "暂时不启动" affordance (the run-confirm dialog)
// and is always honored. A trusted task-scoped agent actor is not: the flag's
// own documented meaning is "without starting ANOTHER run", so honoring it on
// an issue that holds no run at all silently discards the work instead of
// deferring it. That is the RUYI-248 stranding — a leader promoted parked
// sub-issues with `--no-start`, the board showed them in progress and nothing
// was ever queued, with no log to find it by. The suppression therefore needs
// an existing run to anchor on: with an active task on the (issue, agent) pair
// it still suppresses, composing with shouldSuppressActiveSelfAssignment's
// self-claim dedup; with none it is ignored and the run enqueues, where the
// (issue_id, agent_id) pending unique index remains the duplicate backstop.
//
// A failed lookup fails closed to "suppress" for the same reason
// shouldSuppressActiveSelfAssignment does: "cannot confirm whether a run is
// active" must never license a possibly-duplicate run.
func (h *Handler) suppressesRun(ctx context.Context, requested bool, actorType string, trigger service.IssueRunTrigger) bool {
	if !requested || actorType != "agent" {
		return requested
	}
	active, err := h.hasActiveTaskForIssueAndAgent(ctx, trigger.IssueID, trigger.AgentID)
	if err != nil || active {
		return true
	}
	slog.Info("ignoring suppress_run from agent actor: issue has no active run to suppress",
		"issue_id", uuidToString(trigger.IssueID),
		"agent_id", uuidToString(trigger.AgentID),
		"source", string(trigger.Source),
	)
	return false
}

// dispatchIssueRun executes the enqueue side effect for a decision produced by
// WillEnqueueRun, carrying an optional handoff note into the run's opening
// context. The squad path still flows through enqueueSquadLeaderTask so the
// leader access gate and pending dedup stay in one place.
func (h *Handler) dispatchIssueRun(ctx context.Context, issue db.Issue, trigger service.IssueRunTrigger, actorType, actorID, handoffNote string) {
	switch trigger.AssigneeType {
	case "agent":
		// The member who performed this assign/promote is the accountable human
		// for the run (MUL-4302 §4). An agent actor is not a human, so only a
		// member actor is threaded; otherwise attribution falls back to the chain.
		_, _ = h.TaskService.EnqueueTaskForIssueWithHandoff(ctx, issue, handoffNote, memberActorUserID(actorType, actorID))
	case "squad":
		h.enqueueSquadLeaderTask(ctx, issue, pgtype.UUID{}, actorType, actorID, handoffNote)
	}
}

// memberActorUserID returns the acting member's user id as a pgtype.UUID when the
// actor is a member, and an invalid UUID otherwise (an agent actor id is not a
// human and must never become an accountable human). Used to thread the
// assign/promote actor into the attribution resolver (MUL-4302 §4).
func memberActorUserID(actorType, actorID string) pgtype.UUID {
	if actorType != "member" {
		return pgtype.UUID{}
	}
	uid, err := util.ParseUUID(actorID)
	if err != nil {
		return pgtype.UUID{}
	}
	return uid
}

// IssueTriggerPreviewRequest asks "if I apply this assignee and/or status to
// these issues (or create one), which runs will start". All fields are
// optional; a nil prospective field means "leave unchanged".
type IssueTriggerPreviewRequest struct {
	// IssueIDs are existing issues to evaluate (single assign, single status,
	// or a batch). Empty with IsCreate=true evaluates a candidate new issue.
	IssueIDs []string `json:"issue_ids"`
	// IsCreate previews a not-yet-persisted issue from AssigneeType/ID/Status.
	IsCreate     bool    `json:"is_create"`
	AssigneeType *string `json:"assignee_type"`
	AssigneeID   *string `json:"assignee_id"`
	Status       *string `json:"status"`
}

// IssueTriggerPreviewItem is one issue that WILL start a run under the
// prospective write. AgentID is the runnable agent (squad leader for squads).
// HandoffSupported is the soft-gate signal: false when the target runtime's
// daemon is too old to render a handoff note, so the UI can gray out the note
// box rather than silently drop the text. The assignment itself still works.
type IssueTriggerPreviewItem struct {
	IssueID          string `json:"issue_id"`
	AgentID          string `json:"agent_id"`
	Source           string `json:"source"`
	HandoffSupported bool   `json:"handoff_supported"`
}

// IssueTriggerPreviewResponse lists every issue that will enqueue plus a total
// the UI can show directly ("将启动 N 个"). Issues that will NOT start a run are
// simply absent, so total_count == len(triggers).
type IssueTriggerPreviewResponse struct {
	Triggers   []IssueTriggerPreviewItem `json:"triggers"`
	TotalCount int                       `json:"total_count"`
}

// PreviewIssueTrigger dry-runs WillEnqueueRun for a prospective issue write and
// returns the runs that would start, without any side effect. It is the single
// authority the four entry points (create / single assign / single status /
// batch) consult so the frontend never re-implements the enqueue rule
// (MUL-3375). Mirrors PreviewCommentTriggers.
func (h *Handler) PreviewIssueTrigger(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := h.resolveWorkspaceID(r)
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace is required")
		return
	}

	var req IssueTriggerPreviewRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.IssueIDs) > maxPreviewTriggerIssues {
		writeError(w, http.StatusBadRequest, "too many issue_ids")
		return
	}

	// Resolve the prospective assignee once — a malformed id is a deterministic
	// 400, never a silent miscount.
	var (
		newAssigneeType pgtype.Text
		newAssigneeID   pgtype.UUID
		hasNewAssignee  bool
	)
	if req.AssigneeType != nil && *req.AssigneeType != "" && req.AssigneeID != nil && *req.AssigneeID != "" {
		id, parseOK := parseUUIDOrBadRequest(w, *req.AssigneeID, "assignee_id")
		if !parseOK {
			return
		}
		newAssigneeType = pgtype.Text{String: *req.AssigneeType, Valid: true}
		newAssigneeID = id
		hasNewAssignee = true
	}

	actorType, actorID := h.resolveActor(r, userID, workspaceID)
	resp := IssueTriggerPreviewResponse{Triggers: make([]IssueTriggerPreviewItem, 0)}

	appendTrigger := func(issue db.Issue, in service.IssueTriggerInput) {
		probe := h.issueTriggerPreviewProbe(r, actorType, actorID, workspaceID, issue)
		if trigger, ok := h.IssueService.WillEnqueueRun(r.Context(), in, probe); ok {
			resp.Triggers = append(resp.Triggers, IssueTriggerPreviewItem{
				IssueID:          uuidToString(trigger.IssueID),
				AgentID:          uuidToString(trigger.AgentID),
				Source:           string(trigger.Source),
				HandoffSupported: h.runtimeSupportsHandoff(r.Context(), trigger.AgentID),
			})
		}
	}

	if req.IsCreate {
		wsUUID, err := util.ParseUUID(workspaceID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid workspace")
			return
		}
		status := "todo"
		if req.Status != nil && *req.Status != "" {
			status = *req.Status
		}
		candidate := db.Issue{
			WorkspaceID:  wsUUID,
			Status:       status,
			AssigneeType: newAssigneeType,
			AssigneeID:   newAssigneeID,
		}
		appendTrigger(candidate, service.IssueTriggerInput{Issue: candidate, IsCreate: true})
		resp.TotalCount = len(resp.Triggers)
		writeJSON(w, http.StatusOK, resp)
		return
	}

	for _, rawID := range req.IssueIDs {
		issueUUID, err := util.ParseUUID(rawID)
		if err != nil {
			continue // malformed id contributes no trigger; deterministic
		}
		loaded, err := h.Queries.GetIssueInWorkspace(r.Context(), db.GetIssueInWorkspaceParams{
			ID:          issueUUID,
			WorkspaceID: parseUUID(workspaceID),
		})
		if err != nil {
			continue // cross-workspace / unknown id contributes no trigger
		}

		post := loaded
		in := service.IssueTriggerInput{PrevStatus: loaded.Status}
		if hasNewAssignee {
			post.AssigneeType = newAssigneeType
			post.AssigneeID = newAssigneeID
			in.AssigneeChanged = loaded.AssigneeType.String != newAssigneeType.String ||
				uuidToString(loaded.AssigneeID) != uuidToString(newAssigneeID)
		}
		if req.Status != nil && *req.Status != "" {
			post.Status = *req.Status
			in.StatusChanged = loaded.Status != *req.Status
		}
		in.Issue = post
		appendTrigger(post, in)
	}

	resp.TotalCount = len(resp.Triggers)
	writeJSON(w, http.StatusOK, resp)
}

// runtimeSupportsHandoff reports whether the agent's bound runtime reports a
// CLI version new enough to render handoff notes. Drives the preview's
// handoff_supported soft-gate signal. Any resolution failure → false (degrade).
func (h *Handler) runtimeSupportsHandoff(ctx context.Context, agentID pgtype.UUID) bool {
	agent, err := h.Queries.GetAgent(ctx, agentID)
	if err != nil || !agent.RuntimeID.Valid {
		return false
	}
	rt, err := h.getAgentRuntime(ctx, obsmetrics.RuntimeLookupSourceIssue, agent.RuntimeID)
	if err != nil {
		return false
	}
	return agentver.HandoffSupported(readRuntimeCLIVersion(rt.Metadata))
}
