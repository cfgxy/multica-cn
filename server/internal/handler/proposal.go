package handler

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// Self-evolution daily proposals (RUYI-265, spec §A, B1–B3).
//
// B1: every proposal carries a falsifiable prophecy, validated at creation
// and never rewritten afterwards — no endpoint updates the prophecy column.
// B2: the Owner's adoption decision (adoption_snapshot) and the verification
// of whether the prophecy held (verification) are separate records; neither
// implies the other.
// B3: rejected proposals keep their full row for retrieval but the adopt
// path structurally refuses them (restore first), and they never gain a
// prompt/skill version link — the quality curve is driven by version rows
// only, so rejected proposals cannot enter it.

type ProposalResponse struct {
	ID                 string         `json:"id"`
	Type               string         `json:"type"`
	Status             string         `json:"status"`
	Title              string         `json:"title"`
	Summary            string         `json:"summary"`
	Evidence           []any          `json:"evidence"`
	Prophecy           map[string]any `json:"prophecy"`
	GenerationSnapshot map[string]any `json:"generation_snapshot"`
	AdoptionSnapshot   map[string]any `json:"adoption_snapshot,omitempty"`
	Verification       map[string]any `json:"verification,omitempty"`
	AuditLog           []any          `json:"audit_log"`
	TransferError      string         `json:"transfer_error,omitempty"`
	CreatedAt          time.Time      `json:"created_at"`
	UpdatedAt          time.Time      `json:"updated_at"`
}

// validateProposalProphecy enforces the §A.2 type matrix. Quantitative types
// (prompt_revision, skill) need an object, a direction and a numeric band;
// behavior types (project_cognition, lesson, pitfall) need an observable
// outcome and a falsification condition — no direction, no band.
func validateProposalProphecy(proposalType string, prophecy map[string]any) string {
	if len(prophecy) == 0 {
		return "a proposal requires a falsifiable prophecy before it can enter the pool"
	}
	object, hasObject := prophecy["object"].(map[string]any)
	outcome, _ := prophecy["outcome_text"].(string)
	switch proposalType {
	case "prompt_revision", "skill":
		direction, _ := prophecy["direction"].(string)
		low, lowOK := prophecy["range_low"].(float64)
		high, highOK := prophecy["range_high"].(float64)
		switch {
		case !hasObject || len(object) == 0:
			return "quantitative proposals must state the prophecy object"
		case outcome == "":
			return "quantitative proposals must state the expected outcome"
		case direction == "":
			return "quantitative proposals must state a direction (down, up or unchanged)"
		case !lowOK || !highOK:
			return "quantitative proposals must state the numeric band (range_low, range_high)"
		case low > high:
			return "range_low must not exceed range_high"
		}
	default:
		falsify, _ := prophecy["falsify_condition"].(string)
		switch {
		case outcome == "":
			return "behavior proposals must state the observable behavior change"
		case falsify == "":
			return "behavior proposals must state the falsification condition"
		}
	}
	return ""
}

// proposalPromptSnapshot captures the workspace prompt version currently in
// effect — the reference at creation time, the audit baseline at adoption.
// No prompt versions at all is a legitimate "no measurement yet" state and
// is stored as an explicit null, never as a zero.
func (h *Handler) proposalPromptSnapshot(r *http.Request, workspaceID pgtype.UUID) map[string]any {
	snapshot := map[string]any{"captured_at": time.Now().UTC().Format(time.RFC3339)}
	var version pgtype.Int4
	err := h.DB.QueryRow(r.Context(), `
SELECT version FROM prompt_version
WHERE workspace_id = $1 AND scope = 'workspace' AND scope_id = $1
ORDER BY version DESC LIMIT 1`, workspaceID).Scan(&version)
	if err != nil || !version.Valid {
		snapshot["prompt_version"] = nil
		return snapshot
	}
	snapshot["prompt_version"] = version.Int32
	return snapshot
}

