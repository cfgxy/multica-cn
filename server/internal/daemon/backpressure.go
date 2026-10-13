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

// backpressureCard renders the boot config as its wire card: version 0
// (not DB-managed) and the "env" source, until a saved settings row replaces
// or shadows it over the heartbeat ack (RUYI-618 workspace cards,
// RUYI-729 host cards).
func (c Config) backpressureCard() protocol.DaemonBackpressureConfig {
	return protocol.DaemonBackpressureConfig{
		Enabled:               true,
		MemHighPct:            c.BackpressureMemHighPct,
		MemRecoveryPct:        c.BackpressureMemRecoveryPct,
		SwapHighPct:           c.BackpressureSwapHighPct,
		SwapRecoveryPct:       c.BackpressureSwapRecoveryPct,
		PSIHighPct:            c.BackpressurePSIHighPct,
		PSIRecoveryPct:        c.BackpressurePSIRecoveryPct,
		SampleIntervalSeconds: int(c.BackpressureSampleInterval / time.Second),
		WindowSize:            c.BackpressureWindowSize,
		Source:                protocol.BackpressureSourceEnv,
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

// backpressureRuntime is one gate's active configuration: the wire card it
// was built from (echoed back inside reports so the UI and the event
// history show the rule actually in force — RUYI-729), the master switch,
// the sampling cadence, and the window-smoothed machine built from the
// thresholds. Swapped atomically whenever a heartbeat ack delivers settings
// (workspace cards, RUYI-618; the daemon-host override card, RUYI-729) —
// the watcher re-reads it every cycle, so a settings save reaches a running
// daemon without a restart. The machine is rebuilt on each apply, carrying
// the previous window over so the new thresholds judge the same history the
// old ones did (while a fresh window refills, the mean of the samples so
// far applies, so the gate stays responsive).
type backpressureRuntime struct {
	config   protocol.DaemonBackpressureConfig
	enabled  bool
	interval time.Duration
	machine  *backpressureMachine
}

// newBackpressureRuntime builds the container from a wire card. The card is
// the single source: thresholds and cadence are read back out of it so the
// runtime can never drift from what it reports. The machine is built even
// for a disabled card — enabled=false just stops the watcher from advancing
// it (the slot is released instead), so re-enabling is a pure flag flip.
func newBackpressureRuntime(card protocol.DaemonBackpressureConfig, th backpressureThresholds) *backpressureRuntime {
	rt := &backpressureRuntime{
		config:   card,
		enabled:  card.Enabled,
		interval: time.Duration(card.SampleIntervalSeconds) * time.Second,
		machine:  newBackpressureMachine(th, card.WindowSize),
	}
	if rt.interval <= 0 {
		rt.interval = DefaultBackpressureSampleInterval
	}
	return rt
}

// backpressureWorkspaceState is one workspace's applied card plus its gate's
// latest observation (RUYI-618). A daemon can serve several workspaces and
// each carries its own card, so entries live in a copy-on-write map keyed by
// workspace ID: one workspace's heartbeat delivery can never overwrite
// another's parameters or gate state (F1). A workspace that has not
// delivered a card yet evaluates against the boot-default card
// (d.bpRuntime / d.bpState) — the pre-first-ack window keeps the RUYI-393
// boot behavior.
type backpressureWorkspaceState struct {
	runtime *backpressureRuntime
	state   atomic.Pointer[backpressureObservation]
}

// backpressureWorkspaceEntries snapshots the applied-card map. nil until the
// first card is applied.
func (d *Daemon) backpressureWorkspaceEntries() map[string]*backpressureWorkspaceState {
	if m := d.bpWorkspaces.Load(); m != nil {
		return *m
	}
	return nil
}

// backpressureEntryFor returns the workspace's applied card, or nil when no
// card has landed (the workspace then rides the boot-default card).
func (d *Daemon) backpressureEntryFor(workspaceID string) *backpressureWorkspaceState {
	return d.backpressureWorkspaceEntries()[workspaceID]
}

// bootFallbackActive reports whether any watched workspace still evaluates
// the boot-default card (no applied entry yet — or no bindings at all, so a
// registration may land before its first heartbeat ack). The boot gate only
// matters in that window: once every bound workspace carries its own card
// the boot evaluation stops, because it would have no reader and its
// transition logs would describe claims that per-workspace cards no longer
// pause.
func (d *Daemon) bootFallbackActive(entries map[string]*backpressureWorkspaceState) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.workspaces) == 0 {
		return true
	}
	for wsID := range d.workspaces {
		if _, ok := entries[wsID]; !ok {
			return true
		}
	}
	return false
}

