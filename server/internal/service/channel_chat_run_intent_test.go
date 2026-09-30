package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// runIntentFixture bundles the seeded rows one run-intent test needs: the
// attribution fixture (workspace/user/agent), a channel chat session with its
// binding + generation, and the binding identity a ledger row must carry to
// pass the delivery fences.
type runIntentFixture struct {
	pool           *pgxpool.Pool
	workspaceID    string
	userID         string
	agentID        string
	chatSessionID  string
	bindingID      string
	routeRevision  int64
	chatSession    db.ChatSession
	svc            *TaskService
	reconcilerPool *pgxpool.Pool
}

func seedRunIntentFixture(t *testing.T) runIntentFixture {
	t.Helper()
	pool := newResolveOriginatorPool(t)
	ctx := context.Background()
	q := db.New(pool)
	workspaceID, userID, agentID, _ := seedAttributionFixture(t, pool)
	chatSessionID := seedChannelChatSession(t, ctx, pool, workspaceID, agentID, userID)

	var bindingID string
	var routeRevision int64
	if err := pool.QueryRow(ctx, `
		SELECT id, route_revision FROM channel_chat_session_binding WHERE chat_session_id = $1
	`, chatSessionID).Scan(&bindingID, &routeRevision); err != nil {
		t.Fatalf("load seeded binding: %v", err)
	}

	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM channel_chat_run_intent WHERE chat_session_id = $1`, chatSessionID)
	})

	f := runIntentFixture{
		pool: pool, workspaceID: workspaceID, userID: userID, agentID: agentID,
		chatSessionID: chatSessionID, bindingID: bindingID, routeRevision: routeRevision,
		chatSession: db.ChatSession{
			ID: util.MustParseUUID(chatSessionID), AgentID: util.MustParseUUID(agentID),
		},
	}
	f.svc = &TaskService{Queries: q, TxStarter: pool, Bus: events.New()}
	return f
}

// seedRunIntent inserts a ledger row; due rows are claimable by the
// reconciler immediately.
func (f runIntentFixture) seedRunIntent(t *testing.T, revision int64, due bool, forceFresh bool) pgtype.UUID {
	t.Helper()
	fireAt := "now() + interval '6 seconds'"
	if due {
		fireAt = "now() - interval '1 minute'"
	}
	var id pgtype.UUID
	if err := f.pool.QueryRow(context.Background(), fmt.Sprintf(`
		INSERT INTO channel_chat_run_intent (
			id, workspace_id, chat_session_id, context_revision, initiator_user_id,
			force_fresh, binding_id, route_revision, state, fire_at
		) VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, $6, $7, 'pending', %s)
		RETURNING id`, fireAt),
		f.workspaceID, f.chatSessionID, revision, f.userID, forceFresh,
		f.bindingID, f.routeRevision).Scan(&id); err != nil {
		t.Fatalf("seed run intent: %v", err)
	}
	return id
}

func (f runIntentFixture) intentState(t *testing.T, revision int64) (string, pgtype.Text, int) {
	t.Helper()
	var state string
	var deadReason pgtype.Text
	var tasks int
	if err := f.pool.QueryRow(context.Background(), `
		SELECT state, COALESCE(dead_reason, '') FROM channel_chat_run_intent
		WHERE chat_session_id = $1 AND context_revision = $2
	`, f.chatSessionID, revision).Scan(&state, &deadReason); err != nil {
		t.Fatalf("load intent state: %v", err)
	}
	if err := f.pool.QueryRow(context.Background(), `
		SELECT count(*) FROM agent_task_queue WHERE chat_session_id = $1
	`, f.chatSessionID).Scan(&tasks); err != nil {
		t.Fatalf("count tasks: %v", err)
	}
	return state, deadReason, tasks
}

func TestEnqueueChannelChatTask_FiresPendingIntentAndBlocksDuplicate(t *testing.T) {
	f := seedRunIntentFixture(t)
	ctx := context.Background()
	f.seedRunIntent(t, 1, false, false)

	if _, err := f.svc.EnqueueChannelChatTask(ctx, f.chatSession, util.MustParseUUID(f.userID), false, 1,
		util.MustParseUUID(f.bindingID), f.routeRevision); err != nil {
		t.Fatalf("EnqueueChannelChatTask: %v", err)
	}
	state, _, tasks := f.intentState(t, 1)
	if state != "fired" || tasks != 1 {
		t.Fatalf("after enqueue intent/task = %s/%d, want fired/1", state, tasks)
	}

	// A second flush for the same window must not create a second task: the
	// settled row makes the CAS a no-op and the settled-state read skips.
	_, err := f.svc.EnqueueChannelChatTask(ctx, f.chatSession, util.MustParseUUID(f.userID), false, 1,
		util.MustParseUUID(f.bindingID), f.routeRevision)
	if !errors.Is(err, ErrChatRunIntentAlreadyFired) {
		t.Fatalf("duplicate enqueue error = %v, want ErrChatRunIntentAlreadyFired", err)
	}
	state, _, tasks = f.intentState(t, 1)
	if state != "fired" || tasks != 1 {
		t.Fatalf("after duplicate intent/task = %s/%d, want fired/1", state, tasks)
	}
}

func TestEnqueueChannelChatTask_ConcurrentTriggersEnqueueExactlyOnce(t *testing.T) {
	f := seedRunIntentFixture(t)
	ctx := context.Background()
	f.seedRunIntent(t, 1, false, false)

	const racers = 2
	errs := make(chan error, racers)
	for i := 0; i < racers; i++ {
		go func() {
			_, err := f.svc.EnqueueChannelChatTask(ctx, f.chatSession, util.MustParseUUID(f.userID), false, 1,
				util.MustParseUUID(f.bindingID), f.routeRevision)
			errs <- err
		}()
	}
	fired, skipped := 0, 0
	for i := 0; i < racers; i++ {
		switch err := <-errs; {
		case err == nil:
			fired++
		case errors.Is(err, ErrChatRunIntentAlreadyFired):
			skipped++
		default:
			t.Fatalf("concurrent enqueue error = %v", err)
		}
	}
	if fired != 1 || skipped != racers-1 {
		t.Fatalf("concurrent trigger outcome fired/skipped = %d/%d, want 1/%d", fired, skipped, racers-1)
	}
	if _, _, tasks := f.intentState(t, 1); tasks != 1 {
		t.Fatalf("tasks after race = %d, want 1", tasks)
	}
}

func TestEnqueueChannelChatTask_WithoutIntentRowStillEnqueues(t *testing.T) {
	f := seedRunIntentFixture(t)
	ctx := context.Background()

	if _, err := f.svc.EnqueueChannelChatTask(ctx, f.chatSession, util.MustParseUUID(f.userID), false, 1,
		util.MustParseUUID(f.bindingID), f.routeRevision); err != nil {
		t.Fatalf("EnqueueChannelChatTask without ledger row: %v", err)
	}
	var tasks int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM agent_task_queue WHERE chat_session_id = $1`,
		f.chatSessionID).Scan(&tasks); err != nil {
		t.Fatalf("count tasks: %v", err)
	}
	if tasks != 1 {
		t.Fatalf("tasks = %d, want 1 (pre-ledger compatibility)", tasks)
	}
}

