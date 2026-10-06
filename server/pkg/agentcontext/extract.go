package agentcontext

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// This file is the v1 extraction boundary fixed by RUYI-425 stage 4
// (design doc 2026-10-04-RUYI-423-统一AgentContext架构设计.md §3.5 write-back,
// §3.6 hard principles). The rules below are the PERSISTENCE BOUNDARY —
// what counts as a business fact — and are pinned by extract_test.go:
//
//  1. state_change (deterministic lifecycle signals): the session end
//     (payload carries normal/abnormal and the turn count) and the presence
//     of a provider resumption handle. Session start is deliberately NOT an
//     event: live_session.started_at already carries it and a start row adds
//     no auditable business meaning.
//  2. tool_call / tool_result (structured provider frames): one event per
//     function call the provider issued and per function response the client
//     returned. Frames arrive from the gateway's frame parser, not from text.
//  3. user_decision (narrow deterministic predicate): ONLY when a merged
//     user turn matches decisionVerbs — an explicit decision-verb word list,
//     kept deliberately narrow (宁可漏报不可滥报). The payload carries the
//     turn text and the matched verbs; evidence_ref points at the turn.
//     This is pattern matching over transcribed text, never free-form
//     semantic inference: borderline phrasing that misses the list is NOT a
//     fact (v1 accepts recall loss over precision loss).
//
// What is NEVER a fact, by construction (§3.6-4 语音语义不可追溯性禁止的反面
// 边界): a whole transcript turn as "context", assistant narration, filler
// audio, or any content no rule above matched. ExtractVoiceFacts returns
// fewer events, never looser ones.

// decisionVerbs is the §3.6-5 extraction boundary for user_decision, pinned
// here so a review can audit the exact trigger set. Extend only with explicit
// decision predicates; never with generic keywords like "想" / "maybe" / "看看".
var decisionVerbs = regexp.MustCompile(`(?i)(决定|拍板|敲定|就这么定|确定用|选定|定下来|` +
	`decide|decided on|decision is|go with|finalize|settle on|confirm using)`)

// SourceRuntimeGeminiLive stamps voice write-back facts (schema.go's
// source_runtime example value).
const SourceRuntimeGeminiLive = "gemini_live"

// VoiceTranscriptEntry mirrors the persisted live_session.transcript element
// shape ({role,text,at}) without importing the db package.
type VoiceTranscriptEntry struct {
	Role string `json:"role"`
	Text string `json:"text"`
	At   string `json:"at"`
}

// VoiceTurn is one merged conversational turn: consecutive transcript
// entries of the same role collapse into a single turn (the provider streams
// transcriptions as incremental fragments). Index is 1-based and is what
// evidence_ref turns point at.
type VoiceTurn struct {
	Role  string
	Text  string
	Index int
}

// MergeVoiceTurns collapses consecutive same-role transcript entries into
// turns (fragment merge, deterministic).
func MergeVoiceTurns(entries []VoiceTranscriptEntry) []VoiceTurn {
	turns := make([]VoiceTurn, 0, len(entries))
	for _, e := range entries {
		text := strings.TrimSpace(e.Text)
		if text == "" {
			continue
		}
		if n := len(turns); n > 0 && turns[n-1].Role == e.Role {
			turns[n-1].Text += " " + text
			continue
		}
		turns = append(turns, VoiceTurn{Role: e.Role, Text: text, Index: len(turns) + 1})
	}
	return turns
}

// VoiceToolFrame is one provider-side tool interaction the gateway parsed out
// of the websocket stream: IsResult=false for toolCall frames (provider →
// gateway), true for toolResponse frames (client → provider). Turn is the
// 1-based merged user-turn index the frame arrived nearest to (0 when the
// caller cannot attribute it); it feeds evidence_ref only.
type VoiceToolFrame struct {
	Name     string
	CallID   string
	Args     json.RawMessage
	Turn     int
	IsResult bool
}

// VoiceFactsInput is everything one live session's write-back extracts from.
// SessionID is the live session UUID string — the evidence_ref namespace.
type VoiceFactsInput struct {
	SessionID     string
	Entries       []VoiceTranscriptEntry
	ToolFrames    []VoiceToolFrame
	EndedAt       time.Time
	Abnormal      bool // relay died mid-stream rather than closing cleanly
	SessionHandle string
}

// FactEventID derives the DETERMINISTIC idempotency key (§3.6-5): the same
// session, marker and kind always produce the same event_id, so a write-back
// replay after a partial failure re-INSERTs rows that collapse on the
// (workspace_id, event_id) unique index instead of duplicating.
func FactEventID(sessionID, marker string, kind FactKind) string {
	return fmt.Sprintf("live_session:%s#%s#%s", sessionID, marker, kind)
}

// factEvidenceRef points back at the exact turn that produced a fact
// (§3.3 evidence_ref, e.g. "live_session:12#turn:7").
func factEvidenceRef(sessionID string, turn int) string {
	return fmt.Sprintf("live_session:%s#turn:%d", sessionID, turn)
}

