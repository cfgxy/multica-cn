package daemon

import (
	"bufio"
	"context"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Host memory backpressure (RUYI-393): when the machine's memory watermark
// crosses the configured high threshold, the daemon stops claiming new tasks
// and lets them stay queued server-side; when it falls back below the
// recovery watermark, claiming resumes automatically. This cuts the
// "busier → more new runs → deeper OOM" feedback loop at the source while
// leaving already-running tasks untouched.
//
// The gate is a double-threshold hysteresis machine fed by a window-smoothed
// sampler, and it is deliberately orthogonal to the auto-update claim
// barrier (pauseClaims): backpressure has its own state so
// releaseClaimBarrier can never clear it by accident, and auto-update does
// not need to know about it — claims it already blocks make
// claimsInFlight drain on their own.

// backpressureThresholds carries the hysteresis pair for each condition.
// Trigger is OR across conditions; recovery is AND. Percentages are ratios
// of the corresponding total: MemAvailablePct low is bad, SwapUsedPct and
// PSI some-avg10 high are bad.
//
// The defaults are initial safety parameters chosen from the RUYI-392
// incident profile, not calibrated final values — tune them per workspace
// from the settings UI (RUYI-618); the effective card arrives on every
// heartbeat ack and is hot-applied by applyBackpressureConfig.
type backpressureThresholds struct {
	MemHighPct      float64
	MemRecoveryPct  float64
	SwapHighPct     float64
	SwapRecoveryPct float64
	PSIHighPct      float64
	PSIRecoveryPct  float64
}

// triggered reports whether the (smoothed) sample crosses any high
// watermark. SwapHighPct <= 0 disables the swap condition entirely so
// swap-less policy adjustments stay a pure config change.
func (th backpressureThresholds) memLow(p float64) bool {
	return th.MemHighPct > 0 && p < th.MemHighPct
}

func (th backpressureThresholds) swapHigh(p float64) bool {
	return th.SwapHighPct > 0 && p > th.SwapHighPct
}

// psiHigh reports whether the smoothed memory PSI crosses its high
// watermark. Unlike the watermarks, PSI is a stall *rate* — higher is bad.
// A disabled condition (PSIHighPct <= 0) or an unreadable PSI source
// (psiOK false: kernel without PSI, broken procfs mount) never trips it —
// the condition degrades to "skip" and the remaining mem/swap conditions
// keep gating alone, so a missing PSI can neither block nor trip the gate.
func (th backpressureThresholds) psiHigh(psiAvg float64, psiOK bool) bool {
	return th.PSIHighPct > 0 && psiOK && psiAvg > th.PSIHighPct
}

// recovered reports whether every enabled condition is back inside its
// recovery band. A disabled condition counts as recovered; so does a
// condition whose source went unreadable (psiOK false) — failing PSI
// must not wedge the gate active forever.
func (th backpressureThresholds) recovered(memAvailPct, swapUsedPct, psiAvg float64, psiOK bool) bool {
	if th.MemRecoveryPct > 0 && memAvailPct < th.MemRecoveryPct {
		return false
	}
	if th.SwapHighPct > 0 && th.SwapRecoveryPct > 0 && swapUsedPct > th.SwapRecoveryPct {
		return false
	}
	if th.PSIHighPct > 0 && th.PSIRecoveryPct > 0 && psiOK && psiAvg > th.PSIRecoveryPct {
		return false
	}
	return true
}

// backpressureReasons names the condition(s) currently past their high
// watermark: "mem", "swap", "psi", or a "+"-joined combination such as
// "mem+swap+psi".
func (th backpressureThresholds) backpressureReasons(memAvailPct, swapUsedPct, psiAvg float64, psiOK bool) string {
	var parts []string
	if th.memLow(memAvailPct) {
		parts = append(parts, "mem")
	}
	if th.swapHigh(swapUsedPct) {
		parts = append(parts, "swap")
	}
	if th.psiHigh(psiAvg, psiOK) {
		parts = append(parts, "psi")
	}
	return strings.Join(parts, "+")
}

// memSample is one raw host-memory reading. Percentages are 0-100. PSI
// feeds the gate as a third condition (RUYI-397); a sample with PSIReadOK
// false simply carries no PSI vote.
type memSample struct {
	MemAvailablePct    float64
	SwapUsedPct        float64
	PSIMemorySomeAvg10 float64
	PSIReadOK          bool
	At                 time.Time
}

// Default /proc locations.
const (
	procMeminfoPath    = "/proc/meminfo"
	procSwapsPath      = "/proc/swaps"
	procPressureMemory = "/proc/pressure/memory"
)

// memSampleSource reads host memory watermarks from procfs. Paths are fields
// so tests can redirect the sampler at fixture files; production daemons get
// them from newMemSampleSource.
type memSampleSource struct {
	MeminfoPath  string
	SwapsPath    string
	PressurePath string
}

func newMemSampleSource() memSampleSource {
	return memSampleSource{
		MeminfoPath:  procMeminfoPath,
		SwapsPath:    procSwapsPath,
		PressurePath: procPressureMemory,
	}
}

// sample reads one watermark sample. It returns an error when the primary
// signal (meminfo) is unreadable or malformed — callers must hold the
// previous gate state in that case rather than guess. PSI or swap
// information being unavailable is NOT an error: swap-less hosts are normal
// and PSI may be compiled out of the kernel.
func (s memSampleSource) sample() (memSample, error) {
	now := time.Now()

	memAvailPct, err := readMemAvailablePct(s.MeminfoPath)
	if err != nil {
		return memSample{}, fmt.Errorf("read meminfo: %w", err)
	}
	swapUsedPct := readSwapUsedPct(s.SwapsPath)
	psiAvg10, psiOK := readMemoryPressureSomeAvg10(s.PressurePath)

	return memSample{
		MemAvailablePct:    memAvailPct,
		SwapUsedPct:        swapUsedPct,
		PSIMemorySomeAvg10: psiAvg10,
		PSIReadOK:          psiOK,
		At:                 now,
	}, nil
}

// readMemAvailablePct parses MemAvailable/MemTotal from /proc/meminfo
// (values in kB) into a 0-100 ratio.
func readMemAvailablePct(path string) (float64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	var total, avail float64
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		kb, ok := parseMeminfoKb(sc.Text())
		if !ok {
			continue
		}
		switch kb.key {
		case "MemTotal":
			total = kb.value
		case "MemAvailable":
			avail = kb.value
		}
	}
	if err := sc.Err(); err != nil {
		return 0, err
	}
	if total <= 0 {
		return 0, fmt.Errorf("%s: MemTotal missing or zero", path)
	}
	return pct(avail, total), nil
}

