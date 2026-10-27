package execenv

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestResolveRootDirReadOnly pins the recovery probe's resolver contract
// (RUYI-225): side-effect free when no record exists, exact once the
// production writer installed one, and fail-closed on a record it cannot
// vouch for — never a guessed fallback.
func TestResolveRootDirReadOnly(t *testing.T) {
	root := t.TempDir()
	params := RootDirParams{WorkspacesRoot: root, WorkspaceID: "wsro", TaskID: "taskro"}

	got, err := ResolveRootDirReadOnly(params)
	if err != nil {
		t.Fatalf("missing record: %v", err)
	}
	if got != "" {
		t.Fatalf("missing record resolved to %q, want empty", got)
	}
	if _, statErr := os.Stat(filepath.Join(root, taskRootIndexDir)); !os.IsNotExist(statErr) {
		t.Fatalf("read-only resolve created the record index")
	}

	envRoot := filepath.Join(root, "wsro", "taskro")
	if err := os.MkdirAll(envRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	owner := `{"workspace_id":"wsro","task_id":"taskro"}`
	if err := os.WriteFile(filepath.Join(envRoot, ".task_owner"), []byte(owner), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveRootDir(params); err != nil {
		t.Fatalf("install record: %v", err)
	}

	got, err = ResolveRootDirReadOnly(params)
	if err != nil || got != envRoot {
		t.Fatalf("ResolveRootDirReadOnly = (%q, %v), want (%q, nil)", got, err, envRoot)
	}

	recordDir := taskRootRecordDir(params)
	bad := taskRootRecord{WorkspaceID: "ws-other", TaskID: "task-other", RelativePath: "wsro/taskro"}
	data, err := json.Marshal(bad)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(recordDir, taskRootRecordFile), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := ResolveRootDirReadOnly(params); err == nil {
		t.Fatalf("mismatched record resolved to %q without error", got)
	}
}
