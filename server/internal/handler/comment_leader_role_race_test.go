package handler

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func leaderMergeParams(issueID, agentID, squadID, commentID string) db.MergeCommentIntoPendingTaskParams {
	return db.MergeCommentIntoPendingTaskParams{
		NewTriggerCommentID: util.MustParseUUID(commentID),
		NewIsLeaderTask:     true,
		NewSquadID:          util.MustParseUUID(squadID),
		IssueID:             util.MustParseUUID(issueID),
		AgentID:             util.MustParseUUID(agentID),
	}
}

func TestMergeCommentIntoPendingTask_DoesNotOverwriteCompetingLeaderRole(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	fx := newSquadCommentTriggerFixture(t)
	issueID := uuidToString(fx.Issue.ID)
	otherSquadID := dbfx.Squad(t, "competing leader role", fx.LeaderID)
	firstCommentID := dbfx.Comment(t, issueID, "first leader trigger")
	secondCommentID := dbfx.Comment(t, issueID, "second leader trigger")
	var runtimeID string
	dbfx.QueryRow(t, `SELECT runtime_id FROM agent WHERE id = $1`, fx.LeaderID).Scan(&runtimeID)
	taskID := dbfx.Task(t, fx.LeaderID, testutil.Cols{
		"runtime_id": runtimeID, "issue_id": issueID, "status": "queued",
	})

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, params := range []db.MergeCommentIntoPendingTaskParams{
		leaderMergeParams(issueID, fx.LeaderID, fx.SquadID, firstCommentID),
		leaderMergeParams(issueID, fx.LeaderID, otherSquadID, secondCommentID),
	} {
		params := params
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := testHandler.Queries.MergeCommentIntoPendingTask(context.Background(), params)
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)

	var succeeded, missed int
	for err := range errs {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, pgx.ErrNoRows):
			missed++
		default:
			t.Fatalf("merge error: %v", err)
		}
	}
	if succeeded != 1 || missed != 1 {
		t.Fatalf("merge results: succeeded=%d missed=%d, want 1/1", succeeded, missed)
	}
	var isLeader bool
	var squadID string
	dbfx.QueryRow(t, `SELECT is_leader_task, squad_id FROM agent_task_queue WHERE id = $1`, taskID).Scan(&isLeader, &squadID)
	if !isLeader || (squadID != fx.SquadID && squadID != otherSquadID) {
		t.Fatalf("final role: is_leader=%v squad=%s", isLeader, squadID)
	}
}

func TestMergeCommentIntoPendingTask_RechecksStatusAfterClaimLock(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	fx := newSquadCommentTriggerFixture(t)
	issueID := uuidToString(fx.Issue.ID)
	commentID := dbfx.Comment(t, issueID, "leader trigger during claim")
	var runtimeID string
	dbfx.QueryRow(t, `SELECT runtime_id FROM agent WHERE id = $1`, fx.LeaderID).Scan(&runtimeID)
	taskID := dbfx.Task(t, fx.LeaderID, testutil.Cols{
		"runtime_id": runtimeID, "issue_id": issueID, "status": "queued",
	})

	claimTx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin claim transaction: %v", err)
	}
	defer claimTx.Rollback(context.Background())
	var blockerPID int32
	if err := claimTx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&blockerPID); err != nil {
		t.Fatalf("read claim pid: %v", err)
	}
	if _, err := claimTx.Exec(ctx, `UPDATE agent_task_queue SET status = 'dispatched' WHERE id = $1`, taskID); err != nil {
		t.Fatalf("stage claim: %v", err)
	}

	result := make(chan error, 1)
	go func() {
		_, err := testHandler.Queries.MergeCommentIntoPendingTask(ctx,
			leaderMergeParams(issueID, fx.LeaderID, fx.SquadID, commentID))
		result <- err
	}()
	waitForBlockedDatabaseQuery(t, ctx, "UPDATE agent_task_queue AS target", blockerPID)
	if err := claimTx.Commit(ctx); err != nil {
		t.Fatalf("commit claim: %v", err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("merge after claim = %v, want pgx.ErrNoRows", err)
		}
	case <-ctx.Done():
		t.Fatalf("merge did not finish: %v", ctx.Err())
	}

	var status string
	var isLeader bool
	dbfx.QueryRow(t, `SELECT status, is_leader_task FROM agent_task_queue WHERE id = $1`, taskID).Scan(&status, &isLeader)
	if status != "dispatched" || isLeader {
		t.Fatalf("claimed task changed role: status=%s is_leader=%v", status, isLeader)
	}
}
