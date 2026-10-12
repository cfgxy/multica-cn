package handler

// RUYI-660 feature tests: the outer loop's behavior end to end over the real
// HTTP handlers and the real database — the M1 ticket-close pullback, the M2
// auto-continuation verdicts and waiting-human reply routing, the stall
// sweep, the metadata permission gate, and the flag-off status quo.
//
// Plan state lives in issues.metadata; every seed below writes the plan keys
// through the same JSONB column the production writes target.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/multica-ai/multica/server/internal/featureflags"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/pkg/featureflag"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/outerloop"
)

// staticOuterLoopFlags is a test feature-flag provider with one decision.
type staticOuterLoopFlags map[string]bool

func (f staticOuterLoopFlags) Lookup(_ context.Context, key string) (featureflag.Decision, bool) {
	on, ok := f[key]
	if !ok {
		return featureflag.Decision{}, false
	}
	return featureflag.Decision{Key: key, Enabled: on, Reason: featureflag.ReasonDefault}, true
}

func (f staticOuterLoopFlags) Name() string { return "outerloop-feature-test" }

// setOuterLoopFlagForTest swaps the suite handler's flag service for one with
// a single outer_loop decision, restoring the previous service afterwards.
func setOuterLoopFlagForTest(t *testing.T, on bool) {
	t.Helper()
	previous := testHandler.FeatureFlags
	testHandler.FeatureFlags = featureflag.NewService(staticOuterLoopFlags{featureflags.OuterLoop: on})
	t.Cleanup(func() { testHandler.FeatureFlags = previous })
}

// seedOuterLoopPlan inserts an agent (with an online runtime and a running
// acting task the X-Task-ID header can pin) and an in_progress issue assigned
// to that agent, carrying a running/waiting plan with the given counters and
// the agent pinned by UUID.
func seedOuterLoopPlan(t *testing.T, state string, round, maxRounds int) (issueID, agentID, actingTaskID string) {
	t.Helper()
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	rtID := dbfx.Runtime(t, "outerloop-feature-runtime")
	agentID = dbfx.Agent(t, "outerloop-feature-agent", rtID)
	// Active queue rows require a runtime (agent_task_queue_active_requires_runtime).
	actingTaskID = dbfx.Task(t, agentID, testutil.Cols{"status": "running", "runtime_id": rtID})
	metadata, err := json.Marshal(map[string]any{
		"outerloop_state":      state,
		"outerloop_agent":      agentID,
		"outerloop_round":      round,
		"outerloop_max_rounds": maxRounds,
		"outerloop_goal":       "完成 RUYI-660 验收",
	})
	if err != nil {
		t.Fatalf("marshal plan: %v", err)
	}
	issueID = dbfx.Issue(t, "RUYI-660 outer-loop feature issue", testutil.Cols{
		"status":        "in_progress",
		"assignee_type": "agent",
		"assignee_id":   agentID,
		"metadata":      testutil.Raw("'" + string(metadata) + "'::jsonb"),
	})
	return issueID, agentID, actingTaskID
}

// seedAgentTerminalTask inserts a terminal task for (issue, agent). completedAt
// is a raw SQL expression for completed_at ("" leaves the column unset).
func seedAgentTerminalTask(t *testing.T, issueID, agentID, status string, completedAt string) string {
	t.Helper()
	cols := testutil.Cols{"issue_id": issueID, "status": status}
	if completedAt != "" {
		cols["completed_at"] = testutil.Raw(completedAt)
	}
	return dbfx.Task(t, agentID, cols)
}

// outerLoopState reads the issue's plan state from metadata.
func outerLoopState(t *testing.T, issueID string) string {
	t.Helper()
	var state *string
	if err := testPool.QueryRow(context.Background(),
		`SELECT metadata->>'outerloop_state' FROM issue WHERE id = $1`, issueID,
	).Scan(&state); err != nil {
		t.Fatalf("read plan state: %v", err)
	}
	if state == nil {
		return ""
	}
	return *state
}

