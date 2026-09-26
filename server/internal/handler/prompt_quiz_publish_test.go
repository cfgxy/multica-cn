package handler

// Owner Q10, asserted mechanically: the quiz is a label, never a gate
// (RUYI-185).
//
// "Publishing still works when the quiz is unhappy" is not assertable by
// posting a version and seeing 200 — that passes just as well after someone
// wires a quiz check into the publish path, as long as the check happens to
// agree. So these tests do two things at once:
//
//  1. Put the quiz data into each of the three worst states the dispatch card
//     names — every measurement failed, no measurements at all, sweep job
//     wedged — and publish through the real write path.
//  2. Run that publish through a query layer that records every statement
//     touching a prompt_quiz table. A publish that consulted quiz data would
//     have to read one, so the recording is what turns "no dependency" from a
//     convention into a check.
//
// TestQuizTripwireFiresOnADeliberateDependency proves (2) is not vacuous: it
// introduces the dependency the rule forbids and requires the tripwire to
// catch it. If the tripwire were wired up wrong, that test fails rather than
// the suite quietly passing forever.

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/promptquiz"
)

// quizTripwire records SQL that reads or writes quiz state.
type quizTripwire struct {
	mu   sync.Mutex
	seen []string
}

func (tw *quizTripwire) inspect(sql string) {
	if !strings.Contains(sql, "prompt_quiz") {
		return
	}
	tw.mu.Lock()
	defer tw.mu.Unlock()
	tw.seen = append(tw.seen, strings.Join(strings.Fields(sql), " "))
}

func (tw *quizTripwire) tripped() []string {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	return append([]string(nil), tw.seen...)
}

type quizTripwireDB struct {
	inner dbExecutor
	tw    *quizTripwire
}

func (d quizTripwireDB) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	d.tw.inspect(sql)
	return d.inner.Exec(ctx, sql, args...)
}

func (d quizTripwireDB) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	d.tw.inspect(sql)
	return d.inner.Query(ctx, sql, args...)
}

func (d quizTripwireDB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	d.tw.inspect(sql)
	return d.inner.QueryRow(ctx, sql, args...)
}

// quizTripwireTx carries the same watch inside the publish transaction, which
// is where commitPromptGovernanceVersion does all of its real work.
type quizTripwireTx struct {
	pgx.Tx
	tw *quizTripwire
}

func (t quizTripwireTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	t.tw.inspect(sql)
	return t.Tx.Exec(ctx, sql, args...)
}

func (t quizTripwireTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	t.tw.inspect(sql)
	return t.Tx.Query(ctx, sql, args...)
}

func (t quizTripwireTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	t.tw.inspect(sql)
	return t.Tx.QueryRow(ctx, sql, args...)
}

type quizTripwireTxStarter struct {
	inner txStarter
	tw    *quizTripwire
}

func (s quizTripwireTxStarter) Begin(ctx context.Context) (pgx.Tx, error) {
	tx, err := s.inner.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return quizTripwireTx{Tx: tx, tw: s.tw}, nil
}

// watchedPublishHandler returns a copy of the suite handler whose every
// statement passes the tripwire.
func watchedPublishHandler(tw *quizTripwire) *Handler {
	watched := *testHandler
	watched.DB = quizTripwireDB{inner: testHandler.DB, tw: tw}
	watched.Queries = db.New(quizTripwireDB{inner: testHandler.DB, tw: tw})
	watched.TxStarter = quizTripwireTxStarter{inner: testHandler.TxStarter, tw: tw}
	return &watched
}

// seedQuizItem inserts one bank entry for the worst-state fixtures to point at.
func seedQuizItem(t *testing.T, slug string) string {
	t.Helper()
	return dbfx.Insert(t, "prompt_quiz_item", map[string]any{
		"workspace_id":    testWorkspaceID,
		"slug":            slug,
		"title":           "cost probe",
		"body":            "Summarise the steps you would take to triage a failing build.",
		"runtime_profile": "member",
	})
}

// seedFailedMeasurements records `count` graded failures against the scope, the
// worst possible reading a version can have while still having been measured:
// the sample is full, so nothing can be dismissed as "not measured yet", and
// every entry is a failure at a runaway cost.
func seedFailedMeasurements(t *testing.T, scopeID, itemID string, version int32, count int) {
	t.Helper()
	runOwner := dbfx.Agent(t, "quiz-publish-run-owner-"+scopeID[:8], handlerTestRuntimeID(t))
	batchID := newQuizUUID(t)
	for i := 0; i < count; i++ {
		taskID := dbfx.Task(t, runOwner, map[string]any{
			"originator_source": promptquiz.OriginatorSource,
			"status":            "completed",
			// agent_task_queue_active_requires_runtime (migration 251): a row
			// may only lack a runtime once it is finished.
			"completed_at": testutil.Raw("now()"),
		})
		dbfx.Insert(t, "prompt_quiz_result", map[string]any{
			"workspace_id":     testWorkspaceID,
			"scope":            "agent",
			"scope_id":         scopeID,
			"version":          version,
			"item_id":          itemID,
			"item_revision":    1,
			"item_body_sha256": promptquiz.BodyDigest("anchor"),
			"batch_id":         batchID,
			"task_id":          taskID,
			"outcome":          promptquiz.OutcomeFailed,
			"run_tokens":       9_000_000,
		})
	}
}

