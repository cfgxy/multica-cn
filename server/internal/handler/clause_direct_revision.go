package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/legislation"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// DirectClauseRevisionRequest is the Owner-confirmed Clause revision payload.
// The caller supplies the exact values observed during the preceding preview;
// the write path checks them again while it owns both the Clause and carrier
// serialization locks.
type DirectClauseRevisionRequest struct {
	Content                     string `json:"content"`
	ExpectedClauseVersion       int32  `json:"expected_clause_version"`
	ExpectedClauseContentSHA256 string `json:"expected_clause_content_sha256"`
	ExpectedCarrierSHA256       string `json:"expected_carrier_sha256"`
	ConfirmPreviewed            bool   `json:"confirm_previewed"`
}

type directClauseRevisionPrepared struct {
	clause  resolvedClause
	binding db.ClauseBinding
	current string
	next    string
}

// DirectClauseRevisionPreviewResponse contains the canonical carrier diff and
// the exact version/hash tokens a subsequent confirmed write must echo.
type DirectClauseRevisionPreviewResponse struct {
	ClauseID                   string                 `json:"clause_id"`
	Slug                       string                 `json:"slug"`
	CarrierScope               string                 `json:"carrier_scope"`
	CarrierScopeID             string                 `json:"carrier_scope_id"`
	CurrentClauseVersion       int32                  `json:"current_clause_version"`
	NextClauseVersion          int32                  `json:"next_clause_version"`
	CurrentClauseContentSHA256 string                 `json:"current_clause_content_sha256"`
	CurrentCarrierSHA256       string                 `json:"current_carrier_sha256"`
	Diff                       []legislation.DiffLine `json:"diff"`
}

// DirectClauseRevisionResponse is the durable result of an Owner-confirmed
// direct revision. It intentionally contains no proposal id because this path
// never creates or advances a prompt_proposal.
type DirectClauseRevisionResponse struct {
	ClauseID             string `json:"clause_id"`
	Slug                 string `json:"slug"`
	EnactedClauseVersion int32  `json:"enacted_clause_version"`
	ClauseContentSHA256  string `json:"clause_content_sha256"`
	CarrierScope         string `json:"carrier_scope"`
	CarrierScopeID       string `json:"carrier_scope_id"`
	CarrierContentSHA256 string `json:"carrier_content_sha256"`
}

func (r DirectClauseRevisionRequest) validate(requireConfirmation bool) string {
	if strings.TrimSpace(r.Content) == "" {
		return "content must not be empty"
	}
	if len(r.Content) > 100_000 {
		return "content exceeds the maximum allowed size"
	}
	if r.ExpectedClauseVersion <= 0 {
		return "expected_clause_version must be positive"
	}
	if len(r.ExpectedClauseContentSHA256) != 64 || len(r.ExpectedCarrierSHA256) != 64 {
		return "expected content SHA256 values must be 64 characters"
	}
	if requireConfirmation && !r.ConfirmPreviewed {
		return "confirm_previewed must be true after the Owner has reviewed the preview"
	}
	return ""
}

func decodeDirectClauseRevisionRequest(w http.ResponseWriter, r *http.Request) (DirectClauseRevisionRequest, bool) {
	var req DirectClauseRevisionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid direct clause revision request")
		return DirectClauseRevisionRequest{}, false
	}
	return req, true
}

func checkDirectRevisionExpectation(req DirectClauseRevisionRequest, clause resolvedClause, version db.ClauseVersion, current string) error {
	if req.ExpectedClauseVersion != clause.Version {
		return fmt.Errorf("clause version changed from preview; read and preview again")
	}
	if req.ExpectedClauseContentSHA256 != version.ContentSha256 {
		return fmt.Errorf("clause content changed from preview; read and preview again")
	}
	if req.ExpectedCarrierSHA256 != sha256Hex(current) {
		return fmt.Errorf("carrier content changed from preview; preview again")
	}
	return nil
}

