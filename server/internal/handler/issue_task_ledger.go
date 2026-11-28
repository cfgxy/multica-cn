package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Allowed AC and TODO statuses
const (
	ACStatusUncompleted         = "uncompleted"
	ACStatusPendingVerification = "pending_verification"
	ACStatusVerified            = "verified"
	ACStatusRejected            = "rejected"

	TodoStatusPending             = "pending"
	TodoStatusInProgress          = "in_progress"
	TodoStatusPendingVerification = "pending_verification"
	TodoStatusCompleted           = "completed"
	TodoStatusBlocked             = "blocked"
)

func validACStatus(s string) bool {
	switch s {
	case ACStatusUncompleted, ACStatusPendingVerification, ACStatusVerified, ACStatusRejected:
		return true
	}
	return false
}

func validTodoStatus(s string) bool {
	switch s {
	case TodoStatusPending, TodoStatusInProgress, TodoStatusPendingVerification, TodoStatusCompleted, TodoStatusBlocked:
		return true
	}
	return false
}

func dedupeStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	var out []string
	for _, s := range in {
		if !seen[s] && s != "" {
			seen[s] = true
			out = append(out, s)
		}
	}
	if out == nil {
		return []string{}
	}
	return out
}

// IssueACEvidence represents an evidence link or text reference.
type IssueACEvidence struct {
	Type      string `json:"type"` // "run", "commit", "pr", "test", "screenshot", "text", "link"
	URL       string `json:"url,omitempty"`
	Note      string `json:"note,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
	CreatedBy string `json:"created_by,omitempty"`
}

// IssueACResponse is the API presentation of an acceptance criterion.
type IssueACResponse struct {
	ID              string            `json:"id"`
	WorkspaceID     string            `json:"workspace_id"`
	IssueID         string            `json:"issue_id"`
	Key             string            `json:"key"`
	Title           string            `json:"title"`
	Status          string            `json:"status"`
	Evidence        []IssueACEvidence `json:"evidence"`
	RejectionReason string            `json:"rejection_reason"`
	VerifiedAt      *string           `json:"verified_at,omitempty"`
	VerifiedByType  *string           `json:"verified_by_type,omitempty"`
	VerifiedByID    *string           `json:"verified_by_id,omitempty"`
	Position        int32             `json:"position"`
	CreatedAt       string            `json:"created_at"`
	UpdatedAt       string            `json:"updated_at"`
	LinkedTodoIDs   []string          `json:"linked_todo_ids,omitempty"`
}

// IssueTodoResponse is the API presentation of a dynamic TODO item.
type IssueTodoResponse struct {
	ID            string            `json:"id"`
	WorkspaceID   string            `json:"workspace_id"`
	IssueID       string            `json:"issue_id"`
	Title         string            `json:"title"`
	Status        string            `json:"status"`
	NextStep      string            `json:"next_step"`
	BlockedReason string            `json:"blocked_reason"`
	AssigneeType  *string           `json:"assignee_type,omitempty"`
	AssigneeID    *string           `json:"assignee_id,omitempty"`
	ACIDs         []string          `json:"ac_ids"`
	Evidence      []IssueACEvidence `json:"evidence"`
	Position      int32             `json:"position"`
	CreatedAt     string            `json:"created_at"`
	UpdatedAt     string            `json:"updated_at"`
}

// TaskStateAuditResponse is an audit entry for status transitions and evidence.
type TaskStateAuditResponse struct {
	ID          string            `json:"id"`
	WorkspaceID string            `json:"workspace_id"`
	IssueID     string            `json:"issue_id"`
	TargetType  string            `json:"target_type"` // "ac", "todo"
	TargetID    string            `json:"target_id"`
	Action      string            `json:"action"` // "created", "updated", "status_changed", "verified", "rejected", "reopened"
	FromStatus  string            `json:"from_status"`
	ToStatus    string            `json:"to_status"`
	ActorType   string            `json:"actor_type"`
	ActorID     string            `json:"actor_id"`
	Evidence    []IssueACEvidence `json:"evidence"`
	Note        string            `json:"note"`
	CreatedAt   string            `json:"created_at"`
}

// IssueLedgerSummary summarizes AC and TODO counts for fast agent/user orientation.
type IssueLedgerSummary struct {
	ACTotal          int      `json:"ac_total"`
	ACVerified       int      `json:"ac_verified"`
	ACPending        int      `json:"ac_pending"`
	ACRejected       int      `json:"ac_rejected"`
	TodoTotal        int      `json:"todo_total"`
	TodoCompleted    int      `json:"todo_completed"`
	TodoInProgress   int      `json:"todo_in_progress"`
	TodoPending      int      `json:"todo_pending"`
	TodoBlocked      int      `json:"todo_blocked"`
	CurrentNextSteps []string `json:"current_next_steps"`
}

// IssueLedgerResponse is the composite response for an issue's task state ledger.
type IssueLedgerResponse struct {
	ACs     []IssueACResponse        `json:"acs"`
	Todos   []IssueTodoResponse      `json:"todos"`
	Audit   []TaskStateAuditResponse `json:"audit"`
	Summary IssueLedgerSummary       `json:"summary"`
}

// Helper: parse evidence slice from JSONB bytes
func parseEvidenceSlice(raw []byte) []IssueACEvidence {
	if len(raw) == 0 {
		return []IssueACEvidence{}
	}
	var out []IssueACEvidence
	if err := json.Unmarshal(raw, &out); err != nil {
		return []IssueACEvidence{}
	}
	return out
}

// Helper: parse string slice from JSONB bytes
func parseStringSlice(raw []byte) []string {
	if len(raw) == 0 {
		return []string{}
	}
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil {
		return []string{}
	}
	return out
}

func issueACToResponse(ac db.IssueAcceptanceCriterium) IssueACResponse {
	resp := IssueACResponse{
		ID:              uuidToString(ac.ID),
		WorkspaceID:     uuidToString(ac.WorkspaceID),
		IssueID:         uuidToString(ac.IssueID),
		Key:             ac.Key,
		Title:           ac.Title,
		Status:          ac.Status,
		Evidence:        parseEvidenceSlice(ac.Evidence),
		RejectionReason: ac.RejectionReason,
		Position:        ac.Position,
		CreatedAt:       ac.CreatedAt.Time.Format(time.RFC3339Nano),
		UpdatedAt:       ac.UpdatedAt.Time.Format(time.RFC3339Nano),
		LinkedTodoIDs:   []string{},
	}
	if ac.VerifiedAt.Valid {
		s := ac.VerifiedAt.Time.Format(time.RFC3339Nano)
		resp.VerifiedAt = &s
	}
	if ac.VerifiedByType.Valid {
		resp.VerifiedByType = &ac.VerifiedByType.String
	}
	if ac.VerifiedByID.Valid {
		s := uuidToString(ac.VerifiedByID)
		resp.VerifiedByID = &s
	}
	return resp
}

func issueTodoToResponse(todo db.IssueTodo) IssueTodoResponse {
	resp := IssueTodoResponse{
		ID:            uuidToString(todo.ID),
		WorkspaceID:   uuidToString(todo.WorkspaceID),
		IssueID:       uuidToString(todo.IssueID),
		Title:         todo.Title,
		Status:        todo.Status,
		NextStep:      todo.NextStep,
		BlockedReason: todo.BlockedReason,
		ACIDs:         parseStringSlice(todo.AcIds),
		Evidence:      parseEvidenceSlice(todo.Evidence),
		Position:      todo.Position,
		CreatedAt:     todo.CreatedAt.Time.Format(time.RFC3339Nano),
		UpdatedAt:     todo.UpdatedAt.Time.Format(time.RFC3339Nano),
	}
	if todo.AssigneeType.Valid {
		resp.AssigneeType = &todo.AssigneeType.String
	}
	if todo.AssigneeID.Valid {
		s := uuidToString(todo.AssigneeID)
		resp.AssigneeID = &s
	}
	return resp
}

func taskStateAuditToResponse(audit db.IssueTaskStateAudit) TaskStateAuditResponse {
	return TaskStateAuditResponse{
		ID:          uuidToString(audit.ID),
		WorkspaceID: uuidToString(audit.WorkspaceID),
		IssueID:     uuidToString(audit.IssueID),
		TargetType:  audit.TargetType,
		TargetID:    uuidToString(audit.TargetID),
		Action:      audit.Action,
		FromStatus:  audit.FromStatus,
		ToStatus:    audit.ToStatus,
		ActorType:   audit.ActorType,
		ActorID:     uuidToString(audit.ActorID),
		Evidence:    parseEvidenceSlice(audit.Evidence),
		Note:        audit.Note,
		CreatedAt:   audit.CreatedAt.Time.Format(time.RFC3339Nano),
	}
}

// ParsedACItem represents an AC item extracted from markdown.
type ParsedACItem struct {
	Key      string
	Title    string
	Verified bool
}

var (
	acHeaderRE  = regexp.MustCompile(`(?im)^#{1,4}\s*(?:验收标准.*|Acceptance\s*Criteria.*|AC\b.*)$`)
	anyHeaderRE = regexp.MustCompile(`(?m)^#{1,3}\s+`)
	acKeyRE     = regexp.MustCompile(`(?i)^(?:[-*]\s+)?(?:\*\*)?(AC[-_]?\d+)(?:\*\*)?[:：\s]*(.+)$`)
	acCheckRE   = regexp.MustCompile(`(?i)^[-*]\s*\[([ xX])\]\s*(?:(?:\*\*)?(AC[-_]?\d+)(?:\*\*)?[:：\s]*)?(.+)$`)
	acNumRE     = regexp.MustCompile(`(?i)^(?:[-*]|\d+[\.\)])\s+(?:(?:\*\*)?(AC[-_]?\d+)(?:\*\*)?[:：\s]*)?(.+)$`)
)

