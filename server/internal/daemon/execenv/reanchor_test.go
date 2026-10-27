package execenv

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
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
	markerPath := prepareReanchorFixture(t, repo, "agent/j/mul-x")
	res, err := ReanchorBranch(repo, "agent/j/mul-x", content, mainTip, "", note)
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
	marker := readReanchorTestMarker(t, markerPath)
	if marker.BaseCommit != content {
		t.Errorf("marker base_commit = %s, want %s", marker.BaseCommit, content)
	}
	// The re-pinned record must prove ownership the way the next prepare will
	// check it: checkpoint is an ancestor of the tip and the record is ours.
	if _, owned := branchOwnedBy(repo, "agent/j/mul-x", testBranchOwner, worktreeTestLogger()); !owned {
		t.Error("re-anchored record no longer proves ownership for the next prepare")
	}
	gitRun(t, repo, "checkout", "agent/j/mul-x")
	preflight, err := PreflightWorktree(repo)
	if err != nil || preflight.Status != "pass" {
		t.Fatalf("PreflightWorktree after re-anchor = %+v, error = %v; want pass", preflight, err)
	}
	gitRun(t, repo, "checkout", "main")
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
	_, err := ReanchorBranch(repo, "agent/j/mul-invalid-record", content, mainTip, prepareReanchorFixture(t, repo, "agent/j/mul-invalid-record"), ReanchorNote{})
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

	_, err := ReanchorBranch(repo, "agent/j/mul-linked", content, mainTip, prepareReanchorFixture(t, repo, "agent/j/mul-linked"), ReanchorNote{})
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
	markerPath := prepareReanchorFixture(t, repo, "agent/j/mul-history")
	first, err := ReanchorBranch(repo, "agent/j/mul-history", contentOne, mainTip, markerPath, ReanchorNote{})
	if err != nil {
		t.Fatalf("first ReanchorBranch: %v", err)
	}

	gitRun(t, repo, "commit", "--allow-empty", "-m", "content two")
	contentTwo := gitRun(t, repo, "rev-parse", "HEAD")
	second, err := ReanchorBranch(repo, "agent/j/mul-history", contentTwo, contentOne, markerPath, ReanchorNote{})
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

	_, err := ReanchorBranch(repo, "agent/j/mul-y", stale, mainTip, prepareReanchorFixture(t, repo, "agent/j/mul-y"), ReanchorNote{})
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

	_, err := ReanchorBranch(repo, "agent/j/mul-z", content, mainTip, prepareReanchorFixture(t, repo, "agent/j/mul-z"), ReanchorNote{})
	if err == nil || !strings.Contains(err.Error(), "delivery-guard refusal") {
		t.Fatalf("reanchor proceeded despite a pending refusal marker: %v", err)
	}
	if got := gitRun(t, repo, "rev-parse", "refs/heads/agent/j/mul-z"); got != old {
		t.Errorf("branch moved despite refusal marker: %s", got)
	}
}

func TestReanchorBranchRefusesMissingRecordWithoutChangingMarker(t *testing.T) {
	repo := newTestRepo(t)
	gitRun(t, repo, "commit", "--allow-empty", "-m", "m2")
	m2 := gitRun(t, repo, "rev-parse", "HEAD")
	gitRun(t, repo, "commit", "--allow-empty", "-m", "m3")
	mainTip := gitRun(t, repo, "rev-parse", "HEAD")
	gitRun(t, repo, "commit", "--allow-empty", "-m", "content")
	content := gitRun(t, repo, "rev-parse", "HEAD")
	branch := "agent/j/mul-w"
	gitRun(t, repo, "update-ref", "refs/heads/"+branch, m2)
	markerPath := writeReanchorTestMarker(t, repo, branch, m2)
	markerBefore, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatal(err)
	}

	_, err = ReanchorBranch(repo, branch, content, mainTip, markerPath, ReanchorNote{})
	if err == nil || !strings.Contains(err.Error(), "has no Multica branch record") {
		t.Fatalf("ReanchorBranch error = %v, want missing branch record refusal", err)
	}
	if got := gitRun(t, repo, "rev-parse", "refs/heads/"+branch); got != m2 {
		t.Errorf("branch tip = %s, want unchanged %s", got, m2)
	}
	markerAfter, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(markerAfter) != string(markerBefore) {
		t.Fatalf("marker changed after missing-record refusal:\n got %s\nwant %s", markerAfter, markerBefore)
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

	_, err := ReanchorBranch(repo, "agent/j/mul-v", content, mainTip, prepareReanchorFixture(t, repo, "agent/j/mul-v"), ReanchorNote{})
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
	first, err := ReanchorBranch(repo, "agent/j/mul-symbolic-base", contentOne[:12], "origin/main", prepareReanchorFixture(t, repo, "agent/j/mul-symbolic-base"), ReanchorNote{})
	if err != nil {
		t.Fatalf("ReanchorBranch(origin/main, short target): %v", err)
	}
	if first.NewTip != contentOne || first.ExpectedBase != mainTip {
		t.Fatalf("normalized result = %+v, want target %s and base %s", first, contentOne, mainTip)
	}

	gitRun(t, repo, "commit", "--allow-empty", "-m", "content two")
	contentTwo := gitRun(t, repo, "rev-parse", "HEAD")
	gitRun(t, repo, "update-ref", "refs/heads/agent/j/mul-short-base", m2)
	second, err := ReanchorBranch(repo, "agent/j/mul-short-base", contentTwo[:12], contentOne[:12], prepareReanchorFixture(t, repo, "agent/j/mul-short-base"), ReanchorNote{})
	if err != nil {
		t.Fatalf("ReanchorBranch(short base, short target): %v", err)
	}
	if second.NewTip != contentTwo || second.ExpectedBase != contentOne {
		t.Fatalf("normalized result = %+v, want target %s and base %s", second, contentTwo, contentOne)
	}
}

