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
//   - a pending delivery-guard refusal marker refuses the operation: the
//     marker is the guard's in-flight handshake and belongs to its own heal
//     path (RUYI-479), not to an operator rewrite.
//
// Preconditions: the target must exist, must be exactly one commit ahead of
// expectedBase (the verified content commit), and expectedBase must be the
// caller's verified shared-mainline tip. A branch with no local-state record
// is still re-anchored (its tip moves and the audit lands); the record step
// is a no-op reported via RecordReanchored=false.
func ReanchorBranch(gitRoot, branch, target, expectedBase string, note ReanchorNote) (ReanchorResult, error) {
	return reanchorBranch(gitRoot, branch, target, expectedBase, note, func() {})
}

func reanchorBranch(gitRoot, branch, target, expectedBase string, note ReanchorNote, afterLinkedWorktreeCheck func()) (ReanchorResult, error) {
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

	oldTip, err := runGitTrimmed(gitRoot, "rev-parse", "--verify", "refs/heads/"+branch)
	if err != nil {
		return result, fmt.Errorf("reanchor: resolve branch tip: %w", err)
	}
	if oldTip == resolvedTarget {
		return result, fmt.Errorf("reanchor: branch %s already stands at %s; nothing to do", branch, shortID(resolvedTarget))
	}

	oldRecord := runGitSafeTrimmed(gitRoot, "rev-parse", "--verify", "--quiet", userStateRef(branch))
	var newRecord string
	if oldRecord != "" {
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
		newRecord, err = runGitTrimmed(gitRoot, args...)
		if err != nil {
			return result, fmt.Errorf("reanchor: git commit-tree: %w", err)
		}
	}

	archiveTipRef, archiveStateRef, err := newReanchorArchiveRefs(branch)
	if err != nil {
		return result, err
	}
	if err := updateReanchorRefs(gitRoot, branch, resolvedTarget, oldTip, oldRecord, newRecord, archiveTipRef, archiveStateRef); err != nil {
		return result, err
	}

	result.Branch, result.OldTip, result.NewTip, result.ExpectedBase = branch, oldTip, resolvedTarget, resolvedExpectedBase
	result.ArchiveRefs = append(result.ArchiveRefs, archiveTipRef)
	if oldRecord != "" {
		result.OldRecord, result.NewRecord, result.RecordReanchored = oldRecord, newRecord, true
		result.ArchiveRefs = append(result.ArchiveRefs, archiveStateRef)
	}
	return result, writeReanchorAudit(gitRoot, &result, note)
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
