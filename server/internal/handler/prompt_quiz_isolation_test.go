package handler

// Quiz run isolation (RUYI-185, Owner Q18(A)): a measurement run occupies no
// issue and counts in no production statistic.
//
// Both properties are carried by two columns on agent_task_queue —
// issue_id IS NULL and originator_source='quiz' — and the tests below are
// written so that removing either one breaks them:
//
//   - TestQuizRunIsInvisibleToIssueDimensionQueries inserts the SAME run twice
//     over, once isolated and once with an issue attached, and requires the
//     issue-dimension queries to miss the first and find the second. Drop the
//     `issue_id IS NULL` construction from CreatePromptQuizTask and the first
//     half starts matching, which fails.
//   - TestQuizRunIsNotCountedAsProductionActivity requires the task-kind
//     discriminator to say "quiz", and separately requires that a run without
//     the source marker falls through to quick_create — so deleting the quiz
//     branch in computeTaskKind fails the first assertion while the second
//     documents exactly what it would be counted as instead.
//   - TestCollectorCannotPickUpAProductionRun requires the collector's own
//     query to skip a finished production run sitting right next to a finished
//     quiz run.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/promptquiz"
)

// enqueueQuizRun creates a real quiz run through the production query, so the
// isolation under test is the shipped construction and not a test fixture's
// idea of it.
func enqueueQuizRun(t *testing.T, agentID, itemID string) db.AgentTaskQueue {
	t.Helper()
	ctxBlob, err := json.Marshal(map[string]any{
		"kind":          promptquiz.TaskKind,
		"quiz_item_id":  itemID,
		"quiz_batch_id": newQuizUUID(t),
		"quiz_prompt":   "Summarise the steps you would take to triage a failing build.",
	})
	if err != nil {
		t.Fatalf("marshal quiz context: %v", err)
	}
	task, err := testHandler.Queries.CreatePromptQuizTask(context.Background(), db.CreatePromptQuizTaskParams{
		AgentID:   parseUUID(agentID),
		RuntimeID: parseUUID(handlerTestRuntimeID(t)),
		Priority:  0,
		Context:   ctxBlob,
		ItemID:    parseUUID(itemID),
	})
	if err != nil {
		t.Fatalf("enqueue quiz run: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id = $1`, task.ID)
	})
	return task
}

func TestQuizRunIsInvisibleToIssueDimensionQueries(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	agentID := dbfx.Agent(t, "quiz-isolation-agent", handlerTestRuntimeID(t))
	itemID := seedQuizItem(t, "isolation-probe")
	issueID := dbfx.Issue(t, "quiz isolation control issue")

	quizRun := enqueueQuizRun(t, agentID, itemID)
	dbfx.Exec(t, `UPDATE agent_task_queue SET status = 'running' WHERE id = $1`, quizRun.ID)

	// The control: the identical row with the isolation condition removed. This
	// is what "the assertion fails once isolation is removed" means concretely —
	// the second half of every pair below must find this row.
	attachedRun := dbfx.Task(t, agentID, map[string]any{
		"status":            "running",
		"runtime_id":        handlerTestRuntimeID(t),
		"issue_id":          issueID,
		"originator_source": promptquiz.OriginatorSource,
	})

	active, err := testHandler.Queries.ListActiveTasksByIssue(ctx, parseUUID(issueID))
	if err != nil {
		t.Fatalf("ListActiveTasksByIssue: %v", err)
	}
	if containsTaskID(activeTaskIDs(active), uuidToString(quizRun.ID)) {
		t.Fatal("ListActiveTasksByIssue returned the quiz run: a measurement must not occupy an issue (Owner Q18)")
	}
	if !containsTaskID(activeTaskIDs(active), attachedRun) {
		t.Fatal("the control run with issue_id set was not returned either — this query cannot prove isolation")
	}

	hasActive, err := testHandler.Queries.HasActiveTaskForIssue(ctx, parseUUID(issueID))
	if err != nil {
		t.Fatalf("HasActiveTaskForIssue: %v", err)
	}
	if !hasActive {
		t.Fatal("the control run should make the issue look busy; the probe is broken")
	}
	// With the control removed, the quiz run alone must leave the issue idle.
	dbfx.Exec(t, `DELETE FROM agent_task_queue WHERE id = $1`, attachedRun)
	hasActive, err = testHandler.Queries.HasActiveTaskForIssue(ctx, parseUUID(issueID))
	if err != nil {
		t.Fatalf("HasActiveTaskForIssue: %v", err)
	}
	if hasActive {
		t.Fatal("a running quiz run made the issue look busy: it would block issue triggers it has nothing to do with")
	}

	siblings, err := testHandler.Queries.ListActiveSiblingIssueTasks(ctx, db.ListActiveSiblingIssueTasksParams{
		AgentID:     parseUUID(agentID),
		TaskID:      parseUUID(newQuizUUID(t)),
		WorkspaceID: parseUUID(testWorkspaceID),
	})
	if err != nil {
		t.Fatalf("ListActiveSiblingIssueTasks: %v", err)
	}
	for _, s := range siblings {
		if uuidToString(s.TaskID) == uuidToString(quizRun.ID) {
			t.Fatal("the quiz run appeared in the claim-time sibling warning, which is an issue-dimension read")
		}
	}
}

