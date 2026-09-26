package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/promptquiz"
)

// Quiz bank maintenance and the regression reading (RUYI-185, self-evolution
// phase 3).
//
// NOTHING IN THIS FILE IS A GATE. Every endpoint here reads or writes quiz
// data; none of it is consulted by the prompt publish path, and the publish
// path imports nothing from pkg/promptquiz. A quiz whose every measurement
// failed, a version with no measurements at all, and a sweep that has been
// stuck for days all leave version publishing exactly as it was (Owner Q10),
// which prompt_quiz_publish_test.go asserts mechanically rather than by
// convention.
//
// Bank writes are Owner-only for the same reason prompt version writes are:
// a question body is free text that ends up in an agent's context.

// PromptQuizItemResponse is the wire shape of one bank entry.
type PromptQuizItemResponse struct {
	ID             string `json:"id"`
	Slug           string `json:"slug"`
	Title          string `json:"title"`
	Body           string `json:"body"`
	Revision       int32  `json:"revision"`
	RuntimeProfile string `json:"runtime_profile"`
	Active         bool   `json:"active"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

func promptQuizItemToResponse(item db.PromptQuizItem) PromptQuizItemResponse {
	return PromptQuizItemResponse{
		ID:             uuidToString(item.ID),
		Slug:           item.Slug,
		Title:          item.Title,
		Body:           item.Body,
		Revision:       item.Revision,
		RuntimeProfile: item.RuntimeProfile,
		Active:         item.Active,
		CreatedAt:      formatQuizTime(item.CreatedAt),
		UpdatedAt:      formatQuizTime(item.UpdatedAt),
	}
}

func formatQuizTime(ts pgtype.Timestamptz) string {
	if !ts.Valid {
		return ""
	}
	return ts.Time.UTC().Format(httpTimeFormat)
}

type promptQuizItemWrite struct {
	Slug           string `json:"slug"`
	Title          string `json:"title"`
	Body           string `json:"body"`
	RuntimeProfile string `json:"runtime_profile"`
	Active         *bool  `json:"active"`
}

func (pw promptQuizItemWrite) profile() string {
	if pw.RuntimeProfile == "leader_task" {
		return "leader_task"
	}
	return "member"
}

// decodePromptQuizItemWrite reads the body and applies the isolation gate.
//
// The gate's refusal is returned verbatim to the author: it names the kind of
// reference and its offset, and never echoes the matched text, so the error is
// actionable without widening the disclosure surface of a workspace-private
// body (Owner Q8).
func decodePromptQuizItemWrite(w http.ResponseWriter, r *http.Request) (promptQuizItemWrite, bool) {
	var pw promptQuizItemWrite
	if err := json.NewDecoder(r.Body).Decode(&pw); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return pw, false
	}
	if pw.Title == "" {
		writeError(w, http.StatusBadRequest, "title must not be empty")
		return pw, false
	}
	if v := promptquiz.Validate(pw.Body); !v.OK() {
		writeError(w, http.StatusBadRequest, v.Reason())
		return pw, false
	}
	return pw, true
}

// ListPromptQuizItems — GET /api/prompt-quiz/items
func (h *Handler) ListPromptQuizItems(w http.ResponseWriter, r *http.Request) {
	workspaceID := parseUUID(h.resolveWorkspaceID(r))
	items, err := h.Queries.ListPromptQuizItems(r.Context(), db.ListPromptQuizItemsParams{
		WorkspaceID: workspaceID,
		ActiveOnly:  r.URL.Query().Get("active") == "true",
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read the quiz bank")
		return
	}
	out := make([]PromptQuizItemResponse, 0, len(items))
	for _, item := range items {
		out = append(out, promptQuizItemToResponse(item))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

// CreatePromptQuizItem — POST /api/prompt-quiz/items
func (h *Handler) CreatePromptQuizItem(w http.ResponseWriter, r *http.Request) {
	pw, ok := decodePromptQuizItemWrite(w, r)
	if !ok {
		return
	}
	if pw.Slug == "" {
		writeError(w, http.StatusBadRequest, "slug must not be empty")
		return
	}
	workspaceID := parseUUID(h.resolveWorkspaceID(r))

	params := db.CreatePromptQuizItemParams{
		WorkspaceID:    workspaceID,
		Slug:           pw.Slug,
		Title:          pw.Title,
		Body:           pw.Body,
		RuntimeProfile: pw.profile(),
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	params.CreatedByUserID = parseUUID(userID)

	item, err := h.Queries.CreatePromptQuizItem(r.Context(), params)
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "a question with this slug already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to create the question")
		return
	}
	writeJSON(w, http.StatusOK, promptQuizItemToResponse(item))
}

// UpdatePromptQuizItem — PATCH /api/prompt-quiz/items/{itemId}
func (h *Handler) UpdatePromptQuizItem(w http.ResponseWriter, r *http.Request) {
	itemID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "itemId"), "item_id")
	if !ok {
		return
	}
	pw, ok := decodePromptQuizItemWrite(w, r)
	if !ok {
		return
	}
	active := true
	if pw.Active != nil {
		active = *pw.Active
	}

	item, err := h.Queries.UpdatePromptQuizItem(r.Context(), db.UpdatePromptQuizItemParams{
		ID:             itemID,
		WorkspaceID:    parseUUID(h.resolveWorkspaceID(r)),
		Title:          pw.Title,
		Body:           pw.Body,
		RuntimeProfile: pw.profile(),
		Active:         active,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "question not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to update the question")
		return
	}
	writeJSON(w, http.StatusOK, promptQuizItemToResponse(item))
}

// DeletePromptQuizItem — DELETE /api/prompt-quiz/items/{itemId}
//
// A hard delete. Measurements taken against the item keep their item_revision
// and item_body_sha256, so past readings stay interpretable; what is lost is
// the ability to take new ones, which is what deleting a question means.
// Retiring (active=false) is the reversible option and is what the UI offers
// first.
func (h *Handler) DeletePromptQuizItem(w http.ResponseWriter, r *http.Request) {
	itemID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "itemId"), "item_id")
	if !ok {
		return
	}
	rows, err := h.Queries.DeletePromptQuizItem(r.Context(), db.DeletePromptQuizItemParams{
		ID:          itemID,
		WorkspaceID: parseUUID(h.resolveWorkspaceID(r)),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete the question")
		return
	}
	if rows == 0 {
		writeError(w, http.StatusNotFound, "question not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// PromptQuizBaselineResponse is the regression reading for one scope.
//
// Every input to the verdict travels with it — both group summaries, the
// statistic, the threshold — so a reader can recompute the call rather than
// trust the label. sample_size / baseline_size are echoed for the same reason:
// "insufficient" has to be legible as "not enough repeats yet", never as a
// quiet zero.
type PromptQuizBaselineResponse struct {
	Scope           string                 `json:"scope"`
	ScopeID         string                 `json:"scope_id"`
	CurrentVersion  int32                  `json:"current_version"`
	BaselineVersion int32                  `json:"baseline_version"`
	RequiredSample  int                    `json:"required_sample"`
	RequiredBase    int                    `json:"required_baseline"`
	Comparison      *promptquiz.Comparison `json:"comparison,omitempty"`
	// Current is the version's own group, sent whether or not a comparison
	// exists. A scope's first version has nothing to regress against but its
	// distribution is still the thing the next version will be read against,
	// and without this field a reader could not tell "first version, 12 runs
	// recorded" from "nothing measured".
	Current  promptquiz.Summary `json:"current"`
	Measured bool               `json:"measured"`
	Outcomes map[string]int     `json:"outcomes"`
}

// GetPromptQuizBaseline — GET /api/prompt-governance/{scope}/{scopeId}/quiz
//
// Reports the distribution comparison between the two newest measured versions
// of a scope. Read-only, member-visible, and entirely absent from the publish
// path.
func (h *Handler) GetPromptQuizBaseline(w http.ResponseWriter, r *http.Request) {
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

	current, err := h.Queries.GetLatestPromptVersion(r.Context(), db.GetLatestPromptVersionParams{
		Scope:   string(scope),
		ScopeID: scopeID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// No version, therefore nothing to compare. An empty reading, not
			// an error and not a zero score.
			writeJSON(w, http.StatusOK, PromptQuizBaselineResponse{
				Scope: string(scope), ScopeID: uuidToString(scopeID),
				RequiredSample: promptquiz.NewVersionSampleSize,
				RequiredBase:   promptquiz.BaselineSampleSize,
				Outcomes:       map[string]int{},
			})
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to read the prompt version")
		return
	}

	resp := PromptQuizBaselineResponse{
		Scope:          string(scope),
		ScopeID:        uuidToString(scopeID),
		CurrentVersion: current.Version,
		RequiredSample: promptquiz.NewVersionSampleSize,
		RequiredBase:   promptquiz.BaselineSampleSize,
		Outcomes:       map[string]int{},
	}

	currentSample, outcomes, err := h.readQuizSample(r, scope, scopeID, current.Version)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read quiz measurements")
		return
	}
	resp.Outcomes = outcomes
	resp.Current = promptquiz.Summarize(currentSample)
	resp.Measured = len(currentSample.Values) > 0

	if current.Version > 1 {
		resp.BaselineVersion = current.Version - 1
		baseSample, _, err := h.readQuizSample(r, scope, scopeID, resp.BaselineVersion)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to read quiz measurements")
			return
		}
		cmp := promptquiz.Compare(baseSample, currentSample)
		resp.Comparison = &cmp
	}
	writeJSON(w, http.StatusOK, resp)
}

// quizSampleLimit bounds one read. Generous relative to the required group
// sizes so accumulation past them is still visible, bounded so a long-lived
// version cannot make the endpoint scan its whole history.
const quizSampleLimit = 500

// readQuizSample loads one version's raw measurements.
//
// Errored measurements are counted and reported but kept OUT of the sample:
// a run that never answered measured nothing about the prompt, and folding its
// (absent) cost in would let an outage read as a cost improvement. Runaway but
// completed runs stay in — the dispersion measure is chosen so they cannot
// distort it, and "this version runs away more often" is a real reading.
func (h *Handler) readQuizSample(r *http.Request, scope promptVersionScope, scopeID pgtype.UUID, version int32) (promptquiz.Sample, map[string]int, error) {
	rows, err := h.Queries.ListPromptQuizSamples(r.Context(), db.ListPromptQuizSamplesParams{
		Scope:    string(scope),
		ScopeID:  scopeID,
		Version:  version,
		RowLimit: quizSampleLimit,
	})
	if err != nil {
		return promptquiz.Sample{}, nil, err
	}
	outcomes := map[string]int{}
	sample := promptquiz.Sample{}
	for _, row := range rows {
		outcomes[row.Outcome]++
		if row.Outcome == promptquiz.OutcomeErrored || !row.RunTokens.Valid {
			continue
		}
		sample.Values = append(sample.Values, float64(row.RunTokens.Int64))
	}
	return sample, outcomes, nil
}
