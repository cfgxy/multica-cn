//go:build linux

package execenv

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/multica-ai/multica/server/pkg/agent"
)

// Host-dependent smoke tests for the snapshot capability. They are opt-in
// because they exercise real user-namespace + overlayfs mounts, which
// hardened hosts may refuse — the daemon handles that by keeping the
// capability off, and CI hosts are not guaranteed to allow it. Run locally
// against a built daemon binary:
//
//	MULTICA_SNAPSHOT_SMOKE_BIN=/path/to/multica go test ./internal/daemon/execenv/ -run SnapshotSmoke
func snapshotSmokeBinary(t *testing.T) string {
	t.Helper()
	bin := os.Getenv("MULTICA_SNAPSHOT_SMOKE_BIN")
	if bin == "" {
		t.Skip("opt-in smoke: set MULTICA_SNAPSHOT_SMOKE_BIN to a multica binary that wires the __snapshot entrypoints")
	}
	return bin
}

// spawnWithSnapshotNamespaces is the same clone + ID-map shape the daemon
// probe and pkg/agent's wrap spawner use.
func spawnWithSnapshotNamespaces(t *testing.T, argv ...string) ([]byte, error) {
	return spawnWithSnapshotNamespacesEnv(t, snapshotSmokeEnvironment(), argv...)
}

func spawnWithSnapshotNamespacesEnv(t *testing.T, env []string, argv ...string) ([]byte, error) {
	t.Helper()
	cmd := exec.Command(argv[0], argv[1:]...)
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Cloneflags = syscall.CLONE_NEWUSER | syscall.CLONE_NEWNS
	cmd.SysProcAttr.UidMappings = []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Getuid(), Size: 1}}
	cmd.SysProcAttr.GidMappings = []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Getgid(), Size: 1}}
	cmd.Env = env
	return cmd.CombinedOutput()
}

func snapshotSmokeEnvironment(overrides ...string) []string {
	values := make(map[string]string, len(overrides)+1)
	values["HOME"] = os.TempDir()
	for _, override := range overrides {
		key, value, _ := strings.Cut(override, "=")
		values[key] = value
	}
	env := make([]string, 0, len(os.Environ())+len(values))
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		if _, replaced := values[key]; !replaced {
			env = append(env, item)
		}
	}
	for key, value := range values {
		env = append(env, key+"="+value)
	}
	return env
}

func TestProbeSnapshotSupportSmoke(t *testing.T) {
	bin := snapshotSmokeBinary(t)
	ok, detail := ProbeSnapshotSupport(bin, t.TempDir())
	if !ok {
		t.Fatalf("snapshot capability probe failed: %s", detail)
	}
	t.Logf("probe: %s", detail)
}

