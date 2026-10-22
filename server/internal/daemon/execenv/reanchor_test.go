package execenv

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
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
	// Old chain and record are preserved under this operation's unique archive
	// refs, never overwritten by a later re-anchor.
	if len(res.ArchiveRefs) != 2 {
		t.Fatalf("archive refs = %v, want tip and record refs", res.ArchiveRefs)
	}
	if got := gitRun(t, repo, "rev-parse", res.ArchiveRefs[0]); got != oldChainTip {
		t.Errorf("archived old tip = %s, want %s", got, oldChainTip)
	}
	if got := gitRun(t, repo, "rev-parse", res.ArchiveRefs[1]); got != record {
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

func TestReanchorBranchRejectsInvalidRecordBeforeMovingBranch(t *testing.T) {
	repo := newTestRepo(t)
	gitRun(t, repo, "commit", "--allow-empty", "-m", "m2")
	m2 := gitRun(t, repo, "rev-parse", "HEAD")
	gitRun(t, repo, "commit", "--allow-empty", "-m", "m3")
	mainTip := gitRun(t, repo, "rev-parse", "HEAD")

	gitRun(t, repo, "update-ref", "refs/heads/agent/j/mul-invalid-record", m2)
	gitRun(t, repo, "checkout", "-B", "agent/j/mul-invalid-record")
	gitRun(t, repo, "commit", "--allow-empty", "-m", "stale work")
	oldTip := gitRun(t, repo, "rev-parse", "HEAD")
	gitRun(t, repo, "checkout", "main")
	gitRun(t, repo, "commit", "--allow-empty", "-m", "verified content")
	content := gitRun(t, repo, "rev-parse", "HEAD")

	// A normal commit is not a branch-record commit. The old implementation
	// moved the branch before finding this out.
	gitRun(t, repo, "update-ref", userStateRef("agent/j/mul-invalid-record"), mainTip)
	_, err := ReanchorBranch(repo, "agent/j/mul-invalid-record", content, mainTip, ReanchorNote{})
	if err == nil || (!strings.Contains(err.Error(), "read branch record") && !strings.Contains(err.Error(), "carries no checkpoint")) {
		t.Fatalf("invalid record accepted or wrong error: %v", err)
	}
	if got := gitRun(t, repo, "rev-parse", "refs/heads/agent/j/mul-invalid-record"); got != oldTip {
		t.Errorf("branch moved after record validation failure: got %s, want %s", got, oldTip)
	}
	if got := gitRun(t, repo, "rev-parse", userStateRef("agent/j/mul-invalid-record")); got != mainTip {
		t.Errorf("record changed after validation failure: got %s, want %s", got, mainTip)
	}
	if got := gitRun(t, repo, "for-each-ref", "--format=%(refname)", "refs/multica/reanchor/agent/j/mul-invalid-record"); got != "" {
		t.Errorf("archive refs written before validation succeeded: %s", got)
	}
}

func TestReanchorBranchAtomicRefTransactionLeavesNoPartialArchiveOnCASMiss(t *testing.T) {
	repo := newTestRepo(t)
	oldTip := gitRun(t, repo, "rev-parse", "HEAD")
	gitRun(t, repo, "commit", "--allow-empty", "-m", "concurrent branch move")
	concurrentTip := gitRun(t, repo, "rev-parse", "HEAD")
	branch := "agent/j/mul-cas"
	gitRun(t, repo, "update-ref", "refs/heads/"+branch, concurrentTip)

	archiveTip, archiveState, err := newReanchorArchiveRefs(branch)
	if err != nil {
		t.Fatalf("newReanchorArchiveRefs: %v", err)
	}
	// The branch was already moved from oldTip when the transaction begins.
	// Atomic update-ref must leave both archive refs absent, rather than
	// preserving a misleading half-operation before its CAS fails.
	err = updateReanchorRefs(repo, branch, concurrentTip, oldTip, "", "", archiveTip, archiveState)
	if err == nil {
		t.Fatal("stale branch CAS unexpectedly succeeded")
	}
	if got := gitRun(t, repo, "rev-parse", "refs/heads/"+branch); got != concurrentTip {
		t.Errorf("branch changed after CAS miss: got %s, want %s", got, concurrentTip)
	}
	if got := gitRun(t, repo, "for-each-ref", "--format=%(refname)", "refs/multica/reanchor/"+branch); got != "" {
		t.Errorf("archive refs survived failed transaction: %s", got)
	}
}

func TestReanchorBranchRejectsLinkedWorktreeWithoutChangingItsIndex(t *testing.T) {
	repo := newTestRepo(t)
	gitRun(t, repo, "commit", "--allow-empty", "-m", "m2")
	m2 := gitRun(t, repo, "rev-parse", "HEAD")
	gitRun(t, repo, "commit", "--allow-empty", "-m", "m3")
	mainTip := gitRun(t, repo, "rev-parse", "HEAD")
	gitRun(t, repo, "update-ref", "refs/heads/agent/j/mul-linked", m2)
	gitRun(t, repo, "commit", "--allow-empty", "-m", "verified content")
	content := gitRun(t, repo, "rev-parse", "HEAD")

	linked := filepath.Join(t.TempDir(), "linked")
	gitRun(t, repo, "worktree", "add", linked, "agent/j/mul-linked")
	oldTip := gitRun(t, linked, "rev-parse", "HEAD")
	oldIndex := gitRun(t, linked, "ls-files", "--stage")

	_, err := ReanchorBranch(repo, "agent/j/mul-linked", content, mainTip, ReanchorNote{})
	if err == nil || !strings.Contains(err.Error(), "checked out") {
		t.Fatalf("linked worktree branch accepted or wrong error: %v", err)
	}
	if got := gitRun(t, linked, "rev-parse", "HEAD"); got != oldTip {
		t.Errorf("linked worktree HEAD changed: got %s, want %s", got, oldTip)
	}
	if got := gitRun(t, linked, "ls-files", "--stage"); got != oldIndex {
		t.Errorf("linked worktree index changed:\n got %s\nwant %s", got, oldIndex)
	}
	if got := gitRun(t, repo, "rev-parse", "refs/heads/agent/j/mul-linked"); got != oldTip {
		t.Errorf("branch moved despite linked worktree: got %s, want %s", got, oldTip)
	}
}

func TestReanchorBranchKeepsEveryArchiveReachableAcrossCalls(t *testing.T) {
	repo := newTestRepo(t)
	gitRun(t, repo, "commit", "--allow-empty", "-m", "m2")
	m2 := gitRun(t, repo, "rev-parse", "HEAD")
	gitRun(t, repo, "commit", "--allow-empty", "-m", "m3")
	mainTip := gitRun(t, repo, "rev-parse", "HEAD")

	gitRun(t, repo, "update-ref", "refs/heads/agent/j/mul-history", m2)
	gitRun(t, repo, "checkout", "-B", "agent/j/mul-history")
	gitRun(t, repo, "commit", "--allow-empty", "-m", "stale work")
	oldTip := gitRun(t, repo, "rev-parse", "HEAD")
	gitRun(t, repo, "checkout", "main")
	gitRun(t, repo, "commit", "--allow-empty", "-m", "content one")
	contentOne := gitRun(t, repo, "rev-parse", "HEAD")

	snapshot := gitRun(t, repo, "commit-tree", "HEAD^{tree}")
	oldRecord, err := writeBranchRecord(repo, "agent/j/mul-history", snapshot, oldTip, testBranchOwner)
	if err != nil {
		t.Fatalf("writeBranchRecord: %v", err)
	}
	first, err := ReanchorBranch(repo, "agent/j/mul-history", contentOne, mainTip, ReanchorNote{})
	if err != nil {
		t.Fatalf("first ReanchorBranch: %v", err)
	}

	gitRun(t, repo, "commit", "--allow-empty", "-m", "content two")
	contentTwo := gitRun(t, repo, "rev-parse", "HEAD")
	second, err := ReanchorBranch(repo, "agent/j/mul-history", contentTwo, contentOne, ReanchorNote{})
	if err != nil {
		t.Fatalf("second ReanchorBranch: %v", err)
	}
	if len(first.ArchiveRefs) != 2 || len(second.ArchiveRefs) != 2 {
		t.Fatalf("archive ref counts = first %v, second %v", first.ArchiveRefs, second.ArchiveRefs)
	}
	for _, ref := range append(append([]string{}, first.ArchiveRefs...), second.ArchiveRefs...) {
		if got := gitRun(t, repo, "rev-parse", ref); got == "" {
			t.Errorf("archive ref %s is not resolvable", ref)
		}
	}
	if first.ArchiveRefs[0] == second.ArchiveRefs[0] || first.ArchiveRefs[1] == second.ArchiveRefs[1] {
		t.Fatalf("second re-anchor overwrote prior archive refs: first=%v second=%v", first.ArchiveRefs, second.ArchiveRefs)
	}
	if got := gitRun(t, repo, "rev-parse", first.ArchiveRefs[0]); got != oldTip {
		t.Errorf("first archived tip = %s, want %s", got, oldTip)
	}
	if got := gitRun(t, repo, "rev-parse", first.ArchiveRefs[1]); got != oldRecord {
		t.Errorf("first archived record = %s, want %s", got, oldRecord)
	}
	if got := gitRun(t, repo, "rev-parse", second.ArchiveRefs[0]); got != contentOne {
		t.Errorf("second archived tip = %s, want %s", got, contentOne)
	}
	if got := gitRun(t, repo, "rev-parse", second.ArchiveRefs[1]); got != first.NewRecord {
		t.Errorf("second archived record = %s, want %s", got, first.NewRecord)
	}
	gitRun(t, repo, "cat-file", "-e", oldTip+"^{commit}")
	gitRun(t, repo, "cat-file", "-e", oldRecord+"^{commit}")
	if got := gitRun(t, repo, "rev-parse", "refs/heads/agent/j/mul-history"); got != contentTwo {
		t.Errorf("branch tip = %s, want %s", got, contentTwo)
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

func TestReanchorBranchNormalizesRevisionInputs(t *testing.T) {
	repo := newTestRepo(t)
	gitRun(t, repo, "commit", "--allow-empty", "-m", "m2")
	m2 := gitRun(t, repo, "rev-parse", "HEAD")
	gitRun(t, repo, "commit", "--allow-empty", "-m", "main")
	mainTip := gitRun(t, repo, "rev-parse", "HEAD")
	gitRun(t, repo, "update-ref", "refs/remotes/origin/main", mainTip)
	gitRun(t, repo, "commit", "--allow-empty", "-m", "content one")
	contentOne := gitRun(t, repo, "rev-parse", "HEAD")

	gitRun(t, repo, "update-ref", "refs/heads/agent/j/mul-symbolic-base", m2)
	first, err := ReanchorBranch(repo, "agent/j/mul-symbolic-base", contentOne[:12], "origin/main", ReanchorNote{})
	if err != nil {
		t.Fatalf("ReanchorBranch(origin/main, short target): %v", err)
	}
	if first.NewTip != contentOne || first.ExpectedBase != mainTip {
		t.Fatalf("normalized result = %+v, want target %s and base %s", first, contentOne, mainTip)
	}

	gitRun(t, repo, "commit", "--allow-empty", "-m", "content two")
	contentTwo := gitRun(t, repo, "rev-parse", "HEAD")
	gitRun(t, repo, "update-ref", "refs/heads/agent/j/mul-short-base", m2)
	second, err := ReanchorBranch(repo, "agent/j/mul-short-base", contentTwo[:12], contentOne[:12], ReanchorNote{})
	if err != nil {
		t.Fatalf("ReanchorBranch(short base, short target): %v", err)
	}
	if second.NewTip != contentTwo || second.ExpectedBase != contentOne {
		t.Fatalf("normalized result = %+v, want target %s and base %s", second, contentTwo, contentOne)
	}
}

func TestReanchorBranchSerializesConcurrentLinkedWorktreeCreation(t *testing.T) {
	repo := newTestRepo(t)
	gitRun(t, repo, "commit", "--allow-empty", "-m", "m2")
	m2 := gitRun(t, repo, "rev-parse", "HEAD")
	gitRun(t, repo, "commit", "--allow-empty", "-m", "main")
	mainTip := gitRun(t, repo, "rev-parse", "HEAD")
	gitRun(t, repo, "commit", "--allow-empty", "-m", "content")
	content := gitRun(t, repo, "rev-parse", "HEAD")
	branch := "agent/j/mul-locked-race"
	gitRun(t, repo, "update-ref", "refs/heads/"+branch, m2)

	checked := make(chan struct{})
	allowTransaction := make(chan struct{})
	var allowOnce sync.Once
	afterLinkedWorktreeCheck := func() {
		close(checked)
		<-allowTransaction
	}
	t.Cleanup(func() {
		allowOnce.Do(func() { close(allowTransaction) })
	})

	var result ReanchorResult
	var reanchorErr error
	reanchorDone := make(chan struct{})
	go func() {
		result, reanchorErr = reanchorBranch(repo, branch, content, mainTip, ReanchorNote{}, afterLinkedWorktreeCheck)
		close(reanchorDone)
	}()
	select {
	case <-checked:
	case <-time.After(30 * time.Second):
		t.Fatal("ReanchorBranch did not reach the linked-worktree check")
	}

	linked := filepath.Join(t.TempDir(), "linked")
	creatorDone := make(chan error, 1)
	go func() {
		unlock, err := lockGitRoot(repo, nil)
		if err != nil {
			creatorDone <- err
			return
		}
		defer unlock()
		_, err = gitTry(t, repo, "worktree", "add", linked, branch)
		creatorDone <- err
	}()

	// The creator has started after the inspection, but must wait until the
	// re-anchor commits. Without the lock it adds the worktree at the old tip,
	// then update-ref moves its branch behind the live worktree.
	select {
	case err := <-creatorDone:
		t.Fatalf("linked worktree creation escaped the re-anchor critical section: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	allowOnce.Do(func() { close(allowTransaction) })

	select {
	case <-reanchorDone:
		if reanchorErr != nil {
			t.Fatalf("ReanchorBranch: %v", reanchorErr)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("ReanchorBranch did not finish")
	}
	select {
	case err := <-creatorDone:
		if err != nil {
			t.Fatalf("create linked worktree: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("linked worktree creation did not finish")
	}

	if result.NewTip != content {
		t.Fatalf("re-anchor result tip = %s, want %s", result.NewTip, content)
	}
	if got := gitRun(t, repo, "rev-parse", "refs/heads/"+branch); got != content {
		t.Errorf("branch = %s, want %s", got, content)
	}
	if got := gitRun(t, linked, "rev-parse", "HEAD"); got != content {
		t.Errorf("linked worktree HEAD = %s, want %s", got, content)
	}
	if got, want := gitRun(t, linked, "ls-files", "--stage"), gitRun(t, repo, "ls-files", "--stage"); got != want {
		t.Errorf("linked worktree index differs from the re-anchored tree:\n got %s\nwant %s", got, want)
	}
}

type failingReanchorAuditFile struct {
	writeErr error
	closeErr error
}

func (f failingReanchorAuditFile) Write(p []byte) (int, error) {
	if f.writeErr != nil {
		return 0, f.writeErr
	}
	return len(p), nil
}

func (f failingReanchorAuditFile) Close() error {
	return f.closeErr
}

func TestReanchorBranchAuditFailureIsBestEffort(t *testing.T) {
	for _, tc := range []struct {
		name string
		file failingReanchorAuditFile
	}{
		{name: "write", file: failingReanchorAuditFile{writeErr: errors.New("write failed")}},
		{name: "close", file: failingReanchorAuditFile{closeErr: errors.New("close failed")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := newTestRepo(t)
			gitRun(t, repo, "commit", "--allow-empty", "-m", "m2")
			m2 := gitRun(t, repo, "rev-parse", "HEAD")
			gitRun(t, repo, "commit", "--allow-empty", "-m", "main")
			mainTip := gitRun(t, repo, "rev-parse", "HEAD")
			gitRun(t, repo, "commit", "--allow-empty", "-m", "content")
			content := gitRun(t, repo, "rev-parse", "HEAD")
			branch := "agent/j/mul-audit-" + tc.name
			gitRun(t, repo, "update-ref", "refs/heads/"+branch, m2)

			previousOpen := openReanchorAuditFile
			openReanchorAuditFile = func(string) (reanchorAuditFile, error) {
				return tc.file, nil
			}
			t.Cleanup(func() { openReanchorAuditFile = previousOpen })

			result, err := ReanchorBranch(repo, branch, content, mainTip, ReanchorNote{})
			if err != nil {
				t.Fatalf("ReanchorBranch returned an audit error after refs committed: %v", err)
			}
			if got := gitRun(t, repo, "rev-parse", "refs/heads/"+branch); got != content {
				t.Errorf("branch = %s, want %s", got, content)
			}
			if result.AuditPath != "" || result.AuditLine != "" {
				t.Errorf("result claims audit was written after %s failure: %+v", tc.name, result)
			}
		})
	}
}
