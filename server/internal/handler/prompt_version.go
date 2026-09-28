package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/logger"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/promptscan"
)

// Prompt version management (RUYI-183, self-evolution phase 1): version
// history, diff, switch and rollback for the four prompt tiers — workspace
// context, project instructions, squad instructions, agent instructions.
//
// The business column on workspace/project/squad/agent stays the single
// read source for "currently effective content". prompt_version is
// history/audit only. Edit-save, switch-to-a-historical-version and
// rollback are the same underlying write: lock the owning entity row,
// assign the next version number, insert a prompt_version row, and copy
// its content into the business column — all in one transaction. Nothing
// here is ever an UPDATE or DELETE on an existing prompt_version row, so
// the version line itself is the append-only audit trail (ADR-001 §4.2);
// there is no separate switch/audit table.
//
// Every write requires RequireHumanActor + workspace Owner role (wired in
// router.go), per PM spec §3.6: writes are Owner-only, not Owner-or-admin.

// promptVersionScope is one of the four supported tiers. The literal set
// mirrors the prompt_version.scope CHECK constraint.
type promptVersionScope string

const (
	promptVersionScopeWorkspace promptVersionScope = "workspace"
	promptVersionScopeProject   promptVersionScope = "project"
	promptVersionScopeSquad     promptVersionScope = "squad"
	promptVersionScopeAgent     promptVersionScope = "agent"
)

func parsePromptVersionScope(s string) (promptVersionScope, bool) {
	switch promptVersionScope(s) {
	case promptVersionScopeWorkspace, promptVersionScopeProject, promptVersionScopeSquad, promptVersionScopeAgent:
		return promptVersionScope(s), true
	default:
		return "", false
	}
}

// lockScopeEntity locks the owning entity row (not prompt_version) inside
// tx, and returns false with a response already written when the scope_id
// does not exist in this workspace. Locking the entity row is what keeps
// NextPromptVersion race-free even for a scope with zero prior versions —
// there is no prompt_version row to lock yet, but the owning row always
// exists.
//
// It also returns the tier's current effective content, read under that
// same lock, so the empty-content guard in commitPromptGovernanceVersion
// compares against live text that cannot change before the UPDATE.
func (h *Handler) lockScopeEntity(w http.ResponseWriter, r *http.Request, qtx *db.Queries, scope promptVersionScope, workspaceID, scopeID pgtype.UUID) (string, bool) {
	ctx := r.Context()
	var current string
	var err error
	switch scope {
	case promptVersionScopeWorkspace:
		if scopeID != workspaceID {
			writeError(w, http.StatusNotFound, "scope not found in this workspace")
			return "", false
		}
		var row db.LockWorkspaceForPromptVersionRow
		row, err = qtx.LockWorkspaceForPromptVersion(ctx, scopeID)
		current = row.EffectiveContent
	case promptVersionScopeProject:
		var row db.LockProjectForPromptVersionRow
		row, err = qtx.LockProjectForPromptVersion(ctx, db.LockProjectForPromptVersionParams{ID: scopeID, WorkspaceID: workspaceID})
		current = row.EffectiveContent
	case promptVersionScopeSquad:
		var row db.LockSquadForPromptVersionRow
		row, err = qtx.LockSquadForPromptVersion(ctx, db.LockSquadForPromptVersionParams{ID: scopeID, WorkspaceID: workspaceID})
		current = row.EffectiveContent
	case promptVersionScopeAgent:
		var row db.LockAgentForPromptVersionRow
		row, err = qtx.LockAgentForPromptVersion(ctx, db.LockAgentForPromptVersionParams{ID: scopeID, WorkspaceID: workspaceID})
		current = row.EffectiveContent
	}
	if err != nil {
		writeError(w, http.StatusNotFound, "scope not found in this workspace")
		return "", false
	}
	return current, true
}

