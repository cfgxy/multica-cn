package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/middleware"
)

// Daemon-facing knowledge endpoints (RUYI-289). Scans, discovery and adoption
// transfers execute bd IO on the daemon that hosts the paths; the server stays
// the only state authority. Daemons pull a work plan over their existing
// authenticated channel, run it, and report results back — there is no
// server→daemon RPC, so every interaction below is daemon-initiated.

// KnowledgePlanResponse is the full work package for one daemon's knowledge
// cycle: discovery roots (its own local_directory project resources), the
// directories it hosts or that are still unclaimed, the resolved ultimate per
// workspace, and the adoption transfers queued by Owners.
type KnowledgePlanResponse struct {
	Workspaces []KnowledgePlanWorkspace `json:"workspaces"`
}

type KnowledgePlanWorkspace struct {
	WorkspaceID    string                   `json:"workspace_id"`
	HasUltimate    bool                     `json:"has_ultimate"`
	Ultimate       *KnowledgePlanUltimate   `json:"ultimate,omitempty"`
	DiscoveryRoots []string                 `json:"discovery_roots"`
	Dirs           []KnowledgePlanDir       `json:"dirs"`
	Adoptions      []KnowledgePlanAdopt     `json:"adoptions"`
	Operations     []KnowledgePlanOperation `json:"operations"`
}

type KnowledgePlanUltimate struct {
	DirID string `json:"dir_id"`
	Path  string `json:"path"`
}

type KnowledgePlanDir struct {
	DirID         string `json:"dir_id"`
	Kind          string `json:"kind"`
	Path          string `json:"path"`
	Bound         bool   `json:"bound"`
	ScanRequested bool   `json:"scan_requested"`
	// ScanRequired is true when no scan batch has ever been recorded for the
	// directory or an explicit refresh was queued — the daemon must report
	// the source even when it matches the (possibly empty) mirror map.
	ScanRequired bool `json:"scan_required"`
	// Mirror maps every mirrored key to its content SHA-256 so the daemon can
	// skip reporting unchanged sources entirely.
	Mirror map[string]string `json:"mirror"`
}

type KnowledgePlanAdopt struct {
	Kind          string `json:"kind"` // entry
	ID            string `json:"id"`
	Key           string `json:"key"`
	Content       string `json:"content"`
	Actor         string `json:"actor"`
	UltimateDirID string `json:"ultimate_dir_id"`
	UltimatePath  string `json:"ultimate_path"`
}

type KnowledgePlanOperation struct {
	ID            string   `json:"id"`
	OperationType string   `json:"operation_type"`
	Key           string   `json:"key"`
	Content       string   `json:"content"`
	DeleteKeys    []string `json:"delete_keys"`
	Actor         string   `json:"actor"`
	UltimateDirID string   `json:"ultimate_dir_id"`
	UltimatePath  string   `json:"ultimate_path"`
}

// KnowledgeResultsRequest is one daemon's report for a knowledge cycle.
type KnowledgeResultsRequest struct {
	Discoveries []KnowledgeDiscoveryReport `json:"discoveries"`
	Ultimates   []KnowledgeUltimateReport  `json:"ultimates"`
	Scans       []KnowledgeScanReport      `json:"scans"`
	Adoptions   []KnowledgeAdoptReport     `json:"adoptions"`
	Operations  []KnowledgeOperationReport `json:"operations"`
}

