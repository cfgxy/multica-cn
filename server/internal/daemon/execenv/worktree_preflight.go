package execenv

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// WorktreePreflight is the read-only delivery guard report exposed by the CLI.
type WorktreePreflight struct {
	Status         string                `json:"status"`
	TaskID         string                `json:"task_id,omitempty"`
	GitRoot        string                `json:"git_root,omitempty"` // git root the worktree checks ran against
	ExpectedBranch string                `json:"expected_branch,omitempty"`
	CurrentBranch  string                `json:"current_branch,omitempty"`
	Head           string                `json:"head,omitempty"`
	BranchTip      string                `json:"managed_branch_tip,omitempty"`
	BaseCommit     string                `json:"base_commit,omitempty"`
	BaseIsAncestor bool                  `json:"base_is_ancestor"`
	WorktreeStatus []string              `json:"worktree_status"`
	Warnings       []string              `json:"warnings"`
	Errors         []string              `json:"errors"`
	Suggestions    []PreflightSuggestion `json:"suggestions"`
}

// PreflightSuggestion identifies a delivery failure class and its manual,
// non-mutating recovery action.
type PreflightSuggestion struct {
	Kind   string `json:"kind"`
	Action string `json:"action"`
}

type preflightTaskMarker struct {
	ManagedBy  string `json:"managed_by"`
	TaskID     string `json:"task_id"`
	Branch     string `json:"branch"`
	BaseCommit string `json:"base_commit"`
	// PreparedReanchor belongs exclusively to the explicit `worktree reanchor`
	// transaction. Ordinary delivery diagnostics must never turn it into a gate.
	PreparedReanchor json.RawMessage `json:"prepared_reanchor"`
}

// PreflightWorktree inspects the current managed task worktree and never changes
// refs, the index, or files. Its delivery checks are the same checks Finalize
// applies before recording a delivered branch.
func PreflightWorktree(cwd string) (WorktreePreflight, error) {
	result := WorktreePreflight{Status: "fail", WorktreeStatus: []string{}, Warnings: []string{}, Errors: []string{}, Suggestions: []PreflightSuggestion{}}
	marker, markerErr := findPreflightTaskMarker(cwd)
	trustedMarker := false
	if markerErr != nil {
		result.Warnings = append(result.Warnings, fmt.Sprintf("managed task metadata is unavailable: %v; inspect `git status --short --branch` and continue once the Git checks below are clean", markerErr))
	} else {
		result.TaskID = marker.TaskID
		result.ExpectedBranch = marker.Branch
		result.BaseCommit = marker.BaseCommit
		markerComplete := marker.TaskID != "" && marker.Branch != "" && marker.BaseCommit != ""
		if !markerComplete {
			result.Warnings = append(result.Warnings, "managed task metadata is incomplete (task_id, branch, or base_commit is missing); preflight checked the resolvable Git state only")
		}
		if len(marker.PreparedReanchor) > 0 {
			result.Warnings = append(result.Warnings, "an explicit worktree reanchor transaction is prepared; it is not part of ordinary preflight or delivery, so complete or inspect it only if that recovery was intentionally requested")
		}
		currentTaskID := strings.TrimSpace(os.Getenv("MULTICA_TASK_ID"))
		if currentTaskID != "" && currentTaskID != marker.TaskID {
			result.Warnings = append(result.Warnings, fmt.Sprintf("managed task marker belongs to task %s, but MULTICA_TASK_ID is %s; Git delivery checks remain authoritative", marker.TaskID, currentTaskID))
		} else if markerComplete {
			trustedMarker = true
		}
	}

	gitRoot, err := runGitTrimmed(cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		err = fmt.Errorf("resolve worktree git root: %w", err)
		result.addFailure("git_root", err.Error(), "Run preflight from the repository worktree or one of its descendants, then retry.")
		return result, errors.New(strings.Join(result.Errors, "; "))
	}
	result.GitRoot = gitRoot
	if branch, branchErr := runGitTrimmed(gitRoot, "symbolic-ref", "--quiet", "--short", "HEAD"); branchErr == nil {
		result.CurrentBranch = branch
	} else {
		result.addFailure("detached_head", fmt.Sprintf("resolve current branch: %v", branchErr), result.switchBranchAction())
	}
	if head, headErr := runGitTrimmed(gitRoot, "rev-parse", "--verify", "HEAD"); headErr == nil {
		result.Head = head
	} else {
		result.addFailure("head_unresolved", fmt.Sprintf("resolve HEAD: %v", headErr), "Restore or check out a commit on the intended delivery branch, then retry preflight.")
	}
	if trustedMarker {
		if tip, tipErr := runGitTrimmed(gitRoot, "rev-parse", "--verify", "refs/heads/"+marker.Branch); tipErr == nil {
			result.BranchTip = tip
		} else {
			result.addFailure("managed_branch_unresolved", fmt.Sprintf("resolve managed branch %s: %v", marker.Branch, tipErr), fmt.Sprintf("Restore or fetch the expected local branch %q, then retry preflight.", marker.Branch))
		}
	} else if marker.Branch != "" {
		result.Warnings = append(result.Warnings, "managed branch metadata is not trusted for this task; preflight will not compare HEAD with the recorded branch")
	}
	if trustedMarker && result.Head != "" {
		if _, ancestorErr := runGit(gitRoot, "merge-base", "--is-ancestor", marker.BaseCommit, result.Head); ancestorErr == nil {
			result.BaseIsAncestor = true
		} else {
			result.addFailure("ancestor_break", fmt.Sprintf("base commit %s is not proven as an ancestor of HEAD %s", marker.BaseCommit, result.Head), fmt.Sprintf("Merge or rebase the delivery branch onto history containing base commit %s, then retry preflight.", marker.BaseCommit))
		}
	} else if marker.BaseCommit != "" && !trustedMarker {
		result.Warnings = append(result.Warnings, "delivery baseline metadata is not trusted for this task; preflight will not check its ancestry")
	}
	if status, statusErr := runPreflightGit(gitRoot, "status", "--short", "--branch", "--untracked-files=all"); statusErr == nil {
		result.WorktreeStatus = splitStatusLines(status)
	} else {
		result.addFailure("worktree_status", fmt.Sprintf("read worktree status: %v", statusErr), "Restore Git worktree access and inspect `git status --short --branch`, then retry preflight.")
	}
	if unmerged, unmergedErr := runPreflightGit(gitRoot, "ls-files", "--unmerged"); unmergedErr != nil {
		result.addFailure("unmerged_paths_unreadable", fmt.Sprintf("inspect unresolved merge paths: %v", unmergedErr), "Restore Git index access, then retry preflight.")
	} else if strings.TrimSpace(unmerged) != "" {
		result.addFailure("unresolved_merge", fmt.Sprintf("unresolved merge paths: %s", strings.TrimSpace(unmerged)), "Resolve every conflict and stage it with `git add`, or abandon the current merge with `git merge --abort`, then retry preflight.")
	}

	if trustedMarker && result.CurrentBranch != marker.Branch {
		result.addFailure("branch_mismatch", fmt.Sprintf("current branch %q does not match expected branch %q", result.CurrentBranch, marker.Branch), result.switchBranchAction())
	}
	if trustedMarker && result.BranchTip != "" && result.Head != "" && result.BranchTip != result.Head {
		result.addFailure("managed_branch_tip_mismatch", fmt.Sprintf("managed branch %s points at %s while HEAD is %s", marker.Branch, shortID(result.BranchTip), shortID(result.Head)), result.switchBranchAction())
	}
	if len(result.Errors) == 0 {
		result.Status = "pass"
		return result, nil
	}
	return result, errors.New(strings.Join(result.Errors, "; "))
}

