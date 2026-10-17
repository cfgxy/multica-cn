package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

func TestWorktreePreflightCLIOutputAndGuards(t *testing.T) {
	t.Run("managed branch passes and dirty files are reported", func(t *testing.T) {
		repo, base := newPreflightRepo(t)
		writePreflightMarker(t, repo, "task-1", "agent/managed", base)
		if err := os.WriteFile(filepath.Join(repo, "notes.txt"), []byte("scratch\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		got, err := runPreflightCLI(t, repo)
		if err != nil {
			t.Fatalf("runWorktreePreflight: %v", err)
		}
		if got["status"] != "pass" || got["expected_branch"] != "agent/managed" || got["current_branch"] != "agent/managed" {
			t.Fatalf("preflight JSON = %#v", got)
		}
		status, ok := got["worktree_status"].([]any)
		if !ok || !strings.Contains(strings.Join(anyStrings(status), "\n"), "notes.txt") {
			t.Fatalf("worktree_status = %#v, want notes.txt", got["worktree_status"])
		}
	})

	t.Run("origin main advancing does not affect task branch proof", func(t *testing.T) {
		repo, base := newPreflightRepo(t)
		writePreflightMarker(t, repo, "task-mainline", "agent/managed", base)
		gitPreflight(t, repo, "switch", "-c", "agent/mainline")
		if err := os.WriteFile(filepath.Join(repo, "mainline.txt"), []byte("advanced origin\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		gitPreflight(t, repo, "add", "mainline.txt")
		gitPreflight(t, repo, "commit", "-m", "advance origin main")
		advanced := gitPreflight(t, repo, "rev-parse", "HEAD")
		gitPreflight(t, repo, "update-ref", "refs/remotes/origin/main", advanced)
		gitPreflight(t, repo, "switch", "agent/managed")

		got, err := runPreflightCLI(t, repo)
		if err != nil || got["status"] != "pass" || got["managed_branch_tip"] != base {
			t.Fatalf("preflight = %#v, error = %v; want pass on managed base despite origin/main at %s", got, err, advanced)
		}
	})

	t.Run("mainline checkout fails with both tips", func(t *testing.T) {
		repo, base := newPreflightRepo(t)
		writePreflightMarker(t, repo, "task-2", "agent/managed", base)
		gitPreflight(t, repo, "switch", "-c", "agent/ruyi-mainline")
		if err := os.WriteFile(filepath.Join(repo, "change.txt"), []byte("new\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		gitPreflight(t, repo, "add", "change.txt")
		gitPreflight(t, repo, "commit", "-m", "mainline change")
		head := gitPreflight(t, repo, "rev-parse", "HEAD")

		got, err := runPreflightCLI(t, repo)
		if err == nil {
			t.Fatal("expected branch mismatch failure")
		}
		if got["status"] != "fail" || got["head"] != head || got["managed_branch_tip"] != base {
			t.Fatalf("preflight JSON = %#v, want HEAD %s and managed tip %s", got, head, base)
		}
		if !strings.Contains(err.Error(), "does not match expected branch") {
			t.Fatalf("error = %q, want branch mismatch", err)
		}
	})

	t.Run("ancestor break fails", func(t *testing.T) {
		repo, base := newPreflightRepo(t)
		writePreflightMarker(t, repo, "task-3", "agent/managed", base)
		gitPreflight(t, repo, "switch", "--orphan", "agent/other-root")
		if err := os.WriteFile(filepath.Join(repo, "unrelated.txt"), []byte("unrelated\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		gitPreflight(t, repo, "add", "unrelated.txt")
		gitPreflight(t, repo, "commit", "-m", "unrelated root")
		unrelated := gitPreflight(t, repo, "rev-parse", "HEAD")
		gitPreflight(t, repo, "branch", "-f", "agent/managed", unrelated)
		gitPreflight(t, repo, "switch", "agent/managed")

		got, err := runPreflightCLI(t, repo)
		if err == nil || got["status"] != "fail" || got["base_is_ancestor"] != false {
			t.Fatalf("preflight = %#v, error = %v; want ancestor break", got, err)
		}
		if !strings.Contains(err.Error(), "not proven as an ancestor") {
			t.Fatalf("error = %q, want ancestor break", err)
		}
	})

	t.Run("base ancestry is checked against current HEAD when on an unmanaged branch", func(t *testing.T) {
		repo, base := newPreflightRepo(t)
		writePreflightMarker(t, repo, "task-4", "agent/managed", base)
		gitPreflight(t, repo, "switch", "--orphan", "agent/unmanaged")
		if err := os.WriteFile(filepath.Join(repo, "unrelated.txt"), []byte("unrelated\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		gitPreflight(t, repo, "add", "unrelated.txt")
		gitPreflight(t, repo, "commit", "-m", "unrelated root")
		head := gitPreflight(t, repo, "rev-parse", "HEAD")

		got, err := runPreflightCLI(t, repo)
		if err == nil {
			t.Fatal("expected preflight to reject the unmanaged branch")
		}
		if got["status"] != "fail" || got["base_is_ancestor"] != false {
			t.Fatalf("preflight JSON = %#v, want fail with base_is_ancestor=false", got)
		}
		if got["expected_branch"] != "agent/managed" || got["current_branch"] != "agent/unmanaged" || got["head"] != head || got["managed_branch_tip"] != base {
			t.Fatalf("preflight diagnostics = %#v, want expected/current branch, HEAD %s, and managed tip %s", got, head, base)
		}
	})

	t.Run("missing task context fails closed", func(t *testing.T) {
		repo, _ := newPreflightRepo(t)
		got, err := runPreflightCLI(t, repo)
		if err == nil || got["status"] != "fail" || got["current_branch"] != "agent/managed" || got["head"] == "" || !strings.Contains(err.Error(), "no Multica managed task context") {
			t.Fatalf("preflight = %#v, error = %v; want missing context failure", got, err)
		}
	})
}

func TestWorktreePreflightRejectsArguments(t *testing.T) {
	if err := worktreePreflightCmd.Args(worktreePreflightCmd, []string{"unexpected"}); err == nil {
		t.Fatal("expected preflight command to reject positional arguments")
	}
	if _, _, err := rootCmd.Find([]string{"worktree", "preflight"}); err != nil {
		t.Fatalf("worktree preflight is not registered on the root CLI: %v", err)
	}
}

func runPreflightCLI(t *testing.T, cwd string) (map[string]any, error) {
	t.Helper()
	t.Setenv("MULTICA_TASK_ID", "")
	original, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(original); err != nil {
			t.Errorf("restore current directory: %v", err)
		}
	})
	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	err = runWorktreePreflight(cmd, nil)
	var got map[string]any
	if decodeErr := json.Unmarshal(out.Bytes(), &got); decodeErr != nil {
		t.Fatalf("decode preflight JSON %q: %v", out.String(), decodeErr)
	}
	return got, err
}

func newPreflightRepo(t *testing.T) (string, string) {
	t.Helper()
	repo := t.TempDir()
	gitPreflight(t, repo, "init", "-b", "agent/managed")
	gitPreflight(t, repo, "config", "user.name", "Preflight Test")
	gitPreflight(t, repo, "config", "user.email", "preflight@example.invalid")
	if err := os.WriteFile(filepath.Join(repo, "initial.txt"), []byte("initial\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitPreflight(t, repo, "add", "initial.txt")
	gitPreflight(t, repo, "commit", "-m", "initial")
	return repo, gitPreflight(t, repo, "rev-parse", "HEAD")
}

func writePreflightMarker(t *testing.T, repo, taskID, branch, base string) {
	t.Helper()
	path := filepath.Join(repo, execenv.TaskContextMarkerRelPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data := `{"managed_by":"` + execenv.TaskContextMarkerManagedBy + `","task_id":"` + taskID + `","branch":"` + branch + `","base_commit":"` + base + `"}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func gitPreflight(t *testing.T, repo string, args ...string) string {
	t.Helper()
	all := append([]string{"-C", repo}, args...)
	cmd := exec.Command("git", all...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}

func anyStrings(values []any) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if text, ok := value.(string); ok {
			result = append(result, text)
		}
	}
	return result
}
