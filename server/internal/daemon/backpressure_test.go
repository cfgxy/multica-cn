package daemon

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

// ===== sampler fixtures =====

func writeFixture(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture %s: %v", name, err)
	}
	return path
}

func TestReadMemAvailablePct(t *testing.T) {
	dir := t.TempDir()
	path := writeFixture(t, dir, "meminfo", `MemTotal:       32839692 kB
MemFree:          934528 kB
MemAvailable:    4925954 kB
Buffers:          762312 kB
`)

	got, err := readMemAvailablePct(path)
	if err != nil {
		t.Fatalf("readMemAvailablePct: %v", err)
	}
	// 4925954/32839692 ≈ 15.0% — the RUYI-392 incident band.
	if got < 14.9 || got > 15.1 {
		t.Fatalf("MemAvailablePct = %v, want ~15.0", got)
	}
}

func TestReadMemAvailablePct_Errors(t *testing.T) {
	dir := t.TempDir()

	if _, err := readMemAvailablePct(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("missing file must error")
	}
	empty := writeFixture(t, dir, "empty", "Buffers: 1 kB\n")
	if _, err := readMemAvailablePct(empty); err == nil {
		t.Fatal("meminfo without MemTotal must error")
	}
}

func TestReadSwapUsedPct(t *testing.T) {
	dir := t.TempDir()
	path := writeFixture(t, dir, "swaps", `Filename				Type		Size	Used	Priority
/dev/sda2                               partition	33554428	29875232	-2
/swapfile                               file		1000000	512000	-3
`)

	// (29875232+512000)/(33554428+1000000) ≈ 87.9%
	got := readSwapUsedPct(path)
	if got < 87.8 || got > 88.0 {
		t.Fatalf("SwapUsedPct = %v, want ~87.9", got)
	}
}

func TestReadSwapUsedPct_NoSwap(t *testing.T) {
	dir := t.TempDir()
	headerOnly := writeFixture(t, dir, "swaps", "Filename\t\t\t\tType\t\tSize\tUsed\tPriority\n")
	if got := readSwapUsedPct(headerOnly); got != 0 {
		t.Fatalf("header-only swaps: SwapUsedPct = %v, want 0", got)
	}
	// Missing file (e.g. swap not compiled in) is also "no swap", not an error.
	if got := readSwapUsedPct(filepath.Join(dir, "missing")); got != 0 {
		t.Fatalf("missing swaps file: SwapUsedPct = %v, want 0", got)
	}
}

func TestReadMemoryPressureSomeAvg10(t *testing.T) {
	dir := t.TempDir()
	path := writeFixture(t, dir, "pressure", `some avg10=12.34 avg60=1.00 avg300=0.50 total=987654
full avg10=0.00 avg60=0.00 avg300=0.00 total=0
`)

	got, ok := readMemoryPressureSomeAvg10(path)
	if !ok || got != 12.34 {
		t.Fatalf("PSI some avg10 = %v ok=%v, want 12.34 true", got, ok)
	}

	// Missing file degrades: ok=false, zero value — never fatal for the gate.
	got, ok = readMemoryPressureSomeAvg10(filepath.Join(dir, "missing"))
	if ok || got != 0 {
		t.Fatalf("missing PSI file: avg10=%v ok=%v, want 0 false", got, ok)
	}
}

// ===== hysteresis machine =====

func bpTestThresholds() backpressureThresholds {
	return backpressureThresholds{
		MemHighPct:      15,
		MemRecoveryPct:  25,
		SwapHighPct:     80,
		SwapRecoveryPct: 60,
	}
}

// bpPSITestThresholds extends the watermark pair with the default PSI band.
func bpPSITestThresholds() backpressureThresholds {
	th := bpTestThresholds()
	th.PSIHighPct = 50
	th.PSIRecoveryPct = 20
	return th
}

func sample(memAvail, swapUsed float64) memSample {
	return memSample{MemAvailablePct: memAvail, SwapUsedPct: swapUsed, At: time.Now()}
}

// psiSample builds a sample with an explicit PSI reading, mirroring what the
// sampler produces on hosts with (psiOK true) and without (psiOK false) a
// readable /proc/pressure/memory.
func psiSample(memAvail, swapUsed, psi float64, psiOK bool) memSample {
	s := sample(memAvail, swapUsed)
	s.PSIMemorySomeAvg10 = psi
	s.PSIReadOK = psiOK
	return s
}

func TestBackpressureMachine_TriggersOnEitherCondition(t *testing.T) {
	// OR: mem alone trips. Window 1 so the observed sample IS the smoothed
	// value (window-mean smoothing has its own test below).
	m := newBackpressureMachine(bpTestThresholds(), 1)
	if obs := m.observe(sample(20, 10)); obs.Active {
		t.Fatal("healthy sample must not activate")
	}
	obs := m.observe(sample(10, 10))
	if !obs.Active || obs.Reason != "mem" {
		t.Fatalf("mem-low sample: active=%v reason=%q, want active mem", obs.Active, obs.Reason)
	}
	if !obs.Transitioned {
		t.Fatal("first activation must be a transition")
	}

	// OR: swap alone trips on a fresh machine.
	m2 := newBackpressureMachine(bpTestThresholds(), 1)
	obs = m2.observe(sample(30, 90))
	if !obs.Active || obs.Reason != "swap" {
		t.Fatalf("swap-high sample: active=%v reason=%q, want active swap", obs.Active, obs.Reason)
	}

	// OR: both trip names both.
	m3 := newBackpressureMachine(bpTestThresholds(), 1)
	obs = m3.observe(sample(5, 95))
	if !obs.Active || obs.Reason != "mem+swap" {
		t.Fatalf("both-high sample: active=%v reason=%q, want active mem+swap", obs.Active, obs.Reason)
	}
}

