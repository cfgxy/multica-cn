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
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/knowledgegovernance"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/internal/util"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// --- shared fixtures ---------------------------------------------------------

type failNextGovernanceBegin struct {
	*pgxpool.Pool
	fail bool
}

func (db *failNextGovernanceBegin) Begin(ctx context.Context) (pgx.Tx, error) {
	if db.fail {
		db.fail = false
		return nil, errors.New("transient report persistence failure")
	}
	return db.Pool.Begin(ctx)
}

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

func registerKnowledgeUltimate(t *testing.T, daemonID, path string) string {
	t.Helper()
	grantKnowledgeUltimatePath(t, daemonID, path)
	return registerKnowledgeDir(t, "ultimate", path)
}

func claimKnowledgeUltimate(t *testing.T, daemonID, dirID string) {
	t.Helper()
	dir := knowledgeDirState(t, dirID)
	ack := postKnowledgeResults(t, daemonID, map[string]any{
		"ultimates": []map[string]any{{
			"workspace_id": testWorkspaceID,
			"path":         dir.Path,
		}},
	})
	if ack["ultimates_registered"].(float64) != 1 {
		t.Fatalf("ultimate claim acknowledgment: %v", ack)
	}
}

func grantKnowledgeUltimatePath(t *testing.T, daemonID, path string) {
	t.Helper()
	projectID := dbfx.Project(t, "knowledge ultimate path authorization")
	dbfx.Insert(t, "project_resource", testutil.Cols{
		"project_id":    projectID,
		"workspace_id":  testWorkspaceID,
		"resource_type": "local_directory",
		"resource_ref":  `{"daemon_id":"` + daemonID + `","local_path":"` + path + `","execution_mode":"in_place"}`,
		"position":      0,
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

func TestKnowledgeGovernancePersistReportFailureFinishesRun(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Fatal("database fixture is required")
	}
	ctx := context.Background()
	dirID := registerKnowledgeDir(t, "candidate_cli", t.TempDir())

	var agentID string
	err := testPool.QueryRow(ctx, `
		SELECT agent_id::text FROM retrospective_config
		WHERE workspace_id = $1 AND enabled = TRUE LIMIT 1`, testWorkspaceID).Scan(&agentID)
	if errors.Is(err, pgx.ErrNoRows) {
		dbfx.QueryRow(t, `SELECT id::text FROM agent WHERE workspace_id = $1 ORDER BY created_at LIMIT 1`, testWorkspaceID).Scan(&agentID)
		dbfx.InsertNoID(t, "retrospective_config", testutil.Cols{
			"workspace_id": testWorkspaceID, "enabled": true, "include_in_review": false, "window_days": 7, "agent_id": agentID,
		}, "workspace_id = $1", testWorkspaceID)
	} else if err != nil {
		t.Fatalf("read governance execution agent: %v", err)
	}

	runner := &knowledgegovernance.Runner{
		DB:      testPool,
		Queries: testHandler.Queries,
		Enqueue: testHandler.KnowledgeGovernanceTaskEnqueuer(),
	}
	started, err := runner.RunWorkspace(ctx, testWorkspaceID, dirID, testUserID, "manual")
	if err != nil {
		t.Fatalf("start governance run: %v", err)
	}
	taskID := util.MustParseUUID(started.TaskID)
	task, err := testHandler.Queries.GetAgentTask(ctx, taskID)
	if err != nil {
		t.Fatalf("read governance task: %v", err)
	}
	output := `{"findings":[],"backup_reference":"no writeback authorized"}`
	result, err := json.Marshal(protocol.TaskCompletedPayload{TaskID: started.TaskID, Output: output})
	if err != nil {
		t.Fatalf("marshal completed task result: %v", err)
	}
	if _, err := testPool.Exec(ctx, `UPDATE agent_task_queue SET status = 'completed', result = $1::jsonb WHERE id = $2`, result, taskID); err != nil {
		t.Fatalf("complete governance task: %v", err)
	}
	task.Status = "completed"
	task.Result = result

	failingRunner := &knowledgegovernance.Runner{
		DB:      &failNextGovernanceBegin{Pool: testPool, fail: true},
		Queries: testHandler.Queries,
	}
	settled, err := failingRunner.ProcessTaskTerminal(ctx, task, output, "")
	if err == nil || settled == nil || settled.Status != "failed" {
		t.Fatalf("persist failure settlement stats=%+v err=%v, want failed run and diagnostic error", settled, err)
	}
	var status, reason string
	if err := testPool.QueryRow(ctx, `
		SELECT status, report->>'reason' FROM knowledge_governance_run WHERE id = $1`, started.RunID).Scan(&status, &reason); err != nil {
		t.Fatalf("read failed governance run: %v", err)
	}
	if status != "failed" || reason != "report_persistence_failed" {
		t.Fatalf("persist failure run status=%q reason=%q, want failed/report_persistence_failed", status, reason)
	}
	if _, err := runner.RunWorkspace(ctx, testWorkspaceID, dirID, testUserID, "retry"); err != nil {
		t.Fatalf("failed run must release directory scope for retry: %v", err)
	}
}

func knowledgePlan(t *testing.T, daemonID string) KnowledgePlanResponse {
	t.Helper()
	var plan KnowledgePlanResponse
	testutil.Call(t, testHandler.GetKnowledgePlan,
		newRequest(http.MethodGet, "/api/daemon/knowledge/plan?daemon_id="+daemonID, nil).WithContext(
			middleware.WithDaemonContext(context.Background(), testWorkspaceID, daemonID))).
		Want(http.StatusOK).JSON(&plan)
	return plan
}

func postKnowledgeResults(t *testing.T, daemonID string, body map[string]any) map[string]any {
	t.Helper()
	var ack map[string]any
	testutil.Call(t, testHandler.PostKnowledgeResults,
		newRequest(http.MethodPost, "/api/daemon/knowledge/results?daemon_id="+daemonID, body).WithContext(
			middleware.WithDaemonContext(context.Background(), testWorkspaceID, daemonID))).
		Want(http.StatusOK).JSON(&ack)
	return ack
}

func postKnowledgeResultsAsUser(t *testing.T, daemonID string, body map[string]any) {
	t.Helper()
	testutil.Call(t, testHandler.PostKnowledgeResults,
		newRequest(http.MethodPost, "/api/daemon/knowledge/results?daemon_id="+daemonID, body)).
		Want(http.StatusUnauthorized)
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
	ultimatePath := t.TempDir()
	testutil.Call(t, testHandler.PostKnowledgeDir, newRequest(http.MethodPost, "/api/knowledge/dirs",
		map[string]any{"kind": "ultimate", "path": ultimatePath})).Want(http.StatusConflict)
	registerKnowledgeUltimate(t, "registration-daemon", ultimatePath)
	secondUltimatePath := t.TempDir()
	grantKnowledgeUltimatePath(t, "second-registration-daemon", secondUltimatePath)
	testutil.Call(t, testHandler.PostKnowledgeDir, newRequest(http.MethodPost, "/api/knowledge/dirs",
		map[string]any{"kind": "ultimate", "path": secondUltimatePath})).Want(http.StatusConflict)

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

// TestKnowledgeAdoptionRefusesUnboundUltimate keeps both manual-target entry
// points executable: a new target must have a registered daemon path, and a
// legacy unbound target must not turn an adoption into an unconsumable job.
func TestKnowledgeAdoptionRefusesUnboundUltimate(t *testing.T) {
	if testHandler == nil {
		t.Fatal("database fixture is required")
	}
	const daemonID = "daemon-without-ultimate-host"
	const ultimatePath = "/tmp/ultimate-without-registered-host"

	// The management API rejects an ultimate path with no daemon resource.
	testutil.Call(t, testHandler.PostKnowledgeDir, newRequest(http.MethodPost, "/api/knowledge/dirs",
		map[string]any{"kind": "ultimate", "path": ultimatePath})).Want(http.StatusConflict)

	// An older unbound row may exist from before that validation. It remains
	// visible, but cannot strand an adoption because no daemon can consume it.
	dbfx.Insert(t, "knowledge_dir", testutil.Cols{
		"workspace_id": testWorkspaceID,
		"kind":         "ultimate",
		"path":         ultimatePath,
		"created_by":   testUserID,
	})
	candidate := registerKnowledgeDir(t, "candidate_cli", "/tmp/candidate-without-ultimate-host")
	postKnowledgeResults(t, daemonID, map[string]any{
		"scans": []map[string]any{{
			"dir_id": candidate, "trigger_source": "initial", "ok": true,
			"memories": map[string]string{"unbound-target": "must remain pending"},
		}},
	})
	entry := knowledgeEntryByKey(t, candidate, "unbound-target")
	if code := adoptEntry(t, entry.ID); code != http.StatusConflict {
		t.Fatalf("adopt against unbound ultimate: %d, want 409", code)
	}
	if state := knowledgeEntryByKey(t, candidate, "unbound-target").AdoptionState; state != "pending" {
		t.Fatalf("unbound ultimate stranded adoption: state=%q, want pending", state)
	}
}

// TestKnowledgePlanHandsWorkToDaemon pins the plan contents: hosted +
// unclaimed directories with their mirror SHA map, a daemon-bound ultimate,
// and — after a queue-time adoption — the pending transfer job.
func TestKnowledgePlanHandsWorkToDaemon(t *testing.T) {
	if testHandler == nil {
		t.Fatal("database fixture is required")
	}
	const daemonID = "daemon-under-test"

	ultimate := registerKnowledgeUltimate(t, daemonID, "/tmp/ultimate-under-test")
	candidate := registerKnowledgeDir(t, "candidate_cli", "/tmp/candidate-under-test")
	ultimatePath := knowledgeDirState(t, ultimate).Path

	// 只有已登记的精确路径归属 daemon 才能看到或认领未绑定 ultimate。
	unboundPlan := knowledgePlan(t, daemonID)
	for i := range unboundPlan.Workspaces {
		ws := &unboundPlan.Workspaces[i]
		if ws.WorkspaceID != testWorkspaceID {
			continue
		}
		if !ws.HasUltimate || ws.Ultimate != nil || ws.UnboundUltimate == nil || ws.UnboundUltimate.DirID != ultimate {
			t.Fatalf("unbound plan ultimate: has=%v ultimate=%+v claim=%+v, want claimable target", ws.HasUltimate, ws.Ultimate, ws.UnboundUltimate)
		}
	}
	foreignPlan := knowledgePlan(t, "foreign-daemon")
	for i := range foreignPlan.Workspaces {
		ws := &foreignPlan.Workspaces[i]
		if ws.WorkspaceID == testWorkspaceID && ws.UnboundUltimate != nil {
			t.Fatalf("foreign daemon received an unbound ultimate: %+v", ws.UnboundUltimate)
		}
	}
	foreignAck := postKnowledgeResults(t, "foreign-daemon", map[string]any{
		"ultimates": []map[string]any{{
			"workspace_id": testWorkspaceID,
			"path":         ultimatePath,
		}},
	})
	if foreignAck["ultimates_registered"].(float64) != 0 {
		t.Fatalf("foreign daemon claimed unbound ultimate: %v", foreignAck)
	}
	postKnowledgeResultsAsUser(t, daemonID, map[string]any{
		"ultimates": []map[string]any{{
			"workspace_id": testWorkspaceID,
			"path":         ultimatePath,
		}},
	})
	claimKnowledgeUltimate(t, daemonID, ultimate)
	foreignAck = postKnowledgeResults(t, "foreign-daemon", map[string]any{
		"ultimates": []map[string]any{{
			"workspace_id": testWorkspaceID,
			"path":         ultimatePath,
		}},
	})
	if foreignAck["ultimates_registered"].(float64) != 0 {
		t.Fatalf("foreign daemon claimed bound ultimate: %v", foreignAck)
	}

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
	entry := knowledgeEntryByKey(t, candidate, "tip")
	if code := adoptEntry(t, entry.ID); code != http.StatusAccepted {
		t.Fatalf("queue adoption after authorized claim: %d, want 202", code)
	}
	var adoptionJob *KnowledgePlanAdopt
	adoptionPlan := knowledgePlan(t, daemonID)
	for i := range adoptionPlan.Workspaces {
		workspace := &adoptionPlan.Workspaces[i]
		if workspace.WorkspaceID != testWorkspaceID {
			continue
		}
		for j := range workspace.Adoptions {
			if workspace.Adoptions[j].ID == entry.ID {
				adoptionJob = &workspace.Adoptions[j]
			}
		}
	}
	if adoptionJob == nil || adoptionJob.UltimateDirID != ultimate || adoptionJob.UltimatePath != ultimatePath {
		t.Fatalf("authorized daemon missing adoption job: %+v", adoptionJob)
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

	// 已确认删除的 key 必须单独传给 daemon：即使源内容 SHA 未变，重现
	// 也需要服务端处理以恢复镜像投影并留下审计事件。
	testPool.Exec(context.Background(), `
		UPDATE knowledge_entry SET mirror_state = 'source_deleted', missing_confirmation = 2
		WHERE dir_id = $1 AND key = 'tip'`, candidate)
	plan = knowledgePlan(t, daemonID)
	for i := range plan.Workspaces {
		if plan.Workspaces[i].WorkspaceID == testWorkspaceID {
			ws = &plan.Workspaces[i]
		}
	}
	for i := range ws.Dirs {
		if ws.Dirs[i].DirID == candidate {
			planned = &ws.Dirs[i]
		}
	}
	if planned == nil || !planned.SourceDeleted["tip"] {
		t.Fatalf("计划缺少 source_deleted key：%+v", planned)
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

	ultimate := registerKnowledgeUltimate(t, daemonID, "/tmp/ultimate-not-a-scan-target")
	candidate := registerKnowledgeDir(t, "candidate_cli", "/tmp/ultimate-fix-candidate")
	claimKnowledgeUltimate(t, daemonID, ultimate)

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

	ultimate := registerKnowledgeUltimate(t, daemonID, "/tmp/ultimate-scan-guard")

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

	// 同 SHA 的重新出现只恢复镜像状态，不增加不可变版本，但必须留下一条
	// source_restored 审计事件。
	postKnowledgeResults(t, daemonID, map[string]any{
		"scans": []map[string]any{{
			"dir_id": candidate, "trigger_source": "scheduled", "ok": true,
			"memories": map[string]string{"a": "one-edited", "b": "two", "c": "three"},
		}},
	})
	if restored := knowledgeEntryByKey(t, candidate, "b"); restored.MirrorState != "synced" || restored.MissingConfirmation != 0 || restored.CurrentVersion != 1 {
		t.Fatalf("同 SHA 恢复结果=%+v，期望 synced/0/version 1", restored)
	}
	var versionCount, restoredEvents int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FROM knowledge_entry_version
		WHERE entry_id = (SELECT id FROM knowledge_entry WHERE dir_id = $1 AND key = 'b')`, candidate).Scan(&versionCount); err != nil {
		t.Fatalf("读取同 SHA 恢复版本数：%v", err)
	}
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FROM knowledge_lifecycle_event
		WHERE entry_id = (SELECT id FROM knowledge_entry WHERE dir_id = $1 AND key = 'b')
		  AND operation = 'source_restored'`, candidate).Scan(&restoredEvents); err != nil {
		t.Fatalf("读取同 SHA 恢复事件：%v", err)
	}
	if versionCount != 1 || restoredEvents != 1 {
		t.Fatalf("同 SHA 恢复版本数=%d、事件数=%d，期望 1/1", versionCount, restoredEvents)
	}

	// 再次连续缺失后，以改变的内容恢复：该路径必须创建新版本，同时同样
	// 只记录一条 source_restored 事件。
	for i := 0; i < 2; i++ {
		postKnowledgeResults(t, daemonID, map[string]any{
			"scans": []map[string]any{{
				"dir_id": candidate, "trigger_source": "scheduled", "ok": true,
				"memories": map[string]string{"a": "one-edited", "c": "three"},
			}},
		})
	}
	if deleted := knowledgeEntryByKey(t, candidate, "b"); deleted.MirrorState != "source_deleted" || deleted.MissingConfirmation < 2 {
		t.Fatalf("第二次确认删除结果：%+v", deleted)
	}
	postKnowledgeResults(t, daemonID, map[string]any{
		"scans": []map[string]any{{
			"dir_id": candidate, "trigger_source": "scheduled", "ok": true,
			"memories": map[string]string{"a": "one-edited", "b": "two edited", "c": "three"},
		}},
	})
	if restored := knowledgeEntryByKey(t, candidate, "b"); restored.MirrorState != "synced" || restored.MissingConfirmation != 0 || restored.CurrentVersion != 2 || restored.Content != "two edited" {
		t.Fatalf("变更 SHA 恢复结果=%+v，期望 synced/0/version 2/new content", restored)
	}
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FROM knowledge_entry_version
		WHERE entry_id = (SELECT id FROM knowledge_entry WHERE dir_id = $1 AND key = 'b')`, candidate).Scan(&versionCount); err != nil {
		t.Fatalf("读取变更 SHA 恢复版本数：%v", err)
	}
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FROM knowledge_lifecycle_event
		WHERE entry_id = (SELECT id FROM knowledge_entry WHERE dir_id = $1 AND key = 'b')
		  AND operation = 'source_restored'`, candidate).Scan(&restoredEvents); err != nil {
		t.Fatalf("读取变更 SHA 恢复事件：%v", err)
	}
	if versionCount != 2 || restoredEvents != 2 {
		t.Fatalf("变更 SHA 恢复版本数=%d、事件数=%d，期望 2/2", versionCount, restoredEvents)
	}
	var restoredVersionContent string
	if err := testPool.QueryRow(context.Background(), `
		SELECT content FROM knowledge_entry_version
		WHERE entry_id = (SELECT id FROM knowledge_entry WHERE dir_id = $1 AND key = 'b')
		  AND version = 2`, candidate).Scan(&restoredVersionContent); err != nil {
		t.Fatalf("读取变更 SHA 恢复版本内容：%v", err)
	}
	if restoredVersionContent != "two edited" {
		t.Fatalf("变更 SHA 恢复版本内容=%q，期望 %q", restoredVersionContent, "two edited")
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

func TestKnowledgeLifecycleReplacementEntryStaysWithinKnowledgeDirectory(t *testing.T) {
	if testHandler == nil {
		t.Fatal("database fixture is required")
	}
	const daemonID = "daemon-under-test"

	candidate := registerKnowledgeDir(t, "candidate_cli", "/tmp/lifecycle-replacement-contract")
	postKnowledgeResults(t, daemonID, map[string]any{
		"scans": []map[string]any{{
			"dir_id": candidate, "trigger_source": "initial", "ok": true,
			"memories": map[string]string{
				"retired":     "outdated deployment guidance",
				"replacement": "current SSH deployment guidance",
			},
		}},
	})
	entry := knowledgeEntryByKey(t, candidate, "retired")
	replacement := knowledgeEntryByKey(t, candidate, "replacement")
	evidenceRef := "knowledge_scan_batch:" + knowledgeScanBatchID(t, candidate)

	for _, state := range []string{"superseded", "duplicate"} {
		testutil.Call(t, testHandler.PostKnowledgeEntryLifecycle,
			withURLParam(newRequest(http.MethodPost, "/api/knowledge/entries/"+entry.ID+"/lifecycle", map[string]any{
				"state": state, "reason": "Owner selected the current guidance", "evidence_ref": evidenceRef,
				"replacement_entry_id": replacement.ID,
			}), "id", entry.ID)).Want(http.StatusAccepted)

		var entryReplacement, eventReplacement string
		if err := testPool.QueryRow(context.Background(), `
			SELECT replacement_entry_id::text FROM knowledge_entry WHERE id = $1`, entry.ID).Scan(&entryReplacement); err != nil {
			t.Fatalf("read %s entry replacement: %v", state, err)
		}
		if err := testPool.QueryRow(context.Background(), `
			SELECT replacement_entry_id::text FROM knowledge_lifecycle_event
			WHERE entry_id = $1 AND to_state = $2 ORDER BY created_at DESC LIMIT 1`, entry.ID, state).Scan(&eventReplacement); err != nil {
			t.Fatalf("read %s event replacement: %v", state, err)
		}
		if entryReplacement != replacement.ID || eventReplacement != replacement.ID {
			t.Fatalf("%s replacement entry=%q event=%q, want %q", state, entryReplacement, eventReplacement, replacement.ID)
		}
	}

	otherDir := registerKnowledgeDir(t, "candidate_cli", "/tmp/lifecycle-replacement-other-dir")
	postKnowledgeResults(t, daemonID, map[string]any{
		"scans": []map[string]any{{
			"dir_id": otherDir, "trigger_source": "initial", "ok": true,
			"memories": map[string]string{"other": "another directory entry"},
		}},
	})
	otherDirEntry := knowledgeEntryByKey(t, otherDir, "other")
	foreignWorkspaceID := dbfx.Workspace(t, "Foreign replacement workspace", handlerTestSlug("foreign-replacement"))
	foreignDirID := dbfx.Insert(t, "knowledge_dir", testutil.Cols{
		"workspace_id": foreignWorkspaceID, "kind": "candidate_cli", "path": "/tmp/foreign-replacement", "label": "foreign",
	})
	foreignEntryID := dbfx.Insert(t, "knowledge_entry", testutil.Cols{
		"workspace_id": foreignWorkspaceID, "dir_id": foreignDirID, "key": "foreign", "content": "foreign guidance",
		"content_sha256": strings.Repeat("f", 64),
	})

	for _, invalidReplacement := range []string{entry.ID, otherDirEntry.ID, foreignEntryID} {
		testutil.Call(t, testHandler.PostKnowledgeEntryLifecycle,
			withURLParam(newRequest(http.MethodPost, "/api/knowledge/entries/"+entry.ID+"/lifecycle", map[string]any{
				"state": "superseded", "reason": "must reject an out-of-scope replacement", "evidence_ref": evidenceRef,
				"replacement_entry_id": invalidReplacement,
			}), "id", entry.ID)).Want(http.StatusBadRequest)
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

	ultimate := registerKnowledgeUltimate(t, daemonID, "/tmp/ultimate-state-machine")
	candidate := registerKnowledgeDir(t, "candidate_cli", "/tmp/candidate-state-machine")
	claimKnowledgeUltimate(t, daemonID, ultimate)
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

	// A failure without the planned ultimate is inert, because the callback
	// cannot prove where the daemon attempted the transfer.
	ack := postKnowledgeResults(t, daemonID, map[string]any{
		"adoptions": []map[string]any{{
			"kind": "entry", "id": entry.ID, "ok": false, "error": "bd write refused",
		}},
	})
	if ack["adoptions_resolved"].(float64) != 0 {
		t.Fatalf("missing ultimate adoption resolved: %v", ack)
	}
	if state := knowledgeEntryByKey(t, candidate, "flaky").AdoptionState; state != "transferring" {
		t.Fatalf("missing ultimate callback moved transferring entry: %q", state)
	}

	// User identity cannot impersonate the daemon for either successful or
	// failed write results; both leave the queued transfer unchanged.
	postKnowledgeResultsAsUser(t, daemonID, map[string]any{
		"adoptions": []map[string]any{{
			"kind": "entry", "id": entry.ID, "ok": false, "ultimate_dir_id": ultimate, "error": "forged user failure",
		}},
	})
	postKnowledgeResultsAsUser(t, daemonID, map[string]any{
		"adoptions": []map[string]any{{
			"kind": "entry", "id": entry.ID, "ok": true, "ultimate_dir_id": ultimate,
		}},
	})
	if state := knowledgeEntryByKey(t, candidate, "flaky").AdoptionState; state != "transferring" {
		t.Fatalf("user callbacks moved transferring entry: %q", state)
	}

	// A daemon not eligible for the target ultimate may not resolve the job.
	ack = postKnowledgeResults(t, "foreign-daemon", map[string]any{
		"adoptions": []map[string]any{{
			"kind": "entry", "id": entry.ID, "ok": false, "ultimate_dir_id": ultimate, "error": "forged failure",
		}},
	})
	if ack["adoptions_resolved"].(float64) != 0 {
		t.Fatalf("foreign daemon adoption resolved: %v", ack)
	}
	if state := knowledgeEntryByKey(t, candidate, "flaky").AdoptionState; state != "transferring" {
		t.Fatalf("foreign callback moved transferring entry: %q", state)
	}

	// A report for the bound ultimate changes the entry to failed and permits a
	// retry.
	ack = postKnowledgeResults(t, daemonID, map[string]any{
		"adoptions": []map[string]any{{
			"kind": "entry", "id": entry.ID, "ok": false, "ultimate_dir_id": ultimate, "error": "bd write refused",
		}},
	})
	if ack["adoptions_resolved"].(float64) != 1 {
		t.Fatalf("failed adoption acknowledgment: %v", ack)
	}
	failed := knowledgeEntryByKey(t, candidate, "flaky")
	if failed.AdoptionState != "failed" || !strings.Contains(failed.AdoptionError, "bd write refused") {
		t.Fatalf("after failed callback: state=%q error=%q", failed.AdoptionState, failed.AdoptionError)
	}
	if code := adoptEntry(t, entry.ID); code != http.StatusAccepted {
		t.Fatalf("retry after failure: %d, want 202", code)
	}
	// A daemon not eligible for the ultimate's plan may not resolve the retry.
	ack = postKnowledgeResults(t, "foreign-daemon", map[string]any{
		"adoptions": []map[string]any{{
			"kind": "entry", "id": entry.ID, "ok": false, "ultimate_dir_id": ultimate, "error": "forged failure",
		}},
	})
	if ack["adoptions_resolved"].(float64) != 0 {
		t.Fatalf("foreign daemon adoption resolved: %v", ack)
	}
	if state := knowledgeEntryByKey(t, candidate, "flaky").AdoptionState; state != "transferring" {
		t.Fatalf("foreign callback moved transferring entry: %q", state)
	}

	// Success callback lands adopted with provenance; a stale repeat is inert.
	ack = postKnowledgeResults(t, daemonID, map[string]any{
		"adoptions": []map[string]any{{
			"kind": "entry", "id": entry.ID, "ok": true, "ultimate_dir_id": ultimate,
		}},
	})
	if ack["adoptions_resolved"].(float64) != 1 {
		t.Fatalf("successful adoption acknowledgment: %v", ack)
	}
	adopted := knowledgeEntryByKey(t, candidate, "flaky")
	if adopted.AdoptionState != "adopted" || adopted.UltimateDirID == nil || *adopted.UltimateDirID != ultimate {
		t.Fatalf("after success callback: state=%q ultimate=%v", adopted.AdoptionState, adopted.UltimateDirID)
	}
	if adopted.AdoptedFromDirID == nil || *adopted.AdoptedFromDirID != candidate || adopted.AdoptedFromKey == nil || *adopted.AdoptedFromKey != "flaky" {
		t.Fatalf("adopted provenance: from=%v key=%v", adopted.AdoptedFromDirID, adopted.AdoptedFromKey)
	}
	ultimateMirror := knowledgeEntryByKey(t, ultimate, "flaky")
	if ultimateMirror.Content != "content worth retrying" || ultimateMirror.MirrorState != "synced" {
		t.Fatalf("ultimate mirror after adoption: %+v", ultimateMirror)
	}
	if ultimateMirror.AdoptedFromDirID == nil || *ultimateMirror.AdoptedFromDirID != candidate {
		t.Fatalf("ultimate mirror provenance: from=%v", ultimateMirror.AdoptedFromDirID)
	}
	ack = postKnowledgeResults(t, daemonID, map[string]any{
		"adoptions": []map[string]any{{
			"kind": "entry", "id": entry.ID, "ok": false, "ultimate_dir_id": ultimate, "error": "stale",
		}},
	})
	if ack["adoptions_resolved"].(float64) != 0 {
		t.Fatalf("stale adoption acknowledgment: %v", ack)
	}
	if state := knowledgeEntryByKey(t, candidate, "flaky").AdoptionState; state != "adopted" {
		t.Fatalf("stale callback moved a terminal entry: %q", state)
	}
	if code := adoptEntry(t, entry.ID); code != http.StatusConflict {
		t.Fatalf("adopt after adopted: %d, want 409", code)
	}
}

// TestKnowledgeEntryOperations keeps the write boundary server-authoritative:
// only a bound ultimate mirror may queue work, the daemon plan remains scoped
// to that binding, and the mirror advances only after a successful report.
func TestKnowledgeEntryOperations(t *testing.T) {
	if testHandler == nil {
		t.Fatal("database fixture is required")
	}
	const daemonID = "daemon-knowledge-operations"
	ctx := context.Background()
	ultimate := registerKnowledgeUltimate(t, daemonID, "/tmp/ultimate-operations")
	candidate := registerKnowledgeDir(t, "candidate_cli", "/tmp/candidate-operations")
	claimKnowledgeUltimate(t, daemonID, ultimate)
	postKnowledgeResults(t, daemonID, map[string]any{
		"scans": []map[string]any{{
			"dir_id": candidate, "trigger_source": "initial", "ok": true,
			"memories": map[string]string{"readonly": "candidate source"},
		}},
	})
	readonly := knowledgeEntryByKey(t, candidate, "readonly")

	var targetID, sourceID, secondSourceID string
	if err := testPool.QueryRow(ctx, `
INSERT INTO knowledge_entry (workspace_id, dir_id, key, content, content_sha256, adoption_state)
VALUES ($1, $2, 'target', 'original target', $3, 'adopted')
RETURNING id::text`, testWorkspaceID, ultimate, knowledgeContentSHA("original target")).Scan(&targetID); err != nil {
		t.Fatalf("seed target entry: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
INSERT INTO knowledge_entry (workspace_id, dir_id, key, content, content_sha256, adoption_state)
VALUES ($1, $2, 'source', 'original source', $3, 'adopted')
RETURNING id::text`, testWorkspaceID, ultimate, knowledgeContentSHA("original source")).Scan(&sourceID); err != nil {
		t.Fatalf("seed source entry: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
INSERT INTO knowledge_entry (workspace_id, dir_id, key, content, content_sha256, adoption_state)
VALUES ($1, $2, 'second-source', 'second original source', $3, 'adopted')
RETURNING id::text`, testWorkspaceID, ultimate, knowledgeContentSHA("second original source")).Scan(&secondSourceID); err != nil {
		t.Fatalf("seed second source entry: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(ctx, `DELETE FROM knowledge_entry_operation WHERE workspace_id = $1`, testWorkspaceID)
	})

	// Candidate mirrors are read-only, and confirmation is mandatory even when
	// the caller has Owner access to the workspace.
	testutil.Call(t, testHandler.EditKnowledgeEntry,
		withURLParam(newRequest(http.MethodPost, "/api/knowledge/entries/"+readonly.ID+"/edit", map[string]any{"content": "must fail"}), "id", readonly.ID)).
		Want(http.StatusConflict)
	testutil.Call(t, testHandler.DeleteKnowledgeEntry,
		withURLParam(newRequest(http.MethodPost, "/api/knowledge/entries/"+targetID+"/delete", map[string]any{"confirm": false}), "id", targetID)).
		Want(http.StatusBadRequest)

	var first KnowledgeEntryOperationResponse
	testutil.Call(t, testHandler.EditKnowledgeEntry,
		withURLParam(newRequest(http.MethodPost, "/api/knowledge/entries/"+targetID+"/edit", map[string]any{"content": "first edit"}), "id", targetID)).
		Want(http.StatusAccepted).JSON(&first)
	if first.Status != "queued" || first.ID == "" {
		t.Fatalf("first edit response: %+v", first)
	}
	// A target or source already captured by queued work cannot be mutated by a
	// second operation before the daemon returns an outcome.
	testutil.Call(t, testHandler.MergeKnowledgeEntries,
		withURLParam(newRequest(http.MethodPost, "/api/knowledge/entries/"+sourceID+"/merge", map[string]any{
			"content": "must conflict", "source_entry_ids": []string{targetID}, "confirm": true,
		}), "id", sourceID)).Want(http.StatusConflict)

	plan := knowledgePlan(t, daemonID)
	var firstPlan *KnowledgePlanOperation
	for i := range plan.Workspaces {
		for j := range plan.Workspaces[i].Operations {
			if plan.Workspaces[i].Operations[j].ID == first.ID {
				firstPlan = &plan.Workspaces[i].Operations[j]
			}
		}
	}
	if firstPlan == nil || firstPlan.OperationType != "edit" || firstPlan.Key != "target" || firstPlan.Content != "first edit" {
		t.Fatalf("queued edit missing from bound daemon plan: %+v", firstPlan)
	}
	foreignPlan := knowledgePlan(t, "daemon-not-bound-to-ultimate")
	for _, workspace := range foreignPlan.Workspaces {
		for _, operation := range workspace.Operations {
			if operation.ID == first.ID {
				t.Fatalf("foreign daemon received mutable operation: %+v", operation)
			}
		}
	}

	postKnowledgeResults(t, daemonID, map[string]any{
		"operations": []map[string]any{{"id": first.ID, "ok": false, "error": "bd recall mismatch"}},
	})
	var content, status, operationErr string
	if err := testPool.QueryRow(ctx, `
SELECT e.content, o.status, o.error
FROM knowledge_entry e JOIN knowledge_entry_operation o ON o.entry_id = e.id
WHERE o.id = $1`, first.ID).Scan(&content, &status, &operationErr); err != nil {
		t.Fatalf("read failed operation: %v", err)
	}
	if content != "original target" || status != "queued" || !strings.Contains(operationErr, "recall mismatch") {
		t.Fatalf("failed operation changed mirror or cannot retry: content=%q status=%q error=%q", content, status, operationErr)
	}
	failedEntry := knowledgeEntryByKey(t, ultimate, "target")
	if failedEntry.PendingOperation == nil || failedEntry.PendingOperation.ID != first.ID ||
		failedEntry.PendingOperation.OperationType != "edit" || failedEntry.PendingOperation.Status != "queued" ||
		!strings.Contains(failedEntry.PendingOperation.Error, "recall mismatch") || failedEntry.PendingOperation.RequestedAt == nil {
		t.Fatalf("failed operation is not visible through the knowledge read model: %+v", failedEntry.PendingOperation)
	}
	postKnowledgeResults(t, daemonID, map[string]any{
		"operations": []map[string]any{{"id": first.ID, "ok": true}},
	})
	if completedEntry := knowledgeEntryByKey(t, ultimate, "target"); completedEntry.PendingOperation != nil {
		t.Fatalf("completed operation remained visible through the knowledge read model: %+v", completedEntry.PendingOperation)
	}

	var merge KnowledgeEntryOperationResponse
	testutil.Call(t, testHandler.MergeKnowledgeEntries,
		withURLParam(newRequest(http.MethodPost, "/api/knowledge/entries/"+targetID+"/merge", map[string]any{
			"content": "consolidated knowledge", "source_entry_ids": []string{sourceID, secondSourceID}, "confirm": true,
		}), "id", targetID)).Want(http.StatusAccepted).JSON(&merge)
	plan = knowledgePlan(t, daemonID)
	var mergePlan *KnowledgePlanOperation
	for i := range plan.Workspaces {
		for j := range plan.Workspaces[i].Operations {
			if plan.Workspaces[i].Operations[j].ID == merge.ID {
				mergePlan = &plan.Workspaces[i].Operations[j]
			}
		}
	}
	hasDeleteKey := func(keys []string, key string) bool {
		for _, candidate := range keys {
			if candidate == key {
				return true
			}
		}
		return false
	}
	if mergePlan == nil || len(mergePlan.DeleteKeys) != 2 || !hasDeleteKey(mergePlan.DeleteKeys, "source") || !hasDeleteKey(mergePlan.DeleteKeys, "second-source") {
		t.Fatalf("merge plan does not delete the source after recall: %+v", mergePlan)
	}
	// A daemon can report failure after writing the target and deleting one source.
	// The server keeps the operation queued, preserves its error, and leaves every
	// mirror row untouched until a successful replay confirms the complete merge.
	postKnowledgeResults(t, daemonID, map[string]any{
		"operations": []map[string]any{{"id": merge.ID, "ok": false, "error": "bd forget second-source: simulated failure"}},
	})
	var targetContent, targetState, firstSourceState, secondSourceState, operationStatus, operationError string
	if err := testPool.QueryRow(ctx, `
SELECT target.content, target.mirror_state, first_source.mirror_state, second_source.mirror_state, operation.status, operation.error
FROM knowledge_entry target
JOIN knowledge_entry first_source ON first_source.id = $2::uuid
JOIN knowledge_entry second_source ON second_source.id = $3::uuid
JOIN knowledge_entry_operation operation ON operation.entry_id = target.id
WHERE target.id = $1::uuid AND operation.id = $4::uuid`, targetID, sourceID, secondSourceID, merge.ID).
		Scan(&targetContent, &targetState, &firstSourceState, &secondSourceState, &operationStatus, &operationError); err != nil {
		t.Fatalf("read partially completed merge: %v", err)
	}
	if targetContent != "first edit" || targetState != "synced" || firstSourceState != "synced" || secondSourceState != "synced" || operationStatus != "queued" || !strings.Contains(operationError, "second-source") {
		t.Fatalf("partial merge changed mirror or cannot retry: target=(%q,%q) sources=(%q,%q) operation=(%q,%q)", targetContent, targetState, firstSourceState, secondSourceState, operationStatus, operationError)
	}
	for _, key := range []string{"target", "source", "second-source"} {
		entry := knowledgeEntryByKey(t, ultimate, key)
		if entry.PendingOperation == nil || entry.PendingOperation.ID != merge.ID ||
			entry.PendingOperation.OperationType != "merge" || entry.PendingOperation.Status != "queued" ||
			!strings.Contains(entry.PendingOperation.Error, "second-source") {
			t.Fatalf("partial merge is not visible for %q through the knowledge read model: %+v", key, entry.PendingOperation)
		}
	}
	retryPlan := knowledgePlan(t, daemonID)
	var retryOperation *KnowledgePlanOperation
	for i := range retryPlan.Workspaces {
		for j := range retryPlan.Workspaces[i].Operations {
			if retryPlan.Workspaces[i].Operations[j].ID == merge.ID {
				retryOperation = &retryPlan.Workspaces[i].Operations[j]
			}
		}
	}
	if retryOperation == nil || retryOperation.Key != "target" || len(retryOperation.DeleteKeys) != 2 || !hasDeleteKey(retryOperation.DeleteKeys, "source") || !hasDeleteKey(retryOperation.DeleteKeys, "second-source") {
		t.Fatalf("partial merge was not returned for replay: %+v", retryOperation)
	}
	postKnowledgeResults(t, daemonID, map[string]any{
		"operations": []map[string]any{{"id": merge.ID, "ok": true}},
	})
	target := knowledgeEntryByKey(t, ultimate, "target")
	if target.Content != "consolidated knowledge" || target.MirrorState != "synced" {
		t.Fatalf("completed merge target: %+v", target)
	}
	source := knowledgeEntryByKey(t, ultimate, "source")
	if source.MirrorState != "source_deleted" {
		t.Fatalf("completed merge source: %+v", source)
	}
	secondSource := knowledgeEntryByKey(t, ultimate, "second-source")
	if secondSource.MirrorState != "source_deleted" {
		t.Fatalf("completed merge second source: %+v", secondSource)
	}
	if err := testPool.QueryRow(ctx, `SELECT status, error FROM knowledge_entry_operation WHERE id = $1::uuid`, merge.ID).
		Scan(&operationStatus, &operationError); err != nil {
		t.Fatalf("read completed merge operation: %v", err)
	}
	if operationStatus != "completed" || operationError != "" {
		t.Fatalf("completed merge did not clear retry state: status=%q error=%q", operationStatus, operationError)
	}
}