func TestEnqueueChannelChatTask_EnqueueFailureLeavesIntentPending(t *testing.T) {
	f := seedRunIntentFixture(t)
	ctx := context.Background()
	f.seedRunIntent(t, 1, false, false)

	// Inject on a :exec statement INSIDE the enqueue transaction (sqlc :one
	// statements go through QueryRow and bypass the Exec seam): a failure
	// after the CAS rolls the fired flip back with the transaction.
	injected := errors.New("injected seal-input failure")
	failing := &TaskService{
		Queries:   db.New(f.pool),
		TxStarter: &failNamedExecTxStarter{pool: f.pool, queryName: "LinkUnownedChannelChatMessagesToTask", err: injected},
		Bus:       events.New(),
	}
	if _, err := failing.EnqueueChannelChatTask(ctx, f.chatSession, util.MustParseUUID(f.userID), false, 1,
		util.MustParseUUID(f.bindingID), f.routeRevision); !errors.Is(err, injected) {
		t.Fatalf("EnqueueChannelChatTask error = %v, want injected failure", err)
	}
	state, _, tasks := f.intentState(t, 1)
	if state != "pending" || tasks != 0 {
		t.Fatalf("after rollback intent/task = %s/%d, want pending/0", state, tasks)
	}
}

func TestMarkChatRunIntentDead_WinsExactlyOnce(t *testing.T) {
	f := seedRunIntentFixture(t)
	ctx := context.Background()
	f.seedRunIntent(t, 1, false, false)

	won, err := f.svc.MarkChatRunIntentDead(ctx, util.MustParseUUID(f.chatSessionID), 1, "agent_offline", "boom")
	if err != nil || !won {
		t.Fatalf("first MarkChatRunIntentDead = %v/%v, want true/nil", won, err)
	}
	won, err = f.svc.MarkChatRunIntentDead(ctx, util.MustParseUUID(f.chatSessionID), 1, "agent_offline", "boom")
	if err != nil || won {
		t.Fatalf("second MarkChatRunIntentDead = %v/%v, want false/nil", won, err)
	}
	state, reason, _ := f.intentState(t, 1)
	if state != "dead" || reason.String != "agent_offline" {
		t.Fatalf("dead state/reason = %s/%q, want dead/agent_offline", state, reason.String)
	}
}

