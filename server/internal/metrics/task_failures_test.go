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

// stubTaskFailureSource serves canned rows and counts refresh calls so tests
// can pin the TTL behavior without a database — same shape as stubUsageSource.
type stubTaskFailureSource struct {
	rows  []db.SumTaskFailuresForMetricsRow
	err   error
	calls int
}

func (s *stubTaskFailureSource) SumTaskFailuresForMetrics(context.Context) ([]db.SumTaskFailuresForMetricsRow, error) {
	s.calls++
	return s.rows, s.err
}

func failureRow(ws, agent, provider, model, reason string, failed, completed int64) db.SumTaskFailuresForMetricsRow {
	return db.SumTaskFailuresForMetricsRow{
		WorkspaceID: ws, AgentID: agent, Provider: provider, Model: model,
		FailureReason: reason, FailedCount: failed, CompletedCount: completed,
	}
}

// TestTaskFailureCollectorExposesAggregates pins the full exposition
// (RUYI-618): every failed bucket becomes a failures series carrying both the
// raw reason and its display class, the success bucket feeds only the runs
// denominator, and a task with no usage row (failed before any provider call)
// keeps empty provider/model labels instead of being dropped.
func TestTaskFailureCollectorExposesAggregates(t *testing.T) {
	src := &stubTaskFailureSource{rows: []db.SumTaskFailuresForMetricsRow{
		failureRow("ws-1", "agent-1", "openai", "gpt-5.5", "agent_error.provider_server_error", 3, 0),
		// The success bucket of the same (workspace, agent, provider, model):
		// excluded from failures_total, counted in runs_total.
		failureRow("ws-1", "agent-1", "openai", "gpt-5.5", "", 0, 7),
		failureRow("ws-1", "agent-2", "", "", "queued_expired", 2, 0),
		// A failed row the SQL already collapsed into 'unclassified' (the
		// ListDashboardFailuresDaily convention): SumTaskFailuresForMetrics
		// never delivers a FAILED row with an empty reason — empty means the
		// success bucket, as row 2 shows.
		failureRow("ws-2", "agent-1", "anthropic", "claude-x", "unclassified", 1, 4),
	}}
	reg := prometheus.NewRegistry()
	reg.MustRegister(NewTaskFailureCollectorWithTTL(src, time.Hour))

	want := `
# HELP multica_agent_task_failures_total Lifetime failed task count by workspace, agent, model and failure classification.
# TYPE multica_agent_task_failures_total counter
multica_agent_task_failures_total{agent_id="agent-1",failure_class="provider",failure_reason="agent_error.provider_server_error",model="gpt-5.5",provider="openai",workspace_id="ws-1"} 3
multica_agent_task_failures_total{agent_id="agent-1",failure_class="other",failure_reason="unclassified",model="claude-x",provider="anthropic",workspace_id="ws-2"} 1
multica_agent_task_failures_total{agent_id="agent-2",failure_class="runtime",failure_reason="queued_expired",model="",provider="",workspace_id="ws-1"} 2
# HELP multica_agent_task_runs_total Lifetime terminal task count (completed plus failed) by workspace, agent and model — the denominator behind window failure rates.
# TYPE multica_agent_task_runs_total counter
multica_agent_task_runs_total{agent_id="agent-1",model="gpt-5.5",provider="openai",workspace_id="ws-1"} 10
multica_agent_task_runs_total{agent_id="agent-1",model="claude-x",provider="anthropic",workspace_id="ws-2"} 5
multica_agent_task_runs_total{agent_id="agent-2",model="",provider="",workspace_id="ws-1"} 2
`
	if err := promtest.GatherAndCompare(reg, strings.NewReader(want),
		"multica_agent_task_failures_total", "multica_agent_task_runs_total"); err != nil {
		t.Fatal(err)
	}
	if src.calls != 1 {
		t.Fatalf("expected 1 refresh, got %d", src.calls)
	}
}

// TestTaskFailureCollectorTTLCache proves a second scrape inside the TTL
// serves the cached snapshot without touching the source, and a scrape after
// the TTL refreshes again.
func TestTaskFailureCollectorTTLCache(t *testing.T) {
	src := &stubTaskFailureSource{rows: []db.SumTaskFailuresForMetricsRow{
		failureRow("ws-1", "agent-1", "openai", "gpt-5.5", "timeout", 1, 0),
	}}
	reg := prometheus.NewRegistry()
	collector := NewTaskFailureCollectorWithTTL(src, time.Minute)
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

// TestTaskFailureCollectorKeepsSnapshotOnSourceError proves a failing source
// never blanks the exposition: the last good snapshot stays served and the
// failed refresh is not retried until the TTL lapses.
func TestTaskFailureCollectorKeepsSnapshotOnSourceError(t *testing.T) {
	src := &stubTaskFailureSource{rows: []db.SumTaskFailuresForMetricsRow{
		failureRow("ws-1", "agent-1", "openai", "gpt-5.5", "agent_error.process_failure", 2, 3),
	}}
	reg := prometheus.NewRegistry()
	collector := NewTaskFailureCollectorWithTTL(src, time.Minute)
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
# HELP multica_agent_task_failures_total Lifetime failed task count by workspace, agent, model and failure classification.
# TYPE multica_agent_task_failures_total counter
multica_agent_task_failures_total{agent_id="agent-1",failure_class="agent",failure_reason="agent_error.process_failure",model="gpt-5.5",provider="openai",workspace_id="ws-1"} 2
# HELP multica_agent_task_runs_total Lifetime terminal task count (completed plus failed) by workspace, agent and model — the denominator behind window failure rates.
# TYPE multica_agent_task_runs_total counter
multica_agent_task_runs_total{agent_id="agent-1",model="gpt-5.5",provider="openai",workspace_id="ws-1"} 5
`
	if err := promtest.GatherAndCompare(reg, strings.NewReader(want),
		"multica_agent_task_failures_total", "multica_agent_task_runs_total"); err != nil {
		t.Fatalf("stale snapshot was not served after source error: %v", err)
	}
	if src.calls != 2 {
		t.Fatalf("failed refresh was retried before TTL: %d calls", src.calls)
	}
}
