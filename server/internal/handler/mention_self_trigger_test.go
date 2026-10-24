package handler

import (
	"context"
	"net/http"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// enqueueMentionedAgentTasksForTest mirrors the production comment path for
// @mention triggers: compute the cascade trigger set, then enqueue it. Kept as a
// test helper so these integration tests keep asserting enqueue side effects.
func enqueueMentionedAgentTasksForTest(t *testing.T, ctx context.Context, issue db.Issue, comment db.Comment, parentComment *db.Comment, authorType, authorID string) {
	t.Helper()
	triggers, _ := testHandler.computeCommentAgentTriggers(ctx, issue, comment.Content, parentComment, authorType, authorID, commentTriggerComputeOptions{
		SourceTaskID: comment.SourceTaskID,
	})
	testHandler.enqueueCommentAgentTriggers(ctx, issue, comment, triggers)
}

// selfMentionFixture wires the seeded "Handler Test Agent" as J plus two
// fresh issues so we can exercise the agent-self-mention path on the @mention
// branch of computeCommentAgentTriggers. The three tests below cover
// the behavior we want post-MUL-2338:
//
//   - cross-issue self-mention enqueues (child→parent handoff between issues
//     assigned to the same agent must not be swallowed)
//   - same-issue self-mention from a trusted task is suppressed
//   - pending-task de-duplication remains effective for non-suppressed paths
type selfMentionFixture struct {
	JID          string
	RuntimeID    string
	SourceTaskID string
	IssueAID     string // primary issue (used for same-issue scenarios)
	IssueA       db.Issue
	IssueBID     string // a second issue (used for the cross-issue scenario)
	IssueB       db.Issue
	CommentAID   string // a comment on IssueA authored by J — used as the trigger
	CommentA     db.Comment
	CommentBID   string // a comment on IssueB authored by J — used as the trigger
	CommentB     db.Comment
}

func newSelfMentionFixture(t *testing.T) selfMentionFixture {
	t.Helper()
	ctx := context.Background()

	// Reuse the seeded workspace-visible agent — it already has a runtime.
	var jID string
	if err := testPool.QueryRow(ctx, `
		SELECT id FROM agent WHERE workspace_id = $1 ORDER BY created_at ASC LIMIT 1
	`, testWorkspaceID).Scan(&jID); err != nil {
		t.Fatalf("load seeded agent: %v", err)
	}
	var runtimeID string
	if err := testPool.QueryRow(ctx, `SELECT runtime_id FROM agent WHERE id = $1`, jID).Scan(&runtimeID); err != nil {
		t.Fatalf("load runtime: %v", err)
	}

	issueColumns := testutil.Cols{
		"assignee_type": "agent",
		"assignee_id":   jID,
	}
	issueAID := dbfx.Issue(t, "self-mention test A (same-issue scenarios)", issueColumns)
	issueBID := dbfx.Issue(t, "self-mention test B (parent issue, cross-issue handoff)", issueColumns)
	// Triggered tasks are not fixture rows, so clean them before the fixture
	// removes the owning issues.
	dbfx.Cleanup(t, `DELETE FROM agent_task_queue WHERE issue_id = $1 OR issue_id = $2`, issueAID, issueBID)

	sourceTaskID := dbfx.Task(t, jID, testutil.Cols{
		"runtime_id": runtimeID,
		"issue_id":   issueAID,
		"status":     "running",
	})
	commentColumns := testutil.Cols{
		"author_type":    "agent",
		"author_id":      jID,
		"source_task_id": sourceTaskID,
	}
	commentAID := dbfx.Comment(t, issueAID, "[@J](mention://agent/"+jID+") follow-up coming", commentColumns)
	commentBID := dbfx.Comment(t, issueBID, "Child issue done — [@J](mention://agent/"+jID+") please wrap up here", commentColumns)

	issueA, err := testHandler.Queries.GetIssue(ctx, util.MustParseUUID(issueAID))
	if err != nil {
		t.Fatalf("load issueA: %v", err)
	}
	issueB, err := testHandler.Queries.GetIssue(ctx, util.MustParseUUID(issueBID))
	if err != nil {
		t.Fatalf("load issueB: %v", err)
	}
	commentA, err := testHandler.Queries.GetComment(ctx, util.MustParseUUID(commentAID))
	if err != nil {
		t.Fatalf("load commentA: %v", err)
	}
	commentB, err := testHandler.Queries.GetComment(ctx, util.MustParseUUID(commentBID))
	if err != nil {
		t.Fatalf("load commentB: %v", err)
	}

	return selfMentionFixture{
		JID:          jID,
		RuntimeID:    runtimeID,
		SourceTaskID: sourceTaskID,
		IssueAID:     issueAID,
		IssueA:       issueA,
		IssueBID:     issueBID,
		IssueB:       issueB,
		CommentAID:   commentAID,
		CommentA:     commentA,
		CommentBID:   commentBID,
		CommentB:     commentB,
	}
}

func TestCreateComment_SameIssueAgentSelfMentionDoesNotDispatch(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	fx := newSelfMentionFixture(t)

	req := newRequest(http.MethodPost, "/api/issues/"+fx.IssueAID+"/comments", map[string]any{
		"content": "[@J](mention://agent/" + fx.JID + ") no new work",
	})
	req = withURLParam(req, "id", fx.IssueAID)
	req.Header.Set("X-Agent-ID", fx.JID)
	req.Header.Set("X-Task-ID", fx.SourceTaskID)
	var resp CommentResponse
	testutil.Call(t, testHandler.CreateComment, req).Want(http.StatusCreated).JSON(&resp)
	if len(resp.TriggerOutcomes) != 1 {
		t.Fatalf("trigger_outcomes = %+v, want 1", resp.TriggerOutcomes)
	}
	if outcome := resp.TriggerOutcomes[0]; outcome.TargetType != "agent" || outcome.TargetID != fx.JID ||
		outcome.Status != DispatchBlocked || outcome.ReasonCode != ReasonSelfTriggerSuppressed {
		t.Fatalf("trigger outcome = %+v, want agent %s blocked/self_trigger_suppressed", outcome, fx.JID)
	}

	if got := countQueuedOrDispatched(t, fx.JID, fx.IssueAID); got != 0 {
		t.Fatalf("expected no queued follow-up after same-issue self-mention, got %d", got)
	}

	var stampedSourceTaskID string
	if err := testPool.QueryRow(ctx, `
		SELECT source_task_id FROM comment
		WHERE issue_id = $1 AND content = $2
		ORDER BY created_at DESC LIMIT 1
	`, fx.IssueAID, "[@J](mention://agent/"+fx.JID+") no new work").Scan(&stampedSourceTaskID); err != nil {
		t.Fatalf("load created comment source task: %v", err)
	}
	if stampedSourceTaskID != fx.SourceTaskID {
		t.Fatalf("comment source_task_id = %s, want %s", stampedSourceTaskID, fx.SourceTaskID)
	}
}

func TestCompleteTask_SameIssueAgentSelfMentionDoesNotRequeue(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	fx := newSelfMentionFixture(t)

	if w := completeTaskViaHandler(t, fx.SourceTaskID, "done"); w.Code != http.StatusOK {
		t.Fatalf("CompleteTask: expected %d, got %d: %s", http.StatusOK, w.Code, w.Body.String())
	}
	if got := countQueuedOrDispatched(t, fx.JID, fx.IssueAID); got != 0 {
		t.Fatalf("completion replay queued %d follow-up task(s), want 0", got)
	}
}

func TestCompleteTask_ReconcileSameIssueSelfMentionDoesNotRequeue(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	fx := newSelfMentionFixture(t)

	dbfx.Exec(t, `UPDATE agent_task_queue SET status = 'completed', completed_at = now() WHERE id = $1`, fx.SourceTaskID)
	reconcilingTaskID := dbfx.Task(t, fx.JID, testutil.Cols{
		"runtime_id": runtimeIDForAgent(t, fx.JID),
		"issue_id":   fx.IssueAID,
		"status":     "running",
		"created_at": testutil.Raw("now() - interval '1 minute'"),
		"started_at": testutil.Raw("now() - interval '1 minute'"),
	})

	if w := completeTaskViaHandler(t, reconcilingTaskID, "done"); w.Code != http.StatusOK {
		t.Fatalf("CompleteTask: expected %d, got %d: %s", http.StatusOK, w.Code, w.Body.String())
	}
	if got := countQueuedOrDispatched(t, fx.JID, fx.IssueAID); got != 0 {
		t.Fatalf("completion replay queued %d follow-up task(s), want 0", got)
	}
}

func TestEnqueueMentionedAgentTasks_MissingSourceTaskFallsBackToNormalRouting(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	fx := newSelfMentionFixture(t)

	dbfx.Exec(t, `DELETE FROM agent_task_queue WHERE id = $1`, fx.SourceTaskID)
	enqueueMentionedAgentTasksForTest(t, ctx, fx.IssueA, fx.CommentA, nil, "agent", fx.JID)

	if got := countQueuedOrDispatched(t, fx.JID, fx.IssueAID); got != 1 {
		t.Fatalf("missing source task should use normal explicit routing, got %d queued task(s)", got)
	}
}

// countQueuedOrDispatched returns the number of queued|dispatched tasks for
// (agent, issue). Mirrors the predicate used by HasPendingTaskForIssueAndAgent.
func countQueuedOrDispatched(t *testing.T, agentID, issueID string) int {
	t.Helper()
	var n int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FROM agent_task_queue
		WHERE issue_id = $1 AND agent_id = $2 AND status IN ('queued', 'dispatched')
	`, issueID, agentID).Scan(&n); err != nil {
		t.Fatalf("count queued/dispatched tasks: %v", err)
	}
	return n
}

// TestEnqueueMentionedAgentTasks_SelfMentionCrossIssueEnqueues is the
// regression test for the MUL-2338 child→parent handoff. The same agent runs
// in a child issue, then posts a top-level comment on the parent issue (whose
// assignee is the same agent) that @mentions itself. The comment handler MUST
// enqueue a task on the parent issue — silently dropping the trigger was the
// bug Bohan reported.
func TestEnqueueMentionedAgentTasks_SelfMentionCrossIssueEnqueues(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	fx := newSelfMentionFixture(t)

	if got := countQueuedOrDispatched(t, fx.JID, fx.IssueBID); got != 0 {
		t.Fatalf("before: expected 0 pending tasks on parent issue, got %d", got)
	}

	enqueueMentionedAgentTasksForTest(t, ctx, fx.IssueB, fx.CommentB, nil, "agent", fx.JID)

	if got := countQueuedOrDispatched(t, fx.JID, fx.IssueBID); got != 1 {
		t.Fatalf("after self-mention from another issue: expected 1 queued task on parent issue, got %d", got)
	}
}

// TestEnqueueMentionedAgentTasks_SameIssueSelfMentionIsSuppressed proves that
// an agent task cannot schedule a new run by explicitly mentioning itself in
// a comment on that task's own issue.
func TestEnqueueMentionedAgentTasks_SameIssueSelfMentionIsSuppressed(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	fx := newSelfMentionFixture(t)

	if got := countQueuedOrDispatched(t, fx.JID, fx.IssueAID); got != 0 {
		t.Fatalf("before: expected 0 queued/dispatched tasks (only the running task), got %d", got)
	}

	enqueueMentionedAgentTasksForTest(t, ctx, fx.IssueA, fx.CommentA, nil, "agent", fx.JID)

	if got := countQueuedOrDispatched(t, fx.JID, fx.IssueAID); got != 0 {
		t.Fatalf("after same-issue self-mention: expected no queued follow-up, got %d", got)
	}
}

// TestEnqueueMentionedAgentTasks_SameIssueSelfMentionDoesNotAddToPendingTask
// verifies the suppression holds regardless of a pre-existing pending task.
func TestEnqueueMentionedAgentTasks_SameIssueSelfMentionDoesNotAddToPendingTask(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	fx := newSelfMentionFixture(t)

	cases := []struct {
		name   string
		status string
	}{
		{name: "queued task", status: "queued"},
		{name: "dispatched task", status: "dispatched"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := testPool.Exec(ctx, `DELETE FROM agent_task_queue WHERE issue_id = $1 AND id <> $2`, fx.IssueAID, fx.SourceTaskID); err != nil {
				t.Fatalf("reset tasks: %v", err)
			}
			if _, err := testPool.Exec(ctx, `
				INSERT INTO agent_task_queue (agent_id, runtime_id, issue_id, status)
				VALUES ($1, $2, $3, $4)
			`, fx.JID, fx.RuntimeID, fx.IssueAID, tc.status); err != nil {
				t.Fatalf("seed %s task: %v", tc.status, err)
			}

			before := countQueuedOrDispatched(t, fx.JID, fx.IssueAID)
			if before != 1 {
				t.Fatalf("before: expected 1 pre-existing %s task, got %d", tc.status, before)
			}

			enqueueMentionedAgentTasksForTest(t, ctx, fx.IssueA, fx.CommentA, nil, "agent", fx.JID)

			after := countQueuedOrDispatched(t, fx.JID, fx.IssueAID)
			if after != 1 {
				t.Fatalf("after same-issue self-mention with pre-existing %s task: expected 1, got %d", tc.status, after)
			}
		})
	}
}
