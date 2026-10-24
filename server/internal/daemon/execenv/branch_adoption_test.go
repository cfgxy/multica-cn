package execenv

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAdoptManagedBranchBindsContinuationRecordAndMarker(t *testing.T) {
	fixture := newBranchAdoptionFixture(t)

	result, err := AdoptManagedBranch(fixture.repo, fixture.adoption)
	if err != nil {
		t.Fatalf("AdoptManagedBranch: %v", err)
	}
	if result.Branch != fixture.branch || result.RemoteSHA != fixture.tip || result.BaseCommit != fixture.tip {
		t.Fatalf("result = %+v, want branch and exact remote tip %s", result, fixture.tip)
	}
	if result.SourceTaskID != fixture.adoption.SourceOwner.TaskID || result.ContinuationTask != fixture.adoption.TargetOwner.TaskID {
		t.Fatalf("result task binding = %+v", result)
	}
	if current := gitRun(t, fixture.repo, "branch", "--show-current"); current != fixture.branch {
		t.Fatalf("current branch = %q, want adopted %q", current, fixture.branch)
	}

	marker := readBranchAdoptionMarker(t, fixture.markerPath)
	if marker.TaskID != fixture.adoption.TargetOwner.TaskID || marker.AgentID != fixture.adoption.TargetOwner.AgentID || marker.IssueID != fixture.adoption.TargetOwner.ConversationID {
		t.Fatalf("marker identity changed during adoption: %+v", marker)
	}
	if marker.Branch != fixture.branch || marker.BaseCommit != fixture.tip {
		t.Fatalf("marker delivery context = %+v, want branch %s and base %s", marker, fixture.branch, fixture.tip)
	}

	recordRef := gitRun(t, fixture.repo, "rev-parse", userStateRef(fixture.branch))
	if recordRef != result.Record {
		t.Fatalf("branch record ref = %s, want %s", recordRef, result.Record)
	}
	if parent := gitRun(t, fixture.repo, "rev-parse", result.Record+"^1"); parent != fixture.sourceRecord {
		t.Fatalf("continuation record parent = %s, want source record %s", parent, fixture.sourceRecord)
	}
	if checkpoint := gitRun(t, fixture.repo, "rev-parse", result.Record+"^2"); checkpoint != fixture.tip {
		t.Fatalf("continuation record checkpoint = %s, want %s", checkpoint, fixture.tip)
	}
	gotRecord, err := readBranchRecord(fixture.repo, result.Record)
	if err != nil {
		t.Fatalf("read adopted record: %v", err)
	}
	if gotRecord.owner != adoptionRecordOwner(fixture.adoption.TargetOwner) {
		t.Fatalf("continuation record owner = %+v, want target conversation owner", gotRecord.owner)
	}
	message := gitRun(t, fixture.repo, "show", "-s", "--format=%B", result.Record)
	for _, want := range []string{
		"Source task: " + fixture.adoption.SourceOwner.TaskID,
		"Continuation task: " + fixture.adoption.TargetOwner.TaskID,
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("continuation record provenance missing %q:\n%s", want, message)
		}
	}
	if _, owned := branchOwnedBy(fixture.repo, fixture.branch, adoptionRecordOwner(fixture.adoption.TargetOwner), worktreeTestLogger()); !owned {
		t.Fatal("new continuation record is not continuable by the normal branch-owner guard")
	}
	preflight, err := PreflightWorktree(fixture.repo)
	if err != nil || preflight.Status != "pass" {
		t.Fatalf("PreflightWorktree = %+v, error = %v; want pass", preflight, err)
	}
}

