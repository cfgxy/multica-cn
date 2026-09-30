package service

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/metrics"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// The run-intent compensating sweep (RUYI-304): a debounced flush lives in
// memory, so a crash, a restart, or a failed enqueue leaves the durable
// 'pending' row armed with no trigger coming. This reconciler claims due rows
// and drives the enqueue they were promised; the intent ledger — not the
// message — is the unit of recovery, so the recovered run replays the exact
// window (session, revision, initiator, force-fresh) the user actually sent.
//
// Terminal classification mirrors the flush path's semantics: the flush
// already told the user "offline/archived" at trigger time, so the reconciler
// converges the ledger to the same terminal states without a second notice.
// Only ledger state changes here — no replier wiring, per the ADR fallback
// (log + metrics).
const (
	channelChatRunReconcileSweepInterval = 30 * time.Second
	channelChatRunReconcileLease         = 2 * time.Minute
	channelChatRunReconcileSweepLimit    = 50
	// maxChatRunReconcileAttempts bounds transient retries before the row goes
	// dead. The flush fires at ~6s; a healthy deployment resolves rows long
	// before the first backoff expires.
	maxChatRunReconcileAttempts = 5
	chatRunRetryBackoffBase     = 30 * time.Second
	chatRunRetryBackoffCap      = time.Hour
)

// Run-intent dead reasons — stable strings, diagnostic SQL filters on them.
const (
	chatRunDeadSessionArchived  = "session_archived"
	chatRunDeadAgentMissing     = "agent_missing"
	chatRunDeadAgentOffline     = "agent_offline"
	chatRunDeadAgentArchived    = "agent_archived"
	chatRunDeadRouteSuperseded  = "route_superseded"
	chatRunDeadMissingInitiator = "missing_initiator"
	chatRunDeadSessionMissing   = "session_missing"
	chatRunDeadRetriesExhaused  = "retries_exhausted"
)

// ChannelChatRunReconciler compensates abandoned channel chat run intents.
type ChannelChatRunReconciler struct {
	Queries *db.Queries
	Tasks   *TaskService
	Logger  *slog.Logger
	Metrics *metrics.ChannelChatRunReconcilerMetrics
}

func (r *ChannelChatRunReconciler) logger() *slog.Logger {
	if r.Logger != nil {
		return r.Logger
	}
	return slog.Default()
}

// Run sweeps until ctx is cancelled. One tick per sweep interval; all work is
// inside RunOnce.
func (r *ChannelChatRunReconciler) Run(ctx context.Context) {
	ticker := time.NewTicker(channelChatRunReconcileSweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.RunOnce(ctx)
		}
	}
}