func TestReanchorBranchRejectsLinkedWorktreeCreatedDuringReservation(t *testing.T) {
	repo := newTestRepo(t)
	gitRun(t, repo, "commit", "--allow-empty", "-m", "m2")
	m2 := gitRun(t, repo, "rev-parse", "HEAD")
	gitRun(t, repo, "commit", "--allow-empty", "-m", "main")
	mainTip := gitRun(t, repo, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(repo, "reanchor-content.txt"), []byte("new content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "add", "reanchor-content.txt")
	gitRun(t, repo, "commit", "-m", "content")
	content := gitRun(t, repo, "rev-parse", "HEAD")
	branch := "agent/j/mul-reservation-race"
	gitRun(t, repo, "update-ref", "refs/heads/"+branch, m2)

	linked := filepath.Join(t.TempDir(), "linked")
	created := make(chan error, 1)
	oldIndex := ""
	afterLinkedWorktreeCheck := func() {
		go func() {
			_, err := gitTry(t, repo, "worktree", "add", linked, branch)
			created <- err
		}()
		select {
		case err := <-created:
			if err != nil {
				t.Errorf("create linked worktree in re-anchor gap: %v", err)
				return
			}
			oldIndex = gitRun(t, linked, "ls-files", "--stage")
		case <-time.After(30 * time.Second):
			t.Fatal("linked worktree creation did not finish")
		}
	}

	_, err := reanchorBranch(repo, branch, content, mainTip, prepareReanchorFixture(t, repo, branch), ReanchorNote{}, afterLinkedWorktreeCheck)
	if err == nil || !strings.Contains(err.Error(), "reserve branch") {
		t.Fatalf("re-anchor accepted a branch checked out during reservation: %v", err)
	}
	if got := gitRun(t, repo, "rev-parse", "refs/heads/"+branch); got != m2 {
		t.Errorf("branch moved after linked worktree won the gap: got %s, want %s", got, m2)
	}
	if got := gitRun(t, linked, "rev-parse", "HEAD"); got != m2 {
		t.Errorf("linked worktree HEAD changed: got %s, want %s", got, m2)
	}
	if oldIndex != gitRun(t, linked, "ls-files", "--stage") {
		t.Errorf("linked worktree index changed after refused re-anchor")
	}
	if status := gitRun(t, linked, "status", "--porcelain"); status != "" {
		t.Errorf("linked worktree became inconsistent after refused re-anchor: %s", status)
	}
}

func TestReanchorBranchRejectsMarkerWriteWithoutMovingRefs(t *testing.T) {
	repo := newTestRepo(t)
	gitRun(t, repo, "commit", "--allow-empty", "-m", "m2")
	m2 := gitRun(t, repo, "rev-parse", "HEAD")
	gitRun(t, repo, "commit", "--allow-empty", "-m", "main")
	mainTip := gitRun(t, repo, "rev-parse", "HEAD")
	gitRun(t, repo, "commit", "--allow-empty", "-m", "content")
	content := gitRun(t, repo, "rev-parse", "HEAD")
	branch := "agent/j/mul-marker-write-failure"
	gitRun(t, repo, "update-ref", "refs/heads/"+branch, m2)
	markerPath := prepareReanchorFixture(t, repo, branch)

	markerBefore, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	recordBefore := gitRun(t, repo, "rev-parse", userStateRef(branch))
	_, err = reanchorBranchWithOperations(repo, branch, content, mainTip, markerPath, ReanchorNote{}, func() {}, func(string, []byte) error {
		return errors.New("marker storage unavailable")
	}, updateReanchorRefs)
	if err == nil || !strings.Contains(err.Error(), "update task marker") {
		t.Fatalf("reanchor error = %v, want marker-write refusal", err)
	}
	if got := gitRun(t, repo, "rev-parse", "refs/heads/"+branch); got != m2 {
		t.Errorf("branch moved after marker write failure: got %s, want %s", got, m2)
	}
	if got := gitRun(t, repo, "rev-parse", userStateRef(branch)); got != recordBefore {
		t.Errorf("record changed after marker write failure: got %s, want %s", got, recordBefore)
	}
	markerAfter, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(markerAfter) != string(markerBefore) {
		t.Fatalf("marker changed after write refusal:\n got %s\nwant %s", markerAfter, markerBefore)
	}
}

func TestReanchorBranchRestoresMarkerAfterRefTransactionFailure(t *testing.T) {
	repo := newTestRepo(t)
	gitRun(t, repo, "commit", "--allow-empty", "-m", "m2")
	m2 := gitRun(t, repo, "rev-parse", "HEAD")
	gitRun(t, repo, "commit", "--allow-empty", "-m", "main")
	mainTip := gitRun(t, repo, "rev-parse", "HEAD")
	gitRun(t, repo, "commit", "--allow-empty", "-m", "content")
	content := gitRun(t, repo, "rev-parse", "HEAD")
	branch := "agent/j/mul-ref-transaction-failure"
	gitRun(t, repo, "update-ref", "refs/heads/"+branch, m2)
	markerPath := prepareReanchorFixture(t, repo, branch)

	markerBefore, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	recordBefore := gitRun(t, repo, "rev-parse", userStateRef(branch))
	_, err = reanchorBranchWithOperations(repo, branch, content, mainTip, markerPath, ReanchorNote{}, func() {}, writeMarkerFileAtomic, func(string, string, string, string, string, string, string, string) error {
		return errors.New("simulated ref transaction rejection")
	})
	if err == nil || !strings.Contains(err.Error(), "simulated ref transaction rejection") {
		t.Fatalf("reanchor error = %v, want ref transaction failure", err)
	}
	if got := gitRun(t, repo, "rev-parse", "refs/heads/"+branch); got != m2 {
		t.Errorf("branch moved after ref transaction failure: got %s, want %s", got, m2)
	}
	if got := gitRun(t, repo, "rev-parse", userStateRef(branch)); got != recordBefore {
		t.Errorf("record changed after ref transaction failure: got %s, want %s", got, recordBefore)
	}
	markerAfter, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(markerAfter) != string(markerBefore) {
		t.Fatalf("marker was not restored after ref transaction failure:\n got %s\nwant %s", markerAfter, markerBefore)
	}
}

type failingReanchorAuditFile struct {
	writeErr error
	closeErr error
}

func prepareReanchorFixture(t *testing.T, repo, branch string) string {
	t.Helper()
	branchTip := gitRun(t, repo, "rev-parse", "refs/heads/"+branch)
	if existing := runGitSafeTrimmed(repo, "rev-parse", "--verify", "--quiet", userStateRef(branch)); existing == "" {
		snapshot := gitRun(t, repo, "commit-tree", "HEAD^{tree}")
		if _, err := writeBranchRecord(repo, branch, snapshot, branchTip, testBranchOwner); err != nil {
			t.Fatalf("writeBranchRecord(%s): %v", branch, err)
		}
	}
	return writeReanchorTestMarker(t, repo, branch, branchTip)
}

func writeReanchorTestMarker(t *testing.T, repo, branch, baseCommit string) string {
	t.Helper()
	path := filepath.Join(repo, TaskContextMarkerRelPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(taskContextMarkerFile{
		ManagedBy:  TaskContextMarkerManagedBy,
		TaskID:     "reanchor-task",
		AgentID:    "reanchor-agent",
		IssueID:    "reanchor-issue",
		Branch:     branch,
		BaseCommit: baseCommit,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func readReanchorTestMarker(t *testing.T, path string) taskContextMarkerFile {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var marker taskContextMarkerFile
	if err := json.Unmarshal(data, &marker); err != nil {
		t.Fatalf("decode task marker: %v", err)
	}
	return marker
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

			result, err := ReanchorBranch(repo, branch, content, mainTip, prepareReanchorFixture(t, repo, branch), ReanchorNote{})
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
