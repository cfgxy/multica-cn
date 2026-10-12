package execenv

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/pkg/taskfailure"
)

// reAnchorDelivery is the self-heal behind the W2 refusal flip: it may only
// rewrite a delivery whose shape is exactly "the branch's own tip, with the
// turn's base missing from its ancestry". Every other refusal shape must come
// back as errNotHealable so the caller refuses it exactly as before the heal
// existed — the tests below walk the three guards in order.

func TestReAnchorDeclinesAWorktreeThatRecordsNoState(t *testing.T) {
	// Nothing would be recorded for this branch, so there is no delivery to
	// re-anchor: the first guard must decline before touching git at all.
	w := &LocalWorktree{Branch: "agent/j/none", GitRoot: t.TempDir()}
	if _, err := w.reAnchorDelivery("0123456789abcdef0123456789abcdef01234567", worktreeTestLogger()); !errors.Is(err, errNotHealable) {
		t.Errorf("reAnchorDelivery on a stateless worktree = %v, want errNotHealable", err)
	}
}

func TestReAnchorDeclinesWhenTheBranchIsNotTheDelivery(t *testing.T) {
	repo := newTestRepo(t)
	wt := prepareForTest(t, repo)
	t.Cleanup(func() { finalizeAndDiscardForTest(t, wt) })

	writeFile(t, wt.WorkDir+"/agent.txt", "work\n")
	gitRun(t, wt.Path, "add", "-A")
	gitRun(t, wt.Path, "commit", "-m", "turn one")
	// A tip that belongs to some other line of history — the shape a detached
	// or off-branch delivery produces. Re-anchoring it would graft someone
	// else's commit onto this task's branch.
	stray := gitRun(t, repo, "rev-parse", "main")

	if _, err := wt.reAnchorDelivery(stray, worktreeTestLogger()); !errors.Is(err, errNotHealable) {
		t.Errorf("reAnchorDelivery on an off-branch tip = %v, want errNotHealable", err)
	}
}

func TestReAnchorDeclinesWhenTheBaseIsStillAnAncestor(t *testing.T) {
	repo := newTestRepo(t)
	wt := prepareForTest(t, repo)
	t.Cleanup(func() { finalizeAndDiscardForTest(t, wt) })

	// A clean start sits on the user's HEAD: the branch tip still contains the
	// turn's base, so there is no ancestry to repair — rewriting it would only
	// churn the branch.
	tip := gitRun(t, repo, "rev-parse", wt.Branch)
	if _, err := wt.reAnchorDelivery(tip, worktreeTestLogger()); !errors.Is(err, errNotHealable) {
		t.Errorf("reAnchorDelivery on an intact ancestry = %v, want errNotHealable", err)
	}
}

// The heal itself (RUYI-579 W2): a clean-start turn whose agent re-lands its
// work onto an unrelated base mid-turn — the reset/rebase shape behind the
// production refusals. The delivered tree is the agent's own work touching
// nothing the user has uncommitted edits on, so re-committing it onto the
// turn's base is the identical delivery on a legal ancestry, and the turn
// delivers instead of burning a retry round trip.
func TestFinalizeSelfHealsAnAncestorBreakThatLeavesUserEditsAlone(t *testing.T) {
	repo := newTestRepo(t)
	// A user commit so the reset has something to land on that is not the
	// turn's base.
	writeFile(t, filepath.Join(repo, "tracked.txt"), "second version\n")
	gitRun(t, repo, "add", "-A")
	gitRun(t, repo, "commit", "-m", "user commit")
	head := gitRun(t, repo, "rev-parse", "HEAD")
	root := gitRun(t, repo, "rev-parse", "HEAD~1")

	wt := prepareTurn(t, repo, "MUL-6881", turnOneTask)
	if wt.BaseCommit != head {
		t.Fatalf("clean start put the branch on %s, want the user's HEAD %s", wt.BaseCommit, head)
	}
	writeFile(t, filepath.Join(wt.WorkDir, "agent.txt"), "agent work\n")
	gitRun(t, wt.Path, "add", "-A")
	gitRun(t, wt.Path, "commit", "-m", "turn one")
	// The agent re-lands its work onto the older base — the rebase shape:
	// same tree, new parent, the turn's base orphaned.
	gitRun(t, wt.Path, "reset", "--soft", root)
	gitRun(t, wt.Path, "commit", "-m", "turn one, re-landed")

	var buf bytes.Buffer
	outcome, err := wt.Finalize(captureLogger(&buf))
	if err != nil {
		t.Fatalf("Finalize did not self-heal the ancestor break: %v", err)
	}
	if outcome.Branch != wt.Branch {
		t.Errorf("outcome named branch %q, want the delivered %q", outcome.Branch, wt.Branch)
	}
	if !strings.Contains(buf.String(), "delivery_guard_selfheal") {
		t.Errorf("the self-heal left no delivery_guard_selfheal event:\n%s", buf.String())
	}

	// The re-anchored tip sits directly on the turn's base, and the delivered
	// file is in its tree.
	delivered := gitRun(t, repo, "rev-parse", "refs/heads/"+wt.Branch)
	if parent := gitRun(t, repo, "rev-parse", delivered+"^"); parent != head {
		t.Errorf("re-anchored tip parent = %s, want the turn's base %s", parent, head)
	}
	if got := gitRun(t, repo, "show", wt.Branch+":agent.txt"); got != "agent work" {
		t.Errorf("the delivered tree lost the agent's file, got %q", got)
	}

	// Same delivery, only the parent changed: the original message survives
	// with the re-anchor note, and the author is the original commit's.
	msg := gitRun(t, repo, "log", "-1", "--format=%B", delivered)
	if !strings.Contains(msg, "turn one, re-landed") {
		t.Errorf("the re-anchored message lost the original delivery message:\n%s", msg)
	}
	const note = "the original delivered tip was "
	i := strings.Index(msg, note)
	if i < 0 {
		t.Fatalf("the re-anchored message carries no re-anchor note:\n%s", msg)
	}
	// The note ends "... was <sha>." — the sentence's period is not part of
	// the SHA.
	orig := gitRun(t, repo, "rev-parse", strings.TrimSuffix(strings.TrimSpace(msg[i+len(note):]), "."))
	if origAuthor, newAuthor := gitRun(t, repo, "show", "-s", "--format=%an %ae", orig),
		gitRun(t, repo, "show", "-s", "--format=%an %ae", delivered); origAuthor != newAuthor {
		t.Errorf("re-anchored author = %q, want the original commit's %q", newAuthor, origAuthor)
	}
	// A delivered turn takes its worktree with it.
	if _, statErr := os.Stat(wt.Path); statErr == nil {
		t.Error("the self-healed delivery left the worktree behind")
	}
}

