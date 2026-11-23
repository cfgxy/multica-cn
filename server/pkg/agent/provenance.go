package agent

import "time"

const (
	MaxProvenanceTools         = 64
	MaxProvenanceCompactEvents = 16
	MaxProvenanceStreamTypes   = 128
)

// RunProvenance is bounded, redacted evidence for joining a provider run to its
// session transcript. It deliberately contains hashes and metadata, not prompt,
// tool input, tool output, credentials, or transcript bodies.
type RunProvenance struct {
	RunID                         string                   `json:"run_id,omitempty"`
	TaskID                        string                   `json:"task_id,omitempty"`
	RuntimeID                     string                   `json:"runtime_id,omitempty"`
	Generation                    int                      `json:"generation,omitempty"`
	Provider                      string                   `json:"provider,omitempty"`
	RequestedSessionID            string                   `json:"requested_session_id,omitempty"`
	EffectiveSessionID            string                   `json:"effective_session_id,omitempty"`
	SessionGate                   string                   `json:"session_gate,omitempty"`
	ResumeRejected                bool                     `json:"resume_rejected,omitempty"`
	ResumeRejectedTransient       bool                     `json:"resume_rejected_transient,omitempty"`
	PriorSessionResumeUnavailable bool                     `json:"prior_session_resume_unavailable,omitempty"`
	PriorContextBriefSHA256       string                   `json:"prior_context_brief_sha256,omitempty"`
	ResumeContinuityNoticeSHA256  string                   `json:"resume_continuity_notice_sha256,omitempty"`
	SyntheticContinuation         bool                     `json:"synthetic_continuation,omitempty"`
	SyntheticContinuationSource   string                   `json:"synthetic_continuation_source,omitempty"`
	SummarySource                 string                   `json:"summary_source,omitempty"`
	TranscriptSHA256              string                   `json:"transcript_sha256,omitempty"`
	TranscriptStartOffset         int64                    `json:"transcript_start_offset,omitempty"`
	TranscriptEndOffset           int64                    `json:"transcript_end_offset,omitempty"`
	Compactions                   []CompactProvenanceEvent `json:"compactions,omitempty"`
	Stream                        ProviderStreamProvenance `json:"stream"`
	Permission                    PermissionSnapshot       `json:"permission"`
	Tools                         []string                 `json:"tools,omitempty"`
	ObservedAt                    time.Time                `json:"observed_at"`
}

type CompactProvenanceEvent struct {
	Offset        int64  `json:"offset"`
	EndOffset     int64  `json:"end_offset"`
	LineSHA256    string `json:"line_sha256"`
	SummarySHA256 string `json:"summary_sha256,omitempty"`
	Source        string `json:"source,omitempty"`
}

type ProviderStreamProvenance struct {
	EventCount          int      `json:"event_count"`
	InvalidEventCount   int      `json:"invalid_event_count"`
	AssistantEventCount int      `json:"assistant_event_count"`
	ToolUseCount        int      `json:"tool_use_count"`
	UnreadableAssistant int      `json:"unreadable_assistant_count"`
	EventTypes          []string `json:"event_types,omitempty"`
}

type PermissionSnapshot struct {
	Mode             string `json:"mode,omitempty"`
	MCPConfigured    bool   `json:"mcp_configured"`
	SettingsProvided bool   `json:"settings_provided"`
}

// WorkerProvenanceWriter is optional so legacy and test supervisors keep the
// existing WorkerSupervisor contract unchanged.
type WorkerProvenanceWriter interface {
	WriteWorkerProvenance(runID string, provenance RunProvenance) error
}
