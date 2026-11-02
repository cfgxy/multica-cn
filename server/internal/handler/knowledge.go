package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/util"
)

// bd memories unified collection (RUYI-265, spec §K; execution moved to the
// daemon in RUYI-289). Every read path here is a mirror: the source bd files
// are never written by this server. The single write path is the Owner
// adoption transfer into the workspace's ultimate knowledge directory, and
// even that only counts as adopted after a daemon — the process that actually
// hosts bd — reported the transfer complete (G1: 转移完成才算采纳). The server
// stays the only state authority: daemons pull a work plan, execute bd IO on
// the host, and report results back; every state transition happens here.

type KnowledgeDirResponse struct {
	ID            string                      `json:"id"`
	Kind          string                      `json:"kind"`
	Path          string                      `json:"path"`
	ProjectID     *string                     `json:"project_id,omitempty"`
	Label         string                      `json:"label"`
	HealthState   string                      `json:"health_state"`
	HealthNote    string                      `json:"health_note"`
	Removed       bool                        `json:"removed"`
	ScanRequested bool                        `json:"scan_requested"`
	ManagedActive bool                        `json:"managed_active"`
	EntryCount    int64                       `json:"entry_count"`
	LastScan      *KnowledgeScanBatchResponse `json:"last_scan,omitempty"`
	CreatedAt     time.Time                   `json:"created_at"`
	UpdatedAt     time.Time                   `json:"updated_at"`
}

type KnowledgeScanBatchResponse struct {
	ID            string     `json:"id"`
	DirID         string     `json:"dir_id"`
	TriggerSource string     `json:"trigger_source"`
	Result        string     `json:"result"`
	Added         int        `json:"added"`
	Updated       int        `json:"updated"`
	Removed       int        `json:"removed"`
	Error         string     `json:"error,omitempty"`
	StartedAt     time.Time  `json:"started_at"`
	FinishedAt    *time.Time `json:"finished_at,omitempty"`
}

type KnowledgeEntryResponse struct {
	ID                  string                           `json:"id"`
	DirID               string                           `json:"dir_id"`
	Key                 string                           `json:"key"`
	Content             string                           `json:"content"`
	ContentSha256       string                           `json:"content_sha256"`
	MirrorState         string                           `json:"mirror_state"`
	LifecycleState      string                           `json:"lifecycle_state"`
	CurrentVersion      int                              `json:"current_version"`
	MissingConfirmation int                              `json:"missing_confirmation"`
	ReplacementEntryID  *string                          `json:"replacement_entry_id,omitempty"`
	WritebackState      string                           `json:"writeback_state"`
	WritebackError      string                           `json:"writeback_error,omitempty"`
	FirstSeenAt         time.Time                        `json:"first_seen_at"`
	LastConfirmedAt     time.Time                        `json:"last_confirmed_at"`
	AdoptionState       string                           `json:"adoption_state"`
	AdoptedAt           *time.Time                       `json:"adopted_at,omitempty"`
	AdoptedBy           *string                          `json:"adopted_by,omitempty"`
	UltimateDirID       *string                          `json:"ultimate_dir_id,omitempty"`
	AdoptedFromDirID    *string                          `json:"adopted_from_dir_id,omitempty"`
	AdoptedFromKey      *string                          `json:"adopted_from_key,omitempty"`
	AdoptionError       string                           `json:"adoption_error,omitempty"`
	PendingOperation    *KnowledgeEntryOperationResponse `json:"pending_operation,omitempty"`
}

type KnowledgeEntryOperationResponse struct {
	ID            string     `json:"id"`
	OperationType string     `json:"operation_type,omitempty"`
	Status        string     `json:"status"`
	Error         string     `json:"error,omitempty"`
	RequestedAt   *time.Time `json:"requested_at,omitempty"`
}

type KnowledgeEntryVersionResponse struct {
	ID            string    `json:"id"`
	EntryID       string    `json:"entry_id"`
	Version       int       `json:"version"`
	Content       string    `json:"content"`
	ContentSha256 string    `json:"content_sha256"`
	Source        string    `json:"source"`
	CreatedAt     time.Time `json:"created_at"`
}

