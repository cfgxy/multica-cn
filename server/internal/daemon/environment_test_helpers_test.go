package daemon

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func worktreeTestDaemon(t *testing.T) *Daemon {
	t.Helper()
	return newGCTestDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"completed"}`))
	}))
}

func worktreeTestGit(t *testing.T, path string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", path}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func createWorktreeTestRepo(t *testing.T) string {
	t.Helper()
	path := t.TempDir()
	worktreeTestGit(t, path, "init", "-b", "main")
	worktreeTestGit(t, path, "config", "user.name", "Test")
	worktreeTestGit(t, path, "config", "user.email", "test@example.com")
	worktreeTestGit(t, path, "commit", "--allow-empty", "-m", "base")
	return path
}

func writeLifecycleFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestEnvironmentManagerRejectsUnauthorizedRequests(t *testing.T) {
	d := worktreeTestDaemon(t)
	for _, tc := range []struct{ token, origin, profile string }{{}, {token: "wrong"}, {token: "test-token", origin: "https://example.test"}, {token: "test-token", profile: "other"}} {
		req := httptest.NewRequest(http.MethodGet, "/worktrees", nil)
		req.Header.Set("Authorization", "Bearer "+tc.token)
		req.Header.Set("Origin", tc.origin)
		req.Header.Set("X-Multica-Profile", tc.profile)
		response := httptest.NewRecorder()
		d.worktreeManagerHandler()(response, req)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("unauthorized request accepted: %d", response.Code)
		}
	}
}
