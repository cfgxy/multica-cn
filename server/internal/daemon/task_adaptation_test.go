package daemon

import (
	"context"
	"log/slog"
	"net/http/httptest"
	"testing"

	"github.com/multica-ai/multica/server/pkg/taskadaptation"
)

func TestReportTaskResult_RecordsTaskAdaptationAndStripsReceipt(t *testing.T) {
	t.Parallel()
	rec := &reportTaskResultRecorder{}
	srv := httptest.NewServer(rec.handler(t))
	t.Cleanup(srv.Close)
	d := &Daemon{client: NewClient(srv.URL), logger: slog.Default()}
	snapshot := &taskadaptation.Snapshot{TaskID: "task-adapted", PolicyID: "policy-1", PolicyVersion: "v1", Scope: "current_task", Trigger: "trigger", CurrentStrategy: "current", SuggestedStrategy: "suggested", AllowedActions: []string{"bounded"}}
	d.reportTaskResult(context.Background(), "task-adapted", TaskResult{
		Status:  "completed",
		Comment: "work done\n<!-- multica-capability-adaptation: task-adapted: applied -->\n",
	}, slog.Default(), snapshot)

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if rec.payload["output"] != "work done" {
		t.Fatalf("user-visible output retained receipt: %#v", rec.payload["output"])
	}
	outcome, ok := rec.payload["capability_adaptation_result"].(map[string]any)
	if !ok || outcome["task_id"] != "task-adapted" || outcome["policy_id"] != "policy-1" || outcome["policy_version"] != "v1" || outcome["status"] != "applied" {
		t.Fatalf("adaptation result = %#v", rec.payload["capability_adaptation_result"])
	}
}
