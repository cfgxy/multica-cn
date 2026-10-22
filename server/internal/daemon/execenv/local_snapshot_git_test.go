package execenv

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCaptureSnapshotGitArtifactImportsIntoIndependentClone(t *testing.T) {
	repo := t.TempDir()
	gitForSnapshotTest(t, repo, "init", "-q")
	gitForSnapshotTest(t, repo, "config", "user.name", "Test")
	gitForSnapshotTest(t, repo, "config", "user.email", "test@example.invalid")
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitForSnapshotTest(t, repo, "add", "tracked.txt")
	gitForSnapshotTest(t, repo, "commit", "-qm", "base")
	baseline := strings.TrimSpace(gitForSnapshotTest(t, repo, "rev-parse", "HEAD"))
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "binary.bin"), []byte{0, 1, 2, 3}, 0o600); err != nil {
		t.Fatal(err)
	}

	artifactDir := t.TempDir()
	if err := CaptureSnapshotGitArtifact(repo, &SnapshotGitArtifactSpec{ArtifactDir: artifactDir, Baseline: baseline, EnvRoot: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(artifactDir, "manifest.json")); err != nil {
		t.Fatalf("artifact manifest: %v", err)
	}
	if _, err := os.Stat(filepath.Join(artifactDir, "changes.bundle")); err != nil {
		t.Fatalf("artifact bundle: %v", err)
	}

	clone := filepath.Join(t.TempDir(), "clone")
	gitForSnapshotTest(t, "", "clone", "-q", repo, clone)
	gitForSnapshotTest(t, clone, "fetch", filepath.Join(artifactDir, "changes.bundle"), "HEAD:refs/heads/snapshot")
	gitForSnapshotTest(t, clone, "checkout", "-q", "snapshot")
	gitForSnapshotTest(t, clone, "fsck", "--no-dangling")
	if got := gitForSnapshotTest(t, clone, "show", "snapshot:tracked.txt"); got != "changed\n" {
		t.Fatalf("independent clone tracked content = %q, want changed", got)
	}
	if got := gitForSnapshotTest(t, clone, "show", "snapshot:binary.bin"); string(got) != string([]byte{0, 1, 2, 3}) {
		t.Fatalf("independent clone binary content = %v", []byte(got))
	}
	if got := gitForSnapshotTest(t, repo, "status", "--porcelain"); got != "" {
		t.Fatalf("artifact capture must leave its overlay Git view clean, status=%q", got)
	}
}

func gitForSnapshotTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmdArgs := args
	if dir != "" {
		cmdArgs = append([]string{"-C", dir}, args...)
	}
	out, err := exec.Command("git", cmdArgs...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v (%s)", args, err, out)
	}
	return string(out)
}
