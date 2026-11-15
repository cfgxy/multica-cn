package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

func TestWorktreeRetainCommandRecordsAndReleasesCurrentTaskDependency(t *testing.T) {
	root := t.TempDir()
	raw, _ := json.Marshal(execenv.EnvRootOwner{TaskID: "task-a", WorkspaceID: "workspace-a"})
	if err := os.WriteFile(filepath.Join(root, ".task_owner"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MULTICA_TASK_ID", "task-a")
	t.Setenv("MULTICA_WORKSPACE_ID", "workspace-a")
	cmd := newWorktreeRetainCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--env-root", root, "--owner", "QA", "--reason", "remaining verification", "--version", strings.Repeat("c", 40), "--evidence", "rebuild inputs", "--until", time.Now().Add(time.Hour).UTC().Format(time.RFC3339)})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var hold execenv.EnvironmentRetention
	if err := json.Unmarshal(out.Bytes(), &hold); err != nil || hold.Owner != "QA" {
		t.Fatalf("hold output %s, error %v", out.String(), err)
	}
	cmd = newWorktreeRetainCommand()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--env-root", root, "--release"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if retained, err := execenv.EnvironmentRetained(root, time.Now()); err != nil || retained {
		t.Fatalf("release retained=%v err=%v", retained, err)
	}
}