type meminfoKb struct {
	key   string
	value float64
}

// parseMeminfoKb parses one "Key:  123 kB" meminfo line.
func parseMeminfoKb(line string) (meminfoKb, bool) {
	key, value, ok := strings.Cut(line, ":")
	if !ok {
		return meminfoKb{}, false
	}
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return meminfoKb{}, false
	}
	kb, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return meminfoKb{}, false
	}
	return meminfoKb{key: strings.TrimSpace(key), value: kb}, true
}

// readSwapUsedPct sums /proc/swaps Size and Used columns (kB) into a 0-100
// ratio. A swap-less host (header-only or missing file) returns 0 — no swap
// means no swap pressure, not an error.
func readSwapUsedPct(path string) float64 {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()

	var total, used float64
	sc := bufio.NewScanner(f)
	first := true
	for sc.Scan() {
		if first {
			first = false
			continue // header row
		}
		fields := strings.Fields(sc.Text())
		// Filename Type Size Used Priority
		if len(fields) < 4 {
			continue
		}
		size, err1 := strconv.ParseFloat(fields[2], 64)
		use, err2 := strconv.ParseFloat(fields[3], 64)
		if err1 != nil || err2 != nil {
			continue
		}
		total += size
		used += use
	}
	if total <= 0 {
		return 0
	}
	return pct(used, total)
}

// readMemoryPressureSomeAvg10 parses the "some" line's avg10 from
// /proc/pressure/memory. ok is false when the kernel does not expose PSI —
// observation-only, so callers degrade silently.
func readMemoryPressureSomeAvg10(path string) (avg10 float64, ok bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "some" {
			continue
		}
		for _, kv := range fields[1:] {
			if v, found := strings.CutPrefix(kv, "avg10="); found {
				f, err := strconv.ParseFloat(v, 64)
				if err != nil {
					return 0, false
				}
				return f, true
			}
		}
	}
	return 0, false
}

// pct returns value/total as a percentage rounded to one decimal, so report
// payloads and server-side change detection don't churn on float noise.
func pct(value, total float64) float64 {
	if total <= 0 {
		return 0
	}
	return mathRound1(value / total * 100)
}

func mathRound1(v float64) float64 {
	return math.Round(v*10) / 10
}

