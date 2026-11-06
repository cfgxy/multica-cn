package handler

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/taskfailure"
)

func TestClaimTaskByRuntime_DaemonTokenCannotCrossDaemon(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	for _, tc := range []struct {
		name       string
		taskStatus string
	}{
		{name: "queued task", taskStatus: "queued"},
		{name: "stale dispatched task", taskStatus: "dispatched"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runtimeID := dbfx.Runtime(t, tc.name+" runtime", testutil.Cols{
				"daemon_id": "target-daemon",
				"provider":  "claude",
			})
			agentID, issueID := createClaimReclaimAgentAndIssue(t, ctx, runtimeID, tc.name+" agent")

			taskCols := testutil.Cols{
				"runtime_id": runtimeID,
				"issue_id":   issueID,
				"status":     tc.taskStatus,
			}
			if tc.taskStatus == "dispatched" {
				taskCols["dispatched_at"] = testutil.Raw("now() - interval '2 minutes'")
			}
			taskID := dbfx.Task(t, agentID, taskCols)

			var beforeDispatchedAt pgtype.Timestamptz
			dbfx.QueryRow(t, `SELECT dispatched_at FROM agent_task_queue WHERE id = $1`, taskID).Scan(&beforeDispatchedAt)

			req := newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+runtimeID+"/tasks/claim", nil,
				testWorkspaceID, "attacker-daemon")
			req = withURLParam(req, "runtimeId", runtimeID)
			testutil.Call(t, testHandler.ClaimTaskByRuntime, req).Want(http.StatusNotFound)

			var status string
			var afterDispatchedAt pgtype.Timestamptz
			dbfx.QueryRow(t, `SELECT status, dispatched_at FROM agent_task_queue WHERE id = $1`, taskID).Scan(&status, &afterDispatchedAt)
			if status != tc.taskStatus || afterDispatchedAt != beforeDispatchedAt {
				t.Fatalf("task changed after refused claim: status=%q dispatched_at=%v, want status=%q dispatched_at=%v",
					status, afterDispatchedAt, tc.taskStatus, beforeDispatchedAt)
			}
			if got := dbfx.Count(t, `SELECT count(*) FROM task_token WHERE task_id = $1`, taskID); got != 0 {
				t.Fatalf("task token count = %d, want 0", got)
			}
		})
	}
}

func TestClaimTaskByRuntime_DaemonBindingCompatibility(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	for _, tc := range []struct {
		name            string
		runtimeDaemonID any
		requestDaemonID string
		userCredential  bool
	}{
		{name: "matching daemon token", runtimeDaemonID: "claim-daemon", requestDaemonID: "claim-daemon"},
		{name: "unbound runtime daemon", runtimeDaemonID: nil, requestDaemonID: "claim-daemon"},
		{name: "user credential ignores machine binding", runtimeDaemonID: "other-daemon", userCredential: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runtimeID := dbfx.Runtime(t, tc.name+" runtime", testutil.Cols{
				"daemon_id": tc.runtimeDaemonID,
				"provider":  "claude",
			})
			agentID, issueID := createClaimReclaimAgentAndIssue(t, ctx, runtimeID, tc.name+" agent")
			taskID := seedQueuedIssueTask(t, ctx, agentID, runtimeID, issueID)

			var req *http.Request
			if tc.userCredential {
				req = newOwnerDaemonRequest(http.MethodPost, "/api/daemon/runtimes/"+runtimeID+"/tasks/claim", nil)
			} else {
				req = newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+runtimeID+"/tasks/claim", nil,
					testWorkspaceID, tc.requestDaemonID)
			}
			req = withURLParam(req, "runtimeId", runtimeID)
			testutil.Call(t, testHandler.ClaimTaskByRuntime, req).Want(http.StatusOK)

			var status string
			dbfx.QueryRow(t, `SELECT status FROM agent_task_queue WHERE id = $1`, taskID).Scan(&status)
			if status != "dispatched" {
				t.Fatalf("task status = %q, want dispatched", status)
			}
			if got := dbfx.Count(t, `SELECT count(*) FROM task_token WHERE task_id = $1`, taskID); got != 1 {
				t.Fatalf("task token count = %d, want 1", got)
			}
		})
	}
}

func TestClaimTaskByRuntime_OwnerlessAgentOnPrivateRuntimeFailsExplicitly(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	runtimeID := createClaimReclaimRuntime(t, ctx, "Ownerless agent private runtime")
	agentID, issueID := createClaimReclaimAgentAndIssue(t, ctx, runtimeID, "Ownerless private agent")
	dbfx.Exec(t, `UPDATE agent SET owner_id = NULL WHERE id = $1`, agentID)
	taskID := seedQueuedIssueTask(t, ctx, agentID, runtimeID, issueID)

	req := newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+runtimeID+"/tasks/claim", nil,
		testWorkspaceID, "ownerless-agent-claim")
	req = withURLParam(req, "runtimeId", runtimeID)
	w := testutil.Call(t, testHandler.ClaimTaskByRuntime, req).Want(http.StatusForbidden)
	if !strings.Contains(w.Body.String(), "private runtime does not permit task agent") {
		t.Fatalf("ClaimTaskByRuntime body = %q, want explicit private-runtime access error", w.Body.String())
	}

	var status, errorMessage, failureReason string
	dbfx.QueryRow(t, `
		SELECT status, error, failure_reason
		FROM agent_task_queue
		WHERE id = $1
	`, taskID).Scan(&status, &errorMessage, &failureReason)
	if status != "failed" {
		t.Fatalf("task status = %q, want failed", status)
	}
	if !strings.Contains(errorMessage, "agent has no owner") {
		t.Fatalf("task error = %q, want actionable missing-owner error", errorMessage)
	}
	if failureReason != taskfailure.ReasonInvalidTaskIdentity.String() {
		t.Fatalf("failure_reason = %q, want %q", failureReason, taskfailure.ReasonInvalidTaskIdentity)
	}
}

