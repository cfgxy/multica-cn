package promptquizsweep

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/promptquiz"
)

var (
	testPool        *pgxpool.Pool
	testWorkspaceID string
	testUserID      string
)

const (
	fixtureEmail = "promptquizsweep-fixture@multica.ai"
	fixtureSlug  = "promptquizsweep-fixtures"
)

// TestMain follows internal/promptqualityrollup's contract: without a reachable
// database the suite exits green rather than red, so a checkout without
// Postgres still runs the rest of the tree.
func TestMain(m *testing.M) {
	ctx := context.Background()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://multica:multica@localhost:5432/multica?sslmode=disable"
	}
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		fmt.Printf("Skipping quiz sweep DB tests: could not connect: %v\n", err)
		os.Exit(m.Run())
	}
	if err := pool.Ping(ctx); err != nil {
		fmt.Printf("Skipping quiz sweep DB tests: database not reachable: %v\n", err)
		pool.Close()
		os.Exit(m.Run())
	}
	if err := seed(ctx, pool); err != nil {
		fmt.Printf("Skipping quiz sweep DB tests: seed failed: %v\n", err)
		pool.Close()
		os.Exit(m.Run())
	}
	testPool = pool
	code := m.Run()
	_, _ = pool.Exec(ctx, `DELETE FROM workspace WHERE slug = $1`, fixtureSlug)
	_, _ = pool.Exec(ctx, `DELETE FROM "user" WHERE email = $1`, fixtureEmail)
	pool.Close()
	os.Exit(code)
}

func seed(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, `DELETE FROM workspace WHERE slug = $1`, fixtureSlug); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, `DELETE FROM "user" WHERE email = $1`, fixtureEmail); err != nil {
		return err
	}
	if err := pool.QueryRow(ctx, `INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id`,
		"Prompt Quiz Sweep Fixture", fixtureEmail).Scan(&testUserID); err != nil {
		return err
	}
	if err := pool.QueryRow(ctx, `INSERT INTO workspace (name, slug, issue_prefix) VALUES ($1, $2, $3) RETURNING id`,
		"Prompt Quiz Sweep Fixtures", fixtureSlug, "PQS").Scan(&testWorkspaceID); err != nil {
		return err
	}
	_, err := pool.Exec(ctx, `INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'owner')`,
		testWorkspaceID, testUserID)
	return err
}

// bank is one prepared sweep target: an agent with a runtime, a current prompt
// version and one active quiz item.
type bank struct {
	f         *testutil.Fixture
	runner    Runner
	agentID   string
	runtimeID string
	itemID    string
	version   int32
}

func newBank(t *testing.T) *bank {
	t.Helper()
	if testPool == nil {
		t.Skip("database not available")
	}
	f := testutil.New(testPool, testWorkspaceID, testUserID)

	// The sweep's audit row is global and single-row. Restoring it keeps a tick
	// run by this suite from being mistaken for the deployment's last sweep.
	var savedEnqueued, savedCollected int32
	f.QueryRow(t, `SELECT last_enqueued, last_collected FROM prompt_quiz_sweep_state WHERE id = 1`).
		Scan(&savedEnqueued, &savedCollected)
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(),
			`UPDATE prompt_quiz_sweep_state SET last_enqueued = $1, last_collected = $2, last_error = NULL WHERE id = 1`,
			savedEnqueued, savedCollected)
	})

	runtimeID := f.Runtime(t, "quiz-sweep-runtime")
	agentID := f.Agent(t, "quiz-sweep-agent", runtimeID)
	b := &bank{f: f, runner: Runner{Queries: db.New(testPool)}, agentID: agentID, runtimeID: runtimeID, version: 3}

	f.Insert(t, "prompt_version", testutil.Cols{
		"workspace_id":   testWorkspaceID,
		"scope":          "agent",
		"scope_id":       agentID,
		"version":        b.version,
		"content":        "quiz sweep fixture prompt",
		"content_sha256": "0000000000000000000000000000000000000000000000000000000000000000",
		"source":         "import",
	})
	b.itemID = f.Insert(t, "prompt_quiz_item", testutil.Cols{
		"workspace_id":    testWorkspaceID,
		"slug":            "sweep-item",
		"title":           "Sweep item",
		"body":            "Summarise your operating constraints in one sentence.",
		"runtime_profile": "member",
	})

	// Rows the sweep itself creates are not registered by the fixture, so they
	// are removed by owner here. task_usage cascades with its task.
	f.Cleanup(t, `DELETE FROM prompt_quiz_result WHERE scope = 'agent' AND scope_id = $1`, agentID)
	f.Cleanup(t, `DELETE FROM agent_task_queue WHERE agent_id = $1`, agentID)
	return b
}

