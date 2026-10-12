package metrics

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/multica-ai/multica/server/pkg/taskfailure"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// DefaultTaskFailureMetricsTTL bounds how often the collector re-reads the
// lifetime terminal-task aggregate. Scrapes land every few seconds; failures
// move at task granularity, so a couple of minutes of staleness is invisible
// on a rate() chart and keeps the aggregate scan off the scrape path — same
// rationale as DefaultUsageMetricsTTL.
const DefaultTaskFailureMetricsTTL = 2 * time.Minute

// taskFailureSourceTimeout bounds the refresh query so a slow database
// delays the scrape by a bounded amount instead of hanging it.
const taskFailureSourceTimeout = 5 * time.Second

// TaskFailureSource supplies the lifetime per-(workspace, agent, provider,
// model, failure_reason) terminal-task counts. *db.Queries satisfies it; the
// interface exists so tests can pin TTL and error behavior without a
// database.
type TaskFailureSource interface {
	SumTaskFailuresForMetrics(ctx context.Context) ([]db.SumTaskFailuresForMetricsRow, error)
}

// TaskFailureCollector exposes workspace-scoped task failures as Prometheus
// counters aggregated from agent_task_queue terminal rows (RUYI-618):
//
//   - multica_agent_task_failures_total — failed tasks by workspace, agent,
//     provider, model, failure_class and failure_reason. failure_class is
//     the Errors tab's display grouping (taskfailure.DisplayClass), so a
//     PromQL aggregate by class or by raw reason reconciles with the usage
//     page's Errors tab by construction.
//   - multica_agent_task_runs_total — ALL terminal tasks (completed plus
//     failed) by workspace, agent, provider, model: the denominator that
//     makes window failure rates a plain PromQL ratio
//     (increase(failures_total[w]) / increase(runs_total[w])).
//
// Like UsageCollector, the source query returns lifetime sums — valid
// Prometheus counters (monotonically non-decreasing; workspace or agent
// deletion is the only decrease and reads as a counter reset).
type TaskFailureCollector struct {
	src TaskFailureSource
	ttl time.Duration
	now func() time.Time

	failures *prometheus.Desc
	runs     *prometheus.Desc

	mu       sync.Mutex
	snapshot []db.SumTaskFailuresForMetricsRow
	staleAt  time.Time
}

func NewTaskFailureCollector(src TaskFailureSource) *TaskFailureCollector {
	return NewTaskFailureCollectorWithTTL(src, DefaultTaskFailureMetricsTTL)
}

func NewTaskFailureCollectorWithTTL(src TaskFailureSource, ttl time.Duration) *TaskFailureCollector {
	// A non-positive TTL would re-query on every scrape; clamp to the default
	// rather than inherit the misconfiguration.
	if ttl <= 0 {
		ttl = DefaultTaskFailureMetricsTTL
	}
	taskLabels := []string{"workspace_id", "agent_id", "provider", "model"}
	return &TaskFailureCollector{
		src: src,
		ttl: ttl,
		now: time.Now,
		failures: prometheus.NewDesc("multica_agent_task_failures_total",
			"Lifetime failed task count by workspace, agent, model and failure classification.",
			append(append([]string(nil), taskLabels...), "failure_class", "failure_reason"), nil),
		runs: prometheus.NewDesc("multica_agent_task_runs_total",
			"Lifetime terminal task count (completed plus failed) by workspace, agent and model — the denominator behind window failure rates.",
			taskLabels, nil),
	}
}

func (c *TaskFailureCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.failures
	ch <- c.runs
}

func (c *TaskFailureCollector) Collect(ch chan<- prometheus.Metric) {
	// The source groups by failure_reason, so one (workspace, agent,
	// provider, model) can arrive as several rows — one per failure bucket
	// plus the success bucket. failures series map one-to-one onto rows;
	// the runs denominator folds them back together.
	type runsKey struct{ ws, agent, provider, model string }
	runs := make(map[runsKey]float64)
	for _, row := range c.snapshotForCollect() {
		if row.FailureReason != "" {
			ch <- prometheus.MustNewConstMetric(c.failures, prometheus.CounterValue,
				float64(row.FailedCount),
				row.WorkspaceID, row.AgentID, row.Provider, row.Model,
				taskfailure.DisplayClass(taskfailure.Reason(row.FailureReason)),
				row.FailureReason)
		}
		key := runsKey{row.WorkspaceID, row.AgentID, row.Provider, row.Model}
		runs[key] += float64(row.FailedCount + row.CompletedCount)
	}
	for key, total := range runs {
		ch <- prometheus.MustNewConstMetric(c.runs, prometheus.CounterValue, total,
			key.ws, key.agent, key.provider, key.model)
	}
}

// snapshotForCollect serves the cached aggregate, refreshing it when the TTL
// has lapsed. A failed refresh keeps serving the last good snapshot and the
// next attempt waits for a full TTL — a database blip must neither blank the
// exposition nor turn every scrape into a query.
func (c *TaskFailureCollector) snapshotForCollect() []db.SumTaskFailuresForMetricsRow {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.snapshot == nil || !c.now().Before(c.staleAt) {
		ctx, cancel := context.WithTimeout(context.Background(), taskFailureSourceTimeout)
		rows, err := c.src.SumTaskFailuresForMetrics(ctx)
		cancel()
		if err != nil {
			slog.Warn("task failure metrics refresh failed; serving last snapshot", "error", err)
		} else {
			c.snapshot = rows
		}
		c.staleAt = c.now().Add(c.ttl)
	}
	return c.snapshot
}
