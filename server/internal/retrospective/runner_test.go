package retrospective

// Tests for the daily retrospective runner (RUYI-305 E3): window scanning,
// the per-issue idempotency watermark, pool merge, the current-clause
// pre-check, and the boundary the product cares about most — the run
// writes nothing into issues (no comments, no reports); every outcome
// lands in prompt_proposal and retrospective_run only.

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

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
		os.Exit(0)
	}
	testPool = pool
	code := m.Run()
	pool.Close()
	os.Exit(code)
}

// fakeLLM replies with a fixed script: one JSON document per GenerateJSON
// call, in order. It also counts calls so tests can assert model spend.
type fakeLLM struct {
	replies []string
	calls   int
}

func (f *fakeLLM) GenerateJSON(ctx context.Context, model, systemPrompt, userPrompt string, temperature float64, maxCompletionTokens int64) (string, error) {
	if f.calls >= len(f.replies) {
		return `{"drafts":[]}`, nil
	}
	r := f.replies[f.calls]
	f.calls++
	return r, nil
}

func (f *fakeLLM) Enabled() bool { return true }

func draftJSON(changeKind, clauseName, clauseText string) string {
	return fmt.Sprintf(`{"drafts":[{"carrier_scope":"workspace","target_section":"沟通规范","change_kind":%q,"clause_name":%q,"clause_text":%q,`+
		`"gate_answers":{"layer":"workspace=跨项目协作机制","retention":"每轮输出都适用","cost":"少量常驻","conflict":"无同主题条款","dedup":"无重复"},`+
		`"evidence_ref":"issue 标题","evidence_note":"依据说明"}]}`, changeKind, clauseName, clauseText)
}

var fixtureCounter = time.Now().UnixNano()

// retroFixture builds one workspace with the retrospective enabled, two done
// issues inside the window, and returns the workspace id.
func retroFixture(t *testing.T, carrierContext string) string {
	t.Helper()
	fixtureCounter++
	suffix := fmt.Sprintf("retro%d", fixtureCounter)

	var wsID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO workspace (name, slug, description, issue_prefix, context)
		VALUES ($1, $2, '', '', $3) RETURNING id`,
		"retrospective ws "+suffix, "retrospective-"+suffix, carrierContext).Scan(&wsID); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO retrospective_config (workspace_id, enabled, include_in_review, window_days)
		VALUES ($1, true, false, 7)`, wsID); err != nil {
		t.Fatalf("create config: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		for _, stmt := range []string{
			`DELETE FROM retrospective_issue_watermark WHERE workspace_id = $1`,
			`DELETE FROM retrospective_run WHERE workspace_id = $1`,
			`DELETE FROM retrospective_config WHERE workspace_id = $1`,
			`DELETE FROM prompt_proposal WHERE workspace_id = $1`,
			`DELETE FROM prompt_structure_baseline WHERE workspace_id = $1`,
			`DELETE FROM comment WHERE workspace_id = $1`,
			`DELETE FROM issue WHERE workspace_id = $1`,
			`DELETE FROM member WHERE workspace_id = $1`,
			`DELETE FROM "user" WHERE email LIKE 'retrospective-%' AND email LIKE '%` + suffix + `%'`,
			`DELETE FROM workspace WHERE id = $1`,
		} {
			testPool.Exec(ctx, stmt, wsID)
		}
	})
	return wsID
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

func newTestRunner(llm LLMClient) *Runner {
	return &Runner{DB: testPool, Queries: db.New(testPool), LLM: llm}
}

func runRecord(t *testing.T, wsID string) (status, errMsg string) {
	t.Helper()
	if err := testPool.QueryRow(context.Background(), `
		SELECT status, error FROM retrospective_run WHERE workspace_id = $1 ORDER BY created_at DESC LIMIT 1`,
		wsID).Scan(&status, &errMsg); err != nil {
		t.Fatalf("read run record: %v", err)
	}
	return status, errMsg
}