// ExtractACFromMarkdown extracts structured AC items from issue description markdown.
func ExtractACFromMarkdown(desc string) []ParsedACItem {
	if strings.TrimSpace(desc) == "" {
		return nil
	}

	headerLoc := acHeaderRE.FindStringIndex(desc)
	targetText := desc
	if headerLoc != nil {
		afterHeader := desc[headerLoc[1]:]
		nextHeaderLoc := anyHeaderRE.FindStringIndex(afterHeader)
		if nextHeaderLoc != nil {
			targetText = afterHeader[:nextHeaderLoc[0]]
		} else {
			targetText = afterHeader
		}
	}

	lines := strings.Split(targetText, "\n")
	var items []ParsedACItem
	seenKeys := make(map[string]bool)
	idx := 1

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		// Try check item first: - [x] AC1: ...
		if m := acCheckRE.FindStringSubmatch(trimmed); len(m) >= 4 {
			checked := strings.ToLower(m[1]) == "x"
			key := strings.ToUpper(strings.ReplaceAll(m[2], "-", ""))
			title := strings.TrimSpace(m[3])
			if key == "" {
				key = fmt.Sprintf("AC%d", idx)
			}
			cleanTitle := strings.Trim(title, "*_ ")
			if cleanTitle != "" && !seenKeys[key] {
				seenKeys[key] = true
				items = append(items, ParsedACItem{
					Key:      key,
					Title:    cleanTitle,
					Verified: checked,
				})
				idx++
				continue
			}
		}

		// Try explicit AC key: AC1 ... or - **AC1**：...
		if m := acKeyRE.FindStringSubmatch(trimmed); len(m) >= 3 {
			key := strings.ToUpper(strings.ReplaceAll(m[1], "-", ""))
			title := strings.TrimSpace(m[2])
			cleanTitle := strings.Trim(title, "*_ :：")
			if cleanTitle != "" && !seenKeys[key] {
				seenKeys[key] = true
				items = append(items, ParsedACItem{
					Key:      key,
					Title:    cleanTitle,
					Verified: false,
				})
				idx++
				continue
			}
		}

		// If we are strictly within the AC section, also match generic bullets or numbered items:
		if headerLoc != nil {
			if m := acNumRE.FindStringSubmatch(trimmed); len(m) >= 3 {
				key := strings.ToUpper(strings.ReplaceAll(m[1], "-", ""))
				title := strings.TrimSpace(m[2])
				if key == "" {
					key = fmt.Sprintf("AC%d", idx)
				}
				cleanTitle := strings.Trim(title, "*_ :：")
				if cleanTitle != "" && !seenKeys[key] {
					seenKeys[key] = true
					items = append(items, ParsedACItem{
						Key:      key,
						Title:    cleanTitle,
						Verified: false,
					})
					idx++
					continue
				}
			}
		}
	}

	return items
}

