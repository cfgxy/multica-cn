package handler

// Knowledge directories and the daemon-facing plan/results pipeline at the
// HTTP boundary (RUYI-265 spec §K, RUYI-289).
//
// Since RUYI-289 the handler never touches bd: registration, scan requests
// and adoption only mutate queued state; /api/daemon/knowledge/plan hands
// the work to the daemon hosting the paths and /api/daemon/knowledge/results
// lands what the daemon reports. bd IO itself is exercised by the daemon
// package's tests against a fake bd binary.

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/knowledgegovernance"
	"github.com/multica-ai/multica/server/internal/testutil"
)

// --- shared fixtures ---------------------------------------------------------

// registerKnowledgeDir registers one directory (no scan happens in-process)
// and registers cleanup for its mirror rows, batches and the directory.
func registerKnowledgeDir(t *testing.T, kind, path string) string {
	t.Helper()
	var created struct{ ID, Status string }
	testutil.Call(t, testHandler.PostKnowledgeDir, newRequest(http.MethodPost, "/api/knowledge/dirs",
		map[string]any{"kind": kind, "path": path})).Want(http.StatusCreated).JSON(&created)
	ids := []string{created.ID}
	t.Cleanup(func() {
		ctx := context.Background()
		testPool.Exec(ctx, `DELETE FROM agent_task_queue WHERE id IN (SELECT task_id FROM knowledge_governance_run WHERE dir_id = ANY($1::uuid[]))`, ids)
		testPool.Exec(ctx, `DELETE FROM knowledge_governance_finding WHERE run_id IN (SELECT id FROM knowledge_governance_run WHERE dir_id = ANY($1::uuid[]))`, ids)
		testPool.Exec(ctx, `DELETE FROM knowledge_governance_run WHERE dir_id = ANY($1::uuid[])`, ids)
		testPool.Exec(ctx, `DELETE FROM knowledge_lifecycle_event WHERE entry_id IN (SELECT id FROM knowledge_entry WHERE dir_id = ANY($1::uuid[]))`, ids)
		testPool.Exec(ctx, `DELETE FROM knowledge_entry_version WHERE entry_id IN (SELECT id FROM knowledge_entry WHERE dir_id = ANY($1::uuid[]))`, ids)
		testPool.Exec(ctx, `DELETE FROM knowledge_entry WHERE dir_id = ANY($1::uuid[])`, ids)
		testPool.Exec(ctx, `DELETE FROM knowledge_scan_batch WHERE dir_id = ANY($1::uuid[])`, ids)
		testPool.Exec(ctx, `DELETE FROM knowledge_dir WHERE id = ANY($1::uuid[])`, ids)
	})
	if created.Status != "queued for first scan" {
		t.Fatalf("register %s dir: status=%q, want queued for first scan", kind, created.Status)
	}
	return created.ID
}

func knowledgeEntries(t *testing.T, dirID string) []KnowledgeEntryResponse {
	t.Helper()
	var entries []KnowledgeEntryResponse
	testutil.Call(t, testHandler.GetKnowledgeEntries,
		newRequest(http.MethodGet, "/api/knowledge/entries?dir_id="+dirID, nil)).
		Want(http.StatusOK).JSON(&entries)
	return entries
}

func knowledgeEntryByKey(t *testing.T, dirID, key string) KnowledgeEntryResponse {
	t.Helper()
	for _, entry := range knowledgeEntries(t, dirID) {
		if entry.Key == key {
			return entry
		}
	}
	t.Fatalf("no mirrored entry with key %q in dir %s", key, dirID)
	return KnowledgeEntryResponse{}
}

func currentKnowledgeVersionID(t *testing.T, entryID string) string {
	t.Helper()
	var versionID string
	err := testPool.QueryRow(context.Background(), `
	SELECT v.id::text
	FROM knowledge_entry_version v
	JOIN knowledge_entry e ON e.id = v.entry_id
	WHERE e.id = $1 AND v.version = e.current_version`, entryID).Scan(&versionID)
	if err != nil {
		t.Fatalf("current knowledge version for %s: %v", entryID, err)
	}
	return versionID
}

// latestScanBatch reads the most recent batch row for a directory straight
// from the database — the HTTP scan endpoint now only queues work.
func latestScanBatch(t *testing.T, dirID string) KnowledgeScanBatchResponse {
	t.Helper()
	var batch KnowledgeScanBatchResponse
	err := testPool.QueryRow(context.Background(), `
SELECT dir_id::text, trigger_source, result, added, updated, removed, COALESCE(error, '')
FROM knowledge_scan_batch WHERE dir_id = $1 ORDER BY started_at DESC LIMIT 1`, dirID).
		Scan(&batch.DirID, &batch.TriggerSource, &batch.Result, &batch.Added, &batch.Updated, &batch.Removed, &batch.Error)
	if err != nil {
		t.Fatalf("no scan batch recorded for %s: %v", dirID, err)
	}
	return batch
}

func knowledgeScanBatchID(t *testing.T, dirID string) string {
	t.Helper()
	var batchID string
	dbfx.QueryRow(t, `
		SELECT id::text FROM knowledge_scan_batch
		WHERE dir_id = $1 ORDER BY started_at DESC LIMIT 1`, dirID).Scan(&batchID)
	return batchID
}

// knowledgeDirState reads one directory row with the fields the UI reads.
func knowledgeDirState(t *testing.T, dirID string) KnowledgeDirResponse {
	t.Helper()
	var dirs []KnowledgeDirResponse
	testutil.Call(t, testHandler.GetKnowledgeDirs,
		newRequest(http.MethodGet, "/api/knowledge/dirs", nil)).Want(http.StatusOK).JSON(&dirs)
	for _, dir := range dirs {
		if dir.ID == dirID {
			return dir
		}
	}
	t.Fatalf("knowledge dir %s not found", dirID)
	return KnowledgeDirResponse{}
}

