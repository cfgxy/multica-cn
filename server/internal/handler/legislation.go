package handler

// Prompt legislation API (RUYI-305 E2): the rebuilt proposal pool. A
// proposal is a Prompt improvement draft — carrier + target section +
// change kind + the clause's final text + the content-gate five answers +
// evidence anchors — moving through
//
//	draft --submit--> pending_owner --approve(owner)--> gate --> enacted | gate_failed
//	                                   --reject(owner)--> rejected
//	gate_failed --rework--> draft;  rejected --restore(owner)--> draft
//
// Approve runs the gate synchronously (owner decision + system execution in
// one transaction): the sandbox-synthesized full carrier text is validated
// by the legislation engine (E4), a pass enacts the row and rebuilds the
// structure baseline from the synthesized text, a fail parks the row in
// gate_failed with the engine report stored. `approved` is persisted only
// between those two steps, so a mid-flight crash leaves an audited row that
// POST /{id}/enact — owner-only — re-gates and enacts.
//
// E5 boundary: the enacted write into the carrier's effective content and
// the prompt_version snapshot binding are RUYI-285 scope. E2/E4 enact
// everything EXCEPT that write — enacted_version stays NULL, the business
// column is untouched.
//
// Injection defence (dry-run patch 8): approve requires
// confirm_diff_previewed=true, and /preview returns the full-carrier line
// diff so the approval UI can force the owner through it.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/legislation"
	"github.com/multica-ai/multica/server/internal/logger"
	"github.com/multica-ai/multica/server/internal/retrospective"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// ── Response shape ──────────────────────────────────────────────────────────

// PromptProposalResponse is one pool row as the API returns it. The five
// gate answers and the engine report ride through as raw JSON — the client
// renders them verbatim.
type PromptProposalResponse struct {
	ID                  string          `json:"id"`
	WorkspaceID         string          `json:"workspace_id"`
	CarrierScope        string          `json:"carrier_scope"`
	CarrierScopeID      string          `json:"carrier_scope_id"`
	TargetSection       string          `json:"target_section"`
	ChangeKind          string          `json:"change_kind"`
	ClauseName          string          `json:"clause_name"`
	ClauseText          string          `json:"clause_text"`
	GateAnswerLayer     string          `json:"gate_answer_layer"`
	GateAnswerRetention string          `json:"gate_answer_retention"`
	GateAnswerCost      string          `json:"gate_answer_cost"`
	GateAnswerConflict  string          `json:"gate_answer_conflict"`
	GateAnswerDedup     string          `json:"gate_answer_dedup"`
	EvidenceAnchors     json.RawMessage `json:"evidence_anchors"`
	Status              string          `json:"status"`
	GateErrors          json.RawMessage `json:"gate_errors"`
	GateWarnings        json.RawMessage `json:"gate_warnings"`
	JevAdvisory         json.RawMessage `json:"jev_advisory,omitempty"`
	EnactedVersion      *int32          `json:"enacted_version,omitempty"`
	RollbackReason      string          `json:"rollback_reason"`
	MergedFrom          json.RawMessage `json:"merged_from"`
	Source              string          `json:"source"`
	CreatedByType       string          `json:"created_by_type"`
	CreatedByID         string          `json:"created_by_id,omitempty"`
	AuditLog            json.RawMessage `json:"audit_log"`
	CreatedAt           string          `json:"created_at"`
	UpdatedAt           string          `json:"updated_at"`
}

func proposalToResponse(p db.PromptProposal) PromptProposalResponse {
	resp := PromptProposalResponse{
		ID:                  uuidToString(p.ID),
		WorkspaceID:         uuidToString(p.WorkspaceID),
		CarrierScope:        p.CarrierScope,
		CarrierScopeID:      uuidToString(p.CarrierScopeID),
		TargetSection:       p.TargetSection,
		ChangeKind:          p.ChangeKind,
		ClauseName:          p.ClauseName,
		ClauseText:          p.ClauseText,
		GateAnswerLayer:     p.GateAnswerLayer,
		GateAnswerRetention: p.GateAnswerRetention,
		GateAnswerCost:      p.GateAnswerCost,
		GateAnswerConflict:  p.GateAnswerConflict,
		GateAnswerDedup:     p.GateAnswerDedup,
		EvidenceAnchors:     json.RawMessage(p.EvidenceAnchors),
		Status:              p.Status,
		GateErrors:          json.RawMessage(p.GateErrors),
		GateWarnings:        json.RawMessage(p.GateWarnings),
		JevAdvisory:         json.RawMessage(p.JevAdvisory),
		RollbackReason:      p.RollbackReason,
		MergedFrom:          json.RawMessage(p.MergedFrom),
		Source:              p.Source,
		CreatedByType:       p.CreatedByType,
		CreatedByID:         uuidToString(p.CreatedByID),
		AuditLog:            json.RawMessage(p.AuditLog),
	}
	if p.EnactedVersion.Valid {
		v := p.EnactedVersion.Int32
		resp.EnactedVersion = &v
	}
	if p.CreatedAt.Valid {
		resp.CreatedAt = p.CreatedAt.Time.UTC().Format(httpTimeFormat)
	}
	if p.UpdatedAt.Valid {
		resp.UpdatedAt = p.UpdatedAt.Time.UTC().Format(httpTimeFormat)
	}
	return resp
}

// legislationAudit builds one audit_log entry.
func legislationAudit(action, actorType, actorID string, extra map[string]any) []byte {
	entry := map[string]any{
		"action": action, "actor_type": actorType, "actor_id": actorID,
		"at": time.Now().UTC().Format(time.RFC3339),
	}
	for k, v := range extra {
		entry[k] = v
	}
	b, err := json.Marshal([]map[string]any{entry})
	if err != nil {
		return []byte("[]")
	}
	return b
}

