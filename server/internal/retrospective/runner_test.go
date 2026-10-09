package retrospective

// Tests for the daily retrospective runner (RUYI-552 direction 3): the run
// triggers exactly one no-issue platform task for the configured agent,
// records the scanned window on the run row, and the completion processor
// turns the agent's final JSON report into legislation-pool drafts — with
// the boundary the product cares about most enforced twice: no issue is
// ever created and no comment is ever written; every outcome lands in
// prompt_proposal, retrospective_issue_watermark and retrospective_run only.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	ctx := context.Background()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://multica:multica@localhost:5432/multica?sslmode=disable"
	}
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		fmt.Printf("Skipping tests: could not connect to database: %v\n", err)
		os.Exit(0)
	}
	if err := pool.Ping(ctx); err != nil {
		fmt.Printf("Skipping tests: database not reachable: %v\n", err)
		pool.Close()
		os.Exit(0)
	}
	testPool = pool
	code := m.Run()
	pool.Close()
	os.Exit(code)
}

var fixtureCounter = time.Now().UnixNano()

// retroAgentFixture inserts one online runtime plus one agent bound to it,
// both in the given workspace, and returns the agent id.
func retroAgentFixture(t *testing.T, wsID, suffix string) (agentID, runtimeID string) {
	t.Helper()
	var ownerID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id`,
		"Retro Agent Owner "+suffix, "retrospective-agent-owner-"+suffix+"@multica.test").Scan(&ownerID); err != nil {
		t.Fatalf("create agent owner: %v", err)
	}
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent_runtime (workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, metadata, last_seen_at, visibility, owner_id)
		VALUES ($1, 'retro-test-daemon', $2, 'cloud', 'retrospective_test', 'online', '', '{}'::jsonb, now(), 'private', $3)
		RETURNING id`, wsID, "retro runtime "+suffix, ownerID).Scan(&runtimeID); err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent (workspace_id, name, description, runtime_mode, runtime_config, visibility, permission_mode, max_concurrent_tasks, owner_id, runtime_id)
		VALUES ($1, $2, '', 'cloud', '{}'::jsonb, 'private', 'private', 1, $3, $4)
		RETURNING id`, wsID, "retro agent "+suffix, ownerID, runtimeID).Scan(&agentID); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	return agentID, runtimeID
}

// retroFixture builds one workspace whose retrospective is enabled and bound
// to a runnable agent, plus two done issues inside the window, and returns
// (workspace id, agent id). Cleanup removes everything the fixtures created.
func retroFixture(t *testing.T) (wsID, agentID string) {
	t.Helper()
	fixtureCounter++
	suffix := fmt.Sprintf("retro%d", fixtureCounter)

	var wsIDStr string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO workspace (name, slug, description, issue_prefix, context)
		VALUES ($1, $2, '', '', '') RETURNING id`,
		"retrospective ws "+suffix, "retrospective-"+suffix).Scan(&wsIDStr); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	wsID = wsIDStr
	agentID, _ = retroAgentFixture(t, wsID, suffix)
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO retrospective_config (workspace_id, enabled, include_in_review, window_days, agent_id)
		VALUES ($1, true, false, 7, $2)`, wsID, agentID); err != nil {
		t.Fatalf("create config: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		for _, stmt := range []string{
			`DELETE FROM agent_task_queue WHERE workspace_id = $1`,
			`DELETE FROM retrospective_issue_watermark WHERE workspace_id = $1`,
			`DELETE FROM retrospective_run WHERE workspace_id = $1`,
			`DELETE FROM retrospective_config WHERE workspace_id = $1`,
			`DELETE FROM prompt_proposal WHERE workspace_id = $1`,
			`DELETE FROM prompt_structure_baseline WHERE workspace_id = $1`,
			`DELETE FROM comment WHERE workspace_id = $1`,
			`DELETE FROM issue WHERE workspace_id = $1`,
			`DELETE FROM agent WHERE workspace_id = $1`,
			`DELETE FROM agent_runtime WHERE workspace_id = $1`,
			`DELETE FROM member WHERE workspace_id = $1`,
			`DELETE FROM "user" WHERE email LIKE 'retrospective-%' AND email LIKE '%` + suffix + `%'`,
			`DELETE FROM workspace WHERE id = $1`,
		} {
			testPool.Exec(ctx, stmt, wsID)
		}
	})
	return wsID, agentID
}

// retroIssue inserts one done issue (inside the window) with one discussion
// comment and returns the issue id.
func retroIssue(t *testing.T, wsID, title, comment string) string {
	t.Helper()
	fixtureCounter++
	suffix := fmt.Sprintf("retro%d", fixtureCounter)
	var userID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id`,
		"Retro Fixture "+suffix, "retrospective-user-"+suffix+"@multica.test").Scan(&userID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'member')`, wsID, userID); err != nil {
		t.Fatalf("create member: %v", err)
	}
	var issueID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO issue (workspace_id, number, title, description, status, priority, creator_type, creator_id, position, updated_at)
		VALUES ($1, (SELECT COALESCE(MAX(number), 0) + 1 FROM issue WHERE workspace_id = $1), $2, '', 'done', 'none', 'member', $3, 0, now())
		RETURNING id`, wsID, title, userID).Scan(&issueID); err != nil {
		t.Fatalf("create issue: %v", err)
	}
	if comment != "" {
		if _, err := testPool.Exec(context.Background(), `
			INSERT INTO comment (workspace_id, issue_id, author_type, author_id, content, type)
			VALUES ($1, $2, 'member', $3, $4, 'comment')`, wsID, issueID, userID, comment); err != nil {
			t.Fatalf("create comment: %v", err)
		}
	}
	return issueID
}

