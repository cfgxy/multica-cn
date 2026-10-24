package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/multica-ai/multica/server/internal/service"
)

// agentSuppressionConfig is the tunable knob set for the automatic agent
// error-rate suppression loop (RUYI-859 stage B). Every judgment happens
// inside the tick (service.TickAgentBackpressureSuppression) from the
// database's trailing terminal-task windows; the loop itself keeps no
// state, so a restarted process recomputes both windows from scratch.
//
// All values can be overridden via env vars (see envAgentSuppressionConfig).
// Setting Interval <= 0 disables the loop entirely.
type agentSuppressionConfig struct {
	Interval     time.Duration
	StartupDelay time.Duration
}

func defaultAgentSuppressionConfig() agentSuppressionConfig {
	// Two-minute cadence: the entry window is minutes-scale, so faster
	// ticks buy nothing; the recovery window is judged over its full
	// trailing span on each tick, not on a per-tick countdown.
	return agentSuppressionConfig{
		Interval:     2 * time.Minute,
		StartupDelay: 30 * time.Second,
	}
}

func envAgentSuppressionConfig() agentSuppressionConfig {
	cfg := defaultAgentSuppressionConfig()
	cfg.Interval = envDurationOrZero("AGENT_ERROR_RATE_SUPPRESSOR_INTERVAL", cfg.Interval)
	cfg.StartupDelay = envDurationNonNegative("AGENT_ERROR_RATE_SUPPRESSOR_STARTUP_DELAY", cfg.StartupDelay)
	return cfg
}

// runAgentErrorRateSuppressor periodically re-judges every armed agent
// backpressure policy and writes the entered/cleared transitions (system
// scheduling pause + audit + suppression event + realtime broadcast).
// Transition counts are logged only when non-zero so an idle fleet stays
// quiet; the per-transition log lines are owned by the service.
//
// Disable with `AGENT_ERROR_RATE_SUPPRESSOR_INTERVAL=0`.
func runAgentErrorRateSuppressor(ctx context.Context, taskSvc *service.TaskService, cfg agentSuppressionConfig) {
	if cfg.Interval <= 0 {
		slog.Info("agent error-rate suppressor: disabled (interval <= 0)")
		return
	}

	slog.Info(
		"agent error-rate suppressor: starting",
		"interval", cfg.Interval.String(),
		"startup_delay", cfg.StartupDelay.String(),
	)

	// Stagger startup so the whole fleet does not hit the DB the moment
	// every node boots — same rolling-restart concern as the autopilot
	// failure monitor.
	if cfg.StartupDelay > 0 {
		select {
		case <-ctx.Done():
			return
		case <-time.After(cfg.StartupDelay):
		}
	}

	// Run once immediately after the startup delay so a freshly-deployed
	// node re-derives the entry/recovery windows instead of waiting a
	// full interval.
	tickAgentErrorRateSuppressor(ctx, taskSvc)

	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			tickAgentErrorRateSuppressor(ctx, taskSvc)
		}
	}
}

// tickAgentErrorRateSuppressor performs one sweep. The tick function takes
// the context only to honor cancellation between candidates; its reads are
// all plain database state, which is what makes a fresh tick after a
// restart a faithful recompute of both windows.
func tickAgentErrorRateSuppressor(ctx context.Context, taskSvc *service.TaskService) {
	entered, cleared, err := taskSvc.TickAgentBackpressureSuppression(ctx)
	if err != nil {
		slog.Warn("agent error-rate suppressor: tick failed", "error", err)
		return
	}
	if entered > 0 || cleared > 0 {
		slog.Info("agent error-rate suppressor: tick transitions",
			"entered", entered, "cleared", cleared)
	}
}
