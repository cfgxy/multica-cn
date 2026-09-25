package taskfailure

import "testing"

// The D6 denominator rule from ADR-002 §11 T4: a run that failed because the
// environment or the provider broke says nothing about the prompt, so it must
// not sit in the denominator of a prompt-attributable failure rate. Nine
// reasons are named there; this table is the executable copy of that list.
func TestPromptAttributableExcludesEnvironmentAndProviderReasons(t *testing.T) {
	excluded := []Reason{
		ReasonRuntimeOffline,
		ReasonRuntimeReconnectTimeout,
		ReasonRuntimeRecovery,
		ReasonEnvironmentPrepareFailed,
		ReasonAgentProviderQuotaLimit,
		ReasonAgentProviderCapacityOrRateLimit,
		ReasonAgentProviderNetwork,
		ReasonAgentProviderServerError,
		ReasonAgentRuntimeMissingExecutable,
	}
	for _, r := range excluded {
		if PromptAttributable(r) {
			t.Errorf("%s must be excluded from the D6 prompt-attribution denominator", r)
		}
	}
	if got, want := len(excluded), len(promptUnattributableReasons); got != want {
		t.Errorf("excluded reason count drifted: test lists %d, implementation has %d", got, want)
	}
}

// The complement matters just as much: a run that hit the iteration limit or
// came back with unparseable output is exactly the kind of failure a prompt
// can cause, and dropping it would hide the regression the dashboard exists
// to show.
func TestPromptAttributableKeepsPromptShapedReasons(t *testing.T) {
	kept := []Reason{
		ReasonIterationLimit,
		ReasonAgentBlocked,
		ReasonAgentContextOverflow,
		ReasonAgentEmptyOrUnparseableOutput,
		ReasonTimeout,
		ReasonAgentUnknown,
	}
	for _, r := range kept {
		if !PromptAttributable(r) {
			t.Errorf("%s must stay in the D6 prompt-attribution denominator", r)
		}
	}
}

// An unrecognized reason — a newer server writing a value this binary has
// never seen — counts as attributable. Excluding it would let an unknown
// failure class quietly shrink the denominator and inflate the pass rate;
// including it at worst adds noise that is visible in the reason breakdown.
func TestPromptAttributableTreatsUnknownReasonAsAttributable(t *testing.T) {
	if !PromptAttributable(Reason("something_this_binary_has_never_seen")) {
		t.Error("an unrecognized reason must count as attributable, not be silently dropped")
	}
}

// Every name in the exclusion list has to be a real Reason. A typo would be
// invisible at runtime: the lookup simply never matches, and the excluded
// class silently re-enters the denominator.
func TestPromptUnattributableReasonsAreAllKnown(t *testing.T) {
	known := make(map[Reason]bool, len(AllReasons()))
	for _, r := range AllReasons() {
		known[r] = true
	}
	for r := range promptUnattributableReasons {
		if !known[r] {
			t.Errorf("%q is in the exclusion list but is not a declared Reason", r)
		}
	}
}
