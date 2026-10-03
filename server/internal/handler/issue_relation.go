package handler

// RUYI-351 structured issue relations: blocks / blocked_by, relates_to,
// supersedes / superseded_by. Parent lives on the issue row itself and is
// managed through the existing UpdateIssue parent_issue_id path (set /
// clear / remount with cycle detection and expected_revision CAS).
//
// Storage rides the legacy issue_dependency edge table (001_init): issue_id
// is the edge source, depends_on_issue_id the edge target. Edges store one
// canonical row per relation — directional types keep the forward row only
// ('blocked_by' and 'superseded_by' are query-side inverses resolved in
// GetIssueRelations), and the symmetric 'relates_to' stores the
// lexicographically smaller UUID first so either caller direction lands on
// the same row. The table's 001-era FK cascade removes edges whenever either
// endpoint issue is deleted, so no delete path can leave a dangling relation
// behind.
//
// Side-effect contract: relation writes never start, wake, or queue an agent
// run — these handlers deliberately stay clear of WillEnqueueRun and
// dispatchIssueRun. That is the RUYI-351 hard clause the MCP tool schemas
// mirror in their descriptions.
//
// Concurrency: both endpoints' revisions advance on a committed change (a
// relation is part of each issue's observable state), alongside updated_at /
// last_activity_at. The optional expected_revision CAS on the anchor issue
// guards the whole transaction; a stale revision rolls the edge change back
// and answers 409 revision_conflict. Endpoint bumps run in canonical UUID
// order so concurrent relation writes acquire row locks in one global order.

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/logger"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Caller-facing relation types. The *_by forms name the relation from the
// other endpoint's perspective and store as their forward counterpart.
const (
	RelationBlocks       = "blocks"
	RelationBlockedBy    = "blocked_by"
	RelationRelatesTo    = "relates_to"
	RelationSupersedes   = "supersedes"
	RelationSupersededBy = "superseded_by"

	relationStorageBlocks     = "blocks"
	relationStorageRelatesTo  = "relates_to"
	relationStorageSupersedes = "supersedes"
)

func relationTypeAllowed(relationType string) bool {
	switch relationType {
	case RelationBlocks, RelationBlockedBy, RelationRelatesTo, RelationSupersedes, RelationSupersededBy:
		return true
	}
	return false
}

// canonicalRelationEdge folds the caller's relation frame onto the single
// canonical storage row.
func canonicalRelationEdge(source, target pgtype.UUID, relationType string) (pgtype.UUID, pgtype.UUID, string) {
	switch relationType {
	case RelationBlockedBy:
		// A is blocked by B ⇔ B blocks A.
		return target, source, relationStorageBlocks
	case RelationSupersededBy:
		// A is superseded by B ⇔ B supersedes A.
		return target, source, relationStorageSupersedes
	case RelationRelatesTo:
		if bytes.Compare(source.Bytes[:], target.Bytes[:]) > 0 {
			return target, source, relationStorageRelatesTo
		}
		return source, target, relationStorageRelatesTo
	default:
		return source, target, relationType
	}
}

type IssueRelationRef struct {
	ID         string `json:"id"`
	Identifier string `json:"identifier"`
	Title      string `json:"title"`
	Status     string `json:"status"`
}

type IssueRelationsResponse struct {
	IssueID      string             `json:"issue_id"`
	Identifier   string             `json:"identifier"`
	Revision     int64              `json:"revision"`
	Parent       *IssueRelationRef  `json:"parent"`
	Blocks       []IssueRelationRef `json:"blocks"`
	BlockedBy    []IssueRelationRef `json:"blocked_by"`
	RelatesTo    []IssueRelationRef `json:"relates_to"`
	Supersedes   []IssueRelationRef `json:"supersedes"`
	SupersededBy []IssueRelationRef `json:"superseded_by"`
}

func newIssueRelationRef(issue db.Issue, prefix string) IssueRelationRef {
	return IssueRelationRef{
		ID:         uuidToString(issue.ID),
		Identifier: prefix + "-" + strconv.Itoa(int(issue.Number)),
		Title:      issue.Title,
		Status:     issue.Status,
	}
}

