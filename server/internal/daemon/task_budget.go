package daemon

import (
	"github.com/multica-ai/multica/server/pkg/agent"
	"github.com/multica-ai/multica/server/pkg/taskfailure"
)

// taskUsageEntries preserves cost-only reports and run statistics even when
// the provider supplied no billable token counters.
func taskUsageEntries(provider string, result agent.Result) []TaskUsageEntry {
	var entries []TaskUsageEntry
	for model, u := range result.Usage {
		if u.InputTokens == 0 && u.OutputTokens == 0 && u.CacheReadTokens == 0 && u.CacheWriteTokens == 0 && u.ContextTokens == 0 && u.CostUSDTicks == 0 && result.Turns == 0 && result.Compactions == 0 && result.MaxContextTokens == 0 {
			continue
		}
		entries = append(entries, TaskUsageEntry{Provider: provider, Model: model, InputTokens: u.InputTokens, OutputTokens: u.OutputTokens, CacheReadTokens: u.CacheReadTokens, CacheWriteTokens: u.CacheWriteTokens, CostUSDTicks: u.CostUSDTicks, ContextTokens: u.ContextTokens, Turns: result.Turns, Compactions: result.Compactions, MaxContextTokens: result.MaxContextTokens})
	}
	if len(entries) == 0 && (result.Turns > 0 || result.Compactions > 0 || result.MaxContextTokens > 0) {
		entries = append(entries, TaskUsageEntry{Provider: provider, Model: "unknown", Turns: result.Turns, Compactions: result.Compactions, MaxContextTokens: result.MaxContextTokens})
	}
	return entries
}

func executionBudgetStopReason(status string) string {
	switch status {
	case "max_turns":
		return taskfailure.ReasonMaxTurns.String()
	case "context_budget":
		return taskfailure.ReasonContextBudget.String()
	default:
		return ""
	}
}
