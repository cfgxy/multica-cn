package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestBuildSearchQuery_SingleTerm(t *testing.T) {
	query, args := buildSearchQuery("Hello", []string{"Hello"}, 0, false, false, []string{"done", "cancelled"})

	// Pattern should be lowercased in Go.
	if args[0] != "hello" {
		t.Errorf("expected phrase arg to be lowercased, got %q", args[0])
	}

	// Must use LOWER(column) LIKE, not ILIKE.
	if strings.Contains(query, "ILIKE") {
		t.Error("query should not contain ILIKE")
	}
	if !strings.Contains(query, "LOWER(i.title) LIKE") {
		t.Error("query should contain LOWER(i.title) LIKE")
	}
	if !strings.Contains(query, "LOWER(COALESCE(i.description, '')) LIKE") {
		t.Error("query should contain LOWER(COALESCE(i.description, '')) LIKE")
	}
	if !strings.Contains(query, "LOWER(c.content) LIKE") {
		t.Error("query should contain LOWER(c.content) LIKE")
	}

	// Exact title rank should not double-LOWER the pattern.
	if strings.Contains(query, "LOWER(i.title) = LOWER(") {
		t.Error("exact title rank should not wrap pattern in LOWER (already lowercased in Go)")
	}
	if !strings.Contains(query, "LOWER(i.title) = $1") {
		t.Error("exact title rank should compare LOWER(i.title) = $1 directly")
	}

	// Should exclude closed issues by default.
	if !strings.Contains(query, "NOT (i.status = ANY(") {
		t.Error("query should exclude the expanded terminal status keys when includeClosed=false")
	}
	if strings.Contains(query, "issue_effective_status") {
		t.Error("query should not resolve status categories once per issue row")
	}
}

func TestBuildSearchQuery_CustomTerminalStatuses(t *testing.T) {
	terminalStatusKeys := []string{"done", "cancelled", "verified"}
	query, args := buildSearchQuery(
		"Hello",
		[]string{"Hello"},
		0,
		false,
		false,
		terminalStatusKeys,
	)

	if !strings.Contains(query, "NOT (i.status = ANY($5::text[]))") {
		t.Fatalf("query does not filter through the expanded terminal keys:\n%s", query)
	}
	got, ok := args[4].([]string)
	if !ok || len(got) != len(terminalStatusKeys) {
		t.Fatalf("terminal status argument = %#v, want %#v", args[4], terminalStatusKeys)
	}
	for i := range terminalStatusKeys {
		if got[i] != terminalStatusKeys[i] {
			t.Fatalf("terminal status argument = %#v, want %#v", got, terminalStatusKeys)
		}
	}
}

func TestBuildSearchQuery_MultiTerm(t *testing.T) {
	query, args := buildSearchQuery("Foo Bar", []string{"Foo", "Bar"}, 0, false, false, []string{"done", "cancelled"})

	// Both phrase and terms should be lowercased.
	if args[0] != "foo bar" {
		t.Errorf("expected phrase arg lowercased, got %q", args[0])
	}
	// args[0]=exact, args[1]=%phrase%, args[2]=phrase%, args[3]=workspace_id placeholder; term args start at args[4].
	if args[4] != "%foo%" {
		t.Errorf("expected first term arg as contains pattern, got %q", args[4])
	}
	if args[5] != "%bar%" {
		t.Errorf("expected second term arg as contains pattern, got %q", args[5])
	}

	// Multi-word query should have AND conditions.
	if !strings.Contains(query, " AND ") {
		t.Error("multi-word query should contain AND conditions for per-term matching")
	}
}

func TestBuildSearchQuery_WithNumber(t *testing.T) {
	query, args := buildSearchQuery("MUL-42", []string{"MUL-42"}, 42, true, false, []string{"done", "cancelled"})

	_ = args
	// Number match should be in WHERE.
	if !strings.Contains(query, "i.number = ") {
		t.Error("query should contain number match in WHERE clause")
	}
	// Tier 0 rank for identifier match.
	if !strings.Contains(query, "THEN 0") {
		t.Error("query should contain tier 0 rank for identifier match")
	}
}

func TestBuildSearchQuery_IncludeClosed(t *testing.T) {
	query, _ := buildSearchQuery("test", []string{"test"}, 0, false, true, nil)

	if strings.Contains(query, "i.status = ANY(") {
		t.Error("query should not exclude done/cancelled when includeClosed=true")
	}
}

