package execenv

import (
	"encoding/json"
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

func TestCaptureSnapshotGitArtifactExcludesSiblingChangesForDeclaredScope(t *testing.T) {
	repo := t.TempDir()
	gitForSnapshotTest(t, repo, "init", "-q")
	gitForSnapshotTest(t, repo, "config", "user.name", "Test")
	gitForSnapshotTest(t, repo, "config", "user.email", "test@example.invalid")
	for path, contents := range map[string]string{
		"apps/a/owned.txt":    "base-owned\n",
		"apps/b/staged.txt":   "base-staged\n",
		"apps/b/unstaged.txt": "base-unstaged\n",
	} {
		fullPath := filepath.Join(repo, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fullPath, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	gitForSnapshotTest(t, repo, "add", "apps")
	gitForSnapshotTest(t, repo, "commit", "-qm", "base")
	baseline := strings.TrimSpace(gitForSnapshotTest(t, repo, "rev-parse", "HEAD"))

	clone := filepath.Join(t.TempDir(), "clone")
	gitForSnapshotTest(t, "", "clone", "-q", repo, clone)
	if err := os.WriteFile(filepath.Join(repo, "apps", "a", "owned.txt"), []byte("task-owned\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "apps", "b", "staged.txt"), []byte("user-staged\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitForSnapshotTest(t, repo, "add", "apps/b/staged.txt")
	if err := os.WriteFile(filepath.Join(repo, "apps", "b", "unstaged.txt"), []byte("user-unstaged\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "apps", "b", "user-untracked.txt"), []byte("user-untracked\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	artifactDir := t.TempDir()
	if err := CaptureSnapshotGitArtifact(filepath.Join(repo, "apps", "a"), &SnapshotGitArtifactSpec{
		ArtifactDir: artifactDir,
		Baseline:    baseline,
		EnvRoot:     t.TempDir(),
		ScopePath:   "apps/a",
	}, nil); err != nil {
		t.Fatal(err)
	}
	gitForSnapshotTest(t, clone, "fetch", filepath.Join(artifactDir, "changes.bundle"), "HEAD:refs/heads/snapshot")
	gitForSnapshotTest(t, clone, "checkout", "-q", "snapshot")
	gitForSnapshotTest(t, clone, "fsck", "--no-dangling")
	if got := gitForSnapshotTest(t, clone, "show", "snapshot:apps/a/owned.txt"); got != "task-owned\n" {
		t.Fatalf("declared artifact content = %q, want task change", got)
	}
	for _, path := range []string{"apps/b/staged.txt", "apps/b/unstaged.txt"} {
		if got := gitForSnapshotTest(t, clone, "show", "snapshot:"+path); got == "user-staged\n" || got == "user-unstaged\n" {
			t.Fatalf("sibling change %q leaked into artifact: %q", path, got)
		}
	}
	if out, err := exec.Command("git", "-C", clone, "cat-file", "-e", "snapshot:apps/b/user-untracked.txt").CombinedOutput(); err == nil {
		t.Fatalf("sibling untracked file leaked into artifact: %s", out)
	}
}

func TestCaptureSnapshotGitArtifactDeclaredScopeAnchorsToBaselineAfterHostAdvance(t *testing.T) {
	repo := t.TempDir()
	gitForSnapshotTest(t, repo, "init", "-q")
	gitForSnapshotTest(t, repo, "config", "user.name", "Test")
	gitForSnapshotTest(t, repo, "config", "user.email", "test@example.invalid")
	for path, contents := range map[string]string{
		"apps/a/owned.txt":   "base-owned\n",
		"apps/b/sibling.txt": "base-sibling\n",
	} {
		fullPath := filepath.Join(repo, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fullPath, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	gitForSnapshotTest(t, repo, "add", "apps")
	gitForSnapshotTest(t, repo, "commit", "-qm", "baseline")
	baseline := strings.TrimSpace(gitForSnapshotTest(t, repo, "rev-parse", "HEAD"))

	clone := filepath.Join(t.TempDir(), "clone")
	gitForSnapshotTest(t, "", "clone", "-q", repo, clone)
	if err := os.WriteFile(filepath.Join(repo, "apps", "a", "owned.txt"), []byte("task-owned\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The agent may have committed its declared-path work into the private
	// metadata view before capture. The final artifact must still rebuild that
	// working-tree state on top of Baseline, rather than retain a later HEAD.
	gitForSnapshotTest(t, repo, "add", "apps/a/owned.txt")
	gitForSnapshotTest(t, repo, "commit", "-qm", "task private commit")
	if err := os.WriteFile(filepath.Join(repo, "apps", "b", "sibling.txt"), []byte("host-committed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitForSnapshotTest(t, repo, "add", "apps/b/sibling.txt")
	gitForSnapshotTest(t, repo, "commit", "-qm", "host sibling advance")
	advanced := strings.TrimSpace(gitForSnapshotTest(t, repo, "rev-parse", "HEAD"))
	if advanced == baseline {
		t.Fatal("host HEAD did not advance")
	}

	artifactDir := t.TempDir()
	if err := CaptureSnapshotGitArtifact(filepath.Join(repo, "apps", "a"), &SnapshotGitArtifactSpec{
		ArtifactDir: artifactDir,
		Baseline:    baseline,
		EnvRoot:     t.TempDir(),
		ScopePath:   "apps/a",
	}, nil); err != nil {
		t.Fatal(err)
	}
	gitForSnapshotTest(t, clone, "fetch", filepath.Join(artifactDir, "changes.bundle"), "HEAD:refs/heads/snapshot")
	if parent := strings.TrimSpace(gitForSnapshotTest(t, clone, "rev-parse", "snapshot^")); parent != baseline {
		t.Fatalf("artifact parent = %s, want baseline %s", parent, baseline)
	}
	if got := gitForSnapshotTest(t, clone, "show", "snapshot:apps/a/owned.txt"); got != "task-owned\n" {
		t.Fatalf("declared artifact content = %q, want task change", got)
	}
	if got := gitForSnapshotTest(t, clone, "show", "snapshot:apps/b/sibling.txt"); got != "base-sibling\n" {
		t.Fatalf("host sibling commit leaked into artifact: %q", got)
	}
}

func TestCaptureSnapshotGitArtifactDeclaredScopeNoChangeUsesBaselineAfterHostAdvance(t *testing.T) {
	repo := t.TempDir()
	gitForSnapshotTest(t, repo, "init", "-q")
	gitForSnapshotTest(t, repo, "config", "user.name", "Test")
	gitForSnapshotTest(t, repo, "config", "user.email", "test@example.invalid")
	for path, contents := range map[string]string{
		"apps/a/owned.txt":   "base-owned\n",
		"apps/b/sibling.txt": "base-sibling\n",
	} {
		fullPath := filepath.Join(repo, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fullPath, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	gitForSnapshotTest(t, repo, "add", "apps")
	gitForSnapshotTest(t, repo, "commit", "-qm", "baseline")
	baseline := strings.TrimSpace(gitForSnapshotTest(t, repo, "rev-parse", "HEAD"))

	clone := filepath.Join(t.TempDir(), "clone")
	gitForSnapshotTest(t, "", "clone", "-q", repo, clone)
	if err := os.WriteFile(filepath.Join(repo, "apps", "b", "sibling.txt"), []byte("host-committed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitForSnapshotTest(t, repo, "add", "apps/b/sibling.txt")
	gitForSnapshotTest(t, repo, "commit", "-qm", "host sibling advance")

	artifactDir := t.TempDir()
	if err := CaptureSnapshotGitArtifact(filepath.Join(repo, "apps", "a"), &SnapshotGitArtifactSpec{
		ArtifactDir: artifactDir,
		Baseline:    baseline,
		EnvRoot:     t.TempDir(),
		ScopePath:   "apps/a",
	}, nil); err != nil {
		t.Fatal(err)
	}
	gitForSnapshotTest(t, clone, "fetch", filepath.Join(artifactDir, "changes.bundle"), "HEAD:refs/heads/snapshot")
	if head := strings.TrimSpace(gitForSnapshotTest(t, clone, "rev-parse", "snapshot")); head != baseline {
		t.Fatalf("no-change artifact head = %s, want baseline %s", head, baseline)
	}
	if got := gitForSnapshotTest(t, clone, "show", "snapshot:apps/b/sibling.txt"); got != "base-sibling\n" {
		t.Fatalf("host sibling commit leaked into no-change artifact: %q", got)
	}
}

func TestCaptureSnapshotGitArtifactRootAnchorsToBaselineAfterHostAdvance(t *testing.T) {
	host := t.TempDir()
	gitForSnapshotTest(t, host, "init", "-q")
	gitForSnapshotTest(t, host, "config", "user.name", "Test")
	gitForSnapshotTest(t, host, "config", "user.email", "test@example.invalid")
	if err := os.WriteFile(filepath.Join(host, "task.txt"), []byte("base-task\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitForSnapshotTest(t, host, "add", "task.txt")
	gitForSnapshotTest(t, host, "commit", "-qm", "baseline")
	baseline := strings.TrimSpace(gitForSnapshotTest(t, host, "rev-parse", "HEAD"))

	task := filepath.Join(t.TempDir(), "task")
	receiver := filepath.Join(t.TempDir(), "receiver")
	gitForSnapshotTest(t, "", "clone", "-q", host, task)
	gitForSnapshotTest(t, "", "clone", "-q", host, receiver)
	gitForSnapshotTest(t, task, "config", "user.name", "Test")
	gitForSnapshotTest(t, task, "config", "user.email", "test@example.invalid")
	if err := os.WriteFile(filepath.Join(task, "task.txt"), []byte("task-final\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Capture must preserve a final task working tree even when the agent has
	// already committed it into its private metadata view.
	gitForSnapshotTest(t, task, "add", "task.txt")
	gitForSnapshotTest(t, task, "commit", "-qm", "task private commit")
	if err := os.WriteFile(filepath.Join(host, "host-after-start.txt"), []byte("host-only\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitForSnapshotTest(t, host, "add", "host-after-start.txt")
	gitForSnapshotTest(t, host, "commit", "-qm", "host advance")
	gitForSnapshotTest(t, task, "fetch", "-q", "origin")
	gitForSnapshotTest(t, task, "update-ref", "HEAD", "origin/master")

	artifactDir := t.TempDir()
	if err := CaptureSnapshotGitArtifact(task, &SnapshotGitArtifactSpec{ArtifactDir: artifactDir, Baseline: baseline, EnvRoot: t.TempDir()}, nil); err != nil {
		t.Fatal(err)
	}
	gitForSnapshotTest(t, receiver, "fetch", filepath.Join(artifactDir, "changes.bundle"), "HEAD:refs/heads/snapshot")
	if parent := strings.TrimSpace(gitForSnapshotTest(t, receiver, "rev-parse", "snapshot^")); parent != baseline {
		t.Fatalf("artifact parent = %s, want baseline %s", parent, baseline)
	}
	if got := gitForSnapshotTest(t, receiver, "show", "snapshot:task.txt"); got != "task-final\n" {
		t.Fatalf("artifact task content = %q, want task-final", got)
	}
	if out, err := exec.Command("git", "-C", receiver, "cat-file", "-e", "snapshot:host-after-start.txt").CombinedOutput(); err == nil {
		t.Fatalf("host commit leaked into root artifact: %s", out)
	}
	assertSnapshotManifest(t, artifactDir, baseline, strings.TrimSpace(gitForSnapshotTest(t, receiver, "rev-parse", "snapshot")))
}

func TestCaptureSnapshotGitArtifactRootNoChangeUsesBaselineAfterHostAdvance(t *testing.T) {
	host := t.TempDir()
	gitForSnapshotTest(t, host, "init", "-q")
	gitForSnapshotTest(t, host, "config", "user.name", "Test")
	gitForSnapshotTest(t, host, "config", "user.email", "test@example.invalid")
	if err := os.WriteFile(filepath.Join(host, "base.txt"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitForSnapshotTest(t, host, "add", "base.txt")
	gitForSnapshotTest(t, host, "commit", "-qm", "baseline")
	baseline := strings.TrimSpace(gitForSnapshotTest(t, host, "rev-parse", "HEAD"))

	task := filepath.Join(t.TempDir(), "task")
	receiver := filepath.Join(t.TempDir(), "receiver")
	gitForSnapshotTest(t, "", "clone", "-q", host, task)
	gitForSnapshotTest(t, "", "clone", "-q", host, receiver)
	if err := os.WriteFile(filepath.Join(host, "host-after-start.txt"), []byte("host-only\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitForSnapshotTest(t, host, "add", "host-after-start.txt")
	gitForSnapshotTest(t, host, "commit", "-qm", "host advance")
	gitForSnapshotTest(t, task, "fetch", "-q", "origin")
	gitForSnapshotTest(t, task, "update-ref", "HEAD", "origin/master")

	artifactDir := t.TempDir()
	if err := CaptureSnapshotGitArtifact(task, &SnapshotGitArtifactSpec{ArtifactDir: artifactDir, Baseline: baseline, EnvRoot: t.TempDir()}, nil); err != nil {
		t.Fatal(err)
	}
	gitForSnapshotTest(t, receiver, "fetch", filepath.Join(artifactDir, "changes.bundle"), "HEAD:refs/heads/snapshot")
	if head := strings.TrimSpace(gitForSnapshotTest(t, receiver, "rev-parse", "snapshot")); head != baseline {
		t.Fatalf("no-change artifact head = %s, want baseline %s", head, baseline)
	}
	if out, err := exec.Command("git", "-C", receiver, "cat-file", "-e", "snapshot:host-after-start.txt").CombinedOutput(); err == nil {
		t.Fatalf("host commit leaked into no-change root artifact: %s", out)
	}
	assertSnapshotManifest(t, artifactDir, baseline, baseline)
}

func assertSnapshotManifest(t *testing.T, artifactDir, baseline, head string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(artifactDir, "manifest.json"))
	if err != nil {
		t.Fatalf("read artifact manifest: %v", err)
	}
	var manifest snapshotGitArtifactManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("decode artifact manifest: %v", err)
	}
	if manifest.Baseline != baseline || manifest.Head != head {
		t.Fatalf("manifest baseline/head = %s/%s, want %s/%s", manifest.Baseline, manifest.Head, baseline, head)
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
