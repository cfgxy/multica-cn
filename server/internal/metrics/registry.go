package metrics

import (
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"

	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/internal/realtime"
)

type RegistryOptions struct {
	Pool     *pgxpool.Pool
	Realtime *realtime.Metrics
	DaemonWS *daemonws.Metrics
	// TaskUsage, when non-nil, is registered so the task_usage_hourly
	// aggregates (multica_task_usage_* series) are exposed on /metrics.
	TaskUsage prometheus.Collector
	// TaskFailures, when non-nil, is registered so the terminal-task failure
	// aggregates (multica_agent_task_failures_total /
	// multica_agent_task_runs_total) are exposed on /metrics.
	TaskFailures prometheus.Collector
	Version   string
	Commit    string
}

type Registry struct {
	Gatherer       prometheus.Gatherer
	HTTP           *HTTPMetrics
	Business       *BusinessMetrics
	ChannelMedia   *ChannelMediaReconcilerMetrics
	ChannelLease   *ChannelLeaseMetrics
	ChannelChatRun *ChannelChatRunReconcilerMetrics
	Wecom          *WecomMetrics
	Lark           *LarkMetrics
}

func NewRegistry(opts RegistryOptions) *Registry {
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector())
	reg.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	buildInfo := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "multica_build_info",
		Help: "Build information for the Multica server binary.",
	}, []string{"version", "commit"})
	buildInfo.WithLabelValues(defaultLabel(opts.Version, "dev"), defaultLabel(opts.Commit, "unknown")).Set(1)
	reg.MustRegister(buildInfo)

	httpMetrics := NewHTTPMetrics()
	reg.MustRegister(httpMetrics.Collectors()...)

	businessMetrics := NewBusinessMetrics()
	reg.MustRegister(businessMetrics.Collectors()...)

	channelMedia := NewChannelMediaReconcilerMetrics()
	channelChatRun := NewChannelChatRunReconcilerMetrics()
	reg.MustRegister(channelMedia.Collectors()...)
	reg.MustRegister(channelChatRun.Collectors()...)

	channelLease := NewChannelLeaseMetrics()
	reg.MustRegister(channelLease.Collectors()...)

	wecomMetrics := NewWecomMetrics()
	reg.MustRegister(wecomMetrics.Collectors()...)

	larkMetrics := NewLarkMetrics()
	reg.MustRegister(larkMetrics.Collectors()...)

	if opts.Pool != nil {
		reg.MustRegister(NewDBCollector(opts.Pool))
	}
	if opts.Realtime != nil {
		reg.MustRegister(NewRealtimeCollector(opts.Realtime))
	}
	if opts.DaemonWS != nil {
		reg.MustRegister(NewDaemonWSCollector(opts.DaemonWS))
	}
	if opts.TaskUsage != nil {
		reg.MustRegister(opts.TaskUsage)
	}
	if opts.TaskFailures != nil {
		reg.MustRegister(opts.TaskFailures)
	}

	return &Registry{
		Gatherer:       reg,
		HTTP:           httpMetrics,
		Business:       businessMetrics,
		ChannelMedia:   channelMedia,
		ChannelChatRun: channelChatRun,
		ChannelLease:   channelLease,
		Wecom:          wecomMetrics,
		Lark:           larkMetrics,
	}
}

func defaultLabel(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}
