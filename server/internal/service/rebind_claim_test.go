package service

// RUYI-224: an agent rebind used to strand its queued tasks — the claim and
// candidate-discovery fences hard-matched agent_task_queue.runtime_id (the
// enqueue-time runtime), so rows pinned to the old runtime could never be
// claimed after the agent moved. The fixed authority model: the claiming
// runtime must equal the agent's CURRENT binding; a successful claim rewrites
// the row's runtime_id to the actual claimer; and the rebind transaction
// migrates the agent's queued/deferred rows to the new runtime.
//
// The fixtures here deliberately create the torn state (tasks still pinned to
// the old runtime after the agent row moved) so every test exercises both the
// healing path and the fences that must NOT relax (health, private-owner,
// per-(issue, agent) serialization, reclaim authorization).

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// rebindFixture seeds one agent on runtime A with a queued task pinned to A,
// plus a healthy runtime B the agent can be rebound to. rebind() performs the
// agent-row move WITHOUT migrating the queue rows — the exact torn state a
// pre-fix rebind (or a torn rebind transaction) leaves behind.
type rebindFixture struct {
	pool         *pgxpool.Pool
	fx           *testutil.Fixture
	agentID      pgtype.UUID
	oldRuntimeID pgtype.UUID
	newRuntimeID pgtype.UUID
	taskID       string
}

func newRebindFixture(t *testing.T) rebindFixture {
	t.Helper()
	pool := newResolveOriginatorPool(t)
	suffix := time.Now().UnixNano()
	bootstrap := testutil.New(pool, "", "")
	userID := bootstrap.User(t,
		fmt.Sprintf("rebind-user-%d", suffix),
		fmt.Sprintf("rebind-user-%d@example.com", suffix),
	)
	workspaceID := bootstrap.Workspace(t,
		fmt.Sprintf("rebind-claim-%d", suffix),
		fmt.Sprintf("rebind-claim-%d", suffix),
	)
	fx := testutil.New(pool, workspaceID, userID)
	oldRuntimeID := fx.Runtime(t, "rebind-old-runtime", testutil.Cols{"visibility": "public"})
	newRuntimeID := fx.Runtime(t, "rebind-new-runtime", testutil.Cols{"visibility": "public"})
	agentID := fx.Agent(t, "rebind-agent", oldRuntimeID, testutil.Cols{
		"max_concurrent_tasks": 5,
	})
	taskID := fx.Task(t, agentID, testutil.Cols{"runtime_id": oldRuntimeID})
	return rebindFixture{
		pool:         pool,
		fx:           fx,
		agentID:      util.MustParseUUID(agentID),
		oldRuntimeID: util.MustParseUUID(oldRuntimeID),
		newRuntimeID: util.MustParseUUID(newRuntimeID),
		taskID:       taskID,
	}
}

// rebind moves the agent to the new runtime, leaving queue rows pinned to the
// old one (the torn state under test).
func (f rebindFixture) rebind(t *testing.T) {
	t.Helper()
	if _, err := f.pool.Exec(context.Background(),
		`UPDATE agent SET runtime_id = $1 WHERE id = $2`, f.newRuntimeID, f.agentID); err != nil {
		t.Fatalf("rebind agent: %v", err)
	}
}

func (f rebindFixture) claim(t *testing.T, ctx context.Context, runtimeID pgtype.UUID) (db.AgentTaskQueue, error) {
	t.Helper()
	return db.New(f.pool).ClaimAgentTask(ctx, db.ClaimAgentTaskParams{
		AgentID:          f.agentID,
		RuntimeID:        runtimeID,
		PrepareLeaseSecs: 60,
		RuntimeStaleSecs: RuntimeClaimFreshnessSeconds,
	})
}

func (f rebindFixture) taskStatusRuntime(t *testing.T, ctx context.Context, taskID string) (string, string) {
	var status, runtimeID string
	if err := f.pool.QueryRow(ctx,
		`SELECT status, runtime_id::text FROM agent_task_queue WHERE id = $1`, taskID,
	).Scan(&status, &runtimeID); err != nil {
		t.Fatalf("read task %s: %v", taskID, err)
	}
	return status, runtimeID
}

