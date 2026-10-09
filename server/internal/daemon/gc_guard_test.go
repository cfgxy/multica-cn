package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

// newGuardTestDaemon is newGCTestDaemon with the recycle guard enabled and
// pointed at the daemon's own workspaces root, mirroring production wiring.
func newGuardTestDaemon(t *testing.T, handler http.Handler) *Daemon {
	t.Helper()
	d := newGCTestDaemon(t, handler)
	d.cfg.GCGuardEnabled = true
	d.cfg.GCGuardEvidenceTTL = execenv.DefaultRecycleEvidenceTTL
	// Mirror daemon.New's guard installation for a config built by hand.
	execenv.ConfigureRecycleGuard(execenv.RecycleGuardConfig{
		Enabled:     true,
		EvidenceDir: filepath.Join(d.cfg.WorkspacesRoot, ".recycle-evidence"),
		EvidenceTTL: d.cfg.GCGuardEvidenceTTL,
	})
	t.Cleanup(func() { execenv.ConfigureRecycleGuard(execenv.RecycleGuardConfig{}) })
	return d
}

// guardEvidenceFiles lists evidence files for one target basename.
func guardEvidenceFiles(t *testing.T, evidenceDir, nameFragment string) []string {
	t.Helper()
	entries, err := os.ReadDir(evidenceDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		if strings.Contains(e.Name(), nameFragment) {
			names = append(names, filepath.Join(evidenceDir, e.Name()))
		}
	}
	return names
}

// readGuardScan decodes one evidence file.
func readGuardScan(t *testing.T, path string) *execenv.RecycleScan {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var scan execenv.RecycleScan
	if err := json.Unmarshal(data, &scan); err != nil {
		t.Fatalf("decode evidence %s: %v", path, err)
	}
	return &scan
}

// TestCleanTaskDir_GuardBlocksSoleRefCommits is the L2 stub: a task dir whose
// repo holds a commit no branch or remote references must survive the recycle
// with the evidence listing exactly what would have been lost.
func TestCleanTaskDir_GuardBlocksSoleRefCommits(t *testing.T) {
	d := newGuardTestDaemon(t, http.NewServeMux())
	taskDir := createTaskDir(t, d.cfg.WorkspacesRoot, "ws-guard", "guard-task-taskid1", nil)
	runGitForGC(t, taskDir, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(taskDir, "f.txt"), []byte("work\n"), 0o644)
	runGitForGC(t, taskDir, "add", ".")
	runGitForGC(t, taskDir, "commit", "-qm", "base")
	// Detach and commit: the new commit is referenced by nothing but HEAD.
	runGitForGC(t, taskDir, "checkout", "-q", "--detach")
	os.WriteFile(filepath.Join(taskDir, "f.txt"), []byte("sole\n"), 0o644)
	runGitForGC(t, taskDir, "commit", "-am", "sole-ref work")

	_, removed, guard := d.cleanTaskDir(taskDir)

	if removed {
		t.Fatal("sole-ref task dir must not be removed (L2 blocked)")
	}
	if guard != execenv.RecycleBlocked {
		t.Fatalf("guard verdict = %q, want blocked", guard)
	}
	if _, err := os.Stat(taskDir); err != nil {
		t.Fatalf("blocked task dir must stay on disk: %v", err)
	}
	files := guardEvidenceFiles(t, filepath.Join(d.cfg.WorkspacesRoot, ".recycle-evidence"), "task-taskid1")
	if len(files) == 0 {
		t.Fatal("blocked recycle must leave an evidence file")
	}
	scan := readGuardScan(t, files[0])
	if scan.Verdict != execenv.RecycleBlocked || scan.Kind != execenv.RecycleKindTaskDir {
		t.Fatalf("evidence verdict/kind = %s/%s, want blocked/task-dir", scan.Verdict, scan.Kind)
	}
	var listed bool
	for _, r := range scan.Roots {
		for _, p := range r.Predicates {
			if p.Name == "sole_ref_list" && len(p.List) == 1 {
				listed = true
			}
		}
	}
	if !listed {
		t.Fatalf("evidence must list the sole-ref commit: %+v", scan)
	}
}

