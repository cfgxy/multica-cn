package handler

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"

	"github.com/multica-ai/multica/server/internal/retrospective"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/internal/util"
)

// TestTaskToResponseRetrospectiveFields covers the RUYI-673 payload fields
// the activity UI renders retrospective runs from: the pure row→response
// mapping must surface the run id and window end carried in the task's
// context JSONB — no DB.
func TestTaskToResponseRetrospectiveFields(t *testing.T) {
	runID := "5b0e0000-0000-4000-8000-000000000001"
	windowEnd := "2026-10-11T00:00:00Z"

	taskCtx, err := json.Marshal(retrospective.TaskContext{
		Kind:        retrospective.TaskKind,
		RunID:       runID,
		WorkspaceID: "11111111-1111-1111-1111-111111111111",
		WindowStart: "2026-10-10T00:00:00Z",
		WindowEnd:   windowEnd,
	})
	if err != nil {
		t.Fatalf("marshal context: %v", err)
	}

	t.Run("retrospective task surfaces run id + window end", func(t *testing.T) {
		resp := taskToResponse(db.AgentTaskQueue{
			OriginatorSource: pgtype.Text{String: retrospective.TaskOriginatorSource, Valid: true},
			Context:          taskCtx,
		}, "ws-1")
		if resp.Kind != retrospective.TaskKind {
			t.Fatalf("kind = %q, want %q", resp.Kind, retrospective.TaskKind)
		}
		if resp.RetrospectiveRunID != runID {
			t.Errorf("retrospective_run_id = %q, want %q", resp.RetrospectiveRunID, runID)
		}
		if resp.RetrospectiveWindowEnd != windowEnd {
			t.Errorf("retrospective_window_end = %q, want %q", resp.RetrospectiveWindowEnd, windowEnd)
		}
	})

	t.Run("non-retrospective task leaves both fields empty", func(t *testing.T) {
		resp := taskToResponse(db.AgentTaskQueue{
			OriginatorSource: pgtype.Text{String: "direct_human", Valid: true},
			Context:          taskCtx,
		}, "ws-1")
		if resp.RetrospectiveRunID != "" || resp.RetrospectiveWindowEnd != "" {
			t.Errorf("fields = %q/%q, want empty for non-retrospective tasks",
				resp.RetrospectiveRunID, resp.RetrospectiveWindowEnd)
		}
	})

	t.Run("retrospective task with unusable context degrades to empty fields", func(t *testing.T) {
		resp := taskToResponse(db.AgentTaskQueue{
			OriginatorSource: pgtype.Text{String: retrospective.TaskOriginatorSource, Valid: true},
			Context:          []byte(`{"kind":"retrospective","run_id":"not-a-uuid"}`),
		}, "ws-1")
		if resp.RetrospectiveRunID != "" || resp.RetrospectiveWindowEnd != "" {
			t.Errorf("fields = %q/%q, want empty when context does not parse",
				resp.RetrospectiveRunID, resp.RetrospectiveWindowEnd)
		}
	})

	t.Run("enqueued run id parses as a uuid (contract with the context writer)", func(t *testing.T) {
		if _, err := util.ParseUUID(runID); err != nil {
			t.Fatalf("fixture run id must be a valid uuid: %v", err)
		}
		if !strings.Contains(string(taskCtx), runID) {
			t.Fatalf("context must embed the run id, got %s", taskCtx)
		}
	})
}

func TestTaskToResponseQuizFields(t *testing.T) {
	const itemID = "5b0e0000-0000-4000-8000-000000000001"
	const batchID = "8a1f0000-0000-4000-8000-000000000001"
	resp := taskToResponse(db.AgentTaskQueue{
		OriginatorSource: pgtype.Text{String: "quiz", Valid: true},
		Context: []byte(`{"kind":"quiz","quiz_item_id":"` + itemID +
			`","quiz_item_title":"稳定性题目","quiz_batch_id":"` + batchID + `"}`),
	}, "ws-1")
	if resp.Kind != "quiz" {
		t.Fatalf("kind = %q, want quiz", resp.Kind)
	}
	if resp.QuizItemID != itemID || resp.QuizItemTitle != "稳定性题目" || resp.QuizBatchID != batchID {
		t.Errorf("quiz fields = %q/%q/%q, want item ID/title/batch ID",
			resp.QuizItemID, resp.QuizItemTitle, resp.QuizBatchID)
	}

	resp = taskToResponse(db.AgentTaskQueue{
		OriginatorSource: pgtype.Text{String: "quiz", Valid: true},
		Context:          []byte(`{"kind":"quiz","quiz_item_id":"` + itemID + `","quiz_batch_id":"` + batchID + `"}`),
	}, "ws-1")
	if resp.Kind != "quiz" || resp.QuizItemID != itemID || resp.QuizBatchID != batchID || resp.QuizItemTitle != "" {
		t.Errorf("legacy quiz context = %q/%q/%q/%q, want kind and IDs preserved without title",
			resp.Kind, resp.QuizItemID, resp.QuizBatchID, resp.QuizItemTitle)
	}

	resp = taskToResponse(db.AgentTaskQueue{
		OriginatorSource: pgtype.Text{String: "quiz", Valid: true},
		Context:          []byte(`{"kind":"quiz","quiz_item_id":"bad","quiz_batch_id":"bad"}`),
	}, "ws-1")
	if resp.QuizItemID != "" || resp.QuizItemTitle != "" || resp.QuizBatchID != "" {
		t.Errorf("invalid quiz context leaked fields: %q/%q/%q",
			resp.QuizItemID, resp.QuizItemTitle, resp.QuizBatchID)
	}
}

