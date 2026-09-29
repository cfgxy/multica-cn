package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// bd memories unified collection (RUYI-265, spec §K). Every read path here is
// a mirror: the source bd files are never written by this server. The single
// write path is the Owner adoption transfer into the workspace's ultimate
// knowledge directory, and even that only counts as adopted after the entry
// has been read back from the ultimate bd (G1: 转移完成才算采纳).

type KnowledgeDirResponse struct {
	ID          string                      `json:"id"`
	Kind        string                      `json:"kind"`
	Path        string                      `json:"path"`
	ProjectID   *string                     `json:"project_id,omitempty"`
	Label       string                      `json:"label"`
	HealthState string                      `json:"health_state"`
	HealthNote  string                      `json:"health_note"`
	Removed     bool                        `json:"removed"`
	EntryCount  int64                       `json:"entry_count"`
	LastScan    *KnowledgeScanBatchResponse `json:"last_scan,omitempty"`
	CreatedAt   time.Time                   `json:"created_at"`
	UpdatedAt   time.Time                   `json:"updated_at"`
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

// knowledgeBdBin is overridable so tests can point the whole flow at a fake
// bd; production uses the bd on the daemon host's PATH.
func knowledgeBdBin() string {
	if bin := os.Getenv("MULTICA_KNOWLEDGE_BD_BIN"); bin != "" {
		return bin
	}
	return "bd"
}

// bdListMemories reads a directory's memories read-only (--readonly makes the
// no-write guarantee structural, not behavioral). Returns key → content.
func bdListMemories(ctx context.Context, dirPath string) (map[string]string, error) {
	out, err := exec.CommandContext(ctx, knowledgeBdBin(), "memories", "--json", "--readonly", "-C", dirPath).Output()
	if err != nil {
		return nil, err
	}
	// bd envelopes its metadata into the same flat object as the memories (a
	// numeric "schema_version"), so not every value is a string: decode each
	// entry separately and keep only the string-valued memories.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, err
	}
	memories := make(map[string]string, len(raw))
	for key, value := range raw {
		var content string
		if json.Unmarshal(value, &content) == nil {
			memories[key] = content
		}
	}
	return memories, nil
}

// bdRemember writes one memory into a directory's bd (the only write this
// package performs, and only against the ultimate directory). An existing key
// is updated in place, which makes adoption retries idempotent.
func bdRemember(ctx context.Context, dirPath, key, content, actor string) error {
	return exec.CommandContext(ctx, knowledgeBdBin(), "remember", content,
		"--key", key, "--actor", actor, "-C", dirPath).Run()
}

