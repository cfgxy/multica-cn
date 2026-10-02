package promptquizsweep

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

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
	if outcome != promptquiz.OutcomeAnswered {
		t.Errorf("measurement outcome %q, want %q", outcome, promptquiz.OutcomeAnswered)
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

// TestSweepRecordsTheRuntimeAndModelThatRan is the A1 attribution at the sweep
// layer: a measurement is only comparable against another one taken by the same
// instrument, so the instrument has to be stored with the reading. The runtime
// comes from the QUEUE row rather than from the agent — re-pointing an agent at
// another runtime must not re-attribute measurements already taken.
func TestSweepRecordsTheRuntimeAndModelThatRan(t *testing.T) {
	b := newBank(t)
	taskID := b.finishedRun(t, `{"agent": 3}`)
	// Two usage rows on one run, which is what a run that switched models looks
	// like. Both must survive into the label: a reading taken half on one model
	// is not a reading on either.
	for _, model := range []string{"sonnet-test", "opus-test"} {
		b.f.Insert(t, "task_usage", testutil.Cols{
			"task_id":       taskID,
			"provider":      "quiz-sweep-test",
			"model":         model,
			"input_tokens":  10,
			"output_tokens": 5,
		})
	}

	b.run(t)

	var runtimeID, runModel string
	b.f.QueryRow(t, `SELECT runtime_id::text, run_model FROM prompt_quiz_result WHERE task_id = $1`, taskID).
		Scan(&runtimeID, &runModel)
	if runtimeID != b.runtimeID {
		t.Errorf("measurement runtime_id = %q, want the queue row's %q", runtimeID, b.runtimeID)
	}
	if runModel != "opus-test,sonnet-test" {
		t.Errorf("measurement run_model = %q, want %q — every model the run used, ordered so the label is stable",
			runModel, "opus-test,sonnet-test")
	}
}

// TestSweepSkipsRunsWithNoRuntime covers migration 251: a terminal queue row may
// have no runtime at all. Storing such a run would put a reading on the curve
// without saying what measured it, and every later reading would then be compared
// against an unknown instrument.
//
// The control run in the same tick is the reverse verification: it proves the
// skip is selective rather than the collector having failed outright.
func TestSweepSkipsRunsWithNoRuntime(t *testing.T) {
	b := newBank(t)
	orphan := b.finishedRun(t, `{"agent": 3}`)
	control := b.finishedRun(t, `{"agent": 3}`)
	// Allowed by agent_task_queue_active_requires_runtime only because the row
	// is already finished, which is exactly the state the collector reads.
	b.f.Exec(t, `UPDATE agent_task_queue SET runtime_id = NULL WHERE id = $1`, orphan)

	b.run(t)

	if n := b.f.Count(t, `SELECT count(*) FROM prompt_quiz_result WHERE task_id = $1`, orphan); n != 0 {
		t.Errorf("a run with no runtime produced %d measurements, want 0 — an unattributed reading cannot be compared with anything", n)
	}
	if n := b.f.Count(t, `SELECT count(*) FROM prompt_quiz_result WHERE task_id = $1`, control); n != 1 {
		t.Errorf("the control run produced %d measurements, want 1; the skip above is not evidence if the collector stored nothing at all", n)
	}
}

// TestQuizPayloadCarriesNoRubric is the A2 assertion: the public half of an item
// goes to the measured run, the private half (expected answer, grading points)
// never leaves the grading side.
//
// The payload's field set is asserted exactly rather than "does not contain the
// rubric string", so a future field added next to the question has to be declared
// here before it can ship.
func TestQuizPayloadCarriesNoRubric(t *testing.T) {
	b := newBank(t)
	const secret = "EXPECTED ANSWER: the three constraints, verbatim"
	b.f.Exec(t, `UPDATE prompt_quiz_item SET rubric = $1 WHERE id = $2`, secret, b.itemID)

	// EnqueueLimit is a per-tick budget shared with every other scope in the
	// deployment, so tick until this bank gets at least one run.
	for tick := 0; tick < 5 && b.quizTasks(t) == 0; tick++ {
		b.run(t)
	}
	if b.quizTasks(t) == 0 {
		t.Fatal("the sweep ordered no runs, so there is no payload to inspect")
	}

	var payload string
	b.f.QueryRow(t, `SELECT context::text FROM agent_task_queue
		WHERE agent_id = $1 AND originator_source = $2 ORDER BY created_at DESC LIMIT 1`,
		b.agentID, promptquiz.OriginatorSource).Scan(&payload)

	var fields map[string]any
	if err := json.Unmarshal([]byte(payload), &fields); err != nil {
		t.Fatalf("payload is not an object: %v", err)
	}
	want := []string{"kind", "quiz_batch_id", "quiz_item_id", "quiz_prompt"}
	if len(fields) != len(want) {
		t.Errorf("payload has fields %v, want exactly %v", keysOf(fields), want)
	}
	for _, k := range want {
		if _, ok := fields[k]; !ok {
			t.Errorf("payload is missing %q", k)
		}
	}
	if strings.Contains(payload, secret) {
		t.Error("the payload sent to the measured run contains the item's rubric")
	}

	// Reverse verification 1: the leak detector is not vacuous. A payload built
	// the wrong way is caught by the same two checks.
	leaky, err := json.Marshal(map[string]any{
		"kind": promptquiz.TaskKind, "quiz_batch_id": "b", "quiz_item_id": b.itemID,
		"quiz_prompt": "x", "quiz_rubric": secret,
	})
	if err != nil {
		t.Fatalf("encode leaky payload: %v", err)
	}
	var leakyFields map[string]any
	if err := json.Unmarshal(leaky, &leakyFields); err != nil {
		t.Fatalf("decode leaky payload: %v", err)
	}
	if len(leakyFields) == len(want) || !strings.Contains(string(leaky), secret) {
		t.Error("a payload carrying the rubric passed the checks above, so those checks prove nothing")
	}

	// Reverse verification 2: the guarantee is structural, not a matter of care in
	// taskContext. The row type that function receives has no rubric field, so
	// adding rubric to ListActivePromptQuizItemsForProfile — the isolation
	// condition, expressed as a column list in the query — fails here first.
	rowType := reflect.TypeOf(db.ListActivePromptQuizItemsForProfileRow{})
	for i := 0; i < rowType.NumField(); i++ {
		if strings.EqualFold(rowType.Field(i).Name, "Rubric") {
			t.Error("ListActivePromptQuizItemsForProfileRow now carries Rubric: the sweep can reach the answer key, and only this test's payload check stands between it and the run")
		}
	}
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
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

// TestSweepGradesCompletedRunsAgainstTheItemChecks pins the grading seam
// (RUYI-286): a completed run whose answer satisfies the item's published
// assertions stores its weighted pass ratio, per-assertion detail and grading
// time; a run whose answer misses stores a real 0 — while a run with no
// answer text at all stays NULL, because it never answered and "no answer" is
// not "all assertions failed".
func TestSweepGradesCompletedRunsAgainstTheItemChecks(t *testing.T) {
	b := newBank(t)
	checks := `[{"id":"say-front","kind":"includes_all","weight":3,"phrases":["前台阻塞"]},
	            {"id":"say-no-background","kind":"excludes","weight":1,"phrases":["可以先结束回合"]}]`
	b.f.Exec(t, `UPDATE prompt_quiz_item SET rubric_checks = $1::jsonb WHERE id = $2`, checks, b.itemID)

	writeAnswer := func(taskID, content string) {
		t.Helper()
		b.f.Insert(t, "task_message", testutil.Cols{
			"task_id": taskID, "seq": 1, "type": "text", "content": content,
		})
	}

	gradedFull := b.finishedRun(t, `{"agent": 3}`)
	writeAnswer(gradedFull, "正确做法：必须在本回合内前台阻塞收齐构建结果后才能结束回合，不得提前退出等待。")
	gradedZero := b.finishedRun(t, `{"agent": 3}`)
	writeAnswer(gradedZero, "可以先结束回合，等构建完成后再回来收结果。")
	unanswered := b.finishedRun(t, `{"agent": 3}`)

	if out := b.run(t); out.Collected != 3 {
		t.Fatalf("collected %d runs, want 3", out.Collected)
	}

	var fullScore, zeroScore float64
	var fullDetail []byte
	var fullGradedAt, zeroGradedAt *time.Time
	var nullScore *float64
	b.f.QueryRow(t, `SELECT score, score_detail, graded_at FROM prompt_quiz_result WHERE task_id = $1`, gradedFull).
		Scan(&fullScore, &fullDetail, &fullGradedAt)
	b.f.QueryRow(t, `SELECT score, graded_at FROM prompt_quiz_result WHERE task_id = $1`, gradedZero).
		Scan(&zeroScore, &zeroGradedAt)
	b.f.QueryRow(t, `SELECT score FROM prompt_quiz_result WHERE task_id = $1`, unanswered).Scan(&nullScore)

	// (say-front passes 3/3; say-no-background passes on the excludes) → 1.0.
	if fullScore != 1.0 {
		t.Errorf("graded-full score = %v, want 1.0", fullScore)
	}
	if len(fullDetail) == 0 || !strings.Contains(string(fullDetail), "say-front") {
		t.Errorf("graded-full detail missing per-assertion verdicts: %s", fullDetail)
	}
	if fullGradedAt == nil {
		t.Error("graded-full has no graded_at")
	}
	// The exclusion fires → the real zero, not a NULL: an answer that got every
	// assertion wrong measured something.
	if zeroScore != 0.0 {
		t.Errorf("graded-zero score = %v, want 0.0", zeroScore)
	}
	if zeroGradedAt == nil {
		t.Error("graded-zero has no graded_at")
	}
	if nullScore != nil {
		t.Errorf("unanswered run stored score %v, want NULL", *nullScore)
	}
}

// TestSweepBackfillsARowLeftByAScoreIncapableWriter pins the RUYI-325 fix:
// on a shared database running mixed API versions, an older build without
// the grading code can win the sweep lease and store its measurement with
// score / score_detail / graded_at all NULL. That row must not retire the
// task from collection forever — the next tick re-collects it and the grade
// is filled in, so the shared database heals within one sweep cadence.
func TestSweepBackfillsARowLeftByAScoreIncapableWriter(t *testing.T) {
	b := newBank(t)
	checks := `[{"id":"say-front","kind":"includes_all","weight":1,"phrases":["前台阻塞"]}]`
	b.f.Exec(t, `UPDATE prompt_quiz_item SET rubric_checks = $1::jsonb WHERE id = $2`, checks, b.itemID)

	taskID := b.finishedRun(t, `{"agent": 3}`)
	b.f.Insert(t, "task_message", testutil.Cols{
		"task_id": taskID, "seq": 1, "type": "text",
		"content": "正确做法：必须在本回合内前台阻塞收齐构建结果后才能结束回合。",
	})

	// The placeholder, written the way a pre-grading build writes it: the
	// upsert's full legacy column set, the grading columns left at their NULL
	// defaults. A NOT EXISTS keyed on row existence alone retires this task
	// from collection the moment this row lands.
	b.f.Insert(t, "prompt_quiz_result", testutil.Cols{
		"workspace_id":     testWorkspaceID,
		"scope":            "agent",
		"scope_id":         b.agentID,
		"version":          b.version,
		"item_id":          b.itemID,
		"item_revision":    1,
		"item_body_sha256": promptquiz.BodyDigest("Summarise your operating constraints in one sentence."),
		"runtime_id":       b.runtimeID,
		"batch_id":         "11111111-2222-3333-4444-555555555555",
		"task_id":          taskID,
		"outcome":          promptquiz.OutcomeAnswered,
		"run_tokens":       1234,
		"duration_ms":      4321,
	})

	if out := b.run(t); out.Collected != 1 {
		t.Fatalf("tick collected %d, want 1 — the legacy NULL placeholder row must be re-collected and graded", out.Collected)
	}
	var score float64
	var detail []byte
	var gradedAt *time.Time
	b.f.QueryRow(t, `SELECT score, score_detail, graded_at FROM prompt_quiz_result WHERE task_id = $1`, taskID).
		Scan(&score, &detail, &gradedAt)
	if score != 1.0 {
		t.Errorf("backfilled score = %v, want 1.0", score)
	}
	if len(detail) == 0 {
		t.Error("backfilled row carries no score_detail")
	}
	if gradedAt == nil {
		t.Error("backfilled row carries no graded_at")
	}

	// A run the collector judges not gradeable gets its verdict recorded on
	// the row too, so the judged population stops being re-collected: without
	// that marker the not-gradeable NULLs would accumulate across releases
	// and crowd the bounded collection window (CollectLimit).
	unanswered := b.finishedRun(t, `{"agent": 3}`)
	if out := b.run(t); out.Collected != 1 {
		t.Fatalf("second tick collected %d, want 1 — only the fresh unanswered run", out.Collected)
	}
	var unansweredAt *time.Time
	b.f.QueryRow(t, `SELECT graded_at FROM prompt_quiz_result WHERE task_id = $1`, unanswered).
		Scan(&unansweredAt)
	if unansweredAt == nil {
		t.Error("not-gradeable row was stored without a graded_at verdict marker")
	}
	if out := b.run(t); out.Collected != 0 {
		t.Errorf("third tick collected %d, want 0 — judged rows stay retired", out.Collected)
	}
}