// newQuizUUID mints a batch id. The database generates it so the test does not
// depend on a UUID library it otherwise has no use for.
func newQuizUUID(t *testing.T) string {
	t.Helper()
	var id string
	dbfx.QueryRow(t, `SELECT gen_random_uuid()::text`).Scan(&id)
	return id
}

func publishThroughWatchedPath(t *testing.T, tw *quizTripwire, agentID, content string) {
	t.Helper()
	watched := watchedPublishHandler(tw)
	code, raw := callPromptGov(t, watched.SavePromptGovernanceVersion, http.MethodPost,
		"/api/prompt-governance/agent/"+agentID+"/versions",
		map[string]any{"content": content, "change_note": "publish under a bad quiz"},
		map[string]string{"scope": "agent", "scopeId": agentID})
	if code != http.StatusOK {
		t.Fatalf("publish returned %d: %s — the quiz must never be able to hold a release (Owner Q10)", code, raw)
	}
	if tripped := tw.tripped(); len(tripped) > 0 {
		t.Fatalf("the publish path read quiz state, which makes the quiz a gate:\n  %s",
			strings.Join(tripped, "\n  "))
	}
	var stored string
	dbfx.QueryRow(t, `SELECT instructions FROM agent WHERE id = $1`, agentID).Scan(&stored)
	if stored != content {
		t.Fatalf("agent.instructions = %q, want %q — publish reported success without taking effect", stored, content)
	}
}

// ── worst state 1: every measurement failed ────────────────────────────────

func TestPublishSucceedsWhenEveryQuizMeasurementFailed(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := dbfx.Agent(t, "quiz-publish-all-failed", handlerTestRuntimeID(t),
		map[string]any{"instructions": "before"})
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM prompt_version WHERE scope = 'agent' AND scope_id = $1`, agentID)
		testPool.Exec(context.Background(), `DELETE FROM prompt_quiz_result WHERE scope_id = $1`, agentID)
	})
	itemID := seedQuizItem(t, "all-failed-probe")
	seedFailedMeasurements(t, agentID, itemID, 1, promptquiz.NewVersionSampleSize)

	publishThroughWatchedPath(t, &quizTripwire{}, agentID, "after an all-failed quiz")
}

// ── worst state 2: no measurements at all ──────────────────────────────────

func TestPublishSucceedsWithNoQuizMeasurementsAtAll(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := dbfx.Agent(t, "quiz-publish-no-data", handlerTestRuntimeID(t),
		map[string]any{"instructions": "before"})
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM prompt_version WHERE scope = 'agent' AND scope_id = $1`, agentID)
	})
	if n := dbfx.Count(t, `SELECT count(*) FROM prompt_quiz_result WHERE scope_id = $1`, agentID); n != 0 {
		t.Fatalf("fixture leak: %d measurements exist for a scope that must have none", n)
	}

	publishThroughWatchedPath(t, &quizTripwire{}, agentID, "after no quiz at all")
}

// ── worst state 3: the sweep job is wedged ─────────────────────────────────

func TestPublishSucceedsWhileTheQuizSweepIsWedged(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	var prevStarted, prevFinished *time.Time
	var prevErr *string
	testPool.QueryRow(ctx,
		`SELECT last_run_started_at, last_run_finished_at, last_error FROM prompt_quiz_sweep_state WHERE id = 1`,
	).Scan(&prevStarted, &prevFinished, &prevErr)
	t.Cleanup(func() {
		testPool.Exec(ctx, `UPDATE prompt_quiz_sweep_state
			SET last_run_started_at = $1, last_run_finished_at = $2, last_error = $3 WHERE id = 1`,
			prevStarted, prevFinished, prevErr)
	})

	// Started days ago, never finished, last attempt errored: what an operator
	// sees when the job is stuck rather than merely behind.
	dbfx.Exec(t, `UPDATE prompt_quiz_sweep_state
		SET last_run_started_at = now() - interval '5 days',
		    last_run_finished_at = NULL,
		    last_error = 'stuck: no worker completed this sweep'
		WHERE id = 1`)

	agentID := dbfx.Agent(t, "quiz-publish-wedged-sweep", handlerTestRuntimeID(t),
		map[string]any{"instructions": "before"})
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM prompt_version WHERE scope = 'agent' AND scope_id = $1`, agentID)
	})

	publishThroughWatchedPath(t, &quizTripwire{}, agentID, "after a wedged sweep")
}

// ── the tripwire must actually catch a dependency ──────────────────────────

// TestQuizTripwireFiresOnADeliberateDependency is the counterweight to the
// three tests above. It performs exactly the read a quiz gate would need — "is
// this version's current reading a regression?" — through the watched query
// layer, and requires the watch to notice. Without this, the three assertions
// above would still pass if the tripwire silently watched nothing.
func TestQuizTripwireFiresOnADeliberateDependency(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := dbfx.Agent(t, "quiz-tripwire-selftest", handlerTestRuntimeID(t))
	tw := &quizTripwire{}
	watched := watchedPublishHandler(tw)

	// The forbidden dependency, written out in full: read the measurements
	// before deciding whether to publish.
	_, err := watched.Queries.ListPromptQuizSamples(context.Background(), db.ListPromptQuizSamplesParams{
		Scope:    "agent",
		ScopeID:  parseUUID(agentID),
		Version:  1,
		RowLimit: 10,
	})
	if err != nil {
		t.Fatalf("read quiz samples: %v", err)
	}
	if len(tw.tripped()) == 0 {
		t.Fatal("the tripwire did not fire on a direct quiz read, so the three no-gate assertions prove nothing")
	}
}