func TestBuildSearchQuery_SpecialChars(t *testing.T) {
	query, args := buildSearchQuery("100%", []string{"100%"}, 0, false, false, []string{"done", "cancelled"})

	_ = query
	// % should be escaped in the phrase arg.
	if escaped, ok := args[0].(string); !ok || !strings.Contains(escaped, `\%`) {
		t.Errorf("expected %% to be escaped in phrase arg, got %q", args[0])
	}
}

// --- Project search tests ---

func TestBuildProjectSearchQuery_SingleTerm(t *testing.T) {
	query, args := buildProjectSearchQuery("Hello", []string{"Hello"}, false)

	if args[0] != "hello" {
		t.Errorf("expected phrase arg to be lowercased, got %q", args[0])
	}

	if strings.Contains(query, "ILIKE") {
		t.Error("query should not contain ILIKE")
	}
	if !strings.Contains(query, "LOWER(p.title) LIKE") {
		t.Error("query should contain LOWER(p.title) LIKE")
	}
	if !strings.Contains(query, "LOWER(COALESCE(p.description, '')) LIKE") {
		t.Error("query should contain LOWER(COALESCE(p.description, '')) LIKE")
	}

	// Should exclude completed/cancelled by default.
	if !strings.Contains(query, "NOT IN ('completed', 'cancelled')") {
		t.Error("query should exclude completed/cancelled when includeClosed=false")
	}
}

func TestBuildProjectSearchQuery_MultiTerm(t *testing.T) {
	query, args := buildProjectSearchQuery("Foo Bar", []string{"Foo", "Bar"}, false)

	if args[0] != "foo bar" {
		t.Errorf("expected phrase arg lowercased, got %q", args[0])
	}
	if args[2] != "foo" {
		t.Errorf("expected first term arg lowercased, got %q", args[2])
	}
	if args[3] != "bar" {
		t.Errorf("expected second term arg lowercased, got %q", args[3])
	}

	if !strings.Contains(query, " AND ") {
		t.Error("multi-word query should contain AND conditions for per-term matching")
	}
}

func TestBuildProjectSearchQuery_IncludeClosed(t *testing.T) {
	query, _ := buildProjectSearchQuery("test", []string{"test"}, true)

	if strings.Contains(query, "NOT IN ('completed', 'cancelled')") {
		t.Error("query should not exclude completed/cancelled when includeClosed=true")
	}
}

// --- extractSnippet regression tests ---

func TestExtractSnippet_PhraseMatch(t *testing.T) {
	content := "The quick brown fox jumps over the lazy dog near the river bank"
	snippet := extractSnippet(content, "brown fox")
	if !strings.Contains(snippet, "brown fox") {
		t.Errorf("snippet should contain the phrase 'brown fox', got %q", snippet)
	}
}

func TestExtractSnippet_MultiWordNonContiguous(t *testing.T) {
	// "deploy" and "kubernetes" both appear but not as a contiguous phrase.
	content := "We need to deploy the new service. The kubernetes cluster is ready for production workloads."
	snippet := extractSnippet(content, "deploy kubernetes")
	// Should NOT fall back to first 120 chars blindly — should center on earliest term.
	if !strings.Contains(strings.ToLower(snippet), "deploy") && !strings.Contains(strings.ToLower(snippet), "kubernetes") {
		t.Errorf("snippet should contain at least one search term, got %q", snippet)
	}
	// Specifically, "deploy" appears first so snippet should be centered around it.
	if !strings.Contains(strings.ToLower(snippet), "deploy") {
		t.Errorf("snippet should center on earliest term 'deploy', got %q", snippet)
	}
}

func TestExtractSnippet_FallbackWhenNoMatch(t *testing.T) {
	content := strings.Repeat("a", 200)
	snippet := extractSnippet(content, "zzz")
	if len([]rune(snippet)) > 124 { // 120 + "..."
		t.Errorf("snippet should be truncated to ~120 runes when no match, got len=%d", len([]rune(snippet)))
	}
}

func TestExtractSnippet_ShortContent(t *testing.T) {
	content := "short text"
	snippet := extractSnippet(content, "missing")
	if snippet != content {
		t.Errorf("short content with no match should return as-is, got %q", snippet)
	}
}

