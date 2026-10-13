package handler

// Clause governance views (RUYI-751, ADR 007 §9): the read path over the
// clause asset library under /api/prompt-legislation/governance.
//
// Three endpoints, all reads, all member-visible for the same reason the
// prompt quality dashboard is (visibility fine-tuning is the UI 单's call):
//
//   - GET /clauses        overview — asset set with window stats, migration
//     coverage, rollback suggestions, dynamic-path health (degraded share,
//     kill-switch state)
//   - GET /clauses/{id}   one clause's versions, bindings, proposals and
//     daily quality rows
//   - GET /reconciliation — the standing ADR-007 checks: 层集合 ⊆ violations
//     (clause_injection vs prompt_versions attribution), the degraded
//     injection share, and marker-vs-asset drift across carriers
//
// Nothing here recomputes quality: clause_quality_daily is the rollup's
// product and this path only reads it. The drift scan compares the markers
// physically present in each carrier's business column against the same
// full-union active set the re-render uses; it observes and reports, never
// rewrites.

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/pkg/clausemark"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const clauseGovernanceDefaultDays = 30

func clauseGovernanceWindow(days int) (pgtype.Timestamptz, pgtype.Date) {
	since := time.Now().AddDate(0, 0, -days)
	return pgtype.Timestamptz{Time: since, Valid: true},
		pgtype.Date{Time: since, Valid: true}
}

func clauseGovernanceDays(r *http.Request) int {
	if raw := r.URL.Query().Get("days"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 365 {
			return n
		}
	}
	return clauseGovernanceDefaultDays
}

// ── Overview ────────────────────────────────────────────────────────────────

type clauseGovernanceClause struct {
	ID            string `json:"id"`
	Slug          string `json:"slug"`
	Title         string `json:"title"`
	Status        string `json:"status"`
	LatestVersion int32  `json:"latest_version"`
	BoundScope    string `json:"bound_scope,omitempty"`
	BoundScopeID  string `json:"bound_scope_id,omitempty"`
	Injections    int64  `json:"injections"`
	Runs          int64  `json:"runs"`
	// InjectedTokens is the D1 window sum from the daily rollup. The rollup
	// writes NULL while a version has no content row; this list view folds
	// that to 0 and says so with TokensPending instead of faking precision.
	InjectedTokens int64 `json:"injected_tokens"`
	TokensPending  bool  `json:"tokens_pending"`
}

type clauseGovernanceTotals struct {
	Injections   int64 `json:"injections"`
	Runs         int64 `json:"runs"`
	DegradedRows int64 `json:"degraded_rows"`
	DegradedRuns int64 `json:"degraded_runs"`
	ClauseTokens int64 `json:"clause_tokens"`
}

type clauseGovernanceMigration struct {
	TotalCarriers int64            `json:"total_carriers"`
	BoundCarriers int64            `json:"bound_carriers"`
	ByScope       map[string]int64 `json:"by_scope"`
	ByScopeTotal  map[string]int64 `json:"by_scope_total"`
}

type clauseGovernanceOverview struct {
	WorkspaceID        string                   `json:"workspace_id"`
	Since              string                   `json:"since"`
	DynamicDisabled    bool                     `json:"dynamic_injection_disabled"`
	Clauses            []clauseGovernanceClause `json:"clauses"`
	StatusCounts       map[string]int64         `json:"status_counts"`
	Totals             clauseGovernanceTotals   `json:"totals"`
	Migration          clauseGovernanceMigration `json:"migration"`
	RollbackSuggestions []PromptProposalResponse `json:"rollback_suggestions"`
}

