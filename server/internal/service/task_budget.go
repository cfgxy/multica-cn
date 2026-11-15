package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"github.com/multica-ai/multica/server/pkg/taskbudget"
	"github.com/multica-ai/multica/server/pkg/taskfailure"
)

var ErrTaskBudgetExhausted = errors.New("task budget admission refused")

// ErrTaskBudgetUsageUnknown is a temporary admission refusal. It must never
// close the scope: an explicit retry can succeed after usage arrives.
var ErrTaskBudgetUsageUnknown = errors.New("task budget usage is not yet known")

// CreateBudgetedRootTask freezes the current profile and creates a no-issue
// root task. A non-nil q must belong to the caller's existing transaction;
// otherwise the service owns the transaction. Notification remains the caller's
// responsibility after commit.
func (s *TaskService) CreateBudgetedRootTask(ctx context.Context, q *db.Queries, agentID pgtype.UUID, create func(*db.Queries) (db.AgentTaskQueue, error)) (db.AgentTaskQueue, error) {
	return s.budgetTaskCreate(ctx, q, agentID, nil, create)
}

// ValidateBudgetProfile validates the optional runtime_config.budget_profile at
// configuration boundaries. Legacy per-execution settings retain their contract.
func ValidateBudgetProfile(runtimeConfig json.RawMessage) error {
	_, err := taskbudget.ParseRuntimeConfig(runtimeConfig)
	return err
}

// TaskBudgetRuntimeConfig overlays the frozen profile onto the existing claim
// runtime_config wire field. Callers must use this instead of the mutable agent
// profile when preparing a claimed task. Unbudgeted legacy lineages stay off.
func TaskBudgetRuntimeConfig(task db.AgentTaskQueue, current json.RawMessage) (json.RawMessage, error) {
	snapshot, err := taskbudget.FromContext(task.Context)
	if err != nil {
		return nil, err
	}
	fields := map[string]json.RawMessage{}
	if len(current) > 0 {
		if err := json.Unmarshal(current, &fields); err != nil {
			return nil, err
		}
	}
	if fields == nil {
		fields = map[string]json.RawMessage{}
	}
	delete(fields, "budget_profile")
	if snapshot != nil {
		fields["budget_profile"], err = json.Marshal(snapshot.Profile)
		if err != nil {
			return nil, err
		}
	}
	return json.Marshal(fields)
}

// budgetTaskCreate owns a transaction when q is nil; a supplied q MUST already
// belong to the caller's transaction. Reservation and immutable snapshot commit
// with the enqueue, under one scope advisory lock, preventing concurrent siblings
// from both consuming the final max_runs slot.
func (s *TaskService) budgetTaskCreate(ctx context.Context, q *db.Queries, agentID pgtype.UUID, parents []pgtype.UUID, create func(*db.Queries) (db.AgentTaskQueue, error)) (db.AgentTaskQueue, error) {
	if q == nil {
		if s.TxStarter == nil {
			return db.AgentTaskQueue{}, errors.New("budget enqueue requires a transaction")
		}
		tx, err := s.TxStarter.Begin(ctx)
		if err != nil {
			return db.AgentTaskQueue{}, err
		}
		defer tx.Rollback(ctx)
		task, err := s.budgetTaskCreate(ctx, s.Queries.WithTx(tx), agentID, parents, create)
		if err != nil {
			return db.AgentTaskQueue{}, err
		}
		if err = tx.Commit(ctx); err != nil {
			return db.AgentTaskQueue{}, err
		}
		return task, nil
	}
	var snapshot *taskbudget.Snapshot
	inherited := false
	for _, id := range parents {
		if !id.Valid {
			continue
		}
		inherited = true
		parent, err := q.GetAgentTask(ctx, id)
		if err != nil {
			return db.AgentTaskQueue{}, fmt.Errorf("load budget ancestor: %w", err)
		}
		if budgetTerminalReason(parent.FailureReason.String) {
			return db.AgentTaskQueue{}, ErrTaskBudgetExhausted
		}
		snapshot, err = taskbudget.FromContext(parent.Context)
		if err != nil {
			return db.AgentTaskQueue{}, fmt.Errorf("invalid budget ancestor: %w", err)
		}
		break
	}
	if !inherited {
		agent, err := q.GetAgent(ctx, agentID)
		if err != nil {
			return db.AgentTaskQueue{}, err
		}
		profile, err := taskbudget.ParseRuntimeConfig(agent.RuntimeConfig)
		if err != nil {
			return db.AgentTaskQueue{}, err
		}
		if profile != nil {
			snapshot = &taskbudget.Snapshot{SchemaVersion: 1, ScopeID: util.UUIDToString(dbid.NewV7()), Profile: *profile}
		}
	}
	if snapshot != nil {
		if err := q.LockTaskBudgetScope(ctx, snapshot.ScopeID); err != nil {
			return db.AgentTaskQueue{}, err
		}
		usage, err := q.GetTaskBudgetUsage(ctx, snapshot.ScopeID)
		if err != nil {
			return db.AgentTaskQueue{}, err
		}
		// Known exhaustion takes priority over missing reports. Unknown usage
		// alone is temporary and cannot write a terminal scope marker.
		if denial := snapshot.Profile.Denial(usage.Runs, usage.TotalTokens, usage.CostUsdTicks, false, usage.Exhausted); denial != "" {
			return db.AgentTaskQueue{}, fmt.Errorf("%w: %s", ErrTaskBudgetExhausted, denial)
		}
		if (snapshot.Profile.MaxTotalTokens > 0 && usage.UnknownTokens) || (snapshot.Profile.MaxCostUSDTicks > 0 && usage.UnknownCost) {
			return db.AgentTaskQueue{}, ErrTaskBudgetUsageUnknown
		}
	}
	task, err := create(q)
	if err != nil || snapshot == nil {
		return task, err
	}
	// Retry queries already copy the immutable context; don't replace it.
	if existing, e := taskbudget.FromContext(task.Context); e != nil {
		return db.AgentTaskQueue{}, e
	} else if existing != nil {
		return task, nil
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return db.AgentTaskQueue{}, err
	}
	return q.SetTaskBudgetSnapshot(ctx, db.SetTaskBudgetSnapshotParams{ID: task.ID, Snapshot: raw})
}

