package handler

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/promptquality"
)

// Prompt quality dashboard read path (RUYI-184, self-evolution phase 2).
//
// It hangs off the same /api/prompt-governance/{scope}/{scopeId} tree as the
// version history, because it answers a question about those versions and
// because the workspace-scope check there is the one this needs: a member of
// the workspace may read, and a scope id belonging to another workspace is a
// 404 before any row is touched (locklessScopeCheck).
//
// Nothing is computed here. The rollup (internal/promptqualityrollup) has
// already folded each day into prompt_quality_daily storing counts rather than
// rates, and pkg/promptquality decides what a count may be rendered as. This
// handler reads rows, groups them by version, and hands each group to
// Combine + Present. The "no data" and "insufficient sample" states arrive
// from that package and are passed through untouched — there is no branch here
// that could substitute a zero.

// promptQualityMaxDays bounds the window. Half a year of daily rows for one
// scope is a few hundred rows; beyond that the timeline stops being readable
// before the query stops being cheap.
const promptQualityMaxDays = 180

const promptQualityDefaultDays = 30

// PromptQualityMeasuresResponse is one version's seven cards plus the span the
// numbers cover. Version 0 is used for the window aggregate across versions.
type PromptQualityMeasuresResponse struct {
	Version  int32                      `json:"version"`
	Days     int                        `json:"days"`
	FirstDay string                     `json:"first_day,omitempty"`
	LastDay  string                     `json:"last_day,omitempty"`
	Runs     int                        `json:"runs"`
	Measures promptquality.Presentation `json:"measures"`
	Reasons  map[string]int             `json:"failure_reasons"`
	Excluded int                        `json:"excluded_failed_runs"`
}

// PromptQualityPerplexityResponse is one D3 score. There is one per runtime
// profile per version and they are never merged: the profiles are scored
// against different assembled documents, so an average would describe a prompt
// no run ever receives (Owner Q17).
type PromptQualityPerplexityResponse struct {
	Version        int32           `json:"version"`
	RuntimeProfile string          `json:"runtime_profile"`
	Band           string          `json:"band"`
	PercentLow     *float64        `json:"percent_low"`
	PercentHigh    *float64        `json:"percent_high"`
	Evidence       json.RawMessage `json:"evidence"`
	Model          string          `json:"model"`
	ScoredAt       string          `json:"scored_at"`
}

// PromptQualityResponse is the dashboard payload.
type PromptQualityResponse struct {
	Scope   string `json:"scope"`
	ScopeID string `json:"scope_id"`
	Since   string `json:"since"`

	// Window is every version in the range folded together, for the headline
	// row. Versions is the same data split per version, which is what the
	// timeline and the cross-version comparison read.
	Window   PromptQualityMeasuresResponse   `json:"window"`
	Versions []PromptQualityMeasuresResponse `json:"versions"`

	// Perplexity is empty when D3 was never scored for this scope. Empty is
	// the honest answer: the scorer writes no row when it refuses, so there is
	// nothing here to distinguish from a neutral band.
	Perplexity []PromptQualityPerplexityResponse `json:"perplexity"`

	// DataSources reports which inputs are switched on. Its only effect on the
	// UI is a footer label — see promptquality.DescribeSources for why no card
	// may depend on it (T3).
	DataSources promptQualitySourcesResponse `json:"data_sources"`
}

type promptQualitySourcesResponse struct {
	Degraded bool                        `json:"degraded"`
	Items    []promptquality.SourceState `json:"items"`
}