func TestBackpressureMachine_RecoversOnlyOnBothConditions(t *testing.T) {
	// AND: mem recovered but swap still high → stays active.
	m := newBackpressureMachine(bpTestThresholds(), 1)
	if obs := m.observe(sample(10, 90)); !obs.Active {
		t.Fatal("precondition: activate on both-high")
	}
	if obs := m.observe(sample(30, 90)); !obs.Active {
		t.Fatal("mem recovered but swap high must stay active (recovery is AND)")
	}
	if obs := m.observe(sample(30, 50)); obs.Active {
		t.Fatal("both recovered must deactivate")
	}

	// Recovery requires leaving the recovery band on the correct side:
	// mem exactly at the recovery watermark counts as recovered, mem below
	// it does not.
	m2 := newBackpressureMachine(bpTestThresholds(), 1)
	m2.observe(sample(10, 10))
	if obs := m2.observe(sample(24.9, 0)); !obs.Active {
		t.Fatal("24.9% available is below the 25% recovery watermark; must stay active")
	}
	if obs := m2.observe(sample(25.0, 0)); obs.Active {
		t.Fatal("exactly 25% available satisfies recovery; must deactivate")
	}

	// Swap recovery boundary: exactly at the recovery watermark satisfies it.
	m3 := newBackpressureMachine(bpTestThresholds(), 1)
	m3.observe(sample(30, 90))
	if obs := m3.observe(sample(30, 60.0)); obs.Active {
		t.Fatal("swap exactly at 60% recovery watermark must deactivate")
	}

	// The trigger boundary is strict: mem exactly AT the high watermark is
	// not yet low.
	m4 := newBackpressureMachine(bpTestThresholds(), 1)
	if obs := m4.observe(sample(15.0, 0)); obs.Active {
		t.Fatal("exactly 15% available is not below the 15% high watermark")
	}
	if obs := m4.observe(sample(14.9, 0)); !obs.Active {
		t.Fatal("14.9% available is below the high watermark; must activate")
	}
}

func TestBackpressureMachine_NoFlapAroundCriticalWatermark(t *testing.T) {
	// Samples oscillating right below the mem high watermark while swap is
	// deep in pressure: the machine must enter once and stay entered — the
	// recovery AND-condition is nowhere near satisfied, so window smoothing
	// plus hysteresis yield at most 1 transition across the whole episode.
	m := newBackpressureMachine(bpTestThresholds(), 6)
	transitions := 0
	for i := 0; i < 100; i++ {
		mem := 14.0 + float64(i%2) // alternate 14.0 / 15.0, hugging the high mark
		if obs := m.observe(sample(mem, 85)); obs.Transitioned {
			transitions++
		}
	}
	if transitions > 1 {
		t.Fatalf("critical oscillation caused %d transitions in 100 samples, want <= 1", transitions)
	}
	if !m.active {
		t.Fatal("oscillation around the high watermark must leave the gate active")
	}
}

func TestBackpressureMachine_WindowSmoothsSpike(t *testing.T) {
	// A single one-sample spike below the high watermark inside a full
	// window of healthy samples must not trip the gate.
	m := newBackpressureMachine(bpTestThresholds(), 6)
	for i := 0; i < 6; i++ {
		m.observe(sample(40, 10))
	}
	if obs := m.observe(sample(5, 10)); obs.Active {
		t.Fatal("one spiked sample in a healthy window must be smoothed away")
	}
	// A sustained drop does trip once the window fills with low samples.
	for i := 0; i < 6; i++ {
		if obs := m.observe(sample(5, 10)); obs.Active {
			return // tripped after the window absorbed the drop
		}
	}
	t.Fatal("sustained mem-low must trip once the window fills")
}

func TestBackpressureMachine_SwapConditionDisable(t *testing.T) {
	// SwapHighPct <= 0 disables the swap condition entirely — future
	// re-weighting stays a config change.
	th := backpressureThresholds{MemHighPct: 15, MemRecoveryPct: 25, SwapHighPct: 0, SwapRecoveryPct: 0}
	m := newBackpressureMachine(th, 1)
	if obs := m.observe(sample(30, 100)); obs.Active {
		t.Fatal("full swap must not trip when the swap condition is disabled")
	}
	// mem-only trigger and recovery still work.
	m.observe(sample(10, 100))
	if obs := m.observe(sample(30, 100)); obs.Active {
		t.Fatal("recovery must not be blocked by swap when the condition is disabled")
	}
}

func TestBackpressureMachine_TriggersOnPSIAlone(t *testing.T) {
	// OR: mem/swap healthy, PSI alone past the high watermark. The trigger
	// boundary is strict, same as the watermarks: exactly AT the high
	// watermark is not yet past it.
	m := newBackpressureMachine(bpPSITestThresholds(), 1)
	if obs := m.observe(psiSample(40, 10, 50.0, true)); obs.Active {
		t.Fatal("PSI exactly at the high watermark is not yet past it")
	}
	obs := m.observe(psiSample(40, 10, 50.1, true))
	if !obs.Active || obs.Reason != "psi" {
		t.Fatalf("psi-high sample: active=%v reason=%q, want active psi", obs.Active, obs.Reason)
	}
	if !obs.Transitioned {
		t.Fatal("first activation must be a transition")
	}

	// OR: combinations name every past condition.
	m2 := newBackpressureMachine(bpPSITestThresholds(), 1)
	if obs := m2.observe(psiSample(10, 10, 60, true)); !obs.Active || obs.Reason != "mem+psi" {
		t.Fatalf("mem+psi sample: active=%v reason=%q, want active mem+psi", obs.Active, obs.Reason)
	}
	m3 := newBackpressureMachine(bpPSITestThresholds(), 1)
	if obs := m3.observe(psiSample(40, 90, 60, true)); !obs.Active || obs.Reason != "swap+psi" {
		t.Fatalf("swap+psi sample: active=%v reason=%q, want active swap+psi", obs.Active, obs.Reason)
	}
	m4 := newBackpressureMachine(bpPSITestThresholds(), 1)
	if obs := m4.observe(psiSample(10, 90, 60, true)); !obs.Active || obs.Reason != "mem+swap+psi" {
		t.Fatalf("all-high sample: active=%v reason=%q, want active mem+swap+psi", obs.Active, obs.Reason)
	}
}