// seedPromptVersionV1 writes the v1 baseline row for a scope that was just
// created (RUYI-213). Migration 922 snapshotted every tier that existed when
// self-evolution shipped, but it was a one-time backfill: without this call a
// scope created afterwards has no v1 row at all, so version history and diff
// have nothing to anchor on and the tier's original content is unrecoverable.
//
// Idempotent through the unique index on (scope, scope_id, version): a v1 that
// already exists makes the insert a no-op rather than an error, so a caller
// retrying a partially-failed create cannot end up with a second baseline.
//
// Callers pass their transaction's *db.Queries so the baseline commits or rolls
// back with the entity it describes.
func seedPromptVersionV1(ctx context.Context, qtx *db.Queries, scope promptVersionScope, workspaceID, scopeID pgtype.UUID, content string) error {
	return qtx.InsertPromptVersionBaselineIfAbsent(ctx, db.InsertPromptVersionBaselineIfAbsentParams{
		WorkspaceID:   workspaceID,
		Scope:         string(scope),
		ScopeID:       scopeID,
		Content:       content,
		ContentSha256: sha256Hex(content),
		ChangeNote:    "v1 基线：创建时内容",
	})
}

// writeScopeEffectiveContent copies content into the scope's business
// column inside tx. This is the only place any handler in this file writes
// to workspace.context / project.instructions / squad.instructions /
// agent.instructions.
func (h *Handler) writeScopeEffectiveContent(ctx context.Context, qtx *db.Queries, scope promptVersionScope, scopeID pgtype.UUID, content string) error {
	var err error
	switch scope {
	case promptVersionScopeWorkspace:
		_, err = qtx.UpdateWorkspaceContextForPromptVersion(ctx, db.UpdateWorkspaceContextForPromptVersionParams{ID: scopeID, Context: strToText(content)})
	case promptVersionScopeProject:
		_, err = qtx.UpdateProjectInstructionsForPromptVersion(ctx, db.UpdateProjectInstructionsForPromptVersionParams{ID: scopeID, Instructions: strToText(content)})
	case promptVersionScopeSquad:
		_, err = qtx.UpdateSquadInstructionsForPromptVersion(ctx, db.UpdateSquadInstructionsForPromptVersionParams{ID: scopeID, Instructions: content})
	case promptVersionScopeAgent:
		_, err = qtx.UpdateAgentInstructionsForPromptVersion(ctx, db.UpdateAgentInstructionsForPromptVersionParams{ID: scopeID, Instructions: content})
	}
	return err
}

// PromptGovernanceVersionResponse is one version as the API returns it. content is
// always included: unlike the marketplace catalog this is a private,
// workspace-scoped audit surface with no cross-workspace disclosure
// concern, and history/diff both need the full text.
type PromptGovernanceVersionResponse struct {
	ID              string `json:"id"`
	Scope           string `json:"scope"`
	ScopeID         string `json:"scope_id"`
	Version         int32  `json:"version"`
	Content         string `json:"content"`
	ContentSha256   string `json:"content_sha256"`
	Source          string `json:"source"`
	SourceVersion   *int32 `json:"source_version,omitempty"`
	ChangeNote      string `json:"change_note"`
	AuthorUserID    string `json:"author_user_id,omitempty"`
	AuthorIssueID   string `json:"author_note_issue_id,omitempty"`
	ScannerRevision string `json:"scanner_revision"`
	CreatedAt       string `json:"created_at"`
}

func promptGovVersionToResponse(v db.PromptVersion) PromptGovernanceVersionResponse {
	resp := PromptGovernanceVersionResponse{
		ID:              uuidToString(v.ID),
		Scope:           v.Scope,
		ScopeID:         uuidToString(v.ScopeID),
		Version:         v.Version,
		Content:         v.Content,
		ContentSha256:   v.ContentSha256,
		Source:          v.Source,
		ChangeNote:      v.ChangeNote,
		ScannerRevision: v.ScannerRevision,
	}
	if v.SourceVersion.Valid {
		sv := v.SourceVersion.Int32
		resp.SourceVersion = &sv
	}
	if v.AuthorUserID.Valid {
		resp.AuthorUserID = uuidToString(v.AuthorUserID)
	}
	if v.AuthorNoteIssueID.Valid {
		resp.AuthorIssueID = uuidToString(v.AuthorNoteIssueID)
	}
	if v.CreatedAt.Valid {
		resp.CreatedAt = v.CreatedAt.Time.UTC().Format(httpTimeFormat)
	}
	return resp
}

