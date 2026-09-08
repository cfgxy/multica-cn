package agent

import (
	"log/slog"
	"testing"
)

// The whole point of ContextTokens (RUYI-107) is that it does NOT behave like
// the billing counters next to it. A run that resends a growing history reports
// a rising sum for input_tokens and the size of the LAST request for context —
// confusing the two is exactly the bug the field exists to avoid.
func TestClaudeContextTokensSnapshotsLastRequestNotTheSum(t *testing.T) {
	t.Parallel()

	b := &claudeBackend{cfg: Config{Logger: slog.Default()}}
	ch := make(chan Message, 32)
	usage := make(map[string]TokenUsage)
	contextTokens := make(map[string]int64)

	// Three requests in one run, each resending a larger conversation.
	for _, req := range []struct {
		input, cacheRead, cacheWrite, output int64
	}{
		{input: 100, cacheRead: 1_000, cacheWrite: 50, output: 10},
		{input: 120, cacheRead: 2_000, cacheWrite: 0, output: 20},
		{input: 140, cacheRead: 3_000, cacheWrite: 0, output: 30},
	} {
		msg := claudeSDKMessage{
			Type: "assistant",
			Message: mustMarshal(t, claudeMessageContent{
				Role:  "assistant",
				Model: "claude-opus-5",
				Usage: &claudeUsage{
					InputTokens:              req.input,
					OutputTokens:             req.output,
					CacheReadInputTokens:     req.cacheRead,
					CacheCreationInputTokens: req.cacheWrite,
				},
				Content: []claudeContentBlock{{Type: "text", Text: "ok"}},
			}),
		}
		b.handleAssistant(msg, ch, usage, contextTokens)
	}

	// Billing counters accumulate: 100+120+140 input, 1000+2000+3000 cache read.
	got := usage["claude-opus-5"]
	if got.InputTokens != 360 {
		t.Errorf("InputTokens = %d, want the accumulated 360", got.InputTokens)
	}
	if got.CacheReadTokens != 6_000 {
		t.Errorf("CacheReadTokens = %d, want the accumulated 6000", got.CacheReadTokens)
	}

	// Context size does not: it is the last request's whole input side,
	// 140 + 3000 + 0 = 3140. The accumulated equivalent would be 6360, which is
	// more than twice the real conversation and would compact a healthy session
	// less than halfway to its ceiling.
	if want := int64(3_140); contextTokens["claude-opus-5"] != want {
		t.Errorf("context tokens = %d, want the last request's %d", contextTokens["claude-opus-5"], want)
	}
}

// A model that reports no usable numbers must leave the reading absent rather
// than record a zero: the gate treats zero as unknown, but only because nothing
// else writes one — an explicit zero here would be indistinguishable from a
// genuinely empty conversation.
func TestClaudeContextTokensAbsentWhenNothingReported(t *testing.T) {
	t.Parallel()

	b := &claudeBackend{cfg: Config{Logger: slog.Default()}}
	ch := make(chan Message, 8)
	usage := make(map[string]TokenUsage)
	contextTokens := make(map[string]int64)

	b.handleAssistant(claudeSDKMessage{
		Type: "assistant",
		Message: mustMarshal(t, claudeMessageContent{
			Role:    "assistant",
			Model:   "claude-opus-5",
			Usage:   &claudeUsage{},
			Content: []claudeContentBlock{{Type: "text", Text: "ok"}},
		}),
	}, ch, usage, contextTokens)

	if _, ok := contextTokens["claude-opus-5"]; ok {
		t.Errorf("an all-zero usage report must record no context reading, got %d", contextTokens["claude-opus-5"])
	}
}
