package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func claimRuntimeManifest(t *testing.T, h *Handler, runtimeID string, slim bool) *AgentTaskResponse {
	t.Helper()
	req := newDaemonTokenRequest("POST", "/api/daemon/runtimes/"+runtimeID+"/tasks/claim", nil, testWorkspaceID, "manifest-test")
	if slim {
		req.Header.Set("X-Client-Capabilities", protocol.DaemonCapabilitySkillBundlesV1)
	}
	req = withURLParam(req, "runtimeId", runtimeID)
	var out struct {
		Task *AgentTaskResponse `json:"task"`
	}
	testutil.Call(t, h.ClaimTaskByRuntime, req).Want(http.StatusOK).JSON(&out)
	if out.Task == nil || out.Task.RuntimeContextManifest == nil {
		t.Fatal("claim missing runtime manifest")
	}
	return out.Task
}

func storedRuntimeManifest(t *testing.T, taskID string) (*RuntimeContextManifest, []byte) {
	t.Helper()
	var raw []byte
	dbfx.QueryRow(t, `SELECT context FROM agent_task_queue WHERE id = $1`, taskID).Scan(&raw)
	m := runtimeContextManifestFromContext(raw)
	if m == nil {
		t.Fatalf("missing stored manifest: %s", raw)
	}
	return m, raw
}

// Mutate after the carrier read, immediately before version lookup. The claim
// must keep the body it already read and attribute that body's history only.
type manifestRaceDB struct {
	dbExecutor
	once   sync.Once
	mutate func()
}

func (d *manifestRaceDB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if strings.Contains(sql, "SELECT id, version FROM prompt_version") {
		d.once.Do(d.mutate)
	}
	return d.dbExecutor.QueryRow(ctx, sql, args...)
}

func TestRuntimeContextManifestClaimMatchesReadBytes(t *testing.T) {
	ctx := context.Background()
	runtimeID := createClaimReclaimRuntime(t, ctx, "manifest race")
	agentID, issueID := createClaimReclaimAgentAndIssue(t, ctx, runtimeID, "manifest race agent")
	dbfx.Exec(t, `UPDATE agent SET instructions = 'read body', custom_env = '{"SECRET":"do-not-export"}', mcp_config = '{"secret":"do-not-export"}' WHERE id = $1`, agentID)
	insertPromptVersionRow(t, ctx, "agent", agentID, 1)
	taskID := dbfx.Task(t, agentID, testutil.Cols{"runtime_id": runtimeID, "issue_id": issueID, "context": testutil.Raw(`'{"budget":{"remaining":7},"other":"keep"}'::jsonb`)})
	h := *testHandler
	h.DB = &manifestRaceDB{dbExecutor: testPool, mutate: func() {
		dbfx.Exec(t, `UPDATE agent SET instructions = 'new body' WHERE id = $1`, agentID)
		insertPromptVersionRow(t, ctx, "agent", agentID, 2)
		dbfx.Exec(t, `UPDATE agent_task_queue SET context = context || '{"budget":{"remaining":3}}'::jsonb WHERE id = $1`, taskID)
	}}
	resp := claimRuntimeManifest(t, &h, runtimeID, false)
	if resp.Agent.Instructions != "read body" {
		t.Fatalf("instructions = %q", resp.Agent.Instructions)
	}
	m, raw := storedRuntimeManifest(t, taskID)
	var found bool
	for _, carrier := range m.Carriers {
		if carrier.Scope == "project" || carrier.Scope == "squad" {
			t.Fatalf("uninjected tier: %+v", carrier)
		}
		if carrier.Scope == "agent" {
			found = true
			if carrier.Version == nil || *carrier.Version != 1 || carrier.VersionID == "" || carrier.CarrierSHA256 != promptTextSHA256("read body") || carrier.InjectedSHA256 != promptTextSHA256(resp.Agent.Instructions) {
				t.Fatalf("wrong attribution after concurrent edit: %+v", carrier)
			}
		}
	}
	if !found || m.PayloadSHA256["agent.instructions"] != promptTextSHA256(resp.Agent.Instructions) {
		t.Fatalf("missing byte evidence: %+v", m)
	}
	var versions map[string]int32
	var versionRaw []byte
	dbfx.QueryRow(t, `SELECT prompt_versions FROM agent_task_queue WHERE id = $1`, taskID).Scan(&versionRaw)
	if err := json.Unmarshal(versionRaw, &versions); err != nil {
		t.Fatal(err)
	}
	if versions["agent"] != 1 {
		t.Fatalf("legacy versions = %s", versionRaw)
	}
	if !strings.Contains(string(raw), `"remaining": 3`) || !strings.Contains(string(raw), `"other": "keep"`) {
		t.Fatalf("context overwritten: %s", raw)
	}
	serialized, _ := json.Marshal(m)
	if strings.Contains(string(serialized), "do-not-export") || strings.Contains(string(serialized), "read body") {
		t.Fatalf("manifest exported body/config: %s", serialized)
	}
	if len(m.Skills.Offered) == 0 || m.Skills.Offered[0].State != "offered_full_bundle" || len(m.Skills.Resolved) != 0 {
		t.Fatalf("incorrect full skill states: %+v", m.Skills)
	}
	task, err := testHandler.Queries.GetAgentTask(ctx, parseUUID(taskID))
	if err != nil {
		t.Fatal(err)
	}
	if taskToResponse(task, testWorkspaceID).RuntimeContextManifest == nil {
		t.Fatal("existing task response does not expose stored evidence")
	}
}

