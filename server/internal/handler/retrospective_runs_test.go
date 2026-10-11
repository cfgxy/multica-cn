package handler

// Tests for the retrospective run-records surface (RUYI-663): the runs list
// carries the enqueue task id so the UI can offer each run's execution
// transcript. Rows without one — failures recorded before enqueue, and rows
// written before the column existed (935) — must come back as a null that
// the listing tolerates, not an error.

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
)

// insertRetroRun writes one run row directly (no runner) so the response
// shape — not the trigger machinery — is what's under test. Cleanup rides on
// the fixture's workspace teardown.
func insertRetroRun(t *testing.T, wsID string, cols testutil.Cols) string {
	t.Helper()
	base := testutil.Cols{
		"workspace_id": wsID,
		"status":       "succeeded",
		"trigger":      "schedule",
		"window_start": testutil.Raw("now() - interval '2 hours'"),
		"window_end":   testutil.Raw("now() - interval '1 hour'"),
	}
	for k, v := range cols {
		base[k] = v
	}
	return dbfx.Insert(t, "retrospective_run", base)
}

type retroRunResponse struct {
	ID         string         `json:"id"`
	Status     string         `json:"status"`
	TaskID     *string        `json:"task_id"`
	FinishedAt *string        `json:"finished_at"`
	Detail     map[string]any `json:"detail"`
	Error      string         `json:"error"`
}

func listRetroRuns(t *testing.T, wsID, userID string) []retroRunResponse {
	t.Helper()
	code, body := legislationCall(t, testHandler.ListRetrospectiveRuns,
		legislationReq(userID, wsID, http.MethodGet, "/api/retrospective/runs", nil))
	if code != http.StatusOK {
		t.Fatalf("GET runs: expected 200, got %d: %s", code, body)
	}
	var runs []retroRunResponse
	if err := json.Unmarshal([]byte(body), &runs); err != nil {
		t.Fatalf("GET runs body: %v (%s)", err, body)
	}
	return runs
}

func TestRetrospectiveRunsCarryTaskID(t *testing.T) {
	wsID, ownerID, _ := legislationFixture(t)
	taskID := insertRetroRun(t, wsID, testutil.Cols{
		"status":      "succeeded",
		"task_id":     "f47ac10b-58cc-4372-a567-0e02b2c3d479",
		"finished_at": testutil.Raw("now() - interval '30 minutes'"),
		"detail":      testutil.Raw(`'{"issue_ids":["11111111-1111-1111-1111-111111111111"],"analyzed_issue_ids":["22222222-2222-2222-2222-222222222222"]}'::jsonb`),
	})
	insertRetroRun(t, wsID, testutil.Cols{
		"status": "failed",
		// No task: the run failed before enqueue (e.g. agent gate) and rows
		// predating the 935 column never had one.
		"error":  "扫描窗口失败: pq: relation does not exist",
		"detail": testutil.Raw(`'{"reason":{"code":"task_enqueue_failed"}}'::jsonb`),
	})

	runs := listRetroRuns(t, wsID, ownerID)
	if len(runs) != 2 {
		t.Fatalf("expected 2 runs, got %d", len(runs))
	}

	byID := map[string]retroRunResponse{}
	for _, r := range runs {
		byID[r.ID] = r
	}

	enqueued, ok := byID[taskID]
	if !ok {
		t.Fatalf("run %s missing from the listing", taskID)
	}
	if enqueued.TaskID == nil || *enqueued.TaskID != "f47ac10b-58cc-4372-a567-0e02b2c3d479" {
		t.Fatalf("enqueued run must carry its task_id, got %v", enqueued.TaskID)
	}
	if enqueued.FinishedAt == nil {
		t.Fatalf("terminal run must carry finished_at")
	}
	if len(enqueued.Detail["issue_ids"].([]any)) != 1 {
		t.Fatalf("detail.issue_ids must pass through, got %v", enqueued.Detail)
	}

	var failed *retroRunResponse
	for i := range runs {
		if runs[i].ID != taskID {
			failed = &runs[i]
		}
	}
	if failed == nil {
		t.Fatalf("failed run missing from the listing")
	}
	if failed.TaskID != nil {
		t.Fatalf("run without a task must carry a null task_id, got %q", *failed.TaskID)
	}
}