func (h *Handler) proposalAppendAudit(r *http.Request, id pgtype.UUID, action string, detail map[string]any, actor string) {
	entry := map[string]any{"action": action, "actor": actor, "at": time.Now().UTC().Format(time.RFC3339)}
	if detail != nil {
		entry["detail"] = detail
	}
	encoded, err := json.Marshal([]any{entry})
	if err != nil {
		return
	}
	_, _ = h.DB.Exec(r.Context(),
		`UPDATE proposal SET audit_log = audit_log || $2::jsonb, updated_at = now() WHERE id = $1`,
		id, encoded)
}

// GetProposals serves GET /api/proposals: the pool with status/type filters.
// Rejected proposals are ordinary rows here — retention is the point (B3).
func (h *Handler) GetProposals(w http.ResponseWriter, r *http.Request) {
	workspaceID := parseUUID(h.resolveWorkspaceID(r))
	status, proposalType := r.URL.Query().Get("status"), r.URL.Query().Get("type")
	rows, err := h.DB.Query(r.Context(), `
SELECT id, type, status, title, summary, evidence, prophecy, generation_snapshot,
       adoption_snapshot, verification, audit_log, transfer_error, created_at, updated_at
FROM proposal
WHERE workspace_id = $1 AND ($2 = '' OR status = $2) AND ($3 = '' OR type = $3)
ORDER BY created_at DESC LIMIT 200`, workspaceID, status, proposalType)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read proposals")
		return
	}
	defer rows.Close()
	proposals := make([]ProposalResponse, 0, 16)
	for rows.Next() {
		proposal, ok := scanProposalRow(w, rows)
		if !ok {
			return
		}
		proposals = append(proposals, proposal)
	}
	if rows.Err() != nil {
		writeError(w, http.StatusInternalServerError, "failed to read proposals")
		return
	}
	writeJSON(w, http.StatusOK, proposals)
}

func scanProposalRow(w http.ResponseWriter, rows interface{ Scan(dest ...any) error }) (ProposalResponse, bool) {
	var proposal ProposalResponse
	var id pgtype.UUID
	var evidence, prophecy, generation, audit []byte
	var adoption, verification []byte
	if err := rows.Scan(&id, &proposal.Type, &proposal.Status, &proposal.Title, &proposal.Summary,
		&evidence, &prophecy, &generation, &adoption, &verification, &audit,
		&proposal.TransferError, &proposal.CreatedAt, &proposal.UpdatedAt); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read proposals")
		return proposal, false
	}
	proposal.ID = uuidToString(id)
	proposal.Evidence = decodeJSONSlice(evidence)
	proposal.Prophecy = decodeJSONMap(prophecy)
	proposal.GenerationSnapshot = decodeJSONMap(generation)
	proposal.AuditLog = decodeJSONSlice(audit)
	if adoption != nil {
		proposal.AdoptionSnapshot = decodeJSONMap(adoption)
	}
	if verification != nil {
		proposal.Verification = decodeJSONMap(verification)
	}
	return proposal, true
}

func decodeJSONMap(raw []byte) map[string]any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	if m == nil {
		m = map[string]any{}
	}
	return m
}

func decodeJSONSlice(raw []byte) []any {
	if len(raw) == 0 {
		return []any{}
	}
	var s []any
	_ = json.Unmarshal(raw, &s)
	if s == nil {
		s = []any{}
	}
	return s
}

