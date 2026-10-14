package protocol

import (
	"strings"
	"testing"
)

// TestDaemonBackpressureConfigValidate pins the hysteresis rules the
// settings APIs (the 422 workspace surface and the RUYI-729 runtime-host
// surface), the daemon hot-apply path (ignore-and-keep), and the boot
// defaults check all enforce through this one rulebook.
func TestDaemonBackpressureConfigValidate(t *testing.T) {
	valid := DefaultDaemonBackpressureConfig()
	if err := valid.Validate(); err != nil {
		t.Fatalf("defaults must validate: %v", err)
	}

	cases := []struct {
		name     string
		mutate   func(c *DaemonBackpressureConfig)
		fragment string
	}{
		{"mem high out of range", func(c *DaemonBackpressureConfig) { c.MemHighPct = 101 }, "mem high watermark"},
		{"mem high zero", func(c *DaemonBackpressureConfig) { c.MemHighPct = 0 }, "mem high watermark"},
		{"mem hysteresis inverted", func(c *DaemonBackpressureConfig) { c.MemHighPct, c.MemRecoveryPct = 40, 30 }, "mem recovery watermark"},
		{"swap high out of range", func(c *DaemonBackpressureConfig) { c.SwapHighPct = 120 }, "swap high watermark"},
		{"swap hysteresis inverted", func(c *DaemonBackpressureConfig) { c.SwapHighPct, c.SwapRecoveryPct = 50, 60 }, "swap recovery watermark"},
		{"psi high out of range", func(c *DaemonBackpressureConfig) { c.PSIHighPct = 101 }, "psi high watermark"},
		{"psi hysteresis inverted", func(c *DaemonBackpressureConfig) { c.PSIHighPct, c.PSIRecoveryPct = 30, 40 }, "psi recovery watermark"},
		{"sample interval zero", func(c *DaemonBackpressureConfig) { c.SampleIntervalSeconds = 0 }, "sample interval"},
		{"sample interval negative", func(c *DaemonBackpressureConfig) { c.SampleIntervalSeconds = -5 }, "sample interval"},
		{"sample interval above max", func(c *DaemonBackpressureConfig) { c.SampleIntervalSeconds = MaxBackpressureSampleIntervalSeconds + 1 }, "sample interval"},
		{"window size zero", func(c *DaemonBackpressureConfig) { c.WindowSize = 0 }, "window size"},
		{"window size above max", func(c *DaemonBackpressureConfig) { c.WindowSize = MaxBackpressureWindowSize + 1 }, "window size"},
	}
	for _, tc := range cases {
		c := DefaultDaemonBackpressureConfig()
		tc.mutate(c)
		err := c.Validate()
		if err == nil {
			t.Fatalf("%s: expected rejection", tc.name)
		}
		if !strings.Contains(err.Error(), tc.fragment) {
			t.Fatalf("%s: error %q must name the broken condition", tc.name, err)
		}
	}

	// Disabled cards validate structure too, so re-enabling can never
	// activate a stored-invalid card.
	disabled := DefaultDaemonBackpressureConfig()
	disabled.Enabled = false
	disabled.MemHighPct, disabled.MemRecoveryPct = 90, 10
	if err := disabled.Validate(); err == nil {
		t.Fatal("disabled card with inverted hysteresis must still be rejected")
	}

	// Sentinel disables are first-class: swap/PSI high <= 0 turns the
	// condition off without requiring its recovery half.
	noSwapPSI := DefaultDaemonBackpressureConfig()
	noSwapPSI.SwapHighPct, noSwapPSI.SwapRecoveryPct = 0, 0
	noSwapPSI.PSIHighPct, noSwapPSI.PSIRecoveryPct = 0, 0
	if err := noSwapPSI.Validate(); err != nil {
		t.Fatalf("mem-only card must validate: %v", err)
	}

	// The upper bounds are allocation and cadence guards: the boundary
	// values themselves stay legal so an owner can still run an hour-scale
	// gate, but a fat-fingered huge value cannot reach the daemon's
	// window allocation.
	atMax := DefaultDaemonBackpressureConfig()
	atMax.SampleIntervalSeconds = MaxBackpressureSampleIntervalSeconds
	atMax.WindowSize = MaxBackpressureWindowSize
	if err := atMax.Validate(); err != nil {
		t.Fatalf("boundary-max card must validate: %v", err)
	}
}

// TestValidateBackpressureConfig covers the nil-tolerant wrapper over the
// same rulebook: table-driven over the hysteresis invariants, plus the nil
// card case.
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

	// A nil card is a no-op, not a rejection — the daemon-host save path
	// (RUYI-729) passes nil for hosts that never saved a settings row.
	if err := ValidateBackpressureConfig(nil); err != nil {
		t.Fatalf("nil card must pass, got %v", err)
	}
}
