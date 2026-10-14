package outerloop

import "strings"

// IsPipelineClass reports whether a failed run's failure reason is the
// retry-curable infrastructure class: platform/scheduler/runtime failures
// plus the three transient provider conditions the taxonomy itself marks
// "transient — backoff + retry is appropriate". A pipeline-class failure is
// left for the stall sweep to pull back without interrupting a human
// (RUYI-660 M2); everything else escalates to waiting_human, because a
// credential, quota, config or context problem needs a person before any
// retry can succeed.
//
// Direction is deliberately fail-toward-human: an unknown or future
// agent_error.* reason escalates (a classifier gap must not silently loop),
// while a future platform-side reason self-heals. Two platform-side reasons
// are carved out because their own taxonomy notes demand a human:
//
//   - agent_blocked: the agent deliberately asked for human input; auto
//     re-dispatching would fight its own judgment.
//   - runtime_cli_timeout: a deterministic local stall a retry re-pays
//     identically; the fix is configuration, not a rerun.
func IsPipelineClass(reason string) bool {
	if reason == "" {
		return false
	}
	switch reason {
	// Platform-side carve-outs: the human is the only cure.
	case "agent_blocked", "runtime_cli_timeout":
		return false
	// Agent-side transient trio: backoff + retry is the documented cure.
	case "agent_error.provider_capacity_or_rate_limit",
		"agent_error.provider_server_error",
		"agent_error.provider_network":
		return true
	}
	return !strings.HasPrefix(reason, "agent_error.")
}