// PostProposal serves POST /api/proposals. The prophecy is required for
// every type (B1: 无预言不入池 — the server refuses, not just the form) and
// is frozen from here on. The generation snapshot is captured server-side;
// clients never supply their own baseline.
func (h *Handler) PostProposal(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := parseUUID(h.resolveWorkspaceID(r))
	var body struct {
		Type     string         `json:"type"`
		Title    string         `json:"title"`
		Summary  string         `json:"summary"`
		Evidence []any          `json:"evidence"`
		Prophecy map[string]any `json:"prophecy"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid proposal body")
		return
	}
	validTypes := map[string]bool{
		"prompt_revision": true, "project_cognition": true, "lesson": true, "pitfall": true, "skill": true,
	}
	if !validTypes[body.Type] {
		writeError(w, http.StatusBadRequest, "type must be one of prompt_revision, project_cognition, lesson, pitfall, skill")
		return
	}
	if message := validateProposalProphecy(body.Type, body.Prophecy); message != "" {
		writeError(w, http.StatusUnprocessableEntity, message)
		return
	}
	if body.Evidence == nil {
		body.Evidence = []any{}
	}
	encodedProphecy, _ := json.Marshal(body.Prophecy)
	encodedEvidence, _ := json.Marshal(body.Evidence)
	encodedGeneration, _ := json.Marshal(h.proposalPromptSnapshot(r, workspaceID))
	var id pgtype.UUID
	if err := h.DB.QueryRow(r.Context(), `
INSERT INTO proposal (workspace_id, type, title, summary, evidence, prophecy, generation_snapshot, created_by_type, created_by_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, 'member', NULLIF($8, '')::uuid)
RETURNING id`,
		workspaceID, body.Type, body.Title, body.Summary, encodedEvidence, encodedProphecy,
		encodedGeneration, userID).Scan(&id); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create the proposal")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": uuidToString(id)})
}

// AdoptProposal serves POST /api/proposals/{id}/adopt: the Owner decision
// (B2's adoption half). It captures the adoption snapshot inside the same
// flow — the audit baseline verification compares against. Knowledge-type
// proposals (project_cognition/lesson/pitfall) additionally transfer into
// the workspace's ultimate knowledge directory in the same server flow
// (spec §A.3 r4): adoption is only recorded once the transfer succeeded and
// was read back; on failure the proposal stays un-adopted with the reason.
func (h *Handler) AdoptProposal(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := parseUUID(h.resolveWorkspaceID(r))
	proposalID, idOK := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "id")
	if !idOK {
		return
	}
	var proposalType, status string
	if err := h.DB.QueryRow(r.Context(),
		`SELECT type, status FROM proposal WHERE id = $1 AND workspace_id = $2`,
		proposalID, workspaceID).Scan(&proposalType, &status); err != nil {
		writeError(w, http.StatusNotFound, "proposal not found")
		return
	}
	// B3 structural isolation: rejected and archived proposals cannot be
	// adopted — a rejected idea re-entering the pool must go through the
	// explicit, audited restore action first.
	if status != "draft" && status != "needs_revision" {
		writeError(w, http.StatusConflict, "only draft or needs-revision proposals can be adopted; rejected proposals must be restored first")
		return
	}

	adoption := h.proposalPromptSnapshot(r, workspaceID)
	adoption["adopted_by"] = userID

	if proposalType == "project_cognition" || proposalType == "lesson" || proposalType == "pitfall" {
		var title, summary string
		if err := h.DB.QueryRow(r.Context(),
			`SELECT title, summary FROM proposal WHERE id = $1`, proposalID).Scan(&title, &summary); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load the proposal for transfer")
			return
		}
		key := "proposal-" + uuidToString(proposalID)[:8]
		content := title + "\n\n" + summary
		if _, err := h.knowledgeTransfer(r, workspaceID, key, content, userID); err != nil {
			_, _ = h.DB.Exec(r.Context(),
				`UPDATE proposal SET transfer_error = $2, updated_at = now() WHERE id = $1`,
				proposalID, err.Error())
			h.proposalAppendAudit(r, proposalID, "adopt_failed", map[string]any{"reason": err.Error()}, userID)
			writeError(w, http.StatusConflict, "adoption aborted: "+err.Error())
			return
		}
		adoption["knowledge_transfer"] = map[string]any{"key": key, "state": "transferred"}
	}
	encoded, _ := json.Marshal(adoption)
	tag, err := h.DB.Exec(r.Context(), `
UPDATE proposal SET status = 'adopted', adoption_snapshot = $2, transfer_error = '', updated_at = now()
WHERE id = $1 AND status IN ('draft', 'needs_revision')`, proposalID, encoded)
	if err != nil || tag.RowsAffected() == 0 {
		writeError(w, http.StatusInternalServerError, "failed to record the adoption")
		return
	}
	h.proposalAppendAudit(r, proposalID, "adopt", nil, userID)
	writeJSON(w, http.StatusOK, map[string]string{"status": "adopted"})
}

// RejectProposal serves POST /api/proposals/{id}/reject. Rejection is a
// terminal-but-retrievable state (B3): the row, prophecy and snapshots all
// stay; re-entry happens only through the audited restore action.
func (h *Handler) RejectProposal(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := parseUUID(h.resolveWorkspaceID(r))
	proposalID, idOK := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "id")
	if !idOK {
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	tag, err := h.DB.Exec(r.Context(), `
UPDATE proposal SET status = 'rejected', updated_at = now()
WHERE id = $1 AND workspace_id = $2 AND status IN ('draft', 'needs_revision')`,
		proposalID, workspaceID)
	if err != nil || tag.RowsAffected() == 0 {
		writeError(w, http.StatusConflict, "only draft or needs-revision proposals can be rejected")
		return
	}
	h.proposalAppendAudit(r, proposalID, "reject", map[string]any{"reason": body.Reason}, userID)
	writeJSON(w, http.StatusOK, map[string]string{"status": "rejected"})
}

// RestoreProposal serves POST /api/proposals/{id}/restore: rejected or
// archived proposals re-enter the pool as drafts, audited (§A.5).
func (h *Handler) RestoreProposal(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := parseUUID(h.resolveWorkspaceID(r))
	proposalID, idOK := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "id")
	if !idOK {
		return
	}
	tag, err := h.DB.Exec(r.Context(), `
UPDATE proposal SET status = 'draft', updated_at = now()
WHERE id = $1 AND workspace_id = $2 AND status IN ('rejected', 'archived')`,
		proposalID, workspaceID)
	if err != nil || tag.RowsAffected() == 0 {
		writeError(w, http.StatusConflict, "only rejected or archived proposals can be restored")
		return
	}
	h.proposalAppendAudit(r, proposalID, "restore", nil, userID)
	writeJSON(w, http.StatusOK, map[string]string{"status": "draft"})
}

// VerifyProposal serves POST /api/proposals/{id}/verify: the B2 verification
// half, recorded independently of adoption. Manual marks (the ②–④ carrier,
// and Owner overrides generally) append to a history — later marks never
// overwrite earlier ones — and every mark must carry evidence.
func (h *Handler) VerifyProposal(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := parseUUID(h.resolveWorkspaceID(r))
	proposalID, idOK := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "id")
	if !idOK {
		return
	}
	var body struct {
		Verdict  string `json:"verdict"`
		Evidence string `json:"evidence"`
		Note     string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid verification body")
		return
	}
	if body.Verdict != "established" && body.Verdict != "partial" && body.Verdict != "refuted" && body.Verdict != "undeterminable" {
		writeError(w, http.StatusBadRequest, "verdict must be established, partial, refuted or undeterminable")
		return
	}
	if body.Evidence == "" {
		writeError(w, http.StatusUnprocessableEntity, "a manual verification mark requires evidence")
		return
	}
	var status string
	var verification []byte
	if err := h.DB.QueryRow(r.Context(),
		`SELECT status, verification FROM proposal WHERE id = $1 AND workspace_id = $2`,
		proposalID, workspaceID).Scan(&status, &verification); err != nil {
		writeError(w, http.StatusNotFound, "proposal not found")
		return
	}
	if status != "adopted" {
		writeError(w, http.StatusConflict, "only adopted proposals can be verified — verification records what happened after adoption")
		return
	}
	record := decodeJSONMap(verification)
	marks, _ := record["marks"].([]any)
	record["marks"] = append(marks, map[string]any{
		"verdict": body.Verdict, "evidence": body.Evidence, "note": body.Note,
		"operator": userID, "at": time.Now().UTC().Format(time.RFC3339),
	})
	encoded, _ := json.Marshal(record)
	if _, err := h.DB.Exec(r.Context(),
		`UPDATE proposal SET verification = $2, updated_at = now() WHERE id = $1`,
		proposalID, encoded); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to record the verification")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "verified"})
}
