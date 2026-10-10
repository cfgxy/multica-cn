package handler

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/promquery"
)

// usageMetricWindows are the selectable ranges for the usage page's
// system-resource and model-traffic panels. They are shorter and coarser than
// the day-granular dashboard windows because both panels chart Prometheus
// rate/gauge series, which only make sense over hours, not months.
var usageMetricWindows = map[string]time.Duration{
	"1h":  time.Hour,
	"6h":  6 * time.Hour,
	"24h": 24 * time.Hour,
	"7d":  7 * 24 * time.Hour,
}

// usageMetricPanelLimit bounds points per series. 240 points render cleanly
// and keep the Prometheus response small on the 7d window.
const usageMetricPanelLimit = 240

// usageMetricsPlan is one resolved window: the range the panel charts, the
// query step, and the lookbehind window for rate() calls. The rate lookbehind
// is two steps so every evaluation finds samples even when a scrape is
// missed, floored at 1m (2× the 15s scrape would miss double-interval gaps)
// and capped at 30m so the 7d window stays smooth.
type usageMetricsPlan struct {
	Window     time.Duration
	Start      time.Time
	End        time.Time
	Step       time.Duration
	RateWindow time.Duration
}

func planUsageMetricsWindow(window time.Duration, now time.Time) usageMetricsPlan {
	step := window / usageMetricPanelLimit
	if step < 15*time.Second {
		step = 15 * time.Second
	}
	if step > 5*time.Minute {
		step = 5 * time.Minute
	}
	rateWindow := 2 * step
	if rateWindow < time.Minute {
		rateWindow = time.Minute
	}
	if rateWindow > 30*time.Minute {
		rateWindow = 30 * time.Minute
	}
	return usageMetricsPlan{
		Window:     window,
		Start:      now.Add(-window),
		End:        now,
		Step:       step,
		RateWindow: rateWindow,
	}
}

// daemonMatcher builds a PromQL label matcher restricted to the workspace's
// daemons. Regex metacharacters in daemon ids (hostnames are user-controlled)
// are quoted so a crafted id can only ever match itself.
func daemonMatcher(daemonIDs []string) string {
	escaped := make([]string, 0, len(daemonIDs))
	for _, id := range daemonIDs {
		escaped = append(escaped, regexp.QuoteMeta(promquery.EscapeLabelValue(id)))
	}
	return `daemon=~"` + strings.Join(escaped, "|") + `"`
}

// usageQuery is one named PromQL range evaluation inside a panel response.
type usageQuery struct {
	key   string
	query string
}

// runUsageQueries evaluates the panel's queries in order. The per-query
// context keeps a hung Prometheus from stalling the whole panel past the
// client's patience; the budget is shared, so a few fast queries leave room
// for the rest.
func (h *Handler) runUsageQueries(ctx context.Context, queries []usageQuery, plan usageMetricsPlan) (map[string][]promquery.Series, error) {
	shared, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	out := make(map[string][]promquery.Series, len(queries))
	for _, q := range queries {
		series, err := h.Prometheus.QueryRange(shared, promquery.RangeQuery{
			Query: q.query,
			Start: plan.Start,
			End:   plan.End,
			Step:  plan.Step,
		})
		if err != nil {
			return nil, err
		}
		out[q.key] = series
	}
	return out, nil
}

// GetDashboardUsageResources serves the usage page's system-resource panel:
// per-daemon CPU cores, memory/swap, filesystem, and disk IO series for this
// workspace, relayed by daemon heartbeats and stored in Prometheus
// (RUYI-618). With PROMETHEUS_URL unset the panel reports
// configured=false — a deployment without the observability stack is normal,
// not broken — and the frontend hides the section.
func (h *Handler) GetDashboardUsageResources(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	if _, ok := h.workspaceMember(w, r, workspaceID); !ok {
		return
	}
	if !h.Prometheus.Enabled() {
		writeJSON(w, http.StatusOK, map[string]any{"configured": false})
		return
	}
	windowName, window, ok := parseUsageMetricWindow(w, r)
	if !ok {
		return
	}
	daemons, err := h.Queries.ListWorkspaceDaemonIDs(r.Context(), parseUUID(workspaceID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list workspace daemons")
		return
	}
	daemonIDs := make([]string, 0, len(daemons))
	for _, d := range daemons {
		daemonIDs = append(daemonIDs, d.String)
	}
	if len(daemonIDs) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{
			"configured": true, "window": windowName, "series": map[string]any{},
		})
		return
	}
	match := daemonMatcher(daemonIDs)
	plan := planUsageMetricsWindow(window, time.Now())
	queries := []usageQuery{
		{"cpu_cores", fmt.Sprintf("sum by (daemon) (rate(multica_daemon_cpu_seconds_total{%s}[%s]))", match, durationProm(plan.RateWindow))},
		{"memory_total_bytes", fmt.Sprintf("multica_daemon_memory_total_bytes{%s}", match)},
		{"memory_available_bytes", fmt.Sprintf("multica_daemon_memory_available_bytes{%s}", match)},
		{"swap_total_bytes", fmt.Sprintf("multica_daemon_swap_total_bytes{%s}", match)},
		{"swap_free_bytes", fmt.Sprintf("multica_daemon_swap_free_bytes{%s}", match)},
		{"filesystem_size_bytes", fmt.Sprintf("multica_daemon_filesystem_size_bytes{%s}", match)},
		{"filesystem_avail_bytes", fmt.Sprintf("multica_daemon_filesystem_avail_bytes{%s}", match)},
		{"disk_read_bytes_per_second", fmt.Sprintf("rate(multica_daemon_disk_read_bytes_total{%s}[%s])", match, durationProm(plan.RateWindow))},
		{"disk_written_bytes_per_second", fmt.Sprintf("rate(multica_daemon_disk_written_bytes_total{%s}[%s])", match, durationProm(plan.RateWindow))},
	}
	series, err := h.runUsageQueries(r.Context(), queries, plan)
	if err != nil {
		writeError(w, http.StatusBadGateway, "prometheus query failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"configured":   true,
		"window":       windowName,
		"step_seconds": int(plan.Step.Seconds()),
		"series":       series,
	})
}