func TestRuntimeContextManifestClaimUnmatchedAndDegraded(t *testing.T) {
	ctx := context.Background()
	runtimeID := createClaimReclaimRuntime(t, ctx, "manifest mismatch")
	agentID, issueID := createClaimReclaimAgentAndIssue(t, ctx, runtimeID, "manifest mismatch agent")
	dbfx.Exec(t, `UPDATE agent SET instructions = 'history body' WHERE id = $1`, agentID)
	insertPromptVersionRow(t, ctx, "agent", agentID, 1)
	dbfx.Exec(t, `UPDATE agent SET instructions = 'actual body' WHERE id = $1`, agentID)
	taskID := dbfx.Task(t, agentID, testutil.Cols{"runtime_id": runtimeID, "issue_id": issueID})
	claimRuntimeManifest(t, testHandler, runtimeID, true)
	m, _ := storedRuntimeManifest(t, taskID)
	for _, carrier := range m.Carriers {
		if carrier.Scope == "agent" && (carrier.Version != nil || carrier.VersionStatus != "unmatched") {
			t.Fatalf("mismatched history marked exact: %+v", carrier)
		}
	}
	if _, ok := m.promptVersions()["agent"]; ok {
		t.Fatal("unmatched version leaked into legacy versions")
	}
	// A malformed carrier degrades, contributes no carrier text, and keeps an
	// observation even when there is no injected clause or attributable tier.
	dbfx.Exec(t, `UPDATE agent_task_queue SET status = 'completed' WHERE id = $1`, taskID)
	dbfx.Exec(t, `UPDATE agent SET instructions = '<!-- clause:id=broken' WHERE id = $1`, agentID)
	unknownID := dbfx.Task(t, agentID, testutil.Cols{"runtime_id": runtimeID, "context": testutil.Raw(`'{"type":"unknown-fixture"}'::jsonb`)})
	claimRuntimeManifest(t, testHandler, runtimeID, true)
	m, _ = storedRuntimeManifest(t, unknownID)
	if m.TaskKind != "unknown" || m.Rule.Status != "unknown" {
		t.Fatalf("unknown incorrectly resolved: %+v", m)
	}
	var degraded bool
	for _, observation := range m.Observations {
		if observation.Scope == "agent" && observation.Degraded && observation.Reason == "parse_error" {
			degraded = true
		}
	}
	if !degraded {
		t.Fatalf("missing degradation: %+v", m.Observations)
	}
	for _, carrier := range m.Carriers {
		if carrier.Scope == "agent" {
			t.Fatalf("empty degraded carrier attributed: %+v", carrier)
		}
	}
}