// RunOnce reconciles due intent rows one at a time: claim a row, drive its
// enqueue, claim the next, up to the per-sweep limit. Each claim is taken
// immediately before that row's work, so attempts and backoff describe enqueues
// that were actually tried. Every error is per-row and non-fatal.
func (r *ChannelChatRunReconciler) RunOnce(ctx context.Context) {
	for done := 0; done < channelChatRunReconcileSweepLimit; done++ {
		if ctx.Err() != nil {
			return
		}
		leaseToken := pgtype.UUID{Bytes: uuid.New(), Valid: true}
		claim, err := r.Queries.ClaimNextDueChatRunIntent(ctx, db.ClaimNextDueChatRunIntentParams{
			LeaseToken: leaseToken,
			Lease:      pgInterval(channelChatRunReconcileLease),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return
		}
		if err != nil {
			r.logger().Error("channel chat run reconciler: claim due intent failed",
				"error", err.Error())
			return
		}
		r.reconcileClaimed(ctx, claim, leaseToken)
	}
}

// reconcileClaimed drives one claimed intent to a terminal or retried state.
func (r *ChannelChatRunReconciler) reconcileClaimed(ctx context.Context, claim db.ChannelChatRunIntent, leaseToken pgtype.UUID) {
	logger := r.logger().With(
		"intent_id", util.UUIDToString(claim.ID),
		"chat_session_id", util.UUIDToString(claim.ChatSessionID),
		"context_revision", claim.ContextRevision,
	)

	// Fail-closed: without the authenticated initiator snapshot taken at
	// append time the run cannot be attributed — mirror the debounce path's
	// skip-without-impersonation rule.
	if !claim.InitiatorUserID.Valid {
		r.deadClaimed(ctx, claim, leaseToken, chatRunDeadMissingInitiator, "no initiator snapshot", logger)
		return
	}

	session, err := r.Queries.GetChatSession(ctx, claim.ChatSessionID)
	if errors.Is(err, pgx.ErrNoRows) {
		r.deadClaimed(ctx, claim, leaseToken, chatRunDeadSessionMissing, "chat session no longer exists", logger)
		return
	}
	if err != nil {
		r.retryClaimed(ctx, claim, leaseToken, err, logger)
		return
	}

	_, err = r.Tasks.EnqueueChannelChatTask(
		ctx, session, claim.InitiatorUserID, claim.ForceFresh,
		claim.ContextRevision, claim.BindingID, claim.RouteRevision,
	)
	switch {
	case err == nil:
		if r.Metrics != nil {
			r.Metrics.CompensatedRuns.Inc()
		}
		logger.Info("channel chat run reconciler: compensated abandoned run window")
	case errors.Is(err, ErrChatRunIntentAlreadyFired):
		// The flush (or a previous sweep) won the window; nothing to do.
	case errors.Is(err, ErrChatSessionArchived):
		r.deadClaimed(ctx, claim, leaseToken, chatRunDeadSessionArchived, err.Error(), logger)
	case errors.Is(err, ErrChatTaskAgentMissing):
		r.deadClaimed(ctx, claim, leaseToken, chatRunDeadAgentMissing, err.Error(), logger)
	case errors.Is(err, ErrChatRouteFenceMismatch):
		// Superseded by a newer generation (/new) or retired binding —
		// intentional supersession, terminal, never user-notified.
		r.deadClaimed(ctx, claim, leaseToken, chatRunDeadRouteSuperseded, err.Error(), logger)
	case errors.Is(err, ErrChatTaskAgentNoRuntime):
		r.deadClaimed(ctx, claim, leaseToken, chatRunDeadAgentOffline, err.Error(), logger)
	case errors.Is(err, ErrChatTaskAgentArchived):
		r.deadClaimed(ctx, claim, leaseToken, chatRunDeadAgentArchived, err.Error(), logger)
	default:
		r.retryClaimed(ctx, claim, leaseToken, err, logger)
	}
}

// deadClaimed flips the claimed row to dead when this owner still holds the
// lease; a flush that terminalized the row first makes it a no-op.
func (r *ChannelChatRunReconciler) deadClaimed(ctx context.Context, claim db.ChannelChatRunIntent, leaseToken pgtype.UUID, reason, cause string, logger *slog.Logger) {
	rows, err := r.Queries.FailClaimedChatRunIntent(ctx, db.FailClaimedChatRunIntentParams{
		DeadReason: pgtype.Text{String: reason, Valid: true},
		LastError:  pgtype.Text{String: cause, Valid: cause != ""},
		ID:         claim.ID,
		LeaseToken: leaseToken,
	})
	if err != nil {
		logger.Error("channel chat run reconciler: mark intent dead failed", "reason", reason, "error", err.Error())
		return
	}
	if rows == 1 && r.Metrics != nil {
		r.Metrics.PermanentDead.Inc()
	}
	logger.Warn("channel chat run reconciler: intent terminal",
		"reason", reason, "won", rows == 1)
}

// retryClaimed releases the lease with exponential backoff, or kills the row
// once attempts are exhausted.
func (r *ChannelChatRunReconciler) retryClaimed(ctx context.Context, claim db.ChannelChatRunIntent, leaseToken pgtype.UUID, cause error, logger *slog.Logger) {
	attempts := int(claim.Attempts) + 1
	if attempts >= maxChatRunReconcileAttempts {
		rows, err := r.Queries.FailClaimedChatRunIntent(ctx, db.FailClaimedChatRunIntentParams{
			DeadReason: pgtype.Text{String: chatRunDeadRetriesExhaused, Valid: true},
			LastError:  pgtype.Text{String: cause.Error(), Valid: true},
			ID:         claim.ID,
			LeaseToken: leaseToken,
		})
		if err != nil {
			logger.Error("channel chat run reconciler: exhaust intent failed", "error", err.Error())
			return
		}
		if rows == 1 && r.Metrics != nil {
			r.Metrics.RetryExhausted.Inc()
		}
		logger.Warn("channel chat run reconciler: intent retries exhausted", "error", cause.Error())
		return
	}
	backoff := chatRunRetryBackoffBase << min(attempts-1, 10)
	if backoff > chatRunRetryBackoffCap {
		backoff = chatRunRetryBackoffCap
	}
	if _, err := r.Queries.ReleaseChatRunIntentForRetry(ctx, db.ReleaseChatRunIntentForRetryParams{
		Backoff:    pgInterval(backoff),
		LastError:  pgtype.Text{String: cause.Error(), Valid: true},
		ID:         claim.ID,
		LeaseToken: leaseToken,
	}); err != nil {
		logger.Error("channel chat run reconciler: release intent for retry failed", "error", err.Error())
		return
	}
	if r.Metrics != nil {
		r.Metrics.BackoffReleases.Inc()
	}
	logger.Warn("channel chat run reconciler: transient failure; intent released for retry",
		"attempt", attempts, "backoff", backoff.String(), "error", cause.Error())
}
