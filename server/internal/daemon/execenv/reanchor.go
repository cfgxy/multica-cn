package execenv

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ReanchorNote records who re-anchored a managed branch and why; it rides the
// rewritten branch record and the audit line so the move is traceable later.
type ReanchorNote struct {
	OperatorAgent string
	OperatorTask  string
	Issue         string
	Reason        string
}

// ReanchorResult is the auditable outcome of ReanchorBranch.
type ReanchorResult struct {
	Branch           string   `json:"branch"`
	OldTip           string   `json:"old_tip"`
	NewTip           string   `json:"new_tip"`
	ExpectedBase     string   `json:"expected_base"`
	OldRecord        string   `json:"old_record,omitempty"`
	NewRecord        string   `json:"new_record,omitempty"`
	RecordReanchored bool     `json:"record_reanchored"`
	ArchiveRefs      []string `json:"archive_refs"`
	AuditPath        string   `json:"audit_path,omitempty"`
	AuditLine        string   `json:"audit_line,omitempty"`
}

// ReanchorBranch moves a managed task/conversation branch from a stale local
// chain back onto a verified content commit that sits directly on the shared
// mainline tip, so the next managed task starts from the mainline base
// instead of continuing the stale chain (RUYI-780 infra fix).
//
// It is auditable and reversible:
//
//   - the old branch tip and the old local-state record are archived under
//     unique refs/multica/reanchor/ paths before anything moves;
//   - the archive, branch move and record rewrite share one guarded
//     update-ref transaction, so a concurrent change aborts without exposing a
//     branch and record that disagree;
//   - the branch record (refs/multica/local-state/<branch>) is rewritten with
//     the SAME user-state snapshot and owner trailers, checkpoint re-pinned to
//     the new tip. Without the record rewrite the next prepare would fail the
//     checkpoint-ancestor proof and fork the conversation onto a fresh branch
//     name - the re-anchor would silently not stick;
//   - the task marker is re-pinned to the new tip before the Git ref
//     transaction. If that transaction fails, the marker deliberately remains
//     at the requested target: a retry recognizes that prepared state and
//     resumes the guarded ref transaction. Preflight fails closed while the
//     branch and marker disagree, rather than relying on a second filesystem
//     write whose failure could make the operation permanently unrecoverable;
//   - a pending delivery-guard refusal marker refuses the operation: the
//     marker is the guard's in-flight handshake and belongs to its own heal
//     path (RUYI-479), not to an operator rewrite.
//
// Preconditions: the target must exist, must be exactly one commit ahead of
// expectedBase (the verified content commit), and expectedBase must be the
// caller's verified shared-mainline tip. An empty markerPath resolves to the
// task marker at the Git worktree root. A missing, foreign, or unrelated marker
// refuses the operation rather than creating an unprovable delivery relationship.
func ReanchorBranch(gitRoot, branch, target, expectedBase, markerPath string, note ReanchorNote) (ReanchorResult, error) {
	return reanchorBranch(gitRoot, branch, target, expectedBase, markerPath, note, func() {})
}

func reanchorBranch(gitRoot, branch, target, expectedBase, markerPath string, note ReanchorNote, afterLinkedWorktreeCheck func()) (ReanchorResult, error) {
	return reanchorBranchWithOperations(gitRoot, branch, target, expectedBase, markerPath, note, afterLinkedWorktreeCheck, writeMarkerFileAtomic, updateReanchorRefs)
}

