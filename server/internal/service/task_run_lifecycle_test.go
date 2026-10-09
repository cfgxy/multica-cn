package service

// RUYI-292 run lifecycle: integration tests for the user cancel matrix, the
// cancel-race terminal guards, the retry gates and the filtered run list.
// Mapping to tech design §9: AC2 (matrix rows + parallel runs), AC4 (race both
// ways, terminal 409, idempotent repeats, timeout no-flip), AC5/AC6 (retry
// gates, lineage, fresh session, 5s window), AC1 (list filters). AC3/AC8 stay
// with the QA pass on a live runtime, per the tech design.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// runLifecycleEnv is the seeded world one lifecycle test plays in: the shared
// (workspace, agent, issue) trio plus a second agent on the same runtime, so a
// test can hold two pending rows on one issue without tripping
// idx_one_pending_task_per_issue_agent_v2.
type runLifecycleEnv struct {
	svc         *TaskService
	pool        *pgxpool.Pool
	workspaceID string
	creatorID   string
	agentID     string
	agent2ID    string
	issueID     string
	runtimeID   string
}

func newRunLifecycleEnv(t *testing.T) runLifecycleEnv {
	t.Helper()
	pool := newResolveOriginatorPool(t)
	workspaceID, creatorID, agentID, issueID := seedAttributionFixture(t, pool)

	var runtimeID string
	if err := pool.QueryRow(context.Background(), `SELECT runtime_id::text FROM agent WHERE id = $1`, agentID).Scan(&runtimeID); err != nil {
		t.Fatalf("read agent runtime: %v", err)
	}

	// A second agent on the same runtime: the unique pending index is scoped
	// per (issue, agent), so the filtered-list spectrum parks its rerun/retry
	// children here instead of colliding with the primary agent's queued row.
	var agent2ID string
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO agent (workspace_id, name, runtime_mode, runtime_config, runtime_id, visibility,
			max_concurrent_tasks, owner_id, instructions, custom_env, custom_args)
		VALUES ($1, 'lifecycle-second-agent', 'cloud', '{}'::jsonb, $2, 'workspace', 1, $3, '', '{}'::jsonb, '[]'::jsonb)
		RETURNING id`, workspaceID, runtimeID, creatorID).Scan(&agent2ID); err != nil {
		t.Fatalf("seed second agent: %v", err)
	}

	suffix := time.Now().UnixNano()
	_ = suffix
	return runLifecycleEnv{
		svc:         NewTaskService(db.New(pool), pool, nil, events.New()),
		pool:        pool,
		workspaceID: workspaceID,
		creatorID:   creatorID,
		agentID:     agentID,
		agent2ID:    agent2ID,
		issueID:     issueID,
		runtimeID:   runtimeID,
	}
}

// seedRunTask inserts an agent_task_queue row in the given status with the
// lineage/trigger columns a test names; cleanup removes the row via t.Cleanup.
type seedRunTaskOpts struct {
	rerunOf      pgtype.UUID
	retryOf      pgtype.UUID
	triggerCmtID pgtype.UUID
	evidenceKind string
	attempt      int32
	maxAttempts  int32
	started      bool
}

func (e runLifecycleEnv) seedRunTask(t *testing.T, agentID, status string, opts seedRunTaskOpts) pgtype.UUID {
	t.Helper()
	ctx := context.Background()
	var id pgtype.UUID
	if err := e.pool.QueryRow(ctx, `
		INSERT INTO agent_task_queue
			(agent_id, runtime_id, issue_id, status, priority, attempt, max_attempts,
			 rerun_of_task_id, retry_of_task_id, trigger_comment_id, trigger_evidence_kind, started_at)
		VALUES ($1, $2, $3, $4, 0, $5, $6, $7, $8, $9, $10,
			CASE WHEN $11 THEN now() ELSE NULL END)
		RETURNING id`,
		agentID, e.runtimeID, e.issueID, status, opts.attempt, opts.maxAttempts,
		uuidPtrArg(opts.rerunOf), uuidPtrArg(opts.retryOf), uuidPtrArg(opts.triggerCmtID),
		nullTextArg(opts.evidenceKind), opts.started,
	).Scan(&id); err != nil {
		t.Fatalf("insert %s task: %v", status, err)
	}
	t.Cleanup(func() { e.pool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id = $1`, id) })
	return id
}