// TestCleanTaskDir_GuardEvidenceLinkedWorktreeProceeds is the L1 stub: the
// worktree's unpushed commits live on a branch in the surviving shared .git,
// so the recycle proceeds with evidence, and the commit is provably alive
// after the directory is gone.
func TestCleanTaskDir_GuardEvidenceLinkedWorktreeProceeds(t *testing.T) {
	d := newGuardTestDaemon(t, http.NewServeMux())
	source := createGCGitRepo(t)
	// Ownership validation requires <root>/<ws>/<task>, so the linked
	// worktree plays the role of a real task dir.
	wt := filepath.Join(d.cfg.WorkspacesRoot, "ws-guard-l1", "linked-taskid2")
	runGitForGC(t, source, "worktree", "add", "-b", "task-branch", wt)
	os.WriteFile(filepath.Join(wt, "delivered.txt"), []byte("work\n"), 0o644)
	runGitForGC(t, wt, "add", ".")
	runGitForGC(t, wt, "commit", "-qm", "unpushed delivery")
	tip := strings.TrimSpace(runGitForGC(t, wt, "rev-parse", "HEAD"))

	// Give the linked worktree the ownership markers cleanTaskDir requires.
	taskDir := wt
	wsID := "ws-guard-l1"
	owner, err := json.Marshal(execenv.EnvRootOwner{WorkspaceID: wsID, TaskID: "taskid2"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(taskDir, ".task_owner"), owner, 0o644); err != nil {
		t.Fatal(err)
	}

	_, removed, guard := d.cleanTaskDir(taskDir)

	if !removed {
		t.Fatal("linked-worktree recycle with surviving refs must proceed (L1)")
	}
	if guard != execenv.RecycleEvidence {
		t.Fatalf("guard verdict = %q, want evidence", guard)
	}
	files := guardEvidenceFiles(t, filepath.Join(d.cfg.WorkspacesRoot, ".recycle-evidence"), "linked-taskid2")
	if len(files) == 0 {
		t.Fatal("L1 recycle must leave an evidence file")
	}
	scan := readGuardScan(t, files[0])
	if scan.Verdict != execenv.RecycleEvidence {
		t.Fatalf("evidence verdict = %s, want evidence", scan.Verdict)
	}
	found := false
	for _, r := range scan.Roots {
		if r.Topology != execenv.RecycleTopologyLinked {
			t.Fatalf("topology = %s, want linked", r.Topology)
		}
		for _, p := range r.Predicates {
			for _, entry := range p.List {
				if entry == tip {
					found = true
				}
			}
		}
	}
	if !found {
		t.Fatalf("evidence must contain the unpushed commit %s: %+v", tip, scan)
	}
	// The mechanical L1 claim: the commit survives outside the deleted dir.
	if got := strings.TrimSpace(runGitForGC(t, source, "rev-parse", "task-branch")); got != tip {
		t.Fatalf("commit must survive in the shared git dir after removal: got %s, want %s", got, tip)
	}
}

// TestCleanTaskDir_GuardDisabledKeepsLegacyRemoval is kill-switch evidence:
// with the guard off, a sole-ref dir is removed exactly as before the guard
// existed, and no evidence directory is created.
func TestCleanTaskDir_GuardDisabledKeepsLegacyRemoval(t *testing.T) {
	d := newGuardTestDaemon(t, http.NewServeMux())
	d.cfg.GCGuardEnabled = false
	taskDir := createTaskDir(t, d.cfg.WorkspacesRoot, "ws-guard-off", "guard-off-taskid3", nil)
	runGitForGC(t, taskDir, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(taskDir, "f.txt"), []byte("work\n"), 0o644)
	runGitForGC(t, taskDir, "add", ".")
	runGitForGC(t, taskDir, "commit", "-qm", "base")
	runGitForGC(t, taskDir, "checkout", "-q", "--detach")
	os.WriteFile(filepath.Join(taskDir, "f.txt"), []byte("sole\n"), 0o644)
	runGitForGC(t, taskDir, "commit", "-am", "sole-ref work")

	_, removed, guard := d.cleanTaskDir(taskDir)

	if !removed || guard != "" {
		t.Fatalf("guard off: removed=%v guard=%q, want true/empty", removed, guard)
	}
	if _, err := os.Stat(filepath.Join(d.cfg.WorkspacesRoot, ".recycle-evidence")); !os.IsNotExist(err) {
		t.Fatalf("guard off must not create evidence: %v", err)
	}
}

// TestCleanTaskDir_GuardUntrackedOnlyPasses pins the dirty routing: untracked
// entries (build output, logs) never block a recycle; they are evidence only.
func TestCleanTaskDir_GuardUntrackedOnlyPasses(t *testing.T) {
	d := newGuardTestDaemon(t, http.NewServeMux())
	taskDir := createTaskDir(t, d.cfg.WorkspacesRoot, "ws-guard-untracked", "guard-untracked-taskid4", nil)
	runGitForGC(t, taskDir, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(taskDir, "f.txt"), []byte("work\n"), 0o644)
	runGitForGC(t, taskDir, "add", ".")
	runGitForGC(t, taskDir, "commit", "-qm", "base")
	remote := filepath.Join(t.TempDir(), "remote.git")
	runGitForGC(t, "", "init", "-q", "--bare", remote)
	runGitForGC(t, taskDir, "remote", "add", "origin", remote)
	runGitForGC(t, taskDir, "push", "-q", "origin", "main")
	os.WriteFile(filepath.Join(taskDir, "build.log"), []byte("noise\n"), 0o644)

	_, removed, guard := d.cleanTaskDir(taskDir)

	if !removed || guard != execenv.RecyclePass {
		t.Fatalf("untracked-only dir: removed=%v guard=%q, want true/pass", removed, guard)
	}
	files := guardEvidenceFiles(t, filepath.Join(d.cfg.WorkspacesRoot, ".recycle-evidence"), "untracked-taskid4")
	if len(files) == 0 {
		t.Fatal("pass recycle still records evidence (what was scanned)")
	}
}

// TestEvictRepoCache_GuardBlocksUnpushedBranches is the L2 stub for bare
// caches: a branch holding a commit the remote lacks blocks eviction and the
// evidence lists the commit.
func TestEvictRepoCache_GuardBlocksUnpushedBranches(t *testing.T) {
	d := newGuardTestDaemon(t, http.NewServeMux())
	d.cfg.GCRepoTTL = 24 * time.Hour

	wsID := "22222222-2222-2222-2222-222222222222"
	barePath := newEvictTestRepo(t, d, wsID, testRepoURL)
	writeLastUsed(t, barePath, time.Now().Add(-48*time.Hour))
	// NB: branches under agent/* are pruned by the existing stale-agent-branch
	// cleanup before eviction runs, so this stub uses a non-agent branch name —
	// that is the surface the eviction guard protects.
	orphan := strings.TrimSpace(runGitForGC(t, barePath, "commit-tree", "HEAD^{tree}", "-m", "orphan work"))
	runGitForGC(t, barePath, "update-ref", "refs/heads/orphan-work", orphan)

	stats := runRepoGC(d)

	if _, err := os.Stat(barePath); err != nil {
		t.Fatalf("unpushed bare cache must survive eviction: %v", err)
	}
	if stats.guardBlocked != 1 {
		t.Fatalf("guard_blocked = %d, want 1", stats.guardBlocked)
	}
	files := guardEvidenceFiles(t, filepath.Join(d.cfg.WorkspacesRoot, ".recycle-evidence"), "bare-cache")
	if len(files) == 0 {
		t.Fatal("blocked eviction must leave evidence")
	}
	scan := readGuardScan(t, files[0])
	listed := false
	for _, r := range scan.Roots {
		for _, p := range r.Predicates {
			if p.Name == "bare_unpushed_branches_list" {
				for _, entry := range p.List {
					if entry == orphan {
						listed = true
					}
				}
			}
		}
	}
	if !listed {
		t.Fatalf("evidence must list the orphan commit %s: %+v", orphan, scan)
	}
}

// TestEvictRepoCache_GuardFetchFailureFailsClosed: a cache whose remote is
// gone cannot prove eviction loses nothing, so eviction is refused.
func TestEvictRepoCache_GuardFetchFailureFailsClosed(t *testing.T) {
	d := newGuardTestDaemon(t, http.NewServeMux())
	d.cfg.GCRepoTTL = 24 * time.Hour

	wsID := "33333333-3333-3333-3333-333333333333"
	barePath := newEvictTestRepo(t, d, wsID, testRepoURL)
	writeLastUsed(t, barePath, time.Now().Add(-48*time.Hour))
	runGitForGC(t, barePath, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "gone.git"))

	stats := runRepoGC(d)

	if _, err := os.Stat(barePath); err != nil {
		t.Fatalf("cache with unreachable remote must survive eviction: %v", err)
	}
	if stats.guardBlocked != 1 {
		t.Fatalf("guard_blocked = %d, want 1 (fail closed)", stats.guardBlocked)
	}
}

