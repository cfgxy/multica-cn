package metrics

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	promtest "github.com/prometheus/client_golang/prometheus/testutil"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// stubUsageSource serves canned rows and counts refresh calls so tests can
// pin the TTL behavior without a database.
type stubUsageSource struct {
	rows  []db.SumTaskUsageTotalsForMetricsRow
	err   error
	calls int
}

func (s *stubUsageSource) SumTaskUsageTotalsForMetrics(context.Context) ([]db.SumTaskUsageTotalsForMetricsRow, error) {
	s.calls++
	return s.rows, s.err
}

func usageRow(workspace, runtime, provider, model string, input, output, cacheRead, cacheWrite, costTicks, taskCount int64) db.SumTaskUsageTotalsForMetricsRow {
	return db.SumTaskUsageTotalsForMetricsRow{
		WorkspaceID: workspace, RuntimeID: runtime, Provider: provider, Model: model,
		InputTokens: input, OutputTokens: output, CacheReadTokens: cacheRead, CacheWriteTokens: cacheWrite,
		CostUsdTicks: costTicks,
		// NULL uncosted columns (pre-recompute buckets) fall back to ALL
		// tokens being uncosted; the stub mirrors that by default.
		UncostedInputTokens: input, UncostedOutputTokens: output,
		UncostedCacheReadTokens: cacheRead, UncostedCacheWriteTokens: cacheWrite,
		TaskCount: taskCount,
	}
}

// TestUsageCollectorExposesAggregates pins the full exposition for one priced
// model: every token type becomes its own series, cost is authoritative ticks
// plus the rate-table estimate over uncosted tokens, and tasks are exposed as
// a counter (RUYI-618).
//
// gpt-5.5 rates: $5.00 input / $0.50 cache-read / $30.00 output per million.
// Authoritative: 5_000_000_000 ticks = $0.50.
// Estimate: 1_000_000 input × 5.00/M = $5.00, 200_000 output × 30/M = $6.00,
// 300_000 cache-read × 0.50/M = $0.15 → total $11.65.
func TestUsageCollectorExposesAggregates(t *testing.T) {
	src := &stubUsageSource{rows: []db.SumTaskUsageTotalsForMetricsRow{
		usageRow("ws-1", "rt-1", "openai", "gpt-5.5", 1_000_000, 200_000, 300_000, 0, 5_000_000_000, 7),
	}}
	reg := prometheus.NewRegistry()
	reg.MustRegister(NewUsageCollectorWithTTL(src, time.Hour))

	want := `
# HELP multica_task_usage_cost_usd_total Lifetime task usage cost in USD: provider-reported ticks plus the rate-table estimate over uncosted tokens.
# TYPE multica_task_usage_cost_usd_total counter
multica_task_usage_cost_usd_total{model="gpt-5.5",provider="openai",runtime_id="rt-1",workspace_id="ws-1"} 11.65
# HELP multica_task_usage_tasks_total Lifetime task count behind the usage rows.
# TYPE multica_task_usage_tasks_total counter
multica_task_usage_tasks_total{model="gpt-5.5",provider="openai",runtime_id="rt-1",workspace_id="ws-1"} 7
# HELP multica_task_usage_tokens_total Lifetime task usage tokens by token type.
# TYPE multica_task_usage_tokens_total counter
multica_task_usage_tokens_total{model="gpt-5.5",provider="openai",runtime_id="rt-1",token_type="cache_read",workspace_id="ws-1"} 300000
multica_task_usage_tokens_total{model="gpt-5.5",provider="openai",runtime_id="rt-1",token_type="cache_write",workspace_id="ws-1"} 0
multica_task_usage_tokens_total{model="gpt-5.5",provider="openai",runtime_id="rt-1",token_type="input",workspace_id="ws-1"} 1000000
multica_task_usage_tokens_total{model="gpt-5.5",provider="openai",runtime_id="rt-1",token_type="output",workspace_id="ws-1"} 200000
`
	if err := promtest.GatherAndCompare(reg, strings.NewReader(want),
		"multica_task_usage_tokens_total", "multica_task_usage_cost_usd_total", "multica_task_usage_tasks_total"); err != nil {
		t.Fatal(err)
	}
	if src.calls != 1 {
		t.Fatalf("expected 1 refresh, got %d", src.calls)
	}
}