func uuidPtrArg(id pgtype.UUID) any {
	if id.Valid {
		return util.UUIDToString(id)
	}
	return nil
}

func nullTextArg(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (e runLifecycleEnv) status(t *testing.T, id pgtype.UUID) string {
	t.Helper()
	return taskStatus(t, e.pool, id)
}

func (e runLifecycleEnv) creator() pgtype.UUID { return util.MustParseUUID(e.creatorID) }

func markRuntimeOffline(t *testing.T, pool *pgxpool.Pool, runtimeID string, lastSeen time.Time) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`UPDATE agent_runtime SET status = 'offline', last_seen_at = $1 WHERE id = $2`,
		lastSeen, runtimeID); err != nil {
		t.Fatalf("mark runtime offline: %v", err)
	}
}

func (e runLifecycleEnv) seedTriggerComment(t *testing.T) pgtype.UUID {
	t.Helper()
	var id pgtype.UUID
	if err := e.pool.QueryRow(context.Background(),
		`INSERT INTO comment (issue_id, workspace_id, author_type, author_id, content)
		 VALUES ($1, $2, 'member', $3, 'trigger comment') RETURNING id`,
		e.issueID, e.workspaceID, e.creatorID).Scan(&id); err != nil {
		t.Fatalf("seed trigger comment: %v", err)
	}
	t.Cleanup(func() { e.pool.Exec(context.Background(), `DELETE FROM comment WHERE id = $1`, id) })
	return id
}

// --- AC2: the user cancel matrix, one test case per row ---

func TestCancelRunByUserMatrix(t *testing.T) {
	cases := []struct {
		name       string
		status     string
		started    bool
		wantCode   string
		wantStatus string
	}{
		{"queued flips straight to cancelled", "queued", false, RunCancelCodeCancelled, "cancelled"},
		{"dispatched enters two-phase stop", "dispatched", false, RunCancelCodeCancelRequested, "cancel_requested"},
		{"running enters two-phase stop", "running", true, RunCancelCodeCancelRequested, "cancel_requested"},
		{"waiting_local_directory enters two-phase stop", "waiting_local_directory", true, RunCancelCodeCancelRequested, "cancel_requested"},
		{"deferred enters two-phase stop", "deferred", false, RunCancelCodeCancelRequested, "cancel_requested"},
		{"completed answers not_cancellable", "completed", true, RunCancelCodeNotCancellable, "completed"},
		{"failed answers not_cancellable", "failed", true, RunCancelCodeNotCancellable, "failed"},
		{"cancelled answers already_cancelled", "cancelled", true, RunCancelCodeAlreadyCancelled, "cancelled"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newRunLifecycleEnv(t)
			id := env.seedRunTask(t, env.agentID, tc.status, seedRunTaskOpts{started: tc.started})

			outcome, err := env.svc.CancelRunByUser(context.Background(), id, env.creator())
			if err != nil {
				t.Fatalf("CancelRunByUser: %v", err)
			}
			if outcome.Code != tc.wantCode {
				t.Fatalf("code = %q, want %q", outcome.Code, tc.wantCode)
			}
			if got := env.status(t, id); got != tc.wantStatus {
				t.Fatalf("row status = %q, want %q", got, tc.wantStatus)
			}
		})
	}
}

