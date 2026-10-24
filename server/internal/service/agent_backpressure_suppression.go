package service

// RUYI-859 stage B: automatic error-rate suppression for agents whose saved
// backpressure policy has the switch armed (stage A, migration 982).
//
// This file owns the loop's judgments and the loop's two and only paths
// into scheduling_pause:
//
//   - ensure: an agent-level row with created_by NULL and the fixed reason
//     SchedulingPauseReasonErrorRateSuppression (the "system source", 985),
//     inserted with ON CONFLICT DO NOTHING so a human or other-source row
//     already in the slot is observed, never acquired and never rewritten;
//   - release: a precise-source delete matching exactly that row — a human
//     freeze on the same agent survives untouched, and the human management
//     paths (RUYI-608, scheduling_pause.go) are left unchanged.
//
// Both the entry and the recovery judgments are recomputed from the
// database's trailing terminal-task windows on every tick; no in-process
// timer exists, so a fresh tick after a restart sees exactly what a
// restarted process must see. Audit rows and realtime broadcasts are owed
// only on ACTUAL insert/delete transitions: an idempotent no-op tick
// writes nothing (no second audit row, no second event, no second
// broadcast). The automatic transitions carry the system actor
// (AuditActorSystem); the human paths keep their member attribution.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"github.com/multica-ai/multica/server/pkg/taskfailure"
)

// SchedulingPauseReasonErrorRateSuppression is the fixed reason marker of
// the system source row. Together with created_by IS NULL it is the exact
// identity the loop owns and releases; no human path ever writes this pair.
const SchedulingPauseReasonErrorRateSuppression = "error_rate_suppression"

// Suppression-event types written to agent_suppression_event (982): one row
// per entered/cleared transition, never one per tick.
const (
	AgentSuppressionEventEntered = "entered"
	AgentSuppressionEventCleared = "cleared"
)

// BackpressureConfigFromPolicyRow maps a saved policy row to the wire card
// the API read model speaks. A missing row reads the built-in defaults and
// is never armed (the switch defaults off), so only saved rows reach the
// loop; this mapper has no defaults path on purpose.
func BackpressureConfigFromPolicyRow(row db.AgentBackpressurePolicy) *protocol.AgentBackpressureConfig {
	return &protocol.AgentBackpressureConfig{
		WindowMinutes:          int(row.WindowMinutes),
		ErrorRateThresholdPct:  row.ErrorRateThresholdPct,
		MinSamples:             int(row.MinSamples),
		RecoveryWindowMinutes:  int(row.RecoveryWindowMinutes),
		SuppressOnErrorRate:    row.SuppressOnErrorRate,
		CountModelAPIFailures:  row.CountModelApiFailures,
		CountExecutionFailures: row.CountExecutionFailures,
	}
}

// AgentBackpressureWindowReading is the terminal-sample feed for one
// trailing window: the per-display-class split, the selected numerator,
// the denominator and the derived rate (nil when there are no samples).
// Queued rows — including runtime backpressure holds — are structurally
// outside the terminal set and never count here; their depth is read
// separately through the Scheduling block. This is the single source of
// the error-rate criteria: the stage A visibility card, the stage B entry
// and recovery judgments, and their tests all classify through it.
type AgentBackpressureWindowReading struct {
	WindowMinutes        int            `json:"window_minutes"`
	TotalTerminal        int            `json:"total_terminal"`
	SelectedFailures     int            `json:"selected_failures"`
	RecoveryThresholdPct float64        `json:"recovery_threshold_pct"`
	ErrorRatePct         *float64       `json:"error_rate_pct,omitempty"`
	ByClass              map[string]int `json:"by_class"`
}

// AgentBackpressureReadingFor fetches the per-reason terminal counts for
// one trailing window and classifies them into a reading for cfg.
func AgentBackpressureReadingFor(ctx context.Context, q *db.Queries, workspaceID, agentID pgtype.UUID, cfg *protocol.AgentBackpressureConfig, windowMinutes int) (*AgentBackpressureWindowReading, error) {
	rows, err := q.CountAgentTerminalTasksByReasonInWindow(ctx, db.CountAgentTerminalTasksByReasonInWindowParams{
		WorkspaceID:   workspaceID,
		AgentID:       agentID,
		WindowMinutes: int32(windowMinutes),
	})
	if err != nil {
		return nil, err
	}
	return AgentBackpressureReadingFromRows(cfg, windowMinutes, rows), nil
}