// TestEvictRepoCache_L0PristineEvictionUnchangedWithGuardOn pins acceptance
// 3 for the bare path: with the guard enabled, a pristine idle cache is still
// evicted exactly as before.
func TestEvictRepoCache_L0PristineEvictionUnchangedWithGuardOn(t *testing.T) {
	d := newGuardTestDaemon(t, http.NewServeMux())
	d.cfg.GCRepoTTL = 24 * time.Hour

	wsID := "44444444-4444-4444-4444-444444444444"
	barePath := newEvictTestRepo(t, d, wsID, testRepoURL)
	writeLastUsed(t, barePath, time.Now().Add(-48*time.Hour))

	stats := runRepoGC(d)

	if _, err := os.Stat(barePath); !os.IsNotExist(err) {
		t.Fatalf("pristine cache must still be evicted with the guard on: %v", err)
	}
	if stats.repoCachesReclaimed != 1 || stats.guardBlocked != 0 {
		t.Fatalf("reclaimed=%d blocked=%d, want 1/0", stats.repoCachesReclaimed, stats.guardBlocked)
	}
}

// TestRunGC_RecycleEvidenceSurvivesOrphanGCAndExpiresByTTL pins the evidence
// lifetime contract (review note): the evidence directory is a root-level dot
// directory no GC walk claims, so a file survives longer than the orphan TTL;
// only the guard's own sweeper removes it, once past the evidence TTL.
func TestRunGC_RecycleEvidenceSurvivesOrphanGCAndExpiresByTTL(t *testing.T) {
	d := newGuardTestDaemon(t, http.NewServeMux())
	d.cfg.GCOrphanTTL = 1 * time.Hour
	evidenceDir := filepath.Join(d.cfg.WorkspacesRoot, ".recycle-evidence")

	// An orphan-age task dir so the cycle has real reclaim work.
	orphanDir := createTaskDir(t, d.cfg.WorkspacesRoot, "ws-guard-ttl", "orphan-taskid5", nil)
	past := time.Now().Add(-2 * d.cfg.GCOrphanTTL)
	if err := os.Chtimes(orphanDir, past, past); err != nil {
		t.Fatal(err)
	}

	// Evidence older than the orphan TTL but younger than the evidence TTL.
	scan := &execenv.RecycleScan{Target: orphanDir, Kind: execenv.RecycleKindTaskDir,
		Verdict: execenv.RecycleBlocked, ScannedAt: time.Now().UTC()}
	path, err := execenv.WriteRecycleEvidence(scan)
	if err != nil || path == "" {
		t.Fatalf("write evidence: %q %v", path, err)
	}
	old := time.Now().Add(-100 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}

	d.runGC(context.Background())

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("evidence younger than the evidence TTL must survive the orphan GC: %v", err)
	}
	if _, err := os.Stat(evidenceDir); err != nil {
		t.Fatalf("evidence dir must not be reclaimed by any GC path: %v", err)
	}

	// Past the evidence TTL the guard's own sweeper is the only deleter.
	older := time.Now().Add(-31 * 24 * time.Hour)
	if err := os.Chtimes(path, older, older); err != nil {
		t.Fatal(err)
	}
	d.runGC(context.Background())
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("evidence past its TTL must be swept by the guard: %v", err)
	}
}

