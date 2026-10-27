package execenv

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// newTestRepoWithOrigin creates a repo whose default branch is also pushed to
// a local bare origin, with the remote-tracking tip and the origin/HEAD
// symbolic ref in place — the shape anchorNewBranchBase reads. Returns the
// working repo and the bare origin.
func newTestRepoWithOrigin(t *testing.T) (repo, origin string) {
	t.Helper()
	repo = newTestRepo(t)
	origin = t.TempDir() + "/origin.git"
	// git -C needs the target to exist; git init --bare does not create it
	// through that flag.
	if err := os.MkdirAll(origin, 0o755); err != nil {
		t.Fatalf("create bare origin dir: %v", err)
	}
	gitRun(t, origin, "init", "--bare", "--quiet")
	gitRun(t, repo, "remote", "add", "origin", origin)
	gitRun(t, repo, "push", "--quiet", "origin", "main")
	// A clone writes refs/remotes/origin/HEAD; remote add + push does not.
	// Set it the way `git remote set-head origin --auto` would resolve it.
	gitRun(t, repo, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	return repo, origin
}

func originMainTip(t *testing.T, repo string) string {
	t.Helper()
	return gitRun(t, repo, "rev-parse", "--verify", "refs/remotes/origin/main")
}

// branchBaseParams is the minimal identity a task-scoped branch resolves
// under. validTask needs workspace, agent and task; there is no conversation,
// so the branch is task-scoped and freshly created.
func branchBaseParams(taskID string) LocalWorktreeParams {
	return LocalWorktreeParams{
		AgentName:   "J",
		TaskID:      taskID,
		WorkspaceID: testBranchOwner.WorkspaceID,
		AgentID:     testBranchOwner.AgentID,
	}
}

// A branch this prepare is about to CREATE starts from the origin default
// branch's tip, not from local HEAD: the daemon's HEAD may carry un-pushed
// commits only some agent branch has, and a conversation forked there
// diverges from the mainline from birth (RUYI-579 W1).
func TestNewBranchAnchorsAtOriginTip(t *testing.T) {
	repo, _ := newTestRepoWithOrigin(t)
	// Local work the origin has never seen — exactly the shape behind the
	// 10-08 refusals: the daemon HEAD sits on an agent-branch-only commit.
	gitRun(t, repo, "commit", "--allow-empty", "-m", "local un-pushed work")
	head := gitRun(t, repo, "rev-parse", "HEAD")

	plan := resolveTaskBranch(repo, branchBaseParams("w1-anchor-task"), head, worktreeTestLogger())

	want := originMainTip(t, repo)
	if plan.base != want {
		t.Errorf("new branch base = %s, want the origin tip %s (local HEAD %s)", plan.base, want, head)
	}
	if plan.base == head {
		t.Error("new branch anchored at local HEAD, which carries un-pushed work")
	}
}

// No usable origin anchor — no remote at all, the historical shape — keeps
// the pre-W1 behaviour: the branch starts from local HEAD. Fail-closed by
// construction, byte for byte.
func TestNewBranchWithoutOriginKeepsLocalHead(t *testing.T) {
	repo := newTestRepo(t)
	head := gitRun(t, repo, "rev-parse", "HEAD")

	plan := resolveTaskBranch(repo, branchBaseParams("w1-noorigin-task"), head, worktreeTestLogger())

	if plan.base != head {
		t.Errorf("new branch base = %s, want the local HEAD %s unchanged without an origin anchor", plan.base, head)
	}
}

// The divergence event is the observation point for "this conversation was
// forked somewhere the origin does not contain": local HEAD carries un-pushed
// work and that work is deliberately NOT carried into the new branch.
func TestNewBranchBaseDivergenceIsRecorded(t *testing.T) {
	repo, _ := newTestRepoWithOrigin(t)
	gitRun(t, repo, "commit", "--allow-empty", "-m", "local un-pushed work")
	head := gitRun(t, repo, "rev-parse", "HEAD")
	tip := originMainTip(t, repo)

	var buf bytes.Buffer
	plan := resolveTaskBranch(repo, branchBaseParams("w1-divergence-task"), head, captureLogger(&buf))

	if plan.base != tip {
		t.Fatalf("base = %s, want origin tip %s", plan.base, tip)
	}
	logged := buf.String()
	if !strings.Contains(logged, "branch_base_divergence") {
		t.Errorf("divergent fork logged no branch_base_divergence event:\n%s", logged)
	}
	if !strings.Contains(logged, head) || !strings.Contains(logged, tip) {
		t.Errorf("divergence event does not name both SHAs (head %s, origin tip %s):\n%s", head, tip, logged)
	}
}

// A HEAD the origin tip already contains is not divergence: the event stays
// quiet for the ordinary case.
func TestNewBranchBaseInSyncLogsNoDivergence(t *testing.T) {
	repo, _ := newTestRepoWithOrigin(t)
	head := gitRun(t, repo, "rev-parse", "HEAD")

	var buf bytes.Buffer
	resolveTaskBranch(repo, branchBaseParams("w1-sync-task"), head, captureLogger(&buf))

	if strings.Contains(buf.String(), "branch_base_divergence") {
		t.Errorf("in-sync fork logged a divergence event:\n%s", buf.String())
	}
}

// A branch that already exists keeps the base it resolved — the conversation's
// own tip — even when the origin has moved on. Only a NEW branch fetches and
// re-anchors; continuing never drags the branch onto newer mainline history
// mid-conversation.
func TestContinuedBranchIgnoresOriginTip(t *testing.T) {
	repo, _ := newTestRepoWithOrigin(t)

	first := prepareTurn(t, repo, "MUL-6881", turnOneTask)
	writeFile(t, first.WorkDir+"/agent.txt", "turn one\n")
	finalizeOK(t, first)
	branchTip := gitRun(t, repo, "rev-parse", first.Branch)

	// The origin moves; the conversation must not follow it mid-flight.
	writeFile(t, repo+"/origin-push.txt", "newer mainline\n")
	gitRun(t, repo, "add", "-A")
	gitRun(t, repo, "commit", "-m", "mainline moves on")
	gitRun(t, repo, "push", "--quiet", "origin", "main")

	second := prepareTurn(t, repo, "MUL-6881", turnTwoTask)
	t.Cleanup(func() { finalizeAndDiscardForTest(t, second) })

	if !second.Continued {
		t.Fatal("second turn did not continue the conversation's branch")
	}
	if second.BaseCommit != branchTip {
		t.Errorf("continued branch base = %s, want the conversation's own tip %s, not the new origin tip %s",
			second.BaseCommit, branchTip, originMainTip(t, repo))
	}
}

// divergedLocalHead pushes mainline work to the origin, then advances local
// HEAD past it with an un-pushed commit — the 10-08 shape behind RUYI-579 W1:
// the daemon's HEAD carries work only an agent branch has, and the origin tip
// has content local HEAD has never seen.
func divergedLocalHead(t *testing.T, repo string) (head, tip string) {
	t.Helper()
	writeFile(t, repo+"/mainline.txt", "newer mainline\n")
	gitRun(t, repo, "add", "mainline.txt")
	gitRun(t, repo, "commit", "-m", "mainline advances")
	gitRun(t, repo, "push", "--quiet", "origin", "main")
	tip = originMainTip(t, repo)

	writeFile(t, repo+"/carried.txt", "local-only agent work\n")
	gitRun(t, repo, "add", "carried.txt")
	gitRun(t, repo, "commit", "-m", "local un-pushed work")
	head = gitRun(t, repo, "rev-parse", "HEAD")
	return head, tip
}

// The replay's fresh-fork contract predates W1: it assumed the new branch is
// checked out at the snapshot's own parent, so replaying the snapshot's whole
// delta was safe. With the branch anchored at the origin tip instead, that
// delta is the entire tree distance between the two checkouts — replaying it
// would dress mainline movement up as pending reversals and carry local
// un-pushed work into a task branch that must not have it (RUYI-380). Only
// the user's edit set may ride in.
func TestFreshForkAfterOriginAnchorReplaysOnlyUserEdits(t *testing.T) {
	repo, _ := newTestRepoWithOrigin(t)
	_, tip := divergedLocalHead(t, repo)

	// The user's uncommitted edits on top of the diverged HEAD.
	writeFile(t, repo+"/tracked.txt", "user edited\n")
	writeFile(t, repo+"/user-notes.txt", "untracked scratch\n")

	wt := prepareTurn(t, repo, "MUL-W1REPLAY", "22223333-4444-5555-6666-aaaaaaaaaaaa")

	// With user edits present, prepare commits a baseline onto the anchored
	// branch; the baseline's parent is the origin tip the branch forks from.
	if anchoredBase := gitRun(t, repo, "rev-parse", wt.BaseCommit+"^"); anchoredBase != tip {
		t.Errorf("baseline parent = %s, want the anchored origin tip %s", anchoredBase, tip)
	}
	status := gitRun(t, wt.Path, "status", "--porcelain")
	for _, banned := range []string{"mainline.txt", "carried.txt"} {
		if strings.Contains(status, banned) {
			t.Errorf("worktree pending changes touch %s; the anchor delta must not replay:\n%s", banned, status)
		}
	}
	if _, err := os.Stat(wt.Path + "/carried.txt"); !os.IsNotExist(err) {
		t.Errorf("local un-pushed work leaked into the task worktree (stat err %v)", err)
	}
	if got := gitRun(t, wt.Path, "show", ":tracked.txt"); got != "user edited" {
		t.Errorf("worktree tracked.txt = %q, want the user's edit replayed", got)
	}
	if _, err := os.Stat(wt.Path + "/user-notes.txt"); err != nil {
		t.Errorf("user's untracked file missing from the worktree: %v", err)
	}

	writeFile(t, wt.WorkDir+"/agent-output.txt", "agent work\n")
	outcome := finalizeOK(t, wt)
	if outcome.Branch == "" {
		t.Fatal("finalize delivered no branch for a task with agent work")
	}
	if got := gitRun(t, repo, "show", outcome.Branch+":mainline.txt"); got != "newer mainline" {
		t.Errorf("delivered mainline.txt = %q, want the origin version untouched by the replay", got)
	}
	if got := gitRun(t, repo, "show", outcome.Branch+":tracked.txt"); got != "user edited" {
		t.Errorf("delivered tracked.txt = %q, want the user's edit", got)
	}
	if out := gitRun(t, repo, "ls-tree", "--name-only", outcome.Branch, "--", "user-notes.txt"); !strings.Contains(out, "user-notes.txt") {
		t.Errorf("user's untracked file missing from the delivered branch:\n%s", out)
	}
}

// A clean user directory in the same diverged shape must leave the worktree
// untouched: the origin's newer content is the branch's own base, not a
// pending reversal waiting for the agent to commit.
func TestFreshForkCleanDirectoryAfterDivergentAnchorStaysClean(t *testing.T) {
	repo, _ := newTestRepoWithOrigin(t)
	divergedLocalHead(t, repo)

	wt := prepareTurn(t, repo, "MUL-W1CLEAN", "22223333-4444-5555-6666-bbbbbbbbbbbb")
	defer finalizeAndDiscardForTest(t, wt)

	if status := gitRun(t, wt.Path, "status", "--porcelain"); status != "" {
		t.Errorf("clean user directory replayed as pending changes:\n%s", status)
	}
	if got := gitRun(t, wt.Path, "show", ":mainline.txt"); got != "newer mainline" {
		t.Errorf("worktree mainline.txt = %q, want the origin version the branch anchors at", got)
	}
}
