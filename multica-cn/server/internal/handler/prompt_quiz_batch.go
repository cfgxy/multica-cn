package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/promptquizsweep"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/promptquiz"
	"github.com/multica-ai/multica/server/pkg/promptquiz/bank"
)

// Batch runs and graded-sample traceability (RUYI-286).
//
// The sweep (internal/promptquizsweep) accumulates samples on a cadence; the
// endpoints here let an Owner order the same kind of run on demand — a bank
// import or a prompt edit followed by "measure it now" — and then read back
// what a batch produced, per row, down to the per-assertion evidence. Same
// pipes, same guarantees: the runs are real agent_task_queue rows, the payload
// carries the question and nothing else, and grading writes to the columns
// migration 950 added rather than folding into outcome.
//
// Everything in this file is Owner-only. score_detail is the grading output of
// the private half (the rubric), so traceability reads sit behind the same gate
// as the rubric itself.

// Batch caps. An Owner asking for more than this is about to spend real model
// tokens faster than any review can follow, and the sweep's budget discipline
// exists for the same reason. 10 agents × the default item set already
// saturates the combined cap, so maxTotal is the binding limit in practice.
const (
	quizBatchMaxAgents = 10
	quizBatchMaxItems  = 50
	quizBatchMaxTotal  = 50
)

// PromptQuizBatchCreateRequest is the body of POST /api/prompt-quiz/batches.
type PromptQuizBatchCreateRequest struct {
	// AgentIDs names the subjects. Every agent must live in this workspace
	// and carry a bound runtime: a quiz run measures the agent-tier prompt,
	// and an agent with no runtime has nothing to run the question on.
	AgentIDs []string `json:"agent_ids"`
	// ItemIDs optionally restricts the item set. Empty means every active
	// item in the member profile — the same enumeration the sweep reads.
	ItemIDs []string `json:"item_ids"`
}

// PromptQuizBatchCreateResponse reports what the batch actually ordered.
type PromptQuizBatchCreateResponse struct {
	BatchID string `json:"batch_id"`
	// Ordered is the number of agent_task_queue rows created.
	Ordered int `json:"ordered"`
	// RefusedAgents names agents that were dropped and why. A batch with
	// zero valid agents orders nothing — that is a 422, not a silent 200.
	RefusedAgents []QuizBatchRefusedAgent `json:"refused_agents,omitempty"`
}

// QuizBatchRefusedAgent explains one dropped agent.
type QuizBatchRefusedAgent struct {
	AgentID string `json:"agent_id"`
	// "not_found" | "no_runtime".
	Reason string `json:"reason"`
}

// CreatePromptQuizBatch — POST /api/prompt-quiz/batches (Owner-only).
//
// Orders one real run per (valid agent × selected item) under a fresh batch id
// written into each task payload. Enqueueing mirrors the sweep's exactly: the
// CreatePromptQuizTask fence may refuse (agent being deleted), and a refusal
// counts the row as not ordered rather than failing the batch — partial
// progress is the sweep's semantics and stays the batch's.
func (h *Handler) CreatePromptQuizBatch(w http.ResponseWriter, r *http.Request) {
	workspaceID := parseUUID(h.resolveWorkspaceID(r))

	var req PromptQuizBatchCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.AgentIDs) == 0 {
		writeError(w, http.StatusBadRequest, "agent_ids must not be empty")
		return
	}
	if len(req.AgentIDs) > quizBatchMaxAgents {
		writeError(w, http.StatusBadRequest, "too many agents")
		return
	}
	if len(req.ItemIDs) > quizBatchMaxItems {
		writeError(w, http.StatusBadRequest, "too many items")
		return
	}

	// Resolve agents through the workspace-scoped query, so an id from
	// another workspace reads as "not found" and never as a subject.
	type agentRef struct {
		id        pgtype.UUID
		runtimeID pgtype.UUID
	}
	agents := make([]agentRef, 0, len(req.AgentIDs))
	var refused []QuizBatchRefusedAgent
	seen := map[string]bool{}
	for _, raw := range req.AgentIDs {
		id, err := uuid.Parse(raw)
		if err != nil || seen[raw] {
			continue
		}
		seen[raw] = true
		agent, err := h.Queries.GetAgentInWorkspace(r.Context(), db.GetAgentInWorkspaceParams{
			ID:          pgtype.UUID{Bytes: id, Valid: true},
			WorkspaceID: workspaceID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			refused = append(refused, QuizBatchRefusedAgent{AgentID: raw, Reason: "not_found"})
			continue
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to read the agent")
			return
		}
		if !agent.RuntimeID.Valid {
			refused = append(refused, QuizBatchRefusedAgent{AgentID: raw, Reason: "no_runtime"})
			continue
		}
		agents = append(agents, agentRef{id: agent.ID, runtimeID: agent.RuntimeID})
	}
	if len(agents) == 0 {
		// Nothing left to measure: say so instead of minting an empty batch.
		writeErrorCode(w, http.StatusUnprocessableEntity, "no_valid_agents", "every requested agent was refused")
		return
	}

	items, err := h.resolveBatchItems(r, workspaceID, req.ItemIDs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read quiz items")
		return
	}
	if len(items) == 0 {
		writeErrorCode(w, http.StatusUnprocessableEntity, "no_items", "no active quiz items match this batch")
		return
	}
	if max := quizBatchMaxTotal; len(agents)*len(items) > max {
		writeErrorCode(w, http.StatusUnprocessableEntity, "too_many_runs",
			"this batch would order "+strconv.Itoa(len(agents)*len(items))+" runs; cap is "+strconv.Itoa(max))
		return
	}

	batch := newQuizBatchID()
	ordered := 0
	for _, agent := range agents {
		for _, item := range items {
			payload, err := promptquizsweep.TaskContextPayload(uuid.UUID(item.id.Bytes).String(), item.body, batch)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "failed to encode the quiz context")
				return
			}
			_, err = h.Queries.CreatePromptQuizTask(r.Context(), db.CreatePromptQuizTaskParams{
				AgentID:   agent.id,
				RuntimeID: agent.runtimeID,
				// Same standing as sweep-ordered runs: below everything a
				// human is waiting on, above nothing.
				Priority: 0,
				Context:  payload,
				ItemID:   item.id,
			})
			if errors.Is(err, pgx.ErrNoRows) {
				// The fence refused (agent or runtime being deleted). Not an
				// error for the batch: the remaining rows are still valid.
				continue
			}
			if err != nil {
				writeError(w, http.StatusInternalServerError, "failed to order the quiz run")
				return
			}
			ordered++
		}
	}

	resp := PromptQuizBatchCreateResponse{BatchID: uuidToString(batch), Ordered: ordered, RefusedAgents: refused}
	writeJSON(w, http.StatusOK, resp)
}

