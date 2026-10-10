package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Host resource relay (RUYI-618): instead of sampling /proc itself for the
// observability-only host series, the daemon lifts values from a co-located
// node-exporter's /metrics exposition (Owner direction: reuse node-exporter's
// existing capability, integrate on the daemon deployment side). The relay is
// telemetry only — the backpressure gate (RUYI-393) keeps reading /proc
// directly and its semantics are untouched.

// DefaultHostResourceSampleInterval is how often the relay re-samples
// node-exporter. Heartbeats attach the latest snapshot, so the effective
// reporting latency is min(this, HeartbeatInterval).
const DefaultHostResourceSampleInterval = 15 * time.Second

// hostResourceClientTimeout bounds one node-exporter scrape. The relay must
// never hold a heartbeat goroutine: heartbeats read the latest snapshot
// without I/O, and a dead exporter just leaves the previous snapshot in place.
const hostResourceClientTimeout = 5 * time.Second

// filesystemFSTypeAllowlist limits the relayed per-mount capacity to real,
// writable, locally-mounted filesystems. node-exporter's own default is a
// blocklist, but the relay's payload rides heartbeats — an allowlist keeps
// tmpfs/overlay/squashfs noise out of every beat.
var filesystemFSTypeAllowlist = map[string]bool{
	"ext2": true, "ext3": true, "ext4": true,
	"xfs": true, "btrfs": true, "zfs": true, "f2fs": true,
	"jfs": true, "reiserfs": true,
	"exfat": true, "ntfs": true, "ntfs3": true,
	"vfat": true, "fat": true, "fuseblk": true,
}

// blockDeviceExcludePattern matches device names whose diskstats duplicate
// real disk traffic (device-mapper and MD volumes sit on top of sd*/nvme*,
// loop/ram/fd/sr/zram/nbd are not data disks). Mirrors node-exporter's
// default diskstats filtering.
var blockDeviceExcludePattern = regexp.MustCompile(`^(loop|ram|fd|nbd|zram|dm-|md|sr)`)

// hostResourceWarnInterval throttles scrape-failure warnings; a dead exporter
// would otherwise log one line per sample forever. The first failure still
// warns immediately so a misconfigured URL is visible right away.
const hostResourceWarnInterval = time.Minute

// runHostResourceWatcher samples node-exporter on the configured interval and
// publishes the latest snapshot for heartbeat attachment. A failed sample
// keeps the previous snapshot (telemetry degrades, it never lies), and
// context cancellation is the only exit.
func (d *Daemon) runHostResourceWatcher(ctx context.Context) {
	interval := d.cfg.HostResourceSampleInterval
	if interval <= 0 {
		interval = DefaultHostResourceSampleInterval
	}
	d.hostResourceTick(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.hostResourceTick(ctx)
		}
	}
}

func (d *Daemon) hostResourceTick(ctx context.Context) {
	rep, err := d.hostSampler.Sample(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		if last := d.hostLastWarn.Load(); time.Since(time.Unix(0, last)) >= hostResourceWarnInterval {
			d.hostLastWarn.Store(time.Now().UnixNano())
			d.logger.Warn("node-exporter resource scrape failed; keeping previous host snapshot",
				"error", err, "url", d.cfg.NodeExporterURL)
		}
		return
	}
	d.hostResources.Store(rep)
}

// hostResourceReport returns the latest node-exporter snapshot, or nil before
// the first successful sample or when the relay is disabled.
func (d *Daemon) hostResourceReport() *protocol.DaemonResourceReport {
	return d.hostResources.Load()
}

// HostResourceSampler scrapes a co-located node-exporter and builds the
// DaemonResourceReport relayed with runtime heartbeats. Safe for concurrent
// use; Sample never mutates shared state.
type HostResourceSampler struct {
	url    string
	client *http.Client
	now    func() time.Time
}

// fsKey identifies one node_filesystem_* series by its full label set —
// matching size/avail/readonly series must agree on every label, not just
// device+mountpoint, so an fstype collision cannot cross-wire values.
type fsKey struct {
	device, mountpoint, fstype, readonly string
}

// NewHostResourceSampler returns a sampler for the node-exporter exposition
// at url (e.g. http://127.0.0.1:9100/metrics).
func NewHostResourceSampler(url string) *HostResourceSampler {
	return &HostResourceSampler{
		url:    url,
		client: &http.Client{Timeout: hostResourceClientTimeout},
		now:    time.Now,
	}
}

