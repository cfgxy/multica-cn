package execenv

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// snapshotGitArtifactManifest is deliberately small and self-contained: an
// operator can verify the bundle without trusting daemon logs or the source
// checkout that produced it.
type snapshotGitArtifactManifest struct {
	Baseline string `json:"baseline"`
	Head     string `json:"head"`
	Bundle   string `json:"bundle"`
	SHA256   string `json:"sha256"`
}

// CaptureSnapshotGitArtifact cleans daemon-owned runtime files from the
// overlay view, commits every remaining tracked/untracked/binary change into
// the overlay's private Git metadata, then writes an importable bundle. It is
// called only by the snapshot worker while the mount still exists.
func CaptureSnapshotGitArtifact(cwd string, spec *SnapshotGitArtifactSpec, env []string) error {
	if spec == nil || spec.ArtifactDir == "" || spec.Baseline == "" {
		return fmt.Errorf("snapshot git artifact is incomplete")
	}
	env = snapshotGitEnvironment(env)
	run := func(args ...string) (string, error) {
		return runGitEnvWithBase(cwd, env, nil, args...)
	}
	if err := CleanupRuntimeConfig(cwd, spec.Provider); err != nil {
		return fmt.Errorf("clean snapshot runtime config: %w", err)
	}
	if err := CleanupSidecars(spec.EnvRoot); err != nil {
		return fmt.Errorf("clean snapshot sidecars: %w", err)
	}
	addArgs := []string{"add", "-A"}
	if spec.ScopePath != "" {
		scopePath, err := snapshotArtifactScopePath(spec.ScopePath)
		if err != nil {
			return err
		}
		addArgs = append(addArgs, "--", scopePath)
	}
	// The private metadata overlay can still see host ref changes made after the
	// daemon recorded Baseline. Rebuild its index and HEAD from that fixed commit
	// before staging the task's final working tree. read-tree does not refresh
	// the worktree, so it preserves task writes, including ones committed by the
	// agent before capture.
	if out, err := run("read-tree", spec.Baseline); err != nil {
		return fmt.Errorf("rebuild snapshot Git index: %s: %w", strings.TrimSpace(out), err)
	}
	if out, err := run("update-ref", "HEAD", spec.Baseline); err != nil {
		return fmt.Errorf("reset snapshot Git head to baseline: %s: %w", strings.TrimSpace(out), err)
	}
	if _, err := run(addArgs...); err != nil {
		return fmt.Errorf("stage snapshot changes: %w", err)
	}
	head := spec.Baseline
	if _, err := run("diff", "--cached", "--quiet"); err != nil {
		tree, treeErr := run("write-tree")
		if treeErr != nil {
			return fmt.Errorf("write snapshot tree: %w", treeErr)
		}
		commit, commitErr := run("-c", "user.name=Multica Snapshot", "-c", "user.email=snapshot@multica.invalid", "commit-tree", strings.TrimSpace(tree), "-p", spec.Baseline, "-m", "multica: snapshot task artifact")
		if commitErr != nil {
			return fmt.Errorf("commit snapshot changes: %w", commitErr)
		}
		head = strings.TrimSpace(commit)
		if out, updateErr := run("update-ref", "HEAD", head, spec.Baseline); updateErr != nil {
			return fmt.Errorf("update snapshot artifact head: %s: %w", strings.TrimSpace(out), updateErr)
		}
	}
	if err := os.MkdirAll(spec.ArtifactDir, 0o700); err != nil {
		return fmt.Errorf("create snapshot artifact directory: %w", err)
	}
	bundle := filepath.Join(spec.ArtifactDir, "changes.bundle")
	// A no-change task still gets a valid, independently inspectable bundle.
	args := []string{"bundle", "create", bundle, "HEAD"}
	if spec.Baseline != head {
		args = append(args, "^"+spec.Baseline)
	}
	if out, err := run(args...); err != nil {
		return fmt.Errorf("create snapshot artifact bundle from %s to %s: %s: %w", spec.Baseline, head, strings.TrimSpace(out), err)
	}
	raw, err := os.ReadFile(bundle)
	if err != nil {
		return fmt.Errorf("read snapshot artifact bundle: %w", err)
	}
	checksum := sha256.Sum256(raw)
	manifest := snapshotGitArtifactManifest{Baseline: spec.Baseline, Head: head, Bundle: filepath.Base(bundle), SHA256: hex.EncodeToString(checksum[:])}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("encode snapshot artifact manifest: %w", err)
	}
	if err := os.WriteFile(filepath.Join(spec.ArtifactDir, "manifest.json"), append(encoded, '\n'), 0o600); err != nil {
		return fmt.Errorf("write snapshot artifact manifest: %w", err)
	}
	return nil
}

func snapshotArtifactScopePath(scope string) (string, error) {
	if strings.ContainsRune(scope, 0) {
		return "", fmt.Errorf("snapshot Git artifact scope contains NUL")
	}
	clean := filepath.ToSlash(filepath.Clean(scope))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || filepath.IsAbs(scope) {
		return "", fmt.Errorf("snapshot Git artifact scope %q must be a repository-relative directory", scope)
	}
	// Capture runs from the declared directory, so use Git's top-level magic to
	// keep the path repository-relative rather than resolving it below cwd.
	return ":/" + clean, nil
}

// SnapshotGitBaseline resolves the pristine commit before the worker mounts
// the overlay. It is exported for the daemon's one-time pre-launch capture.
func SnapshotGitBaseline(cwd string) (string, error) {
	base, err := runGitEnvWithBase(cwd, SnapshotGitEnvironment(), nil, "rev-parse", "HEAD")
	base = strings.TrimSpace(base)
	if err != nil || base == "" {
		return "", fmt.Errorf("resolve snapshot Git baseline: %w", err)
	}
	return base, nil
}
