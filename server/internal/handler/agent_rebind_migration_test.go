package handler

// RUYI-224: an agent runtime rebind must drag the agent's queued/deferred
// queue rows onto the new runtime in the SAME transaction as the agent-row
// update, and must wake the new runtime's daemon afterwards. These tests
// drive the public UpdateAgent endpoint end to end, including the failure
// path (name-collision 409) to prove a failed rebind leaves the agent row
// and its queue rows exactly as they were.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
)

// wakeupRecorder captures daemon wakeup hints so tests can assert exactly
// which runtimes were notified (and which were not).
type wakeupRecorder struct {
	mu       sync.Mutex
	runtimes []string
}

func (r *wakeupRecorder) NotifyTaskAvailable(runtimeID, taskID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.runtimes = append(r.runtimes, runtimeID)
}

func (r *wakeupRecorder) recorded() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.runtimes...)
}

// stubWakeup swaps a recorder into the shared handler's TaskService and
// restores the previous notifier when the test ends.
func stubWakeup(t *testing.T) *wakeupRecorder {
	t.Helper()
	if testHandler == nil || testHandler.TaskService == nil {
		t.Skip("handler TaskService not initialised")
	}
	rec := &wakeupRecorder{}
	prev := testHandler.TaskService.Wakeup
	testHandler.TaskService.Wakeup = rec
	t.Cleanup(func() { testHandler.TaskService.Wakeup = prev })
	return rec
}

// rebindAgentFixture seeds an agent bound to runtime "-old" plus a second
// healthy runtime "-new" in the same workspace to rebind onto.
func rebindAgentFixture(t *testing.T, name string) (agentID, oldRuntimeID, newRuntimeID string) {
	t.Helper()
	oldRuntimeID = dbfx.Runtime(t, name+"-old", testutil.Cols{"visibility": "public"})
	newRuntimeID = dbfx.Runtime(t, name+"-new", testutil.Cols{"visibility": "public"})
	agentID = dbfx.Agent(t, name, oldRuntimeID, testutil.Cols{
		"max_concurrent_tasks": 5,
	})
	return agentID, oldRuntimeID, newRuntimeID
}

func taskRuntimeColumn(t *testing.T, taskID string) string {
	t.Helper()
	var runtimeID string
	if err := testPool.QueryRow(context.Background(),
		`SELECT runtime_id::text FROM agent_task_queue WHERE id = $1`, taskID).Scan(&runtimeID); err != nil {
		t.Fatalf("load task %s: %v", taskID, err)
	}
	return runtimeID
}

func agentRuntimeColumn(t *testing.T, agentID string) string {
	t.Helper()
	var runtimeID string
	if err := testPool.QueryRow(context.Background(),
		`SELECT runtime_id::text FROM agent WHERE id = $1`, agentID).Scan(&runtimeID); err != nil {
		t.Fatalf("load agent %s: %v", agentID, err)
	}
	return runtimeID
}

func TestUpdateAgentRebindMigratesQueuedTasks(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	agentID, oldRT, newRT := rebindAgentFixture(t, "rebind-migrate")
	queuedID := dbfx.Task(t, agentID, testutil.Cols{"runtime_id": oldRT})
	deferredID := dbfx.Task(t, agentID, testutil.Cols{
		"runtime_id": oldRT,
		"status":     "deferred",
		"fire_at":    testutil.Raw("now() + interval '1 hour'"),
	})
	rec := stubWakeup(t)

	w := httptest.NewRecorder()
	testHandler.UpdateAgent(w, withURLParam(newRequest(
		http.MethodPut, "/api/agents/"+agentID,
		map[string]any{"runtime_id": newRT},
	), "id", agentID))
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateAgent rebind: got %d: %s", w.Code, w.Body.String())
	}

	if got := agentRuntimeColumn(t, agentID); got != newRT {
		t.Fatalf("agent runtime_id = %s, want %s", got, newRT)
	}
	for _, id := range []string{queuedID, deferredID} {
		if got := taskRuntimeColumn(t, id); got != newRT {
			t.Fatalf("task %s runtime_id = %s, want migrated to %s", id, got, newRT)
		}
	}
	woken := rec.recorded()
	if len(woken) != 1 || woken[0] != newRT {
		t.Fatalf("daemon wakeups = %v, want exactly one for new runtime %s", woken, newRT)
	}
}

