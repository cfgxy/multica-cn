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
	if err := CaptureSnapshotGitArtifact(repo, &SnapshotGitArtifactSpec{ArtifactDir: artifactDir, Baseline: baseline, EnvRoot: t.TempDir()}, nil); err != nil {
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

func TestSnapshotGitBaselineIgnoresGitLocationEnvironment(t *testing.T) {
	target := filepath.Join(t.TempDir(), "target")
	outside := filepath.Join(t.TempDir(), "outside")
	for _, repo := range []string{target, outside} {
		if err := os.MkdirAll(repo, 0o700); err != nil {
			t.Fatal(err)
		}
		gitForSnapshotTest(t, repo, "init", "-q")
		gitForSnapshotTest(t, repo, "config", "user.name", "Test")
		gitForSnapshotTest(t, repo, "config", "user.email", "test@example.invalid")
	}
	if err := os.WriteFile(filepath.Join(target, "target.txt"), []byte("target\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitForSnapshotTest(t, target, "add", "target.txt")
	gitForSnapshotTest(t, target, "commit", "-qm", "target")
	want := strings.TrimSpace(gitForSnapshotTest(t, target, "rev-parse", "HEAD"))
	if err := os.WriteFile(filepath.Join(outside, "outside.txt"), []byte("outside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitForSnapshotTest(t, outside, "add", "outside.txt")
	gitForSnapshotTest(t, outside, "commit", "-qm", "outside")

	outsideGitDir := filepath.Join(outside, ".git")
	cases := []struct {
		name string
		env  func() []string
	}{
		{name: "git-dir", env: func() []string { return []string{"GIT_DIR=" + outsideGitDir} }},
		{name: "git-work-tree", env: func() []string { return []string{"GIT_WORK_TREE=" + outside} }},
		{name: "git-common-dir", env: func() []string { return []string{"GIT_COMMON_DIR=" + outsideGitDir} }},
		{name: "git-object-directory", env: func() []string {
			return []string{"GIT_OBJECT_DIRECTORY=" + filepath.Join(outsideGitDir, "objects")}
		}},
		{name: "git-alternate-object-directories", env: func() []string {
			return []string{"GIT_ALTERNATE_OBJECT_DIRECTORIES=" + filepath.Join(outsideGitDir, "objects")}
		}},
		{name: "git-index-file", env: func() []string { return []string{"GIT_INDEX_FILE=" + filepath.Join(outsideGitDir, "index")} }},
		{name: "all", env: func() []string {
			return []string{
				"GIT_DIR=" + outsideGitDir,
				"GIT_WORK_TREE=" + outside,
				"GIT_COMMON_DIR=" + outsideGitDir,
				"GIT_OBJECT_DIRECTORY=" + filepath.Join(outsideGitDir, "objects"),
				"GIT_ALTERNATE_OBJECT_DIRECTORIES=" + filepath.Join(outsideGitDir, "objects"),
				"GIT_INDEX_FILE=" + filepath.Join(outsideGitDir, "index"),
			}
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, item := range tc.env() {
				key, value, ok := strings.Cut(item, "=")
				if !ok {
					t.Fatalf("invalid environment entry %q", item)
				}
				t.Setenv(key, value)
			}
			got, err := SnapshotGitBaseline(target)
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Fatalf("baseline = %q, want target HEAD %q", got, want)
			}
		})
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