// ---------------- Acceptance Criteria Handlers ----------------

// ListIssueACs handles GET /api/issues/{id}/ac
func (h *Handler) ListIssueACs(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}

	acs, err := h.Queries.ListIssueACsByIssue(r.Context(), issue.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list acceptance criteria")
		return
	}

	todos, _ := h.Queries.ListIssueTodosByIssue(r.Context(), issue.ID)
	todoMap := make(map[string][]string) // ac key or id -> todo ids
	for _, t := range todos {
		acIDs := parseStringSlice(t.AcIds)
		for _, aid := range acIDs {
			todoMap[aid] = append(todoMap[aid], uuidToString(t.ID))
		}
	}

	resp := make([]IssueACResponse, len(acs))
	for i, ac := range acs {
		resp[i] = issueACToResponse(ac)
		acIDStr := uuidToString(ac.ID)
		linked := append(todoMap[acIDStr], todoMap[ac.Key]...)
		resp[i].LinkedTodoIDs = dedupeStrings(linked)
	}

	writeJSON(w, http.StatusOK, resp)
}

// CreateIssueACRequest payload
type CreateIssueACRequest struct {
	Key      string            `json:"key"`
	Title    string            `json:"title"`
	Status   string            `json:"status"`
	Evidence []IssueACEvidence `json:"evidence"`
	Position *int32            `json:"position"`
}