// quizTasks counts the quiz runs ordered against this bank's agent.
func (b *bank) quizTasks(t *testing.T) int {
	t.Helper()
	return b.f.Count(t, `SELECT count(*) FROM agent_task_queue
		WHERE agent_id = $1 AND originator_source = $2 AND issue_id IS NULL`,
		b.agentID, promptquiz.OriginatorSource)
}

func (b *bank) results(t *testing.T) int {
	t.Helper()
	return b.f.Count(t, `SELECT count(*) FROM prompt_quiz_result WHERE scope = 'agent' AND scope_id = $1`, b.agentID)
}

func (b *bank) run(t *testing.T) Outcome {
	t.Helper()
	out, err := b.runner.Run(context.Background())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	return out
}

// TestSweepDoesNotReorderWhileTheFirstSampleIsStillRunning is the idempotence
// assertion for the periodic job (RUYI-185 acceptance 7).
//
// The sweep's stop condition is "how far is this version from N", and the runs
// it already ordered have not produced results yet — they are queued. If the
// remainder were computed from stored results alone, every tick until the first
// run finishes would re-order the full N, and an hourly cadence would leave the
// version with a multiple of N measurements. This test fails if the in-flight
// term is dropped from CountPromptQuizMeasurementsForVersion: the second tick
// would order another NewVersionSampleSize runs and the count would double.
func TestSweepDoesNotReorderWhileTheFirstSampleIsStillRunning(t *testing.T) {
	b := newBank(t)

	// Tick until the sample is full rather than asserting one tick fills it:
	// EnqueueLimit is a per-tick budget shared with every other scope in the
	// deployment, so the fill may legitimately take more than one tick.
	filled := 0
	for tick := 0; tick < 5 && filled < promptquiz.NewVersionSampleSize; tick++ {
		b.run(t)
		filled = b.quizTasks(t)
	}
	if filled != promptquiz.NewVersionSampleSize {
		t.Fatalf("sample filled to %d quiz runs, want N=%d", filled, promptquiz.NewVersionSampleSize)
	}

	// The sample is now entirely in flight: nothing has finished, so nothing is
	// stored. A tick that counted only stored results would order the whole N
	// again here.
	if stored := b.results(t); stored != 0 {
		t.Fatalf("%d measurements stored before any run finished, want 0", stored)
	}
	extra := b.run(t)
	if extra.Enqueued != 0 {
		t.Errorf("tick over a full in-flight sample enqueued %d, want 0", extra.Enqueued)
	}
	if got := b.quizTasks(t); got != promptquiz.NewVersionSampleSize {
		t.Errorf("%d quiz runs after the extra tick, want N=%d", got, promptquiz.NewVersionSampleSize)
	}
}

// TestSweepOrdersRunsOnTheNoIssuePath is the isolation property at the sweep
// layer: what the sweep creates must be invisible to issue-dimension queries
// and to production run statistics (Owner Q18-A).
func TestSweepOrdersRunsOnTheNoIssuePath(t *testing.T) {
	b := newBank(t)
	b.run(t)

	if n := b.f.Count(t, `SELECT count(*) FROM agent_task_queue WHERE agent_id = $1 AND issue_id IS NOT NULL`, b.agentID); n != 0 {
		t.Errorf("%d quiz runs carry an issue_id, want 0", n)
	}
	if n := b.f.Count(t, `SELECT count(*) FROM agent_task_queue
		WHERE agent_id = $1 AND (originator_source IS DISTINCT FROM $2 OR trigger_evidence_kind IS DISTINCT FROM $3)`,
		b.agentID, promptquiz.OriginatorSource, promptquiz.TriggerEvidenceKind); n != 0 {
		t.Errorf("%d quiz runs are missing the quiz source or evidence tag, want 0", n)
	}
	if n := b.f.Count(t, `SELECT count(*) FROM agent_task_queue
		WHERE agent_id = $1 AND trigger_evidence_ref_id = $2`, b.agentID, b.itemID); n != promptquiz.NewVersionSampleSize {
		t.Errorf("%d quiz runs point at the item under test, want N=%d", n, promptquiz.NewVersionSampleSize)
	}
}