func requestScan(t *testing.T, dirID string) {
	t.Helper()
	testutil.Call(t, testHandler.ScanKnowledgeDir,
		withURLParam(newRequest(http.MethodPost, "/api/knowledge/dirs/"+dirID+"/scan", nil), "id", dirID)).
		Want(http.StatusAccepted)
}

func TestKnowledgeGovernanceRunWaitsForWorkspaceDeleteFence(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Fatal("database fixture is required")
	}

	dirID := registerKnowledgeDir(t, "candidate_cli", t.TempDir())
	var agentID string
	dbfx.QueryRow(t, `SELECT id::text FROM agent WHERE workspace_id = $1 ORDER BY created_at LIMIT 1`, testWorkspaceID).Scan(&agentID)
	dbfx.InsertNoID(t, "retrospective_config", testutil.Cols{
		"workspace_id":      testWorkspaceID,
		"enabled":           true,
		"include_in_review": false,
		"window_days":       7,
		"agent_id":          agentID,
	}, "workspace_id = $1", testWorkspaceID)

	runner := &knowledgegovernance.Runner{
		DB:      testPool,
		Queries: testHandler.Queries,
		Enqueue: testHandler.KnowledgeGovernanceTaskEnqueuer(),
	}
	holder, err := testPool.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin workspace delete holder: %v", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = holder.Rollback(context.Background())
		}
	}()
	if _, err := holder.Exec(context.Background(), `SELECT id FROM workspace WHERE id = $1 FOR UPDATE`, testWorkspaceID); err != nil {
		t.Fatalf("lock workspace for delete: %v", err)
	}

	type runResult struct {
		stats *knowledgegovernance.RunStats
		err   error
	}
	result := make(chan runResult, 1)
	go func() {
		stats, err := runner.RunWorkspace(context.Background(), testWorkspaceID, dirID, testUserID, "manual")
		result <- runResult{stats: stats, err: err}
	}()

	select {
	case outcome := <-result:
		t.Fatalf("governance start bypassed workspace delete fence: stats=%+v err=%v", outcome.stats, outcome.err)
	case <-time.After(200 * time.Millisecond):
	}
	if err := holder.Commit(context.Background()); err != nil {
		t.Fatalf("release workspace delete holder: %v", err)
	}
	committed = true

	var outcome runResult
	select {
	case outcome = <-result:
	case <-time.After(5 * time.Second):
		t.Fatal("governance start did not resume after workspace delete fence released")
	}
	if outcome.err != nil {
		t.Fatalf("RunWorkspace: %v", outcome.err)
	}
	if outcome.stats == nil || outcome.stats.RunID == "" || outcome.stats.TaskID == "" {
		t.Fatalf("governance start did not return linked run and task ids: %+v", outcome.stats)
	}

	var linkedTaskID string
	dbfx.QueryRow(t, `SELECT task_id::text FROM knowledge_governance_run WHERE id = $1`, outcome.stats.RunID).Scan(&linkedTaskID)
	if linkedTaskID != outcome.stats.TaskID {
		t.Fatalf("run task link = %q, want %q", linkedTaskID, outcome.stats.TaskID)
	}
	var taskCount int
	dbfx.QueryRow(t, `SELECT count(*) FROM agent_task_queue WHERE id = $1 AND issue_id IS NULL`, outcome.stats.TaskID).Scan(&taskCount)
	if taskCount != 1 {
		t.Fatalf("expected one issue-less governance task, got %d", taskCount)
	}
}

func knowledgePlan(t *testing.T, daemonID string) KnowledgePlanResponse {
	t.Helper()
	var plan KnowledgePlanResponse
	testutil.Call(t, testHandler.GetKnowledgePlan,
		newRequest(http.MethodGet, "/api/daemon/knowledge/plan?daemon_id="+daemonID, nil)).
		Want(http.StatusOK).JSON(&plan)
	return plan
}

func postKnowledgeResults(t *testing.T, daemonID string, body map[string]any) map[string]any {
	t.Helper()
	var ack map[string]any
	testutil.Call(t, testHandler.PostKnowledgeResults,
		newRequest(http.MethodPost, "/api/daemon/knowledge/results?daemon_id="+daemonID, body)).
		Want(http.StatusOK).JSON(&ack)
	return ack
}

func adoptEntry(t *testing.T, entryID string) int {
	t.Helper()
	response := testutil.Call(t, testHandler.AdoptKnowledgeEntry,
		withURLParam(newRequest(http.MethodPost, "/api/knowledge/entries/"+entryID+"/adopt", nil), "id", entryID))
	return response.Code
}

// --- tests -------------------------------------------------------------------

