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

	"github.com/multica-ai/multica/server/internal/legislation"
	"github.com/multica-ai/multica/server/pkg/clausemark"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type updateClauseContentRequest struct {
	Content         string `json:"content"`
	ExpectedVersion int32  `json:"expected_version"`
}

func validateClauseManagementRefsTx(ctx context.Context, qtx *db.Queries, workspaceID pgtype.UUID) error {
	clauses, err := qtx.ListActiveWorkspaceClauses(ctx, workspaceID)
	if err != nil {
		return err
	}
	refs := &legislation.RefUniverse{
		Bodies:   make(map[string]string, len(clauses)),
		Binding:  make(map[string]string, len(clauses)),
		Headings: make(map[string][]string),
	}
	for _, clause := range clauses {
		refs.Bodies[clause.Slug] = clause.Content
		refs.Binding[clause.Slug] = carrierKey(clause.Scope, clause.ScopeID)
	}
	active := make(map[string]bool, len(clauses))
	for _, clause := range clauses {
		active[clause.Slug] = true
	}
	return validateClauseManagementCarrierBatches(ctx, workspaceID, refs, active, qtx.ListWorkspaceCarrierTextsBatch)
}

const clauseManagementCarrierBatchSize = 500

func validateClauseManagementCarrierBatches(
	ctx context.Context,
	workspaceID pgtype.UUID,
	refs *legislation.RefUniverse,
	active map[string]bool,
	fetch func(context.Context, db.ListWorkspaceCarrierTextsBatchParams) ([]db.ListWorkspaceCarrierTextsBatchRow, error),
) error {
	var afterScope pgtype.Text
	var afterScopeID pgtype.UUID
	for {
		carriers, err := fetch(ctx, db.ListWorkspaceCarrierTextsBatchParams{
			WorkspaceID:  workspaceID,
			AfterScope:   afterScope,
			AfterScopeID: afterScopeID,
			RowLimit:     clauseManagementCarrierBatchSize,
		})
		if err != nil {
			return err
		}
		if len(carriers) == 0 {
			break
		}
		texts := make(map[string]string, len(carriers))
		for _, carrier := range carriers {
			key := carrierKey(carrier.Scope, carrier.ScopeID)
			text := clausemark.StripMarkers(carrier.EffectiveContent)
			refs.Headings[key] = legislation.Sections(text)
			texts[key] = text
		}
		for _, finding := range legislation.CheckStaticClauseRefs(texts, active) {
			if finding.Level == "error" {
				return &staticClauseReferenceError{message: finding.Message}
			}
		}
		last := carriers[len(carriers)-1]
		afterScope = pgtype.Text{String: last.Scope, Valid: true}
		afterScopeID = last.ScopeID
	}
	for _, finding := range legislation.CheckClauseRefs(refs) {
		if finding.Level == "error" {
			return &staticClauseReferenceError{message: finding.Message}
		}
	}
	return nil
}

func clauseRerenderFailure(w http.ResponseWriter, err error, conflictMessage, failureMessage string) {
	var validationErr *carrierRenderValidationError
	if errors.As(err, &validationErr) {
		writeError(w, http.StatusConflict, conflictMessage)
		return
	}
	writeError(w, http.StatusInternalServerError, failureMessage)
}

func (h *Handler) clauseManagementContext(w http.ResponseWriter, r *http.Request) (string, pgtype.UUID, pgtype.UUID, bool) {
	userID, _, ok := h.legislationActor(w, r)
	if !ok {
		return "", pgtype.UUID{}, pgtype.UUID{}, false
	}
	workspaceIDRaw := h.resolveWorkspaceID(r)
	role, ok := h.workspaceRole(r, userID, workspaceIDRaw)
	if !ok {
		writeError(w, http.StatusNotFound, "workspace not found")
		return "", pgtype.UUID{}, pgtype.UUID{}, false
	}
	if role != "owner" && role != "admin" {
		writeError(w, http.StatusForbidden, "only workspace owners and admins can manage clauses")
		return "", pgtype.UUID{}, pgtype.UUID{}, false
	}
	workspaceID := parseUUID(workspaceIDRaw)
	clauseID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "clauseId"), "clause id")
	return userID, workspaceID, clauseID, ok
}