func TestRuntimeContextManifestResolveHashChangesAndAtomicMerge(t *testing.T) {
	ctx := context.Background()
	runtimeID, taskID, skillIDs := resolveScopeFixture(t, ctx, "manifest resolve", "manifest-resolve-a", "manifest-resolve-b")
	dbfx.Exec(t, `UPDATE agent_task_queue SET status = 'queued', context = '{"budget":{"remaining":9}}'::jsonb, rule_version_id = '11111111-1111-4111-8111-111111111111' WHERE id = $1`, taskID)
	resp := claimRuntimeManifest(t, testHandler, runtimeID, true)
	m := resp.RuntimeContextManifest
	if m.Rule.VersionID != "11111111-1111-4111-8111-111111111111" || m.Rule.Status != "queue_version_ref" {
		t.Fatalf("missing rule reference: %+v", m.Rule)
	}
	refs := map[string]runtimeSkillEvidence{}
	for _, ref := range m.Skills.Offered {
		refs[ref.ID] = ref
		if ref.State != "offered_ref" {
			t.Fatalf("refs claimed loaded: %+v", ref)
		}
	}
	dbfx.Exec(t, `UPDATE skill SET content = 'changed after claim' WHERE id = $1`, skillIDs[0])
	var returned struct {
		Bundles []service.AgentSkillData `json:"bundles"`
	}
	resolveBundles(t, testHandler, runtimeID, taskID, resolveSkillBundleRef{ID: skillIDs[0], Source: refs[skillIDs[0]].Source, Hash: refs[skillIDs[0]].Hash}).Want(http.StatusOK).JSON(&returned)
	if len(returned.Bundles) != 1 || returned.Bundles[0].Hash == refs[skillIDs[0]].Hash {
		t.Fatalf("expected changed bundle: %+v", returned)
	}
	m, _ = storedRuntimeManifest(t, taskID)
	if len(m.Skills.Resolved) != 1 {
		t.Fatalf("resolved = %+v", m.Skills.Resolved)
	}
	for _, resolved := range m.Skills.Resolved {
		if !resolved.HashChanged || resolved.RequestedHash != refs[skillIDs[0]].Hash || resolved.Hash != returned.Bundles[0].Hash || resolved.State != "resolved_bundle" {
			t.Fatalf("missing transition evidence: %+v", resolved)
		}
	}
	// Concurrent distinct resolution writes must merge with each other and
	// with a writer changing a separate context key.
	task, err := testHandler.Queries.GetAgentTask(ctx, parseUUID(taskID))
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 3)
	for _, skillID := range skillIDs {
		wg.Go(func() {
			allowed, err := testHandler.TaskService.LoadRequestedAgentSkillBundles(ctx, parseUUID(resp.AgentID), []service.AgentSkillBundleRef{{ID: skillID, Source: refs[skillID].Source}})
			if err == nil {
				bundle := allowed[service.AgentSkillBundleKey(refs[skillID].Source, skillID)]
				err = testHandler.recordRuntimeSkillResolutions(ctx, task, []resolveSkillBundleRef{{ID: skillID, Source: refs[skillID].Source, Hash: bundle.Hash}}, []service.AgentSkillData{bundle})
			}
			errs <- err
		})
	}
	wg.Go(func() {
		_, err := testPool.Exec(ctx, `UPDATE agent_task_queue SET context = context || '{"budget":{"remaining":2}}'::jsonb WHERE id = $1`, taskID)
		errs <- err
	})
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	m, raw := storedRuntimeManifest(t, taskID)
	if len(m.Skills.Resolved) != 3 || !strings.Contains(string(raw), `"remaining": 2`) {
		t.Fatalf("lost concurrent evidence/budget: %s", raw)
	}
	if refs[skillIDs[0]].Hash != m.Skills.Offered[0].Hash { // Locate by ID; offered order is not a semantic contract.
		var retained bool
		for _, offered := range m.Skills.Offered {
			if offered.ID == skillIDs[0] && offered.Hash == refs[skillIDs[0]].Hash {
				retained = true
			}
		}
		if !retained {
			t.Fatal("resolve replaced claim's offered hash")
		}
	}
}

