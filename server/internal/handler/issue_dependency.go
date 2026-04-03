package handler

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// IssueDependencyResponse is the JSON response for an issue dependency.
type IssueDependencyResponse struct {
	ID               string `json:"id"`
	IssueID          string `json:"issue_id"`
	DependsOnIssueID string `json:"depends_on_issue_id"`
	Type             string `json:"type"`

	// Enriched fields for the related issue
	IssueIdentifier          string `json:"issue_identifier"`
	IssueTitle               string `json:"issue_title"`
	DependsOnIssueIdentifier string `json:"depends_on_issue_identifier"`
	DependsOnIssueTitle      string `json:"depends_on_issue_title"`
}

// ListIssueDependencies returns all dependencies for a given issue.
func (h *Handler) ListIssueDependencies(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	issue, ok := h.loadIssueForUser(w, r, id)
	if !ok {
		return
	}

	deps, err := h.Queries.ListIssueDependencies(r.Context(), issue.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list dependencies")
		return
	}

	prefix := h.getIssuePrefix(r.Context(), issue.WorkspaceID)

	result := make([]IssueDependencyResponse, 0, len(deps))
	for _, d := range deps {
		resp := IssueDependencyResponse{
			ID:               uuidToString(d.ID),
			IssueID:          uuidToString(d.IssueID),
			DependsOnIssueID: uuidToString(d.DependsOnIssueID),
			Type:             d.Type,
		}

		// Enrich with issue identifiers and titles
		if srcIssue, err := h.Queries.GetIssue(r.Context(), d.IssueID); err == nil {
			resp.IssueIdentifier = prefix + "-" + strconv.Itoa(int(srcIssue.Number))
			resp.IssueTitle = srcIssue.Title
		}
		if depIssue, err := h.Queries.GetIssue(r.Context(), d.DependsOnIssueID); err == nil {
			resp.DependsOnIssueIdentifier = prefix + "-" + strconv.Itoa(int(depIssue.Number))
			resp.DependsOnIssueTitle = depIssue.Title
		}

		result = append(result, resp)
	}

	writeJSON(w, http.StatusOK, result)
}

// CreateIssueDependency creates a new dependency between two issues.
func (h *Handler) CreateIssueDependency(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	issue, ok := h.loadIssueForUser(w, r, id)
	if !ok {
		return
	}

	var req struct {
		DependsOnIssueID string `json:"depends_on_issue_id"`
		Type             string `json:"type"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.DependsOnIssueID == "" || req.Type == "" {
		writeError(w, http.StatusBadRequest, "depends_on_issue_id and type are required")
		return
	}

	// Validate relation type
	switch req.Type {
	case "blocks", "blocked_by", "related":
	default:
		writeError(w, http.StatusBadRequest, "type must be one of: blocks, blocked_by, related")
		return
	}

	// Prevent self-reference
	if uuidToString(issue.ID) == req.DependsOnIssueID {
		writeError(w, http.StatusBadRequest, "cannot create a dependency to the same issue")
		return
	}

	// Verify the target issue exists and belongs to the same workspace
	targetIssue, err := h.Queries.GetIssueInWorkspace(r.Context(), db.GetIssueInWorkspaceParams{
		ID:          parseUUID(req.DependsOnIssueID),
		WorkspaceID: issue.WorkspaceID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "target issue not found")
		return
	}

	dep, err := h.Queries.CreateIssueDependency(r.Context(), db.CreateIssueDependencyParams{
		IssueID:          issue.ID,
		DependsOnIssueID: parseUUID(req.DependsOnIssueID),
		Type:             req.Type,
	})
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "dependency already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to create dependency")
		return
	}

	prefix := h.getIssuePrefix(r.Context(), issue.WorkspaceID)
	issueIdentifier := prefix + "-" + strconv.Itoa(int(issue.Number))
	targetIdentifier := prefix + "-" + strconv.Itoa(int(targetIssue.Number))

	userID := requestUserID(r)
	workspaceID := uuidToString(issue.WorkspaceID)
	actorType, actorID := h.resolveActor(r, userID, workspaceID)

	// Publish event for activity listener
	h.publish(protocol.EventIssueDependencyCreated, workspaceID, actorType, actorID, map[string]any{
		"dependency":                dep,
		"issue":                     issue,
		"target_issue":              targetIssue,
		"issue_identifier":          issueIdentifier,
		"target_issue_identifier":   targetIdentifier,
	})

	resp := IssueDependencyResponse{
		ID:                       uuidToString(dep.ID),
		IssueID:                  uuidToString(dep.IssueID),
		DependsOnIssueID:         uuidToString(dep.DependsOnIssueID),
		Type:                     dep.Type,
		IssueIdentifier:          issueIdentifier,
		IssueTitle:               issue.Title,
		DependsOnIssueIdentifier: targetIdentifier,
		DependsOnIssueTitle:      targetIssue.Title,
	}

	writeJSON(w, http.StatusCreated, resp)
}

// DeleteIssueDependency removes a dependency between two issues.
func (h *Handler) DeleteIssueDependency(w http.ResponseWriter, r *http.Request) {
	issueIDStr := chi.URLParam(r, "id")
	issue, ok := h.loadIssueForUser(w, r, issueIDStr)
	if !ok {
		return
	}

	depID := chi.URLParam(r, "depId")
	dep, err := h.Queries.GetIssueDependency(r.Context(), parseUUID(depID))
	if err != nil {
		writeError(w, http.StatusNotFound, "dependency not found")
		return
	}

	// Verify the dependency belongs to this issue
	depIssueID := uuidToString(dep.IssueID)
	depTargetID := uuidToString(dep.DependsOnIssueID)
	issueID := uuidToString(issue.ID)
	if depIssueID != issueID && depTargetID != issueID {
		writeError(w, http.StatusNotFound, "dependency not found")
		return
	}

	// Look up both issues for activity details
	srcIssue, _ := h.Queries.GetIssue(r.Context(), dep.IssueID)
	targetIssue, _ := h.Queries.GetIssue(r.Context(), dep.DependsOnIssueID)

	if err := h.Queries.DeleteIssueDependency(r.Context(), dep.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete dependency")
		return
	}

	prefix := h.getIssuePrefix(r.Context(), issue.WorkspaceID)
	srcIdentifier := prefix + "-" + strconv.Itoa(int(srcIssue.Number))
	targetIdentifier := prefix + "-" + strconv.Itoa(int(targetIssue.Number))

	userID := requestUserID(r)
	workspaceID := uuidToString(issue.WorkspaceID)
	actorType, actorID := h.resolveActor(r, userID, workspaceID)

	h.publish(protocol.EventIssueDependencyRemoved, workspaceID, actorType, actorID, map[string]any{
		"dependency":              dep,
		"issue":                   srcIssue,
		"target_issue":            targetIssue,
		"issue_identifier":        srcIdentifier,
		"target_issue_identifier": targetIdentifier,
	})

	w.WriteHeader(http.StatusNoContent)
}
