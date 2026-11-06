package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func newIssueNoActionTestCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "no-action"}
	cmd.Flags().String("reason", "", "")
	cmd.Flags().String("output", "table", "")
	return cmd
}

func TestRunIssueNoActionPostsReason(t *testing.T) {
	issueID := "11111111-2222-3333-4444-555555555555"
	var gotReason string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/issues/"+issueID+"/no-action" {
			http.NotFound(w, r)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		gotReason, _ = body["reason"].(string)
		_ = json.NewEncoder(w).Encode(map[string]any{"task_id": "task-1", "reason": gotReason})
	}))
	defer srv.Close()
	t.Setenv("MULTICA_SERVER_URL", srv.URL)
	t.Setenv("MULTICA_WORKSPACE_ID", "workspace-1")
	t.Setenv("MULTICA_TOKEN", "test-token")

	cmd := newIssueNoActionTestCmd()
	_ = cmd.Flags().Set("reason", "nothing material changed")
	if err := runIssueNoAction(cmd, []string{issueID}); err != nil {
		t.Fatalf("runIssueNoAction: %v", err)
	}
	if gotReason != "nothing material changed" {
		t.Fatalf("reason = %q", gotReason)
	}
}

func TestWarnUnhandledCommentTriggersReportsSavedButBlocked(t *testing.T) {
	capture := captureStderr(t)
	warnUnhandledCommentTriggers(map[string]any{"trigger_outcomes": []any{
		map[string]any{
			"target_type": "agent",
			"target_id":   "agent-1",
			"status":      "blocked",
			"reason_code": "task_role_conflict",
		},
	}})
	got := capture.read()
	for _, want := range []string{"comment was saved", "agent-1", "task_role_conflict"} {
		if !strings.Contains(got, want) {
			t.Fatalf("warning missing %q: %s", want, got)
		}
	}
}

func TestWarnUnhandledCommentTriggersReportsSavedButUnparsedMention(t *testing.T) {
	capture := captureStderr(t)
	warnUnhandledCommentTriggers(map[string]any{"invalid_mentions": []any{
		map[string]any{"start": float64(3), "end": float64(40)},
	}})
	got := capture.read()
	for _, want := range []string{"comment was saved", "[3,40)", "not recognized"} {
		if !strings.Contains(got, want) {
			t.Fatalf("warning missing %q: %s", want, got)
		}
	}
}