// TestKnowledgeRegistrationQueuesFirstScan pins the new registration
// semantics: the handler only records the directory (dup path and second
// ultimate refuse with 409); the first scan is daemon work now.
func TestKnowledgeRegistrationQueuesFirstScan(t *testing.T) {
	if testHandler == nil {
		t.Fatal("database fixture is required")
	}

	candidate := registerKnowledgeDir(t, "candidate_cli", t.TempDir())
	if dir := knowledgeDirState(t, candidate); !dir.ScanRequested {
		t.Fatalf("fresh registration must be queued for its first scan: %+v", dir)
	}

	// Duplicate path in the same workspace refuses.
	testutil.Call(t, testHandler.PostKnowledgeDir, newRequest(http.MethodPost, "/api/knowledge/dirs",
		map[string]any{"kind": "candidate_cli", "path": knowledgeDirState(t, candidate).Path})).
		Want(http.StatusConflict)

	// A second active ultimate refuses (single adoption target per workspace).
	registerKnowledgeDir(t, "ultimate", t.TempDir())
	testutil.Call(t, testHandler.PostKnowledgeDir, newRequest(http.MethodPost, "/api/knowledge/dirs",
		map[string]any{"kind": "ultimate", "path": t.TempDir()})).Want(http.StatusConflict)

	// Unknown kinds refuse; kind is optional and defaults to candidate_cli.
	testutil.Call(t, testHandler.PostKnowledgeDir, newRequest(http.MethodPost, "/api/knowledge/dirs",
		map[string]any{"kind": " sovereign", "path": t.TempDir()})).Want(http.StatusBadRequest)
	var created struct{ ID string }
	testutil.Call(t, testHandler.PostKnowledgeDir, newRequest(http.MethodPost, "/api/knowledge/dirs",
		map[string]any{"path": t.TempDir()})).Want(http.StatusCreated).JSON(&created)
	testPool.Exec(context.Background(), `DELETE FROM knowledge_dir WHERE id = $1`, created.ID)

	// An explicit scan request queues a refresh instead of scanning inline.
	requestScan(t, candidate)
	if dir := knowledgeDirState(t, candidate); !dir.ScanRequested {
		t.Fatalf("scan request must set scan_requested: %+v", dir)
	}
}

// TestKnowledgePlanHandsWorkToDaemon pins the plan contents: hosted +
// unclaimed directories with their mirror SHA map, the resolved ultimate,
// and — after a queue-time adoption — the pending transfer job.
func TestKnowledgePlanHandsWorkToDaemon(t *testing.T) {
	if testHandler == nil {
		t.Fatal("database fixture is required")
	}
	const daemonID = "daemon-under-test"

	ultimate := registerKnowledgeDir(t, "ultimate", "/tmp/ultimate-under-test")
	candidate := registerKnowledgeDir(t, "candidate_cli", "/tmp/candidate-under-test")

	plan := knowledgePlan(t, daemonID)
	var ws *KnowledgePlanWorkspace
	for i := range plan.Workspaces {
		if plan.Workspaces[i].WorkspaceID == testWorkspaceID {
			ws = &plan.Workspaces[i]
		}
	}
	if ws == nil {
		t.Fatalf("plan misses the test workspace: %+v", plan.Workspaces)
	}
	if !ws.HasUltimate || ws.Ultimate == nil || ws.Ultimate.DirID != ultimate {
		t.Fatalf("plan ultimate: has=%v ultimate=%+v, want dir %s", ws.HasUltimate, ws.Ultimate, ultimate)
	}
	var planned *KnowledgePlanDir
	for i := range ws.Dirs {
		if ws.Dirs[i].DirID == candidate {
			planned = &ws.Dirs[i]
		}
	}
	if planned == nil {
		t.Fatalf("plan misses the candidate dir: %+v", ws.Dirs)
	}
	if !planned.ScanRequired || !planned.ScanRequested || planned.Bound {
		t.Fatalf("fresh candidate in plan: %+v, want scan_required + unclaimed", planned)
	}
	if len(planned.Mirror) != 0 {
		t.Fatalf("fresh candidate mirror: %v, want empty", planned.Mirror)
	}

	// The daemon's first successful scan claims the unclaimed directory.
	postKnowledgeResults(t, daemonID, map[string]any{
		"scans": []map[string]any{{
			"dir_id": candidate, "trigger_source": "initial", "ok": true,
			"memories": map[string]string{"tip": "seeded by the fake daemon"},
		}},
	})
	if dir := knowledgeDirState(t, candidate); dir.HealthState != "ok" {
		t.Fatalf("health after first scan: %q, want ok", dir.HealthState)
	}

	// Mirror SHA map is now populated for the next cycle.
	planned = nil
	for i := range knowledgePlan(t, daemonID).Workspaces {
		w := &knowledgePlan(t, daemonID).Workspaces[i]
		if w.WorkspaceID == testWorkspaceID {
			ws = w
		}
	}
	for i := range ws.Dirs {
		if ws.Dirs[i].DirID == candidate {
			planned = &ws.Dirs[i]
		}
	}
	if planned == nil || !planned.Bound || planned.ScanRequired {
		t.Fatalf("candidate after first scan: %+v, want bound + scan satisfied", planned)
	}
	if planned.Mirror["tip"] == "" {
		t.Fatalf("mirror SHA map missing tip: %v", planned.Mirror)
	}
}

// TestKnowledgePlanExcludesUltimateFromScanDirs pins the RUYI-289 QA P1 fix:
// the ultimate directory is the adoption flow's write target, so the plan
// must expose it as ws.Ultimate (adoption jobs ride it) but never list it in
// Dirs — otherwise every scheduled scan re-imports adopted content into the
// candidate pool as pending duplicates.
func TestKnowledgePlanExcludesUltimateFromScanDirs(t *testing.T) {
	if testHandler == nil {
		t.Fatal("database fixture is required")
	}
	const daemonID = "daemon-under-test"

	ultimate := registerKnowledgeDir(t, "ultimate", "/tmp/ultimate-not-a-scan-target")
	candidate := registerKnowledgeDir(t, "candidate_cli", "/tmp/ultimate-fix-candidate")

	plan := knowledgePlan(t, daemonID)
	var ws *KnowledgePlanWorkspace
	for i := range plan.Workspaces {
		if plan.Workspaces[i].WorkspaceID == testWorkspaceID {
			ws = &plan.Workspaces[i]
		}
	}
	if ws == nil {
		t.Fatalf("plan misses the test workspace: %+v", plan.Workspaces)
	}
	if !ws.HasUltimate || ws.Ultimate == nil || ws.Ultimate.DirID != ultimate {
		t.Fatalf("plan ultimate: has=%v ultimate=%+v, want dir %s", ws.HasUltimate, ws.Ultimate, ultimate)
	}
	for _, dir := range ws.Dirs {
		if dir.DirID == ultimate {
			t.Fatalf("ultimate dir %s handed to the daemon as a scan target: %+v", ultimate, dir)
		}
	}
	planned := false
	for _, dir := range ws.Dirs {
		if dir.DirID == candidate {
			planned = true
		}
	}
	if !planned {
		t.Fatalf("candidate dir %s missing from plan dirs: %+v", candidate, ws.Dirs)
	}
}