// The content gate: a delivery that touches a path the user's uncommitted
// edits live on may have reverted those edits, and re-anchoring it would
// launder the loss into a legal ancestry — the next turn's replay would trust
// the recorded snapshot and never offer them again (MUL-6881). That shape
// stays refused, worktree preserved, marker pinned for the retry.
func TestFinalizeRefusesToSelfHealADeliveryThatTouchesUserEditPaths(t *testing.T) {
	repo := newTestRepo(t)
	writeFile(t, filepath.Join(repo, "tracked.txt"), "user work in progress\n")
	head := gitRun(t, repo, "rev-parse", "HEAD")

	wt := prepareTurn(t, repo, "MUL-6881", turnOneTask)
	t.Cleanup(func() { finalizeAndDiscardForTest(t, wt) })
	writeFile(t, filepath.Join(wt.WorkDir, "agent.txt"), "agent work\n")
	// Reset onto the user's HEAD: the baseline — the user's edit with it —
	// leaves the ancestry, and the agent's delivery rewrites tracked.txt's
	// content back.
	gitRun(t, wt.Path, "reset", "--hard", head)
	gitRun(t, wt.Path, "add", "-A")
	gitRun(t, wt.Path, "commit", "-m", "re-landed on the user's HEAD")

	outcome, err := wt.Finalize(worktreeTestLogger())
	if err == nil {
		t.Fatal("Finalize self-healed a delivery that reverts the user's uncommitted edit")
	}
	var guardErr *DeliveryGuardError
	if !errors.As(err, &guardErr) || guardErr.Kind != taskfailure.GuardKindAncestorBreak {
		t.Errorf("refusal = %v, want a %s guard error", err, taskfailure.GuardKindAncestorBreak)
	}
	if outcome.PreservedPath != wt.Path {
		t.Errorf("PreservedPath = %q, want the worktree at %q", outcome.PreservedPath, wt.Path)
	}
	if _, err := gitTry(t, repo, "rev-parse", "--verify", "--quiet", guardMarkerRef(wt.Branch)); err != nil {
		t.Error("the refused heal left no marker for the retry")
	}
}

// The refusal kinds are the structured contract the daemon turns into the
// task-failure trailer (RUYI-579 W3): a branch-mismatch delivery must say so,
// and an ancestor break must say that, or the failure event cannot be
// classified downstream.
func TestVerifyDeliveryPointClassifiesTheRefusal(t *testing.T) {
	repo := newTestRepo(t)
	writeFile(t, filepath.Join(repo, "tracked.txt"), "user work in progress\n")
	head := gitRun(t, repo, "rev-parse", "HEAD")

	wt := prepareTurn(t, repo, "MUL-6881", turnOneTask)
	t.Cleanup(func() { finalizeAndDiscardForTest(t, wt) })
	if wt.BaseCommit == head {
		t.Fatal("prepare left the branch on the user's own HEAD, with no commit of its own")
	}

	// The branch's tip is not what the worktree would deliver: branch mismatch.
	writeFile(t, wt.WorkDir+"/agent.txt", "work\n")
	gitRun(t, wt.Path, "add", "-A")
	gitRun(t, wt.Path, "commit", "-m", "turn one")
	err := wt.verifyDeliveryPoint(head)
	var mismatched *DeliveryGuardError
	if !errors.As(err, &mismatched) || mismatched.Kind != taskfailure.GuardKindBranchMismatch {
		t.Errorf("off-branch delivery = %v, want a %s guard error", err, taskfailure.GuardKindBranchMismatch)
	}

	// The agent resets back onto the user's HEAD: the base leaves the
	// delivered tip's ancestry. The branch ref moves with the reset, so the
	// worktree HEAD and the branch agree — and what remains is the ancestor
	// break, the one shape the self-heal owns.
	gitRun(t, wt.Path, "reset", "--hard", head)
	err = wt.verifyDeliveryPoint(head)
	var broken *DeliveryGuardError
	if !errors.As(err, &broken) || broken.Kind != taskfailure.GuardKindAncestorBreak {
		t.Errorf("reset-past-baseline delivery = %v, want a %s guard error", err, taskfailure.GuardKindAncestorBreak)
	}
}
