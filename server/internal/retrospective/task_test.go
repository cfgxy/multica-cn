package retrospective

// Tests for the RUYI-674 membership-filter semantics of the completion
// processor: the agent's report is no longer rejected wholesale when it
// names an issue outside the scanned set. Ids are attributed instead —
// a unique single-edit neighbor is auto-corrected (the f15f→f15a class of
// copy typos), ids naming a real issue of the workspace keep their drafts
// (no watermark), and only unattributable ids are dropped — each with a
// breadcrumb in the run detail. Nothing on this path fails the run; hard
// failures stay parse/runtime errors.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// TestEditDistanceAtMost1 locks the single-edit predicate the auto-
// correction is built on: equal, one substitution/insertion/deletion pass,
// everything farther fails.
func TestEditDistanceAtMost1(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"", "", true},
		{"abc", "abc", true},
		{"f15f", "f15a", true}, // the RUYI-674 typo class: one hex swap
		{"abc", "abxc", true},  // one insertion
		{"abxc", "abc", true},  // one deletion
		{"abc", "ab", true},    // tail deletion
		{"", "x", true},        // one insertion into empty
		{"01a11666-f15f", "01a11666-f15", true},
		{"abc", "axyz", false},   // two edits
		{"abc", "axcy", false},   // insertion + substitution
		{"abc", "abcdef", false}, // length gap 3
		{"abcdef", "ab", false},  // length gap 4
	}
	for _, tc := range cases {
		if got := editDistanceAtMost1(tc.a, tc.b); got != tc.want {
			t.Fatalf("editDistanceAtMost1(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

// TestResolveIssueIDAgainstScanned: exact hits resolve as-is, a unique
// single-edit neighbor is auto-corrected, and two equidistant candidates or
// no neighbor at all leave the id unresolved.
func TestResolveIssueIDAgainstScanned(t *testing.T) {
	idA := "7a111111-1111-4111-8111-11111111111a"
	idB := "7a111111-1111-4111-8111-11111111111b"
	idC := "9f222222-2222-4222-8222-222222222222"
	scanned := []string{idA, idB, idC}

	if resolved, corrected, ok := resolveIssueID(idC, scanned); !ok || corrected || resolved != idC {
		t.Fatalf("exact hit must resolve as-is, got (%q, %v, %v)", resolved, corrected, ok)
	}
	typoC := "9f222222-2222-4222-8222-222222222220" // one hex off idC
	if resolved, corrected, ok := resolveIssueID(typoC, scanned); !ok || !corrected || resolved != idC {
		t.Fatalf("unique neighbor must be corrected onto idC, got (%q, %v, %v)", resolved, corrected, ok)
	}
	equidistant := "7a111111-1111-4111-8111-11111111111c" // one hex off both idA and idB
	if _, corrected, ok := resolveIssueID(equidistant, scanned); ok || corrected {
		t.Fatalf("two equidistant candidates must stay unresolved, got corrected=%v ok=%v", corrected, ok)
	}
	far := "00000000-0000-4000-8000-000000000000"
	if _, corrected, ok := resolveIssueID(far, scanned); ok || corrected {
		t.Fatalf("a far-off id must stay unresolved, got corrected=%v ok=%v", corrected, ok)
	}
}

// TestProcessTaskTerminalCorrectsSingleCharTypo reproduces the RUYI-674
// incident shape: the agent copies one issue id with a single hex char off
// (f15f→f15a). The report is accepted, both issues land with watermarks,
// the draft lands under the corrected id, the run succeeds, and the run
// detail carries the correction breadcrumb.
func TestProcessTaskTerminalCorrectsSingleCharTypo(t *testing.T) {
	wsID, _ := retroFixture(t)
	issueA := retroIssue(t, wsID, "发布脚本修复", "根因是路径拼接")
	issueB := retroIssue(t, wsID, "查询优化", "")
	runner, calls := newTestRunner(t)
	if _, err := runner.RunWorkspace(context.Background(), wsID, "manual"); err != nil {
		t.Fatalf("RunWorkspace: %v", err)
	}

	typoA := typoVariant(issueA)
	report := reportWithDrafts([]string{typoA, issueB}, []string{typoA})
	task := db.AgentTaskQueue{
		ID:      util.MustParseUUID(stubTaskID),
		Context: (*calls)[0].params.Context,
	}
	out, err := runner.ProcessTaskTerminal(context.Background(), task, report, "")
	if err != nil {
		t.Fatalf("ProcessTaskTerminal: %v", err)
	}
	if out.IDsCorrected != 1 || out.IDsIgnored != 0 || out.IssuesAnalyzed != 2 || out.ProposalsCreated != 1 {
		t.Fatalf("stats wrong: %+v", out)
	}
	status, errMsg, _ := runRecord(t, wsID)
	if status != "succeeded" || errMsg != "" {
		t.Fatalf("corrected typo must succeed, got %q / %q", status, errMsg)
	}
	if !watermarkExists(t, wsID, issueA) || !watermarkExists(t, wsID, issueB) {
		t.Fatalf("both scanned issues must be watermarked (typo attributed to %s)", issueA)
	}
	if watermarkExists(t, wsID, typoA) {
		t.Fatalf("the typo id must not be watermarked as its own issue")
	}
	detail := runDetailOf(t, wsID)
	if len(detail.CorrectedIssueIDs) != 1 || detail.CorrectedIssueIDs[0].From != typoA || detail.CorrectedIssueIDs[0].To != issueA {
		t.Fatalf("run detail must carry the correction breadcrumb, got %+v", detail.CorrectedIssueIDs)
	}
	if len(detail.IgnoredIssueIDs) != 0 {
		t.Fatalf("nothing should be ignored, got %+v", detail.IgnoredIssueIDs)
	}
	if got := detail.AnalyzedIssueIDs; len(got) != 2 || got[0] != issueA || got[1] != issueB {
		t.Fatalf("analyzed ids must be recorded corrected, got %+v", got)
	}
}

// TestProcessTaskTerminalIgnoresUnattributableIDs: ids nothing can be
// attributed to — a nonexistent UUID and a non-UUID string alike — are
// dropped with their drafts while every attributable result still lands,
// and the run succeeds with the ignore breadcrumb.
func TestProcessTaskTerminalIgnoresUnattributableIDs(t *testing.T) {
	wsID, _ := retroFixture(t)
	issueA := retroIssue(t, wsID, "窗口内修复", "")
	runner, calls := newTestRunner(t)
	if _, err := runner.RunWorkspace(context.Background(), wsID, "manual"); err != nil {
		t.Fatalf("RunWorkspace: %v", err)
	}

	phantom := "ed6a7a20-0000-4000-8000-000000000067" // valid UUID shape, no such issue
	illegal := "RUYI-526"                             // not even a UUID shape
	_, _, proposalsBefore := counts(t, wsID)

	report := reportWithDrafts([]string{issueA, phantom, illegal}, []string{issueA, phantom, illegal})
	task := db.AgentTaskQueue{
		ID:      util.MustParseUUID(stubTaskID),
		Context: (*calls)[0].params.Context,
	}
	out, err := runner.ProcessTaskTerminal(context.Background(), task, report, "")
	if err != nil {
		t.Fatalf("ProcessTaskTerminal: %v", err)
	}
	if out.IssuesAnalyzed != 1 || out.ProposalsCreated != 1 || out.IDsIgnored != 2 || out.IDsCorrected != 0 {
		t.Fatalf("stats wrong: %+v", out)
	}
	status, _, _ := runRecord(t, wsID)
	if status != "succeeded" {
		t.Fatalf("unattributable ids must not fail the run, got %q", status)
	}
	_, _, proposalsAfter := counts(t, wsID)
	if proposalsAfter != proposalsBefore+1 {
		t.Fatalf("only the attributable draft may land: %d→%d", proposalsBefore, proposalsAfter)
	}
	if watermarkExists(t, wsID, phantom) {
		t.Fatalf("a phantom id must not be watermarked")
	}
	detail := runDetailOf(t, wsID)
	if len(detail.IgnoredIssueIDs) != 2 || detail.IgnoredIssueIDs[0] != phantom || detail.IgnoredIssueIDs[1] != illegal {
		t.Fatalf("run detail must carry both ignored ids, got %+v", detail.IgnoredIssueIDs)
	}
	if detail.Reason != nil {
		t.Fatalf("a succeeded run must carry no reason, got %+v", detail.Reason)
	}
}

// TestProcessTaskTerminalForeignRealIssueGetsDraftsNoWatermark: an id
// outside the scanned set that names a real issue of this workspace keeps
// its drafts (anchors carry the reported id) but never gets a watermark —
// the next pass may rescan it, and dedup absorbs the overlap.
func TestProcessTaskTerminalForeignRealIssueGetsDraftsNoWatermark(t *testing.T) {
	wsID, _ := retroFixture(t)
	issueA := retroIssue(t, wsID, "窗口内修复", "")
	foreign := retroInReviewIssue(t, wsID, "评审中的窗口外 Issue") // real, but not scanned (include_in_review off)
	runner, calls := newTestRunner(t)
	if _, err := runner.RunWorkspace(context.Background(), wsID, "manual"); err != nil {
		t.Fatalf("RunWorkspace: %v", err)
	}

	report := reportWithDrafts([]string{issueA, foreign}, []string{issueA, foreign})
	task := db.AgentTaskQueue{
		ID:      util.MustParseUUID(stubTaskID),
		Context: (*calls)[0].params.Context,
	}
	out, err := runner.ProcessTaskTerminal(context.Background(), task, report, "")
	if err != nil {
		t.Fatalf("ProcessTaskTerminal: %v", err)
	}
	if out.IssuesAnalyzed != 2 || out.ProposalsCreated != 2 || out.IDsIgnored != 0 {
		t.Fatalf("stats wrong: %+v", out)
	}
	status, _, _ := runRecord(t, wsID)
	if status != "succeeded" {
		t.Fatalf("run must succeed with partial out-of-scope results, got %q", status)
	}
	if watermarkExists(t, wsID, foreign) {
		t.Fatalf("a foreign real issue must not be watermarked")
	}
	if !watermarkExists(t, wsID, issueA) {
		t.Fatalf("the scanned issue must be watermarked")
	}
	var anchors string
	if err := testPool.QueryRow(context.Background(),
		`SELECT string_agg(evidence_anchors::text, '') FROM prompt_proposal WHERE workspace_id = $1`,
		wsID).Scan(&anchors); err != nil {
		t.Fatalf("read anchors: %v", err)
	}
	if !strings.Contains(anchors, foreign) {
		t.Fatalf("the foreign draft's evidence anchor must carry the reported issue id, got %s", anchors)
	}
	detail := runDetailOf(t, wsID)
	if len(detail.AcceptedOutOfScopeIDs) != 1 || detail.AcceptedOutOfScopeIDs[0] != foreign {
		t.Fatalf("run detail must record the accepted out-of-scope id, got %+v", detail.AcceptedOutOfScopeIDs)
	}
}

// TestProcessTaskTerminalAmbiguousCorrectionStaysUnattributable: when two
// scanned ids are equidistant from the reported one, no coin-flip
// attribution happens — the id falls through to the unattributable path.
func TestProcessTaskTerminalAmbiguousCorrectionStaysUnattributable(t *testing.T) {
	wsID, _ := retroFixture(t)
	idA := "7a111111-1111-4111-8111-11111111111a"
	idB := "7a111111-1111-4111-8111-11111111111b"
	idY := "7a111111-1111-4111-8111-11111111111c" // one edit off both
	retroIssueWithID(t, wsID, idA, "等距候选甲")
	retroIssueWithID(t, wsID, idB, "等距候选乙")
	runner, calls := newTestRunner(t)
	if _, err := runner.RunWorkspace(context.Background(), wsID, "manual"); err != nil {
		t.Fatalf("RunWorkspace: %v", err)
	}

	report := reportWithDrafts([]string{idY}, []string{idY})
	task := db.AgentTaskQueue{
		ID:      util.MustParseUUID(stubTaskID),
		Context: (*calls)[0].params.Context,
	}
	out, err := runner.ProcessTaskTerminal(context.Background(), task, report, "")
	if err != nil {
		t.Fatalf("ProcessTaskTerminal: %v", err)
	}
	if out.IDsCorrected != 0 || out.IDsIgnored != 1 || out.IssuesAnalyzed != 0 || out.ProposalsCreated != 0 {
		t.Fatalf("stats wrong: %+v", out)
	}
	status, _, _ := runRecord(t, wsID)
	if status != "succeeded" {
		t.Fatalf("an ambiguous id is a breadcrumb, not a failure, got %q", status)
	}
	detail := runDetailOf(t, wsID)
	if len(detail.CorrectedIssueIDs) != 0 {
		t.Fatalf("ambiguous id must not be corrected, got %+v", detail.CorrectedIssueIDs)
	}
	if len(detail.IgnoredIssueIDs) != 1 || detail.IgnoredIssueIDs[0] != idY {
		t.Fatalf("ambiguous id must land in ignored, got %+v", detail.IgnoredIssueIDs)
	}
}

// TestProcessTaskTerminalParseFailureStillHardFails: garbage output stays a
// hard failure under the new semantics — the only completion-side failures
// left are parse/runtime errors, never membership.
func TestProcessTaskTerminalParseFailureStillHardFails(t *testing.T) {
	wsID, _ := retroFixture(t)
	_ = retroIssue(t, wsID, "解析失败路径", "")
	runner, calls := newTestRunner(t)
	if _, err := runner.RunWorkspace(context.Background(), wsID, "manual"); err != nil {
		t.Fatalf("RunWorkspace: %v", err)
	}

	_, _, proposalsBefore := counts(t, wsID)
	task := db.AgentTaskQueue{
		ID:      util.MustParseUUID(stubTaskID),
		Context: (*calls)[0].params.Context,
	}
	if _, err := runner.ProcessTaskTerminal(context.Background(), task, "这不是 JSON", ""); err != nil {
		t.Fatalf("ProcessTaskTerminal: %v", err)
	}
	status, _, _ := runRecord(t, wsID)
	if status != "failed" {
		t.Fatalf("parse failure must stay a hard failure, got %q", status)
	}
	if reason := runReasonOf(t, wsID); reason.Code != ReasonAgentReportInvalid || reason.IssueID != "" {
		t.Fatalf("want %s, got %+v", ReasonAgentReportInvalid, reason)
	}
	_, _, proposalsAfter := counts(t, wsID)
	if proposalsAfter != proposalsBefore {
		t.Fatalf("failed parse must not land drafts: %d→%d", proposalsBefore, proposalsAfter)
	}
}

// TestProcessTaskTerminalDraftOutsideAnalyzedStillLands: the output contract
// lets a draft reference a scanned issue the agent forgot to list under
// analyzed_issue_ids — the draft must land (and watermark its issue), not
// silently disappear.
func TestProcessTaskTerminalDraftOutsideAnalyzedStillLands(t *testing.T) {
	wsID, _ := retroFixture(t)
	issueA := retroIssue(t, wsID, "已列分析", "")
	issueB := retroIssue(t, wsID, "漏列分析", "")
	runner, calls := newTestRunner(t)
	if _, err := runner.RunWorkspace(context.Background(), wsID, "manual"); err != nil {
		t.Fatalf("RunWorkspace: %v", err)
	}

	report := reportWithDrafts([]string{issueA}, []string{issueA, issueB})
	task := db.AgentTaskQueue{
		ID:      util.MustParseUUID(stubTaskID),
		Context: (*calls)[0].params.Context,
	}
	out, err := runner.ProcessTaskTerminal(context.Background(), task, report, "")
	if err != nil {
		t.Fatalf("ProcessTaskTerminal: %v", err)
	}
	if out.IssuesAnalyzed != 2 || out.ProposalsCreated != 2 {
		t.Fatalf("stats wrong: %+v", out)
	}
	if !watermarkExists(t, wsID, issueB) {
		t.Fatalf("the under-reported issue's draft must land with its watermark")
	}
	status, _, _ := runRecord(t, wsID)
	if status != "succeeded" {
		t.Fatalf("run must succeed, got %q", status)
	}
}

// TestCorrectionBreadcrumbsAreIDsOnly locks the security shape of the new
// run-detail fields (RUYI-674): breadcrumbs carry issue ids and counts only
// — no report text, no evidence content.
func TestCorrectionBreadcrumbsAreIDsOnly(t *testing.T) {
	b, err := json.Marshal(runDetail{
		CorrectedIssueIDs:     []idCorrection{{From: "a", To: "b"}},
		IgnoredIssueIDs:       []string{"c"},
		AcceptedOutOfScopeIDs: []string{"d"},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	detail := string(b)
	for _, allowed := range []string{`"corrected_issue_ids"`, `"ignored_issue_ids"`, `"accepted_out_of_scope_ids"`, `"from"`, `"to"`} {
		if !strings.Contains(detail, allowed) {
			t.Fatalf("detail JSON missing %s: %s", allowed, detail)
		}
	}
	if strings.Contains(strings.TrimPrefix(detail, "{"), "clause") || strings.Contains(detail, "evidence") {
		t.Fatalf("breadcrumbs must stay id/count-shaped, got %s", detail)
	}
}

// --- helpers -------------------------------------------------------------

// typoVariant flips the id's last hex character to a different digit — the
// f15f→f15a class of single-character copy typos RUYI-674 auto-corrects.
func typoVariant(id string) string {
	last := id[len(id)-1]
	replacement := byte('0')
	if last == '0' {
		replacement = '1'
	}
	return id[:len(id)-1] + string(replacement)
}

// reportWithDrafts builds one agent final-message report: analyzed ids as
// given, plus one add_clause draft per draft id (clause names suffixed so
// multiple drafts never merge).
func reportWithDrafts(analyzed, draftIDs []string) string {
	drafts := make([]string, 0, len(draftIDs))
	for i, id := range draftIDs {
		drafts = append(drafts, fmt.Sprintf(
			`{"issue_id":%q,"carrier_scope":"workspace","target_section":"沟通规范","change_kind":"add_clause","clause_name":"条款%d","clause_text":"- **条款%d**：测试条款内容","gate_answers":{"layer":"workspace=跨项目协作机制","retention":"每轮输出都适用","cost":"少量常驻","conflict":"无同主题条款","dedup":"无重复"},"evidence_ref":"issue 标题","evidence_note":"依据说明"}`,
			id, i, i))
	}
	analyzedJSON, err := json.Marshal(analyzed)
	if err != nil {
		panic(err)
	}
	return fmt.Sprintf(`{"analyzed_issue_ids":%s,"drafts":[%s]}`, analyzedJSON, strings.Join(drafts, ","))
}

// retroIssueWithID inserts one done issue with an explicit id — for cases
// that must control the id's exact hex (equidistant-correction fixtures).
func retroIssueWithID(t *testing.T, wsID, id, title string) string {
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
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO issue (id, workspace_id, number, title, description, status, priority, creator_type, creator_id, position, updated_at)
		VALUES ($1, $2, (SELECT COALESCE(MAX(number), 0) + 1 FROM issue WHERE workspace_id = $2), $3, '', 'done', 'none', 'member', $4, 0, now())`,
		id, wsID, title, userID); err != nil {
		t.Fatalf("create issue %s: %v", id, err)
	}
	return id
}

// retroInReviewIssue inserts one in_review issue: real in this workspace,
// but outside the scanned set while the config keeps include_in_review off.
func retroInReviewIssue(t *testing.T, wsID, title string) string {
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
		VALUES ($1, (SELECT COALESCE(MAX(number), 0) + 1 FROM issue WHERE workspace_id = $1), $2, '', 'in_review', 'none', 'member', $3, 0, now())
		RETURNING id`, wsID, title, userID).Scan(&issueID); err != nil {
		t.Fatalf("create in_review issue: %v", err)
	}
	return issueID
}

// runDetailOf reads the latest run's full detail JSONB.
func runDetailOf(t *testing.T, wsID string) runDetail {
	t.Helper()
	_, errMsg, detail := runRecord(t, wsID)
	var d runDetail
	if err := json.Unmarshal([]byte(detail), &d); err != nil {
		t.Fatalf("parse run detail %s (error %q): %v", detail, errMsg, err)
	}
	return d
}

// watermarkExists reports whether the issue carries a retrospective
// watermark in this workspace.
func watermarkExists(t *testing.T, wsID, issueID string) bool {
	t.Helper()
	var n int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM retrospective_issue_watermark WHERE workspace_id = $1 AND issue_id = $2`,
		wsID, issueID).Scan(&n); err != nil {
		t.Fatalf("read watermark: %v", err)
	}
	return n > 0
}