func TestBackpressureMachine_PSIHysteresis(t *testing.T) {
	// PSI oscillating right below the high watermark must not trip: trigger
	// is strict-greater, so the gate stays off across the whole run.
	m := newBackpressureMachine(bpPSITestThresholds(), 1)
	transitions := 0
	for i := 0; i < 50; i++ {
		psi := 49.0 + float64(i%2) // alternate 49.0 / 50.0, hugging the high mark
		if obs := m.observe(psiSample(40, 10, psi, true)); obs.Transitioned {
			transitions++
		}
	}
	if transitions != 0 {
		t.Fatalf("oscillation below the high watermark tripped %d times, want 0", transitions)
	}

	// Once active, PSI must fall to the recovery watermark to clear; the
	// exact recovery value satisfies it, anything above keeps the gate on
	// (hysteresis band between 20 and 50 absorbs sustained oscillation).
	m2 := newBackpressureMachine(bpPSITestThresholds(), 1)
	m2.observe(psiSample(40, 10, 80, true))
	if obs := m2.observe(psiSample(40, 10, 20.1, true)); !obs.Active {
		t.Fatal("20.1% PSI is above the 20% recovery watermark; must stay active")
	}
	if obs := m2.observe(psiSample(40, 10, 20.0, true)); obs.Active {
		t.Fatal("PSI exactly at the recovery watermark must deactivate")
	}
}

func TestBackpressureMachine_NoFlapOnPSIOscillation(t *testing.T) {
	// With a smoothing window, PSI spiking past the high watermark every
	// other sample averages out and must produce at most the one real
	// transition a sustained stall causes.
	m := newBackpressureMachine(bpPSITestThresholds(), 6)
	for i := 0; i < 6; i++ {
		m.observe(psiSample(40, 10, 0, true))
	}
	transitions := 0
	for i := 0; i < 100; i++ {
		psi := 98.0 * float64(i%2) // alternate 0 / 98
		if obs := m.observe(psiSample(40, 10, psi, true)); obs.Transitioned {
			transitions++
		}
	}
	if transitions != 0 {
		t.Fatalf("alternating PSI spikes inside a smoothing window caused %d transitions, want 0", transitions)
	}
}

func TestBackpressureMachine_PSIReadFailureDegrades(t *testing.T) {
	// An unreadable PSI source carries no vote: it neither trips the gate
	// (a meaningless value must not look like pressure) nor blocks recovery
	// of a gate PSI itself tripped.
	m := newBackpressureMachine(bpPSITestThresholds(), 1)
	if obs := m.observe(psiSample(40, 10, 99, false)); obs.Active {
		t.Fatal("psi-high value with PSIReadOK=false must not trip")
	}

	m2 := newBackpressureMachine(bpPSITestThresholds(), 1)
	m2.observe(psiSample(40, 10, 80, true))
	if obs := m2.observe(psiSample(40, 10, 0, false)); obs.Active {
		t.Fatal("recovery must not be blocked by PSI once readings fail")
	}

	// mem keeps gating independently while PSI is degraded.
	m3 := newBackpressureMachine(bpPSITestThresholds(), 1)
	if obs := m3.observe(psiSample(10, 10, 99, false)); !obs.Active || obs.Reason != "mem" {
		t.Fatalf("mem must still trip while PSI is degraded: active=%v reason=%q", obs.Active, obs.Reason)
	}
}

func TestBackpressureMachine_PSIDisable(t *testing.T) {
	// PSIHighPct <= 0 disables the condition entirely — a full-stall PSI
	// reading must not trip and must not block mem-only recovery.
	th := backpressureThresholds{MemHighPct: 15, MemRecoveryPct: 25, PSIHighPct: 0, PSIRecoveryPct: 0}
	m := newBackpressureMachine(th, 1)
	if obs := m.observe(psiSample(40, 10, 100, true)); obs.Active {
		t.Fatal("full PSI stall must not trip when the condition is disabled")
	}
	m.observe(psiSample(10, 10, 100, true))
	if obs := m.observe(psiSample(30, 10, 100, true)); obs.Active {
		t.Fatal("recovery must not be blocked by PSI when the condition is disabled")
	}
}

// ===== daemon gate integration =====

// writeWatermarkFixtures materializes a host state into the sampler's procfs
// fixtures: meminfo with the given MemAvailable ratio and a swaps file with
// the given Used ratio against a 1,000,000 kB total.
func writeWatermarkFixtures(t *testing.T, dir string, memAvailPct, swapUsedPct float64) {
	t.Helper()
	meminfo := fmt.Sprintf("MemTotal:      1000000 kB\nMemAvailable:  %d kB\n", int(memAvailPct*10000))
	if err := os.WriteFile(filepath.Join(dir, "meminfo"), []byte(meminfo), 0o600); err != nil {
		t.Fatalf("write meminfo fixture: %v", err)
	}
	swaps := "Filename\tType\tSize\tUsed\tPriority\n"
	if swapUsedPct > 0 {
		swaps += fmt.Sprintf("/dev/sda2\tpartition\t1000000\t%d\t-2\n", int(swapUsedPct*10000))
	}
	if err := os.WriteFile(filepath.Join(dir, "swaps"), []byte(swaps), 0o600); err != nil {
		t.Fatalf("write swaps fixture: %v", err)
	}
	writePressureFixture(t, dir, 1.50)
}

// writePressureFixture rewrites just the /proc/pressure/memory fixture so
// gate tests can move the PSI reading between ticks.
func writePressureFixture(t *testing.T, dir string, avg10 float64) {
	t.Helper()
	pressure := fmt.Sprintf("some avg10=%.2f avg60=1.00 avg300=0.50 total=1\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=0\n", avg10)
	if err := os.WriteFile(filepath.Join(dir, "pressure"), []byte(pressure), 0o600); err != nil {
		t.Fatalf("write pressure fixture: %v", err)
	}
}

// setBackpressureRuntime installs a gate config directly — the boot-defaults
// shape. The heartbeat path goes through applyBackpressureConfig instead.
func setBackpressureRuntime(d *Daemon, th backpressureThresholds, windowSize int) {
	d.bpRuntime.Store(&backpressureRuntime{
		enabled:  true,
		interval: DefaultBackpressureSampleInterval,
		machine:  newBackpressureMachine(th, windowSize),
	})
}

func newBackpressureTestDaemon(t *testing.T) (*Daemon, string) {
	t.Helper()
	dir := t.TempDir()
	writeWatermarkFixtures(t, dir, 40, 10)
	d := &Daemon{logger: slog.Default()}
	d.bpSource = memSampleSource{
		MeminfoPath:  filepath.Join(dir, "meminfo"),
		SwapsPath:    filepath.Join(dir, "swaps"),
		PressurePath: filepath.Join(dir, "pressure"),
	}
	setBackpressureRuntime(d, bpTestThresholds(), 1)
	return d, dir
}