type recordedEnqueue struct {
	params EnqueueParams
}

// newTestRunner wires a Runner over the test pool whose enqueuer records
// every call and answers with a valid task id (the run row stores it; the
// task row itself is not needed by the code under test).
func newTestRunner(t *testing.T) (*Runner, *[]recordedEnqueue) {
	t.Helper()
	calls := &[]recordedEnqueue{}
	runner := &Runner{
		DB:      testPool,
		Queries: db.New(testPool),
		Enqueue: func(ctx context.Context, params EnqueueParams) (string, error) {
			*calls = append(*calls, recordedEnqueue{params: params})
			return "f47ac10b-58cc-4372-a567-0e02b2c3d479", nil
		},
	}
	return runner, calls
}

func runRecord(t *testing.T, wsID string) (status, errMsg, detail string) {
	t.Helper()
	if err := testPool.QueryRow(context.Background(), `
		SELECT status, error, detail::text FROM retrospective_run WHERE workspace_id = $1 ORDER BY created_at DESC LIMIT 1`,
		wsID).Scan(&status, &errMsg, &detail); err != nil {
		t.Fatalf("read run record: %v", err)
	}
	return status, errMsg, detail
}

func counts(t *testing.T, wsID string) (issues, comments, proposals int) {
	t.Helper()
	if err := testPool.QueryRow(context.Background(), `
		SELECT
			(SELECT count(*) FROM issue WHERE workspace_id = $1),
			(SELECT count(*) FROM comment WHERE workspace_id = $1),
			(SELECT count(*) FROM prompt_proposal WHERE workspace_id = $1)`, wsID).
		Scan(&issues, &comments, &proposals); err != nil {
		t.Fatalf("read counts: %v", err)
	}
	return issues, comments, proposals
}

const stubTaskID = "f47ac10b-58cc-4372-a567-0e02b2c3d479"

// reportJSON builds one agent final-message report listing the given issue
// as analyzed with a single add_clause draft.
func reportJSON(issueID, clauseName string) string {
	return fmt.Sprintf(`{"analyzed_issue_ids":[%q],"drafts":[{"issue_id":%q,"carrier_scope":"workspace","target_section":"沟通规范","change_kind":"add_clause","clause_name":%q,"clause_text":"- **新条款**：测试条款内容","gate_answers":{"layer":"workspace=跨项目协作机制","retention":"每轮输出都适用","cost":"少量常驻","conflict":"无同主题条款","dedup":"无重复"},"evidence_ref":"issue 标题","evidence_note":"依据说明"}]}`,
		issueID, issueID, clauseName)
}

