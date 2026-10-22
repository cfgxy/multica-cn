package daemon

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// snapshotResourceForTest marshals one snapshot-mode local_directory resource
// the way the server stores it in project_resource.resource_ref.
func snapshotResourceForTest(t *testing.T, id, dir, daemonID string, primary bool, subdirs ...string) ProjectResourceData {
	t.Helper()
	raw, err := json.Marshal(localDirectoryRef{
		LocalPath:      dir,
		DaemonID:       daemonID,
		ExecutionMode:  localDirectoryModeSnapshot,
		Primary:        primary,
		Subdirectories: subdirs,
	})
	if err != nil {
		t.Fatalf("marshal snapshot ref: %v", err)
	}
	return ProjectResourceData{ID: id, ResourceType: localDirectoryResourceType, ResourceRef: raw}
}

// scopeJSONForTest renders the task-level local_directory_scope payload.
func scopeJSONForTest(t *testing.T, resourceID string, subdirs ...string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal([]protocol.TaskDirectoryScope{{ResourceID: resourceID, Subdirectories: subdirs}})
	if err != nil {
		t.Fatalf("marshal scope: %v", err)
	}
	return raw
}

func TestResolveSnapshotPlanLeaderTaskTakesNoPlan(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	task := Task{
		ID:               "t1",
		IsLeaderTask:     true,
		ProjectResources: []ProjectResourceData{snapshotResourceForTest(t, "r1", dir, "d1", true)},
	}
	plan, err := resolveSnapshotPlan(task, "d1")
	if err != nil || plan != nil {
		t.Fatalf("leader tasks must not route to snapshot, plan=%v err=%v", plan, err)
	}
}

func TestResolveSnapshotPlanUndeclaredPrimary(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	task := Task{
		ID:               "t1",
		IssueID:          "11111111-1111-1111-1111-111111111111",
		ProjectResources: []ProjectResourceData{snapshotResourceForTest(t, "r1", dir, "d1", true)},
	}
	plan, err := resolveSnapshotPlan(task, "d1")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if plan.Declared {
		t.Fatal("no task scope must resolve as undeclared")
	}
	if len(plan.Units) != 1 || plan.Units[0].Subdir != "" {
		t.Fatalf("undeclared plan is one whole-root unit, got %+v", plan.Units)
	}
	if plan.Primary.AbsPath != filepath.Clean(dir) {
		t.Fatalf("primary unit must cover the resource root, got %q", plan.Primary.AbsPath)
	}
	// THE acceptance-1 property: an undeclared task owes no task-lifetime
	// lock, which is what lets two tasks share one directory concurrently.
	if locks := plan.DeclaredLockPaths(); locks != nil {
		t.Fatalf("undeclared plan must take no lock, got %v", locks)
	}
}

func TestResolveSnapshotPlanUndeclaredSoleRowWithoutPrimary(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	task := Task{
		ID:               "t1",
		ProjectResources: []ProjectResourceData{snapshotResourceForTest(t, "r1", dir, "d1", false)},
	}
	plan, err := resolveSnapshotPlan(task, "d1")
	if err != nil {
		t.Fatalf("sole snapshot row acts as primary: %v", err)
	}
	if plan.Primary == nil || plan.Primary.AbsPath != filepath.Clean(dir) {
		t.Fatalf("sole row must become the primary unit, got %+v", plan.Primary)
	}
}

func TestResolveSnapshotPlanUndeclaredAmbiguousRows(t *testing.T) {
	t.Parallel()
	d1, d2 := t.TempDir(), t.TempDir()

	// Two rows, no primary, no scope: refuse to guess which one the task meant.
	task := Task{
		ID:               "t1",
		ProjectResources: []ProjectResourceData{snapshotResourceForTest(t, "r1", d1, "d1", false), snapshotResourceForTest(t, "r2", d2, "d1", false)},
	}
	if _, err := resolveSnapshotPlan(task, "d1"); err == nil || !strings.Contains(err.Error(), "no primary") {
		t.Fatalf("ambiguous undeclared rows must fail with a no-primary error, got %v", err)
	}

	// Two primaries on one project is bypassed or corrupted data either way.
	task2 := Task{
		ID:               "t2",
		ProjectResources: []ProjectResourceData{snapshotResourceForTest(t, "r1", d1, "d1", true), snapshotResourceForTest(t, "r2", d2, "d1", true)},
	}
	if _, err := resolveSnapshotPlan(task2, "d1"); err == nil || !strings.Contains(err.Error(), "multiple primary") {
		t.Fatalf("two primaries must fail fast, got %v", err)
	}
}