// backpressureObservationForWorkspace snapshots the latest gate evaluation
// for one workspace. Precedence: the daemon-host override observation while
// a host card is in force (RUYI-729 — a starving machine must pause all its
// workspaces regardless of their own cards, and a host card with
// Enabled=false fails the whole machine open), then the workspace's applied
// card's observation, then the boot-default observation while the workspace
// has no card of its own. nil = this workspace's gate has never observed
// (or is disabled) — fail open.
func (d *Daemon) backpressureObservationForWorkspace(workspaceID string) *backpressureObservation {
	if d.bpHostRuntime.Load() != nil {
		return d.bpHostState.Load()
	}
	if e := d.backpressureWorkspaceEntries()[workspaceID]; e != nil {
		return e.state.Load()
	}
	return d.bpState.Load()
}

// backpressureBlockedForWorkspace reports whether the claim gate must refuse
// new claims for one workspace right now.
func (d *Daemon) backpressureBlockedForWorkspace(workspaceID string) bool {
	st := d.backpressureObservationForWorkspace(workspaceID)
	return st != nil && st.Active
}

// runtimeWorkspaceID resolves which watched workspace a runtime belongs to.
// Empty when the runtime is unknown — a heartbeat racing its
// deregistration.
func (d *Daemon) runtimeWorkspaceID(runtimeID string) string {
	return d.workspaceByRuntime()[runtimeID]
}

// workspaceByRuntime maps every runtime ID of every watched workspace to its
// workspace ID in one pass under the daemon lock.
func (d *Daemon) workspaceByRuntime() map[string]string {
	d.mu.Lock()
	defer d.mu.Unlock()
	m := make(map[string]string, len(d.runtimeIndex))
	for wsID, ws := range d.workspaces {
		for _, rid := range ws.runtimeIDs {
			m[rid] = wsID
		}
	}
	return m
}

// filterBackpressureClaimable partitions a claim batch by each runtime's
// workspace gate (F1): runtimes whose workspace is in backpressure drop out,
// so the batch claim never asks for tasks a suppressed workspace must not
// receive while other workspaces on this host keep claiming. Runtimes whose
// workspace cannot be resolved stay claimable — fail open, matching the
// gate's nil-state stance.
func (d *Daemon) filterBackpressureClaimable(runtimeIDs []string) []string {
	wsByRuntime := d.workspaceByRuntime()
	blocked := make(map[string]bool)
	claimable := make([]string, 0, len(runtimeIDs))
	for _, rid := range runtimeIDs {
		wsID := wsByRuntime[rid]
		if wsID == "" {
			claimable = append(claimable, rid)
			continue
		}
		b, known := blocked[wsID]
		if !known {
			b = d.backpressureBlockedForWorkspace(wsID)
			blocked[wsID] = b
		}
		if !b {
			claimable = append(claimable, rid)
		}
	}
	return claimable
}

// anyBackpressureBlocked reports whether any workspace owning these runtimes
// currently gates claims — the authoritative re-check inside the claim
// barrier. The poller's filter normally dropped held runtimes already; the
// re-check closes the filter-to-claim window.
func (d *Daemon) anyBackpressureBlocked(runtimeIDs []string) bool {
	wsByRuntime := d.workspaceByRuntime()
	seen := make(map[string]bool)
	for _, rid := range runtimeIDs {
		wsID := wsByRuntime[rid]
		if wsID == "" || seen[wsID] {
			continue
		}
		seen[wsID] = true
		if d.backpressureBlockedForWorkspace(wsID) {
			return true
		}
	}
	return false
}