const httpTimeFormat = "2006-01-02T15:04:05.000Z07:00"

// promptGovernanceGate runs the two-category legislative gate (ADR-001 §4.5)
// server-side, ahead of any write: credentials via promptscan, and a
// minimal mechanically-judgable structural check. The full check_prompts.py
// structural rule set lives in a separate prompt-ops repository outside
// multica and is out of scope for this issue; only the two bounds below —
// non-empty and a size ceiling — are enforced here. See the issue comment
// for the explicit scope-cut note.
const promptGovernanceMaxBytes = 200_000

func promptGovernanceGate(content string) (promptscan.Result, bool, string) {
	if len(content) == 0 {
		return promptscan.Result{}, false, "content must not be empty"
	}
	if len(content) > promptGovernanceMaxBytes {
		return promptscan.Result{}, false, "content exceeds the maximum allowed size"
	}
	scan := promptscan.Scan(content)
	return scan, scan.OK(), ""
}

// ── Handlers ────────────────────────────────────────────────────────────────

// ListPromptGovernanceVersions — GET /api/prompt-governance/{scope}/{scopeId}/versions
func (h *Handler) ListPromptGovernanceVersions(w http.ResponseWriter, r *http.Request) {
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

	limit := int32(50)
	offset := int32(0)
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			limit = int32(n)
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = int32(n)
		}
	}

	rows, err := h.Queries.ListPromptVersions(r.Context(), db.ListPromptVersionsParams{
		Scope: string(scope), ScopeID: scopeID, Limit: limit, Offset: offset,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list prompt versions")
		return
	}
	total, err := h.Queries.CountPromptVersions(r.Context(), db.CountPromptVersionsParams{Scope: string(scope), ScopeID: scopeID})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list prompt versions")
		return
	}

	out := make([]PromptGovernanceVersionResponse, 0, len(rows))
	for _, v := range rows {
		out = append(out, promptGovVersionToResponse(v))
	}
	writeJSON(w, http.StatusOK, map[string]any{"versions": out, "total": total})
}

// locklessScopeCheck validates scope_id belongs to workspaceID for read
// endpoints, without taking a row lock (reads do not need to serialize
// against writers — the version numbering invariant only matters for the
// write path in lockScopeEntity).
func (h *Handler) locklessScopeCheck(w http.ResponseWriter, r *http.Request, scope promptVersionScope, workspaceID, scopeID pgtype.UUID) bool {
	ctx := r.Context()
	var err error
	switch scope {
	case promptVersionScopeWorkspace:
		if scopeID != workspaceID {
			writeError(w, http.StatusNotFound, "scope not found in this workspace")
			return false
		}
	case promptVersionScopeProject:
		_, err = h.Queries.GetProjectInWorkspace(ctx, db.GetProjectInWorkspaceParams{ID: scopeID, WorkspaceID: workspaceID})
	case promptVersionScopeSquad:
		_, err = h.Queries.GetSquadInWorkspace(ctx, db.GetSquadInWorkspaceParams{ID: scopeID, WorkspaceID: workspaceID})
	case promptVersionScopeAgent:
		_, err = h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{ID: scopeID, WorkspaceID: workspaceID})
	}
	if err != nil {
		writeError(w, http.StatusNotFound, "scope not found in this workspace")
		return false
	}
	return true
}

// GetPromptGovernanceVersionDiff — GET /api/prompt-governance/{scope}/{scopeId}/diff?from=X&to=Y
// Returns both versions' full content; the frontend computes the visual
// diff, so the response is intentionally not a patch/hunk format.
// GetPromptGovernanceVersion — GET /api/prompt-governance/{scope}/{scopeId}/versions/{version}
func (h *Handler) GetPromptGovernanceVersion(w http.ResponseWriter, r *http.Request) {
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
	version, err := strconv.Atoi(chi.URLParam(r, "version"))
	if err != nil || version <= 0 {
		writeError(w, http.StatusBadRequest, "invalid version")
		return
	}
	row, err := h.Queries.GetPromptVersionByScopeVersion(r.Context(), db.GetPromptVersionByScopeVersionParams{
		Scope: string(scope), ScopeID: scopeID, Version: int32(version),
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "version not found")
		return
	}
	writeJSON(w, http.StatusOK, promptGovVersionToResponse(row))
}

