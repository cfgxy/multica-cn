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

func TestRepairTaskDeliveryContext(t *testing.T) {
	writeMarker := func(t *testing.T, contents string) string {
		t.Helper()
		dir := t.TempDir()
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
			t.Fatal(err)
		}
		return marker
	}

	t.Run("repairs matching agent and issue when task id is missing", func(t *testing.T) {
		path := writeMarker(t, `{"managed_by":"multica-daemon-task","agent_id":"agent-a","issue_id":"issue-a"}`)
		if err := RepairTaskDeliveryContext(path, "task-a", "agent-a", "issue-a", "agent/a/task-a", "base-a"); err != nil {
			t.Fatal(err)
		}
		marker := readMarker(t, path)
		if marker.TaskID != "task-a" || marker.Branch != "agent/a/task-a" || marker.BaseCommit != "base-a" {
			t.Fatalf("marker = %+v, want restored identity and delivery fields", marker)
		}
	})

	t.Run("matching task id accepts stale optional identity and empty baseline", func(t *testing.T) {
		path := writeMarker(t, `{"managed_by":"multica-daemon-task","task_id":"task-a","agent_id":"agent-old","issue_id":"issue-old","branch":"agent/old","base_commit":"base-old"}`)
		if err := RepairTaskDeliveryContext(path, "task-a", "agent-a", "issue-a", "agent/a/task-a", ""); err != nil {
			t.Fatal(err)
		}
		marker := readMarker(t, path)
		if marker.AgentID != "agent-a" || marker.IssueID != "issue-a" || marker.Branch != "agent/a/task-a" || marker.BaseCommit != "base-old" {
			t.Fatalf("marker = %+v, want refreshed identity, branch, and preserved baseline", marker)
		}
	})

	for _, tc := range []struct {
		name     string
		contents string
	}{
		{name: "root shaped", contents: `{"managed_by":"multica-daemon-task"}`},
		{name: "agent only", contents: `{"managed_by":"multica-daemon-task","agent_id":"agent-a"}`},
		{name: "other task", contents: `{"managed_by":"multica-daemon-task","task_id":"task-other","agent_id":"agent-a","issue_id":"issue-a"}`},
		{name: "foreign", contents: `{"managed_by":"other-tool","task_id":"task-a"}`},
		{name: "corrupt", contents: `{"managed_by":`},
	} {
		t.Run("rejects "+tc.name, func(t *testing.T) {
			path := writeMarker(t, tc.contents)
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := RepairTaskDeliveryContext(path, "task-a", "agent-a", "issue-a", "agent/a/task-a", "base-a"); err == nil {
				t.Fatal("expected repair rejection")
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != string(before) {
				t.Fatalf("unsafe marker changed:\n got %s\nwant %s", after, before)
			}
		})
	}
}

func TestRepairTaskContextMarkerFileRejectsSymlinkAndInPlaceRewrite(t *testing.T) {
	t.Run("symlinked marker directory", func(t *testing.T) {
		workDir := t.TempDir()
		targetDir := t.TempDir()
		target := filepath.Join(targetDir, filepath.Base(TaskContextMarkerRelPath))
		before := []byte(`{"managed_by":"multica-daemon-task","agent_id":"agent-a","issue_id":"issue-a"}`)
		if err := os.WriteFile(target, before, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(targetDir, filepath.Join(workDir, ".multica")); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		if err := RepairTaskDeliveryContext(filepath.Join(workDir, TaskContextMarkerRelPath), "task-a", "agent-a", "issue-a", "agent/a/task-a", "base-a"); err == nil {
			t.Fatal("expected symlinked marker directory rejection")
		}
		after, err := os.ReadFile(target)
		if err != nil {
			t.Fatal(err)
		}
		if string(after) != string(before) {
			t.Fatalf("symlink target changed:\n got %s\nwant %s", after, before)
		}
	})

	t.Run("symlinked workdir ancestor", func(t *testing.T) {
		realWorkDir := t.TempDir()
		markerPath := filepath.Join(realWorkDir, TaskContextMarkerRelPath)
		if err := os.MkdirAll(filepath.Dir(markerPath), 0o755); err != nil {
			t.Fatal(err)
		}
		before := []byte(`{"managed_by":"multica-daemon-task","agent_id":"agent-a","issue_id":"issue-a"}`)
		if err := os.WriteFile(markerPath, before, 0o644); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(t.TempDir(), "workdir-link")
		if err := os.Symlink(realWorkDir, link); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		if err := RepairTaskDeliveryContext(filepath.Join(link, TaskContextMarkerRelPath), "task-a", "agent-a", "issue-a", "agent/a/task-a", "base-a"); err == nil {
			t.Fatal("expected symlinked workdir ancestor rejection")
		}
		after, err := os.ReadFile(markerPath)
		if err != nil {
			t.Fatal(err)
		}
		if string(after) != string(before) {
			t.Fatalf("symlinked workdir target changed:\n got %s\nwant %s", after, before)
		}
	})

	t.Run("in place rewrite", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, TaskContextMarkerRelPath)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		original := []byte(`{"managed_by":"multica-daemon-task","task_id":"task-a"}`)
		if err := os.WriteFile(path, original, 0o644); err != nil {
			t.Fatal(err)
		}
		rewritten := []byte(`{"managed_by":"multica-daemon-task","task_id":"task-b"}`)
		err := repairTaskContextMarkerFile(path, func(data []byte) ([]byte, error) {
			if err := os.WriteFile(path, rewritten, 0o644); err != nil {
				return nil, err
			}
			return data, nil
		})
		if err == nil {
			t.Fatal("expected in-place rewrite rejection")
		}
		after, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(after) != string(rewritten) {
			t.Fatalf("marker changed after rejection:\n got %s\nwant %s", after, rewritten)
		}
	})
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
