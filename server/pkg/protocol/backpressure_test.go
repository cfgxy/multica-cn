package protocol

import (
	"strings"
	"testing"
)

// TestDaemonBackpressureConfigValidate pins the hysteresis rules the
// settings API (422 surface), the daemon hot-apply path (ignore-and-keep),
// and the boot defaults check all enforce through this one rulebook.
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
		{"window size zero", func(c *DaemonBackpressureConfig) { c.WindowSize = 0 }, "window size"},
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
}