// proposalInputFromRow projects a row onto the engine's input.
func proposalInputFromRow(p db.PromptProposal) legislation.ProposalInput {
	return legislation.ProposalInput{
		ChangeKind:          p.ChangeKind,
		ClauseName:          p.ClauseName,
		ClauseText:          p.ClauseText,
		TargetSection:       p.TargetSection,
		GateAnswerLayer:     p.GateAnswerLayer,
		GateAnswerRetention: p.GateAnswerRetention,
		GateAnswerCost:      p.GateAnswerCost,
		GateAnswerConflict:  p.GateAnswerConflict,
		GateAnswerDedup:     p.GateAnswerDedup,
	}
}

func writeGateReport(w http.ResponseWriter, res legislation.Result) {
	writeJSON(w, http.StatusConflict, map[string]any{
		"status":   "gate_failed",
		"errors":   res.Errors,
		"warnings": res.Warnings,
	})
}

// ── Validation ──────────────────────────────────────────────────────────────

func validChangeKind(k string) bool {
	return k == "add_clause" || k == "revise_clause" || k == "remove_clause"
}

// validateLegislationProposal checks the write-path invariants the DB CHECKs
// cannot express: remove needs no text, add/revise need text, and the clause
// name is bounded.
func validateLegislationProposal(changeKind, clauseName, clauseText string) string {
	if !validChangeKind(changeKind) {
		return "unknown change_kind"
	}
	name := len([]rune(clauseName))
	if name == 0 || name > 60 {
		return "clause_name must be 1–60 characters"
	}
	if changeKind != "remove_clause" && len([]rune(clauseText)) == 0 {
		return "clause_text must not be empty"
	}
	if len(clauseText) > 100_000 {
		return "clause_text exceeds the maximum allowed size"
	}
	return ""
}

// ── Gate transaction core ───────────────────────────────────────────────────

// safeRunGate wraps the engine with a recover: a panic inside the gate must
// block enactment (fail-closed), never crash the request into a 500-without-
// verdict.
func safeRunGate(current, synthesized string, baseline *legislation.Baseline, p legislation.ProposalInput) (res legislation.Result) {
	defer func() {
		if rec := recover(); rec != nil {
			res = legislation.Result{Errors: []legislation.Finding{{
				Level: "error", Message: fmt.Sprintf("门闸内部异常，已阻断（fail-closed）：%v", rec),
			}}, Warnings: []legislation.Finding{}}
		}
	}()
	return legislation.RunGate(current, synthesized, baseline, p)
}

func errorResult(err error) legislation.Result {
	return legislation.Result{Errors: []legislation.Finding{{
		Level: "error", Message: fmt.Sprintf("沙箱合成失败，已阻断：%v", err),
	}}, Warnings: []legislation.Finding{}}
}

// baselineFromRow decodes a persisted baseline.
func baselineFromRow(row db.PromptStructureBaseline) *legislation.Baseline {
	var b BaselineJSON
	_ = json.Unmarshal(row.Sections, &b.Sections)
	_ = json.Unmarshal(row.Clauses, &b.Clauses)
	return &legislation.Baseline{Sections: b.Sections, Clauses: b.Clauses}
}

type BaselineJSON struct {
	Sections []string `json:"sections"`
	Clauses  []string `json:"clauses"`
}

// ensureBaselineTx returns the carrier's structure baseline, bootstrapping
// it from the current effective content on first sight (grandfathered
// exactly once — every later change is gated against an approved baseline).
func ensureBaselineTx(ctx context.Context, qtx *db.Queries, workspaceID pgtype.UUID, scope promptVersionScope, scopeID pgtype.UUID, current string) (*legislation.Baseline, error) {
	row, err := qtx.GetPromptStructureBaseline(ctx, db.GetPromptStructureBaselineParams{
		CarrierScope: string(scope), CarrierScopeID: scopeID,
	})
	if err == nil {
		return baselineFromRow(row), nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	sections, clauses := legislation.ExtractStructure(current)
	if _, err := qtx.UpsertPromptStructureBaseline(ctx, db.UpsertPromptStructureBaselineParams{
		WorkspaceID:    workspaceID,
		CarrierScope:   string(scope),
		CarrierScopeID: scopeID,
		Sections:       mustJSON(sections),
		Clauses:        mustJSON(clauses),
		ContentSha256:  sha256Hex(current),
	}); err != nil {
		return nil, err
	}
	return &legislation.Baseline{Sections: sections, Clauses: clauses}, nil
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte("[]")
	}
	return b
}