// AgentBackpressureReadingFromRows classifies per-reason terminal rows into
// the display classes the card's two switches select over. A succeeded row
// (reason ”) is denominator-only; a failed row with an empty reason folds
// to 'unclassified' upstream of this call and lands in ClassOther.
func AgentBackpressureReadingFromRows(cfg *protocol.AgentBackpressureConfig, windowMinutes int, rows []db.CountAgentTerminalTasksByReasonInWindowRow) *AgentBackpressureWindowReading {
	reading := &AgentBackpressureWindowReading{
		WindowMinutes:        windowMinutes,
		RecoveryThresholdPct: cfg.ErrorRateThresholdPct * protocol.AgentBackpressureRecoveryFactor,
		ByClass:              map[string]int{},
	}
	for _, row := range rows {
		cls := taskfailure.ClassOther
		if row.Reason != "" {
			cls = taskfailure.DisplayClass(taskfailure.Reason(row.Reason))
		}
		reading.ByClass[cls] += int(row.TaskCount)
		if row.Reason == "" {
			continue // succeeded: denominator only
		}
		if (cls == taskfailure.ClassAuth || cls == taskfailure.ClassRateLimit ||
			cls == taskfailure.ClassTimeout || cls == taskfailure.ClassProvider) && cfg.CountModelAPIFailures {
			reading.SelectedFailures += int(row.TaskCount)
		}
		if (cls == taskfailure.ClassRuntime || cls == taskfailure.ClassAgent || cls == taskfailure.ClassOther) && cfg.CountExecutionFailures {
			reading.SelectedFailures += int(row.TaskCount)
		}
	}
	for _, row := range rows {
		reading.TotalTerminal += int(row.TaskCount)
	}
	if reading.TotalTerminal > 0 {
		rate := float64(reading.SelectedFailures) / float64(reading.TotalTerminal) * 100
		reading.ErrorRatePct = &rate
	}
	return reading
}

// EntryArmedFor is the entry judgment over the FULL entry window: the
// denominator must reach min_samples and the aggregated rate of the
// selected failure classes must be at or above the entry threshold. No
// samples — or below the floor — can never arm.
func EntryArmedFor(cfg *protocol.AgentBackpressureConfig, r *AgentBackpressureWindowReading) bool {
	if r == nil || r.ErrorRatePct == nil || r.TotalTerminal < cfg.MinSamples {
		return false
	}
	return *r.ErrorRatePct >= cfg.ErrorRateThresholdPct
}

// RecoveryClearedFor is the recovery judgment over the FULL recovery
// window: the denominator must reach min_samples AND the aggregated rate of
// the selected classes must sit STRICTLY below the fixed 0.5 x entry
// threshold. No samples means no recovery — the pause holds.
func RecoveryClearedFor(cfg *protocol.AgentBackpressureConfig, r *AgentBackpressureWindowReading) bool {
	if r == nil || r.ErrorRatePct == nil || r.TotalTerminal < cfg.MinSamples {
		return false
	}
	return *r.ErrorRatePct < cfg.ErrorRateThresholdPct*protocol.AgentBackpressureRecoveryFactor
}

// errorRateSuppressionRow reports whether a scheduling_pause row is the
// loop's own system source row: created_by NULL together with the fixed
// reason. Every other row — any human freeze — is foreign to the loop.
func errorRateSuppressionRow(row db.SchedulingPause) bool {
	return !row.CreatedBy.Valid && row.Reason == SchedulingPauseReasonErrorRateSuppression
}