func TestBackpressureGate_BlocksAndRecovers(t *testing.T) {
	d, _ := newBackpressureTestDaemon(t)

	// No state yet (watcher hasn't run): fail open.
	if d.backpressureBlockedForWorkspace("") {
		t.Fatal("nil gate state must fail open")
	}
	if d.backpressureReportForRuntime("rt-1") != nil {
		t.Fatal("nil gate state must omit the report")
	}

	d.backpressureTick()
	if d.backpressureBlockedForWorkspace("") {
		t.Fatal("healthy sample must not block")
	}
	if rep := d.backpressureReportForRuntime("rt-1"); rep == nil || rep.MemAvailablePct != 40 || rep.SwapUsedPct != 10 || !rep.PSIReadOK {
		t.Fatalf("healthy report mismatch: %+v", d.backpressureReportForRuntime("rt-1"))
	}

	// Enter backpressure on the mem condition.
	writeWatermarkFixtures(t, fixtureDir(d), 10, 10)
	d.backpressureTick()
	if !d.backpressureBlockedForWorkspace("") {
		t.Fatal("mem-low sample must block claims")
	}
	if rep := d.backpressureReportForRuntime("rt-1"); rep.Reason != "mem" {
		t.Fatalf("reason = %q, want mem", rep.Reason)
	}
	if d.backpressureReportForRuntime("rt-1").DeferredClaims != 0 {
		t.Fatal("fresh episode must report zero deferred claims")
	}

	// The poller counts deferred cycles while blocked.
	d.bpDeferred.Add(3)
	if got := d.backpressureReportForRuntime("rt-1").DeferredClaims; got != 3 {
		t.Fatalf("DeferredClaims = %d, want 3", got)
	}

	// Recovery resets the episode counter (done by the watcher's clear path)
	// and unblocks the gate.
	writeWatermarkFixtures(t, fixtureDir(d), 40, 10)
	d.backpressureTick()
	if d.backpressureBlockedForWorkspace("") {
		t.Fatal("recovered sample must unblock claims")
	}
	if d.bpDeferred.Load() != 0 {
		t.Fatalf("deferred counter must reset on clear, got %d", d.bpDeferred.Load())
	}
}

func TestBackpressureGate_SamplerFailureHoldsState(t *testing.T) {
	d, _ := newBackpressureTestDaemon(t)
	d.backpressureTick() // healthy

	// Break the primary signal; the gate must hold its previous (open) state.
	if err := os.Remove(filepath.Join(fixtureDir(d), "meminfo")); err != nil {
		t.Fatalf("remove meminfo: %v", err)
	}
	d.backpressureTick()
	if d.backpressureBlockedForWorkspace("") {
		t.Fatal("a failed sample must hold the previous gate state, not trip the gate")
	}

	// Same fail-safe from the closed side: remove the fixture while active.
	writeWatermarkFixtures(t, fixtureDir(d), 5, 10)
	d.backpressureTick()
	if !d.backpressureBlockedForWorkspace("") {
		t.Fatal("precondition: gate active on mem-low")
	}
	d.backpressureTick()
	if !d.backpressureBlockedForWorkspace("") {
		t.Fatal("a failed sample must hold the previous active state, not clear the gate")
	}
}

func TestBackpressureGate_WakeupNudgeOnRecovery(t *testing.T) {
	d, _ := newBackpressureTestDaemon(t)
	wakeup := make(chan struct{}, 1)
	d.bpWakeup.Store(&wakeup)

	writeWatermarkFixtures(t, fixtureDir(d), 5, 10)
	d.backpressureTick()
	if !d.backpressureBlockedForWorkspace("") {
		t.Fatal("precondition: gate active")
	}

	// Recovery must nudge the poller wakeup so the next claim cycle starts
	// immediately instead of after one backoff interval.
	writeWatermarkFixtures(t, fixtureDir(d), 40, 10)
	d.backpressureTick()
	select {
	case <-wakeup:
	default:
		t.Fatal("recovery must signal the poller wakeup channel")
	}
}

func TestBackpressureGate_CoexistsWithAutoUpdateBarrier(t *testing.T) {
	d, _ := newBackpressureTestDaemon(t)
	// rt-1 must resolve to a workspace for the claim barrier to consult that
	// workspace's (here: boot-fallback) gate; registration binds runtimes
	// before any claim runs.
	seedWorkspaceBindings(t, d, map[string][]string{"ws-1": {"rt-1"}})

	// Both mechanisms closed → refuse; clearing one must not open the gate
	// for the other.
	d.pauseClaims = true
	writeWatermarkFixtures(t, fixtureDir(d), 5, 10)
	d.backpressureTick()
	if d.tryEnterClaimFor([]string{"rt-1"}) {
		t.Fatal("claim must refuse while both the auto-update barrier and backpressure are set")
	}

	// Clearing the auto-update barrier alone keeps backpressure in effect.
	d.releaseClaimBarrier()
	if d.tryEnterClaimFor([]string{"rt-1"}) {
		t.Fatal("claim must refuse while backpressure alone is set")
	}

	// Recovering from backpressure reopens claiming under a fresh
	// auto-update barrier cycle.
	writeWatermarkFixtures(t, fixtureDir(d), 40, 10)
	d.backpressureTick()
	if !d.tryEnterClaimFor([]string{"rt-1"}) {
		t.Fatal("claim must succeed once backpressure clears")
	}
	d.exitClaim()

	// And the reverse order: backpressure clear + auto-update barrier set
	// still refuses (existing behavior, asserted so the gate composition is
	// pinned from both sides).
	d.pauseClaims = true
	if d.tryEnterClaimFor([]string{"rt-1"}) {
		t.Fatal("claim must refuse while the auto-update barrier is set")
	}
	d.releaseClaimBarrier()
}

// fixtureDir recovers the temp dir a test daemon's fixtures live in (they
// share it with meminfo at the top level).
func fixtureDir(d *Daemon) string {
	return filepath.Dir(d.bpSource.MeminfoPath)
}

// ===== config =====