// TestRunWorkspaceEnqueuesExactlyOneNoIssueTask: the trigger scans the
// window, records the membership on the run row, and hands the enqueuer
// exactly one params set naming the configured agent — with the context
// parsing back through ParseTaskContext, and zero issue/comment writes.
func TestRunWorkspaceEnqueuesExactlyOneNoIssueTask(t *testing.T) {
	wsID, agentID := retroFixture(t)
	issueA := retroIssue(t, wsID, "修复了发布脚本", "根因是路径拼接")
	_ = retroIssue(t, wsID, "优化了查询", "")
	runner, calls := newTestRunner(t)

	issuesBefore, commentsBefore, _ := counts(t, wsID)

	stats, err := runner.RunWorkspace(context.Background(), wsID, "manual")
	if err != nil {
		t.Fatalf("RunWorkspace: %v", err)
	}
	if stats.IssuesScanned != 2 {
		t.Fatalf("IssuesScanned = %d, want 2", stats.IssuesScanned)
	}
	if n := len(*calls); n != 1 {
		t.Fatalf("expected exactly one enqueue, got %d", n)
	}
	params := (*calls)[0].params
	if params.AgentID != agentID {
		t.Fatalf("enqueue agent mismatch: %+v", params)
	}
	if params.RunID != stats.RunID || params.WorkspaceID != wsID {
		t.Fatalf("enqueue run/workspace mismatch: %+v", params)
	}
	parsed, ok := ParseTaskContext(params.Context)
	if !ok {
		t.Fatalf("task context does not parse back")
	}
	if parsed.WorkspaceID != wsID || len(parsed.Issues) != 2 {
		t.Fatalf("task context payload wrong: %+v", parsed)
	}
	prompt, ok := PromptFromContext(params.Context)
	if !ok || !strings.Contains(prompt, "Do NOT create, update, or comment on any issue") {
		t.Fatalf("prompt must parse and carry the no-issue boundary")
	}

	issuesAfter, commentsAfter, _ := counts(t, wsID)
	if issuesAfter != issuesBefore || commentsAfter != commentsBefore {
		t.Fatalf("trigger touched the issue surface: issues %d→%d comments %d→%d",
			issuesBefore, issuesAfter, commentsBefore, commentsAfter)
	}

	status, errMsg, detail := runRecord(t, wsID)
	if status != "running" || errMsg != "" {
		t.Fatalf("run should be running with no error, got %q / %q", status, errMsg)
	}
	for _, id := range []string{issueA} {
		if !strings.Contains(detail, id) {
			t.Fatalf("run detail must record membership, missing %s in %s", id, detail)
		}
	}
}

// TestRunWorkspaceEmptyWindowFinishesImmediately: nothing completed in the
// window → succeed without spending an agent run.
func TestRunWorkspaceEmptyWindowFinishesImmediately(t *testing.T) {
	wsID, _ := retroFixture(t)
	runner, calls := newTestRunner(t)

	stats, err := runner.RunWorkspace(context.Background(), wsID, "schedule")
	if err != nil {
		t.Fatalf("RunWorkspace: %v", err)
	}
	if stats.IssuesScanned != 0 {
		t.Fatalf("IssuesScanned = %d, want 0", stats.IssuesScanned)
	}
	if n := len(*calls); n != 0 {
		t.Fatalf("empty window must not enqueue, got %d calls", n)
	}
	status, _, _ := runRecord(t, wsID)
	if status != "succeeded" {
		t.Fatalf("empty-window run should succeed immediately, got %q", status)
	}
}