// batchItem is the slice of a quiz item the batch path needs. Deliberately
// narrow: body, not rubric — the payload builder takes what the daemon sees
// and nothing more, mirroring the sweep's structural isolation.
type batchItem struct {
	id   pgtype.UUID
	body string
}

// resolveBatchItems turns the request's item selection into batchItems. Empty
// selection means every active member-profile item (the sweep's enumeration);
// a named selection must each exist and be active in this workspace.
func (h *Handler) resolveBatchItems(r *http.Request, workspaceID pgtype.UUID, itemIDs []string) ([]batchItem, error) {
	out := []batchItem{}
	if len(itemIDs) == 0 {
		rows, err := h.Queries.ListActivePromptQuizItemsForProfile(r.Context(), db.ListActivePromptQuizItemsForProfileParams{
			WorkspaceID:    workspaceID,
			RuntimeProfile: "member",
		})
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			out = append(out, batchItem{id: row.ID, body: row.Body})
		}
		return out, nil
	}

	seen := map[string]bool{}
	for _, raw := range itemIDs {
		id, err := uuid.Parse(raw)
		if err != nil || seen[raw] {
			continue
		}
		seen[raw] = true
		item, err := h.Queries.GetPromptQuizItem(r.Context(), db.GetPromptQuizItemParams{
			ID:          pgtype.UUID{Bytes: id, Valid: true},
			WorkspaceID: workspaceID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !item.Active {
			continue
		}
		out = append(out, batchItem{id: item.ID, body: item.Body})
	}
	return out, nil
}

// newQuizBatchID mints the batch id the runs will carry. A UUIDv7 so the
// batch list reads in creation order without a table of batches — the batch
// exists as a shared id across agent_task_queue rows, exactly like the
// sweep's per-tick id.
func newQuizBatchID() pgtype.UUID {
	id := uuid.Must(uuid.NewV7())
	return pgtype.UUID{Bytes: id, Valid: true}
}

// PromptQuizBatchResponse is one batch's read-back, one row per run.
type PromptQuizBatchResponse struct {
	BatchID string                  `json:"batch_id"`
	Rows    []PromptQuizSampleRow   `json:"rows"`
	Counts  map[string]int          `json:"counts"`
	Scores  promptquiz.ScoreSummary `json:"scores"`
}

// GetPromptQuizBatch — GET /api/prompt-quiz/batches/{batchId} (Owner-only).
//
// The traceability view: which items ran, which answered, which errored, what
// each graded answer scored and on what evidence, and the task id that joins
// back to the real run. Row shape is shared with the samples endpoint — one
// population, two windows onto it.
func (h *Handler) GetPromptQuizBatch(w http.ResponseWriter, r *http.Request) {
	workspaceID := parseUUID(h.resolveWorkspaceID(r))
	batchID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "batchId"), "batch_id")
	if !ok {
		return
	}
	rows, err := h.Queries.ListPromptQuizResultsForBatch(r.Context(), db.ListPromptQuizResultsForBatchParams{
		WorkspaceID: workspaceID,
		BatchID:     batchID,
		RowLimit:    quizSampleLimit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read the batch")
		return
	}

	resp := PromptQuizBatchResponse{
		BatchID: uuidToString(batchID),
		Rows:    []PromptQuizSampleRow{},
		Counts:  map[string]int{},
	}
	sample := promptquiz.Sample{Scores: []promptquiz.ScoredReading{}}
	for _, row := range rows {
		resp.Rows = appendQuizBatchRow(resp.Rows, row)
		resp.Counts[row.Outcome]++
		if row.Score.Valid {
			sample.Scores = append(sample.Scores, promptquiz.ScoredReading{
				ItemID: uuidToString(row.ItemID), Score: float64(row.Score.Float32),
			})
		}
	}
	// Scored-only aggregation over the batch's own rows. A batch is one
	// cohort by construction (one payload wording, the agent's runtime at
	// order time), so no cohort filter is applied here — and the summary is
	// a read-back, not the comparison instrument the baseline uses.
	resp.Scores = promptquiz.SummarizeScores(sample)
	writeJSON(w, http.StatusOK, resp)
}