// TestUpdateAgentRebindRollbackKeepsQueuePinned proves the rebind write is
// atomic: the name collision aborts the UpdateAgent statement inside the
// rebind transaction, and the rollback must leave the agent row on the old
// runtime with its queue rows unmigrated and no daemon wakeup — a torn
// rebind here is exactly the stranded-queue bug this endpoint must not
// manufacture itself.
func TestUpdateAgentRebindRollbackKeepsQueuePinned(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	agentID, oldRT, newRT := rebindAgentFixture(t, "rebind-rollback")
	queuedID := dbfx.Task(t, agentID, testutil.Cols{"runtime_id": oldRT})
	deferredID := dbfx.Task(t, agentID, testutil.Cols{
		"runtime_id": oldRT,
		"status":     "deferred",
		"fire_at":    testutil.Raw("now() + interval '1 hour'"),
	})
	// Holds agent_workspace_name_unique for the colliding name below.
	dbfx.Agent(t, "rebind-rollback-collide", newRT, testutil.Cols{})
	rec := stubWakeup(t)

	w := httptest.NewRecorder()
	testHandler.UpdateAgent(w, withURLParam(newRequest(
		http.MethodPut, "/api/agents/"+agentID,
		map[string]any{"name": "rebind-rollback-collide", "runtime_id": newRT},
	), "id", agentID))
	if w.Code != http.StatusConflict {
		t.Fatalf("UpdateAgent colliding rebind: got %d, want 409: %s", w.Code, w.Body.String())
	}

	if got := agentRuntimeColumn(t, agentID); got != oldRT {
		t.Fatalf("agent runtime_id = %s after failed rebind, want still %s", got, oldRT)
	}
	for _, id := range []string{queuedID, deferredID} {
		if got := taskRuntimeColumn(t, id); got != oldRT {
			t.Fatalf("task %s runtime_id = %s after failed rebind, want still pinned to %s", id, got, oldRT)
		}
	}
	if woken := rec.recorded(); len(woken) != 0 {
		t.Fatalf("daemon wakeups = %v after failed rebind, want none", woken)
	}
}

// TestUpdateAgentSameRuntimeSkipsRebindNotify pins the non-rebind path: an
// update that carries the agent's current runtime is not a rebind, so no
// queue migration runs and the daemon wakeup must not fire.
func TestUpdateAgentSameRuntimeSkipsRebindNotify(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	agentID, oldRT, _ := rebindAgentFixture(t, "rebind-same")
	queuedID := dbfx.Task(t, agentID, testutil.Cols{"runtime_id": oldRT})
	rec := stubWakeup(t)

	w := httptest.NewRecorder()
	testHandler.UpdateAgent(w, withURLParam(newRequest(
		http.MethodPut, "/api/agents/"+agentID,
		map[string]any{"runtime_id": oldRT},
	), "id", agentID))
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateAgent same-runtime: got %d: %s", w.Code, w.Body.String())
	}

	if got := agentRuntimeColumn(t, agentID); got != oldRT {
		t.Fatalf("agent runtime_id = %s, want unchanged %s", got, oldRT)
	}
	if got := taskRuntimeColumn(t, queuedID); got != oldRT {
		t.Fatalf("task %s runtime_id = %s, want unchanged %s", queuedID, got, oldRT)
	}
	if woken := rec.recorded(); len(woken) != 0 {
		t.Fatalf("daemon wakeups = %v for same-runtime update, want none", woken)
	}
}
