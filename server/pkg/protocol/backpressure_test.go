package protocol

import "testing"

// The settings save path is the only writer of the gate card (RUYI-729), so
// the validator is the wall between a fat-fingered admin and a gate that
// flaps or wedges. Table-driven over the hysteresis invariants the daemon
// side mirrors in validateBackpressureThresholds.

func TestValidateBackpressureConfig(t *testing.T) {
	valid := func(mutate func(*DaemonBackpressureConfig)) DaemonBackpressureConfig {
		c := DefaultDaemonBackpressureConfig()
		if mutate != nil {
			mutate(c)
		}
		return *c
	}

	cases := []struct {
		name    string
		card    DaemonBackpressureConfig
		wantErr bool
	}{
		{"defaults pass", valid(nil), false},
		{"mem high at lower bound edge", valid(func(c *DaemonBackpressureConfig) { c.MemHighPct = 0.1 }), false},
		{"mem high zero rejected", valid(func(c *DaemonBackpressureConfig) { c.MemHighPct = 0 }), true},
		{"mem high negative rejected", valid(func(c *DaemonBackpressureConfig) { c.MemHighPct = -5 }), true},
		{"mem high over 100 rejected", valid(func(c *DaemonBackpressureConfig) { c.MemHighPct = 101 }), true},
		{"mem recovery equal to high rejected", valid(func(c *DaemonBackpressureConfig) { c.MemHighPct = 30; c.MemRecoveryPct = 30 }), true},
		{"mem recovery below high rejected", valid(func(c *DaemonBackpressureConfig) { c.MemHighPct = 30; c.MemRecoveryPct = 20 }), true},
		{"mem recovery over 100 rejected", valid(func(c *DaemonBackpressureConfig) { c.MemRecoveryPct = 100.1 }), true},
		{"mem recovery at 100 passes", valid(func(c *DaemonBackpressureConfig) { c.MemHighPct = 30; c.MemRecoveryPct = 100 }), false},
		{"swap disabled ignores recovery", valid(func(c *DaemonBackpressureConfig) { c.SwapHighPct = 0; c.SwapRecoveryPct = 0 }), false},
		{"swap band valid", valid(func(c *DaemonBackpressureConfig) { c.SwapHighPct = 80; c.SwapRecoveryPct = 60 }), false},
		{"swap high over 100 rejected", valid(func(c *DaemonBackpressureConfig) { c.SwapHighPct = 120 }), true},
		{"swap recovery equal to high rejected", valid(func(c *DaemonBackpressureConfig) { c.SwapHighPct = 80; c.SwapRecoveryPct = 80 }), true},
		{"swap recovery above high rejected", valid(func(c *DaemonBackpressureConfig) { c.SwapHighPct = 80; c.SwapRecoveryPct = 90 }), true},
		{"psi disabled ignores recovery", valid(func(c *DaemonBackpressureConfig) { c.PSIHighPct = 0; c.PSIRecoveryPct = 99 }), false},
		{"psi band valid", valid(func(c *DaemonBackpressureConfig) { c.PSIHighPct = 50; c.PSIRecoveryPct = 20 }), false},
		{"psi high over 100 rejected", valid(func(c *DaemonBackpressureConfig) { c.PSIHighPct = 100.5 }), true},
		{"psi recovery equal to high rejected", valid(func(c *DaemonBackpressureConfig) { c.PSIHighPct = 50; c.PSIRecoveryPct = 50 }), true},
		{"zero interval rejected", valid(func(c *DaemonBackpressureConfig) { c.SampleIntervalSeconds = 0 }), true},
		{"zero window rejected", valid(func(c *DaemonBackpressureConfig) { c.WindowSize = 0 }), true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			card := tc.card
			err := ValidateBackpressureConfig(&card)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ValidateBackpressureConfig(%+v) error = %v, wantErr %v", card, err, tc.wantErr)
			}
		})
	}

	// A nil card is a no-op, not a rejection — the heartbeat delivery path
	// passes nil for unconfigured hosts.
	if err := ValidateBackpressureConfig(nil); err != nil {
		t.Fatalf("nil card must pass, got %v", err)
	}
}