// runGateAndEnactTx is the approve/enact shared core, called with an open
// transaction after the row has been moved into `approved`. It locks the
// carrier row (serialize per carrier — the enact queue), synthesizes,
// gates, and lands the row on enacted (baseline rebuilt) or gate_failed
// (report stored), all inside the caller's transaction. The response for a
// gate failure is written by the caller from the returned result.
func (h *Handler) runGateAndEnactTx(ctx context.Context, r *http.Request, qtx *db.Queries, workspaceID pgtype.UUID, p db.PromptProposal, auditBase []byte) (bool, legislation.Result) {
	scope := promptVersionScope(p.CarrierScope)
	current, err := lockScopeEntityResult(ctx, qtx, scope, workspaceID, p.CarrierScopeID)
	if err != nil {
		return false, errorResult(errors.New("carrier not found in this workspace"))
	}
	baseline, err := ensureBaselineTx(ctx, qtx, workspaceID, scope, p.CarrierScopeID, current)
	if err != nil {
		return false, errorResult(fmt.Errorf("结构基线读取失败: %w", err))
	}

	input := proposalInputFromRow(p)
	var res legislation.Result
	synth, synthErr := legislation.Synthesize(current, input)
	if synthErr != nil {
		res = errorResult(synthErr)
	} else {
		res = safeRunGate(current, synth, baseline, input)
	}

	// Gate-stage soft judgment (RUYI-347): once the carrier synthesized, the
	// full text is judged and the gate-stage report overwrites the
	// submit-stage one (stage disambiguates). A synthesis failure or a
	// disabled layer keeps the previous verdict in place. Either way the
	// E1–E4 verdict above is untouched — this layer reports, never blocks.
	advisory := p.JevAdvisory
	if client := h.jevAdvisory(); client.Enabled() && synthErr == nil {
		advisory = client.Assess(ctx, "gate", synth).JSON()
	}

	if !res.OK() {
		if _, err := qtx.MarkPromptProposalGateFailed(ctx, db.MarkPromptProposalGateFailedParams{
			ID: p.ID, WorkspaceID: workspaceID,
			Audit:       auditBase,
			Errors:      mustJSON(res.Errors),
			Warnings:    mustJSON(res.Warnings),
			JevAdvisory: advisory,
		}); err != nil {
			slog.Error("gate_failed persist failed", append(logger.RequestAttrs(r), "error", err)...)
		}
		return false, res
	}

	sections, clauses := legislation.ExtractStructure(synth)
	if _, err := qtx.UpsertPromptStructureBaseline(ctx, db.UpsertPromptStructureBaselineParams{
		WorkspaceID:    workspaceID,
		CarrierScope:   string(scope),
		CarrierScopeID: p.CarrierScopeID,
		Sections:       mustJSON(sections),
		Clauses:        mustJSON(clauses),
		ContentSha256:  sha256Hex(synth),
	}); err != nil {
		slog.Error("baseline rebuild failed", append(logger.RequestAttrs(r), "error", err)...)
		return false, errorResult(fmt.Errorf("结构基线重建失败: %w", err))
	}
	if _, err := qtx.EnactPromptProposal(ctx, db.EnactPromptProposalParams{
		ID: p.ID, WorkspaceID: workspaceID, Audit: auditBase, Warnings: mustJSON(res.Warnings),
		JevAdvisory: advisory,
	}); err != nil {
		slog.Error("enact persist failed", append(logger.RequestAttrs(r), "error", err)...)
		return false, errorResult(fmt.Errorf("enact 落账失败: %w", err))
	}
	return true, res
}

// ── Handlers ────────────────────────────────────────────────────────────────

// ListPromptProposals — GET /api/prompt-legislation/proposals?status=
func (h *Handler) ListPromptProposals(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Queries.ListPromptProposals(r.Context(), db.ListPromptProposalsParams{
		WorkspaceID: parseUUID(h.resolveWorkspaceID(r)),
		Column2:     r.URL.Query().Get("status"),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list proposals")
		return
	}
	out := make([]PromptProposalResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, proposalToResponse(row))
	}
	writeJSON(w, http.StatusOK, out)
}

// PromptProposalRequest is the create/update body.
type PromptProposalRequest struct {
	CarrierScope        string              `json:"carrier_scope"`
	CarrierScopeID      string              `json:"carrier_scope_id"`
	TargetSection       string              `json:"target_section"`
	ChangeKind          string              `json:"change_kind"`
	ClauseName          string              `json:"clause_name"`
	ClauseText          string              `json:"clause_text"`
	GateAnswerLayer     string              `json:"gate_answer_layer"`
	GateAnswerRetention string              `json:"gate_answer_retention"`
	GateAnswerCost      string              `json:"gate_answer_cost"`
	GateAnswerConflict  string              `json:"gate_answer_conflict"`
	GateAnswerDedup     string              `json:"gate_answer_dedup"`
	EvidenceAnchors     []map[string]string `json:"evidence_anchors"`
}

func (req PromptProposalRequest) validate() string {
	if !legislation.ValidScope(req.CarrierScope) {
		return "unknown carrier_scope"
	}
	if _, err := util.ParseUUID(req.CarrierScopeID); err != nil {
		return "invalid carrier_scope_id"
	}
	return validateLegislationProposal(req.ChangeKind, req.ClauseName, req.ClauseText)
}