func (r *WorktreePreflight) addFailure(kind, message, action string) {
	r.Errors = append(r.Errors, message)
	r.Suggestions = append(r.Suggestions, PreflightSuggestion{Kind: kind, Action: action})
}

func (r WorktreePreflight) switchBranchAction() string {
	if r.ExpectedBranch != "" {
		return fmt.Sprintf("Switch to the expected delivery branch with `git switch %s`, then retry preflight.", r.ExpectedBranch)
	}
	return "Switch HEAD to the intended delivery branch, then retry preflight."
}

func runPreflightGit(dir string, args ...string) (string, error) {
	return runGitEnv(dir, []string{"GIT_OPTIONAL_LOCKS=0"}, args...)
}

func findPreflightTaskMarker(start string) (preflightTaskMarker, error) {
	if strings.TrimSpace(start) == "" {
		return preflightTaskMarker{}, errors.New("current directory is required")
	}
	dir, err := filepath.Abs(start)
	if err != nil {
		return preflightTaskMarker{}, fmt.Errorf("resolve current directory: %w", err)
	}
	for {
		path := filepath.Join(dir, TaskContextMarkerRelPath)
		data, readErr := os.ReadFile(path)
		if readErr == nil {
			var marker preflightTaskMarker
			if err := json.Unmarshal(data, &marker); err != nil {
				return preflightTaskMarker{}, fmt.Errorf("read managed task marker %s: %w", path, err)
			}
			if marker.ManagedBy != TaskContextMarkerManagedBy {
				return preflightTaskMarker{}, fmt.Errorf("task marker %s is not managed by Multica", path)
			}
			return marker, nil
		}
		if !os.IsNotExist(readErr) {
			return preflightTaskMarker{}, fmt.Errorf("read managed task marker %s: %w", path, readErr)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return preflightTaskMarker{}, errors.New("no Multica managed task context found; run this command inside a managed worktree")
}

func splitStatusLines(status string) []string {
	trimmed := strings.TrimSuffix(status, "\n")
	if trimmed == "" {
		return []string{}
	}
	return strings.Split(trimmed, "\n")
}
