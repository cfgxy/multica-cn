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

	"github.com/jackc/pgx/v5/pgtype"
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
			`DELETE FROM project WHERE workspace_id = $1`,
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

// runReasonOf reads the latest run's structured failure verdict. Fails the
// test when the run carries no reason — failure paths must always write one
// (RUYI-561), and the legacy `error` text column must stay empty on rows
// this code finishes.
func runReasonOf(t *testing.T, wsID string) runReason {
	t.Helper()
	_, errMsg, detail := runRecord(t, wsID)
	var d struct {
		Reason *runReason `json:"reason"`
	}
	if err := json.Unmarshal([]byte(detail), &d); err != nil {
		t.Fatalf("parse run detail %s: %v", detail, err)
	}
	if d.Reason == nil || d.Reason.Code == "" {
		t.Fatalf("run has no structured reason (error column %q, detail %s)", errMsg, detail)
	}
	if errMsg != "" {
		t.Fatalf("error column must stay empty on coded failures, got %q", errMsg)
	}
	return *d.Reason
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
// possible before migration 935) records a failed run carrying the
// actionable agent_not_configured reason and reports ErrNoAgent instead of
// dispatching anything.
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
	status, _, _ := runRecord(t, wsID)
	if status != "failed" {
		t.Fatalf("run should fail, got %q", status)
	}
	if reason := runReasonOf(t, wsID); reason.Code != ReasonAgentNotConfigured || reason.IssueID != "" {
		t.Fatalf("want actionable %s reason without params, got %+v", ReasonAgentNotConfigured, reason)
	}
}

