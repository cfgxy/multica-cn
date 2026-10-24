package execenv

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// BranchAdoption binds a suspended source task's managed branch to one running
// continuation task. The daemon constructs this only from its token-bound task
// context and the server's authorization response; it is not a CLI trust
// boundary.
type BranchAdoption struct {
	Branch            string
	ExpectedRemoteSHA string
	Remote            string
	MarkerPath        string
	SourceOwner       BranchAdoptionOwner
	TargetOwner       BranchAdoptionOwner
}

// BranchAdoptionOwner is the subset of a managed branch record that adoption
// proves and then records. ConversationID is the durable issue/chat identity.
type BranchAdoptionOwner struct {
	WorkspaceID    string
	AgentID        string
	ConversationID string
	TaskID         string
}

// BranchAdoptionResult is the successful, auditable local mapping result.
type BranchAdoptionResult struct {
	Branch           string `json:"branch"`
	RemoteSHA        string `json:"remote_sha"`
	SourceTaskID     string `json:"source_task_id"`
	ContinuationTask string `json:"continuation_task_id"`
	Record           string `json:"record"`
	BaseCommit       string `json:"base_commit"`
}

// AdoptManagedBranch validates a remote branch's exact tip and its existing
// Multica ownership record, then changes the current task worktree to that
// branch and records the continuation owner. Every validation runs before the
// marker or branch-record ref is changed. The source record remains the first
// parent of the new record, retaining its provenance without a parallel ref.
func AdoptManagedBranch(gitRoot string, adoption BranchAdoption) (BranchAdoptionResult, error) {
	return adoptManagedBranch(gitRoot, adoption)
}

// AdoptManagedBranch binds the adoption to this running worktree. It holds the
// same lifecycle lock as Finalize, so a successful checkout, marker, and branch
// record update become visible to Finalize together with the matching in-memory
// delivery state.
func (w *LocalWorktree) AdoptManagedBranch(adoption BranchAdoption) (BranchAdoptionResult, error) {
	if w == nil {
		return BranchAdoptionResult{}, errors.New("adopt branch: no running local worktree")
	}
	w.lifecycleMu.Lock()
	defer w.lifecycleMu.Unlock()
	if w.aborted != nil {
		return BranchAdoptionResult{}, fmt.Errorf("adopt branch: local worktree is no longer deliverable: %w", w.aborted)
	}

	result, err := adoptManagedBranch(w.Path, adoption)
	if err != nil {
		return result, err
	}

	// The new record carries the source branch's exact snapshot tree and names
	// the continuation owner. It is therefore the only snapshot Finalize may
	// advance after checkout changes the worktree's branch.
	w.Branch = result.Branch
	w.BaseCommit = result.BaseCommit
	w.DirtyBaseCaptured = false
	w.Continued = true
	w.ReplayConflicts = nil
	w.createdBranch = false
	w.userState = result.Record
	w.priorState = result.Record
	w.owner = adoptionRecordOwner(adoption.TargetOwner)
	w.tracksState = true
	w.snapshotPending = false

	return result, nil
}

