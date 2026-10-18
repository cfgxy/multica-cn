package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/redact"
)

func TestParseRunActivityCursorAndLimit(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		want      int32
		wantSet   bool
		wantError bool
	}{
		{name: "omitted cursor"},
		{name: "zero cursor", input: "0", wantSet: true},
		{name: "positive cursor", input: "17", want: 17, wantSet: true},
		{name: "negative cursor", input: "-1", wantError: true},
		{name: "overflow cursor", input: "2147483648", wantError: true},
		{name: "fractional cursor", input: "1.5", wantError: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, set, err := parseRunActivityAfterSeq(tt.input)
			if (err != nil) != tt.wantError || got != tt.want || set != tt.wantSet {
				t.Fatalf("parseRunActivityAfterSeq(%q) = (%d, %t, %v)", tt.input, got, set, err)
			}
		})
	}

	for _, tt := range []struct {
		input string
		want  int
		bad   bool
	}{{"", runActivityDefaultLimit, false}, {"1", 1, false}, {"100", 100, false}, {"0", 0, true}, {"101", 0, true}, {"x", 0, true}} {
		got, err := parseRunActivityLimit(tt.input)
		if (err != nil) != tt.bad || got != tt.want {
			t.Fatalf("parseRunActivityLimit(%q) = (%d, %v)", tt.input, got, err)
		}
	}
}

func TestGetRunActivitySerializesRateLimitAndAllowsRetryAfterWindow(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	issueID, taskID := createRunActivityTestIssueTask(t)
	start := make(chan struct{})
	statuses := make(chan int, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			statuses <- getRunActivityStatus(testHandler, issueID, taskID)
		}()
	}
	close(start)
	wg.Wait()
	close(statuses)
	counts := map[int]int{}
	for status := range statuses {
		counts[status]++
	}
	if counts[http.StatusOK] != 1 || counts[http.StatusTooManyRequests] != 1 {
		t.Fatalf("concurrent statuses = %v, want one 200 and one 429", counts)
	}

	if _, err := testPool.Exec(context.Background(), `
		UPDATE audit_event
		SET occurred_at = now() - interval '11 seconds'
		WHERE domain = 'run' AND event_type = 'run.activity_read'
		  AND task_id = $1 AND details->>'result' = 'success'
	`, taskID); err != nil {
		t.Fatalf("expire success audit window: %v", err)
	}
	if got := getRunActivityStatus(testHandler, issueID, taskID); got != http.StatusOK {
		t.Fatalf("status after 10-second window = %d, want 200", got)
	}
}