type KnowledgeLifecycleEventResponse struct {
	ID                 string     `json:"id"`
	EntryID            string     `json:"entry_id"`
	FromState          string     `json:"from_state"`
	ToState            string     `json:"to_state"`
	Operation          string     `json:"operation"`
	Reason             string     `json:"reason"`
	EvidenceRef        string     `json:"evidence_ref"`
	ReplacementEntryID *string    `json:"replacement_entry_id,omitempty"`
	WritebackState     string     `json:"writeback_state"`
	WritebackError     string     `json:"writeback_error,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	CompletedAt        *time.Time `json:"completed_at,omitempty"`
}

// GetKnowledgeDirs serves GET /api/knowledge/dirs: the workspace's registered
// directories with entry counts and the latest scan batch per directory.
func (h *Handler) GetKnowledgeDirs(w http.ResponseWriter, r *http.Request) {
	workspaceID := parseUUID(h.resolveWorkspaceID(r))
	rows, err := h.DB.Query(r.Context(), `
	SELECT d.id, d.kind, d.path, d.project_id, d.label, d.health_state, d.health_note,
	       d.removed, d.scan_requested, d.managed_active, d.created_at, d.updated_at,
       (SELECT count(*) FROM knowledge_entry e WHERE e.dir_id = d.id),
       lb.id, COALESCE(lb.trigger_source, ''), COALESCE(lb.result, ''), COALESCE(lb.added, 0), COALESCE(lb.updated, 0),
       COALESCE(lb.removed, 0), COALESCE(lb.error, ''), lb.started_at, lb.finished_at
FROM knowledge_dir d
LEFT JOIN LATERAL (
    SELECT * FROM knowledge_scan_batch b
    WHERE b.dir_id = d.id ORDER BY b.started_at DESC LIMIT 1
) lb ON TRUE
WHERE d.workspace_id = $1
ORDER BY d.kind = 'ultimate' DESC, d.created_at`, workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read knowledge directories")
		return
	}
	defer rows.Close()
	dirs := make([]KnowledgeDirResponse, 0, 8)
	for rows.Next() {
		var dir KnowledgeDirResponse
		var id, projectID pgtype.UUID
		var batchID pgtype.UUID
		var trigger, result string
		var added, updatedCnt, removed int
		var batchErr string
		var startedAt pgtype.Timestamptz
		var finishedAt pgtype.Timestamptz
		if err := rows.Scan(&id, &dir.Kind, &dir.Path, &projectID, &dir.Label,
			&dir.HealthState, &dir.HealthNote, &dir.Removed, &dir.ScanRequested, &dir.ManagedActive,
			&dir.CreatedAt, &dir.UpdatedAt,
			&dir.EntryCount, &batchID, &trigger, &result, &added, &updatedCnt, &removed,
			&batchErr, &startedAt, &finishedAt); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to read knowledge directories")
			return
		}
		dir.ID = uuidToString(id)
		if projectID.Valid {
			value := uuidToString(projectID)
			dir.ProjectID = &value
		}
		if batchID.Valid {
			finished := timestampTime(finishedAt)
			dir.LastScan = &KnowledgeScanBatchResponse{
				ID: uuidToString(batchID), DirID: dir.ID, TriggerSource: trigger,
				Result: result, Added: added, Updated: updatedCnt, Removed: removed,
				Error: batchErr, StartedAt: timestampTime(startedAt), FinishedAt: &finished,
			}
		}
		dirs = append(dirs, dir)
	}
	if rows.Err() != nil {
		writeError(w, http.StatusInternalServerError, "failed to read knowledge directories")
		return
	}
	writeJSON(w, http.StatusOK, dirs)
}

// PostKnowledgeDir serves POST /api/knowledge/dirs: register a manually added
// external source. Kind is normally a server decision (candidate_cli). A
// management-channel ultimate is accepted only when its exact path has a
// registered daemon host, so it can eventually receive adoption work.
func (h *Handler) PostKnowledgeDir(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := parseUUID(h.resolveWorkspaceID(r))
	var body struct {
		Kind      string `json:"kind"`
		Path      string `json:"path"`
		ProjectID string `json:"project_id"`
		Label     string `json:"label"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Path == "" {
		writeError(w, http.StatusBadRequest, "path is required")
		return
	}
	if body.ProjectID != "" {
		if _, ok := parseUUIDOrBadRequest(w, body.ProjectID, "project_id"); !ok {
			return
		}
	}
	// UI forms no longer send a kind; an explicit ultimate designation stays
	// available to the management channel for backward compatibility.
	if body.Kind == "" {
		body.Kind = "candidate_cli"
	}
	if body.Kind != "ultimate" && body.Kind != "candidate_cli" && body.Kind != "candidate_auto" {
		writeError(w, http.StatusBadRequest, "kind must be ultimate, candidate_cli or candidate_auto")
		return
	}
	if body.Kind == "ultimate" {
		var hasDaemonHost bool
		err := h.DB.QueryRow(r.Context(), `
	SELECT EXISTS(
		SELECT 1 FROM project_resource
		WHERE workspace_id = $1
		  AND resource_type = 'local_directory'
		  AND resource_ref->>'local_path' = $2
		  AND COALESCE(resource_ref->>'daemon_id', '') <> ''
	)`, workspaceID, body.Path).Scan(&hasDaemonHost)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to validate the ultimate knowledge directory host")
			return
		}
		if !hasDaemonHost {
			writeError(w, http.StatusConflict, "an ultimate knowledge directory must match a registered daemon local directory")
			return
		}
	}

	// One active ultimate per workspace and one row per live path are
	// database-enforced (migrations 951/952); the insert below surfaces the
	// conflict either way.
	var id pgtype.UUID
	err := h.DB.QueryRow(r.Context(), `
INSERT INTO knowledge_dir (workspace_id, kind, path, project_id, label, created_by, scan_requested)
VALUES ($1, $2, $3, NULLIF($4, '')::uuid, $5, NULLIF($6, '')::uuid, TRUE)
RETURNING id`, workspaceID, body.Kind, body.Path, body.ProjectID, body.Label, userID).Scan(&id)
	if err != nil {
		if isPgUniqueViolation(err) {
			writeError(w, http.StatusConflict, "this path or ultimate designation is already registered")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to register the knowledge directory")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{
		"id": uuidToString(id), "status": "queued for first scan",
	})
}

// ScanKnowledgeDir serves POST /api/knowledge/dirs/{id}/scan: queue a manual
// read-only rescan. The hosting daemon picks the flag up on its next knowledge
// cycle and records the batch; the queued flag is visible in the dir response
// so the UI can show the request is pending instead of pretending it ran.
func (h *Handler) ScanKnowledgeDir(w http.ResponseWriter, r *http.Request) {
	workspaceID := parseUUID(h.resolveWorkspaceID(r))
	dirID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	tag, err := h.DB.Exec(r.Context(), `
UPDATE knowledge_dir SET scan_requested = TRUE, updated_at = now()
WHERE id = $1 AND workspace_id = $2 AND removed = FALSE`, dirID, workspaceID)
	if err != nil || tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "knowledge directory not found")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "queued"})
}

