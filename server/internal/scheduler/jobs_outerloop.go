package scheduler

import (
	"context"
	"fmt"
	"time"

	"github.com/multica-ai/multica/server/internal/service"
)

// JobNameOuterLoopStallSweep is the canonical audit name for the
// outer-loop stall sweep (RUYI-660). Stable across releases.
const JobNameOuterLoopStallSweep = "outerloop_stall_sweep"

// OuterLoopSweeper is the narrow contract this job needs from
// service.TaskService. Defined here so tests can stub the sweeper without
// pulling in the rest of the service layer.
type OuterLoopSweeper interface {
	OuterLoopSweepOnce(ctx context.Context) (service.OuterLoopSweepResult, error)
}

// OuterLoopStallSweepJob returns the JobSpec that pulls stalled outer-loop
// plans back onto the task queue. The sweep is the consistency net for the
// paths the synchronous terminal hook cannot see: server-side sweeper
// terminalizations (queued_expired, runtime_offline), a lost enqueue race,
// and the closed-ticket pullback that could not land. The sweep's own grace
// window (service) keeps it from racing the hook on fresh terminalizations.
//
// The pass is cheap by construction: it scans only issues whose metadata
// says outerloop_state=running, and every issue without an active task past
// the grace window is one indexed read. A zero-result pass is the normal
// steady state.
func OuterLoopStallSweepJob(sweeper OuterLoopSweeper) JobSpec {
	return JobSpec{
		Name:              JobNameOuterLoopStallSweep,
		Cadence:           5 * time.Minute,
		ScheduleDelay:     5 * time.Minute,
		CatchUpMode:       CatchUpLatestOnly,
		CatchUpWindow:     time.Hour,
		RunTimeout:        5 * time.Minute,
		StaleTimeout:      10 * time.Minute,
		HeartbeatInterval: 30 * time.Second,
		AllowStaleReentry: true,
		MaxAttempts:       2,
		RetryBackoff: []time.Duration{
			time.Minute,
		},
		Scopes:  StaticScopes(ScopeGlobal),
		Handler: makeOuterLoopSweepHandler(sweeper),
	}
}

func makeOuterLoopSweepHandler(sweeper OuterLoopSweeper) Handler {
	return func(ctx context.Context, in HandlerInput) (HandlerResult, error) {
		res, err := sweeper.OuterLoopSweepOnce(ctx)
		if err != nil {
			return HandlerResult{}, fmt.Errorf("%s: %w", JobNameOuterLoopStallSweep, err)
		}
		return HandlerResult{Result: map[string]any{
			"scanned":      res.Scanned,
			"resumed":      res.Resumed,
			"soft_stopped": res.SoftStopped,
			"stopped":      res.Stopped,
		}}, nil
	}
}
