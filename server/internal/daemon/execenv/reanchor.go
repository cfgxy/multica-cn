package execenv

import (
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
//     refs/multica/reanchor/ before anything moves (fixed ref pair: a second
//     re-anchor archives the intermediate state, and the JSONL audit line is
//     the full chain log);
//   - the branch move is a guarded update-ref (old tip is the expected value,
//     so a concurrent move aborts instead of clobbering);
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
	var result ReanchorResult
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return result, errors.New("reanchor: branch is required")
	}
	if out, err := runGitTrimmed(gitRoot, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch); err != nil || out == "" {
		return result, fmt.Errorf("reanchor: branch %s does not exist: %w", branch, err)
	}
	if out, err := runGitTrimmed(gitRoot, "rev-parse", "--verify", "--quiet", target); err != nil || out == "" {
		return result, fmt.Errorf("reanchor: target commit %s does not exist: %w", target, err)
	}
	if out, err := runGitTrimmed(gitRoot, "rev-parse", "--verify", "--quiet", expectedBase); err != nil || out == "" {
		return result, fmt.Errorf("reanchor: expected base %s does not exist: %w", expectedBase, err)
	}
	// The target must stand directly on the verified mainline tip: re-anchoring
	// onto an intermediate chain would just bind the next task to another
	// stale base. The parent check and the ancestor check are the two proofs
	// that matter.
	parent, err := runGitTrimmed(gitRoot, "rev-parse", "--verify", "--quiet", target+"^")
	if err != nil || parent == "" {
		return result, fmt.Errorf("reanchor: target %s has no parent; it is not a single content commit on a mainline base", shortID(target))
	}
	if parent != expectedBase {
		return result, fmt.Errorf("reanchor: target %s sits on %s, not the verified base %s", shortID(target), shortID(parent), shortID(expectedBase))
	}
	if _, err := runGit(gitRoot, "merge-base", "--is-ancestor", expectedBase, target); err != nil {
		return result, fmt.Errorf("reanchor: base %s is not an ancestor of target %s: %w", shortID(expectedBase), shortID(target), err)
	}

	// Fail closed while the delivery guard holds this branch: a refusal marker
	// means a previous run was refused and preserved; that handshake has its
	// own heal path and an operator must not rewrite it underneath.
	if refusal := runGitSafeTrimmed(gitRoot, "rev-parse", "--verify", "--quiet", guardRefusalRef(branch)); refusal != "" {
		return result, fmt.Errorf("reanchor: branch %s has a pending delivery-guard refusal at %s; resolve it via the guard's heal path first", branch, shortID(refusal))
	}

	oldTip, err := runGitTrimmed(gitRoot, "rev-parse", "--verify", "refs/heads/"+branch)
	if err != nil {
		return result, fmt.Errorf("reanchor: resolve branch tip: %w", err)
	}
	if oldTip == target {
		return result, fmt.Errorf("reanchor: branch %s already stands at %s; nothing to do", branch, shortID(target))
	}

	result.Branch, result.OldTip, result.NewTip, result.ExpectedBase = branch, oldTip, target, expectedBase

	// 1) Archive the displaced state before anything moves.
	if out, err := runGit(gitRoot, "update-ref", reanchorRef(branch), oldTip); err != nil {
		return result, fmt.Errorf("reanchor: archive old tip: %s: %w", strings.TrimSpace(out), err)
	}
	result.ArchiveRefs = append(result.ArchiveRefs, reanchorRef(branch))

	oldRecord := runGitSafeTrimmed(gitRoot, "rev-parse", "--verify", "--quiet", userStateRef(branch))
	if oldRecord != "" {
		result.OldRecord = oldRecord
		if out, err := runGit(gitRoot, "update-ref", reanchorStateRef(branch), oldRecord); err != nil {
			return result, fmt.Errorf("reanchor: archive old record: %s: %w", strings.TrimSpace(out), err)
		}
		result.ArchiveRefs = append(result.ArchiveRefs, reanchorStateRef(branch))
	}

	// 2) Move the branch with the old tip as the expected value: if anything
	// else moved it between our read and this write, abort instead of clobber.
	if out, err := runGit(gitRoot, "update-ref", "refs/heads/"+branch, target, oldTip); err != nil {
		return result, fmt.Errorf("reanchor: move branch (old-value guard): %s: %w", strings.TrimSpace(out), err)
	}

	// 3) Rewrite the local-state record: same user-state snapshot (the old
	// record's first parent), same owner trailers, checkpoint re-pinned to the
	// new tip.
	if oldRecord == "" {
		// No record: the branch was not owned by any managed conversation, so
		// there is nothing to re-pin. Say so via RecordReanchored=false; the
		// next prepare forks a fresh branch, which is the safe shape.
		return result, writeReanchorAudit(gitRoot, &result, note)
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
	msg := branchRecordMessage(rec.owner) + "\n" + reanchorRecordNote(branch, note, oldTip, target, expectedBase)
	args := append(commitIdentityArgs(gitRoot), "commit-tree", userState+"^{tree}",
		"-p", userState, "-p", target, "-m", msg)
	newRecord, err := runGitTrimmed(gitRoot, args...)
	if err != nil {
		return result, fmt.Errorf("reanchor: git commit-tree: %w", err)
	}
	if out, err := runGit(gitRoot, "update-ref", userStateRef(branch), newRecord, oldRecord); err != nil {
		return result, fmt.Errorf("reanchor: update branch record (old-value guard): %s: %w", strings.TrimSpace(out), err)
	}
	result.NewRecord, result.RecordReanchored = newRecord, true
	return result, writeReanchorAudit(gitRoot, &result, note)
}

func reanchorRef(branch string) string      { return "refs/multica/reanchor/" + branch }
func reanchorStateRef(branch string) string { return "refs/multica/reanchor/local-state/" + branch }

func reanchorRecordNote(branch string, note ReanchorNote, oldTip, target, expectedBase string) string {
	var b strings.Builder
	b.WriteString("multica: re-anchored task branch record\n\n")
	fmt.Fprintf(&b, "Old chain tip %s archived at %s; checkpoint re-pinned to %s, a single content commit on verified base %s.\n", shortID(oldTip), reanchorRef(branch), shortID(target), shortID(expectedBase))
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
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write(append(buf, '\n')); err != nil {
		return err
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
