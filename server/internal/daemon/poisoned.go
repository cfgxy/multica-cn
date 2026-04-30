package daemon

import "strings"

// FailureReason values for tasks that "completed" with output but the
// output is actually a known agent fallback marker — i.e. the agent gave
// up and emitted a meta message instead of a real result. Listed here so
// the server-side query GetLastTaskSession can filter them out and a
// rerun starts from a fresh agent session instead of resuming the same
// poisoned conversation.
const (
	FailureReasonIterationLimit   = "iteration_limit"
	FailureReasonAgentFallbackMsg = "agent_fallback_message"
)

// poisonedMarkers maps a substring fingerprint of a known agent fallback
// terminal message to its failure_reason classifier. Match is case-
// insensitive and substring-based — the exact wording drifts across
// model versions, so we anchor on the most stable phrase fragment.
var poisonedMarkers = []struct {
	Substring string
	Reason    string
}{
	{"i reached the iteration limit", FailureReasonIterationLimit},
	{"put your final update inside the content string", FailureReasonAgentFallbackMsg},
}

// classifyPoisonedOutput reports whether output matches a known agent
// fallback terminal message and, if so, returns the failure_reason that
// should be persisted on the task row.
func classifyPoisonedOutput(output string) (string, bool) {
	if output == "" {
		return "", false
	}
	lowered := strings.ToLower(output)
	for _, m := range poisonedMarkers {
		if strings.Contains(lowered, m.Substring) {
			return m.Reason, true
		}
	}
	return "", false
}
