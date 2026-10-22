package execenv

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRegisterTaskDeliveryContext covers the marker registration that makes a
// `multica repo checkout` worktree provable by `multica worktree preflight`
// (RUYI-848): the task marker gains branch + base_commit without losing the
// task identity fields the delivery guard and the CLI fallback rely on.
func TestRegisterTaskDeliveryContext(t *testing.T) {
	writeMarker := func(t *testing.T, dir, contents string) string {
		t.Helper()
		path := filepath.Join(dir, TaskContextMarkerRelPath)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	readMarker := func(t *testing.T, path string) taskContextMarkerFile {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var marker taskContextMarkerFile
		if err := json.Unmarshal(data, &marker); err != nil {
			t.Fatalf("marker no longer parseable: %v\n%s", err, data)
		}
		return marker
	}

	t.Run("merges branch and baseline preserving identity fields", func(t *testing.T) {
		dir := t.TempDir()
		markerJSON := `{"managed_by":"multica-daemon-task","task_id":"task-a","agent_id":"agent-a","issue_id":"issue-a","chat_session_id":"chat-a"}`
		path := writeMarker(t, dir, markerJSON)
		if err := RegisterTaskDeliveryContext(path, "agent/a/1", "40charbasecommit40charbasecommit"); err != nil {
			t.Fatalf("RegisterTaskDeliveryContext: %v", err)
		}
		marker := readMarker(t, path)
		if marker.TaskID != "task-a" || marker.AgentID != "agent-a" || marker.IssueID != "issue-a" || marker.ChatSessionID != "chat-a" {
			t.Fatalf("identity fields lost: %+v", marker)
		}
		if marker.Branch != "agent/a/1" || marker.BaseCommit != "40charbasecommit40charbasecommit" {
			t.Fatalf("delivery fields not registered: %+v", marker)
		}
	})

	t.Run("repeated registration updates the baseline", func(t *testing.T) {
		dir := t.TempDir()
		path := writeMarker(t, dir, `{"managed_by":"multica-daemon-task","task_id":"task-a"}`)
		if err := RegisterTaskDeliveryContext(path, "agent/a/1", "base-one"); err != nil {
			t.Fatal(err)
		}
		if err := RegisterTaskDeliveryContext(path, "agent/a/1", "base-two"); err != nil {
			t.Fatal(err)
		}
		marker := readMarker(t, path)
		if marker.BaseCommit != "base-two" || marker.TaskID != "task-a" {
			t.Fatalf("marker = %+v, want updated baseline with preserved task id", marker)
		}
	})

	t.Run("empty arguments never clear an existing registration", func(t *testing.T) {
		dir := t.TempDir()
		path := writeMarker(t, dir, `{"managed_by":"multica-daemon-task","task_id":"task-a","branch":"agent/a/1","base_commit":"base-one"}`)
		if err := RegisterTaskDeliveryContext(path, "", ""); err != nil {
			t.Fatal(err)
		}
		marker := readMarker(t, path)
		if marker.Branch != "agent/a/1" || marker.BaseCommit != "base-one" {
			t.Fatalf("empty registration cleared the delivery fields: %+v", marker)
		}
	})

	t.Run("refuses a marker owned by something else", func(t *testing.T) {
		dir := t.TempDir()
		path := writeMarker(t, dir, `{"managed_by":"other-tool","task_id":"task-a"}`)
		if err := RegisterTaskDeliveryContext(path, "agent/a/1", "base"); err == nil {
			t.Fatal("expected refusal for a foreign marker")
		}
	})

	t.Run("refuses a torn or missing marker", func(t *testing.T) {
		dir := t.TempDir()
		path := writeMarker(t, dir, `{"managed_by": "multica-daemon-task"` /* torn: unparseable JSON */)
		if err := RegisterTaskDeliveryContext(path, "agent/a/1", "base"); err == nil {
			t.Fatal("expected refusal for an unparseable marker")
		}
		if err := RegisterTaskDeliveryContext(filepath.Join(dir, "no-such-marker"), "agent/a/1", "base"); err == nil {
			t.Fatal("expected refusal for a missing marker")
		}
	})
}

// TestWriteTaskContextMarkerRefreshKeepsTaskIdentity guards the RUYI-848
// failure: a workdir-reuse refresh whose context dropped the task id must
// hand the existing marker's task_id forward instead of wiping it, so
// preflight's task-identity check stays diagnosable.
func TestWriteTaskContextMarkerRefreshKeepsTaskIdentity(t *testing.T) {
	dir := t.TempDir()
	if err := writeTaskContextMarker(dir, TaskContextForEnv{TaskID: "dev-task", AgentID: "dev-agent", IssueID: "issue-x"}, nil); err != nil {
		t.Fatal(err)
	}
	// The reusing task's context lost its TaskID (regressed ctx): the refresh
	// must keep the marker's existing task identity instead of blanking it.
	if err := writeTaskContextMarker(dir, TaskContextForEnv{TaskID: "", AgentID: "qa-agent", IssueID: "issue-x"}, nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, TaskContextMarkerRelPath))
	if err != nil {
		t.Fatal(err)
	}
	var marker taskContextMarkerFile
	if err := json.Unmarshal(data, &marker); err != nil {
		t.Fatal(err)
	}
	if marker.TaskID != "dev-task" {
		t.Fatalf("refresh with empty ctx.TaskID wiped the marker task_id: %+v", marker)
	}
	if marker.AgentID != "qa-agent" {
		t.Fatalf("refresh did not rebind the agent identity: %+v", marker)
	}
}

