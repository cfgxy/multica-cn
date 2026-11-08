package agent

import "testing"

func TestSupportsTaskMCP(t *testing.T) {
	tests := map[string]bool{
		"claude":      true,
		"codebuddy":   true,
		"codearts":    true,
		"codex":       true,
		"cursor":      true,
		"dim":         true,
		"dsh":         true,
		"grok":        true,
		"hermes":      true,
		"kiro":        true,
		"kimi":        true,
		"mcode":       true,
		"opencode":    true,
		"openclaw":    true,
		"omp":         true,
		"qoder":       true,
		"qoderclicn":  true,
		"qwen":        true,
		"qwenpaw":     true,
		"reasonix":    true,
		"traecli":     true,
		"zcode":       true,
		"antigravity": false,
		"copilot":     false,
		"deerflow":    false,
		"deveco":      false,
		"pi":          false,
		"zeroclaw":    false,
		"unknown":     false,
	}
	for provider, want := range tests {
		if got := SupportsTaskMCP(provider); got != want {
			t.Errorf("SupportsTaskMCP(%q) = %t, want %t", provider, got, want)
		}
	}
}