// CreateIssueAC handles POST /api/issues/{id}/ac
func (h *Handler) CreateIssueAC(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	actorType, actorID := h.resolveActor(r, userID, uuidToString(issue.WorkspaceID))
	actorUUID := parseUUID(actorID)

	var req CreateIssueACRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	title := strings.TrimSpace(req.Title)
	if title == "" {
		writeError(w, http.StatusBadRequest, "title is required")
		return
	}

	status := strings.TrimSpace(req.Status)
	if status == "" {
		status = ACStatusUncompleted
	}
	if !validACStatus(status) {
		writeError(w, http.StatusBadRequest, "invalid status")
		return
	}

	existing, _ := h.Queries.ListIssueACsByIssue(r.Context(), issue.ID)
	key := strings.TrimSpace(req.Key)
	if key == "" {
		key = fmt.Sprintf("AC%d", len(existing)+1)
	}

	pos := int32(len(existing))
	if req.Position != nil {
		pos = *req.Position
	}

	evidenceBytes, _ := json.Marshal(req.Evidence)
	if req.Evidence == nil {
		evidenceBytes = []byte("[]")
	}

	acID := uuidToPG(uuid.New())
	var verifiedAt pgtype.Timestamptz
	var verifiedByType pgtype.Text
	var verifiedByID pgtype.UUID
	if status == ACStatusVerified {
		verifiedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
		verifiedByType = pgtype.Text{String: actorType, Valid: true}
		verifiedByID = actorUUID
	}

	row, err := h.Queries.CreateIssueAC(r.Context(), db.CreateIssueACParams{
		ID:              acID,
		WorkspaceID:     issue.WorkspaceID,
		IssueID:         issue.ID,
		Key:             key,
		Title:           title,
		Status:          status,
		Evidence:        evidenceBytes,
		RejectionReason: "",
		VerifiedAt:      verifiedAt,
		VerifiedByType:  verifiedByType,
		VerifiedByID:    verifiedByID,
		Position:        pos,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create acceptance criterion: "+err.Error())
		return
	}

	// Record audit
	_, _ = h.Queries.CreateTaskStateAudit(r.Context(), db.CreateTaskStateAuditParams{
		ID:          uuidToPG(uuid.New()),
		WorkspaceID: issue.WorkspaceID,
		IssueID:     issue.ID,
		TargetType:  "ac",
		TargetID:    row.ID,
		Action:      "created",
		FromStatus:  "",
		ToStatus:    status,
		ActorType:   actorType,
		ActorID:     actorUUID,
		Evidence:    evidenceBytes,
		Note:        "manually created",
	})

	writeJSON(w, http.StatusCreated, issueACToResponse(row))
}

// ExtractIssueACs handles POST /api/issues/{id}/ac/extract
func (h *Handler) ExtractIssueACs(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	actorType, actorID := h.resolveActor(r, userID, uuidToString(issue.WorkspaceID))
	actorUUID := parseUUID(actorID)

	if !issue.Description.Valid || strings.TrimSpace(issue.Description.String) == "" {
		writeError(w, http.StatusBadRequest, "issue description is empty")
		return
	}

	parsedItems := ExtractACFromMarkdown(issue.Description.String)
	if len(parsedItems) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{
			"extracted_count": 0,
			"acs":             []IssueACResponse{},
		})
		return
	}

	existing, _ := h.Queries.ListIssueACsByIssue(r.Context(), issue.ID)
	existingByKey := make(map[string]db.IssueAcceptanceCriterium)
	for _, ex := range existing {
		existingByKey[strings.ToUpper(ex.Key)] = ex
	}

	extractedCount := 0
	for i, item := range parsedItems {
		normKey := strings.ToUpper(item.Key)
		if _, found := existingByKey[normKey]; found {
			// Idempotent: preserve existing row, do not overwrite progress or verification state!
			continue
		}

		st := ACStatusUncompleted
		var verifiedAt pgtype.Timestamptz
		var verifiedByType pgtype.Text
		var verifiedByID pgtype.UUID
		if item.Verified {
			st = ACStatusVerified
			verifiedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
			verifiedByType = pgtype.Text{String: actorType, Valid: true}
			verifiedByID = actorUUID
		}

		newID := uuidToPG(uuid.New())
		row, err := h.Queries.CreateIssueAC(r.Context(), db.CreateIssueACParams{
			ID:              newID,
			WorkspaceID:     issue.WorkspaceID,
			IssueID:         issue.ID,
			Key:             item.Key,
			Title:           item.Title,
			Status:          st,
			Evidence:        []byte("[]"),
			RejectionReason: "",
			VerifiedAt:      verifiedAt,
			VerifiedByType:  verifiedByType,
			VerifiedByID:    verifiedByID,
			Position:        int32(len(existing) + i),
		})
		if err == nil {
			extractedCount++
			_, _ = h.Queries.CreateTaskStateAudit(r.Context(), db.CreateTaskStateAuditParams{
				ID:          uuidToPG(uuid.New()),
				WorkspaceID: issue.WorkspaceID,
				IssueID:     issue.ID,
				TargetType:  "ac",
				TargetID:    row.ID,
				Action:      "created",
				FromStatus:  "",
				ToStatus:    st,
				ActorType:   actorType,
				ActorID:     actorUUID,
				Evidence:    []byte("[]"),
				Note:        "extracted from issue description",
			})
		}
	}

	allAcs, _ := h.Queries.ListIssueACsByIssue(r.Context(), issue.ID)
	resp := make([]IssueACResponse, len(allAcs))
	for i, ac := range allAcs {
		resp[i] = issueACToResponse(ac)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"extracted_count": extractedCount,
		"acs":             resp,
	})
}

// GetIssueAC handles GET /api/issues/{id}/ac/{acId}
func (h *Handler) GetIssueAC(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	acID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "acId"), "acId")
	if !ok {
		return
	}

	ac, err := h.Queries.GetIssueAC(r.Context(), acID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "acceptance criterion not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to get acceptance criterion")
		return
	}
	if ac.IssueID != issue.ID {
		writeError(w, http.StatusNotFound, "acceptance criterion does not belong to this issue")
		return
	}

	writeJSON(w, http.StatusOK, issueACToResponse(ac))
}

// UpdateIssueACRequest payload
type UpdateIssueACRequest struct {
	Key             *string            `json:"key"`
	Title           *string            `json:"title"`
	Status          *string            `json:"status"`
	Evidence        *[]IssueACEvidence `json:"evidence"`
	RejectionReason *string            `json:"rejection_reason"`
	Position        *int32             `json:"position"`
}

