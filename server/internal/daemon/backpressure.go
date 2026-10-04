package daemon

import (
	"bufio"
	"context"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
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
// of the corresponding total: MemAvailablePct low is bad, SwapUsedPct high
// is bad.
//
// The defaults are initial safety parameters chosen from the RUYI-392
// incident profile, not calibrated final values — tune via env as GTI
// field data comes in (see LoadConfig).
type backpressureThresholds struct {
	MemHighPct      float64
	MemRecoveryPct  float64
	SwapHighPct     float64
	SwapRecoveryPct float64
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

// recovered reports whether both conditions are back inside their recovery
// band. A disabled swap condition counts as recovered.
func (th backpressureThresholds) recovered(memAvailPct, swapUsedPct float64) bool {
	if th.MemRecoveryPct > 0 && memAvailPct < th.MemRecoveryPct {
		return false
	}
	if th.SwapHighPct > 0 && th.SwapRecoveryPct > 0 && swapUsedPct > th.SwapRecoveryPct {
		return false
	}
	return true
}

// backpressureReasons names the condition(s) currently past their high
// watermark: "mem", "swap", or "mem+swap".
func (th backpressureThresholds) backpressureReasons(memAvailPct, swapUsedPct float64) string {
	mem := th.memLow(memAvailPct)
	swap := th.swapHigh(swapUsedPct)
	switch {
	case mem && swap:
		return "mem+swap"
	case mem:
		return "mem"
	case swap:
		return "swap"
	default:
		return ""
	}
}

// memSample is one raw host-memory reading. Percentages are 0-100;
// PSI is observation-only telemetry and never feeds the gate.
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
	reason := th.backpressureReasons(memAvailPct, swapUsedPct)
	transitioned := false

	switch {
	case !m.active && reason != "":
		m.active = true
		transitioned = true
	case m.active && reason == "" && th.recovered(memAvailPct, swapUsedPct):
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
	}
}

// validateBackpressureThresholds rejects configs whose hysteresis bands are
// inverted or degenerate: recovery thresholds must sit on the safe side of
// their high counterparts (above for MemAvailable, below for SwapUsed), or
// the gate would flap at the boundary it exists to damp. Sample interval and
// window must be positive so the watcher actually moves.
func validateBackpressureThresholds(th backpressureThresholds, sampleInterval time.Duration, windowSize int) error {
	if th.MemHighPct <= 0 || th.MemHighPct > 100 {
		return fmt.Errorf("backpressure: mem high watermark %g%% out of range (0,100]", th.MemHighPct)
	}
	if th.MemRecoveryPct <= th.MemHighPct || th.MemRecoveryPct > 100 {
		return fmt.Errorf("backpressure: mem recovery watermark %g%% must be above the high watermark %g%% (hysteresis)", th.MemRecoveryPct, th.MemHighPct)
	}
	if th.SwapHighPct > 0 {
		if th.SwapHighPct > 100 {
			return fmt.Errorf("backpressure: swap high watermark %g%% out of range (0,100]", th.SwapHighPct)
		}
		if th.SwapRecoveryPct < 0 || th.SwapRecoveryPct >= th.SwapHighPct {
			return fmt.Errorf("backpressure: swap recovery watermark %g%% must be below the high watermark %g%% (hysteresis)", th.SwapRecoveryPct, th.SwapHighPct)
		}
	}
	if sampleInterval <= 0 {
		return fmt.Errorf("backpressure: sample interval %s must be positive", sampleInterval)
	}
	if windowSize < 1 {
		return fmt.Errorf("backpressure: window size %d must be at least 1", windowSize)
	}
	return nil
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

// runBackpressureWatcher samples host memory on the configured interval and
// advances the hysteresis machine. State transitions are logged loudly —
// entering backpressure explains why tasks stop being claimed, and the exit
// log pairs with the server-side audit trail. One failed sample holds the
// previous gate state: an unreadable /proc must neither trip nor clear the
// gate on a guess.
func (d *Daemon) runBackpressureWatcher(ctx context.Context) {
	interval := d.cfg.BackpressureSampleInterval
	if interval <= 0 {
		interval = DefaultBackpressureSampleInterval
	}
	d.backpressureTick()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.backpressureTick()
		}
	}
}

// backpressureWarnInterval throttles sampler-failure warnings so a broken
// procfs mount cannot flood the log at the sampling cadence.
const backpressureWarnInterval = time.Minute

func (d *Daemon) backpressureTick() {
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
	obs := d.bpMachine.observe(sample)
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
			"swap_used_pct", obs.SwapUsedPct)
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
		"deferred_claims", d.bpDeferred.Swap(0),
		"held_for", heldFor.Round(time.Second).String())
	// Nudge the batch poller so recovery takes effect on the next cycle
	// instead of after one backoff interval.
	if ch := d.bpWakeup.Load(); ch != nil {
		signalPollerWakeup(*ch)
	}
}
