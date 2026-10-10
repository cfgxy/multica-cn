package handler

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	promtest "github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/multica-ai/multica/server/internal/hostmetrics"
	"github.com/multica-ai/multica/server/internal/testutil"
)

// resourceReportBody mirrors the wire shape daemon builds for the heartbeat
// "resources" field (protocol.DaemonResourceReport).
func resourceReportBody() map[string]any {
	return map[string]any{
		"collected_at":        time.Now().UTC().Format(time.RFC3339),
		"cpu_mode_seconds":    map[string]any{"idle": 123.4, "user": 5.6},
		"mem_total_bytes":     17179869184,
		"mem_available_bytes": 8589934592,
		"swap_total_bytes":    2147483648,
		"swap_free_bytes":     1073741824,
		"filesystems": []any{map[string]any{
			"device": "/dev/sda1", "mountpoint": "/", "fs_type": "ext4",
			"size_bytes": 990, "avail_bytes": 495,
		}},
		"disk_read_bytes_total":    11,
		"disk_written_bytes_total": 22,
	}
}

// relayedMemoryGauge is the exposition excerpt the relay must produce for the
// fixture daemon: last write wins per daemon, and nothing else is registered.
func relayedMemoryGauge(daemonID string) string {
	return `
# HELP multica_daemon_memory_total_bytes Host memory total in bytes (node_memory_MemTotal_bytes).
# TYPE multica_daemon_memory_total_bytes gauge
multica_daemon_memory_total_bytes{daemon="` + daemonID + `"} 1.7179869184e+10
`
}

// TestDaemonHeartbeatHTTP_ResourceRelay proves the HTTP heartbeat persists a
// resource snapshot into the exposition store keyed by the runtime's daemon,
// and that a legacy heartbeat without the field keeps the last snapshot
// instead of clobbering it (RUYI-618).
func TestDaemonHeartbeatHTTP_ResourceRelay(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	runtimeID := dbfx.Runtime(t, "HR relay rt", testutil.Cols{
		"daemon_id":   "hr-relay-daemon",
		"device_info": "host resource relay fixture",
	})

	store := hostmetrics.NewStore(hostmetrics.DefaultSnapshotTTL)
	registry := prometheus.NewRegistry()
	registry.MustRegister(hostmetrics.NewCollector(store))
	prev := testHandler.HostResources
	testHandler.HostResources = store
	t.Cleanup(func() { testHandler.HostResources = prev })

	beat := func(body map[string]any) {
		t.Helper()
		req := newDaemonTokenRequest(http.MethodPost, "/api/daemon/heartbeat", body,
			testWorkspaceID, "hr-relay-daemon")
		testutil.Call(t, testHandler.DaemonHeartbeat, req).Want(http.StatusOK)
	}

	beat(map[string]any{"runtime_id": runtimeID, "resources": resourceReportBody()})
	want := relayedMemoryGauge("hr-relay-daemon")
	if err := promtest.GatherAndCompare(registry, strings.NewReader(want),
		"multica_daemon_memory_total_bytes"); err != nil {
		t.Fatalf("relay did not expose the snapshot: %v", err)
	}

	// A legacy heartbeat (no resources field) must keep the last snapshot.
	beat(map[string]any{"runtime_id": runtimeID})
	if err := promtest.GatherAndCompare(registry, strings.NewReader(want),
		"multica_daemon_memory_total_bytes"); err != nil {
		t.Fatalf("legacy heartbeat clobbered the snapshot: %v", err)
	}
}