func budgetTerminalReason(reason string) bool {
	return reason == taskfailure.ReasonBudgetExhausted.String() || reason == taskfailure.ReasonBudgetInvalid.String()
}

func (s *TaskService) createBudgetRetry(ctx context.Context, q *db.Queries, params db.CreateRetryTaskParams) (db.AgentTaskQueue, error) {
	parent, err := q.GetAgentTask(ctx, params.ID)
	if err != nil {
		return db.AgentTaskQueue{}, err
	}
	child, err := s.budgetTaskCreate(ctx, q, parent.AgentID, []pgtype.UUID{parent.ID}, func(q *db.Queries) (db.AgentTaskQueue, error) { return q.CreateRetryTask(ctx, params) })
	if errors.Is(err, ErrTaskBudgetExhausted) {
		if _, markErr := q.MarkTaskBudgetExhausted(ctx, db.MarkTaskBudgetExhaustedParams{ID: parent.ID, BudgetError: err.Error()}); markErr != nil {
			return db.AgentTaskQueue{}, markErr
		}
		return db.AgentTaskQueue{}, ErrTaskBudgetExhausted
	}
	return child, err
}

// rerunBudgetedIssue holds the scope lock across admission, cancellation and
// replacement. Any failure rolls back cancellations and queued audit writes.
func (s *TaskService) rerunBudgetedIssue(ctx context.Context, issue db.Issue, agentID, sourceID, triggerID pgtype.UUID, commentIDs []pgtype.UUID, isLeader bool, squadID, actorID pgtype.UUID) (*db.AgentTaskQueue, error) {
	if s.TxStarter == nil {
		return nil, errors.New("budget rerun requires a transaction")
	}
	tx, err := s.TxStarter.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	q := s.Queries.WithTx(tx)
	var cancelled []db.AgentTaskQueue
	task, err := s.budgetTaskCreate(ctx, q, agentID, []pgtype.UUID{sourceID}, func(q *db.Queries) (db.AgentTaskQueue, error) {
		var cancelErr error
		cancelled, cancelErr = q.CancelPendingTasksByIssueAndAgent(ctx, db.CancelPendingTasksByIssueAndAgentParams{IssueID: issue.ID, AgentID: agentID})
		if cancelErr != nil {
			return db.AgentTaskQueue{}, cancelErr
		}
		if err := SettleDeliveredDelegatedFailureRecoveries(ctx, q, cancelled...); err != nil {
			return db.AgentTaskQueue{}, err
		}
		writer := &TaskService{Queries: q, budgetQueries: q, suppressTaskQueued: true, Composio: s.Composio, FeatureFlags: s.FeatureFlags}
		return writer.enqueueRerunTask(ctx, issue, agentID, triggerID, commentIDs, isLeader, squadID, actorID, sourceID)
	})
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	for _, prior := range cancelled {
		s.captureTaskCancelled(ctx, prior)
		s.ReconcileAgentStatus(ctx, prior.AgentID)
		s.broadcastTaskEvent(ctx, protocol.EventTaskCancelled, prior)
	}
	actor, actorUUID := memberOrSystemActor(actorID)
	TryAuditTaskQueued(ctx, s.Queries, task, actor, actorUUID)
	TryAppendAuditEvents(ctx, s.Queries, RunEventFromTask(ctx, s.Queries, AuditRunRerun, "", actor, actorUUID, task).WithTrigger("rerun_of_task", util.UUIDToString(sourceID)))
	s.broadcastTaskEvent(ctx, protocol.EventTaskQueued, task)
	s.NotifyTaskEnqueued(ctx, task)
	return &task, nil
}