// TestRunWorkspaceIdempotentAndSilent: the first run analyzes both issues and
// lands drafts in the pool; an immediate second run over the overlapping
// window re-derives nothing (watermarks); and no run ever writes a comment
// row — the issue surface stays untouched.
func TestRunWorkspaceIdempotentAndSilent(t *testing.T) {
	wsID := retroFixture(t, "# 规范\n\n## 沟通规范\n")
	issueA := retroIssue(t, wsID, "复盘测试 A", "讨论内容甲")
	issueB := retroIssue(t, wsID, "复盘测试 B", "讨论内容乙")

	var commentsBefore int
	if err := testPool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM comment WHERE workspace_id = $1`, wsID).Scan(&commentsBefore); err != nil {
		t.Fatal(err)
	}

	llm := &fakeLLM{replies: []string{
		draftJSON("add_clause", "同步纪律", "- **同步纪律**：各成员开工前回报当日计划。"),
		draftJSON("add_clause", "证据留存", "- **证据留存**：结论必须附可复核证据。"),
	}}
	runner := newTestRunner(llm)

	stats, err := runner.RunWorkspace(context.Background(), wsID, "manual")
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	if status, errMsg := runRecord(t, wsID); status != "succeeded" {
		t.Fatalf("first run record: %q / %q", status, errMsg)
	}
	if stats.IssuesScanned != 2 || stats.IssuesAnalyzed != 2 {
		t.Fatalf("first run stats: scanned=%d analyzed=%d, want 2/2", stats.IssuesScanned, stats.IssuesAnalyzed)
	}
	if stats.ProposalsCreated != 2 {
		t.Fatalf("first run created %d proposals, want 2", stats.ProposalsCreated)
	}
	if status, errMsg := runRecord(t, wsID); status != "succeeded" || errMsg != "" {
		t.Fatalf("first run record: %q / %q", status, errMsg)
	}
	for _, issueID := range []string{issueA, issueB} {
		var n int
		if err := testPool.QueryRow(context.Background(),
			`SELECT COUNT(*) FROM retrospective_issue_watermark WHERE workspace_id = $1 AND issue_id = $2`,
			wsID, issueID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Fatalf("issue %s watermark count = %d, want 1", issueID, n)
		}
	}

	// Overlapping window, second run: everything is watermarked.
	stats2, err := runner.RunWorkspace(context.Background(), wsID, "manual")
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if stats2.IssuesScanned != 2 || stats2.IssuesAnalyzed != 0 {
		t.Fatalf("second run stats: scanned=%d analyzed=%d, want 2/0", stats2.IssuesScanned, stats2.IssuesAnalyzed)
	}
	if stats2.ProposalsCreated != 0 || stats2.ProposalsMerged != 0 {
		t.Fatalf("second run created new work: %+v", stats2)
	}

	var commentsAfter int
	if err := testPool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM comment WHERE workspace_id = $1`, wsID).Scan(&commentsAfter); err != nil {
		t.Fatal(err)
	}
	if commentsAfter != commentsBefore {
		t.Fatalf("run wrote issue comments: before=%d after=%d", commentsBefore, commentsAfter)
	}
}

// TestRunWorkspaceMergeAndPrecheck pins the two dedup layers: a draft whose
// clause already lives in the carrier is skipped before insert; a same-topic
// draft from a second issue merges into the live pool row (anchors
// accumulate) instead of creating a duplicate.
func TestRunWorkspaceMergeAndPrecheck(t *testing.T) {
	carrier := "# 规范\n\n## 沟通规范\n\n- **同步纪律**：各成员开工前回报当日计划。\n"
	wsID := retroFixture(t, carrier)
	retroIssue(t, wsID, "复盘测试 C", "讨论内容丙") // drafts the live clause → precheck skip
	retroIssue(t, wsID, "复盘测试 D", "讨论内容丁") // drafts the new clause
	retroIssue(t, wsID, "复盘测试 E", "讨论内容戊") // drafts the same new clause → merge

	llm := &fakeLLM{replies: []string{
		draftJSON("add_clause", "同步纪律", "- **同步纪律**：各成员开工前回报当日计划。"),
		draftJSON("add_clause", "评审纪律", "- **评审纪律**：改动需评审后落库。"),
		draftJSON("add_clause", "评审纪律", "- **评审纪律**：改动需评审后落库。"),
	}}
	runner := newTestRunner(llm)

	stats, err := runner.RunWorkspace(context.Background(), wsID, "manual")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if status, errMsg := runRecord(t, wsID); status != "succeeded" {
		t.Fatalf("run record: %q / %q", status, errMsg)
	}
	if stats.DuplicatesSkipped != 1 {
		t.Fatalf("duplicates_skipped = %d, want 1 (the live-clause precheck)", stats.DuplicatesSkipped)
	}
	if stats.ProposalsCreated != 1 || stats.ProposalsMerged != 1 {
		t.Fatalf("created=%d merged=%d, want 1/1", stats.ProposalsCreated, stats.ProposalsMerged)
	}

	var anchors, mergedFrom string
	if err := testPool.QueryRow(context.Background(), `
		SELECT evidence_anchors::text, merged_from::text FROM prompt_proposal
		WHERE workspace_id = $1 AND clause_name = '评审纪律'`, wsID).Scan(&anchors, &mergedFrom); err != nil {
		t.Fatalf("read merged proposal: %v", err)
	}
	if mergedFrom == `[]` {
		t.Fatalf("merged row has no merge record: %s", mergedFrom)
	}
	// Three issues ran; three watermarks — the skipped and merged issues are
	// still never re-analyzed.
	var n int
	if err := testPool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM retrospective_issue_watermark WHERE workspace_id = $1`, wsID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("watermarks = %d, want 3", n)
	}
}

// TestRunWorkspaceLLMDisabled: an unconfigured LLM is not a crash — the run
// records a failed run naming the missing configuration, and nothing enters
// the pool.
func TestRunWorkspaceLLMDisabled(t *testing.T) {
	wsID := retroFixture(t, "# 规范\n")
	retroIssue(t, wsID, "复盘测试 F", "讨论内容己")

	runner := newTestRunner(nil)
	stats, err := runner.RunWorkspace(context.Background(), wsID, "manual")
	if err != nil {
		t.Fatalf("disabled LLM run must not error: %v", err)
	}
	if stats.IssuesScanned != 1 {
		t.Fatalf("scanned=%d, want 1", stats.IssuesScanned)
	}
	status, errMsg := runRecord(t, wsID)
	if status != "failed" {
		t.Fatalf("run status = %q, want failed", status)
	}
	if errMsg == "" {
		t.Fatal("failed run must name the reason")
	}
	var proposals int
	if err := testPool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM prompt_proposal WHERE workspace_id = $1`, wsID).Scan(&proposals); err != nil {
		t.Fatal(err)
	}
	if proposals != 0 {
		t.Fatalf("disabled-LLM run created %d proposals", proposals)
	}
}
