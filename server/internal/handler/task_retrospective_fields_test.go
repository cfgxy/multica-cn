package handler

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"

	"github.com/multica-ai/multica/server/internal/retrospective"
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
