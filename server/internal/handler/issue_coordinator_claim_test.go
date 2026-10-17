package handler

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/featureflags"
	"github.com/multica-ai/multica/server/internal/issuecoordinator"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/featureflag"
)

func TestClaimTaskByRuntimeIssueCoordinatorDisabledKeepsLegacySession(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Fatal("handler test database is required")
	}

	for _, tc := range []struct {
		name   string
		setOff bool
	}{
		{name: "flag_missing"},
		{name: "flag_explicitly_false", setOff: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.setOff {
				withFeatureFlag(t, testHandler, featureflags.IssueCoordinatorSessionPOC, false)
			} else {
				withCoordinatorFlags(t, featureflag.NewService(featureflag.NewStaticProvider()))
			}

			runtimeID := dbfx.Runtime(t, "Issue coordinator disabled claim runtime", testutil.Cols{"provider": "codex"})
			agentID := dbfx.Agent(t, "Issue coordinator disabled claim agent", runtimeID)
			issueID := dbfx.Issue(t, "Issue coordinator disabled claim issue", testutil.Cols{"status": "in_progress"})
			dbfx.Task(t, agentID, testutil.Cols{
				"runtime_id": runtimeID, "issue_id": issueID, "status": "completed",
				"session_id": "legacy-session", "started_at": testutil.Raw("now() - interval '1 minute'"),
				"completed_at": testutil.Raw("now()"), "result": testutil.Raw(`'{"message":"legacy handoff"}'::jsonb`),
			})
			taskID := dbfx.Task(t, agentID, testutil.Cols{
				"runtime_id": runtimeID, "issue_id": issueID, "is_leader_task": true,
			})

			w := claimIssueCoordinatorTask(t, runtimeID)
			var response struct {
				Task *AgentTaskResponse `json:"task"`
			}
			w.JSON(&response)
			if response.Task == nil || response.Task.ID != taskID {
				t.Fatalf("claim response task = %+v, want %s", response.Task, taskID)
			}
			if response.Task.IssueCoordinator != nil {
				t.Fatalf("disabled coordinator populated claim metadata: %+v", response.Task.IssueCoordinator)
			}
			if response.Task.PriorSessionID != "legacy-session" {
				t.Fatalf("legacy prior session = %q, want legacy-session", response.Task.PriorSessionID)
			}
		})
	}
}

func TestClaimTaskByRuntimeIssueCoordinatorColdStartKeepsSnapshotAndHandoff(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Fatal("handler test database is required")
	}

	provider := featureflag.NewStaticProvider()
	provider.Set(featureflags.IssueCoordinatorSessionPOC, featureflag.Rule{Default: true})
	withCoordinatorFlags(t, featureflag.NewService(provider))

	runtimeID := dbfx.Runtime(t, "Issue coordinator cold-start claim runtime", testutil.Cols{"provider": "codex"})
	agentID := dbfx.Agent(t, "Issue coordinator cold-start claim agent", runtimeID)
	issueID := dbfx.Issue(t, "Issue coordinator cold-start claim issue", testutil.Cols{"status": "in_progress"})
	priorTaskID := dbfx.Task(t, agentID, testutil.Cols{
		"runtime_id": runtimeID, "issue_id": issueID, "status": "completed",
		"session_id": "legacy-session", "started_at": testutil.Raw("now() - interval '1 minute'"),
		"completed_at": testutil.Raw("now()"), "result": testutil.Raw(`'{"output":"continuity handoff marker"}'::jsonb`),
	})
	if err := testHandler.Queries.UpsertTaskUsage(context.Background(), db.UpsertTaskUsageParams{
		TaskID: parseUUID(priorTaskID), Provider: "codex", Model: "test-model",
		ContextTokens: pgtype.Int8{Int64: 350000, Valid: true},
	}); err != nil {
		t.Fatalf("seed prior task context size: %v", err)
	}
	taskID := dbfx.Task(t, agentID, testutil.Cols{
		"runtime_id": runtimeID, "issue_id": issueID, "is_leader_task": true,
	})
	snapshot, err := issuecoordinator.MarshalSnapshot(issuecoordinator.Snapshot{
		IssueID: issueID, IssueRevision: 7, IssueStatus: "in_progress",
		EvidenceRefs: []string{"claim-evidence-ref"},
	})
	if err != nil {
		t.Fatal(err)
	}
	dbfx.Exec(t, `
		INSERT INTO issue_coordinator_session (
			issue_id, leader_agent_id, runtime_id, session_id, generation,
			snapshot_version, snapshot, last_event_sequence, continuity_state
		) VALUES ($1, $2, $3, 'coordinator-session', 4, 3, $4::jsonb, 8, 'cold_start')
	`, issueID, agentID, runtimeID, string(snapshot))
	t.Cleanup(func() {
		if _, err := testPool.Exec(context.Background(), `DELETE FROM issue_coordinator_session WHERE issue_id = $1 AND leader_agent_id = $2`, issueID, agentID); err != nil {
			t.Errorf("clean up issue coordinator session: %v", err)
		}
	})

	w := claimIssueCoordinatorTask(t, runtimeID)
	var response struct {
		Task *AgentTaskResponse `json:"task"`
	}
	w.JSON(&response)
	if response.Task == nil || response.Task.ID != taskID {
		t.Fatalf("claim response task = %+v, want %s", response.Task, taskID)
	}
	if response.Task.IssueCoordinator == nil {
		t.Fatalf("coordinator metadata missing from enabled claim: %+v", response.Task.IssueCoordinator)
	}
	if response.Task.PriorSessionID != "" || response.Task.PriorSessionResumeUnavailable {
		t.Fatalf("cold-start claim retained unsafe session state: session=%q unavailable=%v", response.Task.PriorSessionID, response.Task.PriorSessionResumeUnavailable)
	}
	for _, want := range []string{"Issue coordinator cold start", "generation 4", "snapshot version 3", "claim-evidence-ref", "continuity handoff marker"} {
		if !strings.Contains(response.Task.PriorContextBrief, want) {
			t.Fatalf("cold-start claim brief missing %q: %s", want, response.Task.PriorContextBrief)
		}
	}
}