// CreatePromptProposal — POST /api/prompt-legislation/proposals
func (h *Handler) CreatePromptProposal(w http.ResponseWriter, r *http.Request) {
	var req PromptProposalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if msg := req.validate(); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	workspaceID := parseUUID(h.resolveWorkspaceID(r))
	// Carrier entity must exist in this workspace — the house rule's
	// "validated against the scope's owning table on every write path".
	if _, ok := h.readScopeEffectiveContent(w, r, promptVersionScope(req.CarrierScope), workspaceID, parseUUID(req.CarrierScopeID)); !ok {
		return
	}

	userID, actorType, ok := h.legislationActor(w, r)
	if !ok {
		return
	}
	anchors := req.EvidenceAnchors
	if anchors == nil {
		anchors = []map[string]string{}
	}
	created, err := h.Queries.CreatePromptProposal(r.Context(), db.CreatePromptProposalParams{
		WorkspaceID:         workspaceID,
		CarrierScope:        req.CarrierScope,
		CarrierScopeID:      parseUUID(req.CarrierScopeID),
		TargetSection:       req.TargetSection,
		ChangeKind:          req.ChangeKind,
		ClauseName:          req.ClauseName,
		ClauseText:          req.ClauseText,
		GateAnswerLayer:     req.GateAnswerLayer,
		GateAnswerRetention: req.GateAnswerRetention,
		GateAnswerCost:      req.GateAnswerCost,
		GateAnswerConflict:  req.GateAnswerConflict,
		GateAnswerDedup:     req.GateAnswerDedup,
		EvidenceAnchors:     mustJSON(anchors),
		Source:              "manual",
		CreatedByType:       actorType,
		CreatedByID:         parseUUID(userID),
		AuditLog:            legislationAudit("created", actorType, userID, nil),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create proposal")
		return
	}
	writeJSON(w, http.StatusCreated, proposalToResponse(created))
}

// UpdatePromptProposal — PATCH /api/prompt-legislation/proposals/{id}
// Draft-only edit; creator or owner.
func (h *Handler) UpdatePromptProposal(w http.ResponseWriter, r *http.Request) {
	p, ok := h.loadPromptProposalForWrite(w, r)
	if !ok {
		return
	}
	var req PromptProposalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if msg := req.validate(); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	workspaceID := parseUUID(h.resolveWorkspaceID(r))
	if _, ok := h.readScopeEffectiveContent(w, r, promptVersionScope(req.CarrierScope), workspaceID, parseUUID(req.CarrierScopeID)); !ok {
		return
	}
	anchors := req.EvidenceAnchors
	if anchors == nil {
		anchors = []map[string]string{}
	}
	updated, err := h.Queries.UpdatePromptProposalDraft(r.Context(), db.UpdatePromptProposalDraftParams{
		ID:                  p.ID,
		WorkspaceID:         workspaceID,
		CarrierScope:        req.CarrierScope,
		CarrierScopeID:      parseUUID(req.CarrierScopeID),
		TargetSection:       req.TargetSection,
		ChangeKind:          req.ChangeKind,
		ClauseName:          req.ClauseName,
		ClauseText:          req.ClauseText,
		GateAnswerLayer:     req.GateAnswerLayer,
		GateAnswerRetention: req.GateAnswerRetention,
		GateAnswerCost:      req.GateAnswerCost,
		GateAnswerConflict:  req.GateAnswerConflict,
		GateAnswerDedup:     req.GateAnswerDedup,
		EvidenceAnchors:     mustJSON(anchors),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusConflict, "proposal is no longer a draft")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to update proposal")
		return
	}
	writeJSON(w, http.StatusOK, proposalToResponse(updated))
}

// jevAdvisory returns the advisory client for this handler: an injected
// client (tests, staging) wins, otherwise the process default built from the
// MULTICA_JEV_ADVISORY_* environment.
func (h *Handler) jevAdvisory() *legislation.AdvisoryClient {
	if h.JevAdvisory != nil {
		return h.JevAdvisory
	}
	return legislation.AdvisoryFromEnv()
}

// SubmitPromptProposal — POST /api/prompt-legislation/proposals/{id}/submit
func (h *Handler) SubmitPromptProposal(w http.ResponseWriter, r *http.Request) {
	p, ok := h.loadPromptProposalForWrite(w, r)
	if !ok {
		return
	}
	userID, actorType, _ := h.legislationActor(w, r)
	// Warn-only precheck (RUYI-347): the clause text is judged before the
	// row leaves draft. A disabled layer persists NULL (未检/关态); any fault
	// degrades into the stored skip report and never fails the submit.
	advisory := h.jevAdvisory().Assess(r.Context(), "submit", p.ClauseText).JSON()
	submitted, err := h.Queries.SubmitPromptProposal(r.Context(), db.SubmitPromptProposalParams{
		ID:          p.ID,
		WorkspaceID: parseUUID(h.resolveWorkspaceID(r)),
		Audit:       legislationAudit("submitted", actorType, userID, nil),
		JevAdvisory: advisory,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusConflict, "only draft proposals can be submitted")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to submit proposal")
		return
	}
	writeJSON(w, http.StatusOK, proposalToResponse(submitted))
}

// PromptProposalPreviewResponse is the approval-facing diff payload: the
// full-carrier line diff (dry-run patch 8) plus the verdict inputs.
type PromptProposalPreviewResponse struct {
	Proposal      PromptProposalResponse `json:"proposal"`
	Diff          []legislation.DiffLine `json:"diff"`
	CurrentSha256 string                 `json:"current_sha256"`
	BaselineUsed  bool                   `json:"baseline_used"`
}

// PreviewPromptProposal — POST /api/prompt-legislation/proposals/{id}/preview
// Synthesizes the sandbox text and returns the full-carrier line diff. Read-
// only against the carrier (no row lock): previews may interleave with other
// work, approval re-locks and re-derives everything.
func (h *Handler) PreviewPromptProposal(w http.ResponseWriter, r *http.Request) {
	p, ok := h.loadPromptProposal(w, r)
	if !ok {
		return
	}
	workspaceID := parseUUID(h.resolveWorkspaceID(r))
	current, ok := h.readScopeEffectiveContent(w, r, promptVersionScope(p.CarrierScope), workspaceID, p.CarrierScopeID)
	if !ok {
		return
	}
	synth, err := legislation.Synthesize(current, proposalInputFromRow(p))
	if err != nil {
		writeJSON(w, http.StatusOK, PromptProposalPreviewResponse{
			Proposal:      proposalToResponse(p),
			Diff:          []legislation.DiffLine{{Kind: "context", Text: fmt.Sprintf("草案无法合成：%v", err)}},
			CurrentSha256: sha256Hex(current),
		})
		return
	}
	baselineUsed := true
	if _, err := h.Queries.GetPromptStructureBaseline(r.Context(), db.GetPromptStructureBaselineParams{
		CarrierScope: p.CarrierScope, CarrierScopeID: p.CarrierScopeID,
	}); err != nil {
		baselineUsed = false
	}
	writeJSON(w, http.StatusOK, PromptProposalPreviewResponse{
		Proposal:      proposalToResponse(p),
		Diff:          legislation.DiffLines(current, synth),
		CurrentSha256: sha256Hex(current),
		BaselineUsed:  baselineUsed,
	})
}

// ApprovePromptProposalRequest — the injection-defence flag.
type ApprovePromptProposalRequest struct {
	ConfirmDiffPreviewed bool `json:"confirm_diff_previewed"`
}

// ApprovePromptProposal — POST /api/prompt-legislation/proposals/{id}/approve
// Owner-only (route-level). Runs the gate synchronously: enacted or
// gate_failed in the caller's transaction.
func (h *Handler) ApprovePromptProposal(w http.ResponseWriter, r *http.Request) {
	var req ApprovePromptProposalRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	h.approveOne(w, r, chi.URLParam(r, "id"), req.ConfirmDiffPreviewed)
}

// interceptWriter buffers an approveOne response so the batch path can
// report per-id outcomes instead of leaking the first writer's bytes.
type interceptWriter struct {
	header http.Header
	buf    bytes.Buffer
	status int
}

func (w *interceptWriter) Header() http.Header { return w.header }
func (w *interceptWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.buf.Write(b)
}
func (w *interceptWriter) WriteHeader(code int) { w.status = code }

// BatchApprovePromptProposals — POST /api/prompt-legislation/proposals/approve-batch
// Owner-only. Each id goes through the identical approve path in its own
// transaction; one gate failure does not stop the rest. The response lists
// per-id outcomes in input order.
func (h *Handler) BatchApprovePromptProposals(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs                  []string `json:"ids"`
		ConfirmDiffPreviewed bool     `json:"confirm_diff_previewed"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if !req.ConfirmDiffPreviewed {
		writeError(w, http.StatusBadRequest, "approval requires confirm_diff_previewed (the full-text diff must be reviewed first)")
		return
	}
	if len(req.IDs) == 0 {
		writeError(w, http.StatusBadRequest, "ids must not be empty")
		return
	}
	if len(req.IDs) > 20 {
		writeError(w, http.StatusBadRequest, "batch approve is capped at 20 proposals")
		return
	}
	results := make([]map[string]any, 0, len(req.IDs))
	for _, id := range req.IDs {
		w2 := &interceptWriter{header: http.Header{}}
		h.approveOne(w2, r, id, true)
		results = append(results, map[string]any{
			"id":     id,
			"status": w2.status,
			"body":   w2.buf.String(),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

// approveOne is the shared single-approve core. It writes its own response,
// which lets the batch path reuse it verbatim with an intercepted writer.
func (h *Handler) approveOne(w http.ResponseWriter, r *http.Request, id string, confirmDiffPreviewed bool) {
	p, ok := h.loadPromptProposalByID(w, r, id)
	if !ok {
		return
	}
	if p.Status != "pending_owner" && p.Status != "approved" {
		writeError(w, http.StatusConflict, "only pending_owner (or an approved row left by a crash) can be approved")
		return
	}
	if !confirmDiffPreviewed && p.Status == "pending_owner" {
		writeError(w, http.StatusBadRequest, "approval requires confirm_diff_previewed (the full-text diff must be reviewed first)")
		return
	}
	userID, _, _ := h.legislationActor(w, r)
	workspaceID := parseUUID(h.resolveWorkspaceID(r))

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to approve proposal")
		return
	}
	defer tx.Rollback(r.Context()) //nolint:errcheck
	qtx := h.Queries.WithTx(tx)

	audit := legislationAudit("approved", "member", userID, nil)
	if p.Status == "pending_owner" {
		approved, err := qtx.ApprovePromptProposal(r.Context(), db.ApprovePromptProposalParams{
			ID: p.ID, WorkspaceID: workspaceID, Audit: audit,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeError(w, http.StatusConflict, "proposal is no longer pending approval")
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to approve proposal")
			return
		}
		p = approved
	}

	enacted, res := h.runGateAndEnactTx(r.Context(), r, qtx, workspaceID, p, audit)
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to approve proposal")
		return
	}
	if !enacted {
		slog.Info("prompt proposal gate-failed on approve", append(logger.RequestAttrs(r),
			"proposal_id", uuidToString(p.ID), "errors", len(res.Errors))...)
		writeGateReport(w, res)
		return
	}
	reloaded, err := h.Queries.GetPromptProposal(r.Context(), db.GetPromptProposalParams{ID: p.ID, WorkspaceID: workspaceID})
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"status": "enacted"})
		return
	}
	writeJSON(w, http.StatusOK, proposalToResponse(reloaded))
}

// EnactPromptProposal — POST /api/prompt-legislation/proposals/{id}/enact
// Owner-only recovery path: an `approved` row left behind by a mid-flight
// crash is re-gated and enacted. No preview flag — the approval (with its
// forced preview) already happened to reach `approved`.
func (h *Handler) EnactPromptProposal(w http.ResponseWriter, r *http.Request) {
	p, ok := h.loadPromptProposal(w, r)
	if !ok {
		return
	}
	if p.Status != "approved" {
		writeError(w, http.StatusConflict, "only an approved proposal can be enacted (approve is the normal path)")
		return
	}
	userID, _, _ := h.legislationActor(w, r)
	workspaceID := parseUUID(h.resolveWorkspaceID(r))

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to enact proposal")
		return
	}
	defer tx.Rollback(r.Context()) //nolint:errcheck
	qtx := h.Queries.WithTx(tx)

	audit := legislationAudit("enacted", "member", userID, map[string]any{"path": "recovery"})
	enacted, res := h.runGateAndEnactTx(r.Context(), r, qtx, workspaceID, p, audit)
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to enact proposal")
		return
	}
	if !enacted {
		writeGateReport(w, res)
		return
	}
	reloaded, err := h.Queries.GetPromptProposal(r.Context(), db.GetPromptProposalParams{ID: p.ID, WorkspaceID: workspaceID})
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"status": "enacted"})
		return
	}
	writeJSON(w, http.StatusOK, proposalToResponse(reloaded))
}

// RejectPromptProposalRequest carries the audited rejection reason.
type RejectPromptProposalRequest struct {
	Reason string `json:"reason"`
}

// RejectPromptProposal — POST /api/prompt-legislation/proposals/{id}/reject
// Owner-only; rejected rows keep their record (B3 语义, 留档).
func (h *Handler) RejectPromptProposal(w http.ResponseWriter, r *http.Request) {
	p, ok := h.loadPromptProposal(w, r)
	if !ok {
		return
	}
	var req RejectPromptProposalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len([]rune(req.Reason)) == 0 {
		writeError(w, http.StatusBadRequest, "reason is required")
		return
	}
	userID, _, _ := h.legislationActor(w, r)
	rejected, err := h.Queries.RejectPromptProposal(r.Context(), db.RejectPromptProposalParams{
		ID:          p.ID,
		WorkspaceID: parseUUID(h.resolveWorkspaceID(r)),
		Audit:       legislationAudit("rejected", "member", userID, map[string]any{"reason": req.Reason}),
		Reason:      req.Reason,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusConflict, "only pending_owner proposals can be rejected")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to reject proposal")
		return
	}
	writeJSON(w, http.StatusOK, proposalToResponse(rejected))
}

// ReworkPromptProposal — POST /api/prompt-legislation/proposals/{id}/rework
// gate_failed → draft; the creator (or an owner, via the same endpoint)
// fixes the clause text or the five answers.
func (h *Handler) ReworkPromptProposal(w http.ResponseWriter, r *http.Request) {
	p, ok := h.loadPromptProposalForWrite(w, r)
	if !ok {
		return
	}
	userID, actorType, _ := h.legislationActor(w, r)
	reworked, err := h.Queries.ReworkPromptProposal(r.Context(), db.ReworkPromptProposalParams{
		ID:          p.ID,
		WorkspaceID: parseUUID(h.resolveWorkspaceID(r)),
		Audit:       legislationAudit("rework", actorType, userID, nil),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusConflict, "only gate_failed proposals can be sent back to draft")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to rework proposal")
		return
	}
	writeJSON(w, http.StatusOK, proposalToResponse(reworked))
}

// RestorePromptProposal — POST /api/prompt-legislation/proposals/{id}/restore
// Owner-only; rejected → draft (audited re-entry, B3).
func (h *Handler) RestorePromptProposal(w http.ResponseWriter, r *http.Request) {
	p, ok := h.loadPromptProposal(w, r)
	if !ok {
		return
	}
	userID, _, _ := h.legislationActor(w, r)
	restored, err := h.Queries.RestorePromptProposal(r.Context(), db.RestorePromptProposalParams{
		ID:          p.ID,
		WorkspaceID: parseUUID(h.resolveWorkspaceID(r)),
		Audit:       legislationAudit("restored", "member", userID, nil),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusConflict, "only rejected proposals can be restored")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to restore proposal")
		return
	}
	writeJSON(w, http.StatusOK, proposalToResponse(restored))
}

// ── Shared loaders ──────────────────────────────────────────────────────────

func (h *Handler) loadPromptProposal(w http.ResponseWriter, r *http.Request) (db.PromptProposal, bool) {
	return h.loadPromptProposalByID(w, r, chi.URLParam(r, "id"))
}

func (h *Handler) loadPromptProposalByID(w http.ResponseWriter, r *http.Request, id string) (db.PromptProposal, bool) {
	pid, ok := parseUUIDOrBadRequest(w, id, "id")
	if !ok {
		return db.PromptProposal{}, false
	}
	p, err := h.Queries.GetPromptProposal(r.Context(), db.GetPromptProposalParams{
		ID: pid, WorkspaceID: parseUUID(h.resolveWorkspaceID(r)),
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "proposal not found")
		return db.PromptProposal{}, false
	}
	return p, true
}

// loadPromptProposalForWrite gates draft-affecting actions to the proposal's
// creator or a workspace owner. The role check here is read-only on purpose:
// requireWorkspaceRole writes its own 403, which would race this handler's
// creator-allow path and turn a creator's own submit into a 403.
func (h *Handler) loadPromptProposalForWrite(w http.ResponseWriter, r *http.Request) (db.PromptProposal, bool) {
	p, ok := h.loadPromptProposal(w, r)
	if !ok {
		return db.PromptProposal{}, false
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return db.PromptProposal{}, false
	}
	if p.CreatedByType == "member" && uuidToString(p.CreatedByID) == userID {
		return p, true
	}
	if member, err := h.getWorkspaceMember(r.Context(), userID, h.resolveWorkspaceID(r)); err == nil && roleAllowed(member.Role, "owner") {
		return p, true
	}
	writeError(w, http.StatusForbidden, "only the proposal's creator or a workspace owner can modify it")
	return db.PromptProposal{}, false
}

// legislationActor resolves the acting identity for audit rows. Human-only
// routes guarantee a user; the actor type is recorded as member.
func (h *Handler) legislationActor(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return "", "", false
	}
	return userID, "member", true
}

// readScopeEffectiveContent is the lockless read counterpart of
// lockScopeEntity: 404 when the scope entity does not exist in the workspace.
func (h *Handler) readScopeEffectiveContent(w http.ResponseWriter, r *http.Request, scope promptVersionScope, workspaceID, scopeID pgtype.UUID) (string, bool) {
	ctx := r.Context()
	var (
		content string
		err     error
	)
	switch scope {
	case promptVersionScopeWorkspace:
		if scopeID != workspaceID {
			writeError(w, http.StatusNotFound, "scope not found in this workspace")
			return "", false
		}
		content, err = h.Queries.GetWorkspacePromptContent(ctx, scopeID)
	case promptVersionScopeProject:
		content, err = h.Queries.GetProjectPromptContent(ctx, db.GetProjectPromptContentParams{ID: scopeID, WorkspaceID: workspaceID})
	case promptVersionScopeSquad:
		content, err = h.Queries.GetSquadPromptContent(ctx, db.GetSquadPromptContentParams{ID: scopeID, WorkspaceID: workspaceID})
	case promptVersionScopeAgent:
		content, err = h.Queries.GetAgentPromptContent(ctx, db.GetAgentPromptContentParams{ID: scopeID, WorkspaceID: workspaceID})
	}
	if err != nil {
		writeError(w, http.StatusNotFound, "scope not found in this workspace")
		return "", false
	}
	return content, true
}

// ── Structure baseline read ─────────────────────────────────────────────────

// GetPromptStructureBaselineHandler — GET /api/prompt-governance/{scope}/{scopeId}/baseline
func (h *Handler) GetPromptStructureBaselineHandler(w http.ResponseWriter, r *http.Request) {
	scope, ok := parsePromptVersionScope(chi.URLParam(r, "scope"))
	if !ok {
		writeError(w, http.StatusBadRequest, "unknown scope")
		return
	}
	scopeID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "scopeId"), "scope_id")
	if !ok {
		return
	}
	row, err := h.Queries.GetPromptStructureBaseline(r.Context(), db.GetPromptStructureBaselineParams{
		CarrierScope: string(scope), CarrierScopeID: scopeID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, http.StatusOK, map[string]any{"exists": false})
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to read baseline")
		return
	}
	var b BaselineJSON
	_ = json.Unmarshal(row.Sections, &b.Sections)
	_ = json.Unmarshal(row.Clauses, &b.Clauses)
	writeJSON(w, http.StatusOK, map[string]any{
		"exists":         true,
		"sections":       b.Sections,
		"clauses":        b.Clauses,
		"content_sha256": row.ContentSha256,
		"updated_at":     row.UpdatedAt.Time.UTC().Format(httpTimeFormat),
	})
}

// ── Retrospective config / runs / manual trigger ────────────────────────────

// GetRetrospectiveConfig — GET /api/retrospective/config
func (h *Handler) GetRetrospectiveConfig(w http.ResponseWriter, r *http.Request) {
	workspaceID := parseUUID(h.resolveWorkspaceID(r))
	cfg, err := h.Queries.GetRetrospectiveConfig(r.Context(), workspaceID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, "failed to read retrospective config")
		return
	}
	var agentName string
	if err == nil && cfg.AgentID.Valid {
		// Best-effort display name: an agent archived after saving still
		// shows by name; failure leaves the id as the only handle.
		if a, agentErr := h.Queries.GetAgent(r.Context(), cfg.AgentID); agentErr == nil {
			agentName = a.Name
		}
	}
	var cfgPtr *db.RetrospectiveConfig
	if err == nil {
		cfgPtr = &cfg
	}
	writeJSON(w, http.StatusOK, retrospectiveConfigResponse(cfgPtr, agentName))
}

// retrospectiveConfigResponse projects the saved config (RUYI-552 direction
// 3): the selected agent rides flat, the name resolved best-effort for the
// selector's display label.
func retrospectiveConfigResponse(cfg *db.RetrospectiveConfig, agentName string) map[string]any {
	enabled, include, days := false, false, int32(1)
	var agentID string
	if cfg != nil {
		enabled, include, days = cfg.Enabled, cfg.IncludeInReview, cfg.WindowDays
		agentID = uuidToString(cfg.AgentID)
	}
	return map[string]any{
		"enabled":           enabled,
		"include_in_review": include,
		"window_days":       days,
		"agent_id":          agentID,
		"agent_name":        agentName,
	}
}

// UpdateRetrospectiveConfig — PUT /api/retrospective/config (owner-only)
func (h *Handler) UpdateRetrospectiveConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled         *bool   `json:"enabled"`
		IncludeInReview *bool   `json:"include_in_review"`
		WindowDays      *int    `json:"window_days"`
		AgentID         *string `json:"agent_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	workspaceID := parseUUID(h.resolveWorkspaceID(r))
	current, err := h.Queries.GetRetrospectiveConfig(r.Context(), workspaceID)
	enabled, include, days := false, false, 1
	var agentID pgtype.UUID
	if err == nil {
		enabled, include, days = current.Enabled, current.IncludeInReview, int(current.WindowDays)
		agentID = current.AgentID
	} else if !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, "failed to read retrospective config")
		return
	}
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	if req.IncludeInReview != nil {
		include = *req.IncludeInReview
	}
	if req.WindowDays != nil {
		days = *req.WindowDays
	}
	if days < 1 || days > 30 {
		writeError(w, http.StatusBadRequest, "window_days must be between 1 and 30")
		return
	}
	if req.AgentID != nil {
		// "" clears the selection; non-empty must resolve to a runnable
		// agent in this workspace (exists, not archived, has a runtime).
		if *req.AgentID == "" {
			agentID = pgtype.UUID{}
		} else {
			parsed, parseErr := util.ParseUUID(*req.AgentID)
			if parseErr != nil {
				writeError(w, http.StatusBadRequest, "agent_id is not a valid UUID")
				return
			}
			agent, agentErr := h.Queries.GetAgent(r.Context(), parsed)
			if agentErr != nil || uuidToString(agent.WorkspaceID) != uuidToString(workspaceID) {
				writeError(w, http.StatusBadRequest, "agent not found in this workspace")
				return
			}
			if agent.ArchivedAt.Valid {
				writeError(w, http.StatusBadRequest, "agent is archived")
				return
			}
			if !agent.RuntimeID.Valid {
				writeError(w, http.StatusBadRequest, "agent has no runtime")
				return
			}
			agentID = parsed
		}
	}
	if enabled && !agentID.Valid {
		writeError(w, http.StatusBadRequest, "enabling the retrospective requires a selected agent")
		return
	}
	requestUserID := requestUserID(r)
	var originator pgtype.UUID
	if parsed, err := util.ParseUUID(requestUserID); err == nil {
		originator = parsed
	}
	cfg, err := h.Queries.UpsertRetrospectiveConfig(r.Context(), db.UpsertRetrospectiveConfigParams{
		WorkspaceID:     workspaceID,
		Enabled:         enabled,
		IncludeInReview: include,
		WindowDays:      int32(days),
		AgentID:         agentID,
		UpdatedBy:       originator,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save retrospective config")
		return
	}
	var agentName string
	if cfg.AgentID.Valid {
		if a, agentErr := h.Queries.GetAgent(r.Context(), cfg.AgentID); agentErr == nil {
			agentName = a.Name
		}
	}
	writeJSON(w, http.StatusOK, retrospectiveConfigResponse(&cfg, agentName))
}

