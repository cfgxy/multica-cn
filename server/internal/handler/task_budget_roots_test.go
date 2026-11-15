package handler

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/knowledgegovernance"
	"github.com/multica-ai/multica/server/internal/retrospective"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
	"github.com/multica-ai/multica/server/pkg/taskbudget"
)

func assertRootBudget(t *testing.T, task db.AgentTaskQueue, enabled bool) {
	t.Helper()
	snapshot, err := taskbudget.FromContext(task.Context)
	if err != nil {
		t.Fatal(err)
	}
	if !enabled {
		if snapshot != nil {
			t.Fatal("default root acquired budget")
		}
		return
	}
	if snapshot == nil || snapshot.SchemaVersion != 1 || snapshot.Profile.MaxRuns != 2 || snapshot.Profile.PerRunTimeoutSeconds != 7 || task.IssueID.Valid {
		t.Fatalf("root missing frozen profile: %s", task.Context)
	}
	config, err := service.TaskBudgetRuntimeConfig(task, json.RawMessage(`{"budget_profile":{"per_run_timeout_seconds":99}}`))
	if err != nil {
		t.Fatal(err)
	}
	profile, err := taskbudget.ParseRuntimeConfig(config)
	if err != nil || profile.Timeout(time.Hour) != 7*time.Second {
		t.Fatalf("claim lost frozen timeout: %s %v", config, err)
	}
}

func TestBudgetRootHandlerEntries(t *testing.T) {
	if testHandler == nil {
		t.Fatal("database fixture required")
	}
	ctx := context.Background()
	for _, enabled := range []bool{false, true} {
		label := "default"
		if enabled {
			label = "profile"
		}
		t.Run(label, func(t *testing.T) {
			runtimeID := handlerTestRuntimeID(t)
			agentID := dbfx.Agent(t, "budget-root-"+label, runtimeID)
			if enabled {
				dbfx.Exec(t, `UPDATE agent SET runtime_config='{"budget_profile":{"max_runs":2,"per_run_timeout_seconds":7}}' WHERE id=$1`, agentID)
			}
			h := *testHandler
			h.TaskService = service.NewTaskService(h.Queries, testPool, nil, nil)
			// Real retrospective enqueue port, including its fenced queue INSERT.
			id, err := h.RetrospectiveTaskEnqueuer()(ctx, retrospective.EnqueueParams{RunID: util.UUIDToString(dbid.NewV7()), AgentID: agentID, RuntimeID: runtimeID, OriginatorUserID: testUserID, Context: []byte(`{"type":"retrospective","evidence":"retained"}`)})
			if err != nil {
				t.Fatal(err)
			}
			task, err := h.Queries.GetAgentTask(ctx, util.MustParseUUID(id))
			if err != nil {
				t.Fatal(err)
			}
			assertRootBudget(t, task, enabled)
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(task.Context, &fields); err != nil || string(fields["evidence"]) != `"retained"` {
				t.Fatal("retrospective context overwritten")
			}

			// Real governance adapter must reuse the transaction carrying the active
			// run row; a nested transaction cannot see that uncommitted row.
			enqueueGovernance := func(commit bool) string {
				t.Helper()
				tx, err := testPool.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback(ctx)
				runID := dbid.NewV7()
				if _, err := tx.Exec(ctx, `INSERT INTO knowledge_governance_run (id,workspace_id,dir_id,agent_id,title,prompt_version,trigger,created_by) VALUES ($1,$2,$3,$4,'budget test','v1','manual',$5)`, runID, testWorkspaceID, dbid.NewV7(), agentID, testUserID); err != nil {
					t.Fatal(err)
				}
				task, err := h.KnowledgeGovernanceTaskEnqueuer()(ctx, h.Queries.WithTx(tx), knowledgegovernance.EnqueueParams{RunID: util.UUIDToString(runID), AgentID: agentID, RuntimeID: runtimeID, OriginatorID: testUserID, Context: []byte(`{"type":"knowledge_governance","evidence":"retained"}`)})
				if err != nil {
					t.Fatal(err)
				}
				assertRootBudget(t, task, enabled)
				if commit {
					if err := tx.Commit(ctx); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := tx.Rollback(ctx); err != nil {
						t.Fatal(err)
					}
				}
				if !commit {
					var count int
					if err := testPool.QueryRow(ctx, `SELECT count(*) FROM agent_task_queue WHERE id=$1`, task.ID).Scan(&count); err != nil || count != 0 {
						t.Fatal("governance rollback left task or snapshot")
					}
				}
				return util.UUIDToString(task.ID)
			}
			governanceID := enqueueGovernance(true)
			task, err = h.Queries.GetAgentTask(ctx, util.MustParseUUID(governanceID))
			if err != nil {
				t.Fatal(err)
			}
			assertRootBudget(t, task, enabled)
			enqueueGovernance(false)

			itemID := seedQuizItem(t, "budget-quiz-"+label)
			// The HTTP batch endpoint must freeze one independent root scope per run.
			var batch PromptQuizBatchCreateResponse
			testutil.Call(t, h.CreatePromptQuizBatch, newRequest("POST", "/api/prompt-quiz/batches", map[string]any{"agent_ids": []string{agentID}, "item_ids": []string{itemID}})).Want(200).JSON(&batch)
			if batch.Ordered != 1 {
				t.Fatalf("ordered=%d", batch.Ordered)
			}
			var quizID string
			if err := testPool.QueryRow(ctx, `SELECT id::text FROM agent_task_queue WHERE agent_id=$1 AND originator_source='quiz' ORDER BY created_at DESC LIMIT 1`, agentID).Scan(&quizID); err != nil {
				t.Fatal(err)
			}
			task, err = h.Queries.GetAgentTask(ctx, util.MustParseUUID(quizID))
			if err != nil {
				t.Fatal(err)
			}
			assertRootBudget(t, task, enabled)
			if enabled {
				scopes := map[string]bool{}
				for _, rawID := range []string{id, governanceID, quizID} {
					task, err := h.Queries.GetAgentTask(ctx, util.MustParseUUID(rawID))
					if err != nil {
						t.Fatal(err)
					}
					s, err := taskbudget.FromContext(task.Context)
					if err != nil {
						t.Fatal(err)
					}
					scopes[s.ScopeID] = true
				}
				if len(scopes) != 3 {
					t.Fatal("independent root tasks shared scope")
				}
			}
		})
	}
}