func TestBackpressureGate_PSITripAndRecover(t *testing.T) {
	d, dir := newBackpressureTestDaemon(t)
	setBackpressureRuntime(d, bpPSITestThresholds(), 1)

	d.backpressureTick()
	if d.backpressureBlockedForWorkspace("") {
		t.Fatal("healthy watermarks and PSI must not block claims")
	}

	// A PSI stall past the high watermark trips the gate even with both
	// watermarks healthy — PSI is a full member of the trigger OR.
	writePressureFixture(t, dir, 80)
	d.backpressureTick()
	if !d.backpressureBlockedForWorkspace("") {
		t.Fatal("PSI stall must block claims")
	}

	// Falling back under the recovery watermark clears it.
	writePressureFixture(t, dir, 5)
	d.backpressureTick()
	if d.backpressureBlockedForWorkspace("") {
		t.Fatal("PSI recovery must unblock claims")
	}

	// A failed PSI read degrades: the last gate state holds and mem/swap
	// keep gating — pressure value is meaningless, so no new trip.
	writePressureFixture(t, dir, 99)
	os.Remove(filepath.Join(dir, "pressure"))
	d.backpressureTick()
	if d.backpressureBlockedForWorkspace("") {
		t.Fatal("failed PSI read must not trip the gate")
	}
}

func TestValidateBackpressureThresholds(t *testing.T) {
	valid := backpressureThresholds{MemHighPct: 15, MemRecoveryPct: 25, SwapHighPct: 80, SwapRecoveryPct: 60}
	if err := validateBackpressureThresholds(valid, 5*time.Second, 6); err != nil {
		t.Fatalf("default thresholds must validate: %v", err)
	}
	if err := validateBackpressureThresholds(valid, 5*time.Second, 6); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}

	cases := []struct {
		name string
		th   backpressureThresholds
	}{
		{"mem recovery not above high", backpressureThresholds{MemHighPct: 25, MemRecoveryPct: 25}},
		{"mem recovery below high", backpressureThresholds{MemHighPct: 25, MemRecoveryPct: 15}},
		{"mem high out of range", backpressureThresholds{MemHighPct: 101, MemRecoveryPct: 101}},
		{"mem high disabled", backpressureThresholds{MemHighPct: 0, MemRecoveryPct: 25}},
		{"swap high out of range", backpressureThresholds{MemHighPct: 15, MemRecoveryPct: 25, SwapHighPct: 120, SwapRecoveryPct: 60}},
		{"swap recovery not below high", backpressureThresholds{MemHighPct: 15, MemRecoveryPct: 25, SwapHighPct: 60, SwapRecoveryPct: 60}},
		{"psi recovery not below high", backpressureThresholds{MemHighPct: 15, MemRecoveryPct: 25, PSIHighPct: 50, PSIRecoveryPct: 50}},
		{"psi recovery above high", backpressureThresholds{MemHighPct: 15, MemRecoveryPct: 25, PSIHighPct: 50, PSIRecoveryPct: 60}},
		{"psi high out of range", backpressureThresholds{MemHighPct: 15, MemRecoveryPct: 25, PSIHighPct: 101, PSIRecoveryPct: 20}},
		{"psi recovery negative", backpressureThresholds{MemHighPct: 15, MemRecoveryPct: 25, PSIHighPct: 50, PSIRecoveryPct: -1}},
	}
	for _, tc := range cases {
		if err := validateBackpressureThresholds(tc.th, 5*time.Second, 6); err == nil {
			t.Fatalf("%s: must reject", tc.name)
		}
	}

	// Rejection errors name the offending field so a misconfigured env var
	// is diagnosable from the startup failure alone.
	err := validateBackpressureThresholds(backpressureThresholds{MemHighPct: 15, MemRecoveryPct: 25, PSIHighPct: 50, PSIRecoveryPct: 60}, 5*time.Second, 6)
	if err == nil || !strings.Contains(err.Error(), "psi recovery watermark") {
		t.Fatalf("psi recovery error must name the field, got %v", err)
	}
	err = validateBackpressureThresholds(backpressureThresholds{MemHighPct: 15, MemRecoveryPct: 25, PSIHighPct: 101, PSIRecoveryPct: 20}, 5*time.Second, 6)
	if err == nil || !strings.Contains(err.Error(), "psi high watermark") {
		t.Fatalf("psi high error must name the field, got %v", err)
	}

	// Swap condition disabled (high <= 0) skips the swap band checks.
	if err := validateBackpressureThresholds(backpressureThresholds{MemHighPct: 15, MemRecoveryPct: 25}, 5*time.Second, 6); err != nil {
		t.Fatalf("disabled swap condition must validate: %v", err)
	}
	// Same for PSI, and a well-formed PSI band validates alongside them.
	if err := validateBackpressureThresholds(backpressureThresholds{MemHighPct: 15, MemRecoveryPct: 25, PSIHighPct: 0, PSIRecoveryPct: 0}, 5*time.Second, 6); err != nil {
		t.Fatalf("disabled psi condition must validate: %v", err)
	}
	if err := validateBackpressureThresholds(backpressureThresholds{MemHighPct: 15, MemRecoveryPct: 25, SwapHighPct: 80, SwapRecoveryPct: 60, PSIHighPct: 50, PSIRecoveryPct: 20}, 5*time.Second, 6); err != nil {
		t.Fatalf("valid psi band rejected: %v", err)
	}
	if err := validateBackpressureThresholds(valid, 0, 6); err == nil {
		t.Fatal("zero sample interval must reject")
	}
	if err := validateBackpressureThresholds(valid, 5*time.Second, 0); err == nil {
		t.Fatal("zero window size must reject")
	}
}