// GetPromptQualityDashboard — GET /api/prompt-governance/{scope}/{scopeId}/quality?days=N
func (h *Handler) GetPromptQualityDashboard(w http.ResponseWriter, r *http.Request) {
	scope, ok := parsePromptVersionScope(chi.URLParam(r, "scope"))
	if !ok {
		writeError(w, http.StatusBadRequest, "unknown scope")
		return
	}
	scopeID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "scopeId"), "scope_id")
	if !ok {
		return
	}
	workspaceID := parseUUID(h.resolveWorkspaceID(r))

	if !h.locklessScopeCheck(w, r, scope, workspaceID, scopeID) {
		return
	}

	days := promptQualityDefaultDays
	if v := r.URL.Query().Get("days"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= promptQualityMaxDays {
			days = n
		}
	}
	since := time.Now().UTC().AddDate(0, 0, -(days - 1)).Truncate(24 * time.Hour)

	rows, err := h.Queries.ListPromptQualityDaily(r.Context(), db.ListPromptQualityDailyParams{
		Scope:   string(scope),
		ScopeID: scopeID,
		Since:   pgtype.Date{Time: since, Valid: true},
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read prompt quality")
		return
	}

	scores, err := h.Queries.ListPromptPerplexityScores(r.Context(), db.ListPromptPerplexityScoresParams{
		Scope:   string(scope),
		ScopeID: scopeID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read prompt quality")
		return
	}

	resp := PromptQualityResponse{
		Scope:       string(scope),
		ScopeID:     uuidToString(scopeID),
		Since:       since.Format("2006-01-02"),
		Versions:    promptQualityByVersion(rows),
		Perplexity:  promptQualityScoresToResponse(scores),
		DataSources: h.promptQualitySources(),
	}

	all := make([]promptquality.Result, 0, len(rows))
	for _, row := range rows {
		all = append(all, promptQualityRowToResult(row))
	}
	resp.Window = promptQualityMeasures(0, rows, all)

	writeJSON(w, http.StatusOK, resp)
}

// promptQualitySources reports the optional inputs' state. The scoring model
// is read from the handler's LLM client rather than the environment so the
// answer matches what the scorer would actually do if asked right now.
func (h *Handler) promptQualitySources() promptQualitySourcesResponse {
	scoring := h.LLM != nil && h.LLM.Enabled()
	sources := promptquality.DescribeSources(scoring)
	return promptQualitySourcesResponse{Degraded: sources.Degraded(), Items: sources.Items}
}

// promptQualityByVersion splits the window per version, newest first. The
// dashboard's timeline is a version axis, not a date axis: a prompt edit is the
// event that can move these numbers, so a version's days belong together even
// when they are not contiguous.
func promptQualityByVersion(rows []db.PromptQualityDaily) []PromptQualityMeasuresResponse {
	grouped := map[int32][]db.PromptQualityDaily{}
	order := []int32{}
	for _, row := range rows {
		if _, seen := grouped[row.Version]; !seen {
			order = append(order, row.Version)
		}
		grouped[row.Version] = append(grouped[row.Version], row)
	}
	sort.Slice(order, func(i, j int) bool { return order[i] > order[j] })

	out := make([]PromptQualityMeasuresResponse, 0, len(order))
	for _, version := range order {
		group := grouped[version]
		results := make([]promptquality.Result, 0, len(group))
		for _, row := range group {
			results = append(results, promptQualityRowToResult(row))
		}
		out = append(out, promptQualityMeasures(version, group, results))
	}
	return out
}

func promptQualityMeasures(version int32, rows []db.PromptQualityDaily, results []promptquality.Result) PromptQualityMeasuresResponse {
	combined := promptquality.Combine(results)
	out := PromptQualityMeasuresResponse{
		Version:  version,
		Days:     len(rows),
		Runs:     combined.FinishedRuns,
		Measures: promptquality.Present(combined),
		Reasons:  combined.FailureReasonCounts,
		Excluded: combined.ExcludedFailedRuns,
	}
	for _, row := range rows {
		if !row.Day.Valid {
			continue
		}
		day := row.Day.Time.UTC().Format("2006-01-02")
		if out.FirstDay == "" || day < out.FirstDay {
			out.FirstDay = day
		}
		if day > out.LastDay {
			out.LastDay = day
		}
	}
	return out
}

// promptQualityRowToResult reverses the rollup's write. Every nullable column
// becomes a nil pointer rather than a zero, which is the whole reason those
// columns are nullable (T5).
func promptQualityRowToResult(row db.PromptQualityDaily) promptquality.Result {
	out := promptquality.Result{
		FinishedRuns:           int(row.FinishedRuns),
		DisciplineCoveredRuns:  int(row.DisciplineCoveredRuns),
		ToolResultsMeasured:    row.ToolResultsMeasured,
		ToolResultsError:       row.ToolResultsError,
		AttemptTotal:           int(row.AttemptTotal),
		RetriedRuns:            int(row.RetriedRuns),
		AttributableFailedRuns: int(row.AttributableFailedRuns),
		ExcludedFailedRuns:     int(row.ExcludedFailedRuns),
		FailureReasonCounts:    map[string]int{},
		FirstPassIssues:        int(row.FirstPassIssues),
		ReviewedIssues:         int(row.ReviewedIssues),
	}
	if row.InjectedTokens.Valid {
		v := row.InjectedTokens.Int64
		out.InjectedTokens = &v
	}
	if row.RunTokensMedian.Valid {
		v := row.RunTokensMedian.Int64
		out.RunTokensMedian = &v
	}
	if v, ok := numericToFloat(row.DisciplineScoreMedian); ok {
		n := int(*v)
		out.DisciplineScoreMedian = &n
	}
	if len(row.FailureReasonCounts) > 0 {
		// A malformed counts blob leaves the map empty rather than failing the
		// request: D6's headline number lives in its own column, and losing the
		// breakdown is not a reason to blank the other six cards.
		_ = json.Unmarshal(row.FailureReasonCounts, &out.FailureReasonCounts)
	}
	return out
}

func promptQualityScoresToResponse(scores []db.PromptPerplexityScore) []PromptQualityPerplexityResponse {
	out := make([]PromptQualityPerplexityResponse, 0, len(scores))
	for _, s := range scores {
		item := PromptQualityPerplexityResponse{
			Version:        s.Version,
			RuntimeProfile: s.RuntimeProfile,
			Band:           s.Band,
			PercentLow:     firstOrNil(numericToFloat(s.PercentLow)),
			PercentHigh:    firstOrNil(numericToFloat(s.PercentHigh)),
			Evidence:       json.RawMessage(s.Evidence),
			Model:          s.Model,
		}
		if len(item.Evidence) == 0 {
			item.Evidence = json.RawMessage("[]")
		}
		if s.ScoredAt.Valid {
			item.ScoredAt = s.ScoredAt.Time.UTC().Format(httpTimeFormat)
		}
		out = append(out, item)
	}
	return out
}

func numericToFloat(n pgtype.Numeric) (*float64, bool) {
	if !n.Valid {
		return nil, false
	}
	f, err := n.Float64Value()
	if err != nil || !f.Valid {
		return nil, false
	}
	v := f.Float64
	return &v, true
}

func firstOrNil(v *float64, _ bool) *float64 { return v }