func TestResolveSnapshotPlanDeclaredSubdirectories(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, sub := range []string{"packages/core", "apps/web"} {
		if err := os.MkdirAll(filepath.Join(dir, filepath.FromSlash(sub)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	res := snapshotResourceForTest(t, "r1", dir, "d1", false, "packages/core", "apps/web")
	task := Task{
		ID:                  "t1",
		IssueID:             "11111111-1111-1111-1111-111111111111",
		ProjectResources:    []ProjectResourceData{res},
		LocalDirectoryScope: scopeJSONForTest(t, "r1", "packages/core"),
	}
	plan, err := resolveSnapshotPlan(task, "d1")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !plan.Declared || len(plan.Units) != 1 {
		t.Fatalf("declared scope materializes exactly its own units, got %+v", plan.Units)
	}
	if plan.Units[0].Subdir != "packages/core" {
		t.Fatalf("unit covers the declared subdir, got %q", plan.Units[0].Subdir)
	}
	if plan.Units[0].LockPath == "" {
		t.Fatal("declared unit carries a real-path lock key")
	}
	if locks := plan.DeclaredLockPaths(); len(locks) != 1 || locks[0] != plan.Units[0].LockPath {
		t.Fatalf("DeclaredLockPaths must name the declared unit's lock, got %v", locks)
	}
}

func TestResolveSnapshotPlanRefusesToWiden(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "packages", "core"), 0o755); err != nil {
		t.Fatal(err)
	}
	res := snapshotResourceForTest(t, "r1", dir, "d1", false, "packages/core")

	// A subdir outside the resource's effective set must not be silently
	// widened into a mount.
	outside := Task{
		ID:                  "t1",
		ProjectResources:    []ProjectResourceData{res},
		LocalDirectoryScope: scopeJSONForTest(t, "r1", "somewhere/else"),
	}
	if _, err := resolveSnapshotPlan(outside, "d1"); err == nil || !strings.Contains(err.Error(), "outside the effective set") {
		t.Fatalf("scope outside the effective set must fail, got %v", err)
	}

	// A scope naming a resource that is not a snapshot directory of this
	// daemon must fail the same way.
	foreign := Task{
		ID:                  "t2",
		ProjectResources:    []ProjectResourceData{res},
		LocalDirectoryScope: scopeJSONForTest(t, "r-other", "packages/core"),
	}
	if _, err := resolveSnapshotPlan(foreign, "d1"); err == nil || !strings.Contains(err.Error(), "refusing to widen") {
		t.Fatalf("foreign resource scope must fail, got %v", err)
	}

	// A subdirectory inside the effective set but missing on disk is a
	// claim-time failure, not a worker-time one.
	resWithGhost := snapshotResourceForTest(t, "r1", dir, "d1", false, "packages/core", "packages/ghost")
	missing := Task{
		ID:                  "t3",
		ProjectResources:    []ProjectResourceData{resWithGhost},
		LocalDirectoryScope: scopeJSONForTest(t, "r1", "packages/ghost"),
	}
	if _, err := resolveSnapshotPlan(missing, "d1"); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("missing subdir must fail at claim time, got %v", err)
	}

	// A missing primary root fails at claim time too.
	ghostRoot := filepath.Join(t.TempDir(), "ghost")
	ghostTask := Task{
		ID:               "t4",
		ProjectResources: []ProjectResourceData{snapshotResourceForTest(t, "r1", ghostRoot, "d1", true)},
	}
	if _, err := resolveSnapshotPlan(ghostTask, "d1"); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("missing primary root must fail at claim time, got %v", err)
	}
}