// bdRecall reads one memory back from a directory's bd.
func bdRecall(ctx context.Context, dirPath, key string) (string, error) {
	out, err := exec.CommandContext(ctx, knowledgeBdBin(), "recall", key, "-C", dirPath).Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// GetKnowledgeDirs serves GET /api/knowledge/dirs: the workspace's registered
// directories with entry counts and the latest scan batch per directory.
func (h *Handler) GetKnowledgeDirs(w http.ResponseWriter, r *http.Request) {
	workspaceID := parseUUID(h.resolveWorkspaceID(r))
	rows, err := h.DB.Query(r.Context(), `
SELECT d.id, d.kind, d.path, d.project_id, d.label, d.health_state, d.health_note,
       d.removed, d.created_at, d.updated_at,
       (SELECT count(*) FROM knowledge_entry e WHERE e.dir_id = d.id),
       lb.id, lb.trigger_source, lb.result, lb.added, lb.updated, lb.removed,
       lb.error, lb.started_at, lb.finished_at
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
			&dir.HealthState, &dir.HealthNote, &dir.Removed, &dir.CreatedAt, &dir.UpdatedAt,
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

// PostKnowledgeDir serves POST /api/knowledge/dirs: register a candidate
// source or designate the workspace's ultimate knowledge directory. Both
// arrive through the same server channel (the multica CLI path and any UI
// path share this authorization); registration immediately runs a first
// read-only scan so the row lands with real verification, not an assumption.
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
	if body.Kind != "ultimate" && body.Kind != "candidate_cli" && body.Kind != "candidate_auto" {
		writeError(w, http.StatusBadRequest, "kind must be ultimate, candidate_cli or candidate_auto")
		return
	}

	// One active ultimate directory per workspace, and no duplicate paths.
	var existing int
	if err := h.DB.QueryRow(r.Context(), `
SELECT count(*) FROM knowledge_dir
WHERE workspace_id = $1 AND removed = FALSE
  AND (($2 = 'ultimate' AND kind = 'ultimate') OR path = $3)`,
		workspaceID, body.Kind, body.Path).Scan(&existing); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to register the knowledge directory")
		return
	}
	if existing > 0 {
		writeError(w, http.StatusConflict, "this path or ultimate designation is already registered")
		return
	}

	var id pgtype.UUID
	if err := h.DB.QueryRow(r.Context(), `
INSERT INTO knowledge_dir (workspace_id, kind, path, project_id, label, created_by)
VALUES ($1, $2, $3, NULLIF($4, '')::uuid, $5, NULLIF($6, '')::uuid)
RETURNING id`, workspaceID, body.Kind, body.Path, body.ProjectID, body.Label, userID).Scan(&id); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to register the knowledge directory")
		return
	}
	// First scan doubles as the access verification: a directory the server
	// cannot read still registers, but with an explicit health state, and the
	// failure is recorded as a batch so the log shows why it is empty.
	scanResult := "scanned"
	if _, err := h.knowledgeScan(r, workspaceID, id, body.Path, "initial"); err != nil {
		scanResult = "registered with a failed initial scan: " + err.Error()
	}
	writeJSON(w, http.StatusCreated, map[string]string{
		"id": uuidToString(id), "status": scanResult,
	})
}

// ScanKnowledgeDir serves POST /api/knowledge/dirs/{id}/scan: a manual
// read-only rescan of one directory.
func (h *Handler) ScanKnowledgeDir(w http.ResponseWriter, r *http.Request) {
	workspaceID := parseUUID(h.resolveWorkspaceID(r))
	dirID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	var path string
	var removed bool
	if err := h.DB.QueryRow(r.Context(),
		`SELECT path, removed FROM knowledge_dir WHERE id = $1 AND workspace_id = $2`,
		dirID, workspaceID).Scan(&path, &removed); err != nil {
		writeError(w, http.StatusNotFound, "knowledge directory not found")
		return
	}
	if removed {
		writeError(w, http.StatusConflict, "this directory was unregistered")
		return
	}
	batch, err := h.knowledgeScan(r, workspaceID, dirID, path, "manual")
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, batch)
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
// single write action of the whole knowledge flow, Owner-only. Transfer and
// adoption are one server flow — success is only recorded after the entry
// has been written into the ultimate bd and read back from it; on failure
// the source entry is untouched and the reason is kept for retry.
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
	var entry KnowledgeEntryResponse
	var id, dirID pgtype.UUID
	if err := h.DB.QueryRow(r.Context(), `
SELECT e.id, e.dir_id, e.key, e.content, e.adoption_state
FROM knowledge_entry e WHERE e.id = $1 AND e.workspace_id = $2`, entryID, workspaceID).
		Scan(&id, &dirID, &entry.Key, &entry.Content, &entry.AdoptionState); err != nil {
		writeError(w, http.StatusNotFound, "knowledge entry not found")
		return
	}
	if entry.AdoptionState == "adopted" {
		writeError(w, http.StatusConflict, "this entry is already adopted")
		return
	}
	if err := h.knowledgeAdopt(r, workspaceID, id, entry.Key, entry.Content, userID); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "adopted"})
}

// knowledgeTransfer is the shared write-read-back flow (entry adoption and
// knowledge proposal adoption both run it): resolve the ultimate directory,
// refuse same-content-different-key duplicates, write via bd, read the key
// back and compare. It returns the ultimate directory id on success; no
// database state is modified here — callers record success or failure.
func (h *Handler) knowledgeTransfer(r *http.Request, workspaceID pgtype.UUID,
	key, content, userID string) (pgtype.UUID, error) {
	ultimateID, ultimatePath, err := h.knowledgeUltimateDir(r.Context(), workspaceID)
	if err != nil {
		return ultimateID, err
	}
	// Same content under a different key already adopted: a parallel copy,
	// not a re-adoption — refuse so the ultimate library stays deduplicated.
	memories, err := bdListMemories(r.Context(), ultimatePath)
	if err != nil {
		return ultimateID, &knowledgeAdoptError{"ultimate knowledge directory is not readable: " + err.Error()}
	}
	for existingKey, existingContent := range memories {
		if existingKey != key && existingContent == content {
			return ultimateID, &knowledgeAdoptError{"the ultimate library already has this content under key " + existingKey}
		}
	}
	if err := bdRemember(r.Context(), ultimatePath, key, content, "multica-"+userID); err != nil {
		return ultimateID, &knowledgeAdoptError{"transfer failed: " + err.Error()}
	}
	readBack, err := bdRecall(r.Context(), ultimatePath, key)
	if err != nil {
		return ultimateID, &knowledgeAdoptError{"read-back failed: " + err.Error()}
	}
	if strings.TrimSpace(readBack) != strings.TrimSpace(content) {
		return ultimateID, &knowledgeAdoptError{"read-back mismatch: the ultimate library content differs from the source"}
	}
	return ultimateID, nil
}

// knowledgeAdopt runs knowledgeTransfer for a mirrored entry and then
// persists the adoption with provenance. Any failure marks
// adoption_state='failed' with the reason; nothing else is touched.
func (h *Handler) knowledgeAdopt(r *http.Request, workspaceID, entryID pgtype.UUID,
	key, content, userID string) error {
	ultimateID, err := h.knowledgeTransfer(r, workspaceID, key, content, userID)
	if err != nil {
		return h.knowledgeAdoptFailed(r, entryID, err.Error())
	}
	_, err = h.DB.Exec(r.Context(), `
UPDATE knowledge_entry
SET adoption_state = 'adopted', adopted_at = now(), adopted_by = NULLIF($2, '')::uuid,
    ultimate_dir_id = $3, adopted_from_dir_id = dir_id, adopted_from_key = key,
    adoption_error = ''
WHERE id = $1`, entryID, userID, ultimateID)
	if err != nil {
		return &knowledgeAdoptError{"transfer succeeded but recording the adoption failed: " + err.Error()}
	}
	return nil
}

type knowledgeAdoptError struct{ message string }

func (e *knowledgeAdoptError) Error() string { return e.message }

func (h *Handler) knowledgeAdoptFailed(r *http.Request, entryID pgtype.UUID, reason string) error {
	_, _ = h.DB.Exec(r.Context(),
		`UPDATE knowledge_entry SET adoption_state = 'failed', adoption_error = $2 WHERE id = $1`,
		entryID, reason)
	return &knowledgeAdoptError{reason}
}

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

// knowledgeScan runs one read-only scan of a directory and mirrors the
// result: new keys are added, changed content is updated, vanished keys are
// flagged source_deleted, and every batch — including zero-change ones — is
// logged so incremental ingestion stays recomputable.
func (h *Handler) knowledgeScan(r *http.Request, workspaceID, dirID pgtype.UUID,
	path, trigger string) (*KnowledgeScanBatchResponse, error) {
	started := time.Now()
	batch := &KnowledgeScanBatchResponse{
		DirID: uuidToString(dirID), TriggerSource: trigger, StartedAt: started,
	}
	fail := func(reason string) (*KnowledgeScanBatchResponse, error) {
		batch.Result = "failed"
		batch.Error = reason
		finished := time.Now()
		batch.FinishedAt = &finished
		_, _ = h.DB.Exec(r.Context(), `
INSERT INTO knowledge_scan_batch (workspace_id, dir_id, trigger_source, result, error, started_at, finished_at)
VALUES ($1, $2, $3, 'failed', $4, $5, $6)`,
			workspaceID, dirID, trigger, reason, started, finished)
		_, _ = h.DB.Exec(r.Context(),
			`UPDATE knowledge_dir SET health_state = 'no_access', health_note = $2, updated_at = now() WHERE id = $1`,
			dirID, reason)
		return batch, &knowledgeAdoptError{reason}
	}

	memories, err := bdListMemories(r.Context(), path)
	if err != nil {
		return fail("scan failed: " + err.Error())
	}

	existing := make(map[string]string, len(memories))
	known, err := h.DB.Query(r.Context(),
		`SELECT key, content_sha256 FROM knowledge_entry WHERE dir_id = $1`, dirID)
	if err != nil {
		return fail("scan failed: " + err.Error())
	}
	for known.Next() {
		var key, sha string
		if err := known.Scan(&key, &sha); err != nil {
			known.Close()
			return fail("scan failed: " + err.Error())
		}
		existing[key] = sha
	}
	known.Close()

	for key, content := range memories {
		sha := knowledgeContentSHA(content)
		if prior, ok := existing[key]; ok {
			if prior == sha {
				_, _ = h.DB.Exec(r.Context(),
					`UPDATE knowledge_entry SET last_confirmed_at = now() WHERE dir_id = $1 AND key = $2`, dirID, key)
				continue
			}
			if _, err := h.DB.Exec(r.Context(), `
UPDATE knowledge_entry SET content = $3, content_sha256 = $4, mirror_state = 'synced',
    last_confirmed_at = now() WHERE dir_id = $1 AND key = $2`,
				dirID, key, content, sha); err != nil {
				return fail("scan failed: " + err.Error())
			}
			batch.Updated++
			continue
		}
		if _, err := h.DB.Exec(r.Context(), `
INSERT INTO knowledge_entry (workspace_id, dir_id, key, content, content_sha256)
VALUES ($1, $2, $3, $4, $5) ON CONFLICT DO NOTHING`,
			workspaceID, dirID, key, content, sha); err != nil {
			return fail("scan failed: " + err.Error())
		}
		batch.Added++
	}
	for key := range existing {
		if _, stillThere := memories[key]; stillThere {
			continue
		}
		if _, err := h.DB.Exec(r.Context(), `
UPDATE knowledge_entry SET mirror_state = 'source_deleted', last_confirmed_at = now()
WHERE dir_id = $1 AND key = $2 AND mirror_state = 'synced'`, dirID, key); err != nil {
			return fail("scan failed: " + err.Error())
		}
		batch.Removed++
	}

	if batch.Added > 0 || batch.Updated > 0 || batch.Removed > 0 {
		batch.Result = "changed"
	} else {
		batch.Result = "noop"
	}
	finished := time.Now()
	batch.FinishedAt = &finished
	_, err = h.DB.Exec(r.Context(), `
INSERT INTO knowledge_scan_batch (workspace_id, dir_id, trigger_source, result, added, updated, removed, started_at, finished_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		workspaceID, dirID, trigger, batch.Result, batch.Added, batch.Updated, batch.Removed, started, finished)
	if err != nil {
		return batch, &knowledgeAdoptError{"scan succeeded but logging the batch failed: " + err.Error()}
	}
	_, _ = h.DB.Exec(r.Context(),
		`UPDATE knowledge_dir SET health_state = 'ok', health_note = '', updated_at = now() WHERE id = $1`, dirID)
	return batch, nil
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