// outerLoopRound reads the issue's round counter from metadata.
func outerLoopRound(t *testing.T, issueID string) int {
	t.Helper()
	var round *string
	if err := testPool.QueryRow(context.Background(),
		`SELECT metadata->>'outerloop_round' FROM issue WHERE id = $1`, issueID,
	).Scan(&round); err != nil {
		t.Fatalf("read plan round: %v", err)
	}
	if round == nil {
		return 0
	}
	var n int
	if _, err := testPool.Exec(context.Background(), `SELECT $1::int`, *round); err != nil {
		t.Fatalf("round not an int: %v", err)
	}
	if err := json.Unmarshal([]byte(*round), &n); err != nil {
		t.Fatalf("decode round %q: %v", *round, err)
	}
	return n
}

// agentIssueRequest builds an issue request acting as the named agent through
// its running task (resolveActor's validated pair).
func agentIssueRequest(method, path string, body any, agentID, actingTaskID string) *http.Request {
	r := newRequest(method, path, body)
	r.Header.Set("X-Agent-ID", agentID)
	r.Header.Set("X-Task-ID", actingTaskID)
	return r
}

// queuedTaskCount counts queued tasks for (issue, agent).
func queuedTaskCount(t *testing.T, issueID, agentID string) int {
	t.Helper()
	return dbfx.Count(t,
		`SELECT count(*) FROM agent_task_queue WHERE issue_id = $1 AND agent_id = $2 AND status = 'queued'`,
		issueID, agentID)
}

// systemCommentCount counts system comments whose content matches substr.
func systemCommentCount(t *testing.T, issueID, substr string) int {
	t.Helper()
	return dbfx.Count(t,
		`SELECT count(*) FROM comment WHERE issue_id = $1 AND author_type = 'system' AND content LIKE '%' || $2 || '%'`,
		issueID, substr)
}

// ---------------------------------------------------------------------------
// M1: the early ticket-close pullback
// ---------------------------------------------------------------------------

