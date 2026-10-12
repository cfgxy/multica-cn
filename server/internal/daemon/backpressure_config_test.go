package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Stage-1 hot reload (RUYI-729): a saved settings card rides the next
// heartbeat ack and must rebuild the gate — new thresholds, window carry-over,
// same-version no-op, invalid cards rejected, disabled cards clearing the
// episode. Each case exercises applyBackpressureConfig against the same
// fixture-backed daemon the gate tests use.

// bpCardWith builds a card from explicit fields, versioned so every apply in
// a test moves the version forward (the field the server bumps on save).
func bpCardWith(version int64, mutate func(*protocol.DaemonBackpressureConfig)) protocol.DaemonBackpressureConfig {
	card := backpressureTestCard(bpTestThresholds())
	card.ConfigVersion = version
	card.Source = protocol.BackpressureSourceDB
	if mutate != nil {
		mutate(&card)
	}
	return card
}

func TestApplyBackpressureConfig_RebuildsWithNewThresholds(t *testing.T) {
	d, _ := newBackpressureTestDaemon(t)
	if d.bpRuntime.Load() == nil {
		t.Fatal("runtime container missing")
	}

	// The acceptance line for stage 1: after a config-version bump the state
	// machine judges samples against the NEW thresholds. Boot card trips at
	// mem<15; the saved card at version 2 trips at mem<35 — a 30% sample is
	// healthy under the old lines, backpressured under the new ones.
	card := bpCardWith(2, func(c *protocol.DaemonBackpressureConfig) {
		c.MemHighPct = 35
		c.MemRecoveryPct = 55
	})
	d.applyBackpressureConfig("rt-1", &card)

	rt := d.bpRuntime.Load()
	if rt == nil || rt.machine == nil {
		t.Fatal("gate must stay enabled after a valid card")
	}
	if rt.config.ConfigVersion != 2 || rt.config.MemHighPct != 35 {
		t.Fatalf("card not applied: version=%d mem_high=%g", rt.config.ConfigVersion, rt.config.MemHighPct)
	}

	// A 30%-available host is healthy under the boot lines (trip at 15) but
	// backpressured under the saved ones — judge it through the real tick
	// path, which is what feeds bpState.
	writeWatermarkFixtures(t, fixtureDir(d), 30, 10)
	d.backpressureTick()
	if !d.backpressureBlocked() {
		t.Fatal("30% available must block under the new 35% line")
	}
}

func TestApplyBackpressureConfig_SameVersionIsNoOp(t *testing.T) {
	d, _ := newBackpressureTestDaemon(t)
	// Byte-identical to the boot card the daemon was built with: same
	// version (0), same (empty) source, same fields.
	card := backpressureTestCard(bpTestThresholds())
	before := d.bpRuntime.Load()

	d.applyBackpressureConfig("rt-1", &card)

	if d.bpRuntime.Load() != before {
		t.Fatal("unchanged card must not rebuild the gate")
	}
}

func TestApplyBackpressureConfig_InvalidCardRejected(t *testing.T) {
	d, _ := newBackpressureTestDaemon(t)
	before := d.bpRuntime.Load()

	// Inverted hysteresis: recovery below the trigger. Server-side
	// ValidateBackpressureConfig should never let this through; the daemon
	// still refuses it rather than running a flapping gate.
	card := bpCardWith(3, func(c *protocol.DaemonBackpressureConfig) {
		c.MemHighPct = 50
		c.MemRecoveryPct = 20
	})
	d.applyBackpressureConfig("rt-1", &card)

	if d.bpRuntime.Load() != before {
		t.Fatal("invalid card must be dropped; previous gate stays in force")
	}
}

func TestApplyBackpressureConfig_WindowCarriesOver(t *testing.T) {
	d, _ := newBackpressureTestDaemon(t)
	prev := d.bpRuntime.Load().machine
	prev.observe(sample(40, 10)) // boot window holds one sample (size 1)

	card := bpCardWith(2, func(c *protocol.DaemonBackpressureConfig) {
		c.WindowSize = 6
	})
	d.applyBackpressureConfig("rt-1", &card)

	built := d.bpRuntime.Load().machine
	if built == prev {
		t.Fatal("new card must rebuild the machine")
	}
	if len(built.window) != 1 {
		t.Fatalf("smoothing window must carry over, got %d samples", len(built.window))
	}
	if built.windowSize != 6 {
		t.Fatalf("window size = %d, want the card's 6", built.windowSize)
	}
}