func TestRuntimeContextManifestClauseActualSet(t *testing.T) {
	f := newClauseInjectionFixture(t)
	clauseID := enactWorkspaceClause(t, f, "每日同步", cleanClauseText)
	taskID := dbfx.Task(t, f.agentID, testutil.Cols{"runtime_id": f.runtimeID, "issue_id": f.issueID})
	req := newDaemonTokenRequest("POST", "/api/daemon/runtimes/"+f.runtimeID+"/tasks/claim", nil, f.wsID, "manifest-clause")
	req = withURLParam(req, "runtimeId", f.runtimeID)
	testutil.Call(t, testHandler.ClaimTaskByRuntime, req).Want(http.StatusOK)
	m, _ := storedRuntimeManifest(t, taskID)
	if len(m.Clauses) != 1 || m.Clauses[0].ID != clauseID || m.Clauses[0].Version != 1 || m.Clauses[0].SHA256 == "" || m.Clauses[0].Degraded {
		t.Fatalf("actual clause evidence = %+v", m.Clauses)
	}
	var carrierVersion int32
	dbfx.QueryRow(t, `SELECT carrier_version FROM clause_injection WHERE task_id = $1 AND clause_id = $2`, taskID, clauseID).Scan(&carrierVersion)
	if carrierVersion != 1 {
		t.Fatalf("injection carrier version = %d", carrierVersion)
	}
	// A bound library clause excluded by task kind is not an injected clause.
	dbfx.Exec(t, `UPDATE clause_binding SET conditions = '{"task_kinds":["chat"]}'::jsonb WHERE clause_id = $1 AND status = 'active'`, clauseID)
	dbfx.Exec(t, `UPDATE agent_task_queue SET status = 'completed' WHERE id = $1`, taskID)
	filteredID := dbfx.Task(t, f.agentID, testutil.Cols{"runtime_id": f.runtimeID, "issue_id": f.issueID})
	req = newDaemonTokenRequest("POST", "/api/daemon/runtimes/"+f.runtimeID+"/tasks/claim", nil, f.wsID, "manifest-clause")
	req = withURLParam(req, "runtimeId", f.runtimeID)
	testutil.Call(t, testHandler.ClaimTaskByRuntime, req).Want(http.StatusOK)
	filtered, _ := storedRuntimeManifest(t, filteredID)
	if len(filtered.Clauses) != 0 {
		t.Fatalf("excluded clause advertised as injected: %+v", filtered.Clauses)
	}
}

func TestRuntimeContextManifestHistoricResolveUnchanged(t *testing.T) {
	ctx := context.Background()
	runtimeID, taskID, ids := resolveScopeFixture(t, ctx, "historic resolve", "historic-manifest-skill")
	dbfx.Exec(t, `UPDATE agent_task_queue SET context = '{"legacy":"preserved"}'::jsonb WHERE id = $1`, taskID)
	resolveBundles(t, testHandler, runtimeID, taskID, workspaceRef(ids[0])).Want(http.StatusOK)
	var raw []byte
	dbfx.QueryRow(t, `SELECT context FROM agent_task_queue WHERE id = $1`, taskID).Scan(&raw)
	if runtimeContextManifestFromContext(raw) != nil || !strings.Contains(string(raw), "preserved") {
		t.Fatalf("historic context rewritten: %s", raw)
	}
	if runtimeContextManifestFromContext([]byte(`{"runtime_context_manifest":{"schema_version":99}}`)) != nil {
		t.Fatal("unknown schema treated as v1")
	}
}

var _ dbExecutor = (*manifestRaceDB)(nil)

type manifestPersistFailureDB struct {
	dbExecutor
	failures int
}

