// RUYI-645: daemon-side service inventory collector and ops-capability
// probe (design §5). The collector walks the machine's service manager once
// every opsInventoryInterval (plus one early cycle after startup) and POSTs
// the result to /api/daemon/inventory keyed by daemon_id — a machine fact,
// not workspace data; the server resolves which workspaces can read it.
// The caps probe feeds the same report's daemon and the register payload so
// the server's submission gates know what this node can run.
package daemon

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	opsInventoryInterval = 5 * time.Minute
	opsInventoryStartup  = 30 * time.Second // let the first register land first
	opsCommandBudget     = 5 * time.Second  // per collector command
	opsCollectBudget     = 10 * time.Second // whole collection
	opsCapsCacheTTL      = 5 * time.Minute
)

// opsServiceEntry mirrors the server's daemonInventoryService wire shape.
type opsServiceEntry struct {
	Name   string `json:"name"`
	Type   string `json:"type,omitempty"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// opsCapsCache memoizes the capability probe; register is cheap but the
// drift path can re-register repeatedly, and binary lookups are not free.
var (
	opsCapsMu     sync.Mutex
	opsCapsCached []string
	opsCapsAt     time.Time
)

// opsCaps returns this node's ops capabilities, re-probing at most once per
// TTL. Called from the register path (so a reconnect re-reports after any
// local change, e.g. docker installed) and from the inventory report.
func (d *Daemon) opsCaps() []string {
	opsCapsMu.Lock()
	defer opsCapsMu.Unlock()
	if opsCapsCached != nil && time.Since(opsCapsAt) < opsCapsCacheTTL {
		return opsCapsCached
	}
	opsCapsCached = probeOpsCaps()
	opsCapsAt = time.Now()
	return opsCapsCached
}

// probeOpsCaps detects which whitelisted command families this node can
// actually run. Deliberately strict: a systemd cap requires systemd as pid1
// (not merely a systemctl binary), because that's the only setup where the
// whitelisted commands can succeed.
func probeOpsCaps() []string {
	caps := []string{}
	switch runtime.GOOS {
	case "linux":
		if systemdRunning() {
			caps = append(caps, "systemd")
		}
	case "darwin":
		if _, err := exec.LookPath("launchctl"); err == nil {
			caps = append(caps, "launchd")
		}
	}
	if _, err := exec.LookPath("docker"); err == nil {
		caps = append(caps, "docker")
	}
	return caps
}

func systemdRunning() bool {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return false
	}
	comm, err := os.ReadFile("/proc/1/comm")
	if err != nil {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(string(comm)), "systemd")
}

// opsInventoryLoop collects and reports the service inventory forever; the
// first cycle runs shortly after startup so a freshly registered node has a
// usable inventory (and unit gate) within its first minutes.
func (d *Daemon) opsInventoryLoop(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(opsInventoryStartup):
	}
	d.collectAndReportInventory(ctx)

	ticker := time.NewTicker(opsInventoryInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.collectAndReportInventory(ctx)
		}
	}
}

func (d *Daemon) collectAndReportInventory(ctx context.Context) {
	services, err := collectServiceInventory()
	report := map[string]any{
		"daemon_id":      d.cfg.DaemonID,
		"collect_status": "ok",
		"services":       services,
	}
	if err != nil {
		// A failed collection never carries a partial list: the server
		// forces the unavailable form (assertion 2).
		report["collect_status"] = "unavailable"
		report["services"] = []opsServiceEntry{}
		report["error"] = err.Error()
		d.logger.Warn("ops inventory collection failed", "daemon_id", d.cfg.DaemonID, "error", err)
	} else {
		storeOpsInventoryUnits(services)
	}

	collectCtx, cancel := context.WithTimeout(ctx, opsCollectBudget)
	defer cancel()
	if err := d.client.ReportInventory(collectCtx, report); err != nil {
		d.logger.Warn("ops inventory report failed", "daemon_id", d.cfg.DaemonID, "error", err)
		return
	}
	d.logger.Debug("ops inventory reported", "daemon_id", d.cfg.DaemonID, "services", len(services))
}

// collectServiceInventory runs the platform collector under a hard budget.
func collectServiceInventory() ([]opsServiceEntry, error) {
	switch {
	case runtime.GOOS == "linux" && systemdRunning():
		out, err := runCollected("systemctl", "list-units", "--type=service", "--all", "--no-legend", "--no-pager")
		if err != nil {
			return nil, err
		}
		return parseSystemctlUnits(out), nil
	case runtime.GOOS == "darwin":
		out, err := runCollected("launchctl", "list")
		if err != nil {
			return nil, err
		}
		return parseLaunchctlList(out), nil
	case runtime.GOOS == "windows":
		out, err := runCollected("sc", "query", "state=", "all")
		if err != nil {
			return nil, err
		}
		return parseWindowsServices(out), nil
	default:
		return nil, errUnsupportedOpsPlatform
	}
}

var errUnsupportedOpsPlatform = errOpsPlatform{}

type errOpsPlatform struct{}

func (errOpsPlatform) Error() string {
	return "platform has no service inventory collector"
}

func runCollected(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), opsCommandBudget)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// parseSystemctlUnits reads `systemctl list-units --type=service --all
// --no-legend` lines: "unit load active sub description…". The active+sub
// pair collapses to one status word; the human description rides along as
// detail so the Web panel can explain a unit without a second query.
func parseSystemctlUnits(out string) []opsServiceEntry {
	units := []opsServiceEntry{}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		detail := ""
		if idx := strings.Index(line, fields[3]); idx >= 0 {
			detail = strings.TrimSpace(line[idx+len(fields[3]):])
		}
		// An active unit's sub-state (running/exited/waiting) is the
		// informative part for an operator panel; inactive and failed
		// already say enough on their own.
		status := fields[2]
		if status == "active" {
			status = fields[3]
		}
		units = append(units, opsServiceEntry{
			Name:   fields[0],
			Type:   "systemd",
			Status: status,
			Detail: detail,
		})
	}
	return units
}

