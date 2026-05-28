package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/spf13/cobra"
)

func newCommentResolveTestCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "resolve"}
	cmd.Flags().String("output", "json", "")
	return cmd
}

// TestRunCommentResolveCallsResolveEndpoint pins that `comment resolve <id>`
// POSTs to /api/comments/{id}/resolve.
func TestRunCommentResolveCallsResolveEndpoint(t *testing.T) {
	const commentID = "c-123"
	var hitPath, hitMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitPath = r.URL.Path
		hitMethod = r.Method
		json.NewEncoder(w).Encode(map[string]any{"id": commentID, "resolved_at": "2026-01-01T00:00:00Z"})
	}))
	defer srv.Close()

	t.Setenv("MULTICA_SERVER_URL", srv.URL)
	t.Setenv("MULTICA_WORKSPACE_ID", "ws-1")
	t.Setenv("MULTICA_TOKEN", "test-token")

	cmd := newCommentResolveTestCmd()
	if err := runCommentResolve(cmd, []string{commentID}); err != nil {
		t.Fatalf("runCommentResolve: %v", err)
	}
	if hitMethod != http.MethodPost {
		t.Errorf("method = %s, want POST", hitMethod)
	}
	if want := "/api/comments/" + commentID + "/resolve"; hitPath != want {
		t.Errorf("path = %s, want %s", hitPath, want)
	}
}

// TestRunIssueCommentListSendsUnresolvedParam pins that --unresolved is wired to
// the unresolved=true query param.
func TestRunIssueCommentListSendsUnresolvedParam(t *testing.T) {
	const identifier = "MUL-9"
	var commentsQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/issues/" + identifier:
			json.NewEncoder(w).Encode(map[string]any{"id": "issue-9", "identifier": identifier, "title": "t"})
		case "/api/issues/issue-9/comments":
			commentsQuery = r.URL.RawQuery
			json.NewEncoder(w).Encode([]map[string]any{})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	t.Setenv("MULTICA_SERVER_URL", srv.URL)
	t.Setenv("MULTICA_WORKSPACE_ID", "ws-1")
	t.Setenv("MULTICA_TOKEN", "test-token")

	cmd := newIssueCommentListTestCmd()
	_ = cmd.Flags().Set("unresolved", "true")
	if err := runIssueCommentList(cmd, []string{identifier}); err != nil {
		t.Fatalf("runIssueCommentList: %v", err)
	}
	if commentsQuery != "unresolved=true" {
		t.Errorf("comments query = %q, want unresolved=true", commentsQuery)
	}
}

// TestRunIssueCommentListUnresolvedRejectsThreadAndRecent pins the client-side
// combination guard so the user gets a local error instead of a 400.
func TestRunIssueCommentListUnresolvedRejectsThreadAndRecent(t *testing.T) {
	// resolveIssueRef will be called before the guard, so stub a server that
	// resolves the identifier; the guard then fires before any comments call.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"id": "issue-x", "identifier": "MUL-1", "title": "t"})
	}))
	defer srv.Close()
	t.Setenv("MULTICA_SERVER_URL", srv.URL)
	t.Setenv("MULTICA_WORKSPACE_ID", "ws-1")
	t.Setenv("MULTICA_TOKEN", "test-token")

	for _, tc := range []struct {
		name string
		set  func(c *cobra.Command)
	}{
		{"with thread", func(c *cobra.Command) { _ = c.Flags().Set("thread", "00000000-0000-0000-0000-000000000001") }},
		{"with recent", func(c *cobra.Command) { _ = c.Flags().Set("recent", "5") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := newIssueCommentListTestCmd()
			_ = cmd.Flags().Set("unresolved", "true")
			tc.set(cmd)
			if err := runIssueCommentList(cmd, []string{"MUL-1"}); err == nil {
				t.Fatalf("expected error combining --unresolved with %s", tc.name)
			}
		})
	}
}