func (d *manifestPersistFailureDB) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if strings.Contains(sql, "jsonb_build_object('runtime_context_manifest'") {
		d.failures++
		return pgconn.CommandTag{}, errors.New("injected manifest persistence failure")
	}
	return d.dbExecutor.Exec(ctx, sql, args...)
}

func TestRuntimeContextManifestPersistFailureRequeuesClaim(t *testing.T) {
	for _, batch := range []bool{false, true} {
		name := "single"
		if batch {
			name = "batch"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			runtimeID := createClaimReclaimRuntime(t, ctx, "manifest persistence failure "+name)
			agentID, issueID := createClaimReclaimAgentAndIssue(t, ctx, runtimeID, "manifest persistence failure "+name)
			taskID := dbfx.Task(t, agentID, testutil.Cols{"runtime_id": runtimeID, "issue_id": issueID})
			failingDB := &manifestPersistFailureDB{dbExecutor: testPool}
			h := *testHandler
			h.DB = failingDB
			claim := func(handler *Handler, success bool) {
				t.Helper()
				if batch {
					req := newDaemonTokenRequest("POST", "/api/daemon/tasks/claim",
						map[string]any{"daemon_id": batchClaimTestDaemonID, "runtime_ids": []string{runtimeID}, "max_tasks": 1},
						testWorkspaceID, batchClaimTestDaemonID)
					var out batchClaimResponse
					testutil.Call(t, handler.ClaimTasksByRuntime, req).Want(http.StatusOK).JSON(&out)
					if success {
						if len(out.Tasks) != 1 || out.Tasks[0].ID != taskID {
							t.Fatalf("requeued task not claimable: %+v", out.Tasks)
						}
					} else if len(out.Tasks) != 0 {
						t.Fatalf("manifest failure dispatched a batch task: %+v", out.Tasks)
					}
					return
				}
				req := newDaemonTokenRequest("POST", "/api/daemon/runtimes/"+runtimeID+"/tasks/claim", nil,
					testWorkspaceID, "manifest-failure")
				req = withURLParam(req, "runtimeId", runtimeID)
				if !success {
					response := testutil.Call(t, handler.ClaimTaskByRuntime, req).Want(http.StatusInternalServerError)
					if strings.Contains(response.Body.String(), `"task":`) || !strings.Contains(response.Body.String(), "failed to persist runtime context manifest") {
						t.Fatalf("manifest failure returned dispatch payload: %s", response.Body.String())
					}
					return
				}
				var out struct {
					Task *AgentTaskResponse `json:"task"`
				}
				testutil.Call(t, handler.ClaimTaskByRuntime, req).Want(http.StatusOK).JSON(&out)
				if out.Task == nil || out.Task.ID != taskID {
					t.Fatalf("requeued task not claimable: %+v", out.Task)
				}
			}
			claim(&h, false)
			if failingDB.failures != 1 {
				t.Fatalf("manifest failure injection count = %d, want 1", failingDB.failures)
			}
			task, err := testHandler.Queries.GetAgentTask(ctx, parseUUID(taskID))
			if err != nil {
				t.Fatal(err)
			}
			if task.Status != "queued" || task.DispatchedAt.Valid || task.StartedAt.Valid {
				t.Fatalf("manifest failure stranded task: status=%s dispatched=%+v started=%+v", task.Status, task.DispatchedAt, task.StartedAt)
			}
			if runtimeContextManifestFromContext(task.Context) != nil {
				t.Fatal("failed manifest write left evidence")
			}
			claim(testHandler, true)
			storedRuntimeManifest(t, taskID)
		})
	}
}

// Pause the old HTTP request at its evidence write, then complete a real
// reclaim and resolution before allowing the old statement to reach Postgres.
type manifestGenerationRaceDB struct {
	dbExecutor
	statement string
	once      sync.Once
	advance   func()
}

func (d *manifestGenerationRaceDB) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if strings.Contains(sql, d.statement) {
		d.once.Do(d.advance)
	}
	return d.dbExecutor.Exec(ctx, sql, args...)
}