// ExtractVoiceFacts runs the v1 rules over one session's inputs, in
// deterministic order: tool interactions (stream order), user decisions
// (turn order), then lifecycle events. Seq numbers the slice 0..n-1 — the
// write order IS the order, and replaying the same input reproduces it
// byte-for-byte.
func ExtractVoiceFacts(in VoiceFactsInput) []Fact {
	turns := MergeVoiceTurns(in.Entries)
	facts := make([]Fact, 0, len(turns)/2+len(in.ToolFrames)+2)
	recorded := in.EndedAt
	if recorded.IsZero() {
		recorded = time.Now().UTC()
	}
	appendFact := func(eventID string, kind FactKind, payload map[string]any, evidence string) {
		facts = append(facts, Fact{
			EventID:       eventID,
			Seq:           int64(len(facts)),
			SourceRuntime: SourceRuntimeGeminiLive,
			Kind:          kind,
			Payload:       payload,
			EvidenceRef:   evidence,
			RecordedAt:    recorded.Format(time.RFC3339),
		})
	}

	for _, f := range in.ToolFrames {
		kind := FactToolCall
		marker := "call"
		if f.IsResult {
			kind = FactToolResult
			marker = "resp"
		}
		payload := map[string]any{"name": f.Name, "turn": f.Turn}
		if f.CallID != "" {
			payload["call_id"] = f.CallID
		}
		if len(f.Args) > 0 && !f.IsResult {
			var args any
			if json.Unmarshal(f.Args, &args) == nil {
				payload["args"] = args
			}
		}
		appendFact(FactEventID(in.SessionID, fmt.Sprintf("%s:%d:%s", marker, f.Turn, f.Name), kind),
			kind, payload, factEvidenceRef(in.SessionID, f.Turn))
	}

	for _, t := range turns {
		if t.Role != "user" {
			continue
		}
		matched := decisionVerbs.FindAllString(t.Text, -1)
		if len(matched) == 0 {
			continue
		}
		appendFact(FactEventID(in.SessionID, fmt.Sprintf("turn:%d", t.Index), FactUserDecision),
			FactUserDecision,
			map[string]any{"text": t.Text, "matched": matched, "role": t.Role},
			factEvidenceRef(in.SessionID, t.Index))
	}

	if in.SessionHandle != "" {
		appendFact(FactEventID(in.SessionID, "handle", FactStateChange),
			FactStateChange,
			map[string]any{"has_resumption_handle": true},
			factEvidenceRef(in.SessionID, 0))
	}

	status := "normal"
	if in.Abnormal {
		status = "abnormal"
	}
	appendFact(FactEventID(in.SessionID, "end", FactStateChange),
		FactStateChange,
		map[string]any{"status": status, "turn_count": len(turns)},
		factEvidenceRef(in.SessionID, 0))

	return facts
}

// BuildVoiceSummary assembles the summary PROJECTION from facts alone
// (§3.6-3): template concatenation, no model call, deterministic — the same
// facts always rebuild the same summary, which is what makes the stored
// live_session.summary column safely overwritable. v1 keeps LLM summaries
// out by design decision (dispatch criterion 4: 模板拼接，LLM 摘要留增强).
func BuildVoiceSummary(sessionID string, facts []Fact, turnCount int) string {
	var b strings.Builder
	if len(facts) == 0 {
		fmt.Fprintf(&b, "Voice session live_session:%s (%d turns): no structured facts extracted.",
			sessionID, turnCount)
		return b.String()
	}
	fmt.Fprintf(&b, "Voice session live_session:%s (%d turns), %d fact event(s):", sessionID, turnCount, len(facts))
	for _, f := range facts {
		fmt.Fprintf(&b, "\n- [%s] %s", f.Kind, factBriefLine(f))
	}
	return b.String()
}

// factBriefLine renders one fact as a single citable line. Length-capped so
// a long quoted decision cannot blow the brief budget.
func factBriefLine(f Fact) string {
	switch f.Kind {
	case FactUserDecision:
		text, _ := f.Payload["text"].(string)
		text = strings.TrimSpace(text)
		const cap = 120
		if len([]rune(text)) > cap {
			text = string([]rune(text)[:cap]) + "…"
		}
		return "user decision: " + text
	case FactToolCall:
		name, _ := f.Payload["name"].(string)
		return "tool call " + name
	case FactToolResult:
		name, _ := f.Payload["name"].(string)
		return "tool result " + name
	case FactStateChange:
		status, _ := f.Payload["status"].(string)
		if status != "" {
			return "session ended (" + status + ")"
		}
		if hasHandle, _ := f.Payload["has_resumption_handle"].(bool); hasHandle {
			return "resumption handle recorded"
		}
		return "state change"
	}
	return f.EventID
}