// backpressureMachine is the window-smoothed hysteresis state machine.
// Observe is not safe for concurrent use; the daemon serializes it behind
// its single watcher goroutine.
type backpressureMachine struct {
	thresholds backpressureThresholds
	windowSize int
	window     []memSample
	active     bool
}

// backpressureObservation is one state-machine evaluation result. The
// embedded report is what gets shipped to the server; Transitioned and Since
// drive daemon-side logging and are never serialized.
type backpressureObservation struct {
	protocol.DaemonBackpressureReport

	Transitioned bool
	Since        time.Time
}

func newBackpressureMachine(th backpressureThresholds, windowSize int) *backpressureMachine {
	if windowSize < 1 {
		windowSize = 1
	}
	return &backpressureMachine{
		thresholds: th,
		windowSize: windowSize,
		window:     make([]memSample, 0, windowSize),
	}
}

// observe folds one sample into the smoothing window and re-evaluates the
// gate. The window mean absorbs transient spikes so a single allocation
// burst cannot flap claims; hysteresis (high ≠ recovery) absorbs sustained
// oscillation around one threshold. While the window is still filling, the
// mean of the samples so far is used — a daemon starting on an already
// starved host must trip immediately, not after a full window.
func (m *backpressureMachine) observe(s memSample) backpressureObservation {
	if len(m.window) >= m.windowSize {
		m.window = m.window[1:]
	}
	m.window = append(m.window, s)

	var memAvailPct, swapUsedPct, psiAvg float64
	psiOK := false
	for _, w := range m.window {
		memAvailPct += w.MemAvailablePct
		swapUsedPct += w.SwapUsedPct
		psiAvg += w.PSIMemorySomeAvg10
		psiOK = psiOK || w.PSIReadOK
	}
	n := float64(len(m.window))
	memAvailPct = mathRound1(memAvailPct / n)
	swapUsedPct = mathRound1(swapUsedPct / n)
	psiAvg = mathRound1(psiAvg / n)

	th := m.thresholds
	reason := th.backpressureReasons(memAvailPct, swapUsedPct, psiAvg, psiOK)
	transitioned := false

	switch {
	case !m.active && reason != "":
		m.active = true
		transitioned = true
	case m.active && reason == "" && th.recovered(memAvailPct, swapUsedPct, psiAvg, psiOK):
		m.active = false
		transitioned = true
	}

	obs := backpressureObservation{
		DaemonBackpressureReport: protocol.DaemonBackpressureReport{
			Active:             m.active,
			MemAvailablePct:    memAvailPct,
			SwapUsedPct:        swapUsedPct,
			PSIMemorySomeAvg10: psiAvg,
			PSIReadOK:          psiOK,
		},
		Transitioned: transitioned,
	}
	if m.active {
		obs.Reason = reason
	}
	return obs
}

// backpressureBlocked reports whether the claim gate must refuse new claims
// right now. A nil state (sampler disabled or not started yet) never blocks
// — the gate fails open so a broken sampler can't silently stop all
// scheduling.
func (d *Daemon) backpressureBlocked() bool {
	st := d.bpState.Load()
	return st != nil && st.Active
}

// backpressureThresholds exposes the configured hysteresis pair for the
// Daemon constructor.
func (c Config) backpressureThresholds() backpressureThresholds {
	return backpressureThresholds{
		MemHighPct:      c.BackpressureMemHighPct,
		MemRecoveryPct:  c.BackpressureMemRecoveryPct,
		SwapHighPct:     c.BackpressureSwapHighPct,
		SwapRecoveryPct: c.BackpressureSwapRecoveryPct,
		PSIHighPct:      c.BackpressurePSIHighPct,
		PSIRecoveryPct:  c.BackpressurePSIRecoveryPct,
	}
}

// validateBackpressureThresholds adapts the in-memory threshold pair to the
// wire-config validation in protocol — the single rulebook the settings API,
// the heartbeat delivery, and this boot path all enforce. Intervals are
// whole seconds on every caller (the code default; the wire card is
// seconds-denominated), so the conversion is lossless here.
func validateBackpressureThresholds(th backpressureThresholds, sampleInterval time.Duration, windowSize int) error {
	cfg := protocol.DaemonBackpressureConfig{
		MemHighPct:            th.MemHighPct,
		MemRecoveryPct:        th.MemRecoveryPct,
		SwapHighPct:           th.SwapHighPct,
		SwapRecoveryPct:       th.SwapRecoveryPct,
		PSIHighPct:            th.PSIHighPct,
		PSIRecoveryPct:        th.PSIRecoveryPct,
		SampleIntervalSeconds: int(sampleInterval / time.Second),
		WindowSize:            windowSize,
	}
	return cfg.Validate()
}

