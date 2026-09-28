package agent

import (
	"log/slog"
	"testing"
)

// modelArgValue returns the value the built argv passes to the CLI's
// `--model`, or "" when the flag is absent. It reads the flag the launcher
// itself emits, so a CustomArgs-supplied `--model` later in the line cannot
// be mistaken for it.
func modelArgValue(t *testing.T, args []string) string {
	t.Helper()
	for i, a := range args {
		if a == "--model" {
			if i+1 >= len(args) {
				t.Fatalf("--model present with no value: %v", args)
			}
			return args[i+1]
		}
	}
	return ""
}

// TestBuildClaudeArgsStripsContextTagForGatewayModels is the canonical matrix
// for how a context-window tag reaches the CLI. The Claude CLI rejects the
// launch outright when a gateway alias carries the tag
// (`[claude-code:unrecognized_model] {"model":"gpt-5.6-sol[1m]"}`, RUYI-256),
// while a Claude row needs the tag to keep the window it advertises.
func TestBuildClaudeArgsStripsContextTagForGatewayModels(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		model string
		want  string
	}{
		// Gateway aliases: the tag is a Multica-side window marker the
		// gateway never coined, so the bare id goes on the wire.
		{"openai sol", "gpt-5.6-sol[1m]", "gpt-5.6-sol"},
		{"openai terra", "gpt-5.6-terra[1m]", "gpt-5.6-terra"},
		{"deepseek", "deepseek-v4-flash[1m]", "deepseek-v4-flash"},
		// kimi-k3[1m] and grok-4.5[1m] are the ids pricing.go already folds
		// onto the untagged SKU (see PriceForModelAlias), so the launcher
		// must fold them the same way.
		{"kimi", "kimi-k3[1m]", "kimi-k3"},
		{"grok", "grok-4.5[1m]", "grok-4.5"},
		{"qwen", "qwen3.8-max[1m]", "qwen3.8-max"},
		{"vendor prefixed alias", "moonshotai/kimi-k3[1m]", "moonshotai/kimi-k3"},

		// Claude rows: claudeModelsFromInfos keeps the tag on purpose —
		// `claude-opus-5[1m]` is literally what the CLI runs for the
		// "Opus (1M context)" row, and stripping it downgrades the window.
		{"claude opus tagged", "claude-opus-5[1m]", "claude-opus-5[1m]"},
		{"claude fable tagged", "claude-fable-5-1[1m]", "claude-fable-5-1[1m]"},
		{"claude vendor prefixed", "anthropic/claude-sonnet-5[1m]", "anthropic/claude-sonnet-5[1m]"},
		{"claude mixed case", "Claude-Opus-5[1M]", "Claude-Opus-5[1M]"},

		// Untagged ids pass through verbatim on both sides.
		{"untagged gateway", "gpt-5.6-sol", "gpt-5.6-sol"},
		{"untagged claude", "claude-opus-5", "claude-opus-5"},

		// Shapes that are not a complete trailing tag are left alone, the
		// same call pricing's contextTagRe and the frontend's
		// stripContextTag make.
		{"empty tag", "gpt-5.6-sol[]", "gpt-5.6-sol[]"},
		{"unterminated tag", "gpt-5.6-sol[1m", "gpt-5.6-sol[1m"},
		{"tag not at end", "gpt-5.6-sol[1m]-preview", "gpt-5.6-sol[1m]-preview"},
		// A doubly tagged id is not a shape we recognise: peeling one tag
		// would forward a still-tagged id that reads as deliberate.
		{"double tag", "gpt-5.6-sol[1m][2m]", "gpt-5.6-sol[1m][2m]"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			args := buildClaudeArgs(ExecOptions{Model: tc.model}, slog.Default())
			if got := modelArgValue(t, args); got != tc.want {
				t.Fatalf("model %q: --model %q, want %q", tc.model, got, tc.want)
			}
		})
	}
}

// TestBuildClaudeArgsOmitsModelWhenUnset keeps the empty model meaning "let
// the CLI resolve its own default" — normalization must not introduce a flag.
func TestBuildClaudeArgsOmitsModelWhenUnset(t *testing.T) {
	t.Parallel()

	args := buildClaudeArgs(ExecOptions{}, slog.Default())
	if got := modelArgValue(t, args); got != "" {
		t.Fatalf("expected no --model flag, got %q in %v", got, args)
	}
}

// TestBuildClaudeArgsKeepsEffortAfterStrippedModel pins the ordering comment
// in buildClaudeArgs: --effort still follows the model selection it runs
// against once the tag is stripped.
func TestBuildClaudeArgsKeepsEffortAfterStrippedModel(t *testing.T) {
	t.Parallel()

	args := buildClaudeArgs(ExecOptions{Model: "gpt-5.6-sol[1m]", ThinkingLevel: "high"}, slog.Default())
	for i, a := range args {
		if a != "--model" {
			continue
		}
		if i+3 >= len(args) || args[i+1] != "gpt-5.6-sol" || args[i+2] != "--effort" || args[i+3] != "high" {
			t.Fatalf("expected `--model gpt-5.6-sol --effort high`, got %v", args)
		}
		return
	}
	t.Fatalf("expected --model in args: %v", args)
}