func TestCancelRunByUserStampsAttributionOnTwoPhaseStop(t *testing.T) {
	env := newRunLifecycleEnv(t)
	id := env.seedRunTask(t, env.agentID, "running", seedRunTaskOpts{started: true})

	outcome, err := env.svc.CancelRunByUser(context.Background(), id, env.creator())
	if err != nil {
		t.Fatalf("CancelRunByUser: %v", err)
	}
	if outcome.Code != RunCancelCodeCancelRequested {
		t.Fatalf("code = %q, want cancel_requested", outcome.Code)
	}

	var by string
	var at pgtype.Timestamptz
	if err := env.pool.QueryRow(context.Background(),
		`SELECT cancel_requested_by_user_id::text, cancel_requested_at FROM agent_task_queue WHERE id = $1`, id).
		Scan(&by, &at); err != nil {
		t.Fatalf("read attribution: %v", err)
	}
	if by != env.creatorID {
		t.Fatalf("cancel_requested_by_user_id = %q, want %q", by, env.creatorID)
	}
	if !at.Valid {
		t.Fatal("cancel_requested_at must be stamped")
	}
}

func TestCancelRunByUserRepeatIsIdempotent(t *testing.T) {
	env := newRunLifecycleEnv(t)

	t.Run("repeat against cancel_requested re-broadcasts, stays cancel_requested", func(t *testing.T) {
		id := env.seedRunTask(t, env.agentID, "running", seedRunTaskOpts{started: true})
		if _, err := env.svc.CancelRunByUser(context.Background(), id, env.creator()); err != nil {
			t.Fatalf("first cancel: %v", err)
		}
		outcome, err := env.svc.CancelRunByUser(context.Background(), id, env.creator())
		if err != nil {
			t.Fatalf("repeat cancel: %v", err)
		}
		if outcome.Code != RunCancelCodeAlreadyCancelling {
			t.Fatalf("repeat code = %q, want already_cancelling", outcome.Code)
		}
		if got := env.status(t, id); got != "cancel_requested" {
			t.Fatalf("repeat must not flip the row, got %q", got)
		}
	})

	t.Run("repeat after confirmation answers already_cancelled", func(t *testing.T) {
		id := env.seedRunTask(t, env.agentID, "cancelled", seedRunTaskOpts{started: true})
		outcome, err := env.svc.CancelRunByUser(context.Background(), id, env.creator())
		if err != nil {
			t.Fatalf("cancel on cancelled: %v", err)
		}
		if outcome.Code != RunCancelCodeAlreadyCancelled {
			t.Fatalf("code = %q, want already_cancelled", outcome.Code)
		}
	})
}

func TestCancelRunByUserLeavesParallelRunUntouched(t *testing.T) {
	env := newRunLifecycleEnv(t)
	first := env.seedRunTask(t, env.agentID, "running", seedRunTaskOpts{started: true})
	second := env.seedRunTask(t, env.agentID, "running", seedRunTaskOpts{started: true})

	if _, err := env.svc.CancelRunByUser(context.Background(), first, env.creator()); err != nil {
		t.Fatalf("cancel first: %v", err)
	}
	if got := env.status(t, second); got != "running" {
		t.Fatalf("parallel run disturbed: status = %q, want running", got)
	}
	if got := env.status(t, first); got != "cancel_requested" {
		t.Fatalf("targeted run = %q, want cancel_requested", got)
	}
}

// --- AC4: race both ways — a terminal write landing on a stopping run loses ---

func TestCompleteTaskOnCancelRequestedConvergesToCancelled(t *testing.T) {
	env := newRunLifecycleEnv(t)
	id := env.seedRunTask(t, env.agentID, "cancel_requested", seedRunTaskOpts{started: true})

	task, _, err := env.svc.CompleteTask(context.Background(), id, []byte(`{"ok":true}`), "", "", "", false, "", "")
	if err != nil {
		t.Fatalf("CompleteTask: %v", err)
	}
	if task.Status != "cancelled" {
		t.Fatalf("completion converged to %q, want cancelled (cancel wins)", task.Status)
	}
	if got := env.status(t, id); got != "cancelled" {
		t.Fatalf("row status = %q, want cancelled", got)
	}
	var result []byte
	if err := env.pool.QueryRow(context.Background(), `SELECT result FROM agent_task_queue WHERE id = $1`, id).Scan(&result); err != nil {
		t.Fatalf("read result: %v", err)
	}
	if result != nil {
		t.Fatal("converged row must not carry the losing completion's result")
	}
}