// TestRunWorkspaceNoAgent: a legacy row enabled without an agent (only
// possible before migration 935) records a failed run and reports
// ErrNoAgent instead of dispatching anything.
func TestRunWorkspaceNoAgent(t *testing.T) {
	wsID, _ := retroFixture(t)
	_ = retroIssue(t, wsID, "有活干但没人干", "")
	if _, err := testPool.Exec(context.Background(),
		`UPDATE retrospective_config SET agent_id = NULL WHERE workspace_id = $1`, wsID); err != nil {
		t.Fatalf("clear agent: %v", err)
	}
	runner, calls := newTestRunner(t)

	_, err := runner.RunWorkspace(context.Background(), wsID, "schedule")
	if err == nil || err.Error() != ErrNoAgent.Error() {
		t.Fatalf("expected ErrNoAgent, got %v", err)
	}
	if n := len(*calls); n != 0 {
		t.Fatalf("no agent must not enqueue, got %d calls", n)
	}
	status, errMsg, _ := runRecord(t, wsID)
	if status != "failed" || errMsg == "" {
		t.Fatalf("run should fail with a reason, got %q / %q", status, errMsg)
	}
}

// TestProcessTaskTerminalLandsDrafts: the agent's JSON report becomes a
// prompt_proposal draft, the issue gets its watermark, the run finishes
// succeeded — and the issue surface is untouched.
func TestProcessTaskTerminalLandsDrafts(t *testing.T) {
	wsID, _ := retroFixture(t)
	issueA := retroIssue(t, wsID, "发布脚本修复", "根因是路径拼接")
	runner, calls := newTestRunner(t)
	if _, err := runner.RunWorkspace(context.Background(), wsID, "manual"); err != nil {
		t.Fatalf("RunWorkspace: %v", err)
	}
	if n := len(*calls); n != 1 {
		t.Fatalf("expected one enqueue, got %d", n)
	}

	issuesBefore, commentsBefore, proposalsBefore := counts(t, wsID)

	task := db.AgentTaskQueue{
		ID:      util.MustParseUUID(stubTaskID),
		Context: (*calls)[0].params.Context,
	}
	out, err := runner.ProcessTaskTerminal(context.Background(), task, reportJSON(issueA, "发布脚本复核条款"), "")
	if err != nil {
		t.Fatalf("ProcessTaskTerminal: %v", err)
	}
	if out.ProposalsCreated != 1 || out.IssuesAnalyzed != 1 {
		t.Fatalf("stats wrong: %+v", out)
	}
	issuesAfter, commentsAfter, proposalsAfter := counts(t, wsID)
	if issuesAfter != issuesBefore || commentsAfter != commentsBefore {
		t.Fatalf("completion touched the issue surface: issues %d→%d comments %d→%d",
			issuesBefore, issuesAfter, commentsBefore, commentsAfter)
	}
	if proposalsAfter != proposalsBefore+1 {
		t.Fatalf("expected one new proposal, got %d→%d", proposalsBefore, proposalsAfter)
	}
	var watermark int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM retrospective_issue_watermark WHERE workspace_id = $1 AND issue_id = $2`,
		wsID, issueA).Scan(&watermark); err != nil || watermark != 1 {
		t.Fatalf("watermark missing (%v, %d)", err, watermark)
	}
	status, errMsg, detail := runRecord(t, wsID)
	if status != "succeeded" || errMsg != "" {
		t.Fatalf("run should be succeeded, got %q / %q", status, errMsg)
	}
	if !strings.Contains(detail, "analyzed_issue_ids") {
		t.Fatalf("run detail must record analyzed ids, got %s", detail)
	}
}

// TestProcessTaskTerminalRejectsOutOfScopeIssues: a report analyzing an
// issue outside the recorded window is rejected whole — nothing lands.
func TestProcessTaskTerminalRejectsOutOfScopeIssues(t *testing.T) {
	wsID, _ := retroFixture(t)
	inScope := retroIssue(t, wsID, "窗口内修复", "")
	runner, calls := newTestRunner(t)
	if _, err := runner.RunWorkspace(context.Background(), wsID, "manual"); err != nil {
		t.Fatalf("RunWorkspace: %v", err)
	}

	_, _, proposalsBefore := counts(t, wsID)

	task := db.AgentTaskQueue{
		ID:      util.MustParseUUID(stubTaskID),
		Context: (*calls)[0].params.Context,
	}
	_, err := runner.ProcessTaskTerminal(context.Background(), task, reportJSON(inScope+issueSuffixStub(), "越权条款"), "")
	// The rejection is the run's verdict, recorded on the run row — not a
	// processing error, same contract as the taskErr path.
	if err != nil {
		t.Fatalf("rejection should record a failed run, not error: %v", err)
	}
	status, errMsg, _ := runRecord(t, wsID)
	if status != "failed" || !strings.Contains(errMsg, "回看窗口之外") {
		t.Fatalf("run should fail with the out-of-scope reason, got %q / %q", status, errMsg)
	}
	_, _, proposalsAfter := counts(t, wsID)
	if proposalsAfter != proposalsBefore {
		t.Fatalf("rejected report must not land drafts: %d→%d", proposalsBefore, proposalsAfter)
	}
}

func issueSuffixStub() string {
	fixtureCounter++
	return fmt.Sprintf("00000000-0000-4000-8000-%012d", fixtureCounter)
}

// TestProcessTaskTerminalDedupAgainstLiveClauses: a draft whose clause
// already exists in the carrier is counted as a duplicate and still
// watermarks the issue.
func TestProcessTaskTerminalDedupAgainstLiveClauses(t *testing.T) {
	wsID, _ := retroFixture(t)
	// The carrier must contain the clause the draft duplicates.
	if _, err := testPool.Exec(context.Background(),
		`UPDATE workspace SET context = $2 WHERE id = $1`, wsID,
		"# 平台协作规范\n\n## 沟通规范\n\n- **行文基调**：专业、正式、准确、简明扼要。\n"); err != nil {
		t.Fatalf("seed carrier: %v", err)
	}
	issueA := retroIssue(t, wsID, "行文基调修复", "")
	runner, calls := newTestRunner(t)
	if _, err := runner.RunWorkspace(context.Background(), wsID, "manual"); err != nil {
		t.Fatalf("RunWorkspace: %v", err)
	}

	task := db.AgentTaskQueue{
		ID:      util.MustParseUUID(stubTaskID),
		Context: (*calls)[0].params.Context,
	}
	dup := strings.Replace(reportJSON(issueA, "行文基调"), "新条款", "行文基调", 1)
	out, err := runner.ProcessTaskTerminal(context.Background(), task, dup, "")
	if err != nil {
		t.Fatalf("ProcessTaskTerminal: %v", err)
	}
	if out.DuplicatesSkipped != 1 || out.ProposalsCreated != 0 {
		t.Fatalf("expected the live clause to be skipped: %+v", out)
	}
	status, _, _ := runRecord(t, wsID)
	if status != "succeeded" {
		t.Fatalf("dedup-only report still succeeds, got %q", status)
	}
	var watermark int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM retrospective_issue_watermark WHERE workspace_id = $1 AND issue_id = $2`,
		wsID, issueA).Scan(&watermark); err != nil || watermark != 1 {
		t.Fatalf("watermark missing after dedup (%v, %d)", err, watermark)
	}
}