func TestExtractSnippet_CaseInsensitive(t *testing.T) {
	content := "Error in HTML rendering pipeline"
	snippet := extractSnippet(content, "html")
	if !strings.Contains(snippet, "HTML") {
		t.Errorf("snippet should find case-insensitive match, got %q", snippet)
	}
}

func TestExtractSnippet_CJKContent(t *testing.T) {
	content := "这是一段很长的中文内容，包含了搜索关键词测试用例，用来验证多字节字符不会被截断的情况"
	snippet := extractSnippet(content, "搜索关键词")
	if !strings.Contains(snippet, "搜索关键词") {
		t.Errorf("snippet should contain CJK phrase, got %q", snippet)
	}
}

// --- Ranking regression tests ---

func TestBuildSearchQuery_CommentRankTiers(t *testing.T) {
	query, _ := buildSearchQuery("test phrase", []string{"test", "phrase"}, 0, false, false, []string{"done", "cancelled"})

	// Comment phrase match should be tier 7
	if !strings.Contains(query, "THEN 7") {
		t.Error("query should contain tier 7 for comment phrase match")
	}
	// Comment all-term match should be tier 8
	if !strings.Contains(query, "THEN 8") {
		t.Error("query should contain tier 8 for comment all-term match")
	}
	// Fallback should be 9, not 7
	if !strings.Contains(query, "ELSE 9") {
		t.Error("query fallback should be ELSE 9")
	}
}

func TestBuildSearchQuery_DescriptionRankTiers(t *testing.T) {
	query, _ := buildSearchQuery("foo bar", []string{"foo", "bar"}, 0, false, false, []string{"done", "cancelled"})

	// Description phrase match should be tier 5
	if !strings.Contains(query, "THEN 5") {
		t.Error("query should contain tier 5 for description phrase match")
	}
	// Description all-term match should be tier 6
	if !strings.Contains(query, "THEN 6") {
		t.Error("query should contain tier 6 for description all-term match")
	}
}

func TestBuildSearchQuery_SingleTermNoAllTermTiers(t *testing.T) {
	query, _ := buildSearchQuery("html", []string{"html"}, 0, false, false, []string{"done", "cancelled"})

	// Extract the rank CASE expression (ends with "ELSE 9 END") to avoid
	// false matches against statusRank which also contains THEN 4/6.
	rankEnd := strings.Index(query, "ELSE 9 END")
	if rankEnd == -1 {
		t.Fatal("query should contain rank expression with ELSE 9 END")
	}
	rankExpr := query[:rankEnd]

	// Single-term queries should NOT have tier 4 (title all-terms), 6 (desc all-terms), or 8 (comment all-terms)
	if strings.Contains(rankExpr, "THEN 4") {
		t.Error("single-term query should not have tier 4 (title all-terms)")
	}
	if strings.Contains(rankExpr, "THEN 6") {
		t.Error("single-term query should not have tier 6 (description all-terms)")
	}
	if strings.Contains(rankExpr, "THEN 8") {
		t.Error("single-term query should not have tier 8 (comment all-terms)")
	}
}

// TestBuildSearchQuery_CommentSubqueryWorkspaceScope regressions the
// MUL-4059 fix: every EXISTS / correlated subquery over `comment` MUST
// filter by c.workspace_id = $wsParam. Without this, Postgres rewrites
// the correlated subquery into a hashed subplan that materializes every
// comment in the entire table matching the LIKE — on prd this was
// 536k rows / 32.3 s for '%search%'. With the filter the hashed set
// collapses to this workspace's comments and the plan uses the
// idx_comment_workspace supporting btree.
//
// $4 is buildSearchQuery's canonical workspace_id placeholder (the
// caller writes wsUUID into args[3] before executing).
func TestBuildSearchQuery_CommentSubqueryWorkspaceScope(t *testing.T) {
	singleQuery, _ := buildSearchQuery("html", []string{"html"}, 0, false, false, []string{"done", "cancelled"})

	// Every occurrence of `FROM comment c` must be followed by the
	// c.workspace_id = $4 constraint. Counting is safer than a single
	// substring check because the WHERE, rank CASE, matched_comment_content
	// subqueries all touch `comment` and must each carry the filter.
	fromCount := strings.Count(singleQuery, "FROM comment c")
	scopedCount := strings.Count(singleQuery, "c.workspace_id = $4")
	if fromCount == 0 {
		t.Fatalf("single-term query has no comment subquery — did buildSearchQuery drop it?")
	}
	if scopedCount < fromCount {
		t.Errorf("single-term query has %d comment subqueries but only %d workspace_id filters — %d unscoped subquery(ies) will trigger the MUL-4059 global-hash plan",
			fromCount, scopedCount, fromCount-scopedCount)
	}

	// Multi-term uses one extra comment subquery in the WHERE and one in
	// the rank CASE for the all-terms match — same invariant applies.
	multiQuery, _ := buildSearchQuery("foo bar", []string{"foo", "bar"}, 0, false, false, []string{"done", "cancelled"})
	fromCountMulti := strings.Count(multiQuery, "FROM comment c")
	scopedCountMulti := strings.Count(multiQuery, "c.workspace_id = $4")
	if scopedCountMulti < fromCountMulti {
		t.Errorf("multi-term query has %d comment subqueries but only %d workspace_id filters",
			fromCountMulti, scopedCountMulti)
	}
}