// TestKnowledgeResultsRefuseUltimateScanReports pins the server-side guard of
// the same fix: a scan report for an ultimate directory is dropped at the
// trust boundary — no mirror rows, no batch log, no health change — so
// adopted content cannot re-enter the pool through a stale or stray report.
func TestKnowledgeResultsRefuseUltimateScanReports(t *testing.T) {
	if testHandler == nil {
		t.Fatal("database fixture is required")
	}
	const daemonID = "daemon-under-test"

	ultimate := registerKnowledgeDir(t, "ultimate", "/tmp/ultimate-scan-guard")

	ack := postKnowledgeResults(t, daemonID, map[string]any{
		"scans": []map[string]any{{
			"dir_id": ultimate, "trigger_source": "scheduled", "ok": true,
			"memories": map[string]string{"adopted-key": "content already living in the ultimate store"},
		}},
	})
	if ack["scans_applied"].(float64) != 0 {
		t.Fatalf("ultimate scan report applied: %v", ack)
	}
	var batches, entries int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM knowledge_scan_batch WHERE dir_id = $1`, ultimate).Scan(&batches); err != nil {
		t.Fatal(err)
	}
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM knowledge_entry WHERE dir_id = $1`, ultimate).Scan(&entries); err != nil {
		t.Fatal(err)
	}
	if batches != 0 || entries != 0 {
		t.Fatalf("ultimate scan side effects: batches=%d entries=%d, want 0/0", batches, entries)
	}
}

// TestKnowledgeResultsLandsScans pins the diff pipeline: a daemon-reported
// memories map becomes added/updated mirror rows, while two successful missing
// observations are required before source_deleted; unchanged and failed reports
// land as noop/failed batches; and reports for another daemon are refused.
func TestKnowledgeResultsLandsScans(t *testing.T) {
	if testHandler == nil {
		t.Fatal("database fixture is required")
	}
	const daemonID = "daemon-under-test"

	candidate := registerKnowledgeDir(t, "candidate_cli", "/tmp/scan-target-under-test")

	postKnowledgeResults(t, daemonID, map[string]any{
		"scans": []map[string]any{{
			"dir_id": candidate, "trigger_source": "initial", "ok": true,
			"memories": map[string]string{"a": "one", "b": "two"},
		}},
	})
	if batch := latestScanBatch(t, candidate); batch.Result != "changed" || batch.Added != 2 {
		t.Fatalf("initial scan batch: %+v, want changed +2", batch)
	}
	if dir := knowledgeDirState(t, candidate); dir.ScanRequested || dir.HealthState != "ok" {
		t.Fatalf("dir after scan: requested=%v health=%q, want cleared + ok", dir.ScanRequested, dir.HealthState)
	}

	// Source evolves: a edited, c added, b absent once. A single observation is
	// not proof of deletion, so b remains searchable pending a second successful scan.
	postKnowledgeResults(t, daemonID, map[string]any{
		"scans": []map[string]any{{
			"dir_id": candidate, "trigger_source": "scheduled", "ok": true,
			"memories": map[string]string{"a": "one-edited", "c": "three"},
		}},
	})
	if batch := latestScanBatch(t, candidate); batch.Result != "changed" || batch.Added != 1 || batch.Updated != 1 || batch.Removed != 0 {
		t.Fatalf("changed scan batch: %+v, want 1/1/0", batch)
	}
	if pending := knowledgeEntryByKey(t, candidate, "b"); pending.MirrorState != "synced" || pending.MissingConfirmation != 1 {
		t.Fatalf("first missing observation: %+v, want synced with one confirmation", pending)
	}
	if edited := knowledgeEntryByKey(t, candidate, "a"); edited.Content != "one-edited" || edited.CurrentVersion != 2 {
		t.Fatalf("edited key a: content=%q version=%d, want one-edited/v2", edited.Content, edited.CurrentVersion)
	}

	postKnowledgeResults(t, daemonID, map[string]any{
		"scans": []map[string]any{{
			"dir_id": candidate, "trigger_source": "scheduled", "ok": true,
			"memories": map[string]string{"a": "one-edited", "c": "three"},
		}},
	})
	if batch := latestScanBatch(t, candidate); batch.Result != "changed" || batch.Removed != 1 {
		t.Fatalf("second missing scan batch: %+v, want one confirmed removal", batch)
	}
	if deleted := knowledgeEntryByKey(t, candidate, "b"); deleted.MirrorState != "source_deleted" || deleted.MissingConfirmation < 2 {
		t.Fatalf("confirmed missing key b: %+v", deleted)
	}
	var deletionEvidenceRef string
	if err := testPool.QueryRow(context.Background(), `
		SELECT evidence_ref
		FROM knowledge_lifecycle_event
		WHERE entry_id = (SELECT id FROM knowledge_entry WHERE dir_id = $1 AND key = 'b')
		  AND operation = 'source_deleted'`, candidate).Scan(&deletionEvidenceRef); err != nil {
		t.Fatalf("source deletion evidence: %v", err)
	}
	if !strings.HasPrefix(deletionEvidenceRef, "knowledge_scan_batch:") {
		t.Fatalf("source deletion lacks scan evidence: %q", deletionEvidenceRef)
	}

	// An unchanged report logs a zero-change noop batch.
	postKnowledgeResults(t, daemonID, map[string]any{
		"scans": []map[string]any{{
			"dir_id": candidate, "trigger_source": "manual", "ok": true, "unchanged": true,
		}},
	})
	if batch := latestScanBatch(t, candidate); batch.Result != "noop" || batch.TriggerSource != "manual" {
		t.Fatalf("unchanged scan batch: %+v, want manual noop", batch)
	}

	// A failed report flips health to no_access with the reason.
	postKnowledgeResults(t, daemonID, map[string]any{
		"scans": []map[string]any{{
			"dir_id": candidate, "trigger_source": "scheduled", "ok": false,
			"error": "bd disappeared",
		}},
	})
	if batch := latestScanBatch(t, candidate); batch.Result != "failed" || !strings.Contains(batch.Error, "bd disappeared") {
		t.Fatalf("failed scan batch: %+v", batch)
	}
	if dir := knowledgeDirState(t, candidate); dir.HealthState != "no_access" || !strings.Contains(dir.HealthNote, "bd disappeared") {
		t.Fatalf("dir after failure: health=%q note=%q", dir.HealthState, dir.HealthNote)
	}

	// Reports for a directory bound to ANOTHER daemon are ignored — a daemon
	// may not poison a competitor's hosted source.
	testPool.Exec(context.Background(),
		`UPDATE knowledge_dir SET daemon_id = 'some-other-daemon' WHERE id = $1`, candidate)
	ack := postKnowledgeResults(t, daemonID, map[string]any{
		"scans": []map[string]any{{
			"dir_id": candidate, "trigger_source": "scheduled", "ok": false,
			"error": "forged failure",
		}},
	})
	if ack["scans_applied"].(float64) != 0 {
		t.Fatalf("foreign daemon scan applied: %v", ack)
	}
	if dir := knowledgeDirState(t, candidate); dir.HealthState != "no_access" {
		t.Fatalf("foreign daemon report must not touch health, got %q", dir.HealthState)
	}
}