func (f rebindFixture) setRuntimeHealth(t *testing.T, runtimeID pgtype.UUID, status string, staleInterval string) {
	t.Helper()
	sql := `UPDATE agent_runtime SET status = $2, last_seen_at = ` +
		fmt.Sprintf("now() - interval '%s' WHERE id = $1", staleInterval)
	if _, err := f.pool.Exec(context.Background(), sql, runtimeID, status); err != nil {
		t.Fatalf("set runtime health: %v", err)
	}
}

// TestClaimAgentTaskFollowsAgentRebind is the reproduction: bind runtime A,
// queue a task, rebind to runtime B — the daemon polling B must claim the
// stranded row and the row's runtime_id must be rewritten to B. The old
// runtime must no longer be able to claim for this agent.
func TestClaimAgentTaskFollowsAgentRebind(t *testing.T) {
	ctx := context.Background()
	f := newRebindFixture(t)
	f.rebind(t)

	claimed, err := f.claim(t, ctx, f.newRuntimeID)
	if err != nil {
		t.Fatalf("claim on new runtime after rebind: %v", err)
	}
	if util.UUIDToString(claimed.ID) != f.taskID {
		t.Fatalf("claimed task = %s, want %s", util.UUIDToString(claimed.ID), f.taskID)
	}
	if util.UUIDToString(claimed.RuntimeID) != util.UUIDToString(f.newRuntimeID) {
		t.Fatalf("claimed row runtime_id = %s, want rewritten to claiming runtime %s",
			util.UUIDToString(claimed.RuntimeID), util.UUIDToString(f.newRuntimeID))
	}
	status, runtimeID := f.taskStatusRuntime(t, ctx, f.taskID)
	if status != "dispatched" || runtimeID != util.UUIDToString(f.newRuntimeID) {
		t.Fatalf("task after claim: status=%s runtime_id=%s, want dispatched on %s",
			status, runtimeID, util.UUIDToString(f.newRuntimeID))
	}

	// The old runtime lost its authorization when the agent moved.
	stranded := f.fx.Task(t, util.UUIDToString(f.agentID), testutil.Cols{"runtime_id": util.UUIDToString(f.oldRuntimeID)})
	if _, err := f.claim(t, ctx, f.oldRuntimeID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("claim on old runtime after rebind: err = %v, want no rows", err)
	}
	if status, _ := f.taskStatusRuntime(t, ctx, stranded); status != "queued" {
		t.Fatalf("stranded task status = %q after old-runtime claim, want queued", status)
	}

	// A runtime the agent was never bound to cannot claim either.
	foreign := util.MustParseUUID("00000000-0000-0000-0000-0000000000f1")
	if _, err := f.claim(t, ctx, foreign); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("claim on foreign runtime: err = %v, want no rows", err)
	}
}

// TestClaimAgentTaskHealthFencesFollowCurrentRuntime pins that the health
// gates (online + fresh heartbeat) now attach to the agent's CURRENT runtime,
// and that the old runtime's health is irrelevant once the agent left it.
func TestClaimAgentTaskHealthFencesFollowCurrentRuntime(t *testing.T) {
	ctx := context.Background()
	f := newRebindFixture(t)
	f.rebind(t)

	f.setRuntimeHealth(t, f.newRuntimeID, "offline", "0 seconds")
	if _, err := f.claim(t, ctx, f.newRuntimeID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("claim with offline current runtime: err = %v, want no rows", err)
	}

	f.setRuntimeHealth(t, f.newRuntimeID, "online", "10 minutes")
	if _, err := f.claim(t, ctx, f.newRuntimeID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("claim with stale current runtime: err = %v, want no rows", err)
	}

	// The OLD runtime is fresh and online, but the agent no longer lives
	// there — its health must not re-authorize the claim.
	f.setRuntimeHealth(t, f.oldRuntimeID, "online", "0 seconds")
	if _, err := f.claim(t, ctx, f.oldRuntimeID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("claim on fresh old runtime after rebind: err = %v, want no rows", err)
	}

	f.setRuntimeHealth(t, f.newRuntimeID, "online", "0 seconds")
	if _, err := f.claim(t, ctx, f.newRuntimeID); err != nil {
		t.Fatalf("claim with healthy current runtime: %v", err)
	}
}