// GetIssueRelations returns the issue's structured relations: parent plus
// the five edge views. Read-only.
func (h *Handler) GetIssueRelations(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}

	edges, err := h.Queries.ListIssueRelationsForIssue(r.Context(), issue.ID)
	if err != nil {
		slog.Error("list issue relations", append(logger.RequestAttrs(r), "issue_id", uuidToString(issue.ID), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to list issue relations")
		return
	}

	prefix := h.getIssuePrefix(r.Context(), issue.WorkspaceID)
	resp := IssueRelationsResponse{
		IssueID:      uuidToString(issue.ID),
		Identifier:   prefix + "-" + strconv.Itoa(int(issue.Number)),
		Revision:     issue.Revision,
		Blocks:       []IssueRelationRef{},
		BlockedBy:    []IssueRelationRef{},
		RelatesTo:    []IssueRelationRef{},
		Supersedes:   []IssueRelationRef{},
		SupersededBy: []IssueRelationRef{},
	}
	if issue.ParentIssueID.Valid {
		if parent, err := h.Queries.GetIssue(r.Context(), issue.ParentIssueID); err == nil {
			ref := newIssueRelationRef(parent, h.getIssuePrefix(r.Context(), parent.WorkspaceID))
			resp.Parent = &ref
		}
	}
	for _, edge := range edges {
		ref := IssueRelationRef{
			ID:         uuidToString(edge.OtherID),
			Identifier: prefix + "-" + strconv.Itoa(int(edge.OtherNumber)),
			Title:      edge.OtherTitle,
			Status:     edge.OtherStatus,
		}
		forward := edge.IssueID == issue.ID
		switch edge.Type {
		case relationStorageBlocks:
			if forward {
				resp.Blocks = append(resp.Blocks, ref)
			} else {
				resp.BlockedBy = append(resp.BlockedBy, ref)
			}
		case relationStorageSupersedes:
			if forward {
				resp.Supersedes = append(resp.Supersedes, ref)
			} else {
				resp.SupersededBy = append(resp.SupersededBy, ref)
			}
		case RelationBlockedBy:
			// Legacy 001-vocabulary row (A stored as blocked-by B): no code
			// path writes it, but it reads as the inverse of a forward row.
			if forward {
				resp.BlockedBy = append(resp.BlockedBy, ref)
			} else {
				resp.Blocks = append(resp.Blocks, ref)
			}
		default:
			// relates_to is symmetric; the legacy 'related' value folds in here.
			resp.RelatesTo = append(resp.RelatesTo, ref)
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

type AddIssueRelationRequest struct {
	Type             string `json:"type"`
	TargetIssueID    string `json:"target_issue_id"`
	ExpectedRevision *int64 `json:"expected_revision,omitempty"`
}

// AddIssueRelation creates one relation edge. Duplicate edges (including a
// relates_to named from the other side) answer 409 relation_exists.
func (h *Handler) AddIssueRelation(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}

	var req AddIssueRelationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if !relationTypeAllowed(req.Type) {
		writeError(w, http.StatusBadRequest,
			"type must be one of: blocks, blocked_by, relates_to, supersedes, superseded_by")
		return
	}
	if req.ExpectedRevision != nil && *req.ExpectedRevision < 1 {
		writeError(w, http.StatusBadRequest, "expected_revision must be a positive integer")
		return
	}
	targetID, ok := parseUUIDOrBadRequest(w, req.TargetIssueID, "target_issue_id")
	if !ok {
		return
	}
	if targetID == issue.ID {
		writeError(w, http.StatusBadRequest, "an issue cannot relate to itself")
		return
	}
	if _, err := h.Queries.GetIssueInWorkspace(r.Context(), db.GetIssueInWorkspaceParams{
		ID:          targetID,
		WorkspaceID: issue.WorkspaceID,
	}); err != nil {
		writeError(w, http.StatusBadRequest, "target issue not found in this workspace")
		return
	}

	edgeSource, edgeTarget, storageType := canonicalRelationEdge(issue.ID, targetID, req.Type)

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		slog.Error("begin relation tx", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to add relation")
		return
	}
	defer rollbackRelationTx(r, tx)
	qtx := h.Queries.WithTx(tx)

	edge, err := qtx.InsertIssueRelation(r.Context(), db.InsertIssueRelationParams{
		IssueID:          edgeSource,
		DependsOnIssueID: edgeTarget,
		Type:             storageType,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		writeRelationExists(w, issue.ID, req.Type, targetID)
		return
	}
	if err != nil {
		slog.Error("insert issue relation", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to add relation")
		return
	}

	newRevision, responded, err := bumpRelationEndpoints(w, r, qtx, issue, targetID, req.ExpectedRevision)
	if responded || err != nil {
		if err != nil {
			slog.Error("bump relation revisions", append(logger.RequestAttrs(r), "error", err)...)
			writeError(w, http.StatusInternalServerError, "failed to add relation")
		}
		return // 409 already written, or internal failure; defer rolls the edge back
	}

	if err := tx.Commit(r.Context()); err != nil {
		slog.Error("commit relation tx", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to add relation")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"added": true,
		"relation": map[string]any{
			"id":              uuidToString(edge.ID),
			"type":            req.Type,
			"source_issue_id": uuidToString(issue.ID),
			"target_issue_id": uuidToString(targetID),
		},
		"issue": map[string]any{
			"id":       uuidToString(issue.ID),
			"revision": newRevision,
		},
	})
}

// RemoveIssueRelation deletes one relation edge by semantic type and target.
// Unknown edges answer 404 relation_not_found. expected_revision rides as an
// optional query parameter (DELETE bodies are awkward for clients).
func (h *Handler) RemoveIssueRelation(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}

	relationType := chi.URLParam(r, "relType")
	if !relationTypeAllowed(relationType) {
		writeError(w, http.StatusBadRequest,
			"relation type must be one of: blocks, blocked_by, relates_to, supersedes, superseded_by")
		return
	}
	targetID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "targetId"), "target issue id")
	if !ok {
		return
	}
	var expectedRevision *int64
	if raw := r.URL.Query().Get("expected_revision"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < 1 {
			writeError(w, http.StatusBadRequest, "expected_revision must be a positive integer")
			return
		}
		expectedRevision = &parsed
	}

	edgeSource, edgeTarget, storageType := canonicalRelationEdge(issue.ID, targetID, relationType)

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		slog.Error("begin relation tx", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to remove relation")
		return
	}
	defer rollbackRelationTx(r, tx)
	qtx := h.Queries.WithTx(tx)

	if _, err := qtx.DeleteIssueRelation(r.Context(), db.DeleteIssueRelationParams{
		IssueID:          edgeSource,
		DependsOnIssueID: edgeTarget,
		Type:             storageType,
	}); errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, map[string]any{
			"error":    "relation not found",
			"code":     "relation_not_found",
			"issue_id": uuidToString(issue.ID),
			"relation": map[string]string{
				"type":            relationType,
				"source_issue_id": uuidToString(issue.ID),
				"target_issue_id": uuidToString(targetID),
			},
		})
		return
	} else if err != nil {
		slog.Error("delete issue relation", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to remove relation")
		return
	}

	newRevision, responded, err := bumpRelationEndpoints(w, r, qtx, issue, targetID, expectedRevision)
	if responded || err != nil {
		if err != nil {
			slog.Error("bump relation revisions", append(logger.RequestAttrs(r), "error", err)...)
			writeError(w, http.StatusInternalServerError, "failed to remove relation")
		}
		return
	}

	if err := tx.Commit(r.Context()); err != nil {
		slog.Error("commit relation tx", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to remove relation")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"removed": true,
		"relation": map[string]string{
			"type":            relationType,
			"source_issue_id": uuidToString(issue.ID),
			"target_issue_id": uuidToString(targetID),
		},
		"issue": map[string]any{
			"id":       uuidToString(issue.ID),
			"revision": newRevision,
		},
	})
}

func rollbackRelationTx(r *http.Request, tx pgx.Tx) {
	if err := tx.Rollback(r.Context()); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		slog.Warn("relation tx rollback", append(logger.RequestAttrs(r), "error", err)...)
	}
}