func TestKnowledgeManagedWritebackQueuesAndArchivesSourceChanges(t *testing.T) {
	if testHandler == nil {
		t.Fatal("database fixture is required")
	}
	const daemonID = "daemon-under-test"

	candidate := registerKnowledgeDir(t, "candidate_cli", "/tmp/writeback-fails-closed")
	postKnowledgeResults(t, daemonID, map[string]any{
		"scans": []map[string]any{{
			"dir_id": candidate, "trigger_source": "initial", "ok": true,
			"memories": map[string]string{"reviewed": "owner-reviewed content"},
		}},
	})
	entry := knowledgeEntryByKey(t, candidate, "reviewed")
	scanEvidence := "knowledge_scan_batch:" + knowledgeScanBatchID(t, candidate)

	testutil.Call(t, testHandler.PutKnowledgeDirManaged,
		withURLParam(newRequest(http.MethodPut, "/api/knowledge/dirs/"+candidate+"/managed",
			map[string]any{"managed_active": true}), "id", candidate)).Want(http.StatusOK)
	testutil.Call(t, testHandler.PostKnowledgeEntryLifecycle,
		withURLParam(newRequest(http.MethodPost, "/api/knowledge/entries/"+entry.ID+"/lifecycle", map[string]any{
			"state": "deprecated", "reason": "superseded by an Owner-reviewed rule", "evidence_ref": scanEvidence, "writeback": true,
		}), "id", entry.ID)).Want(http.StatusAccepted)

	queued := knowledgeEntryByKey(t, candidate, "reviewed")
	if queued.LifecycleState != "deprecated" || queued.WritebackState != "queued" {
		t.Fatalf("write-back was not queued: %+v", queued)
	}
	plan := knowledgePlan(t, daemonID)
	if len(plan.Workspaces) != 1 || len(plan.Workspaces[0].Writebacks) != 1 {
		t.Fatalf("write-back plan = %+v, want one job", plan)
	}
	remove := plan.Workspaces[0].Writebacks[0]
	if remove.Operation != "remove" || remove.EntryID != entry.ID || remove.Key != "reviewed" || remove.ExpectedContentSha256 != entry.ContentSha256 {
		t.Fatalf("remove plan = %+v", remove)
	}

	// A subsequent Owner decision supersedes the queued physical delete before
	// a daemon receives it. The source remains untouched until a new explicit
	// write-back is requested.
	testutil.Call(t, testHandler.PostKnowledgeEntryLifecycle,
		withURLParam(newRequest(http.MethodPost, "/api/knowledge/entries/"+entry.ID+"/lifecycle", map[string]any{
			"state": "superseded", "reason": "a later Owner decision replaced the queued deletion", "evidence_ref": scanEvidence,
		}), "id", entry.ID)).Want(http.StatusAccepted)
	plan = knowledgePlan(t, daemonID)
	if len(plan.Workspaces) != 1 || len(plan.Workspaces[0].Writebacks) != 0 {
		t.Fatalf("superseded write-back plan = %+v, want no jobs", plan)
	}
	var cancelledState string
	if err := testPool.QueryRow(context.Background(), `
		SELECT writeback_state FROM knowledge_lifecycle_event WHERE id = $1`, remove.EventID).Scan(&cancelledState); err != nil {
		t.Fatalf("read superseded write-back event: %v", err)
	}
	if cancelledState != "cancelled" {
		t.Fatalf("superseded write-back state = %q, want cancelled", cancelledState)
	}

	// Queue a fresh explicitly authorized delete for the remainder of the
	// physical delete/restore receipt coverage below.
	testutil.Call(t, testHandler.PostKnowledgeEntryLifecycle,
		withURLParam(newRequest(http.MethodPost, "/api/knowledge/entries/"+entry.ID+"/lifecycle", map[string]any{
			"state": "deprecated", "reason": "superseded by an Owner-reviewed rule", "evidence_ref": scanEvidence, "writeback": true,
		}), "id", entry.ID)).Want(http.StatusAccepted)
	plan = knowledgePlan(t, daemonID)
	if len(plan.Workspaces) != 1 || len(plan.Workspaces[0].Writebacks) != 1 {
		t.Fatalf("replacement write-back plan = %+v, want one job", plan)
	}
	remove = plan.Workspaces[0].Writebacks[0]
	postKnowledgeResults(t, daemonID, map[string]any{"writebacks": []map[string]any{{
		"event_id": remove.EventID, "entry_id": entry.ID, "ok": true,
	}}})

	deleted := knowledgeEntryByKey(t, candidate, "reviewed")
	if deleted.MirrorState != "source_deleted" || deleted.WritebackState != "succeeded" || deleted.MissingConfirmation != 2 {
		t.Fatalf("delete receipt did not update source projection: %+v", deleted)
	}
	versionID := currentKnowledgeVersionID(t, entry.ID)
	testutil.Call(t, testHandler.PostKnowledgeEntryRestore,
		withURLParam(newRequest(http.MethodPost, "/api/knowledge/entries/"+entry.ID+"/restore", map[string]any{
			"version_id": versionID, "reason": "Owner restored the audited version", "evidence_ref": scanEvidence, "writeback": true,
		}), "id", entry.ID)).Want(http.StatusAccepted)

	plan = knowledgePlan(t, daemonID)
	if len(plan.Workspaces) != 1 || len(plan.Workspaces[0].Writebacks) != 1 {
		t.Fatalf("restore plan = %+v, want one job", plan)
	}
	restore := plan.Workspaces[0].Writebacks[0]
	if restore.Operation != "restore" || restore.Content != entry.Content {
		t.Fatalf("restore plan = %+v", restore)
	}
	postKnowledgeResults(t, daemonID, map[string]any{"writebacks": []map[string]any{{
		"event_id": restore.EventID, "entry_id": entry.ID, "ok": true,
	}}})

	updated := knowledgeEntryByKey(t, candidate, "reviewed")
	if updated.LifecycleState != "active" || updated.WritebackState != "succeeded" || updated.CurrentVersion != entry.CurrentVersion+1 || updated.Content != entry.Content {
		t.Fatalf("restore receipt did not create an active new version: %+v", updated)
	}
}