func activeTaskIDs(rows []db.AgentTaskQueue) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, uuidToString(r.ID))
	}
	return out
}

func containsTaskID(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

func TestQuizRunIsNotCountedAsProductionActivity(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := dbfx.Agent(t, "quiz-kind-agent", handlerTestRuntimeID(t))
	itemID := seedQuizItem(t, "kind-probe")
	quizRun := enqueueQuizRun(t, agentID, itemID)

	if got := computeTaskKind(quizRun); got != promptquiz.TaskKind {
		t.Fatalf("computeTaskKind = %q, want %q — an unmarked measurement is counted as production activity", got, promptquiz.TaskKind)
	}

	// The same row minus the source marker. Naming what it degrades into is the
	// point: quick_create is the member-creates-an-issue statistic, which is
	// precisely the number a measurement must stay out of.
	unmarked := quizRun
	unmarked.OriginatorSource.Valid = false
	unmarked.OriginatorSource.String = ""
	if got := computeTaskKind(unmarked); got != "quick_create" {
		t.Fatalf("without originator_source the run reads as %q; the quiz branch in computeTaskKind is what keeps it out of that bucket, and this test no longer proves it", got)
	}
}

func TestCollectorCannotPickUpAProductionRun(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	agentID := dbfx.Agent(t, "quiz-collector-agent", handlerTestRuntimeID(t))
	itemID := seedQuizItem(t, "collector-probe")

	quizRun := enqueueQuizRun(t, agentID, itemID)
	dbfx.Exec(t, `UPDATE agent_task_queue SET status = 'completed', completed_at = now() WHERE id = $1`, quizRun.ID)

	issueID := dbfx.Issue(t, "quiz collector control issue")
	productionRun := dbfx.Task(t, agentID, map[string]any{
		"status":            "completed",
		"issue_id":          issueID,
		"completed_at":      testutil.Raw("now()"),
		"originator_source": "direct_human",
	})

	rows, err := testHandler.Queries.ListFinishedPromptQuizTasks(ctx, 100)
	if err != nil {
		t.Fatalf("ListFinishedPromptQuizTasks: %v", err)
	}
	var sawQuiz, sawProduction bool
	for _, r := range rows {
		switch uuidToString(r.TaskID) {
		case uuidToString(quizRun.ID):
			sawQuiz = true
		case productionRun:
			sawProduction = true
		}
	}
	if !sawQuiz {
		t.Fatal("the collector did not see its own finished quiz run; the isolation assertion below would pass vacuously")
	}
	if sawProduction {
		t.Fatal("the collector picked up a production run: a member's work would be graded as a quiz measurement")
	}
}
