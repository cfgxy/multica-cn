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
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
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
	ID               string     `json:"id"`
	DirID            string     `json:"dir_id"`
	Key              string     `json:"key"`
	Content          string     `json:"content"`
	ContentSha256    string     `json:"content_sha256"`
	MirrorState      string     `json:"mirror_state"`
	FirstSeenAt      time.Time  `json:"first_seen_at"`
	LastConfirmedAt  time.Time  `json:"last_confirmed_at"`
	AdoptionState    string     `json:"adoption_state"`
	AdoptedAt        *time.Time `json:"adopted_at,omitempty"`
	AdoptedBy        *string    `json:"adopted_by,omitempty"`
	UltimateDirID    *string    `json:"ultimate_dir_id,omitempty"`
	AdoptedFromDirID *string    `json:"adopted_from_dir_id,omitempty"`
	AdoptedFromKey   *string    `json:"adopted_from_key,omitempty"`
	AdoptionError    string     `json:"adoption_error,omitempty"`
}

// GetKnowledgeDirs serves GET /api/knowledge/dirs: the workspace's registered
// directories with entry counts and the latest scan batch per directory.
func (h *Handler) GetKnowledgeDirs(w http.ResponseWriter, r *http.Request) {
	workspaceID := parseUUID(h.resolveWorkspaceID(r))
	rows, err := h.DB.Query(r.Context(), `
SELECT d.id, d.kind, d.path, d.project_id, d.label, d.health_state, d.health_note,
       d.removed, d.scan_requested, d.created_at, d.updated_at,
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
			&dir.HealthState, &dir.HealthNote, &dir.Removed, &dir.ScanRequested,
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
// external source. Kind is a server decision (candidate_cli) — the daemon
// auto-discovers project directories and auto-designates the ultimate, so a
// hand registration only ever adds an external candidate. Scanning happens on
// the daemon that hosts the path (registration just queues the first scan);
// the row lands immediately and the first batch shows up within one knowledge
// cycle, doubling as the access verification.
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
	rows, err := h.DB.Query(r.Context(), `
SELECT e.id, e.dir_id, e.key, e.content, e.content_sha256, e.mirror_state,
       e.first_seen_at, e.last_confirmed_at, e.adoption_state, e.adopted_at,
       e.adopted_by, e.ultimate_dir_id, e.adopted_from_dir_id, e.adopted_from_key,
       e.adoption_error
FROM knowledge_entry e
WHERE e.workspace_id = $1
  AND ($2::uuid IS NULL OR e.dir_id = $2)
  AND ($3 = '' OR position($3 in lower(e.key)) > 0 OR position($3 in lower(e.content)) > 0)
ORDER BY e.last_confirmed_at DESC
LIMIT 200`, workspaceID, dirID, query)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read knowledge entries")
		return
	}
	defer rows.Close()
	entries := make([]KnowledgeEntryResponse, 0, 64)
	for rows.Next() {
		var entry KnowledgeEntryResponse
		var id, entryDirID, adoptedBy, ultimateDirID, fromDirID pgtype.UUID
		var adoptedAt pgtype.Timestamptz
		if err := rows.Scan(&id, &entryDirID, &entry.Key, &entry.Content, &entry.ContentSha256,
			&entry.MirrorState, &entry.FirstSeenAt, &entry.LastConfirmedAt, &entry.AdoptionState,
			&adoptedAt, &adoptedBy, &ultimateDirID, &fromDirID, &entry.AdoptedFromKey,
			&entry.AdoptionError); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to read knowledge entries")
			return
		}
		entry.ID = uuidToString(id)
		entry.DirID = uuidToString(entryDirID)
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
		entries = append(entries, entry)
	}
	if rows.Err() != nil {
		writeError(w, http.StatusInternalServerError, "failed to read knowledge entries")
		return
	}
	writeJSON(w, http.StatusOK, entries)
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

// knowledgeUltimateDir resolves the workspace's active ultimate directory.
func (h *Handler) knowledgeUltimateDir(ctx context.Context, workspaceID pgtype.UUID) (pgtype.UUID, string, error) {
	var id pgtype.UUID
	var path string
	err := h.DB.QueryRow(ctx, `
SELECT id, path FROM knowledge_dir
WHERE workspace_id = $1 AND kind = 'ultimate' AND removed = FALSE
ORDER BY created_at LIMIT 1`, workspaceID).Scan(&id, &path)
	if err != nil {
		return id, "", &knowledgeAdoptError{"no ultimate knowledge directory is designated for this workspace"}
	}
	return id, path, nil
}

// applyKnowledgeScanResult mirrors one daemon-reported scan into the database:
// new keys are added, changed content is updated, vanished keys are flagged
// source_deleted, and every batch — including zero-change ones — is logged so
// incremental ingestion stays recomputable. This is the state-authority half
// of the daemon-side scan (RUYI-289): no bd IO happens here.
func (h *Handler) applyKnowledgeScanResult(ctx context.Context, workspaceID, dirID pgtype.UUID,
	path, trigger string, memories map[string]string, started, finished time.Time) (*KnowledgeScanBatchResponse, error) {
	batch := &KnowledgeScanBatchResponse{
		DirID: uuidToString(dirID), TriggerSource: trigger, StartedAt: started,
	}

	existing := make(map[string]string, len(memories))
	known, err := h.DB.Query(ctx,
		`SELECT key, content_sha256 FROM knowledge_entry WHERE dir_id = $1`, dirID)
	if err != nil {
		return h.recordFailedScanBatch(ctx, workspaceID, dirID, trigger, started, finished, err.Error())
	}
	for known.Next() {
		var key, sha string
		if err := known.Scan(&key, &sha); err != nil {
			known.Close()
			return h.recordFailedScanBatch(ctx, workspaceID, dirID, trigger, started, finished, err.Error())
		}
		existing[key] = sha
	}
	known.Close()

	for key, content := range memories {
		sha := knowledgeContentSHA(content)
		if prior, ok := existing[key]; ok {
			if prior == sha {
				_, _ = h.DB.Exec(ctx,
					`UPDATE knowledge_entry SET last_confirmed_at = now() WHERE dir_id = $1 AND key = $2`, dirID, key)
				continue
			}
			if _, err := h.DB.Exec(ctx, `
UPDATE knowledge_entry SET content = $3, content_sha256 = $4, mirror_state = 'synced',
    last_confirmed_at = now() WHERE dir_id = $1 AND key = $2`,
				dirID, key, content, sha); err != nil {
				return h.recordFailedScanBatch(ctx, workspaceID, dirID, trigger, started, finished, err.Error())
			}
			batch.Updated++
			continue
		}
		if _, err := h.DB.Exec(ctx, `
INSERT INTO knowledge_entry (workspace_id, dir_id, key, content, content_sha256)
VALUES ($1, $2, $3, $4, $5) ON CONFLICT DO NOTHING`,
			workspaceID, dirID, key, content, sha); err != nil {
			return h.recordFailedScanBatch(ctx, workspaceID, dirID, trigger, started, finished, err.Error())
		}
		batch.Added++
	}
	for key := range existing {
		if _, stillThere := memories[key]; stillThere {
			continue
		}
		if _, err := h.DB.Exec(ctx, `
UPDATE knowledge_entry SET mirror_state = 'source_deleted', last_confirmed_at = now()
WHERE dir_id = $1 AND key = $2 AND mirror_state = 'synced'`, dirID, key); err != nil {
			return h.recordFailedScanBatch(ctx, workspaceID, dirID, trigger, started, finished, err.Error())
		}
		batch.Removed++
	}

	if batch.Added > 0 || batch.Updated > 0 || batch.Removed > 0 {
		batch.Result = "changed"
	} else {
		batch.Result = "noop"
	}
	batch.FinishedAt = &finished
	if _, err := h.DB.Exec(ctx, `
INSERT INTO knowledge_scan_batch (workspace_id, dir_id, trigger_source, result, added, updated, removed, started_at, finished_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		workspaceID, dirID, trigger, batch.Result, batch.Added, batch.Updated, batch.Removed, started, finished); err != nil {
		return batch, &knowledgeAdoptError{"scan succeeded but logging the batch failed: " + err.Error()}
	}
	_, _ = h.DB.Exec(ctx,
		`UPDATE knowledge_dir SET health_state = 'ok', health_note = '', updated_at = now() WHERE id = $1`, dirID)

	// A candidate source whose first scan found entries enters the proposal
	// pool exactly once (RUYI-289 §4): one system proposal per directory,
	// database-enforced by uidx_proposal_system_dir.
	if batch.Added > 0 {
		h.maybeCreateSystemProposal(ctx, workspaceID, dirID, path, len(memories))
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