func TestValidKnowledgeEvidenceRef(t *testing.T) {
	for _, tc := range []struct {
		ref  string
		want bool
	}{
		{ref: "issue:00000000-0000-0000-0000-000000000001", want: true},
		{ref: "pr:00000000-0000-0000-0000-000000000001", want: true},
		{ref: "code:pr:00000000-0000-0000-0000-000000000001@aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", want: true},
		{ref: "run:00000000-0000-0000-0000-000000000001", want: true},
		{ref: "knowledge_scan_batch:00000000-0000-0000-0000-000000000001", want: true},
		{ref: "", want: false},
		{ref: "x:y", want: false},
		{ref: "review note", want: false},
		{ref: "issue:RUYI-916", want: false},
		{ref: "https://example.test/issues/916", want: false},
		{ref: "code:pr:00000000-0000-0000-0000-000000000001@short", want: false},
		{ref: ":missing-prefix", want: false},
		{ref: "issue:", want: false},
	} {
		if got := validKnowledgeEvidenceRef(tc.ref); got != tc.want {
			t.Errorf("validKnowledgeEvidenceRef(%q) = %t, want %t", tc.ref, got, tc.want)
		}
	}
}

func TestKnowledgeLifecycleCandidatesRequireEvidenceAndDoNotAlterMirrorContent(t *testing.T) {
	if testHandler == nil {
		t.Fatal("database fixture is required")
	}
	const daemonID = "daemon-under-test"

	candidate := registerKnowledgeDir(t, "candidate_cli", "/tmp/lifecycle-candidates")
	postKnowledgeResults(t, daemonID, map[string]any{
		"scans": []map[string]any{{
			"dir_id": candidate, "trigger_source": "initial", "ok": true,
			"memories": map[string]string{"reviewed": "owner-reviewed content"},
		}},
	})
	entry := knowledgeEntryByKey(t, candidate, "reviewed")
	versionID := currentKnowledgeVersionID(t, entry.ID)
	scanEvidence := "knowledge_scan_batch:" + knowledgeScanBatchID(t, candidate)

	testutil.Call(t, testHandler.PostKnowledgeEntryLifecycle,
		withURLParam(newRequest(http.MethodPost, "/api/knowledge/entries/"+entry.ID+"/lifecycle", map[string]any{
			"state": "needs_review", "reason": "evidence is still being collected",
		}), "id", entry.ID)).Want(http.StatusBadRequest)
	testutil.Call(t, testHandler.PostKnowledgeEntryRestore,
		withURLParam(newRequest(http.MethodPost, "/api/knowledge/entries/"+entry.ID+"/restore", map[string]any{
			"version_id": versionID, "reason": "Owner requested a source restoration review",
		}), "id", entry.ID)).Want(http.StatusBadRequest)

	testutil.Call(t, testHandler.PostKnowledgeEntryLifecycle,
		withURLParam(newRequest(http.MethodPost, "/api/knowledge/entries/"+entry.ID+"/lifecycle", map[string]any{
			"state": "needs_review", "reason": "evidence is still being collected", "evidence_ref": scanEvidence,
		}), "id", entry.ID)).Want(http.StatusAccepted)
	afterLifecycle := knowledgeEntryByKey(t, candidate, "reviewed")
	if afterLifecycle.Content != entry.Content || afterLifecycle.ContentSha256 != entry.ContentSha256 || afterLifecycle.CurrentVersion != entry.CurrentVersion || afterLifecycle.MirrorState != entry.MirrorState || afterLifecycle.LifecycleState != "needs_review" {
		t.Fatalf("lifecycle candidate altered source mirror projection: before=%+v after=%+v", entry, afterLifecycle)
	}

	testutil.Call(t, testHandler.PostKnowledgeEntryRestore,
		withURLParam(newRequest(http.MethodPost, "/api/knowledge/entries/"+entry.ID+"/restore", map[string]any{
			"version_id": versionID, "reason": "Owner requested a source restoration review", "evidence_ref": scanEvidence,
		}), "id", entry.ID)).Want(http.StatusAccepted)
	afterRestore := knowledgeEntryByKey(t, candidate, "reviewed")
	if afterRestore.Content != entry.Content || afterRestore.ContentSha256 != entry.ContentSha256 || afterRestore.CurrentVersion != entry.CurrentVersion || afterRestore.MirrorState != entry.MirrorState || afterRestore.LifecycleState != "needs_review" {
		t.Fatalf("restore candidate altered source mirror projection: before=%+v after=%+v", entry, afterRestore)
	}

	var operation, evidenceRef string
	if err := testPool.QueryRow(context.Background(), `
		SELECT operation, evidence_ref
		FROM knowledge_lifecycle_event
		WHERE entry_id = $1 AND operation = 'restore_candidate'`, entry.ID).Scan(&operation, &evidenceRef); err != nil {
		t.Fatalf("read restore candidate audit event: %v", err)
	}
	if operation != "restore_candidate" || evidenceRef != scanEvidence {
		t.Fatalf("restore candidate audit event: operation=%q evidence_ref=%q", operation, evidenceRef)
	}
}