func TestLoadConfig_BackpressureDefaultsEnvPathRetired(t *testing.T) {
	stageFakeAgent(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SHELL", filepath.Join(t.TempDir(), "missing-shell"))

	overrides := Overrides{
		ServerURL:      "http://localhost:0",
		WorkspacesRoot: t.TempDir(),
	}
	cfg, err := LoadConfig(overrides)
	if err != nil {
		t.Fatalf("LoadConfig defaults: %v", err)
	}
	if !cfg.BackpressureEnabled {
		t.Fatal("backpressure must default to enabled")
	}
	if cfg.BackpressureMemHighPct != 15 || cfg.BackpressureMemRecoveryPct != 25 ||
		cfg.BackpressureSwapHighPct != 80 || cfg.BackpressureSwapRecoveryPct != 60 {
		t.Fatalf("default thresholds mismatch: %+v", cfg)
	}
	if cfg.BackpressurePSIHighPct != 50 || cfg.BackpressurePSIRecoveryPct != 20 {
		t.Fatalf("default PSI thresholds mismatch: %+v", cfg)
	}
	if cfg.BackpressureSampleInterval != 5*time.Second || cfg.BackpressureWindowSize != 6 {
		t.Fatalf("default sampler mismatch: interval=%s window=%d", cfg.BackpressureSampleInterval, cfg.BackpressureWindowSize)
	}

	// The MULTICA_DAEMON_BACKPRESSURE_* env path is retired (RUYI-618): the
	// workspace settings delivered on heartbeat acks are the single runtime
	// authority, so even garbage env values must be ignored silently instead
	// of failing the boot or bending the defaults — that is what keeps a
	// stale systemd drop-in from silently fighting the workspace config.
	t.Setenv("MULTICA_DAEMON_BACKPRESSURE", "false")
	t.Setenv("MULTICA_DAEMON_BACKPRESSURE_MEM_HIGH_PCT", "not-a-number")
	t.Setenv("MULTICA_DAEMON_BACKPRESSURE_MEM_RECOVERY_PCT", "1")
	t.Setenv("MULTICA_DAEMON_BACKPRESSURE_SWAP_HIGH_PCT", "999")
	t.Setenv("MULTICA_DAEMON_BACKPRESSURE_SAMPLE_INTERVAL", "-5s")
	t.Setenv("MULTICA_DAEMON_BACKPRESSURE_WINDOW", "0")
	cfg, err = LoadConfig(overrides)
	if err != nil {
		t.Fatalf("retired env path must not fail config load: %v", err)
	}
	if !cfg.BackpressureEnabled || cfg.BackpressureMemHighPct != 15 ||
		cfg.BackpressureMemRecoveryPct != 25 || cfg.BackpressureSampleInterval != 5*time.Second ||
		cfg.BackpressureWindowSize != 6 {
		t.Fatalf("retired env path must not change defaults: %+v", cfg)
	}
}

// ===== workspace settings hot-apply (RUYI-618) =====

// seedWorkspaceBindings wires runtime→workspace mappings so claim filtering,
// report attribution, and boot-fallback scoping have a runtime set to work
// with — the shape registration produces before the first heartbeat ack.
func seedWorkspaceBindings(t *testing.T, d *Daemon, wsRuntimes map[string][]string) {
	t.Helper()
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.workspaces == nil {
		d.workspaces = make(map[string]*workspaceState, len(wsRuntimes))
	}
	if d.runtimeIndex == nil {
		d.runtimeIndex = make(map[string]Runtime)
	}
	for wsID, rids := range wsRuntimes {
		d.workspaces[wsID] = &workspaceState{workspaceID: wsID, runtimeIDs: append([]string(nil), rids...)}
		for _, rid := range rids {
			d.runtimeIndex[rid] = Runtime{ID: rid}
		}
	}
}

func TestApplyBackpressureConfig_HotSwapChangesGate(t *testing.T) {
	d, dir := newBackpressureTestDaemon(t) // 40% available: healthy against 15/25
	d.backpressureTick()
	if d.backpressureBlockedForWorkspace("") {
		t.Fatal("precondition: fixture host must start healthy on the boot card")
	}

	// The workspace tightens the watermarks past the current sample. The new
	// machine takes effect on the next tick — no restart, no env reload.
	tight := &protocol.DaemonBackpressureConfig{
		Enabled:               true,
		MemHighPct:            45,
		MemRecoveryPct:        55,
		SampleIntervalSeconds: 2,
		WindowSize:            3,
	}
	d.applyBackpressureConfig("ws-1", "rt-1", tight)
	entry := d.backpressureEntryFor("ws-1")
	if entry == nil || !entry.runtime.enabled || entry.runtime.interval != 2*time.Second ||
		entry.runtime.machine.windowSize != 3 || entry.runtime.machine.thresholds.MemHighPct != 45 {
		t.Fatalf("applied workspace card mismatch: %+v", entry)
	}
	d.backpressureTick()
	if !d.backpressureBlockedForWorkspace("ws-1") {
		t.Fatal("tightened workspace watermark must trip the workspace gate on the next sample")
	}
	// The boot card (15/25) still evaluates for workspaces that have not
	// delivered their own — and stays open at 40%.
	if d.backpressureBlockedForWorkspace("") {
		t.Fatal("workspace card must not leak into the boot-default gate")
	}

	// Relaxing the card clears the pause the same way.
	loose := &protocol.DaemonBackpressureConfig{
		Enabled:               true,
		MemHighPct:            15,
		MemRecoveryPct:        25,
		SampleIntervalSeconds: 2,
		WindowSize:            3,
	}
	writeWatermarkFixtures(t, dir, 60, 10)
	d.applyBackpressureConfig("ws-1", "rt-1", loose)
	d.backpressureTick()
	if d.backpressureBlockedForWorkspace("ws-1") {
		t.Fatal("relaxed workspace watermark must release the gate on the next sample")
	}
}

func TestApplyBackpressureConfig_InvalidConfigIgnored(t *testing.T) {
	d, _ := newBackpressureTestDaemon(t)
	invalid := &protocol.DaemonBackpressureConfig{
		Enabled: true, MemHighPct: 80, MemRecoveryPct: 20,
		SampleIntervalSeconds: 5, WindowSize: 6,
	}

	// Inverted hysteresis — exactly what the server refuses with 422, but a
	// buggy or malicious server must not be able to corrupt a running gate
	// either: no entry may appear for the workspace.
	d.applyBackpressureConfig("ws-1", "rt-1", invalid)
	if d.backpressureEntryFor("ws-1") != nil {
		t.Fatal("invalid config must not install a workspace gate")
	}

	// And an already-applied valid card survives an invalid redelivery.
	valid := protocol.DefaultDaemonBackpressureConfig()
	d.applyBackpressureConfig("ws-1", "rt-1", valid)
	first := d.backpressureEntryFor("ws-1")
	if first == nil {
		t.Fatal("precondition: valid card must apply")
	}
	d.applyBackpressureConfig("ws-1", "rt-1", invalid)
	if d.backpressureEntryFor("ws-1") != first {
		t.Fatal("invalid config must not replace the workspace's active card")
	}
}

func TestApplyBackpressureConfig_DisabledReleasesClaims(t *testing.T) {
	d, _ := newBackpressureTestDaemon(t)
	seedWorkspaceBindings(t, d, map[string][]string{"ws-1": {"rt-1"}})

	// Latch the workspace gate: the card trips at the 40% fixture, the entry
	// shadows the boot card for this workspace.
	d.applyBackpressureConfig("ws-1", "rt-1", &protocol.DaemonBackpressureConfig{
		Enabled: true, MemHighPct: 45, MemRecoveryPct: 55,
		SampleIntervalSeconds: 5, WindowSize: 1,
	})
	d.backpressureTick()
	if !d.backpressureBlockedForWorkspace("ws-1") {
		t.Fatal("precondition: workspace gate must latch")
	}
	wakeup := make(chan struct{}, 1)
	d.bpWakeup.Store(&wakeup)

	// Disabled card: still a structurally valid config (validation runs even
	// for disabled), and it must release the latched pause immediately.
	d.applyBackpressureConfig("ws-1", "rt-1", &protocol.DaemonBackpressureConfig{
		Enabled: false, MemHighPct: 15, MemRecoveryPct: 25,
		SampleIntervalSeconds: 5, WindowSize: 6,
	})
	if d.backpressureBlockedForWorkspace("ws-1") {
		t.Fatal("disabling the gate must release a latched pause immediately")
	}
	select {
	case <-wakeup:
	default:
		t.Fatal("disabled gate must wake the batch poller")
	}
	// The report key disappears entirely while disabled: upstream consumers
	// see an unpressured runtime, not a stale one.
	if d.backpressureReportForRuntime("rt-1") != nil {
		t.Fatal("disabled gate must stop reporting backpressure state")
	}
	d.backpressureTick()
	if d.backpressureBlockedForWorkspace("ws-1") {
		t.Fatal("disabled card must stay released on later ticks")
	}
}

func TestApplyBackpressureConfig_IdenticalDeliveryIsNoOp(t *testing.T) {
	d, _ := newBackpressureTestDaemon(t)
	// The server sends the card on every heartbeat; a repeat must not churn
	// the log or rebuild the machine.
	cfg := protocol.DefaultDaemonBackpressureConfig()
	d.applyBackpressureConfig("ws-1", "rt-1", cfg)
	first := d.backpressureEntryFor("ws-1")
	if first == nil {
		t.Fatal("precondition: card must apply")
	}
	d.applyBackpressureConfig("ws-1", "rt-1", cfg)
	if d.backpressureEntryFor("ws-1") != first {
		t.Fatal("identical repeat delivery must keep the same workspace gate")
	}
}

// TestApplyBackpressureConfig_WorkspaceCardsIndependent is the F1 regression:
// one daemon, two workspaces, two different cards. Per-beat redelivery from
// one workspace must never overwrite the other's parameters or gate state —
// the pre-F1 global singleton swapped the whole gate on every ack, so two
// workspaces fought each heartbeat.
func TestApplyBackpressureConfig_WorkspaceCardsIndependent(t *testing.T) {
	d, dir := newBackpressureTestDaemon(t) // 40% available
	seedWorkspaceBindings(t, d, map[string][]string{"ws-1": {"rt-1"}, "ws-2": {"rt-2"}})

	tight := &protocol.DaemonBackpressureConfig{
		Enabled: true, MemHighPct: 45, MemRecoveryPct: 55,
		SampleIntervalSeconds: 2, WindowSize: 3,
	}
	loose := &protocol.DaemonBackpressureConfig{
		Enabled: true, MemHighPct: 10, MemRecoveryPct: 20,
		SampleIntervalSeconds: 5, WindowSize: 6,
	}
	d.applyBackpressureConfig("ws-1", "rt-1", tight)
	d.applyBackpressureConfig("ws-2", "rt-2", loose)
	d.backpressureTick()

	if !d.backpressureBlockedForWorkspace("ws-1") {
		t.Fatal("ws-1's tight card must trip at 40% available")
	}
	if d.backpressureBlockedForWorkspace("ws-2") {
		t.Fatal("ws-2's loose card must stay open at 40% available")
	}

	// ws-2's heartbeat keeps delivering its card on every beat; none of those
	// applies may touch ws-1's gate.
	ws1Entry := d.backpressureEntryFor("ws-1")
	for i := 0; i < 3; i++ {
		d.applyBackpressureConfig("ws-2", "rt-2", loose)
		d.backpressureTick()
	}
	if d.backpressureEntryFor("ws-1") != ws1Entry {
		t.Fatal("another workspace's repeated delivery must not rebuild ws-1's gate")
	}
	if !d.backpressureBlockedForWorkspace("ws-1") || d.backpressureBlockedForWorkspace("ws-2") {
		t.Fatal("per-workspace verdicts must survive the neighbor's beats")
	}

	// Reports stay attributed: each runtime ships its own workspace's state.
	rep1 := d.backpressureReportForRuntime("rt-1")
	if rep1 == nil || !rep1.Active || rep1.Reason != "mem" {
		t.Fatalf("ws-1 runtime report mismatch: %+v", rep1)
	}
	rep2 := d.backpressureReportForRuntime("rt-2")
	if rep2 == nil || rep2.Active {
		t.Fatalf("ws-2 runtime report must be inactive, got %+v", rep2)
	}

	// Recovery is per workspace too: relaxing ws-1's card releases only ws-1.
	writeWatermarkFixtures(t, dir, 60, 10)
	d.applyBackpressureConfig("ws-1", "rt-1", loose)
	d.backpressureTick()
	if d.backpressureBlockedForWorkspace("ws-1") {
		t.Fatal("ws-1 must recover on its own card")
	}
}

// TestApplyBackpressureConfig_DisabledCardShadowsBootGate pins the ownership
// rule: until a workspace's first card lands, the boot-default card gates it
// (pre-ack window); once a disabled card lands, the owner's choice wins even
// on a starving host, and the boot gate stops evaluating once every bound
// workspace carries a card.
func TestApplyBackpressureConfig_DisabledCardShadowsBootGate(t *testing.T) {
	d, dir := newBackpressureTestDaemon(t)
	seedWorkspaceBindings(t, d, map[string][]string{"ws-1": {"rt-1"}})

	// Starve the host BEFORE any card arrives: the boot gate (15/25) latches
	// and gates the not-yet-acked workspace through the boot fallback.
	writeWatermarkFixtures(t, dir, 5, 10)
	d.backpressureTick()
	if !d.backpressureBlockedForWorkspace("ws-1") {
		t.Fatal("pre-ack workspace must be gated by the boot-default card")
	}

	// The owner disarms the gate: the workspace's own card shadows the boot
	// fallback immediately.
	d.applyBackpressureConfig("ws-1", "rt-1", &protocol.DaemonBackpressureConfig{
		Enabled: false, MemHighPct: 15, MemRecoveryPct: 25,
		SampleIntervalSeconds: 5, WindowSize: 6,
	})
	if d.backpressureBlockedForWorkspace("ws-1") {
		t.Fatal("a workspace's disabled card must shadow the boot gate")
	}

	// With every bound workspace carrying a card, the boot slot stops
	// evaluating and drops its latch — it has no reader left.
	d.backpressureTick()
	if d.backpressureBlockedForWorkspace("") {
		t.Fatal("boot gate must release once every workspace carries its own card")
	}
}

// TestBackpressureClaimFilterAndAttribution pins the claim-path shape of F1:
// held workspaces drop out of the batch, mixed batches carry no
// machine-level report, single-workspace batches keep it, and the claim
// barrier refuses while any workspace in the batch is held.
func TestBackpressureClaimFilterAndAttribution(t *testing.T) {
	d, _ := newBackpressureTestDaemon(t) // 40% available
	seedWorkspaceBindings(t, d, map[string][]string{
		"ws-1": {"rt-1a", "rt-1b"},
		"ws-2": {"rt-2"},
	})

	tight := &protocol.DaemonBackpressureConfig{
		Enabled: true, MemHighPct: 45, MemRecoveryPct: 55,
		SampleIntervalSeconds: 5, WindowSize: 1,
	}
	d.applyBackpressureConfig("ws-1", "rt-1a", tight)
	d.backpressureTick()
	if !d.backpressureBlockedForWorkspace("ws-1") {
		t.Fatal("precondition: ws-1 must be held")
	}

	// Held workspaces' runtimes drop out; other workspaces keep claiming.
	claimable := d.filterBackpressureClaimable([]string{"rt-1a", "rt-1b", "rt-2"})
	if len(claimable) != 1 || claimable[0] != "rt-2" {
		t.Fatalf("filter must drop ws-1's runtimes only, got %v", claimable)
	}

	// A mixed batch carries no machine-level report: one statement cannot
	// describe workspaces that gate differently.
	if rep := d.backpressureReportForClaim([]string{"rt-1a", "rt-2"}); rep != nil {
		t.Fatalf("mixed batch must omit the claim report, got %+v", rep)
	}
	// A single-workspace batch keeps the machine-level shape (pre-F1 parity).
	rep := d.backpressureReportForClaim([]string{"rt-1a", "rt-1b"})
	if rep == nil || !rep.Active {
		t.Fatalf("single-workspace batch must carry that workspace's report, got %+v", rep)
	}

	// The claim barrier refuses while any batch workspace is held, and
	// allows once the batch is clean.
	if d.tryEnterClaimFor([]string{"rt-1a", "rt-2"}) {
		d.exitClaim()
		t.Fatal("claim must refuse while ws-1 is held")
	}
	if !d.tryEnterClaimFor([]string{"rt-2"}) {
		t.Fatal("claim must proceed for the clean workspace")
	}
	d.exitClaim()

	// Both held → nothing claimable.
	d.applyBackpressureConfig("ws-2", "rt-2", tight)
	d.backpressureTick()
	if claimable := d.filterBackpressureClaimable([]string{"rt-1a", "rt-2"}); len(claimable) != 0 {
		t.Fatalf("all-held batch must filter to empty, got %v", claimable)
	}
}

// TestBackpressureSampleIntervalTakesMinimum pins the watcher cadence: the
// fastest enabled card wins so a 1s workspace is not slowed by a 60s
// neighbor; disabled cards and the boot slot only count when enabled.
func TestBackpressureSampleIntervalTakesMinimum(t *testing.T) {
	d, _ := newBackpressureTestDaemon(t) // boot card at the 5s default
	if got := d.backpressureSampleInterval(); got != DefaultBackpressureSampleInterval {
		t.Fatalf("boot-only cadence = %s, want default", got)
	}

	// Cards only tighten (or fail to beat) the boot cadence while it is
	// enabled; drop the boot card to isolate the per-workspace minimum.
	d.bpRuntime.Store(nil)
	d.applyBackpressureConfig("ws-1", "rt-1", &protocol.DaemonBackpressureConfig{
		Enabled: true, MemHighPct: 15, MemRecoveryPct: 25,
		SampleIntervalSeconds: 60, WindowSize: 2,
	})
	d.applyBackpressureConfig("ws-2", "rt-2", &protocol.DaemonBackpressureConfig{
		Enabled: true, MemHighPct: 15, MemRecoveryPct: 25,
		SampleIntervalSeconds: 10, WindowSize: 2,
	})
	if got := d.backpressureSampleInterval(); got != 10*time.Second {
		t.Fatalf("cadence = %s, want the fastest enabled card (10s)", got)
	}

	// Disabled cards never set the pace.
	d.applyBackpressureConfig("ws-3", "rt-3", &protocol.DaemonBackpressureConfig{
		Enabled: false, MemHighPct: 15, MemRecoveryPct: 25,
		SampleIntervalSeconds: 1, WindowSize: 1,
	})
	if got := d.backpressureSampleInterval(); got != 10*time.Second {
		t.Fatalf("disabled card must not set the cadence, got %s", got)
	}
}

// TestDropBackpressureWorkspace pins the unbind cleanup: the applied card
// must not outlive its workspace.
func TestDropBackpressureWorkspace(t *testing.T) {
	d, _ := newBackpressureTestDaemon(t)
	d.applyBackpressureConfig("ws-1", "rt-1", protocol.DefaultDaemonBackpressureConfig())
	if d.backpressureEntryFor("ws-1") == nil {
		t.Fatal("precondition: card must apply")
	}

	d.dropBackpressureWorkspace("ws-1")
	if d.backpressureEntryFor("ws-1") != nil {
		t.Fatal("unbound workspace's card must be dropped")
	}
	// Dropping again, or an unknown workspace, is a safe no-op.
	d.dropBackpressureWorkspace("ws-1")
	d.dropBackpressureWorkspace("ws-unknown")
}
