package lark

// Metrics is the observability seam for outbound file delivery.
//
// The delivery path is built to stay quiet: a file that cannot be read, an
// upload Lark refuses and a send whose verdict never arrives all end with the
// user getting a short notice in the chat and the turn moving on. From the
// server there is no error to alert on and no log line an operator watches.
// A deployment whose object storage has been unreachable since Tuesday and one
// where no agent happened to produce a file look identical without these.
//
// Delivered is the denominator: without it a drop count of zero cannot be told
// apart from a channel nobody sent a file through.
//
// Defined here as an interface rather than taking the Prometheus registry
// directly so the package tests can assert on what an operator would see
// without standing up a registry, and so a deployment with metrics disabled
// wires nothing. The production sink is internal/metrics.LarkMetrics.
//
// Every reason argument comes from the closed sets in outbound_media.go. No
// installation, workspace or session id reaches a label — that is the same
// unbounded-cardinality rule internal/metrics forbiddenMetricLabels enforces.
type Metrics interface {
	// RecordAttachmentDelivered counts one FILE put in front of a Lark user.
	RecordAttachmentDelivered()
	// RecordAttachmentDropped counts one FILE that definitely did not arrive.
	RecordAttachmentDropped(reason string)
	// RecordAttachmentUnconfirmed counts one FILE whose outcome is unknown —
	// it may already be on the user's screen, so it is deliberately not a
	// drop: an operator paging on the drop rate must not be paged for
	// deliveries that probably happened.
	RecordAttachmentUnconfirmed(reason string)
	// RecordAttachmentDeliveryShed counts one SCHEDULING decision, not a
	// file: admission is refused before the lookup runs, when nothing yet
	// knows whether the turn carries zero files or five.
	RecordAttachmentDeliveryShed()
}

// nopMetrics is the default sink: a deployment without metrics enabled wires
// nothing and the call sites stay free of nil checks.
type nopMetrics struct{}

func (nopMetrics) RecordAttachmentDelivered()         {}
func (nopMetrics) RecordAttachmentDropped(string)     {}
func (nopMetrics) RecordAttachmentUnconfirmed(string) {}
func (nopMetrics) RecordAttachmentDeliveryShed()      {}

// orNopMetrics normalizes a nil Metrics to the no-op sink.
func orNopMetrics(m Metrics) Metrics {
	if m == nil {
		return nopMetrics{}
	}
	return m
}