// ListRetrospectiveRuns — GET /api/retrospective/runs
func (h *Handler) ListRetrospectiveRuns(w http.ResponseWriter, r *http.Request) {
	runs, err := h.Queries.ListRetrospectiveRuns(r.Context(), parseUUID(h.resolveWorkspaceID(r)))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list retrospective runs")
		return
	}
	out := make([]map[string]any, 0, len(runs))
	for _, run := range runs {
		out = append(out, retrospectiveRunToResponse(run))
	}
	writeJSON(w, http.StatusOK, out)
}

func retrospectiveRunToResponse(run db.RetrospectiveRun) map[string]any {
	resp := map[string]any{
		"id":                 uuidToString(run.ID),
		"status":             run.Status,
		"trigger":            run.Trigger,
		"window_start":       run.WindowStart.Time.UTC().Format(httpTimeFormat),
		"window_end":         run.WindowEnd.Time.UTC().Format(httpTimeFormat),
		"issues_scanned":     run.IssuesScanned,
		"issues_analyzed":    run.IssuesAnalyzed,
		"proposals_created":  run.ProposalsCreated,
		"proposals_merged":   run.ProposalsMerged,
		"duplicates_skipped": run.DuplicatesSkipped,
		"error":              run.Error,
		// Raw JSONB (carries issue_ids: the window membership recorded at
		// trigger, validated all-or-nothing at completion); json.RawMessage
		// so it embeds as an object, not base64.
		"detail":     json.RawMessage(run.Detail),
		"created_at": run.CreatedAt.Time.UTC().Format(httpTimeFormat),
	}
	if run.FinishedAt.Valid {
		resp["finished_at"] = run.FinishedAt.Time.UTC().Format(httpTimeFormat)
	}
	return resp
}

