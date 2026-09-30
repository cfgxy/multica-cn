package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/daemon/protocol"
	"github.com/multica-ai/multica/server/pkg/db"
	"github.com/multica-ai/multica/server/pkg/util"
)

// RUYI-292 run lifecycle: the user-initiated single-run cancel matrix and the
// run-level retry entry. Behaviour spec = product spec (RUYI-292 comment
// 01a0ed63 §3-§6) as engineered in ruyi-292-tech-design.md §3 (D2/D3/D5/D6).

// CancelRun codes returned by CancelRunByUser. One code per row of the product
// cancel matrix; the HTTP/MCP/CLI surfaces share them verbatim so all clients
// speak one dialect.
const (
	RunCancelCodeCancelled         = "cancelled"          // stopped outright (no process to interrupt)
	RunCancelCodeCancelRequested   = "cancel_requested"   // stop accepted, awaiting daemon confirmation
	RunCancelCodeAlreadyCancelling = "already_cancelling" // repeat cancel = re-send interrupt nudge
	RunCancelCodeAlreadyCancelled  = "already_cancelled"  // idempotent repeat after confirmation
	RunCancelCodeNotCancellable    = "not_cancellable"    // completed/failed: nothing to stop (409)
)

// RunCancelOutcome is the cancel matrix result: the row AS IT IS after the call
// plus the matrix code describing what happened.
type RunCancelOutcome struct {
	Task db.AgentTaskQueue
	Code string
}

// cancelRequestedRebroadcastWait is how long an already_cancelling repeat waits
// before the daemon-side interrupt confirmation path is considered slow; it only
// shapes the log line, never the status — an unconfirmed cancel keeps its
// cancel_requested status until the ack lands (product rule: never fake a
// terminal state).
const cancelRequestedRebroadcastWait = 30 * time.Second

