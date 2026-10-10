package hostmetrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Collector adapts Store to prometheus.Collector. Series are named after the
// node-exporter originals they relay, prefixed with multica_daemon_ so the
// server's exposition stays a single scrape target that still reads like the
// node-exporter operators already know:
//
//	multica_daemon_cpu_seconds_total{daemon,mode}
//	multica_daemon_memory_total_bytes / _available_bytes{daemon}
//	multica_daemon_swap_total_bytes / _free_bytes{daemon}
//	multica_daemon_filesystem_{size,avail}_bytes{daemon,device,mountpoint,fstype}
//	multica_daemon_disk_{read,written}_bytes_total{daemon}
//	multica_daemon_resource_scrape_age_seconds{daemon}
//
// CPU utilization / IO rates are consumers' job (rate() in PromQL); the
// exposition relays counter values verbatim.
type Collector struct {
	store *Store

	cpuSecondsTotal *prometheus.Desc
	memTotal        *prometheus.Desc
	memAvailable    *prometheus.Desc
	swapTotal       *prometheus.Desc
	swapFree        *prometheus.Desc
	fsSize          *prometheus.Desc
	fsAvail         *prometheus.Desc
	diskRead        *prometheus.Desc
	diskWritten     *prometheus.Desc
	scrapeAge       *prometheus.Desc
}

// NewCollector returns the collector reading from store.
func NewCollector(store *Store) *Collector {
	daemon := []string{"daemon"}
	return &Collector{
		store: store,
		cpuSecondsTotal: prometheus.NewDesc("multica_daemon_cpu_seconds_total",
			"Cumulative CPU-seconds per mode summed across cores, relayed from the daemon host's node-exporter.",
			[]string{"daemon", "mode"}, nil),
		memTotal: prometheus.NewDesc("multica_daemon_memory_total_bytes",
			"Host memory total in bytes (node_memory_MemTotal_bytes).", daemon, nil),
		memAvailable: prometheus.NewDesc("multica_daemon_memory_available_bytes",
			"Host memory available in bytes (node_memory_MemAvailable_bytes).", daemon, nil),
		swapTotal: prometheus.NewDesc("multica_daemon_swap_total_bytes",
			"Host swap total in bytes; 0 when absent.", daemon, nil),
		swapFree: prometheus.NewDesc("multica_daemon_swap_free_bytes",
			"Host swap free in bytes; 0 when absent.", daemon, nil),
		fsSize: prometheus.NewDesc("multica_daemon_filesystem_size_bytes",
			"Filesystem size in bytes for real writable mounts (node_filesystem_size_bytes).",
			[]string{"daemon", "device", "mountpoint", "fstype"}, nil),
		fsAvail: prometheus.NewDesc("multica_daemon_filesystem_avail_bytes",
			"Filesystem space available to non-root users in bytes (node_filesystem_avail_bytes).",
			[]string{"daemon", "device", "mountpoint", "fstype"}, nil),
		diskRead: prometheus.NewDesc("multica_daemon_disk_read_bytes_total",
			"Cumulative bytes read across real block devices (node_disk_read_bytes_total).", daemon, nil),
		diskWritten: prometheus.NewDesc("multica_daemon_disk_written_bytes_total",
			"Cumulative bytes written across real block devices (node_disk_written_bytes_total).", daemon, nil),
		scrapeAge: prometheus.NewDesc("multica_daemon_resource_scrape_age_seconds",
			"Seconds since the daemon's last resource snapshot was collected; grows while a daemon is offline.",
			daemon, nil),
	}
}

// Describe implements prometheus.Collector with the static descriptor set.
func (c *Collector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.cpuSecondsTotal
	ch <- c.memTotal
	ch <- c.memAvailable
	ch <- c.swapTotal
	ch <- c.swapFree
	ch <- c.fsSize
	ch <- c.fsAvail
	ch <- c.diskRead
	ch <- c.diskWritten
	ch <- c.scrapeAge
}

// Collect implements prometheus.Collector by walking the store's live
// snapshots. CollectedAt parsing failures skip the age gauge but not the
// series — a bad clock on the daemon must not blank the host chart.
func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	for daemonID, snap := range c.store.populated() {
		rep := snap.report
		for mode, seconds := range rep.CPUModeSeconds {
			ch <- prometheus.MustNewConstMetric(c.cpuSecondsTotal, prometheus.CounterValue, seconds, daemonID, mode)
		}
		ch <- prometheus.MustNewConstMetric(c.memTotal, prometheus.GaugeValue, rep.MemTotalBytes, daemonID)
		ch <- prometheus.MustNewConstMetric(c.memAvailable, prometheus.GaugeValue, rep.MemAvailableBytes, daemonID)
		ch <- prometheus.MustNewConstMetric(c.swapTotal, prometheus.GaugeValue, rep.SwapTotalBytes, daemonID)
		ch <- prometheus.MustNewConstMetric(c.swapFree, prometheus.GaugeValue, rep.SwapFreeBytes, daemonID)
		for _, fs := range rep.Filesystems {
			ch <- prometheus.MustNewConstMetric(c.fsSize, prometheus.GaugeValue, fs.SizeBytes,
				daemonID, fs.Device, fs.Mountpoint, fs.FSType)
			ch <- prometheus.MustNewConstMetric(c.fsAvail, prometheus.GaugeValue, fs.AvailBytes,
				daemonID, fs.Device, fs.Mountpoint, fs.FSType)
		}
		ch <- prometheus.MustNewConstMetric(c.diskRead, prometheus.CounterValue, rep.DiskReadBytesTotal, daemonID)
		ch <- prometheus.MustNewConstMetric(c.diskWritten, prometheus.CounterValue, rep.DiskWrittenBytesTotal, daemonID)
		if collectedAt, err := time.Parse(time.RFC3339, rep.CollectedAt); err == nil {
			age := c.store.now().Sub(collectedAt).Seconds()
			if age < 0 {
				age = 0
			}
			ch <- prometheus.MustNewConstMetric(c.scrapeAge, prometheus.GaugeValue, age, daemonID)
		}
	}
}

var _ prometheus.Collector = (*Collector)(nil)
