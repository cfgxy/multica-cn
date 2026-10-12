package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/multica-ai/multica/server/internal/handler"
)

// defaultVoiceProbeRefreshInterval is the production cadence for the
// credential-probe re-check sweep (RUYI-692 D1 ③): slow enough to be noise
// against the provider, fast enough that a healed network shows its badge
// well within an operator's attention span. MULTICA_VOICE_PROBE_REFRESH
// overrides it; a non-positive value disables the sweep entirely.
const defaultVoiceProbeRefreshInterval = 10 * time.Minute

// runVoiceProbeRefresher ticks the badge refresh sweep. A round is a handful
// of lightweight models.list calls against voice instances whose badge is
// degraded or stale; probe failures are recorded as badges, never fatal.
func runVoiceProbeRefresher(ctx context.Context, h *handler.Handler, interval time.Duration) {
	if interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.RefreshVoiceProbeBadges(ctx)
		}
	}
}

func voiceProbeRefreshIntervalFromEnv(getenv func(string) string) time.Duration {
	raw := getenv("MULTICA_VOICE_PROBE_REFRESH")
	if raw == "" {
		return defaultVoiceProbeRefreshInterval
	}
	interval, err := time.ParseDuration(raw)
	if err != nil {
		slog.Warn("invalid MULTICA_VOICE_PROBE_REFRESH; using the default", "value", raw, "error", err)
		return defaultVoiceProbeRefreshInterval
	}
	return interval
}