func (h *Handler) GetPromptGovernanceVersionDiff(w http.ResponseWriter, r *http.Request) {
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

	from, ok1 := strconv.Atoi(r.URL.Query().Get("from"))
	to, ok2 := strconv.Atoi(r.URL.Query().Get("to"))
	if ok1 != nil || ok2 != nil || from <= 0 || to <= 0 {
		writeError(w, http.StatusBadRequest, "from and to must be positive version numbers")
		return
	}

	fromRow, err := h.Queries.GetPromptVersionByScopeVersion(r.Context(), db.GetPromptVersionByScopeVersionParams{Scope: string(scope), ScopeID: scopeID, Version: int32(from)})
	if err != nil {
		writeError(w, http.StatusNotFound, "from version not found")
		return
	}
	toRow, err := h.Queries.GetPromptVersionByScopeVersion(r.Context(), db.GetPromptVersionByScopeVersionParams{Scope: string(scope), ScopeID: scopeID, Version: int32(to)})
	if err != nil {
		writeError(w, http.StatusNotFound, "to version not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"from": promptGovVersionToResponse(fromRow),
		"to":   promptGovVersionToResponse(toRow),
	})
}

// SavePromptGovernanceVersionRequest is the body for both save-edit and
// switch/rollback — the latter two are POST .../switch with no body other
// than the target version in the URL.
type SavePromptGovernanceVersionRequest struct {
	Content    string `json:"content"`
	ChangeNote string `json:"change_note"`
}

// SavePromptGovernanceVersion — POST /api/prompt-governance/{scope}/{scopeId}/versions
// Owner-only (wired via RequireWorkspaceRole "owner" in router.go). Runs the
// legislative gate, then in one transaction: locks the owning entity row,
// assigns the next version, inserts the prompt_version row, and writes the
// business column.
func (h *Handler) SavePromptGovernanceVersion(w http.ResponseWriter, r *http.Request) {
	scope, ok := parsePromptVersionScope(chi.URLParam(r, "scope"))
	if !ok {
		writeError(w, http.StatusBadRequest, "unknown scope")
		return
	}
	scopeID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "scopeId"), "scope_id")
	if !ok {
		return
	}
	var req SavePromptGovernanceVersionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	scan, ok, badReason := promptGovernanceGate(req.Content)
	if badReason != "" {
		writeError(w, http.StatusBadRequest, badReason)
		return
	}
	if !ok {
		slog.Info("prompt version save blocked by secret scan", append(logger.RequestAttrs(r),
			"scope", string(scope), "scope_id", chi.URLParam(r, "scopeId"),
			"findings", len(scan.Findings), "scanner_revision", scan.Revision)...)
		writePromptScanBlocked(w, scan)
		return
	}

	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	h.commitPromptGovernanceVersion(w, r, scope, scopeID, promptGovWrite{
		content:         req.Content,
		changeNote:      req.ChangeNote,
		source:          "edit",
		scannerRevision: scan.Revision,
		gateResult:      scan.Findings,
		authorUserID:    userID,
	})
}

// SwitchPromptGovernanceVersion — POST /api/prompt-governance/{scope}/{scopeId}/versions/{version}/switch
// Copy-forward: creates a new version whose content equals the target
// historical version's content (source='revert', source_version=target),
// and makes it effective. This is the same mechanism rollback uses — PM
// spec §3.3's "switching to a historical version = generating a new version
// with that content" holds for both.
func (h *Handler) SwitchPromptGovernanceVersion(w http.ResponseWriter, r *http.Request) {
	scope, ok := parsePromptVersionScope(chi.URLParam(r, "scope"))
	if !ok {
		writeError(w, http.StatusBadRequest, "unknown scope")
		return
	}
	scopeID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "scopeId"), "scope_id")
	if !ok {
		return
	}
	targetVersion, err := strconv.Atoi(chi.URLParam(r, "version"))
	if err != nil || targetVersion <= 0 {
		writeError(w, http.StatusBadRequest, "invalid version")
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	target, err := h.Queries.GetPromptVersionByScopeVersion(r.Context(), db.GetPromptVersionByScopeVersionParams{
		Scope: string(scope), ScopeID: scopeID, Version: int32(targetVersion),
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "target version not found")
		return
	}

	sv := target.Version
	h.commitPromptGovernanceVersion(w, r, scope, scopeID, promptGovWrite{
		content:      target.Content,
		changeNote:   "切换至历史版本 v" + strconv.Itoa(int(sv)),
		source:       "revert",
		sourceVer:    &sv,
		authorUserID: userID,
	})
}