func TestClaimTaskByRuntimeIssueCoordinatorResumeRejectionKeepsSnapshotAndHandoff(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Fatal("handler test database is required")
	}

	provider := featureflag.NewStaticProvider()
	provider.Set(featureflags.IssueCoordinatorSessionPOC, featureflag.Rule{Default: true})
	withCoordinatorFlags(t, featureflag.NewService(provider))

	runtimeID := dbfx.Runtime(t, "Issue coordinator rejected-resume claim runtime", testutil.Cols{"provider": "codex"})
	agentID := dbfx.Agent(t, "Issue coordinator rejected-resume claim agent", runtimeID)
	issueID := dbfx.Issue(t, "Issue coordinator rejected-resume claim issue", testutil.Cols{"status": "in_progress"})
	dbfx.Task(t, agentID, testutil.Cols{
		"runtime_id": runtimeID, "issue_id": issueID, "status": "completed",
		"session_rollout_missing": true, "started_at": testutil.Raw("now() - interval '1 minute'"),
		"completed_at": testutil.Raw("now()"), "result": testutil.Raw(`'{"output":"rejected-session handoff marker"}'::jsonb`),
	})
	taskID := dbfx.Task(t, agentID, testutil.Cols{
		"runtime_id": runtimeID, "issue_id": issueID, "is_leader_task": true,
	})
	snapshot, err := issuecoordinator.MarshalSnapshot(issuecoordinator.Snapshot{
		IssueID: issueID, IssueRevision: 8, IssueStatus: "in_progress",
		EvidenceRefs: []string{"rejected-session-evidence-ref"},
	})
	if err != nil {
		t.Fatal(err)
	}
	dbfx.Exec(t, `
		INSERT INTO issue_coordinator_session (
			issue_id, leader_agent_id, runtime_id, session_id, generation,
			snapshot_version, snapshot, last_event_sequence, continuity_state
		) VALUES ($1, $2, $3, 'coordinator-session', 5, 4, $4::jsonb, 9, 'resumable')
	`, issueID, agentID, runtimeID, string(snapshot))
	t.Cleanup(func() {
		if _, err := testPool.Exec(context.Background(), `DELETE FROM issue_coordinator_session WHERE issue_id = $1 AND leader_agent_id = $2`, issueID, agentID); err != nil {
			t.Errorf("clean up issue coordinator session: %v", err)
		}
	})

	w := claimIssueCoordinatorTask(t, runtimeID)
	var response struct {
		Task *AgentTaskResponse `json:"task"`
	}
	w.JSON(&response)
	if response.Task == nil || response.Task.ID != taskID || response.Task.IssueCoordinator == nil {
		t.Fatalf("claim response missing task/coordinator: %+v", response.Task)
	}
	if response.Task.PriorSessionID != "" || response.Task.PriorSessionResumeUnavailable {
		t.Fatalf("rejected-resume claim retained unsafe session state: session=%q unavailable=%v", response.Task.PriorSessionID, response.Task.PriorSessionResumeUnavailable)
	}
	for _, want := range []string{"Issue coordinator cold start", "rejected-session-evidence-ref", "rejected-session handoff marker", "could not have its context carried over"} {
		if !strings.Contains(response.Task.PriorContextBrief, want) {
			t.Fatalf("rejected-resume claim brief missing %q: %s", want, response.Task.PriorContextBrief)
		}
	}
}

func withCoordinatorFlags(t *testing.T, flags *featureflag.Service) {
	t.Helper()
	previous := testHandler.FeatureFlags
	testHandler.FeatureFlags = flags
	t.Cleanup(func() { testHandler.FeatureFlags = previous })
}

func claimIssueCoordinatorTask(t *testing.T, runtimeID string) *testutil.Response {
	t.Helper()
	req := newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+runtimeID+"/tasks/claim", nil,
		testWorkspaceID, "issue-coordinator-claim-test")
	req = withURLParam(req, "runtimeId", runtimeID)
	return testutil.Call(t, testHandler.ClaimTaskByRuntime, req).Want(http.StatusOK)
}
