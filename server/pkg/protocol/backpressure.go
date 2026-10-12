package protocol

import (
	"fmt"
	"time"
)

// Daemon-level host-backpressure configuration (RUYI-729 stage 1).
//
// The Runtime detail page saves one settings row per daemon host; every
// heartbeat ack delivers the row to that daemon, which hot-rebuilds its
// hysteresis gate without a restart. The daemon echoes the effective card
// (DB-delivered or its own boot config) back inside every backpressure
// report, so the UI and the event history can show exactly which rule was
// in force — the "why did everything stall at noon" answer needs the
// thresholds that were live at the time, not today's.
//
// Every field is advisory between peers: daemons that predate this struct
// ignore unknown ack fields, and servers treat a report without a config
// snapshot as "old daemon, env-configured" and degrade the UI instead of
// guessing.

// Backpressure defaults. These mirror the daemon's LoadConfig defaults
// (server/internal/daemon/config.go Default* constants) for the no-DB-row
// case: the server delivers nothing until a row is saved, and the daemon
// keeps running on its boot config (env-overridden or these values).
const (
	DefaultBackpressureEnabled         = true
	DefaultBackpressureMemHighPct      = 15.0
	DefaultBackpressureMemRecoveryPct  = 25.0
	DefaultBackpressureSwapHighPct     = 80.0
	DefaultBackpressureSwapRecoveryPct = 60.0
	// PSI memory pressure joins the gate as a third condition (RUYI-397):
	// trigger OR, recovery AND, same hysteresis discipline as the
	// watermarks. PSIHighPct <= 0 disables the condition entirely, and an
	// unreadable PSI source is skipped (degrades without blocking
	// mem/swap).
	DefaultBackpressurePSIHighPct     = 50.0
	DefaultBackpressurePSIRecoveryPct = 20.0
	DefaultBackpressureSampleInterval = 5 * time.Second
	DefaultBackpressureWindowSize     = 6 // samples; 6 × 5s = 30s smoothing window
)

// Backpressure config sources. SourceDB marks a card delivered from the
// daemon_backpressure_settings row; SourceEnv marks the daemon's boot-time
// (env or code default) configuration.
const (
	BackpressureSourceDB  = "db"
	BackpressureSourceEnv = "env"
)

// DaemonBackpressureConfig is the effective backpressure card: the wire
// shape both for the heartbeat-ack delivery and for the report's config
// snapshot. Trigger (OR): MemAvailable% < MemHigh OR SwapUsed% > SwapHigh
// OR PSI some-avg10 > PSIHigh. Recovery (AND): MemAvailable% >= MemRecovery
// AND SwapUsed% <= SwapRecovery AND PSI <= PSIRecovery. SwapHighPct /
// PSIHighPct <= 0 disable their conditions.
type DaemonBackpressureConfig struct {
	// Enabled is the gate master switch: false pauses nothing regardless of
	// the watermarks.
	Enabled bool `json:"enabled"`
	// MemHighPct is the MemAvailable% below which new claims pause,
	// percent of total; range (0,100].
	MemHighPct float64 `json:"mem_high_pct"`
	// MemRecoveryPct is the MemAvailable% above which claiming resumes;
	// must exceed MemHighPct (hysteresis).
	MemRecoveryPct float64 `json:"mem_recovery_pct"`
	// SwapHighPct is the SwapUsed% above which new claims pause; <=0
	// disables the swap condition.
	SwapHighPct float64 `json:"swap_high_pct"`
	// SwapRecoveryPct is the SwapUsed% below which claiming resumes; must
	// sit below SwapHighPct when the swap condition is enabled.
	SwapRecoveryPct float64 `json:"swap_recovery_pct"`
	// PSIHighPct is the memory PSI some-avg10% above which new claims
	// pause; <=0 disables the PSI condition.
	PSIHighPct float64 `json:"psi_high_pct"`
	// PSIRecoveryPct is the PSI some-avg10% below which claiming resumes;
	// must sit below PSIHighPct when the PSI condition is enabled.
	PSIRecoveryPct float64 `json:"psi_recovery_pct"`
	// SampleIntervalSeconds is the /proc sampling cadence for the gate, in
	// whole seconds (>=1).
	SampleIntervalSeconds int `json:"sample_interval_seconds"`
	// WindowSize is the smoothing window in samples before thresholds are
	// evaluated on the mean (>=1).
	WindowSize int `json:"window_size"`
	// ConfigVersion is the settings row's save counter. Reports carry it so
	// the server's event history can tell which edit produced an episode;
	// ack delivery carries it so the daemon can skip re-applying an
	// unchanged card. Version 0 means "not DB-managed" (boot config).
	ConfigVersion int64 `json:"config_version,omitempty"`
	// Source names where this card came from: "db" (settings row) or "env"
	// (daemon boot config). Ack delivery omits it — the daemon knows what
	// it applied once the card lands.
	Source string `json:"source,omitempty"`
}