// TestProcessTaskTerminalFirstVerdictWins: a second terminal report after
// the run already finished is a no-op.
func TestProcessTaskTerminalFirstVerdictWins(t *testing.T) {
	wsID, _ := retroFixture(t)
	issueA := retroIssue(t, wsID, "首个裁决胜出", "")
	runner, calls := newTestRunner(t)
	if _, err := runner.RunWorkspace(context.Background(), wsID, "manual"); err != nil {
		t.Fatalf("RunWorkspace: %v", err)
	}
	task := db.AgentTaskQueue{
		ID:      util.MustParseUUID(stubTaskID),
		Context: (*calls)[0].params.Context,
	}
	if _, err := runner.ProcessTaskTerminal(context.Background(), task, reportJSON(issueA, "先到条款"), ""); err != nil {
		t.Fatalf("first terminal: %v", err)
	}
	_, _, proposalsBefore := counts(t, wsID)
	if _, err := runner.ProcessTaskTerminal(context.Background(), task, reportJSON(issueA, "后到条款"), "late failure"); err != nil {
		t.Fatalf("second terminal should be a silent no-op, got %v", err)
	}
	_, _, proposalsAfter := counts(t, wsID)
	if proposalsAfter != proposalsBefore {
		t.Fatalf("late verdict must not land anything: %d→%d", proposalsBefore, proposalsAfter)
	}
}