// DeleteKnowledgeDir serves DELETE /api/knowledge/dirs/{id}: unregister a
// CLI-added directory. Auto-discovered directories follow their project
// registration instead. Mirror entries are kept, flagged source_removed —
// history stays retrievable; nothing is physically deleted.
func (h *Handler) DeleteKnowledgeDir(w http.ResponseWriter, r *http.Request) {
	workspaceID := parseUUID(h.resolveWorkspaceID(r))
	dirID := parseUUID(chi.URLParam(r, "id"))
	tag, err := h.DB.Exec(r.Context(), `
UPDATE knowledge_dir SET removed = TRUE, updated_at = now()
WHERE id = $1 AND workspace_id = $2 AND kind = 'candidate_cli' AND removed = FALSE`,
		dirID, workspaceID)
	if err != nil || tag.RowsAffected() == 0 {
		writeError(w, http.StatusConflict, "only CLI-added directories can be unregistered here")
		return
	}
	if _, err := h.DB.Exec(r.Context(),
		`UPDATE knowledge_entry SET mirror_state = 'source_removed' WHERE dir_id = $1`, dirID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to retire the mirrored entries")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "unregistered"})
}

// GetKnowledgeEntries serves GET /api/knowledge/entries: the mirror with
// substring search (exact substring, case-insensitive — no semantics, no
// ranking) ordered by last confirmation, newest first.
func (h *Handler) GetKnowledgeEntries(w http.ResponseWriter, r *http.Request) {
	workspaceID := parseUUID(h.resolveWorkspaceID(r))
	var dirID pgtype.UUID
	if raw := r.URL.Query().Get("dir_id"); raw != "" {
		parsed, ok := parseUUIDOrBadRequest(w, raw, "dir_id")
		if !ok {
			return
		}
		dirID = parsed
	}
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	lifecycle := r.URL.Query().Get("lifecycle")
	if lifecycle == "" {
		lifecycle = "active"
	}
	if lifecycle != "active" && lifecycle != "archived" && lifecycle != "all" {
		writeError(w, http.StatusBadRequest, "lifecycle must be active, archived or all")
		return
	}
	rows, err := h.DB.Query(r.Context(), `
SELECT e.id, e.dir_id, e.key, e.content, e.content_sha256, e.mirror_state,
	       e.lifecycle_state, e.current_version, e.missing_confirmation, e.replacement_entry_id,
	       e.writeback_state, e.writeback_error,
       e.first_seen_at, e.last_confirmed_at, e.adoption_state, e.adopted_at,
       e.adopted_by, e.ultimate_dir_id, e.adopted_from_dir_id, e.adopted_from_key,
       e.adoption_error, pending.id, COALESCE(pending.operation_type, ''),
       COALESCE(pending.status, ''), COALESCE(pending.error, ''), pending.created_at
FROM knowledge_entry e
	LEFT JOIN LATERAL (
	    SELECT o.id, o.operation_type, o.status, o.error, o.created_at
	    FROM knowledge_entry_operation o
	    WHERE o.workspace_id = e.workspace_id AND o.status = 'queued'
	      AND (o.entry_id = e.id OR o.delete_entry_ids ? e.id::text)
	    ORDER BY o.created_at DESC, o.id DESC
	    LIMIT 1
	) pending ON TRUE
WHERE e.workspace_id = $1
  AND ($2::uuid IS NULL OR e.dir_id = $2)
  AND ($3 = '' OR position($3 in lower(e.key)) > 0 OR position($3 in lower(e.content)) > 0)
	  AND ($4 = 'all' OR ($4 = 'active' AND e.lifecycle_state = 'active')
	       OR ($4 = 'archived' AND e.lifecycle_state <> 'active'))
ORDER BY e.last_confirmed_at DESC
	LIMIT 200`, workspaceID, dirID, query, lifecycle)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read knowledge entries")
		return
	}
	defer rows.Close()
	entries := make([]KnowledgeEntryResponse, 0, 64)
	for rows.Next() {
		var entry KnowledgeEntryResponse
		var id, entryDirID, replacementEntryID, adoptedBy, ultimateDirID, fromDirID, pendingID pgtype.UUID
		var adoptedAt, pendingRequestedAt pgtype.Timestamptz
		var pendingOperationType, pendingStatus, pendingError string
		if err := rows.Scan(&id, &entryDirID, &entry.Key, &entry.Content, &entry.ContentSha256,
			&entry.MirrorState, &entry.LifecycleState, &entry.CurrentVersion, &entry.MissingConfirmation,
			&replacementEntryID, &entry.WritebackState, &entry.WritebackError,
			&entry.FirstSeenAt, &entry.LastConfirmedAt, &entry.AdoptionState,
			&adoptedAt, &adoptedBy, &ultimateDirID, &fromDirID, &entry.AdoptedFromKey,
			&entry.AdoptionError, &pendingID, &pendingOperationType, &pendingStatus, &pendingError,
			&pendingRequestedAt); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to read knowledge entries")
			return
		}
		entry.ID = uuidToString(id)
		entry.DirID = uuidToString(entryDirID)
		if replacementEntryID.Valid {
			value := uuidToString(replacementEntryID)
			entry.ReplacementEntryID = &value
		}
		if adoptedAt.Valid {
			value := adoptedAt.Time
			entry.AdoptedAt = &value
		}
		if adoptedBy.Valid {
			value := uuidToString(adoptedBy)
			entry.AdoptedBy = &value
		}
		if ultimateDirID.Valid {
			value := uuidToString(ultimateDirID)
			entry.UltimateDirID = &value
		}
		if fromDirID.Valid {
			value := uuidToString(fromDirID)
			entry.AdoptedFromDirID = &value
		}
		if pendingID.Valid {
			pending := &KnowledgeEntryOperationResponse{
				ID: uuidToString(pendingID), OperationType: pendingOperationType,
				Status: pendingStatus, Error: pendingError,
			}
			if pendingRequestedAt.Valid {
				value := pendingRequestedAt.Time
				pending.RequestedAt = &value
			}
			entry.PendingOperation = pending
		}
		entries = append(entries, entry)
	}
	if rows.Err() != nil {
		writeError(w, http.StatusInternalServerError, "failed to read knowledge entries")
		return
	}
	writeJSON(w, http.StatusOK, entries)
}

func (h *Handler) GetKnowledgeEntryVersions(w http.ResponseWriter, r *http.Request) {
	workspaceID := parseUUID(h.resolveWorkspaceID(r))
	entryID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	rows, err := h.DB.Query(r.Context(), `
SELECT id, entry_id, version, content, content_sha256, source, created_at
FROM knowledge_entry_version
WHERE workspace_id = $1 AND entry_id = $2
ORDER BY version DESC`, workspaceID, entryID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read knowledge entry versions")
		return
	}
	defer rows.Close()
	versions := make([]KnowledgeEntryVersionResponse, 0, 4)
	for rows.Next() {
		var version KnowledgeEntryVersionResponse
		var id, rowEntryID pgtype.UUID
		if err := rows.Scan(&id, &rowEntryID, &version.Version, &version.Content, &version.ContentSha256, &version.Source, &version.CreatedAt); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to read knowledge entry versions")
			return
		}
		version.ID = uuidToString(id)
		version.EntryID = uuidToString(rowEntryID)
		versions = append(versions, version)
	}
	writeJSON(w, http.StatusOK, versions)
}

func (h *Handler) GetKnowledgeEntryLifecycle(w http.ResponseWriter, r *http.Request) {
	workspaceID := parseUUID(h.resolveWorkspaceID(r))
	entryID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	rows, err := h.DB.Query(r.Context(), `
SELECT id, entry_id, from_state, to_state, operation, reason, evidence_ref,
       replacement_entry_id, writeback_state, writeback_error, created_at, completed_at
FROM knowledge_lifecycle_event
WHERE workspace_id = $1 AND entry_id = $2
ORDER BY created_at DESC`, workspaceID, entryID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read knowledge lifecycle")
		return
	}
	defer rows.Close()
	events := make([]KnowledgeLifecycleEventResponse, 0, 4)
	for rows.Next() {
		var event KnowledgeLifecycleEventResponse
		var id, rowEntryID, replacement pgtype.UUID
		var completed pgtype.Timestamptz
		if err := rows.Scan(&id, &rowEntryID, &event.FromState, &event.ToState, &event.Operation,
			&event.Reason, &event.EvidenceRef, &replacement, &event.WritebackState,
			&event.WritebackError, &event.CreatedAt, &completed); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to read knowledge lifecycle")
			return
		}
		event.ID = uuidToString(id)
		event.EntryID = uuidToString(rowEntryID)
		if replacement.Valid {
			value := uuidToString(replacement)
			event.ReplacementEntryID = &value
		}
		if completed.Valid {
			value := completed.Time
			event.CompletedAt = &value
		}
		events = append(events, event)
	}
	writeJSON(w, http.StatusOK, events)
}

