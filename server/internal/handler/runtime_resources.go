package handler

// RUYI-859 stage 1: per-Runtime host resource visibility.
//
// GET /api/runtimes/{runtimeId}/resources narrows the workspace's
// system-resource panel (the RUYI-618 endpoint /api/dashboard/usage/resources)
// to the single daemon host the Runtime row binds, so the Runtime detail
// page can chart that host's CPU / memory / swap / disk / IO / uptime
// instead of every daemon the workspace owns. The narrowing is server-side:
// the client passes only a fixed window name, and the daemon id is resolved
// from the runtime row the caller is already authorized to read — a member
// of workspace A cannot use any URL trick to read workspace B's daemons,
// because the row lookup happens inside the same membership gate the
// backpressure surface uses.
//
// The response keeps the RUYI-618 payload semantics (configured=false is the
// normal "no observability stack" state, not an error) and adds two
// Runtime-page facts the workspace panel does not need: sampled_at (the
// newest sample timestamp, so "how fresh is this reading" is visible) and
// boot_time_seconds (node_boot_time_seconds, from which the page derives
// host uptime). has_daemon=false is the structured answer for cloud /
// manual runtimes that bind no daemon host.

import (
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/multica-ai/multica/server/internal/metrics"
	"github.com/multica-ai/multica/server/internal/promquery"
)

// runtimeResourceQueries are the per-daemon node-exporter series for one
// Runtime's host. Same arithmetic as the workspace panel (usage_metrics.go)
// plus node_boot_time_seconds for host uptime.
func runtimeResourceQueries(daemonID string, rateWindow time.Duration) []usageQuery {
	match := daemonMatcher([]string{daemonID})
	fsFilter := `fstype!~"tmpfs|fuse.*|squashfs|nsfs|overlay|iso9660"`
	diskFilter := `device!~"^(loop|ram|fd|sr|dm-|zram)"`
	return []usageQuery{
		{"cpu_cores", fmt.Sprintf(`sum by (daemon) (rate(node_cpu_seconds_total{%s,mode!~"idle"}[%s]))`, match, durationProm(rateWindow))},
		{"memory_total_bytes", fmt.Sprintf("node_memory_MemTotal_bytes{%s}", match)},
		{"memory_available_bytes", fmt.Sprintf("node_memory_MemAvailable_bytes{%s}", match)},
		{"swap_total_bytes", fmt.Sprintf("node_memory_SwapTotal_bytes{%s}", match)},
		{"swap_free_bytes", fmt.Sprintf("node_memory_SwapFree_bytes{%s}", match)},
		{"filesystem_size_bytes", fmt.Sprintf(`sum by (daemon) (node_filesystem_size_bytes{%s,%s})`, match, fsFilter)},
		{"filesystem_avail_bytes", fmt.Sprintf(`sum by (daemon) (node_filesystem_avail_bytes{%s,%s})`, match, fsFilter)},
		{"disk_read_bytes_per_second", fmt.Sprintf(`sum by (daemon) (rate(node_disk_read_bytes_total{%s,%s}[%s]))`, match, diskFilter, durationProm(rateWindow))},
		{"disk_written_bytes_per_second", fmt.Sprintf(`sum by (daemon) (rate(node_disk_written_bytes_total{%s,%s}[%s]))`, match, diskFilter, durationProm(rateWindow))},
		{"boot_time_seconds", fmt.Sprintf("node_boot_time_seconds{%s}", match)},
	}
}

// runtimeResourcesResponse is the Runtime detail page's host-resource read
// model.
type runtimeResourcesResponse struct {
	// Configured is false when the server has no PROMETHEUS_URL — a
	// deployment without the observability stack is normal, and the page
	// renders a visible "not configured" state for it.
	Configured bool `json:"configured"`
	// DaemonID is the runtime row's daemon host; empty for cloud / manual
	// runtimes, which also set HasDaemon=false.
	DaemonID  string `json:"daemon_id"`
	HasDaemon bool   `json:"has_daemon"`
	Window    string `json:"window,omitempty"`
	// StepSeconds is the Prometheus step the range was evaluated at.
	StepSeconds int `json:"step_seconds,omitempty"`
	// SampledAt is the newest sample timestamp across all series, RFC3339 —
	// "how fresh is this reading" without a second request.
	SampledAt string `json:"sampled_at,omitempty"`
	// Series mirrors the workspace panel's keying (cpu_cores, …,
	// boot_time_seconds), already normalized to {labels, points:[{t,v}]}.
	Series map[string][]promquery.Series `json:"series"`
}

// GetRuntimeResources serves the Runtime detail page's "Resources &
// Backpressure" section. Read authorization mirrors the backpressure
// surface exactly (requireRuntimeReadAccess: membership + the runtime's own
// visibility), the window is the same fixed table as the dashboard panel,
// and the daemon id comes from the row — never from the request.
func (h *Handler) GetRuntimeResources(w http.ResponseWriter, r *http.Request) {
	rt, _, ok := h.requireRuntimeReadAccess(w, r, metrics.RuntimeLookupSourceRuntimeAPI, chi.URLParam(r, "runtimeId"))
	if !ok {
		return
	}
	// Cloud / manual runtimes bind no daemon host: the structured empty
	// answer, not a 404 (the caller is authorized; the host simply does
	// not exist), and not an error state.
	if !rt.DaemonID.Valid || rt.DaemonID.String == "" {
		writeJSON(w, http.StatusOK, runtimeResourcesResponse{Configured: h.Prometheus.Enabled()})
		return
	}
	daemonID := rt.DaemonID.String
	if !h.Prometheus.Enabled() {
		writeJSON(w, http.StatusOK, runtimeResourcesResponse{
			Configured: false,
			DaemonID:   daemonID,
			HasDaemon:  true,
			Series:     map[string][]promquery.Series{},
		})
		return
	}
	windowName, window, ok := parseUsageMetricWindow(w, r)
	if !ok {
		return
	}
	plan := planUsageMetricsWindow(window, time.Now())
	series, err := h.runUsageQueries(r.Context(), runtimeResourceQueries(daemonID, plan.RateWindow), plan)
	if err != nil {
		writeError(w, http.StatusBadGateway, "prometheus query failed")
		return
	}
	resp := runtimeResourcesResponse{
		Configured:  true,
		DaemonID:    daemonID,
		HasDaemon:   true,
		Window:      windowName,
		StepSeconds: int(plan.Step.Seconds()),
		Series:      map[string][]promquery.Series{},
	}
	newest := time.Time{}
	for key, s := range series {
		resp.Series[key] = s
		for _, sr := range s {
			for _, p := range sr.Points {
				if ts := time.Unix(p.Time, 0).UTC(); ts.After(newest) {
					newest = ts
				}
			}
		}
	}
	if !newest.IsZero() {
		resp.SampledAt = newest.Format(time.RFC3339)
	}
	writeJSON(w, http.StatusOK, resp)
}
