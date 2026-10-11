package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// freshSchedulingPauseCmd returns a standalone cobra command carrying the
// exact flag set the real pause/resume commands register, so flag lookups in
// the runners resolve without leaking state across subtests.
func freshSchedulingPauseCmd(use string, withReason bool) *cobra.Command {
	c := &cobra.Command{Use: use}
	c.Flags().String("output", "json", "")
	if withReason {
		c.Flags().String("reason", "", "")
	}
	return c
}

func freshAgentTasksStatusCmd() *cobra.Command {
	c := &cobra.Command{Use: "tasks"}
	c.Flags().String("output", "table", "")
	c.Flags().String("status", "", "")
	return c
}

// setSchedulingPauseCLIEnv points the runners at the fake server. The mat_
// token clears the daemon-execution-context guard in newAPIClient: this test
// binary may run inside a real daemon task where MULTICA_DAEMON_PORT is set,
// and the guard then demands a task-scoped token.
func setSchedulingPauseCLIEnv(t *testing.T, serverURL string) {
	t.Helper()
	setCLITestServerEnv(t, serverURL)
	t.Setenv("MULTICA_TOKEN", "mat_test-token")
}

// TestRunAgentPausePostsReason pins the wire contract of `multica agent
// pause`: POST to the scheduling-pause endpoint carrying the --reason note,
// with the server's state echoed back through the view struct.
func TestRunAgentPausePostsReason(t *testing.T) {
	var gotReason string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/agents/agent-1/scheduling-pause" {
			t.Errorf("path = %q", r.URL.Path)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotReason, _ = body["reason"].(string)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"paused": true, "scope": "agent", "reason": "drill",
			"created_by": "u-1", "created_at": "2026-01-01T00:00:00Z", "queued_count": 2,
		})
	}))
	defer srv.Close()
	setSchedulingPauseCLIEnv(t, srv.URL)

	cmd := freshSchedulingPauseCmd("pause", true)
	_ = cmd.Flags().Set("reason", "incident drill")
	if err := runAgentPause(cmd, []string{"agent-1"}); err != nil {
		t.Fatalf("runAgentPause: %v", err)
	}
	if gotReason != "incident drill" {
		t.Errorf("server saw reason %q; want the --reason value verbatim", gotReason)
	}
}

// TestRunAgentResumeDeletesAndReportsCount pins the resume contract: DELETE
// (idempotent, no body) against the same endpoint, decoded into the pause
// view — the queued_count read-back is what the resume output reports.
func TestRunAgentResumeDeletesAndReportsCount(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method = %s, want DELETE", r.Method)
		}
		if r.URL.Path != "/api/agents/agent-1/scheduling-pause" {
			t.Errorf("path = %q", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"paused": false, "queued_count": 3})
	}))
	defer srv.Close()
	setSchedulingPauseCLIEnv(t, srv.URL)

	for _, output := range []string{"table", "json"} {
		cmd := freshSchedulingPauseCmd("resume", false)
		_ = cmd.Flags().Set("output", output)
		if err := runAgentResume(cmd, []string{"agent-1"}); err != nil {
			t.Fatalf("runAgentResume (output=%s): %v", output, err)
		}
	}
}

// TestRunAgentTasksStatusFilterAppendsQuery pins the freeze-preview flag:
// `multica agent tasks <id> --status queued` must reach the server as a
// status query parameter (RUYI-608 pre-resume preview), URL-escaped, and
// callers without the flag must keep sending no query string.
func TestRunAgentTasksStatusFilterAppendsQuery(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/agents/agent-1/tasks" {
			t.Errorf("path = %q", r.URL.Path)
		}
		gotQuery = r.URL.RawQuery
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"id": "task-1", "issue_id": "issue-1", "status": "queued", "created_at": "2026-01-01T00:00:00Z"},
		})
	}))
	defer srv.Close()
	setSchedulingPauseCLIEnv(t, srv.URL)

	cmd := freshAgentTasksStatusCmd()
	_ = cmd.Flags().Set("status", "queued")
	if err := runAgentTasks(cmd, []string{"agent-1"}); err != nil {
		t.Fatalf("runAgentTasks: %v", err)
	}
	if gotQuery != "status=queued" {
		t.Errorf("raw query = %q; want status=queued", gotQuery)
	}

	cmd = freshAgentTasksStatusCmd()
	if err := runAgentTasks(cmd, []string{"agent-1"}); err != nil {
		t.Fatalf("runAgentTasks without filter: %v", err)
	}
	if gotQuery != "" {
		t.Errorf("raw query = %q without --status; want empty", gotQuery)
	}
}

// TestRunWorkspacePauseResumeHitsWorkspaceEndpoint pins the workspace-level
// wire contract: with no positional argument the workspace resolves from the
// configured workspace id, and the verbs hit POST/DELETE on the workspace
// pause path in order.
func TestRunWorkspacePauseResumeHitsWorkspaceEndpoint(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path)
		switch r.Method {
		case http.MethodPost:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"paused": true, "scope": "workspace", "reason": "ws drill", "queued_count": 5,
			})
		case http.MethodDelete:
			_ = json.NewEncoder(w).Encode(map[string]any{"paused": false, "queued_count": 5})
		}
	}))
	defer srv.Close()
	setSchedulingPauseCLIEnv(t, srv.URL)

	cmd := freshSchedulingPauseCmd("pause", true)
	_ = cmd.Flags().Set("reason", "ws drill")
	_ = cmd.Flags().Set("output", "table")
	if err := runWorkspacePause(cmd, nil); err != nil {
		t.Fatalf("runWorkspacePause: %v", err)
	}

	cmd = freshSchedulingPauseCmd("resume", false)
	_ = cmd.Flags().Set("output", "table")
	if err := runWorkspaceResume(cmd, nil); err != nil {
		t.Fatalf("runWorkspaceResume: %v", err)
	}

	want := []string{
		"POST /api/workspaces/ws-1/scheduling-pause",
		"DELETE /api/workspaces/ws-1/scheduling-pause",
	}
	if strings.Join(seen, "|") != strings.Join(want, "|") {
		t.Errorf("requests = %v; want %v", seen, want)
	}
}