func (h *Handler) PutKnowledgeDirManaged(w http.ResponseWriter, r *http.Request) {
	workspaceID := parseUUID(h.resolveWorkspaceID(r))
	dirID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	var body struct {
		ManagedActive bool `json:"managed_active"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid managed state")
		return
	}
	query := `
UPDATE knowledge_dir SET managed_active = $3, updated_at = now()
WHERE id = $1 AND workspace_id = $2 AND removed = FALSE`
	if body.ManagedActive {
		query += ` AND kind = 'candidate_cli' AND project_id IS NULL`
	}
	tag, err := h.DB.Exec(r.Context(), query, dirID, workspaceID, body.ManagedActive)
	if err != nil || tag.RowsAffected() == 0 {
		writeError(w, http.StatusConflict, "only active local candidate knowledge directories can enable managed write-back")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"managed_active": body.ManagedActive})
}

func validKnowledgeLifecycleState(state string) bool {
	switch state {
	case "needs_review", "superseded", "deprecated", "invalid", "duplicate":
		return true
	default:
		return false
	}
}

type knowledgeEvidenceKind string

const (
	knowledgeEvidenceIssue           knowledgeEvidenceKind = "issue"
	knowledgeEvidencePullRequest     knowledgeEvidenceKind = "pr"
	knowledgeEvidenceCodePullRequest knowledgeEvidenceKind = "code_pr"
	knowledgeEvidenceRun             knowledgeEvidenceKind = "run"
	knowledgeEvidenceScanBatch       knowledgeEvidenceKind = "knowledge_scan_batch"
)

type knowledgeEvidenceRef struct {
	kind      knowledgeEvidenceKind
	id        pgtype.UUID
	sha       string
	canonical string
}

func validKnowledgeEvidenceRef(ref string) bool {
	_, ok := parseKnowledgeEvidenceRef(ref)
	return ok
}

// parseKnowledgeEvidenceRef accepts only evidence records the server can
// resolve within a workspace. The canonical result is safe to persist in the
// lifecycle audit log after the matching database row is verified.
func parseKnowledgeEvidenceRef(ref string) (knowledgeEvidenceRef, bool) {
	ref = strings.TrimSpace(ref)
	if strings.ContainsAny(ref, " \t\r\n") {
		return knowledgeEvidenceRef{}, false
	}
	parseID := func(kind knowledgeEvidenceKind, raw string) (knowledgeEvidenceRef, bool) {
		id, err := util.ParseUUID(raw)
		if err != nil {
			return knowledgeEvidenceRef{}, false
		}
		canonicalID := uuidToString(id)
		return knowledgeEvidenceRef{kind: kind, id: id, canonical: string(kind) + ":" + canonicalID}, true
	}

	switch {
	case strings.HasPrefix(ref, "issue:"):
		return parseID(knowledgeEvidenceIssue, strings.TrimPrefix(ref, "issue:"))
	case strings.HasPrefix(ref, "pr:"):
		return parseID(knowledgeEvidencePullRequest, strings.TrimPrefix(ref, "pr:"))
	case strings.HasPrefix(ref, "run:"):
		return parseID(knowledgeEvidenceRun, strings.TrimPrefix(ref, "run:"))
	case strings.HasPrefix(ref, "knowledge_scan_batch:"):
		return parseID(knowledgeEvidenceScanBatch, strings.TrimPrefix(ref, "knowledge_scan_batch:"))
	case strings.HasPrefix(ref, "code:pr:"):
		rawPR, rawSHA, ok := strings.Cut(strings.TrimPrefix(ref, "code:pr:"), "@")
		if !ok || !validGitObjectID(rawSHA) {
			return knowledgeEvidenceRef{}, false
		}
		parsed, ok := parseID(knowledgeEvidenceCodePullRequest, rawPR)
		if !ok {
			return knowledgeEvidenceRef{}, false
		}
		parsed.sha = strings.ToLower(rawSHA)
		parsed.canonical = "code:pr:" + uuidToString(parsed.id) + "@" + parsed.sha
		return parsed, true
	default:
		return knowledgeEvidenceRef{}, false
	}
}

func validGitObjectID(sha string) bool {
	if len(sha) != 40 && len(sha) != 64 {
		return false
	}
	for _, c := range sha {
		if !(c >= '0' && c <= '9') && !(c >= 'a' && c <= 'f') && !(c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

// verifyKnowledgeEvidenceRef proves the reference is available to the current
// workspace before its lifecycle event is written. Scan batches are additionally
// bound to the entry's directory so unrelated knowledge scopes cannot justify a
// disposition.
func verifyKnowledgeEvidenceRef(ctx context.Context, tx pgx.Tx, workspaceID, dirID pgtype.UUID, ref knowledgeEvidenceRef) error {
	var found int
	var err error
	switch ref.kind {
	case knowledgeEvidenceIssue:
		err = tx.QueryRow(ctx, `SELECT 1 FROM issue WHERE id = $1 AND workspace_id = $2`, ref.id, workspaceID).Scan(&found)
	case knowledgeEvidencePullRequest:
		err = tx.QueryRow(ctx, `SELECT 1 FROM github_pull_request WHERE id = $1 AND workspace_id = $2`, ref.id, workspaceID).Scan(&found)
	case knowledgeEvidenceCodePullRequest:
		err = tx.QueryRow(ctx, `
			SELECT 1 FROM github_pull_request
			WHERE id = $1 AND workspace_id = $2 AND lower(head_sha) = $3`, ref.id, workspaceID, ref.sha).Scan(&found)
	case knowledgeEvidenceRun:
		err = tx.QueryRow(ctx, `
			SELECT 1 FROM agent_task_queue q
			JOIN agent a ON a.id = q.agent_id
			WHERE q.id = $1 AND a.workspace_id = $2`, ref.id, workspaceID).Scan(&found)
	case knowledgeEvidenceScanBatch:
		err = tx.QueryRow(ctx, `
			SELECT 1 FROM knowledge_scan_batch
			WHERE id = $1 AND workspace_id = $2 AND dir_id = $3`, ref.id, workspaceID, dirID).Scan(&found)
	default:
		return errors.New("unsupported knowledge evidence reference")
	}
	return err
}

func (h *Handler) PostKnowledgeEntryLifecycle(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := parseUUID(h.resolveWorkspaceID(r))
	entryID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	var body struct {
		State       string `json:"state"`
		Reason      string `json:"reason"`
		EvidenceRef string `json:"evidence_ref"`
		Writeback   bool   `json:"writeback"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "state, reason, and a locatable evidence_ref are required")
		return
	}
	evidence, evidenceOK := parseKnowledgeEvidenceRef(body.EvidenceRef)
	if !validKnowledgeLifecycleState(body.State) || strings.TrimSpace(body.Reason) == "" || !evidenceOK {
		writeError(w, http.StatusBadRequest, "state, reason, and a locatable evidence_ref are required")
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start knowledge lifecycle transaction")
		return
	}
	defer tx.Rollback(r.Context())
	var previous, contentSHA, dirKind string
	var dirID, currentVersionID, projectID pgtype.UUID
	var managedActive, removed bool
	if err := tx.QueryRow(r.Context(), `
SELECT e.lifecycle_state, e.content_sha256, e.dir_id, v.id, d.kind, d.project_id, d.managed_active, d.removed
FROM knowledge_entry e
JOIN knowledge_entry_version v ON v.entry_id = e.id AND v.workspace_id = e.workspace_id AND v.version = e.current_version
JOIN knowledge_dir d ON d.id = e.dir_id
WHERE e.id = $1 AND e.workspace_id = $2
FOR UPDATE OF e, d`, entryID, workspaceID).Scan(&previous, &contentSHA, &dirID, &currentVersionID, &dirKind, &projectID, &managedActive, &removed); err != nil {
		writeError(w, http.StatusNotFound, "knowledge entry not found")
		return
	}
	if err := verifyKnowledgeEvidenceRef(r.Context(), tx, workspaceID, dirID, evidence); err != nil {
		writeError(w, http.StatusBadRequest, "evidence_ref is not available in this knowledge scope")
		return
	}
	if _, err := tx.Exec(r.Context(), `
	UPDATE knowledge_lifecycle_event
	SET writeback_state = 'cancelled', writeback_error = 'superseded by a later Owner lifecycle decision', completed_at = now()
	WHERE entry_id = $1 AND writeback_state = 'queued'`, entryID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to supersede pending knowledge write-back")
		return
	}
	operation := "transition"
	writebackState := "not_requested"
	expectedContentSHA := ""
	versionID := pgtype.UUID{}
	if body.Writeback {
		if body.State == "needs_review" || dirKind != "candidate_cli" || projectID.Valid || !managedActive || removed {
			writeError(w, http.StatusConflict, "write-back requires an active managed local candidate directory and a final lifecycle state")
			return
		}
		operation = "writeback_remove"
		writebackState = "queued"
		expectedContentSHA = contentSHA
		versionID = currentVersionID
	}
	if _, err := tx.Exec(r.Context(), `
UPDATE knowledge_entry SET lifecycle_state = $3, replacement_entry_id = NULL,
    writeback_state = $4, writeback_error = '' WHERE id = $1 AND workspace_id = $2`,
		entryID, workspaceID, body.State, writebackState); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update knowledge lifecycle")
		return
	}
	_, err = tx.Exec(r.Context(), `
INSERT INTO knowledge_lifecycle_event (workspace_id, entry_id, from_state, to_state, operation, reason, evidence_ref, replacement_entry_id, version_id, expected_content_sha256, writeback_state, actor_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NULLIF($9, '')::uuid, $10, $11, NULLIF($12, '')::uuid)`,
		workspaceID, entryID, previous, body.State, operation, strings.TrimSpace(body.Reason), evidence.canonical, pgtype.UUID{}, versionID, expectedContentSHA, writebackState, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to record knowledge lifecycle")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit knowledge lifecycle update")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": writebackState})
}

