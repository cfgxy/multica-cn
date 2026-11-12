package agent

import (
	"os"
	"testing"
)

func TestTranscriptUsageBeforeDeduplicatesAssistantBlocks(t *testing.T) {
	path := t.TempDir() + "/session.jsonl"
	data := []byte("{" +
		"\"type\":\"assistant\",\"isSidechain\":false,\"message\":{" +
		"\"id\":\"m1\",\"model\":\"claude-test\",\"usage\":{" +
		"\"input_tokens\":100,\"output_tokens\":10}}}\n" +
		"{" +
		"\"type\":\"assistant\",\"isSidechain\":false,\"message\":{" +
		"\"id\":\"m1\",\"model\":\"claude-test\",\"usage\":{" +
		"\"input_tokens\":100,\"output_tokens\":10}}}\n" +
		"{" +
		"\"type\":\"assistant\",\"isSidechain\":false,\"message\":{" +
		"\"id\":\"m2\",\"model\":\"claude-test\",\"usage\":{" +
		"\"input_tokens\":50,\"output_tokens\":5}}}\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	baseline, ok := transcriptUsageBefore(path, int64(len(data)))
	if !ok {
		t.Fatal("transcriptUsageBefore reported an unreadable transcript")
	}
	if got := baseline["claude-test"].InputTokens; got != 150 {
		t.Fatalf("baseline input = %d, want 150 after deduplicating repeated assistant blocks", got)
	}
}

func TestClaudeUsageAccountingMarksResumedResultCumulative(t *testing.T) {
	usage := map[string]TokenUsage{"claude-test": {InputTokens: 250, OutputTokens: 30}}
	baseline := map[string]TokenUsage{"claude-test": {InputTokens: 100, OutputTokens: 10}}
	accounting := claudeUsageAccounting(usage, baseline, true, true, "session-1")["claude-test"]
	if accounting.Mode != UsageAccountingCumulative || !accounting.BaselineKnown {
		t.Fatalf("accounting = %+v, want known cumulative", accounting)
	}
	if accounting.Raw.InputTokens != 250 || accounting.Baseline.InputTokens != 100 {
		t.Fatalf("raw/baseline = %d/%d, want 250/100", accounting.Raw.InputTokens, accounting.Baseline.InputTokens)
	}
}