func TestRuntimeContextManifestStaleClaimCannotReplaceNewGeneration(t *testing.T) {
	for _, batch := range []bool{false, true} {
		name := "single"
		if batch {
			name = "batch"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			runtimeID, taskID, ids := resolveScopeFixture(t, ctx, "manifest generation "+name, "manifest-generation-"+name)
			dbfx.Exec(t, `UPDATE agent_task_queue SET status = 'queued', dispatched_at = NULL, context = '{"other":"keep"}'::jsonb WHERE id = $1`, taskID)
			var agentID string
			dbfx.QueryRow(t, `SELECT agent_id FROM agent_task_queue WHERE id = $1`, taskID).Scan(&agentID)
			dbfx.Exec(t, `UPDATE agent SET instructions = 'old generation' WHERE id = $1`, agentID)
			insertPromptVersionRow(t, ctx, "agent", agentID, 1)
			var newer db.AgentTaskQueue
			race := &manifestGenerationRaceDB{dbExecutor: testPool, statement: "jsonb_build_object('runtime_context_manifest'"}
			race.advance = func() {
				// Simulate time passing while the old builder is stalled. The
				// next HTTP claim runs the production reclaim SQL.
				dbfx.Exec(t, `UPDATE agent_task_queue SET dispatched_at = now() - interval '10 minutes', prepare_lease_expires_at = now() - interval '1 second' WHERE id = $1`, taskID)
				dbfx.Exec(t, `UPDATE agent SET instructions = 'new generation' WHERE id = $1`, agentID)
				insertPromptVersionRow(t, ctx, "agent", agentID, 2)
				resp := claimRuntimeManifest(t, testHandler, runtimeID, true)
				if resp.ID != taskID || resp.RuntimeContextManifest.Claim == nil {
					t.Fatalf("wrong reclaimed task/generation: %+v", resp)
				}
				resolveBundles(t, testHandler, runtimeID, taskID, workspaceRef(ids[0])).Want(http.StatusOK)
				var err error
				newer, err = testHandler.Queries.GetAgentTask(ctx, parseUUID(taskID))
				if err != nil {
					t.Fatal(err)
				}
				if len(runtimeContextManifestFromContext(newer.Context).Skills.Resolved) != 1 {
					t.Fatal("new generation lacks resolution evidence")
				}
			}
			h := *testHandler
			h.DB = race
			if batch {
				req := newDaemonTokenRequest("POST", "/api/daemon/tasks/claim", map[string]any{"daemon_id": batchClaimTestDaemonID, "runtime_ids": []string{runtimeID}, "max_tasks": 1}, testWorkspaceID, batchClaimTestDaemonID)
				var out batchClaimResponse
				testutil.Call(t, h.ClaimTasksByRuntime, req).Want(http.StatusOK).JSON(&out)
				if len(out.Tasks) != 0 {
					t.Fatalf("stale batch dispatched a payload: %+v", out.Tasks)
				}
			} else {
				req := newDaemonTokenRequest("POST", "/api/daemon/runtimes/"+runtimeID+"/tasks/claim", nil, testWorkspaceID, "manifest-generation")
				req = withURLParam(req, "runtimeId", runtimeID)
				testutil.Call(t, h.ClaimTaskByRuntime, req).Want(http.StatusInternalServerError)
			}
			current, err := testHandler.Queries.GetAgentTask(ctx, parseUUID(taskID))
			if err != nil {
				t.Fatal(err)
			}
			if current.Status != "dispatched" || current.DispatchedAt != newer.DispatchedAt || string(current.Context) != string(newer.Context) || string(current.PromptVersions) != string(newer.PromptVersions) {
				t.Fatalf("old builder/requeue changed newer claim: before=%+v after=%+v", newer, current)
			}
			var versions map[string]int32
			if err := json.Unmarshal(current.PromptVersions, &versions); err != nil || versions["agent"] != 2 {
				t.Fatalf("new prompt versions lost: %s, %v", current.PromptVersions, err)
			}
		})
	}
}

