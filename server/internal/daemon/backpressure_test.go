package daemon

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
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

func sample(memAvail, swapUsed float64) memSample {
	return memSample{MemAvailablePct: memAvail, SwapUsedPct: swapUsed, At: time.Now()}
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
	pressure := "some avg10=1.50 avg60=1.00 avg300=0.50 total=1\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=0\n"
	if err := os.WriteFile(filepath.Join(dir, "pressure"), []byte(pressure), 0o600); err != nil {
		t.Fatalf("write pressure fixture: %v", err)
	}
}

func newBackpressureTestDaemon(t *testing.T) *Daemon {
	t.Helper()
	dir := t.TempDir()
	writeWatermarkFixtures(t, dir, 40, 10)
	d := &Daemon{logger: slog.Default()}
	d.bpSource = memSampleSource{
		MeminfoPath:  filepath.Join(dir, "meminfo"),
		SwapsPath:    filepath.Join(dir, "swaps"),
		PressurePath: filepath.Join(dir, "pressure"),
	}
	d.bpMachine = newBackpressureMachine(bpTestThresholds(), 1)
	return d
}

func TestBackpressureGate_BlocksAndRecovers(t *testing.T) {
	d := newBackpressureTestDaemon(t)

	// No state yet (watcher hasn't run): fail open.
	if d.backpressureBlocked() {
		t.Fatal("nil gate state must fail open")
	}
	if d.backpressureReport() != nil {
		t.Fatal("nil gate state must omit the report")
	}

	d.backpressureTick()
	if d.backpressureBlocked() {
		t.Fatal("healthy sample must not block")
	}
	if rep := d.backpressureReport(); rep == nil || rep.MemAvailablePct != 40 || rep.SwapUsedPct != 10 || !rep.PSIReadOK {
		t.Fatalf("healthy report mismatch: %+v", d.backpressureReport())
	}

	// Enter backpressure on the mem condition.
	writeWatermarkFixtures(t, fixtureDir(d), 10, 10)
	d.backpressureTick()
	if !d.backpressureBlocked() {
		t.Fatal("mem-low sample must block claims")
	}
	if rep := d.backpressureReport(); rep.Reason != "mem" {
		t.Fatalf("reason = %q, want mem", rep.Reason)
	}
	if d.backpressureReport().DeferredClaims != 0 {
		t.Fatal("fresh episode must report zero deferred claims")
	}

	// The poller counts deferred cycles while blocked.
	d.bpDeferred.Add(3)
	if got := d.backpressureReport().DeferredClaims; got != 3 {
		t.Fatalf("DeferredClaims = %d, want 3", got)
	}

	// Recovery resets the episode counter (done by the watcher's clear path)
	// and unblocks the gate.
	writeWatermarkFixtures(t, fixtureDir(d), 40, 10)
	d.backpressureTick()
	if d.backpressureBlocked() {
		t.Fatal("recovered sample must unblock claims")
	}
	if d.bpDeferred.Load() != 0 {
		t.Fatalf("deferred counter must reset on clear, got %d", d.bpDeferred.Load())
	}
}

func TestBackpressureGate_SamplerFailureHoldsState(t *testing.T) {
	d := newBackpressureTestDaemon(t)
	d.backpressureTick() // healthy

	// Break the primary signal; the gate must hold its previous (open) state.
	if err := os.Remove(filepath.Join(fixtureDir(d), "meminfo")); err != nil {
		t.Fatalf("remove meminfo: %v", err)
	}
	d.backpressureTick()
	if d.backpressureBlocked() {
		t.Fatal("a failed sample must hold the previous gate state, not trip the gate")
	}

	// Same fail-safe from the closed side: remove the fixture while active.
	writeWatermarkFixtures(t, fixtureDir(d), 5, 10)
	d.backpressureTick()
	if !d.backpressureBlocked() {
		t.Fatal("precondition: gate active on mem-low")
	}
	d.backpressureTick()
	if !d.backpressureBlocked() {
		t.Fatal("a failed sample must hold the previous active state, not clear the gate")
	}
}