// TestClaimAgentTaskPrivateOwnerFenceFollowsCurrentRuntime pins that the
// private-runtime owner gate attaches to the agent's CURRENT runtime: a
// private runtime owned by someone else cannot claim the agent's stranded
// rows even though the task itself sits on a public runtime.
func TestClaimAgentTaskPrivateOwnerFenceFollowsCurrentRuntime(t *testing.T) {
	ctx := context.Background()
	pool := newResolveOriginatorPool(t)
	suffix := time.Now().UnixNano()
	bootstrap := testutil.New(pool, "", "")
	agentOwner := bootstrap.User(t,
		fmt.Sprintf("rebind-owner-%d", suffix),
		fmt.Sprintf("rebind-owner-%d@example.com", suffix),
	)
	otherUser := bootstrap.User(t,
		fmt.Sprintf("rebind-other-%d", suffix),
		fmt.Sprintf("rebind-other-%d@example.com", suffix),
	)
	workspaceID := bootstrap.Workspace(t,
		fmt.Sprintf("rebind-private-%d", suffix),
		fmt.Sprintf("rebind-private-%d", suffix),
	)
	fx := testutil.New(pool, workspaceID, agentOwner)
	publicRuntime := fx.Runtime(t, "rebind-public", testutil.Cols{"visibility": "public"})
	privateRuntime := fx.Runtime(t, "rebind-private", testutil.Cols{
		"visibility": "private",
		"owner_id":   otherUser,
	})
	agentID := fx.Agent(t, "rebind-private-agent", privateRuntime, testutil.Cols{
		"max_concurrent_tasks": 5,
	})
	taskID := fx.Task(t, agentID, testutil.Cols{"runtime_id": publicRuntime})

	f := rebindFixture{
		pool: pool, fx: fx,
		agentID:      util.MustParseUUID(agentID),
		oldRuntimeID: util.MustParseUUID(publicRuntime),
		newRuntimeID: util.MustParseUUID(privateRuntime),
		taskID:       taskID,
	}

	// The agent IS bound to the private runtime, but its owner is foreign:
	// the owner gate follows the current binding and blocks the claim.
	if _, err := f.claim(t, ctx, f.newRuntimeID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("claim on foreign-owned private runtime: err = %v, want no rows", err)
	}
	if status, _ := f.taskStatusRuntime(t, ctx, taskID); status != "queued" {
		t.Fatalf("task status = %q, want queued after rejected claim", status)
	}

	// Hand the runtime to the agent's owner: the same claim now succeeds.
	if _, err := pool.Exec(ctx, `UPDATE agent_runtime SET owner_id = $1 WHERE id = $2`,
		agentOwner, privateRuntime); err != nil {
		t.Fatalf("transfer runtime ownership: %v", err)
	}
	if _, err := f.claim(t, ctx, f.newRuntimeID); err != nil {
		t.Fatalf("claim on own private runtime: %v", err)
	}
}

// TestListQueuedClaimCandidatesFollowAgentRebind pins the discovery side of
// the fence: the daemon polling the agent's CURRENT runtime must see the
// stranded rows, and the old runtime must not.
func TestListQueuedClaimCandidatesFollowAgentRebind(t *testing.T) {
	ctx := context.Background()
	f := newRebindFixture(t)
	f.rebind(t)
	q := db.New(f.pool)

	candidates, err := q.ListQueuedClaimCandidatesByRuntime(ctx, f.oldRuntimeID)
	if err != nil {
		t.Fatalf("list candidates on old runtime: %v", err)
	}
	if len(candidates) != 0 {
		t.Fatalf("old runtime saw %d candidates, want 0", len(candidates))
	}

	candidates, err = q.ListQueuedClaimCandidatesByRuntime(ctx, f.newRuntimeID)
	if err != nil {
		t.Fatalf("list candidates on new runtime: %v", err)
	}
	if len(candidates) != 1 || util.UUIDToString(candidates[0].ID) != f.taskID {
		t.Fatalf("new runtime candidates = %v, want exactly the stranded task %s", candidates, f.taskID)
	}

	batch, err := q.ListQueuedClaimCandidatesByRuntimes(ctx, []pgtype.UUID{f.oldRuntimeID})
	if err != nil {
		t.Fatalf("batch candidates on old runtime: %v", err)
	}
	if len(batch) != 0 {
		t.Fatalf("old runtime batch saw %d candidates, want 0", len(batch))
	}
	batch, err = q.ListQueuedClaimCandidatesByRuntimes(ctx, []pgtype.UUID{f.oldRuntimeID, f.newRuntimeID})
	if err != nil {
		t.Fatalf("batch candidates on both runtimes: %v", err)
	}
	if len(batch) != 1 || util.UUIDToString(batch[0].ID) != f.taskID {
		t.Fatalf("batch candidates = %v, want exactly the stranded task %s", batch, f.taskID)
	}
}