// TestRecycleGuardDryScan_ClassifiesWithoutDeleting exercises the gc
// --dry-run engine: every verdict, dry-run flags, and zero deletions.
func TestRecycleGuardDryScan_ClassifiesWithoutDeleting(t *testing.T) {
	d := newGuardTestDaemon(t, http.NewServeMux())
	root := d.cfg.WorkspacesRoot

	// L0: pushed, clean task dir.
	cleanDir := createTaskDir(t, root, "ws-dry", "dry-clean-taskid6", nil)
	runGitForGC(t, cleanDir, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(cleanDir, "f.txt"), []byte("w\n"), 0o644)
	runGitForGC(t, cleanDir, "add", ".")
	runGitForGC(t, cleanDir, "commit", "-qm", "base")
	remote := filepath.Join(t.TempDir(), "remote.git")
	runGitForGC(t, "", "init", "-q", "--bare", remote)
	runGitForGC(t, cleanDir, "remote", "add", "origin", remote)
	runGitForGC(t, cleanDir, "push", "-q", "origin", "main")

	// L2: sole-ref task dir.
	blockDir := createTaskDir(t, root, "ws-dry", "dry-block-taskid7", nil)
	runGitForGC(t, blockDir, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(blockDir, "f.txt"), []byte("w\n"), 0o644)
	runGitForGC(t, blockDir, "add", ".")
	runGitForGC(t, blockDir, "commit", "-qm", "base")
	runGitForGC(t, blockDir, "checkout", "-q", "--detach")
	os.WriteFile(filepath.Join(blockDir, "f.txt"), []byte("s\n"), 0o644)
	runGitForGC(t, blockDir, "commit", "-am", "sole")

	// Bare cache with an unpushed branch.
	d2 := newGuardTestDaemon(t, http.NewServeMux())
	_ = d2
	barePath := newEvictTestRepo(t, d, "55555555-5555-5555-5555-555555555555", testRepoURL)
	orphan := strings.TrimSpace(runGitForGC(t, barePath, "commit-tree", "HEAD^{tree}", "-m", "orphan"))
	runGitForGC(t, barePath, "update-ref", "refs/heads/agent/orphan", orphan)

	scans, err := RecycleGuardDryScan(root)
	if err != nil {
		t.Fatal(err)
	}
	byTarget := map[string]*execenv.RecycleScan{}
	for _, s := range scans {
		byTarget[s.Target] = s
		if !s.DryRun {
			t.Fatalf("dry scan must set dry_run for %s", s.Target)
		}
	}
	if s := byTarget[cleanDir]; s == nil || s.Verdict != execenv.RecyclePass {
		t.Fatalf("clean dir scan = %+v, want pass", s)
	}
	if s := byTarget[blockDir]; s == nil || s.Verdict != execenv.RecycleBlocked {
		t.Fatalf("sole-ref dir scan = %+v, want blocked", s)
	}
	if s := byTarget[barePath]; s == nil || s.Verdict != execenv.RecycleBlocked || s.Kind != execenv.RecycleKindBareRepo {
		t.Fatalf("bare cache scan = %+v, want blocked bare-cache", s)
	}
	// Nothing was deleted.
	for _, target := range []string{cleanDir, blockDir, barePath} {
		if _, err := os.Stat(target); err != nil {
			t.Fatalf("dry scan must not delete %s: %v", target, err)
		}
	}
	blocked, evidence, passed := RecycleGuardDryScanSummary(scans)
	if blocked != 2 || passed < 1 {
		t.Fatalf("summary blocked=%d evidence=%d passed=%d, want >=2 blocked", blocked, evidence, passed)
	}
}