// --- MUL-5824: cancelled work must not outrank live work ---

// orderByClause returns everything after the final ORDER BY, so ranking-order
// assertions cannot be satisfied by an expression that merely appears in the
// SELECT list or the WHERE clause.
func orderByClause(t *testing.T, query string) string {
	t.Helper()
	i := strings.LastIndex(query, "ORDER BY ")
	if i == -1 {
		t.Fatalf("query has no ORDER BY clause:\n%s", query)
	}
	return query[i+len("ORDER BY "):]
}

// The cancelled demotion must sort BEFORE the relevance tiers, not after.
// As a tie-breaker it would be inert: statusRank only orders issues that
// already landed in the same tier, so an exactly-titled cancelled issue
// (tier 1) would still beat an in_progress title-contains match (tier 3).
func TestBuildSearchQuery_CancelledDemotedAheadOfRelevance(t *testing.T) {
	orderBy := orderByClause(t, buildSearchQueryForTest(t, "login bug", []string{"login", "bug"}, 0, false, true))

	cancelledAt := strings.Index(orderBy, "i.status = 'cancelled' AND NOT")
	if cancelledAt == -1 {
		t.Fatalf("ORDER BY has no cancelled demotion:\n%s", orderBy)
	}

	// "ELSE 9 END" terminates the relevance CASE (rankExpr).
	relevanceEndsAt := strings.Index(orderBy, "ELSE 9 END")
	if relevanceEndsAt == -1 {
		t.Fatalf("ORDER BY has no relevance rank CASE:\n%s", orderBy)
	}
	if cancelledAt > relevanceEndsAt {
		t.Errorf("cancelled demotion sorts after the relevance tiers, so a well-matching cancelled issue still outranks live work:\n%s", orderBy)
	}

	// The demotion must not replace the existing status ordering.
	if !strings.Contains(orderBy, "WHEN 'in_progress' THEN 0") {
		t.Errorf("statusRank was dropped from ORDER BY:\n%s", orderBy)
	}
	if !strings.Contains(orderBy, "i.updated_at DESC") {
		t.Errorf("recency tie-breaker was dropped from ORDER BY:\n%s", orderBy)
	}
}

// Searching an exact title or an exact identifier is unambiguous targeting —
// the searcher already knows which issue they want, so demoting it would just
// hide the row they asked for.
func TestBuildSearchQuery_CancelledDirectHitExempt(t *testing.T) {
	// $1 is the exact (non-wildcard) phrase param.
	textOnly := orderByClause(t, buildSearchQueryForTest(t, "ship it", []string{"ship", "it"}, 0, false, true))
	if !strings.Contains(textOnly, "i.status = 'cancelled' AND NOT (LOWER(i.title) = $1)") {
		t.Errorf("exact-title hit is not exempt from the cancelled demotion:\n%s", textOnly)
	}
	if strings.Contains(textOnly, "i.number = ") {
		t.Errorf("non-numeric query should not reference i.number in the demotion:\n%s", textOnly)
	}

	withNumber := orderByClause(t, buildSearchQueryForTest(t, "MUL-42", []string{"MUL-42"}, 42, true, true))
	if !strings.Contains(withNumber, "LOWER(i.title) = $1 OR i.number = ") {
		t.Errorf("identifier lookup is not exempt from the cancelled demotion, so MUL-42 sinks below every fuzzy match:\n%s", withNumber)
	}
}