func (h *Handler) prepareDirectClauseRevision(ctx context.Context, qtx *db.Queries, workspaceID, clauseID pgtype.UUID, req DirectClauseRevisionRequest, lockCarrier bool) (directClauseRevisionPrepared, error) {
	clause, err := resolveProposalClauseTx(ctx, qtx, workspaceID, clauseID, "")
	if err != nil {
		return directClauseRevisionPrepared{}, err
	}
	if clause.Status != "active" {
		return directClauseRevisionPrepared{}, fmt.Errorf("条款「%s」当前状态为 %s，仅 active 条款可修订", clause.Title, clause.Status)
	}
	binding, err := qtx.GetActiveClauseBinding(ctx, clause.ID)
	if err != nil {
		return directClauseRevisionPrepared{}, fmt.Errorf("条款「%s」没有生效绑定，无法修订", clause.Title)
	}
	scope := promptVersionScope(binding.Scope)
	if !isFourTierPromptScope(scope) {
		return directClauseRevisionPrepared{}, fmt.Errorf("条款「%s」绑定到不支持直接修订的载体", clause.Title)
	}
	var current string
	if lockCarrier {
		current, err = lockScopeEntityResult(ctx, qtx, scope, workspaceID, binding.ScopeID)
		if err != nil {
			return directClauseRevisionPrepared{}, errors.New("carrier not found in this workspace")
		}
	} else {
		switch scope {
		case promptVersionScopeWorkspace:
			if binding.ScopeID != workspaceID {
				return directClauseRevisionPrepared{}, errors.New("carrier not found in this workspace")
			}
			current, err = qtx.GetWorkspacePromptContent(ctx, binding.ScopeID)
		case promptVersionScopeProject:
			current, err = qtx.GetProjectPromptContent(ctx, db.GetProjectPromptContentParams{ID: binding.ScopeID, WorkspaceID: workspaceID})
		case promptVersionScopeSquad:
			current, err = qtx.GetSquadPromptContent(ctx, db.GetSquadPromptContentParams{ID: binding.ScopeID, WorkspaceID: workspaceID})
		case promptVersionScopeAgent:
			current, err = qtx.GetAgentPromptContent(ctx, db.GetAgentPromptContentParams{ID: binding.ScopeID, WorkspaceID: workspaceID})
		}
		if err != nil {
			return directClauseRevisionPrepared{}, errors.New("carrier not found in this workspace")
		}
	}
	version, err := qtx.GetClauseVersion(ctx, db.GetClauseVersionParams{ClauseID: clause.ID, Version: clause.Version})
	if err != nil {
		return directClauseRevisionPrepared{}, fmt.Errorf("条款「%s」当前版本读取失败: %w", clause.Title, err)
	}
	if err := checkDirectRevisionExpectation(req, clause, version, current); err != nil {
		return directClauseRevisionPrepared{}, err
	}
	content := strings.TrimRight(req.Content, "\n")
	if err := legislation.ValidateRevisionText(clause.Title, content); err != nil {
		return directClauseRevisionPrepared{}, err
	}
	next, err := h.renderCarrierFromActiveClausesTx(ctx, qtx, workspaceID, carrierRef{scope: binding.Scope, id: binding.ScopeID}, current, &clauseRenderOverride{
		ClauseID: clause.ID,
		Version:  clause.Version + 1,
		Content:  content,
	})
	if err != nil {
		return directClauseRevisionPrepared{}, err
	}
	return directClauseRevisionPrepared{clause: clause, binding: binding, current: current, next: next}, nil
}

// PreviewDirectClauseRevision — POST /api/prompt-legislation/governance/clauses/{clauseId}/direct-revision-preview
// Builds the exact post-write carrier without mutating any Clause, binding, or
// carrier state. Only an Owner may call this route because its tokens authorize
// the matching direct write.
func (h *Handler) PreviewDirectClauseRevision(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeDirectClauseRevisionRequest(w, r)
	if !ok {
		return
	}
	if message := req.validate(false); message != "" {
		writeError(w, http.StatusBadRequest, message)
		return
	}
	clauseID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "clauseId"), "clause_id")
	if !ok {
		return
	}
	workspaceID := parseUUID(h.resolveWorkspaceID(r))
	prepared, err := h.prepareDirectClauseRevision(r.Context(), h.Queries, workspaceID, clauseID, req, false)
	if err != nil {
		writeError(w, http.StatusConflict, fmt.Sprintf("直接修订无法预览，已阻断：%v", err))
		return
	}
	writeJSON(w, http.StatusOK, DirectClauseRevisionPreviewResponse{
		ClauseID:                   uuidToString(prepared.clause.ID),
		Slug:                       prepared.clause.Slug,
		CarrierScope:               prepared.binding.Scope,
		CarrierScopeID:             uuidToString(prepared.binding.ScopeID),
		CurrentClauseVersion:       prepared.clause.Version,
		NextClauseVersion:          prepared.clause.Version + 1,
		CurrentClauseContentSHA256: req.ExpectedClauseContentSHA256,
		CurrentCarrierSHA256:       sha256Hex(prepared.current),
		Diff:                       legislation.DiffLines(prepared.current, prepared.next),
	})
}

