package daemon

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/agent"
	"github.com/multica-ai/multica/server/pkg/taskbudget"
)

func TestBudgetFakeBackendStopsAndCostOnly(t *testing.T) {
	for _, status := range []string{"max_turns", "context_budget"} {
		t.Run(status, func(t *testing.T) {
			d := newTestDaemon(t)
			fb := &fakeBackend{results: []agent.Result{{Status: status, SessionID: "partial", Usage: map[string]agent.TokenUsage{"m": {CostUSDTicks: 42}}, Turns: 3, MaxContextTokens: 100}}}
			opts := agent.ExecOptions{Timeout: (taskbudget.Profile{PerRunTimeoutSeconds: 7}).Timeout(time.Hour)}
			got, _, err := d.executeAndDrain(context.Background(), fb, "prompt", opts, slog.Default(), "task", "", "", new(atomic.Int32))
			if err != nil {
				t.Fatal(err)
			}
			if reason := executionBudgetStopReason(got.Status); reason != status {
				t.Fatalf("reason=%q want %q", reason, status)
			}
			entries := taskUsageEntries("fake", got)
			if len(entries) != 1 || entries[0].CostUSDTicks != 42 || entries[0].Turns != 3 {
				t.Fatalf("lost cost or stats: %+v", entries)
			}
			if fb.calls[0].Timeout != 7*time.Second || got.SessionID != "partial" {
				t.Fatal("timeout or recovery session lost")
			}
		})
	}
}

func TestBudgetFreshRetryStatistics(t *testing.T) {
	for _, retryStatus := range []string{"completed", "failed"} {
		t.Run(retryStatus, func(t *testing.T) {
			d := newTestDaemon(t)
			fb := &fakeBackend{results: []agent.Result{
				{Status: "failed", SessionID: "old", Turns: 2, Compactions: 1, MaxContextTokens: 200, Usage: map[string]agent.TokenUsage{"m": {InputTokens: 10, CostUSDTicks: 5}}},
				{Status: retryStatus, Turns: 3, Compactions: 2, MaxContextTokens: 150, Usage: map[string]agent.TokenUsage{"m": {OutputTokens: 4, CostUSDTicks: 7}}},
			}}
			first, tools, err := d.executeAndDrain(context.Background(), fb, "p", agent.ExecOptions{}, slog.Default(), "task", "", "", new(atomic.Int32))
			if err != nil {
				t.Fatal(err)
			}
			retry, retryTools, err := d.executeAndDrain(context.Background(), fb, "p", agent.ExecOptions{}, slog.Default(), "task", "", "", new(atomic.Int32))
			if err != nil {
				t.Fatal(err)
			}
			got, _ := reconcileFreshRetryResult(first, first.Usage, tools, retry, retryTools, nil)
			if got.Turns != 5 || got.Compactions != 3 || got.MaxContextTokens != 200 || got.Usage["m"].CostUSDTicks != 12 || got.Usage["m"].InputTokens != 10 || got.Usage["m"].OutputTokens != 4 {
				t.Fatalf("incorrect cumulative stats: %+v", got)
			}
		})
	}
	first := agent.Result{Turns: 2, Compactions: 1, MaxContextTokens: 90}
	retry := agent.Result{Turns: 1, Compactions: 1, MaxContextTokens: 100, Usage: map[string]agent.TokenUsage{"m": {CostUSDTicks: 2}}}
	got, _ := reconcileFreshRetryResult(first, nil, 0, retry, 0, context.DeadlineExceeded)
	if got.Turns != 3 || got.MaxContextTokens != 100 || got.Usage["m"].CostUSDTicks != 2 {
		t.Fatalf("startup-error partial usage lost: %+v", got)
	}
}

func TestBudgetUsageStatisticsWithoutModel(t *testing.T) {
	entries := taskUsageEntries("fake", agent.Result{Turns: 2, Compactions: 1, MaxContextTokens: 8})
	if len(entries) != 1 || entries[0].Turns != 2 {
		t.Fatalf("lost stats: %+v", entries)
	}
	if entries := taskUsageEntries("fake", agent.Result{Usage: map[string]agent.TokenUsage{"m": {CostUSDTicks: 4}}}); len(entries) != 1 {
		t.Fatalf("lost cost-only usage: %+v", entries)
	}
}
