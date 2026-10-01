package service

// RUYI-286 rework: a quiz run's agent_task_queue row carries no issue / chat /
// autopilot link, and until the rework ResolveTaskWorkspaceID knew nothing
// about its context payload, so it returned "". requireDaemonTaskAccessWith-
// Workspace turns "" into 404 "task not found" — every daemon endpoint a
// claimed run calls (/start, /progress, /complete, /fail) rejected, and the
// QA batch's 36 ordered runs all stuck at dispatched. The resolver now follows
// the context's quiz_item_id to prompt_quiz_item.workspace_id, the same
// recovery quick-create got for its context workspace_id. These tests pin
// that resolution against the payload the sweep and the batch endpoint
// actually produce (promptquizsweep.TaskContextPayload), not a hand-written
// lookalike, so payload drift fails here instead of on a daemon in the field.

import (
	"context"
	"fmt"
	"testing"
	"time"

	pgtypeuuid "github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/promptquizsweep"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// quizResolutionFixture seeds one workspace with an agent on an online runtime
// and one quiz item — the minimum the resolver reads through.
type quizResolutionFixture struct {
	svc         *TaskService
	fx          *testutil.Fixture
	pool        *pgxpool.Pool
	workspaceID string
	runtimeID   string
	agentID     string
}

func newQuizResolutionFixture(t *testing.T) quizResolutionFixture {
	t.Helper()
	pool := newResolveOriginatorPool(t)
	suffix := time.Now().UnixNano()
	bootstrap := testutil.New(pool, "", "")
	userID := bootstrap.User(t,
		fmt.Sprintf("quiz-res-user-%d", suffix),
		fmt.Sprintf("quiz-res-user-%d@example.com", suffix),
	)
	workspaceID := bootstrap.Workspace(t,
		fmt.Sprintf("quiz-res-%d", suffix),
		fmt.Sprintf("quiz-res-%d", suffix),
	)
	fx := testutil.New(pool, workspaceID, userID)
	runtimeID := fx.Runtime(t, "quiz-res-runtime")
	agentID := fx.Agent(t, "quiz-res-agent", runtimeID)
	return quizResolutionFixture{
		svc:         NewTaskService(db.New(pool), pool, nil, events.New()),
		fx:          fx,
		pool:        pool,
		workspaceID: workspaceID,
		runtimeID:   runtimeID,
		agentID:     agentID,
	}
}

// quizTask queues a task shaped exactly like CreatePromptQuizTask writes it:
// no issue link, originator_source='quiz', and the context payload the two
// production ordering paths (sweep tick + batch endpoint) share.
func (f quizResolutionFixture) quizTask(t *testing.T, itemID string) string {
	t.Helper()
	context, err := promptquizsweep.TaskContextPayload(itemID, "quiz resolution regression body", pgtype.UUID{})
	if err != nil {
		t.Fatalf("build quiz context: %v", err)
	}
	return f.fx.Task(t, f.agentID, testutil.Cols{
		"runtime_id":              f.runtimeID,
		"context":                 context,
		"originator_source":       "quiz",
		"trigger_evidence_kind":   "quiz_item",
		"trigger_evidence_ref_id": itemID,
	})
}

func TestResolveTaskWorkspaceIDQuizContext(t *testing.T) {
	ctx := context.Background()

	t.Run("quiz context resolves through the item's workspace", func(t *testing.T) {
		fx := newQuizResolutionFixture(t)
		itemID := fx.fx.Insert(t, "prompt_quiz_item", testutil.Cols{
			"workspace_id": fx.workspaceID,
			"slug":         "quiz-res-item",
			"title":        "Quiz resolution regression",
			"body":         "What is the capital of France?",
		})
		taskID := fx.quizTask(t, itemID)

		task, err := db.New(fx.pool).GetAgentTask(ctx, mustTaskUUID(t, taskID))
		if err != nil {
			t.Fatalf("load task: %v", err)
		}
		got := fx.svc.ResolveTaskWorkspaceID(ctx, task)
		if got != fx.workspaceID {
			t.Fatalf("ResolveTaskWorkspaceID(quiz task) = %q, want workspace %q — an empty string here 404s every daemon call the run makes", got, fx.workspaceID)
		}
	})

	t.Run("quiz context whose item is gone stays unresolvable", func(t *testing.T) {
		fx := newQuizResolutionFixture(t)
		itemID := fx.fx.Insert(t, "prompt_quiz_item", testutil.Cols{
			"workspace_id": fx.workspaceID,
			"slug":         "quiz-res-deleted",
			"title":        "Deleted item",
			"body":         "Gone.",
		})
		taskID := fx.quizTask(t, itemID)
		if _, err := fx.pool.Exec(ctx, `DELETE FROM prompt_quiz_item WHERE id = $1`, mustTaskUUID(t, itemID)); err != nil {
			t.Fatalf("delete item: %v", err)
		}

		task, err := db.New(fx.pool).GetAgentTask(ctx, mustTaskUUID(t, taskID))
		if err != nil {
			t.Fatalf("load task: %v", err)
		}
		// The item is the workspace anchor — the same authority the batch
		// endpoint and the sweep order runs under — so an item whose row is
		// gone leaves the task unresolvable ("not found" to the daemon),
		// never attributable to another workspace.
		if got := fx.svc.ResolveTaskWorkspaceID(ctx, task); got != "" {
			t.Fatalf("ResolveTaskWorkspaceID(quiz task, item deleted) = %q, want empty", got)
		}
	})

	t.Run("unrelated no-link context still returns empty", func(t *testing.T) {
		fx := newQuizResolutionFixture(t)
		taskID := fx.fx.Task(t, fx.agentID, testutil.Cols{
			"runtime_id": fx.runtimeID,
			"context":    []byte(`{"kind":"something_else","note":"not a quiz"}`),
		})

		task, err := db.New(fx.pool).GetAgentTask(ctx, mustTaskUUID(t, taskID))
		if err != nil {
			t.Fatalf("load task: %v", err)
		}
		if got := fx.svc.ResolveTaskWorkspaceID(ctx, task); got != "" {
			t.Fatalf("ResolveTaskWorkspaceID(unrelated context) = %q, want empty", got)
		}
	})
}

func mustTaskUUID(t *testing.T, raw string) pgtype.UUID {
	t.Helper()
	id, err := pgtypeuuid.Parse(raw)
	if err != nil {
		t.Fatalf("parse uuid %q: %v", raw, err)
	}
	return pgtype.UUID{Bytes: id, Valid: true}
}