func TestGetRunActivityAuditsUnresolvedRunsWithoutLeakingRunIdentity(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	issueID, _ := createRunActivityTestIssueTask(t)
	otherIssueID, otherTaskID := createRunActivityTestIssueTask(t)
	for _, taskID := range []string{uuid.NewString(), otherTaskID} {
		w := httptest.NewRecorder()
		req := newRequestAs(testUserID, http.MethodGet, "/api/issues/"+issueID+"/tasks/"+taskID+"/activity", nil)
		req = withURLParams(req, "id", issueID, "taskId", taskID)
		testHandler.GetRunActivity(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("run %s response = %d, want 404", taskID, w.Code)
		}
		if strings.Contains(w.Body.String(), taskID) || strings.Contains(w.Body.String(), otherIssueID) {
			t.Fatalf("rejection disclosed run identity: %s", w.Body.String())
		}
	}
	var rejectionCount int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FROM audit_event
		WHERE domain = 'run' AND event_type = 'run.activity_read'
		  AND issue_id = $1 AND task_id IS NULL AND details->>'result' = 'not_found'
	`, issueID).Scan(&rejectionCount); err != nil {
		t.Fatalf("read rejection audit count: %v", err)
	}
	if rejectionCount != 2 {
		t.Fatalf("rejection audit count = %d, want 2", rejectionCount)
	}
}

func TestGetRunActivityFailsClosedWhenRejectionAuditFails(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	issueID, _ := createRunActivityTestIssueTask(t)
	h := *testHandler
	h.runActivityAuditWriter = func(*http.Request, *db.Queries, pgtype.UUID, pgtype.UUID, pgtype.UUID, pgtype.UUID, string, int32, int, string, runActivityResponse, int, string) bool {
		return false
	}
	taskID := uuid.NewString()
	w := httptest.NewRecorder()
	req := newRequestAs(testUserID, http.MethodGet, "/api/issues/"+issueID+"/tasks/"+taskID+"/activity", nil)
	req = withURLParams(req, "id", issueID, "taskId", taskID)
	h.GetRunActivity(w, req)
	if w.Code != http.StatusServiceUnavailable || strings.Contains(w.Body.String(), "messages") || strings.Contains(w.Body.String(), taskID) {
		t.Fatalf("audit failure response = %d %q, want body-free 503", w.Code, w.Body.String())
	}
}

func createRunActivityTestIssueTask(t *testing.T) (string, string) {
	t.Helper()
	ctx := context.Background()
	var agentID, runtimeID string
	if err := testPool.QueryRow(ctx, `SELECT id, runtime_id FROM agent WHERE workspace_id = $1 AND name = 'Handler Test Agent'`, testWorkspaceID).Scan(&agentID, &runtimeID); err != nil {
		t.Fatalf("find handler test agent: %v", err)
	}
	var issueID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, title, status, priority, creator_type, creator_id, number, position)
		SELECT $1, 'run activity test', 'todo', 'none', 'member', $2,
		       COALESCE(max(number), 0) + 1, COALESCE(max(position), 0) + 1
		FROM issue WHERE workspace_id = $1
		RETURNING id
	`, testWorkspaceID, testUserID).Scan(&issueID); err != nil {
		t.Fatalf("create run activity issue: %v", err)
	}
	var taskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (agent_id, runtime_id, issue_id, status, priority)
		VALUES ($1, $2, $3, 'queued', 0) RETURNING id
	`, agentID, runtimeID, issueID).Scan(&taskID); err != nil {
		t.Fatalf("create run activity task: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM audit_event WHERE issue_id = $1`, issueID)
		testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE issue_id = $1`, issueID)
		testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issueID)
	})
	return issueID, taskID
}

func getRunActivityStatus(h *Handler, issueID, taskID string) int {
	w := httptest.NewRecorder()
	req := newRequestAs(testUserID, http.MethodGet, "/api/issues/"+issueID+"/tasks/"+taskID+"/activity", nil)
	req = withURLParams(req, "id", issueID, "taskId", taskID)
	h.GetRunActivity(w, req)
	return w.Code
}

func TestRunActivityProgressSeparatesSnapshotAndNewEvidence(t *testing.T) {
	created := pgtype.Timestamptz{Time: time.Date(2026, 10, 17, 1, 0, 0, 0, time.UTC), Valid: true}
	latest := db.ListTaskMessagesPageRow{Seq: 8, CreatedAt: created}
	newMessage := db.ListTaskMessagesPageRow{Seq: 9, CreatedAt: created}
	baseTask := db.AgentTaskQueue{Status: "running", CreatedAt: created}

	tests := []struct {
		name      string
		task      db.AgentTaskQueue
		rows      []db.ListTaskMessagesPageRow
		last      pgtype.Timestamptz
		hasLatest bool
		cursor    bool
		after     int32
		want      string
	}{
		{name: "first snapshot does not imply progress", task: baseTask, rows: []db.ListTaskMessagesPageRow{latest}, last: created, hasLatest: true, want: "no_recent_evidence"},
		{name: "new sequence is progress", task: baseTask, rows: []db.ListTaskMessagesPageRow{newMessage}, last: created, hasLatest: true, cursor: true, after: 8, want: "advancing"},
		{name: "running without any trace is unknown", task: baseTask, want: "unknown"},
		{name: "terminal status wins", task: db.AgentTaskQueue{Status: "completed"}, want: "finished"},
		{name: "explicit wait wins", task: db.AgentTaskQueue{Status: "running", WaitReason: pgtype.Text{String: "waiting for worker", Valid: true}}, want: "waiting"},
		{name: "waiting state is explicit", task: db.AgentTaskQueue{Status: "waiting_local_directory"}, want: "waiting"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildRunActivityResponse(tt.task, tt.rows, tt.last, tt.hasLatest, tt.after, tt.cursor, false)
			if got.ProgressAssessment.Status != tt.want {
				t.Fatalf("assessment = %q, want %q", got.ProgressAssessment.Status, tt.want)
			}
			if got.WorkerEvidence.ProcessLiveness != "unknown" || got.RunState.Status == "running" && got.ProgressAssessment.Status == "advancing" && !tt.cursor {
				t.Fatalf("worker/status evidence was conflated: %+v", got)
			}
		})
	}
}

func TestRunActivityWorkerDeltaAdvancesOnlyAfterCursor(t *testing.T) {
	tests := []struct {
		name      string
		status    string
		hadCursor bool
		stdout    string
		stderr    string
		want      string
	}{
		{name: "new worker bytes advance", status: "no_recent_evidence", hadCursor: true, stdout: "output", want: "advancing"},
		{name: "first snapshot is baseline", status: "no_recent_evidence", stdout: "initial", want: "no_recent_evidence"},
		{name: "unchanged cursor has no delta", status: "no_recent_evidence", hadCursor: true, want: "no_recent_evidence"},
		{name: "finished remains finished", status: "finished", hadCursor: true, stderr: "exit", want: "finished"},
		{name: "waiting remains waiting", status: "waiting", hadCursor: true, stdout: "output", want: "waiting"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := runActivityResponse{}
			resp.ProgressAssessment.Status = tt.status
			applyWorkerEvidenceProgress(&resp, tt.hadCursor, tt.stdout, tt.stderr)
			if resp.ProgressAssessment.Status != tt.want {
				t.Fatalf("status = %q, want %q", resp.ProgressAssessment.Status, tt.want)
			}
			if tt.want == "advancing" && len(resp.ProgressAssessment.Basis) != 1 || tt.want != "advancing" && len(resp.ProgressAssessment.Basis) != 0 {
				t.Fatalf("basis = %v", resp.ProgressAssessment.Basis)
			}
		})
	}
}

func TestRunActivityMessageIsRedactedAndBounded(t *testing.T) {
	secret := "ghp_" + strings.Repeat("a", 36)
	message := boundRunActivityMessage(runActivityMessage{
		Seq: 1, Type: "tool_result", Content: redact.Text(strings.Repeat("x", 9000) + " " + secret),
		Output: strings.Repeat("y", 9000),
		Input:  redact.InputMap(map[string]any{"token": secret}),
	})
	encoded, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > runActivityMessageBytes {
		t.Fatalf("encoded message is %d bytes, limit is %d", len(encoded), runActivityMessageBytes)
	}
	if !message.Truncated || strings.Contains(string(encoded), secret) {
		t.Fatalf("message not truncated/redacted: %s", encoded)
	}
}
