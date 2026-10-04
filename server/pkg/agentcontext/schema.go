// Package agentcontext is the Go type contract for the unified Agent Context
// document (RUYI-425 stage 1; design doc
// 2026-10-04-RUYI-423-统一AgentContext架构设计.md §3.3).
//
// Scope is deliberately narrow: these types FIX THE WIRE SHAPE and its
// serialization so that every later producer (context assembler) and consumer
// (ClaudeCodeAdapter today, GeminiLiveAdapter later) agrees on field names,
// layer boundaries and versioning before any assembly logic exists. Nothing
// here touches the database, the dispatch path or the daemons.
//
// Two design rules are baked into the shape and MUST survive future edits:
//
//   - §3.6: the facts layer is the sole truth source; continuity is a
//     projection. Struct comments carry the rule so a refactor cannot quietly
//     promote a projection to authoritative state.
//   - §4.5 / Owner decision 3: secrets_refs holds REFERENCES only. The full
//     document is log-safe and reproducible by construction — a plaintext
//     credential must never have a field to live in. The struct deliberately
//     offers none.
package agentcontext

// SchemaVersion is the value stamped into every document this code
// generation produces. Bump only for a breaking shape change; consumers
// must tolerate unknown fields (encoding/json does by default) so additive
// revisions keep version "1".
const SchemaVersion = "1"

// AgentContext is the root document. context_id names the task or live
// session the assembly was built for, in the shape "ctx_<task_or_session_id>".
type AgentContext struct {
	SchemaVersion string `json:"schema_version"`
	ContextID     string `json:"context_id"`
	Layers        Layers `json:"layers"`
}

// NewContext returns a document with the schema version stamped and all
// layers present but empty, so a producer fills named fields instead of
// discovering missing layers at marshal time.
func NewContext(contextID string) *AgentContext {
	return &AgentContext{
		SchemaVersion: SchemaVersion,
		ContextID:     contextID,
		Layers: Layers{
			SecretsRefs: []SecretRef{},
		},
	}
}

// Layers groups the seven §3.3 layers in document order.
type Layers struct {
	// Static carries low-frequency context delivered via the runtime brief:
	// workspace notes, project instructions, agent identity, instructions
	// and skill slugs, formatting rules.
	Static StaticLayer `json:"static"`
	// Task carries everything specific to this run or trigger.
	Task TaskLayer `json:"task"`
	// Facts is the AUTHORITATIVE fact layer (§3.6): auditable, replayable
	// business facts from any runtime. Summaries elsewhere in the document
	// are projections of this layer, never the other way around.
	Facts FactsLayer `json:"facts"`
	// Continuity carries per-session resumption state. Both sides are
	// projections (§3.6); the voice summary is rebuildable from facts.
	Continuity ContinuityLayer `json:"continuity"`
	// RuntimeBinding records the resolved slot reality at assembly time.
	RuntimeBinding RuntimeBindingLayer `json:"runtime_binding"`
	// SecretsRefs lists credential references; values resolve through the
	// gateway at runtime and never leave the server (§4.5).
	SecretsRefs []SecretRef `json:"secrets_refs"`
	// Snapshot records how the assembly was built — the baseline for
	// write-back and audit comparisons.
	Snapshot SnapshotLayer `json:"snapshot"`
}

// StaticLayer mirrors §3.3 "static". Empty string fields mean "not provided".
type StaticLayer struct {
	Workspace    StaticWorkspace    `json:"workspace"`
	Project      StaticProject      `json:"project"`
	Identity     StaticIdentity     `json:"identity"`
	Instructions StaticInstructions `json:"instructions"`
	Formatting   StaticFormatting   `json:"formatting"`
}

type StaticWorkspace struct {
	ID        string `json:"id,omitempty"`
	ContextMD string `json:"context_md,omitempty"`
}

type StaticProject struct {
	ID             string `json:"id,omitempty"`
	InstructionsMD string `json:"instructions_md,omitempty"`
}

type StaticIdentity struct {
	AgentID    string `json:"agent_id,omitempty"`
	AgentName  string `json:"agent_name,omitempty"`
	RolePrompt string `json:"role_prompt,omitempty"`
}

type StaticInstructions struct {
	AgentInstructionsMD string   `json:"agent_instructions_md,omitempty"`
	Skills              []string `json:"skills,omitempty"`
}

type StaticFormatting struct {
	IssueMD   string `json:"issue_md,omitempty"`
	CommentMD string `json:"comment_md,omitempty"`
	Mentions  string `json:"mentions,omitempty"`
}

// TaskKind enumerates the §3.3 task.kind values.
type TaskKind string

const (
	TaskKindIssue       TaskKind = "issue"
	TaskKindChat        TaskKind = "chat"
	TaskKindAutopilot   TaskKind = "autopilot"
	TaskKindQuickCreate TaskKind = "quick_create"
	TaskKindVoice       TaskKind = "voice_session"
)