func TestApplyBackpressureConfig_DisabledClearsEpisode(t *testing.T) {
	d, _ := newBackpressureTestDaemon(t)

	// Build an active episode first: 10% available trips the boot line. The
	// state rides bpState, so drive it through the real tick path.
	writeWatermarkFixtures(t, fixtureDir(d), 10, 10)
	d.backpressureTick()
	if !d.backpressureBlocked() {
		t.Fatal("episode must be active before the disable arrives")
	}

	card := bpCardWith(2, func(c *protocol.DaemonBackpressureConfig) {
		c.Enabled = false
	})
	d.applyBackpressureConfig("rt-1", &card)

	rt := d.bpRuntime.Load()
	if rt.machine != nil {
		t.Fatal("disabled card must leave the gate without a machine")
	}
	if d.backpressureBlocked() {
		t.Fatal("disabled gate must fail open — claims resume immediately")
	}
	st := d.bpState.Load()
	if st == nil || st.Active {
		t.Fatal("active episode must clear the moment the gate is disabled")
	}
	if st.Config == nil || st.Config.Enabled {
		t.Fatal("the inactive report must carry the disabled card for the UI")
	}
}

func TestApplyBackpressureConfig_SamplingIntervalHotSwaps(t *testing.T) {
	d, _ := newBackpressureTestDaemon(t)
	if got := d.bpInterval(); got != time.Second {
		t.Fatalf("boot cadence = %v, want 1s", got)
	}

	card := bpCardWith(2, func(c *protocol.DaemonBackpressureConfig) {
		c.SampleIntervalSeconds = 30
	})
	d.applyBackpressureConfig("rt-1", &card)

	if got := d.bpInterval(); got != 30*time.Second {
		t.Fatalf("cadence after save = %v, want 30s", got)
	}
}

// backpressureBootConfig mirrors newBackpressureTestDaemon's boot card so
// the fallback path (d.cfg.backpressureCard) rebuilds the same gate the
// daemon booted with.
func backpressureBootConfig() Config {
	th := bpTestThresholds()
	return Config{
		BackpressureEnabled:         true,
		BackpressureMemHighPct:      th.MemHighPct,
		BackpressureMemRecoveryPct:  th.MemRecoveryPct,
		BackpressureSwapHighPct:     th.SwapHighPct,
		BackpressureSwapRecoveryPct: th.SwapRecoveryPct,
		BackpressurePSIHighPct:      th.PSIHighPct,
		BackpressurePSIRecoveryPct:  th.PSIRecoveryPct,
		BackpressureSampleInterval:  time.Second,
		BackpressureWindowSize:      1,
	}
}

// D1 rework (RUYI-729): after the owner deletes the saved settings row the
// ack stops carrying a card. A daemon that once carried a saved card must
// converge back to its boot config (env overrides or code defaults) on the
// next beat — held runtimes release and deferred claims stop without a
// restart. Keeping the carved thresholds forever is the defect.
func TestHeartbeatAckWithoutCardRestoresBootConfigAfterDBCard(t *testing.T) {
	d, dir := newBackpressureTestDaemon(t)
	d.cfg = backpressureBootConfig()

	// Host saved settings: a DB card at version 2 with thresholds carved
	// above the boot lines (mem trips at 35 instead of 15).
	card := bpCardWith(2, func(c *protocol.DaemonBackpressureConfig) {
		c.MemHighPct = 35
		c.MemRecoveryPct = 55
	})
	d.handleHeartbeatActions(context.Background(), "rt-1", &HeartbeatResponse{BackpressureConfig: &card})
	if rt := d.bpRuntime.Load(); rt == nil || rt.config.ConfigVersion != 2 || rt.config.MemHighPct != 35 {
		t.Fatalf("saved card not applied first: %+v", d.bpRuntime.Load())
	}
	writeWatermarkFixtures(t, fixtureDir(d), 30, 10)
	d.backpressureTick()
	if !d.backpressureBlocked() {
		t.Fatal("fixture setup: 30% available must block under the carved 35% line")
	}

	// Owner deletes the row: the next heartbeat ack delivers no card.
	d.handleHeartbeatActions(context.Background(), "rt-1", &HeartbeatResponse{})

	rt := d.bpRuntime.Load()
	if rt == nil || rt.machine == nil {
		t.Fatal("gate must stay enabled after the settings row is removed")
	}
	if rt.config.ConfigVersion != 0 || rt.config.Source != "" || rt.config.MemHighPct != 15 {
		t.Fatalf("gate not restored to boot config: version=%d source=%q mem_high=%g",
			rt.config.ConfigVersion, rt.config.Source, rt.config.MemHighPct)
	}

	// The restored gate judges samples by the boot lines: the same 30% host
	// that was backpressured under the carved lines is healthy again — the
	// hold releases without a restart.
	d.backpressureTick()
	if d.backpressureBlocked() {
		t.Fatal("30%% available must not block under the restored boot lines")
	}
	_ = dir
}

// The other half of the D1 semantic: a daemon that never received a saved
// card keeps its boot config when the ack carries no card — "no settings
// row" is just the unconfigured state, not a reset signal.
func TestHeartbeatAckWithoutCardKeepsBootWhenNeverManaged(t *testing.T) {
	d, _ := newBackpressureTestDaemon(t)
	d.cfg = backpressureBootConfig()
	before := d.bpRuntime.Load()

	d.handleHeartbeatActions(context.Background(), "rt-1", &HeartbeatResponse{})

	if d.bpRuntime.Load() != before {
		t.Fatal("a daemon that never received a saved card must keep its boot config")
	}
}