// usageTrafficDims are the breakdown dimensions the traffic panel accepts.
type usageTrafficDim struct {
	label string
	// by is the PromQL grouping (also the series label set requested).
	by string
}

var usageTrafficDims = map[string]usageTrafficDim{
	"runtime":  {label: "runtime", by: "runtime_id"},
	"provider": {label: "provider", by: "provider"},
	"model":    {label: "model", by: "model"},
}

// GetDashboardUsageTraffic serves the usage page's model-traffic panel:
// token / cost / task rates per workspace aggregated from the
// multica_task_usage_* exposition (RUYI-618), broken down by runtime,
// provider, or model.
func (h *Handler) GetDashboardUsageTraffic(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	if _, ok := h.workspaceMember(w, r, workspaceID); !ok {
		return
	}
	if !h.Prometheus.Enabled() {
		writeJSON(w, http.StatusOK, map[string]any{"configured": false})
		return
	}
	windowName, window, ok := parseUsageMetricWindow(w, r)
	if !ok {
		return
	}
	byName := strings.TrimSpace(r.URL.Query().Get("by"))
	if byName == "" {
		byName = "provider"
	}
	dim, ok := usageTrafficDims[byName]
	if !ok {
		writeError(w, http.StatusBadRequest, "by must be one of runtime, provider, model")
		return
	}
	plan := planUsageMetricsWindow(window, time.Now())
	ws := fmt.Sprintf("workspace_id=%q", promquery.EscapeLabelValue(workspaceID))
	queries := []usageQuery{
		{"tokens_per_second", fmt.Sprintf("sum by (%s) (rate(multica_task_usage_tokens_total{%s}[%s]))", dim.by, ws, durationProm(plan.RateWindow))},
		{"cost_usd_per_second", fmt.Sprintf("sum by (%s) (rate(multica_task_usage_cost_usd_total{%s}[%s]))", dim.by, ws, durationProm(plan.RateWindow))},
		{"tasks_per_second", fmt.Sprintf("sum by (%s) (rate(multica_task_usage_tasks_total{%s}[%s]))", dim.by, ws, durationProm(plan.RateWindow))},
	}
	series, err := h.runUsageQueries(r.Context(), queries, plan)
	if err != nil {
		writeError(w, http.StatusBadGateway, "prometheus query failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"configured":   true,
		"window":       windowName,
		"by":           dim.label,
		"step_seconds": int(plan.Step.Seconds()),
		"series":       series,
	})
}

// parseUsageMetricWindow reads the ?window= param against the fixed window
// table, defaulting to 24h. An unknown value is a client typo and gets a 400
// rather than silent fallback.
func parseUsageMetricWindow(w http.ResponseWriter, r *http.Request) (string, time.Duration, bool) {
	name := strings.TrimSpace(r.URL.Query().Get("window"))
	if name == "" {
		name = "24h"
	}
	window, ok := usageMetricWindows[name]
	if !ok {
		writeError(w, http.StatusBadRequest, "window must be one of 1h, 6h, 24h, 7d")
		return "", 0, false
	}
	return name, window, true
}

// durationProm renders a duration as a PromQL range literal. The inputs come
// from the window table and step math above, so whole seconds always hold.
func durationProm(d time.Duration) string {
	return fmt.Sprintf("%ds", int(d.Seconds()))
}