// backpressureReportForWorkspace snapshots one workspace's gate state for
// upstream reporting. nil when that gate has never run (or is disabled) —
// the report key is omitted from payloads in that case. The config card
// actually in force rides along (RUYI-729) with per-condition readings, so
// the server's event history and the UI can show exactly which rule was
// live: the host override card while it shadows, else the workspace's
// applied card, else the boot card — matching the observation precedence.
func (d *Daemon) backpressureReportForWorkspace(workspaceID string) *protocol.DaemonBackpressureReport {
	st := d.backpressureObservationForWorkspace(workspaceID)
	if st == nil {
		return nil
	}
	rep := st.DaemonBackpressureReport
	rep.DeferredClaims = d.bpDeferred.Load()
	var cfg *protocol.DaemonBackpressureConfig
	if rt := d.bpHostRuntime.Load(); rt != nil {
		cfg = &rt.config
	} else if e := d.backpressureEntryFor(workspaceID); e != nil {
		cfg = &e.runtime.config
	} else if boot := d.bpRuntime.Load(); boot != nil {
		cfg = &boot.config
	}
	if cfg != nil {
		c := *cfg
		rep.Config = &c
		rep.Conditions = conditionReadings(&c, &st.DaemonBackpressureReport)
	}
	return &rep
}

// backpressureReportForRuntime attributes the gate report to the runtime's
// own workspace (F1): a multi-workspace daemon no longer ships one
// machine-level state to every runtime it hosts.
func (d *Daemon) backpressureReportForRuntime(runtimeID string) *protocol.DaemonBackpressureReport {
	return d.backpressureReportForWorkspace(d.runtimeWorkspaceID(runtimeID))
}

// backpressureReportForClaim builds the claim-side report. A batch whose
// runtimes all belong to one workspace keeps the RUYI-393 machine-level
// shape — that workspace's report, bit-identical to the pre-F1 behavior for
// single-workspace daemons. A mixed batch carries no report at all: one
// machine statement cannot describe workspaces that gate differently, and
// the server falls back to the stored per-runtime reports, which heartbeats
// keep per-workspace correct.
func (d *Daemon) backpressureReportForClaim(runtimeIDs []string) *protocol.DaemonBackpressureReport {
	wsByRuntime := d.workspaceByRuntime()
	wsID := ""
	for _, rid := range runtimeIDs {
		id := wsByRuntime[rid]
		if id == "" {
			continue
		}
		if wsID == "" {
			wsID = id
		} else if wsID != id {
			return nil
		}
	}
	return d.backpressureReportForWorkspace(wsID)
}

