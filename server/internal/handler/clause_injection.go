package handler

// Claim-time dynamic clause injection (RUYI-751 / ADR 007 §5–§6).
//
// buildClaimedTaskResponse calls applyDynamicClauses at each of the four
// carrier read points (agent / squad / project / workspace). The normal path
// never parses carrier markers: the effective clause set comes straight from
// the asset library (active bindings + task_kind condition filter, ADR §4),
// and only the stable context is split off the business column. Two
// fallbacks feed the snapshot path, where only rendered clauses whose current
// active binding still targets the carrier are retained:
//
//  1. the global kill switch (feature flag clause_dynamic_injection_disabled,
//     ADR §5 defense 2) — fleet-wide stop-loss / rollout lever;
//  2. any computation failure — parse error, query error, unexpected shape
//     (ADR §5 defense 1, same philosophy as recordClaimedPromptVersions:
//     attribution and injection are observability, never a claim
//     precondition).
//
// Degraded snapshot runs still get counted: the snapshot's marker segments
// resolve to (clause, version) pairs recorded with degraded=true, so the
// governance view can separate filtered from unfiltered traffic. A snapshot
// that cannot be parsed is a counted MISS and contributes no text, preventing
// malformed stale markers from bypassing the active-binding check.

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/featureflags"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	"github.com/multica-ai/multica/server/pkg/clausemark"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Clause task kinds — the closed value domain binding conditions may
// whitelist (ADR 007 §1). A claim's kind is derived from which origin
// reference its queue row carries (see claimTaskKind).
var clauseTaskKinds = map[string]bool{
	"issue":        true,
	"chat":         true,
	"autopilot":    true,
	"quick_create": true,
}

// claimTaskKind maps a claimed queue row to its ADR 007 task kind. The
// origin columns are mutually exclusive by construction (enqueue paths set
// exactly one); the order below is just a defensive total order. Unknown
// shapes resolve to "" — binding conditions then behave as "no kind match",
// the same as an unknown kind under a whitelist.
func claimTaskKind(task *db.AgentTaskQueue) string {
	switch {
	case task.IssueID.Valid:
		return "issue"
	case task.ChatSessionID.Valid:
		return "chat"
	case task.AutopilotRunID.Valid:
		return "autopilot"
	}
	if len(task.Context) > 0 {
		var qc serviceQuickCreateContext
		if err := json.Unmarshal(task.Context, &qc); err == nil && qc.Type == service.QuickCreateContextType {
			return "quick_create"
		}
	}
	return ""
}

// serviceQuickCreateContext is the minimal shape of the enqueue-time context
// blob this file needs; the full struct lives in service. Kept local so the
// injection path stays decoupled from enqueue-side changes.
type serviceQuickCreateContext struct {
	Type string `json:"type"`
}

// clauseInjectionRecord is one pending clause_injection row produced by an
// applyDynamicClauses call, persisted once at the end of response assembly.
type clauseInjectionRecord struct {
	ClauseID      pgtype.UUID
	ClauseVersion int32
	Scope         string
	Degraded      bool
}

type clauseInjectionObservation struct {
	Scope    string
	ScopeID  pgtype.UUID
	Degraded bool
	Reason   string
}

// dynamicClauseResult is what one carrier read point yields: the text to
// inject and the injection rows it produced.
type dynamicClauseResult struct {
	Text         string
	Injections   []clauseInjectionRecord
	Observations []clauseInjectionObservation
}

