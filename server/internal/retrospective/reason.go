package retrospective

// Stable, machine-readable failure reasons for retrospective runs (RUYI-561).
//
// The run row's `error` text column is legacy surface: rows finished before
// the reason codes shipped carry raw server-side sentences there (some with
// internal error chains), and the UI must not render them. Every failure
// this package records from now on writes a reason into the run row's
// `detail` JSONB — detail.reason.code plus at most the whitelisted,
// non-sensitive params — and logs the full error chain to the server log
// only (slog at the failure site). The UI localizes from these codes;
// unknown codes degrade to a generic localized fallback instead of raw text.
//
// The values are wire surface: the frontend mirrors them in
// packages/views/self-evolution/components/retrospective-run-error.ts, and a
// parity test locks each code to a four-locale key. Renaming a value is a
// breaking change for in-flight run rows — add a new code instead.
const (
	// Agent gate — actionable: the user can fix the configuration and retry.
	ReasonAgentNotConfigured  = "agent_not_configured"  // enabled config with no agent selected (pre-migration row)
	ReasonAgentMissing        = "agent_missing"         // configured agent id resolves to nothing
	ReasonAgentArchived       = "agent_archived"        // configured agent is archived
	ReasonAgentRuntimeMissing = "agent_runtime_missing" // configured agent has no runtime bound

	// Trigger-side failures — not actionable; the next run retries.
	ReasonWindowScanFailed      = "window_scan_failed"       // listing the completed-issue window failed
	ReasonWatermarkCheckFailed  = "watermark_check_failed"   // probing an issue's watermark failed
	ReasonScanScopeRecordFailed = "scan_scope_record_failed" // persisting the scanned membership failed
	ReasonTaskEnqueueFailed     = "task_enqueue_failed"      // enqueueing the platform task failed

	// Completion-side failures — not actionable; nothing was recorded, the
	// next run re-scans the same window (no watermark was written).
	ReasonAgentReportInvalid = "agent_report_invalid" // final message is not a valid retrospective report
	ReasonAgentNoReport      = "agent_no_report"      // run finished without a final message
	ReasonAgentRunFailed     = "agent_run_failed"     // the platform task reported failure (raw text: server log only)

	// Membership violations — not actionable (agent misbehavior); the whole
	// report is rejected and nothing is recorded. Carries the offending
	// issue id as the only whitelisted param.
	ReasonReportOutOfScope = "report_out_of_scope" // analyzed_issue_ids names an issue outside the window
	ReasonDraftOutOfScope  = "draft_out_of_scope"  // a draft references an issue outside the window

	// Reconciler backstops — not actionable; the next run retries.
	ReasonTaskNotCompleted  = "task_not_completed"  // linked task went terminal without the completion hook seeing it
	ReasonTaskNeverEnqueued = "task_never_enqueued" // the task never reached the queue; aged out
)

// runReason is the structured failure verdict on the run row's detail. IssueID
// is the only param allowed on the wire, and only the out-of-scope codes set
// it — a UUID the workspace's own members already see in issue URLs.
type runReason struct {
	Code    string `json:"code"`
	IssueID string `json:"issue_id,omitempty"`
}
