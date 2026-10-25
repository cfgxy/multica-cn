package agent

import (
	"bytes"
	"encoding/json"
)

// hasManagedMcpConfig preserves the API's three-state MCP semantics. Only SQL
// NULL / JSON null mean "inherit the runtime configuration"; any object,
// including an explicitly empty one, is a managed set and enables strict mode.
func hasManagedMcpConfig(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null"))
}

// SupportsTaskMCP reports whether a backend can consume daemon-provided task
// MCP servers. Keep this explicit allowlist aligned with
// packages/core/agents/mcp-support.ts: providers absent from it must fail
// closed so callers do not mint one-time credentials for a backend that cannot
// reach the task-local server.
func SupportsTaskMCP(provider string) bool {
	switch provider {
	case "claude", "codebuddy", "codearts", "codex", "cursor", "dim", "dsh",
		"grok", "hermes", "kiro", "kimi", "mcode", "opencode", "openclaw",
		"omp", "qoder", "qoderclicn", "qwen", "qwenpaw", "reasonix", "traecli", "zcode":
		return true
	default:
		return false
	}
}
