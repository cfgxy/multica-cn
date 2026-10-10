package metrics

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// DefaultUsageMetricsTTL bounds how often the collector re-reads the
// task_usage_hourly aggregates. Scrapes land every few seconds; the underlying
// numbers move at task granularity, so a couple of minutes of staleness is
// invisible on a rate() chart and keeps the aggregate query off the scrape path.
const DefaultUsageMetricsTTL = 2 * time.Minute

// usageSourceTimeout bounds the refresh query so a slow database delays the
// scrape by a bounded amount instead of hanging it.
const usageSourceTimeout = 5 * time.Second

// UsageTotalsSource supplies the lifetime per-(workspace, runtime, provider,
// model) aggregates. *db.Queries satisfies it; the interface exists so tests
// can pin TTL and error behavior without a database.
type UsageTotalsSource interface {
	SumTaskUsageTotalsForMetrics(ctx context.Context) ([]db.SumTaskUsageTotalsForMetricsRow, error)
}

// tokenTypeSeries pairs the Prometheus token_type label with the aggregate
// column it exposes.
var tokenTypeSeries = []struct {
	label string
	value func(db.SumTaskUsageTotalsForMetricsRow) int64
}{
	{"input", func(r db.SumTaskUsageTotalsForMetricsRow) int64 { return r.InputTokens }},
	{"output", func(r db.SumTaskUsageTotalsForMetricsRow) int64 { return r.OutputTokens }},
	{"cache_read", func(r db.SumTaskUsageTotalsForMetricsRow) int64 { return r.CacheReadTokens }},
	{"cache_write", func(r db.SumTaskUsageTotalsForMetricsRow) int64 { return r.CacheWriteTokens }},
}

// UsageCollector exposes workspace-scoped model traffic as Prometheus
// counters aggregated from task_usage_hourly (RUYI-618): token volume, task
// count, and cost (authoritative provider ticks plus a rate-table estimate
// over uncosted tokens — the migration-213 consumer contract). The
// in-process multica_llm_* counters stay as they are: they carry no
// workspace label and reset on restart, so they cannot back a workspace
// usage view; these series can.
//
// The source query returns lifetime sums, which makes the published values
// valid Prometheus counters (monotonically non-decreasing; workspace
// deletion is the only decrease and reads as a counter reset).
type UsageCollector struct {
	src UsageTotalsSource
	ttl time.Duration
	now func() time.Time

	tokens *prometheus.Desc
	cost   *prometheus.Desc
	tasks  *prometheus.Desc

	mu       sync.Mutex
	snapshot []db.SumTaskUsageTotalsForMetricsRow
	staleAt  time.Time
}

func NewUsageCollector(src UsageTotalsSource) *UsageCollector {
	return NewUsageCollectorWithTTL(src, DefaultUsageMetricsTTL)
}

func NewUsageCollectorWithTTL(src UsageTotalsSource, ttl time.Duration) *UsageCollector {
	// A non-positive TTL would re-query on every scrape; clamp to the default
	// rather than inherit the misconfiguration.
	if ttl <= 0 {
		ttl = DefaultUsageMetricsTTL
	}
	labels := []string{"workspace_id", "runtime_id", "provider", "model"}
	return &UsageCollector{
		src: src,
		ttl: ttl,
		now: time.Now,
		tokens: prometheus.NewDesc("multica_task_usage_tokens_total",
			"Lifetime task usage tokens by token type.",
			append(append([]string(nil), labels...), "token_type"), nil),
		cost: prometheus.NewDesc("multica_task_usage_cost_usd_total",
			"Lifetime task usage cost in USD: provider-reported ticks plus the rate-table estimate over uncosted tokens.",
			labels, nil),
		tasks: prometheus.NewDesc("multica_task_usage_tasks_total",
			"Lifetime task count behind the usage rows.",
			labels, nil),
	}
}

func (c *UsageCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.tokens
	ch <- c.cost
	ch <- c.tasks
}

func (c *UsageCollector) Collect(ch chan<- prometheus.Metric) {
	for _, row := range c.snapshotForCollect() {
		labels := []string{row.WorkspaceID, row.RuntimeID, row.Provider, row.Model}
		for _, tt := range tokenTypeSeries {
			ch <- prometheus.MustNewConstMetric(c.tokens, prometheus.CounterValue,
				float64(tt.value(row)), append(append([]string(nil), labels...), tt.label)...)
		}
		ch <- prometheus.MustNewConstMetric(c.cost, prometheus.CounterValue, usageCostUSD(row), labels...)
		ch <- prometheus.MustNewConstMetric(c.tasks, prometheus.CounterValue,
			float64(row.TaskCount), labels...)
	}
}

// snapshotForCollect serves the cached aggregate, refreshing it when the TTL
// has lapsed. A failed refresh keeps serving the last good snapshot and the
// next attempt waits for a full TTL — a database blip must neither blank the
// exposition nor turn every scrape into a query.
func (c *UsageCollector) snapshotForCollect() []db.SumTaskUsageTotalsForMetricsRow {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.snapshot == nil || !c.now().Before(c.staleAt) {
		ctx, cancel := context.WithTimeout(context.Background(), usageSourceTimeout)
		rows, err := c.src.SumTaskUsageTotalsForMetrics(ctx)
		cancel()
		if err != nil {
			slog.Warn("usage metrics refresh failed; serving last snapshot", "error", err)
		} else {
			c.snapshot = rows
		}
		c.staleAt = c.now().Add(c.ttl)
	}
	return c.snapshot
}

// usageCostUSD applies the migration-213 consumer contract on one aggregate
// row: provider-reported ticks are authoritative, uncosted tokens are priced
// from the static rate table. A model with no rate row contributes its
// authoritative cost only — inventing a rate would put a fake charge on real
// spend, and dropping the authoritative part would hide it.
func usageCostUSD(row db.SumTaskUsageTotalsForMetricsRow) float64 {
	cost := float64(row.CostUsdTicks) / CostUSDTicksPerUSD
	if price, priced := PriceForModelAlias(row.Model); priced {
		cost += tokenCostUSD(row.UncostedInputTokens, price.InputPerM)
		cost += tokenCostUSD(row.UncostedOutputTokens, price.OutputPerM)
		cost += tokenCostUSD(row.UncostedCacheReadTokens, price.CacheReadPerM)
		cost += tokenCostUSD(row.UncostedCacheWriteTokens, price.CacheWritePerM)
	}
	return cost
}
