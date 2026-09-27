package metrics

import "github.com/prometheus/client_golang/prometheus"

// LarkMetrics is the production sink behind the Feishu adapter's Metrics
// interface (server/internal/integrations/lark/metrics.go).
//
// Scoped to outbound file delivery, because that is the path built to stay
// quiet: an object storage that cannot be read, an upload Feishu refuses and a
// send whose verdict never arrives all end with a short notice in the chat and
// the turn moving on. From the server there is no error to alert on. A
// deployment whose storage has been unreachable since Tuesday and one where no
// agent happened to produce a file look identical without these.
//
// Delivered is the denominator: a drop count of zero cannot otherwise be told
// apart from a channel nobody sent a file through.
//
// Unconfirmed is kept apart from dropped on purpose. A send whose verdict never
// came back may already be on the user's screen, so counting it as a drop would
// page an operator for deliveries that probably happened — and the adapter
// never resends one, because a duplicate file cannot be taken back.
//
// reason is a closed set (lark/outbound_media.go) and the only label here. No
// installation, workspace or session id reaches a label, the same
// unbounded-cardinality rule forbiddenMetricLabels enforces.
type LarkMetrics struct {
	AttachmentDelivered   prometheus.Counter
	AttachmentDropped     *prometheus.CounterVec
	AttachmentSheds       prometheus.Counter
	AttachmentUnconfirmed *prometheus.CounterVec
}

func NewLarkMetrics() *LarkMetrics {
	counter := func(name, help string) prometheus.Counter {
		return prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "multica", Subsystem: "lark", Name: name, Help: help,
		})
	}
	return &LarkMetrics{
		AttachmentDelivered: counter("outbound_attachment_delivered_total",
			"Files put in front of a Feishu user. Counts FILES, not replies: a reply whose words arrived and whose file did not is a delivered reply with a failed attachment, and one number cannot say both."),
		AttachmentDropped: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "multica", Subsystem: "lark", Name: "outbound_attachment_dropped_total",
			Help: "Files an agent produced that definitely did not reach the Feishu user, by reason: the object was unreadable or oversize, the upload never minted a key, or Feishu answered the send and refused it. Every reason here means somebody is waiting for a file that is not coming.",
		}, []string{"reason"}),
		AttachmentSheds: counter("outbound_attachment_delivery_shed_total",
			"Attachment deliveries refused admission before the lookup ran. Counts SCHEDULING decisions, not files: at that point nothing knows whether the turn carries zero files or five, so a per-file count from this gate would fabricate cardinality."),
		AttachmentUnconfirmed: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "multica", Subsystem: "lark", Name: "outbound_attachment_unconfirmed_total",
			Help: "Files whose delivery outcome is UNKNOWN, by reason: the send request went out and no verdict came back. Deliberately not drops — the file may already be in the chat, an operator paging on the drop rate must not be paged for deliveries that probably happened, and nothing here should prompt a resend.",
		}, []string{"reason"}),
	}
}

func (m *LarkMetrics) Collectors() []prometheus.Collector {
	return []prometheus.Collector{
		m.AttachmentDelivered, m.AttachmentDropped,
		m.AttachmentSheds, m.AttachmentUnconfirmed,
	}
}

// ---- the adapter's Metrics interface ----

func (m *LarkMetrics) RecordAttachmentDelivered() { m.AttachmentDelivered.Inc() }
func (m *LarkMetrics) RecordAttachmentDropped(reason string) {
	m.AttachmentDropped.WithLabelValues(reason).Inc()
}
func (m *LarkMetrics) RecordAttachmentDeliveryShed() { m.AttachmentSheds.Inc() }
func (m *LarkMetrics) RecordAttachmentUnconfirmed(reason string) {
	m.AttachmentUnconfirmed.WithLabelValues(reason).Inc()
}
