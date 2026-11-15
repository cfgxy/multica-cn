package daemon

import (
	"context"
	"net/http"
	"os"
	"sync/atomic"
	"testing"
)

func TestUsageOutboxPreservesFailedReportAndReplaysAfterRestart(t *testing.T) {
	var fail atomic.Bool
	fail.Store(true)
	var reports atomic.Int32
	d := newGCTestDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reports.Add(1)
		if fail.Load() {
			http.Error(w, "temporary write failure", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	usage := []TaskUsageEntry{{Provider: "fake", Model: "model-a", InputTokens: 2, CostUSDTicks: 10}}
	if err := d.reportUsageDurably(context.Background(), "task-a", usage); err == nil {
		t.Fatal("failed usage report was accepted")
	}
	path := usageOutboxPath(d.cfg.WorkspacesRoot, "task-a")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("failed report was lost: %v", err)
	}
	fail.Store(false)
	restarted := New(d.cfg, d.logger)
	restarted.client = d.client
	restarted.flushUsageOutbox(context.Background())
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("successful replay not acknowledged: %v", err)
	}
	if reports.Load() != 2 {
		t.Fatalf("reports = %d, want initial plus replay", reports.Load())
	}
}