// Sample fetches and reduces one node-exporter exposition into a
// DaemonResourceReport. It errors when the endpoint is unreachable or does
// not look like a node-exporter (no CPU seconds) so a misconfigured URL
// surfaces in logs instead of silently reporting empty hosts.
func (s *HostResourceSampler) Sample(ctx context.Context) (*protocol.DaemonResourceReport, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url, nil)
	if err != nil {
		return nil, fmt.Errorf("build node-exporter request: %w", err)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("scrape node-exporter: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, resp.Body) //nolint:errcheck
		return nil, fmt.Errorf("scrape node-exporter: status %d", resp.StatusCode)
	}

	parser := expfmt.NewTextParser(model.LegacyValidation)
	families, err := parser.TextToMetricFamilies(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("parse node-exporter exposition: %w", err)
	}

	report := &protocol.DaemonResourceReport{
		CollectedAt: s.now().UTC().Format(time.RFC3339),
	}

	if cpu := families["node_cpu_seconds_total"]; cpu != nil {
		report.CPUModeSeconds = sumByLabel(cpu, "mode")
	}
	if v, ok := gaugeValue(families, "node_memory_MemTotal_bytes"); ok {
		report.MemTotalBytes = v
	}
	if v, ok := gaugeValue(families, "node_memory_MemAvailable_bytes"); ok {
		report.MemAvailableBytes = v
	}
	if v, ok := gaugeValue(families, "node_memory_SwapTotal_bytes"); ok {
		report.SwapTotalBytes = v
	}
	if v, ok := gaugeValue(families, "node_memory_SwapFree_bytes"); ok {
		report.SwapFreeBytes = v
	}
	report.Filesystems = filesystemSamples(families)
	if read, written, ok := diskIOSamples(families); ok {
		report.DiskReadBytesTotal = read
		report.DiskWrittenBytesTotal = written
	}

	if len(report.CPUModeSeconds) == 0 {
		return nil, errors.New("node-exporter exposition has no node_cpu_seconds_total; is the URL a node-exporter?")
	}
	return report, nil
}

// gaugeValue returns the value of a label-less gauge family.
func gaugeValue(families map[string]*dto.MetricFamily, name string) (float64, bool) {
	f := families[name]
	if f == nil || len(f.Metric) == 0 {
		return 0, false
	}
	m := f.Metric[0]
	switch {
	case m.GetGauge() != nil:
		return m.GetGauge().GetValue(), true
	case m.GetUntyped() != nil:
		return m.GetUntyped().GetValue(), true
	default:
		return 0, false
	}
}

// sumByLabel sums a family's series per label value, dropping the per-core /
// per-device detail the heartbeat payload never carries.
func sumByLabel(f *dto.MetricFamily, label string) map[string]float64 {
	if f == nil {
		return nil
	}
	out := make(map[string]float64)
	for _, m := range f.Metric {
		v := metricValue(m)
		for _, l := range m.GetLabel() {
			if l.GetName() == label {
				out[l.GetValue()] += v
				break
			}
		}
	}
	return out
}

func metricValue(m *dto.Metric) float64 {
	switch {
	case m.GetCounter() != nil:
		return m.GetCounter().GetValue()
	case m.GetGauge() != nil:
		return m.GetGauge().GetValue()
	case m.GetUntyped() != nil:
		return m.GetUntyped().GetValue()
	default:
		return 0
	}
}

// labelMap builds the label lookup for one series.
func labelMap(m *dto.Metric) map[string]string {
	out := make(map[string]string, len(m.GetLabel()))
	for _, l := range m.GetLabel() {
		out[l.GetName()] = l.GetValue()
	}
	return out
}

// filesystemSamples pairs node_filesystem_size_bytes / _avail_bytes into
// per-mount samples for real, writable filesystems only.
func filesystemSamples(families map[string]*dto.MetricFamily) []protocol.DaemonFilesystemSample {
	size := filesystemByLabels(families["node_filesystem_size_bytes"])
	if len(size) == 0 {
		return nil
	}
	avail := filesystemByLabels(families["node_filesystem_avail_bytes"])
	readonly := filesystemByLabels(families["node_filesystem_readonly"])

	var out []protocol.DaemonFilesystemSample
	for key, sizeVal := range size {
		if readonly[key] > 0 {
			continue
		}
		availVal, ok := avail[key]
		if !ok {
			continue
		}
		if !filesystemFSTypeAllowlist[key.fstype] {
			continue
		}
		out = append(out, protocol.DaemonFilesystemSample{
			Device:     key.device,
			Mountpoint: key.mountpoint,
			FSType:     key.fstype,
			SizeBytes:  sizeVal,
			AvailBytes: availVal,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Mountpoint < out[j].Mountpoint
	})
	return out
}

// filesystemByLabels flattens a filesystem family into value-by-label-set.
func filesystemByLabels(f *dto.MetricFamily) map[fsKey]float64 {
	if f == nil {
		return nil
	}
	out := make(map[fsKey]float64, len(f.Metric))
	for _, m := range f.Metric {
		labels := labelMap(m)
		if len(labels) == 0 {
			continue
		}
		out[fsKey{
			device:     labels["device"],
			mountpoint: labels["mountpoint"],
			fstype:     labels["fstype"],
			readonly:   labels["readonly"],
		}] = metricValue(m)
	}
	return out
}

// diskIOSamples sums node_disk_read/written_bytes_total across real block
// devices. ok is false when the families are absent entirely so a
// diskstats-less exporter does not report silent zeros.
func diskIOSamples(families map[string]*dto.MetricFamily) (read, written float64, ok bool) {
	readF := families["node_disk_read_bytes_total"]
	writtenF := families["node_disk_written_bytes_total"]
	if readF == nil && writtenF == nil {
		return 0, 0, false
	}
	for _, m := range readF.GetMetric() {
		if blockDeviceExcludePattern.MatchString(labelMap(m)["device"]) {
			continue
		}
		read += metricValue(m)
	}
	for _, m := range writtenF.GetMetric() {
		if blockDeviceExcludePattern.MatchString(labelMap(m)["device"]) {
			continue
		}
		written += metricValue(m)
	}
	return read, written, true
}