// TestReclaimAuthorizationFollowsAgentBinding pins the reclaim fences: a
// stale dispatched row is only re-delivered while the row's runtime is still
// the agent's CURRENT binding. After a rebind the old runtime must not
// re-deliver — including through the batch variant whose runtime filter still
// contains the old runtime.
func TestReclaimAuthorizationFollowsAgentBinding(t *testing.T) {
	ctx := context.Background()
	f := newRebindFixture(t)
	q := db.New(f.pool)
	resetStaleDispatch := func() {
		t.Helper()
		if _, err := f.pool.Exec(ctx, `
			UPDATE agent_task_queue
			SET status = 'dispatched', started_at = NULL,
			    dispatched_at = now() - interval '10 minutes',
			    prepare_lease_expires_at = now() - interval '1 minute'
			WHERE id = $1
		`, f.taskID); err != nil {
			t.Fatalf("reset stale dispatch: %v", err)
		}
	}
	reclaimOne := func(runtimeID pgtype.UUID) error {
		_, err := q.ReclaimStaleDispatchedTaskForRuntime(ctx, db.ReclaimStaleDispatchedTaskForRuntimeParams{
			RuntimeID:         runtimeID,
			PrepareLeaseSecs:  60,
			ClaimRecoverySecs: 30,
			RuntimeStaleSecs:  RuntimeClaimFreshnessSeconds,
		})
		return err
	}
	batchReclaim := func(ids ...pgtype.UUID) []db.AgentTaskQueue {
		got, err := q.ReclaimStaleDispatchedTasksForRuntimes(ctx, db.ReclaimStaleDispatchedTasksForRuntimesParams{
			RuntimeIds:        ids,
			PrepareLeaseSecs:  60,
			ClaimRecoverySecs: 30,
			RuntimeStaleSecs:  RuntimeClaimFreshnessSeconds,
			MaxTasks:          10,
		})
		if err != nil {
			t.Fatalf("batch reclaim: %v", err)
		}
		return got
	}

	// Bound to the row's runtime: reclaim works as before.
	resetStaleDispatch()
	if err := reclaimOne(f.oldRuntimeID); err != nil {
		t.Fatalf("reclaim while bound: %v", err)
	}

	// After the rebind the old runtime's authorization is gone — the row
	// stays dispatched (recovery of that wedge is out of scope for
	// RUYI-224), and neither the singular nor the batch path may re-deliver.
	f.rebind(t)
	resetStaleDispatch()
	if err := reclaimOne(f.oldRuntimeID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("reclaim on old runtime after rebind: err = %v, want no rows", err)
	}
	if got := batchReclaim(f.oldRuntimeID); len(got) != 0 {
		t.Fatalf("batch reclaim on old runtime after rebind recovered %d tasks, want 0", len(got))
	}
	if got := batchReclaim(f.oldRuntimeID, f.newRuntimeID); len(got) != 0 {
		t.Fatalf("batch reclaim across both runtimes after rebind recovered %d tasks, want 0", len(got))
	}
	if status, _ := f.taskStatusRuntime(t, ctx, f.taskID); status != "dispatched" {
		t.Fatalf("task status = %q, want dispatched (untouched by reclaim)", status)
	}
}

