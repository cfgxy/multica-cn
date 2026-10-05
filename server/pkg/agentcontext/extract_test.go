package agentcontext

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func turns(entries ...VoiceTranscriptEntry) []VoiceTranscriptEntry {
	return entries
}

// 边界钉住（验收 2）：连续同角色转录片段归并为一轮，空片段丢弃。
func TestMergeVoiceTurnsCollapsesFragments(t *testing.T) {
	got := MergeVoiceTurns(turns(
		VoiceTranscriptEntry{Role: "user", Text: "我们"},
		VoiceTranscriptEntry{Role: "user", Text: "决定用 Postgres"},
		VoiceTranscriptEntry{Role: "assistant", Text: "好的"},
		VoiceTranscriptEntry{Role: "user", Text: "   "},
	))
	if len(got) != 2 {
		t.Fatalf("want 2 merged turns, got %d: %+v", len(got), got)
	}
	if got[0].Text != "我们 决定用 Postgres" || got[0].Index != 1 {
		t.Errorf("turn 0 = %+v", got[0])
	}
	if got[1].Role != "assistant" || got[1].Index != 2 {
		t.Errorf("turn 1 = %+v", got[1])
	}
}

// 抽取规则 v1（验收 1+2）：用户决策命中词表才成事实；助手叙述、闲聊不抽。
func TestExtractVoiceFactsDecisionBoundary(t *testing.T) {
	in := VoiceFactsInput{
		SessionID: "sess-1",
		Entries: turns(
			VoiceTranscriptEntry{Role: "user", Text: "今天天气怎么样"},
			VoiceTranscriptEntry{Role: "assistant", Text: "我决定帮你查一下"}, // 助手话中的“决定”不抽
			VoiceTranscriptEntry{Role: "user", Text: "那就拍板，确定用方案二"},
		),
		EndedAt: time.Date(2026, 10, 5, 9, 0, 0, 0, time.UTC),
	}
	facts := ExtractVoiceFacts(in)
	var decisions []Fact
	for _, f := range facts {
		if f.Kind == FactUserDecision {
			decisions = append(decisions, f)
		}
	}
	if len(decisions) != 1 {
		t.Fatalf("want exactly 1 user_decision, got %d", len(decisions))
	}
	d := decisions[0]
	if d.EventID != "live_session:sess-1#turn:3#user_decision" {
		t.Errorf("event_id = %q", d.EventID)
	}
	if d.EvidenceRef != "live_session:sess-1#turn:3" {
		t.Errorf("evidence_ref = %q", d.EvidenceRef)
	}
	if text, _ := d.Payload["text"].(string); !strings.Contains(text, "拍板") {
		t.Errorf("payload text missing original turn: %v", d.Payload)
	}
	matched, ok := d.Payload["matched"].([]string)
	if !ok || len(matched) == 0 {
		t.Errorf("payload matched missing: %v", d.Payload)
	}
}

// 边界钉住：无命中词的普通对话只产生生命周期事件，转录永不整体充当 facts
// （§3.6-4 负向）。
func TestExtractVoiceFactsNoFactsFromPlainConversation(t *testing.T) {
	in := VoiceFactsInput{
		SessionID: "sess-2",
		Entries: turns(
			VoiceTranscriptEntry{Role: "user", Text: "帮我看看这个功能怎么用"},
			VoiceTranscriptEntry{Role: "assistant", Text: "这个功能需要先配置，然后点击按钮就可以了"},
		),
		EndedAt: time.Date(2026, 10, 5, 9, 0, 0, 0, time.UTC),
	}
	facts := ExtractVoiceFacts(in)
	for _, f := range facts {
		if f.Kind == FactUserDecision {
			t.Fatalf("plain conversation must not yield user_decision: %+v", f)
		}
	}
	if len(facts) != 1 || facts[0].Kind != FactStateChange {
		t.Fatalf("want only the session-end state_change, got %+v", facts)
	}
	if status, _ := facts[0].Payload["status"].(string); status != "normal" {
		t.Errorf("end status = %q", status)
	}
	if facts[0].Payload["turn_count"] != 2 {
		t.Errorf("end turn_count = %v", facts[0].Payload["turn_count"])
	}
}

