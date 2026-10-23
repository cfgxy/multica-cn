package protocol

import "fmt"

// AgentBackpressureConfig is the agent-level error-rate backpressure card
// (RUYI-859 stage A: config surface and read-only visibility; the automatic
// suppression loop is stage B). It is the wire shape for the agent detail
// page's settings tab and for the policy row's audit snapshots.
//
// Semantics: the entry judgment aggregates terminal task samples (completed
// + failed, completed_at inside a full trailing window_minutes) and counts
// the failures whose taskfailure display class is selected by the two
// inclusion switches; the rate arms the suppression at
// error_rate_threshold_pct with at least min_samples samples. The recovery
// judgment aggregates over a full trailing recovery_window_minutes, must
// reach min_samples, and clears the suppression only while the rate stays
// below the FIXED 0.5 x error_rate_threshold_pct — not an independently
// editable threshold (RUYI-740 form-usability ruling).
//
// An unsaved agent has no row; the API reads the defaults through
// DefaultAgentBackpressureConfig: monitoring only, automatic suppression off.
type AgentBackpressureConfig struct {
	// WindowMinutes is the trailing window, in minutes (>=1), the entry
	// judgment aggregates over.
	WindowMinutes int `json:"window_minutes"`
	// ErrorRateThresholdPct is the aggregated failure percentage (0,100]
	// that arms the suppression; the recovery threshold is fixed at 0.5x
	// this value and never stored as a second editable field.
	ErrorRateThresholdPct float64 `json:"error_rate_threshold_pct"`
	// MinSamples is the minimum terminal samples inside the window before the
	// rate is evaluated at all.
	MinSamples int `json:"min_samples"`
	// RecoveryWindowMinutes is the full trailing window, in minutes (>=1),
	// the recovery judgment aggregates over.
	RecoveryWindowMinutes int `json:"recovery_window_minutes"`
	// SuppressOnErrorRate is the automatic-suppression master switch.
	// Default false: an unsaved or default agent is monitored only.
	SuppressOnErrorRate bool `json:"suppress_on_error_rate"`
	// CountModelAPIFailures selects the model/API-side display classes
	// (auth, rate_limit, timeout, provider) in the failure numerator.
	CountModelAPIFailures bool `json:"count_model_api_failures"`
	// CountExecutionFailures selects the platform execution display classes
	// (runtime, agent, other including unclassified) in the numerator.
	CountExecutionFailures bool `json:"count_execution_failures"`
}

// DefaultAgentBackpressureConfig is what the read model reports for an agent
// whose workspace admin never saved a policy row.
func DefaultAgentBackpressureConfig() *AgentBackpressureConfig {
	return &AgentBackpressureConfig{
		WindowMinutes:          60,
		ErrorRateThresholdPct:  30,
		MinSamples:             5,
		RecoveryWindowMinutes:  30,
		SuppressOnErrorRate:    false,
		CountModelAPIFailures:  true,
		CountExecutionFailures: true,
	}
}

// AgentBackpressureRecoveryFactor is the fixed recovery multiplier: the
// automatic loop clears a suppression only while the aggregated rate over
// the full recovery window stays below threshold x factor. It is a product
// constant, never a per-agent field.
const AgentBackpressureRecoveryFactor = 0.5

// ValidateAgentBackpressureConfig is the save-path boundary check, mirroring
// ValidateBackpressureConfig's shape (nil-tolerant). Thresholds are
// validated even when SuppressOnErrorRate is false, so toggling the switch
// on can never arm a stored-invalid card.
func ValidateAgentBackpressureConfig(c *AgentBackpressureConfig) error {
	if c == nil {
		return nil
	}
	if c.WindowMinutes < 1 {
		return fmt.Errorf("backpressure: window_minutes %d must be at least 1", c.WindowMinutes)
	}
	if c.ErrorRateThresholdPct <= 0 || c.ErrorRateThresholdPct > 100 {
		return fmt.Errorf("backpressure: error_rate_threshold_pct %g out of range (0,100]", c.ErrorRateThresholdPct)
	}
	if c.MinSamples < 1 {
		return fmt.Errorf("backpressure: min_samples %d must be at least 1", c.MinSamples)
	}
	if c.RecoveryWindowMinutes < 1 {
		return fmt.Errorf("backpressure: recovery_window_minutes %d must be at least 1", c.RecoveryWindowMinutes)
	}
	return nil
}
