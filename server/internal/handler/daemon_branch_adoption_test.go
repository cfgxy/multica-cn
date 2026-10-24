package handler

import (
	"net/http"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
)

func TestAuthorizeBranchAdoptionReturnsTrustedSnapshot(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	fixture := newBranchAdoptionAuthorizationFixture(t)

	w := testutil.Call(t, testHandler.AuthorizeBranchAdoption,
		branchAdoptionAuthorizationRequest(fixture.continuationTaskID, fixture.sourceTaskID, fixture.branch, testWorkspaceID)).Want(http.StatusOK)
	var response BranchAdoptionAuthorizationResponse
	w.JSON(&response)
	if response.Branch != fixture.branch || !response.SourceIssueSuspended || !response.ContinuationTaskIsRunning {
		t.Fatalf("authorization response = %+v", response)
	}
	if response.Source.WorkspaceID != testWorkspaceID || response.Source.AgentID != fixture.sourceAgentID || response.Source.ConversationID != fixture.sourceIssueID || response.Source.TaskID != fixture.sourceTaskID {
		t.Fatalf("source snapshot = %+v", response.Source)
	}
	if response.Continuation.WorkspaceID != testWorkspaceID || response.Continuation.AgentID != fixture.continuationAgentID || response.Continuation.ConversationID != fixture.continuationIssueID || response.Continuation.TaskID != fixture.continuationTaskID {
		t.Fatalf("continuation snapshot = %+v", response.Continuation)
	}
}

func TestAuthorizeBranchAdoptionRejectsInvalidMappings(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	tests := []struct {
		name      string
		mutate    func(t *testing.T, fixture *branchAdoptionAuthorizationFixture)
		sourceID  func(*branchAdoptionAuthorizationFixture) string
		branch    func(*branchAdoptionAuthorizationFixture) string
		workspace func(*branchAdoptionAuthorizationFixture) string
		want      int
	}{
		{
			name: "continuation not running",
			mutate: func(t *testing.T, fixture *branchAdoptionAuthorizationFixture) {
				dbfx.Exec(t, `UPDATE agent_task_queue SET status = 'queued' WHERE id = $1`, fixture.continuationTaskID)
			},
			sourceID:  func(f *branchAdoptionAuthorizationFixture) string { return f.sourceTaskID },
			branch:    func(f *branchAdoptionAuthorizationFixture) string { return f.branch },
			workspace: func(_ *branchAdoptionAuthorizationFixture) string { return testWorkspaceID },
			want:      http.StatusConflict,
		},
		{
			name:      "source and continuation are the same task",
			mutate:    func(_ *testing.T, _ *branchAdoptionAuthorizationFixture) {},
			sourceID:  func(f *branchAdoptionAuthorizationFixture) string { return f.continuationTaskID },
			branch:    func(f *branchAdoptionAuthorizationFixture) string { return f.branch },
			workspace: func(_ *branchAdoptionAuthorizationFixture) string { return testWorkspaceID },
			want:      http.StatusConflict,
		},
		{
			name:      "requested branch differs from source task mapping",
			mutate:    func(_ *testing.T, _ *branchAdoptionAuthorizationFixture) {},
			sourceID:  func(f *branchAdoptionAuthorizationFixture) string { return f.sourceTaskID },
			branch:    func(_ *branchAdoptionAuthorizationFixture) string { return "agent/source/other" },
			workspace: func(_ *branchAdoptionAuthorizationFixture) string { return testWorkspaceID },
			want:      http.StatusConflict,
		},
		{
			name: "source issue is not suspended",
			mutate: func(t *testing.T, fixture *branchAdoptionAuthorizationFixture) {
				dbfx.Exec(t, `UPDATE issue SET suspended_at = NULL WHERE id = $1`, fixture.sourceIssueID)
			},
			sourceID:  func(f *branchAdoptionAuthorizationFixture) string { return f.sourceTaskID },
			branch:    func(f *branchAdoptionAuthorizationFixture) string { return f.branch },
			workspace: func(_ *branchAdoptionAuthorizationFixture) string { return testWorkspaceID },
			want:      http.StatusConflict,
		},
		{
			name:      "daemon token belongs to another workspace",
			mutate:    func(_ *testing.T, _ *branchAdoptionAuthorizationFixture) {},
			sourceID:  func(f *branchAdoptionAuthorizationFixture) string { return f.sourceTaskID },
			branch:    func(f *branchAdoptionAuthorizationFixture) string { return f.branch },
			workspace: func(_ *branchAdoptionAuthorizationFixture) string { return "foreign-workspace" },
			want:      http.StatusNotFound,
		},
		{
			name: "source task belongs to another workspace",
			mutate: func(t *testing.T, fixture *branchAdoptionAuthorizationFixture) {
				foreignWorkspaceID := dbfx.Workspace(t, "branch adoption foreign workspace", "branch-adoption-foreign")
				foreign := testutil.New(testPool, foreignWorkspaceID, testUserID)
				foreignAgentID := foreign.Agent(t, "branch adoption foreign source agent", "")
				foreignIssueID := foreign.Issue(t, "branch adoption foreign source issue", testutil.Cols{
					"suspended_at": testutil.Raw("now()"),
				})
				fixture.sourceTaskID = foreign.Task(t, foreignAgentID, testutil.Cols{
					"issue_id":    foreignIssueID,
					"branch_name": fixture.branch,
				})
			},
			sourceID:  func(f *branchAdoptionAuthorizationFixture) string { return f.sourceTaskID },
			branch:    func(f *branchAdoptionAuthorizationFixture) string { return f.branch },
			workspace: func(_ *branchAdoptionAuthorizationFixture) string { return testWorkspaceID },
			want:      http.StatusNotFound,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newBranchAdoptionAuthorizationFixture(t)
			tt.mutate(t, fixture)
			testutil.Call(t, testHandler.AuthorizeBranchAdoption,
				branchAdoptionAuthorizationRequest(fixture.continuationTaskID, tt.sourceID(fixture), tt.branch(fixture), tt.workspace(fixture))).Want(tt.want)
		})
	}
}

