package protocol

import (
	"fmt"
	"time"
)

// Workspace-level host-memory backpressure settings (RUYI-618). The settings
// UI saves them per workspace; every heartbeat ack delivers the effective
// card to the workspace's daemons, which hot-apply it to the admission gate.
//
// The defaults live here — next to the wire type — so the daemon boot path,
// the server's heartbeat delivery, and the settings API read one shared
// source instead of three copies that can drift. They are INITIAL SAFETY
// PARAMETERS chosen from the RUYI-392 incident profile, not calibrated final
// values; tune them per workspace from the settings UI.
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
)

// DaemonBackpressureConfig is the workspace's effective backpressure card as
// carried on a heartbeat ack. Trigger (OR): MemAvailable% < MemHigh OR
// SwapUsed% > SwapHigh OR PSI some-avg10 > PSIHigh. Recovery (AND):
// MemAvailable% >= MemRecovery AND SwapUsed% <= SwapRecovery AND PSI <=
// PSIRecovery. SwapHighPct / PSIHighPct <= 0 disable their conditions.
type DaemonBackpressureConfig struct {
	// Enabled is the gate master switch: false pauses nothing regardless of
	// the watermarks.
	Enabled bool `json:"enabled"`
	// MemHighPct is the MemAvailable%% below which new claims pause,
	// percent of total; range (0,100].
	MemHighPct float64 `json:"mem_high_pct"`
	// MemRecoveryPct is the MemAvailable%% above which claiming resumes;
	// must exceed MemHighPct (hysteresis).
	MemRecoveryPct float64 `json:"mem_recovery_pct"`
	// SwapHighPct is the SwapUsed%% above which new claims pause; <=0
	// disables the swap condition.
	SwapHighPct float64 `json:"swap_high_pct"`
	// SwapRecoveryPct is the SwapUsed%% below which claiming resumes; must
	// sit below SwapHighPct when the swap condition is enabled.
	SwapRecoveryPct float64 `json:"swap_recovery_pct"`
	// PSIHighPct is the memory PSI some-avg10%% above which new claims
	// pause; <=0 disables the PSI condition.
	PSIHighPct float64 `json:"psi_high_pct"`
	// PSIRecoveryPct is the PSI some-avg10%% below which claiming resumes;
	// must sit below PSIHighPct when the PSI condition is enabled.
	PSIRecoveryPct float64 `json:"psi_recovery_pct"`
	// SampleIntervalSeconds is the /proc sampling cadence for the gate, in
	// whole seconds (>=1).
	SampleIntervalSeconds int `json:"sample_interval_seconds"`
	// WindowSize is the smoothing window in samples before thresholds are
	// evaluated on the mean (>=1).
	WindowSize int `json:"window_size"`
}

// DefaultDaemonBackpressureConfig returns the code defaults as a wire card —
// what a workspace gets before its owner ever saved the settings form.
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
// The settings API surfaces these strings verbatim; the daemon logs them and
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
	if c.WindowSize < 1 {
		return fmt.Errorf("backpressure: window size %d must be at least 1", c.WindowSize)
	}
	return nil
}
