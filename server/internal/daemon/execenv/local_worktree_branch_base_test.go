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
