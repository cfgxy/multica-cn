package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Daemon-host override plane (RUYI-729): a saved host card rides the next
// heartbeat ack and must shadow the workspace/boot gates — new thresholds,
// window carry-over, identical-delivery no-op, invalid cards rejected,
// disabled cards clearing the episode, and a deleted row (nil card)
// converging the override away. Each case exercises
// applyBackpressureHostConfig against the same fixture-backed daemon the
// gate tests use.

// bpCardWith builds a host card from explicit fields, versioned so every
// apply in a test moves the version forward (the field the server bumps on
// save).
func bpCardWith(version int64, mutate func(*protocol.DaemonBackpressureConfig)) protocol.DaemonBackpressureConfig {
	card := backpressureTestCard(bpTestThresholds())
	card.ConfigVersion = version
	card.Source = protocol.BackpressureSourceDB
	if mutate != nil {
		mutate(&card)
	}
	return card
}

func TestApplyBackpressureHostConfig_RebuildsWithNewThresholds(t *testing.T) {
	d, _ := newBackpressureTestDaemon(t)
	seedWorkspaceBindings(t, d, map[string][]string{"ws-1": {"rt-1"}})
	if d.bpHostRuntime.Load() != nil {
		t.Fatal("no host card yet — the override must start absent")
	}

	// The acceptance line for the host plane: after a config-version bump
	// the state machine judges samples against the NEW thresholds. The
	// workspace/boot lines trip at mem<15; the saved card at version 2
	// trips at mem<35 — a 30% sample is healthy under the old lines,
	// backpressured under the new one.
	card := bpCardWith(2, func(c *protocol.DaemonBackpressureConfig) {
		c.MemHighPct = 35
		c.MemRecoveryPct = 55
	})
	d.applyBackpressureHostConfig("rt-1", &card)

	rt := d.bpHostRuntime.Load()
	if rt == nil || rt.machine == nil || !rt.enabled {
		t.Fatal("gate must stay enabled after a valid card")
	}
	if rt.config.ConfigVersion != 2 || rt.config.MemHighPct != 35 {
		t.Fatalf("card not applied: version=%d mem_high=%g", rt.config.ConfigVersion, rt.config.MemHighPct)
	}

	// A 30%-available host is healthy under the old lines (trip at 15) but
	// backpressured under the saved one — judge it through the real tick
	// path, which is what feeds the host observation, and read it through
	// the workspace the runtime belongs to (the override shadows it).
	writeWatermarkFixtures(t, fixtureDir(d), 30, 10)
	d.backpressureTick()
	if !d.backpressureBlockedForWorkspace("ws-1") {
		t.Fatal("30% available must block under the host card's 35% line")
	}
}

func TestApplyBackpressureHostConfig_IdenticalDeliveryIsNoOp(t *testing.T) {
	d, _ := newBackpressureTestDaemon(t)
	card := bpCardWith(2, nil)
	d.applyBackpressureHostConfig("rt-1", &card)
	before := d.bpHostRuntime.Load()
	if before == nil {
		t.Fatal("first delivery must install the host card")
	}

	d.applyBackpressureHostConfig("rt-1", &card)

	if d.bpHostRuntime.Load() != before {
		t.Fatal("identical repeat delivery must not rebuild the host gate")
	}
}

func TestApplyBackpressureHostConfig_InvalidCardRejected(t *testing.T) {
	d, _ := newBackpressureTestDaemon(t)

	// Inverted hysteresis: recovery below the trigger. Server-side
	// ValidateBackpressureConfig should never let this through; the daemon
	// still refuses it rather than running a flapping gate.
	card := bpCardWith(3, func(c *protocol.DaemonBackpressureConfig) {
		c.MemHighPct = 50
		c.MemRecoveryPct = 20
	})
	d.applyBackpressureHostConfig("rt-1", &card)

	if rt := d.bpHostRuntime.Load(); rt != nil {
		t.Fatal("invalid card must be dropped; no host override may appear")
	}
}