func newTestRunReconciler(f runIntentFixture) *ChannelChatRunReconciler {
	return &ChannelChatRunReconciler{
		Queries: db.New(f.pool),
		Tasks:   f.svc,
	}
}

func TestChannelChatRunReconciler_CompensatesDueIntent(t *testing.T) {
	f := seedRunIntentFixture(t)
	f.seedRunIntent(t, 1, true, false)

	newTestRunReconciler(f).RunOnce(context.Background())

	state, _, tasks := f.intentState(t, 1)
	if state != "fired" || tasks != 1 {
		t.Fatalf("after compensation intent/task = %s/%d, want fired/1", state, tasks)
	}
}

func TestChannelChatRunReconciler_SessionArchivedIsTerminal(t *testing.T) {
	f := seedRunIntentFixture(t)
	ctx := context.Background()
	f.seedRunIntent(t, 1, true, false)
	if _, err := f.pool.Exec(ctx, `UPDATE chat_session SET status = 'archived' WHERE id = $1`, f.chatSessionID); err != nil {
		t.Fatalf("archive chat session: %v", err)
	}

	newTestRunReconciler(f).RunOnce(ctx)

	state, reason, tasks := f.intentState(t, 1)
	if state != "dead" || reason.String != "session_archived" || tasks != 0 {
		t.Fatalf("archived-session outcome state/reason/tasks = %s/%q/%d, want dead/session_archived/0", state, reason.String, tasks)
	}
}

func TestChannelChatRunReconciler_RouteSupersededIsTerminalWithoutTask(t *testing.T) {
	f := seedRunIntentFixture(t)
	ctx := context.Background()
	// Arm the intent against a binding that is not the session's real one —
	// the durable shape of "superseded by a newer generation".
	f.seedRunIntentSuperseded(t, 1)

	newTestRunReconciler(f).RunOnce(ctx)

	state, reason, tasks := f.intentState(t, 1)
	if state != "dead" || reason.String != "route_superseded" || tasks != 0 {
		t.Fatalf("superseded outcome state/reason/tasks = %s/%q/%d, want dead/route_superseded/0", state, reason.String, tasks)
	}
}