func TestBuildClaimedTaskResponseRejectsAgentOwnerChangedAfterClaim(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	newOwnerID := dbfx.User(t, "Claim owner mismatch", "claim-owner-mismatch-"+uuid.NewString()+"@example.com")
	dbfx.Member(t, testWorkspaceID, newOwnerID, "member")
	runtimeID := createClaimReclaimRuntime(t, ctx, "Claim then change owner runtime")
	agentID, issueID := createClaimReclaimAgentAndIssue(t, ctx, runtimeID, "Claim then change owner agent")
	taskID := seedQueuedIssueTask(t, ctx, agentID, runtimeID, issueID)

	task, err := testHandler.TaskService.ClaimTaskForRuntime(ctx, parseUUID(runtimeID))
	if err != nil {
		t.Fatalf("claim task: %v", err)
	}
	if task == nil || uuidToString(task.ID) != taskID {
		t.Fatalf("claimed task = %+v, want %s", task, taskID)
	}
	dbfx.Exec(t, `UPDATE agent SET owner_id = $1 WHERE id = $2`, newOwnerID, agentID)

	runtime, err := testHandler.Queries.GetAgentRuntimeForWorkspace(ctx, db.GetAgentRuntimeForWorkspaceParams{
		ID:          parseUUID(runtimeID),
		WorkspaceID: parseUUID(testWorkspaceID),
	})
	if err != nil {
		t.Fatalf("load runtime: %v", err)
	}
	req := newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+runtimeID+"/tasks/claim", nil,
		testWorkspaceID, "claim-then-owner-change")
	_, _, _, _, failure := testHandler.buildClaimedTaskResponse(
		req, task, runtime, runtimeID, testWorkspaceID,
	)
	if failure == nil || failure.status != http.StatusForbidden || failure.outcome != "error_runtime_access_denied" {
		t.Fatalf("failure = %+v, want runtime-access forbidden", failure)
	}

	var status, errorMessage, failureReason string
	dbfx.QueryRow(t, `
		SELECT status, error, failure_reason
		FROM agent_task_queue
		WHERE id = $1
	`, taskID).Scan(&status, &errorMessage, &failureReason)
	if status != "failed" {
		t.Fatalf("task status = %q, want failed", status)
	}
	if !strings.Contains(errorMessage, "agent and runtime have different owners") {
		t.Fatalf("task error = %q, want actionable owner-mismatch error", errorMessage)
	}
	if strings.Contains(errorMessage, "agent has no owner") {
		t.Fatalf("task error = %q, must not describe a non-null owner as missing", errorMessage)
	}
	if failureReason != taskfailure.ReasonInvalidTaskIdentity.String() {
		t.Fatalf("failure_reason = %q, want %q", failureReason, taskfailure.ReasonInvalidTaskIdentity)
	}
}

func TestBuildClaimedTaskResponseRejectsAgentReboundAfterClaim(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	oldRuntimeID := createClaimReclaimRuntime(t, ctx, "Claim then rebind old runtime")
	newRuntimeID := createClaimReclaimRuntime(t, ctx, "Claim then rebind new runtime")
	agentID, issueID := createClaimReclaimAgentAndIssue(t, ctx, oldRuntimeID, "Claim then rebind agent")
	taskID := seedQueuedIssueTask(t, ctx, agentID, oldRuntimeID, issueID)

	task, err := testHandler.TaskService.ClaimTaskForRuntime(ctx, parseUUID(oldRuntimeID))
	if err != nil {
		t.Fatalf("claim task: %v", err)
	}
	if task == nil || uuidToString(task.ID) != taskID {
		t.Fatalf("claimed task = %+v, want %s", task, taskID)
	}
	dbfx.Exec(t, `UPDATE agent SET runtime_id = $1 WHERE id = $2`, newRuntimeID, agentID)

	runtime, err := testHandler.Queries.GetAgentRuntimeForWorkspace(ctx, db.GetAgentRuntimeForWorkspaceParams{
		ID:          parseUUID(oldRuntimeID),
		WorkspaceID: parseUUID(testWorkspaceID),
	})
	if err != nil {
		t.Fatalf("load old runtime: %v", err)
	}
	req := newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+oldRuntimeID+"/tasks/claim", nil,
		testWorkspaceID, "claim-then-rebind")
	_, _, _, _, failure := testHandler.buildClaimedTaskResponse(
		req, task, runtime, oldRuntimeID, testWorkspaceID,
	)
	if failure == nil || failure.status != http.StatusConflict || failure.outcome != "error_agent_runtime_changed" {
		t.Fatalf("failure = %+v, want runtime-changed conflict", failure)
	}

	var status, errorMessage, failureReason string
	dbfx.QueryRow(t, `
		SELECT status, error, failure_reason
		FROM agent_task_queue
		WHERE id = $1
	`, taskID).Scan(&status, &errorMessage, &failureReason)
	if status != "failed" {
		t.Fatalf("task status = %q, want failed", status)
	}
	if !strings.Contains(errorMessage, "moved to another runtime") {
		t.Fatalf("task error = %q, want actionable rebind error", errorMessage)
	}
	if failureReason != taskfailure.ReasonInvalidTaskIdentity.String() {
		t.Fatalf("failure_reason = %q, want %q", failureReason, taskfailure.ReasonInvalidTaskIdentity)
	}
}