// backpressureSampleInterval is the cadence the watcher currently runs at:
// the fastest cadence any enabled gate source demands (each workspace's
// applied card, the boot default, and the daemon-host override card
// (RUYI-729)), else the code default. The minimum keeps every card honest —
// a 1s workspace is not slowed down by a 60s neighbor.
func (d *Daemon) backpressureSampleInterval() time.Duration {
	fastest := time.Duration(0)
	consider := func(rt *backpressureRuntime) {
		if rt != nil && rt.enabled && rt.interval > 0 && (fastest == 0 || rt.interval < fastest) {
			fastest = rt.interval
		}
	}
	consider(d.bpRuntime.Load())
	if host := d.bpHostRuntime.Load(); host != nil {
		consider(host)
	}
	for _, e := range d.backpressureWorkspaceEntries() {
		consider(e.runtime)
	}
	if fastest > 0 {
		return fastest
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

// backpressureSamplerFailed throttles sampler-failure warnings so a broken
// procfs mount cannot flood the log at the sampling cadence, and reports
// whether the tick must hold the previous gate state (it must — an
// unreadable /proc must neither trip nor clear a gate on a guess).
func (d *Daemon) backpressureSamplerFailed(err error) bool {
	if err == nil {
		return false
	}
	if last := d.bpLastWarn.Load(); last == 0 || time.Since(time.Unix(0, last)) > backpressureWarnInterval {
		if d.bpLastWarn.CompareAndSwap(last, time.Now().UnixNano()) {
			d.logger.Warn("backpressure sampler failed; holding previous gate state",
				"error", err)
		}
	}
	return true
}

// backpressureTick samples host memory once and folds the sample into every
// enabled gate source: each workspace's applied card advances its own
// machine and observation (F1), and the boot-default slot advances only
// while some watched workspace still lacks a card of its own (the
// pre-first-ack window; see bootFallbackActive). While a daemon-host
// override card is in force (RUYI-729) the host slot is the only one
// evaluated — it speaks for every workspace on this machine — and the
// shadowed boot and workspace latches are released every tick so stale
// shadows can never resume governing after the override lifts (they
// re-latch from their carried windows on the first tick after removal).
// One failed sample holds every previous state — an unreadable /proc must
// neither trip nor clear a gate on a guess.
func (d *Daemon) backpressureTick() {
	if host := d.bpHostRuntime.Load(); host != nil {
		if host.enabled {
			sample, err := d.bpSource.sample()
			if d.backpressureSamplerFailed(err) {
				return
			}
			d.advanceBackpressureGate(&d.bpHostState, host.machine, sample, "scope", "daemon-host")
		} else {
			// Host-level disable: fail the whole machine open.
			d.releaseBackpressureSlot(&d.bpHostState, "scope", "daemon-host")
		}
		d.releaseBackpressureSlot(&d.bpState, "scope", "boot-default")
		for wsID, e := range d.backpressureWorkspaceEntries() {
			d.releaseBackpressureSlot(&e.state, "workspace_id", wsID)
		}
		return
	}

	boot := d.bpRuntime.Load()
	entries := d.backpressureWorkspaceEntries()
	bootActive := boot != nil && boot.enabled && d.bootFallbackActive(entries)

	anyActive := bootActive
	for _, e := range entries {
		if e.runtime.enabled {
			anyActive = true
			break
		}
	}
	if !anyActive {
		// Every gate source disabled: fail open and forget any latched
		// episode so paused claims resume immediately.
		if st := d.bpState.Swap(nil); st != nil && st.Active {
			d.logger.Warn("memory backpressure gate disabled — resuming task claims",
				"scope", "boot-default", "reason", st.Reason, "deferred_claims", d.bpDeferred.Swap(0))
			if ch := d.bpWakeup.Load(); ch != nil {
				signalPollerWakeup(*ch)
			}
		}
		for wsID, e := range entries {
			d.releaseBackpressureSlot(&e.state, "workspace_id", wsID)
		}
		return
	}

	sample, err := d.bpSource.sample()
	if d.backpressureSamplerFailed(err) {
		return
	}

	if bootActive {
		d.advanceBackpressureGate(&d.bpState, boot.machine, sample, "scope", "boot-default")
	} else {
		// Boot evaluation stopped (every workspace now carries its own
		// card): drop any latched boot episode so a later loss of all cards
		// cannot resurrect a stale pause.
		d.releaseBackpressureSlot(&d.bpState, "scope", "boot-default")
	}
	for wsID, e := range entries {
		if e.runtime.enabled {
			d.advanceBackpressureGate(&e.state, e.runtime.machine, sample, "workspace_id", wsID)
		} else {
			d.releaseBackpressureSlot(&e.state, "workspace_id", wsID)
		}
	}
}

// advanceBackpressureGate folds one sample into one gate slot's machine and
// stores the observation, logging transitions with the caller's attribution
// fields (boot-default vs one workspace's card).
func (d *Daemon) advanceBackpressureGate(slot *atomic.Pointer[backpressureObservation], machine *backpressureMachine, sample memSample, logArgs ...any) {
	prev := slot.Load()
	obs := machine.observe(sample)
	now := time.Now()
	if obs.Transitioned {
		obs.Since = now
	} else if prev != nil {
		obs.Since = prev.Since
	}
	slot.Store(&obs)

	if !obs.Transitioned {
		return
	}
	if obs.Active {
		d.logger.Warn("memory backpressure ENTERED — pausing new task claims",
			append(logArgs[:len(logArgs):len(logArgs)],
				"reason", obs.Reason,
				"mem_available_pct", obs.MemAvailablePct,
				"swap_used_pct", obs.SwapUsedPct,
				"psi_some_avg10", obs.PSIMemorySomeAvg10,
				"psi_read_ok", obs.PSIReadOK)...)
		return
	}
	// The episode's start lives in the previous state's Since — obs.Since was
	// just reset to now by the transition above.
	heldFor := time.Duration(0)
	if prev != nil {
		heldFor = now.Sub(prev.Since)
	}
	d.logger.Info("memory backpressure CLEARED — resuming task claims",
		append(logArgs[:len(logArgs):len(logArgs)],
			"mem_available_pct", obs.MemAvailablePct,
			"swap_used_pct", obs.SwapUsedPct,
			"psi_some_avg10", obs.PSIMemorySomeAvg10,
			"psi_read_ok", obs.PSIReadOK,
			"deferred_claims", d.bpDeferred.Swap(0),
			"held_for", heldFor.Round(time.Second).String())...)
	// Nudge the batch poller so recovery takes effect on the next cycle
	// instead of after one backoff interval.
	if ch := d.bpWakeup.Load(); ch != nil {
		signalPollerWakeup(*ch)
	}
}

// releaseBackpressureSlot clears one gate slot's latched observation, waking
// the poller when the latch was actively pausing claims (the card turned
// disabled, or the slot's evaluation stopped).
func (d *Daemon) releaseBackpressureSlot(slot *atomic.Pointer[backpressureObservation], logArgs ...any) {
	if st := slot.Swap(nil); st != nil && st.Active {
		d.logger.Warn("memory backpressure gate disabled — resuming task claims",
			append(logArgs[:len(logArgs):len(logArgs)],
				"reason", st.Reason, "deferred_claims", d.bpDeferred.Swap(0))...)
		if ch := d.bpWakeup.Load(); ch != nil {
			signalPollerWakeup(*ch)
		}
	}
}

// applyBackpressureConfig hot-applies one workspace's backpressure settings
// carried on a heartbeat ack (RUYI-618). Cards are scoped per workspace
// (F1): a daemon serving several workspaces keeps one gate per workspace, so
// workspace A's delivery can never overwrite workspace B's parameters or
// gate state. Invalid configs are ignored loudly and that workspace's
// previous parameters stay authoritative (fail-open to the known-good set);
// identical repeat deliveries are dropped silently so the per-beat delivery
// cannot churn the log.
func (d *Daemon) applyBackpressureConfig(workspaceID, runtimeID string, cfg *protocol.DaemonBackpressureConfig) {
	if cfg == nil {
		return
	}
	if workspaceID == "" {
		// The runtime's workspace could not be resolved (a beat racing its
		// deregistration). Sibling runtimes of the workspace re-deliver the
		// card on their own beats; a phantom-key entry would orphan the card.
		d.logger.Debug("skipping backpressure apply; runtime has no workspace", "runtime_id", runtimeID)
		return
	}
	if err := cfg.Validate(); err != nil {
		d.logger.Warn("ignoring invalid workspace backpressure config", "workspace_id", workspaceID, "runtime_id", runtimeID, "error", err)
		return
	}
	rt := newBackpressureRuntime(*cfg, backpressureThresholds{
		MemHighPct:      cfg.MemHighPct,
		MemRecoveryPct:  cfg.MemRecoveryPct,
		SwapHighPct:     cfg.SwapHighPct,
		SwapRecoveryPct: cfg.SwapRecoveryPct,
		PSIHighPct:      cfg.PSIHighPct,
		PSIRecoveryPct:  cfg.PSIRecoveryPct,
	})

	d.bpApplyMu.Lock()
	entries := d.backpressureWorkspaceEntries()
	if prev := entries[workspaceID]; prev != nil && prev.runtime.config == rt.config {
		d.bpApplyMu.Unlock()
		return
	}
	// Copy-on-write swap: readers (claim filter, reports) never take the
	// apply lock. The previous observation carries over so a latched episode
	// survives a threshold change until the next tick re-evaluates it on the
	// new machine — same shape as the pre-F1 single-gate apply. The previous
	// machine's smoothing window carries over too, so the new thresholds
	// judge the same history the old ones did instead of starting from an
	// empty window (RUYI-729).
	entry := &backpressureWorkspaceState{runtime: rt}
	if prev := entries[workspaceID]; prev != nil {
		entry.state.Store(prev.state.Load())
		if prev.runtime.machine != nil && rt.machine != nil {
			rt.machine.window = append(rt.machine.window, prev.runtime.machine.window...)
			if len(rt.machine.window) > rt.machine.windowSize {
				rt.machine.window = rt.machine.window[len(rt.machine.window)-rt.machine.windowSize:]
			}
		}
	}
	next := make(map[string]*backpressureWorkspaceState, len(entries)+1)
	for k, v := range entries {
		next[k] = v
	}
	next[workspaceID] = entry
	// A fresh reload channel per apply: the watcher selects on whichever
	// channel is currently stored, so closing the previous pointer wakes it
	// exactly once per swap and concurrent applies stay panic-free.
	nextCh := make(chan struct{})
	if prevCh := d.bpReload.Swap(&nextCh); prevCh != nil {
		close(*prevCh)
	}
	d.bpWorkspaces.Store(&next)
	d.bpApplyMu.Unlock()

	if !cfg.Enabled {
		// Disabling the gate must release claims latched by an earlier
		// episode immediately, not on the next sampler tick.
		d.releaseBackpressureSlot(&entry.state, "workspace_id", workspaceID)
		d.logger.Info("workspace backpressure settings applied — gate disabled",
			"workspace_id", workspaceID, "runtime_id", runtimeID)
		return
	}
	d.logger.Info("workspace backpressure settings applied",
		"workspace_id", workspaceID,
		"runtime_id", runtimeID,
		"mem_high_pct", cfg.MemHighPct, "mem_recovery_pct", cfg.MemRecoveryPct,
		"swap_high_pct", cfg.SwapHighPct, "swap_recovery_pct", cfg.SwapRecoveryPct,
		"psi_high_pct", cfg.PSIHighPct, "psi_recovery_pct", cfg.PSIRecoveryPct,
		"sample_interval_seconds", cfg.SampleIntervalSeconds, "window_size", cfg.WindowSize)
}

// dropBackpressureWorkspace forgets a workspace's applied card when the
// daemon stops watching the workspace (F1): the entry must not outlive its
// workspace, or a stale latch could suppress a future re-bind's pre-ack
// claims.
func (d *Daemon) dropBackpressureWorkspace(workspaceID string) {
	d.bpApplyMu.Lock()
	entries := d.backpressureWorkspaceEntries()
	cur, ok := entries[workspaceID]
	if !ok {
		d.bpApplyMu.Unlock()
		return
	}
	next := make(map[string]*backpressureWorkspaceState, len(entries)-1)
	for k, v := range entries {
		if k != workspaceID {
			next[k] = v
		}
	}
	d.bpWorkspaces.Store(&next)
	d.bpApplyMu.Unlock()
	d.releaseBackpressureSlot(&cur.state, "workspace_id", workspaceID)
}

// applyBackpressureHostConfig hot-applies the daemon-host override card
// carried on a heartbeat ack (RUYI-729). While in force it shadows the
// boot default and every workspace card on this machine (see
// backpressureObservationForWorkspace); a changed card rebuilds the host
// machine while keeping the smoothing window's samples, so the next
// evaluation blends history under the new lines. Invalid configs are
// ignored loudly and the previously in-force card stays authoritative —
// fail-closed on config, fail-open on scheduling. Identical repeat
// deliveries are dropped silently so the per-beat delivery cannot churn the
// log. Disabling releases the host episode immediately and fails the
// override open until a re-enable.
func (d *Daemon) applyBackpressureHostConfig(runtimeID string, card *protocol.DaemonBackpressureConfig) {
	if card == nil {
		return
	}
	if err := card.Validate(); err != nil {
		d.logger.Warn("ignoring invalid daemon backpressure config", "runtime_id", runtimeID, "error", err)
		return
	}
	if card.Source == protocol.BackpressureSourceDB {
		d.bpDBManaged.Store(true)
	}
	rt := newBackpressureRuntime(*card, backpressureThresholds{
		MemHighPct:      card.MemHighPct,
		MemRecoveryPct:  card.MemRecoveryPct,
		SwapHighPct:     card.SwapHighPct,
		SwapRecoveryPct: card.SwapRecoveryPct,
		PSIHighPct:      card.PSIHighPct,
		PSIRecoveryPct:  card.PSIRecoveryPct,
	})

	d.bpApplyMu.Lock()
	prev := d.bpHostRuntime.Load()
	if prev != nil && prev.config == rt.config {
		d.bpApplyMu.Unlock()
		return
	}
	if prev != nil && prev.machine != nil && rt.machine != nil {
		rt.machine.window = append(rt.machine.window, prev.machine.window...)
		if len(rt.machine.window) > rt.machine.windowSize {
			rt.machine.window = rt.machine.window[len(rt.machine.window)-rt.machine.windowSize:]
		}
	}
	d.bpHostRuntime.Store(rt)
	// Same wake-once-per-swap discipline as the workspace apply: the watcher
	// re-evaluates on the new card right away instead of waiting out the old
	// interval.
	nextCh := make(chan struct{})
	if prevCh := d.bpReload.Swap(&nextCh); prevCh != nil {
		close(*prevCh)
	}
	d.bpApplyMu.Unlock()

	if !card.Enabled {
		d.releaseBackpressureSlot(&d.bpHostState, "scope", "daemon-host")
		d.logger.Info("daemon backpressure settings applied — gate disabled",
			"runtime_id", runtimeID)
		return
	}
	d.logger.Info("daemon backpressure settings applied",
		"runtime_id", runtimeID,
		"config_version", card.ConfigVersion,
		"mem_high_pct", card.MemHighPct, "mem_recovery_pct", card.MemRecoveryPct,
		"swap_high_pct", card.SwapHighPct, "swap_recovery_pct", card.SwapRecoveryPct,
		"psi_high_pct", card.PSIHighPct, "psi_recovery_pct", card.PSIRecoveryPct,
		"sample_interval_seconds", card.SampleIntervalSeconds, "window_size", card.WindowSize)
}

// applyBackpressureHostConfigFallback converges the host override away when
// a heartbeat ack stops carrying a daemon card. For a daemon that once
// carried a saved card that nil means the owner deleted the settings row —
// keeping the last override forever was the D1 defect (RUYI-729 rework):
// the workspace cards (re-delivered every beat) and the boot default govern
// again without a restart, on the very next tick. A daemon that never
// received a saved card keeps its boot config; "no card" there is just the
// unconfigured state, not a reset signal. Repeat calls converge on the
// CAS in here, so beating here costs one atomic read.
func (d *Daemon) applyBackpressureHostConfigFallback(runtimeID string) {
	if !d.bpDBManaged.CompareAndSwap(true, false) {
		return
	}
	d.bpApplyMu.Lock()
	if prev := d.bpHostRuntime.Swap(nil); prev != nil {
		d.logger.Info("daemon backpressure settings removed — workspace card governs again",
			"runtime_id", runtimeID, "prev_config_version", prev.config.ConfigVersion)
	}
	d.bpApplyMu.Unlock()
	// The shadowed slots stopped evaluating while the override was in
	// force, so their latches are stale by definition; fail them open and
	// let the next tick (woken immediately below) re-latch them from their
	// carried windows.
	d.releaseBackpressureSlot(&d.bpState, "scope", "boot-default")
	for wsID, e := range d.backpressureWorkspaceEntries() {
		d.releaseBackpressureSlot(&e.state, "workspace_id", wsID)
	}
	nextCh := make(chan struct{})
	if prevCh := d.bpReload.Swap(&nextCh); prevCh != nil {
		close(*prevCh)
	}
}

// conditionReadings renders the report's smoothed values against the card's
// high watermarks — the full current-vs-threshold triple for every
// condition, including the ones not tripping, so "how close are we to the
// line" is answerable without knowing the config's semantics.
func conditionReadings(card *protocol.DaemonBackpressureConfig, rep *protocol.DaemonBackpressureReport) []protocol.DaemonBackpressureCondition {
	th := backpressureThresholds{
		MemHighPct:      card.MemHighPct,
		MemRecoveryPct:  card.MemRecoveryPct,
		SwapHighPct:     card.SwapHighPct,
		SwapRecoveryPct: card.SwapRecoveryPct,
		PSIHighPct:      card.PSIHighPct,
		PSIRecoveryPct:  card.PSIRecoveryPct,
	}
	return []protocol.DaemonBackpressureCondition{
		{
			Condition: "mem",
			Value:     rep.MemAvailablePct,
			Threshold: card.MemHighPct,
			Triggered: th.memLow(rep.MemAvailablePct),
		},
		{
			Condition: "swap",
			Value:     rep.SwapUsedPct,
			Threshold: card.SwapHighPct,
			Triggered: th.swapHigh(rep.SwapUsedPct),
		},
		{
			Condition: "psi",
			Value:     rep.PSIMemorySomeAvg10,
			Threshold: card.PSIHighPct,
			Triggered: th.psiHigh(rep.PSIMemorySomeAvg10, rep.PSIReadOK),
		},
	}
}
