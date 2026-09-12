package agent

import (
	"log/slog"
	"testing"

	"github.com/multica-ai/multica/server/internal/agentconfig"
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

	// Context size does not: it is the size the NEXT turn would inherit, i.e.
	// the last request's whole input side PLUS the reply it produced —
	// 140 + 3000 + 0 + 30 = 3170. The accumulated equivalent would be 6420,
	// which is more than twice the real conversation and would compact a healthy
	// session less than halfway to its ceiling.
	if want := int64(3_170); contextTokens["claude-opus-5"] != want {
		t.Errorf("context tokens = %d, want the last request plus its output, %d", contextTokens["claude-opus-5"], want)
	}
}

// TestClaudeContextTokensCountTheTurnsOwnOutput is the boundary the first
// implementation got wrong: it snapshotted the input side alone, so a turn that
// ended just under a threshold and then wrote a long reply was resumed even
// though the transcript the next turn inherits is already over it.
func TestClaudeContextTokensCountTheTurnsOwnOutput(t *testing.T) {
	t.Parallel()

	// Under the default 400_000 ceiling at 80%, the soft switch is 320_000 and
	// the hard one is 400_000. Both cases sit below their threshold on the
	// input side alone and above it once the reply is counted.
	tests := []struct {
		name                string
		input, output, want int64
		// wantDecision is what the gate does with the real reading;
		// wantInputOnly is what it would have done with the input side alone,
		// i.e. the behavior this fixture exists to rule out.
		wantDecision  agentconfig.SessionResumeDecision
		wantInputOnly agentconfig.SessionResumeDecision
	}{
		{
			name:  "the reply carries the turn across the soft threshold",
			input: 319_999, output: 10_000, want: 329_999,
			wantDecision:  agentconfig.SessionResumeCompactSoft,
			wantInputOnly: agentconfig.SessionResumeAllowed,
		},
		{
			name:  "the reply carries the turn across the hard ceiling",
			input: 399_000, output: 5_000, want: 404_000,
			wantDecision: agentconfig.SessionResumeCompactHard,
			// Already past the soft switch on its own, but compacting softly
			// still RESUMES nothing near the hard limit — the ceiling is the
			// stage the missing output was hiding.
			wantInputOnly: agentconfig.SessionResumeCompactSoft,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := &claudeBackend{cfg: Config{Logger: slog.Default()}}
			ch := make(chan Message, 8)
			contextTokens := make(map[string]int64)

			b.handleAssistant(claudeSDKMessage{
				Type: "assistant",
				Message: mustMarshal(t, claudeMessageContent{
					Role:  "assistant",
					Model: "claude-opus-5",
					Usage: &claudeUsage{
						InputTokens:  tt.input,
						OutputTokens: tt.output,
					},
					Content: []claudeContentBlock{{Type: "text", Text: "ok"}},
				}),
			}, ch, make(map[string]TokenUsage), contextTokens)

			got := contextTokens["claude-opus-5"]
			if got != tt.want {
				t.Fatalf("context tokens = %d, want %d (input %d + output %d)", got, tt.want, tt.input, tt.output)
			}
			// The reading only matters through the gate: on the input side
			// alone both of these resume, which is the regression.
			if d := agentconfig.DecideSessionResume(got, true,
				agentconfig.DefaultSessionMaxContextTokens, agentconfig.DefaultSessionCompactPct); d != tt.wantDecision {
				t.Fatalf("gate decision = %q, want %q", d, tt.wantDecision)
			}
			if d := agentconfig.DecideSessionResume(tt.input, true,
				agentconfig.DefaultSessionMaxContextTokens, agentconfig.DefaultSessionCompactPct); d != tt.wantInputOnly {
				t.Fatalf("fixture no longer isolates the output: the input side alone decides %q, want %q", d, tt.wantInputOnly)
			}
		})
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

// The gateway spelling incident (field finding 2026-09-12): assistant events
// carry the model the gateway put in the response body (`gpt-5.6-terra`),
// while the result event totals the same traffic under the requested variant
// (`gpt-5.6-terra[1m]`). An exact-key fold matched nothing, so every run
// reported zero context size, the gate read it as unknown and resumed —
// 6510 usage rows with not one reading, the session gate (RUYI-107) starved
// of data for its entire deployment.
func TestClaudeContextTokensFoldAcrossModelSpellingDrift(t *testing.T) {
	t.Parallel()

	usage := map[string]TokenUsage{
		"gpt-5.6-terra[1m]": {InputTokens: 100, OutputTokens: 10},
		"gpt-5.6-sol":       {InputTokens: 200, OutputTokens: 20},
	}
	contextTokens := map[string]int64{
		"gpt-5.6-terra": 123_456, // body spelling; totals key the [1m] variant
		"gpt-5.6-sol":   234_567, // spelled identically in both events
	}

	foldContextTokens(usage, contextTokens)

	if got := usage["gpt-5.6-terra[1m]"].ContextTokens; got != 123_456 {
		t.Errorf("reading on the drifted spelling = %d, want 123456 folded onto the [1m] totals entry", got)
	}
	if got := usage["gpt-5.6-sol"].ContextTokens; got != 234_567 {
		t.Errorf("reading on the matching spelling = %d, want 234567", got)
	}
}

// The drift can point either way (the body may name the variant the totals
// omit), and a model that appears in neither map must stay at zero rather
// than lend its reading to a neighbor.
func TestClaudeContextTokensFoldIsDirectionAgnosticAndNeverBorrows(t *testing.T) {
	t.Parallel()

	usage := map[string]TokenUsage{
		"gpt-5.6-terra": {InputTokens: 100, OutputTokens: 10},
		"gpt-5.6-sol":   {InputTokens: 200, OutputTokens: 20},
	}
	contextTokens := map[string]int64{
		"gpt-5.6-terra[1m]": 345_678, // totals spell it without the suffix
		"model-nowhere-else": 456_789, // matches nothing, must be dropped
	}

	foldContextTokens(usage, contextTokens)

	if got := usage["gpt-5.6-terra"].ContextTokens; got != 345_678 {
		t.Errorf("reverse-drift reading = %d, want 345678 on the plain totals entry", got)
	}
	if got := usage["gpt-5.6-sol"].ContextTokens; got != 0 {
		t.Errorf("unrelated model picked up a reading: %d, want 0", got)
	}
}

func TestNormalizeModelKey(t *testing.T) {
	t.Parallel()

	tests := []struct{ in, want string }{
		{"gpt-5.6-terra[1m]", "gpt-5.6-terra"},
		{"GPT-5.6-Terra", "gpt-5.6-terra"},
		{"  claude-opus-5  ", "claude-opus-5"},
		{"claude-opus-5", "claude-opus-5"},
		{"[1m]", ""},
	}
	for _, tt := range tests {
		if got := normalizeModelKey(tt.in); got != tt.want {
			t.Errorf("normalizeModelKey(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