// TestProcessTaskTerminalFailurePath: a daemon-reported failure finishes the
// run failed without landing anything.
func TestProcessTaskTerminalFailurePath(t *testing.T) {
	wsID, _ := retroFixture(t)
	_ = retroIssue(t, wsID, "失败路径", "")
	runner, calls := newTestRunner(t)
	if _, err := runner.RunWorkspace(context.Background(), wsID, "manual"); err != nil {
		t.Fatalf("RunWorkspace: %v", err)
	}
	task := db.AgentTaskQueue{
		ID:      util.MustParseUUID(stubTaskID),
		Context: (*calls)[0].params.Context,
	}
	_, _, proposalsBefore := counts(t, wsID)
	if _, err := runner.ProcessTaskTerminal(context.Background(), task, "", "runtime crashed mid-run"); err != nil {
		t.Fatalf("ProcessTaskTerminal: %v", err)
	}
	status, errMsg, _ := runRecord(t, wsID)
	if status != "failed" || errMsg == "" {
		t.Fatalf("run should record the failure, got %q / %q", status, errMsg)
	}
	_, _, proposalsAfter := counts(t, wsID)
	if proposalsAfter != proposalsBefore {
		t.Fatalf("failure must not land drafts: %d→%d", proposalsBefore, proposalsAfter)
	}
}

// TestProcessTaskTerminalMergeIntoExistingProposal: a draft matching a
// pending proposal on carrier + section + name merges evidence instead of
// creating a second row.
func TestProcessTaskTerminalMergeIntoExistingProposal(t *testing.T) {
	wsID, _ := retroFixture(t)
	issueA := retroIssue(t, wsID, "合并验证", "")
	runner, calls := newTestRunner(t)
	if _, err := runner.RunWorkspace(context.Background(), wsID, "manual"); err != nil {
		t.Fatalf("RunWorkspace: %v", err)
	}

	// Seed a pending proposal that the incoming draft will match.
	var proposalID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO prompt_proposal (workspace_id, carrier_scope, carrier_scope_id, target_section, change_kind, clause_name, clause_text,
			gate_answer_layer, gate_answer_retention, gate_answer_cost, gate_answer_conflict, gate_answer_dedup,
			evidence_anchors, status, source, created_by_type, created_by_id)
		VALUES ($1, 'workspace', $1, '沟通规范', 'add_clause', '合并条款', '- **合并条款**：已有草案',
			'已有草案层次', '每轮适用', '少量常驻', '无冲突', '无重复',
			'[{"issue_id": null, "ref": "seed", "note": "已有证据"}]'::jsonb, 'draft', 'retrospective', 'system', $1)
		RETURNING id`, wsID).Scan(&proposalID); err != nil {
		t.Fatalf("seed proposal: %v", err)
	}

	task := db.AgentTaskQueue{
		ID:      util.MustParseUUID(stubTaskID),
		Context: (*calls)[0].params.Context,
	}
	out, err := runner.ProcessTaskTerminal(context.Background(), task, reportJSON(issueA, "合并条款"), "")
	if err != nil {
		t.Fatalf("ProcessTaskTerminal: %v", err)
	}
	if out.ProposalsMerged != 1 || out.ProposalsCreated != 0 {
		t.Fatalf("expected merge not create: %+v", out)
	}
	var anchors string
	if err := testPool.QueryRow(context.Background(),
		`SELECT evidence_anchors::text FROM prompt_proposal WHERE id = $1`, proposalID).Scan(&anchors); err != nil {
		t.Fatalf("read anchors: %v", err)
	}
	if !strings.Contains(anchors, issueA) {
		t.Fatalf("merge must append the new evidence anchor, got %s", anchors)
	}
}

// TestReconcileStaleRuns: a running run whose task failed is failed by the
// bulk backstop; a running run that never got enqueued ages out too.
func TestReconcileStaleRuns(t *testing.T) {
	wsID, agentID := retroFixture(t)
	_ = retroIssue(t, wsID, "对账窗口", "")
	runner, _ := newTestRunner(t)
	if _, err := runner.RunWorkspace(context.Background(), wsID, "manual"); err != nil {
		t.Fatalf("RunWorkspace: %v", err)
	}

	// Point the run at a task row in terminal-failed state. runtime_id is
	// NOT NULL by the active_requires_runtime check until completed_at is
	// set, so the row reuses the fixture agent's runtime.
	var runtimeID string
	if err := testPool.QueryRow(context.Background(),
		`SELECT runtime_id::text FROM agent WHERE id = $1`, agentID).Scan(&runtimeID); err != nil {
		t.Fatalf("read agent runtime: %v", err)
	}
	var runTaskID string
	if err := testPool.QueryRow(context.Background(),
		`SELECT task_id::text FROM retrospective_run WHERE workspace_id = $1 ORDER BY created_at DESC LIMIT 1`, wsID).Scan(&runTaskID); err != nil {
		t.Fatalf("read run task id: %v", err)
	}
	if runTaskID == "" || runTaskID == "NULL" {
		t.Fatalf("run should carry the enqueued task id, got %q", runTaskID)
	}
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO agent_task_queue (id, agent_id, runtime_id, issue_id, status, created_at)
		VALUES ($1, $2, $3, NULL, 'failed', now())`, stubTaskID, agentID, runtimeID); err != nil {
		t.Fatalf("insert failed task row: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id = $1`, stubTaskID)
	})

	n, err := runner.ReconcileStaleRuns(context.Background())
	if err != nil {
		t.Fatalf("ReconcileStaleRuns: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected one reconciled run, got %d", n)
	}
	status, errMsg, _ := runRecord(t, wsID)
	if status != "failed" || errMsg == "" {
		t.Fatalf("stale run should be failed with a reason, got %q / %q", status, errMsg)
	}
}