func TestSnapshotWorktreeFallbackOnGitRoot(t *testing.T) {
	t.Parallel()
	gitDir := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", gitDir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v (%s)", err, out)
	}
	plan := &snapshotPlan{Primary: &snapshotUnit{Ref: localDirectoryRef{LocalPath: gitDir}, AbsPath: filepath.Clean(gitDir)}}
	fb, err := snapshotWorktreeFallback(plan)
	if err != nil || fb == nil {
		t.Fatalf("git root must fall back to the worktree flow, fb=%v err=%v", fb, err)
	}
	if !fb.UsesWorktree() {
		t.Fatalf("fallback must be worktree-mode, got %+v", fb.Ref)
	}

	plain := t.TempDir()
	plan2 := &snapshotPlan{Primary: &snapshotUnit{Ref: localDirectoryRef{LocalPath: plain}, AbsPath: filepath.Clean(plain)}}
	fb2, err := snapshotWorktreeFallback(plan2)
	if err != nil || fb2 != nil {
		t.Fatalf("non-git root must proceed with mounts, fb=%v err=%v", fb2, err)
	}
}

func TestAuditSnapshotGitScope(t *testing.T) {
	t.Parallel()

	plain := newTestGitRoot(t)
	plainPlan := &snapshotPlan{Units: []snapshotUnit{{AbsPath: plain}}, Primary: &snapshotUnit{AbsPath: plain}}
	if audit := auditSnapshotGitScope(plainPlan); !audit.HasGit || !audit.Safe {
		t.Fatalf("ordinary repository must be fully auditable, got %+v", audit)
	}

	parent := newTestGitRoot(t)
	linked := filepath.Join(t.TempDir(), "linked")
	if out, err := exec.Command("git", "-C", parent, "worktree", "add", "-q", linked).CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v (%s)", err, out)
	}
	linkedPlan := &snapshotPlan{Units: []snapshotUnit{{AbsPath: linked}}, Primary: &snapshotUnit{AbsPath: linked}}
	if audit := auditSnapshotGitScope(linkedPlan); !audit.HasGit || audit.Safe || !strings.Contains(audit.Reason, "outside") {
		t.Fatalf("linked worktree with external common dir must fail audit, got %+v", audit)
	}

	nested := newTestGitRoot(t)
	child := filepath.Join(nested, "child")
	if out, err := exec.Command("git", "init", "-q", child).CombinedOutput(); err != nil {
		t.Fatalf("git init nested: %v (%s)", err, out)
	}
	nestedPlan := &snapshotPlan{Units: []snapshotUnit{{AbsPath: nested}}, Primary: &snapshotUnit{AbsPath: nested}}
	if audit := auditSnapshotGitScope(nestedPlan); !audit.HasGit || !audit.Safe {
		t.Fatalf("nested repositories fully inside scope must be auditable, got %+v", audit)
	}
}

func newTestGitRoot(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.name", "Test"}, {"config", "user.email", "test@example.invalid"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", repo, "add", "tracked.txt").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v (%s)", err, out)
	}
	if out, err := exec.Command("git", "-C", repo, "commit", "-qm", "base").CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v (%s)", err, out)
	}
	return repo
}

func TestSnapshotUnitDisplayName(t *testing.T) {
	t.Parallel()
	u := &snapshotUnit{Ref: localDirectoryRef{Label: "multica"}, AbsPath: "/home/me/multica", Subdir: "packages/core"}
	if got := u.displayName(); got != "multica/packages/core" {
		t.Fatalf("label wins and subdir joins, got %q", got)
	}
	u2 := &snapshotUnit{AbsPath: "/home/me/multica", Subdir: ""}
	if got := u2.displayName(); got != "multica" {
		t.Fatalf("basename when unlabeled, got %q", got)
	}
}

