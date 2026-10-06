package agentcontext

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/pkg/agent"
)

// TestRoundTrip proves the contract serializes losslessly: a fully-populated
// document survives marshal → unmarshal unchanged, and the wire shape matches
// the §3.3 field names exactly (snake_case layer keys, schema_version first
// class).
func TestRoundTrip(t *testing.T) {
	ctx := NewContext("ctx_task_123")
	ctx.Layers.Static.Workspace = StaticWorkspace{ID: "ws-1", ContextMD: "# notes"}
	ctx.Layers.Static.Project = StaticProject{ID: "proj-1", InstructionsMD: "be precise"}
	ctx.Layers.Static.Identity = StaticIdentity{AgentID: "agent-1", AgentName: "顾小鱼", RolePrompt: "dev"}
	ctx.Layers.Static.Instructions = StaticInstructions{AgentInstructionsMD: "- ship", Skills: []string{"beads"}}
	ctx.Layers.Static.Formatting = StaticFormatting{IssueMD: "md", CommentMD: "md", Mentions: "mention://agent/x"}

	ctx.Layers.Task = TaskLayer{
		Kind: TaskKindVoice,
		Issue: TaskIssue{
			ID:     "RUYI-425",
			Title:  "落地统一 Agent Context",
			BodyMD: "body",
			NewComments: []map[string]any{
				{"author": "leader", "body": "go"},
			},
		},
		HandoffNote: "handoff",
		SiblingRuns: []map[string]any{{"task_id": "T-1"}},
		Initiator:   TaskInitiator{Name: "owner", Email: "cfgxy@example.com"},
	}

	ctx.Layers.Facts.Events = []Fact{
		{
			EventID:       "evt-1",
			Seq:           42,
			SourceRuntime: "gemini_live",
			Kind:          FactUserDecision,
			Payload:       map[string]any{"decision": "不补卡"},
			EvidenceRef:   "live_session:12#turn:7",
			RecordedAt:    "2026-10-05T00:00:00Z",
		},
	}

	ctx.Layers.Continuity.Text = ContinuityText{
		PriorSessionID:    "sess-9",
		PriorContextBrief: "brief",
		ResumeUnavailable: true,
	}
	ctx.Layers.Continuity.Voice = ContinuityVoice{
		PriorSessionHandle: "handle-abc",
		LastVoiceSummary:   "summary",
		CarryOverDecisions: []string{"用 AWQ int4"},
	}

	ctx.Layers.RuntimeBinding = RuntimeBindingLayer{
		Text:           SlotBinding{InstanceID: "inst-1", ProtocolFamily: "claude_code", Capabilities: []string{agent.CapabilityText, agent.CapabilityTools}},
		Voice:          SlotBinding{InstanceID: "inst-2", ProtocolFamily: "gemini_live", Capabilities: []string{agent.CapabilityRealtimeVoice, agent.CapabilityTools}},
		VoiceAvailable: true,
	}

	ctx.Layers.SecretsRefs = []SecretRef{
		{Scope: SecretScopeVoiceRuntimeInstance, Ref: "inst-2:gemini_api_key"},
	}

	ctx.Layers.Snapshot = SnapshotLayer{
		AssembledAt:  "2026-10-05T01:00:00Z",
		SourceHashes: map[string]string{"workspace": "sha256:aa"},
		VoiceSetupParams: VoiceSetupParams{
			Model:                "gemini-2.5-flash-native-audio",
			SystemInstructionRef: "sha256:bb",
		},
	}

	blob, err := json.Marshal(ctx)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var back AgentContext
	if err := json.Unmarshal(blob, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(back, *ctx) {
		t.Fatalf("round trip lost data:\n want %+v\n got  %+v", *ctx, back)
	}

	// Wire-shape anchors from §3.3, pinned so a rename cannot ship silently.
	for _, key := range []string{
		`"schema_version":"1"`,
		`"context_id":"ctx_task_123"`,
		`"runtime_binding"`,
		`"voice_available":true`,
		`"secrets_refs":[{"scope":"voice_runtime_instance","ref":"inst-2:gemini_api_key"}]`,
		`"source_runtime":"gemini_live"`,
		`"evidence_ref":"live_session:12#turn:7"`,
	} {
		if !strings.Contains(string(blob), key) {
			t.Errorf("serialized document missing %s", key)
		}
	}
}

// TestNewContextDefaults checks the constructor contract: version stamped,
// secrets_refs a non-nil list (log-safe empty document, no null surprises).
func TestNewContextDefaults(t *testing.T) {
	ctx := NewContext("ctx_x")
	if ctx.SchemaVersion != SchemaVersion {
		t.Fatalf("schema version = %q, want %q", ctx.SchemaVersion, SchemaVersion)
	}
	if ctx.Layers.SecretsRefs == nil {
		t.Fatal("secrets_refs must default to an empty list, not nil")
	}
	blob, err := json.Marshal(ctx)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(blob), `"secrets_refs":[]`) {
		t.Fatalf("empty secrets_refs should serialize as [], got %s", blob)
	}
}

// TestSecretsRefsHoldNoValues is the §4.5 guard: nothing in the package shape
// can carry a plaintext credential — the only secret-shaped struct is the
// ref pair, and its JSON never grows a value field.
func TestSecretsRefsHoldNoValues(t *testing.T) {
	blob, err := json.Marshal([]SecretRef{{Scope: SecretScopeVoiceRuntimeInstance, Ref: "i:k"}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, banned := range []string{"value", "secret_encrypted", "api_key_value", "token"} {
		if strings.Contains(string(blob), banned) {
			t.Errorf("SecretRef serialization contains banned key %q: %s", banned, blob)
		}
	}
}