// TickAgentBackpressureSuppression runs one sweep of the automatic loop:
// every armed policy is re-judged from the database and transitions are
// written only where an actual insert/delete happened. Per-candidate
// failures log and skip so one bad agent cannot stall the sweep. Returns
// the transition counts so the caller can log real state changes only.
//
// RUYI-859 stage B rework: alongside the armed candidate set, the tick also
// sweeps the system-row holders — agents that hold the loop's own
// system source row while their policy is off, deleted, or the agent is
// archived, and therefore dropped OUT of the armed set. Each such row is
// released exactly once by its source owner with system identity; the
// human paths are source-guarded and never lift it.
func (s *TaskService) TickAgentBackpressureSuppression(ctx context.Context) (entered, cleared int, err error) {
	policies, err := s.Queries.SelectArmedAgentBackpressurePolicies(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("select armed backpressure policies: %w", err)
	}
	armed := make(map[string]struct{}, len(policies))
	for _, p := range policies {
		key := util.UUIDToString(p.WorkspaceID) + "/" + util.UUIDToString(p.AgentID)
		armed[key] = struct{}{}
		switch s.suppressAgentTick(ctx, p) {
		case AgentSuppressionEventEntered:
			entered++
		case AgentSuppressionEventCleared:
			cleared++
		}
	}

	// Sweep cohort: every holder of a system source row that no armed
	// policy above owns. These are the orphan rows — the switch was turned
	// off, the policy row was deleted, or the agent was archived — that the
	// armed-only candidate set can no longer reach and that a human resume
	// may no longer lift either. Each is released precisely once; a second
	// tick finds no row and owes nothing.
	holders, err := s.Queries.SelectAgentErrorRateSuppressionHolders(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("select system pause holders: %w", err)
	}
	for _, h := range holders {
		key := util.UUIDToString(h.WorkspaceID) + "/" + util.UUIDToString(h.AgentID)
		if _, inArmed := armed[key]; inArmed {
			continue // already judged as an armed candidate above
		}
		if s.sweepSystemPauseRelease(ctx, h) {
			cleared++
		}
	}
	return entered, cleared, nil
}

// sweepSystemPauseRelease releases one orphan system source row with the
// source owner's identity: the built-in default card is the judgment the
// snapshot records, and the release is unconditional — an ownerless row
// (policy off, deleted, agent archived) has no judgment that can keep it
// alive. It returns whether the actual delete happened, so the tick counts
// it exactly once.
func (s *TaskService) sweepSystemPauseRelease(ctx context.Context, h db.SchedulingPause) bool {
	row := db.AgentBackpressurePolicy{WorkspaceID: h.WorkspaceID, AgentID: h.AgentID}
	return s.releaseErrorRateSchedulingPause(ctx, row, protocol.DefaultAgentBackpressureConfig(), nil) == AgentSuppressionEventCleared
}

