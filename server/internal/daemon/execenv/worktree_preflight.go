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
	Status         string   `json:"status"`
	TaskID         string   `json:"task_id,omitempty"`
	ExpectedBranch string   `json:"expected_branch,omitempty"`
	CurrentBranch  string   `json:"current_branch,omitempty"`
	Head           string   `json:"head,omitempty"`
	BranchTip      string   `json:"managed_branch_tip,omitempty"`
	BaseCommit     string   `json:"base_commit,omitempty"`
	BaseIsAncestor bool     `json:"base_is_ancestor"`
	WorktreeStatus []string `json:"worktree_status"`
	Errors         []string `json:"errors"`
}

type preflightTaskMarker struct {
	ManagedBy  string `json:"managed_by"`
	TaskID     string `json:"task_id"`
	Branch     string `json:"branch"`
	BaseCommit string `json:"base_commit"`
}

// PreflightWorktree inspects the current managed task worktree and never changes
// refs, the index, or files. Its delivery checks are the same checks Finalize
// applies before recording a delivered branch.
func PreflightWorktree(cwd string) (WorktreePreflight, error) {
	result := WorktreePreflight{Status: "fail", WorktreeStatus: []string{}, Errors: []string{}}
	marker, err := findPreflightTaskMarker(cwd)
	if err != nil {
		result.Errors = append(result.Errors, err.Error())
	} else {
		result.TaskID = marker.TaskID
		result.ExpectedBranch = marker.Branch
		result.BaseCommit = marker.BaseCommit
	}
	if err == nil && (marker.TaskID == "" || marker.Branch == "" || marker.BaseCommit == "") {
		result.Errors = append(result.Errors, "managed task marker is missing task_id, branch, or base_commit; delivery relationship cannot be proven")
	}
	if err == nil {
		if currentTaskID := strings.TrimSpace(os.Getenv("MULTICA_TASK_ID")); currentTaskID != "" && currentTaskID != marker.TaskID {
			result.Errors = append(result.Errors, fmt.Sprintf("managed task marker belongs to task %s, but MULTICA_TASK_ID is %s", marker.TaskID, currentTaskID))
		}
	}

	gitRoot, err := runGitTrimmed(cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		err = fmt.Errorf("resolve worktree git root: %w", err)
		result.Errors = append(result.Errors, err.Error())
		return result, errors.New(strings.Join(result.Errors, "; "))
	}
	if branch, branchErr := runGitTrimmed(gitRoot, "symbolic-ref", "--quiet", "--short", "HEAD"); branchErr == nil {
		result.CurrentBranch = branch
	} else {
		result.Errors = append(result.Errors, fmt.Sprintf("resolve current branch: %v", branchErr))
	}
	if head, headErr := runGitTrimmed(gitRoot, "rev-parse", "--verify", "HEAD"); headErr == nil {
		result.Head = head
	} else {
		result.Errors = append(result.Errors, fmt.Sprintf("resolve HEAD: %v", headErr))
	}
	if marker.Branch != "" {
		if tip, tipErr := runGitTrimmed(gitRoot, "rev-parse", "--verify", "refs/heads/"+marker.Branch); tipErr == nil {
			result.BranchTip = tip
		} else {
			result.Errors = append(result.Errors, fmt.Sprintf("resolve managed branch %s: %v", marker.Branch, tipErr))
		}
	}
	if marker.BaseCommit != "" && result.Head != "" {
		if _, ancestorErr := runGit(gitRoot, "merge-base", "--is-ancestor", marker.BaseCommit, result.Head); ancestorErr == nil {
			result.BaseIsAncestor = true
		} else {
			result.Errors = append(result.Errors, fmt.Sprintf("base commit %s is not proven as an ancestor of HEAD %s", marker.BaseCommit, result.Head))
		}
	}
	if status, statusErr := runPreflightGit(gitRoot, "status", "--short", "--branch", "--untracked-files=all"); statusErr == nil {
		result.WorktreeStatus = splitStatusLines(status)
	} else {
		result.Errors = append(result.Errors, fmt.Sprintf("read worktree status: %v", statusErr))
	}

	if marker.Branch != "" && result.CurrentBranch != marker.Branch {
		result.Errors = append(result.Errors, fmt.Sprintf("current branch %q does not match expected branch %q", result.CurrentBranch, marker.Branch))
	}
	if marker.Branch != "" && marker.BaseCommit != "" {
		if guardErr := verifyDeliveryPoint(gitRoot, marker.Branch, marker.BaseCommit, result.Head); guardErr != nil {
			result.Errors = append(result.Errors, guardErr.Error())
		}
	}
	if len(result.Errors) == 0 {
		result.Status = "pass"
		return result, nil
	}
	return result, errors.New(strings.Join(result.Errors, "; "))
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