func TestFailTaskOnCancelRequestedConvergesWithoutRetryChild(t *testing.T) {
	env := newRunLifecycleEnv(t)
	id := env.seedRunTask(t, env.agentID, "cancel_requested",
		seedRunTaskOpts{started: true, attempt: 0, maxAttempts: 3})

	task, err := env.svc.FailTask(context.Background(), id, "boom", "", "", "", "agent_error", false, "", "")
	if err != nil {
		t.Fatalf("FailTask: %v", err)
	}
	if task.Status != "cancelled" {
		t.Fatalf("failure converged to %q, want cancelled (cancel wins)", task.Status)
	}

	var children int
	if err := env.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM agent_task_queue WHERE retry_of_task_id = $1`, id).Scan(&children); err != nil {
		t.Fatalf("count retry children: %v", err)
	}
	if children != 0 {
		t.Fatalf("converged failure must not spawn an auto-retry child, found %d", children)
	}
}

func TestConvergeCancelRequestedToCancelledCAS(t *testing.T) {
	env := newRunLifecycleEnv(t)
	q := db.New(env.pool)
	ctx := context.Background()

	t.Run("cancel_requested flips with completed_at recorded", func(t *testing.T) {
		id := env.seedRunTask(t, env.agentID, "cancel_requested", seedRunTaskOpts{started: true})
		confirmed, err := q.ConvergeCancelRequestedToCancelled(ctx, id)
		if err != nil {
			t.Fatalf("converge: %v", err)
		}
		if confirmed.Status != "cancelled" {
			t.Fatalf("status = %q, want cancelled", confirmed.Status)
		}
		if !confirmed.CompletedAt.Valid {
			t.Fatal("confirmed stop must record completed_at")
		}
		// Replay is a no-op: the CAS misses and the caller keeps the row as-is.
		if _, err := q.ConvergeCancelRequestedToCancelled(ctx, id); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("replayed converge should miss (ErrNoRows), got %v", err)
		}
	})

	t.Run("completed row is untouched by the CAS", func(t *testing.T) {
		id := env.seedRunTask(t, env.agentID, "completed", seedRunTaskOpts{started: true})
		if _, err := q.ConvergeCancelRequestedToCancelled(ctx, id); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("converge on completed should miss, got %v", err)
		}
		if got := env.status(t, id); got != "completed" {
			t.Fatalf("completed row rewritten to %q", got)
		}
	})
}

// --- offline sweeper convergence: grace honoured, honest terminal ---

func TestOfflineRuntimeConvergesCancelRequestedBeyondGrace(t *testing.T) {
	ctx := context.Background()

	t.Run("runtime inside reconnect grace is left alone", func(t *testing.T) {
		env := newRunLifecycleEnv(t)
		id := env.seedRunTask(t, env.agentID, "cancel_requested", seedRunTaskOpts{started: true})
		markRuntimeOffline(t, env.pool, env.runtimeID, time.Now().Add(-5*time.Minute))

		if _, err := env.svc.ConvergeCancelRequestedForOfflineRuntimes(ctx, db.ConvergeCancelRequestedForOfflineRuntimesParams{
			ReconnectGraceSecs: 15 * 60,
			MaxPerTick:         100,
		}); err != nil {
			t.Fatalf("sweep within grace: %v", err)
		}
		if got := env.status(t, id); got != "cancel_requested" {
			t.Fatalf("row flipped inside grace window to %q", got)
		}
	})

	t.Run("cancel_requested on a dead runtime converges to cancelled", func(t *testing.T) {
		env := newRunLifecycleEnv(t)
		id := env.seedRunTask(t, env.agentID, "cancel_requested", seedRunTaskOpts{started: true})
		markRuntimeOffline(t, env.pool, env.runtimeID, time.Now().Add(-time.Hour))

		converged, err := env.svc.ConvergeCancelRequestedForOfflineRuntimes(ctx, db.ConvergeCancelRequestedForOfflineRuntimesParams{
			ReconnectGraceSecs: 60,
			MaxPerTick:         100,
		})
		if err != nil {
			t.Fatalf("sweep beyond grace: %v", err)
		}
		found := false
		for _, task := range converged {
			if task.ID == id {
				found = true
				if task.Status != "cancelled" {
					t.Fatalf("converged row status = %q, want cancelled", task.Status)
				}
			}
		}
		if !found {
			t.Fatal("cancel_requested row on an hour-dead runtime was not converged")
		}
	})
}

// --- AC5/AC6: retry gates, lineage and the 5s idempotency window ---

func TestRetryRunCreatesLineageChildForFinishedSource(t *testing.T) {
	for _, sourceStatus := range []string{"failed", "cancelled"} {
		t.Run(sourceStatus, func(t *testing.T) {
			// One env per source status: the first retry's queued child would
			// occupy the (issue, agent) serialization slot otherwise.
			env := newRunLifecycleEnv(t)
			source := env.seedRunTask(t, env.agentID, sourceStatus, seedRunTaskOpts{started: true})

			child, created, err := env.svc.RetryRun(context.Background(), util.MustParseUUID(env.issueID), source, env.creator(), nil)
			if err != nil {
				t.Fatalf("RetryRun: %v", err)
			}
			if !created {
				t.Fatal("first retry must create the child")
			}
			if child.Status != "queued" {
				t.Fatalf("child status = %q, want queued", child.Status)
			}
			if !child.RerunOfTaskID.Valid || util.UUIDToString(child.RerunOfTaskID) != util.UUIDToString(source) {
				t.Fatal("child must link to the source via rerun_of_task_id")
			}
			if !child.ForceFreshSession {
				t.Fatal("retry must run in a fresh session")
			}
			if util.UUIDToString(child.AgentID) != env.agentID {
				t.Fatal("retry targets the source run's agent")
			}
		})
	}
}

func TestRetryRunGates(t *testing.T) {
	env := newRunLifecycleEnv(t)
	ctx := context.Background()

	t.Run("in-flight source answers retry_source_not_finished", func(t *testing.T) {
		source := env.seedRunTask(t, env.agentID, "running", seedRunTaskOpts{started: true})
		_, _, err := env.svc.RetryRun(ctx, util.MustParseUUID(env.issueID), source, env.creator(), nil)
		if !errors.Is(err, ErrRetrySourceNotFinished) {
			t.Fatalf("err = %v, want ErrRetrySourceNotFinished", err)
		}
	})

	t.Run("unfinished descendant answers retry_descendant_active", func(t *testing.T) {
		source := env.seedRunTask(t, env.agentID, "failed", seedRunTaskOpts{started: true})
		staleChild := env.seedRunTask(t, env.agent2ID, "queued", seedRunTaskOpts{rerunOf: source})
		// Age the child past the 5s window so the idempotency path does not
		// swallow the conflict this case is about.
		if _, err := env.pool.Exec(ctx, `UPDATE agent_task_queue SET created_at = now() - interval '30 seconds' WHERE id = $1`, staleChild); err != nil {
			t.Fatalf("age child: %v", err)
		}
		_, _, err := env.svc.RetryRun(ctx, util.MustParseUUID(env.issueID), source, env.creator(), nil)
		if !errors.Is(err, ErrRetryDescendantActive) {
			t.Fatalf("err = %v, want ErrRetryDescendantActive", err)
		}
	})

	t.Run("busy (issue, agent) slot answers agent_already_queued", func(t *testing.T) {
		source := env.seedRunTask(t, env.agentID, "failed", seedRunTaskOpts{started: true})
		// A queued run of the SAME agent on this issue that is not a descendant
		// of the source: the serialization slot is taken.
		env.seedRunTask(t, env.agentID, "queued", seedRunTaskOpts{})
		_, _, err := env.svc.RetryRun(ctx, util.MustParseUUID(env.issueID), source, env.creator(), nil)
		if !errors.Is(err, ErrRetryAgentHasQueuedRun) {
			t.Fatalf("err = %v, want ErrRetryAgentHasQueuedRun", err)
		}
	})
}

func TestRetryRunWithinWindowReturnsExistingChild(t *testing.T) {
	env := newRunLifecycleEnv(t)
	source := env.seedRunTask(t, env.agentID, "failed", seedRunTaskOpts{started: true})

	first, created, err := env.svc.RetryRun(context.Background(), util.MustParseUUID(env.issueID), source, env.creator(), nil)
	if err != nil || !created {
		t.Fatalf("first retry: created=%v err=%v", created, err)
	}

	repeat, createdAgain, err := env.svc.RetryRun(context.Background(), util.MustParseUUID(env.issueID), source, env.creator(), nil)
	if err != nil {
		t.Fatalf("repeat retry: %v", err)
	}
	if createdAgain {
		t.Fatal("in-window repeat must not create a sibling")
	}
	if repeat.ID != first.ID {
		t.Fatalf("repeat returned run %s, want the original child %s", util.UUIDToString(repeat.ID), util.UUIDToString(first.ID))
	}

	var siblings int
	if err := env.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM agent_task_queue WHERE rerun_of_task_id = $1`, source).Scan(&siblings); err != nil {
		t.Fatalf("count children: %v", err)
	}
	if siblings != 1 {
		t.Fatalf("double retry produced %d children, want exactly 1", siblings)
	}
}