// tool 帧（验收 1）：toolCall/toolResult 逐调用成事件，args 进 payload，
// evidence 回指轮次。
func TestExtractVoiceFactsToolFrames(t *testing.T) {
	in := VoiceFactsInput{
		SessionID: "sess-3",
		ToolFrames: []VoiceToolFrame{
			{Name: "search", CallID: "c1", Args: json.RawMessage(`{"q":"rum"}`), Turn: 1},
			{Name: "search", CallID: "c1", IsResult: true, Turn: 1},
		},
		EndedAt: time.Date(2026, 10, 5, 9, 0, 0, 0, time.UTC),
	}
	facts := ExtractVoiceFacts(in)
	if len(facts) != 3 { // call + result + end
		t.Fatalf("want 3 facts, got %+v", facts)
	}
	if facts[0].Kind != FactToolCall || facts[0].EventID != "live_session:sess-3#call:1:search#tool_call" {
		t.Errorf("call fact = %+v", facts[0])
	}
	if args, ok := facts[0].Payload["args"].(map[string]any); !ok || args["q"] != "rum" {
		t.Errorf("args payload = %v", facts[0].Payload["args"])
	}
	if facts[1].Kind != FactToolResult || facts[1].EventID != "live_session:sess-3#resp:1:search#tool_result" {
		t.Errorf("result fact = %+v", facts[1])
	}
}

// 幂等前提（验收 3）：同一输入重复抽取，event_id/seq/payload 完全一致 —
// 确定性重放。
func TestExtractVoiceFactsDeterministicReplay(t *testing.T) {
	mk := func() VoiceFactsInput {
		return VoiceFactsInput{
			SessionID:     "sess-4",
			Entries:       turns(VoiceTranscriptEntry{Role: "user", Text: "敲定，用方案一"}),
			ToolFrames:    []VoiceToolFrame{{Name: "deploy", Turn: 1}},
			EndedAt:       time.Date(2026, 10, 5, 9, 0, 0, 0, time.UTC),
			SessionHandle: "handle-abc",
			Abnormal:      true,
		}
	}
	a, b := ExtractVoiceFacts(mk()), ExtractVoiceFacts(mk())
	if len(a) != len(b) {
		t.Fatalf("replay length drift: %d vs %d", len(a), len(b))
	}
	for i := range a {
		aj, _ := json.Marshal(a[i])
		bj, _ := json.Marshal(b[i])
		if string(aj) != string(bj) {
			t.Fatalf("replay drift at %d:\n%s\n%s", i, aj, bj)
		}
	}
	if a[len(a)-1].EventID != "live_session:sess-4#end#state_change" {
		t.Errorf("end event_id = %q", a[len(a)-1].EventID)
	}
	if status, _ := a[len(a)-1].Payload["status"].(string); status != "abnormal" {
		t.Errorf("abnormal end status = %q", status)
	}
}

// 生命周期边界：无 resumption handle 时不产生 handle 事件。
func TestExtractVoiceFactsNoHandleEventWithoutHandle(t *testing.T) {
	facts := ExtractVoiceFacts(VoiceFactsInput{SessionID: "s", EndedAt: time.Now()})
	for _, f := range facts {
		if strings.Contains(f.EventID, "#handle#") {
			t.Fatalf("handle event must not exist without a handle: %+v", f)
		}
	}
}

// 摘要投影（验收 4）：模板拼接、可由 facts 重建同一字符串。
func TestBuildVoiceSummaryTemplate(t *testing.T) {
	facts := ExtractVoiceFacts(VoiceFactsInput{
		SessionID: "sess-5",
		Entries:   turns(VoiceTranscriptEntry{Role: "user", Text: "决定用方案一"}),
		EndedAt:   time.Date(2026, 10, 5, 9, 0, 0, 0, time.UTC),
	})
	s1 := BuildVoiceSummary("sess-5", facts, 1)
	s2 := BuildVoiceSummary("sess-5", facts, 1)
	if s1 != s2 {
		t.Fatalf("summary not deterministic:\n%s\n%s", s1, s2)
	}
	if !strings.Contains(s1, "2 fact event(s)") || !strings.Contains(s1, "[user_decision] user decision: 决定用方案一") {
		t.Errorf("summary template unexpected:\n%s", s1)
	}
	// 无 facts：显式说明，绝不编造内容（验收 7 前提）。
	empty := BuildVoiceSummary("sess-6", nil, 4)
	if empty != "Voice session live_session:sess-6 (4 turns): no structured facts extracted." {
		t.Errorf("empty summary = %q", empty)
	}
}

// 摘要行长度截断：超长决策引用不进 brief 预算。
func TestBuildVoiceSummaryTruncatesLongDecision(t *testing.T) {
	long := strings.Repeat("很", 300)
	facts := ExtractVoiceFacts(VoiceFactsInput{
		SessionID: "s",
		Entries:   turns(VoiceTranscriptEntry{Role: "user", Text: "决定" + long}),
		EndedAt:   time.Now(),
	})
	s := BuildVoiceSummary("s", facts, 1)
	if strings.Count(s, "很") > 121 {
		t.Errorf("summary line not truncated: len=%d", len(s))
	}
}