// UpdateIssueAC handles PATCH /api/issues/{id}/ac/{acId}
func (h *Handler) UpdateIssueAC(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	acID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "acId"), "acId")
	if !ok {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	actorType, actorID := h.resolveActor(r, userID, uuidToString(issue.WorkspaceID))
	actorUUID := parseUUID(actorID)

	existing, err := h.Queries.GetIssueAC(r.Context(), acID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "acceptance criterion not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to get acceptance criterion")
		return
	}
	if existing.IssueID != issue.ID {
		writeError(w, http.StatusNotFound, "acceptance criterion does not belong to this issue")
		return
	}

	var req UpdateIssueACRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	key := existing.Key
	if req.Key != nil && strings.TrimSpace(*req.Key) != "" {
		key = strings.TrimSpace(*req.Key)
	}

	title := existing.Title
	if req.Title != nil && strings.TrimSpace(*req.Title) != "" {
		title = strings.TrimSpace(*req.Title)
	}

	status := existing.Status
	fromStatus := existing.Status
	verifiedAt := existing.VerifiedAt
	verifiedByType := existing.VerifiedByType
	verifiedByID := existing.VerifiedByID
	rejectionReason := existing.RejectionReason

	if req.Status != nil {
		newStatus := strings.TrimSpace(*req.Status)
		if !validACStatus(newStatus) {
			writeError(w, http.StatusBadRequest, "invalid status")
			return
		}
		status = newStatus
		if status == ACStatusVerified && fromStatus != ACStatusVerified {
			verifiedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
			verifiedByType = pgtype.Text{String: actorType, Valid: true}
			verifiedByID = actorUUID
		} else if status != ACStatusVerified {
			verifiedAt = pgtype.Timestamptz{Valid: false}
			verifiedByType = pgtype.Text{Valid: false}
			verifiedByID = pgtype.UUID{Valid: false}
		}
	}

	if req.RejectionReason != nil {
		rejectionReason = *req.RejectionReason
	}

	evidenceBytes := existing.Evidence
	if req.Evidence != nil {
		evidenceBytes, _ = json.Marshal(*req.Evidence)
	}

	pos := existing.Position
	if req.Position != nil {
		pos = *req.Position
	}

	updated, err := h.Queries.UpdateIssueAC(r.Context(), db.UpdateIssueACParams{
		ID:              existing.ID,
		Key:             key,
		Title:           title,
		Status:          status,
		Evidence:        evidenceBytes,
		RejectionReason: rejectionReason,
		VerifiedAt:      verifiedAt,
		VerifiedByType:  verifiedByType,
		VerifiedByID:    verifiedByID,
		Position:        pos,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update acceptance criterion: "+err.Error())
		return
	}

	// Audit trail
	action := "updated"
	if status != fromStatus {
		if status == ACStatusVerified {
			action = "verified"
		} else if status == ACStatusRejected {
			action = "rejected"
		} else {
			action = "status_changed"
		}
	}
	_, _ = h.Queries.CreateTaskStateAudit(r.Context(), db.CreateTaskStateAuditParams{
		ID:          uuidToPG(uuid.New()),
		WorkspaceID: issue.WorkspaceID,
		IssueID:     issue.ID,
		TargetType:  "ac",
		TargetID:    updated.ID,
		Action:      action,
		FromStatus:  fromStatus,
		ToStatus:    status,
		ActorType:   actorType,
		ActorID:     actorUUID,
		Evidence:    evidenceBytes,
		Note:        rejectionReason,
	})

	writeJSON(w, http.StatusOK, issueACToResponse(updated))
}

// VerifyIssueACRequest payload
type VerifyIssueACRequest struct {
	Evidence []IssueACEvidence `json:"evidence"`
	Note     string            `json:"note"`
}

// VerifyIssueAC handles POST /api/issues/{id}/ac/{acId}/verify
func (h *Handler) VerifyIssueAC(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	acID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "acId"), "acId")
	if !ok {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	actorType, actorID := h.resolveActor(r, userID, uuidToString(issue.WorkspaceID))
	actorUUID := parseUUID(actorID)

	existing, err := h.Queries.GetIssueAC(r.Context(), acID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "acceptance criterion not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to get acceptance criterion")
		return
	}

	var req VerifyIssueACRequest
	_ = json.NewDecoder(r.Body).Decode(&req)

	evidenceList := parseEvidenceSlice(existing.Evidence)
	if len(req.Evidence) > 0 {
		evidenceList = append(evidenceList, req.Evidence...)
	}
	evidenceBytes, _ := json.Marshal(evidenceList)

	updated, err := h.Queries.UpdateIssueAC(r.Context(), db.UpdateIssueACParams{
		ID:              existing.ID,
		Key:             existing.Key,
		Title:           existing.Title,
		Status:          ACStatusVerified,
		Evidence:        evidenceBytes,
		RejectionReason: "",
		VerifiedAt:      pgtype.Timestamptz{Time: time.Now(), Valid: true},
		VerifiedByType:  pgtype.Text{String: actorType, Valid: true},
		VerifiedByID:    actorUUID,
		Position:        existing.Position,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to verify acceptance criterion: "+err.Error())
		return
	}

	_, _ = h.Queries.CreateTaskStateAudit(r.Context(), db.CreateTaskStateAuditParams{
		ID:          uuidToPG(uuid.New()),
		WorkspaceID: issue.WorkspaceID,
		IssueID:     issue.ID,
		TargetType:  "ac",
		TargetID:    updated.ID,
		Action:      "verified",
		FromStatus:  existing.Status,
		ToStatus:    ACStatusVerified,
		ActorType:   actorType,
		ActorID:     actorUUID,
		Evidence:    evidenceBytes,
		Note:        req.Note,
	})

	writeJSON(w, http.StatusOK, issueACToResponse(updated))
}

// RejectIssueACRequest payload
type RejectIssueACRequest struct {
	Reason   string            `json:"reason"`
	Evidence []IssueACEvidence `json:"evidence"`
	Note     string            `json:"note"`
}