func TestRunLineageUnionsBothColumns(t *testing.T) {
	env := newRunLifecycleEnv(t)

	// root ← manual rerun (rerun_of) ← system retry (retry_of): a mixed chain.
	root := env.seedRunTask(t, env.agentID, "failed", seedRunTaskOpts{started: true})
	manual := env.seedRunTask(t, env.agent2ID, "cancelled", seedRunTaskOpts{rerunOf: root, started: true})
	child := env.seedRunTask(t, env.agent2ID, "failed", seedRunTaskOpts{rerunOf: manual, started: true})
	systemRetry := env.seedRunTask(t, env.agent2ID, "queued", seedRunTaskOpts{retryOf: child})

	q := db.New(env.pool)
	ancestors, err := q.ListRunAncestry(context.Background(), child)
	if err != nil {
		t.Fatalf("ListRunAncestry: %v", err)
	}
	if len(ancestors) != 3 { // root, manual, child itself
		t.Fatalf("ancestry length = %d, want 3 (root, manual, child)", len(ancestors))
	}

	descendants, err := q.ListRunDescendants(context.Background(), root)
	if err != nil {
		t.Fatalf("ListRunDescendants: %v", err)
	}
	if len(descendants) != 4 { // everything: manual, child, systemRetry, root itself
		t.Fatalf("descendants length = %d, want 4", len(descendants))
	}
	seen := map[string]bool{}
	for _, d := range descendants {
		seen[util.UUIDToString(d.ID)] = true
	}
	for _, want := range []pgtype.UUID{manual, child, systemRetry} {
		if !seen[util.UUIDToString(want)] {
			t.Fatalf("descendants of root missing %s", util.UUIDToString(want))
		}
	}
}