// TestSweepCollectsEachRunExactlyOnce covers collection idempotence: the
// collector upserts on task_id, so a tick re-entered after a crash rewrites the
// measurement it already wrote instead of doubling the sample.
func TestSweepCollectsEachRunExactlyOnce(t *testing.T) {
	b := newBank(t)
	taskID := b.finishedRun(t, `{"agent": 3}`)

	if out := b.run(t); out.Collected != 1 {
		t.Fatalf("first tick collected %d, want 1", out.Collected)
	}
	if out := b.run(t); out.Collected != 0 {
		t.Errorf("second tick collected %d, want 0 — the run already has a measurement", out.Collected)
	}
	if got := b.results(t); got != 1 {
		t.Fatalf("%d measurements for one finished run, want 1", got)
	}

	var version int32
	var outcome string
	var revision int32
	b.f.QueryRow(t, `SELECT version, outcome, item_revision FROM prompt_quiz_result WHERE task_id = $1`, taskID).
		Scan(&version, &outcome, &revision)
	if version != b.version {
		t.Errorf("measurement recorded version %d, want %d", version, b.version)
	}
	if outcome != promptquiz.OutcomePassed {
		t.Errorf("measurement outcome %q, want %q", outcome, promptquiz.OutcomePassed)
	}
	if revision != 1 {
		t.Errorf("measurement item_revision %d, want 1", revision)
	}
}

// TestSweepSkipsRunsWithNoAgentTierVersion covers migration 917's convention:
// an absent tier key is not version 0. Such a run measured no version and must
// be left uncollected rather than attributed to one.
func TestSweepSkipsRunsWithNoAgentTierVersion(t *testing.T) {
	b := newBank(t)
	b.finishedRun(t, `{"workspace": 4}`)

	if out := b.run(t); out.Collected != 0 {
		t.Errorf("collected %d runs with no agent-tier version, want 0", out.Collected)
	}
	if got := b.results(t); got != 0 {
		t.Errorf("%d measurements stored, want 0", got)
	}
}

// TestSweepDistinguishesUnmeasuredCostFromZeroCost is the measured-vs-zero pair
// at the sweep layer. A run whose usage rows never arrived stores NULL, which
// the baseline drops; a run that genuinely reported zero stores 0, which the
// baseline keeps. Collapsing the first into the second would let a usage
// reporting outage read as a cost improvement.
func TestSweepDistinguishesUnmeasuredCostFromZeroCost(t *testing.T) {
	b := newBank(t)
	unmeasured := b.finishedRun(t, `{"agent": 3}`)
	zeroCost := b.finishedRun(t, `{"agent": 3}`)
	b.f.Insert(t, "task_usage", testutil.Cols{
		"task_id":       zeroCost,
		"provider":      "quiz-sweep-test",
		"model":         "quiz-sweep-test",
		"input_tokens":  0,
		"output_tokens": 0,
	})

	b.run(t)

	var unmeasuredIsNull, zeroIsNull bool
	var zeroTokens int64
	b.f.QueryRow(t, `SELECT run_tokens IS NULL FROM prompt_quiz_result WHERE task_id = $1`, unmeasured).Scan(&unmeasuredIsNull)
	b.f.QueryRow(t, `SELECT run_tokens IS NULL, COALESCE(run_tokens, -1) FROM prompt_quiz_result WHERE task_id = $1`, zeroCost).
		Scan(&zeroIsNull, &zeroTokens)
	if !unmeasuredIsNull {
		t.Error("a run with no usage rows stored a cost, want NULL")
	}
	if zeroIsNull {
		t.Error("a run that reported zero cost stored NULL, want 0")
	}
	if zeroTokens != 0 {
		t.Errorf("zero-cost run stored %d tokens, want 0", zeroTokens)
	}
}

// finishedRun inserts a terminal quiz run the collector should pick up, with
// the given agent_task_queue.prompt_versions attribution.
func (b *bank) finishedRun(t *testing.T, promptVersions string) string {
	t.Helper()
	batch := "11111111-2222-3333-4444-555555555555"
	payload := fmt.Sprintf(`{"kind": %q, "quiz_batch_id": %q, "quiz_item_id": %q, "quiz_prompt": "x"}`,
		promptquiz.TaskKind, batch, b.itemID)
	return b.f.Task(t, b.agentID, testutil.Cols{
		"runtime_id":              b.runtimeID,
		"status":                  "completed",
		"originator_source":       promptquiz.OriginatorSource,
		"trigger_evidence_kind":   promptquiz.TriggerEvidenceKind,
		"trigger_evidence_ref_id": b.itemID,
		"context":                 testutil.Raw(fmt.Sprintf("'%s'::jsonb", payload)),
		"prompt_versions":         testutil.Raw(fmt.Sprintf("'%s'::jsonb", promptVersions)),
		"started_at":              testutil.Raw("now() - interval '2 minutes'"),
		"completed_at":            testutil.Raw("now()"),
	})
}
