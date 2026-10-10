package taskfailure

import "testing"

// The display grouping mirrors the client's fold in
// packages/core/dashboard/failure-class.ts: the Prometheus failure metrics
// (RUYI-618) carry a failure_class label so a PromQL aggregation by that
// label reconciles with the usage page's Errors tab by construction. These
// tests pin both directions of that contract — the class spelling (the wire
// values the client renders) and the per-reason mapping (including reasons
// the client map does not list, which must fall through to "other" exactly
// as its unknown-reason default does).
func TestDisplayClassStaysWithinTheClientTaxonomy(t *testing.T) {
	classes := map[string]bool{}
	for _, reason := range AllReasons() {
		class := DisplayClass(reason)
		if class == "" {
			t.Errorf("DisplayClass(%q) returned an empty class", reason)
		}
		classes[class] = true
	}
	// The client renders exactly these seven, most-actionable first. A class
	// spelled differently here would split one page bucket into two series.
	for _, class := range []string{"auth", "rate_limit", "timeout", "provider", "runtime", "agent", "other"} {
		if !classes[class] {
			t.Errorf("class %q is never produced from AllReasons()", class)
		}
	}
	if len(classes) != 7 {
		t.Errorf("DisplayClass produced %d distinct classes, want the 7 the client renders: %v", len(classes), classes)
	}
}

func TestDisplayClassMapping(t *testing.T) {
	cases := map[Reason]string{
		ReasonAgentProviderAuthOrAccess:        "auth",
		ReasonAgentMissingConfig:               "auth",
		ReasonAgentProviderCapacityOrRateLimit: "rate_limit",
		ReasonAgentProviderQuotaLimit:          "rate_limit",
		ReasonTimeout:                          "timeout",
		ReasonAgentTimeout:                     "timeout",
		// Historical coarse values that still sit in pre-MUL-1949 rows.
		"codex_semantic_inactivity":            "timeout",
		ReasonAgentProviderServerError:         "provider",
		ReasonAgentProviderNetwork:             "provider",
		ReasonAgentModelNotFoundOrUnavailable:  "provider",
		ReasonAPIInvalidRequest:                "provider",
		ReasonRuntimeOffline:                   "runtime",
		ReasonRuntimeRecovery:                  "runtime",
		ReasonQueuedExpired:                    "runtime",
		ReasonAgentRuntimeMissingExecutable:    "runtime",
		ReasonAgentRuntimeVersionUnsupported:   "runtime",
		ReasonSkillBundleUnavailable:           "runtime",
		ReasonRuntimeCLITimeout:                "runtime",
		ReasonEnvironmentPrepareFailed:         "runtime",
		ReasonAgentProcessFailure:              "agent",
		"codex_resume_oversized":               "agent",
		ReasonAgentEmptyOrUnparseableOutput:    "agent",
		ReasonAgentContextOverflow:             "agent",
		ReasonIterationLimit:                   "agent",
		ReasonAgentBlocked:                     "agent",
		ReasonAgentUnknown:                     "other",
		"agent_error":                          "other",
		"manual":                               "other",
		"unclassified":                         "other",
		// Canonical reasons the client map predates: its unknown-reason
		// default puts them in "other", so this fold must too — otherwise
		// the metric's class totals stop adding up to the page's.
		ReasonRuntimeReconnectTimeout: "other",
		ReasonInvalidTaskIdentity:     "other",
		ReasonDeliveryGuard:           "other",
		// A reason from a backend newer than this fold: "other", never
		// dropped, so class totals keep reconciling with the raw count.
		"agent_error.some_future_reason": "other",
		"":                               "other",
	}
	for reason, want := range cases {
		if got := DisplayClass(reason); got != want {
			t.Errorf("DisplayClass(%q) = %q, want %q", reason, got, want)
		}
	}
}