func TestKnowledgeLifecycleEvidenceReferencesAreScopedAndResolvable(t *testing.T) {
	if testHandler == nil {
		t.Fatal("database fixture is required")
	}
	const daemonID = "daemon-under-test"
	const sha = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	candidate := registerKnowledgeDir(t, "candidate_cli", "/tmp/lifecycle-evidence-contract")
	postKnowledgeResults(t, daemonID, map[string]any{
		"scans": []map[string]any{{
			"dir_id": candidate, "trigger_source": "initial", "ok": true,
			"memories": map[string]string{"reviewed": "owner-reviewed content"},
		}},
	})
	entry := knowledgeEntryByKey(t, candidate, "reviewed")
	versionID := currentKnowledgeVersionID(t, entry.ID)
	issueID := dbfx.Issue(t, "knowledge evidence fixture")
	prID := dbfx.Insert(t, "github_pull_request", testutil.Cols{
		"workspace_id":    testWorkspaceID,
		"installation_id": 1,
		"repo_owner":      "multica-ai",
		"repo_name":       "knowledge-evidence",
		"pr_number":       916,
		"title":           "Knowledge evidence fixture",
		"state":           "open",
		"html_url":        "https://example.test/pull/916",
		"pr_created_at":   testutil.Raw("now()"),
		"pr_updated_at":   testutil.Raw("now()"),
		"head_sha":        sha,
	})
	agentID := dbfx.Agent(t, "knowledge evidence run", handlerTestRuntimeID(t))
	runID := dbfx.Task(t, agentID, testutil.Cols{
		"runtime_id": handlerTestRuntimeID(t),
		"issue_id":   issueID,
	})
	scanEvidence := "knowledge_scan_batch:" + knowledgeScanBatchID(t, candidate)

	transition := func(ref string) {
		t.Helper()
		testutil.Call(t, testHandler.PostKnowledgeEntryLifecycle,
			withURLParam(newRequest(http.MethodPost, "/api/knowledge/entries/"+entry.ID+"/lifecycle", map[string]any{
				"state": "needs_review", "reason": "evidence contract regression", "evidence_ref": ref,
			}), "id", entry.ID)).Want(http.StatusAccepted)
	}
	transition("issue:" + issueID)
	transition("pr:" + prID)
	transition("code:pr:" + prID + "@" + strings.ToUpper(sha))
	transition("run:" + runID)
	transition(scanEvidence)

	testutil.Call(t, testHandler.PostKnowledgeEntryRestore,
		withURLParam(newRequest(http.MethodPost, "/api/knowledge/entries/"+entry.ID+"/restore", map[string]any{
			"version_id": versionID, "reason": "restore evidence contract regression", "evidence_ref": "pr:" + prID,
		}), "id", entry.ID)).Want(http.StatusAccepted)

	foreignWorkspaceID := dbfx.Workspace(t, "Foreign evidence workspace", handlerTestSlug("foreign-evidence"))
	foreignIssueID := dbfx.Issue(t, "foreign evidence fixture", testutil.Cols{"workspace_id": foreignWorkspaceID})
	for _, ref := range []string{
		"x:y",
		"issue:00000000-0000-0000-0000-000000000001",
		"issue:" + foreignIssueID,
	} {
		testutil.Call(t, testHandler.PostKnowledgeEntryLifecycle,
			withURLParam(newRequest(http.MethodPost, "/api/knowledge/entries/"+entry.ID+"/lifecycle", map[string]any{
				"state": "needs_review", "reason": "must reject inaccessible evidence", "evidence_ref": ref,
			}), "id", entry.ID)).Want(http.StatusBadRequest)
	}

	var stored string
	dbfx.QueryRow(t, `
		SELECT evidence_ref FROM knowledge_lifecycle_event
		WHERE entry_id = $1 AND evidence_ref LIKE 'code:pr:%'
		ORDER BY created_at DESC LIMIT 1`, entry.ID).Scan(&stored)
	if stored != "code:pr:"+prID+"@"+sha {
		t.Fatalf("stored code evidence_ref = %q, want canonical SHA reference", stored)
	}
}