// backpressureReport snapshots the current gate state for upstream reporting.
// Returns nil when the sampler has never run (or is disabled) — the report
// key is omitted from claim/heartbeat payloads in that case.
func (d *Daemon) backpressureReport() *protocol.DaemonBackpressureReport {
	st := d.bpState.Load()
	if st == nil {
		return nil
	}
	rep := st.DaemonBackpressureReport
	rep.DeferredClaims = d.bpDeferred.Load()
	return &rep
}

// backpressureRuntime is the gate's active configuration: master switch,
// sampling cadence, and the window-smoothed machine built from the
// thresholds. Swapped atomically whenever a heartbeat ack delivers
// workspace settings (RUYI-618) — the watcher re-reads it every cycle, so a
// settings save reaches a running daemon without a restart. The machine is
// rebuilt on each apply, restarting the smoothing window on the new
// parameters (while it refills, the mean of the samples so far applies, so
// the gate stays responsive).
type backpressureRuntime struct {
	enabled  bool
	interval time.Duration
	machine  *backpressureMachine
}

// backpressureSampleInterval is the cadence the watcher currently runs at:
// the applied workspace setting, else the code default.
func (d *Daemon) backpressureSampleInterval() time.Duration {
	if rt := d.bpRuntime.Load(); rt != nil && rt.interval > 0 {
		return rt.interval
	}
	return DefaultBackpressureSampleInterval
}

// backpressureReloadSignal is the channel an apply wakes the watcher with —
// nil (a case that never fires) until the first settings swap lands.
func backpressureReloadSignal(p *atomic.Pointer[chan struct{}]) <-chan struct{} {
	if ch := p.Load(); ch != nil {
		return *ch
	}
	return nil
}

// runBackpressureWatcher samples host memory and advances the hysteresis
// machine. State transitions are logged loudly — entering backpressure
// explains why tasks stop being claimed, and the exit log pairs with the
// server-side audit trail. One failed sample holds the previous gate state:
// an unreadable /proc must neither trip nor clear the gate on a guess. The
// cadence and the parameters themselves are re-read every cycle so a
// workspace settings save (RUYI-618) takes effect immediately, including a
// resample right after the swap instead of waiting out the old interval.
func (d *Daemon) runBackpressureWatcher(ctx context.Context) {
	d.backpressureTick()
	timer := time.NewTimer(d.backpressureSampleInterval())
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			d.backpressureTick()
		case <-backpressureReloadSignal(&d.bpReload):
			d.backpressureTick()
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(d.backpressureSampleInterval())
	}
}

// backpressureWarnInterval throttles sampler-failure warnings so a broken
// procfs mount cannot flood the log at the sampling cadence.
const backpressureWarnInterval = time.Minute

func (d *Daemon) backpressureTick() {
	rt := d.bpRuntime.Load()
	if rt == nil || !rt.enabled {
		// Gate disabled (workspace setting): fail open and forget any
		// latched episode so paused claims resume immediately.
		if st := d.bpState.Swap(nil); st != nil && st.Active {
			d.logger.Warn("memory backpressure gate disabled — resuming task claims",
				"reason", st.Reason, "deferred_claims", d.bpDeferred.Swap(0))
			if ch := d.bpWakeup.Load(); ch != nil {
				signalPollerWakeup(*ch)
			}
		}
		return
	}
	sample, err := d.bpSource.sample()
	if err != nil {
		if last := d.bpLastWarn.Load(); last == 0 || time.Since(time.Unix(0, last)) > backpressureWarnInterval {
			if d.bpLastWarn.CompareAndSwap(last, time.Now().UnixNano()) {
				d.logger.Warn("backpressure sampler failed; holding previous gate state",
					"error", err)
			}
		}
		return
	}

	prev := d.bpState.Load()
	obs := rt.machine.observe(sample)
	now := time.Now()
	if obs.Transitioned {
		obs.Since = now
	} else if prev != nil {
		obs.Since = prev.Since
	}
	d.bpState.Store(&obs)

	if !obs.Transitioned {
		return
	}
	if obs.Active {
		d.logger.Warn("memory backpressure ENTERED — pausing new task claims",
			"reason", obs.Reason,
			"mem_available_pct", obs.MemAvailablePct,
			"swap_used_pct", obs.SwapUsedPct,
			"psi_some_avg10", obs.PSIMemorySomeAvg10,
			"psi_read_ok", obs.PSIReadOK)
		return
	}
	// The episode's start lives in the previous state's Since — obs.Since was
	// just reset to now by the transition above.
	heldFor := time.Duration(0)
	if prev != nil {
		heldFor = now.Sub(prev.Since)
	}
	d.logger.Info("memory backpressure CLEARED — resuming task claims",
		"mem_available_pct", obs.MemAvailablePct,
		"swap_used_pct", obs.SwapUsedPct,
		"psi_some_avg10", obs.PSIMemorySomeAvg10,
		"psi_read_ok", obs.PSIReadOK,
		"deferred_claims", d.bpDeferred.Swap(0),
		"held_for", heldFor.Round(time.Second).String())
	// Nudge the batch poller so recovery takes effect on the next cycle
	// instead of after one backoff interval.
	if ch := d.bpWakeup.Load(); ch != nil {
		signalPollerWakeup(*ch)
	}
}