type KnowledgeOperationReport struct {
	ID    string `json:"id"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// KnowledgeDiscoveryReport registers an auto-discovered candidate: a project
// directory pinned to this daemon whose path contains a .beads database.
type KnowledgeDiscoveryReport struct {
	WorkspaceID string `json:"workspace_id"`
	Path        string `json:"path"`
	ProjectID   string `json:"project_id,omitempty"`
	Label       string `json:"label"`
}

// KnowledgeUltimateReport auto-designates the workspace's ultimate knowledge
// directory on a daemon-managed stable path; losing the uniqueness race is a
// normal, non-error outcome.
type KnowledgeUltimateReport struct {
	WorkspaceID string `json:"workspace_id"`
	Path        string `json:"path"`
}

type KnowledgeScanReport struct {
	DirID string `json:"dir_id"`
	// scheduled | manual | initial
	TriggerSource string `json:"trigger_source"`
	OK            bool   `json:"ok"`
	Error         string `json:"error,omitempty"`
	// Unchanged means the source matches the mirror SHA map from the plan;
	// Memories is then empty and the server logs a zero-change batch only.
	Unchanged bool              `json:"unchanged"`
	Memories  map[string]string `json:"memories,omitempty"`
}

type KnowledgeAdoptReport struct {
	Kind string `json:"kind"` // entry
	ID   string `json:"id"`
	OK   bool   `json:"ok"`
	// UltimateDirID is the directory the daemon actually transferred into, so
	// the recorded provenance reflects execution time, not queue time.
	UltimateDirID string `json:"ultimate_dir_id,omitempty"`
	Error         string `json:"error,omitempty"`
}

// knowledgePlanDaemonID resolves and cross-checks the requesting daemon's id:
// the query parameter is required for user-identity callers, and a daemon
// token may only ever speak for itself.
func knowledgePlanDaemonID(r *http.Request) (string, int, string) {
	daemonID := strings.TrimSpace(r.URL.Query().Get("daemon_id"))
	ctxDaemonID := middleware.DaemonIDFromContext(r.Context())
	if ctxDaemonID != "" {
		if daemonID != "" && daemonID != ctxDaemonID {
			return "", http.StatusForbidden, "daemon tokens can only address their own daemon"
		}
		return ctxDaemonID, 0, ""
	}
	if daemonID == "" {
		return "", http.StatusBadRequest, "daemon_id is required"
	}
	return daemonID, 0, ""
}

// knowledgePlanWorkspaces resolves the workspaces a plan covers for this
// caller: a user identity sees every workspace it belongs to (the same
// projection ListDaemonWorkspaces serves), a workspace-scoped daemon token
// sees only its bound workspace.
func (h *Handler) knowledgePlanWorkspaces(r *http.Request) ([]pgtype.UUID, int, string) {
	if userID := requestUserID(r); userID != "" {
		rows, err := h.Queries.ListDaemonWorkspaces(r.Context(), parseUUID(userID))
		if err != nil {
			return nil, http.StatusInternalServerError, "failed to resolve daemon workspaces"
		}
		ids := make([]pgtype.UUID, 0, len(rows))
		for _, row := range rows {
			ids = append(ids, row.ID)
		}
		return ids, 0, ""
	}
	workspaceID := middleware.DaemonWorkspaceIDFromContext(r.Context())
	if workspaceID == "" {
		return nil, http.StatusUnauthorized, "daemon workspace identity required"
	}
	return []pgtype.UUID{parseUUID(workspaceID)}, 0, ""
}

// knowledgeParseUUID is the daemon-report tolerant id parse: reports carry
// string ids, and a malformed one drops the item instead of failing the batch.
func knowledgeParseUUID(s string) (pgtype.UUID, bool) {
	id, err := parseUUIDLoose(s)
	if err != nil || !id.Valid {
		return pgtype.UUID{}, false
	}
	return id, true
}

// GetKnowledgePlan serves GET /api/daemon/knowledge/plan?daemon_id=: the work
// package for one knowledge cycle.
func (h *Handler) GetKnowledgePlan(w http.ResponseWriter, r *http.Request) {
	daemonID, status, message := knowledgePlanDaemonID(r)
	if status != 0 {
		writeError(w, status, message)
		return
	}
	workspaceIDs, status, message := h.knowledgePlanWorkspaces(r)
	if status != 0 {
		writeError(w, status, message)
		return
	}
	if len(workspaceIDs) == 0 {
		writeJSON(w, http.StatusOK, &KnowledgePlanResponse{})
		return
	}

	plan := &KnowledgePlanResponse{Workspaces: make([]KnowledgePlanWorkspace, 0, len(workspaceIDs))}
	// planByWorkspace accumulates every per-workspace mutation; Workspaces is
	// materialized from the same pointers at the end so nothing is lost to a
	// stale copy.
	planByWorkspace := make(map[string]*KnowledgePlanWorkspace, len(workspaceIDs))
	for _, ws := range workspaceIDs {
		planByWorkspace[uuidToString(ws)] = &KnowledgePlanWorkspace{WorkspaceID: uuidToString(ws), DiscoveryRoots: []string{}, Dirs: []KnowledgePlanDir{}, Adoptions: []KnowledgePlanAdopt{}, Operations: []KnowledgePlanOperation{}}
	}

	// Discovery roots: this daemon's local_directory project resources per
	// workspace. The daemon decides which roots actually host a bd database.
	rootRows, err := h.DB.Query(r.Context(), `
SELECT pr.workspace_id::text, pr.resource_ref->>'local_path'
FROM project_resource pr
WHERE pr.resource_type = 'local_directory'
  AND pr.resource_ref->>'daemon_id' = $1
  AND pr.workspace_id = ANY($2)`, daemonID, workspaceIDs)
	if err == nil {
		for rootRows.Next() {
			var workspaceID, path string
			if err := rootRows.Scan(&workspaceID, &path); err == nil {
				if wsPlan, ok := planByWorkspace[workspaceID]; ok {
					wsPlan.DiscoveryRoots = append(wsPlan.DiscoveryRoots, path)
				}
			}
		}
		rootRows.Close()
	}

	// Directories this daemon hosts or that are not claimed by any daemon.
	// Ultimate directories are excluded: they are the adoption flow's write
	// target (handled by the dedicated query below), never scan sources —
	// scanning one would re-import adopted content as pending candidates.
	type planDirRow struct {
		workspaceID string
		dir         KnowledgePlanDir
	}
	dirRows, err := h.DB.Query(r.Context(), `
SELECT d.id::text, d.workspace_id::text, d.kind, d.path, d.daemon_id, d.scan_requested,
       EXISTS(SELECT 1 FROM knowledge_scan_batch b WHERE b.dir_id = d.id) AS has_batch
FROM knowledge_dir d
WHERE d.workspace_id = ANY($1) AND d.removed = FALSE AND d.kind <> 'ultimate'
  AND (d.daemon_id = $2 OR d.daemon_id = '')`,
		workspaceIDs, daemonID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to build the knowledge plan")
		return
	}
	dirIDs := make([]pgtype.UUID, 0, 8)
	for dirRows.Next() {
		var row planDirRow
		var daemonIDCol string
		var hasBatch bool
		if err := dirRows.Scan(&row.dir.DirID, &row.workspaceID, &row.dir.Kind, &row.dir.Path,
			&daemonIDCol, &row.dir.ScanRequested, &hasBatch); err != nil {
			dirRows.Close()
			writeError(w, http.StatusInternalServerError, "failed to build the knowledge plan")
			return
		}
		row.dir.Bound = daemonIDCol == daemonID
		row.dir.ScanRequired = row.dir.ScanRequested || !hasBatch
		row.dir.Mirror = map[string]string{}
		if wsPlan, ok := planByWorkspace[row.workspaceID]; ok {
			wsPlan.Dirs = append(wsPlan.Dirs, row.dir)
			if id, ok := knowledgeParseUUID(row.dir.DirID); ok {
				dirIDs = append(dirIDs, id)
			}
		}
	}
	dirRows.Close()

	if len(dirIDs) > 0 {
		shaRows, err := h.DB.Query(r.Context(), `
SELECT dir_id::text, key, content_sha256 FROM knowledge_entry WHERE dir_id = ANY($1)`, dirIDs)
		if err == nil {
			mirrorByDir := make(map[string]map[string]string, len(dirIDs))
			for shaRows.Next() {
				var dirID, key, sha string
				if err := shaRows.Scan(&dirID, &key, &sha); err != nil {
					break
				}
				if mirrorByDir[dirID] == nil {
					mirrorByDir[dirID] = map[string]string{}
				}
				mirrorByDir[dirID][key] = sha
			}
			shaRows.Close()
			for _, wsPlan := range planByWorkspace {
				for j := range wsPlan.Dirs {
					if mirror, ok := mirrorByDir[wsPlan.Dirs[j].DirID]; ok {
						wsPlan.Dirs[j].Mirror = mirror
					}
				}
			}
		}
	}

	// Ultimates: the workspace's active adoption target, whichever daemon
	// designated it — but adoption work below only rides the daemon hosting it.
	type ultimateRow struct {
		workspaceID string
		ult         KnowledgePlanUltimate
		daemonIDCol string
	}
	ultRows, err := h.DB.Query(r.Context(), `
SELECT id::text, workspace_id::text, path, daemon_id
FROM knowledge_dir
WHERE workspace_id = ANY($1) AND kind = 'ultimate' AND removed = FALSE`, workspaceIDs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to build the knowledge plan")
		return
	}
	ultimateByWorkspace := make(map[string]ultimateRow)
	for ultRows.Next() {
		var row ultimateRow
		if err := ultRows.Scan(&row.ult.DirID, &row.workspaceID, &row.ult.Path, &row.daemonIDCol); err != nil {
			ultRows.Close()
			writeError(w, http.StatusInternalServerError, "failed to build the knowledge plan")
			return
		}
		ultimateByWorkspace[row.workspaceID] = row
	}
	ultRows.Close()
	for workspaceID, row := range ultimateByWorkspace {
		wsPlan := planByWorkspace[workspaceID]
		if wsPlan == nil {
			continue
		}
		wsPlan.HasUltimate = true
		usableByDaemon := row.daemonIDCol == "" || row.daemonIDCol == daemonID
		if usableByDaemon {
			wsPlan.Ultimate = &row.ult
		}
	}

	// Queued adoption transfers: entry jobs carry the source key/content.
	entryRows, err := h.DB.Query(r.Context(), `
SELECT e.id::text, e.workspace_id::text, e.key, e.content, COALESCE(e.adopted_by::text, '')
FROM knowledge_entry e
WHERE e.workspace_id = ANY($1) AND e.adoption_state = 'transferring'`, workspaceIDs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to build the knowledge plan")
		return
	}
	type pendingJob struct {
		plan KnowledgePlanAdopt
	}
	pendingByWorkspace := map[string][]pendingJob{}
	for entryRows.Next() {
		var job pendingJob
		var workspaceID, adoptedBy string
		if err := entryRows.Scan(&job.plan.ID, &workspaceID, &job.plan.Key, &job.plan.Content, &adoptedBy); err != nil {
			entryRows.Close()
			writeError(w, http.StatusInternalServerError, "failed to build the knowledge plan")
			return
		}
		job.plan.Kind = "entry"
		job.plan.Actor = "multica-" + adoptedBy
		pendingByWorkspace[workspaceID] = append(pendingByWorkspace[workspaceID], job)
	}
	entryRows.Close()

	for workspaceID, jobs := range pendingByWorkspace {
		wsPlan := planByWorkspace[workspaceID]
		if wsPlan == nil || wsPlan.Ultimate == nil {
			continue
		}
		for _, job := range jobs {
			job.plan.UltimateDirID = wsPlan.Ultimate.DirID
			job.plan.UltimatePath = wsPlan.Ultimate.Path
			wsPlan.Adoptions = append(wsPlan.Adoptions, job.plan)
		}
	}

	// Every mutable operation is attached to an active ultimate directory that
	// is already bound to this daemon. Candidate mirrors and unbound hubs never
	// cross this trust boundary.
	opRows, err := h.DB.Query(r.Context(), `
SELECT o.id::text, o.workspace_id::text, o.operation_type, e.key, o.content,
       o.delete_entry_ids::text, COALESCE(o.requested_by::text, ''), d.id::text, d.path
FROM knowledge_entry_operation o
JOIN knowledge_entry e ON e.id = o.entry_id AND e.workspace_id = o.workspace_id
JOIN knowledge_dir d ON d.id = e.dir_id AND d.workspace_id = o.workspace_id
WHERE o.workspace_id = ANY($1) AND o.status = 'queued'
  AND d.kind = 'ultimate' AND d.removed = FALSE AND d.daemon_id = $2`, workspaceIDs, daemonID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to build the knowledge plan")
		return
	}
	for opRows.Next() {
		var operation KnowledgePlanOperation
		var workspaceID, sourceIDsJSON, requestedBy string
		if err := opRows.Scan(&operation.ID, &workspaceID, &operation.OperationType, &operation.Key,
			&operation.Content, &sourceIDsJSON, &requestedBy, &operation.UltimateDirID, &operation.UltimatePath); err != nil {
			opRows.Close()
			writeError(w, http.StatusInternalServerError, "failed to build the knowledge plan")
			return
		}
		if err := json.Unmarshal([]byte(sourceIDsJSON), &operation.DeleteKeys); err != nil {
			operation.DeleteKeys = []string{}
		} else if len(operation.DeleteKeys) > 0 {
			keyRows, keyErr := h.DB.Query(r.Context(), `
SELECT key FROM knowledge_entry
WHERE workspace_id::text = $1 AND id::text = ANY($2::text[])`, workspaceID, operation.DeleteKeys)
			if keyErr == nil {
				keys := make([]string, 0, len(operation.DeleteKeys))
				for keyRows.Next() {
					var key string
					if keyRows.Scan(&key) == nil {
						keys = append(keys, key)
					}
				}
				keyRows.Close()
				operation.DeleteKeys = keys
			}
		}
		operation.Actor = "multica-" + requestedBy
		if wsPlan := planByWorkspace[workspaceID]; wsPlan != nil {
			wsPlan.Operations = append(wsPlan.Operations, operation)
		}
	}
	opRows.Close()

	// Materialize the accumulated workspaces in stable id order.
	for _, ws := range workspaceIDs {
		if wsPlan, ok := planByWorkspace[uuidToString(ws)]; ok {
			plan.Workspaces = append(plan.Workspaces, *wsPlan)
		}
	}

	writeJSON(w, http.StatusOK, plan)
}

// PostKnowledgeResults serves POST /api/daemon/knowledge/results?daemon_id=:
// one daemon's report for a knowledge cycle. Every item is applied
// independently and best-effort; the response summarizes what landed.
func (h *Handler) PostKnowledgeResults(w http.ResponseWriter, r *http.Request) {
	daemonID, status, message := knowledgePlanDaemonID(r)
	if status != 0 {
		writeError(w, status, message)
		return
	}
	workspaceIDs, status, message := h.knowledgePlanWorkspaces(r)
	if status != 0 {
		writeError(w, status, message)
		return
	}
	allowed := make(map[string]pgtype.UUID, len(workspaceIDs))
	for _, ws := range workspaceIDs {
		allowed[uuidToString(ws)] = ws
	}
	var body KnowledgeResultsRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid knowledge results body")
		return
	}

	var registered, ultimatesAccepted, ultimatesConflicts, scansApplied, adoptionsResolved, operationsResolved int
	for _, disc := range body.Discoveries {
		workspaceID, ok := allowed[disc.WorkspaceID]
		if !ok || disc.Path == "" {
			continue
		}
		tag, err := h.DB.Exec(r.Context(), `
INSERT INTO knowledge_dir (workspace_id, kind, path, project_id, label, daemon_id)
VALUES ($1, 'candidate_auto', $2, NULLIF($3, '')::uuid, $4, $5)
ON CONFLICT DO NOTHING`,
			workspaceID, disc.Path, disc.ProjectID, disc.Label, daemonID)
		if err == nil && tag.RowsAffected() > 0 {
			registered++
		}
	}
	for _, ult := range body.Ultimates {
		workspaceID, ok := allowed[ult.WorkspaceID]
		if !ok || ult.Path == "" {
			continue
		}
		tag, err := h.DB.Exec(r.Context(), `
INSERT INTO knowledge_dir (workspace_id, kind, path, daemon_id)
VALUES ($1, 'ultimate', $2, $3)
ON CONFLICT DO NOTHING`,
			workspaceID, ult.Path, daemonID)
		switch {
		case err == nil && tag.RowsAffected() > 0:
			ultimatesAccepted++
		case err != nil && !isPgUniqueViolation(err):
			// Write failure: uncounted, the next cycle retries.
		default:
			// This workspace's ultimate uniqueness race was lost — the
			// normal multi-daemon outcome, not an error.
			ultimatesConflicts++
		}
	}
	for _, scan := range body.Scans {
		dirID, ok := knowledgeParseUUID(scan.DirID)
		if !ok {
			continue
		}
		trigger := scan.TriggerSource
		if trigger != "scheduled" && trigger != "manual" && trigger != "initial" {
			continue
		}
		var workspaceID pgtype.UUID
		var dirPath, kind, boundDaemon string
		err := h.DB.QueryRow(r.Context(), `
SELECT workspace_id, path, kind, daemon_id FROM knowledge_dir
WHERE id = $1 AND removed = FALSE`, dirID).
			Scan(&workspaceID, &dirPath, &kind, &boundDaemon)
		if err != nil {
			continue
		}
		if _, allowedWorkspace := allowed[uuidToString(workspaceID)]; !allowedWorkspace {
			continue
		}
		// Same trust-boundary rule as the plan: a scan report for an ultimate
		// directory — a stale cycle's report, or a daemon not deriving its
		// work from the plan — must not land mirror rows.
		if kind == "ultimate" {
			continue
		}
		// A daemon may only scan directories it hosts or unclaimed ones.
		if boundDaemon != "" && boundDaemon != daemonID {
			continue
		}
		// An unclaimed directory's failures are not recorded: the reporting
		// daemon may merely lack the path while another daemon hosts it.
		// Claim-by-scan below ties health tracking to a real owner.
		if !scan.OK && boundDaemon == "" {
			continue
		}
		started := time.Now()
		finished := time.Now()
		if !scan.OK {
			h.recordFailedScanBatch(r.Context(), workspaceID, dirID, trigger, started, finished, "scan failed: "+scan.Error)
		} else if scan.Unchanged {
			h.recordUnchangedScanBatch(r.Context(), workspaceID, dirID, trigger, started, finished)
		} else {
			h.applyKnowledgeScanResult(r.Context(), workspaceID, dirID, dirPath, trigger, scan.Memories, started, finished)
		}
		// A queued manual/initial scan request has been serviced either way;
		// the outcome is visible through the batch log and health state.
		_, _ = h.DB.Exec(r.Context(),
			`UPDATE knowledge_dir SET scan_requested = FALSE, updated_at = now() WHERE id = $1`, dirID)
		// A successful scan claims an unclaimed directory for this daemon.
		if scan.OK && boundDaemon == "" {
			_, _ = h.DB.Exec(r.Context(),
				`UPDATE knowledge_dir SET daemon_id = $2 WHERE id = $1 AND daemon_id = ''`, dirID, daemonID)
		}
		scansApplied++
	}
	for _, adopt := range body.Adoptions {
		id, ok := knowledgeParseUUID(adopt.ID)
		if !ok {
			continue
		}
		resolved := h.applyKnowledgeAdoptResult(r.Context(), daemonID, adopt.Kind, id, adopt.OK, adopt.UltimateDirID, adopt.Error)
		if resolved {
			adoptionsResolved++
		}
	}
	for _, operation := range body.Operations {
		id, ok := knowledgeParseUUID(operation.ID)
		if !ok {
			continue
		}
		if h.applyKnowledgeOperationResult(r.Context(), id, daemonID, operation.OK, operation.Error) {
			operationsResolved++
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"discoveries_registered": registered,
		"ultimates_registered":   ultimatesAccepted,
		"ultimate_conflicts":     ultimatesConflicts,
		"scans_applied":          scansApplied,
		"adoptions_resolved":     adoptionsResolved,
		"operations_resolved":    operationsResolved,
	})
}

// applyKnowledgeOperationResult advances one durable daemon job. Its queued
// and daemon-binding guards make stale reports inert; all mirror mutations and
// completion markers share one transaction after the daemon has read back bd.
// A daemon failure leaves the operation queued: bd commands can have applied a
// prefix of a merge before reporting an error, and replay is idempotent.
func (h *Handler) applyKnowledgeOperationResult(ctx context.Context, operationID pgtype.UUID, daemonID string, ok bool, failureReason string) bool {
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return false
	}
	defer tx.Rollback(ctx)
	var workspaceID, operationType, content string
	var entryID pgtype.UUID
	var sourceIDsJSON []byte
	err = tx.QueryRow(ctx, `
SELECT o.workspace_id::text, o.entry_id, o.operation_type, o.content, o.delete_entry_ids
FROM knowledge_entry_operation o
JOIN knowledge_entry e ON e.id = o.entry_id AND e.workspace_id = o.workspace_id
JOIN knowledge_dir d ON d.id = e.dir_id AND d.workspace_id = o.workspace_id
WHERE o.id = $1 AND o.status = 'queued' AND d.kind = 'ultimate'
  AND d.removed = FALSE AND d.daemon_id = $2`, operationID, daemonID).
		Scan(&workspaceID, &entryID, &operationType, &content, &sourceIDsJSON)
	if err != nil {
		return false
	}
	if !ok {
		if failureReason == "" {
			failureReason = "daemon did not confirm the knowledge operation"
		}
		tag, err := tx.Exec(ctx, `
UPDATE knowledge_entry_operation
SET error = $2, completed_at = NULL
WHERE id = $1 AND status = 'queued'`, operationID, failureReason)
		if err != nil || tag.RowsAffected() == 0 {
			return false
		}
		return tx.Commit(ctx) == nil
	}
	switch operationType {
	case "edit", "merge":
		tag, err := tx.Exec(ctx, `
UPDATE knowledge_entry SET content = $2, content_sha256 = $3, last_confirmed_at = now(), mirror_state = 'synced'
WHERE id = $1`, entryID, content, knowledgeContentSHA(content))
		if err != nil || tag.RowsAffected() == 0 {
			return false
		}
		if operationType == "merge" {
			var sourceIDs []string
			if json.Unmarshal(sourceIDsJSON, &sourceIDs) != nil || len(sourceIDs) == 0 {
				return false
			}
			tag, err = tx.Exec(ctx, `
UPDATE knowledge_entry SET mirror_state = 'source_deleted', last_confirmed_at = now()
WHERE workspace_id::text = $1 AND id::text = ANY($2::text[]) AND id <> $3`, workspaceID, sourceIDs, entryID)
			if err != nil || tag.RowsAffected() != int64(len(sourceIDs)) {
				return false
			}
		}
	case "delete":
		tag, err := tx.Exec(ctx, `
UPDATE knowledge_entry SET mirror_state = 'source_deleted', last_confirmed_at = now() WHERE id = $1`, entryID)
		if err != nil || tag.RowsAffected() == 0 {
			return false
		}
	default:
		return false
	}
	tag, err := tx.Exec(ctx, `
UPDATE knowledge_entry_operation SET status = 'completed', error = '', completed_at = now()
WHERE id = $1 AND status = 'queued'`, operationID)
	if err != nil || tag.RowsAffected() == 0 {
		return false
	}
	return tx.Commit(ctx) == nil
}

// applyKnowledgeAdoptResult lands a daemon-reported adoption transfer. The
// transferring and bound-ultimate guards make stale or foreign callbacks inert.
// A successful read-back both marks the candidate adopted and materializes the
// canonical ultimate mirror, so later owner edits target the memory bd wrote.
func (h *Handler) applyKnowledgeAdoptResult(ctx context.Context, daemonID, kind string, id pgtype.UUID,
	ok bool, ultimateDirID, failureReason string) bool {
	switch kind {
	case "entry":
		tx, err := h.TxStarter.Begin(ctx)
		if err != nil {
			return false
		}
		defer tx.Rollback(ctx)

		var workspaceID, sourceDirID, key, content, contentSHA string
		var adoptedBy pgtype.UUID
		err = tx.QueryRow(ctx, `
SELECT e.workspace_id::text, e.dir_id::text, e.key, e.content, e.content_sha256, e.adopted_by
FROM knowledge_entry e
JOIN knowledge_dir d ON d.id = $2::uuid AND d.workspace_id = e.workspace_id
WHERE e.id = $1 AND e.adoption_state = 'transferring'
  AND d.kind = 'ultimate' AND d.removed = FALSE AND d.daemon_id = $3
FOR UPDATE OF e`, id, ultimateDirID, daemonID).
			Scan(&workspaceID, &sourceDirID, &key, &content, &contentSHA, &adoptedBy)
		if err != nil {
			return false
		}

		if !ok {
			if failureReason == "" {
				failureReason = "daemon did not confirm the knowledge adoption"
			}
			tag, err := tx.Exec(ctx, `
UPDATE knowledge_entry SET adoption_state = 'failed', adoption_error = $2
WHERE id = $1 AND adoption_state = 'transferring'`, id, failureReason)
			if err != nil || tag.RowsAffected() == 0 {
				return false
			}
			return tx.Commit(ctx) == nil
		}

		tag, err := tx.Exec(ctx, `
UPDATE knowledge_entry
SET adoption_state = 'adopted', adopted_at = now(), ultimate_dir_id = $2::uuid,
    adopted_from_dir_id = dir_id, adopted_from_key = key, adoption_error = ''
WHERE id = $1 AND adoption_state = 'transferring'`, id, ultimateDirID)
		if err != nil || tag.RowsAffected() == 0 {
			return false
		}
		tag, err = tx.Exec(ctx, `
INSERT INTO knowledge_entry (
    workspace_id, dir_id, key, content, content_sha256, mirror_state,
    adoption_state, adopted_at, adopted_by, ultimate_dir_id,
    adopted_from_dir_id, adopted_from_key, adoption_error
)
VALUES ($1::uuid, $2::uuid, $3, $4, $5, 'synced', 'adopted', now(), $6, $2::uuid, $7::uuid, $3, '')
ON CONFLICT (dir_id, key) DO UPDATE
SET content = EXCLUDED.content,
    content_sha256 = EXCLUDED.content_sha256,
    mirror_state = 'synced',
    last_confirmed_at = now(),
    adoption_state = 'adopted',
    adopted_at = EXCLUDED.adopted_at,
    adopted_by = EXCLUDED.adopted_by,
    ultimate_dir_id = EXCLUDED.ultimate_dir_id,
    adopted_from_dir_id = EXCLUDED.adopted_from_dir_id,
    adopted_from_key = EXCLUDED.adopted_from_key,
    adoption_error = ''`, workspaceID, ultimateDirID, key, content, contentSHA, adoptedBy, sourceDirID)
		if err != nil || tag.RowsAffected() == 0 {
			return false
		}
		return tx.Commit(ctx) == nil
	}
	return false
}
