package agent

import (
	"testing"

	"github.com/multica-ai/multica/server/internal/agentconfig"
)

// ContextTokens must mean the same thing on both backends (RUYI-107): the size
// of the conversation the NEXT turn inherits. Codex reports it per turn, so the
// field is assigned rather than accumulated, and it includes the reply this
// turn produced — that reply is appended to the thread before anyone resumes it.
func TestCodexContextTokensSnapshotsLastTurnIncludingItsOutput(t *testing.T) {
	t.Parallel()

	c, _, _ := newTestCodexClient(t)

	for _, turn := range []map[string]any{
		{"usage": map[string]any{"input_tokens": float64(1_000), "cached_input_tokens": float64(600), "output_tokens": float64(50)}},
		{"usage": map[string]any{"input_tokens": float64(3_000), "cached_input_tokens": float64(2_500), "output_tokens": float64(120)}},
	} {
		c.extractUsageFromMap(turn)
	}

	// Billing counters accumulate the UNCACHED remainder: (1000-600)+(3000-2500).
	if got := c.usage.InputTokens; got != 900 {
		t.Errorf("InputTokens = %d, want the accumulated uncached 900", got)
	}
	if got := c.usage.CacheReadTokens; got != 3_100 {
		t.Errorf("CacheReadTokens = %d, want the accumulated 3100", got)
	}
	// The context reading does not accumulate, and it uses the RAW input side —
	// a cached prefix is cheaper to send, not absent from the window.
	if want := int64(3_120); c.usage.ContextTokens != want {
		t.Errorf("ContextTokens = %d, want the last turn's %d (3000 raw input + 120 output)", c.usage.ContextTokens, want)
	}
}

// TestCodexContextTokensCountTheTurnsOwnOutput mirrors the Claude boundary case:
// on the input side alone these turns resume, and the transcript the next one
// inherits is already past the threshold.
func TestCodexContextTokensCountTheTurnsOwnOutput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                string
		input, output, want int64
		wantDecision        agentconfig.SessionResumeDecision
		wantInputOnly       agentconfig.SessionResumeDecision
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
			wantDecision:  agentconfig.SessionResumeCompactHard,
			wantInputOnly: agentconfig.SessionResumeCompactSoft,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _, _ := newTestCodexClient(t)
			c.extractUsageFromMap(map[string]any{"usage": map[string]any{
				"input_tokens":  float64(tt.input),
				"output_tokens": float64(tt.output),
			}})

			got := c.usage.ContextTokens
			if got != tt.want {
				t.Fatalf("ContextTokens = %d, want %d (input %d + output %d)", got, tt.want, tt.input, tt.output)
			}
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

// A turn that reports no input side leaves the previous reading alone rather
// than overwriting it with a zero the gate would read as "unknown".
func TestCodexContextTokensKeepsLastRealReading(t *testing.T) {
	t.Parallel()

	c, _, _ := newTestCodexClient(t)
	c.extractUsageFromMap(map[string]any{"usage": map[string]any{
		"input_tokens": float64(5_000), "output_tokens": float64(100),
	}})
	c.extractUsageFromMap(map[string]any{"usage": map[string]any{"output_tokens": float64(7)}})

	if want := int64(5_100); c.usage.ContextTokens != want {
		t.Errorf("ContextTokens = %d, want the last real reading %d", c.usage.ContextTokens, want)
	}
}