// parseLaunchctlList reads `launchctl list`: header line, then
// "PID\tLastExitStatus\tLabel" rows; "-" PID means not currently running.
func parseLaunchctlList(out string) []opsServiceEntry {
	units := []opsServiceEntry{}
	for _, line := range strings.Split(out, "\n")[1:] {
		fields := strings.Split(strings.TrimRight(line, "\r"), "\t")
		if len(fields) < 3 {
			continue
		}
		pid := strings.TrimSpace(fields[0])
		lastStatus := strings.TrimSpace(fields[1])
		status := "exited"
		if pid != "-" {
			status = "running"
		}
		units = append(units, opsServiceEntry{
			Name:   strings.TrimSpace(fields[2]),
			Type:   "launchd",
			Status: status,
			Detail: fmt.Sprintf("last exit status: %s", lastStatus),
		})
	}
	return units
}

// parseWindowsServices reads `sc query state= all` blocks: each service is
// SERVICE_NAME / DISPLAY_NAME / STATE lines; the STATE number maps to the
// running/stopped pair that matters for v1.
func parseWindowsServices(out string) []opsServiceEntry {
	units := []opsServiceEntry{}
	var current *opsServiceEntry
	flush := func() {
		if current != nil {
			units = append(units, *current)
			current = nil
		}
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if name, ok := strings.CutPrefix(line, "SERVICE_NAME: "); ok {
			flush()
			current = &opsServiceEntry{Name: strings.TrimSpace(name), Type: "windows"}
			continue
		}
		if current == nil {
			continue
		}
		if state, ok := strings.CutPrefix(strings.TrimSpace(line), "STATE"); ok {
			state = strings.TrimSpace(strings.TrimPrefix(state, ":"))
			switch {
			case strings.Contains(state, "RUNNING"):
				current.Status = "running"
			case strings.Contains(state, "STOPPED"):
				current.Status = "stopped"
			default:
				current.Status = "other"
			}
			current.Detail = state
		}
	}
	flush()
	return units
}
