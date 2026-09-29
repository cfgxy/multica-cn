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
		testPool.Exec(ctx, `DELETE FROM knowledge_entry WHERE dir_id = ANY($1::uuid[])`, ids)
		testPool.Exec(ctx, `DELETE FROM knowledge_scan_batch WHERE dir_id = ANY($1::uuid[])`, ids)
		testPool.Exec(ctx, `DELETE FROM knowledge_dir WHERE id = ANY($1::uuid[])`, ids)
	})
	if created.Status != "queued for first scan" {
		t.Fatalf("register %s dir: status=%q, want queued for first scan", kind, created.Status)
	}
	return created.ID
}

// cleanupSystemProposals removes the pool rows auto-discovery created during
// the test (keyed on the generation snapshot's knowledge_dir_id).
func cleanupSystemProposals(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		testPool.Exec(context.Background(),
			`DELETE FROM proposal WHERE workspace_id = $1 AND created_by_type = 'system'`, testWorkspaceID)
	})
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

// TestKnowledgeResultsLandsScans pins the diff pipeline: a daemon-reported
// memories map becomes added/updated/source_deleted mirror rows and a faithful
// batch log; unchanged and failed reports land as noop/failed batches; the
// queue flags clear; and reports for another daemon's directory are refused.
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

	// Source evolves: a edited, c added, b deleted.
	postKnowledgeResults(t, daemonID, map[string]any{
		"scans": []map[string]any{{
			"dir_id": candidate, "trigger_source": "scheduled", "ok": true,
			"memories": map[string]string{"a": "one-edited", "c": "three"},
		}},
	})
	if batch := latestScanBatch(t, candidate); batch.Result != "changed" || batch.Added != 1 || batch.Updated != 1 || batch.Removed != 1 {
		t.Fatalf("changed scan batch: %+v, want 1/1/1", batch)
	}
	if deleted := knowledgeEntryByKey(t, candidate, "b"); deleted.MirrorState != "source_deleted" {
		t.Fatalf("vanished key b: mirror_state=%q, want source_deleted", deleted.MirrorState)
	}
	if edited := knowledgeEntryByKey(t, candidate, "a"); edited.Content != "one-edited" {
		t.Fatalf("edited key a: content=%q", edited.Content)
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

// TestKnowledgeSystemProposalOncePerDirectory pins §4: the first scan of a
// newly discovered candidate_auto source with entries creates exactly one
// system proposal with a B1-valid prophecy and source evidence; later scans,
// and ultimate/candidate_cli kinds, never create one.
func TestKnowledgeSystemProposalOncePerDirectory(t *testing.T) {
	if testHandler == nil {
		t.Fatal("database fixture is required")
	}
	cleanupSystemProposals(t)
	const daemonID = "daemon-under-test"

	discovered := postKnowledgeResults(t, daemonID, map[string]any{
		"discoveries": []map[string]any{{
			"workspace_id": testWorkspaceID, "path": "/tmp/project-alpha", "label": "project-alpha",
		}},
	})
	if discovered["discoveries_registered"].(float64) != 1 {
		t.Fatalf("discovery not registered: %v", discovered)
	}
	var dirID string
	if err := testPool.QueryRow(context.Background(), `
SELECT id FROM knowledge_dir WHERE workspace_id = $1 AND path = '/tmp/project-alpha'`,
		testWorkspaceID).Scan(&dirID); err != nil {
		t.Fatalf("discovered dir missing: %v", err)
	}
	testPool.Exec(context.Background(), `UPDATE knowledge_dir SET daemon_id = $2 WHERE id = $1`, dirID, daemonID)
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM knowledge_dir WHERE id = $1`, dirID)
	})

	// Re-reporting the same discovery must not duplicate the row.
	again := postKnowledgeResults(t, daemonID, map[string]any{
		"discoveries": []map[string]any{{
			"workspace_id": testWorkspaceID, "path": "/tmp/project-alpha", "label": "project-alpha",
		}},
	})
	if again["discoveries_registered"].(float64) != 0 {
		t.Fatalf("duplicate discovery registered: %v", again)
	}

	postKnowledgeResults(t, daemonID, map[string]any{
		"scans": []map[string]any{{
			"dir_id": dirID, "trigger_source": "initial", "ok": true,
			"memories": map[string]string{"k1": "one", "k2": "two"},
		}},
	})
	var proposals []ProposalResponse
	testutil.Call(t, testHandler.GetProposals, newRequest(http.MethodGet, "/api/proposals", nil)).
		Want(http.StatusOK).JSON(&proposals)
	var system []ProposalResponse
	for _, proposal := range proposals {
		if proposal.CreatedByType == "system" {
			system = append(system, proposal)
		}
	}
	if len(system) != 1 {
		t.Fatalf("system proposals after first scan: %d, want exactly 1", len(system))
	}
	created := system[0]
	if created.Type != "project_cognition" {
		t.Fatalf("system proposal type: %q", created.Type)
	}
	if created.Prophecy["outcome_text"] == "" || created.Prophecy["falsify_condition"] == "" {
		t.Fatalf("system proposal prophecy is not B1-valid: %v", created.Prophecy)
	}
	if created.GenerationSnapshot["knowledge_dir_id"] != dirID {
		t.Fatalf("generation snapshot must name the source dir: %v", created.GenerationSnapshot)
	}
	evidence, _ := created.Evidence[0].(map[string]any)
	if evidence["path"] != "/tmp/project-alpha" || evidence["entry_count"].(float64) != 2 {
		t.Fatalf("system proposal evidence: %v", evidence)
	}

	// A later changed scan must not create a second proposal.
	postKnowledgeResults(t, daemonID, map[string]any{
		"scans": []map[string]any{{
			"dir_id": dirID, "trigger_source": "scheduled", "ok": true,
			"memories": map[string]string{"k1": "one", "k2": "two", "k3": "three"},
		}},
	})
	proposals = nil
	testutil.Call(t, testHandler.GetProposals, newRequest(http.MethodGet, "/api/proposals", nil)).
		Want(http.StatusOK).JSON(&proposals)
	system = system[:0]
	for _, proposal := range proposals {
		if proposal.CreatedByType == "system" {
			system = append(system, proposal)
		}
	}
	if len(system) != 1 {
		t.Fatalf("system proposals after second scan: %d, want still 1", len(system))
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