// RejectIssueAC handles POST /api/issues/{id}/ac/{acId}/reject
func (h *Handler) RejectIssueAC(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	acID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "acId"), "acId")
	if !ok {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	actorType, actorID := h.resolveActor(r, userID, uuidToString(issue.WorkspaceID))
	actorUUID := parseUUID(actorID)

	existing, err := h.Queries.GetIssueAC(r.Context(), acID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "acceptance criterion not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to get acceptance criterion")
		return
	}

	var req RejectIssueACRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		writeError(w, http.StatusBadRequest, "rejection reason is required")
		return
	}

	evidenceList := parseEvidenceSlice(existing.Evidence)
	if len(req.Evidence) > 0 {
		evidenceList = append(evidenceList, req.Evidence...)
	}
	evidenceBytes, _ := json.Marshal(evidenceList)

	updated, err := h.Queries.UpdateIssueAC(r.Context(), db.UpdateIssueACParams{
		ID:              existing.ID,
		Key:             existing.Key,
		Title:           existing.Title,
		Status:          ACStatusRejected,
		Evidence:        evidenceBytes,
		RejectionReason: reason,
		VerifiedAt:      pgtype.Timestamptz{Valid: false},
		VerifiedByType:  pgtype.Text{Valid: false},
		VerifiedByID:    pgtype.UUID{Valid: false},
		Position:        existing.Position,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to reject acceptance criterion: "+err.Error())
		return
	}

	_, _ = h.Queries.CreateTaskStateAudit(r.Context(), db.CreateTaskStateAuditParams{
		ID:          uuidToPG(uuid.New()),
		WorkspaceID: issue.WorkspaceID,
		IssueID:     issue.ID,
		TargetType:  "ac",
		TargetID:    updated.ID,
		Action:      "rejected",
		FromStatus:  existing.Status,
		ToStatus:    ACStatusRejected,
		ActorType:   actorType,
		ActorID:     actorUUID,
		Evidence:    evidenceBytes,
		Note:        reason,
	})

	writeJSON(w, http.StatusOK, issueACToResponse(updated))
}

// DeleteIssueAC handles DELETE /api/issues/{id}/ac/{acId}
func (h *Handler) DeleteIssueAC(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	acID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "acId"), "acId")
	if !ok {
		return
	}

	existing, err := h.Queries.GetIssueAC(r.Context(), acID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "acceptance criterion not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to get acceptance criterion")
		return
	}
	if existing.IssueID != issue.ID {
		writeError(w, http.StatusNotFound, "acceptance criterion does not belong to this issue")
		return
	}

	if err := h.Queries.DeleteIssueAC(r.Context(), acID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete acceptance criterion")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ---------------- Dynamic TODO Handlers ----------------

// ListIssueTodos handles GET /api/issues/{id}/todos
func (h *Handler) ListIssueTodos(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}

	todos, err := h.Queries.ListIssueTodosByIssue(r.Context(), issue.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list todos")
		return
	}

	resp := make([]IssueTodoResponse, len(todos))
	for i, t := range todos {
		resp[i] = issueTodoToResponse(t)
	}

	writeJSON(w, http.StatusOK, resp)
}

// CreateIssueTodoRequest payload
type CreateIssueTodoRequest struct {
	Title         string            `json:"title"`
	Status        string            `json:"status"`
	NextStep      string            `json:"next_step"`
	BlockedReason string            `json:"blocked_reason"`
	AssigneeType  *string           `json:"assignee_type"`
	AssigneeID    *string           `json:"assignee_id"`
	ACIDs         []string          `json:"ac_ids"`
	Evidence      []IssueACEvidence `json:"evidence"`
	Position      *int32            `json:"position"`
}

// CreateIssueTodo handles POST /api/issues/{id}/todos
func (h *Handler) CreateIssueTodo(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	actorType, actorID := h.resolveActor(r, userID, uuidToString(issue.WorkspaceID))
	actorUUID := parseUUID(actorID)

	var req CreateIssueTodoRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	title := strings.TrimSpace(req.Title)
	if title == "" {
		writeError(w, http.StatusBadRequest, "title is required")
		return
	}

	status := strings.TrimSpace(req.Status)
	if status == "" {
		status = TodoStatusPending
	}
	if !validTodoStatus(status) {
		writeError(w, http.StatusBadRequest, "invalid status")
		return
	}

	existing, _ := h.Queries.ListIssueTodosByIssue(r.Context(), issue.ID)
	pos := int32(len(existing))
	if req.Position != nil {
		pos = *req.Position
	}

	acIDsBytes, _ := json.Marshal(req.ACIDs)
	if req.ACIDs == nil {
		acIDsBytes = []byte("[]")
	}

	evidenceBytes, _ := json.Marshal(req.Evidence)
	if req.Evidence == nil {
		evidenceBytes = []byte("[]")
	}

	var assigneeType pgtype.Text
	if req.AssigneeType != nil && *req.AssigneeType != "" {
		assigneeType = pgtype.Text{String: *req.AssigneeType, Valid: true}
	}
	var assigneeID pgtype.UUID
	if req.AssigneeID != nil && *req.AssigneeID != "" {
		var valid bool
		assigneeID, valid = parseUUIDOrBadRequest(w, *req.AssigneeID, "assignee_id")
		if !valid {
			return
		}
	}

	todoID := uuidToPG(uuid.New())
	row, err := h.Queries.CreateIssueTodo(r.Context(), db.CreateIssueTodoParams{
		ID:            todoID,
		WorkspaceID:   issue.WorkspaceID,
		IssueID:       issue.ID,
		Title:         title,
		Status:        status,
		NextStep:      req.NextStep,
		BlockedReason: req.BlockedReason,
		AssigneeType:  assigneeType,
		AssigneeID:    assigneeID,
		AcIds:         acIDsBytes,
		Evidence:      evidenceBytes,
		Position:      pos,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create todo: "+err.Error())
		return
	}

	_, _ = h.Queries.CreateTaskStateAudit(r.Context(), db.CreateTaskStateAuditParams{
		ID:          uuidToPG(uuid.New()),
		WorkspaceID: issue.WorkspaceID,
		IssueID:     issue.ID,
		TargetType:  "todo",
		TargetID:    row.ID,
		Action:      "created",
		FromStatus:  "",
		ToStatus:    status,
		ActorType:   actorType,
		ActorID:     actorUUID,
		Evidence:    evidenceBytes,
		Note:        req.NextStep,
	})

	writeJSON(w, http.StatusCreated, issueTodoToResponse(row))
}

// GetIssueTodo handles GET /api/issues/{id}/todos/{todoId}
func (h *Handler) GetIssueTodo(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	todoID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "todoId"), "todoId")
	if !ok {
		return
	}

	todo, err := h.Queries.GetIssueTodo(r.Context(), todoID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "todo not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to get todo")
		return
	}
	if todo.IssueID != issue.ID {
		writeError(w, http.StatusNotFound, "todo does not belong to this issue")
		return
	}

	writeJSON(w, http.StatusOK, issueTodoToResponse(todo))
}

