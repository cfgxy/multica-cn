package taskfailure

// Prompt attribution (RUYI-184, dimension D6).
//
// The quality dashboard reports how often runs under a given prompt version
// failed. That number is only meaningful if the denominator contains failures
// a prompt could plausibly have caused. A runtime that went offline, a
// provider that returned 5xx, a missing CLI binary — none of those move when
// the prompt changes, and leaving them in makes every provider incident look
// like a prompt regression on the chart (ADR-002 §11 T4).
//
// The excluded set is deliberately narrow and deliberately explicit. It is not
// "everything IsAgentError says is infrastructure": ReasonAgentContextOverflow
// is an agent-side error that a bloated prompt causes directly, and
// ReasonAgentProviderAuthOrAccess is a configuration problem that no prompt
// edit fixes but that also is not a transient environment fault — it stays in
// the denominator so a misconfigured workspace is visible rather than
// invisible.

// promptUnattributableReasons is the nine-reason environment/provider class
// that must never enter the D6 denominator. A map rather than a slice because
// the rollup checks one reason per run against it.
var promptUnattributableReasons = map[Reason]bool{
	// Platform side: the run never got a fair chance to execute.
	ReasonRuntimeOffline:           true,
	ReasonRuntimeReconnectTimeout:  true,
	ReasonRuntimeRecovery:          true,
	ReasonEnvironmentPrepareFailed: true,

	// Provider side: the upstream model service refused or broke.
	ReasonAgentProviderQuotaLimit:          true,
	ReasonAgentProviderCapacityOrRateLimit: true,
	ReasonAgentProviderNetwork:             true,
	ReasonAgentProviderServerError:         true,

	// Local runtime side: the agent CLI is not installed. The process never
	// started, so nothing was ever read from the prompt.
	ReasonAgentRuntimeMissingExecutable: true,
}

// PromptAttributable reports whether a failed run carrying this reason belongs
// in the D6 prompt-attribution denominator.
//
// An unrecognized reason is attributable. A newer server can write a value
// this binary has never seen, and the safe direction is to keep it: an unknown
// failure inside the denominator is visible noise, while an unknown failure
// silently excluded would shrink the denominator and make the prompt look
// better than the data says.
func PromptAttributable(r Reason) bool {
	return !promptUnattributableReasons[r]
}
