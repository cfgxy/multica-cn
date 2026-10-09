package execenv

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testGuardLogger(t *testing.T) *slog.Logger {
	t.Helper()
	return slog.New(slog.NewTextHandler(os.Stderr, nil))
}

func timeNowUTC() time.Time { return time.Now().UTC() }

// guardTestGit runs git in dir and fails the test on error.
func guardTestGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		"GIT_AUTHOR_DATE=2006-01-02T15:04:05Z", "GIT_COMMITTER_DATE=2006-01-02T15:04:05Z",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// guardTestRepo creates a repo with one commit on main.
func guardTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	guardTestGit(t, dir, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	guardTestGit(t, dir, "add", ".")
	guardTestGit(t, dir, "commit", "-qm", "initial")
	return dir
}

func configureTestGuard(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), ".recycle-evidence")
	ConfigureRecycleGuard(RecycleGuardConfig{Enabled: true, EvidenceDir: dir, EvidenceTTL: DefaultRecycleEvidenceTTL})
	t.Cleanup(func() { ConfigureRecycleGuard(RecycleGuardConfig{}) })
	return dir
}

func predicateByName(scan RecycleRootScan, name string) *RecyclePredicate {
	for i := range scan.Predicates {
		if scan.Predicates[i].Name == name {
			return &scan.Predicates[i]
		}
	}
	return nil
}

