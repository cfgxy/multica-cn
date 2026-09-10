package handler

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/multica-ai/multica/server/internal/agentconfig"
)

func validateAgentMaxConcurrentTasks(value int32) error {
	if err := agentconfig.ValidateMaxConcurrentTasks(value); err != nil {
		return fmt.Errorf("max_concurrent_tasks %w", err)
	}
	return nil
}

func defaultAndValidateAgentMaxConcurrentTasks(rawFields map[string]json.RawMessage, value *int32) error {
	raw, provided := rawFields["max_concurrent_tasks"]
	if !provided || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		*value = agentconfig.DefaultMaxConcurrentTasks
		return nil
	}
	return validateAgentMaxConcurrentTasks(*value)
}

func validateAgentSessionMaxContextTokens(value int64) error {
	if err := agentconfig.ValidateSessionMaxContextTokens(value); err != nil {
		return fmt.Errorf("session_max_context_tokens %w", err)
	}
	return nil
}

func validateAgentSessionCompactPct(value int32) error {
	if err := agentconfig.ValidateSessionCompactPct(value); err != nil {
		return fmt.Errorf("session_compact_pct %w", err)
	}
	return nil
}

// defaultAndValidateAgentSessionGate applies the same omitted-means-default
// rule as max_concurrent_tasks, and for the same reason: 0 is a legal, meaning-
// carrying value for session_max_context_tokens (it disables the gate), so an
// absent field must not decode to it. A client that never heard of these
// settings would otherwise create every agent with the gate switched off.
func defaultAndValidateAgentSessionGate(rawFields map[string]json.RawMessage, maxTokens *int64, compactPct *int32) error {
	if raw, provided := rawFields["session_max_context_tokens"]; !provided || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		*maxTokens = agentconfig.DefaultSessionMaxContextTokens
	} else if err := validateAgentSessionMaxContextTokens(*maxTokens); err != nil {
		return err
	}
	if raw, provided := rawFields["session_compact_pct"]; !provided || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		*compactPct = agentconfig.DefaultSessionCompactPct
	} else if err := validateAgentSessionCompactPct(*compactPct); err != nil {
		return err
	}
	return nil
}
