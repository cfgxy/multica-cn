//go:build windows

package execenv

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRepairTaskDeliveryContextOnWindows(t *testing.T) {
	workDir := t.TempDir()
	markerPath := filepath.Join(workDir, TaskContextMarkerRelPath)
	if err := os.MkdirAll(filepath.Dir(markerPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(markerPath, []byte(`{"managed_by":"multica-daemon-task","agent_id":"agent-a","issue_id":"issue-a"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := RepairTaskDeliveryContext(markerPath, "task-a", "agent-a", "issue-a", "agent/a/task-a", "base-a"); err != nil {
		t.Fatalf("RepairTaskDeliveryContext: %v", err)
	}
	data, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	var marker taskContextMarkerFile
	if err := json.Unmarshal(data, &marker); err != nil {
		t.Fatal(err)
	}
	if marker.TaskID != "task-a" || marker.Branch != "agent/a/task-a" || marker.BaseCommit != "base-a" {
		t.Fatalf("marker = %+v, want repaired task identity and delivery context", marker)
	}
}
