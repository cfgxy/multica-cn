package execenv

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestPreflightWorktreeFindsDeliveryMarkerAtCheckoutRoot(t *testing.T) {
	t.Setenv("MULTICA_TASK_ID", "")
	requireGit(t)

	checkoutRoot := t.TempDir()
	repo := filepath.Join(checkoutRoot, "checked-out-repository")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatalf("create checkout repository: %v", err)
	}
	gitRun(t, repo, "init", "-b", "main")
	gitRun(t, repo, "config", "user.email", "test@example.com")
	gitRun(t, repo, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("test\n"), 0o644); err != nil {
		t.Fatalf("write repository file: %v", err)
	}
	gitRun(t, repo, "add", "README.md")
	gitRun(t, repo, "commit", "-m", "initial")
	baseCommit := gitRun(t, repo, "rev-parse", "HEAD")
	gitRun(t, repo, "checkout", "-b", "agent/test")

	markerPath := filepath.Join(checkoutRoot, TaskContextMarkerRelPath)
	if err := os.MkdirAll(filepath.Dir(markerPath), 0o755); err != nil {
		t.Fatalf("create checkout root marker directory: %v", err)
	}
	markerData, err := json.Marshal(taskContextMarkerFile{
		ManagedBy:  TaskContextMarkerManagedBy,
		TaskID:     "task-1",
		AgentID:    "agent-1",
		IssueID:    "issue-1",
		Branch:     "agent/test",
		BaseCommit: baseCommit,
	})
	if err != nil {
		t.Fatalf("marshal task marker: %v", err)
	}
	if err := os.WriteFile(markerPath, markerData, 0o644); err != nil {
		t.Fatalf("write checkout root marker: %v", err)
	}

	result, err := PreflightWorktree(repo)
	if err != nil || result.Status != "pass" {
		t.Fatalf("PreflightWorktree = %+v, error = %v; want pass", result, err)
	}
	if result.TaskID != "task-1" || result.ExpectedBranch != "agent/test" || result.BaseCommit != baseCommit {
		t.Fatalf("preflight delivery context = %+v, want checkout root marker context", result)
	}
}