// applyDynamicClauses filters one carrier business column down to the clause
// set effective for this task kind, returning the text to inject. Never
// fails: every failure mode degrades to the snapshot path and the claim
// proceeds (ADR 007 §5).
func (h *Handler) applyDynamicClauses(ctx context.Context, workspaceID pgtype.UUID, scope promptVersionScope, scopeID pgtype.UUID, taskKind, text string) dynamicClauseResult {
	degrade := func(reason string) dynamicClauseResult {
		fallbackText, injections := h.snapshotFallback(ctx, workspaceID, scope, scopeID, taskKind, text, reason)
		return dynamicClauseResult{
			Text:         fallbackText,
			Injections:   injections,
			Observations: []clauseInjectionObservation{{Scope: string(scope), ScopeID: scopeID, Degraded: true, Reason: reason}},
		}
	}

	if h.clauseDynamicInjectionDisabled(ctx) {
		return degrade("kill_switch")
	}

	set, err := h.Queries.ListEffectiveCarrierClauses(ctx, db.ListEffectiveCarrierClausesParams{
		Scope:    string(scope),
		ScopeID:  scopeID,
		TaskKind: taskKind,
	})
	if err != nil {
		slog.Warn("clause injection: effective set query failed; degrading to snapshot",
			"scope", string(scope), "error", err)
		return degrade("query_error")
	}
	doc, err := clausemark.Parse(text)
	if err != nil {
		// The carrier's rendered region is malformed, so the degraded path
		// will fail closed on its text.
		slog.Warn("clause injection: carrier snapshot parse failed; filtering degraded text",
			"scope", string(scope), "error", err)
		return degrade("parse_error")
	}

	rendered := make([]clausemark.RenderedClause, 0, len(set))
	injections := make([]clauseInjectionRecord, 0, len(set))
	for _, row := range set {
		rendered = append(rendered, clausemark.RenderedClause{
			Slug: row.Slug, Version: int(row.Version), Body: row.Content,
		})
		injections = append(injections, clauseInjectionRecord{
			ClauseID:      row.ClauseID,
			ClauseVersion: row.Version,
		})
	}
	return dynamicClauseResult{
		Text:       clausemark.Render(doc.Stable, rendered),
		Injections: injections,
	}
}

// snapshotFallbackInjections returns the injection accounting from a filtered
// degraded snapshot.
func (h *Handler) snapshotFallbackInjections(ctx context.Context, workspaceID pgtype.UUID, scope promptVersionScope, scopeID pgtype.UUID, taskKind, text, reason string) []clauseInjectionRecord {
	_, injections := h.snapshotFallback(ctx, workspaceID, scope, scopeID, taskKind, text, reason)
	return injections
}

// snapshotFallback keeps only marker segments whose clause and active binding
// still authorize injection from this carrier. A malformed snapshot fails
// closed because its segment boundaries cannot be trusted.
func (h *Handler) snapshotFallback(ctx context.Context, workspaceID pgtype.UUID, scope promptVersionScope, scopeID pgtype.UUID, taskKind, text, reason string) (string, []clauseInjectionRecord) {
	doc, err := clausemark.Parse(text)
	stableMarkers := strings.Join(strings.Fields(doc.Stable), "")
	if err != nil || strings.Contains(stableMarkers, "<!--clause") || strings.Contains(stableMarkers, "<!--/clause") {
		slog.Warn("clause injection: degraded run snapshot unparseable; clause-level count missing (reconciliation will flag)",
			"scope", string(scope), "reason", reason, "error", err)
		return "", nil
	}
	out := make([]clauseInjectionRecord, 0, len(doc.Segments))
	rendered := make([]clausemark.RenderedClause, 0, len(doc.Segments))
	for _, seg := range doc.Segments {
		clause, err := h.Queries.GetClauseBySlug(ctx, db.GetClauseBySlugParams{Slug: seg.Slug, WorkspaceID: workspaceID})
		if err != nil || clause.Status != "active" {
			slog.Warn("clause injection: snapshot segment slug not in asset library; count missing for it",
				"scope", string(scope), "slug", seg.Slug, "reason", reason)
			continue
		}
		binding, err := h.Queries.GetActiveClauseBinding(ctx, clause.ID)
		if err != nil {
			slog.Warn("clause injection: snapshot segment has no current active binding; skipping it",
				"scope", string(scope), "slug", seg.Slug, "reason", reason, "error", err)
			continue
		}
		if binding.Scope != string(scope) || binding.ScopeID != scopeID {
			continue
		}
		if !clauseBindingAllowsTaskKind(binding.Conditions, taskKind) {
			continue
		}
		rendered = append(rendered, clausemark.RenderedClause{Slug: seg.Slug, Version: seg.Rev, Body: seg.Body})
		out = append(out, clauseInjectionRecord{
			ClauseID:      clause.ID,
			ClauseVersion: int32(seg.Rev),
			Degraded:      true,
		})
	}
	return clausemark.Render(doc.Stable, rendered), out
}

