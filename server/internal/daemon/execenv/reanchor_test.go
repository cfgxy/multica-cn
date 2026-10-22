package execenv

import (
	"os"
	"strings"
	"testing"
)

// TestReanchorBranchReanchorsStaleChain pins the RUYI-780 infrastructure
// repair: a managed conversation branch bound to a stale local chain moves onto
// the verified content commit, the old chain is archived, the branch record is
// re-pinned (same user-state, same owner, new checkpoint), and the audit line
// lands.
func TestReanchorBranchReanchorsStaleChain(t *testing.T) {
	repo := newTestRepo(t)

	// mainline: initial -> m2 -> m3 (the "origin main" tip the operator
	// verified). A stale local chain hangs off m2 with two extra commits -
	// the shape of a branch that kept replaying old local work.
	gitRun(t, repo, "commit", "--allow-empty", "-m", "m2")
	m2 := gitRun(t, repo, "rev-parse", "HEAD")
	gitRun(t, repo, "commit", "--allow-empty", "-m", "m3")
	mainTip := gitRun(t, repo, "rev-parse", "HEAD")

	gitRun(t, repo, "update-ref", "refs/heads/agent/j/mul-x", m2)
	oldTip := gitRun(t, repo, "rev-parse", "refs/heads/agent/j/mul-x")
	gitRun(t, repo, "checkout", "-B", "agent/j/mul-x", oldTip)
	gitRun(t, repo, "commit", "--allow-empty", "-m", "stale local work a")
	gitRun(t, repo, "commit", "--allow-empty", "-m", "stale local work b")
	oldChainTip := gitRun(t, repo, "rev-parse", "HEAD")
	gitRun(t, repo, "checkout", "main")

	// The verified content commit: exactly one commit ahead of the mainline
	// tip, the only source the next task may stand on.
	gitRun(t, repo, "commit", "--allow-empty", "-m", "verified content")
	content := gitRun(t, repo, "rev-parse", "HEAD")

	// Record the branch the way a previous prepare would: user-state snapshot
	// commit + checkpoint at the stale chain tip, owned by the test owner.
	snapshot := gitRun(t, repo, "commit-tree", "HEAD^{tree}")
	record, err := writeBranchRecord(repo, "agent/j/mul-x", snapshot, oldChainTip, testBranchOwner)
	if err != nil {
		t.Fatalf("writeBranchRecord: %v", err)
	}

	note := ReanchorNote{OperatorAgent: "a", OperatorTask: "t", Issue: "RUYI-780", Reason: "stale chain repair"}
	res, err := ReanchorBranch(repo, "agent/j/mul-x", content, mainTip, note)
	if err != nil {
		t.Fatalf("ReanchorBranch: %v", err)
	}

	// Branch now stands on the content commit.
	if got := gitRun(t, repo, "rev-parse", "refs/heads/agent/j/mul-x"); got != content {
		t.Errorf("branch tip = %s, want %s", got, content)
	}
	// Old chain preserved under the archive ref, never deleted.
	if got := gitRun(t, repo, "rev-parse", "refs/multica/reanchor/agent/j/mul-x"); got != oldChainTip {
		t.Errorf("archived old tip = %s, want %s", got, oldChainTip)
	}
	if got := gitRun(t, repo, "rev-parse", "refs/multica/reanchor/local-state/agent/j/mul-x"); got != record {
		t.Errorf("archived old record = %s, want %s", got, record)
	}
	// Record re-pinned: same user-state parent, new checkpoint, owner intact.
	if !res.RecordReanchored || res.NewRecord == "" {
		t.Fatalf("record not re-anchored: %+v", res)
	}
	if got := gitRun(t, repo, "rev-parse", "refs/multica/local-state/agent/j/mul-x"); got != res.NewRecord {
		t.Errorf("local-state ref = %s, want %s", got, res.NewRecord)
	}
	if got := gitRun(t, repo, "rev-parse", "--quiet", res.NewRecord+"^1"); got != snapshot {
		t.Errorf("new record user-state parent = %s, want the old snapshot %s", got, snapshot)
	}
	if got := gitRun(t, repo, "rev-parse", "--quiet", res.NewRecord+"^2"); got != content {
		t.Errorf("new record checkpoint = %s, want %s", got, content)
	}
	rec, err := readBranchRecord(repo, res.NewRecord)
	if err != nil {
		t.Fatalf("readBranchRecord: %v", err)
	}
	if rec.owner != testBranchOwner {
		t.Errorf("record owner = %+v, want %+v", rec.owner, testBranchOwner)
	}
	// The re-pinned record must prove ownership the way the next prepare will
	// check it: checkpoint is an ancestor of the tip and the record is ours.
	if _, owned := branchOwnedBy(repo, "agent/j/mul-x", testBranchOwner, worktreeTestLogger()); !owned {
		t.Error("re-anchored record no longer proves ownership for the next prepare")
	}
	// Audit line landed.
	auditPath := res.AuditPath
	if auditPath == "" {
		t.Fatal("no audit path reported")
	}
	audit, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	for _, want := range []string{"RUYI-780", `"new_tip":"` + content, `"old_tip":"` + oldChainTip} {
		if !strings.Contains(string(audit), want) {
			t.Errorf("audit line missing %q in %s", want, audit)
		}
	}
}