func TestScanHeadRoot_LinkedUnpushedIsEvidence(t *testing.T) {
	source := guardTestRepo(t)
	wt := filepath.Join(t.TempDir(), "wt")
	guardTestGit(t, source, "worktree", "add", "-b", "task-branch", wt)
	if err := os.WriteFile(filepath.Join(wt, "f.txt"), []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	guardTestGit(t, wt, "commit", "-am", "unpushed work")

	scan := scanHeadRoot(context.Background(), wt, false)
	if scan.Verdict != RecycleEvidence {
		t.Fatalf("linked unpushed verdict = %q, want evidence; reasons: %v", scan.Verdict, scan.Reasons)
	}
	// No remote exists: the initial commit is unpushed too, so both count.
	if p := predicateByName(scan, "unpushed"); p == nil || p.Count != 2 {
		t.Fatalf("unpushed predicate = %+v, want count 2", p)
	}
	if p := predicateByName(scan, "unpushed_list"); p == nil || len(p.List) != 2 {
		t.Fatalf("unpushed_list predicate = %+v, want two entries", p)
	}
	if p := predicateByName(scan, "sole_ref"); p == nil || p.Count != 0 {
		t.Fatalf("sole_ref predicate = %+v, want count 0 (branch ref survives in shared .git)", p)
	}
	for _, p := range scan.Predicates {
		if p.Command == "" {
			t.Fatalf("predicate %s has no verbatim command", p.Name)
		}
	}
}

func TestScanHeadRoot_LinkedSoleRefBlocks(t *testing.T) {
	source := guardTestRepo(t)
	wt := filepath.Join(t.TempDir(), "wt")
	guardTestGit(t, source, "worktree", "add", "--detach", wt)
	if err := os.WriteFile(filepath.Join(wt, "f.txt"), []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	guardTestGit(t, wt, "commit", "-am", "detached sole work")

	scan := scanHeadRoot(context.Background(), wt, false)
	if scan.Verdict != RecycleBlocked {
		t.Fatalf("linked sole-ref verdict = %q, want blocked; reasons: %v", scan.Verdict, scan.Reasons)
	}
	if p := predicateByName(scan, "sole_ref"); p == nil || p.Count != 1 {
		t.Fatalf("sole_ref predicate = %+v, want count 1", p)
	}
}

func TestScanHeadRoot_StandaloneUnpushedBlocks(t *testing.T) {
	repo := guardTestRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "f.txt"), []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	guardTestGit(t, repo, "commit", "-am", "unpushed work")

	scan := scanHeadRoot(context.Background(), repo, true)
	if scan.Verdict != RecycleBlocked {
		t.Fatalf("standalone unpushed verdict = %q, want blocked; reasons: %v", scan.Verdict, scan.Reasons)
	}
}

func TestScanHeadRoot_StandaloneStashBlocksLinkedStashIsNote(t *testing.T) {
	// Standalone: the stash ref lives in the git dir being deleted.
	repo := guardTestRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "f.txt"), []byte("stashed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	guardTestGit(t, repo, "stash", "push", "-m", "wip")
	scan := scanHeadRoot(context.Background(), repo, true)
	if scan.Verdict != RecycleBlocked {
		t.Fatalf("standalone stash verdict = %q, want blocked; reasons: %v", scan.Verdict, scan.Reasons)
	}

	// Linked: the stash survives in the shared .git — recorded, not blocking.
	// The source is pushed first so nothing else reads as unpushed.
	source := guardTestRepo(t)
	linkedRemote := filepath.Join(t.TempDir(), "remote.git")
	guardTestGit(t, "", "init", "-q", "--bare", linkedRemote)
	guardTestGit(t, source, "remote", "add", "origin", linkedRemote)
	guardTestGit(t, source, "push", "-q", "origin", "main")
	wt := filepath.Join(t.TempDir(), "wt")
	guardTestGit(t, source, "worktree", "add", "-b", "stash-branch", wt)
	if err := os.WriteFile(filepath.Join(wt, "f.txt"), []byte("stashed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	guardTestGit(t, wt, "stash", "push", "-m", "wip")
	scan = scanHeadRoot(context.Background(), wt, false)
	if scan.Verdict != RecyclePass {
		t.Fatalf("linked stash verdict = %q, want pass; reasons: %v", scan.Verdict, scan.Reasons)
	}
	found := false
	for _, r := range scan.Reasons {
		if strings.Contains(r, "stash") {
			found = true
		}
	}
	if !found {
		t.Fatalf("linked stash should be recorded in reasons: %v", scan.Reasons)
	}
}

func TestScanHeadRoot_DirtyTrackedBlocksUntrackedIsEvidenceOnly(t *testing.T) {
	// All commits pushed; a tracked modification must block.
	source := guardTestRepo(t)
	remote := filepath.Join(t.TempDir(), "remote.git")
	guardTestGit(t, "", "init", "-q", "--bare", remote)
	guardTestGit(t, source, "remote", "add", "origin", remote)
	guardTestGit(t, source, "push", "-q", "origin", "main")
	wt := filepath.Join(t.TempDir(), "wt")
	guardTestGit(t, source, "worktree", "add", "-b", "dirty-branch", wt)
	if err := os.WriteFile(filepath.Join(wt, "f.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	scan := scanHeadRoot(context.Background(), wt, false)
	if scan.Verdict != RecycleBlocked {
		t.Fatalf("dirty tracked verdict = %q, want blocked; reasons: %v", scan.Verdict, scan.Reasons)
	}

	// Untracked entries are noise: evidence, never a block.
	wt2 := filepath.Join(t.TempDir(), "wt2")
	guardTestGit(t, source, "worktree", "add", "-b", "untracked-branch", wt2)
	if err := os.WriteFile(filepath.Join(wt2, "build.log"), []byte("noise\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	scan = scanHeadRoot(context.Background(), wt2, false)
	if scan.Verdict != RecyclePass {
		t.Fatalf("untracked-only verdict = %q, want pass; reasons: %v", scan.Verdict, scan.Reasons)
	}
	if p := predicateByName(scan, "untracked"); p == nil || p.Count != 1 {
		t.Fatalf("untracked predicate = %+v, want count 1", p)
	}
}

func TestScanHeadRoot_UnbornHeadIsNotAFailure(t *testing.T) {
	repo := t.TempDir()
	guardTestGit(t, repo, "init", "-q", "-b", "main")
	scan := scanHeadRoot(context.Background(), repo, true)
	if scan.Verdict != RecyclePass {
		t.Fatalf("unborn HEAD verdict = %q, want pass; reasons: %v", scan.Verdict, scan.Reasons)
	}
}

func TestScanBareRoot_PristinePassesUnpushedBlocks(t *testing.T) {
	source := guardTestRepo(t)
	remote := filepath.Join(t.TempDir(), "remote.git")
	guardTestGit(t, "", "clone", "-q", "--bare", source, remote)

	// Pristine cache: after fetching remote refs, nothing is unpushed —
	// eviction must stay L0 (acceptance: normal recycle regression).
	scan := scanBareRoot(context.Background(), remote)
	if scan.Verdict != RecyclePass {
		t.Fatalf("pristine bare cache verdict = %q, want pass; reasons: %v; predicates: %+v",
			scan.Verdict, scan.Reasons, scan.Predicates)
	}

	// A local branch holding a commit the remote lacks: blocked, listed.
	orphan := strings.TrimSpace(guardTestGit(t, remote, "commit-tree", "HEAD^{tree}", "-m", "orphan work"))
	guardTestGit(t, remote, "update-ref", "refs/heads/agent/orphan", orphan)
	scan = scanBareRoot(context.Background(), remote)
	if scan.Verdict != RecycleBlocked {
		t.Fatalf("unpushed bare cache verdict = %q, want blocked; reasons: %v", scan.Verdict, scan.Reasons)
	}
	if p := predicateByName(scan, "bare_unpushed_branches"); p == nil || p.Count != 1 {
		t.Fatalf("bare_unpushed_branches = %+v, want count 1", p)
	}
	if p := predicateByName(scan, "bare_unpushed_branches_list"); p == nil || len(p.List) != 1 {
		t.Fatalf("bare_unpushed_branches_list = %+v, want one entry", p)
	}
	if p := predicateByName(scan, "stash"); p == nil || !p.NA {
		t.Fatalf("bare stash predicate = %+v, want NA (bare repos cannot hold stashes)", p)
	}
}

func TestScanBareRoot_RemoteUnreachableFailsClosed(t *testing.T) {
	source := guardTestRepo(t)
	remote := filepath.Join(t.TempDir(), "remote.git")
	guardTestGit(t, "", "clone", "-q", "--bare", source, remote)
	// Break origin: the cache can no longer prove eviction loses nothing.
	guardTestGit(t, remote, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "gone.git"))

	scan := scanBareRoot(context.Background(), remote)
	if scan.Verdict != RecycleBlocked {
		t.Fatalf("unreachable-remote verdict = %q, want blocked (fail closed)", scan.Verdict)
	}
}

// guardTestBareCache builds a bare cache with the daemon's modern
// remote-tracking layout: origin configured with the refs/remotes/origin/*
// fetch refspec and backfilled (gitCloneBareContext's contract). A plain
// `git clone --bare` writes no fetch refspec at all, and without it pushes
// from linked worktrees never record refs/remotes/origin/<branch> — the
// comparison base ScanAgentBranchForRecycle reads would never see a push.
func guardTestBareCache(t *testing.T) string {
	t.Helper()
	source := guardTestRepo(t)
	bare := filepath.Join(t.TempDir(), "cache.git")
	guardTestGit(t, "", "clone", "-q", "--bare", source, bare)
	guardTestGit(t, bare, "config", "remote.origin.fetch", "+refs/heads/*:refs/remotes/origin/*")
	guardTestGit(t, bare, "fetch", "-q", "--prune", "--no-tags", "origin", "+refs/heads/*:refs/remotes/origin/*")
	return bare
}

// TestScanAgentBranch_PushedPassesUnpushedBlocks pins the branch guard's
// routing (RUYI-617): a stale agent branch whose commits are all covered by
// remote-tracking refs scans pass — today's cleanup proceeds — while a
// branch holding a commit no remote-tracking ref reaches routes blocked
// with the commit listed.
func TestScanAgentBranch_PushedPassesUnpushedBlocks(t *testing.T) {
	bare := guardTestBareCache(t)
	ctx := context.Background()

	// Pushed shape: the branch ref plus its covering refs/remotes/origin/*
	// entry, exactly what a worktree push records under the cache's refspec.
	pushedTip := strings.TrimSpace(guardTestGit(t, bare, "rev-parse", "HEAD"))
	guardTestGit(t, bare, "branch", "agent/stale/pushed", pushedTip)
	guardTestGit(t, bare, "update-ref", "refs/remotes/origin/agent/stale/pushed", pushedTip)

	pass := ScanAgentBranchForRecycle(ctx, bare, "agent/stale/pushed")
	if pass.Verdict != RecyclePass || pass.Topology != RecycleTopologyBare {
		t.Fatalf("pushed branch verdict/topology = %s/%s, want pass/bare: %+v", pass.Verdict, pass.Topology, pass)
	}
	if p := predicateByName(pass, "unpushed"); p == nil || p.Count != 0 {
		t.Fatalf("unpushed predicate = %+v, want count 0", p)
	}

	// Unpushed shape: a commit no remote-tracking ref can reach.
	orphan := strings.TrimSpace(guardTestGit(t, bare, "commit-tree", "HEAD^{tree}", "-m", "unpushed task work"))
	guardTestGit(t, bare, "update-ref", "refs/heads/agent/stale/unpushed", orphan)

	blocked := ScanAgentBranchForRecycle(ctx, bare, "agent/stale/unpushed")
	if blocked.Verdict != RecycleBlocked {
		t.Fatalf("unpushed branch verdict = %s, want blocked: %+v", blocked.Verdict, blocked)
	}
	list := predicateByName(blocked, "unpushed_list")
	if list == nil || len(list.List) != 1 || list.List[0] != orphan {
		t.Fatalf("unpushed_list = %+v, want exactly %s", list, orphan)
	}
	if !strings.Contains(list.Command, "agent/stale/unpushed") {
		t.Fatalf("list command %q must name the branch", list.Command)
	}
}

// TestScanAgentBranch_ScanFailureFailsClosed: a branch ref pointing at a
// missing object makes rev-list fail; the scan must read as blocked — never
// as empty — so the deletion skips this tick.
func TestScanAgentBranch_ScanFailureFailsClosed(t *testing.T) {
	bare := guardTestBareCache(t)
	// Raw ref-file write bypasses object validation, so rev-list fails.
	if err := os.MkdirAll(filepath.Join(bare, "refs", "heads", "agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bare, "refs", "heads", "agent", "broken"),
		[]byte("0000000000000000000000000000000000000001\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	scan := ScanAgentBranchForRecycle(context.Background(), bare, "agent/broken")
	if scan.Verdict != RecycleBlocked {
		t.Fatalf("scan failure verdict = %s, want blocked: %+v", scan.Verdict, scan)
	}
	p := predicateByName(scan, "unpushed")
	if p == nil || p.ExitCode == 0 || p.Error == "" {
		t.Fatalf("failure must be recorded on the predicate: %+v", p)
	}
}

func TestRecycleEvidenceWrittenAndSurvivesRecycle(t *testing.T) {
	evidenceDir := configureTestGuard(t)
	source := guardTestRepo(t)
	wt := filepath.Join(t.TempDir(), "wt")
	guardTestGit(t, source, "worktree", "add", "-b", "evidence-branch", wt)
	if err := os.WriteFile(filepath.Join(wt, "f.txt"), []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	guardTestGit(t, wt, "commit", "-am", "unpushed work")

	// The worktree removal is the recycle; the evidence must outlive it.
	GuardWorktreeEvidence(source, wt, testGuardLogger(t))
	entries, err := os.ReadDir(evidenceDir)
	if err != nil || len(entries) == 0 {
		t.Fatalf("expected evidence files in %s (err=%v)", evidenceDir, err)
	}
	data, err := os.ReadFile(filepath.Join(evidenceDir, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{`"kind": "` + RecycleKindWorktree + `"`, `"topology": "linked"`, "unpushed"} {
		if !strings.Contains(text, want) {
			t.Fatalf("evidence missing %q:\n%s", want, text)
		}
	}

	// The same removal again with the guard disabled writes nothing.
	ConfigureRecycleGuard(RecycleGuardConfig{})
	other := filepath.Join(t.TempDir(), "wt2")
	guardTestGit(t, source, "worktree", "add", "-b", "evidence-branch-2", other)
	GuardWorktreeEvidence(source, other, testGuardLogger(t))
	entries, _ = os.ReadDir(evidenceDir)
	if len(entries) != 1 {
		t.Fatalf("disabled guard must not write evidence, found %d files", len(entries))
	}
}

func TestSweepRecycleEvidenceOnlyRemovesExpired(t *testing.T) {
	evidenceDir := configureTestGuard(t)
	scan := &RecycleScan{Target: t.TempDir(), Kind: RecycleKindTaskDir, Verdict: RecyclePass,
		ScannedAt: timeNowUTC()}
	if _, err := WriteRecycleEvidence(scan); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(evidenceDir)
	if len(entries) != 1 {
		t.Fatalf("expected one evidence file, got %d", len(entries))
	}
	old := timeNowUTC().Add(-31 * 24 * time.Hour)
	if err := os.Chtimes(filepath.Join(evidenceDir, entries[0].Name()), old, old); err != nil {
		t.Fatal(err)
	}
	if n := SweepRecycleEvidence(DefaultRecycleEvidenceTTL, timeNowUTC()); n != 1 {
		t.Fatalf("sweeper removed %d, want 1", n)
	}
	if n := SweepRecycleEvidence(DefaultRecycleEvidenceTTL, timeNowUTC()); n != 0 {
		t.Fatalf("second sweep removed %d, want 0", n)
	}
}

func TestEvidenceListCap(t *testing.T) {
	evidenceDir := configureTestGuard(t)
	source := guardTestRepo(t)
	wt := filepath.Join(t.TempDir(), "wt")
	guardTestGit(t, source, "worktree", "add", "-b", "many-commits", wt)
	for i := 0; i < 55; i++ {
		if err := os.WriteFile(filepath.Join(wt, "f.txt"), []byte(strings.Repeat("x", i+1)+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		guardTestGit(t, wt, "commit", "-aqm", "c")
	}
	scan := scanHeadRoot(context.Background(), wt, false)
	list := predicateByName(scan, "unpushed_list")
	if list == nil || len(list.List) != recycleEvidenceListCap || !list.Truncated {
		t.Fatalf("list cap: %+v, want %d entries truncated", list, recycleEvidenceListCap)
	}
	_ = evidenceDir
}