func writeRelationExists(w http.ResponseWriter, issueID pgtype.UUID, relationType string, targetID pgtype.UUID) {
	writeJSON(w, http.StatusConflict, map[string]any{
		"error":    "relation already exists",
		"code":     "relation_exists",
		"issue_id": uuidToString(issueID),
		"relation": map[string]string{
			"type":            relationType,
			"source_issue_id": uuidToString(issueID),
			"target_issue_id": uuidToString(targetID),
		},
	})
}

// bumpRelationEndpoints advances both endpoints' revisions in canonical UUID
// order, applying the optimistic-lock CAS to the anchor. responded=true means
// a stale expected_revision: the structured 409 is already written and the
// caller must let the transaction roll back.
func bumpRelationEndpoints(w http.ResponseWriter, r *http.Request, qtx *db.Queries, anchor db.Issue, targetID pgtype.UUID, expectedRevision *int64) (newRevision int64, responded bool, err error) {
	first, second := anchor.ID, targetID
	if bytes.Compare(first.Bytes[:], second.Bytes[:]) > 0 {
		first, second = second, first
	}
	for _, id := range []pgtype.UUID{first, second} {
		if id != anchor.ID {
			if err := qtx.BumpIssueRevision(r.Context(), id); err != nil {
				return 0, false, err
			}
			continue
		}
		params := db.BumpIssueRevisionGuardedParams{ID: id}
		if expectedRevision != nil {
			params.ExpectedRevision = pgtype.Int8{Int64: *expectedRevision, Valid: true}
		}
		row, err := qtx.BumpIssueRevisionGuarded(r.Context(), params)
		if errors.Is(err, pgx.ErrNoRows) {
			current, reloadErr := qtx.GetIssueInWorkspace(r.Context(), db.GetIssueInWorkspaceParams{
				ID:          anchor.ID,
				WorkspaceID: anchor.WorkspaceID,
			})
			if reloadErr != nil {
				return 0, false, reloadErr
			}
			writeRevisionConflict(w, "issue", anchor.ID, *expectedRevision, current.Revision)
			return 0, true, nil
		}
		if err != nil {
			return 0, false, err
		}
		newRevision = row.Revision
	}
	return newRevision, false, nil
}