type branchAdoptionAuthorizationFixture struct {
	branch              string
	sourceAgentID       string
	sourceIssueID       string
	sourceTaskID        string
	continuationAgentID string
	continuationIssueID string
	continuationTaskID  string
}

func newBranchAdoptionAuthorizationFixture(t *testing.T) *branchAdoptionAuthorizationFixture {
	t.Helper()
	const branch = "agent/source/ruyi-871"
	runtimeID := handlerTestRuntimeID(t)
	sourceAgentID := dbfx.Agent(t, "branch adoption source agent", runtimeID, testutil.Cols{
		"instructions": "",
		"custom_env":   testutil.Raw("'{}'::jsonb"),
		"custom_args":  testutil.Raw("'[]'::jsonb"),
	})
	sourceIssueID := dbfx.Issue(t, "branch adoption source issue", testutil.Cols{
		"suspended_at": testutil.Raw("now()"),
	})
	sourceTaskID := dbfx.Task(t, sourceAgentID, testutil.Cols{
		"runtime_id":  runtimeID,
		"issue_id":    sourceIssueID,
		"branch_name": branch,
	})
	continuationAgentID := dbfx.Agent(t, "branch adoption continuation agent", runtimeID, testutil.Cols{
		"instructions": "",
		"custom_env":   testutil.Raw("'{}'::jsonb"),
		"custom_args":  testutil.Raw("'[]'::jsonb"),
	})
	continuationIssueID := dbfx.Issue(t, "branch adoption continuation issue", testutil.Cols{
		"status": "in_progress",
	})
	continuationTaskID := dbfx.Task(t, continuationAgentID, testutil.Cols{
		"runtime_id": runtimeID,
		"issue_id":   continuationIssueID,
		"status":     "running",
		"started_at": testutil.Raw("now()"),
	})
	return &branchAdoptionAuthorizationFixture{
		branch:              branch,
		sourceAgentID:       sourceAgentID,
		sourceIssueID:       sourceIssueID,
		sourceTaskID:        sourceTaskID,
		continuationAgentID: continuationAgentID,
		continuationIssueID: continuationIssueID,
		continuationTaskID:  continuationTaskID,
	}
}

func branchAdoptionAuthorizationRequest(continuationTaskID, sourceTaskID, branch, workspaceID string) *http.Request {
	req := newDaemonTokenRequest(http.MethodPost, "/api/daemon/tasks/"+continuationTaskID+"/branch-adoptions/authorize", map[string]string{
		"source_task_id": sourceTaskID,
		"branch":         branch,
	}, workspaceID, "branch-adoption-daemon")
	return withURLParam(req, "taskId", continuationTaskID)
}