// TestUsageCollectorTTLCache proves a second scrape inside the TTL serves the
// cached snapshot without touching the source, and a scrape after the TTL
// refreshes again.
func TestUsageCollectorTTLCache(t *testing.T) {
	src := &stubUsageSource{rows: []db.SumTaskUsageTotalsForMetricsRow{
		usageRow("ws-1", "rt-1", "openai", "gpt-5.5", 10, 0, 0, 0, 0, 1),
	}}
	reg := prometheus.NewRegistry()
	collector := NewUsageCollectorWithTTL(src, time.Minute)
	reg.MustRegister(collector)

	gather := func() {
		t.Helper()
		if _, err := reg.Gather(); err != nil {
			t.Fatal(err)
		}
	}
	gather()
	gather()
	if src.calls != 1 {
		t.Fatalf("second scrape inside TTL refreshed the source: %d calls", src.calls)
	}

	collector.now = func() time.Time { return time.Now().Add(2 * time.Minute) }
	gather()
	if src.calls != 2 {
		t.Fatalf("scrape past TTL did not refresh: %d calls", src.calls)
	}
}

// TestUsageCollectorKeepsSnapshotOnSourceError proves a failing source never
// blanks the exposition: the last good snapshot stays served and the failed
// refresh is not retried until the TTL lapses.
func TestUsageCollectorKeepsSnapshotOnSourceError(t *testing.T) {
	src := &stubUsageSource{rows: []db.SumTaskUsageTotalsForMetricsRow{
		usageRow("ws-1", "rt-1", "openai", "gpt-5.5", 10, 0, 0, 0, 0, 1),
	}}
	reg := prometheus.NewRegistry()
	collector := NewUsageCollectorWithTTL(src, time.Minute)
	reg.MustRegister(collector)

	if _, err := reg.Gather(); err != nil {
		t.Fatal(err)
	}
	src.err = errors.New("db down")
	collector.now = func() time.Time { return time.Now().Add(2 * time.Minute) }
	if _, err := reg.Gather(); err != nil {
		t.Fatal(err)
	}
	if src.calls != 2 {
		t.Fatalf("expected the stale refresh attempt, got %d calls", src.calls)
	}

	want := `
# HELP multica_task_usage_tokens_total Lifetime task usage tokens by token type.
# TYPE multica_task_usage_tokens_total counter
multica_task_usage_tokens_total{model="gpt-5.5",provider="openai",runtime_id="rt-1",token_type="cache_read",workspace_id="ws-1"} 0
multica_task_usage_tokens_total{model="gpt-5.5",provider="openai",runtime_id="rt-1",token_type="cache_write",workspace_id="ws-1"} 0
multica_task_usage_tokens_total{model="gpt-5.5",provider="openai",runtime_id="rt-1",token_type="input",workspace_id="ws-1"} 10
multica_task_usage_tokens_total{model="gpt-5.5",provider="openai",runtime_id="rt-1",token_type="output",workspace_id="ws-1"} 0
`
	if err := promtest.GatherAndCompare(reg, strings.NewReader(want),
		"multica_task_usage_tokens_total"); err != nil {
		t.Fatalf("stale snapshot was not served after source error: %v", err)
	}
	if src.calls != 2 {
		t.Fatalf("failed refresh was retried before TTL: %d calls", src.calls)
	}
}

// TestUsageCollectorUnpricedModelKeepsAuthoritativeCost pins the unpriced
// branch: no rate-table row means no estimate is invented, and the
// provider-reported cost still lands.
func TestUsageCollectorUnpricedModelKeepsAuthoritativeCost(t *testing.T) {
	src := &stubUsageSource{rows: []db.SumTaskUsageTotalsForMetricsRow{
		usageRow("ws-1", "rt-1", "grok", "grok-unlisted", 1000, 0, 0, 0, 123, 1),
	}}
	reg := prometheus.NewRegistry()
	reg.MustRegister(NewUsageCollectorWithTTL(src, time.Hour))

	want := `
# HELP multica_task_usage_cost_usd_total Lifetime task usage cost in USD: provider-reported ticks plus the rate-table estimate over uncosted tokens.
# TYPE multica_task_usage_cost_usd_total counter
multica_task_usage_cost_usd_total{model="grok-unlisted",provider="grok",runtime_id="rt-1",workspace_id="ws-1"} 1.23e-08
`
	if err := promtest.GatherAndCompare(reg, strings.NewReader(want),
		"multica_task_usage_cost_usd_total"); err != nil {
		t.Fatal(err)
	}
}