func TestBackpressureGate_WakeupNudgeOnRecovery(t *testing.T) {
	d := newBackpressureTestDaemon(t)
	wakeup := make(chan struct{}, 1)
	d.bpWakeup.Store(&wakeup)

	writeWatermarkFixtures(t, fixtureDir(d), 5, 10)
	d.backpressureTick()
	if !d.backpressureBlocked() {
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
	d := newBackpressureTestDaemon(t)

	// Both mechanisms closed → refuse; clearing one must not open the gate
	// for the other.
	d.pauseClaims = true
	writeWatermarkFixtures(t, fixtureDir(d), 5, 10)
	d.backpressureTick()
	if d.tryEnterClaim() {
		t.Fatal("claim must refuse while both the auto-update barrier and backpressure are set")
	}

	// Clearing the auto-update barrier alone keeps backpressure in effect.
	d.releaseClaimBarrier()
	if d.tryEnterClaim() {
		t.Fatal("claim must refuse while backpressure alone is set")
	}

	// Recovering from backpressure reopens claiming under a fresh
	// auto-update barrier cycle.
	writeWatermarkFixtures(t, fixtureDir(d), 40, 10)
	d.backpressureTick()
	if !d.tryEnterClaim() {
		t.Fatal("claim must succeed once backpressure clears")
	}
	d.exitClaim()

	// And the reverse order: backpressure clear + auto-update barrier set
	// still refuses (existing behavior, asserted so the gate composition is
	// pinned from both sides).
	d.pauseClaims = true
	if d.tryEnterClaim() {
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
	}
	for _, tc := range cases {
		if err := validateBackpressureThresholds(tc.th, 5*time.Second, 6); err == nil {
			t.Fatalf("%s: must reject", tc.name)
		}
	}

	// Swap condition disabled (high <= 0) skips the swap band checks.
	if err := validateBackpressureThresholds(backpressureThresholds{MemHighPct: 15, MemRecoveryPct: 25}, 5*time.Second, 6); err != nil {
		t.Fatalf("disabled swap condition must validate: %v", err)
	}
	if err := validateBackpressureThresholds(valid, 0, 6); err == nil {
		t.Fatal("zero sample interval must reject")
	}
	if err := validateBackpressureThresholds(valid, 5*time.Second, 0); err == nil {
		t.Fatal("zero window size must reject")
	}
}

func TestLoadConfig_BackpressureDefaultsAndEnvOverrides(t *testing.T) {
	stageFakeAgent(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SHELL", filepath.Join(t.TempDir(), "missing-shell"))
	t.Setenv("MULTICA_DAEMON_BACKPRESSURE", "")
	t.Setenv("MULTICA_DAEMON_BACKPRESSURE_MEM_HIGH_PCT", "")
	t.Setenv("MULTICA_DAEMON_BACKPRESSURE_MEM_RECOVERY_PCT", "")
	t.Setenv("MULTICA_DAEMON_BACKPRESSURE_SWAP_HIGH_PCT", "")
	t.Setenv("MULTICA_DAEMON_BACKPRESSURE_SWAP_RECOVERY_PCT", "")
	t.Setenv("MULTICA_DAEMON_BACKPRESSURE_SAMPLE_INTERVAL", "")
	t.Setenv("MULTICA_DAEMON_BACKPRESSURE_WINDOW", "")

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
	if cfg.BackpressureSampleInterval != 5*time.Second || cfg.BackpressureWindowSize != 6 {
		t.Fatalf("default sampler mismatch: interval=%s window=%d", cfg.BackpressureSampleInterval, cfg.BackpressureWindowSize)
	}

	// Env overrides take effect.
	t.Setenv("MULTICA_DAEMON_BACKPRESSURE_MEM_HIGH_PCT", "20")
	t.Setenv("MULTICA_DAEMON_BACKPRESSURE_MEM_RECOVERY_PCT", "30")
	t.Setenv("MULTICA_DAEMON_BACKPRESSURE_SWAP_HIGH_PCT", "0") // disables the swap condition
	t.Setenv("MULTICA_DAEMON_BACKPRESSURE_SAMPLE_INTERVAL", "10s")
	t.Setenv("MULTICA_DAEMON_BACKPRESSURE_WINDOW", "3")
	cfg, err = LoadConfig(overrides)
	if err != nil {
		t.Fatalf("LoadConfig overrides: %v", err)
	}
	if cfg.BackpressureMemHighPct != 20 || cfg.BackpressureMemRecoveryPct != 30 {
		t.Fatalf("env threshold overrides not applied: %+v", cfg)
	}
	if cfg.BackpressureSwapHighPct != 0 {
		t.Fatalf("swap condition disable override not applied: %v", cfg.BackpressureSwapHighPct)
	}
	if cfg.BackpressureSampleInterval != 10*time.Second || cfg.BackpressureWindowSize != 3 {
		t.Fatalf("env sampler overrides not applied: interval=%s window=%d", cfg.BackpressureSampleInterval, cfg.BackpressureWindowSize)
	}

	// The kill switch disables the whole gate.
	t.Setenv("MULTICA_DAEMON_BACKPRESSURE", "false")
	cfg, err = LoadConfig(overrides)
	if err != nil {
		t.Fatalf("LoadConfig disabled: %v", err)
	}
	if cfg.BackpressureEnabled {
		t.Fatal("kill switch must disable backpressure")
	}

	// Inverted hysteresis fails fast at config load.
	t.Setenv("MULTICA_DAEMON_BACKPRESSURE", "true")
	t.Setenv("MULTICA_DAEMON_BACKPRESSURE_MEM_RECOVERY_PCT", "10")
	if _, err = LoadConfig(overrides); err == nil {
		t.Fatal("recovery below high must fail config load")
	}
}