func reanchorBranchWithOperations(gitRoot, branch, target, expectedBase, markerPath string, note ReanchorNote, afterLinkedWorktreeCheck func(), writeMarker func(string, []byte) error, updateRefs func(string, string, string, string, string, string, string, string) error) (ReanchorResult, error) {
	var result ReanchorResult
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return result, errors.New("reanchor: branch is required")
	}
	if out, err := runGitTrimmed(gitRoot, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch); err != nil || out == "" {
		return result, fmt.Errorf("reanchor: branch %s does not exist: %w", branch, err)
	}
	resolvedTarget, err := runGitTrimmed(gitRoot, "rev-parse", "--verify", "--quiet", target)
	if err != nil || resolvedTarget == "" {
		return result, fmt.Errorf("reanchor: target commit %s does not exist: %w", target, err)
	}
	resolvedExpectedBase, err := runGitTrimmed(gitRoot, "rev-parse", "--verify", "--quiet", expectedBase)
	if err != nil || resolvedExpectedBase == "" {
		return result, fmt.Errorf("reanchor: expected base %s does not exist: %w", expectedBase, err)
	}
	// The target must stand directly on the verified mainline tip: re-anchoring
	// onto an intermediate chain would just bind the next task to another
	// stale base. The parent check and the ancestor check are the two proofs
	// that matter.
	parent, err := runGitTrimmed(gitRoot, "rev-parse", "--verify", "--quiet", resolvedTarget+"^")
	if err != nil || parent == "" {
		return result, fmt.Errorf("reanchor: target %s has no parent; it is not a single content commit on a mainline base", shortID(resolvedTarget))
	}
	if parent != resolvedExpectedBase {
		return result, fmt.Errorf("reanchor: target %s sits on %s, not the verified base %s", shortID(resolvedTarget), shortID(parent), shortID(resolvedExpectedBase))
	}
	if _, err := runGit(gitRoot, "merge-base", "--is-ancestor", resolvedExpectedBase, resolvedTarget); err != nil {
		return result, fmt.Errorf("reanchor: base %s is not an ancestor of target %s: %w", shortID(resolvedExpectedBase), shortID(resolvedTarget), err)
	}

	// Worktree creation and ref updates share the repository-wide lock with the
	// managed worktree path. Holding it from the inspection through update-ref
	// prevents a linked worktree from appearing after the check but before the
	// branch transaction commits.
	unlock, err := lockGitRoot(gitRoot, nil)
	if err != nil {
		return result, fmt.Errorf("reanchor: lock repository: %w", err)
	}
	defer unlock()

	// Fail closed while the delivery guard holds this branch: a refusal marker
	// means a previous run was refused and preserved; that handshake has its
	// own heal path and an operator must not rewrite it underneath.
	if refusal := runGitSafeTrimmed(gitRoot, "rev-parse", "--verify", "--quiet", guardRefusalRef(branch)); refusal != "" {
		return result, fmt.Errorf("reanchor: branch %s has a pending delivery-guard refusal at %s; resolve it via the guard's heal path first", branch, shortID(refusal))
	}
	if path, checkedOut, err := branchCheckoutPath(gitRoot, branch); err != nil {
		return result, fmt.Errorf("reanchor: inspect linked worktrees: %w", err)
	} else if checkedOut {
		return result, fmt.Errorf("reanchor: branch %s is checked out at %s; refusing to change its ref behind a live worktree", branch, path)
	}
	afterLinkedWorktreeCheck()

	// A ref transaction alone does not participate in Git's linked-worktree
	// branch exclusion. Reserve the branch with a no-checkout worktree before
	// moving it: ordinary `git worktree add <path> <branch>` then fails until
	// the transaction finishes, while a contender that won the first check
	// makes the reservation itself fail without changing the branch.
	releaseReservation, err := reserveReanchorBranch(gitRoot, branch)
	if err != nil {
		return result, err
	}
	defer releaseReservation()

	oldTip, err := runGitTrimmed(gitRoot, "rev-parse", "--verify", "refs/heads/"+branch)
	if err != nil {
		return result, fmt.Errorf("reanchor: resolve branch tip: %w", err)
	}
	if oldTip == resolvedTarget {
		return result, fmt.Errorf("reanchor: branch %s already stands at %s; nothing to do", branch, shortID(resolvedTarget))
	}

	oldRecord := runGitSafeTrimmed(gitRoot, "rev-parse", "--verify", "--quiet", userStateRef(branch))
	if oldRecord == "" {
		return result, fmt.Errorf("reanchor: branch %s has no Multica branch record", branch)
	}
	userState, err := runGitTrimmed(gitRoot, "rev-parse", "--verify", "--quiet", oldRecord+"^1")
	if err != nil || userState == "" {
		return result, fmt.Errorf("reanchor: branch record %s has no user-state parent; refusing to guess one", shortID(oldRecord))
	}
	rec, err := readBranchRecord(gitRoot, oldRecord)
	if err != nil {
		return result, fmt.Errorf("reanchor: read branch record: %w", err)
	}
	if rec.checkpoint == "" {
		return result, fmt.Errorf("reanchor: branch record %s carries no checkpoint; refusing to rewrite it", shortID(oldRecord))
	}
	msg := branchRecordMessage(rec.owner) + "\n" + reanchorRecordNote(branch, note, oldTip, resolvedTarget, resolvedExpectedBase)
	args := append(commitIdentityArgs(gitRoot), "commit-tree", userState+"^{tree}",
		"-p", userState, "-p", resolvedTarget, "-m", msg)
	newRecord, err := runGitTrimmed(gitRoot, args...)
	if err != nil {
		return result, fmt.Errorf("reanchor: git commit-tree: %w", err)
	}

	markerPath, markerAfter, writeMarkerNow, err := reanchoredTaskMarker(gitRoot, markerPath, branch, oldTip, resolvedTarget)
	if err != nil {
		return result, err
	}

	archiveTipRef, archiveStateRef, err := newReanchorArchiveRefs(branch)
	if err != nil {
		return result, err
	}
	// The filesystem and Git's ref store cannot share one transaction. A marker
	// already pinned to target is a durable prepared state left by an earlier
	// rejected ref transaction, so it needs no second filesystem write before we
	// retry that transaction.
	if writeMarkerNow {
		if err := writeMarker(markerPath, markerAfter); err != nil {
			return result, fmt.Errorf("reanchor: update task marker: %w", err)
		}
	}
	if err := updateRefs(gitRoot, branch, resolvedTarget, oldTip, oldRecord, newRecord, archiveTipRef, archiveStateRef); err != nil {
		if writeMarkerNow {
			return result, fmt.Errorf("reanchor: move branch/record: %w; task marker remains prepared at %s while branch %s stays at %s; rerun this reanchor to resume safely", err, shortID(resolvedTarget), branch, shortID(oldTip))
		}
		return result, fmt.Errorf("reanchor: resume branch/record move: %w; task marker remains prepared at %s and this reanchor can be retried", err, shortID(resolvedTarget))
	}

	result.Branch, result.OldTip, result.NewTip, result.ExpectedBase = branch, oldTip, resolvedTarget, resolvedExpectedBase
	result.ArchiveRefs = append(result.ArchiveRefs, archiveTipRef)
	result.OldRecord, result.NewRecord, result.RecordReanchored = oldRecord, newRecord, true
	result.ArchiveRefs = append(result.ArchiveRefs, archiveStateRef)
	return result, writeReanchorAudit(gitRoot, &result, note)
}