// --- AC1: the filtered run list (status merge buckets + trigger + limit) ---

func (e runLifecycleEnv) seedRunListSpectrum(t *testing.T) {
	t.Helper()
	cmtID := e.seedTriggerComment(t)

	// Primary agent: one pending row (unique-index slot), in-flight rows and
	// finished trigger sources. Second agent: the lineage children, parked in
	// their own pending slots.
	e.seedRunTask(t, e.agentID, "queued", seedRunTaskOpts{triggerCmtID: cmtID})
	e.seedRunTask(t, e.agentID, "waiting_local_directory", seedRunTaskOpts{started: true})
	e.seedRunTask(t, e.agentID, "running", seedRunTaskOpts{started: true})
	e.seedRunTask(t, e.agentID, "failed", seedRunTaskOpts{started: true})
	rerunSource := e.seedRunTask(t, e.agentID, "cancelled", seedRunTaskOpts{started: true})
	e.seedRunTask(t, e.agent2ID, "queued", seedRunTaskOpts{rerunOf: rerunSource})
	retrySource := e.seedRunTask(t, e.agentID, "failed", seedRunTaskOpts{started: true})
	e.seedRunTask(t, e.agent2ID, "failed", seedRunTaskOpts{retryOf: retrySource, started: true})
}

func listFiltered(t *testing.T, pool *pgxpool.Pool, issueID, statusFilter, triggerFilter string, limit int32) []db.AgentTaskQueue {
	t.Helper()
	q := db.New(pool)
	rows, err := q.ListTasksByIssueFiltered(context.Background(), db.ListTasksByIssueFilteredParams{
		IssueID:       util.MustParseUUID(issueID),
		StatusFilter:  pgtype.Text{String: statusFilter, Valid: statusFilter != ""},
		TriggerFilter: pgtype.Text{String: triggerFilter, Valid: triggerFilter != ""},
		RowLimit:      limit,
	})
	if err != nil {
		t.Fatalf("ListTasksByIssueFiltered: %v", err)
	}
	return rows
}