// appendQuizBatchRow maps one batch row onto the wire shape, in place.
func appendQuizBatchRow(dst []PromptQuizSampleRow, row db.ListPromptQuizResultsForBatchRow) []PromptQuizSampleRow {
	return append(dst, PromptQuizSampleRow{
		TaskID:       uuidToString(row.TaskID),
		Scope:        row.Scope,
		ScopeID:      uuidToString(row.ScopeID),
		Version:      row.Version,
		ItemID:       uuidToString(row.ItemID),
		ItemRevision: row.ItemRevision,
		ItemSlug:     row.ItemSlug.String,
		ItemTitle:    row.ItemTitle.String,
		Outcome:      row.Outcome,
		Score:        quizScorePtr(row.Score),
		ScoreDetail:  json.RawMessage(row.ScoreDetail),
		GradedAt:     quizTimePtr(row.GradedAt),
		MeasuredAt:   quizTimePtr(row.MeasuredAt),
		RunTokens:    quizTokensPtr(row.RunTokens),
		TaskStatus:   row.TaskStatus,
	})
}

// PromptQuizSampleRow is one graded sample on the wire. Shared by the batch
// read-back and the version samples endpoint: same population, two windows.
type PromptQuizSampleRow struct {
	TaskID       string `json:"task_id"`
	Scope        string `json:"scope"`
	ScopeID      string `json:"scope_id"`
	Version      int32  `json:"version"`
	ItemID       string `json:"item_id"`
	ItemRevision int32  `json:"item_revision"`
	ItemSlug     string `json:"item_slug,omitempty"`
	ItemTitle    string `json:"item_title,omitempty"`
	Outcome      string `json:"outcome"`
	// Score is null when the row is not graded: an errored run, or an item
	// whose checks are empty. It MUST render as "not graded" everywhere, never
	// as 0.
	Score *float64 `json:"score"`
	// ScoreDetail carries the per-assertion verdicts and evidence, in the
	// grader's own shape (pkg/promptquiz.Grade). Evidence names assertions
	// and what was found; it never quotes the answer text.
	ScoreDetail json.RawMessage `json:"score_detail,omitempty"`
	GradedAt    *string         `json:"graded_at,omitempty"`
	MeasuredAt  *string         `json:"measured_at,omitempty"`
	RunTokens   *int64          `json:"run_tokens,omitempty"`
	// TaskStatus is the agent_task_queue row's status, so "ordered but not
	// yet picked up" stays visible instead of looking like a missing sample.
	TaskStatus string `json:"task_status"`
}