// DefaultDaemonBackpressureConfig returns the code defaults as a wire card —
// the same numbers the daemon falls back to without env overrides, used by
// the server when a Runtime page asks for the effective config of a host
// that never saved a row.
func DefaultDaemonBackpressureConfig() *DaemonBackpressureConfig {
	return &DaemonBackpressureConfig{
		Enabled:               DefaultBackpressureEnabled,
		MemHighPct:            DefaultBackpressureMemHighPct,
		MemRecoveryPct:        DefaultBackpressureMemRecoveryPct,
		SwapHighPct:           DefaultBackpressureSwapHighPct,
		SwapRecoveryPct:       DefaultBackpressureSwapRecoveryPct,
		PSIHighPct:            DefaultBackpressurePSIHighPct,
		PSIRecoveryPct:        DefaultBackpressurePSIRecoveryPct,
		SampleIntervalSeconds: int(DefaultBackpressureSampleInterval / time.Second),
		WindowSize:            DefaultBackpressureWindowSize,
	}
}

// ValidateBackpressureConfig checks a card's hysteresis bands are not
// inverted or degenerate — the same discipline as the daemon's
// validateBackpressureThresholds, applied at the save path so a bad card
// never reaches a daemon. Sample interval and window must be positive so
// the watcher actually moves.
func ValidateBackpressureConfig(c *DaemonBackpressureConfig) error {
	if c == nil {
		return nil
	}
	if c.MemHighPct <= 0 || c.MemHighPct > 100 {
		return errBackpressure("mem high watermark %g%% out of range (0,100]", c.MemHighPct)
	}
	if c.MemRecoveryPct <= c.MemHighPct || c.MemRecoveryPct > 100 {
		return errBackpressure("mem recovery watermark %g%% must be above the high watermark %g%% (hysteresis)", c.MemRecoveryPct, c.MemHighPct)
	}
	if c.SwapHighPct > 0 {
		if c.SwapHighPct > 100 {
			return errBackpressure("swap high watermark %g%% out of range (0,100]", c.SwapHighPct)
		}
		if c.SwapRecoveryPct < 0 || c.SwapRecoveryPct >= c.SwapHighPct {
			return errBackpressure("swap recovery watermark %g%% must be below the high watermark %g%% (hysteresis)", c.SwapRecoveryPct, c.SwapHighPct)
		}
	}
	if c.PSIHighPct > 0 {
		if c.PSIHighPct > 100 {
			return errBackpressure("psi high watermark %g%% out of range (0,100]", c.PSIHighPct)
		}
		if c.PSIRecoveryPct < 0 || c.PSIRecoveryPct >= c.PSIHighPct {
			return errBackpressure("psi recovery watermark %g%% must be below the high watermark %g%% (hysteresis)", c.PSIRecoveryPct, c.PSIHighPct)
		}
	}
	if c.SampleIntervalSeconds < 1 {
		return errBackpressure("sample interval %ds must be at least 1s", c.SampleIntervalSeconds)
	}
	if c.WindowSize < 1 {
		return errBackpressure("window size %d must be at least 1", c.WindowSize)
	}
	return nil
}

// DaemonBackpressureCondition is one gate condition's reading at report
// time: the smoothed value, the threshold it is judged against, and whether
// it is currently past the line. All three conditions are always present
// when a config snapshot rides along — the full current-vs-threshold triple
// is what turns "backpressure active" into "mem at 12.3% vs 15% line" for
// the UI and the event history.
type DaemonBackpressureCondition struct {
	// Condition is "mem", "swap", or "psi".
	Condition string `json:"condition"`
	// Value is the smoothed reading (MemAvailable%, SwapUsed%, or PSI
	// some-avg10%).
	Value float64 `json:"value"`
	// Threshold is the high watermark the value is compared against
	// (recovery watermarks are derivable from the config snapshot).
	Threshold float64 `json:"threshold"`
	// Triggered reports whether the condition is past its high watermark
	// right now. Disabled conditions (threshold <= 0) carry Triggered
	// false.
	Triggered bool `json:"triggered"`
}

// backpressureErr formats config validation failures without leaking
// anything sensitive — these strings come straight back to the settings UI.
type backpressureError string

func (e backpressureError) Error() string { return string(e) }

func errBackpressure(format string, args ...any) error {
	return backpressureError("backpressure: " + fmt.Sprintf(format, args...))
}