func TestMaterializeSnapshotTrees(t *testing.T) {
	t.Parallel()
	envRoot := t.TempDir()
	dir := t.TempDir()
	plan := &snapshotPlan{
		Primary: &snapshotUnit{AbsPath: filepath.Clean(dir)},
		Units:   []snapshotUnit{{AbsPath: filepath.Clean(dir)}},
	}
	upper, err := plan.materializeTrees(envRoot, "11111111-1111-1111-1111-111111111111", "task-1")
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	base := filepath.Join(execenv.SnapshotRoot(envRoot))
	entries, readErr := os.ReadDir(base)
	if readErr != nil {
		t.Fatalf("snapshot root: %v", readErr)
	}
	if len(entries) != 1 {
		t.Fatalf("one stem per undeclared task, got %d entries", len(entries))
	}
	for _, name := range []string{"pristine", "upper", "work"} {
		if _, err := os.Stat(filepath.Join(base, entries[0].Name(), name)); err != nil {
			t.Errorf("%s dir must exist: %v", name, err)
		}
	}
	if !strings.HasSuffix(upper, filepath.Join(entries[0].Name(), "upper")) {
		t.Fatalf("primary upper must sit in the stem, got %q", upper)
	}
	// Idempotent: the run-brief block re-runs this defensively.
	if _, err := plan.materializeTrees(envRoot, "11111111-1111-1111-1111-111111111111", "task-1"); err != nil {
		t.Fatalf("materialize must be idempotent: %v", err)
	}
}