func statusesOf(rows []db.AgentTaskQueue) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Status)
	}
	return out
}

func TestRunListFilters(t *testing.T) {
	env := newRunLifecycleEnv(t)
	env.seedRunListSpectrum(t)

	t.Run("pending alias expands to the queued-family bucket", func(t *testing.T) {
		got := statusesOf(listFiltered(t, env.pool, env.issueID, "pending", "", 100))
		for _, s := range got {
			switch s {
			case "queued", "dispatched", "deferred", "waiting_local_directory":
			default:
				t.Fatalf("pending bucket leaked %q", s)
			}
		}
		if len(got) != 3 { // primary queued + primary waiting_local_directory + second agent's queued rerun child
			t.Fatalf("pending bucket = %v, want 3 queued-family rows", got)
		}
	})

	t.Run("explicit status filter honours in-flight and terminal rows", func(t *testing.T) {
		got := statusesOf(listFiltered(t, env.pool, env.issueID, "running,waiting_local_directory", "", 100))
		if len(got) != 2 {
			t.Fatalf("running+waiting filter = %v, want 2 rows", got)
		}
	})

	t.Run("trigger=rerun matches only rerun lineage", func(t *testing.T) {
		rows := listFiltered(t, env.pool, env.issueID, "", "rerun", 100)
		if len(rows) != 1 || !rows[0].RerunOfTaskID.Valid {
			t.Fatalf("trigger=rerun returned %d rows, want the single rerun child", len(rows))
		}
	})

	t.Run("trigger=system_retry matches only retry lineage", func(t *testing.T) {
		rows := listFiltered(t, env.pool, env.issueID, "", "system_retry", 100)
		if len(rows) != 1 || !rows[0].RetryOfTaskID.Valid {
			t.Fatalf("trigger=system_retry returned %d rows, want the single retry child", len(rows))
		}
	})

	t.Run("trigger=comment matches the comment-triggered run", func(t *testing.T) {
		rows := listFiltered(t, env.pool, env.issueID, "", "comment", 100)
		if len(rows) != 1 || !rows[0].TriggerCommentID.Valid {
			t.Fatalf("trigger=comment returned %d rows, want the comment-triggered row", len(rows))
		}
	})

	t.Run("limit caps the page", func(t *testing.T) {
		rows := listFiltered(t, env.pool, env.issueID, "", "", 2)
		if len(rows) != 2 {
			t.Fatalf("limit=2 returned %d rows", len(rows))
		}
	})

	t.Run("unknown filter matches nothing under 200 semantics", func(t *testing.T) {
		rows := listFiltered(t, env.pool, env.issueID, "teleported", "", 100)
		if len(rows) != 0 {
			t.Fatalf("unknown status filter returned %d rows, want 0", len(rows))
		}
	})
}
