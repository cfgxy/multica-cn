package hostmetrics

import (
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func testReport(collectedAt string) *protocol.DaemonResourceReport {
	return &protocol.DaemonResourceReport{
		CollectedAt: collectedAt,
		CPUModeSeconds: map[string]float64{
			"idle": 100.5, "user": 20.25,
		},
		MemTotalBytes:     17179869184,
		MemAvailableBytes: 6442450636.8,
		SwapTotalBytes:    4294967296,
		SwapFreeBytes:     2147483648,
		Filesystems: []protocol.DaemonFilesystemSample{
			{Device: "/dev/sda1", Mountpoint: "/", FSType: "ext4", SizeBytes: 108447358976, AvailBytes: 41234567890.1},
		},
		DiskReadBytesTotal:    2222000000,
		DiskWrittenBytesTotal: 3333000000,
	}
}

func fixedClock(at time.Time) func() time.Time {
	return func() time.Time { return at }
}

func TestCollectorExposesRelayedSeries(t *testing.T) {
	now := time.Date(2026, 10, 10, 8, 0, 0, 0, time.UTC)
	store := NewStore(DefaultSnapshotTTL)
	store.now = fixedClock(now)
	store.RecordHostResources("daemon-1", testReport("2026-10-10T07:59:45Z"))
	store.RecordHostResources("daemon-1", testReport("2026-10-10T07:59:45Z")) // last write wins, idempotent

	reg := prometheus.NewPedanticRegistry()
	reg.MustRegister(NewCollector(store))

	want := `
# HELP multica_daemon_cpu_seconds_total Cumulative CPU-seconds per mode summed across cores, relayed from the daemon host's node-exporter.
# TYPE multica_daemon_cpu_seconds_total counter
multica_daemon_cpu_seconds_total{daemon="daemon-1",mode="idle"} 100.5
multica_daemon_cpu_seconds_total{daemon="daemon-1",mode="user"} 20.25
# HELP multica_daemon_disk_read_bytes_total Cumulative bytes read across real block devices (node_disk_read_bytes_total).
# TYPE multica_daemon_disk_read_bytes_total counter
multica_daemon_disk_read_bytes_total{daemon="daemon-1"} 2.222e+09
# HELP multica_daemon_disk_written_bytes_total Cumulative bytes written across real block devices (node_disk_written_bytes_total).
# TYPE multica_daemon_disk_written_bytes_total counter
multica_daemon_disk_written_bytes_total{daemon="daemon-1"} 3.333e+09
# HELP multica_daemon_filesystem_avail_bytes Filesystem space available to non-root users in bytes (node_filesystem_avail_bytes).
# TYPE multica_daemon_filesystem_avail_bytes gauge
multica_daemon_filesystem_avail_bytes{daemon="daemon-1",device="/dev/sda1",fstype="ext4",mountpoint="/"} 4.12345678901e+10
# HELP multica_daemon_filesystem_size_bytes Filesystem size in bytes for real writable mounts (node_filesystem_size_bytes).
# TYPE multica_daemon_filesystem_size_bytes gauge
multica_daemon_filesystem_size_bytes{daemon="daemon-1",device="/dev/sda1",fstype="ext4",mountpoint="/"} 1.08447358976e+11
# HELP multica_daemon_memory_available_bytes Host memory available in bytes (node_memory_MemAvailable_bytes).
# TYPE multica_daemon_memory_available_bytes gauge
multica_daemon_memory_available_bytes{daemon="daemon-1"} 6.4424506368e+09
# HELP multica_daemon_memory_total_bytes Host memory total in bytes (node_memory_MemTotal_bytes).
# TYPE multica_daemon_memory_total_bytes gauge
multica_daemon_memory_total_bytes{daemon="daemon-1"} 1.7179869184e+10
# HELP multica_daemon_resource_scrape_age_seconds Seconds since the daemon's last resource snapshot was collected; grows while a daemon is offline.
# TYPE multica_daemon_resource_scrape_age_seconds gauge
multica_daemon_resource_scrape_age_seconds{daemon="daemon-1"} 15
# HELP multica_daemon_swap_free_bytes Host swap free in bytes; 0 when absent.
# TYPE multica_daemon_swap_free_bytes gauge
multica_daemon_swap_free_bytes{daemon="daemon-1"} 2.147483648e+09
# HELP multica_daemon_swap_total_bytes Host swap total in bytes; 0 when absent.
# TYPE multica_daemon_swap_total_bytes gauge
multica_daemon_swap_total_bytes{daemon="daemon-1"} 4.294967296e+09
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(want)); err != nil {
		t.Fatal(err)
	}
}

func TestStoreTTLExpiresStaleDaemons(t *testing.T) {
	t0 := time.Date(2026, 10, 10, 8, 0, 0, 0, time.UTC)
	clock := t0
	store := NewStore(time.Minute)
	store.now = func() time.Time { return clock }

	store.RecordHostResources("older", testReport("2026-10-10T07:59:45Z"))
	clock = clock.Add(50 * time.Second)
	store.RecordHostResources("recent", testReport("2026-10-10T07:59:45Z"))
	clock = clock.Add(20 * time.Second) // "older" is 70s past recording, "recent" only 20s

	if _, ok := store.populated()["older"]; ok {
		t.Fatal("daemon snapshot past the TTL must age out of the exposition")
	}
	if _, ok := store.populated()["recent"]; !ok {
		t.Fatal("daemon snapshot inside the TTL must survive")
	}
}

func TestStoreIgnoresEmptyInput(t *testing.T) {
	store := NewStore(DefaultSnapshotTTL)
	store.RecordHostResources("", testReport("2026-10-10T07:59:45Z"))
	store.RecordHostResources("daemon-1", nil)
	if got := len(store.populated()); got != 0 {
		t.Fatalf("populated = %d entries, want 0", got)
	}
}