// TestRegisterTaskDeliveryContextRefusesRootMarker guards the node-wide
// workspaces-root marker: delivery context is per-task state and must never
// be merged into a marker that carries no task identity.
func TestRegisterTaskDeliveryContextRefusesRootMarker(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, TaskContextMarkerRelPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"managed_by":"multica-daemon-task"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RegisterTaskDeliveryContext(path, "agent/a/1", "base"); err == nil {
		t.Fatal("expected refusal for a non task-scoped (root-shape) marker")
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), `"managed_by"`) || strings.Contains(string(data), "branch") {
		t.Fatalf("root-shape marker was modified: %s", data)
	}
}

// TestWriteTaskContextMarkerDevQaHandoff covers the atomic DEV->QA workdir
// handoff (RUYI-848): when a QA task reuses a DEV workdir whose marker already
// carries a registered delivery (branch + base_commit from `repo checkout`),
// the marker refresh under the QA task\'s identity keeps that registration
// instead of wiping it.
func TestWriteTaskContextMarkerDevQaHandoff(t *testing.T) {
	dir := t.TempDir()
	devCtx := TaskContextForEnv{
		TaskID:        "dev-task",
		AgentID:       "dev-agent",
		IssueID:       "issue-x",
		ManagedBranch: "agent/dev-task",
		BaseCommit:    "base-dev",
	}
	if err := writeTaskContextMarker(dir, devCtx, nil); err != nil {
		t.Fatalf("write DEV marker: %v", err)
	}

	// The QA task reuses the same workdir with no delivery of its own yet.
	qaCtx := TaskContextForEnv{
		TaskID:  "qa-task",
		AgentID: "qa-agent",
		IssueID: "issue-x",
	}
	if err := writeTaskContextMarker(dir, qaCtx, nil); err != nil {
		t.Fatalf("refresh marker for QA task: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, TaskContextMarkerRelPath))
	if err != nil {
		t.Fatal(err)
	}
	var marker taskContextMarkerFile
	if err := json.Unmarshal(data, &marker); err != nil {
		t.Fatalf("marker not parseable after handoff: %v\n%s", err, data)
	}
	if marker.TaskID != "qa-task" || marker.AgentID != "qa-agent" {
		t.Fatalf("handoff did not rebind the marker to the QA task: %+v", marker)
	}
	if marker.Branch != "agent/dev-task" || marker.BaseCommit != "base-dev" {
		t.Fatalf("handoff lost the DEV delivery registration: %+v", marker)
	}
}

// TestWriteTaskContextMarkerOwnDeliveryWins guards the inverse: a task that
// starts with its own managed worktree keeps its own delivery fields even
// when the reused marker carried an older registration.
func TestWriteTaskContextMarkerOwnDeliveryWins(t *testing.T) {
	dir := t.TempDir()
	if err := writeTaskContextMarker(dir, TaskContextForEnv{TaskID: "dev-task"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := RegisterTaskDeliveryContext(filepath.Join(dir, TaskContextMarkerRelPath), "agent/dev", "base-dev"); err != nil {
		t.Fatal(err)
	}
	afterCtx := TaskContextForEnv{
		TaskID:        "qa-task",
		AgentID:       "qa-agent",
		ManagedBranch: "agent/qa-task",
		BaseCommit:    "base-qa",
	}
	if err := writeTaskContextMarker(dir, afterCtx, nil); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, TaskContextMarkerRelPath))
	var marker taskContextMarkerFile
	if err := json.Unmarshal(data, &marker); err != nil {
		t.Fatal(err)
	}
	if marker.TaskID != "qa-task" || marker.Branch != "agent/qa-task" || marker.BaseCommit != "base-qa" {
		t.Fatalf("own delivery must win over the carried one: %+v", marker)
	}
}
