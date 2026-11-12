package agent

import (
	"encoding/json"
	"testing"
)

func TestACPUsageAccountingCapturesPrePromptBaseline(t *testing.T) {
	client := &hermesClient{}
	var live []Message
	client.onMessage = func(message Message) { live = append(live, message) }
	client.mergeUsage(parseACPTokenUsageSnapshot(json.RawMessage(`{"inputTokens":100,"outputTokens":10,"cachedReadTokens":20}`)))
	client.markPromptStarted(true)
	if len(live) != 1 || live[0].Usage == nil || !live[0].UsageCumulative || live[0].Usage.InputTokens != 100 {
		t.Fatalf("live baseline message = %+v, want one cumulative pre-prompt snapshot", live)
	}
	client.mergeUsage(parseACPTokenUsageSnapshot(json.RawMessage(`{"inputTokens":250,"outputTokens":35,"cachedReadTokens":40}`)))

	accounting := client.usageAccounting("session-1", true, false)
	if accounting.Mode != UsageAccountingCumulative || !accounting.BaselineKnown {
		t.Fatalf("accounting = %+v, want cumulative with known baseline", accounting)
	}
	if accounting.Raw.InputTokens != 250 || accounting.Baseline.InputTokens != 100 {
		t.Fatalf("raw/baseline input = %d/%d, want 250/100", accounting.Raw.InputTokens, accounting.Baseline.InputTokens)
	}
}

func TestACPUsageAccountingMarksMismatchedResumeUnknown(t *testing.T) {
	client := &hermesClient{}
	client.mergeUsage(parseACPTokenUsageSnapshot(json.RawMessage(`{"inputTokens":250,"outputTokens":35}`)))
	accounting := client.usageAccounting("new-session", false, true)
	if accounting.Mode != UsageAccountingUnknown || accounting.Raw.InputTokens != 250 {
		t.Fatalf("accounting = %+v, want unknown raw snapshot for mismatched resume", accounting)
	}
}