// promptGovWrite carries the fields that differ between an edit-save and a
// switch/rollback; commitPromptGovernanceVersion runs the shared transactional core.
type promptGovWrite struct {
	content         string
	changeNote      string
	source          string
	sourceVer       *int32
	scannerRevision string
	gateResult      []promptscan.Finding
	authorUserID    string
}

// commitPromptGovernanceVersion is the single write path every effective-content
// change funnels through: lock the owning entity row, assign the next
// version, insert the prompt_version row, and copy content into the
// business column — all inside one transaction, so a failure at any step
// leaves both the version line and the business column unchanged.
func (h *Handler) commitPromptGovernanceVersion(w http.ResponseWriter, r *http.Request, scope promptVersionScope, scopeID pgtype.UUID, pw promptGovWrite) {
	ctx := r.Context()
	workspaceID := parseUUID(h.resolveWorkspaceID(r))

	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save prompt version")
		return
	}
	defer tx.Rollback(ctx)
	qtx := h.Queries.WithTx(tx)

	currentContent, ok := h.lockScopeEntity(w, r, qtx, scope, workspaceID, scopeID)
	if !ok {
		return
	}

	// Empty-content guard (RUYI-213): a blank incoming content must never
	// erase live prompt text. The 922 v1 backfill snapshotted whatever the
	// business column held at the time, so a tier whose content was already
	// lost carries an empty v1 row — switching to it would copy that emptiness
	// straight back over content restored since. Rejecting with 409 leaves both
	// the version line and the business column untouched; writing blank over
	// blank stays allowed, since there is nothing to lose.
	if strings.TrimSpace(pw.content) == "" && strings.TrimSpace(currentContent) != "" {
		slog.Warn("prompt version write blocked: empty content would erase live text", append(logger.RequestAttrs(r),
			"scope", string(scope), "source", pw.source)...)
		writeError(w, http.StatusConflict, "refusing to write empty content over the tier's current non-empty content; edit the content or pick a non-empty version")
		return
	}

	next, err := qtx.NextPromptVersion(ctx, db.NextPromptVersionParams{Scope: string(scope), ScopeID: scopeID})
	if err != nil {
		slog.Error("prompt version number failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to save prompt version")
		return
	}

	gateBlob, err := json.Marshal(pw.gateResult)
	if err != nil || pw.gateResult == nil {
		gateBlob = []byte("[]")
	}

	var sourceVerNarg pgtype.Int4
	if pw.sourceVer != nil {
		sourceVerNarg = pgtype.Int4{Int32: *pw.sourceVer, Valid: true}
	}
	authorUUID := parseUUID(pw.authorUserID)

	created, err := qtx.CreatePromptVersion(ctx, db.CreatePromptVersionParams{
		WorkspaceID:     workspaceID,
		Scope:           string(scope),
		ScopeID:         scopeID,
		Version:         next,
		Content:         pw.content,
		ContentSha256:   sha256Hex(pw.content),
		Source:          pw.source,
		SourceVersion:   sourceVerNarg,
		ChangeNote:      pw.changeNote,
		ScannerRevision: pw.scannerRevision,
		GateResult:      gateBlob,
		AuthorUserID:    authorUUID,
	})
	if err != nil {
		slog.Error("prompt version insert failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to save prompt version")
		return
	}

	if err := h.writeScopeEffectiveContent(ctx, qtx, scope, scopeID, pw.content); err != nil {
		slog.Error("prompt effective content write failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to save prompt version")
		return
	}

	if err := tx.Commit(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save prompt version")
		return
	}

	writeJSON(w, http.StatusOK, promptGovVersionToResponse(created))
}