// 'done' is finished work worth referencing; only 'cancelled' is thrown away.
func TestBuildSearchQuery_DoneNotDemotedAheadOfRelevance(t *testing.T) {
	orderBy := orderByClause(t, buildSearchQueryForTest(t, "login", []string{"login"}, 0, false, true))

	relevanceEndsAt := strings.Index(orderBy, "ELSE 9 END")
	if doneAt := strings.Index(orderBy, "i.status = 'done'"); doneAt != -1 && doneAt < relevanceEndsAt {
		t.Errorf("done issues were demoted ahead of relevance; only cancelled should be:\n%s", orderBy)
	}
}

// Project search has no statusRank at all, and the command palette renders
// projects above issues — an undemoted cancelled project can be the first row
// of the entire result list.
func TestBuildProjectSearchQuery_CancelledDemotedAheadOfRelevance(t *testing.T) {
	query, _ := buildProjectSearchQuery("platform", []string{"platform"}, true)
	orderBy := orderByClause(t, query)

	cancelledAt := strings.Index(orderBy, "p.status = 'cancelled'")
	if cancelledAt == -1 {
		t.Fatalf("project ORDER BY has no cancelled demotion:\n%s", orderBy)
	}
	relevanceEndsAt := strings.Index(orderBy, "ELSE 5 END")
	if relevanceEndsAt == -1 {
		t.Fatalf("project ORDER BY has no relevance rank CASE:\n%s", orderBy)
	}
	if cancelledAt > relevanceEndsAt {
		t.Errorf("cancelled projects sort after the relevance tiers:\n%s", orderBy)
	}
	if !strings.Contains(orderBy, "LOWER(p.title) <> $1") {
		t.Errorf("exact-title hit is not exempt from the cancelled demotion:\n%s", orderBy)
	}
	if !strings.Contains(orderBy, "p.updated_at DESC") {
		t.Errorf("recency tie-breaker was dropped from project ORDER BY:\n%s", orderBy)
	}
}

// buildSearchQuery mutates the terms slice in place (lowercasing); this wrapper
// keeps each test's literals independent.
func buildSearchQueryForTest(t *testing.T, phrase string, terms []string, num int, hasNum bool, includeClosed bool) string {
	t.Helper()
	query, _ := buildSearchQuery(phrase, append([]string(nil), terms...), num, hasNum, includeClosed, []string{"done", "cancelled"})
	return query
}

// --- RUYI-753 代码优化阶段：评论短语匹配单次物化 ---

// 同一评论短语 EXISTS 谓词此前在 WHERE 过滤与 rank Tier 7 各物化一次
// （生产 EXPLAIN 为两个 hashed SubPlan，各对候选集做一轮 LOWER+LIKE recheck，
// 各约 130ms）。本单将其改为单个 LEFT JOIN 旗标子查询物化一次，WHERE 与
// 排序两处复用 cm 旗标；matched_comment_content 标量子查询走 issue 侧
// btree（每行约 0.5ms），保持原形态。
func TestBuildSearchQuery_CommentMatchSingleMaterialization(t *testing.T) {
	phraseExists := "EXISTS (SELECT 1 FROM comment c WHERE c.issue_id = i.id AND c.workspace_id = $4 AND LOWER(c.content) LIKE $2)"

	singleQuery, _ := buildSearchQuery("html", []string{"html"}, 0, false, false, []string{"done", "cancelled"})

	joinCount := strings.Count(singleQuery, "LEFT JOIN (SELECT DISTINCT c.issue_id FROM comment c")
	if joinCount != 1 {
		t.Fatalf("single-term query should materialize the comment phrase match exactly once via one flag join, got %d:\n%s", joinCount, singleQuery)
	}
	if strings.Contains(singleQuery, phraseExists) {
		t.Errorf("single-term query still materializes the phrase EXISTS separately (double hashed SubPlan):\n%s", singleQuery)
	}
	// 单词查询只应有两处 comment 引用：旗标 join + matched_comment_content 标量子查询。
	if from := strings.Count(singleQuery, "FROM comment c"); from != 2 {
		t.Errorf("single-term query should touch comment exactly twice (flag join + content subquery), got %d", from)
	}
	orderBy := orderByClause(t, singleQuery)
	if !strings.Contains(orderBy, "WHEN cm.issue_id IS NOT NULL THEN 7") {
		t.Errorf("rank tier 7 should reuse the joined flag instead of a second EXISTS:\n%s", orderBy)
	}
	if !strings.Contains(singleQuery, "LOWER(COALESCE(i.description, '')) LIKE $2 OR cm.issue_id IS NOT NULL") {
		t.Errorf("WHERE phrase match should reuse the joined flag:\n%s", singleQuery)
	}

	multiQuery, _ := buildSearchQuery("foo bar", []string{"foo", "bar"}, 0, false, false, []string{"done", "cancelled"})
	if strings.Contains(multiQuery, phraseExists) {
		t.Errorf("multi-term query still materializes the phrase EXISTS separately:\n%s", multiQuery)
	}
	// 多词的 per-term 与 Tier 8 EXISTS 语义保持不变（各自单次物化，非本单目标）。
	if !strings.Contains(multiQuery, "EXISTS (SELECT 1 FROM comment c WHERE c.issue_id = i.id AND c.workspace_id = $4 AND LOWER(c.content) LIKE $5)") {
		t.Errorf("multi-term per-term comment EXISTS should be preserved:\n%s", multiQuery)
	}
	if !strings.Contains(orderByClause(t, multiQuery), "THEN 8") {
		t.Error("multi-term query should keep tier 8 (all terms in one comment)")
	}
}

