package taskfailure

// DisplayClass folds a failure_reason into the 7-bucket display grouping the
// usage page's Errors tab renders (auth / rate_limit / timeout / provider /
// runtime / agent / other). The canonical 27-reason taxonomy is too fine for
// a stacked chart, a PromQL aggregate or a backpressure threshold to reason
// about, so both the client and the Prometheus failure metrics (RUYI-618)
// consume this coarser view.
//
// The mapping below mirrors packages/core/dashboard/failure-class.ts
// (REASON_CLASS) value for value — including its omissions: reasons the
// client map does not list (runtime_reconnect_timeout, invalid_task_identity,
// delivery_guard, anything a newer backend introduces) fall through to
// "other", exactly as the client's unknown-reason default does. Keeping the
// two folds byte-identical is what lets a PromQL aggregation by failure_class
// reconcile with the Errors tab by construction; the client-side test at
// packages/core/dashboard/failure-class.ts and
// TestDisplayClassStaysWithinTheClientTaxonomy pin each side of that
// contract. Update both together.

// Display class values — the wire values the client renders as the Failure
// mix breakdown. Do not rename: they are Prometheus label values.
const (
	ClassAuth      = "auth"
	ClassRateLimit = "rate_limit"
	ClassTimeout   = "timeout"
	ClassProvider  = "provider"
	ClassRuntime   = "runtime"
	ClassAgent     = "agent"
	ClassOther     = "other"
)

// reasonClass mirrors REASON_CLASS in the client's failure-class.ts. Keys are
// wire values written into agent_task_queue.failure_reason: the canonical
// Reason strings, the "unclassified" sentinel the failure rollups substitute
// for a failed row with an empty column, and pre-MUL-1949 coarse values that
// still sit in historical rows.
var reasonClass = map[Reason]string{
	// Credentials / access.
	ReasonAgentProviderAuthOrAccess: ClassAuth,
	ReasonAgentMissingConfig:        ClassAuth,

	// Capacity the account ran out of — rate limits and billing quota share
	// a class because the operator response is the same: wait, or raise a cap.
	ReasonAgentProviderCapacityOrRateLimit: ClassRateLimit,
	ReasonAgentProviderQuotaLimit:          ClassRateLimit,

	// Ran too long. Platform-side sweeper timeout and the agent's own hard
	// timeout land together — from the dashboard both read as "this run hung".
	ReasonTimeout:             ClassTimeout,
	ReasonAgentTimeout:        ClassTimeout,
	"codex_semantic_inactivity": ClassTimeout,

	// The upstream model API misbehaved or was asked for something it rejected.
	ReasonAgentProviderServerError:        ClassProvider,
	ReasonAgentProviderNetwork:            ClassProvider,
	ReasonAgentModelNotFoundOrUnavailable: ClassProvider,
	ReasonAPIInvalidRequest:               ClassProvider,

	// Multica-side execution substrate: daemon offline / restarted, task
	// never got picked up, runner binary missing or too old, environment or
	// skills the daemon could not prepare.
	ReasonRuntimeOffline:                 ClassRuntime,
	ReasonRuntimeRecovery:                ClassRuntime,
	ReasonQueuedExpired:                  ClassRuntime,
	ReasonAgentRuntimeMissingExecutable:  ClassRuntime,
	ReasonAgentRuntimeVersionUnsupported: ClassRuntime,
	ReasonSkillBundleUnavailable:         ClassRuntime,
	ReasonRuntimeCLITimeout:              ClassRuntime,
	ReasonEnvironmentPrepareFailed:       ClassRuntime,

	// The agent process itself produced the failure.
	ReasonAgentProcessFailure:           ClassAgent,
	"codex_resume_oversized":            ClassAgent,
	ReasonAgentEmptyOrUnparseableOutput: ClassAgent,
	ReasonAgentContextOverflow:          ClassAgent,
	ReasonIterationLimit:                ClassAgent,
	ReasonAgentBlocked:                  ClassAgent,

	// Catchall + legacy coarse values.
	ReasonAgentUnknown: ClassOther,
	"agent_error":      ClassOther,
	"manual":           ClassOther,
	"unclassified":     ClassOther,
}

// DisplayClass folds a failure_reason into its display class.
//
// Unknown reasons — including ones a newer backend introduced — resolve to
// "other" rather than being dropped, so the class totals always reconcile
// with the raw failure count.
//
// Callers must not pass the empty string: in the failure rollups that value
// is the succeeded bucket, not a failure. It resolves to "other" here so a
// caller that leaks one in inflates a visible bucket instead of silently
// corrupting an error rate.
func DisplayClass(r Reason) string {
	if class, ok := reasonClass[r]; ok {
		return class
	}
	return ClassOther
}