// TestRunWorkspaceAgentGateReasons: each way a configured agent can become
// unusable records its own actionable reason on the failed run — missing,
// archived, runtime-less — so the UI can name the exact fix.
func TestRunWorkspaceAgentGateReasons(t *testing.T) {
	cases := []struct {
		name   string
		mutate string // SQL making the configured agent unusable
		want   string
	}{
		{"configured agent vanished", `UPDATE retrospective_config SET agent_id = '00000000-0000-4000-8000-000000000001' WHERE workspace_id = $1`, ReasonAgentMissing},
		{"configured agent archived", `UPDATE agent SET archived_at = now() WHERE id = (SELECT agent_id FROM retrospective_config WHERE workspace_id = $1)`, ReasonAgentArchived},
		{"configured agent lost its runtime", `UPDATE agent SET runtime_id = NULL WHERE id = (SELECT agent_id FROM retrospective_config WHERE workspace_id = $1)`, ReasonAgentRuntimeMissing},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wsID, _ := retroFixture(t)
			_ = retroIssue(t, wsID, "有人干但干不了", "")
			if _, err := testPool.Exec(context.Background(), tc.mutate, wsID); err != nil {
				t.Fatalf("mutate agent: %v", err)
			}
			runner, calls := newTestRunner(t)

			_, err := runner.RunWorkspace(context.Background(), wsID, "schedule")
			if err == nil || err.Error() != ErrNoAgent.Error() {
				t.Fatalf("expected ErrNoAgent, got %v", err)
			}
			if n := len(*calls); n != 0 {
				t.Fatalf("unusable agent must not enqueue, got %d calls", n)
			}
			status, _, _ := runRecord(t, wsID)
			if status != "failed" {
				t.Fatalf("run should fail, got %q", status)
			}
			if reason := runReasonOf(t, wsID); reason.Code != tc.want || reason.IssueID != "" {
				t.Fatalf("want %s without params, got %+v", tc.want, reason)
			}
		})
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
	status, _, _ := runRecord(t, wsID)
	if status != "failed" {
		t.Fatalf("run should record the failure, got %q", status)
	}
	// The daemon's raw failure text must stay out of the run row (RUYI-561):
	// the coded reason is all the UI gets.
	if reason := runReasonOf(t, wsID); reason.Code != ReasonAgentRunFailed || reason.IssueID != "" {
		t.Fatalf("want %s without params, got %+v", ReasonAgentRunFailed, reason)
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
	status, _, _ := runRecord(t, wsID)
	if status != "failed" {
		t.Fatalf("stale run should be failed, got %q", status)
	}
	if reason := runReasonOf(t, wsID); reason.Code != ReasonTaskNotCompleted || reason.IssueID != "" {
		t.Fatalf("stale run should carry %s, got %+v", ReasonTaskNotCompleted, reason)
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

// retroProject inserts one project and returns its id; the fixture cleanup
// in retroFixture removes it.
func retroProject(t *testing.T, wsID, title, instructions string) string {
	t.Helper()
	fixtureCounter++
	var projectID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO project (workspace_id, title, description, status, instructions)
		VALUES ($1, $2, '', 'in_progress', $3) RETURNING id`,
		wsID, title, instructions).Scan(&projectID); err != nil {
		t.Fatalf("create project: %v", err)
	}
	return projectID
}

// retroIssueInProject is retroIssue with the issue filed under a project
// (RUYI-723: the draft's project carrier is resolved from this column).
func retroIssueInProject(t *testing.T, wsID, projectID, title string) string {
	t.Helper()
	issueID := retroIssue(t, wsID, title, "")
	if _, err := testPool.Exec(context.Background(),
		`UPDATE issue SET project_id = $2 WHERE id = $1`, issueID, projectID); err != nil {
		t.Fatalf("file issue under project: %v", err)
	}
	return issueID
}

// proposalValueContext reads the four RUYI-723 decision-context columns of
// the workspace's newest proposal.
func proposalValueContext(t *testing.T, wsID string) (problem, effect, gain string, score int, valid bool) {
	t.Helper()
	var problemN, effectN, gainN pgtype.Text
	var scoreN pgtype.Int4
	if err := testPool.QueryRow(context.Background(), `
		SELECT problem, expected_effect, expected_gain, value_score
		FROM prompt_proposal WHERE workspace_id = $1 ORDER BY created_at DESC LIMIT 1`,
		wsID).Scan(&problemN, &effectN, &gainN, &scoreN); err != nil {
		t.Fatalf("read proposal value context: %v", err)
	}
	return problemN.String, effectN.String, gainN.String, int(scoreN.Int32), scoreN.Valid
}

// TestRunContextCarriesCarrierSectionLists (RUYI-723 requirement C): the
// enqueued context carries the workspace and every scanned project's
// carrier — scope, scope id, display name, current `## ` section list — and
// each scanned issue names its project carrier.
func TestRunContextCarriesCarrierSectionLists(t *testing.T) {
	wsID, _ := retroFixture(t)
	if _, err := testPool.Exec(context.Background(),
		`UPDATE workspace SET context = $2 WHERE id = $1`, wsID,
		"# 平台协作规范\n\n## 沟通规范\n\n- **行文基调**：专业、正式。\n\n## 团队纪律\n\n- 单写者。\n"); err != nil {
		t.Fatalf("seed workspace carrier: %v", err)
	}
	projectID := retroProject(t, wsID, "multica 项目", "# 项目说明\n\n## 研发基础设施\n\n- 槽位固定。\n")
	if _, err := testPool.Exec(context.Background(),
		`UPDATE project SET instructions = $2 WHERE id = $1`, projectID,
		"# 项目说明\n\n## 研发基础设施\n\n- **项目只读**：测试前先确认目标环境。\n"); err != nil {
		t.Fatalf("seed project carrier: %v", err)
	}
	retroIssueInProject(t, wsID, projectID, "发布脚本修复")

	runner, calls := newTestRunner(t)
	if _, err := runner.RunWorkspace(context.Background(), wsID, "manual"); err != nil {
		t.Fatalf("RunWorkspace: %v", err)
	}
	var tc TaskContext
	if err := json.Unmarshal((*calls)[0].params.Context, &tc); err != nil {
		t.Fatalf("context is not a TaskContext: %v", err)
	}
	if len(tc.Carriers) != 2 {
		t.Fatalf("expected workspace + project carriers, got %+v", tc.Carriers)
	}
	byScope := map[string]TaskContextCarrier{}
	for _, c := range tc.Carriers {
		byScope[c.Scope+"|"+c.ScopeID] = c
	}
	wsCarrier, ok := byScope["workspace|"+wsID]
	if !ok {
		t.Fatalf("workspace carrier missing: %+v", tc.Carriers)
	}
	if len(wsCarrier.Sections) != 2 || wsCarrier.Sections[0] != "沟通规范" || wsCarrier.Sections[1] != "团队纪律" {
		t.Fatalf("workspace sections wrong: %+v", wsCarrier.Sections)
	}
	if len(wsCarrier.StaticClauses) != 1 || wsCarrier.StaticClauses[0] != "行文基调" {
		t.Fatalf("workspace static clauses wrong: %+v", wsCarrier.StaticClauses)
	}
	prjCarrier, ok := byScope["project|"+projectID]
	if !ok {
		t.Fatalf("project carrier missing: %+v", tc.Carriers)
	}
	if len(prjCarrier.Sections) != 1 || prjCarrier.Sections[0] != "研发基础设施" {
		t.Fatalf("project sections wrong: %+v", prjCarrier.Sections)
	}
	if len(prjCarrier.StaticClauses) != 1 || prjCarrier.StaticClauses[0] != "项目只读" {
		t.Fatalf("project static clauses wrong: %+v", prjCarrier.StaticClauses)
	}
	if prjCarrier.Name != "multica 项目" {
		t.Fatalf("project carrier name wrong: %q", prjCarrier.Name)
	}
	if len(tc.Issues) != 1 || tc.Issues[0].ProjectID != projectID {
		t.Fatalf("scanned issue must name its project carrier: %+v", tc.Issues)
	}
}

// TestPromptRendersCarrierListsAndScoringAnchors (RUYI-723 requirements
// B+C): with carriers present the prompt lists each carrier's sections, the
// verbatim-target rule, the scope/layer consistency rule and the value
// scoring anchors; a legacy context without carriers keeps rendering (and
// the new contract fields stay in the output schema for tolerant parsing).
func TestPromptRendersCarrierListsAndScoringAnchors(t *testing.T) {
	wsID := "44444444-4444-4444-8444-444444444444"
	projectID := "55555555-5555-5555-8555-555555555555"
	ctx := TaskContext{
		Kind:        TaskKind,
		RunID:       "33333333-3333-4333-8333-333333333333",
		WorkspaceID: wsID,
		WindowStart: "2026-10-01T00:00:00Z",
		WindowEnd:   "2026-10-08T00:00:00Z",
		Issues:      []TaskContextIssue{{ID: "22222222-2222-4222-8222-222222222222", Title: "修复了发布脚本", ProjectID: projectID}},
		Carriers: []TaskContextCarrier{
			{Scope: "workspace", ScopeID: wsID, Name: "Dev", Sections: []string{"沟通规范", "团队纪律"}, StaticClauses: []string{"单写者"}, Clauses: []string{"运行时复盘去重"}},
			{Scope: "project", ScopeID: projectID, Name: "multica 项目", Sections: []string{"研发基础设施"}, StaticClauses: []string{"项目只读"}},
		},
	}
	prompt, ok := PromptFromContext(mustJSON(t, ctx))
	if !ok {
		t.Fatalf("PromptFromContext rejected its own context")
	}
	for _, want := range []string{
		wsID,
		projectID,
		"沟通规范", "团队纪律", "研发基础设施",
		"static clauses: 单写者", "structured clauses: 运行时复盘去重", "static clauses: 项目只读",
		// verbatim-target rule (C)
		"VERBATIM",
		// scope/layer consistency rule (C)
		"gate_answers.layer",
		// out-of-platform targets are skipped this iteration (C)
		"Squad",
		// the four decision-context fields (B)
		"problem", "expected_effect", "expected_gain", "value_score",
		// scoring anchors (B)
		"rollback",
	} {
		if !strings.Contains(prompt, want) {
			prompt500 := prompt
			if len(prompt500) > 500 {
				prompt500 = prompt500[:500]
			}
			t.Fatalf("prompt missing %q; head: %s", want, prompt500)
		}
	}

	legacy := ctx
	legacy.Carriers = nil
	legacyPrompt, ok := PromptFromContext(mustJSON(t, legacy))
	if !ok {
		t.Fatalf("PromptFromContext rejected a legacy context")
	}
	if strings.Contains(legacyPrompt, "Prompt carriers") {
		t.Fatalf("legacy context must not render a carrier list")
	}
	for _, want := range []string{"problem", "expected_effect", "expected_gain", "value_score"} {
		if !strings.Contains(legacyPrompt, want) {
			t.Fatalf("legacy prompt keeps the decision-context contract, missing %q", want)
		}
	}
}

// TestProcessTaskTerminalValueContextFields (RUYI-723 requirement B): a
// full-field report lands problem/expected_effect/expected_gain and a
// 1-5 value score on the proposal row.
func TestProcessTaskTerminalValueContextFields(t *testing.T) {
	wsID, _ := retroFixture(t)
	issueA := retroIssue(t, wsID, "发布脚本修复", "根因是路径拼接")
	runner, calls := newTestRunner(t)
	if _, err := runner.RunWorkspace(context.Background(), wsID, "manual"); err != nil {
		t.Fatalf("RunWorkspace: %v", err)
	}
	report := fmt.Sprintf(`{"analyzed_issue_ids":[%q],"drafts":[{"issue_id":%q,"carrier_scope":"workspace","target_section":"","change_kind":"add_clause","clause_name":"全字段条款","clause_text":"- **全字段条款**：内容","problem":"发布脚本路径拼接错误导致每次发版返工","expected_effect":"路径一次性修正，发版不再因脚本失败中断","expected_gain":"每次发版节省约 30 分钟核对","value_score":4,"gate_answers":{"layer":"workspace","retention":"每轮适用","cost":"一行","conflict":"无","dedup":"无"},"evidence_ref":"issue 标题","evidence_note":"依据"}]}`,
		issueA, issueA)
	task := db.AgentTaskQueue{
		ID:      util.MustParseUUID(stubTaskID),
		Context: (*calls)[0].params.Context,
	}
	out, err := runner.ProcessTaskTerminal(context.Background(), task, report, "")
	if err != nil {
		t.Fatalf("ProcessTaskTerminal: %v", err)
	}
	if out.ProposalsCreated != 1 {
		t.Fatalf("expected one proposal, got %+v", out)
	}
	problem, effect, gain, score, valid := proposalValueContext(t, wsID)
	if problem != "发布脚本路径拼接错误导致每次发版返工" || effect != "路径一次性修正，发版不再因脚本失败中断" || gain != "每次发版节省约 30 分钟核对" {
		t.Fatalf("text fields wrong: %q / %q / %q", problem, effect, gain)
	}
	if !valid || score != 4 {
		t.Fatalf("value score wrong: valid=%v score=%d", valid, score)
	}
}

// TestProcessTaskTerminalValueScoreClamping (RUYI-723 requirement B): the
// score is a tolerant reference — floats round, out-of-range clamps to the
// 1-5 band, zero/negative and unparseable degrade to NULL (unrated), and
// none of it fails the run.
func TestProcessTaskTerminalValueScoreClamping(t *testing.T) {
	wsID, _ := retroFixture(t)
	issueHigh := retroIssue(t, wsID, "越界钳高", "")
	issueFloat := retroIssue(t, wsID, "小数取整", "")
	issueNull := retroIssue(t, wsID, "空分未评", "")
	runner, calls := newTestRunner(t)
	if _, err := runner.RunWorkspace(context.Background(), wsID, "manual"); err != nil {
		t.Fatalf("RunWorkspace: %v", err)
	}
	draft := func(issueID, name, score string) string {
		return fmt.Sprintf(`{"issue_id":%q,"carrier_scope":"workspace","target_section":"","change_kind":"add_clause","clause_name":%q,"clause_text":"- **%s**：内容","problem":"p","expected_effect":"e","expected_gain":"g","value_score":%s,"gate_answers":{"layer":"workspace","retention":"r","cost":"c","conflict":"cf","dedup":"d"},"evidence_ref":"ref","evidence_note":"note"}`,
			issueID, name, name, score)
	}
	report := fmt.Sprintf(`{"analyzed_issue_ids":[%q,%q,%q],"drafts":[%s,%s,%s]}`,
		issueHigh, issueFloat, issueNull,
		draft(issueHigh, "越界条款", "9"),
		draft(issueFloat, "小数条款", "4.6"),
		draft(issueNull, "空分条款", "null"),
	)
	task := db.AgentTaskQueue{
		ID:      util.MustParseUUID(stubTaskID),
		Context: (*calls)[0].params.Context,
	}
	out, err := runner.ProcessTaskTerminal(context.Background(), task, report, "")
	if err != nil {
		t.Fatalf("ProcessTaskTerminal: %v", err)
	}
	if out.ProposalsCreated != 3 {
		t.Fatalf("all three drafts must land: %+v", out)
	}
	rows := func() map[string]int {
		t.Helper()
		out := map[string]int{}
		rows, err := testPool.Query(context.Background(), `
			SELECT clause_name, value_score FROM prompt_proposal WHERE workspace_id = $1`, wsID)
		if err != nil {
			t.Fatalf("query proposals: %v", err)
		}
		defer rows.Close()
		for rows.Next() {
			var name string
			var score pgtype.Int4
			if err := rows.Scan(&name, &score); err != nil {
				t.Fatalf("scan: %v", err)
			}
			if !score.Valid {
				out[name] = 0
			} else {
				out[name] = int(score.Int32)
			}
		}
		return out
	}()
	if rows["越界条款"] != 5 || rows["小数条款"] != 5 {
		t.Fatalf("clamp wrong: %+v", rows)
	}
	score, ok := rows["空分条款"]
	if ok && score != 0 {
		t.Fatalf("null score must stay unrated, got %d", score)
	}
	if !ok {
		t.Fatalf("空分条款 row missing: %+v", rows)
	}
}

// TestParseRunOutputToleratesLegacyShape (RUYI-723 requirement B): reports
// without the decision-context fields parse cleanly — the fields stay
// zero-valued and the run is never failed by their absence.
func TestParseRunOutputToleratesLegacyShape(t *testing.T) {
	issueID := "11111111-1111-4111-8111-111111111111"
	out, err := ParseRunOutput(reportJSON(issueID, "旧形状条款"))
	if err != nil {
		t.Fatalf("legacy report rejected: %v", err)
	}
	if len(out.Drafts) != 1 {
		t.Fatalf("expected one draft, got %d", len(out.Drafts))
	}
	d := out.Drafts[0]
	if d.Problem != "" || d.ExpectedEffect != "" || d.ExpectedGain != "" || d.ValueScore != 0 {
		t.Fatalf("legacy draft must carry zero-valued new fields: %+v", d)
	}
}

// TestProcessTaskTerminalProjectCarrierScope (RUYI-723 requirement C): a
// project-scoped draft resolves carrier_scope_id from its scanned issue's
// project; drafts naming carriers this iteration does not produce (squad,
// agent, off-platform handbooks) or a project the issue does not have are
// dropped and counted, and a scope-less draft keeps the workspace default.
func TestProcessTaskTerminalProjectCarrierScope(t *testing.T) {
	wsID, _ := retroFixture(t)
	if _, err := testPool.Exec(context.Background(),
		`UPDATE workspace SET context = $2 WHERE id = $1`, wsID,
		"# 平台协作规范\n\n## 沟通规范\n\n- **项目载体条款**：工作区同名规则。\n"); err != nil {
		t.Fatalf("seed workspace clause: %v", err)
	}
	projectID := retroProject(t, wsID, "scoped 项目", "# 项目说明\n\n## 研发基础设施\n")
	issueWithProject := retroIssueInProject(t, wsID, projectID, "带项目修复")
	issueNoProject := retroIssue(t, wsID, "无项目修复", "")
	runner, calls := newTestRunner(t)
	if _, err := runner.RunWorkspace(context.Background(), wsID, "manual"); err != nil {
		t.Fatalf("RunWorkspace: %v", err)
	}
	if _, err := testPool.Exec(context.Background(),
		`UPDATE project SET instructions = $2 WHERE id = $1`, projectID,
		"# 项目说明\n\n## 研发基础设施\n\n- **运行期新增条款**：入队后写入项目的规则。\n"); err != nil {
		t.Fatalf("update project carrier after enqueue: %v", err)
	}
	draft := func(issueID, scope, name string) string {
		s := fmt.Sprintf(`"carrier_scope":%s,`, "\""+scope+"\"")
		if scope == "" {
			s = ""
		}
		return fmt.Sprintf(`{"issue_id":%q,%s"target_section":"","change_kind":"add_clause","clause_name":%q,"clause_text":"- **%s**：内容","gate_answers":{"layer":"workspace","retention":"r","cost":"c","conflict":"cf","dedup":"d"},"evidence_ref":"ref","evidence_note":"note"}`,
			issueID, s, name, name)
	}
	report := fmt.Sprintf(`{"analyzed_issue_ids":[%q,%q],"drafts":[%s,%s,%s,%s,%s]}`,
		issueWithProject, issueNoProject,
		draft(issueWithProject, "project", "项目载体条款"),
		draft(issueWithProject, "project", "运行期新增条款"),
		draft(issueWithProject, "", "默认工作区条款"),
		draft(issueNoProject, "squad", "小队载体条款"),
		draft(issueNoProject, "project", "无主项目条款"),
	)
	task := db.AgentTaskQueue{
		ID:      util.MustParseUUID(stubTaskID),
		Context: (*calls)[0].params.Context,
	}
	out, err := runner.ProcessTaskTerminal(context.Background(), task, report, "")
	if err != nil {
		t.Fatalf("ProcessTaskTerminal: %v", err)
	}
	if out.ProposalsCreated != 2 || out.DuplicatesSkipped != 3 {
		t.Fatalf("expected 2 created / 3 skipped, got %+v", out)
	}
	status, errMsg, _ := runRecord(t, wsID)
	if status != "succeeded" || errMsg != "" {
		t.Fatalf("dropped drafts must not fail the run, got %q / %q", status, errMsg)
	}
	var scope, scopeID string
	if err := testPool.QueryRow(context.Background(), `
		SELECT carrier_scope, carrier_scope_id::text FROM prompt_proposal
		WHERE workspace_id = $1 AND clause_name = '项目载体条款'`, wsID).Scan(&scope, &scopeID); err != nil {
		t.Fatalf("read project-scoped proposal: %v", err)
	}
	if scope != "project" || scopeID != projectID {
		t.Fatalf("project draft must resolve to the issue's project, got %s / %s", scope, scopeID)
	}
	if err := testPool.QueryRow(context.Background(), `
		SELECT carrier_scope FROM prompt_proposal
		WHERE workspace_id = $1 AND clause_name = '默认工作区条款'`, wsID).Scan(&scope); err != nil {
		t.Fatalf("read scope-less proposal: %v", err)
	}
	if scope != "workspace" {
		t.Fatalf("scope-less draft must default to workspace, got %s", scope)
	}
}

func TestProcessTaskTerminalContinuesAfterProjectDeletion(t *testing.T) {
	wsID, _ := retroFixture(t)
	projectID := retroProject(t, wsID, "待删除项目", "# 项目说明\n\n## 研发基础设施\n")
	projectIssue := retroIssueInProject(t, wsID, projectID, "项目载体待删除")
	workspaceIssue := retroIssue(t, wsID, "工作区提案继续处理", "")
	runner, calls := newTestRunner(t)
	if _, err := runner.RunWorkspace(context.Background(), wsID, "manual"); err != nil {
		t.Fatalf("RunWorkspace: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `DELETE FROM project WHERE id = $1`, projectID); err != nil {
		t.Fatalf("delete project carrier: %v", err)
	}
	draft := func(issueID, scope, name string) string {
		return fmt.Sprintf(`{"issue_id":%q,"carrier_scope":%q,"target_section":"","change_kind":"add_clause","clause_name":%q,"clause_text":"- **%s**：内容","gate_answers":{"layer":"workspace","retention":"r","cost":"c","conflict":"cf","dedup":"d"},"evidence_ref":"ref","evidence_note":"note"}`,
			issueID, scope, name, name)
	}
	report := fmt.Sprintf(`{"analyzed_issue_ids":[%q,%q],"drafts":[%s,%s]}`,
		projectIssue, workspaceIssue,
		draft(projectIssue, "project", "已删除项目条款"),
		draft(workspaceIssue, "workspace", "工作区继续条款"),
	)
	task := db.AgentTaskQueue{ID: util.MustParseUUID(stubTaskID), Context: (*calls)[0].params.Context}
	out, err := runner.ProcessTaskTerminal(context.Background(), task, report, "")
	if err != nil {
		t.Fatalf("ProcessTaskTerminal: %v", err)
	}
	if out.ProposalsCreated != 1 || out.DuplicatesSkipped != 1 {
		t.Fatalf("expected surviving workspace proposal and skipped project proposal, got %+v", out)
	}
	status, _, _ := runRecord(t, wsID)
	if status != "succeeded" {
		t.Fatalf("project deletion must not fail other drafts, got run status %q", status)
	}
}

func TestUpsertDraftSkipsProjectDeletedAfterInventoryRead(t *testing.T) {
	wsID, _ := retroFixture(t)
	projectID := retroProject(t, wsID, "复核期间删除", "")
	issueID := retroIssueInProject(t, wsID, projectID, "项目条款复核")
	runner, _ := newTestRunner(t)
	if _, err := testPool.Exec(context.Background(), `DELETE FROM project WHERE id = $1`, projectID); err != nil {
		t.Fatalf("delete project after inventory read: %v", err)
	}

	stats := &RunStats{}
	err := runner.upsertDraft(context.Background(), wsID, "33333333-3333-4333-8333-333333333333", issueID,
		extractedDraft{
			CarrierScope: "project",
			ChangeKind:   "add_clause",
			ClauseName:   "删除竞态条款",
			ClauseText:   "- **删除竞态条款**：内容",
		}, nil, stats, false, map[string]string{issueID: projectID})
	if err != nil {
		t.Fatalf("upsertDraft: %v", err)
	}
	if stats.ProposalsCreated != 0 || stats.ProposalsMerged != 0 || stats.DuplicatesSkipped != 1 {
		t.Fatalf("deleted project draft should be skipped, got %+v", stats)
	}
	var count int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM prompt_proposal WHERE workspace_id = $1`, wsID).Scan(&count); err != nil {
		t.Fatalf("count proposals: %v", err)
	}
	if count != 0 {
		t.Fatalf("proposal persisted for deleted project: count=%d", count)
	}
}

// retroClauseAsset seeds one active structured clause asset (clause,
// clause_version, clause_binding) bound to the workspace carrier and
// returns the clause id. The retroFixture cleanup does not cover the
// clause tables (RUYI-751), so the seed owns its own teardown.
func retroClauseAsset(t *testing.T, wsID, slug, title string) string {
	return retroClauseAssetForCarrier(t, wsID, "workspace", wsID, slug, title)
}

func retroClauseAssetForCarrier(t *testing.T, wsID, scope, scopeID, slug, title string) string {
	t.Helper()
	var clauseID string
	if err := testPool.QueryRow(context.Background(),
		`INSERT INTO clause (workspace_id, slug, title, status)
		 VALUES ($1, $2, $3, 'active') RETURNING id`,
		wsID, slug, title).Scan(&clauseID); err != nil {
		t.Fatalf("create clause asset: %v", err)
	}
	if _, err := testPool.Exec(context.Background(),
		`INSERT INTO clause_version (clause_id, version, content, content_sha256, carrier_scope)
			 VALUES ($1, 1, $2, $3, $4)`,
		clauseID, "- **"+title+"**：结构化条款正文", "testsha", scope); err != nil {
		t.Fatalf("create clause version: %v", err)
	}
	if _, err := testPool.Exec(context.Background(),
		`INSERT INTO clause_binding (clause_id, scope, scope_id, status)
			 VALUES ($1, $2, $3, 'active')`,
		clauseID, scope, scopeID); err != nil {
		t.Fatalf("create clause binding: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		for _, stmt := range []string{
			`DELETE FROM clause_binding WHERE clause_id = $1`,
			`DELETE FROM clause_version WHERE clause_id = $1`,
			`DELETE FROM clause WHERE id = $1`,
		} {
			if _, err := testPool.Exec(ctx, stmt, clauseID); err != nil {
				t.Logf("clause asset cleanup: %v", err)
			}
		}
	})
	return clauseID
}

// TestRunContextCarriesClauseInventory (RUYI-858): the run context's
// workspace carrier lists the active structured clause asset titles, and
// the prompt's three-action section engages because the inventory is
// non-empty — the agent can see the clause library's dynamic surface even
// when its text is not rendered into the carrier's static content.
func TestRunContextCarriesClauseInventory(t *testing.T) {
	wsID, _ := retroFixture(t)
	if _, err := testPool.Exec(context.Background(),
		`UPDATE workspace SET context = $2 WHERE id = $1`, wsID,
		"# 平台协作规范\n\n## 沟通规范\n\n- **单写者**：一项规则由一个责任方维护。\n"); err != nil {
		t.Fatalf("seed carrier: %v", err)
	}
	retroClauseAsset(t, wsID, "cls_858a01", "运行时复盘去重")
	retroIssue(t, wsID, "复盘清单检查", "")

	runner, calls := newTestRunner(t)
	if _, err := runner.RunWorkspace(context.Background(), wsID, "manual"); err != nil {
		t.Fatalf("RunWorkspace: %v", err)
	}
	var tc TaskContext
	if err := json.Unmarshal((*calls)[0].params.Context, &tc); err != nil {
		t.Fatalf("context is not a TaskContext: %v", err)
	}
	var wsCarrier TaskContextCarrier
	found := false
	for _, c := range tc.Carriers {
		if c.Scope == "workspace" && c.ScopeID == wsID {
			wsCarrier, found = c, true
		}
	}
	if !found {
		t.Fatalf("workspace carrier missing: %+v", tc.Carriers)
	}
	if len(wsCarrier.StaticClauses) != 1 || wsCarrier.StaticClauses[0] != "单写者" {
		t.Fatalf("expected the static clause title on the carrier, got %+v", wsCarrier.StaticClauses)
	}
	if len(wsCarrier.Clauses) != 1 || wsCarrier.Clauses[0] != "运行时复盘去重" {
		t.Fatalf("expected the structured clause title on the carrier, got %+v", wsCarrier.Clauses)
	}
	prompt, ok := PromptFromContext(mustJSON(t, tc))
	if !ok {
		t.Fatalf("PromptFromContext rejected its own context")
	}
	for _, want := range []string{"holds two kinds of rules", "static clauses: 单写者", "structured clauses: 运行时复盘去重", "add_clause", "revise_clause", "remove_clause"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing clause inventory/action %q", want)
		}
	}
}

func TestRunContextCarriesClauseInventoriesWithoutSections(t *testing.T) {
	wsID, _ := retroFixture(t)
	if _, err := testPool.Exec(context.Background(),
		`UPDATE workspace SET context = $2 WHERE id = $1`, wsID,
		"# 工作区规则\n\n- **工作区静态条款**：正文条款。\n"); err != nil {
		t.Fatalf("seed workspace carrier: %v", err)
	}
	projectID := retroProject(t, wsID, "无章节项目", "# 项目规则\n\n- **项目静态条款**：正文条款。\n")
	retroClauseAssetForCarrier(t, wsID, "workspace", wsID, "cls_858a03", "工作区结构化条款")
	retroClauseAssetForCarrier(t, wsID, "project", projectID, "cls_858a04", "项目结构化条款")
	retroIssueInProject(t, wsID, projectID, "无章节标题复盘")

	runner, calls := newTestRunner(t)
	if _, err := runner.RunWorkspace(context.Background(), wsID, "manual"); err != nil {
		t.Fatalf("RunWorkspace: %v", err)
	}
	var tc TaskContext
	if err := json.Unmarshal((*calls)[0].params.Context, &tc); err != nil {
		t.Fatalf("context is not a TaskContext: %v", err)
	}
	if len(tc.Carriers) != 2 {
		t.Fatalf("expected both carriers without sections, got %+v", tc.Carriers)
	}
	byScope := map[string]TaskContextCarrier{}
	for _, carrier := range tc.Carriers {
		byScope[carrier.Scope] = carrier
		if len(carrier.Sections) != 0 {
			t.Fatalf("carrier unexpectedly has sections: %+v", carrier)
		}
	}
	for scope, wants := range map[string][2]string{
		"workspace": {"工作区静态条款", "工作区结构化条款"},
		"project":   {"项目静态条款", "项目结构化条款"},
	} {
		carrier, ok := byScope[scope]
		if !ok {
			t.Fatalf("%s carrier missing: %+v", scope, tc.Carriers)
		}
		if len(carrier.StaticClauses) != 1 || carrier.StaticClauses[0] != wants[0] {
			t.Fatalf("%s static clauses wrong: %+v", scope, carrier.StaticClauses)
		}
		if len(carrier.Clauses) != 1 || carrier.Clauses[0] != wants[1] {
			t.Fatalf("%s structured clauses wrong: %+v", scope, carrier.Clauses)
		}
	}
	prompt, ok := PromptFromContext(mustJSON(t, tc))
	if !ok {
		t.Fatalf("PromptFromContext rejected its own context")
	}
	for _, want := range []string{
		"static clauses: 工作区静态条款", "structured clauses: 工作区结构化条款",
		"static clauses: 项目静态条款", "structured clauses: 项目结构化条款",
		"use the empty string to append the clause at the carrier's document tail",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q", want)
		}
	}
}

// TestProcessTaskTerminalDedupAgainstClauseAssets (RUYI-858): an add_clause
// name differing only by whitespace from an active structured asset is
// dropped as a duplicate.
func TestProcessTaskTerminalDedupAgainstClauseAssets(t *testing.T) {
	wsID, _ := retroFixture(t)
	if _, err := testPool.Exec(context.Background(),
		`UPDATE workspace SET context = $2 WHERE id = $1`, wsID,
		"# 平台协作规范\n\n## 沟通规范\n\n- **单写者**：一项规则由一个责任方维护。\n"); err != nil {
		t.Fatalf("seed carrier: %v", err)
	}
	retroClauseAsset(t, wsID, "cls_858a02", "运行时复盘去重")
	issueA := retroIssue(t, wsID, "复盘去重验证", "")
	runner, calls := newTestRunner(t)
	if _, err := runner.RunWorkspace(context.Background(), wsID, "manual"); err != nil {
		t.Fatalf("RunWorkspace: %v", err)
	}

	task := db.AgentTaskQueue{
		ID:      util.MustParseUUID(stubTaskID),
		Context: (*calls)[0].params.Context,
	}
	out, err := runner.ProcessTaskTerminal(context.Background(), task, reportJSON(issueA, "运行时复盘去重 "), "")
	if err != nil {
		t.Fatalf("ProcessTaskTerminal: %v", err)
	}
	if out.DuplicatesSkipped != 1 || out.ProposalsCreated != 0 {
		t.Fatalf("expected the asset clause to be skipped: %+v", out)
	}
	status, _, _ := runRecord(t, wsID)
	if status != "succeeded" {
		t.Fatalf("asset-dedup-only report still succeeds, got %q", status)
	}
	var watermark int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM retrospective_issue_watermark WHERE workspace_id = $1 AND issue_id = $2`,
		wsID, issueA).Scan(&watermark); err != nil || watermark != 1 {
		t.Fatalf("watermark missing after asset dedup (%v, %d)", err, watermark)
	}
}