// TestReanchorBranchRejectsOffMainlineTarget: a target that sits on the stale
// chain, not on the mainline tip, must be refused - re-anchoring there would
// just bind the next task to another stale base.
func TestReanchorBranchRejectsOffMainlineTarget(t *testing.T) {
	repo := newTestRepo(t)
	gitRun(t, repo, "commit", "--allow-empty", "-m", "m2")
	m2 := gitRun(t, repo, "rev-parse", "HEAD")
	gitRun(t, repo, "update-ref", "refs/heads/agent/j/mul-y", m2)
	gitRun(t, repo, "checkout", "-B", "agent/j/mul-y")
	gitRun(t, repo, "commit", "--allow-empty", "-m", "stale")
	stale := gitRun(t, repo, "rev-parse", "HEAD")
	gitRun(t, repo, "checkout", "main")
	gitRun(t, repo, "commit", "--allow-empty", "-m", "m3")
	mainTip := gitRun(t, repo, "rev-parse", "HEAD")

	_, err := ReanchorBranch(repo, "agent/j/mul-y", stale, mainTip, ReanchorNote{})
	if err == nil || !strings.Contains(err.Error(), "sits on") {
		t.Fatalf("off-mainline target accepted or wrong error: %v", err)
	}
	// Nothing moved: branch still on the stale chain tip position m2+stale.
	if got := gitRun(t, repo, "rev-parse", "refs/heads/agent/j/mul-y"); got != stale {
		t.Errorf("branch moved on refusal: %s", got)
	}
}

// TestReanchorBranchRefusesPendingGuardRefusal: a delivery-guard refusal
// marker holds the branch for its own heal path; the operator rewrite must
// stand down and move nothing.
func TestReanchorBranchRefusesPendingGuardRefusal(t *testing.T) {
	repo := newTestRepo(t)
	gitRun(t, repo, "commit", "--allow-empty", "-m", "m2")
	m2 := gitRun(t, repo, "rev-parse", "HEAD")
	gitRun(t, repo, "commit", "--allow-empty", "-m", "m3")
	mainTip := gitRun(t, repo, "rev-parse", "HEAD")
	gitRun(t, repo, "commit", "--allow-empty", "-m", "content")
	content := gitRun(t, repo, "rev-parse", "HEAD")
	gitRun(t, repo, "update-ref", "refs/heads/agent/j/mul-z", m2)
	old := gitRun(t, repo, "rev-parse", "refs/heads/agent/j/mul-z")
	// A pending refusal marker at the old tip.
	gitRun(t, repo, "update-ref", guardRefusalRef("agent/j/mul-z"), old)

	_, err := ReanchorBranch(repo, "agent/j/mul-z", content, mainTip, ReanchorNote{})
	if err == nil || !strings.Contains(err.Error(), "delivery-guard refusal") {
		t.Fatalf("reanchor proceeded despite a pending refusal marker: %v", err)
	}
	if got := gitRun(t, repo, "rev-parse", "refs/heads/agent/j/mul-z"); got != old {
		t.Errorf("branch moved despite refusal marker: %s", got)
	}
}

// TestReanchorBranchNoRecordStillMovesTip: a branch without a local-state
// record is still re-anchored; the record step reports itself as a no-op and
// the next prepare forks a fresh branch instead of continuing.
func TestReanchorBranchNoRecordStillMovesTip(t *testing.T) {
	repo := newTestRepo(t)
	gitRun(t, repo, "commit", "--allow-empty", "-m", "m2")
	m2 := gitRun(t, repo, "rev-parse", "HEAD")
	gitRun(t, repo, "commit", "--allow-empty", "-m", "m3")
	mainTip := gitRun(t, repo, "rev-parse", "HEAD")
	gitRun(t, repo, "commit", "--allow-empty", "-m", "content")
	content := gitRun(t, repo, "rev-parse", "HEAD")
	gitRun(t, repo, "update-ref", "refs/heads/agent/j/mul-w", m2)

	res, err := ReanchorBranch(repo, "agent/j/mul-w", content, mainTip, ReanchorNote{})
	if err != nil {
		t.Fatalf("ReanchorBranch: %v", err)
	}
	if res.RecordReanchored {
		t.Error("record reported re-anchored for a branch that has no record")
	}
	if got := gitRun(t, repo, "rev-parse", "refs/heads/agent/j/mul-w"); got != content {
		t.Errorf("branch tip = %s, want %s", got, content)
	}
}

// TestReanchorBranchAlreadyAtTarget is a clean no-op refusal: nothing is
// archived, nothing moves, the operator sees a clear error.
func TestReanchorBranchAlreadyAtTarget(t *testing.T) {
	repo := newTestRepo(t)
	gitRun(t, repo, "commit", "--allow-empty", "-m", "m3")
	mainTip := gitRun(t, repo, "rev-parse", "HEAD")
	gitRun(t, repo, "commit", "--allow-empty", "-m", "content")
	content := gitRun(t, repo, "rev-parse", "HEAD")
	gitRun(t, repo, "update-ref", "refs/heads/agent/j/mul-v", content)

	_, err := ReanchorBranch(repo, "agent/j/mul-v", content, mainTip, ReanchorNote{})
	if err == nil || !strings.Contains(err.Error(), "already stands") {
		t.Fatalf("expected an already-at-target refusal, got %v", err)
	}
}
