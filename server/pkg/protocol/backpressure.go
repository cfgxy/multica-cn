package protocol

import (
	"fmt"
	"time"
)

// Host-memory backpressure wire cards (RUYI-393/397/618/729). Two config
// planes share this type family:
//
//   - Workspace plane (RUYI-618): the workspace settings UI saves one card
//     per workspace; every heartbeat ack delivers the effective card (saved
//     card or these defaults) to the workspace's daemons, which hot-apply it
//     to their per-workspace admission gates.
//   - Daemon-host plane (RUYI-729): the Runtime detail page saves one
//     settings row per daemon host; the ack's daemon_backpressure_config
//     field carries that host card, which overrides the workspace card on
//     the host's gates. The daemon echoes the card actually in force back
//     inside every backpressure report, so the UI and the event history can
//     show exactly which rule was live — the "why did everything stall at
//     noon" answer needs the thresholds that were in force at the time, not
//     today's.
//
// Every field is advisory between peers: daemons that predate these structs
// ignore unknown ack fields, and servers treat a report without a config
// snapshot as "old daemon, unconfigured" and degrade the UI instead of
// guessing.

// Backpressure defaults. These mirror the daemon's LoadConfig defaults
// (server/internal/daemon/config.go Default* constants) for the
// no-settings-saved case, and live here — next to the wire type — so the
// daemon boot path, the server's heartbeat delivery, and the settings APIs
// read one shared source instead of three copies that can drift. They are
// INITIAL SAFETY PARAMETERS chosen from the RUYI-392 incident profile, not
// calibrated final values; tune them per workspace (RUYI-618) or per daemon
// host (RUYI-729) from the settings UIs.
const (
	DefaultBackpressureEnabled         = true
	DefaultBackpressureMemHighPct      = 15.0
	DefaultBackpressureMemRecoveryPct  = 25.0
	DefaultBackpressureSwapHighPct     = 80.0
	DefaultBackpressureSwapRecoveryPct = 60.0
	// PSI memory pressure joins the gate as a third condition (RUYI-397):
	// trigger OR, recovery AND, same hysteresis discipline as the
	// watermarks. 50/20 share the initial-safety standing of the watermarks;
	// PSIHighPct <= 0 disables the condition entirely, and an unreadable PSI
	// source is skipped (degrades without blocking mem/swap).
	DefaultBackpressurePSIHighPct     = 50.0
	DefaultBackpressurePSIRecoveryPct = 20.0
	DefaultBackpressureSampleInterval = 5 * time.Second
	DefaultBackpressureWindowSize     = 6 // samples; 6 × 5s = 30s smoothing window

	// Upper bounds for the sampler knobs. They cap the daemon's per-machine
	// window allocation (WindowSize × per-sample struct) and keep the
	// sampling cadence within an hour, so a fat-fingered save cannot turn
	// the first hot-apply into a huge allocation on every daemon host. The
	// maxima are round numbers an hour wide — beyond that the gate stops
	// being an operational control.
	MaxBackpressureSampleIntervalSeconds = 3600
	MaxBackpressureWindowSize            = 3600
)

// Config sources for the daemon-host plane (RUYI-729). SourceDB marks a card
// delivered from the daemon_backpressure_settings row; SourceEnv marks the
// daemon's boot-time (env-overridden or code-default) configuration.
const (
	BackpressureSourceDB  = "db"
	BackpressureSourceEnv = "env"
)

// DaemonBackpressureConfig is the effective backpressure card: the wire
// shape both for the heartbeat-ack delivery (the workspace card on
// backpressure_config, the daemon-host override card on
// daemon_backpressure_config) and for the report's config snapshot. Trigger
// (OR): MemAvailable% < MemHigh OR SwapUsed% > SwapHigh OR PSI some-avg10 >
// PSIHigh. Recovery (AND): MemAvailable% >= MemRecovery AND SwapUsed% <=
// SwapRecovery AND PSI <= PSIRecovery. SwapHighPct / PSIHighPct <= 0 disable
// their conditions.
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
	// ConfigVersion is the daemon-host settings row's save counter (RUYI-729
	// plane only; workspace cards leave it zero). Reports carry it so the
	// event history can tell which edit produced an episode; ack delivery
	// carries it so the daemon can skip re-applying an unchanged card.
	// Version 0 means "not DB-managed".
	ConfigVersion int64 `json:"config_version,omitempty"`
	// Source names where a daemon-host card came from: "db" (settings row)
	// or "env" (daemon boot config). Ack delivery omits it — the daemon
	// knows what it applied once the card lands.
	Source string `json:"source,omitempty"`
}