// GetPromptQuizSamples — GET /api/prompt-quiz/samples (Owner-only).
//
// One scope-version's graded samples, newest first — the drill-down behind the
// baseline reading: "this version scored 0.72; here are the runs, the answers'
// verdicts, and when". Query: scope, scope_id, version, limit.
func (h *Handler) GetPromptQuizSamples(w http.ResponseWriter, r *http.Request) {
	scope, ok := parsePromptVersionScope(r.URL.Query().Get("scope"))
	if !ok {
		writeError(w, http.StatusBadRequest, "unknown scope")
		return
	}
	scopeID, ok := parseUUIDOrBadRequest(w, r.URL.Query().Get("scope_id"), "scope_id")
	if !ok {
		return
	}
	version, err := strconv.ParseInt(r.URL.Query().Get("version"), 10, 32)
	if err != nil || version <= 0 {
		writeError(w, http.StatusBadRequest, "version must be a positive integer")
		return
	}
	limit := int32(quizSampleLimit)
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 32)
		if err != nil || parsed <= 0 || parsed > int64(quizSampleLimit) {
			writeError(w, http.StatusBadRequest, "limit out of range")
			return
		}
		limit = int32(parsed)
	}

	rows, err := h.Queries.ListPromptQuizGradedSamples(r.Context(), db.ListPromptQuizGradedSamplesParams{
		WorkspaceID: parseUUID(h.resolveWorkspaceID(r)),
		Scope:       string(scope),
		ScopeID:     scopeID,
		Version:     int32(version),
		RowLimit:    limit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read the samples")
		return
	}

	out := []PromptQuizSampleRow{}
	for _, row := range rows {
		out = append(out, PromptQuizSampleRow{
			TaskID:       uuidToString(row.TaskID),
			Scope:        string(scope),
			ScopeID:      uuidToString(scopeID),
			Version:      int32(version),
			ItemID:       uuidToString(row.ItemID),
			ItemRevision: row.ItemRevision,
			ItemSlug:     row.ItemSlug.String,
			ItemTitle:    row.ItemTitle.String,
			Outcome:      row.Outcome,
			Score:        quizScorePtr(row.Score),
			ScoreDetail:  json.RawMessage(row.ScoreDetail),
			GradedAt:     quizTimePtr(row.GradedAt),
			MeasuredAt:   quizTimePtr(row.MeasuredAt),
			RunTokens:    quizTokensPtr(row.RunTokens),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"rows": out})
}

// quizScorePtr maps a nullable score onto the wire: nil stays nil, and a
// present score keeps its value. No 0-folding anywhere on this path.
func quizScorePtr(s pgtype.Float4) *float64 {
	if !s.Valid {
		return nil
	}
	v := float64(s.Float32)
	return &v
}

// quizTimePtr renders a timestamp as RFC3339 or null.
func quizTimePtr(t pgtype.Timestamptz) *string {
	if !t.Valid {
		return nil
	}
	v := t.Time.UTC().Format("2006-01-02T15:04:05Z07:00")
	return &v
}

// quizTokensPtr passes nullable run cost through.
func quizTokensPtr(t pgtype.Int8) *int64 {
	if !t.Valid {
		return nil
	}
	v := t.Int64
	return &v
}

// PromptQuizCatalogResponse reports one bank import.
type PromptQuizCatalogResponse struct {
	Imported int      `json:"imported"`
	Slugs    []string `json:"slugs"`
}

// ImportPromptQuizBank — POST /api/prompt-quiz/bank/import (Owner-only).
//
// Upserts the built-in benchmark catalog (pkg/promptquiz/bank) into this
// workspace's bank. Idempotent: each item upserts by (workspace, slug), a
// re-import refreshes body/rubric/checks/tags/difficulty and bumps revision
// only when the body actually changed, so re-running an import after a prompt
// upgrade does not churn the item ids the measurements point at.
//
// The catalog is compile-time content: its isolation is asserted by
// bank/catalog_test.go (no UUIDs, no issue keys, no mention links, no URLs),
// and every item's checks pass promptquiz.ValidateChecks before it can ship —
// this endpoint trusts that and re-validates nothing per request beyond what
// the upsert's own columns enforce.
func (h *Handler) ImportPromptQuizBank(w http.ResponseWriter, r *http.Request) {
	workspaceID := parseUUID(h.resolveWorkspaceID(r))
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	userUUID := parseUUID(userID)

	imported := 0
	slugs := []string{}
	for _, entry := range bank.Catalog {
		checks, err := json.Marshal(entry.Checks)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to encode checks")
			return
		}
		if _, err := h.Queries.UpsertPromptQuizBankItem(r.Context(), db.UpsertPromptQuizBankItemParams{
			WorkspaceID: workspaceID,
			Slug:        entry.Slug,
			Title:       entry.Title,
			Body:        entry.Body,
			Rubric:      entry.Rubric,
			// A catalog item with no checks still stores an empty array, not
			// SQL NULL: "imported and ungraded" and "never had checks" are the
			// same state for an item that ships with its assertions.
			RubricChecks: checks,
			// The category IS the type tag: the catalog ships one tag per
			// item and it is the eight-type benchmark taxonomy.
			Tags:            []string{entry.Category},
			Difficulty:      entry.Difficulty,
			RuntimeProfile:  "member",
			CreatedByUserID: userUUID,
		}); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to import the bank item")
			return
		}
		imported++
		slugs = append(slugs, entry.Slug)
	}
	writeJSON(w, http.StatusOK, PromptQuizCatalogResponse{Imported: imported, Slugs: slugs})
}