// The agent invokes adoption while its worktree is live, but the daemon owns
// Finalize after the provider exits. Keep that boundary in one regression: a
// successful adoption must replace every delivery field before Finalize checks
// the branch and checkpoint it is about to record.
func TestLocalWorktreeAdoptManagedBranchFinalizesContinuation(t *testing.T) {
	repo, _ := newTestRepoWithOrigin(t)
	const sourceBranch = "agent/source/ruyi-871"
	gitRun(t, repo, "switch", "-c", sourceBranch)
	writeFile(t, filepath.Join(repo, "source-delivery.txt"), "source delivery\n")
	gitRun(t, repo, "add", "source-delivery.txt")
	gitRun(t, repo, "commit", "-m", "source delivery")
	sourceTip := gitRun(t, repo, "rev-parse", "HEAD")
	gitRun(t, repo, "push", "--quiet", "origin", sourceBranch)

	sourceOwner := BranchAdoptionOwner{
		WorkspaceID:    "workspace-id",
		AgentID:        "source-agent",
		ConversationID: "source-issue",
		TaskID:         "source-task",
	}
	// A captured user snapshot is rooted at the user's checkout HEAD. Keep the
	// fixture structurally identical so the follow-up Prepare exercises normal
	// replay semantics rather than an impossible parentless snapshot.
	sourceState := gitRun(t, repo, "commit-tree", "main^{tree}", "-p", "main", "-m", "source snapshot")
	if _, err := writeBranchRecord(repo, sourceBranch, sourceState, sourceTip, adoptionRecordOwner(sourceOwner)); err != nil {
		t.Fatalf("write source branch record: %v", err)
	}
	gitRun(t, repo, "switch", "main")

	targetOwner := BranchAdoptionOwner{
		WorkspaceID:    "workspace-id",
		AgentID:        "target-agent",
		ConversationID: "target-issue",
		TaskID:         "target-task",
	}
	worktree, err := PrepareLocalWorktree(LocalWorktreeParams{
		LocalPath:       repo,
		EnvRoot:         t.TempDir(),
		AgentName:       "Target Agent",
		TaskID:          targetOwner.TaskID,
		WorkspaceID:     targetOwner.WorkspaceID,
		AgentID:         targetOwner.AgentID,
		ConversationID:  targetOwner.ConversationID,
		ConversationKey: "RUYI-870",
	}, worktreeTestLogger())
	if err != nil {
		t.Fatalf("PrepareLocalWorktree: %v", err)
	}
	markerPath := filepath.Join(worktree.WorkDir, TaskContextMarkerRelPath)
	if err := os.MkdirAll(filepath.Dir(markerPath), 0o755); err != nil {
		t.Fatal(err)
	}
	markerData, err := json.Marshal(taskContextMarkerFile{
		ManagedBy:  TaskContextMarkerManagedBy,
		TaskID:     targetOwner.TaskID,
		AgentID:    targetOwner.AgentID,
		IssueID:    targetOwner.ConversationID,
		Branch:     worktree.Branch,
		BaseCommit: worktree.BaseCommit,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(markerPath, markerData, 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := worktree.AdoptManagedBranch(BranchAdoption{
		Branch:            sourceBranch,
		ExpectedRemoteSHA: sourceTip,
		MarkerPath:        markerPath,
		SourceOwner:       sourceOwner,
		TargetOwner:       targetOwner,
	})
	if err != nil {
		t.Fatalf("AdoptManagedBranch: %v", err)
	}
	if worktree.Branch != sourceBranch || worktree.BaseCommit != sourceTip {
		t.Fatalf("adopted worktree delivery state = branch %q base %q, want %q %q", worktree.Branch, worktree.BaseCommit, sourceBranch, sourceTip)
	}
	if !worktree.Continued || !worktree.tracksState || worktree.createdBranch {
		t.Fatalf("adopted worktree lifecycle state = continued %t tracks_state %t created_branch %t, want true true false", worktree.Continued, worktree.tracksState, worktree.createdBranch)
	}
	if worktree.userState != result.Record || worktree.priorState != result.Record {
		t.Fatalf("adopted worktree snapshot state = current %q prior %q, want continuation record %q", worktree.userState, worktree.priorState, result.Record)
	}
	if marker := readBranchAdoptionMarker(t, markerPath); marker.Branch != sourceBranch || marker.BaseCommit != sourceTip {
		t.Fatalf("adopted marker = %+v, want source branch and tip", marker)
	}

	writeFile(t, filepath.Join(worktree.WorkDir, "continuation-delivery.txt"), "continued delivery\n")
	outcome, err := worktree.Finalize(worktreeTestLogger())
	if err != nil {
		t.Fatalf("Finalize after adoption: %v", err)
	}
	if outcome.Branch != sourceBranch {
		t.Fatalf("Finalize branch = %q, want adopted branch %q", outcome.Branch, sourceBranch)
	}
	if got := gitRun(t, repo, "show", sourceBranch+":continuation-delivery.txt"); got != "continued delivery" {
		t.Fatalf("adopted branch did not receive continuation delivery: %q", got)
	}
	recordRef := gitRun(t, repo, "rev-parse", userStateRef(sourceBranch))
	record, err := readBranchRecord(repo, recordRef)
	if err != nil {
		t.Fatalf("read continuation record: %v", err)
	}
	if record.owner != adoptionRecordOwner(targetOwner) {
		t.Fatalf("continuation record owner = %+v, want target owner", record.owner)
	}
	if got, want := record.checkpoint, gitRun(t, repo, "rev-parse", sourceBranch); got != want {
		t.Fatalf("continuation record checkpoint = %q, want adopted branch tip %q", got, want)
	}

	// The follow-up task starts from a fresh env root. Its target-derived branch
	// name is not the adopted source branch, so this proves the durable adoption
	// index, rather than the in-memory LocalWorktree state, resumes the work line.
	followUpOwner := targetOwner
	followUpOwner.TaskID = "target-follow-up-task"
	followUp, err := PrepareLocalWorktree(LocalWorktreeParams{
		LocalPath:       repo,
		EnvRoot:         t.TempDir(),
		AgentName:       "Target Agent",
		TaskID:          followUpOwner.TaskID,
		WorkspaceID:     followUpOwner.WorkspaceID,
		AgentID:         followUpOwner.AgentID,
		ConversationID:  followUpOwner.ConversationID,
		ConversationKey: "RUYI-870",
	}, worktreeTestLogger())
	if err != nil {
		t.Fatalf("PrepareLocalWorktree after adoption: %v", err)
	}
	if followUp.Branch != sourceBranch || !followUp.Continued {
		t.Fatalf("follow-up worktree = branch %q continued %t, want adopted branch %q and continuation", followUp.Branch, followUp.Continued, sourceBranch)
	}
	if got := readFile(t, filepath.Join(followUp.WorkDir, "continuation-delivery.txt")); got != "continued delivery\n" {
		t.Fatalf("follow-up worktree did not retain adopted delivery: %q", got)
	}
	finalizeOK(t, followUp)

	// A user can later delete the adopted branch. The private discovery ref must
	// then be reclaimed rather than recreating that source name from a fresh base.
	gitRun(t, repo, "branch", "-D", sourceBranch)
	staleOwner := targetOwner
	staleOwner.TaskID = "target-after-deletion-task"
	afterDeletion, err := PrepareLocalWorktree(LocalWorktreeParams{
		LocalPath:       repo,
		EnvRoot:         t.TempDir(),
		AgentName:       "Target Agent",
		TaskID:          staleOwner.TaskID,
		WorkspaceID:     staleOwner.WorkspaceID,
		AgentID:         staleOwner.AgentID,
		ConversationID:  staleOwner.ConversationID,
		ConversationKey: "RUYI-870",
	}, worktreeTestLogger())
	if err != nil {
		t.Fatalf("PrepareLocalWorktree after adopted branch deletion: %v", err)
	}
	if afterDeletion.Branch == sourceBranch {
		t.Fatalf("PrepareLocalWorktree recreated deleted adopted branch %q", sourceBranch)
	}
	if _, err := gitTry(t, repo, "rev-parse", "--verify", "--quiet", adoptedContinuationRef(adoptionRecordOwner(targetOwner))); err == nil {
		t.Fatal("deleted adopted branch left its continuation discovery ref behind")
	}
	finalizeOK(t, afterDeletion)
}

func TestAdoptManagedBranchRefusesCompetingContinuation(t *testing.T) {
	fixture := newBranchAdoptionFixture(t)
	if _, err := AdoptManagedBranch(fixture.repo, fixture.adoption); err != nil {
		t.Fatalf("first AdoptManagedBranch: %v", err)
	}

	continuation := adoptionRecordOwner(fixture.adoption.TargetOwner)
	mappingRef := adoptedContinuationRef(continuation)
	mappingBefore := gitRun(t, fixture.repo, "rev-parse", mappingRef)
	firstRecordBefore := gitRun(t, fixture.repo, "rev-parse", userStateRef(fixture.branch))
	markerBefore, err := os.ReadFile(fixture.markerPath)
	if err != nil {
		t.Fatal(err)
	}

	gitRun(t, fixture.repo, "switch", "main")
	const secondBranch = "agent/other-source/ruyi-872"
	gitRun(t, fixture.repo, "switch", "-c", secondBranch)
	writeFile(t, filepath.Join(fixture.repo, "second-source.txt"), "second source delivery\n")
	gitRun(t, fixture.repo, "add", "second-source.txt")
	gitRun(t, fixture.repo, "commit", "-m", "second source delivery")
	secondTip := gitRun(t, fixture.repo, "rev-parse", "HEAD")
	gitRun(t, fixture.repo, "push", "--quiet", "origin", secondBranch)
	secondSource := BranchAdoptionOwner{
		WorkspaceID:    fixture.adoption.SourceOwner.WorkspaceID,
		AgentID:        "other-source-agent",
		ConversationID: "other-source-issue",
		TaskID:         "other-source-task",
	}
	secondState := gitRun(t, fixture.repo, "commit-tree", "main^{tree}", "-p", "main", "-m", "second source snapshot")
	secondRecord, err := writeBranchRecord(fixture.repo, secondBranch, secondState, secondTip, adoptionRecordOwner(secondSource))
	if err != nil {
		t.Fatalf("write second source branch record: %v", err)
	}
	gitRun(t, fixture.repo, "switch", "main")

	secondAdoption := fixture.adoption
	secondAdoption.Branch = secondBranch
	secondAdoption.ExpectedRemoteSHA = secondTip
	secondAdoption.SourceOwner = secondSource
	if _, err := AdoptManagedBranch(fixture.repo, secondAdoption); err == nil || !strings.Contains(err.Error(), "already owns adopted branch") {
		t.Fatalf("second AdoptManagedBranch error = %v, want competing continuation refusal", err)
	}
	if got := gitRun(t, fixture.repo, "rev-parse", mappingRef); got != mappingBefore {
		t.Fatalf("continuation mapping changed after refusal: got %s, want %s", got, mappingBefore)
	}
	if got := gitRun(t, fixture.repo, "rev-parse", userStateRef(fixture.branch)); got != firstRecordBefore {
		t.Fatalf("first source record changed after refusal: got %s, want %s", got, firstRecordBefore)
	}
	if got := gitRun(t, fixture.repo, "rev-parse", userStateRef(secondBranch)); got != secondRecord {
		t.Fatalf("second source record changed after refusal: got %s, want %s", got, secondRecord)
	}
	markerAfter, err := os.ReadFile(fixture.markerPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(markerAfter) != string(markerBefore) {
		t.Fatalf("marker changed after competing continuation refusal:\n got %s\nwant %s", markerAfter, markerBefore)
	}
	if current := gitRun(t, fixture.repo, "branch", "--show-current"); current != "main" {
		t.Fatalf("current branch changed after competing continuation refusal: got %q, want main", current)
	}
}

func TestUpdateAdoptedBranchRecordUsesContinuationMappingCAS(t *testing.T) {
	fixture := newBranchAdoptionFixture(t)
	continuation := adoptionRecordOwner(fixture.adoption.TargetOwner)
	newRecord, err := writeAdoptedBranchRecord(
		fixture.repo,
		fixture.sourceRecord,
		fixture.branch,
		fixture.tip,
		fixture.adoption.TargetOwner,
		fixture.adoption.SourceOwner.TaskID,
	)
	if err != nil {
		t.Fatalf("writeAdoptedBranchRecord: %v", err)
	}

	mappingRef := adoptedContinuationRef(continuation)
	gitRun(t, fixture.repo, "update-ref", mappingRef, fixture.sourceRecord)
	if err := updateAdoptedBranchRecord(fixture.repo, fixture.branch, fixture.sourceRecord, newRecord, continuation, ""); err == nil {
		t.Fatal("updateAdoptedBranchRecord succeeded after another process created the continuation mapping")
	}
	if got := gitRun(t, fixture.repo, "rev-parse", userStateRef(fixture.branch)); got != fixture.sourceRecord {
		t.Fatalf("source record changed after mapping create conflict: got %s, want %s", got, fixture.sourceRecord)
	}
	if got := gitRun(t, fixture.repo, "rev-parse", mappingRef); got != fixture.sourceRecord {
		t.Fatalf("continuation mapping changed after create conflict: got %s, want %s", got, fixture.sourceRecord)
	}

	gitRun(t, fixture.repo, "update-ref", mappingRef, fixture.tip)
	if err := updateAdoptedBranchRecord(fixture.repo, fixture.branch, fixture.sourceRecord, newRecord, continuation, fixture.sourceRecord); err == nil {
		t.Fatal("updateAdoptedBranchRecord succeeded after another process replaced the continuation mapping")
	}
	if got := gitRun(t, fixture.repo, "rev-parse", userStateRef(fixture.branch)); got != fixture.sourceRecord {
		t.Fatalf("source record changed after mapping update conflict: got %s, want %s", got, fixture.sourceRecord)
	}
	if got := gitRun(t, fixture.repo, "rev-parse", mappingRef); got != fixture.tip {
		t.Fatalf("continuation mapping changed after update conflict: got %s, want %s", got, fixture.tip)
	}
}

func TestAdoptManagedBranchRefusesBeforeBinding(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, fixture *branchAdoptionFixture)
		want   string
	}{
		{
			name: "remote SHA mismatch",
			mutate: func(_ *testing.T, fixture *branchAdoptionFixture) {
				fixture.adoption.ExpectedRemoteSHA = strings.Repeat("f", 40)
			},
			want: "not expected",
		},
		{
			name: "source record owner mismatch",
			mutate: func(_ *testing.T, fixture *branchAdoptionFixture) {
				fixture.adoption.SourceOwner.AgentID = "another-source-agent"
			},
			want: "does not own",
		},
		{
			name: "foreign continuation marker",
			mutate: func(t *testing.T, fixture *branchAdoptionFixture) {
				if err := os.WriteFile(fixture.markerPath, []byte(`{"managed_by":"foreign","task_id":"target-task"}`), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			want: "marker identity",
		},
		{
			name: "local branch SHA mismatch",
			mutate: func(t *testing.T, fixture *branchAdoptionFixture) {
				gitRun(t, fixture.repo, "update-ref", "refs/heads/"+fixture.branch, gitRun(t, fixture.repo, "rev-parse", "main"))
			},
			want: "local branch",
		},
		{
			name: "pending delivery guard refusal",
			mutate: func(t *testing.T, fixture *branchAdoptionFixture) {
				gitRun(t, fixture.repo, "update-ref", guardRefusalRef(fixture.branch), fixture.tip)
			},
			want: "pending delivery-guard refusal",
		},
		{
			name: "source record checkpoint continuity broken",
			mutate: func(t *testing.T, fixture *branchAdoptionFixture) {
				orphan := gitRun(t, fixture.repo, "commit-tree", "main^{tree}", "-m", "orphan checkpoint")
				message := gitRun(t, fixture.repo, "show", "-s", "--format=%B", fixture.sourceRecord)
				brokenRecord := gitRun(t, fixture.repo, "commit-tree", fixture.sourceRecord+"^{tree}", "-p", fixture.sourceRecord, "-p", orphan, "-m", message)
				gitRun(t, fixture.repo, "update-ref", userStateRef(fixture.branch), brokenRecord)
			},
			want: "no longer contains",
		},
		{
			name: "branch checked out by another worktree",
			mutate: func(t *testing.T, fixture *branchAdoptionFixture) {
				linkedPath := filepath.Join(t.TempDir(), "linked")
				gitRun(t, fixture.repo, "worktree", "add", "--quiet", linkedPath, fixture.branch)
				t.Cleanup(func() { gitRun(t, fixture.repo, "worktree", "remove", "--force", linkedPath) })
			},
			want: "is checked out",
		},
		{
			name: "checkout would overwrite an untracked file",
			mutate: func(t *testing.T, fixture *branchAdoptionFixture) {
				if err := os.WriteFile(filepath.Join(fixture.repo, "candidate.txt"), []byte("local conflict\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			want: "check out",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newBranchAdoptionFixture(t)
			tt.mutate(t, fixture)
			markerBefore, err := os.ReadFile(fixture.markerPath)
			if err != nil {
				t.Fatal(err)
			}
			recordBefore := gitRun(t, fixture.repo, "rev-parse", userStateRef(fixture.branch))
			currentBefore := gitRun(t, fixture.repo, "branch", "--show-current")
			_, err = AdoptManagedBranch(fixture.repo, fixture.adoption)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("AdoptManagedBranch error = %v, want %q", err, tt.want)
			}
			if got := gitRun(t, fixture.repo, "rev-parse", userStateRef(fixture.branch)); got != recordBefore {
				t.Fatalf("branch record changed after refusal: got %s, want %s", got, recordBefore)
			}
			markerAfter, err := os.ReadFile(fixture.markerPath)
			if err != nil {
				t.Fatal(err)
			}
			if string(markerAfter) != string(markerBefore) {
				t.Fatalf("marker changed after refusal:\n got %s\nwant %s", markerAfter, markerBefore)
			}
			if got := gitRun(t, fixture.repo, "branch", "--show-current"); got != currentBefore {
				t.Fatalf("current branch changed after refusal: got %q, want %q", got, currentBefore)
			}
		})
	}
}

type branchAdoptionFixture struct {
	repo         string
	branch       string
	tip          string
	sourceRecord string
	markerPath   string
	adoption     BranchAdoption
}

func newBranchAdoptionFixture(t *testing.T) *branchAdoptionFixture {
	t.Helper()
	repo, _ := newTestRepoWithOrigin(t)
	const branch = "agent/source/ruyi-871"
	gitRun(t, repo, "switch", "-c", branch)
	if err := os.WriteFile(filepath.Join(repo, "candidate.txt"), []byte("candidate\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "add", "candidate.txt")
	gitRun(t, repo, "commit", "-m", "candidate")
	tip := gitRun(t, repo, "rev-parse", "HEAD")
	gitRun(t, repo, "push", "--quiet", "origin", branch)

	sourceOwner := BranchAdoptionOwner{
		WorkspaceID:    "workspace-id",
		AgentID:        "source-agent",
		ConversationID: "source-issue",
		TaskID:         "source-task",
	}
	snapshot := gitRun(t, repo, "commit-tree", "HEAD^{tree}")
	sourceRecord, err := writeBranchRecord(repo, branch, snapshot, tip, adoptionRecordOwner(sourceOwner))
	if err != nil {
		t.Fatalf("write source branch record: %v", err)
	}
	gitRun(t, repo, "switch", "-c", "agent/target/ruyi-870", "main")

	targetOwner := BranchAdoptionOwner{
		WorkspaceID:    "workspace-id",
		AgentID:        "target-agent",
		ConversationID: "target-issue",
		TaskID:         "target-task",
	}
	markerPath := filepath.Join(repo, TaskContextMarkerRelPath)
	if err := os.MkdirAll(filepath.Dir(markerPath), 0o755); err != nil {
		t.Fatal(err)
	}
	markerData, err := json.Marshal(taskContextMarkerFile{
		ManagedBy:  TaskContextMarkerManagedBy,
		TaskID:     targetOwner.TaskID,
		AgentID:    targetOwner.AgentID,
		IssueID:    targetOwner.ConversationID,
		Branch:     "agent/target/ruyi-870",
		BaseCommit: gitRun(t, repo, "rev-parse", "HEAD"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(markerPath, markerData, 0o644); err != nil {
		t.Fatal(err)
	}
	return &branchAdoptionFixture{
		repo:         repo,
		branch:       branch,
		tip:          tip,
		sourceRecord: sourceRecord,
		markerPath:   markerPath,
		adoption: BranchAdoption{
			Branch:            branch,
			ExpectedRemoteSHA: tip,
			MarkerPath:        markerPath,
			SourceOwner:       sourceOwner,
			TargetOwner:       targetOwner,
		},
	}
}

func readBranchAdoptionMarker(t *testing.T, path string) taskContextMarkerFile {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var marker taskContextMarkerFile
	if err := json.Unmarshal(data, &marker); err != nil {
		t.Fatalf("decode marker: %v", err)
	}
	return marker
}