// 多工作区隔离（MUL-4059 契约的 join 形态表述）：旗标子查询必须内嵌与外层
// 相同的 workspace 绑定（$4，由调用方写入 args[3]），保证 A 工作区评论命中
// 不可能落入 B 工作区搜索结果，且物化集合不会膨胀为跨工作区全表。
func TestBuildSearchQuery_CommentFlagJoinWorkspaceIsolation(t *testing.T) {
	cases := []struct {
		name  string
		query string
	}{
		{"single-term", buildSearchQueryForTest(t, "html", []string{"html"}, 0, false, false)},
		{"multi-term", buildSearchQueryForTest(t, "foo bar", []string{"foo", "bar"}, 0, false, false)},
	}
	for _, tc := range cases {
		if !strings.Contains(tc.query, "WHERE i.workspace_id = $4 AND") {
			t.Errorf("%s: outer issue scan must bind the caller workspace param $4", tc.name)
		}
		joinStart := strings.Index(tc.query, "LEFT JOIN (SELECT DISTINCT c.issue_id FROM comment c")
		if joinStart == -1 {
			t.Fatalf("%s: comment flag join missing", tc.name)
		}
		joinEnd := strings.Index(tc.query[joinStart:], ") cm ON cm.issue_id = i.id")
		if joinEnd == -1 {
			t.Fatalf("%s: cannot locate flag join subquery boundary", tc.name)
		}
		joinSub := tc.query[joinStart : joinStart+joinEnd]
		if !strings.Contains(joinSub, "c.workspace_id = $4") {
			t.Errorf("%s: comment flag join is not workspace-scoped (MUL-4059 regression):\n%s", tc.name, joinSub)
		}
	}
}

