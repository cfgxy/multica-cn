package lark

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeTerminalGenerator struct {
	enabled  bool
	response string
	err      error
	prompt   string
}

func (f *fakeTerminalGenerator) Enabled() bool { return f.enabled }
func (f *fakeTerminalGenerator) GenerateJSON(_ context.Context, _ string, _ string, prompt string, _ float64, _ int64) (string, error) {
	f.prompt = prompt
	return f.response, f.err
}

func TestTerminalSummaryCleansContextAndRedactsFallback(t *testing.T) {
	in := terminalSummaryInput("请部署 <recent_context>API_KEY=secret</recent_context> 「历史问题」", nil, "token=abc123", ProgressStateCompleted)
	got := fallbackTerminalSummary(in)
	joined := got.Demand + got.Actions + got.Result
	if strings.Contains(joined, "recent_context") || strings.Contains(joined, "历史问题") || strings.Contains(joined, "abc123") {
		t.Fatalf("fallback leaked hidden context or secret: %#v", got)
	}
	if got.Demand != "请部署" || got.Actions == "" || got.Result == "" {
		t.Fatalf("unexpected deterministic fallback: %#v", got)
	}
}

func TestLLMTerminalSummarizerSanitizesModelOutput(t *testing.T) {
	client := &fakeTerminalGenerator{enabled: true, response: `{"demand":"部署服务","actions":"完成发布","result":"token=abc123 已隐藏"}`}
	got, err := NewLLMTerminalSummarizer(client).Summarize(context.Background(), TerminalSummaryInput{Demand: "部署服务", Actions: "完成发布", State: ProgressStateCompleted})
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}
	if strings.Contains(got.Result, "abc123") || got.Demand != "部署服务" || got.Actions != "完成发布" {
		t.Fatalf("unsafe or unexpected LLM summary: %#v", got)
	}
	if !strings.Contains(client.prompt, `"actions":"完成发布"`) {
		t.Fatalf("LLM prompt missing bounded actions: %s", client.prompt)
	}
}

func TestLLMTerminalSummarizerReturnsFailureForFallback(t *testing.T) {
	client := &fakeTerminalGenerator{enabled: true, err: errors.New("deadline exceeded")}
	if _, err := NewLLMTerminalSummarizer(client).Summarize(context.Background(), TerminalSummaryInput{}); err == nil {
		t.Fatal("expected caller to fall back after LLM failure")
	}
}
