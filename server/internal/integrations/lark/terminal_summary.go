package lark

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/pkg/redact"
)

const terminalSummaryTimeout = 4 * time.Second

var (
	recentContextRE = regexp.MustCompile(`(?s)<recent_context\b[^>]*>.*?</recent_context\s*>`)
	quotedHistoryRE = regexp.MustCompile(`「[^」]*」`)
	tagRE           = regexp.MustCompile(`(?s)<[^>]+>`)
)

// TerminalSummary is the compact, user-visible explanation retained after a
// process card is settled. It contains no raw tool payloads.
type TerminalSummary struct {
	Demand  string `json:"demand"`
	Actions string `json:"actions"`
	Result  string `json:"result"`
}

// TerminalSummaryInput contains only the bounded, redacted material needed to
// explain a completed channel task. It is deliberately separate from the full
// task transcript so the assist-layer call cannot accidentally grow into a
// history export.
type TerminalSummaryInput struct {
	Demand      string
	Actions     string
	Activities  []string
	FinalResult string
	State       ProgressState
}

// TerminalSummarizer is asynchronous-card infrastructure: failure to provide a
// summary is non-fatal because the deterministic renderer remains available.
type TerminalSummarizer interface {
	Summarize(context.Context, TerminalSummaryInput) (TerminalSummary, error)
}

type terminalJSONGenerator interface {
	Enabled() bool
	GenerateJSON(ctx context.Context, model, systemPrompt, userPrompt string, temperature float64, maxCompletionTokens int64) (string, error)
}

type llmTerminalSummarizer struct{ client terminalJSONGenerator }

// NewLLMTerminalSummarizer adapts pkg/llm.Client without coupling Patcher
// tests to a live model transport. Nil or disabled clients fail closed to the
// deterministic fallback.
func NewLLMTerminalSummarizer(client terminalJSONGenerator) TerminalSummarizer {
	return llmTerminalSummarizer{client: client}
}

func (s llmTerminalSummarizer) Summarize(ctx context.Context, in TerminalSummaryInput) (TerminalSummary, error) {
	if s.client == nil || !s.client.Enabled() {
		return TerminalSummary{}, errors.New("terminal summary LLM is not configured")
	}
	payload, err := json.Marshal(struct {
		Demand     string   `json:"demand"`
		Actions    string   `json:"actions"`
		Activities []string `json:"activities"`
		Result     string   `json:"result"`
		State      string   `json:"state"`
	}{
		Demand: in.Demand, Actions: in.Actions, Activities: in.Activities, Result: in.FinalResult, State: string(in.State),
	})
	if err != nil {
		return TerminalSummary{}, fmt.Errorf("marshal terminal summary input: %w", err)
	}
	const system = "Return JSON only with demand, actions, result. Each value must be a concise natural-language Chinese sentence (15-40 Chinese characters when possible). Summarize the user's actual request, meaningful work completed, and concrete outcome. Never repeat recent context, quoted history, XML/system tags, credentials, tool names, or raw parameters."
	raw, err := s.client.GenerateJSON(ctx, "", system, string(payload), 0.2, 240)
	if err != nil {
		return TerminalSummary{}, err
	}
	var out TerminalSummary
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return TerminalSummary{}, fmt.Errorf("decode terminal summary JSON: %w", err)
	}
	out = sanitizeTerminalSummary(out)
	if out.Demand == "" || out.Actions == "" || out.Result == "" {
		return TerminalSummary{}, errors.New("terminal summary JSON has empty fields")
	}
	return out, nil
}

func cleanTerminalText(s string) string {
	s = recentContextRE.ReplaceAllString(s, " ")
	s = quotedHistoryRE.ReplaceAllString(s, " ")
	s = tagRE.ReplaceAllString(s, " ")
	return collapseWhitespace(redact.Text(s))
}

func clampTerminalSummaryText(s string) string { return clampTerminalText(cleanTerminalText(s), 80) }

func sanitizeTerminalSummary(in TerminalSummary) TerminalSummary {
	return TerminalSummary{
		Demand:  clampTerminalSummaryText(in.Demand),
		Actions: clampTerminalSummaryText(in.Actions),
		Result:  clampTerminalSummaryText(in.Result),
	}
}

func fallbackTerminalSummary(in TerminalSummaryInput) TerminalSummary {
	result := cleanTerminalText(in.FinalResult)
	if result == "" {
		result = strings.TrimPrefix(progressTerminalResult(in.State), "结果：")
	}
	return sanitizeTerminalSummary(TerminalSummary{
		Demand:  strings.TrimPrefix(progressDemand(in.Demand), "需求："),
		Actions: strings.TrimPrefix(in.Actions, "执行："),
		Result:  result,
	})
}

func terminalSummaryInput(trigger string, entries []ProgressEntry, result string, state ProgressState) TerminalSummaryInput {
	activities := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.Text == "" {
			continue
		}
		activities = append(activities, cleanTerminalText(entry.Text))
	}
	return TerminalSummaryInput{
		Demand:      cleanTerminalText(strings.TrimSpace(trigger)),
		Actions:     strings.TrimPrefix(progressActions(entries), "执行："),
		Activities:  activities,
		FinalResult: cleanTerminalText(result),
		State:       state,
	}
}