// applyBackpressureConfig hot-applies the workspace's backpressure settings
// carried on a heartbeat ack (RUYI-618). The server sends the effective card
// on every beat — saved row or code defaults — so a save in the web UI
// reaches running daemons within one heartbeat, no restart. Invalid configs
// are ignored loudly and the previous parameters stay authoritative
// (fail-open to the known-good set); identical configs are dropped silently
// so the per-beat delivery cannot churn the log.
func (d *Daemon) applyBackpressureConfig(runtimeID string, cfg *protocol.DaemonBackpressureConfig) {
	if cfg == nil {
		return
	}
	if err := cfg.Validate(); err != nil {
		d.logger.Warn("ignoring invalid workspace backpressure config", "runtime_id", runtimeID, "error", err)
		return
	}
	rt := &backpressureRuntime{
		enabled:  cfg.Enabled,
		interval: time.Duration(cfg.SampleIntervalSeconds) * time.Second,
		machine: newBackpressureMachine(backpressureThresholds{
			MemHighPct:      cfg.MemHighPct,
			MemRecoveryPct:  cfg.MemRecoveryPct,
			SwapHighPct:     cfg.SwapHighPct,
			SwapRecoveryPct: cfg.SwapRecoveryPct,
			PSIHighPct:      cfg.PSIHighPct,
			PSIRecoveryPct:  cfg.PSIRecoveryPct,
		}, cfg.WindowSize),
	}

	d.bpApplyMu.Lock()
	if prev := d.bpRuntime.Load(); prev != nil && prev.enabled == rt.enabled &&
		prev.interval == rt.interval && prev.machine.thresholds == rt.machine.thresholds &&
		prev.machine.windowSize == rt.machine.windowSize {
		d.bpApplyMu.Unlock()
		return
	}
	// A fresh reload channel per apply: the watcher selects on whichever
	// channel is currently stored, so closing the previous pointer wakes it
	// exactly once per swap and concurrent applies stay panic-free.
	next := make(chan struct{})
	if prevCh := d.bpReload.Swap(&next); prevCh != nil {
		close(*prevCh)
	}
	d.bpRuntime.Store(rt)
	d.bpApplyMu.Unlock()

	if !cfg.Enabled {
		// Disabling the gate must release claims latched by an earlier
		// episode immediately, not on the next sampler tick.
		if st := d.bpState.Swap(nil); st != nil && st.Active {
			d.logger.Warn("workspace backpressure disabled — resuming paused task claims",
				"runtime_id", runtimeID, "reason", st.Reason, "deferred_claims", d.bpDeferred.Swap(0))
			if ch := d.bpWakeup.Load(); ch != nil {
				signalPollerWakeup(*ch)
			}
		}
		d.logger.Info("workspace backpressure settings applied — gate disabled", "runtime_id", runtimeID)
		return
	}
	d.logger.Info("workspace backpressure settings applied",
		"runtime_id", runtimeID,
		"mem_high_pct", cfg.MemHighPct, "mem_recovery_pct", cfg.MemRecoveryPct,
		"swap_high_pct", cfg.SwapHighPct, "swap_recovery_pct", cfg.SwapRecoveryPct,
		"psi_high_pct", cfg.PSIHighPct, "psi_recovery_pct", cfg.PSIRecoveryPct,
		"sample_interval_seconds", cfg.SampleIntervalSeconds, "window_size", cfg.WindowSize)
}