func reanchoredTaskMarker(gitRoot, markerPath, branch, oldTip, target string) (string, []byte, bool, error) {
	markerPath = strings.TrimSpace(markerPath)
	if markerPath == "" {
		root, err := runGitTrimmed(gitRoot, "rev-parse", "--show-toplevel")
		if err != nil {
			return "", nil, false, fmt.Errorf("reanchor: resolve git root for task marker: %w", err)
		}
		markerPath = filepath.Join(root, TaskContextMarkerRelPath)
	}
	before, err := os.ReadFile(markerPath)
	if err != nil {
		return "", nil, false, fmt.Errorf("reanchor: read task marker %s: %w", markerPath, err)
	}
	var marker taskContextMarkerFile
	if err := json.Unmarshal(before, &marker); err != nil {
		return "", nil, false, fmt.Errorf("reanchor: task marker %s is not parseable", markerPath)
	}
	if marker.ManagedBy != TaskContextMarkerManagedBy {
		return "", nil, false, fmt.Errorf("reanchor: task marker %s is not managed by Multica", markerPath)
	}
	if marker.TaskID == "" {
		return "", nil, false, fmt.Errorf("reanchor: task marker %s has no task_id", markerPath)
	}
	if marker.Branch != branch {
		return "", nil, false, fmt.Errorf("reanchor: task marker branch %q does not match %q", marker.Branch, branch)
	}
	if marker.BaseCommit == "" {
		return "", nil, false, fmt.Errorf("reanchor: task marker %s has no base_commit", markerPath)
	}
	if marker.BaseCommit == target {
		// The initial marker write succeeded but the ref transaction did not.
		// Its exact target is the durable recovery proof; retry the guarded ref
		// transaction without another marker write that could fail independently.
		return markerPath, before, false, nil
	}
	if _, err := runGit(gitRoot, "merge-base", "--is-ancestor", marker.BaseCommit, oldTip); err != nil {
		return "", nil, false, fmt.Errorf("reanchor: task marker base %s is not an ancestor of branch %s at %s: %w", shortID(marker.BaseCommit), branch, shortID(oldTip), err)
	}
	marker.BaseCommit = target
	after, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return "", nil, false, fmt.Errorf("reanchor: marshal task marker: %w", err)
	}
	return markerPath, after, true, nil
}

func branchCheckoutPath(gitRoot, branch string) (string, bool, error) {
	out, err := runGitStdout(gitRoot, "worktree", "list", "--porcelain")
	if err != nil {
		return "", false, err
	}
	var path string
	want := "branch refs/heads/" + branch
	for _, line := range strings.Split(out, "\n") {
		if after, ok := strings.CutPrefix(line, "worktree "); ok {
			path = after
			continue
		}
		if line == want {
			return path, true, nil
		}
	}
	return "", false, nil
}

func reserveReanchorBranch(gitRoot, branch string) (func(), error) {
	parent, err := os.MkdirTemp("", "multica-reanchor-")
	if err != nil {
		return nil, fmt.Errorf("reanchor: create branch reservation directory: %w", err)
	}
	reservationPath := filepath.Join(parent, "checkout")
	if _, err := runGit(gitRoot, "worktree", "add", "--no-checkout", reservationPath, branch); err != nil {
		_ = os.RemoveAll(parent)
		return nil, fmt.Errorf("reanchor: reserve branch %s against linked worktrees: %w", branch, err)
	}
	return func() {
		_, _ = runGit(gitRoot, "worktree", "remove", "--force", reservationPath)
		_ = os.RemoveAll(parent)
	}, nil
}