func lockClauseCarrierTextTx(ctx context.Context, qtx *db.Queries, workspaceID, scopeID pgtype.UUID, scope string) (string, error) {
	switch scope {
	case "workspace":
		if scopeID != workspaceID {
			return "", fmt.Errorf("workspace carrier does not match the current workspace")
		}
		row, err := qtx.LockWorkspaceForPromptVersion(ctx, scopeID)
		return row.EffectiveContent, err
	case "project":
		row, err := qtx.LockProjectForPromptVersion(ctx, db.LockProjectForPromptVersionParams{ID: scopeID, WorkspaceID: workspaceID})
		return row.EffectiveContent, err
	case "squad":
		row, err := qtx.LockSquadForPromptVersion(ctx, db.LockSquadForPromptVersionParams{ID: scopeID, WorkspaceID: workspaceID})
		return row.EffectiveContent, err
	case "agent":
		row, err := qtx.LockAgentForPromptVersion(ctx, db.LockAgentForPromptVersionParams{ID: scopeID, WorkspaceID: workspaceID})
		return row.EffectiveContent, err
	default:
		return "", fmt.Errorf("unsupported clause carrier scope %q", scope)
	}
}

func carrierHasClauseMarker(text, slug string) bool {
	for _, marker := range clausemark.ScanMarkers(text) {
		if marker.Slug == slug {
			return true
		}
	}
	return false
}