// CancelRunByUser is the product cancel matrix for one run. Two-phase per D3:
// queued rows (no process to interrupt) flip straight to cancelled through the
// full legacy cancellation flow (chat settlement, agent reconcile, terminal
// broadcast); in-flight rows flip to cancel_requested and wait for the daemon's
// cancel-ack to record the confirmed stop. Terminal rows are never rewritten —
// completed/failed answer 409 (not_cancellable), cancelled answers
// already_cancelled, and a repeat against cancel_requested re-broadcasts the
// nudge so the daemon polls immediately.
func (s *TaskService) CancelRunByUser(ctx context.Context, taskID pgtype.UUID, cancellerUserID pgtype.UUID) (*RunCancelOutcome, error) {
	task, err := s.Queries.GetAgentTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	switch task.Status {
	case "completed", "failed":
		return &RunCancelOutcome{Task: task, Code: RunCancelCodeNotCancellable}, nil
	case "cancelled":
		return &RunCancelOutcome{Task: task, Code: RunCancelCodeAlreadyCancelled}, nil
	case "cancel_requested":
		// Repeat cancel = re-send the interrupt nudge: rebroadcast so daemon
		// clients that resubscribed late still see it, and log the aging run so
		// stuck confirmations surface in server logs.
		s.broadcastTaskEvent(ctx, protocol.EventTaskCancelRequested, task)
		age := time.Since(task.CancelRequestedAt.Time)
		if age > cancelRequestedRebroadcastWait {
			slog.Warn("cancel requested still unconfirmed; repeat cancel re-broadcast",
				"task_id", util.UUIDToString(task.ID),
				"cancel_requested_at", task.CancelRequestedAt.Time,
				"age", age.String(),
			)
		}
		return &RunCancelOutcome{Task: task, Code: RunCancelCodeAlreadyCancelling}, nil
	case "queued":
		// No process anywhere: the server is authoritative. The legacy
		// user-cancel flow does the direct flip together with every side effect
		// a queued run owns (chat input settle, agent status, terminal event).
		cancelled, err := s.CancelTaskByUser(ctx, taskID)
		if err != nil {
			return nil, err
		}
		return &RunCancelOutcome{Task: *cancelled, Code: RunCancelCodeCancelled}, nil
	default:
		// dispatched / running / waiting_local_directory / deferred: two-phase.
		updated, err := s.Queries.RequestAgentTaskCancel(ctx, db.RequestAgentTaskCancelParams{
			ID:                      taskID,
			CancelRequestedByUserID: cancellerUserID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			// The row reached a terminal state between our read and the CAS —
			// first writer won. Report the current row under its own matrix code.
			existing, gerr := s.Queries.GetAgentTask(ctx, taskID)
			if gerr != nil {
				return nil, gerr
			}
			return s.cancelMatrixVerdict(existing), nil
		}
		if err != nil {
			return nil, fmt.Errorf("request cancel: %w", err)
		}
		s.broadcastTaskEvent(ctx, protocol.EventTaskCancelRequested, updated)
		return &RunCancelOutcome{Task: updated, Code: RunCancelCodeCancelRequested}, nil
	}
}

// cancelMatrixVerdict maps an already-terminal row onto its matrix code.
func (s *TaskService) cancelMatrixVerdict(task db.AgentTaskQueue) *RunCancelOutcome {
	switch task.Status {
	case "cancelled":
		return &RunCancelOutcome{Task: task, Code: RunCancelCodeAlreadyCancelled}
	case "cancel_requested":
		return &RunCancelOutcome{Task: task, Code: RunCancelCodeAlreadyCancelling}
	default:
		return &RunCancelOutcome{Task: task, Code: RunCancelCodeNotCancellable}
	}
}

// Sentinel errors for RetryRun. Callers map each to its own HTTP/MCP response
// code (409 family) with the shared human-readable copy.
var (
	// ErrRetrySourceNotFinished: only finished runs (completed/failed/cancelled)
	// can be retried — an in-flight run is either waitable or cancellable.
	ErrRetrySourceNotFinished = errors.New("source run has not finished")
	// ErrRetryAgentHasQueuedRun: same (issue, agent) already holds an unfinished
	// run; retrying would stack work instead of replacing it (use cancel first).
	ErrRetryAgentHasQueuedRun = errors.New("agent already has an unfinished run on this issue")
	// ErrRetryDescendantActive: this source already has an unfinished child from
	// a previous retry; two live children per source is the run storm the
	// product rule exists to prevent.
	ErrRetryDescendantActive = errors.New("source run already has an unfinished retry descendant")
)

// retryThrottleWindow is the double-click damping window (tech design D6
// rule 3): a repeat retry within this window returns the still-unfinished
// child the first call created instead of enqueueing a sibling.
const retryThrottleWindow = 5

// RetryRun creates a new run that re-attempts a finished one. Lineage rides
// rerun_of_task_id via RerunIssue (D5: manual retry reuses the rerun lineage —
// no new column, no merge with retry_of_task_id), so the target agent is the
// SOURCE run's agent with its CURRENT configuration, the trigger input is the
// source's surviving comment plan, and the run is a fresh session by
// force_fresh_session. The three D6 gates run before any mutation; the 5s
// window returns the already-created child (idempotent repeat), everything
// else either 409s or enqueues exactly one child.
func (s *TaskService) RetryRun(ctx context.Context, issueID pgtype.UUID, sourceTaskID pgtype.UUID, actorUserID pgtype.UUID, canInvoke func(agent db.Agent) bool) (*db.AgentTaskQueue, error) {
	source, err := s.Queries.GetAgentTask(ctx, sourceTaskID)
	if err != nil {
		return nil, err
	}
	if !source.IssueID.Valid || util.UUIDToString(source.IssueID) != util.UUIDToString(issueID) {
		return nil, fmt.Errorf("source task does not belong to this issue")
	}
	switch source.Status {
	case "completed", "failed", "cancelled":
		// retryable
	default:
		return nil, ErrRetrySourceNotFinished
	}

	// Double-click damping first: the unfinished child of a retry seconds ago
	// IS the retry the caller is asking for.
	recent, err := s.Queries.FindRecentRetryDescendant(ctx, db.FindRecentRetryDescendantParams{
		RerunOfTaskID: sourceTaskID,
		ThrottleSecs:  retryThrottleWindow,
	})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("look up recent retry descendant: %w", err)
	}
	if recent.ID.Valid {
		return &recent, nil
	}

	descendantActive, err := s.Queries.HasActiveRetryDescendant(ctx, sourceTaskID)
	if err != nil {
		return nil, fmt.Errorf("check retry descendant: %w", err)
	}
	if descendantActive {
		return nil, ErrRetryDescendantActive
	}

	agentBusy, err := s.Queries.HasActiveTaskForIssueAgent(ctx, db.HasActiveTaskForIssueAgentParams{
		IssueID: issueID,
		AgentID: source.AgentID,
	})
	if err != nil {
		return nil, fmt.Errorf("check agent queue slot: %w", err)
	}
	if agentBusy {
		return nil, ErrRetryAgentHasQueuedRun
	}

	// MUL-4525 invoke gate + lineage + attribution are RerunIssue's job; the
	// retry entry adds only the gates above.
	return s.RerunIssue(ctx, issueID, sourceTaskID, pgtype.UUID{}, actorUserID, canInvoke)
}