// UpdateIssueTodoRequest payload
type UpdateIssueTodoRequest struct {
	Title         *string            `json:"title"`
	Status        *string            `json:"status"`
	NextStep      *string            `json:"next_step"`
	BlockedReason *string            `json:"blocked_reason"`
	AssigneeType  *string            `json:"assignee_type"`
	AssigneeID    *string            `json:"assignee_id"`
	ACIDs         *[]string          `json:"ac_ids"`
	Evidence      *[]IssueACEvidence `json:"evidence"`
	Position      *int32             `json:"position"`
}

// UpdateIssueTodo handles PATCH /api/issues/{id}/todos/{todoId}
func (h *Handler) UpdateIssueTodo(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	todoID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "todoId"), "todoId")
	if !ok {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	actorType, actorID := h.resolveActor(r, userID, uuidToString(issue.WorkspaceID))
	actorUUID := parseUUID(actorID)

	existing, err := h.Queries.GetIssueTodo(r.Context(), todoID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "todo not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to get todo")
		return
	}
	if existing.IssueID != issue.ID {
		writeError(w, http.StatusNotFound, "todo does not belong to this issue")
		return
	}

	var req UpdateIssueTodoRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	title := existing.Title
	if req.Title != nil && strings.TrimSpace(*req.Title) != "" {
		title = strings.TrimSpace(*req.Title)
	}

	status := existing.Status
	fromStatus := existing.Status
	if req.Status != nil {
		newStatus := strings.TrimSpace(*req.Status)
		if !validTodoStatus(newStatus) {
			writeError(w, http.StatusBadRequest, "invalid status")
			return
		}
		status = newStatus
		// Note: Rule AC3: completing a TODO does NOT mark associated AC as verified!
	}

	nextStep := existing.NextStep
	if req.NextStep != nil {
		nextStep = *req.NextStep
	}

	blockedReason := existing.BlockedReason
	if req.BlockedReason != nil {
		blockedReason = *req.BlockedReason
	}

	assigneeType := existing.AssigneeType
	if req.AssigneeType != nil {
		if *req.AssigneeType == "" {
			assigneeType = pgtype.Text{Valid: false}
		} else {
			assigneeType = pgtype.Text{String: *req.AssigneeType, Valid: true}
		}
	}

	assigneeID := existing.AssigneeID
	if req.AssigneeID != nil {
		if *req.AssigneeID == "" {
			assigneeID = pgtype.UUID{Valid: false}
		} else {
			var valid bool
			assigneeID, valid = parseUUIDOrBadRequest(w, *req.AssigneeID, "assignee_id")
			if !valid {
				return
			}
		}
	}

	acIDsBytes := existing.AcIds
	if req.ACIDs != nil {
		acIDsBytes, _ = json.Marshal(*req.ACIDs)
	}

	evidenceBytes := existing.Evidence
	if req.Evidence != nil {
		evidenceBytes, _ = json.Marshal(*req.Evidence)
	}

	pos := existing.Position
	if req.Position != nil {
		pos = *req.Position
	}

	updated, err := h.Queries.UpdateIssueTodo(r.Context(), db.UpdateIssueTodoParams{
		ID:            existing.ID,
		Title:         title,
		Status:        status,
		NextStep:      nextStep,
		BlockedReason: blockedReason,
		AssigneeType:  assigneeType,
		AssigneeID:    assigneeID,
		AcIds:         acIDsBytes,
		Evidence:      evidenceBytes,
		Position:      pos,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update todo: "+err.Error())
		return
	}

	action := "updated"
	if status != fromStatus {
		action = "status_changed"
	}
	_, _ = h.Queries.CreateTaskStateAudit(r.Context(), db.CreateTaskStateAuditParams{
		ID:          uuidToPG(uuid.New()),
		WorkspaceID: issue.WorkspaceID,
		IssueID:     issue.ID,
		TargetType:  "todo",
		TargetID:    updated.ID,
		Action:      action,
		FromStatus:  fromStatus,
		ToStatus:    status,
		ActorType:   actorType,
		ActorID:     actorUUID,
		Evidence:    evidenceBytes,
		Note:        nextStep,
	})

	writeJSON(w, http.StatusOK, issueTodoToResponse(updated))
}

// ReopenIssueTodoRequest payload
type ReopenIssueTodoRequest struct {
	NextStep string `json:"next_step"`
	Note     string `json:"note"`
}