func newReanchorArchiveRefs(branch string) (string, string, error) {
	bytes := make([]byte, 12)
	if _, err := rand.Read(bytes); err != nil {
		return "", "", fmt.Errorf("reanchor: generate archive ref suffix: %w", err)
	}
	suffix := hex.EncodeToString(bytes)
	return "refs/multica/reanchor/" + branch + "-" + suffix,
		"refs/multica/reanchor/local-state/" + branch + "-" + suffix, nil
}

func updateReanchorRefs(gitRoot, branch, target, oldTip, oldRecord, newRecord, archiveTipRef, archiveStateRef string) error {
	var input strings.Builder
	input.WriteString("start\n")
	fmt.Fprintf(&input, "create %s %s\n", archiveTipRef, oldTip)
	if oldRecord != "" {
		fmt.Fprintf(&input, "create %s %s\n", archiveStateRef, oldRecord)
	}
	fmt.Fprintf(&input, "update refs/heads/%s %s %s\n", branch, target, oldTip)
	if oldRecord != "" {
		fmt.Fprintf(&input, "update %s %s %s\n", userStateRef(branch), newRecord, oldRecord)
	}
	input.WriteString("prepare\ncommit\n")
	if out, err := runGitInput(gitRoot, input.String(), "update-ref", "--stdin"); err != nil {
		return fmt.Errorf("reanchor: atomically archive and move branch/record: %s: %w", strings.TrimSpace(out), err)
	}
	return nil
}

func reanchorRecordNote(branch string, note ReanchorNote, oldTip, target, expectedBase string) string {
	var b strings.Builder
	b.WriteString("multica: re-anchored task branch record\n\n")
	fmt.Fprintf(&b, "Old chain tip %s was archived before the atomic re-anchor; checkpoint re-pinned to %s, a single content commit on verified base %s.\n", shortID(oldTip), shortID(target), shortID(expectedBase))
	if note.OperatorAgent != "" || note.OperatorTask != "" {
		fmt.Fprintf(&b, "Operator: agent=%s task=%s\n", note.OperatorAgent, note.OperatorTask)
	}
	if note.Issue != "" {
		fmt.Fprintf(&b, "Issue: %s\n", note.Issue)
	}
	if note.Reason != "" {
		fmt.Fprintf(&b, "Reason: %s\n", note.Reason)
	}
	return b.String()
}

// writeReanchorAudit appends one JSON line per re-anchor under the repository
// top's .multica/ directory (the same git-ignored local ledger style as
// ALLOCATIONS.md). Best-effort by design: the branch move and the record
// rewrite are already durable, and a missing or unwritable .multica must not
// undo them - the record commit carries the same fact.
type reanchorAuditFile interface {
	Write([]byte) (int, error)
	Close() error
}

var openReanchorAuditFile = func(path string) (reanchorAuditFile, error) {
	return os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
}

func writeReanchorAudit(gitRoot string, result *ReanchorResult, note ReanchorNote) error {
	top, err := runGitTrimmed(gitRoot, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil // bare or unusual root: the record commit is the audit of record
	}
	dir := filepath.Join(top, ".multica")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil
	}
	line := map[string]string{
		"branch":         result.Branch,
		"old_tip":        result.OldTip,
		"new_tip":        result.NewTip,
		"expected_base":  result.ExpectedBase,
		"old_record":     result.OldRecord,
		"new_record":     result.NewRecord,
		"operator_agent": note.OperatorAgent,
		"operator_task":  note.OperatorTask,
		"issue":          note.Issue,
		"reason":         note.Reason,
	}
	buf, err := json.Marshal(line)
	if err != nil {
		return nil
	}
	path := filepath.Join(dir, "reanchor-audit.jsonl")
	f, err := openReanchorAuditFile(path)
	if err != nil {
		return nil
	}
	lineWithNewline := append(buf, '\n')
	if n, err := f.Write(lineWithNewline); err != nil || n != len(lineWithNewline) {
		_ = f.Close()
		return nil
	}
	if err := f.Close(); err != nil {
		return nil
	}
	result.AuditPath = path
	result.AuditLine = string(buf)
	return nil
}

// runGitSafeTrimmed is like runGitTrimmed but returns "" instead of an error
// when the revision does not exist - probe-shaped calls (does this ref exist)
// where absence is an expected answer.
func runGitSafeTrimmed(dir string, args ...string) string {
	out, err := runGitTrimmed(dir, args...)
	if err != nil {
		return ""
	}
	return out
}
