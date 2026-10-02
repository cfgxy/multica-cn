package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/promptquiz"
)

// The two read failures a baseline can hit, kept as sentinels so the shared
// reader (promptQuizBaselineData) and its two HTTP callers render the same
// messages the endpoint wrote before the overview joined it.
var (
	errQuizVersionRead = errors.New("failed to read the prompt version")
	errQuizSampleRead  = errors.New("failed to read quiz measurements")
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

// PromptQuizItemResponse is the wire shape of one bank entry, member-visible.
//
// NO rubric FIELD, deliberately. The private half of an item (migration 935) is
// readable only through the single-item endpoint, which the router puts behind
// the owner role; a struct without the field cannot leak it by being handed the
// wrong row.
type PromptQuizItemResponse struct {
	ID       string `json:"id"`
	Slug     string `json:"slug"`
	Title    string `json:"title"`
	Body     string `json:"body"`
	Revision int32  `json:"revision"`
	// Tags and Difficulty are member-visible presentation metadata (migration
	// 950): what a question covers and how hard it is. They are NOT the
	// answer key — that stays split between rubric and rubric_checks, which
	// only the owner-gated detail read returns.
	Tags           []string `json:"tags,omitempty"`
	Difficulty     string   `json:"difficulty,omitempty"`
	RuntimeProfile string   `json:"runtime_profile"`
	Active         bool     `json:"active"`
	// Discrimination is the A4 mark: whether this question's readings still
	// spread enough to tell two prompt versions apart. Empty on responses that
	// did not compute it, which a reader must treat as "not judged" rather than
	// as "fine" — the pending/ok distinction is carried in the value itself.
	Discrimination string `json:"discrimination,omitempty"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

// PromptQuizItemDetailResponse adds the private halves, for the owner-only
// read and for the writes whose author just supplied them.
type PromptQuizItemDetailResponse struct {
	PromptQuizItemResponse
	Rubric string `json:"rubric"`
	// RubricChecks is the structured half of the answer key (migration 950).
	// Same gating as Rubric: this struct is only ever built on the owner
	// group's routes, and the member-visible list type has no field for it.
	RubricChecks json.RawMessage `json:"rubric_checks,omitempty"`
}

func promptQuizItemToResponse(item db.PromptQuizItem) PromptQuizItemResponse {
	return PromptQuizItemResponse{
		ID:             uuidToString(item.ID),
		Slug:           item.Slug,
		Title:          item.Title,
		Body:           item.Body,
		Revision:       item.Revision,
		Tags:           item.Tags,
		Difficulty:     item.Difficulty,
		RuntimeProfile: item.RuntimeProfile,
		Active:         item.Active,
		CreatedAt:      formatQuizTime(item.CreatedAt),
		UpdatedAt:      formatQuizTime(item.UpdatedAt),
	}
}

func promptQuizItemToDetail(item db.PromptQuizItem) PromptQuizItemDetailResponse {
	return PromptQuizItemDetailResponse{
		PromptQuizItemResponse: promptQuizItemToResponse(item),
		Rubric:                 item.Rubric,
		RubricChecks:           item.RubricChecks,
	}
}

// promptQuizItemRowToResponse converts the member-visible list row, which is a
// different type from db.PromptQuizItem precisely because it has no rubric and
// no rubric_checks (migration 950 keeps the named-column discipline: tags and
// difficulty DO travel, the answer key's two halves do not).
func promptQuizItemRowToResponse(row db.ListPromptQuizItemsRow) PromptQuizItemResponse {
	return PromptQuizItemResponse{
		ID:             uuidToString(row.ID),
		Slug:           row.Slug,
		Title:          row.Title,
		Body:           row.Body,
		Revision:       row.Revision,
		Tags:           row.Tags,
		Difficulty:     row.Difficulty,
		RuntimeProfile: row.RuntimeProfile,
		Active:         row.Active,
		CreatedAt:      formatQuizTime(row.CreatedAt),
		UpdatedAt:      formatQuizTime(row.UpdatedAt),
	}
}

func formatQuizTime(ts pgtype.Timestamptz) string {
	if !ts.Valid {
		return ""
	}
	return ts.Time.UTC().Format(httpTimeFormat)
}

type promptQuizItemWrite struct {
	Slug  string `json:"slug"`
	Title string `json:"title"`
	// Body is the public half — the question put to the measured run.
	Body string `json:"body"`
	// Rubric is the private half — the expected answer and the grading points.
	// Optional: an item with no answer key yet is a normal state.
	Rubric string `json:"rubric"`
	// RubricChecks is the structured half of the answer key (migration 950):
	// machine-checkable assertions the collector grades answers against.
	// Optional; gated by promptquiz.ValidateChecks like the free-text half is
	// by ValidateRubric.
	RubricChecks json.RawMessage `json:"rubric_checks"`
	// Tags and Difficulty are member-visible metadata (migration 950).
	Tags           []string `json:"tags"`
	Difficulty     string   `json:"difficulty"`
	RuntimeProfile string   `json:"runtime_profile"`
	Active         *bool    `json:"active"`
}

func (pw promptQuizItemWrite) profile() string {
	if pw.RuntimeProfile == "leader_task" {
		return "leader_task"
	}
	return "member"
}

// difficulty normalizes the write's difficulty to a CHECK-legal value. An
// omitted difficulty means "medium", not "unchanged": the write path replaces
// the item wholesale, same as the rest of the fields.
func (pw promptQuizItemWrite) normalizedDifficulty() string {
	switch pw.Difficulty {
	case "easy", "hard":
		return pw.Difficulty
	default:
		return "medium"
	}
}

// cleanTags trims, drops empties and duplicates, and caps the list. Tags are
// presentation metadata; the cap keeps one import from turning the member
// list into a tag cloud.
func cleanTags(tags []string) []string {
	out := make([]string, 0, len(tags))
	seen := make(map[string]bool, len(tags))
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" || seen[tag] || len(out) == 8 {
			continue
		}
		if len(tag) > 32 {
			tag = tag[:32]
		}
		seen[tag] = true
		out = append(out, tag)
	}
	return out
}

// decodePromptQuizItemWrite reads the body and applies the isolation gate.
//
// The gate's refusal is returned verbatim to the author: it names the kind of
// reference and its offset, and never echoes the matched text, so the error is
// actionable without widening the disclosure surface of a workspace-private
// body (Owner Q8). The checks gate follows the same rule — it names the
// offending assertion, never the phrase that tripped it.
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
	if v := promptquiz.ValidateRubric(pw.Rubric); !v.OK() {
		writeError(w, http.StatusBadRequest, v.RubricReason())
		return pw, false
	}
	if v := promptquiz.ValidateChecks(pw.RubricChecks); !v.OK() {
		writeError(w, http.StatusBadRequest, v.Reason)
		return pw, false
	}
	// Tags are always normalized (never nil: the column is NOT NULL, and an
	// omitted field means "no tags", not SQL NULL).
	pw.Tags = cleanTags(pw.Tags)
	// A JSON null or empty payload means "no checks": SQL NULL, not a jsonb
	// null — the DDL CHECK admits SQL NULL or an array only, and a jsonb null
	// is neither.
	if t := strings.TrimSpace(string(pw.RubricChecks)); t == "" || t == "null" {
		pw.RubricChecks = nil
	}
	return pw, true
}

// ListPromptQuizItems — GET /api/prompt-quiz/items
//
// The bank plus each question's discrimination mark (A4). The mark travels with
// the list rather than behind its own endpoint because the only thing anyone does
// with it is decide whether to edit or retire the question it belongs to, and
// that decision is made in this list.
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
	marks, err := h.Queries.ListPromptQuizItemDiscrimination(r.Context(), workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read quiz measurements")
		return
	}
	byItem := make(map[string]promptquiz.Discrimination, len(marks))
	for _, m := range marks {
		byItem[uuidToString(m.ItemID)] = promptquiz.DiscriminationFor(
			int(m.Attempts), int(m.Graded), m.Median, m.Iqr,
		)
	}

	out := make([]PromptQuizItemResponse, 0, len(items))
	for _, item := range items {
		resp := promptQuizItemRowToResponse(item)
		// A question with no measurements at all has no row in the aggregate,
		// which is pending rather than missing: it has been written but never
		// asked.
		mark, ok := byItem[resp.ID]
		if !ok {
			mark = promptquiz.DiscriminationPending
		}
		resp.Discrimination = string(mark)
		out = append(out, resp)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

// GetPromptQuizItem — GET /api/prompt-quiz/items/{itemId}
//
// The only read that returns the private half, which is why the router keeps it
// in the owner-only group next to the writes rather than with the member-visible
// list.
func (h *Handler) GetPromptQuizItem(w http.ResponseWriter, r *http.Request) {
	itemID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "itemId"), "item_id")
	if !ok {
		return
	}
	item, err := h.Queries.GetPromptQuizItem(r.Context(), db.GetPromptQuizItemParams{
		ID:          itemID,
		WorkspaceID: parseUUID(h.resolveWorkspaceID(r)),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "question not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to read the question")
		return
	}
	writeJSON(w, http.StatusOK, promptQuizItemToDetail(item))
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
		Rubric:         pw.Rubric,
		RubricChecks:   pw.RubricChecks,
		Tags:           pw.Tags,
		Difficulty:     pw.normalizedDifficulty(),
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
	writeJSON(w, http.StatusOK, promptQuizItemToDetail(item))
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
		Rubric:         pw.Rubric,
		RubricChecks:   pw.RubricChecks,
		Tags:           pw.Tags,
		Difficulty:     pw.normalizedDifficulty(),
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
	writeJSON(w, http.StatusOK, promptQuizItemToDetail(item))
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
	// Outcomes counts every stored row by outcome. The vocabulary is
	// answered/errored (migration 933) and says whether the run produced an
	// answer at all; NEITHER value is a grade, so this map must never be
	// presented as a pass rate.
	Outcomes map[string]int `json:"outcomes"`
	// Incomparable counts readings that are real measurements but were taken
	// with a different instrument than the current cohort — a different question
	// wording, or a different runtime/model pair. Without these two counts a
	// reader would see a group shrink from 30 to 4 after a bank edit with no way
	// to tell that from a collection failure.
	Incomparable         int `json:"incomparable"`
	BaselineIncomparable int `json:"baseline_incomparable"`
	// Scores is the graded side of the current group (RUYI-286), computed
	// over the SAME cohort-selected sample the token summary reads. Nil when
	// no cohort exists. An aggregate of counts and means only — no per-run
	// evidence travels on this member-visible endpoint; the drill-down behind
	// it (score_detail) is Owner-only by route.
	Scores *promptquiz.ScoreSummary `json:"scores,omitempty"`
	// BaselineScores is the graded side of the baseline group, same rules.
	BaselineScores *promptquiz.ScoreSummary `json:"baseline_scores,omitempty"`
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

	resp, err := h.promptQuizBaselineData(r.Context(), scope, scopeID)
	if err != nil {
		if errors.Is(err, errQuizVersionRead) {
			writeError(w, http.StatusInternalServerError, errQuizVersionRead.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, errQuizSampleRead.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// promptQuizBaselineData is the read both the per-scope quiz endpoint and the
// workspace overview (RUYI-284) serve. One function owns what a baseline
// comparison is, so the two surfaces cannot drift apart; callers have already
// established that scopeID belongs to the workspace. Every field a reader
// needs to recompute the verdict travels on the response — nothing here is
// collapsed into a bare label.
func (h *Handler) promptQuizBaselineData(ctx context.Context, scope promptVersionScope, scopeID pgtype.UUID) (PromptQuizBaselineResponse, error) {
	resp := PromptQuizBaselineResponse{
		Scope:          string(scope),
		ScopeID:        uuidToString(scopeID),
		RequiredSample: promptquiz.NewVersionSampleSize,
		RequiredBase:   promptquiz.BaselineSampleSize,
		Outcomes:       map[string]int{},
	}

	current, err := h.Queries.GetLatestPromptVersion(ctx, db.GetLatestPromptVersionParams{
		Scope:   string(scope),
		ScopeID: scopeID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// No version, therefore nothing to compare. An empty reading, not
			// an error and not a zero score.
			return resp, nil
		}
		return resp, fmt.Errorf("%w: %v", errQuizVersionRead, err)
	}
	resp.CurrentVersion = current.Version

	currentRows, outcomes, err := h.readQuizSample(ctx, scope, scopeID, current.Version)
	if err != nil {
		return resp, fmt.Errorf("%w: %v", errQuizSampleRead, err)
	}
	resp.Outcomes = outcomes

	// The current version's own readings define the measuring stick, and both
	// groups are then read through it. Deriving it from the baseline instead would
	// pin the deployment to a retired wording; letting each group derive its own
	// would compare two different instruments, which is the defect this replaces.
	cohort, ok := promptquiz.CohortOf(currentRows)
	if !ok {
		// No usable reading yet, so no cohort and no sample. Every stored row is
		// still counted in Outcomes above.
		return resp, nil
	}
	currentSample, incomparable := cohort.Select(currentRows)
	resp.Incomparable = incomparable
	resp.Current = promptquiz.Summarize(currentSample)
	// Graded side of the same sample. Kept OUT of Summarize: the token
	// distribution and the pass ratio are different units, and folding them
	// would let a reader subtract tokens from correctness.
	scores := promptquiz.SummarizeScores(currentSample)
	resp.Scores = &scores
	resp.Measured = len(currentSample.Values) > 0

	if current.Version > 1 {
		resp.BaselineVersion = current.Version - 1
		baseRows, _, err := h.readQuizSample(ctx, scope, scopeID, resp.BaselineVersion)
		if err != nil {
			return resp, fmt.Errorf("%w: %v", errQuizSampleRead, err)
		}
		baseSample, baseIncomparable := cohort.Select(baseRows)
		resp.BaselineIncomparable = baseIncomparable
		baseScores := promptquiz.SummarizeScores(baseSample)
		resp.BaselineScores = &baseScores
		cmp := promptquiz.Compare(baseSample, currentSample)
		resp.Comparison = &cmp
	}
	return resp, nil
}

// quizSampleLimit bounds one read. Generous relative to the required group
// sizes so accumulation past them is still visible, bounded so a long-lived
// version cannot make the endpoint scan its whole history.
const quizSampleLimit = 500

// readQuizSample loads one version's raw measurements, newest first, WITHOUT
// pooling them.
//
// It deliberately does not return a promptquiz.Sample. Rows of one version are
// not a sample group until a cohort has been applied: two rows can belong to the
// same version and still be incomparable because the question was reworded
// between them (migration 929: "excluded by revision") or because a different
// runtime/model executed them (A1). Pooling here is what let a bank edit change
// the measuring stick mid-curve; promptquiz.Cohort now owns that decision, and
// cohort_test.go asserts that removing any of its isolation conditions merges
// readings that must stay separate.
//
// Outcome counts are taken over EVERY stored row, before any cohort filtering:
// "3 runs errored" is a fact about the version, not about the current wording.
// Errored rows never enter a sample — a run that never answered measured nothing
// about the prompt, and folding its absent cost in would let an outage read as a
// cost improvement. Runaway but completed runs stay in.
func (h *Handler) readQuizSample(ctx context.Context, scope promptVersionScope, scopeID pgtype.UUID, version int32) ([]promptquiz.Measurement, map[string]int, error) {
	rows, err := h.Queries.ListPromptQuizSamples(ctx, db.ListPromptQuizSamplesParams{
		Scope:    string(scope),
		ScopeID:  scopeID,
		Version:  version,
		RowLimit: quizSampleLimit,
	})
	if err != nil {
		return nil, nil, err
	}
	outcomes := map[string]int{}
	out := make([]promptquiz.Measurement, 0, len(rows))
	for _, row := range rows {
		outcomes[row.Outcome]++
		m := promptquiz.Measurement{
			Fingerprint: promptquiz.Fingerprint{
				ItemID: uuidToString(row.ItemID),
				Wording: promptquiz.Wording{
					Revision: row.ItemRevision,
					BodySHA:  row.ItemBodySha256,
				},
				// An invalid UUID / NULL model reads as the empty string, which
				// Fingerprint treats as a distinct key rather than a wildcard —
				// the same semantics IS NOT DISTINCT FROM gives the SQL-side
				// count in CountPromptQuizMeasurementsForVersion.
				RuntimeID: uuidToString(row.RuntimeID),
				Model:     row.RunModel.String,
			},
			Outcome: row.Outcome,
		}
		if row.RunTokens.Valid {
			m.Value, m.Valued = float64(row.RunTokens.Int64), true
		}
		// Score rides along (RUYI-286) but never into the token statistics:
		// SummarizeScores reads it over the cohort-selected sample in the
		// handler, which is a display aggregate, not the comparison.
		if row.Score.Valid {
			m.Score, m.Scored = float64(row.Score.Float32), true
		}
		out = append(out, m)
	}
	return out, outcomes, nil
}