func adoptManagedBranch(gitRoot string, adoption BranchAdoption) (BranchAdoptionResult, error) {
	var result BranchAdoptionResult
	adoption.Branch = strings.TrimSpace(adoption.Branch)
	adoption.ExpectedRemoteSHA = strings.TrimSpace(adoption.ExpectedRemoteSHA)
	adoption.Remote = strings.TrimSpace(adoption.Remote)
	adoption.MarkerPath = strings.TrimSpace(adoption.MarkerPath)
	if adoption.Remote == "" {
		adoption.Remote = "origin"
	}
	if err := validateBranchAdoption(adoption); err != nil {
		return result, err
	}
	if _, err := runGit(gitRoot, "check-ref-format", "--branch", adoption.Branch); err != nil {
		return result, fmt.Errorf("adopt branch: invalid branch %q: %w", adoption.Branch, err)
	}

	root, err := runGitTrimmed(gitRoot, "rev-parse", "--show-toplevel")
	if err != nil {
		return result, fmt.Errorf("adopt branch: resolve git root: %w", err)
	}
	markerBefore, marker, err := readAdoptionMarker(adoption.MarkerPath, adoption.TargetOwner)
	if err != nil {
		return result, err
	}
	_ = marker

	// ls-remote performs no local ref update. Fetching here would make a rejected
	// adoption mutate the cache before the branch-record proof is complete.
	remoteSHA, err := remoteBranchSHA(root, adoption.Remote, adoption.Branch)
	if err != nil {
		return result, err
	}
	if remoteSHA != adoption.ExpectedRemoteSHA {
		return result, fmt.Errorf("adopt branch: remote %s/%s is %s, not expected %s", adoption.Remote, adoption.Branch, remoteSHA, adoption.ExpectedRemoteSHA)
	}

	unlock, err := lockGitRoot(root, nil)
	if err != nil {
		return result, fmt.Errorf("adopt branch: lock repository: %w", err)
	}
	defer unlock()

	// Re-check every mutable Git fact under the repository lock. The remote was
	// intentionally read before lock acquisition, but the local branch/record
	// checks are repeated so another local task cannot invalidate the proof.
	branchTip, err := runGitTrimmed(root, "rev-parse", "--verify", "refs/heads/"+adoption.Branch)
	if err != nil {
		return result, fmt.Errorf("adopt branch: resolve managed branch %s: %w", adoption.Branch, err)
	}
	if branchTip != adoption.ExpectedRemoteSHA {
		return result, fmt.Errorf("adopt branch: local branch %s is %s, not expected %s", adoption.Branch, branchTip, adoption.ExpectedRemoteSHA)
	}
	if refusal := runGitSafeTrimmed(root, "rev-parse", "--verify", "--quiet", guardRefusalRef(adoption.Branch)); refusal != "" {
		return result, fmt.Errorf("adopt branch: branch %s has a pending delivery-guard refusal at %s", adoption.Branch, shortID(refusal))
	}

	oldRecordRef, err := readUserStateRef(root, adoption.Branch)
	if err != nil || oldRecordRef == "" {
		return result, fmt.Errorf("adopt branch: branch %s has no Multica branch record", adoption.Branch)
	}
	oldRecord, err := readBranchRecord(root, oldRecordRef)
	if err != nil {
		return result, fmt.Errorf("adopt branch: read source branch record: %w", err)
	}
	sourceOwner := adoptionRecordOwner(adoption.SourceOwner)
	if oldRecord.owner != sourceOwner || oldRecord.checkpoint == "" {
		return result, errors.New("adopt branch: source task does not own the managed branch record")
	}
	if _, err := runGit(root, "merge-base", "--is-ancestor", oldRecord.checkpoint, "refs/heads/"+adoption.Branch); err != nil {
		return result, fmt.Errorf("adopt branch: source branch no longer contains its recorded checkpoint: %w", err)
	}
	if path, checkedOut, err := branchCheckoutPath(root, adoption.Branch); err != nil {
		return result, fmt.Errorf("adopt branch: inspect linked worktrees: %w", err)
	} else if checkedOut && !sameAdoptionPath(path, root) {
		return result, fmt.Errorf("adopt branch: branch %s is checked out at %s", adoption.Branch, path)
	}

	newMarker, err := adoptedMarker(marker, adoption.Branch, adoption.ExpectedRemoteSHA)
	if err != nil {
		return result, err
	}
	newRecord, err := writeAdoptedBranchRecord(root, oldRecordRef, adoption.ExpectedRemoteSHA, adoption.TargetOwner, adoption.SourceOwner.TaskID)
	if err != nil {
		return result, err
	}

	currentBranch := runGitSafeTrimmed(root, "symbolic-ref", "--quiet", "--short", "HEAD")
	currentHead, err := runGitTrimmed(root, "rev-parse", "HEAD")
	if err != nil {
		return result, fmt.Errorf("adopt branch: resolve current checkout: %w", err)
	}
	switched := false
	if currentBranch != adoption.Branch {
		if out, err := runGit(root, "checkout", adoption.Branch); err != nil {
			return result, fmt.Errorf("adopt branch: check out %s: %s: %w", adoption.Branch, strings.TrimSpace(out), err)
		}
		switched = true
	}
	restoreCheckout := func() error {
		if !switched {
			return nil
		}
		args := []string{"checkout"}
		if currentBranch == "" {
			args = append(args, "--detach", currentHead)
		} else {
			args = append(args, currentBranch)
		}
		if out, err := runGit(root, args...); err != nil {
			return fmt.Errorf("restore original checkout: %s: %w", strings.TrimSpace(out), err)
		}
		return nil
	}

	// The marker is written before the ref transaction and restored on a failed
	// transaction. The filesystem and Git ref store cannot share one primitive,
	// so this order makes the durable branch ownership change the commit point.
	if err := writeMarkerFileAtomic(adoption.MarkerPath, newMarker); err != nil {
		if rollbackErr := restoreCheckout(); rollbackErr != nil {
			return result, fmt.Errorf("adopt branch: bind continuation task marker: %w (also %v)", err, rollbackErr)
		}
		return result, fmt.Errorf("adopt branch: bind continuation task marker: %w", err)
	}
	if err := updateAdoptedBranchRecord(root, adoption.Branch, oldRecordRef, newRecord); err != nil {
		markerRollbackErr := writeMarkerFileAtomic(adoption.MarkerPath, markerBefore)
		checkoutRollbackErr := restoreCheckout()
		if markerRollbackErr != nil || checkoutRollbackErr != nil {
			return result, fmt.Errorf("adopt branch: update branch record: %w (restore marker: %v; restore checkout: %v)", err, markerRollbackErr, checkoutRollbackErr)
		}
		return result, err
	}

	return BranchAdoptionResult{
		Branch:           adoption.Branch,
		RemoteSHA:        adoption.ExpectedRemoteSHA,
		SourceTaskID:     adoption.SourceOwner.TaskID,
		ContinuationTask: adoption.TargetOwner.TaskID,
		Record:           newRecord,
		BaseCommit:       adoption.ExpectedRemoteSHA,
	}, nil
}