func TestRuntimeContextManifestStaleResolutionCannotMergeIntoNewGeneration(t *testing.T) {
	ctx := context.Background()
	runtimeID, taskID, ids := resolveScopeFixture(t, ctx, "resolution generation", "resolution-generation")
	dbfx.Exec(t, `UPDATE agent_task_queue SET status = 'queued', dispatched_at = NULL WHERE id = $1`, taskID)
	claimRuntimeManifest(t, testHandler, runtimeID, true)
	var newer db.AgentTaskQueue
	race := &manifestGenerationRaceDB{dbExecutor: testPool, statement: "jsonb_set(context,"}
	race.advance = func() {
		dbfx.Exec(t, `UPDATE agent_task_queue SET dispatched_at = now() - interval '10 minutes', prepare_lease_expires_at = now() - interval '1 second' WHERE id = $1`, taskID)
		claimRuntimeManifest(t, testHandler, runtimeID, true)
		dbfx.Exec(t, `UPDATE skill SET content = 'new generation skill' WHERE id = $1`, ids[0])
		resolveBundles(t, testHandler, runtimeID, taskID, workspaceRef(ids[0])).Want(http.StatusOK)
		var err error
		newer, err = testHandler.Queries.GetAgentTask(ctx, parseUUID(taskID))
		if err != nil {
			t.Fatal(err)
		}
	}
	h := *testHandler
	h.DB = race
	resolveBundles(t, &h, runtimeID, taskID, workspaceRef(ids[0])).Want(http.StatusConflict)
	current, err := testHandler.Queries.GetAgentTask(ctx, parseUUID(taskID))
	if err != nil {
		t.Fatal(err)
	}
	if len(runtimeContextManifestFromContext(current.Context).Skills.Resolved) != 1 || string(current.Context) != string(newer.Context) || string(current.PromptVersions) != string(newer.PromptVersions) {
		t.Fatalf("stale resolution overwrote newer evidence: before=%s after=%s", newer.Context, current.Context)
	}
}

func TestRuntimeContextManifestPersistRequiresLivePreparingClaim(t *testing.T) {
	ctx := context.Background()
	runtimeID, taskID, _ := resolveScopeFixture(t, ctx, "manifest CAS", "manifest-cas")
	dbfx.Exec(t, `UPDATE agent_task_queue SET status = 'queued', dispatched_at = NULL WHERE id = $1`, taskID)
	claimRuntimeManifest(t, testHandler, runtimeID, true)
	task, err := testHandler.Queries.GetAgentTask(ctx, parseUUID(taskID))
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"runtime", "dispatch", "expired", "started", "missing"} {
		t.Run(scenario, func(t *testing.T) {
			candidate := task
			switch scenario {
			case "runtime":
				candidate.RuntimeID = parseUUID(testRuntimeID)
			case "dispatch":
				candidate.DispatchedAt.Time = candidate.DispatchedAt.Time.Add(-time.Second)
			case "expired":
				dbfx.Exec(t, `UPDATE agent_task_queue SET prepare_lease_expires_at = now() - interval '1 second' WHERE id = $1`, taskID)
			case "started":
				dbfx.Exec(t, `UPDATE agent_task_queue SET status = 'running', started_at = now() WHERE id = $1`, taskID)
			case "missing":
				candidate.ID = parseUUID("11111111-1111-4111-8111-111111111111")
			}
			if err := testHandler.persistRuntimeContextManifest(ctx, candidate, newRuntimeContextManifest(&candidate, "issue")); !errors.Is(err, pgx.ErrNoRows) {
				t.Fatalf("%s CAS error = %v, want ErrNoRows", scenario, err)
			}
			current, err := testHandler.Queries.GetAgentTask(ctx, task.ID)
			if err != nil {
				t.Fatal(err)
			}
			if string(current.Context) != string(task.Context) || string(current.PromptVersions) != string(task.PromptVersions) {
				t.Fatal("failed CAS changed evidence")
			}
			dbfx.Exec(t, `UPDATE agent_task_queue SET status = 'dispatched', started_at = NULL, prepare_lease_expires_at = now() + interval '1 minute' WHERE id = $1`, taskID)
		})
	}
}
