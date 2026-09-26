package promptquality

import (
	"os"
	"strings"
)

// Where the seven dimensions get their numbers, and what the dashboard says
// when one of those places is switched off (RUYI-184 T3).
//
// The rule the acceptance criterion states is that no dimension and no part of
// the dashboard may depend on an external service. This file is how that rule
// is made checkable rather than merely intended: every source declares whether
// it is required, and a test asserts that the only required ones are the
// platform's own tables. Turning Langfuse off can therefore change a footer
// label and nothing else — there is no code path where its absence removes a
// card, because no card names it as a source.

// SourceKind identifies one place data comes from.
type SourceKind string

const (
	// SourcePlatform is the run stream: agent_task_queue, task_usage,
	// task_message. Always present; it is the same database serving the
	// request.
	SourcePlatform SourceKind = "platform"

	// SourceScoring is the LLM used for D3. Absent when no model is
	// configured, which leaves D3 unscored and the other six untouched.
	SourceScoring SourceKind = "scoring_model"

	// SourceLangfuse is the optional trace export. Nothing reads from it:
	// it is a destination, declared here so the dashboard can say whether
	// exporting is on without implying the numbers came from there.
	SourceLangfuse SourceKind = "langfuse"
)

// SourceState is one source as the API reports it.
type SourceState struct {
	Kind SourceKind `json:"kind"`

	// Available is whether the source is configured and reachable-by-config.
	Available bool `json:"available"`

	// Required is whether the dashboard needs it. Only the platform's own
	// data is required; everything else degrades to a label.
	Required bool `json:"required"`

	// Degraded marks a source that is off but optional, which is what the
	// footer renders. A required source is never "degraded" — it is either
	// there or the request could not have been served.
	Degraded bool `json:"degraded"`
}

// Sources is the status block the response carries.
type Sources struct {
	Items []SourceState `json:"items"`
}

// Degraded reports whether any optional source is switched off. The footer
// shows a low-key line when this is true; no card changes state.
func (s Sources) Degraded() bool {
	for _, item := range s.Items {
		if item.Degraded {
			return true
		}
	}
	return false
}

// ByKind looks one source up.
func (s Sources) ByKind(k SourceKind) (SourceState, bool) {
	for _, item := range s.Items {
		if item.Kind == k {
			return item, true
		}
	}
	return SourceState{}, false
}

// DescribeSources reports the state of each source. scoringEnabled comes from
// the LLM client; Langfuse is read from the environment because it has no
// client in this repository — it is configured, not called.
func DescribeSources(scoringEnabled bool) Sources {
	langfuse := LangfuseExportEnabled()
	return Sources{Items: []SourceState{
		{Kind: SourcePlatform, Available: true, Required: true},
		{Kind: SourceScoring, Available: scoringEnabled, Degraded: !scoringEnabled},
		{Kind: SourceLangfuse, Available: langfuse, Degraded: !langfuse},
	}}
}

// LangfuseExportEnabled reports whether trace export is configured. Export is
// off by default and the whole dashboard works with it off; per the Q8 ruling
// an enabled export carries usage counts, version identifiers and annotation
// results only, never prompt bodies.
func LangfuseExportEnabled() bool {
	return strings.TrimSpace(os.Getenv("LANGFUSE_PUBLIC_KEY")) != "" &&
		strings.TrimSpace(os.Getenv("LANGFUSE_SECRET_KEY")) != ""
}