func TestHydrateQuizItemTitlesForLegacyTask(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	const slug = "legacy-quiz-task-title-read"
	t.Cleanup(func() {
		testPool.Exec(context.Background(),
			`DELETE FROM prompt_quiz_item WHERE workspace_id = $1 AND slug = $2`, testWorkspaceID, slug)
	})

	var created map[string]any
	testutil.Call(t, testHandler.CreatePromptQuizItem,
		newRequest("POST", "/api/prompt-quiz/items", map[string]any{
			"slug": slug, "title": "旧任务对应的真实题目", "body": "题面",
		})).Want(200).JSON(&created)
	itemID, ok := created["id"].(string)
	if !ok || itemID == "" {
		t.Fatalf("created item id = %v, want string ID", created["id"])
	}

	tasks := []AgentTaskResponse{{
		Kind: "quiz", QuizItemID: itemID,
	}}
	testHandler.hydrateQuizItemTitles(context.Background(), testWorkspaceID, tasks)
	if tasks[0].QuizItemTitle != "旧任务对应的真实题目" {
		t.Fatalf("hydrated title = %q, want current item title", tasks[0].QuizItemTitle)
	}

	if _, err := testPool.Exec(context.Background(),
		`DELETE FROM prompt_quiz_item WHERE workspace_id = $1 AND id = $2::uuid`, testWorkspaceID, itemID); err != nil {
		t.Fatalf("delete quiz item: %v", err)
	}
	tasks[0].QuizItemTitle = ""
	testHandler.hydrateQuizItemTitles(context.Background(), testWorkspaceID, tasks)
	if tasks[0].QuizItemID != itemID || tasks[0].QuizItemTitle != "" {
		t.Errorf("unreadable item metadata = %q/%q, want ID retained and no fabricated title",
			tasks[0].QuizItemID, tasks[0].QuizItemTitle)
	}
}

func TestTaskToResponseExposesOnlyMatchingAdaptationReceipt(t *testing.T) {
	const taskID = "8fcd3a5f-14e9-4d5b-95b9-79b70bb0a146"
	taskContext := []byte(`{"capability_adaptation":{"policy_id":"policy-a","policy_version":"v2","scope":"current_task","trigger":"large task","current_strategy":"one pass","suggested_strategy":"small verified steps","allowed_actions":["checkpoint"]}}`)
	result, err := json.Marshal(map[string]any{
		"capability_adaptation_result": map[string]string{
			"task_id": taskID, "policy_id": "policy-a", "policy_version": "v2", "status": "applied",
		},
	})
	if err != nil {
		t.Fatalf("marshal task result: %v", err)
	}
	resp := taskToResponse(db.AgentTaskQueue{
		ID:      pgtype.UUID{Bytes: [16]byte{0x8f, 0xcd, 0x3a, 0x5f, 0x14, 0xe9, 0x4d, 0x5b, 0x95, 0xb9, 0x79, 0xb7, 0x0b, 0xb0, 0xa1, 0x46}, Valid: true},
		Context: taskContext,
		Result:  result,
	}, "workspace-1")
	if resp.CapabilityAdaptation == nil || resp.CapabilityAdaptation.TaskID != taskID {
		t.Fatalf("adaptation snapshot = %+v, want task-bound snapshot", resp.CapabilityAdaptation)
	}
	if resp.CapabilityAdaptationResult == nil || resp.CapabilityAdaptationResult.Status != "applied" {
		t.Fatalf("adaptation result = %+v, want validated outcome", resp.CapabilityAdaptationResult)
	}

	result, _ = json.Marshal(map[string]any{"capability_adaptation_result": map[string]string{
		"task_id": "11111111-1111-4111-8111-111111111111", "policy_id": "policy-a", "policy_version": "v2", "status": "applied",
	}})
	resp = taskToResponse(db.AgentTaskQueue{
		ID:      pgtype.UUID{Bytes: [16]byte{0x8f, 0xcd, 0x3a, 0x5f, 0x14, 0xe9, 0x4d, 0x5b, 0x95, 0xb9, 0x79, 0xb7, 0x0b, 0xb0, 0xa1, 0x46}, Valid: true},
		Context: taskContext,
		Result:  result,
	}, "workspace-1")
	if resp.CapabilityAdaptation != nil || resp.CapabilityAdaptationResult != nil {
		t.Errorf("mismatched receipt leaked into task details: %+v / %+v", resp.CapabilityAdaptation, resp.CapabilityAdaptationResult)
	}
}
