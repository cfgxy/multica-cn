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
func CaptureSnapshotGitArtifact(cwd string, spec *SnapshotGitArtifactSpec) error {
	if spec == nil || spec.ArtifactDir == "" || spec.Baseline == "" {
		return fmt.Errorf("snapshot git artifact is incomplete")
	}
	if err := CleanupRuntimeConfig(cwd, spec.Provider); err != nil {
		return fmt.Errorf("clean snapshot runtime config: %w", err)
	}
	if err := CleanupSidecars(spec.EnvRoot); err != nil {
		return fmt.Errorf("clean snapshot sidecars: %w", err)
	}
	if _, err := runGit(cwd, "add", "-A"); err != nil {
		return fmt.Errorf("stage snapshot changes: %w", err)
	}
	if _, err := runGit(cwd, "diff", "--cached", "--quiet"); err != nil {
		if _, commitErr := runGit(cwd, "-c", "user.name=Multica Snapshot", "-c", "user.email=snapshot@multica.invalid", "commit", "--no-verify", "-m", "multica: snapshot task artifact"); commitErr != nil {
			return fmt.Errorf("commit snapshot changes: %w", commitErr)
		}
	}
	head, err := runGitTrimmed(cwd, "rev-parse", "HEAD")
	if err != nil {
		return fmt.Errorf("resolve snapshot artifact head: %w", err)
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
	if out, err := runGit(cwd, args...); err != nil {
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

// SnapshotGitBaseline resolves the pristine commit before the worker mounts
// the overlay. It is exported for the daemon's one-time pre-launch capture.
func SnapshotGitBaseline(cwd string) (string, error) {
	base, err := runGitTrimmed(cwd, "rev-parse", "HEAD")
	if err != nil || strings.TrimSpace(base) == "" {
		return "", fmt.Errorf("resolve snapshot Git baseline: %w", err)
	}
	return base, nil
}
