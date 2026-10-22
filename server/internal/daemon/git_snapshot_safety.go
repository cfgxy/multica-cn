package daemon

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// gitSnapshotAudit describes the Git metadata boundary discovered before a
// snapshot worker is created. It is deliberately read-only: a failed audit is
// a reason to use the existing worktree backend, never a reason to repair a
// user's repository metadata.
type gitSnapshotAudit struct {
	HasGit bool
	Safe   bool
	Reason string
}

// auditSnapshotGitScope recursively discovers Git repositories in the declared
// snapshot units and proves that each repository's worktree, git dir, and
// common git dir are all covered by a unit. Git's own resolution is used for
// indirection (.git files, commondir, and symlinks) instead of parsing those
// implementation details by hand.
func auditSnapshotGitScope(plan *snapshotPlan) gitSnapshotAudit {
	if plan == nil {
		return gitSnapshotAudit{Safe: true}
	}
	units := plan.Units
	// Unit tests and recovery callers can construct the minimal plan shape
	// with only Primary. Treat that as one whole-root unit rather than
	// silently declaring the directory non-Git.
	if len(units) == 0 && plan.Primary != nil {
		units = []snapshotUnit{*plan.Primary}
	}

	covered := make([]string, 0, len(units))
	for _, unit := range units {
		real, err := resolveRealPath(unit.AbsPath)
		if err != nil {
			return gitSnapshotAudit{Reason: fmt.Sprintf("cannot resolve snapshot scope %q: %v", unit.AbsPath, err)}
		}
		covered = append(covered, real)
	}

	repositories := make(map[string]struct{})
	for _, unit := range units {
		if err := filepath.WalkDir(unit.AbsPath, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.Name() != ".git" {
				return nil
			}
			repositories[filepath.Dir(path)] = struct{}{}
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}); err != nil {
			return gitSnapshotAudit{Reason: fmt.Sprintf("cannot enumerate Git metadata below %q: %v", unit.AbsPath, err)}
		}
	}

	// A unit may be a subdirectory of a repository, so its .git is outside the
	// walk. Resolve that shape too; scope containment below rejects it safely.
	for _, unit := range units {
		if top, ok := gitToplevel(unit.AbsPath); ok {
			repositories[top] = struct{}{}
		}
	}
	if len(repositories) == 0 {
		return gitSnapshotAudit{Safe: true}
	}

	for repo := range repositories {
		if _, err := os.Stat(filepath.Join(repo, ".gitmodules")); err == nil {
			return gitSnapshotAudit{HasGit: true, Reason: fmt.Sprintf("repository %q declares submodules", repo)}
		} else if !os.IsNotExist(err) {
			return gitSnapshotAudit{HasGit: true, Reason: fmt.Sprintf("cannot inspect submodule declaration in %q: %v", repo, err)}
		}

		top, err := gitPath(repo, "--show-toplevel")
		if err != nil {
			return gitSnapshotAudit{HasGit: true, Reason: fmt.Sprintf("cannot resolve Git worktree %q: %v", repo, err)}
		}
		gitDir, err := gitPath(repo, "--absolute-git-dir")
		if err != nil {
			return gitSnapshotAudit{HasGit: true, Reason: fmt.Sprintf("cannot resolve Git directory for %q: %v", repo, err)}
		}
		commonDir, err := gitPath(repo, "--git-common-dir")
		if err != nil {
			return gitSnapshotAudit{HasGit: true, Reason: fmt.Sprintf("cannot resolve common Git directory for %q: %v", repo, err)}
		}
		for _, path := range []string{top, gitDir, commonDir} {
			real, err := filepath.EvalSymlinks(path)
			if err != nil {
				return gitSnapshotAudit{HasGit: true, Reason: fmt.Sprintf("cannot resolve Git path %q: %v", path, err)}
			}
			if !withinAnyScope(real, covered) {
				return gitSnapshotAudit{HasGit: true, Reason: fmt.Sprintf("Git path %q is outside the declared snapshot scope", real)}
			}
		}
	}
	return gitSnapshotAudit{HasGit: true, Safe: true}
}

func gitPath(repo, arg string) (string, error) {
	out, err := exec.Command("git", "-C", repo, "rev-parse", arg).Output()
	if err != nil {
		return "", err
	}
	path := strings.TrimSpace(string(out))
	if path == "" {
		return "", fmt.Errorf("Git returned an empty path")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(repo, path)
	}
	return filepath.Clean(path), nil
}

func withinAnyScope(path string, scopes []string) bool {
	for _, scope := range scopes {
		rel, err := filepath.Rel(scope, path)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}
