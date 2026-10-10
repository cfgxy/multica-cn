package daemon

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

// serveNodeExporterFixture serves testdata/node-exporter-sample.txt, the
// fixture analog of a real node-exporter exposition: per-core CPU counters,
// memory/swap gauges, filesystem families mixing real mounts with tmpfs /
// overlay / read-only noise, and diskstats mixing real devices with loop0.
func serveNodeExporterFixture(t *testing.T) *httptest.Server {
	t.Helper()
	data, err := os.ReadFile("testdata/node-exporter-sample.txt")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = w.Write(data)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestHostResourceSamplerSample(t *testing.T) {
	srv := serveNodeExporterFixture(t)
	s := NewHostResourceSampler(srv.URL)

	rep, err := s.Sample(context.Background())
	if err != nil {
		t.Fatalf("Sample: %v", err)
	}
	if rep.CollectedAt == "" {
		t.Fatal("CollectedAt must be set")
	}

	// CPU: per-core counters must be summed per mode across cores.
	wantCPU := map[string]float64{
		"idle": 3073.5, // 1024.5 + 2049
		"user": 322.5,  // 107.5 + 215
		"iowait": 9.75, // 3.25 + 6.5
		"system": 194.25,
		"steal":  0.25,
	}
	if len(rep.CPUModeSeconds) != len(wantCPU) {
		t.Fatalf("CPU modes = %v, want %d modes", rep.CPUModeSeconds, len(wantCPU))
	}
	for mode, want := range wantCPU {
		if got := rep.CPUModeSeconds[mode]; math.Abs(got-want) > 1e-9 {
			t.Errorf("cpu[%s] = %v, want %v", mode, got, want)
		}
	}

	if rep.MemTotalBytes != 1.7179869184e+10 {
		t.Errorf("MemTotalBytes = %v", rep.MemTotalBytes)
	}
	if rep.MemAvailableBytes != 6.4424506368e+09 {
		t.Errorf("MemAvailableBytes = %v", rep.MemAvailableBytes)
	}
	if rep.SwapTotalBytes != 4.294967296e+09 || rep.SwapFreeBytes != 2.147483648e+09 {
		t.Errorf("swap = %v/%v", rep.SwapTotalBytes, rep.SwapFreeBytes)
	}

	// Filesystems: only real, writable, allowlisted fstypes survive. /run
	// (tmpfs), /data (readonly=1) and the overlay mount are all filtered.
	wantMounts := []string{"/", "/boot/efi"}
	if len(rep.Filesystems) != len(wantMounts) {
		t.Fatalf("filesystems = %+v, want mounts %v", rep.Filesystems, wantMounts)
	}
	for i, want := range wantMounts {
		if rep.Filesystems[i].Mountpoint != want {
			t.Errorf("filesystems[%d].Mountpoint = %q, want %q", i, rep.Filesystems[i].Mountpoint, want)
		}
	}
	root := rep.Filesystems[0]
	if root.Device != "/dev/sda1" || root.FSType != "ext4" {
		t.Errorf("root sample identity = %+v", root)
	}
	if root.SizeBytes != 1.08447358976e+11 || root.AvailBytes != 4.12345678901e+10 {
		t.Errorf("root sample capacity = %+v", root)
	}

	// Disk IO: loop0 excluded; sda + sdb + nvme0n1 summed per direction.
	if want, got := 2.222e+09+1.111e+09+5.555e+08, rep.DiskReadBytesTotal; got != want {
		t.Errorf("DiskReadBytesTotal = %v, want %v", got, want)
	}
	if want, got := 3.333e+09+6.666e+08+4.444e+08, rep.DiskWrittenBytesTotal; got != want {
		t.Errorf("DiskWrittenBytesTotal = %v, want %v", got, want)
	}
}

func TestHostResourceSamplerRejectsNonNodeExporter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("# HELP some_other_metric Just noise.\n# TYPE some_other_metric gauge\nsome_other_metric 1\n"))
	}))
	defer srv.Close()

	if _, err := NewHostResourceSampler(srv.URL).Sample(context.Background()); err == nil {
		t.Fatal("expected error for exposition without node_cpu_seconds_total")
	}
}

func TestHostResourceSamplerStatusError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	if _, err := NewResourceSamplerAt(srv.URL).Sample(context.Background()); err == nil {
		t.Fatal("expected error for non-200 scrape")
	}
}

// NewResourceSamplerAt is a test hook alias keeping the constructor surface
// single; it exists so the status-error test does not duplicate setup.
func NewResourceSamplerAt(url string) *HostResourceSampler { return NewHostResourceSampler(url) }

func TestFilesystemFilterRules(t *testing.T) {
	// The allowlist + readonly filters are the payload's noise gate; pin them
	// so an over-broad edit cannot quietly put tmpfs/overlay back on the wire.
	for _, fstype := range []string{"tmpfs", "devtmpfs", "overlay", "squashfs", "proc", "sysfs", "cgroup2", "ramfs"} {
		if filesystemFSTypeAllowlist[fstype] {
			t.Errorf("fstype %q must not be relayed", fstype)
		}
	}
	for _, device := range []string{"loop0", "ram3", "fd0", "sr0", "dm-2", "md127", "nbd1", "zram0"} {
		if !blockDeviceExcludePattern.MatchString(device) {
			t.Errorf("device %q must be excluded from disk IO sums", device)
		}
	}
	for _, device := range []string{"sda", "sda1", "nvme0n1", "nvme0n1p1", "vdb", "xvdf", "vd1"} {
		if blockDeviceExcludePattern.MatchString(device) {
			t.Errorf("device %q is a real data disk and must not be excluded", device)
		}
	}
}

// TestDaemonResourceReportWireShape pins the JSON contract consumed by the
// server heartbeat handler (both transports share it via the protocol type).
func TestDaemonResourceReportWireShape(t *testing.T) {
	rep := &protocol.DaemonResourceReport{
		CollectedAt:        "2026-10-10T00:00:00Z",
		CPUModeSeconds:     map[string]float64{"idle": 1},
		MemTotalBytes:      2,
		Filesystems:        []protocol.DaemonFilesystemSample{{Device: "/dev/sda1", Mountpoint: "/", FSType: "ext4", SizeBytes: 3, AvailBytes: 4}},
		DiskReadBytesTotal: 5,
	}
	b, err := json.Marshal(rep)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(b)
	for _, key := range []string{`"collected_at"`, `"cpu_mode_seconds"`, `"mem_total_bytes"`, `"filesystems"`, `"disk_read_bytes_total"`, `"mountpoint"`, `"avail_bytes"`} {
		if !strings.Contains(s, key) {
			t.Errorf("wire JSON missing %s: %s", key, s)
		}
	}
}