func clauseBindingAllowsTaskKind(conditions []byte, taskKind string) bool {
	if len(conditions) == 0 {
		return true
	}
	var raw any
	if err := json.Unmarshal(conditions, &raw); err != nil {
		return false
	}
	object, ok := raw.(map[string]any)
	if !ok {
		return true
	}
	taskKinds, ok := object["task_kinds"]
	if !ok || taskKinds == nil {
		return true
	}
	allowed, ok := taskKinds.([]any)
	if !ok {
		return true
	}
	for _, value := range allowed {
		if allowedKind, ok := value.(string); ok && allowedKind == taskKind {
			return true
		}
	}
	return false
}

// recordClauseInjectionEvents persists one claim's injection rows and degraded
// carrier observations. Failures are logged and swallowed: attribution is
// observability, never a claim precondition. Injection rows are idempotent by
// task, clause, and version; observations are append-only per degraded claim.
// carrier_version is resolved per scope at write time — the version the
// carrier had when the run was injected.
func (h *Handler) recordClauseInjectionEvents(ctx context.Context, workspaceID, taskID pgtype.UUID, scopeIDs map[string]string, injections []clauseInjectionRecord, observations []clauseInjectionObservation) {
	for _, observation := range observations {
		if !observation.Degraded {
			continue
		}
		if err := h.Queries.InsertClauseInjectionObservation(ctx, db.InsertClauseInjectionObservationParams{
			WorkspaceID:       workspaceID,
			TaskID:            taskID,
			CarrierScope:      observation.Scope,
			CarrierScopeID:    observation.ScopeID,
			Degraded:          observation.Degraded,
			DegradationReason: observation.Reason,
		}); err != nil {
			slog.Warn("clause injection: observation persist failed (attribution is observability, not a claim precondition)",
				"task_id", uuidToString(taskID), "scope", observation.Scope, "error", err)
		}
	}
	if len(injections) == 0 {
		return
	}
	versions := map[string]pgtype.Int4{}
	for _, inj := range injections {
		if _, done := versions[inj.Scope]; done {
			continue
		}
		versions[inj.Scope] = pgtype.Int4{}
		parsed, err := util.ParseUUID(scopeIDs[inj.Scope])
		if err != nil {
			continue
		}
		v, err := h.Queries.GetLatestPromptVersion(ctx, db.GetLatestPromptVersionParams{
			Scope:   inj.Scope,
			ScopeID: parsed,
		})
		if err != nil {
			continue
		}
		versions[inj.Scope] = pgtype.Int4{Int32: v.Version, Valid: true}
	}
	for _, inj := range injections {
		if err := h.Queries.InsertClauseInjection(ctx, db.InsertClauseInjectionParams{
			WorkspaceID:    workspaceID,
			TaskID:         taskID,
			ClauseID:       inj.ClauseID,
			ClauseVersion:  inj.ClauseVersion,
			CarrierScope:   inj.Scope,
			CarrierVersion: versions[inj.Scope],
			Degraded:       inj.Degraded,
		}); err != nil {
			slog.Warn("clause injection: persist failed (attribution is observability, not a claim precondition)",
				"task_id", uuidToString(taskID), "clause_id", uuidToString(inj.ClauseID), "error", err)
		}
	}
}

// clauseDynamicInjectionDisabled reads the ADR 007 §5 global kill switch.
func (h *Handler) clauseDynamicInjectionDisabled(ctx context.Context) bool {
	return featureflags.ClauseDynamicInjectionDisabledEnabled(ctx, h.FeatureFlags)
}