// TestKnowledgeAdoptionStateMachine pins the async adoption: queue-time
// guards (no ultimate → 409, already adopted → 409, in-flight → 409), the
// plan carrying the pending job, and the callback landing adopted-with-
// provenance or failed-with-reason. Stale callbacks stay inert.
func TestKnowledgeAdoptionStateMachine(t *testing.T) {
	if testHandler == nil {
		t.Fatal("database fixture is required")
	}
	const daemonID = "daemon-under-test"

	ultimate := registerKnowledgeDir(t, "ultimate", "/tmp/ultimate-state-machine")
	candidate := registerKnowledgeDir(t, "candidate_cli", "/tmp/candidate-state-machine")
	postKnowledgeResults(t, daemonID, map[string]any{
		"scans": []map[string]any{{
			"dir_id": candidate, "trigger_source": "initial", "ok": true,
			"memories": map[string]string{"flaky": "content worth retrying"},
		}},
	})
	entry := knowledgeEntryByKey(t, candidate, "flaky")

	// No-ultimate refusal: drop the ultimate row temporarily.
	testPool.Exec(context.Background(), `UPDATE knowledge_dir SET removed = TRUE WHERE id = $1`, ultimate)
	if code := adoptEntry(t, entry.ID); code != http.StatusConflict {
		t.Fatalf("adopt without ultimate: %d, want 409", code)
	}
	if state := knowledgeEntryByKey(t, candidate, "flaky").AdoptionState; state != "pending" {
		t.Fatalf("refused adoption must not move the entry: %q", state)
	}
	testPool.Exec(context.Background(), `UPDATE knowledge_dir SET removed = FALSE WHERE id = $1`, ultimate)

	// Queueing succeeds and shows in the daemon plan with the ultimate.
	if code := adoptEntry(t, entry.ID); code != http.StatusAccepted {
		t.Fatalf("first adopt: %d, want 202", code)
	}
	if code := adoptEntry(t, entry.ID); code != http.StatusConflict {
		t.Fatalf("second adopt while transferring: %d, want 409", code)
	}
	plan := knowledgePlan(t, daemonID)
	var job *KnowledgePlanAdopt
	for i := range plan.Workspaces {
		if plan.Workspaces[i].WorkspaceID != testWorkspaceID {
			continue
		}
		for j := range plan.Workspaces[i].Adoptions {
			if plan.Workspaces[i].Adoptions[j].ID == entry.ID {
				job = &plan.Workspaces[i].Adoptions[j]
			}
		}
	}
	if job == nil || job.Key != "flaky" || job.UltimateDirID != ultimate {
		t.Fatalf("pending adoption missing from plan: %+v", job)
	}

	// Failure callback lands 'failed' with the reason; retry queues again.
	postKnowledgeResults(t, daemonID, map[string]any{
		"adoptions": []map[string]any{{"kind": "entry", "id": entry.ID, "ok": false, "error": "bd write refused"}},
	})
	failed := knowledgeEntryByKey(t, candidate, "flaky")
	if failed.AdoptionState != "failed" || !strings.Contains(failed.AdoptionError, "bd write refused") {
		t.Fatalf("after failed callback: state=%q error=%q", failed.AdoptionState, failed.AdoptionError)
	}
	if code := adoptEntry(t, entry.ID); code != http.StatusAccepted {
		t.Fatalf("retry after failure: %d, want 202", code)
	}

	// Success callback lands adopted with provenance; a stale repeat is inert.
	postKnowledgeResults(t, daemonID, map[string]any{
		"adoptions": []map[string]any{{
			"kind": "entry", "id": entry.ID, "ok": true, "ultimate_dir_id": ultimate,
		}},
	})
	adopted := knowledgeEntryByKey(t, candidate, "flaky")
	if adopted.AdoptionState != "adopted" || adopted.UltimateDirID == nil || *adopted.UltimateDirID != ultimate {
		t.Fatalf("after success callback: state=%q ultimate=%v", adopted.AdoptionState, adopted.UltimateDirID)
	}
	if adopted.AdoptedFromDirID == nil || *adopted.AdoptedFromDirID != candidate || adopted.AdoptedFromKey == nil || *adopted.AdoptedFromKey != "flaky" {
		t.Fatalf("adopted provenance: from=%v key=%v", adopted.AdoptedFromDirID, adopted.AdoptedFromKey)
	}
	postKnowledgeResults(t, daemonID, map[string]any{
		"adoptions": []map[string]any{{"kind": "entry", "id": entry.ID, "ok": false, "error": "stale"}},
	})
	if state := knowledgeEntryByKey(t, candidate, "flaky").AdoptionState; state != "adopted" {
		t.Fatalf("stale callback moved a terminal entry: %q", state)
	}
	if code := adoptEntry(t, entry.ID); code != http.StatusConflict {
		t.Fatalf("adopt after adopted: %d, want 409", code)
	}
}