func (h *Handler) PostKnowledgeEntryRestore(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := parseUUID(h.resolveWorkspaceID(r))
	entryID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	var body struct {
		VersionID   string `json:"version_id"`
		Reason      string `json:"reason"`
		EvidenceRef string `json:"evidence_ref"`
		Writeback   bool   `json:"writeback"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "version_id, reason, and a locatable evidence_ref are required")
		return
	}
	evidence, evidenceOK := parseKnowledgeEvidenceRef(body.EvidenceRef)
	if body.VersionID == "" || strings.TrimSpace(body.Reason) == "" || !evidenceOK {
		writeError(w, http.StatusBadRequest, "version_id, reason, and a locatable evidence_ref are required")
		return
	}
	versionID, ok := parseUUIDOrBadRequest(w, body.VersionID, "version_id")
	if !ok {
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start knowledge restoration transaction")
		return
	}
	defer tx.Rollback(r.Context())
	var previous, currentContentSHA, dirKind string
	var dirID, projectID pgtype.UUID
	var managedActive, removed bool
	var version, currentVersion int
	err = tx.QueryRow(r.Context(), `
			SELECT v.version, e.lifecycle_state, e.current_version, e.dir_id, e.content_sha256,
			       d.kind, d.project_id, d.managed_active, d.removed
FROM knowledge_entry_version v JOIN knowledge_entry e ON e.id = v.entry_id
JOIN knowledge_dir d ON d.id = e.dir_id
WHERE v.id = $1 AND v.entry_id = $2 AND v.workspace_id = $3 FOR UPDATE OF e, d`, versionID, entryID, workspaceID).
		Scan(&version, &previous, &currentVersion, &dirID, &currentContentSHA, &dirKind, &projectID, &managedActive, &removed)
	if err != nil {
		writeError(w, http.StatusNotFound, "knowledge entry version not found")
		return
	}
	if err := verifyKnowledgeEvidenceRef(r.Context(), tx, workspaceID, dirID, evidence); err != nil {
		writeError(w, http.StatusBadRequest, "evidence_ref is not available in this knowledge scope")
		return
	}
	if _, err := tx.Exec(r.Context(), `
	UPDATE knowledge_lifecycle_event
	SET writeback_state = 'cancelled', writeback_error = 'superseded by a later Owner restoration decision', completed_at = now()
	WHERE entry_id = $1 AND writeback_state = 'queued'`, entryID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to supersede pending knowledge write-back")
		return
	}
	operation := "restore_candidate"
	toState := "needs_review"
	writebackState := "not_requested"
	expectedContentSHA := ""
	if body.Writeback {
		if dirKind != "candidate_cli" || projectID.Valid || !managedActive || removed {
			writeError(w, http.StatusConflict, "write-back requires an active managed local candidate directory")
			return
		}
		operation = "writeback_restore"
		toState = "active"
		writebackState = "queued"
		expectedContentSHA = currentContentSHA
	}
	if _, err := tx.Exec(r.Context(), `
UPDATE knowledge_entry SET lifecycle_state = $3, replacement_entry_id = NULL,
    writeback_state = $4, writeback_error = ''
WHERE id = $1 AND workspace_id = $2`, entryID, workspaceID, toState, writebackState); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to record knowledge restoration candidate")
		return
	}
	_, err = tx.Exec(r.Context(), `
	INSERT INTO knowledge_lifecycle_event (workspace_id, entry_id, from_state, to_state, operation, reason, evidence_ref, version_id, expected_content_sha256, writeback_state, actor_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NULLIF($11, '')::uuid)`,
		workspaceID, entryID, previous, toState, operation, strings.TrimSpace(body.Reason), evidence.canonical, versionID, expectedContentSHA, writebackState, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to record knowledge restoration candidate")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit knowledge restoration")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"status": writebackState, "version": currentVersion, "restored_from_version": version})
}

// AdoptKnowledgeEntry serves POST /api/knowledge/entries/{id}/adopt: the
// single write action of the whole knowledge flow, Owner-only. The transfer
// itself runs on the daemon hosting the ultimate directory: this handler only
// queues it (adoption_state='transferring'), and the entry becomes 'adopted'
// only when the daemon reported remember + read-back success (G1). Failures
// land back on 'failed' with the daemon's reason, and re-queuing is the
// retry — bd remember on an existing key updates in place, so retries are
// idempotent even after a half-executed transfer.
func (h *Handler) AdoptKnowledgeEntry(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := parseUUID(h.resolveWorkspaceID(r))
	entryID, entryOk := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "id")
	if !entryOk {
		return
	}
	var adoptionState string
	var id pgtype.UUID
	if err := h.DB.QueryRow(r.Context(), `
SELECT id, adoption_state FROM knowledge_entry WHERE id = $1 AND workspace_id = $2`,
		entryID, workspaceID).Scan(&id, &adoptionState); err != nil {
		writeError(w, http.StatusNotFound, "knowledge entry not found")
		return
	}
	switch adoptionState {
	case "adopted":
		writeError(w, http.StatusConflict, "this entry is already adopted")
		return
	case "transferring":
		writeError(w, http.StatusConflict, "a transfer for this entry is already in progress")
		return
	}
	if _, _, err := h.knowledgeUltimateDir(r.Context(), workspaceID); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	tag, err := h.DB.Exec(r.Context(), `
UPDATE knowledge_entry
SET adoption_state = 'transferring', adopted_by = NULLIF($3, '')::uuid, adoption_error = ''
WHERE id = $1 AND workspace_id = $2 AND adoption_state IN ('pending', 'failed')`,
		entryID, workspaceID, userID)
	if err != nil || tag.RowsAffected() == 0 {
		writeError(w, http.StatusConflict, "this entry cannot be adopted right now")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "transferring"})
}

// EditKnowledgeEntry queues a replacement for an ultimate-library entry. The
// daemon owns bd IO and the mirror is deliberately left unchanged until it
// reports a successful recall of the requested Markdown.
func (h *Handler) EditKnowledgeEntry(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	var body struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Content) == "" {
		writeError(w, http.StatusBadRequest, "content is required")
		return
	}
	h.queueKnowledgeEntryOperation(w, r, userID, "edit", body.Content, nil)
}

// DeleteKnowledgeEntry queues a confirmed deletion. Confirmation is part of
// the request contract as well as the UI dialog, so an API caller cannot
// accidentally delete a memory by merely following the endpoint.
func (h *Handler) DeleteKnowledgeEntry(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	var body struct {
		Confirm bool `json:"confirm"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || !body.Confirm {
		writeError(w, http.StatusBadRequest, "delete confirmation is required")
		return
	}
	h.queueKnowledgeEntryOperation(w, r, userID, "delete", "", nil)
}

// MergeKnowledgeEntries queues a user-confirmed consolidation. The target and
// every deleted source must be live entries in the same ultimate directory;
// source deletion happens only after the daemon read-back proves the target.
func (h *Handler) MergeKnowledgeEntries(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	var body struct {
		Content        string   `json:"content"`
		SourceEntryIDs []string `json:"source_entry_ids"`
		Confirm        bool     `json:"confirm"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || !body.Confirm || strings.TrimSpace(body.Content) == "" || len(body.SourceEntryIDs) == 0 {
		writeError(w, http.StatusBadRequest, "content, source_entry_ids and confirmation are required")
		return
	}
	workspaceID := parseUUID(h.resolveWorkspaceID(r))
	targetID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	var targetDirID pgtype.UUID
	if err := h.DB.QueryRow(r.Context(), `
SELECT dir_id FROM knowledge_entry WHERE id = $1 AND workspace_id = $2`, targetID, workspaceID).Scan(&targetDirID); err != nil {
		writeError(w, http.StatusNotFound, "knowledge entry not found")
		return
	}
	seen := make(map[string]struct{}, len(body.SourceEntryIDs))
	sourceIDs := make([]pgtype.UUID, 0, len(body.SourceEntryIDs))
	for _, rawID := range body.SourceEntryIDs {
		id, valid := parseUUIDOrBadRequest(w, rawID, "source_entry_ids")
		if !valid {
			return
		}
		if id == targetID {
			writeError(w, http.StatusBadRequest, "a merge source cannot be the target entry")
			return
		}
		if _, duplicate := seen[rawID]; duplicate {
			writeError(w, http.StatusBadRequest, "merge sources must be unique")
			return
		}
		seen[rawID] = struct{}{}
		sourceIDs = append(sourceIDs, id)
	}
	rows, err := h.DB.Query(r.Context(), `
SELECT id, dir_id FROM knowledge_entry
WHERE workspace_id = $1 AND id = ANY($2) AND mirror_state = 'synced'`, workspaceID, sourceIDs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to validate merge sources")
		return
	}
	defer rows.Close()
	found := 0
	for rows.Next() {
		var sourceID, sourceDirID pgtype.UUID
		if err := rows.Scan(&sourceID, &sourceDirID); err != nil || sourceDirID != targetDirID {
			writeError(w, http.StatusConflict, "merge sources must be live entries in the target directory")
			return
		}
		found++
	}
	if rows.Err() != nil {
		writeError(w, http.StatusInternalServerError, "failed to validate merge sources")
		return
	}
	if found != len(sourceIDs) {
		writeError(w, http.StatusConflict, "one or more merge sources are unavailable")
		return
	}
	h.queueKnowledgeEntryOperation(w, r, userID, "merge", body.Content, body.SourceEntryIDs)
}

func (h *Handler) queueKnowledgeEntryOperation(w http.ResponseWriter, r *http.Request, userID, operationType, content string, deleteEntryIDs []string) {
	workspaceID := parseUUID(h.resolveWorkspaceID(r))
	entryID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	var kind, daemonID, mirrorState string
	if err := h.DB.QueryRow(r.Context(), `
SELECT d.kind, d.daemon_id, e.mirror_state
FROM knowledge_entry e
JOIN knowledge_dir d ON d.id = e.dir_id
WHERE e.id = $1 AND e.workspace_id = $2 AND d.workspace_id = $2 AND d.removed = FALSE`, entryID, workspaceID).
		Scan(&kind, &daemonID, &mirrorState); err != nil {
		writeError(w, http.StatusNotFound, "knowledge entry not found")
		return
	}
	if kind != "ultimate" || daemonID == "" {
		writeError(w, http.StatusConflict, "only entries in a bound knowledge hub can be changed")
		return
	}
	if mirrorState != "synced" {
		writeError(w, http.StatusConflict, "a deleted or removed source cannot be changed")
		return
	}
	deleteJSON, err := json.Marshal(deleteEntryIDs)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid merge sources")
		return
	}
	involvedEntryIDs := append([]string{uuidToString(entryID)}, deleteEntryIDs...)
	var alreadyQueued bool
	if err := h.DB.QueryRow(r.Context(), `
SELECT EXISTS(
    SELECT 1 FROM knowledge_entry_operation
    WHERE workspace_id = $1 AND status = 'queued'
      AND (entry_id::text = ANY($2::text[]) OR delete_entry_ids ?| $2::text[])
)`, workspaceID, involvedEntryIDs).Scan(&alreadyQueued); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to queue knowledge operation")
		return
	}
	if alreadyQueued {
		writeError(w, http.StatusConflict, "a knowledge operation for this entry is already queued")
		return
	}
	var operationID pgtype.UUID
	if err := h.DB.QueryRow(r.Context(), `
INSERT INTO knowledge_entry_operation (workspace_id, entry_id, operation_type, content, delete_entry_ids, requested_by)
VALUES ($1, $2, $3, $4, $5::jsonb, NULLIF($6, '')::uuid)
RETURNING id`, workspaceID, entryID, operationType, content, string(deleteJSON), userID).Scan(&operationID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to queue knowledge operation")
		return
	}
	writeJSON(w, http.StatusAccepted, KnowledgeEntryOperationResponse{ID: uuidToString(operationID), Status: "queued"})
}

func isPgUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	return false
}

// knowledgeAdoptError marks adoption/scan precondition failures that map to
// an HTTP 409 with the reason shown to the Owner, as opposed to internal
// 500s.
type knowledgeAdoptError struct {
	reason string
}

func (e *knowledgeAdoptError) Error() string { return e.reason }

// knowledgeUltimateDir resolves the workspace's active ultimate directory only
// after a daemon has claimed it. Queuing a transfer without this binding would
// leave it in transferring with no authenticated consumer.
func (h *Handler) knowledgeUltimateDir(ctx context.Context, workspaceID pgtype.UUID) (pgtype.UUID, string, error) {
	var id pgtype.UUID
	var path string
	err := h.DB.QueryRow(ctx, `
SELECT id, path FROM knowledge_dir
	WHERE workspace_id = $1 AND kind = 'ultimate' AND removed = FALSE AND daemon_id <> ''
ORDER BY created_at LIMIT 1`, workspaceID).Scan(&id, &path)
	if err != nil {
		return id, "", &knowledgeAdoptError{"no bound ultimate knowledge directory is designated for this workspace"}
	}
	return id, path, nil
}

func (h *Handler) applyKnowledgeScanResult(ctx context.Context, workspaceID, dirID pgtype.UUID,
	path, trigger string, memories map[string]string, started, finished time.Time) (*KnowledgeScanBatchResponse, error) {
	batch := &KnowledgeScanBatchResponse{
		DirID: uuidToString(dirID), TriggerSource: trigger, StartedAt: started,
	}
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return h.recordFailedScanBatch(ctx, workspaceID, dirID, trigger, started, finished, err.Error())
	}
	defer tx.Rollback(ctx)
	fail := func(err error) (*KnowledgeScanBatchResponse, error) {
		_ = tx.Rollback(ctx)
		return h.recordFailedScanBatch(ctx, workspaceID, dirID, trigger, started, finished, err.Error())
	}

	// Serialize diff application for a directory so concurrent daemon reports
	// cannot each treat the same absence as a distinct confirmation.
	if _, err := tx.Exec(ctx, `SELECT id FROM knowledge_dir WHERE id = $1 FOR UPDATE`, dirID); err != nil {
		return fail(err)
	}
	var batchID pgtype.UUID
	if err := tx.QueryRow(ctx, `
INSERT INTO knowledge_scan_batch (workspace_id, dir_id, trigger_source, result, started_at, finished_at)
VALUES ($1, $2, $3, 'noop', $4, $5) RETURNING id`,
		workspaceID, dirID, trigger, started, finished).Scan(&batchID); err != nil {
		return fail(err)
	}
	batch.ID = uuidToString(batchID)

	type knownEntry struct {
		id      pgtype.UUID
		sha     string
		version int
		mirror  string
	}
	existing := make(map[string]knownEntry, len(memories))
	known, err := tx.Query(ctx,
		`SELECT id, key, content_sha256, current_version, mirror_state FROM knowledge_entry WHERE dir_id = $1`, dirID)
	if err != nil {
		return fail(err)
	}
	for known.Next() {
		var key string
		var entry knownEntry
		if err := known.Scan(&entry.id, &key, &entry.sha, &entry.version, &entry.mirror); err != nil {
			known.Close()
			return fail(err)
		}
		existing[key] = entry
	}
	if err := known.Err(); err != nil {
		known.Close()
		return fail(err)
	}
	known.Close()

	for key, content := range memories {
		sha := knowledgeContentSHA(content)
		if prior, ok := existing[key]; ok {
			if prior.sha == sha {
				if _, err := tx.Exec(ctx,
					`UPDATE knowledge_entry
SET mirror_state = 'synced', missing_confirmation = 0, last_confirmed_at = now()
	WHERE id = $1`, prior.id); err != nil {
					return fail(err)
				}
				if prior.mirror == "source_deleted" {
					if _, err := tx.Exec(ctx, `
						INSERT INTO knowledge_lifecycle_event (workspace_id, entry_id, from_state, to_state, operation, reason, evidence_ref)
	SELECT workspace_id, id, lifecycle_state, lifecycle_state, 'source_restored', 'source reappeared during scan', $2
	FROM knowledge_entry WHERE id = $1`, prior.id, "knowledge_scan_batch:"+uuidToString(batchID)); err != nil {
						return fail(err)
					}
				}
				continue
			}
			if _, err := tx.Exec(ctx, `
	UPDATE knowledge_entry SET content = $2, content_sha256 = $3, current_version = current_version + 1,
	    mirror_state = 'synced', missing_confirmation = 0, last_confirmed_at = now() WHERE id = $1`,
				prior.id, content, sha); err != nil {
				return fail(err)
			}
			if _, err := tx.Exec(ctx, `
INSERT INTO knowledge_entry_version (workspace_id, entry_id, version, content, content_sha256, source, scan_batch_id)
VALUES ($1, $2, $3, $4, $5, 'scan', $6)`,
				workspaceID, prior.id, prior.version+1, content, sha, batchID); err != nil {
				return fail(err)
			}
			batch.Updated++
			continue
		}
		var entryID pgtype.UUID
		if err := tx.QueryRow(ctx, `
INSERT INTO knowledge_entry (workspace_id, dir_id, key, content, content_sha256)
	VALUES ($1, $2, $3, $4, $5) ON CONFLICT DO NOTHING RETURNING id`,
			workspaceID, dirID, key, content, sha).Scan(&entryID); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fail(err)
		}
		if entryID.Valid {
			if _, err := tx.Exec(ctx, `
INSERT INTO knowledge_entry_version (workspace_id, entry_id, version, content, content_sha256, source, scan_batch_id)
VALUES ($1, $2, 1, $3, $4, 'initial', $5)`, workspaceID, entryID, content, sha, batchID); err != nil {
				return fail(err)
			}
			batch.Added++
		}
	}
	for key, prior := range existing {
		if _, stillThere := memories[key]; stillThere {
			continue
		}
		var deleted bool
		if err := tx.QueryRow(ctx, `
UPDATE knowledge_entry
SET missing_confirmation = missing_confirmation + 1,
    mirror_state = CASE WHEN missing_confirmation + 1 >= 2 THEN 'source_deleted' ELSE mirror_state END,
    last_confirmed_at = now()
WHERE id = $1
RETURNING mirror_state = 'source_deleted'`, prior.id).Scan(&deleted); err != nil {
			return fail(err)
		}
		if deleted && prior.mirror != "source_deleted" {
			if _, err := tx.Exec(ctx, `
				INSERT INTO knowledge_lifecycle_event (workspace_id, entry_id, from_state, to_state, operation, reason, evidence_ref)
	SELECT workspace_id, id, lifecycle_state, lifecycle_state, 'source_deleted', 'confirmed absent in two successful scans', $2
	FROM knowledge_entry WHERE id = $1`, prior.id, "knowledge_scan_batch:"+uuidToString(batchID)); err != nil {
				return fail(err)
			}
			batch.Removed++
		}
	}

	if batch.Added > 0 || batch.Updated > 0 || batch.Removed > 0 {
		batch.Result = "changed"
	} else {
		batch.Result = "noop"
	}
	batch.FinishedAt = &finished
	if _, err := tx.Exec(ctx, `
UPDATE knowledge_scan_batch SET result = $2, added = $3, updated = $4, removed = $5, finished_at = $6
WHERE id = $1`, batchID, batch.Result, batch.Added, batch.Updated, batch.Removed, finished); err != nil {
		return fail(err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE knowledge_dir SET health_state = 'ok', health_note = '', updated_at = now() WHERE id = $1`, dirID); err != nil {
		return fail(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return h.recordFailedScanBatch(ctx, workspaceID, dirID, trigger, started, finished, err.Error())
	}

	return batch, nil
}

// recordUnchangedScanBatch logs a zero-change batch for a scan the daemon
// verified byte-identical against the mirror SHA map (no diff payload sent),
// refreshing confirmation timestamps in one statement.
func (h *Handler) recordUnchangedScanBatch(ctx context.Context, workspaceID, dirID pgtype.UUID,
	trigger string, started, finished time.Time) *KnowledgeScanBatchResponse {
	_, _ = h.DB.Exec(ctx, `
UPDATE knowledge_entry SET last_confirmed_at = now() WHERE dir_id = $1`, dirID)
	_, err := h.DB.Exec(ctx, `
INSERT INTO knowledge_scan_batch (workspace_id, dir_id, trigger_source, result, started_at, finished_at)
VALUES ($1, $2, $3, 'noop', $4, $5)`, workspaceID, dirID, trigger, started, finished)
	batch := &KnowledgeScanBatchResponse{
		DirID: uuidToString(dirID), TriggerSource: trigger, Result: "noop",
		StartedAt: started, FinishedAt: &finished,
	}
	if err != nil {
		return batch
	}
	_, _ = h.DB.Exec(ctx,
		`UPDATE knowledge_dir SET health_state = 'ok', health_note = '', updated_at = now() WHERE id = $1`, dirID)
	return batch
}

// recordFailedScanBatch records a daemon-reported scan failure: the batch is
// logged and the directory's health flips to no_access with the reason, so
// the UI can show why a source is stale.
func (h *Handler) recordFailedScanBatch(ctx context.Context, workspaceID, dirID pgtype.UUID,
	trigger string, started, finished time.Time, reason string) (*KnowledgeScanBatchResponse, error) {
	_, _ = h.DB.Exec(ctx, `
INSERT INTO knowledge_scan_batch (workspace_id, dir_id, trigger_source, result, error, started_at, finished_at)
VALUES ($1, $2, $3, 'failed', $4, $5, $6)`, workspaceID, dirID, trigger, reason, started, finished)
	_, _ = h.DB.Exec(ctx,
		`UPDATE knowledge_dir SET health_state = 'no_access', health_note = $2, updated_at = now() WHERE id = $1`,
		dirID, reason)
	batch := &KnowledgeScanBatchResponse{
		DirID: uuidToString(dirID), TriggerSource: trigger, Result: "failed",
		Error: reason, StartedAt: started, FinishedAt: &finished,
	}
	return batch, &knowledgeAdoptError{reason}
}

func knowledgeContentSHA(content string) string {
	digest := sha256.Sum256([]byte(content))
	return hex.EncodeToString(digest[:])
}

func timestampTime(ts pgtype.Timestamptz) time.Time {
	if ts.Valid {
		return ts.Time
	}
	return time.Time{}
}
