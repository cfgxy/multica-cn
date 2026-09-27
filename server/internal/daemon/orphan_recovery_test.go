package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/pkg/agent"
)

// recoveryFixture wires a daemon against a fake server that answers the
// in-flight list, records every probe question the daemon asks, and records
// each per-task fail and blind recover-orphans side effect — everything the
// RUYI-225 recovery contract needs to be asserted from the outside.
type recoveryFixture struct {
	daemon *Daemon
	server *httptest.Server
	root   string // workspaces root with real env-root directories

	mu          sync.Mutex
	inFlight    []InFlightTask   // served for any runtime's in-flight list
	inFlightAsk []string         // runtime IDs the daemon asked about
	failed      []failedTaskCall // /tasks/<id>/fail bodies, in order
	orphans     []string         // runtime IDs that got recover-orphans
	workspaces  []WorkspaceInfo  // served from /api/daemon/workspaces
	registered  [][]string       // providers per register call, in order
}

type failedTaskCall struct {
	TaskID        string `json:"-"`
	ErrorMsg      string `json:"error"`
	FailureReason string `json:"failure_reason"`
}

func newRecoveryFixture(t *testing.T) *recoveryFixture {
	t.Helper()
	fx := &recoveryFixture{root: t.TempDir()}

	origDetect := detectAgentVersion
	origCheck := checkAgentMinVersion
	t.Cleanup(func() {
		detectAgentVersion = origDetect
		checkAgentMinVersion = origCheck
	})
	detectAgentVersion = func(_ context.Context, _ agent.Command) (string, error) { return "9.9.9", nil }
	checkAgentMinVersion = func(_, _ string) error { return nil }

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/tasks/in-flight"):
			parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/daemon/runtimes/"), "/")
			fx.mu.Lock()
			fx.inFlightAsk = append(fx.inFlightAsk, parts[0])
			tasks := fx.inFlight
			fx.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(tasks)
		case strings.HasPrefix(r.URL.Path, "/api/daemon/tasks/") && strings.HasSuffix(r.URL.Path, "/fail"):
			var call failedTaskCall
			_ = json.NewDecoder(r.Body).Decode(&call)
			call.TaskID = strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/daemon/tasks/"), "/fail")
			fx.mu.Lock()
			fx.failed = append(fx.failed, call)
			fx.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{}`))
		case strings.HasSuffix(r.URL.Path, "/recover-orphans"):
			parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/daemon/runtimes/"), "/")
			fx.mu.Lock()
			fx.orphans = append(fx.orphans, parts[0])
			fx.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"orphaned":0,"retried":0}`))
		case r.URL.Path == "/api/daemon/workspaces":
			fx.mu.Lock()
			list := append([]WorkspaceInfo(nil), fx.workspaces...)
			fx.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(list)
		case r.URL.Path == "/api/daemon/register":
			var body struct {
				Runtimes []struct {
					Type string `json:"type"`
				} `json:"runtimes"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			fx.mu.Lock()
			var resp RegisterResponse
			var providers []string
			for _, rt := range body.Runtimes {
				resp.Runtimes = append(resp.Runtimes, Runtime{
					ID: "rt-" + rt.Type, Name: "n", Provider: rt.Type, Status: "online",
				})
				providers = append(providers, rt.Type)
			}
			fx.registered = append(fx.registered, providers)
			fx.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	fx.daemon = freshDaemon(srv.URL)
	fx.daemon.cfg.WorkspacesRoot = fx.root
	fx.server = srv
	return fx
}

func (fx *recoveryFixture) setInFlight(tasks ...InFlightTask) {
	fx.mu.Lock()
	defer fx.mu.Unlock()
	fx.inFlight = tasks
}

func (fx *recoveryFixture) recordedFails() []failedTaskCall {
	fx.mu.Lock()
	defer fx.mu.Unlock()
	return append([]failedTaskCall(nil), fx.failed...)
}

func (fx *recoveryFixture) askedInFlight() []string {
	fx.mu.Lock()
	defer fx.mu.Unlock()
	return append([]string(nil), fx.inFlightAsk...)
}

func (fx *recoveryFixture) recordedOrphans() []string {
	fx.mu.Lock()
	defer fx.mu.Unlock()
	return append([]string(nil), fx.orphans...)
}

// buildEnvRoot creates the on-disk shape a running task leaves behind: the
// two-level env root with its owner marker, and the task-root record
// installed through the production writer so identity resolution finds it.
func buildEnvRoot(t *testing.T, root, wsID, taskID string) string {
	t.Helper()
	envRoot := filepath.Join(root, wsID, taskID)
	if err := os.MkdirAll(envRoot, 0o755); err != nil {
		t.Fatalf("mkdir env root: %v", err)
	}
	owner := `{"workspace_id":"` + wsID + `","task_id":"` + taskID + `"}`
	if err := os.WriteFile(filepath.Join(envRoot, ".task_owner"), []byte(owner), 0o644); err != nil {
		t.Fatalf("write owner: %v", err)
	}
	resolved, err := execenv.ResolveRootDir(execenv.RootDirParams{
		WorkspacesRoot: root,
		WorkspaceID:    wsID,
		TaskID:         taskID,
	})
	if err != nil {
		t.Fatalf("install task-root record: %v", err)
	}
	if resolved != envRoot {
		t.Fatalf("ResolveRootDir = %s, want %s", resolved, envRoot)
	}
	return envRoot
}

// holdEnvRootLock keeps the env root's execution lock held until the test
// ends — the live-worker half of the probe matrix.
func holdEnvRootLock(t *testing.T, root, envRoot string) {
	t.Helper()
	wsRoot, err := os.OpenRoot(root)
	if err != nil {
		t.Fatalf("open workspaces root: %v", err)
	}
	t.Cleanup(func() { _ = wsRoot.Close() })
	rel, err := filepath.Rel(root, envRoot)
	if err != nil {
		t.Fatalf("rel: %v", err)
	}
	claim, _, err := execenv.LockEnvRootForReuse(wsRoot, rel, envRoot)
	if err != nil {
		t.Fatalf("hold env-root lock: %v", err)
	}
	if claim == nil {
		t.Fatalf("env root %s missing", envRoot)
	}
	t.Cleanup(claim.Release)
}

// TestRecoverInFlightTasks_FailsDeadWorker: a free execution lock proves the
// worker died with the previous daemon process, so the task is failed with
// the runtime_recovery reason and re-enters the retry pipeline.
func TestRecoverInFlightTasks_FailsDeadWorker(t *testing.T) {
	fx := newRecoveryFixture(t)
	envRoot := buildEnvRoot(t, fx.root, "ws1", "taskdead")
	fx.setInFlight(InFlightTask{ID: "taskdead", WorkspaceID: "ws1", Status: "running", WorkDir: filepath.Join(envRoot, "repo")})

	fx.daemon.recoverInFlightTasksForRuntime(context.Background(), "rt-1")

	fails := fx.recordedFails()
	if len(fails) != 1 {
		t.Fatalf("fail calls = %d, want 1 (%+v)", len(fails), fails)
	}
	if fails[0].TaskID != "taskdead" || fails[0].FailureReason != "runtime_recovery" {
		t.Fatalf("fail = %+v, want taskdead/runtime_recovery", fails[0])
	}
	if fails[0].ErrorMsg != orphanRecoveryErrMsg {
		t.Fatalf("error msg = %q, want %q", fails[0].ErrorMsg, orphanRecoveryErrMsg)
	}
}

// TestRecoverInFlightTasks_LeavesHeldLockAlone: a held execution lock proves
// a live worker (this is exactly the MUL-3332 case the blind recovery killed),
// so the task must come out of this untouched.
func TestRecoverInFlightTasks_LeavesHeldLockAlone(t *testing.T) {
	fx := newRecoveryFixture(t)
	envRoot := buildEnvRoot(t, fx.root, "ws1", "tasklive")
	holdEnvRootLock(t, fx.root, envRoot)
	fx.setInFlight(InFlightTask{ID: "tasklive", WorkspaceID: "ws1", Status: "running", WorkDir: filepath.Join(envRoot, "repo")})

	fx.daemon.recoverInFlightTasksForRuntime(context.Background(), "rt-1")

	if fails := fx.recordedFails(); len(fails) != 0 {
		t.Fatalf("live worker was failed: %+v", fails)
	}
}

// TestRecoverInFlightTasks_LeavesWorkDirlessTaskAlone: without a pinned
// work_dir there is nothing to probe, and the conservative stance is to leave
// the task for the first-registration path's blanket fallback.
func TestRecoverInFlightTasks_LeavesWorkDirlessTaskAlone(t *testing.T) {
	fx := newRecoveryFixture(t)
	fx.setInFlight(InFlightTask{ID: "taskwaiting", WorkspaceID: "ws1", Status: "waiting_local_directory"})

	fx.daemon.recoverInFlightTasksForRuntime(context.Background(), "rt-1")

	if fails := fx.recordedFails(); len(fails) != 0 {
		t.Fatalf("workdirless task was failed: %+v", fails)
	}
	if asked := fx.askedInFlight(); len(asked) != 1 {
		t.Fatalf("in-flight asks = %v, want [rt-1]", asked)
	}
}

// TestRecoverInFlightTasks_SkipsTaskActiveInProcess: a task this very process
// is executing (preparation and local-directory waiters included) is skipped
// by ID before any probing.
func TestRecoverInFlightTasks_SkipsTaskActiveInProcess(t *testing.T) {
	fx := newRecoveryFixture(t)
	envRoot := buildEnvRoot(t, fx.root, "ws1", "taskown")
	fx.setInFlight(InFlightTask{ID: "taskown", WorkspaceID: "ws1", Status: "running", WorkDir: filepath.Join(envRoot, "repo")})
	fx.daemon.markTaskActiveInProcess("taskown")
	t.Cleanup(func() { fx.daemon.unmarkTaskActiveInProcess("taskown") })

	fx.daemon.recoverInFlightTasksForRuntime(context.Background(), "rt-1")

	if fails := fx.recordedFails(); len(fails) != 0 {
		t.Fatalf("in-process task was failed: %+v", fails)
	}
}

// TestRecoverInFlightTasks_SkipsWithoutResolvableRoot: no task-root record
// means the probe can neither confirm nor refute liveness — it must skip, and
// the read-only resolution must not poison the record index a later retry
// would trust.
func TestRecoverInFlightTasks_SkipsWithoutResolvableRoot(t *testing.T) {
	fx := newRecoveryFixture(t)
	fx.setInFlight(InFlightTask{ID: "taskghost", WorkspaceID: "ws1", Status: "running", WorkDir: filepath.Join(fx.root, "ws1", "taskghost", "repo")})

	fx.daemon.recoverInFlightTasksForRuntime(context.Background(), "rt-1")

	if fails := fx.recordedFails(); len(fails) != 0 {
		t.Fatalf("unresolvable task was failed: %+v", fails)
	}
	if _, err := os.Stat(filepath.Join(fx.root, ".task_roots")); !os.IsNotExist(err) {
		t.Fatalf("probe created task-root index entries; read-only resolve leaked writes")
	}
}

// TestConvergeRegistrationRecoversInFlight pins the RUYI-225 fix itself: a
// runtime that only registers on a later converge round (its first-round
// version probe failed — the exact zcode/hermes incident shape) gets its dead
// in-flight workers failed through the probe, without any blind call that
// would kill live work.
func TestConvergeRegistrationRecoversInFlight(t *testing.T) {
	fx := newRecoveryFixture(t)
	fx.daemon.cfg.Agents = map[string]AgentEntry{"codex": {Path: "/fake/codex"}}
	setProbe := stubAgentProbe(t, map[string]AgentEntry{"codex": {Path: "/fake/codex"}})
	fx.mu.Lock()
	fx.workspaces = []WorkspaceInfo{{ID: "ws1", Name: "one"}}
	fx.mu.Unlock()

	if err := fx.daemon.syncWorkspacesFromAPI(context.Background(), false); err != nil {
		t.Fatalf("syncWorkspacesFromAPI: %v", err)
	}
	envRoot := buildEnvRoot(t, fx.root, "ws1", "taskdz")
	fx.setInFlight(InFlightTask{ID: "taskdz", WorkspaceID: "ws1", Status: "running", WorkDir: filepath.Join(envRoot, "repo")})

	// The second provider installs mid-flight; converge registers it now.
	setProbe(map[string]AgentEntry{
		"codex": {Path: "/fake/codex"},
		"agy":   {Path: "/fake/agy"},
	})
	fx.daemon.refreshAgentAvailability()
	fx.daemon.convergeRuntimeRegistrations(context.Background())

	if asked := fx.askedInFlight(); len(asked) == 0 {
		t.Fatalf("converge never asked for the new runtime's in-flight tasks")
	}
	fails := fx.recordedFails()
	if len(fails) != 1 || fails[0].TaskID != "taskdz" || fails[0].FailureReason != "runtime_recovery" {
		t.Fatalf("converge recovery fails = %+v, want taskdz/runtime_recovery", fails)
	}
}

// TestFirstRegistrationProbesThenKeepsBlindFallback: on the startup path the
// probe runs first (dead workers failed per task) and the historical blind
// recover-orphans still fires as the fallback for tasks without work_dir.
func TestFirstRegistrationProbesThenKeepsBlindFallback(t *testing.T) {
	fx := newRecoveryFixture(t)
	fx.daemon.cfg.Agents = map[string]AgentEntry{"codex": {Path: "/fake/codex"}}
	stubAgentProbe(t, map[string]AgentEntry{"codex": {Path: "/fake/codex"}})
	fx.mu.Lock()
	fx.workspaces = []WorkspaceInfo{{ID: "ws1", Name: "one"}}
	fx.mu.Unlock()
	envRoot := buildEnvRoot(t, fx.root, "ws1", "taskstart")
	fx.setInFlight(InFlightTask{ID: "taskstart", WorkspaceID: "ws1", Status: "running", WorkDir: filepath.Join(envRoot, "repo")})

	if err := fx.daemon.syncWorkspacesFromAPI(context.Background(), false); err != nil {
		t.Fatalf("syncWorkspacesFromAPI: %v", err)
	}

	fails := fx.recordedFails()
	if len(fails) != 1 || fails[0].TaskID != "taskstart" || fails[0].FailureReason != "runtime_recovery" {
		t.Fatalf("probe recovery fails = %+v, want taskstart/runtime_recovery", fails)
	}
	if orphans := fx.recordedOrphans(); len(orphans) == 0 {
		t.Fatalf("blind recover-orphans fallback did not fire on first registration")
	}
}