// DefaultDaemonBackpressureConfig returns the code defaults as a wire card —
// what a workspace gets before its owner ever saved the settings form, and
// what the server reports for a daemon host that never saved a row.
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

// Validate rejects configs whose hysteresis bands are inverted or degenerate:
// recovery thresholds must sit on the safe side of their high counterparts
// (above for MemAvailable, below for SwapUsed and PSI), or the gate would
// flap at the boundary it exists to damp. Sample interval and window must be
// positive so the watcher actually moves. Thresholds are validated even when
// Enabled is false, so re-enabling can never activate a stored-invalid card.
// The settings APIs surface these strings verbatim; the daemon logs them and
// ignores the card.
func (c *DaemonBackpressureConfig) Validate() error {
	if c.MemHighPct <= 0 || c.MemHighPct > 100 {
		return fmt.Errorf("backpressure: mem high watermark %g%% out of range (0,100]", c.MemHighPct)
	}
	if c.MemRecoveryPct <= c.MemHighPct || c.MemRecoveryPct > 100 {
		return fmt.Errorf("backpressure: mem recovery watermark %g%% must be above the high watermark %g%% (hysteresis)", c.MemRecoveryPct, c.MemHighPct)
	}
	if c.SwapHighPct > 0 {
		if c.SwapHighPct > 100 {
			return fmt.Errorf("backpressure: swap high watermark %g%% out of range (0,100]", c.SwapHighPct)
		}
		if c.SwapRecoveryPct < 0 || c.SwapRecoveryPct >= c.SwapHighPct {
			return fmt.Errorf("backpressure: swap recovery watermark %g%% must be below the high watermark %g%% (hysteresis)", c.SwapRecoveryPct, c.SwapHighPct)
		}
	}
	if c.PSIHighPct > 0 {
		if c.PSIHighPct > 100 {
			return fmt.Errorf("backpressure: psi high watermark %g%% out of range (0,100]", c.PSIHighPct)
		}
		if c.PSIRecoveryPct < 0 || c.PSIRecoveryPct >= c.PSIHighPct {
			return fmt.Errorf("backpressure: psi recovery watermark %g%% must be below the high watermark %g%% (hysteresis)", c.PSIRecoveryPct, c.PSIHighPct)
		}
	}
	if c.SampleIntervalSeconds <= 0 {
		return fmt.Errorf("backpressure: sample interval %s must be positive", time.Duration(c.SampleIntervalSeconds)*time.Second)
	}
	if c.SampleIntervalSeconds > MaxBackpressureSampleIntervalSeconds {
		return fmt.Errorf("backpressure: sample interval %d must not exceed %d", c.SampleIntervalSeconds, MaxBackpressureSampleIntervalSeconds)
	}
	if c.WindowSize < 1 {
		return fmt.Errorf("backpressure: window size %d must be at least 1", c.WindowSize)
	}
	if c.WindowSize > MaxBackpressureWindowSize {
		return fmt.Errorf("backpressure: window size %d must not exceed %d", c.WindowSize, MaxBackpressureWindowSize)
	}
	return nil
}

// ValidateBackpressureConfig is the nil-tolerant form of Validate, kept for
// the daemon-host save path (RUYI-729) whose callers treat a missing card as
// "nothing to check", not a rejection.
func ValidateBackpressureConfig(c *DaemonBackpressureConfig) error {
	if c == nil {
		return nil
	}
	return c.Validate()
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