// TaskLayer mirrors §3.3 "task". Kind is required; everything else is
// kind-dependent.
type TaskLayer struct {
	Kind        TaskKind         `json:"kind,omitempty"`
	Issue       TaskIssue        `json:"issue,omitempty"`
	HandoffNote string           `json:"handoff_note,omitempty"`
	SiblingRuns []map[string]any `json:"sibling_runs,omitempty"`
	Initiator   TaskInitiator    `json:"initiator,omitempty"`
}

type TaskIssue struct {
	ID          string           `json:"id,omitempty"`
	Title       string           `json:"title,omitempty"`
	BodyMD      string           `json:"body_md,omitempty"`
	NewComments []map[string]any `json:"new_comments,omitempty"`
}

type TaskInitiator struct {
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
}

// FactKind enumerates the §3.3 facts.events[].kind values.
type FactKind string

const (
	FactUserDecision FactKind = "user_decision"
	FactToolCall     FactKind = "tool_call"
	FactToolResult   FactKind = "tool_result"
	FactStateChange  FactKind = "state_change"
)

// Fact is one authoritative business fact (§3.6). EventID is the idempotency
// key; EvidenceRef points back at the session and turn that produced it.
type Fact struct {
	EventID       string         `json:"event_id"`
	Seq           int64          `json:"seq"`
	SourceRuntime string         `json:"source_runtime"` // e.g. "gemini_live", "claude_code"
	Kind          FactKind       `json:"kind"`
	Payload       map[string]any `json:"payload,omitempty"`
	EvidenceRef   string         `json:"evidence_ref,omitempty"` // e.g. "live_session:12#turn:7"
	RecordedAt    string         `json:"recorded_at,omitempty"`
}

// FactsLayer mirrors §3.3 "facts". Events is nil-safe: an empty facts layer
// marshals as an empty list so consumers never special-case null.
type FactsLayer struct {
	Events []Fact `json:"events"`
}

// ContinuityLayer mirrors §3.3 "continuity": the text side collects the
// fields the current dispatch path already passes; the voice side adds the
// live-session equivalents. BOTH are projections (§3.6).
type ContinuityLayer struct {
	Text  ContinuityText  `json:"text"`
	Voice ContinuityVoice `json:"voice"`
}

type ContinuityText struct {
	PriorSessionID    string `json:"prior_session_id,omitempty"`
	PriorContextBrief string `json:"prior_context_brief,omitempty"`
	ResumeUnavailable bool   `json:"resume_unavailable,omitempty"`
}

type ContinuityVoice struct {
	// PriorSessionHandle maps to the gateway's sessionResumption handle.
	PriorSessionHandle string `json:"prior_session_handle,omitempty"`
	// LastVoiceSummary is a write-back projection, rebuildable from facts
	// (§3.6); it is injected into clientContent / the brief.
	LastVoiceSummary string `json:"last_voice_summary,omitempty"`
	// CarryOverDecisions are conclusions the user explicitly took away. Each
	// entry ALSO lands in the facts layer — that is where the authority
	// lives; this list is the convenience projection.
	CarryOverDecisions []string `json:"carry_over_decisions,omitempty"`
}

// RuntimeBindingLayer mirrors §3.3 "runtime_binding": the resolved slot
// reality, validated at assembly time.
type RuntimeBindingLayer struct {
	Text  SlotBinding `json:"text"`
	Voice SlotBinding `json:"voice"`
	// VoiceAvailable is the product degradation signal (Owner question 6):
	// false when the voice slot is empty, disabled or its credential is
	// broken. Consumers downgrade to text instead of failing.
	VoiceAvailable bool `json:"voice_available"`
}

type SlotBinding struct {
	InstanceID     string   `json:"instance_id,omitempty"`
	ProtocolFamily string   `json:"protocol_family,omitempty"`
	Capabilities   []string `json:"capabilities,omitempty"`
}

// SecretScope enumerates the §3.3 secrets_refs[].scope values.
type SecretScope string

const (
	SecretScopeVoiceRuntimeInstance SecretScope = "voice_runtime_instance"
)

// SecretRef is a credential POINTER (§4.5). Ref matches the DB carrier
// agent_runtime.credential_ref: "<instance-uuid>:<credential-key>". No
// struct in this package has anywhere for a credential value to go.
type SecretRef struct {
	Scope SecretScope `json:"scope"`
	Ref   string      `json:"ref"`
}

// SnapshotLayer mirrors §3.3 "snapshot".
type SnapshotLayer struct {
	AssembledAt string `json:"assembled_at,omitempty"`
	// SourceHashes keys the assembled inputs to the hash of what was read
	// (e.g. "workspace" -> "sha256...").
	SourceHashes map[string]string `json:"source_hashes,omitempty"`
	// VoiceSetupParams records what the voice session was set up with.
	VoiceSetupParams VoiceSetupParams `json:"voice_setup_params,omitempty"`
}

type VoiceSetupParams struct {
	Model                string `json:"model,omitempty"`
	SystemInstructionRef string `json:"system_instruction_ref,omitempty"`
}