// TriggerRetrospectiveRun — POST /api/retrospective/run (owner-only, manual)
// Enqueues one retrospective run through the platform's own agent-task
// trigger (RUYI-552 direction 3): the selected agent's single Run reads the
// window and submits improvement drafts — no Issue is created and no comment
// is posted anywhere. The run record carries the outcome; agent execution is
// asynchronous from this caller's point of view.
func (h *Handler) TriggerRetrospectiveRun(w http.ResponseWriter, r *http.Request) {
	if h.RetrospectiveRunner == nil {
		writeError(w, http.StatusInternalServerError, "retrospective runner is not wired")
		return
	}
	stats, err := h.RetrospectiveRunner.RunWorkspace(r.Context(), h.resolveWorkspaceID(r), "manual")
	if err != nil {
		if errors.Is(err, retrospective.ErrNoConfig) {
			writeError(w, http.StatusBadRequest, "retrospective is not enabled for this workspace")
			return
		}
		if errors.Is(err, retrospective.ErrNoAgent) {
			// Same contract the old llm_not_configured error had: the UI
			// localizes off the code; the sentence is fallback text.
			writeErrorCode(w, http.StatusConflict, "agent_not_configured", "未配置可用的执行智能体：请先在每日总复盘配置中选择一个智能体")
			return
		}
		// The run record holds the failure; still report it to the caller.
		if stats != nil {
			writeJSON(w, http.StatusOK, map[string]any{
				"run": stats, "note": "run finished with failure; see the run record",
			})
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to run retrospective")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"run": stats})
}

// RetrospectiveTaskEnqueuer adapts the Handler into the Runner's enqueue
// port: it persists the no-issue agent_task_queue row via the fenced INSERT
// and wakes the daemon through the task service (same notify path as
// mention-triggered tasks), keeping trigger-audit semantics intact.
func (h *Handler) RetrospectiveTaskEnqueuer() retrospective.TaskEnqueuer {
	return func(ctx context.Context, params retrospective.EnqueueParams) (string, error) {
		var originator pgtype.UUID
		if params.OriginatorUserID != "" {
			if parsed, err := util.ParseUUID(params.OriginatorUserID); err == nil {
				originator = parsed
			}
		}
		runID, runErr := util.ParseUUID(params.RunID)
		if runErr != nil {
			return "", fmt.Errorf("retrospective task enqueue: invalid run id: %w", runErr)
		}
		task, err := h.Queries.CreateRetrospectiveTask(ctx, db.CreateRetrospectiveTaskParams{
			AgentID:           util.MustParseUUID(params.AgentID),
			RuntimeID:         util.MustParseUUID(params.RuntimeID),
			OriginatorUserID:  originator,
			AccountableUserID: originator,
			Priority:          params.Priority,
			RunID:             runID,
			Context:           params.Context,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return "", fmt.Errorf("retrospective task enqueue refused: agent or workspace is gone")
			}
			return "", err
		}
		if h.TaskService != nil {
			h.TaskService.NotifyTaskEnqueued(ctx, task)
		}
		return uuidToString(task.ID), nil
	}
}