// suppressAgentTick re-judges one armed policy and returns the transition
// it performed ("" when it observed or kept state without writing).
func (s *TaskService) suppressAgentTick(ctx context.Context, p db.AgentBackpressurePolicy) string {
	cfg := BackpressureConfigFromPolicyRow(p)

	entryReading, err := AgentBackpressureReadingFor(ctx, s.Queries, p.WorkspaceID, p.AgentID, cfg, cfg.WindowMinutes)
	if err != nil {
		slog.Warn("agent error-rate suppression: read entry window failed",
			"agent_id", util.UUIDToString(p.AgentID), "error", err)
		return ""
	}

	pause, err := s.Queries.GetAgentSchedulingPause(ctx, db.GetAgentSchedulingPauseParams{
		WorkspaceID: p.WorkspaceID,
		AgentID:     p.AgentID,
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// No agent-level row in the slot. Only an armed entry judgment
		// earns the system pause; everything else stays untouched.
		if !EntryArmedFor(cfg, entryReading) {
			return ""
		}
		return s.ensureErrorRateSchedulingPause(ctx, p, cfg, entryReading)
	case err != nil:
		slog.Warn("agent error-rate suppression: read scheduling pause failed",
			"agent_id", util.UUIDToString(p.AgentID), "error", err)
		return ""
	default:
		// A human or other-source row occupies the slot: observe only,
		// never acquire ownership, never lift it.
		if !errorRateSuppressionRow(pause) {
			return ""
		}
		// Still armed on the entry window: keep the pause (hysteresis).
		if EntryArmedFor(cfg, entryReading) {
			return ""
		}
		recoveryReading, err := AgentBackpressureReadingFor(ctx, s.Queries, p.WorkspaceID, p.AgentID, cfg, cfg.RecoveryWindowMinutes)
		if err != nil {
			slog.Warn("agent error-rate suppression: read recovery window failed",
				"agent_id", util.UUIDToString(p.AgentID), "error", err)
			return ""
		}
		// No-sample recovery windows hold the pause: RecoveryClearedFor
		// requires the denominator to reach min_samples AND the rate to
		// sit strictly under the fixed 0.5x line before any delete.
		if !RecoveryClearedFor(cfg, recoveryReading) {
			return ""
		}
		return s.releaseErrorRateSchedulingPause(ctx, p, cfg, recoveryReading)
	}
}

// ensureErrorRateSchedulingPause inserts the system source row. The audit
// row and the suppression-event snapshot share the insert's transaction:
// an insert without its trail is treated as not having happened. The
// broadcast happens after commit and only when this tick actually
// inserted (a 0-row ON CONFLICT result means a foreign row took the slot
// in flight: observed, not transitioned).
func (s *TaskService) ensureErrorRateSchedulingPause(ctx context.Context, p db.AgentBackpressurePolicy, cfg *protocol.AgentBackpressureConfig, entry *AgentBackpressureWindowReading) string {
	var (
		inserted bool
		queued   int64
		rate     float64
	)
	if entry.ErrorRatePct != nil {
		rate = *entry.ErrorRatePct
	}
	err := s.runInTx(ctx, func(qtx *db.Queries) error {
		rows, err := qtx.EnsureAgentErrorRateSchedulingPause(ctx, db.EnsureAgentErrorRateSchedulingPauseParams{
			WorkspaceID: p.WorkspaceID,
			AgentID:     p.AgentID,
		})
		if err != nil {
			return fmt.Errorf("ensure system scheduling pause: %w", err)
		}
		inserted = rows > 0
		if !inserted {
			// A foreign-source row occupies the slot: the loop observed,
			// it did not act. No audit, no event, no broadcast.
			return nil
		}
		q, err := qtx.CountQueuedTasksForAgent(ctx, p.AgentID)
		if err != nil {
			return fmt.Errorf("count frozen queued tasks: %w", err)
		}
		queued = q

		details, _ := json.Marshal(map[string]any{
			"scope":        SchedulingPauseScopeAgent,
			"reason":       SchedulingPauseReasonErrorRateSuppression,
			"queued_count": queued,
		})
		if err := AppendAuditEvents(ctx, qtx, AgentEvent(AuditAgentSchedulingPaused, AuditActorSystem,
			pgtype.UUID{}, p.WorkspaceID, p.AgentID, details)); err != nil {
			return fmt.Errorf("audit system scheduling pause: %w", err)
		}

		snapshot, err := json.Marshal(cfg)
		if err != nil {
			return fmt.Errorf("snapshot backpressure policy: %w", err)
		}
		_, err = qtx.InsertAgentSuppressionEvent(ctx, db.InsertAgentSuppressionEventParams{
			WorkspaceID:     p.WorkspaceID,
			AgentID:         p.AgentID,
			EventType:       AgentSuppressionEventEntered,
			SampleCount:     int32(entry.TotalTerminal),
			ErrorRatePct:    rate,
			EffectivePolicy: snapshot,
		})
		if err != nil {
			return fmt.Errorf("write suppression event: %w", err)
		}
		return nil
	})
	if err != nil {
		slog.Warn("agent error-rate suppression: ensure pause failed",
			"agent_id", util.UUIDToString(p.AgentID), "error", err)
		return ""
	}
	if !inserted {
		return ""
	}
	s.broadcastSchedulingChange(ctx, p.WorkspaceID, &p.AgentID, SchedulingPauseScopeAgent, true,
		SchedulingPauseReasonErrorRateSuppression, queued)
	slog.Info("agent error-rate suppression: entered",
		"agent_id", util.UUIDToString(p.AgentID),
		"samples", entry.TotalTerminal,
		"error_rate_pct", rate)
	return AgentSuppressionEventEntered
}

// releaseErrorRateSchedulingPause deletes the system source row only. The
// counted-queue and audit anchor the actual delete in one transaction; the
// EmptyClaim bump and the broadcast happen after commit and only when the
// delete actually matched — a human resume that lifted the row out of band
// already fired its own transition on the human side.
func (s *TaskService) releaseErrorRateSchedulingPause(ctx context.Context, p db.AgentBackpressurePolicy, cfg *protocol.AgentBackpressureConfig, recovery *AgentBackpressureWindowReading) string {
	var (
		released  bool
		queued    int64
		runtimeID pgtype.UUID
		rate      float64
	)
	// A nil recovery reading is the sweep release: the row was released by
	// its source owner with no judgment window to sample, so the event
	// records sample_count 0 / rate 0. The zero reading also keeps the
	// TotalTerminal dereference below safe.
	if recovery == nil {
		recovery = &AgentBackpressureWindowReading{}
	}
	if recovery.ErrorRatePct != nil {
		rate = *recovery.ErrorRatePct
	}
	err := s.runInTx(ctx, func(qtx *db.Queries) error {
		agent, err := qtx.GetAgent(ctx, p.AgentID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				// The agent vanished: it left the armed candidate set by
				// join, so this row (if any) is inert; nothing to lift.
				return nil
			}
			return fmt.Errorf("load agent: %w", err)
		}
		runtimeID = agent.RuntimeID

		// Count BEFORE deleting, mirroring ResumeAgentScheduling: the
		// number may over-report by the claims landing in flight, but it
		// never under-reports the held work that is about to flow.
		q, err := qtx.CountQueuedTasksForAgent(ctx, p.AgentID)
		if err != nil {
			return fmt.Errorf("count frozen queued tasks: %w", err)
		}
		queued = q

		rows, err := qtx.DeleteAgentErrorRateSchedulingPause(ctx, db.DeleteAgentErrorRateSchedulingPauseParams{
			WorkspaceID: p.WorkspaceID,
			AgentID:     p.AgentID,
		})
		if err != nil {
			return fmt.Errorf("release system scheduling pause: %w", err)
		}
		released = rows > 0
		if !released {
			return nil
		}

		details, _ := json.Marshal(map[string]any{
			"scope":        SchedulingPauseScopeAgent,
			"queued_count": queued,
		})
		if err := AppendAuditEvents(ctx, qtx, AgentEvent(AuditAgentSchedulingResumed, AuditActorSystem,
			pgtype.UUID{}, p.WorkspaceID, p.AgentID, details)); err != nil {
			return fmt.Errorf("audit system scheduling resume: %w", err)
		}

		snapshot, err := json.Marshal(cfg)
		if err != nil {
			return fmt.Errorf("snapshot backpressure policy: %w", err)
		}
		_, err = qtx.InsertAgentSuppressionEvent(ctx, db.InsertAgentSuppressionEventParams{
			WorkspaceID:     p.WorkspaceID,
			AgentID:         p.AgentID,
			EventType:       AgentSuppressionEventCleared,
			SampleCount:     int32(recovery.TotalTerminal),
			ErrorRatePct:    rate,
			EffectivePolicy: snapshot,
		})
		if err != nil {
			return fmt.Errorf("write suppression event: %w", err)
		}
		return nil
	})
	if err != nil {
		slog.Warn("agent error-rate suppression: release pause failed",
			"agent_id", util.UUIDToString(p.AgentID), "error", err)
		return ""
	}
	if !released {
		return ""
	}
	if runtimeID.Valid {
		s.notifyRuntimeMayHaveWork(runtimeID, "")
	}
	s.broadcastSchedulingChange(ctx, p.WorkspaceID, &p.AgentID, SchedulingPauseScopeAgent, false, "", queued)
	slog.Info("agent error-rate suppression: cleared",
		"agent_id", util.UUIDToString(p.AgentID),
		"samples", recovery.TotalTerminal,
		"error_rate_pct", rate)
	return AgentSuppressionEventCleared
}