// 验收 2: an agent closing the ticket without the end marker is pulled back —
// the status reverts and a system comment explains why.
func TestOuterLoopPullbackAgentCloseWithoutMarker(t *testing.T) {
	issueID, agentID, actingTaskID := seedOuterLoopPlan(t, "running", 1, 3)
	dbfx.Comment(t, issueID, "进度：实现推进中，还差测试", testutil.Cols{"author_type": "agent", "author_id": agentID})

	w := httptest.NewRecorder()
	r := agentIssueRequest("PATCH", "/api/issues/"+issueID, map[string]any{"status": "done"}, agentID, actingTaskID)
	r = withURLParam(r, "id", issueID)
	testHandler.UpdateIssue(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("agent close: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var status string
	if err := testPool.QueryRow(context.Background(),
		`SELECT status FROM issue WHERE id = $1`, issueID).Scan(&status); err != nil {
		t.Fatalf("reload issue: %v", err)
	}
	if status != "in_progress" {
		t.Errorf("status after pullback = %q, want in_progress (reverted)", status)
	}
	if got := outerLoopState(t, issueID); got != "running" {
		t.Errorf("plan state = %q, want running (pullback keeps the loop alive)", got)
	}
	if got := systemCommentCount(t, issueID, "外循环拉回"); got != 1 {
		t.Errorf("system pullback comments = %d, want 1", got)
	}
}

// A member closing the ticket is an intentional stop: the plan retires and
// the closure stands.
func TestOuterLoopMemberCloseStopsPlanSilently(t *testing.T) {
	issueID, agentID, _ := seedOuterLoopPlan(t, "running", 1, 3)
	dbfx.Comment(t, issueID, "还没写完", testutil.Cols{"author_type": "agent", "author_id": agentID})

	w := httptest.NewRecorder()
	r := newRequest("PATCH", "/api/issues/"+issueID, map[string]any{"status": "done"})
	r = withURLParam(r, "id", issueID)
	testHandler.UpdateIssue(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("member close: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var status string
	if err := testPool.QueryRow(context.Background(),
		`SELECT status FROM issue WHERE id = $1`, issueID).Scan(&status); err != nil {
		t.Fatalf("reload issue: %v", err)
	}
	if status != "done" {
		t.Errorf("status = %q, want done (member closure is never reverted)", status)
	}
	if got := outerLoopState(t, issueID); got != "stopped" {
		t.Errorf("plan state = %q, want stopped", got)
	}
	if got := systemCommentCount(t, issueID, "外循环拉回"); got != 0 {
		t.Errorf("pullback comments = %d, want 0 (member stop is silent)", got)
	}
}

// An agent closing WITH the end marker in its latest report is a legitimate
// stage-end close: the closure stands and the plan retires.
func TestOuterLoopAgentCloseWithMarkerStopsPlan(t *testing.T) {
	issueID, agentID, actingTaskID := seedOuterLoopPlan(t, "running", 2, 3)
	dbfx.Comment(t, issueID, "全部落地，自测通过。\n\n阶段状态：STAGE_COMPLETE", testutil.Cols{"author_type": "agent", "author_id": agentID})

	w := httptest.NewRecorder()
	r := agentIssueRequest("PATCH", "/api/issues/"+issueID, map[string]any{"status": "done"}, agentID, actingTaskID)
	r = withURLParam(r, "id", issueID)
	testHandler.UpdateIssue(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("agent marker close: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var status string
	if err := testPool.QueryRow(context.Background(),
		`SELECT status FROM issue WHERE id = $1`, issueID).Scan(&status); err != nil {
		t.Fatalf("reload issue: %v", err)
	}
	if status != "done" {
		t.Errorf("status = %q, want done (marker close stands)", status)
	}
	if got := outerLoopState(t, issueID); got != "stopped" {
		t.Errorf("plan state = %q, want stopped", got)
	}
	if got := systemCommentCount(t, issueID, "外循环拉回"); got != 0 {
		t.Errorf("pullback comments = %d, want 0", got)
	}
}

// ---------------------------------------------------------------------------
// M2: the auto-continuation verdicts
// ---------------------------------------------------------------------------

// 验收 3: a completed round without the marker enqueues the next round with
// the handoff document and advances the counter.
func TestOuterLoopCompletedWithoutMarkerContinues(t *testing.T) {
	ctx := context.Background()
	issueID, agentID, _ := seedOuterLoopPlan(t, "running", 1, 3)
	dbfx.Comment(t, issueID, "第一轮做了一半，还差收尾", testutil.Cols{"author_type": "agent", "author_id": agentID})
	taskID := seedAgentTerminalTask(t, issueID, agentID, "completed", "now()")

	queries := db.New(testPool)
	task, err := queries.GetAgentTask(ctx, mustParseUUID(t, taskID))
	if err != nil {
		t.Fatalf("load terminal task: %v", err)
	}
	testHandler.TaskService.OnOuterLoopTaskTerminal(ctx, task, "completed", "")

	if got := queuedTaskCount(t, issueID, agentID); got != 1 {
		t.Errorf("queued continuation tasks = %d, want 1", got)
	}
	if got := outerLoopRound(t, issueID); got != 2 {
		t.Errorf("round = %d, want 2", got)
	}
	if got := outerLoopState(t, issueID); got != "running" {
		t.Errorf("plan state = %q, want running", got)
	}
	if got := dbfx.Count(t,
		`SELECT count(*) FROM agent_task_queue WHERE issue_id = $1 AND agent_id = $2 AND handoff_note LIKE '%外循环续跑%'`,
		issueID, agentID); got != 1 {
		t.Errorf("handoff-carrying tasks = %d, want 1", got)
	}
}

// Reaching the cap soft-stops: no next round, plan stopped, goal and max
// preserved, system comment announces the stop.
func TestOuterLoopNextBeyondMaxSoftStops(t *testing.T) {
	ctx := context.Background()
	issueID, agentID, _ := seedOuterLoopPlan(t, "running", 3, 3)
	dbfx.Comment(t, issueID, "第三轮仍未收敛", testutil.Cols{"author_type": "agent", "author_id": agentID})
	taskID := seedAgentTerminalTask(t, issueID, agentID, "completed", "now()")

	queries := db.New(testPool)
	task, err := queries.GetAgentTask(ctx, mustParseUUID(t, taskID))
	if err != nil {
		t.Fatalf("load terminal task: %v", err)
	}
	testHandler.TaskService.OnOuterLoopTaskTerminal(ctx, task, "completed", "")

	if got := queuedTaskCount(t, issueID, agentID); got != 0 {
		t.Errorf("queued tasks after soft stop = %d, want 0", got)
	}
	if got := outerLoopState(t, issueID); got != "stopped" {
		t.Errorf("plan state = %q, want stopped", got)
	}
	if got := outerLoopRound(t, issueID); got != 3 {
		t.Errorf("round = %d, want 3 (preserved)", got)
	}
	var maxRounds *string
	if err := testPool.QueryRow(context.Background(),
		`SELECT metadata->>'outerloop_max_rounds' FROM issue WHERE id = $1`, issueID).Scan(&maxRounds); err != nil {
		t.Fatalf("read max rounds: %v", err)
	}
	if maxRounds == nil || *maxRounds != "3" {
		t.Errorf("max rounds = %v, want 3 (preserved)", maxRounds)
	}
	if got := systemCommentCount(t, issueID, "最大轮次"); got != 1 {
		t.Errorf("soft-stop comments = %d, want 1", got)
	}
}

// The end marker in the report retires the plan without a next round.
func TestOuterLoopCompletedWithMarkerStopsSilently(t *testing.T) {
	ctx := context.Background()
	issueID, agentID, _ := seedOuterLoopPlan(t, "running", 1, 3)
	dbfx.Comment(t, issueID, "验收全部通过。\n\n阶段状态：STAGE_COMPLETE", testutil.Cols{"author_type": "agent", "author_id": agentID})
	taskID := seedAgentTerminalTask(t, issueID, agentID, "completed", "now()")

	queries := db.New(testPool)
	task, err := queries.GetAgentTask(ctx, mustParseUUID(t, taskID))
	if err != nil {
		t.Fatalf("load terminal task: %v", err)
	}
	testHandler.TaskService.OnOuterLoopTaskTerminal(ctx, task, "completed", "")

	if got := queuedTaskCount(t, issueID, agentID); got != 0 {
		t.Errorf("queued tasks after marker stop = %d, want 0", got)
	}
	if got := outerLoopState(t, issueID); got != "stopped" {
		t.Errorf("plan state = %q, want stopped", got)
	}
}

// 验收 4 (failure half): a non-pipeline failure parks the loop on a human.
func TestOuterLoopFailedNonPipelineWaitsHuman(t *testing.T) {
	ctx := context.Background()
	issueID, agentID, _ := seedOuterLoopPlan(t, "running", 1, 3)
	taskID := seedAgentTerminalTask(t, issueID, agentID, "failed", "now()")

	queries := db.New(testPool)
	task, err := queries.GetAgentTask(ctx, mustParseUUID(t, taskID))
	if err != nil {
		t.Fatalf("load failed task: %v", err)
	}
	testHandler.TaskService.OnOuterLoopTaskTerminal(ctx, task, "failed", "agent_error.runtime_crash")

	if got := outerLoopState(t, issueID); got != "waiting_human" {
		t.Errorf("plan state = %q, want waiting_human", got)
	}
	var reason *string
	if err := testPool.QueryRow(context.Background(),
		`SELECT metadata->>'outerloop_wait_reason' FROM issue WHERE id = $1`, issueID).Scan(&reason); err != nil {
		t.Fatalf("read wait reason: %v", err)
	}
	if reason == nil {
		t.Error("wait reason not recorded")
	}
	if got := systemCommentCount(t, issueID, "外循环暂停"); got != 1 {
		t.Errorf("wait-for-human comments = %d, want 1", got)
	}
	if got := queuedTaskCount(t, issueID, agentID); got != 0 {
		t.Errorf("queued tasks while waiting = %d, want 0", got)
	}
}

// A pipeline-class failure (transient provider trouble) leaves the loop
// running: the stall sweep, not a human, pulls it back.
func TestOuterLoopPipelineFailureLeavesLoopRunning(t *testing.T) {
	ctx := context.Background()
	issueID, agentID, _ := seedOuterLoopPlan(t, "running", 1, 3)
	taskID := seedAgentTerminalTask(t, issueID, agentID, "failed", "now()")

	queries := db.New(testPool)
	task, err := queries.GetAgentTask(ctx, mustParseUUID(t, taskID))
	if err != nil {
		t.Fatalf("load failed task: %v", err)
	}
	testHandler.TaskService.OnOuterLoopTaskTerminal(ctx, task, "failed", "provider_capacity_or_rate_limit")

	if got := outerLoopState(t, issueID); got != "running" {
		t.Errorf("plan state = %q, want running (pipeline class self-heals)", got)
	}
	if got := systemCommentCount(t, issueID, "外循环暂停"); got != 0 {
		t.Errorf("wait-for-human comments = %d, want 0", got)
	}
}

// 验收 4 (reply half): a member "stop" while waiting retires the plan.
func TestOuterLoopMemberStopReplyStopsPlan(t *testing.T) {
	issueID, agentID, _ := seedOuterLoopPlan(t, "waiting_human", 1, 3)

	w := httptest.NewRecorder()
	r := newRequest("POST", "/api/issues/"+issueID+"/comments", map[string]any{
		"content": "先停止，方案要重新评估",
		"type":    "comment",
	})
	r = withURLParam(r, "id", issueID)
	testHandler.CreateComment(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("member reply: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	if got := outerLoopState(t, issueID); got != "stopped" {
		t.Errorf("plan state = %q, want stopped", got)
	}
	_ = agentID // the comment's own trigger pass legitimately wakes the agent once
}

// Any other member reply while waiting resumes the loop with the reply as
// the trigger comment (Leader 确认口径 2: unmatched reply defaults to resume).
func TestOuterLoopMemberContinueReplyResumes(t *testing.T) {
	issueID, agentID, _ := seedOuterLoopPlan(t, "waiting_human", 1, 3)

	w := httptest.NewRecorder()
	r := newRequest("POST", "/api/issues/"+issueID+"/comments", map[string]any{
		"content": "补充信息：数据库密码已更新，继续推进",
		"type":    "comment",
	})
	r = withURLParam(r, "id", issueID)
	testHandler.CreateComment(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("member reply: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	if got := outerLoopState(t, issueID); got != "running" {
		t.Errorf("plan state = %q, want running (resumed)", got)
	}
	if got := queuedTaskCount(t, issueID, agentID); got != 1 {
		t.Errorf("queued resume tasks = %d, want 1", got)
	}
	var trigger *string
	if err := testPool.QueryRow(context.Background(),
		`SELECT trigger_comment_id::text FROM agent_task_queue
		 WHERE issue_id = $1 AND agent_id = $2 AND status = 'queued' LIMIT 1`,
		issueID, agentID).Scan(&trigger); err != nil {
		t.Fatalf("load resume task trigger: %v", err)
	}
	if trigger == nil || *trigger == "" {
		t.Error("resume task carries no trigger comment")
	}
}

// The stall sweep pulls back a stalled running plan: no active task, agent
// owns the latest comment, and the last terminal task is past the grace
// window.
func TestOuterLoopSweepResumesStalledPlan(t *testing.T) {
	ctx := context.Background()
	issueID, agentID, _ := seedOuterLoopPlan(t, "running", 1, 3)
	dbfx.Comment(t, issueID, "上一轮结束后没有后续", testutil.Cols{"author_type": "agent", "author_id": agentID})
	seedAgentTerminalTask(t, issueID, agentID, "completed", "now() - interval '30 minutes'")

	res, err := testHandler.TaskService.OuterLoopSweepOnce(ctx)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if res.Resumed == 0 {
		t.Errorf("sweep resumed = %d, want >= 1 (result: %+v)", res.Resumed, res)
	}
	if got := queuedTaskCount(t, issueID, agentID); got != 1 {
		t.Errorf("queued tasks after sweep = %d, want 1", got)
	}
	if got := outerLoopRound(t, issueID); got != 2 {
		t.Errorf("round after sweep = %d, want 2", got)
	}
}

// ---------------------------------------------------------------------------
// 验收 5: the metadata permission gate
// ---------------------------------------------------------------------------

func TestOuterLoopMetadataGate(t *testing.T) {
	issueID, agentID, actingTaskID := seedOuterLoopPlan(t, "running", 1, 3)

	putMeta := func(agent bool, key string, value any) int {
		w := httptest.NewRecorder()
		r := newRequest("PUT", "/api/issues/"+issueID+"/metadata/"+key, map[string]any{"value": value})
		if agent {
			r = agentIssueRequest("PUT", "/api/issues/"+issueID+"/metadata/"+key, map[string]any{"value": value}, agentID, actingTaskID)
		}
		r = withURLParams(r, "id", issueID, "key", key)
		testHandler.SetIssueMetadataKey(w, r)
		return w.Code
	}

	// Agents hold no control plane.
	if got := putMeta(true, "outerloop_state", "stopped"); got != http.StatusForbidden {
		t.Errorf("agent outerloop_state write = %d, want 403", got)
	}
	if got := putMeta(true, "outerloop_max_rounds", 50); got != http.StatusForbidden {
		t.Errorf("agent outerloop_max_rounds write = %d, want 403", got)
	}
	// Server-only keys reject everyone.
	if got := putMeta(true, "outerloop_round", 9); got != http.StatusForbidden {
		t.Errorf("agent outerloop_round write = %d, want 403", got)
	}
	if got := putMeta(false, "outerloop_wait_reason", "nope"); got != http.StatusForbidden {
		t.Errorf("member outerloop_wait_reason write = %d, want 403", got)
	}
	// The brief is the agent's writable key.
	if got := putMeta(true, "outerloop_goal", "改为完成验收二"); got != http.StatusOK {
		t.Errorf("agent outerloop_goal write = %d, want 200", got)
	}
	// Members own the control plane, with validation.
	if got := putMeta(false, "outerloop_state", "stopped"); got != http.StatusOK {
		t.Errorf("member outerloop_state write = %d, want 200", got)
	}
	if got := putMeta(false, "outerloop_state", "bogus"); got != http.StatusBadRequest {
		t.Errorf("member invalid outerloop_state write = %d, want 400", got)
	}
	if got := putMeta(false, "outerloop_max_rounds", 50); got != http.StatusOK {
		t.Errorf("member outerloop_max_rounds write = %d, want 200", got)
	}
	if got := putMeta(false, "outerloop_max_rounds", 3); got != http.StatusBadRequest {
		t.Errorf("member max-rounds lowering write = %d, want 400 (monotonic up)", got)
	}
	if got := putMeta(false, "outerloop_max_rounds", 2000); got != http.StatusBadRequest {
		t.Errorf("member max-rounds over cap = %d, want 400", got)
	}
	// Agents may not delete plan keys either.
	w := httptest.NewRecorder()
	r := agentIssueRequest("DELETE", "/api/issues/"+issueID+"/metadata/outerloop_goal", nil, agentID, actingTaskID)
	r = withURLParams(r, "id", issueID, "key", "outerloop_goal")
	testHandler.DeleteIssueMetadataKey(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("agent outerloop_goal delete = %d, want 403", w.Code)
	}
}

// 验收 6: with the flag off the gate is inert and the loop never acts —
// plan-less behavior is the status quo.
func TestOuterLoopFlagOffKeepsStatusQuo(t *testing.T) {
	setOuterLoopFlagForTest(t, false)
	issueID, agentID, actingTaskID := seedOuterLoopPlan(t, "running", 1, 3)
	dbfx.Comment(t, issueID, "还没做完", testutil.Cols{"author_type": "agent", "author_id": agentID})

	// The gate no longer polices plan keys: an agent may write the state key.
	w := httptest.NewRecorder()
	r := agentIssueRequest("PUT", "/api/issues/"+issueID+"/metadata/outerloop_state", map[string]any{"value": "stopped"}, agentID, actingTaskID)
	r = withURLParams(r, "id", issueID, "key", "outerloop_state")
	testHandler.SetIssueMetadataKey(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("flag-off agent outerloop_state write = %d, want 200 (gate inert)", w.Code)
	}

	// Restore the plan to running, then close without a marker: no pullback.
	dbfx.Exec(t, `UPDATE issue SET metadata = jsonb_set(metadata, '{outerloop_state}', '"running"') WHERE id = $1`, issueID)
	w = httptest.NewRecorder()
	r = agentIssueRequest("PATCH", "/api/issues/"+issueID, map[string]any{"status": "done"}, agentID, actingTaskID)
	r = withURLParam(r, "id", issueID)
	testHandler.UpdateIssue(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("flag-off agent close: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var status string
	if err := testPool.QueryRow(context.Background(),
		`SELECT status FROM issue WHERE id = $1`, issueID).Scan(&status); err != nil {
		t.Fatalf("reload issue: %v", err)
	}
	if status != "done" {
		t.Errorf("flag-off close status = %q, want done (no pullback while disabled)", status)
	}
	if got := systemCommentCount(t, issueID, "外循环拉回"); got != 0 {
		t.Errorf("flag-off pullback comments = %d, want 0", got)
	}
}

// ---------------------------------------------------------------------------
// M1 fallback (Leader note): a pullback that cannot land announces the state
// and leaves the loop running for the sweep.
// ---------------------------------------------------------------------------

// The guarded revert only fires when the row still sits in the closed state
// the pullback observed. When the guard misses (a member edit won the race),
// the fallback must fail soft: the standing status is untouched, the failure
// is announced in a system comment, and the plan stays running for the sweep.
func TestOuterLoopPullbackFallbackFailsSoftWhenGuardMisses(t *testing.T) {
	ctx := context.Background()
	issueID, agentID, _ := seedOuterLoopPlan(t, "running", 1, 3)
	dbfx.Comment(t, issueID, "进行中", testutil.Cols{"author_type": "agent", "author_id": agentID})

	// The row never entered "done": the guard (expected_current_status=done)
	// will miss exactly like it would after a winning interleaved edit.
	prev := mustLoadIssueForTest(t, issueID)
	closed := prev
	closed.Status = "done"
	plan := outerloop.Plan{State: "running", Round: 1, MaxRounds: 3}
	testHandler.outerLoopPullbackClosedTicket(ctx, prev, closed, plan)

	var status string
	if err := testPool.QueryRow(context.Background(),
		`SELECT status FROM issue WHERE id = $1`, issueID).Scan(&status); err != nil {
		t.Fatalf("reload issue: %v", err)
	}
	if status != "in_progress" {
		t.Errorf("status = %q, want in_progress (guarded revert must not clobber a newer state)", status)
	}
	if got := systemCommentCount(t, issueID, "外循环拉回失败"); got != 1 {
		t.Errorf("fallback announcements = %d, want 1", got)
	}
	if got := outerLoopState(t, issueID); got != "running" {
		t.Errorf("plan state = %q, want running (fallback keeps the loop alive for the sweep)", got)
	}
}

// ---------------------------------------------------------------------------

func mustLoadIssueForTest(t *testing.T, issueID string) db.Issue {
	t.Helper()
	issue, err := db.New(testPool).GetIssue(context.Background(), mustParseUUID(t, issueID))
	if err != nil {
		t.Fatalf("load issue: %v", err)
	}
	return issue
}