func (h *Handler) UpdateClauseContent(w http.ResponseWriter, r *http.Request) {
	userID, workspaceID, clauseID, ok := h.clauseManagementContext(w, r)
	if !ok {
		return
	}
	var req updateClauseContentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Content) == "" || req.ExpectedVersion < 1 {
		writeError(w, http.StatusBadRequest, "content and expected_version are required")
		return
	}
	content := strings.TrimRight(req.Content, "\n")

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update clause")
		return
	}
	defer tx.Rollback(r.Context()) //nolint:errcheck
	qtx := h.Queries.WithTx(tx)
	if err := lockClauseReferenceWorkspaceTx(r.Context(), qtx, workspaceID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update clause")
		return
	}
	clause, err := qtx.LockClauseForDirectManagement(r.Context(), db.LockClauseForDirectManagementParams{ID: clauseID, WorkspaceID: workspaceID})
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "clause not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update clause")
		return
	}
	if clause.Status == "revoked" {
		writeError(w, http.StatusConflict, "revoked clauses cannot be edited")
		return
	}
	if err := legislation.ValidateRevisionText(clause.Title, req.Content); err != nil {
		writeError(w, http.StatusBadRequest, "invalid clause content")
		return
	}

	latestVersion, err := qtx.LatestClauseVersion(r.Context(), clauseID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusConflict, "clause has no editable version")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load clause version")
		return
	}
	if latestVersion != req.ExpectedVersion {
		writeError(w, http.StatusConflict, "clause was updated; reload before editing")
		return
	}
	version, err := qtx.GetClauseVersion(r.Context(), db.GetClauseVersionParams{ClauseID: clauseID, Version: latestVersion})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load clause version")
		return
	}
	binding, bindingErr := qtx.GetActiveClauseBinding(r.Context(), clauseID)
	var carrier carrierRef
	var carrierText string
	if clause.Status == "active" {
		if errors.Is(bindingErr, pgx.ErrNoRows) {
			writeError(w, http.StatusConflict, "active clause has no active binding")
			return
		}
		if bindingErr != nil {
			writeError(w, http.StatusInternalServerError, "failed to load clause binding")
			return
		}
		carrier = carrierRef{scope: binding.Scope, id: binding.ScopeID}
		carrierText, err = lockClauseCarrierTextTx(r.Context(), qtx, workspaceID, carrier.id, carrier.scope)
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusConflict, "clause carrier is no longer available")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load clause carrier")
			return
		}
	} else if bindingErr == nil {
		writeError(w, http.StatusConflict, "inactive clause unexpectedly has an active binding")
		return
	} else if !errors.Is(bindingErr, pgx.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, "failed to load clause binding")
		return
	}

	nextVersion := latestVersion + 1
	carrierScope := version.CarrierScope
	if clause.Status == "active" {
		carrierScope = carrier.scope
	}
	if _, err := qtx.CreateClauseVersion(r.Context(), db.CreateClauseVersionParams{
		ClauseID: clauseID, Version: nextVersion, Content: content, ContentSha256: sha256Hex(content),
		Source: "manual", CarrierScope: carrierScope, CreatedByType: "member", CreatedByID: parseUUID(userID),
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save clause version")
		return
	}
	texts := map[carrierRef]string{}
	if clause.Status == "active" {
		if _, err := qtx.BumpClauseBindingVersion(r.Context(), db.BumpClauseBindingVersionParams{
			ID: binding.ID, SinceClauseVersion: nextVersion,
			Conditions: binding.Conditions,
			Audit:      clauseAudit("manual_version_bumped", "member", userID, map[string]any{"to_version": nextVersion}),
		}); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to update clause binding")
			return
		}
		if err := validateClauseManagementRefsTx(r.Context(), qtx, workspaceID); err != nil {
			var refErr *staticClauseReferenceError
			if errors.As(err, &refErr) {
				writeError(w, http.StatusConflict, refErr.Error())
			} else {
				writeError(w, http.StatusInternalServerError, "failed to validate clause references")
			}
			return
		}
		texts[carrier] = carrierText
		if err := h.rerenderAffectedCarriersTx(r.Context(), qtx, workspaceID, []carrierRef{carrier}, texts, nil, "member", userID); err != nil {
			clauseRerenderFailure(w, err, "updated clause did not pass carrier validation", "failed to update clause carrier")
			return
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit clause update")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) DeactivateClause(w http.ResponseWriter, r *http.Request) {
	userID, workspaceID, clauseID, ok := h.clauseManagementContext(w, r)
	if !ok {
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to deactivate clause")
		return
	}
	defer tx.Rollback(r.Context()) //nolint:errcheck
	qtx := h.Queries.WithTx(tx)
	if err := lockClauseReferenceWorkspaceTx(r.Context(), qtx, workspaceID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to deactivate clause")
		return
	}
	clause, err := qtx.LockClauseForDirectManagement(r.Context(), db.LockClauseForDirectManagementParams{ID: clauseID, WorkspaceID: workspaceID})
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "clause not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to deactivate clause")
		return
	}
	if clause.Status == "revoked" {
		writeError(w, http.StatusConflict, "revoked clauses cannot be deactivated")
		return
	}
	if clause.Status == "active" {
		binding, err := qtx.GetActiveClauseBinding(r.Context(), clauseID)
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusConflict, "active clause has no active binding")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load clause binding")
			return
		}
		carrier := carrierRef{scope: binding.Scope, id: binding.ScopeID}
		carrierText, err := lockClauseCarrierTextTx(r.Context(), qtx, workspaceID, carrier.id, carrier.scope)
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusConflict, "clause carrier is no longer available")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load clause carrier")
			return
		}
		if _, err := qtx.WithdrawClauseBinding(r.Context(), db.WithdrawClauseBindingParams{
			ClauseID: clauseID,
			Audit:    clauseAudit("manual_deactivated", "member", userID, nil),
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeError(w, http.StatusConflict, "active clause binding could not be withdrawn")
			} else {
				writeError(w, http.StatusInternalServerError, "failed to withdraw clause binding")
			}
			return
		}
		if _, err := qtx.UpdateClauseStatus(r.Context(), db.UpdateClauseStatusParams{ID: clauseID, WorkspaceID: workspaceID, Status: "deprecated"}); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to deactivate clause")
			return
		}
		if err := validateClauseManagementRefsTx(r.Context(), qtx, workspaceID); err != nil {
			var refErr *staticClauseReferenceError
			if errors.As(err, &refErr) {
				writeError(w, http.StatusConflict, refErr.Error())
			} else {
				writeError(w, http.StatusInternalServerError, "failed to validate clause references")
			}
			return
		}
		if err := h.rerenderAffectedCarriersTx(r.Context(), qtx, workspaceID, []carrierRef{carrier}, map[carrierRef]string{carrier: carrierText}, nil, "member", userID); err != nil {
			clauseRerenderFailure(w, err, "deactivated clause could not be removed from its carrier", "failed to update clause carrier")
			return
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit clause deactivation")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) DeleteClauseAsset(w http.ResponseWriter, r *http.Request) {
	userID, workspaceID, clauseID, ok := h.clauseManagementContext(w, r)
	if !ok {
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete clause")
		return
	}
	defer tx.Rollback(r.Context()) //nolint:errcheck
	qtx := h.Queries.WithTx(tx)
	if err := lockClauseReferenceWorkspaceTx(r.Context(), qtx, workspaceID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete clause")
		return
	}
	clause, err := qtx.LockClauseForDirectManagement(r.Context(), db.LockClauseForDirectManagementParams{ID: clauseID, WorkspaceID: workspaceID})
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "clause not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete clause")
		return
	}
	var refs []carrierRef
	texts := map[carrierRef]string{}
	bindings, err := qtx.ListClauseBindings(r.Context(), clauseID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load clause bindings")
		return
	}
	var activeBinding *db.ClauseBinding
	for i := range bindings {
		if bindings[i].Status != "active" {
			continue
		}
		if clause.Status != "active" || activeBinding != nil {
			writeError(w, http.StatusConflict, "clause binding status is inconsistent")
			return
		}
		activeBinding = &bindings[i]
	}
	if clause.Status == "active" && activeBinding == nil {
		writeError(w, http.StatusConflict, "active clause has no active binding")
		return
	}
	activeCarrier := carrierRef{}
	if activeBinding != nil {
		activeCarrier = carrierRef{scope: activeBinding.Scope, id: activeBinding.ScopeID}
	}
	seen := map[carrierRef]bool{}
	for _, binding := range bindings {
		carrier := carrierRef{scope: binding.Scope, id: binding.ScopeID}
		if seen[carrier] {
			continue
		}
		seen[carrier] = true
		carrierText, err := lockClauseCarrierTextTx(r.Context(), qtx, workspaceID, carrier.id, carrier.scope)
		if errors.Is(err, pgx.ErrNoRows) {
			if carrier == activeCarrier {
				writeError(w, http.StatusConflict, "clause carrier is no longer available")
				return
			}
			continue
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load clause carrier")
			return
		}
		if carrier == activeCarrier || carrierHasClauseMarker(carrierText, clause.Slug) {
			refs = append(refs, carrier)
			texts[carrier] = carrierText
		}
	}
	if activeBinding != nil {
		if _, err := qtx.WithdrawClauseBinding(r.Context(), db.WithdrawClauseBindingParams{
			ClauseID: clauseID,
			Audit:    clauseAudit("manual_deleted", "member", userID, nil),
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeError(w, http.StatusConflict, "active clause binding could not be withdrawn")
			} else {
				writeError(w, http.StatusInternalServerError, "failed to withdraw clause binding")
			}
			return
		}
	}
	for _, remove := range []func(context.Context, pgtype.UUID) error{
		qtx.DeleteClauseBindings, qtx.DeleteClauseVersions, qtx.DeleteClauseQuality, qtx.DeleteClauseInjections,
	} {
		if err := remove(r.Context(), clauseID); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to delete clause assets")
			return
		}
	}
	deleted, err := qtx.DeleteClause(r.Context(), db.DeleteClauseParams{ID: clauseID, WorkspaceID: workspaceID})
	if err != nil || deleted != 1 {
		writeError(w, http.StatusInternalServerError, "failed to delete clause")
		return
	}
	if err := validateClauseManagementRefsTx(r.Context(), qtx, workspaceID); err != nil {
		var refErr *staticClauseReferenceError
		if errors.As(err, &refErr) {
			writeError(w, http.StatusConflict, refErr.Error())
		} else {
			writeError(w, http.StatusInternalServerError, "failed to validate clause references")
		}
		return
	}
	if err := h.rerenderAffectedCarriersTx(r.Context(), qtx, workspaceID, refs, texts, nil, "member", userID); err != nil {
		clauseRerenderFailure(w, err, "deleted clause could not be removed from its carrier", "failed to update clause carrier")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit clause deletion")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