// adoptionRecordOwner deliberately excludes TaskID. Issue branches are owned by
// a conversation, so carrying one task ID into the new record would stop the
// ordinary next issue turn from continuing it. The server separately proves the
// source task's immutable BranchName before this local record proof runs.
func adoptionRecordOwner(owner BranchAdoptionOwner) branchOwner {
	return branchOwner{
		WorkspaceID:    owner.WorkspaceID,
		AgentID:        owner.AgentID,
		ConversationID: owner.ConversationID,
	}
}

func validateBranchAdoption(adoption BranchAdoption) error {
	for _, field := range []struct {
		name  string
		value string
	}{
		{"branch", adoption.Branch},
		{"expected remote SHA", adoption.ExpectedRemoteSHA},
		{"marker path", adoption.MarkerPath},
		{"source workspace", adoption.SourceOwner.WorkspaceID},
		{"source agent", adoption.SourceOwner.AgentID},
		{"source conversation", adoption.SourceOwner.ConversationID},
		{"source task", adoption.SourceOwner.TaskID},
		{"continuation workspace", adoption.TargetOwner.WorkspaceID},
		{"continuation agent", adoption.TargetOwner.AgentID},
		{"continuation conversation", adoption.TargetOwner.ConversationID},
		{"continuation task", adoption.TargetOwner.TaskID},
	} {
		if field.value == "" {
			return fmt.Errorf("adopt branch: %s is required", field.name)
		}
	}
	if adoption.SourceOwner.WorkspaceID != adoption.TargetOwner.WorkspaceID {
		return errors.New("adopt branch: source and continuation task workspaces differ")
	}
	return nil
}

func remoteBranchSHA(gitRoot, remote, branch string) (string, error) {
	out, err := runGitTrimmed(gitRoot, "ls-remote", "--exit-code", remote, "refs/heads/"+branch)
	if err != nil {
		return "", fmt.Errorf("adopt branch: resolve remote %s/%s: %w", remote, branch, err)
	}
	sha, _, ok := strings.Cut(out, "\t")
	if !ok || strings.TrimSpace(sha) == "" {
		return "", fmt.Errorf("adopt branch: remote %s/%s returned no object ID", remote, branch)
	}
	return strings.TrimSpace(sha), nil
}

func readAdoptionMarker(path string, owner BranchAdoptionOwner) ([]byte, taskContextMarkerFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, taskContextMarkerFile{}, fmt.Errorf("adopt branch: read continuation task marker: %w", err)
	}
	var marker taskContextMarkerFile
	if err := json.Unmarshal(data, &marker); err != nil {
		return nil, marker, errors.New("adopt branch: continuation task marker is not parseable")
	}
	if marker.ManagedBy != TaskContextMarkerManagedBy || marker.TaskID != owner.TaskID || marker.AgentID != owner.AgentID || marker.IssueID != owner.ConversationID {
		return nil, marker, errors.New("adopt branch: continuation task marker identity does not match the authorized task")
	}
	return data, marker, nil
}

func adoptedMarker(marker taskContextMarkerFile, branch, baseCommit string) ([]byte, error) {
	marker.Branch = branch
	marker.BaseCommit = baseCommit
	data, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("adopt branch: marshal continuation marker: %w", err)
	}
	return data, nil
}

func writeAdoptedBranchRecord(gitRoot, oldRecord, checkpoint string, continuation BranchAdoptionOwner, sourceTaskID string) (string, error) {
	owner := adoptionRecordOwner(continuation)
	message := branchRecordMessage(owner) + "\nmultica: authorized continuation branch adoption\n\n"
	message += fmt.Sprintf("Source task: %s\n", sourceTaskID)
	message += fmt.Sprintf("Continuation task: %s\n", continuation.TaskID)
	args := append(commitIdentityArgs(gitRoot), "commit-tree", oldRecord+"^{tree}", "-p", oldRecord, "-p", checkpoint, "-m", message)
	record, err := runGitTrimmed(gitRoot, args...)
	if err != nil {
		return "", fmt.Errorf("adopt branch: write continuation branch record: %w", err)
	}
	return record, nil
}

func updateAdoptedBranchRecord(gitRoot, branch, oldRecord, newRecord string) error {
	input := "start\n" +
		fmt.Sprintf("update %s %s %s\n", userStateRef(branch), newRecord, oldRecord) +
		"prepare\ncommit\n"
	if out, err := runGitInput(gitRoot, input, "update-ref", "--stdin"); err != nil {
		return fmt.Errorf("adopt branch: atomically bind branch record: %s: %w", strings.TrimSpace(out), err)
	}
	return nil
}

func sameAdoptionPath(left, right string) bool {
	leftAbs, leftErr := filepath.EvalSymlinks(left)
	rightAbs, rightErr := filepath.EvalSymlinks(right)
	return leftErr == nil && rightErr == nil && leftAbs == rightAbs
}