// TestSearchIssues_CommentMatchIsWorkspaceScoped 是多工作区隔离的真实 DB 断言
// （MUL-4059 契约在旗标 join 形态下的行为锁定）：A 工作区里仅评论命中的 issue
// 必须出现在 A 的搜索结果且 match_source=comment；同一关键词在 B 工作区不得
// 命中 A 的 issue——评论匹配严格限本工作区。
func TestSearchIssues_CommentMatchIsWorkspaceScoped(t *testing.T) {
	ctx := context.Background()
	token := fmt.Sprintf("ruyi753isolation%x", time.Now().UnixNano())

	// 工作区 B：与套件工作区 A 相互独立。
	var wsB string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workspace (name, slug, description, issue_prefix)
		VALUES ('RUYI-753 Isolation B', $1, '', 'ISO')
		RETURNING id
	`, "iso-753-"+handlerFixtureSuffix).Scan(&wsB); err != nil {
		t.Fatalf("create workspace B: %v", err)
	}
	t.Cleanup(func() { _, _ = testPool.Exec(ctx, `DELETE FROM workspace WHERE id = $1`, wsB) })

	if _, err := testPool.Exec(ctx, `
		INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'owner')
	`, wsB, testUserID); err != nil {
		t.Fatalf("add member to workspace B: %v", err)
	}

	// seedIssueWithComment 在指定工作区建 issue（原子递增该工作区的计数器），
	// comment 非空时附带一条评论，返回 issue id。
	var seedIssueWithComment func(wsID, title, comment string) string
	seedIssueWithComment = func(wsID, title, comment string) string {
		t.Helper()
		var number int
		if err := testPool.QueryRow(ctx, `
			UPDATE workspace
			SET issue_counter = GREATEST(issue_counter, (SELECT COALESCE(MAX(number), 0) FROM issue WHERE workspace_id = $1)) + 1
			WHERE id = $1 RETURNING issue_counter
		`, wsID).Scan(&number); err != nil {
			t.Fatalf("next issue number in %s: %v", wsID, err)
		}
		var issueID string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO issue (workspace_id, title, status, priority, creator_type, creator_id, position, number, created_at, updated_at)
			VALUES ($1, $2, 'todo', 'none', 'member', $3, 0, $4, now(), now())
			RETURNING id
		`, wsID, title, testUserID, number).Scan(&issueID); err != nil {
			t.Fatalf("create issue %q: %v", title, err)
		}
		t.Cleanup(func() { _, _ = testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, issueID) })
		if comment != "" {
			var commentID string
			if err := testPool.QueryRow(ctx, `
				INSERT INTO comment (workspace_id, issue_id, author_type, author_id, content)
				VALUES ($1, $2, 'member', $3, $4)
				RETURNING id
			`, wsID, issueID, testUserID, comment).Scan(&commentID); err != nil {
				t.Fatalf("create comment on %q: %v", title, err)
			}
			t.Cleanup(func() { _, _ = testPool.Exec(ctx, `DELETE FROM comment WHERE id = $1`, commentID) })
		}
		return issueID
	}

	// A 工作区：标题不含 token，命中只能来自评论。
	issueA := seedIssueWithComment(testWorkspaceID, "isolation anchor alpha", "quoted text "+token+" in comment body")
	// B 工作区：同构 issue，评论不含 token。
	_ = seedIssueWithComment(wsB, "isolation anchor beta", "unrelated remark")

	searchIn := func(wsID, q string) []SearchIssueResponse {
		t.Helper()
		path := fmt.Sprintf(
			"/api/issues/search?workspace_id=%s&q=%s&limit=50&include_closed=true",
			wsID, url.QueryEscape(q),
		)
		req := newRequest("GET", path, nil)
		// newRequest 默认注入套件工作区头，其优先级高于 query 参数；这里显式
		// 覆盖为被测工作区，保证搜索真正落在 wsID 上。
		req.Header.Set("X-Workspace-ID", wsID)
		w := httptest.NewRecorder()
		testHandler.SearchIssues(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("SearchIssues(ws=%s, %q): expected 200, got %d: %s", wsID, q, w.Code, w.Body.String())
		}
		var response struct {
			Issues []SearchIssueResponse `json:"issues"`
		}
		if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
			t.Fatalf("decode search response: %v", err)
		}
		return response.Issues
	}

	// A 工作区命中：来源必须是评论，且带回评论内容。
	inA := searchIn(testWorkspaceID, token)
	if len(inA) != 1 || inA[0].ID != issueA {
		t.Fatalf("workspace A search(%q) = %d rows, want exactly the comment-matched issue %s", token, len(inA), issueA)
	}
	if inA[0].MatchSource != "comment" {
		t.Fatalf("workspace A match_source = %q, want %q", inA[0].MatchSource, "comment")
	}
	if inA[0].MatchedCommentSnippet == nil || !strings.Contains(*inA[0].MatchedCommentSnippet, token) {
		t.Fatalf("workspace A matched_comment_snippet = %v, want it to contain the token", inA[0].MatchedCommentSnippet)
	}

	// B 工作区：A 的评论命中不得跨工作区出现。
	inB := searchIn(wsB, token)
	if len(inB) != 0 {
		t.Fatalf("workspace B search(%q) returned %d rows, want 0 — comment match leaked across workspaces: %v", token, len(inB), inB)
	}
}
