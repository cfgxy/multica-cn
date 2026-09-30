package metrics

import "github.com/prometheus/client_golang/prometheus"

// ChannelChatRunReconcilerMetrics observes the channel chat run-intent
// compensating sweep (RUYI-304): how many abandoned debounce windows it
// recovered into real task runs, how many rows it had to terminalize for
// permanent causes, and how often transient failures forced a retry or
// exhausted the attempt budget.
type ChannelChatRunReconcilerMetrics struct {
	// CompensatedRuns counts windows the reconciler enqueued after the
	// in-memory flush never happened (crash, restart, enqueue failure).
	CompensatedRuns prometheus.Counter
	// PermanentDead counts rows terminalized for a permanent cause (session
	// archived, agent missing/offline/archived, route superseded, missing
	// initiator snapshot, session missing).
	PermanentDead prometheus.Counter
	// RetryExhausted counts rows that burned the full attempt budget on
	// transient errors and were terminalized as retries_exhausted.
	RetryExhausted prometheus.Counter
	// BackoffReleases counts transient-failure releases back to 'pending'.
	BackoffReleases prometheus.Counter
}

func NewChannelChatRunReconcilerMetrics() *ChannelChatRunReconcilerMetrics {
	return &ChannelChatRunReconcilerMetrics{
		CompensatedRuns: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "multica",
			Subsystem: "channel_chat_run",
			Name:      "reconciler_compensated_runs_total",
			Help:      "Abandoned channel run windows recovered into task runs by the reconciler.",
		}),
		PermanentDead: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "multica",
			Subsystem: "channel_chat_run",
			Name:      "reconciler_permanent_dead_total",
			Help:      "Run-intent rows terminalized for a permanent cause.",
		}),
		RetryExhausted: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "multica",
			Subsystem: "channel_chat_run",
			Name:      "reconciler_retry_exhausted_total",
			Help:      "Run-intent rows terminalized after exhausting the transient-retry budget.",
		}),
		BackoffReleases: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "multica",
			Subsystem: "channel_chat_run",
			Name:      "reconciler_backoff_releases_total",
			Help:      "Run-intent rows released for retry after a transient failure.",
		}),
	}
}

func (m *ChannelChatRunReconcilerMetrics) Collectors() []prometheus.Collector {
	return []prometheus.Collector{
		m.CompensatedRuns, m.PermanentDead, m.RetryExhausted, m.BackoffReleases,
	}
}