// DirectReviseClause — POST /api/prompt-legislation/governance/clauses/{clauseId}/direct-revision
// Commits an Owner-confirmed direct Clause revision. It has a dedicated audit
// and never creates or advances prompt_proposal rows.
func (h *Handler) DirectReviseClause(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeDirectClauseRevisionRequest(w, r)
	if !ok {
		return
	}
	if message := req.validate(true); message != "" {
		writeError(w, http.StatusBadRequest, message)
		return
	}
	clauseID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "clauseId"), "clause_id")
	if !ok {
		return
	}
	actorID, actorType, ok := h.legislationActor(w, r)
	if !ok {
		return
	}
	workspaceID := parseUUID(h.resolveWorkspaceID(r))
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start direct clause revision")
		return
	}
	defer tx.Rollback(r.Context()) //nolint:errcheck
	qtx := h.Queries.WithTx(tx)
	if err := lockClauseReferenceWorkspaceTx(r.Context(), qtx, workspaceID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to lock clause references")
		return
	}
	prepared, err := h.prepareDirectClauseRevision(r.Context(), qtx, workspaceID, clauseID, req, true)
	if err != nil {
		writeError(w, http.StatusConflict, fmt.Sprintf("直接修订已过期或无效：%v", err))
		return
	}
	if scan, allowed, reason := promptGovernanceGate(prepared.next); !allowed {
		_ = scan
		writeError(w, http.StatusConflict, fmt.Sprintf("直接修订未通过内容门闸：%s", reason))
		return
	}
	contractProposal := db.PromptProposal{
		WorkspaceID:    workspaceID,
		CarrierScope:   prepared.binding.Scope,
		CarrierScopeID: prepared.binding.ScopeID,
		ChangeKind:     "revise_clause",
		ClauseName:     prepared.clause.Title,
		ClauseText:     strings.TrimRight(req.Content, "\n"),
		ClauseID:       prepared.clause.ID,
	}
	ref := carrierRef{scope: prepared.binding.Scope, id: prepared.binding.ScopeID}
	if findings := h.refContractGate(r.Context(), qtx, workspaceID, contractProposal,
		map[carrierRef]string{ref: prepared.current}, map[carrierRef]string{ref: prepared.next}); len(findings) > 0 {
		writeError(w, http.StatusConflict, fmt.Sprintf("条款引用契约检查失败，已阻断：%s", findings[0].Message))
		return
	}
	nextVersion := prepared.clause.Version + 1
	content := strings.TrimRight(req.Content, "\n")
	if _, err := qtx.CreateClauseVersion(r.Context(), db.CreateClauseVersionParams{
		ClauseID:      prepared.clause.ID,
		Version:       nextVersion,
		Content:       content,
		ContentSha256: sha256Hex(content),
		Source:        "owner_direct_revision",
		CarrierScope:  prepared.binding.Scope,
		CreatedByType: actorType,
		CreatedByID:   parseUUID(actorID),
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to write direct clause version")
		return
	}
	if _, err := qtx.BumpClauseBindingVersion(r.Context(), db.BumpClauseBindingVersionParams{
		ID:                 prepared.binding.ID,
		SinceClauseVersion: nextVersion,
		Conditions:         prepared.binding.Conditions,
		Audit: clauseAudit("owner_direct_revision", actorType, actorID, map[string]any{
			"from_version": prepared.clause.Version,
			"to_version":   nextVersion,
		}),
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update clause binding")
		return
	}
	if err := h.rerenderAffectedCarriersTx(r.Context(), qtx, workspaceID, []carrierRef{ref}, map[carrierRef]string{ref: prepared.current}, actorType, actorID); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to re-render clause carrier: %v", err))
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit direct clause revision")
		return
	}
	writeJSON(w, http.StatusOK, DirectClauseRevisionResponse{
		ClauseID:             uuidToString(prepared.clause.ID),
		Slug:                 prepared.clause.Slug,
		EnactedClauseVersion: nextVersion,
		ClauseContentSHA256:  sha256Hex(content),
		CarrierScope:         prepared.binding.Scope,
		CarrierScopeID:       uuidToString(prepared.binding.ScopeID),
		CarrierContentSHA256: sha256Hex(prepared.next),
	})
}