// TestParseRunOutputBoundaries: the report parser accepts plain and fenced
// JSON and rejects everything else with a readable error.
func TestParseRunOutputBoundaries(t *testing.T) {
	issueID := "11111111-1111-4111-8111-111111111111"
	good := reportJSON(issueID, "解析条款")
	if _, err := ParseRunOutput(good); err != nil {
		t.Fatalf("plain JSON rejected: %v", err)
	}
	fenced := "```json\n" + good + "\n```"
	if _, err := ParseRunOutput(fenced); err != nil {
		t.Fatalf("fenced JSON rejected: %v", err)
	}
	if _, err := ParseRunOutput("这不是 JSON"); err == nil {
		t.Fatalf("garbage accepted")
	}
}

// TestPromptFromContextContract: the rendered prompt states the no-issue /
// no-comment boundary, carries the workspace and run ids, the window, the
// issue list, and the JSON output contract.
func TestPromptFromContextContract(t *testing.T) {
	issueID := "22222222-2222-4222-8222-222222222222"
	ctx := TaskContext{
		Kind:        TaskKind,
		RunID:       "33333333-3333-4333-8333-333333333333",
		WorkspaceID: "44444444-4444-4444-8444-444444444444",
		WindowStart: "2026-10-01T00:00:00Z",
		WindowEnd:   "2026-10-08T00:00:00Z",
		Issues:      []TaskContextIssue{{ID: issueID, Title: "修复了发布脚本"}},
	}
	prompt, ok := PromptFromContext(mustJSON(t, ctx))
	if !ok {
		t.Fatalf("PromptFromContext rejected its own context")
	}
	for _, want := range []string{
		"Do NOT create, update, or comment on any issue",
		ctx.WorkspaceID,
		ctx.RunID,
		issueID,
		"修复了发布脚本",
		"analyzed_issue_ids",
		"issue_id",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q", want)
		}
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}
