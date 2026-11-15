// Package taskbudget defines the optional, immutable scope budget contract.
package taskbudget

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"time"
)

// Profile limits reservations across a lineage. Zero means unlimited. Tokens
// include input + output + cache read + cache write, not context window size.
// Cost is expressed in 1e-10 USD ticks and is checked against reported usage.
type Profile struct {
	MaxRuns              int64 `json:"max_runs,omitempty"`
	MaxTotalTokens       int64 `json:"max_total_tokens,omitempty"`
	MaxCostUSDTicks      int64 `json:"max_cost_usd_ticks,omitempty"`
	PerRunTimeoutSeconds int64 `json:"per_run_timeout_seconds,omitempty"`
}

type Snapshot struct {
	SchemaVersion int     `json:"schema_version"`
	ScopeID       string  `json:"scope_id"`
	Profile       Profile `json:"profile"`
}

// ParseRuntimeConfig rejects malformed profiles instead of turning limits off.
func ParseRuntimeConfig(raw []byte) (*Profile, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var config map[string]json.RawMessage
	if err := json.Unmarshal(raw, &config); err != nil {
		return nil, fmt.Errorf("invalid runtime_config: %w", err)
	}
	value, ok := config["budget_profile"]
	if !ok {
		return nil, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(value, &fields); err != nil || fields == nil {
		return nil, fmt.Errorf("budget_profile must be an object")
	}
	allowed := map[string]bool{"max_runs": true, "max_total_tokens": true, "max_cost_usd_ticks": true, "per_run_timeout_seconds": true}
	for key, value := range fields {
		if !allowed[key] {
			return nil, fmt.Errorf("unknown budget_profile field %q", key)
		}
		var n int64
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, fmt.Errorf("budget_profile.%s must be a nonnegative integer", key)
		}
		if err := json.Unmarshal(value, &n); err != nil || n < 0 {
			return nil, fmt.Errorf("budget_profile.%s must be a nonnegative integer", key)
		}
	}
	var p Profile
	if err := json.Unmarshal(value, &p); err != nil {
		return nil, err
	}
	if p.PerRunTimeoutSeconds > math.MaxInt64/int64(time.Second) {
		return nil, fmt.Errorf("budget_profile.per_run_timeout_seconds is too large")
	}
	return &p, nil
}

func FromContext(raw []byte) (*Snapshot, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var context map[string]json.RawMessage
	if err := json.Unmarshal(raw, &context); err != nil {
		return nil, err
	}
	value, ok := context["budget_scope"]
	if !ok {
		return nil, nil
	}
	var s Snapshot
	if err := json.Unmarshal(value, &s); err != nil || s.ScopeID == "" {
		return nil, fmt.Errorf("invalid budget_scope snapshot")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(value, &fields); err != nil {
		return nil, err
	}
	// Unversioned snapshots are accepted only as the initial implementation's
	// fixture format. Rows without any snapshot remain unbudgeted.
	if version, present := fields["schema_version"]; present {
		if bytes.Equal(bytes.TrimSpace(version), []byte("null")) || s.SchemaVersion != 1 {
			return nil, fmt.Errorf("unsupported budget_scope schema_version")
		}
	}
	s.SchemaVersion = 1
	if _, ok := fields["profile"]; !ok {
		return nil, fmt.Errorf("budget_scope profile missing")
	}
	config, err := json.Marshal(map[string]json.RawMessage{"budget_profile": fields["profile"]})
	if err != nil {
		return nil, err
	}
	p, err := ParseRuntimeConfig(config)
	if err != nil {
		return nil, err
	}
	s.Profile = *p
	return &s, nil
}

// Timeout applies the tighter positive per-execution deadline.
func (p Profile) Timeout(base time.Duration) time.Duration {
	if p.PerRunTimeoutSeconds == 0 {
		return base
	}
	limit := time.Duration(p.PerRunTimeoutSeconds) * time.Second
	if base <= 0 || limit < base {
		return limit
	}
	return base
}

// Denial is empty when admission is allowed. Numeric sums arrive as decimal
// strings so PostgreSQL aggregates cannot overflow int64 before comparison.
func (p Profile) Denial(runs int64, tokens, cost string, unknownCost, closed bool) string {
	if closed {
		return "budget scope is exhausted"
	}
	if p.MaxRuns > 0 && runs >= p.MaxRuns {
		return "budget scope reached max_runs"
	}
	if p.MaxTotalTokens > 0 && reached(tokens, p.MaxTotalTokens) {
		return "budget scope reached max_total_tokens"
	}
	if p.MaxCostUSDTicks > 0 {
		if unknownCost {
			return "budget scope cost is unknown; admission refused"
		}
		if reached(cost, p.MaxCostUSDTicks) {
			return "budget scope reached max_cost_usd_ticks"
		}
	}
	return ""
}

func reached(value string, limit int64) bool {
	n, ok := new(big.Int).SetString(value, 10)
	return !ok || n.Sign() < 0 || n.Cmp(big.NewInt(limit)) >= 0
}
