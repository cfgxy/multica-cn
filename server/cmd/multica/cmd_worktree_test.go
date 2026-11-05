package main

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
		if got["git_root"] != repo {
			t.Fatalf("git_root = %v, want %s (the worktree the checks ran against)", got["git_root"], repo)
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

		got, err := runPreflightCLIWithTaskID(t, repo, "task-2")
		if err == nil {
			t.Fatal("expected branch mismatch failure")
		}
		if got["status"] != "fail" || got["head"] != head || got["managed_branch_tip"] != base {
			t.Fatalf("preflight JSON = %#v, want HEAD %s and managed tip %s", got, head, base)
		}
		if !strings.Contains(err.Error(), "does not match expected branch") {
			t.Fatalf("error = %q, want branch mismatch", err)
		}
		if !hasPreflightSuggestion(got, "branch_mismatch") {
			t.Fatalf("preflight suggestions = %#v, want branch_mismatch", got["suggestions"])
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

		got, err := runPreflightCLIWithTaskID(t, repo, "task-3")
		if err == nil || got["status"] != "fail" || got["base_is_ancestor"] != false {
			t.Fatalf("preflight = %#v, error = %v; want ancestor break", got, err)
		}
		if !strings.Contains(err.Error(), "not proven as an ancestor") {
			t.Fatalf("error = %q, want ancestor break", err)
		}
		if !hasPreflightSuggestion(got, "ancestor_break") {
			t.Fatalf("preflight suggestions = %#v, want ancestor_break", got["suggestions"])
		}
	})

	t.Run("stale complete marker only reports diagnostics", func(t *testing.T) {
		repo, base := newPreflightRepo(t)
		writePreflightMarker(t, repo, "task-a", "agent/stale", base)
		gitPreflight(t, repo, "branch", "agent/stale")

		got, err := runPreflightCLIWithTaskID(t, repo, "task-b")
		if err != nil || got["status"] != "pass" {
			t.Fatalf("preflight = %#v, error = %v; want pass with stale marker warning", got, err)
		}
		if got["expected_branch"] != "agent/stale" || got["base_commit"] != base {
			t.Fatalf("preflight diagnostics = %#v, want stale marker fields retained", got)
		}
		warnings, ok := got["warnings"].([]any)
		if !ok || !strings.Contains(strings.Join(anyStrings(warnings), "\n"), "belongs to task task-a") {
			t.Fatalf("preflight warnings = %#v, want stale task diagnostic", got["warnings"])
		}
		if got["managed_branch_tip"] != nil || got["base_is_ancestor"] != false {
			t.Fatalf("preflight = %#v, stale marker must not drive delivery checks", got)
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

		got, err := runPreflightCLIWithTaskID(t, repo, "task-4")
		if err == nil {
			t.Fatal("expected preflight to reject the unmanaged branch")
		}
		if got["status"] != "fail" || got["base_is_ancestor"] != false {
			t.Fatalf("preflight JSON = %#v, want fail with base_is_ancestor=false", got)
		}
		if got["expected_branch"] != "agent/managed" || got["current_branch"] != "agent/unmanaged" || got["head"] != head || got["managed_branch_tip"] != base {
			t.Fatalf("preflight diagnostics = %#v, want expected/current branch, HEAD %s, and managed tip %s", got, head, base)
		}
		if !hasPreflightSuggestion(got, "branch_mismatch") || !hasPreflightSuggestion(got, "ancestor_break") {
			t.Fatalf("preflight suggestions = %#v, want branch_mismatch and ancestor_break", got["suggestions"])
		}
	})

	t.Run("missing task context reports a warning", func(t *testing.T) {
		repo, _ := newPreflightRepo(t)
		got, err := runPreflightCLI(t, repo)
		if err != nil || got["status"] != "pass" || got["current_branch"] != "agent/managed" || got["head"] == "" {
			t.Fatalf("preflight = %#v, error = %v; want Git-only pass", got, err)
		}
		warnings, _ := got["warnings"].([]any)
		if len(warnings) == 0 || !strings.Contains(warnings[0].(string), "no Multica managed task context") {
			t.Fatalf("preflight warnings = %#v, want missing context diagnosis", got["warnings"])
		}
	})

	t.Run("unresolved merge remains blocking", func(t *testing.T) {
		repo, base := newPreflightRepo(t)
		writePreflightMarker(t, repo, "task-conflict", "agent/managed", base)
		gitPreflight(t, repo, "switch", "-c", "agent/other")
		if err := os.WriteFile(filepath.Join(repo, "initial.txt"), []byte("other\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		gitPreflight(t, repo, "add", "initial.txt")
		gitPreflight(t, repo, "commit", "-m", "other change")
		gitPreflight(t, repo, "switch", "agent/managed")
		if err := os.WriteFile(filepath.Join(repo, "initial.txt"), []byte("managed\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		gitPreflight(t, repo, "add", "initial.txt")
		gitPreflight(t, repo, "commit", "-m", "managed change")
		if _, err := gitPreflightTry(repo, "merge", "agent/other"); err == nil {
			t.Fatal("fixture merge unexpectedly succeeded")
		}

		got, err := runPreflightCLIWithTaskID(t, repo, "task-conflict")
		if err == nil || got["status"] != "fail" {
			t.Fatalf("preflight = %#v, error = %v; want unresolved merge failure", got, err)
		}
		if !strings.Contains(err.Error(), "unresolved merge paths") {
			t.Fatalf("preflight error = %v, want unresolved merge paths", err)
		}
		if !hasPreflightSuggestion(got, "unresolved_merge") {
			t.Fatalf("preflight suggestions = %#v, want unresolved_merge", got["suggestions"])
		}
	})
}

func TestWorktreeAdoptCLISendsTaskBoundLocalRequest(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/worktree/adopt" {
			t.Errorf("request = %s %s, want POST /worktree/adopt", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer task-token" {
			t.Errorf("Authorization = %q", got)
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if body["source_task_id"] != "source-task" || body["branch"] != "agent/source/ruyi-871" || body["expected_remote_sha"] != strings.Repeat("a", 40) {
			t.Errorf("request body = %#v", body)
		}
		if body["workdir"] == "" {
			t.Error("workdir must come from the current directory")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"branch":"agent/source/ruyi-871","remote_sha":"` + strings.Repeat("a", 40) + `","source_task_id":"source-task","continuation_task_id":"target-task","record":"record","base_commit":"` + strings.Repeat("a", 40) + `"}`))
	}))
	srv.Listener = listener
	srv.Start()
	defer srv.Close()

	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("MULTICA_DAEMON_PORT", port)
	t.Setenv("MULTICA_TOKEN", "task-token")
	previousSource, previousBranch, previousSHA := worktreeAdoptSourceTask, worktreeAdoptBranch, worktreeAdoptExpectedRemoteSHA
	t.Cleanup(func() {
		worktreeAdoptSourceTask, worktreeAdoptBranch, worktreeAdoptExpectedRemoteSHA = previousSource, previousBranch, previousSHA
	})
	worktreeAdoptSourceTask = "source-task"
	worktreeAdoptBranch = "agent/source/ruyi-871"
	worktreeAdoptExpectedRemoteSHA = strings.Repeat("a", 40)
	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	if err := runWorktreeAdopt(cmd, nil); err != nil {
		t.Fatalf("runWorktreeAdopt: %v", err)
	}
	var result map[string]string
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode command output: %v", err)
	}
	if result["continuation_task_id"] != "target-task" || result["record"] != "record" {
		t.Fatalf("result = %#v", result)
	}
}

func TestWorktreeAdoptCLIRequiresFlagsAndIsRegistered(t *testing.T) {
	previousSource, previousBranch, previousSHA := worktreeAdoptSourceTask, worktreeAdoptBranch, worktreeAdoptExpectedRemoteSHA
	t.Cleanup(func() {
		worktreeAdoptSourceTask, worktreeAdoptBranch, worktreeAdoptExpectedRemoteSHA = previousSource, previousBranch, previousSHA
	})
	worktreeAdoptSourceTask, worktreeAdoptBranch, worktreeAdoptExpectedRemoteSHA = "", "", ""
	if err := runWorktreeAdopt(&cobra.Command{}, nil); err == nil || !strings.Contains(err.Error(), "--source-task is required") {
		t.Fatalf("missing flags error = %v", err)
	}
	if _, _, err := rootCmd.Find([]string{"worktree", "adopt"}); err != nil {
		t.Fatalf("worktree adopt is not registered on the root CLI: %v", err)
	}
}

// A legacy marker is useful diagnostic context, never a gate for otherwise
// healthy Git delivery.
func TestWorktreePreflightLegacyMarkerShapeWarns(t *testing.T) {
	repo, _ := newPreflightRepo(t)
	path := filepath.Join(repo, execenv.TaskContextMarkerRelPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"managed_by":"multica-daemon-task","task_id":"task-legacy","agent_id":"agent-x","issue_id":"issue-x"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := runPreflightCLI(t, repo)
	if err != nil {
		t.Fatalf("legacy marker should not block preflight: %#v, error=%v", got, err)
	}
	if got["status"] != "pass" || got["git_root"] != repo {
		t.Fatalf("preflight JSON = %#v, want pass with the resolved git root", got)
	}
	warnings, ok := got["warnings"].([]any)
	if !ok || len(warnings) == 0 {
		t.Fatalf("preflight warnings = %#v, want incomplete metadata warning", got["warnings"])
	}
}

func TestWorktreePreflightDoesNotRefreshIndexStatCache(t *testing.T) {
	repo, base := newPreflightRepo(t)
	writePreflightMarker(t, repo, "task-readonly", "agent/managed", base)
	trackedPath := filepath.Join(repo, "initial.txt")
	trackedBefore, err := os.ReadFile(trackedPath)
	if err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(10 * time.Second)
	if err := os.Chtimes(trackedPath, future, future); err != nil {
		t.Fatal(err)
	}

	indexPath := filepath.Join(repo, ".git", "index")
	indexBefore, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	refsBefore := gitPreflight(t, repo, "show-ref", "--head")
	markerPath := filepath.Join(repo, execenv.TaskContextMarkerRelPath)
	markerBefore, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatal(err)
	}

	// A caller setting optional locks must not change preflight's read-only behavior.
	t.Setenv("GIT_OPTIONAL_LOCKS", "1")
	got, err := runPreflightCLI(t, repo)
	if err != nil || got["status"] != "pass" {
		t.Fatalf("preflight = %#v, error = %v; want pass", got, err)
	}

	indexAfter, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(indexBefore, indexAfter) {
		t.Fatal("preflight changed .git/index")
	}
	if refsAfter := gitPreflight(t, repo, "show-ref", "--head"); refsAfter != refsBefore {
		t.Fatalf("preflight changed refs: before %q, after %q", refsBefore, refsAfter)
	}
	trackedAfter, err := os.ReadFile(trackedPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(trackedBefore, trackedAfter) {
		t.Fatal("preflight changed tracked worktree file bytes")
	}
	markerAfter, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(markerBefore, markerAfter) {
		t.Fatal("preflight changed managed marker bytes")
	}
	t.Logf("probe: status=%v index_unchanged=true refs_unchanged=true tracked_file_bytes_unchanged=true marker_bytes_unchanged=true", got["status"])
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
	return runPreflightCLIWithTaskID(t, cwd, "")
}

func runPreflightCLIWithTaskID(t *testing.T, cwd, taskID string) (map[string]any, error) {
	t.Helper()
	t.Setenv("MULTICA_TASK_ID", taskID)
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

func hasPreflightSuggestion(got map[string]any, kind string) bool {
	suggestions, _ := got["suggestions"].([]any)
	for _, raw := range suggestions {
		suggestion, _ := raw.(map[string]any)
		if suggestion["kind"] == kind {
			return true
		}
	}
	return false
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

func gitPreflightTry(repo string, args ...string) (string, error) {
	all := append([]string{"-C", repo}, args...)
	output, err := exec.Command("git", all...).CombinedOutput()
	return strings.TrimSpace(string(output)), err
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