// waitingServerForTest counts MarkTaskWaitingLocalDirectory calls the way the
// serialise test observes the waiting_local_directory state flip.
func waitingServerForTest(t *testing.T, counter *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if strings.HasSuffix(req.URL.Path, "/wait-local-directory") {
			counter.Add(1)
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestSnapshotUndeclaredTasksRunLockFree is the daemon-layer half of the
// acceptance-1 negative assertion: two tasks from DIFFERENT issues bound to
// the SAME directory, both undeclared, must acquire nothing and report no
// waiting_local_directory — their isolation is the private upperdir, and
// lock-freedom is what makes the concurrency real.
func TestSnapshotUndeclaredTasksRunLockFree(t *testing.T) {
	t.Parallel()
	const daemonID = "d-mine"
	dir := t.TempDir()
	var waitCalls atomic.Int32
	srv := waitingServerForTest(t, &waitCalls)

	d := &Daemon{
		cfg:                Config{DaemonID: daemonID},
		client:             NewClient(srv.URL),
		localPathLocks:     NewLocalPathLocker(),
		logger:             slog.Default(),
		cancelPollInterval: time.Hour,
	}
	res := snapshotResourceForTest(t, "r1", dir, daemonID, true)
	taskA := Task{ID: "task-a", IssueID: "issue-1", ProjectResources: []ProjectResourceData{res}}
	taskB := Task{ID: "task-b", IssueID: "issue-2", ProjectResources: []ProjectResourceData{res}}

	// True concurrency: both acquisitions in flight together. If either took
	// the whole-path mutex the second would park and this channel never
	// closes within the grace window below.
	var wg sync.WaitGroup
	releaseA, abortA, releaseB, abortB := func() {}, false, func() {}, false
	wg.Add(2)
	go func() {
		defer wg.Done()
		releaseA, abortA = d.acquireLocalDirectoryLockIfNeeded(context.Background(), taskA, slog.Default())
	}()
	go func() {
		defer wg.Done()
		releaseB, abortB = d.acquireLocalDirectoryLockIfNeeded(context.Background(), taskB, slog.Default())
	}()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("undeclared snapshot tasks contended — the acceptance-1 lock-freedom broke")
	}
	if abortA || abortB {
		t.Fatalf("neither task may abort, got abortA=%v abortB=%v", abortA, abortB)
	}
	if releaseA != nil || releaseB != nil {
		t.Fatalf("undeclared snapshot tasks hold no lock to release, got A!=nil=%v B!=nil=%v", releaseA != nil, releaseB != nil)
	}
	if calls := waitCalls.Load(); calls != 0 {
		t.Fatalf("no task may report waiting_local_directory, saw %d calls", calls)
	}

	// Belt and suspenders: the whole-path mutex is demonstrably still free —
	// an ordinary third task acquires it instantly.
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	probeCtx, probeCancel := context.WithTimeout(context.Background(), time.Second)
	defer probeCancel()
	probeRelease, err := d.localPathLocks.Acquire(probeCtx, real, "task-probe", func(string) {})
	if err != nil {
		t.Fatalf("whole-path mutex must stay free under undeclared snapshot tasks: %v", err)
	}
	probeRelease()
}

// TestSnapshotDeclaredScopesSerialiseOnSubdir is the acceptance-4 positive:
// two tasks declaring the SAME subdirectory of one snapshot resource
// serialize for the task's lifetime, and the later one reports
// waiting_local_directory while parked.
func TestSnapshotDeclaredScopesSerialiseOnSubdir(t *testing.T) {
	t.Parallel()
	const daemonID = "d-mine"
	dir := t.TempDir()
	sub := filepath.Join(dir, "packages", "core")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	var waitCalls atomic.Int32
	srv := waitingServerForTest(t, &waitCalls)

	d := &Daemon{
		cfg:                Config{DaemonID: daemonID},
		client:             NewClient(srv.URL),
		localPathLocks:     NewLocalPathLocker(),
		logger:             slog.Default(),
		cancelPollInterval: time.Hour,
	}
	res := snapshotResourceForTest(t, "r1", dir, daemonID, false, "packages/core")
	first := Task{
		ID:                  "task-1",
		IssueID:             "issue-1",
		ProjectResources:    []ProjectResourceData{res},
		LocalDirectoryScope: scopeJSONForTest(t, "r1", "packages/core"),
	}
	second := Task{
		ID:                  "task-2",
		IssueID:             "issue-2",
		ProjectResources:    []ProjectResourceData{res},
		LocalDirectoryScope: scopeJSONForTest(t, "r1", "packages/core"),
	}

	release, abort := d.acquireLocalDirectoryLockIfNeeded(context.Background(), first, slog.Default())
	if abort || release == nil {
		t.Fatalf("first declared task must hold its scope lock (abort=%v)", abort)
	}

	blocked := make(chan struct{})
	var secondRelease func()
	go func() {
		defer close(blocked)
		r, _ := d.acquireLocalDirectoryLockIfNeeded(context.Background(), second, slog.Default())
		secondRelease = r
	}()

	// Parked, which is the point; the parking task flips to
	// waiting_local_directory via the server call.
	deadline := time.Now().Add(2 * time.Second)
	for waitCalls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if waitCalls.Load() == 0 {
		t.Error("declared scope wait never reported waiting_local_directory")
	}

	release()
	select {
	case <-blocked:
	case <-time.After(2 * time.Second):
		t.Fatal("second task never won the scope lock after release")
	}
	if secondRelease != nil {
		secondRelease()
	}
}

// TestSnapshotDisjointDeclaredScopesRunInParallel: two tasks declaring
// DISJOINT subdirectories of one resource overlap completely — set-granular
// isolation, the other half of acceptance 4.
func TestSnapshotDisjointDeclaredScopesRunInParallel(t *testing.T) {
	t.Parallel()
	const daemonID = "d-mine"
	dir := t.TempDir()
	for _, sub := range []string{"packages/core", "apps/web"} {
		if err := os.MkdirAll(filepath.Join(dir, filepath.FromSlash(sub)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	var waitCalls atomic.Int32
	srv := waitingServerForTest(t, &waitCalls)

	d := &Daemon{
		cfg:                Config{DaemonID: daemonID},
		client:             NewClient(srv.URL),
		localPathLocks:     NewLocalPathLocker(),
		logger:             slog.Default(),
		cancelPollInterval: time.Hour,
	}
	res := snapshotResourceForTest(t, "r1", dir, daemonID, false, "packages/core", "apps/web")
	core := Task{
		ID:                  "task-core",
		IssueID:             "issue-1",
		ProjectResources:    []ProjectResourceData{res},
		LocalDirectoryScope: scopeJSONForTest(t, "r1", "packages/core"),
	}
	web := Task{
		ID:                  "task-web",
		IssueID:             "issue-2",
		ProjectResources:    []ProjectResourceData{res},
		LocalDirectoryScope: scopeJSONForTest(t, "r1", "apps/web"),
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if r, abort := d.acquireLocalDirectoryLockIfNeeded(context.Background(), core, slog.Default()); abort || r == nil {
			t.Error("core task must hold its scope lock")
		}
	}()
	go func() {
		defer wg.Done()
		if r, abort := d.acquireLocalDirectoryLockIfNeeded(context.Background(), web, slog.Default()); abort || r == nil {
			t.Error("web task must hold its scope lock")
		}
	}()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("disjoint declared scopes must not serialize")
	}
	if calls := waitCalls.Load(); calls != 0 {
		t.Fatalf("disjoint scopes never contend, saw %d waiting calls", calls)
	}
}

// TestSnapshotChatTaskSkipsScopeLock: a chat turn on a declared scope reads
// the tree like the user's own editor would — it keeps its routing but never
// queues behind a writer, the same contract as the in_place exemption.
func TestSnapshotChatTaskSkipsScopeLock(t *testing.T) {
	t.Parallel()
	const daemonID = "d-mine"
	dir := t.TempDir()
	sub := filepath.Join(dir, "packages", "core")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	var waitCalls atomic.Int32
	srv := waitingServerForTest(t, &waitCalls)

	d := &Daemon{
		cfg:                Config{DaemonID: daemonID},
		client:             NewClient(srv.URL),
		localPathLocks:     NewLocalPathLocker(),
		logger:             slog.Default(),
		cancelPollInterval: time.Hour,
	}
	res := snapshotResourceForTest(t, "r1", dir, daemonID, false, "packages/core")
	// Hold the scope lock as a writer would...
	writer := Task{
		ID:                  "writer",
		IssueID:             "issue-1",
		ProjectResources:    []ProjectResourceData{res},
		LocalDirectoryScope: scopeJSONForTest(t, "r1", "packages/core"),
	}
	release, abort := d.acquireLocalDirectoryLockIfNeeded(context.Background(), writer, slog.Default())
	if abort || release == nil {
		t.Fatalf("writer must hold the scope lock (abort=%v)", abort)
	}
	t.Cleanup(release)

	// ...then prove the chat task walks straight past it.
	chat := Task{
		ID:                  "chat-1",
		ChatSessionID:       "chat-1",
		IssueID:             "issue-2",
		ProjectResources:    []ProjectResourceData{res},
		LocalDirectoryScope: scopeJSONForTest(t, "r1", "packages/core"),
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if r, abort := d.acquireLocalDirectoryLockIfNeeded(context.Background(), chat, slog.Default()); abort || r != nil {
			t.Error("chat task takes no scope lock to release")
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("chat task must not queue behind the declared scope writer")
	}
}

// TestSnapshotMultiLockWaitCountsTaskOnce is the F7 regression: the gauge
// counts tasks waiting on a directory, not individual lock waits. A task
// declaring two subdirectories, both held, blocks twice — the gauge must net
// +1 while parked and drain to 0 after the task proceeds and releases.
func TestSnapshotMultiLockWaitCountsTaskOnce(t *testing.T) {
	t.Parallel()
	const daemonID = "d-mine"
	dir := t.TempDir()
	for _, sub := range []string{"apps/a", "apps/b"} {
		if err := os.MkdirAll(filepath.Join(dir, filepath.FromSlash(sub)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	var waitCalls atomic.Int32
	srv := waitingServerForTest(t, &waitCalls)
	d := &Daemon{
		cfg:                Config{DaemonID: daemonID},
		client:             NewClient(srv.URL),
		localPathLocks:     NewLocalPathLocker(),
		logger:             slog.Default(),
		cancelPollInterval: time.Hour,
	}
	res := snapshotResourceForTest(t, "r1", dir, daemonID, false, "apps/a", "apps/b")

	holdA := Task{ID: "task-a", IssueID: "issue-a", ProjectResources: []ProjectResourceData{res}, LocalDirectoryScope: scopeJSONForTest(t, "r1", "apps/a")}
	holdB := Task{ID: "task-b", IssueID: "issue-b", ProjectResources: []ProjectResourceData{res}, LocalDirectoryScope: scopeJSONForTest(t, "r1", "apps/b")}
	releaseA, abortA := d.acquireLocalDirectoryLockIfNeeded(context.Background(), holdA, slog.Default())
	if abortA || releaseA == nil {
		t.Fatalf("holder A must take its scope lock (abort=%v)", abortA)
	}
	releaseB, abortB := d.acquireLocalDirectoryLockIfNeeded(context.Background(), holdB, slog.Default())
	if abortB || releaseB == nil {
		t.Fatalf("holder B must take its scope lock (abort=%v)", abortB)
	}

	blocked := Task{ID: "task-c", IssueID: "issue-c", ProjectResources: []ProjectResourceData{res}, LocalDirectoryScope: scopeJSONForTest(t, "r1", "apps/a", "apps/b")}
	done := make(chan struct{})
	var releaseC func()
	go func() {
		defer close(done)
		r, _ := d.acquireLocalDirectoryLockIfNeeded(context.Background(), blocked, slog.Default())
		releaseC = r
	}()

	// Parked on the first lock (sorted order: apps/a): one wait reported,
	// the task counted exactly once.
	waitFor := func(n int32) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for waitCalls.Load() < n && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		if waitCalls.Load() < n {
			t.Fatalf("task never reported wait #%d", n)
		}
	}
	waitFor(1)
	if got := d.resourceWaitTasks.Load(); got != 1 {
		t.Errorf("waiting task must count once on its first blocking lock, got %d", got)
	}

	// First lock freed: the task advances to the second lock and blocks
	// AGAIN — the gauge must stay at 1 (one waiting task), not drift to 2.
	releaseA()
	waitFor(2)
	if got := d.resourceWaitTasks.Load(); got != 1 {
		t.Errorf("second blocking lock must not count the task twice, got %d", got)
	}

	releaseB()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("blocked task never acquired both locks after release")
	}
	if releaseC != nil {
		releaseC()
	}
	if got := d.resourceWaitTasks.Load(); got != 0 {
		t.Errorf("gauge must drain to zero after the task proceeds and releases, got %d", got)
	}
}

// TestLocalDirectoryGCExemption is the F8 contract: only runs that actually
// execute inside the user's tree or host the snapshot retention surface earn
// the GC exemption — a snapshot task whose root is a git repo routes to the
// worktree flow, and its env root must stay disposable like any worktree
// task's.
func TestLocalDirectoryGCExemption(t *testing.T) {
	t.Parallel()
	const daemonID = "d-mine"

	plain := t.TempDir()
	snapTask := Task{ID: "t1", ProjectResources: []ProjectResourceData{snapshotResourceForTest(t, "r1", plain, daemonID, true)}}
	if !localDirectoryGCExemption(snapTask, daemonID) {
		t.Error("snapshot run on a non-git root must stamp the GC exemption")
	}

	gitDir := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", gitDir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v (%s)", err, out)
	}
	gitTask := Task{ID: "t2", ProjectResources: []ProjectResourceData{snapshotResourceForTest(t, "r2", gitDir, daemonID, true)}}
	if localDirectoryGCExemption(gitTask, daemonID) {
		t.Error("git-root snapshot task routes to the worktree flow and must NOT stamp the exemption")
	}

	localResource := func(id, mode string) ProjectResourceData {
		t.Helper()
		raw, err := json.Marshal(localDirectoryRef{LocalPath: plain, DaemonID: daemonID, ExecutionMode: mode})
		if err != nil {
			t.Fatalf("marshal ref: %v", err)
		}
		return ProjectResourceData{ID: id, ResourceType: localDirectoryResourceType, ResourceRef: raw}
	}
	if !localDirectoryGCExemption(Task{ID: "t3", ProjectResources: []ProjectResourceData{localResource("r3", localDirectoryModeInPlace)}}, daemonID) {
		t.Error("in_place assignment runs inside the user's tree and must stamp the exemption")
	}
	if localDirectoryGCExemption(Task{ID: "t4", ProjectResources: []ProjectResourceData{localResource("r4", localDirectoryModeWorktree)}}, daemonID) {
		t.Error("worktree assignment owns a disposable env root and must NOT stamp the exemption")
	}
	if localDirectoryGCExemption(Task{ID: "t5"}, daemonID) {
		t.Error("no local_directory involvement → no exemption")
	}
}