func (f runIntentFixture) seedRunIntentSuperseded(t *testing.T, revision int64) {
	t.Helper()
	if _, err := f.pool.Exec(context.Background(), `
		INSERT INTO channel_chat_run_intent (
			id, workspace_id, chat_session_id, context_revision, initiator_user_id,
			force_fresh, binding_id, route_revision, state, fire_at
		) VALUES (gen_random_uuid(), $1, $2, $3, $4, false, gen_random_uuid(), $3, 'pending', now() - interval '1 minute')
	`, f.workspaceID, f.chatSessionID, revision, f.userID); err != nil {
		t.Fatalf("seed superseded intent: %v", err)
	}
}

func TestChannelChatRunReconciler_TransientFailureReleasesThenExhausts(t *testing.T) {
	f := seedRunIntentFixture(t)
	ctx := context.Background()
	intentID := f.seedRunIntent(t, 1, true, false)

	// Transient: the enqueue transaction fails on an unrelated statement and
	// the CAS rolls back with it.
	failing := &TaskService{
		Queries:   db.New(f.pool),
		TxStarter: &failNamedExecTxStarter{pool: f.pool, queryName: "LinkUnownedChannelChatMessagesToTask", err: errors.New("transient enqueue failure")},
		Bus:       events.New(),
	}
	rec := &ChannelChatRunReconciler{Queries: db.New(f.pool), Tasks: failing}
	rec.RunOnce(ctx)

	var attempts int
	var nextAttempt pgtype.Timestamptz
	var state string
	if err := f.pool.QueryRow(ctx, `
		SELECT attempts, next_attempt_at, state FROM channel_chat_run_intent WHERE id = $1`, intentID).
		Scan(&attempts, &nextAttempt, &state); err != nil {
		t.Fatalf("load released intent: %v", err)
	}
	if state != "pending" || attempts != 1 || !nextAttempt.Valid || !nextAttempt.Time.After(time.Now()) {
		t.Fatalf("released intent attempts/next/state = %d/%v/%s, want 1/future/pending", attempts, nextAttempt, state)
	}

	// Burn the remaining budget, then the next sweep terminalizes the row.
	for i := 1; i < maxChatRunReconcileAttempts; i++ {
		if _, err := f.pool.Exec(ctx, `UPDATE channel_chat_run_intent SET next_attempt_at = now() - interval '1 minute' WHERE id = $1`, intentID); err != nil {
			t.Fatalf("re-arm intent: %v", err)
		}
		rec.RunOnce(ctx)
	}
	state, reason, tasks := f.intentState(t, 1)
	if state != "dead" || reason.String != "retries_exhausted" || tasks != 0 {
		t.Fatalf("exhausted outcome state/reason/tasks = %s/%q/%d, want dead/retries_exhausted/0", state, reason.String, tasks)
	}
}

func TestChannelChatRunReconciler_ExpiredLeaseIsReclaimable(t *testing.T) {
	f := seedRunIntentFixture(t)
	ctx := context.Background()
	intentID := f.seedRunIntent(t, 1, true, false)
	staleLease := pgtype.UUID{Bytes: [16]byte{1, 2, 3}, Valid: true}
	if _, err := f.pool.Exec(ctx, `
		UPDATE channel_chat_run_intent
		SET claimed_by = $2, claim_expires_at = now() - interval '1 minute'
		WHERE id = $1`, intentID, staleLease); err != nil {
		t.Fatalf("seed stale lease: %v", err)
	}

	newTestRunReconciler(f).RunOnce(ctx)

	state, _, tasks := f.intentState(t, 1)
	if state != "fired" || tasks != 1 {
		t.Fatalf("after lease-expiry reclaim intent/task = %s/%d, want fired/1", state, tasks)
	}
}
