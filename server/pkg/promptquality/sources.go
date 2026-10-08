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
	// there or the request could not have been served. An error state
	// (configured but failing) is NOT degraded: the status card renders the
	// two differently.
	Degraded bool `json:"degraded"`

	// RUYI-551 four-state closed loop. Status is the source's health in the
	// Status* vocabulary; EffectiveSource says who serves it (module_config |
	// deploy_default | system); the validation fields are only meaningful
	// for the scoring model, where a module config is validated on save.
	Status          string `json:"status"`
	EffectiveSource string `json:"effective_source,omitempty"`
	Model           string `json:"model,omitempty"`
	LastValidatedAt string `json:"last_validated_at,omitempty"`
	ValidationError string `json:"validation_error,omitempty"`
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

// Status vocabulary for the four-state closed loop (RUYI-551 §2.5). The
// canonical constants live in internal/selfevconfig; these mirror the same
// wire values, which is what the API carries.
const (
	StatusOK           = "ok"
	StatusUnconfigured = "unconfigured"
	StatusError        = "error"
	StatusDisabled     = "disabled"

	// SourceSystem marks a source that is deployment-level, not
	// workspace-configurable (platform data, Langfuse export).
	SourceSystem = "system"
)

// ScoringSource is what the caller resolved for the scoring model in one
// workspace (RUYI-551): the four-state status, who serves it, and — for a
// module config — the recorded validation outcome.
type ScoringSource struct {
	Status          string
	EffectiveSource string
	Model           string
	LastValidatedAt string
	ValidationError string
}

// SourceFacts carries what the caller resolved per workspace. DescribeSources
// stays the single derivation point: facts in, renderable states out.
type SourceFacts struct {
	Scoring ScoringSource
}

// DescribeSources reports the state of each source. The scoring state comes
// from the workspace's model-service resolution (RUYI-551); Langfuse is read
// from the environment because it has no client in this repository — it is
// configured, not called.
func DescribeSources(facts SourceFacts) Sources {
	langfuseOn := LangfuseExportEnabled()

	scoring := facts.Scoring
	scoringOff := scoring.Status == StatusUnconfigured || scoring.Status == StatusDisabled
	items := []SourceState{
		{Kind: SourcePlatform, Available: true, Required: true, Status: StatusOK, EffectiveSource: SourceSystem},
		{
			Kind:            SourceScoring,
			Available:       !scoringOff,
			Required:        false,
			Degraded:        scoringOff,
			Status:          scoring.Status,
			EffectiveSource: scoring.EffectiveSource,
			Model:           scoring.Model,
			LastValidatedAt: scoring.LastValidatedAt,
			ValidationError: scoring.ValidationError,
		},
	}
	langfuseStatus := StatusUnconfigured
	if langfuseOn {
		langfuseStatus = StatusOK
	}
	items = append(items, SourceState{
		Kind:            SourceLangfuse,
		Available:       langfuseOn,
		Degraded:        !langfuseOn,
		Status:          langfuseStatus,
		EffectiveSource: SourceSystem,
	})
	return Sources{Items: items}
}

// LangfuseExportEnabled reports whether trace export is configured. Export is
// off by default and the whole dashboard works with it off; per the Q8 ruling
// an enabled export carries usage counts, version identifiers and annotation
// results only, never prompt bodies.
func LangfuseExportEnabled() bool {
	return strings.TrimSpace(os.Getenv("LANGFUSE_PUBLIC_KEY")) != "" &&
		strings.TrimSpace(os.Getenv("LANGFUSE_SECRET_KEY")) != ""
}