// ReopenIssueTodo handles POST /api/issues/{id}/todos/{todoId}/reopen
func (h *Handler) ReopenIssueTodo(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	todoID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "todoId"), "todoId")
	if !ok {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	actorType, actorID := h.resolveActor(r, userID, uuidToString(issue.WorkspaceID))
	actorUUID := parseUUID(actorID)

	existing, err := h.Queries.GetIssueTodo(r.Context(), todoID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "todo not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to get todo")
		return
	}
	if existing.IssueID != issue.ID {
		writeError(w, http.StatusNotFound, "todo does not belong to this issue")
		return
	}

	var req ReopenIssueTodoRequest
	_ = json.NewDecoder(r.Body).Decode(&req)

	nextStep := req.NextStep
	if strings.TrimSpace(nextStep) == "" {
		nextStep = existing.NextStep
	}

	updated, err := h.Queries.UpdateIssueTodo(r.Context(), db.UpdateIssueTodoParams{
		ID:            existing.ID,
		Title:         existing.Title,
		Status:        TodoStatusInProgress,
		NextStep:      nextStep,
		BlockedReason: "", // clear blocked reason on reopen
		AssigneeType:  existing.AssigneeType,
		AssigneeID:    existing.AssigneeID,
		AcIds:         existing.AcIds,
		Evidence:      existing.Evidence,
		Position:      existing.Position,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to reopen todo: "+err.Error())
		return
	}

	_, _ = h.Queries.CreateTaskStateAudit(r.Context(), db.CreateTaskStateAuditParams{
		ID:          uuidToPG(uuid.New()),
		WorkspaceID: issue.WorkspaceID,
		IssueID:     issue.ID,
		TargetType:  "todo",
		TargetID:    updated.ID,
		Action:      "reopened",
		FromStatus:  existing.Status,
		ToStatus:    TodoStatusInProgress,
		ActorType:   actorType,
		ActorID:     actorUUID,
		Evidence:    existing.Evidence,
		Note:        req.Note,
	})

	writeJSON(w, http.StatusOK, issueTodoToResponse(updated))
}

// DeleteIssueTodo handles DELETE /api/issues/{id}/todos/{todoId}
func (h *Handler) DeleteIssueTodo(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	todoID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "todoId"), "todoId")
	if !ok {
		return
	}

	existing, err := h.Queries.GetIssueTodo(r.Context(), todoID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "todo not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to get todo")
		return
	}
	if existing.IssueID != issue.ID {
		writeError(w, http.StatusNotFound, "todo does not belong to this issue")
		return
	}

	if err := h.Queries.DeleteIssueTodo(r.Context(), todoID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete todo")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ---------------- Composite Ledger Handler ----------------

// GetIssueLedger handles GET /api/issues/{id}/ledger
func (h *Handler) GetIssueLedger(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}

	ledger, err := h.buildIssueLedger(r.Context(), issue.ID, issue.WorkspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to build ledger: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, ledger)
}

func (h *Handler) buildIssueLedger(ctx context.Context, issueID, workspaceID pgtype.UUID) (IssueLedgerResponse, error) {
	acs, err := h.Queries.ListIssueACsByIssue(ctx, issueID)
	if err != nil {
		return IssueLedgerResponse{}, err
	}

	todos, err := h.Queries.ListIssueTodosByIssue(ctx, issueID)
	if err != nil {
		return IssueLedgerResponse{}, err
	}

	audits, _ := h.Queries.ListTaskStateAuditByIssue(ctx, issueID)

	todoMap := make(map[string][]string) // ac key or id -> todo ids
	for _, t := range todos {
		acIDs := parseStringSlice(t.AcIds)
		for _, aid := range acIDs {
			todoMap[aid] = append(todoMap[aid], uuidToString(t.ID))
		}
	}

	acResp := make([]IssueACResponse, len(acs))
	acVerified := 0
	acPending := 0
	acRejected := 0
	for i, ac := range acs {
		acResp[i] = issueACToResponse(ac)
		linked := append(todoMap[uuidToString(ac.ID)], todoMap[ac.Key]...)
		acResp[i].LinkedTodoIDs = dedupeStrings(linked)

		switch ac.Status {
		case ACStatusVerified:
			acVerified++
		case ACStatusRejected:
			acRejected++
		default:
			acPending++
		}
	}

	todoResp := make([]IssueTodoResponse, len(todos))
	todoCompleted := 0
	todoInProgress := 0
	todoPending := 0
	todoBlocked := 0
	var nextSteps []string

	for i, t := range todos {
		todoResp[i] = issueTodoToResponse(t)
		switch t.Status {
		case TodoStatusCompleted:
			todoCompleted++
		case TodoStatusInProgress:
			todoInProgress++
			if t.NextStep != "" {
				nextSteps = append(nextSteps, t.NextStep)
			}
		case TodoStatusBlocked:
			todoBlocked++
			if t.BlockedReason != "" {
				nextSteps = append(nextSteps, "[Blocked] "+t.BlockedReason)
			}
		default:
			todoPending++
		}
	}

	auditResp := make([]TaskStateAuditResponse, len(audits))
	for i, a := range audits {
		auditResp[i] = taskStateAuditToResponse(a)
	}

	return IssueLedgerResponse{
		ACs:   acResp,
		Todos: todoResp,
		Audit: auditResp,
		Summary: IssueLedgerSummary{
			ACTotal:          len(acs),
			ACVerified:       acVerified,
			ACPending:        acPending,
			ACRejected:       acRejected,
			TodoTotal:        len(todos),
			TodoCompleted:    todoCompleted,
			TodoInProgress:   todoInProgress,
			TodoPending:      todoPending,
			TodoBlocked:      todoBlocked,
			CurrentNextSteps: dedupeStrings(nextSteps),
		},
	}, nil
}