// TestMigrateAgentTaskQueueOnRuntimeRebind pins the rebind-side data fix:
// queued and deferred rows follow the agent to the new runtime, while
// dispatched / running / terminal rows stay with the runtime that (had) them.
func TestMigrateAgentTaskQueueOnRuntimeRebind(t *testing.T) {
	ctx := context.Background()
	f := newRebindFixture(t)
	agent := util.UUIDToString(f.agentID)
	oldID := util.UUIDToString(f.oldRuntimeID)
	newID := util.UUIDToString(f.newRuntimeID)

	deferredID := f.fx.Task(t, agent, testutil.Cols{
		"runtime_id": oldID,
		"status":     "deferred",
		"fire_at":    testutil.Raw("now() + interval '1 hour'"),
	})
	dispatchedID := f.fx.Task(t, agent, testutil.Cols{
		"runtime_id":    oldID,
		"status":        "dispatched",
		"dispatched_at": testutil.Raw("now()"),
	})
	runningID := f.fx.Task(t, agent, testutil.Cols{
		"runtime_id": oldID,
		"status":     "running",
		"started_at": testutil.Raw("now()"),
	})
	completedID := f.fx.Task(t, agent, testutil.Cols{
		"runtime_id":   oldID,
		"status":       "completed",
		"completed_at": testutil.Raw("now()"),
	})

	migrated, err := db.New(f.pool).MigrateAgentTaskQueueOnRuntimeRebind(ctx, db.MigrateAgentTaskQueueOnRuntimeRebindParams{
		AgentID:      f.agentID,
		NewRuntimeID: f.newRuntimeID,
	})
	if err != nil {
		t.Fatalf("migrate queue rows on rebind: %v", err)
	}
	if migrated != 2 {
		t.Fatalf("migrated %d rows, want 2 (queued + deferred)", migrated)
	}

	for _, taskID := range []string{f.taskID, deferredID} {
		if _, runtime := f.taskStatusRuntime(t, ctx, taskID); runtime != newID {
			t.Fatalf("pre-execution task %s runtime_id = %s, want migrated to %s", taskID, runtime, newID)
		}
	}
	for _, taskID := range []string{dispatchedID, runningID, completedID} {
		if _, runtime := f.taskStatusRuntime(t, ctx, taskID); runtime != oldID {
			t.Fatalf("in-flight/terminal task %s runtime_id = %s, want left on %s", taskID, runtime, oldID)
		}
	}
}

// TestClaimTasksForRuntimesHealsStrandedQueuedTask drives the full batch
// daemon-poll path over the torn state: the machine polling the agent's
// CURRENT runtime must come back with the stranded task, claimed onto that
// runtime — and polling the old runtime must return nothing.
func TestClaimTasksForRuntimesHealsStrandedQueuedTask(t *testing.T) {
	ctx := context.Background()
	f := newRebindFixture(t)
	f.rebind(t)
	svc := NewTaskService(db.New(f.pool), f.pool, nil, events.New())

	claimed, err := svc.ClaimTasksForRuntimes(ctx, []pgtype.UUID{f.newRuntimeID}, 5)
	if err != nil {
		t.Fatalf("batch claim on new runtime: %v", err)
	}
	if len(claimed) != 1 || util.UUIDToString(claimed[0].ID) != f.taskID {
		t.Fatalf("batch claim returned %v, want the stranded task %s", claimed, f.taskID)
	}
	if util.UUIDToString(claimed[0].RuntimeID) != util.UUIDToString(f.newRuntimeID) {
		t.Fatalf("claimed runtime_id = %s, want %s",
			util.UUIDToString(claimed[0].RuntimeID), util.UUIDToString(f.newRuntimeID))
	}
	if status, runtime := f.taskStatusRuntime(t, ctx, f.taskID); status != "dispatched" || runtime != util.UUIDToString(f.newRuntimeID) {
		t.Fatalf("task after batch claim: status=%s runtime=%s, want dispatched on new runtime", status, runtime)
	}

	// Polling the old runtime (where the row used to live) yields nothing.
	stranded := f.fx.Task(t, util.UUIDToString(f.agentID), testutil.Cols{"runtime_id": util.UUIDToString(f.oldRuntimeID)})
	claimed, err = svc.ClaimTasksForRuntimes(ctx, []pgtype.UUID{f.oldRuntimeID}, 5)
	if err != nil {
		t.Fatalf("batch claim on old runtime: %v", err)
	}
	if len(claimed) != 0 {
		t.Fatalf("batch claim on old runtime returned %d tasks, want 0", len(claimed))
	}
	if status, _ := f.taskStatusRuntime(t, ctx, stranded); status != "queued" {
		t.Fatalf("stranded task status = %q, want queued", status)
	}
}