// TestSnapshotWorkerSmoke mounts a real overlay through the stage-2 worker
// and asserts the isolation contract end to end: the lower layer's content
// is visible in the view, the agent's write lands in the upper layer, and
// the pristine directory stays byte-identical.
func TestSnapshotWorkerSmoke(t *testing.T) {
	bin := snapshotSmokeBinary(t)
	scratch := t.TempDir()
	target := filepath.Join(scratch, "pristine-target")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "marker.txt"), []byte("lower\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	alias, upper, work := filepath.Join(scratch, "alias"), filepath.Join(scratch, "upper"), filepath.Join(scratch, "work")
	for _, d := range []string{alias, upper, work} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	spec := &SnapshotRunSpec{
		TaskID: "smoke",
		Units:  []SnapshotUnitSpec{{Target: target, Alias: alias, Upper: upper, Work: work}},
		Cwd:    target,
	}
	// Route through the production serialization point so the argv exercises
	// the exact contract the supervised launcher rebuilds from (host IDs in
	// the spec JSON), not a test-local marshal.
	wrap, err := BuildSnapshotWrap(bin, spec)
	if err != nil {
		t.Fatal(err)
	}
	// The payload agent writes through the view, reads the lower layer, and
	// reads the pristine content through the alias — all INSIDE the
	// namespace, where (and only where) the alias bind exists: the same
	// shape every real backend session takes.
	payload := "pwd && echo in-view > in-view.txt && cat marker.txt" +
		" && cat " + alias + "/marker.txt" +
		" && test ! -e " + alias + "/in-view.txt && echo ALIAS_OK"
	out, err := spawnWithSnapshotNamespaces(t,
		bin, SnapshotWorkerSubcommand, wrap.SpecJSON, agent.SnapshotWorkerSep,
		"/bin/sh", "-c", payload)
	if err != nil {
		t.Fatalf("worker failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), target) || !strings.Contains(string(out), "lower\n") {
		t.Fatalf("payload must run inside the view and see the lower layer, got: %s", out)
	}
	if !strings.Contains(string(out), "ALIAS_OK") {
		t.Fatalf("pristine alias must show lower content and hide view writes, got: %s", out)
	}

	// The agent's write landed in the upper layer, reachable from the host
	// after the namespace died with the process.
	if _, err := os.Stat(filepath.Join(upper, "in-view.txt")); err != nil {
		t.Fatalf("view write must accumulate in the upper layer: %v", err)
	}
	// The pristine directory is byte-identical: no in-view.txt, marker intact.
	if _, err := os.Stat(filepath.Join(target, "in-view.txt")); !os.IsNotExist(err) {
		t.Fatalf("view write leaked into the pristine directory (stat err=%v)", err)
	}
	got, err := os.ReadFile(filepath.Join(target, "marker.txt"))
	if err != nil || string(got) != "lower\n" {
		t.Fatalf("pristine marker must be untouched, got %q err=%v", got, err)
	}
	// The alias bind existed only inside the child's namespace, so the
	// parent sees just the empty mountpoint directory it created.
	if entries, _ := os.ReadDir(alias); len(entries) != 0 {
		t.Fatalf("alias must be empty from outside the namespace, got %d entries", len(entries))
	}
}

// TestSnapshotWorkerCapturesGitArtifactSmoke exercises the production worker
// path in which the overlay remains mounted after the agent exits so capture
// can commit its private Git view and write a reviewable bundle.
func TestSnapshotWorkerCapturesGitArtifactSmoke(t *testing.T) {
	bin := snapshotSmokeBinary(t)
	scratch := t.TempDir()
	target := filepath.Join(scratch, "pristine-repo")
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
	gitForSnapshotTest(t, target, "init", "-q")
	gitForSnapshotTest(t, target, "config", "user.name", "Test")
	gitForSnapshotTest(t, target, "config", "user.email", "test@example.invalid")
	if err := os.WriteFile(filepath.Join(target, "tracked.txt"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitForSnapshotTest(t, target, "add", "tracked.txt")
	gitForSnapshotTest(t, target, "commit", "-qm", "base")
	baseline := strings.TrimSpace(gitForSnapshotTest(t, target, "rev-parse", "HEAD"))
	pristineRefs := gitForSnapshotTest(t, target, "show-ref", "--head")

	alias := filepath.Join(scratch, "alias")
	upper := filepath.Join(scratch, "upper")
	work := filepath.Join(scratch, "work")
	for _, dir := range []string{alias, upper, work} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	artifactDir := filepath.Join(scratch, "artifact")
	spec := &SnapshotRunSpec{
		TaskID: "git-artifact-smoke",
		Units:  []SnapshotUnitSpec{{Target: target, Alias: alias, Upper: upper, Work: work}},
		Cwd:    target,
		GitArtifact: &SnapshotGitArtifactSpec{
			ArtifactDir: artifactDir,
			Baseline:    baseline,
			EnvRoot:     filepath.Join(scratch, "env"),
		},
	}
	wrap, err := BuildSnapshotWrap(bin, spec)
	if err != nil {
		t.Fatal(err)
	}
	payload := "printf 'changed\\n' > tracked.txt" +
		" && printf 'untracked\\n' > untracked.txt" +
		" && printf '\\000\\001\\002\\377' > binary.bin"
	out, err := spawnWithSnapshotNamespaces(t,
		bin, SnapshotWorkerSubcommand, wrap.SpecJSON, agent.SnapshotWorkerSep,
		"/bin/sh", "-c", payload)
	if err != nil {
		t.Fatalf("worker failed: %v\n%s", err, out)
	}

	if got, err := os.ReadFile(filepath.Join(target, "tracked.txt")); err != nil || string(got) != "base\n" {
		t.Fatalf("pristine tracked file = %q, err=%v; want base content", got, err)
	}
	for _, name := range []string{"untracked.txt", "binary.bin"} {
		if _, err := os.Stat(filepath.Join(target, name)); !os.IsNotExist(err) {
			t.Fatalf("worker write %s leaked into pristine (stat err=%v)", name, err)
		}
	}
	if got := gitForSnapshotTest(t, target, "status", "--porcelain"); got != "" {
		t.Fatalf("pristine repository must remain clean, status=%q", got)
	}
	if got := gitForSnapshotTest(t, target, "show-ref", "--head"); got != pristineRefs {
		t.Fatalf("snapshot commit must not change pristine refs, got %q want %q", got, pristineRefs)
	}

	clone := filepath.Join(scratch, "clone")
	gitForSnapshotTest(t, "", "clone", "-q", target, clone)
	bundle := filepath.Join(artifactDir, "changes.bundle")
	gitForSnapshotTest(t, clone, "fetch", bundle, "HEAD:refs/heads/snapshot")
	gitForSnapshotTest(t, clone, "checkout", "-q", "snapshot")
	gitForSnapshotTest(t, clone, "fsck", "--no-dangling")
	if got := gitForSnapshotTest(t, clone, "show", "snapshot:tracked.txt"); got != "changed\n" {
		t.Fatalf("captured tracked content = %q, want changed", got)
	}
	if got := gitForSnapshotTest(t, clone, "show", "snapshot:untracked.txt"); got != "untracked\n" {
		t.Fatalf("captured untracked content = %q, want untracked", got)
	}
	if got := []byte(gitForSnapshotTest(t, clone, "show", "snapshot:binary.bin")); string(got) != string([]byte{0, 1, 2, 255}) {
		t.Fatalf("captured binary content = %v", got)
	}
	if entries, err := os.ReadDir(alias); err != nil || len(entries) != 0 {
		t.Fatalf("worker mount namespace must be gone; alias entries=%d err=%v", len(entries), err)
	}
}

func TestSnapshotWorkerCapturesDeclaredGitSubdirectoryArtifactSmoke(t *testing.T) {
	bin := snapshotSmokeBinary(t)
	// The worker must override an inherited false setting because the business
	// directory and repository metadata are separate overlay mounts.
	t.Setenv("GIT_DISCOVERY_ACROSS_FILESYSTEM", "0")
	scratch := t.TempDir()
	repo := filepath.Join(scratch, "pristine-repo")
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
	gitForSnapshotTest(t, repo, "init", "-q")
	gitForSnapshotTest(t, repo, "config", "user.name", "Test")
	gitForSnapshotTest(t, repo, "config", "user.email", "test@example.invalid")
	gitForSnapshotTest(t, repo, "add", "apps")
	gitForSnapshotTest(t, repo, "commit", "-qm", "base")
	baseline := strings.TrimSpace(gitForSnapshotTest(t, repo, "rev-parse", "HEAD"))
	pristineRefs := gitForSnapshotTest(t, repo, "show-ref", "--head")
	clone := filepath.Join(scratch, "clone")
	gitForSnapshotTest(t, "", "clone", "-q", repo, clone)
	outside := filepath.Join(scratch, "outside-sentinel")
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "sentinel"), []byte("must remain untouched\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	outsideBefore := smokeTreeDigest(t, outside, false)

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
	pristineStatus := gitForSnapshotTest(t, repo, "status", "--porcelain=v1", "--untracked-files=all")

	newUnit := func(name, target string) SnapshotUnitSpec {
		alias := filepath.Join(scratch, name, "alias")
		upper := filepath.Join(scratch, name, "upper")
		work := filepath.Join(scratch, name, "work")
		for _, dir := range []string{alias, upper, work} {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
		}
		return SnapshotUnitSpec{Target: target, Alias: alias, Upper: upper, Work: work}
	}
	artifactDir := filepath.Join(scratch, "artifact")
	spec := &SnapshotRunSpec{
		TaskID: "declared-git-subdirectory-artifact-smoke",
		Units: []SnapshotUnitSpec{
			newUnit("containment", repo),
			newUnit("business", filepath.Join(repo, "apps", "a")),
			newUnit("metadata", filepath.Join(repo, ".git")),
		},
		Cwd: filepath.Join(repo, "apps", "a"),
		GitArtifact: &SnapshotGitArtifactSpec{
			ArtifactDir: artifactDir,
			Baseline:    baseline,
			EnvRoot:     filepath.Join(scratch, "env"),
			ScopePath:   "apps/a",
		},
	}
	wrap, err := BuildSnapshotWrap(bin, spec)
	if err != nil {
		t.Fatal(err)
	}
	out, err := spawnWithSnapshotNamespaces(t,
		bin, SnapshotWorkerSubcommand, wrap.SpecJSON, agent.SnapshotWorkerSep,
		"/bin/sh", "-c", "printf 'task-owned\\n' > owned.txt && printf 'task-untracked\\n' > task-untracked.txt && printf 'task-sibling\\n' > ../b/task-sibling.txt")
	if err != nil {
		t.Fatalf("worker failed: %v\n%s", err, out)
	}

	if got, err := os.ReadFile(filepath.Join(repo, "apps", "a", "owned.txt")); err != nil || string(got) != "base-owned\n" {
		t.Fatalf("pristine declared file = %q, err=%v; want base content", got, err)
	}
	if _, err := os.Stat(filepath.Join(repo, "apps", "a", "task-untracked.txt")); !os.IsNotExist(err) {
		t.Fatalf("declared worker write leaked into pristine (stat err=%v)", err)
	}
	if got, err := os.ReadFile(filepath.Join(repo, "apps", "b", "staged.txt")); err != nil || string(got) != "user-staged\n" {
		t.Fatalf("pristine staged sibling = %q, err=%v; want user content", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(repo, "apps", "b", "unstaged.txt")); err != nil || string(got) != "user-unstaged\n" {
		t.Fatalf("pristine unstaged sibling = %q, err=%v; want user content", got, err)
	}
	if _, err := os.Stat(filepath.Join(repo, "apps", "b", "task-sibling.txt")); !os.IsNotExist(err) {
		t.Fatalf("sibling worker write leaked into pristine (stat err=%v)", err)
	}
	if got := gitForSnapshotTest(t, repo, "status", "--porcelain=v1", "--untracked-files=all"); got != pristineStatus {
		t.Fatalf("pristine sibling Git state changed\nbefore: %q\nafter:  %q", pristineStatus, got)
	}
	if got := gitForSnapshotTest(t, repo, "show-ref", "--head"); got != pristineRefs {
		t.Fatalf("snapshot artifact changed pristine refs, got %q want %q", got, pristineRefs)
	}
	if after := smokeTreeDigest(t, outside, false); after != outsideBefore {
		t.Fatalf("snapshot artifact wrote outside its materialized units\nbefore: %s\nafter:  %s", outsideBefore, after)
	}

	gitForSnapshotTest(t, clone, "fetch", filepath.Join(artifactDir, "changes.bundle"), "HEAD:refs/heads/snapshot")
	gitForSnapshotTest(t, clone, "checkout", "-q", "snapshot")
	gitForSnapshotTest(t, clone, "fsck", "--no-dangling")
	if got := gitForSnapshotTest(t, clone, "show", "snapshot:apps/a/owned.txt"); got != "task-owned\n" {
		t.Fatalf("captured declared content = %q, want task change", got)
	}
	if got := gitForSnapshotTest(t, clone, "show", "snapshot:apps/b/staged.txt"); got != "base-staged\n" {
		t.Fatalf("staged sibling leaked into artifact: %q", got)
	}
	if got := gitForSnapshotTest(t, clone, "show", "snapshot:apps/b/unstaged.txt"); got != "base-unstaged\n" {
		t.Fatalf("unstaged sibling leaked into artifact: %q", got)
	}
	if got := gitForSnapshotTest(t, clone, "show", "snapshot:apps/a/task-untracked.txt"); got != "task-untracked\n" {
		t.Fatalf("captured declared untracked content = %q, want task change", got)
	}
	if out, err := exec.Command("git", "-C", clone, "cat-file", "-e", "snapshot:apps/b/task-sibling.txt").CombinedOutput(); err == nil {
		t.Fatalf("unexpected sibling task artifact file: %s", out)
	}
	if out, err := exec.Command("git", "-C", clone, "cat-file", "-e", "snapshot:apps/b/user-untracked.txt").CombinedOutput(); err == nil {
		t.Fatalf("unexpected sibling artifact file: %s", out)
	}
}

// TestSnapshotWorkerRejectsExternalHooksPathBeforeAgentCommitSmoke proves the
// worker-side hook gate runs before the agent's ordinary git commit can invoke
// a host hook. The daemon admission audit rejects this topology earlier; this
// smoke covers the final execution-time backstop against configuration drift.
func TestSnapshotWorkerRejectsExternalHooksPathBeforeAgentCommitSmoke(t *testing.T) {
	bin := snapshotSmokeBinary(t)
	scratch := t.TempDir()
	repo := filepath.Join(scratch, "pristine-repo")
	if err := os.MkdirAll(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	gitForSnapshotTest(t, repo, "init", "-q")
	gitForSnapshotTest(t, repo, "config", "user.name", "Test")
	gitForSnapshotTest(t, repo, "config", "user.email", "test@example.invalid")
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitForSnapshotTest(t, repo, "add", "tracked.txt")
	gitForSnapshotTest(t, repo, "commit", "-qm", "base")
	baseline := strings.TrimSpace(gitForSnapshotTest(t, repo, "rev-parse", "HEAD"))

	outside := filepath.Join(scratch, "outside-hooks")
	hooks := filepath.Join(outside, "hooks")
	if err := os.MkdirAll(hooks, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "sentinel"), []byte("must remain untouched\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	hook := "#!/bin/sh\nprintf hook-ran > " + filepath.Join(outside, "hook-ran") + "\n"
	if err := os.WriteFile(filepath.Join(hooks, "pre-commit"), []byte(hook), 0o700); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", repo, "config", "core.hooksPath", hooks).CombinedOutput(); err != nil {
		t.Fatalf("configure external hooks path: %v (%s)", err, out)
	}
	before := smokeTreeDigest(t, outside, false)

	alias, upper, work := filepath.Join(scratch, "alias"), filepath.Join(scratch, "upper"), filepath.Join(scratch, "work")
	for _, path := range []string{alias, upper, work} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	spec := &SnapshotRunSpec{
		TaskID: "reject-external-hooks-before-agent-commit",
		Units:  []SnapshotUnitSpec{{Target: repo, Alias: alias, Upper: upper, Work: work}},
		Cwd:    repo,
		GitArtifact: &SnapshotGitArtifactSpec{
			ArtifactDir: filepath.Join(scratch, "artifact"),
			Baseline:    baseline,
			EnvRoot:     filepath.Join(scratch, "env"),
		},
	}
	wrap, err := BuildSnapshotWrap(bin, spec)
	if err != nil {
		t.Fatal(err)
	}
	out, err := spawnWithSnapshotNamespaces(t,
		bin, SnapshotWorkerSubcommand, wrap.SpecJSON, agent.SnapshotWorkerSep,
		"/bin/sh", "-c", "printf changed > tracked.txt && git add tracked.txt && git commit -qm isolated")
	if err == nil {
		t.Fatalf("worker must reject external hooks path before agent commit, output: %s", out)
	}
	if !strings.Contains(string(out), "core.hooksPath") {
		t.Fatalf("worker rejection must identify hooks path, output: %s", out)
	}
	if after := smokeTreeDigest(t, outside, false); after != before {
		t.Fatalf("external hook wrote outside snapshot before agent commit was rejected\nbefore: %s\nafter:  %s", before, after)
	}
}

// TestSnapshotWorkerStripsGitLocationEnvironmentSmoke injects each Git
// location override into the parent process. Both the agent and artifact
// capture must resolve Git metadata through the mounted snapshot view.
func TestSnapshotWorkerStripsGitLocationEnvironmentSmoke(t *testing.T) {
	bin := snapshotSmokeBinary(t)
	cases := []struct {
		name  string
		build func(gitDir, worktree string) []string
	}{
		{name: "git-dir", build: func(gitDir, _ string) []string { return []string{"GIT_DIR=" + gitDir} }},
		{name: "git-work-tree", build: func(_ string, worktree string) []string { return []string{"GIT_WORK_TREE=" + worktree} }},
		{name: "git-common-dir", build: func(gitDir, _ string) []string { return []string{"GIT_COMMON_DIR=" + gitDir} }},
		{name: "git-object-directory", build: func(gitDir, _ string) []string {
			return []string{"GIT_OBJECT_DIRECTORY=" + filepath.Join(gitDir, "objects")}
		}},
		{name: "git-alternate-object-directories", build: func(gitDir, _ string) []string {
			return []string{"GIT_ALTERNATE_OBJECT_DIRECTORIES=" + filepath.Join(gitDir, "objects")}
		}},
		{name: "git-index-file", build: func(gitDir, _ string) []string { return []string{"GIT_INDEX_FILE=" + filepath.Join(gitDir, "index")} }},
		{name: "combined", build: func(gitDir, worktree string) []string {
			return []string{
				"GIT_DIR=" + gitDir,
				"GIT_WORK_TREE=" + worktree,
				"GIT_COMMON_DIR=" + gitDir,
				"GIT_OBJECT_DIRECTORY=" + filepath.Join(gitDir, "objects"),
				"GIT_ALTERNATE_OBJECT_DIRECTORIES=" + filepath.Join(gitDir, "objects"),
				"GIT_INDEX_FILE=" + filepath.Join(gitDir, "index"),
			}
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			scratch := t.TempDir()
			target := filepath.Join(scratch, "pristine")
			outside := filepath.Join(scratch, "outside")
			for _, repo := range []string{target, outside} {
				if err := os.MkdirAll(repo, 0o700); err != nil {
					t.Fatal(err)
				}
				gitForSnapshotTest(t, repo, "init", "-q")
				gitForSnapshotTest(t, repo, "config", "user.name", "Test")
				gitForSnapshotTest(t, repo, "config", "user.email", "test@example.invalid")
			}
			if err := os.WriteFile(filepath.Join(target, "tracked.txt"), []byte("base\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			gitForSnapshotTest(t, target, "add", "tracked.txt")
			gitForSnapshotTest(t, target, "commit", "-qm", "base")
			baseline := strings.TrimSpace(gitForSnapshotTest(t, target, "rev-parse", "HEAD"))
			targetBefore := smokeTreeDigest(t, target, false)
			outsideBefore := smokeTreeDigest(t, outside, false)

			unit := func(name string) SnapshotUnitSpec {
				alias := filepath.Join(scratch, name, "alias")
				upper := filepath.Join(scratch, name, "upper")
				work := filepath.Join(scratch, name, "work")
				for _, dir := range []string{alias, upper, work} {
					if err := os.MkdirAll(dir, 0o700); err != nil {
						t.Fatal(err)
					}
				}
				return SnapshotUnitSpec{Target: target, Alias: alias, Upper: upper, Work: work}
			}
			artifactDir := filepath.Join(scratch, "artifact")
			spec := &SnapshotRunSpec{
				TaskID: "git-location-environment-" + tc.name,
				Units:  []SnapshotUnitSpec{unit("view")},
				Cwd:    target,
				GitArtifact: &SnapshotGitArtifactSpec{
					ArtifactDir: artifactDir,
					Baseline:    baseline,
					EnvRoot:     filepath.Join(scratch, "env"),
				},
			}
			wrap, err := BuildSnapshotWrap(bin, spec)
			if err != nil {
				t.Fatal(err)
			}
			payload := "printf 'changed\\n' > tracked.txt" +
				" && printf 'untracked\\n' > untracked.txt" +
				" && printf '\\000\\001\\002\\377' > binary.bin" +
				" && git add -A" +
				" && git commit -qm isolated"
			outsideGitDir := filepath.Join(outside, ".git")
			out, err := spawnWithSnapshotNamespacesEnv(t, snapshotSmokeEnvironment(tc.build(outsideGitDir, outside)...),
				bin, SnapshotWorkerSubcommand, wrap.SpecJSON, agent.SnapshotWorkerSep,
				"/bin/sh", "-c", payload)
			if err != nil {
				t.Fatalf("worker failed: %v\n%s", err, out)
			}
			if after := smokeTreeDigest(t, target, false); after != targetBefore {
				t.Fatalf("snapshot wrote through to pristine target\nbefore: %s\nafter:  %s", targetBefore, after)
			}
			if after := smokeTreeDigest(t, outside, false); after != outsideBefore {
				t.Fatalf("snapshot wrote through to externally selected Git metadata\nbefore: %s\nafter:  %s", outsideBefore, after)
			}

			clone := filepath.Join(scratch, "clone")
			gitForSnapshotTest(t, "", "clone", "-q", target, clone)
			gitForSnapshotTest(t, clone, "fetch", filepath.Join(artifactDir, "changes.bundle"), "HEAD:refs/heads/snapshot")
			gitForSnapshotTest(t, clone, "checkout", "-q", "snapshot")
			gitForSnapshotTest(t, clone, "fsck", "--no-dangling")
			if got := gitForSnapshotTest(t, clone, "show", "snapshot:tracked.txt"); got != "changed\n" {
				t.Fatalf("captured tracked content = %q, want changed", got)
			}
			if got := []byte(gitForSnapshotTest(t, clone, "show", "snapshot:binary.bin")); string(got) != string([]byte{0, 1, 2, 255}) {
				t.Fatalf("captured binary content = %v", got)
			}
		})
	}
}

// TestSnapshotWorkerCapturesLinkedWorktreeArtifactSmoke 验证 linked worktree
// 的工作树与外置 common Git metadata 都在同一个私有 mount namespace 内。它
// 覆盖 tracked、untracked、binary、ref 和 object 的写入，并确认 bundle 仍可在
// 独立 clone 中恢复。
func TestSnapshotWorkerCapturesLinkedWorktreeArtifactSmoke(t *testing.T) {
	bin := snapshotSmokeBinary(t)
	scratch := t.TempDir()
	base := filepath.Join(scratch, "base")
	linked := filepath.Join(scratch, "linked")
	if err := os.MkdirAll(base, 0o700); err != nil {
		t.Fatal(err)
	}
	gitForSnapshotTest(t, base, "init", "-q")
	gitForSnapshotTest(t, base, "config", "user.name", "Test")
	gitForSnapshotTest(t, base, "config", "user.email", "test@example.invalid")
	if err := os.WriteFile(filepath.Join(base, "tracked.txt"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitForSnapshotTest(t, base, "add", "tracked.txt")
	gitForSnapshotTest(t, base, "commit", "-qm", "base")
	gitForSnapshotTest(t, base, "worktree", "add", "-q", "--detach", linked)

	baseline := strings.TrimSpace(gitForSnapshotTest(t, linked, "rev-parse", "HEAD"))
	commonDir := strings.TrimSpace(gitForSnapshotTest(t, linked, "rev-parse", "--git-common-dir"))
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(linked, commonDir)
	}
	before := linkedGitFingerprint(t, linked, commonDir)

	unit := func(name, target string) SnapshotUnitSpec {
		alias := filepath.Join(scratch, name, "alias")
		upper := filepath.Join(scratch, name, "upper")
		work := filepath.Join(scratch, name, "work")
		for _, dir := range []string{alias, upper, work} {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
		}
		return SnapshotUnitSpec{Target: target, Alias: alias, Upper: upper, Work: work}
	}
	artifactDir := filepath.Join(scratch, "artifact")
	spec := &SnapshotRunSpec{
		TaskID: "linked-git-artifact-smoke",
		Units: []SnapshotUnitSpec{
			unit("worktree", linked),
			unit("common", commonDir),
		},
		Cwd: linked,
		GitArtifact: &SnapshotGitArtifactSpec{
			ArtifactDir: artifactDir,
			Baseline:    baseline,
			EnvRoot:     filepath.Join(scratch, "env"),
		},
	}
	wrap, err := BuildSnapshotWrap(bin, spec)
	if err != nil {
		t.Fatal(err)
	}
	payload := "printf 'changed\\n' > tracked.txt" +
		" && printf 'untracked\\n' > untracked.txt" +
		" && printf '\\000\\001\\002\\377' > binary.bin" +
		" && git add -A" +
		" && git commit -qm isolated" +
		" && git update-ref refs/heads/private-isolated HEAD"
	out, err := spawnWithSnapshotNamespaces(t,
		bin, SnapshotWorkerSubcommand, wrap.SpecJSON, agent.SnapshotWorkerSep,
		"/bin/sh", "-c", payload)
	if err != nil {
		t.Fatalf("worker failed: %v\n%s", err, out)
	}

	if after := linkedGitFingerprint(t, linked, commonDir); after != before {
		t.Fatalf("linked worktree snapshot wrote through to pristine Git state\nbefore: %s\nafter:  %s", before, after)
	}
	for _, name := range []string{"untracked.txt", "binary.bin"} {
		if _, err := os.Stat(filepath.Join(linked, name)); !os.IsNotExist(err) {
			t.Fatalf("worker write %s leaked into linked pristine worktree (stat err=%v)", name, err)
		}
	}
	if got, err := os.ReadFile(filepath.Join(linked, "tracked.txt")); err != nil || string(got) != "base\n" {
		t.Fatalf("linked pristine tracked file = %q, err=%v; want base content", got, err)
	}

	clone := filepath.Join(scratch, "clone")
	gitForSnapshotTest(t, "", "clone", "-q", base, clone)
	gitForSnapshotTest(t, clone, "fetch", filepath.Join(artifactDir, "changes.bundle"), "HEAD:refs/heads/snapshot")
	gitForSnapshotTest(t, clone, "checkout", "-q", "snapshot")
	gitForSnapshotTest(t, clone, "fsck", "--no-dangling")
	if got := gitForSnapshotTest(t, clone, "show", "snapshot:tracked.txt"); got != "changed\n" {
		t.Fatalf("captured linked tracked content = %q, want changed", got)
	}
	if got := gitForSnapshotTest(t, clone, "show", "snapshot:untracked.txt"); got != "untracked\n" {
		t.Fatalf("captured linked untracked content = %q, want untracked", got)
	}
	if got := []byte(gitForSnapshotTest(t, clone, "show", "snapshot:binary.bin")); string(got) != string([]byte{0, 1, 2, 255}) {
		t.Fatalf("captured linked binary content = %v", got)
	}
}

func TestSnapshotWorkerNestedUnitsUseIndependentUpperLayersSmoke(t *testing.T) {
	bin := snapshotSmokeBinary(t)
	scratch := t.TempDir()
	root := filepath.Join(scratch, "root")
	child := filepath.Join(root, "child")
	if err := os.MkdirAll(child, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "root-lower"), []byte("root\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(child, "child-lower"), []byte("child\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	unit := func(name, target string) SnapshotUnitSpec {
		alias := filepath.Join(scratch, name+"-alias")
		upper := filepath.Join(scratch, name+"-upper")
		work := filepath.Join(scratch, name+"-work")
		for _, dir := range []string{alias, upper, work} {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
		}
		return SnapshotUnitSpec{Target: target, Alias: alias, Upper: upper, Work: work}
	}
	rootUnit := unit("root", root)
	childUnit := unit("child", child)
	spec := &SnapshotRunSpec{
		TaskID: "nested-upper-smoke",
		Units:  []SnapshotUnitSpec{rootUnit, childUnit},
		Cwd:    child,
	}
	wrap, err := BuildSnapshotWrap(bin, spec)
	if err != nil {
		t.Fatal(err)
	}
	out, err := spawnWithSnapshotNamespaces(t,
		bin, SnapshotWorkerSubcommand, wrap.SpecJSON, agent.SnapshotWorkerSep,
		"/bin/sh", "-c", "printf child-write > child-write && printf root-write > ../root-write")
	if err != nil {
		t.Fatalf("worker failed: %v\\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(childUnit.Upper, "child-write")); err != nil {
		t.Fatalf("child write must land in child upper: %v", err)
	}
	if _, err := os.Stat(filepath.Join(rootUnit.Upper, "child", "child-write")); !os.IsNotExist(err) {
		t.Fatalf("child write leaked into parent upper, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(rootUnit.Upper, "root-write")); err != nil {
		t.Fatalf("root write must land in root upper: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "child-write")); !os.IsNotExist(err) {
		t.Fatalf("child write leaked into pristine root, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "root-write")); !os.IsNotExist(err) {
		t.Fatalf("root write leaked into pristine root, stat err=%v", err)
	}
}

func linkedGitFingerprint(t *testing.T, worktree, commonDir string) string {
	t.Helper()
	gitDir := strings.TrimSpace(gitForSnapshotTest(t, worktree, "rev-parse", "--absolute-git-dir"))
	return strings.Join([]string{
		"status=" + gitForSnapshotTest(t, worktree, "status", "--porcelain=v1", "--untracked-files=all"),
		"refs=" + gitForSnapshotTest(t, worktree, "show-ref", "--head"),
		"worktree=" + smokeTreeDigest(t, worktree, true),
		"git-dir=" + smokeTreeDigest(t, gitDir, false),
		"common-dir=" + smokeTreeDigest(t, commonDir, false),
	}, " | ")
}

func smokeTreeDigest(t *testing.T, root string, skipRootGitDir bool) string {
	t.Helper()
	hash := sha256.New()
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		if skipRootGitDir && path == filepath.Join(root, ".git") && entry.IsDir() {
			return filepath.SkipDir
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(hash, "%s\\x00%s\\x00", filepath.ToSlash(rel), entry.Type()); err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			_, err = fmt.Fprint(hash, target)
			return err
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		_, err = hash.Write(contents)
		return err
	})
	if err != nil {
		t.Fatalf("digest %s: %v", root, err)
	}
	return fmt.Sprintf("%x", hash.Sum(nil))
}

// TestSnapshotWorkerInterruptedAgentKeepsRecoverableArtifactSmoke covers the
// failure path: the wrapped agent exits non-zero after modifying the overlay,
// but the worker still captures a reviewable bundle and the task finalizer
// preserves its ledger-owned recovery record.
func TestSnapshotWorkerInterruptedAgentKeepsRecoverableArtifactSmoke(t *testing.T) {
	bin := snapshotSmokeBinary(t)
	scratch := t.TempDir()
	target := filepath.Join(scratch, "pristine-repo")
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
	gitForSnapshotTest(t, target, "init", "-q")
	gitForSnapshotTest(t, target, "config", "user.name", "Test")
	gitForSnapshotTest(t, target, "config", "user.email", "test@example.invalid")
	if err := os.WriteFile(filepath.Join(target, "tracked.txt"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitForSnapshotTest(t, target, "add", "tracked.txt")
	gitForSnapshotTest(t, target, "commit", "-qm", "base")
	baseline := strings.TrimSpace(gitForSnapshotTest(t, target, "rev-parse", "HEAD"))

	alias := filepath.Join(scratch, "alias")
	upper := filepath.Join(scratch, "upper")
	work := filepath.Join(scratch, "work")
	for _, dir := range []string{alias, upper, work} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	envRoot := filepath.Join(scratch, "env")
	artifactDir := SnapshotGitArtifactDir(envRoot, "interrupted")
	artifact := &SnapshotGitArtifactSpec{ArtifactDir: artifactDir, Baseline: baseline, EnvRoot: envRoot}
	entry := SnapshotLedgerEntry{
		TaskID:      "interrupted",
		IssueID:     "RUYI-845",
		Root:        envRoot,
		Primary:     target,
		Units:       []SnapshotUnitSpec{{Target: target, Alias: alias, Upper: upper, Work: work}},
		GitArtifact: artifact,
	}
	if err := RecordSnapshotStart(envRoot, entry); err != nil {
		t.Fatal(err)
	}
	spec := &SnapshotRunSpec{TaskID: entry.TaskID, Units: entry.Units, Cwd: target, GitArtifact: artifact}
	wrap, err := BuildSnapshotWrap(bin, spec)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := spawnWithSnapshotNamespaces(t, bin, SnapshotWorkerSubcommand, wrap.SpecJSON, agent.SnapshotWorkerSep,
		"/bin/sh", "-c", "printf 'interrupted\\n' > tracked.txt && kill -TERM $$"); err == nil {
		t.Fatalf("interrupted agent unexpectedly succeeded: %s", out)
	}
	if err := FinalizeSnapshot(envRoot, entry.TaskID, true); err != nil {
		t.Fatal(err)
	}

	if got := gitForSnapshotTest(t, target, "status", "--porcelain"); got != "" {
		t.Fatalf("interrupted snapshot must leave pristine repository clean, status=%q", got)
	}
	clone := filepath.Join(scratch, "clone")
	gitForSnapshotTest(t, "", "clone", "-q", target, clone)
	gitForSnapshotTest(t, clone, "fetch", filepath.Join(artifactDir, "changes.bundle"), "HEAD:refs/heads/interrupted")
	gitForSnapshotTest(t, clone, "checkout", "-q", "interrupted")
	gitForSnapshotTest(t, clone, "fsck", "--no-dangling")
	if got := gitForSnapshotTest(t, clone, "show", "interrupted:tracked.txt"); got != "interrupted\n" {
		t.Fatalf("recovered tracked content = %q, want interrupted write", got)
	}

	entries := readLedgerEntries(t, envRoot)
	if len(entries) != 1 || entries[0].State != snapshotStateFailed || entries[0].GitArtifact == nil {
		t.Fatalf("interrupted task must retain failed recovery ledger entry, got %+v", entries)
	}
	if _, err := os.Stat(upper); err != nil {
		t.Fatalf("interrupted task must retain its overlay upperdir: %v", err)
	}
}