// ListClauseGovernance — GET /api/prompt-legislation/governance/clauses?days=N
func (h *Handler) ListClauseGovernance(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	workspaceID := parseUUID(h.resolveWorkspaceID(r))
	days := clauseGovernanceDays(r)
	since, sinceDay := clauseGovernanceWindow(days)

	rows, err := h.Queries.ListClauseOverview(ctx, workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list clauses")
		return
	}
	injStats, err := h.Queries.SumClauseInjectionStatsByClause(ctx, db.SumClauseInjectionStatsByClauseParams{
		WorkspaceID: workspaceID, Since: since,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to sum injection stats")
		return
	}
	tokStats, err := h.Queries.SumClauseTokensByClause(ctx, db.SumClauseTokensByClauseParams{
		WorkspaceID: workspaceID, SinceDay: sinceDay,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to sum clause tokens")
		return
	}
	injByClause := map[string]db.SumClauseInjectionStatsByClauseRow{}
	for _, s := range injStats {
		injByClause[uuidToString(s.ClauseID)] = s
	}
	tokByClause := map[string]int64{}
	for _, t := range tokStats {
		tokByClause[uuidToString(t.ClauseID)] = t.TotalTokens
	}

	clauses := make([]clauseGovernanceClause, 0, len(rows))
	statusCounts := map[string]int64{}
	for _, row := range rows {
		id := uuidToString(row.ID)
		statusCounts[row.Status]++
		entry := clauseGovernanceClause{
			ID:            id,
			Slug:          row.Slug,
			Title:         row.Title,
			Status:        row.Status,
			LatestVersion: row.LatestVersion,
		}
		if row.BoundScope.Valid {
			entry.BoundScope = row.BoundScope.String
			entry.BoundScopeID = uuidToString(row.BoundScopeID)
		}
		if s, ok := injByClause[id]; ok {
			entry.Injections, entry.Runs = s.Injections, s.Runs
		}
		if tok, ok := tokByClause[id]; ok {
			entry.InjectedTokens = tok
		} else if row.LatestVersion > 0 {
			entry.TokensPending = true
		}
		clauses = append(clauses, entry)
	}

	wsTotals, err := h.Queries.CountWorkspaceInjections(ctx, db.CountWorkspaceInjectionsParams{
		WorkspaceID: workspaceID, Since: since,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to count injections")
		return
	}
	wsTokens, err := h.Queries.SumWorkspaceClauseTokens(ctx, db.SumWorkspaceClauseTokensParams{
		WorkspaceID: workspaceID, SinceDay: sinceDay,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to sum clause tokens")
		return
	}

	boundRows, err := h.Queries.CountBoundCarriersByScope(ctx, workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to count bound carriers")
		return
	}
	byScope := map[string]int64{}
	boundTotal := int64(0)
	for _, b := range boundRows {
		byScope[b.Scope] = b.Carriers
		boundTotal += b.Carriers
	}

	carrierTotals, err := h.countGovernanceCarriers(ctx, workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to count carriers")
		return
	}

	rollbackRows, err := h.Queries.ListRollbackSuggestions(ctx, workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list rollback suggestions")
		return
	}
	rollback := make([]PromptProposalResponse, 0, len(rollbackRows))
	for _, p := range rollbackRows {
		rollback = append(rollback, proposalToResponse(p))
	}

	writeJSON(w, http.StatusOK, clauseGovernanceOverview{
		WorkspaceID:     uuidToString(workspaceID),
		Since:           since.Time.UTC().Format(time.RFC3339),
		DynamicDisabled: h.clauseDynamicInjectionDisabled(ctx),
		Clauses:         clauses,
		StatusCounts:    statusCounts,
		Totals: clauseGovernanceTotals{
			Injections:   wsTotals.Injections,
			Runs:         wsTotals.Runs,
			DegradedRows: wsTotals.DegradedRows,
			DegradedRuns: wsTotals.DegradedRuns,
			ClauseTokens: wsTokens,
		},
		Migration: clauseGovernanceMigration{
			TotalCarriers: carrierTotals.total,
			BoundCarriers: boundTotal,
			ByScope:       byScope,
			ByScopeTotal:  carrierTotals.byScope,
		},
		RollbackSuggestions: rollback,
	})
}

type governanceCarrierCounts struct {
	total   int64
	byScope map[string]int64
}

// countGovernanceCarriers sizes the migration-coverage denominator: one
// workspace-tier carrier, plus every live project, squad and agent business
// column. The rows double as the reconciliation drift scan's input, but this
// helper stays read-and-count only.
func (h *Handler) countGovernanceCarriers(ctx context.Context, workspaceID pgtype.UUID) (governanceCarrierCounts, error) {
	out := governanceCarrierCounts{byScope: map[string]int64{}}
	projects, err := h.Queries.ListProjects(ctx, db.ListProjectsParams{WorkspaceID: workspaceID})
	if err != nil {
		return out, err
	}
	squads, err := h.Queries.ListSquads(ctx, workspaceID)
	if err != nil {
		return out, err
	}
	agentRows, err := h.Queries.ListCarrierBusinessColumns(ctx, db.ListCarrierBusinessColumnsParams{
		WorkspaceID: workspaceID, RowLimit: 5000,
	})
	if err != nil {
		return out, err
	}
	out.byScope["workspace"] = 1
	out.byScope["project"] = int64(len(projects))
	out.byScope["squad"] = int64(len(squads))
	out.byScope["agent"] = int64(len(agentRows))
	for _, n := range out.byScope {
		out.total += n
	}
	return out, nil
}

// ── Detail ──────────────────────────────────────────────────────────────────

type clauseVersionEntry struct {
	Version       int32  `json:"version"`
	Content       string `json:"content"`
	ContentSha256 string `json:"content_sha256"`
	Source        string `json:"source"`
	CarrierScope  string `json:"carrier_scope"`
	CreatedByType string `json:"created_by_type"`
	CreatedAt     string `json:"created_at"`
}

type clauseBindingEntry struct {
	ID                  string          `json:"id"`
	Scope               string          `json:"scope"`
	ScopeID             string          `json:"scope_id"`
	Status              string          `json:"status"`
	Conditions          json.RawMessage `json:"conditions,omitempty"`
	SinceClauseVersion  int32           `json:"since_clause_version"`
	CreatedAt           string          `json:"created_at"`
}

type clauseQualityDayEntry struct {
	Day                    string `json:"day"`
	ClauseVersion          int32  `json:"clause_version"`
	FinishedRuns           int32  `json:"finished_runs"`
	InjectedTokens         *int64 `json:"injected_tokens"`
	RunTokensMedian        *int64 `json:"run_tokens_median"`
	AttributableFailedRuns int32  `json:"attributable_failed_runs"`
	ExcludedFailedRuns     int32  `json:"excluded_failed_runs"`
}

type clauseGovernanceDetail struct {
	ID            string                   `json:"id"`
	Slug          string                   `json:"slug"`
	Title         string                   `json:"title"`
	Status        string                   `json:"status"`
	LatestVersion int32                    `json:"latest_version"`
	Versions      []clauseVersionEntry     `json:"versions"`
	Bindings      []clauseBindingEntry     `json:"bindings"`
	Proposals     []PromptProposalResponse `json:"proposals"`
	Quality       []clauseQualityDayEntry  `json:"quality_daily"`
	Injections    int64                    `json:"injections"`
	Runs          int64                    `json:"runs"`
	InjectedTokens int64                   `json:"injected_tokens"`
}

// GetClauseGovernanceDetail — GET /api/prompt-legislation/governance/clauses/{clauseId}?days=N
func (h *Handler) GetClauseGovernanceDetail(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	workspaceID := parseUUID(h.resolveWorkspaceID(r))
	clauseID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "clauseId"), "clauseId")
	if !ok {
		return
	}
	days := clauseGovernanceDays(r)
	since, sinceDay := clauseGovernanceWindow(days)

	clause, err := h.Queries.GetClauseByID(ctx, db.GetClauseByIDParams{ID: clauseID, WorkspaceID: workspaceID})
	if err != nil {
		// A clause of another workspace is a 404, not a 403 — same
		// non-disclosure the scope checks use.
		writeError(w, http.StatusNotFound, "clause not found")
		return
	}

	versions, err := h.Queries.ListClauseVersions(ctx, clauseID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list versions")
		return
	}
	bindings, err := h.Queries.ListClauseBindings(ctx, clauseID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list bindings")
		return
	}
	proposals, err := h.Queries.ListClauseProposals(ctx, clauseID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list proposals")
		return
	}
	quality, err := h.Queries.ListClauseQualityDaily(ctx, db.ListClauseQualityDailyParams{
		ClauseID: clauseID, SinceDay: sinceDay,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list quality days")
		return
	}
	injStats, err := h.Queries.CountClauseInjections(ctx, db.CountClauseInjectionsParams{
		ClauseID: clauseID, Since: since,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to count injections")
		return
	}
	tokens, err := h.Queries.SumClauseInjectionTokens(ctx, db.SumClauseInjectionTokensParams{
		ClauseID: clauseID, SinceDay: sinceDay,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to sum tokens")
		return
	}

	versionEntries := make([]clauseVersionEntry, 0, len(versions))
	latest := int32(0)
	for _, v := range versions {
		if v.Version > latest {
			latest = v.Version
		}
		versionEntries = append(versionEntries, clauseVersionEntry{
			Version:       v.Version,
			Content:       v.Content,
			ContentSha256: v.ContentSha256,
			Source:        v.Source,
			CarrierScope:  v.CarrierScope,
			CreatedByType: v.CreatedByType,
			CreatedAt:     v.CreatedAt.Time.UTC().Format(time.RFC3339),
		})
	}
	bindingEntries := make([]clauseBindingEntry, 0, len(bindings))
	for _, b := range bindings {
		entry := clauseBindingEntry{
			ID:                 uuidToString(b.ID),
			Scope:              b.Scope,
			ScopeID:            uuidToString(b.ScopeID),
			Status:             b.Status,
			SinceClauseVersion: b.SinceClauseVersion,
			CreatedAt:          b.CreatedAt.Time.UTC().Format(time.RFC3339),
		}
		if len(b.Conditions) > 0 {
			entry.Conditions = json.RawMessage(b.Conditions)
		}
		bindingEntries = append(bindingEntries, entry)
	}
	proposalEntries := make([]PromptProposalResponse, 0, len(proposals))
	for _, p := range proposals {
		proposalEntries = append(proposalEntries, proposalToResponse(p))
	}
	qualityEntries := make([]clauseQualityDayEntry, 0, len(quality))
	for _, q := range quality {
		entry := clauseQualityDayEntry{
			Day:                    q.Day.Time.Format("2006-01-02"),
			ClauseVersion:          q.ClauseVersion,
			FinishedRuns:           q.FinishedRuns,
			AttributableFailedRuns: q.AttributableFailedRuns,
			ExcludedFailedRuns:     q.ExcludedFailedRuns,
		}
		if q.InjectedTokens.Valid {
			v := q.InjectedTokens.Int64
			entry.InjectedTokens = &v
		}
		if q.RunTokensMedian.Valid {
			v := q.RunTokensMedian.Int64
			entry.RunTokensMedian = &v
		}
		qualityEntries = append(qualityEntries, entry)
	}

	writeJSON(w, http.StatusOK, clauseGovernanceDetail{
		ID:             uuidToString(clause.ID),
		Slug:           clause.Slug,
		Title:          clause.Title,
		Status:         clause.Status,
		LatestVersion:  latest,
		Versions:       versionEntries,
		Bindings:       bindingEntries,
		Proposals:      proposalEntries,
		Quality:        qualityEntries,
		Injections:     injStats.Injections,
		Runs:           injStats.Runs,
		InjectedTokens: tokens,
	})
}

// ── Reconciliation (常设对账观测, ADR 007 QA plan) ─────────────────────────

type clauseLayerViolation struct {
	TaskID        string `json:"task_id"`
	ClauseID      string `json:"clause_id"`
	ClauseVersion int32  `json:"clause_version"`
	CarrierScope  string `json:"carrier_scope"`
	Degraded      bool   `json:"degraded"`
	CreatedAt     string `json:"created_at"`
}

type clauseDriftEntry struct {
	// Kind: stale_rev (marker behind the asset), orphan_marker (no live
	// bound clause), missing_segment (bound clause without a marker).
	Kind        string `json:"kind"`
	Slug        string `json:"slug"`
	MarkerRev   int    `json:"marker_rev,omitempty"`
	ExpectedRev int    `json:"expected_rev,omitempty"`
}

type clauseDriftCarrier struct {
	Scope   string            `json:"scope"`
	ScopeID string            `json:"scope_id"`
	Entries []clauseDriftEntry `json:"entries"`
}

type clauseReconciliation struct {
	Since             string                 `json:"since"`
	Healthy           bool                   `json:"healthy"`
	LayerSetViolations []clauseLayerViolation `json:"layer_set_violations"`
	Totals            clauseGovernanceTotals `json:"totals"`
	DriftCarriers     []clauseDriftCarrier   `json:"drift"`
	CarriersScanned   int                    `json:"carriers_scanned"`
}

// GetClauseReconciliation — GET /api/prompt-legislation/governance/reconciliation?days=N
//
// The three standing checks in one read. Findings are WARN-logged so they
// surface in the ops log stream even when nobody is looking at the endpoint;
// the response is the auditable artifact.
func (h *Handler) GetClauseReconciliation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	workspaceID := parseUUID(h.resolveWorkspaceID(r))
	days := clauseGovernanceDays(r)
	since, sinceDay := clauseGovernanceWindow(days)

	violationRows, err := h.Queries.ListClauseInjectionLayerViolations(ctx, db.ListClauseInjectionLayerViolationsParams{
		WorkspaceID: workspaceID, Since: since, RowLimit: 100,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to check layer invariant")
		return
	}
	violations := make([]clauseLayerViolation, 0, len(violationRows))
	for _, v := range violationRows {
		violations = append(violations, clauseLayerViolation{
			TaskID:        uuidToString(v.TaskID),
			ClauseID:      uuidToString(v.ClauseID),
			ClauseVersion: v.ClauseVersion,
			CarrierScope:  v.CarrierScope,
			Degraded:      v.Degraded,
			CreatedAt:     v.CreatedAt.Time.UTC().Format(time.RFC3339),
		})
	}

	wsTotals, err := h.Queries.CountWorkspaceInjections(ctx, db.CountWorkspaceInjectionsParams{
		WorkspaceID: workspaceID, Since: since,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to count injections")
		return
	}
	wsTokens, err := h.Queries.SumWorkspaceClauseTokens(ctx, db.SumWorkspaceClauseTokensParams{
		WorkspaceID: workspaceID, SinceDay: sinceDay,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to sum clause tokens")
		return
	}

	drift, scanned, err := h.scanClauseDrift(ctx, workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to scan carrier drift")
		return
	}

	healthy := len(violations) == 0 && len(drift) == 0
	if !healthy {
		slog.Warn("clause reconciliation: invariant or drift findings",
			"workspace_id", uuidToString(workspaceID),
			"layer_violations", len(violations),
			"drift_carriers", len(drift))
	}

	writeJSON(w, http.StatusOK, clauseReconciliation{
		Since: since.Time.UTC().Format(time.RFC3339),
		Healthy: healthy,
		LayerSetViolations: violations,
		Totals: clauseGovernanceTotals{
			Injections:   wsTotals.Injections,
			Runs:         wsTotals.Runs,
			DegradedRows: wsTotals.DegradedRows,
			DegradedRuns: wsTotals.DegradedRuns,
			ClauseTokens: wsTokens,
		},
		DriftCarriers:   drift,
		CarriersScanned: scanned,
	})
}

// scanClauseDrift compares every non-empty carrier business column's clause
// markers against the workspace's active asset set — the same full-union set
// the re-render builds from, so "in sync" here means "a re-render would be a
// no-op".
func (h *Handler) scanClauseDrift(ctx context.Context, workspaceID pgtype.UUID) ([]clauseDriftCarrier, int, error) {
	activeRows, err := h.Queries.ListActiveWorkspaceClauses(ctx, workspaceID)
	if err != nil {
		return nil, 0, err
	}
	expected := map[string]map[string]int{}
	for _, row := range activeRows {
		if expected[row.Scope] == nil {
			expected[row.Scope] = map[string]int{}
		}
		expected[row.Scope][row.Slug] = int(row.Version)
	}

	type driftCarrier struct {
		scope string
		id    pgtype.UUID
		text  string
	}
	carriers := []driftCarrier{}

	ws, err := h.Queries.GetWorkspace(ctx, workspaceID)
	if err == nil && ws.Context.Valid && ws.Context.String != "" {
		carriers = append(carriers, driftCarrier{"workspace", workspaceID, ws.Context.String})
	}
	projects, err := h.Queries.ListProjects(ctx, db.ListProjectsParams{WorkspaceID: workspaceID})
	if err != nil {
		return nil, 0, err
	}
	for _, p := range projects {
		if p.Instructions.Valid && strings.TrimSpace(p.Instructions.String) != "" {
			carriers = append(carriers, driftCarrier{"project", p.ID, p.Instructions.String})
		}
	}
	squads, err := h.Queries.ListSquads(ctx, workspaceID)
	if err != nil {
		return nil, 0, err
	}
	for _, s := range squads {
		if strings.TrimSpace(s.Instructions) != "" {
			carriers = append(carriers, driftCarrier{"squad", s.ID, s.Instructions})
		}
	}
	agentRows, err := h.Queries.ListCarrierBusinessColumns(ctx, db.ListCarrierBusinessColumnsParams{
		WorkspaceID: workspaceID, RowLimit: 5000,
	})
	if err != nil {
		return nil, 0, err
	}
	for _, a := range agentRows {
		if strings.TrimSpace(a.BusinessColumn) != "" {
			carriers = append(carriers, driftCarrier{a.Scope, a.ScopeID, a.BusinessColumn})
		}
	}

	out := []clauseDriftCarrier{}
	for _, c := range carriers {
		markers := clausemark.ScanMarkers(c.text)
		want := expected[c.scope]
		var entries []clauseDriftEntry
		seen := map[string]bool{}
		for _, mk := range markers {
			seen[mk.Slug] = true
			exp, ok := want[mk.Slug]
			if !ok {
				entries = append(entries, clauseDriftEntry{Kind: "orphan_marker", Slug: mk.Slug, MarkerRev: mk.Rev})
			} else if mk.Rev != exp {
				entries = append(entries, clauseDriftEntry{Kind: "stale_rev", Slug: mk.Slug, MarkerRev: mk.Rev, ExpectedRev: exp})
			}
		}
		for slug, exp := range want {
			if !seen[slug] {
				entries = append(entries, clauseDriftEntry{Kind: "missing_segment", Slug: slug, ExpectedRev: exp})
			}
		}
		if len(entries) > 0 {
			out = append(out, clauseDriftCarrier{
				Scope:   c.scope,
				ScopeID: uuidToString(c.id),
				Entries: entries,
			})
		}
	}
	return out, len(carriers), nil
}
