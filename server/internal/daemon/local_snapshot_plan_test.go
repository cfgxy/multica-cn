package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
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

func worktreeResourceForTest(t *testing.T, id, dir, daemonID string) ProjectResourceData {
	return worktreeResourceWithSubdirectoriesForTest(t, id, dir, daemonID)
}

func worktreeResourceWithSubdirectoriesForTest(t *testing.T, id, dir, daemonID string, subdirs ...string) ProjectResourceData {
	t.Helper()
	raw, err := json.Marshal(localDirectoryRef{
		LocalPath:      dir,
		DaemonID:       daemonID,
		ExecutionMode:  localDirectoryModeWorktree,
		Subdirectories: subdirs,
	})
	if err != nil {
		t.Fatalf("marshal worktree ref: %v", err)
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

func TestSnapshotScopeLockRejectsResourceSymlinkRetargetAfterLock(t *testing.T) {
	t.Parallel()
	scratch := t.TempDir()
	repoA := filepath.Join(scratch, "repo-a")
	repoB := filepath.Join(scratch, "repo-b")
	resourcePath := filepath.Join(scratch, "resource")
	for _, dir := range []string{filepath.Join(repoA, "src"), filepath.Join(repoB, "src")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(repoB, "sentinel"), []byte("must-not-be-touched\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(repoA, resourcePath); err != nil {
		t.Fatal(err)
	}

	const daemonID = "daemon-toctou"
	resource := snapshotResourceForTest(t, "resource-1", resourcePath, daemonID, true, "src")
	task := Task{
		ID:                  "task-toctou",
		IssueID:             "issue-toctou",
		ProjectResources:    []ProjectResourceData{resource},
		LocalDirectoryScope: scopeJSONForTest(t, resource.ID, "src"),
	}
	srv := waitingServerForTest(t, new(atomic.Int32))
	defer srv.Close()
	d := &Daemon{
		cfg:            Config{DaemonID: daemonID},
		client:         NewClient(srv.URL),
		localPathLocks: NewLocalPathLocker(),
		logger:         slog.Default(),
	}
	d.client.snapshotSupport.Store(true)

	release, abort := d.acquireLocalDirectoryLockIfNeeded(context.Background(), task, slog.Default())
	if abort || release == nil {
		t.Fatalf("snapshot scope lock acquisition failed, abort=%v release=%v", abort, release != nil)
	}
	defer release()
	if err := os.Remove(resourcePath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(repoB, resourcePath); err != nil {
		t.Fatal(err)
	}

	plan, err := resolveSnapshotPlan(task, daemonID)
	if err != nil {
		t.Fatal(err)
	}
	rawBinding, ok := d.snapshotLockBindings.LoadAndDelete(task.ID)
	if !ok {
		t.Fatal("snapshot scope lock did not record a binding")
	}
	binding, ok := rawBinding.(*snapshotLockBinding)
	if !ok {
		t.Fatalf("snapshot lock binding type = %T", rawBinding)
	}
	if err := applySnapshotLockBinding(plan, binding); err == nil {
		t.Fatal("retargeted resource symlink must fail closed before materialization")
	}
	if got, err := os.ReadFile(filepath.Join(repoB, "sentinel")); err != nil || string(got) != "must-not-be-touched\n" {
		t.Fatalf("retargeted directory changed, contents=%q err=%v", got, err)
	}
}

func TestResolveSnapshotPlanUsesWorktreeResourceOnlyWithGitSnapshotGate(t *testing.T) {
	plainTask := Task{
		ID:               "plain",
		ProjectResources: []ProjectResourceData{worktreeResourceForTest(t, "plain", t.TempDir(), "d1")},
	}
	gitRoot := newTestGitRoot(t)
	task := Task{
		ID:               "t1",
		ProjectResources: []ProjectResourceData{worktreeResourceForTest(t, "r1", gitRoot, "d1")},
	}

	t.Setenv("MULTICA_ENABLE_GIT_SNAPSHOT", "")
	plan, err := resolveSnapshotPlan(task, "d1")
	if err != nil || plan != nil {
		t.Fatalf("disabled gate must preserve the worktree path, plan=%+v err=%v", plan, err)
	}

	t.Setenv("MULTICA_ENABLE_GIT_SNAPSHOT", "1")
	plan, err = resolveSnapshotPlan(plainTask, "d1")
	if err != nil || plan != nil {
		t.Fatalf("enabled gate must preserve a non-Git worktree backend, plan=%+v err=%v", plan, err)
	}

	plan, err = resolveSnapshotPlan(task, "d1")
	if err != nil || plan == nil || plan.Primary == nil || plan.Primary.AbsPath != filepath.Clean(gitRoot) {
		t.Fatalf("enabled gate must plan the ordinary worktree resource, plan=%+v err=%v", plan, err)
	}
	if fallback, err := snapshotWorktreeFallback(plan); err != nil || fallback != nil {
		t.Fatalf("eligible ordinary worktree resource must enter snapshot delivery, fallback=%+v err=%v", fallback, err)
	}
	if fallback, err := snapshotWorktreeFallbackWithGitGateAndCapability(plan, true, false); err != nil || fallback == nil || !fallback.UsesWorktree() {
		t.Fatalf("incapable daemon must preserve the worktree fallback, fallback=%+v err=%v", fallback, err)
	} else if got := snapshotExecutionObservationFor(plan, fallback, true, false); got.DowngradeCode != "git_snapshot_capability_unavailable" || got.ExecutionMode != localDirectoryModeWorktree {
		t.Fatalf("incapable worktree observation = %+v", got)
	}

	nested := filepath.Join(gitRoot, "child")
	if out, err := exec.Command("git", "init", "-q", nested).CombinedOutput(); err != nil {
		t.Fatalf("git init nested: %v (%s)", err, out)
	}
	plan, err = resolveSnapshotPlan(task, "d1")
	if err != nil || plan == nil {
		t.Fatalf("resolve nested worktree candidate: plan=%+v err=%v", plan, err)
	}
	if fallback, err := snapshotWorktreeFallback(plan); err != nil || fallback == nil || !fallback.UsesWorktree() {
		t.Fatalf("nested repository must preserve the worktree fallback, fallback=%+v err=%v", fallback, err)
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

func TestResolveSnapshotPlanRejectsDeclaredScopeSymlinkEscapesResourceRoot(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		subdir string
		setup  func(t *testing.T, root, outside string)
	}{
		{
			name:   "leaf",
			subdir: "src/link",
			setup: func(t *testing.T, root, outside string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Join(root, "src", "link")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:   "intermediate",
			subdir: "src/link/child",
			setup: func(t *testing.T, root, outside string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(filepath.Join(outside, "child"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Join(root, "src", "link")); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "resource")
			outside := filepath.Join(t.TempDir(), "outside")
			if err := os.MkdirAll(outside, 0o755); err != nil {
				t.Fatal(err)
			}
			tc.setup(t, root, outside)
			resource := snapshotResourceForTest(t, "r1", root, "d1", true, "src")
			task := Task{
				ID:                  "symlink-escape-" + tc.name,
				ProjectResources:    []ProjectResourceData{resource},
				LocalDirectoryScope: scopeJSONForTest(t, resource.ID, tc.subdir),
			}
			if plan, err := resolveSnapshotPlan(task, "d1"); err == nil || !strings.Contains(err.Error(), "escapes resource root") {
				t.Fatalf("escaped declared scope must fail before planning, plan=%+v err=%v", plan, err)
			}
		})
	}
}

func TestResolveSnapshotPlanRejectsCanonicalScopeAliases(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	real := filepath.Join(root, "real")
	alias := filepath.Join(root, "alias")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, alias); err != nil {
		t.Fatal(err)
	}
	resource := snapshotResourceForTest(t, "r1", root, "d1", true, "real", "alias")
	task := Task{
		ID:                  "canonical-alias",
		ProjectResources:    []ProjectResourceData{resource},
		LocalDirectoryScope: scopeJSONForTest(t, resource.ID, "real", "alias"),
	}
	if plan, err := resolveSnapshotPlan(task, "d1"); err == nil || !strings.Contains(err.Error(), "same directory") {
		t.Fatalf("canonical scope aliases must fail closed, plan=%+v err=%v", plan, err)
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

	parent := newTestGitRoot(t)
	linked := filepath.Join(t.TempDir(), "linked")
	if out, err := exec.Command("git", "-C", parent, "worktree", "add", "-q", linked).CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v (%s)", err, out)
	}
	linkedPlan := &snapshotPlan{Units: []snapshotUnit{{AbsPath: linked}}, Primary: &snapshotUnit{Ref: localDirectoryRef{LocalPath: linked}, AbsPath: linked}}
	if fb, err := snapshotWorktreeFallback(linkedPlan); err != nil || fb == nil || !fb.UsesWorktree() {
		t.Fatalf("linked worktree with external common dir must fall back, fb=%v err=%v", fb, err)
	}

	nested := newTestGitRoot(t)
	if out, err := exec.Command("git", "init", "-q", filepath.Join(nested, "child")).CombinedOutput(); err != nil {
		t.Fatalf("git init nested: %v (%s)", err, out)
	}
	nestedPlan := &snapshotPlan{Units: []snapshotUnit{{AbsPath: nested}}, Primary: &snapshotUnit{Ref: localDirectoryRef{LocalPath: nested}, AbsPath: nested}}
	if fb, err := snapshotWorktreeFallback(nestedPlan); err != nil || fb == nil || !fb.UsesWorktree() {
		t.Fatalf("nested Git inside snapshot scope must fall back, fb=%v err=%v", fb, err)
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
	if audit := auditSnapshotGitScope(linkedPlan); !audit.HasGit || !audit.Safe || len(audit.Dependencies) != 1 {
		t.Fatalf("linked worktree with external common dir must produce one auditable dependency, got %+v", audit)
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

	withAlternate := newTestGitRoot(t)
	alternateSource := newTestGitRoot(t)
	alternateObjects := strings.TrimSpace(runTopologyGit(t, alternateSource, "rev-parse", "--git-path", "objects"))
	if !filepath.IsAbs(alternateObjects) {
		alternateObjects = filepath.Join(alternateSource, alternateObjects)
	}
	alternatesFile := filepath.Join(withAlternate, ".git", "objects", "info", "alternates")
	if err := os.WriteFile(alternatesFile, []byte(alternateObjects+"\n"), 0o600); err != nil {
		t.Fatalf("write object alternate: %v", err)
	}
	alternatePlan := &snapshotPlan{Units: []snapshotUnit{{AbsPath: withAlternate}}, Primary: &snapshotUnit{AbsPath: withAlternate}}
	audit := auditSnapshotGitScope(alternatePlan)
	if !audit.HasGit || !audit.Safe || !withinAnyScope(alternateObjects, audit.Dependencies) {
		t.Fatalf("external alternate object database must be auditable, got %+v", audit)
	}
	if fallback, err := snapshotWorktreeFallbackWithGitGate(alternatePlan, true); err != nil || fallback != nil {
		t.Fatalf("single repository with alternate objects must remain eligible, fallback=%+v err=%v", fallback, err)
	}
	if len(alternatePlan.GitMetadataUnits) != 1 || alternatePlan.GitMetadataUnits[0].AbsPath != alternateObjects {
		t.Fatalf("alternate object database must receive its own metadata overlay, got %+v", alternatePlan.GitMetadataUnits)
	}
}

func TestAuditSnapshotGitScopeIgnoresGitLocationEnvironment(t *testing.T) {
	repo := newTestGitRoot(t)
	outside := newTestGitRoot(t)
	outsideGitDir := filepath.Join(outside, ".git")
	t.Setenv("GIT_DIR", outsideGitDir)
	t.Setenv("GIT_WORK_TREE", outside)
	t.Setenv("GIT_COMMON_DIR", outsideGitDir)
	t.Setenv("GIT_OBJECT_DIRECTORY", filepath.Join(outsideGitDir, "objects"))
	t.Setenv("GIT_ALTERNATE_OBJECT_DIRECTORIES", filepath.Join(outsideGitDir, "objects"))
	t.Setenv("GIT_INDEX_FILE", filepath.Join(outsideGitDir, "index"))

	audit := auditSnapshotGitScope(&snapshotPlan{
		Units:   []snapshotUnit{{AbsPath: repo}},
		Primary: &snapshotUnit{AbsPath: repo},
	})
	if !audit.HasGit || !audit.Safe || len(audit.Repositories) != 1 {
		t.Fatalf("audit must use the repository under the declared scope, got %+v", audit)
	}
	if got := audit.Repositories[0].Worktree; got != repo {
		t.Fatalf("audited worktree = %q, want %q", got, repo)
	}
	for _, dependency := range audit.Dependencies {
		if withinAnyScope(dependency, []string{outside}) {
			t.Fatalf("ambient Git location override leaked into dependency %q", dependency)
		}
	}
}

func TestAuditSnapshotGitScopeRejectsEscapingMetadataRedirects(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(t *testing.T, repo, outside string)
	}{
		{
			name: "refs directory symlink",
			mutate: func(t *testing.T, repo, outside string) {
				t.Helper()
				refs := filepath.Join(repo, ".git", "refs")
				if err := os.RemoveAll(refs); err != nil {
					t.Fatalf("remove refs: %v", err)
				}
				if err := os.MkdirAll(filepath.Join(outside, "heads"), 0o700); err != nil {
					t.Fatalf("create external refs sentinel: %v", err)
				}
				if err := os.Symlink(outside, refs); err != nil {
					t.Fatalf("link refs outside snapshot scope: %v", err)
				}
			},
		},
		{
			name: "index file symlink",
			mutate: func(t *testing.T, repo, outside string) {
				t.Helper()
				index := filepath.Join(repo, ".git", "index")
				if err := os.Remove(index); err != nil && !os.IsNotExist(err) {
					t.Fatalf("remove index: %v", err)
				}
				if err := os.Symlink(filepath.Join(outside, "sentinel"), index); err != nil {
					t.Fatalf("link index outside snapshot scope: %v", err)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repo := newTestGitRoot(t)
			outside := t.TempDir()
			if err := os.WriteFile(filepath.Join(outside, "sentinel"), []byte("must remain untouched\n"), 0o600); err != nil {
				t.Fatalf("write external sentinel: %v", err)
			}
			tc.mutate(t, repo, outside)
			before := treeDigest(t, outside, false)
			declared := filepath.Join(repo, "apps", "a")
			if err := os.MkdirAll(declared, 0o700); err != nil {
				t.Fatalf("create declared scope: %v", err)
			}
			ref := localDirectoryRef{LocalPath: repo, ExecutionMode: localDirectoryModeWorktree}
			plan := &snapshotPlan{
				Units:    []snapshotUnit{{Ref: ref, AbsPath: declared, Subdir: "apps/a"}},
				Primary:  &snapshotUnit{Ref: ref, AbsPath: declared, Subdir: "apps/a"},
				Declared: true,
			}

			audit := auditSnapshotGitScope(plan)
			if !audit.HasGit || audit.Safe || !strings.Contains(audit.Reason, "metadata redirect") {
				t.Fatalf("escaping Git metadata must fail audit, got %+v", audit)
			}
			fallback, err := snapshotWorktreeFallbackWithGitGate(plan, true)
			if err == nil || fallback != nil || !strings.Contains(err.Error(), "unsafe Git metadata") {
				t.Fatalf("unsafe metadata must fail closed before worktree preparation, fallback=%+v err=%v", fallback, err)
			}
			if after := treeDigest(t, outside, false); after != before {
				t.Fatalf("metadata audit and fallback must not touch external sentinel\nbefore: %s\nafter:  %s", before, after)
			}
		})
	}
}

func TestAuditSnapshotGitScopeRejectsMetadataSymlinkCycle(t *testing.T) {
	repo := newTestGitRoot(t)
	index := filepath.Join(repo, ".git", "index")
	if err := os.Remove(index); err != nil {
		t.Fatal(err)
	}
	cycleA := filepath.Join(repo, ".git", "cycle-a")
	cycleB := filepath.Join(repo, ".git", "cycle-b")
	if err := os.Symlink(cycleB, cycleA); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(cycleA, cycleB); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(cycleA, index); err != nil {
		t.Fatal(err)
	}

	plan := &snapshotPlan{Units: []snapshotUnit{{AbsPath: repo}}, Primary: &snapshotUnit{AbsPath: repo}}
	if audit := auditSnapshotGitScope(plan); audit.Safe || !strings.Contains(audit.Reason, "metadata redirect") {
		t.Fatalf("metadata symlink cycle must fail closed, got %+v", audit)
	}
}

func TestAuditSnapshotGitScopeHonorsCancellation(t *testing.T) {
	repo := newTestGitRoot(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	audit := auditSnapshotGitScopeContext(ctx, &snapshotPlan{Units: []snapshotUnit{{AbsPath: repo}}, Primary: &snapshotUnit{AbsPath: repo}})
	if audit.Safe || !strings.Contains(audit.Reason, "time budget") {
		t.Fatalf("cancelled audit must fail closed, got %+v", audit)
	}
}

func TestAuditSnapshotGitScopeAcceptsRefNamesMatchingMetadataFiles(t *testing.T) {
	repo := newTestGitRoot(t)
	for _, ref := range []string{"alternates", "commondir", "gitdir"} {
		runTopologyGit(t, repo, "update-ref", "refs/heads/"+ref, "HEAD")
	}
	plan := &snapshotPlan{Units: []snapshotUnit{{AbsPath: repo}}, Primary: &snapshotUnit{AbsPath: repo}}
	if audit := auditSnapshotGitScope(plan); !audit.HasGit || !audit.Safe {
		t.Fatalf("ordinary refs named like metadata files must remain safe, got %+v", audit)
	}
}

func TestAuditSnapshotGitScopeAuditsSharedHooksWithinWorktree(t *testing.T) {
	for _, tc := range []struct {
		name     string
		setup    func(t *testing.T, repo string)
		wantSafe bool
	}{
		{
			name: "shared hooks inside worktree",
			setup: func(t *testing.T, repo string) {
				t.Helper()
				shared := filepath.Join(repo, ".githooks")
				if err := os.Mkdir(shared, 0o700); err != nil {
					t.Fatalf("create shared hooks: %v", err)
				}
				if err := os.WriteFile(filepath.Join(shared, "pre-commit"), []byte("#!/bin/sh\n"), 0o700); err != nil {
					t.Fatalf("write shared hook: %v", err)
				}
				replaceTestGitHooksWithSymlink(t, repo, shared)
			},
			wantSafe: true,
		},
		{
			name: "external hooks",
			setup: func(t *testing.T, repo string) {
				t.Helper()
				external := filepath.Join(t.TempDir(), "hooks")
				if err := os.MkdirAll(external, 0o700); err != nil {
					t.Fatalf("create external hooks: %v", err)
				}
				replaceTestGitHooksWithSymlink(t, repo, external)
			},
			wantSafe: false,
		},
		{
			name: "shared hooks resolve outside worktree",
			setup: func(t *testing.T, repo string) {
				t.Helper()
				external := filepath.Join(t.TempDir(), "hooks")
				if err := os.MkdirAll(external, 0o700); err != nil {
					t.Fatalf("create external hooks: %v", err)
				}
				shared := filepath.Join(repo, ".githooks")
				if err := os.Symlink(external, shared); err != nil {
					t.Fatalf("redirect shared hooks outside worktree: %v", err)
				}
				replaceTestGitHooksWithSymlink(t, repo, shared)
			},
			wantSafe: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := newTestGitRoot(t)
			tc.setup(t, repo)
			ref := localDirectoryRef{LocalPath: repo, ExecutionMode: localDirectoryModeWorktree}
			unit := snapshotUnit{Ref: ref, AbsPath: repo}
			plan := &snapshotPlan{Units: []snapshotUnit{unit}, Primary: &unit}
			audit := auditSnapshotGitScope(plan)
			if audit.Safe != tc.wantSafe {
				t.Fatalf("shared hooks audit safe = %t, want %t: %+v", audit.Safe, tc.wantSafe, audit)
			}
		})
	}
}

func replaceTestGitHooksWithSymlink(t *testing.T, repo, target string) {
	t.Helper()
	hooks := filepath.Join(repo, ".git", "hooks")
	if err := os.RemoveAll(hooks); err != nil {
		t.Fatalf("remove default hooks: %v", err)
	}
	if err := os.Symlink(target, hooks); err != nil {
		t.Fatalf("redirect hooks: %v", err)
	}
}

func TestSnapshotWorktreeFallbackFailsClosedWhenGitAuditCannotComplete(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	ref := localDirectoryRef{LocalPath: missing, ExecutionMode: localDirectoryModeWorktree}
	plan := &snapshotPlan{Units: []snapshotUnit{{Ref: ref, AbsPath: missing}}, Primary: &snapshotUnit{Ref: ref, AbsPath: missing}}

	fallback, err := snapshotWorktreeFallbackWithGitGate(plan, true)
	if err == nil || fallback != nil || !strings.Contains(err.Error(), "unsafe Git metadata") {
		t.Fatalf("incomplete Git audit must fail closed, fallback=%+v err=%v", fallback, err)
	}
}

func TestWorktreeMetadataAuditSkipsSiblingLinkedWorktrees(t *testing.T) {
	repo := newTestGitRoot(t)
	sibling := filepath.Join(t.TempDir(), "sibling")
	runTopologyGit(t, repo, "worktree", "add", "-q", "--detach", sibling)

	assignment := &localDirectoryAssignment{
		Ref:     localDirectoryRef{LocalPath: repo, ExecutionMode: localDirectoryModeWorktree},
		AbsPath: repo,
	}
	if err := validateWorktreeGitMetadata(assignment); err != nil {
		t.Fatalf("sibling linked worktree must not block source worktree preparation: %v", err)
	}
}

func TestWorktreeMetadataAuditAcceptsLinkedWorktreeBacklink(t *testing.T) {
	repo := newTestGitRoot(t)
	linked := filepath.Join(t.TempDir(), "linked")
	runTopologyGit(t, repo, "worktree", "add", "-q", "--detach", linked)

	assignment := &localDirectoryAssignment{
		Ref:      localDirectoryRef{LocalPath: linked, ExecutionMode: localDirectoryModeWorktree},
		AbsPath:  linked,
		RealPath: linked,
	}
	if err := validateWorktreeGitMetadata(assignment); err != nil {
		t.Fatalf("standard linked-worktree backlink must remain safe for worktree preparation: %v", err)
	}
	plan := &snapshotPlan{Units: []snapshotUnit{{AbsPath: linked}}, Primary: &snapshotUnit{AbsPath: linked}}
	if audit := auditSnapshotGitScope(plan); !audit.HasGit || !audit.Safe {
		t.Fatalf("standard linked-worktree backlink must remain safe for snapshot audit, got %+v", audit)
	}
}

func TestAuditSnapshotGitScopeRejectsRedirectIntoSiblingWorktreeAdminState(t *testing.T) {
	repo := newTestGitRoot(t)
	sibling := filepath.Join(t.TempDir(), "sibling")
	runTopologyGit(t, repo, "worktree", "add", "-q", "--detach", sibling)
	siblingGitDir := strings.TrimSpace(runTopologyGit(t, sibling, "rev-parse", "--absolute-git-dir"))

	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(outside, "heads"), 0o700); err != nil {
		t.Fatalf("create external refs sentinel: %v", err)
	}
	redirectedRefs := filepath.Join(siblingGitDir, "redirected-refs")
	if err := os.MkdirAll(redirectedRefs, 0o700); err != nil {
		t.Fatalf("create sibling admin redirect: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(redirectedRefs, "heads")); err != nil {
		t.Fatalf("redirect sibling admin refs outside repository: %v", err)
	}
	refs := filepath.Join(repo, ".git", "refs")
	if err := os.RemoveAll(refs); err != nil {
		t.Fatalf("remove repository refs: %v", err)
	}
	if err := os.Symlink(redirectedRefs, refs); err != nil {
		t.Fatalf("redirect refs into sibling admin state: %v", err)
	}

	ref := localDirectoryRef{LocalPath: repo, ExecutionMode: localDirectoryModeWorktree}
	unit := snapshotUnit{Ref: ref, AbsPath: repo}
	plan := &snapshotPlan{Units: []snapshotUnit{unit}, Primary: &unit}
	if audit := auditSnapshotGitScope(plan); audit.Safe || !strings.Contains(audit.Reason, "sibling Git worktree admin state") {
		t.Fatalf("redirect into sibling worktree admin state must fail audit, got %+v", audit)
	}
}

func TestAuditSnapshotGitScopeRejectsRedirectIntoSiblingWorktreeAdminRoot(t *testing.T) {
	repo := newTestGitRoot(t)
	sibling := filepath.Join(t.TempDir(), "sibling")
	runTopologyGit(t, repo, "worktree", "add", "-q", "--detach", sibling)
	siblingGitDir := strings.TrimSpace(runTopologyGit(t, sibling, "rev-parse", "--absolute-git-dir"))
	worktreesRoot := filepath.Dir(siblingGitDir)

	refs := filepath.Join(repo, ".git", "refs")
	if err := os.RemoveAll(refs); err != nil {
		t.Fatalf("remove repository refs: %v", err)
	}
	if err := os.Symlink(worktreesRoot, refs); err != nil {
		t.Fatalf("redirect refs into sibling worktree admin root: %v", err)
	}

	plan := &snapshotPlan{Units: []snapshotUnit{{AbsPath: repo}}, Primary: &snapshotUnit{AbsPath: repo}}
	if audit := auditSnapshotGitScope(plan); audit.Safe || !strings.Contains(audit.Reason, "sibling Git worktree admin root") {
		t.Fatalf("redirect into sibling worktree admin root must fail audit, got %+v", audit)
	}
}

func TestSiblingGitWorktreeAdminDirsUsesLinkedGitDirWhenCommonDirRedirects(t *testing.T) {
	repo := newTestGitRoot(t)
	linked := filepath.Join(t.TempDir(), "linked")
	sibling := filepath.Join(t.TempDir(), "sibling")
	runTopologyGit(t, repo, "worktree", "add", "-q", "--detach", linked)
	runTopologyGit(t, repo, "worktree", "add", "-q", "--detach", sibling)
	linkedGitDir := strings.TrimSpace(runTopologyGit(t, linked, "rev-parse", "--absolute-git-dir"))
	siblingGitDir := strings.TrimSpace(runTopologyGit(t, sibling, "rev-parse", "--absolute-git-dir"))
	redirectedCommonDir := filepath.Join(siblingGitDir, "redirected-common")
	if err := os.MkdirAll(redirectedCommonDir, 0o700); err != nil {
		t.Fatalf("create redirected common directory: %v", err)
	}

	forbidden, _, err := siblingGitWorktreeAdminDirs([]string{redirectedCommonDir}, linkedGitDir)
	if err != nil {
		t.Fatalf("enumerate sibling worktree admin directories: %v", err)
	}
	if !withinAnyScope(siblingGitDir, forbidden) {
		t.Fatalf("linked GitDir must identify sibling admin state despite redirected commonDir, got %v", forbidden)
	}
}

func TestWorktreeMetadataAuditRejectsRedirectIntoAlternateObjectDatabase(t *testing.T) {
	repo := newTestGitRoot(t)
	alternate := newTestGitRoot(t)
	alternateObjects := strings.TrimSpace(runTopologyGit(t, alternate, "rev-parse", "--git-path", "objects"))
	if !filepath.IsAbs(alternateObjects) {
		alternateObjects = filepath.Join(alternate, alternateObjects)
	}
	if err := os.WriteFile(filepath.Join(repo, ".git", "objects", "info", "alternates"), []byte(alternateObjects+"\n"), 0o600); err != nil {
		t.Fatalf("write alternate object database: %v", err)
	}

	redirectedRefs := filepath.Join(alternateObjects, "redirected-refs")
	if err := os.MkdirAll(filepath.Join(redirectedRefs, "heads"), 0o700); err != nil {
		t.Fatalf("create alternate sentinel: %v", err)
	}
	refs := filepath.Join(repo, ".git", "refs")
	if err := os.RemoveAll(refs); err != nil {
		t.Fatalf("remove repository refs: %v", err)
	}
	if err := os.Symlink(redirectedRefs, refs); err != nil {
		t.Fatalf("redirect refs into alternate object database: %v", err)
	}

	before := treeDigest(t, alternate, false)
	assignment := &localDirectoryAssignment{
		Ref:     localDirectoryRef{LocalPath: repo, ExecutionMode: localDirectoryModeWorktree},
		AbsPath: repo,
	}
	plan := &snapshotPlan{Units: []snapshotUnit{{AbsPath: repo}}, Primary: &snapshotUnit{AbsPath: repo}}
	if audit := auditSnapshotGitScope(plan); audit.Safe || !strings.Contains(audit.Reason, "metadata redirect") {
		t.Fatalf("snapshot metadata must reject a writable redirect into an alternate object database, got %+v", audit)
	}
	if err := validateWorktreeGitMetadata(assignment); err == nil || !strings.Contains(err.Error(), "metadata redirect") {
		t.Fatalf("worktree metadata must reject a writable redirect into an alternate object database, got %v", err)
	}
	if after := treeDigest(t, alternate, false); after != before {
		t.Fatalf("metadata validation must not modify alternate object database\nbefore: %s\nafter:  %s", before, after)
	}
}

func TestWorktreeMetadataAuditSkipsNestedSubmoduleSiblingWorktrees(t *testing.T) {
	parent := newTestGitRoot(t)
	submoduleSource := newTestGitRoot(t)
	runTopologyGit(t, parent, "-c", "protocol.file.allow=always", "submodule", "add", "-q", submoduleSource, "dep")

	sibling := filepath.Join(t.TempDir(), "submodule-sibling")
	runTopologyGit(t, filepath.Join(parent, "dep"), "worktree", "add", "-q", "--detach", sibling)

	assignment := &localDirectoryAssignment{
		Ref:     localDirectoryRef{LocalPath: parent, ExecutionMode: localDirectoryModeWorktree},
		AbsPath: parent,
	}
	if err := validateWorktreeGitMetadata(assignment); err != nil {
		t.Fatalf("nested submodule sibling worktree must not block parent worktree preparation: %v", err)
	}
}

func TestAuditSnapshotGitScopeSkipsReftableSubmoduleSiblingWorktree(t *testing.T) {
	repo := newTestGitRoot(t)
	gitDir := filepath.Join(repo, ".git")
	commonDir := filepath.Join(repo, ".git", "modules", "dep")
	if err := os.MkdirAll(filepath.Join(commonDir, "objects"), 0o700); err != nil {
		t.Fatalf("create reftable object directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(commonDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o600); err != nil {
		t.Fatalf("write reftable HEAD: %v", err)
	}
	sibling := filepath.Join(commonDir, "worktrees", "sibling")
	if err := os.MkdirAll(sibling, 0o700); err != nil {
		t.Fatalf("create sibling worktree admin directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sibling, "gitdir"), []byte(filepath.Join(t.TempDir(), "sibling", ".git")+"\n"), 0o600); err != nil {
		t.Fatalf("write sibling gitdir backlink: %v", err)
	}

	if err := auditGitMetadataRedirectsWithin(repo, gitDir, gitDir, []string{gitDir}, []string{gitDir}, []string{gitDir}, []string{gitDir, commonDir}, true); err != nil {
		t.Fatalf("known reftable submodule sibling worktree must not make parent audit unsafe: %v", err)
	}
}

func TestAuditSnapshotGitScopeAuditsRefsWorktreesNamespace(t *testing.T) {
	repo := newTestGitRoot(t)
	refs := filepath.Join(repo, ".git", "refs")
	if err := os.WriteFile(filepath.Join(refs, "HEAD"), []byte("not a symbolic ref\n"), 0o600); err != nil {
		t.Fatalf("write ref namespace HEAD: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(refs, "objects"), 0o700); err != nil {
		t.Fatalf("create ref namespace objects: %v", err)
	}
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(outside, "heads"), 0o700); err != nil {
		t.Fatalf("create external ref sentinel: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(refs, "worktrees"), 0o700); err != nil {
		t.Fatalf("create refs worktrees namespace: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(refs, "worktrees", "heads")); err != nil {
		t.Fatalf("redirect ref namespace through symlink: %v", err)
	}

	plan := &snapshotPlan{Units: []snapshotUnit{{AbsPath: repo}}, Primary: &snapshotUnit{AbsPath: repo}}
	if audit := auditSnapshotGitScope(plan); audit.Safe || !strings.Contains(audit.Reason, "metadata redirect") {
		t.Fatalf("refs/worktrees namespace redirect must fail audit, got %+v", audit)
	}
}

func TestRevalidateSnapshotGitPlanRejectsMetadataChangeAfterPlanning(t *testing.T) {
	repo := newTestGitRoot(t)
	ref := localDirectoryRef{LocalPath: repo, ExecutionMode: localDirectoryModeSnapshot}
	unit := snapshotUnit{Ref: ref, AbsPath: repo}
	plan := &snapshotPlan{Units: []snapshotUnit{unit}, Primary: &unit}
	if fallback, err := snapshotWorktreeFallbackWithGitGateAndCapability(plan, true, true); err != nil || fallback != nil {
		t.Fatalf("prepare snapshot plan: fallback=%+v err=%v", fallback, err)
	}

	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(outside, "heads"), 0o700); err != nil {
		t.Fatalf("create external refs sentinel: %v", err)
	}
	refs := filepath.Join(repo, ".git", "refs")
	if err := os.RemoveAll(refs); err != nil {
		t.Fatalf("remove refs: %v", err)
	}
	if err := os.Symlink(outside, refs); err != nil {
		t.Fatalf("redirect refs outside repository: %v", err)
	}

	if _, err := revalidateSnapshotGitPlan(plan, true); err == nil || !strings.Contains(err.Error(), "unsafe Git metadata") {
		t.Fatalf("metadata changed after planning must fail snapshot startup, got %v", err)
	}
}

func TestSnapshotGitConfigurationRejectsExternalHooksAndIncludes(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, repo, outside string)
	}{
		{
			name: "external core hooks path",
			setup: func(t *testing.T, repo, outside string) {
				t.Helper()
				hooks := filepath.Join(outside, "hooks")
				if err := os.MkdirAll(hooks, 0o700); err != nil {
					t.Fatal(err)
				}
				hook := "#!/bin/sh\nprintf hook-ran > " + filepath.Join(outside, "hook-ran") + "\n"
				if err := os.WriteFile(filepath.Join(hooks, "pre-commit"), []byte(hook), 0o700); err != nil {
					t.Fatal(err)
				}
				if out, err := exec.Command("git", "-C", repo, "config", "core.hooksPath", hooks).CombinedOutput(); err != nil {
					t.Fatalf("set external hooks path: %v (%s)", err, out)
				}
			},
		},
		{
			name: "external include hooks path",
			setup: func(t *testing.T, repo, outside string) {
				t.Helper()
				hooks := filepath.Join(outside, "hooks")
				if err := os.MkdirAll(hooks, 0o700); err != nil {
					t.Fatal(err)
				}
				hook := "#!/bin/sh\nprintf hook-ran > " + filepath.Join(outside, "hook-ran") + "\n"
				if err := os.WriteFile(filepath.Join(hooks, "pre-commit"), []byte(hook), 0o700); err != nil {
					t.Fatal(err)
				}
				config := filepath.Join(outside, "hooks.gitconfig")
				if err := os.WriteFile(config, []byte("[core]\n\thooksPath = "+hooks+"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				if out, err := exec.Command("git", "-C", repo, "config", "include.path", config).CombinedOutput(); err != nil {
					t.Fatalf("include external hooks config: %v (%s)", err, out)
				}
			},
		},
		{
			name: "inactive external conditional include",
			setup: func(t *testing.T, repo, outside string) {
				t.Helper()
				config := filepath.Join(outside, "hooks.gitconfig")
				if err := os.WriteFile(config, []byte("[core]\n\thooksPath = "+filepath.Join(outside, "hooks")+"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				if out, err := exec.Command("git", "-C", repo, "config", "includeIf.onbranch:does-not-match.path", config).CombinedOutput(); err != nil {
					t.Fatalf("include inactive external hooks config: %v (%s)", err, out)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := newTestGitRoot(t)
			outside := t.TempDir()
			if err := os.WriteFile(filepath.Join(outside, "sentinel"), []byte("must remain untouched\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			tc.setup(t, repo, outside)
			before := treeDigest(t, outside, false)
			ref := localDirectoryRef{LocalPath: repo, ExecutionMode: localDirectoryModeWorktree}
			unit := snapshotUnit{Ref: ref, AbsPath: repo}
			plan := &snapshotPlan{Units: []snapshotUnit{unit}, Primary: &unit}
			audit := auditSnapshotGitScope(plan)
			if !audit.Safe || !audit.HasGit {
				t.Fatalf("metadata audit must remain safe before config audit, got %+v", audit)
			}
			if err := auditSnapshotGitConfiguration(audit); err == nil || !strings.Contains(err.Error(), "outside materialized dependencies") {
				t.Fatalf("external hook configuration must reject snapshot admission, got %v", err)
			}
			fallback, err := snapshotWorktreeFallbackWithGitGateAndCapability(plan, true, true)
			if err == nil || fallback != nil || !strings.Contains(err.Error(), "unsafe Git configuration") {
				t.Fatalf("unsafe snapshot configuration must fail closed, fallback=%+v err=%v", fallback, err)
			}
			if after := treeDigest(t, outside, false); after != before {
				t.Fatalf("snapshot admission executed an external hook\nbefore: %s\nafter:  %s", before, after)
			}
		})
	}
}

func TestSnapshotGitConfigurationRejectsIncludeCycle(t *testing.T) {
	repo := newTestGitRoot(t)
	configA := filepath.Join(repo, ".git", "config-a")
	configB := filepath.Join(repo, ".git", "config-b")
	if err := os.WriteFile(configA, []byte("[include]\n\tpath = "+configB+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configB, []byte("[include]\n\tpath = "+configA+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(repo, ".git", "config")
	raw, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, append(raw, []byte("[includeIf \"gitdir:/never-match/\"]\n\tpath = "+configA+"\n")...), 0o600); err != nil {
		t.Fatal(err)
	}

	plan := &snapshotPlan{Units: []snapshotUnit{{AbsPath: repo}}, Primary: &snapshotUnit{AbsPath: repo}}
	audit := auditSnapshotGitScope(plan)
	if !audit.Safe {
		t.Fatalf("metadata audit must complete before config audit, got %+v", audit)
	}
	if err := auditSnapshotGitConfiguration(audit); err == nil {
		t.Fatal("Git include cycle must fail closed")
	}
}

func TestSnapshotGitConfigurationAcceptsMaterializedHooksPath(t *testing.T) {
	repo := newTestGitRoot(t)
	hooks := filepath.Join(repo, ".githooks")
	if err := os.MkdirAll(hooks, 0o700); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", repo, "config", "core.hooksPath", ".githooks").CombinedOutput(); err != nil {
		t.Fatalf("configure materialized hooks path: %v (%s)", err, out)
	}
	ref := localDirectoryRef{LocalPath: repo, ExecutionMode: localDirectoryModeWorktree}
	unit := snapshotUnit{Ref: ref, AbsPath: repo}
	plan := &snapshotPlan{Units: []snapshotUnit{unit}, Primary: &unit}
	audit := auditSnapshotGitScope(plan)
	if err := auditSnapshotGitConfiguration(audit); err != nil {
		t.Fatalf("materialized hooks path must be safe: %v", err)
	}
	if fallback, err := snapshotWorktreeFallbackWithGitGateAndCapability(plan, true, true); err != nil || fallback != nil {
		t.Fatalf("materialized hooks path must allow snapshot delivery, fallback=%+v err=%v", fallback, err)
	}
}

func TestGitSnapshotDeliveryEligibleIsNarrow(t *testing.T) {
	gitRoot := newTestGitRoot(t)
	ordinary := &snapshotPlan{Units: []snapshotUnit{{AbsPath: gitRoot}}, Primary: &snapshotUnit{AbsPath: gitRoot}}
	if !gitSnapshotDeliveryEligible(ordinary, true) {
		t.Fatal("ordinary single-root repository must be eligible when the gate is enabled")
	}
	if gitSnapshotDeliveryEligible(ordinary, false) {
		t.Fatal("delivery gate must default to a worktree fallback")
	}
	if fallback, err := snapshotWorktreeFallbackWithGitGate(ordinary, true); err != nil || fallback != nil {
		t.Fatalf("enabled ordinary repository must enter snapshot delivery, fallback=%+v err=%v", fallback, err)
	}
	declaredSubdir := filepath.Join(gitRoot, "apps", "a")
	if err := os.MkdirAll(declaredSubdir, 0o755); err != nil {
		t.Fatal(err)
	}
	declared := &snapshotPlan{
		Units:    []snapshotUnit{{AbsPath: declaredSubdir, Subdir: "apps/a"}},
		Primary:  &snapshotUnit{AbsPath: declaredSubdir, Subdir: "apps/a"},
		Declared: true,
	}
	if !gitSnapshotDeliveryEligible(declared, true) {
		t.Fatal("single declared subdirectory of an ordinary repository must be eligible")
	}
	if fallback, err := snapshotWorktreeFallbackWithGitGate(declared, true); err != nil || fallback != nil {
		t.Fatalf("eligible declared subdirectory must enter snapshot delivery, fallback=%+v err=%v", fallback, err)
	}
	if len(declared.GitContainmentUnits) != 1 || declared.GitContainmentUnits[0].AbsPath != gitRoot {
		t.Fatalf("declared subdirectory must materialize a repository containment unit, got %+v", declared.GitContainmentUnits)
	}
	if len(declared.GitMetadataUnits) != 1 || declared.GitMetadataUnits[0].AbsPath != filepath.Join(gitRoot, ".git") {
		t.Fatalf("declared subdirectory must materialize repository .git metadata, got %+v", declared.GitMetadataUnits)
	}

	nested := newTestGitRoot(t)
	if out, err := exec.Command("git", "init", "-q", filepath.Join(nested, "child")).CombinedOutput(); err != nil {
		t.Fatalf("git init nested: %v (%s)", err, out)
	}
	nestedPlan := &snapshotPlan{Units: []snapshotUnit{{AbsPath: nested}}, Primary: &snapshotUnit{AbsPath: nested}}
	if gitSnapshotDeliveryEligible(nestedPlan, true) {
		t.Fatal("nested Git must keep the worktree fallback")
	}

	parent := newTestGitRoot(t)
	linked := filepath.Join(t.TempDir(), "linked")
	if out, err := exec.Command("git", "-C", parent, "worktree", "add", "-q", linked).CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v (%s)", err, out)
	}
	linkedPlan := &snapshotPlan{Units: []snapshotUnit{{AbsPath: linked}}, Primary: &snapshotUnit{AbsPath: linked}}
	if !gitSnapshotDeliveryEligible(linkedPlan, true) {
		t.Fatal("single linked worktree with auditable common metadata must be eligible")
	}
	if fallback, err := snapshotWorktreeFallbackWithGitGate(linkedPlan, true); err != nil || fallback != nil {
		t.Fatalf("eligible linked worktree must enter snapshot delivery, fallback=%+v err=%v", fallback, err)
	}
	if len(linkedPlan.GitMetadataUnits) != 1 {
		t.Fatalf("linked worktree must materialize one external Git metadata unit, got %+v", linkedPlan.GitMetadataUnits)
	}
	linkedSubdir := filepath.Join(linked, "apps", "a")
	if err := os.MkdirAll(linkedSubdir, 0o755); err != nil {
		t.Fatal(err)
	}
	linkedSubdirPlan := &snapshotPlan{
		Units:   []snapshotUnit{{AbsPath: linkedSubdir, Subdir: "apps/a"}},
		Primary: &snapshotUnit{AbsPath: linkedSubdir, Subdir: "apps/a"},
	}
	if gitSnapshotDeliveryEligible(linkedSubdirPlan, true) {
		t.Fatal("declared subdirectory of linked worktree must retain the worktree fallback")
	}

	declaredSubmodule := newTestGitRoot(t)
	if err := os.WriteFile(filepath.Join(declaredSubmodule, ".gitmodules"), []byte("[submodule \"dep\"]\n\tpath = dep\n\turl = ../dep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitRun := exec.Command("git", "-C", declaredSubmodule, "add", ".gitmodules")
	if out, err := gitRun.CombinedOutput(); err != nil {
		t.Fatalf("git add .gitmodules: %v (%s)", err, out)
	}
	if out, err := exec.Command("git", "-C", declaredSubmodule, "commit", "-qm", "declare submodule").CombinedOutput(); err != nil {
		t.Fatalf("git commit .gitmodules: %v (%s)", err, out)
	}
	declaredSubmodulePlan := &snapshotPlan{Units: []snapshotUnit{{AbsPath: declaredSubmodule}}, Primary: &snapshotUnit{AbsPath: declaredSubmodule}}
	if gitSnapshotDeliveryEligible(declaredSubmodulePlan, true) {
		t.Fatal("uninitialized submodule declaration must retain the worktree fallback")
	}
}

func TestGitSnapshotIneligibleWithoutWorktreeFallbackFailsClosed(t *testing.T) {
	nested := newTestGitRoot(t)
	if out, err := exec.Command("git", "init", "-q", filepath.Join(nested, "child")).CombinedOutput(); err != nil {
		t.Fatalf("git init nested: %v (%s)", err, out)
	}
	plan := &snapshotPlan{
		Units:   []snapshotUnit{{AbsPath: nested}},
		Primary: &snapshotUnit{AbsPath: nested},
	}
	if fallback, err := snapshotWorktreeFallbackWithGitGate(plan, true); err == nil || fallback != nil {
		t.Fatalf("unavailable worktree fallback must fail closed, fallback=%+v err=%v", fallback, err)
	}
}

func TestSnapshotExecutionObservation(t *testing.T) {
	gitRoot := newTestGitRoot(t)
	ordinary := &snapshotPlan{Units: []snapshotUnit{{Ref: localDirectoryRef{LocalPath: gitRoot}, AbsPath: gitRoot}}, Primary: &snapshotUnit{Ref: localDirectoryRef{LocalPath: gitRoot}, AbsPath: gitRoot}}

	fallback, err := snapshotWorktreeFallbackWithGitGate(ordinary, false)
	if err != nil || fallback == nil {
		t.Fatalf("disabled gate must use the worktree fallback, fallback=%+v err=%v", fallback, err)
	}
	if got := snapshotExecutionObservationFor(ordinary, fallback, false, true); got.ExecutionMode != localDirectoryModeWorktree || got.DowngradeCode != "git_snapshot_gate_disabled" || !got.Capability {
		t.Fatalf("disabled gate observation = %+v", got)
	}

	fallback, err = snapshotWorktreeFallbackWithGitGate(ordinary, true)
	if err != nil || fallback != nil {
		t.Fatalf("eligible ordinary repository must use snapshot, fallback=%+v err=%v", fallback, err)
	}
	if got := snapshotExecutionObservationFor(ordinary, fallback, true, true); got.ExecutionMode != localDirectoryModeSnapshot || got.DowngradeCode != "" || !got.GateEnabled {
		t.Fatalf("snapshot observation = %+v", got)
	}

	nested := newTestGitRoot(t)
	if out, err := exec.Command("git", "init", "-q", filepath.Join(nested, "child")).CombinedOutput(); err != nil {
		t.Fatalf("git init nested: %v (%s)", err, out)
	}
	nestedPlan := &snapshotPlan{Units: []snapshotUnit{{Ref: localDirectoryRef{LocalPath: nested}, AbsPath: nested}}, Primary: &snapshotUnit{Ref: localDirectoryRef{LocalPath: nested}, AbsPath: nested}}
	fallback, err = snapshotWorktreeFallbackWithGitGate(nestedPlan, true)
	if err != nil || fallback == nil {
		t.Fatalf("nested repository must fall back, fallback=%+v err=%v", fallback, err)
	}
	if got := snapshotExecutionObservationFor(nestedPlan, fallback, true, true); got.DowngradeCode != "git_snapshot_unsupported_topology" {
		t.Fatalf("nested repository observation = %+v", got)
	}
}

// TestGitSnapshotUnsupportedTopologiesFallbackWithoutPristineWrites uses real
// disposable Git repositories to pin the fail-closed boundary. The ordinary
// single-repository worker tests exercise snapshot delivery; each topology
// here must instead retain the established worktree backend without changing
// a tracked file, untracked file, binary payload, refs, objects, or Git admin
// metadata while the daemon evaluates the gate and safety audit.
func TestGitSnapshotUnsupportedTopologiesFallbackWithoutPristineWrites(t *testing.T) {
	t.Setenv("MULTICA_ENABLE_GIT_SNAPSHOT", "1")

	type topology struct {
		name         string
		resourceRoot string
		repositories []string
		wantSnapshot bool
	}

	linkedBase := newTestGitRoot(t)
	linked := filepath.Join(t.TempDir(), "linked")
	runTopologyGit(t, linkedBase, "worktree", "add", "-q", "--detach", linked)
	writeTopologyMarkers(t, linked)

	nested := newTestGitRoot(t)
	child := newTopologyGitRoot(t, filepath.Join(nested, "child"))
	writeTopologyMarkers(t, nested)
	writeTopologyMarkers(t, child)

	moduleSource := newTestGitRoot(t)
	submodule := newTestGitRoot(t)
	runTopologyGit(t, submodule, "-c", "protocol.file.allow=always", "submodule", "add", "-q", moduleSource, "deps/module")
	runTopologyGit(t, submodule, "add", ".gitmodules", "deps/module")
	runTopologyGit(t, submodule, "commit", "-qm", "add module")
	writeTopologyMarkers(t, submodule)
	writeTopologyMarkers(t, filepath.Join(submodule, "deps", "module"))

	parent := t.TempDir()
	parentA := newTopologyGitRoot(t, filepath.Join(parent, "repo-a"))
	parentB := newTopologyGitRoot(t, filepath.Join(parent, "repo-b"))
	writeTopologyMarkers(t, parentA)
	writeTopologyMarkers(t, parentB)

	for _, tc := range []topology{
		{name: "linked_worktree", resourceRoot: linked, repositories: []string{linked}, wantSnapshot: true},
		{name: "nested_repository", resourceRoot: nested, repositories: []string{nested, child}},
		{name: "submodule", resourceRoot: submodule, repositories: []string{submodule, filepath.Join(submodule, "deps", "module")}},
		{name: "parent_with_multiple_repositories", resourceRoot: parent, repositories: []string{parentA, parentB}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := topologyFingerprint(t, tc.repositories)
			task := Task{
				ID:               "task-" + tc.name,
				ProjectResources: []ProjectResourceData{worktreeResourceForTest(t, "resource-"+tc.name, tc.resourceRoot, "d1")},
			}
			plan, err := resolveSnapshotPlan(task, "d1")
			if err != nil {
				t.Fatalf("resolve snapshot plan: %v", err)
			}
			if plan == nil {
				// A parent that is not itself a Git root never becomes a snapshot
				// candidate. Audit the representative plan too, so discovery of
				// both child repositories remains covered by this matrix.
				plan = &snapshotPlan{
					Primary: &snapshotUnit{Ref: localDirectoryRef{LocalPath: tc.resourceRoot, ExecutionMode: localDirectoryModeWorktree}, AbsPath: tc.resourceRoot},
					Units:   []snapshotUnit{{AbsPath: tc.resourceRoot}},
				}
			}

			audit := auditSnapshotGitScope(plan)
			if !audit.HasGit || !audit.Safe {
				t.Fatalf("audit = %+v, want a complete Git dependency graph", audit)
			}
			if got := gitSnapshotDeliveryEligible(plan, true); got != tc.wantSnapshot {
				t.Fatalf("snapshot delivery eligibility = %v, want %v", got, tc.wantSnapshot)
			}
			fallback, err := snapshotWorktreeFallback(plan)
			if err != nil {
				t.Fatalf("select backend: %v", err)
			}
			if tc.wantSnapshot {
				if fallback != nil || len(plan.GitMetadataUnits) == 0 {
					t.Fatalf("isolatable linked worktree must use snapshot with metadata overlays, fallback=%+v units=%+v", fallback, plan.GitMetadataUnits)
				}
			} else if fallback == nil || !fallback.UsesWorktree() {
				t.Fatalf("topology without a single-repository delivery contract must preserve worktree backend, fallback=%+v", fallback)
			}

			if after := topologyFingerprint(t, tc.repositories); after != before {
				t.Fatalf("planning and fallback changed pristine Git state\nbefore: %s\nafter:  %s", before, after)
			}
		})
	}
}

func newTopologyGitRoot(t *testing.T, repo string) string {
	t.Helper()
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.name", "Test"}, {"config", "user.email", "test@example.invalid"}} {
		runTopologyGit(t, repo, args...)
	}
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runTopologyGit(t, repo, "add", "tracked.txt")
	runTopologyGit(t, repo, "commit", "-qm", "base")
	return repo
}

func writeTopologyMarkers(t *testing.T, repo string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("locally-modified\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "untracked.txt"), []byte("untracked\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "payload.bin"), []byte{0, 1, 2, 255}, 0o600); err != nil {
		t.Fatal(err)
	}
}

func runTopologyGit(t *testing.T, repo string, args ...string) string {
	t.Helper()
	argv := append([]string(nil), args...)
	if repo != "" {
		argv = append([]string{"-C", repo}, argv...)
	}
	out, err := exec.Command("git", argv...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v (%s)", argv, err, out)
	}
	return string(out)
}

func topologyFingerprint(t *testing.T, repositories []string) string {
	t.Helper()
	parts := make([]string, 0, len(repositories)*5)
	for _, repo := range repositories {
		status := runTopologyGit(t, repo, "status", "--porcelain=v1", "--untracked-files=all")
		refs := runTopologyGit(t, repo, "show-ref", "--head")
		gitDir := strings.TrimSpace(runTopologyGit(t, repo, "rev-parse", "--absolute-git-dir"))
		commonDir := strings.TrimSpace(runTopologyGit(t, repo, "rev-parse", "--git-common-dir"))
		if !filepath.IsAbs(commonDir) {
			commonDir = filepath.Join(repo, commonDir)
		}
		parts = append(parts,
			repo+" status="+status,
			repo+" refs="+refs,
			repo+" worktree="+treeDigest(t, repo, true),
			repo+" git-dir="+treeDigest(t, gitDir, false),
			repo+" common-dir="+treeDigest(t, commonDir, false),
		)
	}
	return strings.Join(parts, " | ")
}

func treeDigest(t *testing.T, root string, skipRootGitDir bool) string {
	t.Helper()
	hash := sha256.New()
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		if skipRootGitDir && path == filepath.Join(root, ".git") && entry.IsDir() {
			return filepath.SkipDir
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(hash, "%s\\x00%s\\x00", filepath.ToSlash(rel), entry.Type()); err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			_, err = fmt.Fprint(hash, target)
			return err
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		_, err = hash.Write(contents)
		return err
	})
	if err != nil {
		t.Fatalf("digest %s: %v", root, err)
	}
	return fmt.Sprintf("%x", hash.Sum(nil))
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

func TestGitSnapshotDeclaredSubdirectoryPlanLocksAndLedger(t *testing.T) {
	t.Setenv("MULTICA_ENABLE_GIT_SNAPSHOT", "1")
	const daemonID = "d-mine"
	repo := newTestGitRoot(t)
	for _, subdir := range []string{"apps/a", "apps/a/child", "apps/b"} {
		path := filepath.Join(repo, filepath.FromSlash(subdir))
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "tracked.txt"), []byte(subdir+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	runTopologyGit(t, repo, "add", "apps")
	runTopologyGit(t, repo, "commit", "-qm", "add declared directories")

	resource := worktreeResourceWithSubdirectoriesForTest(t, "r1", repo, daemonID, "apps/a", "apps/b")
	taskA := Task{
		ID:                  "task-a",
		IssueID:             "issue-a",
		ProjectResources:    []ProjectResourceData{resource},
		LocalDirectoryScope: scopeJSONForTest(t, "r1", "apps/a"),
	}
	planA, err := resolveSnapshotPlan(taskA, daemonID)
	if err != nil {
		t.Fatalf("resolve Git subdirectory plan: %v", err)
	}
	if planA == nil || planA.Primary == nil || planA.Primary.AbsPath != filepath.Join(repo, "apps", "a") {
		t.Fatalf("Git subdirectory primary = %+v, want apps/a", planA)
	}
	if fallback, err := snapshotWorktreeFallbackWithGitGateAndCapability(planA, true, true); err != nil || fallback != nil {
		t.Fatalf("ordinary Git subdirectory must use snapshot delivery, fallback=%+v err=%v", fallback, err)
	}
	if len(planA.GitMetadataUnits) != 1 || planA.GitMetadataUnits[0].AbsPath != filepath.Join(repo, ".git") || planA.GitMetadataUnits[0].InternalKey == "" {
		t.Fatalf("Git metadata unit = %+v, want internal repository .git unit", planA.GitMetadataUnits)
	}
	if len(planA.GitContainmentUnits) != 1 || planA.GitContainmentUnits[0].AbsPath != repo || planA.GitContainmentUnits[0].InternalKey == "" {
		t.Fatalf("Git containment unit = %+v, want internal repository root unit", planA.GitContainmentUnits)
	}
	if locks := planA.DeclaredLockPaths(); len(locks) != 1 || locks[0] != planA.Units[0].LockPath {
		t.Fatalf("containment must not widen declared scope locks, got %v", locks)
	}

	envRoot := t.TempDir()
	if _, err := planA.materializeTrees(envRoot, taskA.IssueID, taskA.ID); err != nil {
		t.Fatalf("materialize Git subdirectory trees: %v", err)
	}
	units := make([]execenv.SnapshotUnitSpec, 0, len(planA.materializedUnits()))
	for _, unit := range planA.materializedUnits() {
		alias, upper, work := execenv.PlanSnapshotTree(envRoot, snapshotUnitStem(taskA.IssueID, taskA.ID, unit))
		if _, err := os.Stat(upper); err != nil {
			t.Fatalf("materialized unit upper %q: %v", unit.AbsPath, err)
		}
		units = append(units, execenv.SnapshotUnitSpec{Target: unit.AbsPath, Subdir: unit.Subdir, Alias: alias, Upper: upper, Work: work})
	}
	workerSpec := execenv.SnapshotRunSpec{TaskID: taskA.ID, Units: units, Cwd: planA.Primary.AbsPath}
	if len(workerSpec.Units) != 3 || workerSpec.Units[0].Target != repo || workerSpec.Units[1].Target != filepath.Join(repo, "apps", "a") || workerSpec.Units[2].Target != filepath.Join(repo, ".git") || workerSpec.Cwd != filepath.Join(repo, "apps", "a") {
		t.Fatalf("worker spec must retain containment, business, metadata units and business cwd, got %+v", workerSpec)
	}
	if err := execenv.RecordSnapshotStart(envRoot, execenv.SnapshotLedgerEntry{TaskID: taskA.ID, IssueID: taskA.IssueID, Root: envRoot, Primary: planA.Primary.AbsPath, Units: units}); err != nil {
		t.Fatalf("record snapshot ledger: %v", err)
	}
	rawLedger, err := os.ReadFile(execenv.SnapshotLedgerPath(envRoot))
	if err != nil {
		t.Fatalf("read snapshot ledger: %v", err)
	}
	var ledger struct {
		Entries []execenv.SnapshotLedgerEntry `json:"entries"`
	}
	if err := json.Unmarshal(rawLedger, &ledger); err != nil {
		t.Fatalf("decode snapshot ledger: %v", err)
	}
	if len(ledger.Entries) != 1 || len(ledger.Entries[0].Units) != 3 || ledger.Entries[0].Units[0].Target != repo || ledger.Entries[0].Units[1].Target != filepath.Join(repo, "apps", "a") || ledger.Entries[0].Units[2].Target != filepath.Join(repo, ".git") {
		t.Fatalf("ledger must retain the full containment, business, and metadata unit set, got %+v", ledger)
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
	d.client.snapshotSupport.Store(true)
	taskB := Task{
		ID:                  "task-b",
		IssueID:             "issue-b",
		ProjectResources:    []ProjectResourceData{resource},
		LocalDirectoryScope: scopeJSONForTest(t, "r1", "apps/b"),
	}

	var releases [2]func()
	var aborts [2]bool
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		releases[0], aborts[0] = d.acquireLocalDirectoryLockIfNeeded(context.Background(), taskA, slog.Default())
	}()
	go func() {
		defer wg.Done()
		releases[1], aborts[1] = d.acquireLocalDirectoryLockIfNeeded(context.Background(), taskB, slog.Default())
	}()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("disjoint Git subdirectories must run in parallel")
	}
	if aborts[0] || aborts[1] || releases[0] == nil || releases[1] == nil {
		t.Fatalf("disjoint Git subdirectories must both hold scope locks, aborts=%v releases=%v", aborts, releases)
	}
	if calls := waitCalls.Load(); calls != 0 {
		t.Fatalf("disjoint Git subdirectories must not wait, saw %d calls", calls)
	}
	releases[0]()
	releases[1]()

	firstRelease, firstAbort := d.acquireLocalDirectoryLockIfNeeded(context.Background(), taskA, slog.Default())
	if firstAbort || firstRelease == nil {
		t.Fatalf("first overlapping Git subdirectory task must acquire lock, abort=%v", firstAbort)
	}
	child := Task{
		ID:                  "task-child",
		IssueID:             "issue-child",
		ProjectResources:    []ProjectResourceData{resource},
		LocalDirectoryScope: scopeJSONForTest(t, "r1", "apps/a/child"),
	}
	blocked := make(chan func(), 1)
	go func() {
		release, _ := d.acquireLocalDirectoryLockIfNeeded(context.Background(), child, slog.Default())
		blocked <- release
	}()
	deadline := time.Now().Add(2 * time.Second)
	for waitCalls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if waitCalls.Load() == 0 {
		t.Fatal("parent/child Git subdirectory task never reported waiting_local_directory")
	}
	firstRelease()
	select {
	case release := <-blocked:
		if release == nil {
			t.Fatal("parent/child Git subdirectory task aborted after wait")
		}
		release()
	case <-time.After(2 * time.Second):
		t.Fatal("parent/child Git subdirectory task did not acquire after release")
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
// counts tasks waiting on a directory, not individual scope paths. A task
// declaring two subdirectories, both held, waits for the atomic scope lease;
// the gauge must net +1 while parked and drain to 0 after it proceeds.
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

	// Parked on the atomic lease: one wait reported, and the task counted
	// exactly once even though both declared paths are currently unavailable.
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

	// Freeing only one path cannot partially grant the atomic lease. The task
	// remains parked, does not re-report a second wait, and the gauge stays at
	// 1 rather than drifting as each conflicting path changes.
	releaseA()
	select {
	case <-done:
		t.Fatal("partially available atomic scope lease acquired before every path was free")
	case <-time.After(50 * time.Millisecond):
	}
	if calls := waitCalls.Load(); calls != 1 {
		t.Fatalf("atomic scope wait must be reported once, saw %d calls", calls)
	}
	if got := d.resourceWaitTasks.Load(); got != 1 {
		t.Errorf("partially available scope lease must keep one waiting task, got %d", got)
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
	if !localDirectoryGCExemption(snapTask, daemonID, true) {
		t.Error("snapshot run on a non-git root must stamp the GC exemption")
	}

	gitDir := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", gitDir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v (%s)", err, out)
	}
	gitTask := Task{ID: "t2", ProjectResources: []ProjectResourceData{snapshotResourceForTest(t, "r2", gitDir, daemonID, true)}}
	if localDirectoryGCExemption(gitTask, daemonID, true) {
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
	if !localDirectoryGCExemption(Task{ID: "t3", ProjectResources: []ProjectResourceData{localResource("r3", localDirectoryModeInPlace)}}, daemonID, true) {
		t.Error("in_place assignment runs inside the user's tree and must stamp the exemption")
	}
	if localDirectoryGCExemption(Task{ID: "t4", ProjectResources: []ProjectResourceData{localResource("r4", localDirectoryModeWorktree)}}, daemonID, true) {
		t.Error("worktree assignment owns a disposable env root and must NOT stamp the exemption")
	}
	if localDirectoryGCExemption(Task{ID: "t5"}, daemonID, true) {
		t.Error("no local_directory involvement → no exemption")
	}
}