func TestApplyBackpressureHostConfig_WindowCarriesOver(t *testing.T) {
	d, _ := newBackpressureTestDaemon(t)
	first := bpCardWith(1, nil)
	d.applyBackpressureHostConfig("rt-1", &first)
	prev := d.bpHostRuntime.Load().machine
	prev.observe(sample(40, 10)) // host window holds one sample (size 1)

	card := bpCardWith(2, func(c *protocol.DaemonBackpressureConfig) {
		c.WindowSize = 6
	})
	d.applyBackpressureHostConfig("rt-1", &card)

	built := d.bpHostRuntime.Load().machine
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

func TestApplyBackpressureHostConfig_DisabledClearsEpisode(t *testing.T) {
	d, _ := newBackpressureTestDaemon(t)
	seedWorkspaceBindings(t, d, map[string][]string{"ws-1": {"rt-1"}})

	// Build an active host episode first: 30% available trips the card's
	// raised line through the real tick path.
	card := bpCardWith(1, func(c *protocol.DaemonBackpressureConfig) {
		c.MemHighPct = 35
		c.MemRecoveryPct = 55
	})
	d.applyBackpressureHostConfig("rt-1", &card)
	writeWatermarkFixtures(t, fixtureDir(d), 30, 10)
	d.backpressureTick()
	if !d.backpressureBlockedForWorkspace("ws-1") {
		t.Fatal("episode must be active before the disable arrives")
	}

	disabled := bpCardWith(2, func(c *protocol.DaemonBackpressureConfig) {
		c.Enabled = false
	})
	d.applyBackpressureHostConfig("rt-1", &disabled)

	rt := d.bpHostRuntime.Load()
	if rt == nil || rt.enabled {
		t.Fatal("disabled card must take the host slot with enabled=false")
	}
	if d.backpressureBlockedForWorkspace("ws-1") {
		t.Fatal("disabled host gate must fail open — claims resume immediately")
	}
	if st := d.bpHostState.Load(); st != nil {
		t.Fatal("active episode must clear the moment the host gate is disabled")
	}
}

func TestApplyBackpressureHostConfig_SamplingIntervalHotSwaps(t *testing.T) {
	d, _ := newBackpressureTestDaemon(t)
	if got := d.backpressureSampleInterval(); got != DefaultBackpressureSampleInterval {
		t.Fatalf("cadence before any card = %v, want the code default", got)
	}

	// The watcher runs at the FASTEST enabled source's cadence, so a host
	// card only shows up here by demanding a faster beat than the other
	// sources — a slower card must never slow the rest down.
	card := bpCardWith(2, func(c *protocol.DaemonBackpressureConfig) {
		c.SampleIntervalSeconds = 1
	})
	d.applyBackpressureHostConfig("rt-1", &card)

	if got := d.backpressureSampleInterval(); got != time.Second {
		t.Fatalf("cadence after save = %v, want the host card's 1s", got)
	}
}

// backpressureBootConfig mirrors the boot card the fallback should converge
// to after the override lifts: the boot gate installed via
// setBackpressureRuntime carries these same lines.
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

// D1 rework (RUYI-729): after the owner deletes the saved host settings row
// the ack stops carrying a card. A daemon that once carried a saved card
// must converge the override away on the next beat — the workspace/boot
// gates resume governing (their lines re-judge the same host) without a
// restart. Keeping the carved thresholds forever is the defect.
func TestHeartbeatAckWithoutCardRestoresBootConfigAfterDBCard(t *testing.T) {
	d, _ := newBackpressureTestDaemon(t)
	d.cfg = backpressureBootConfig()
	setBackpressureRuntime(d, bpTestThresholds(), 1)
	seedWorkspaceBindings(t, d, map[string][]string{"ws-1": {"rt-1"}})

	// Host saved settings: a DB card at version 2 with thresholds carved
	// above the boot lines (mem trips at 35 instead of 15).
	card := bpCardWith(2, func(c *protocol.DaemonBackpressureConfig) {
		c.MemHighPct = 35
		c.MemRecoveryPct = 55
	})
	d.handleHeartbeatActions(context.Background(), "rt-1", &HeartbeatResponse{DaemonBackpressureConfig: &card})
	if rt := d.bpHostRuntime.Load(); rt == nil || rt.config.ConfigVersion != 2 || rt.config.MemHighPct != 35 {
		t.Fatalf("saved card not applied first: %+v", d.bpHostRuntime.Load())
	}
	writeWatermarkFixtures(t, fixtureDir(d), 30, 10)
	d.backpressureTick()
	if !d.backpressureBlockedForWorkspace("ws-1") {
		t.Fatal("fixture setup: 30% available must block under the carved 35% line")
	}

	// Owner deletes the row: the next heartbeat ack delivers no card.
	d.handleHeartbeatActions(context.Background(), "rt-1", &HeartbeatResponse{})

	if rt := d.bpHostRuntime.Load(); rt != nil {
		t.Fatalf("host override must converge away once the row is gone: %+v", rt.config)
	}
	if rt := d.bpRuntime.Load(); rt == nil || rt.config.MemHighPct != 15 {
		t.Fatalf("boot gate must still carry the boot lines: %+v", rt)
	}

	// The boot lines judge samples again: the same 30% host that was
	// backpressured under the carved lines is healthy — the hold releases
	// without a restart.
	d.backpressureTick()
	if d.backpressureBlockedForWorkspace("ws-1") {
		t.Fatal("30% available must not block under the restored boot lines")
	}
}

// The other half of the D1 semantic: a daemon that never received a saved
// card keeps its boot config when the ack carries no card — "no settings
// row" is just the unconfigured state, not a reset signal.
func TestHeartbeatAckWithoutCardKeepsBootWhenNeverManaged(t *testing.T) {
	d, _ := newBackpressureTestDaemon(t)
	d.cfg = backpressureBootConfig()
	setBackpressureRuntime(d, bpTestThresholds(), 1)
	before := d.bpRuntime.Load()

	d.handleHeartbeatActions(context.Background(), "rt-1", &HeartbeatResponse{})

	if d.bpHostRuntime.Load() != nil {
		t.Fatal("no override may appear from a nil-card ack on an unmanaged daemon")
	}
	if d.bpRuntime.Load() != before {
		t.Fatal("a daemon that never received a saved card must keep its boot config")
	}
}
