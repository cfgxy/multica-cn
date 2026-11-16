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
	"time"

	"github.com/multica-ai/multica/server/pkg/agent"
	"golang.org/x/sys/unix"
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
	mainBranch := strings.TrimSpace(gitForSnapshotTest(t, target, "branch", "--show-current"))
	gitForSnapshotTest(t, target, "switch", "-q", "-c", "host-secret")
	if err := os.WriteFile(filepath.Join(target, "secret.txt"), []byte("must not enter snapshot\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitForSnapshotTest(t, target, "add", "secret.txt")
	gitForSnapshotTest(t, target, "commit", "-qm", "host secret")
	secret := strings.TrimSpace(gitForSnapshotTest(t, target, "rev-parse", "HEAD"))
	gitForSnapshotTest(t, target, "switch", "-q", mainBranch)
	// These local settings must not enter the private repository. In particular,
	// copying either command-valued config or autocrlf after checkout makes the
	// worker's behavior depend on mutable host configuration.
	gitForSnapshotTest(t, target, "config", "diff.host-secret.command", "/bin/false")
	gitForSnapshotTest(t, target, "config", "core.autocrlf", "true")
	gitForSnapshotTest(t, target, "reset", "--hard", "-q", baseline)
	pristineRefs := gitForSnapshotTest(t, target, "show-ref", "--head")
	if err := os.WriteFile(filepath.Join(target, "tracked.txt"), []byte("host-dirty\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "host-untracked.txt"), []byte("must not enter snapshot\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	pristineStatus := gitForSnapshotTest(t, target, "status", "--porcelain=v1", "--untracked-files=all")

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
	payload := "test \"$(cat tracked.txt)\" = 'base'" +
		" && test ! -e host-untracked.txt" +
		" && ! git show-ref --verify --quiet refs/heads/host-secret" +
		" && ! git cat-file -e " + secret + "^{commit}" +
		" && test -z \"$(git remote)\"" +
		" && ! git config --local --get diff.host-secret.command" +
		" && ! git config --local --get core.autocrlf" +
		" && printf 'changed\\n' > tracked.txt" +
		" && printf 'untracked\\n' > untracked.txt" +
		" && printf '\\000\\001\\002\\377' > binary.bin"
	out, err := spawnWithSnapshotNamespaces(t,
		bin, SnapshotWorkerSubcommand, wrap.SpecJSON, agent.SnapshotWorkerSep,
		"/bin/sh", "-c", payload)
	if err != nil {
		t.Fatalf("worker failed: %v\n%s", err, out)
	}

	if got, err := os.ReadFile(filepath.Join(target, "tracked.txt")); err != nil || string(got) != "host-dirty\n" {
		t.Fatalf("host tracked file = %q, err=%v; snapshot must not rewrite it", got, err)
	}
	for _, name := range []string{"untracked.txt", "binary.bin"} {
		if _, err := os.Stat(filepath.Join(target, name)); !os.IsNotExist(err) {
			t.Fatalf("worker write %s leaked into pristine (stat err=%v)", name, err)
		}
	}
	if got := gitForSnapshotTest(t, target, "status", "--porcelain=v1", "--untracked-files=all"); got != pristineStatus {
		t.Fatalf("host repository status changed, before=%q after=%q", pristineStatus, got)
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
	if got, err := os.ReadFile(filepath.Join(alias, "tracked.txt")); err != nil || string(got) != "base\n" {
		t.Fatalf("private Git lower must remain available for ledger cleanup, got %q err=%v", got, err)
	}
	if _, err := os.Stat(filepath.Join(alias, ".git")); err != nil {
		t.Fatalf("private Git lower metadata must be materialized: %v", err)
	}
	if _, err := os.Stat(filepath.Join(alias, "host-untracked.txt")); !os.IsNotExist(err) {
		t.Fatalf("host untracked content entered private lower, stat err=%v", err)
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
		var identity unix.Stat_t
		if err := unix.Stat(target, &identity); err != nil {
			t.Fatal(err)
		}
		return SnapshotUnitSpec{
			Target:            target,
			TargetDevice:      uint64(identity.Dev),
			TargetInode:       uint64(identity.Ino),
			TargetIdentitySet: true,
			Alias:             alias,
			Upper:             upper,
			Work:              work,
		}
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

func TestSnapshotWorkerCapturesSHA256GitArtifactSmoke(t *testing.T) {
	bin := snapshotSmokeBinary(t)
	scratch := t.TempDir()
	repo := filepath.Join(scratch, "sha256-repo")
	if err := os.MkdirAll(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	gitForSnapshotTest(t, repo, "init", "--object-format=sha256", "-q")
	gitForSnapshotTest(t, repo, "config", "user.name", "Test")
	gitForSnapshotTest(t, repo, "config", "user.email", "test@example.invalid")
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitForSnapshotTest(t, repo, "add", "tracked.txt")
	gitForSnapshotTest(t, repo, "commit", "-qm", "base")
	baseline := strings.TrimSpace(gitForSnapshotTest(t, repo, "rev-parse", "HEAD"))
	if len(baseline) != 64 {
		t.Fatalf("SHA-256 baseline = %q, want 64 hex characters", baseline)
	}

	alias, upper, work := filepath.Join(scratch, "alias"), filepath.Join(scratch, "upper"), filepath.Join(scratch, "work")
	for _, path := range []string{alias, upper, work} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	artifactDir := filepath.Join(scratch, "artifact")
	spec := &SnapshotRunSpec{
		TaskID: "sha256-git-artifact",
		Units:  []SnapshotUnitSpec{{Target: repo, Alias: alias, Upper: upper, Work: work}},
		Cwd:    repo,
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
	payload := "test \"$(git rev-parse --show-object-format)\" = sha256" +
		" && test \"$(git rev-parse HEAD)\" = " + baseline +
		" && test \"$(cat tracked.txt)\" = base" +
		" && printf 'changed\\n' > tracked.txt && git add tracked.txt && git commit -qm isolated"
	out, err := spawnWithSnapshotNamespaces(t,
		bin, SnapshotWorkerSubcommand, wrap.SpecJSON, agent.SnapshotWorkerSep,
		"/bin/sh", "-c", payload)
	if err != nil {
		t.Fatalf("SHA-256 snapshot worker failed: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(gitForSnapshotTest(t, alias, "rev-parse", "--show-object-format")); got != "sha256" {
		t.Fatalf("private Git lower object format = %q, want sha256", got)
	}
	if got := strings.TrimSpace(gitForSnapshotTest(t, alias, "rev-parse", "HEAD")); got != baseline {
		t.Fatalf("private Git lower baseline = %q, want %s", got, baseline)
	}

	clone := filepath.Join(scratch, "clone")
	gitForSnapshotTest(t, "", "clone", "-q", repo, clone)
	gitForSnapshotTest(t, clone, "fetch", filepath.Join(artifactDir, "changes.bundle"), "HEAD:refs/heads/snapshot")
	gitForSnapshotTest(t, clone, "checkout", "-q", "snapshot")
	gitForSnapshotTest(t, clone, "fsck", "--no-dangling")
	if got := gitForSnapshotTest(t, clone, "show", "snapshot:tracked.txt"); got != "changed\n" {
		t.Fatalf("SHA-256 artifact tracked content = %q, want changed", got)
	}
}

// TestSnapshotWorkerDropsExternalHooksPathSmoke proves a host Git template
// is neither imported nor executed by the private snapshot repository.
func TestSnapshotWorkerDropsExternalHooksPathSmoke(t *testing.T) {
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

	template := filepath.Join(scratch, "git-template")
	hooks := filepath.Join(template, "hooks")
	if err := os.MkdirAll(hooks, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(template, "sentinel"), []byte("must remain untouched\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	hook := "#!/bin/sh\nprintf hook-ran > " + filepath.Join(template, "hook-ran") + "\n"
	if err := os.WriteFile(filepath.Join(hooks, "pre-commit"), []byte(hook), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_TEMPLATE_DIR", template)
	before := smokeTreeDigest(t, template, false)

	alias, upper, work := filepath.Join(scratch, "alias"), filepath.Join(scratch, "upper"), filepath.Join(scratch, "work")
	for _, path := range []string{alias, upper, work} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	spec := &SnapshotRunSpec{
		TaskID: "drop-external-hooks-path",
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
		"/bin/sh", "-c", "! git config --local --get core.hooksPath && test ! -e .git/hooks/pre-commit && test ! -e "+filepath.Join(alias, ".git", "hooks", "pre-commit")+" && printf changed > tracked.txt && git add tracked.txt && git commit -qm isolated")
	if err != nil {
		t.Fatalf("worker must isolate external Git template: %v\n%s", err, out)
	}
	if after := smokeTreeDigest(t, template, false); after != before {
		t.Fatalf("external template hook wrote outside snapshot\nbefore: %s\nafter:  %s", before, after)
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

// TestReviewLinkedWorktreeWithShallowerCommonDir fixes the Git-root selection
// regression where mountOrder chose a shallow linked-worktree common directory
// before the deeper primary worktree unit.
func TestReviewLinkedWorktreeWithShallowerCommonDir(t *testing.T) {
	bin := snapshotSmokeBinary(t)
	scratch := t.TempDir()
	base := filepath.Join(scratch, "repo")
	linked := filepath.Join(scratch, "nested", "deep", "linked")
	if err := os.MkdirAll(filepath.Dir(linked), 0o700); err != nil {
		t.Fatal(err)
	}
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
	if strings.Count(filepath.Clean(commonDir), string(filepath.Separator)) >= strings.Count(filepath.Clean(linked), string(filepath.Separator)) {
		t.Fatalf("test topology is not shallow-common: common=%s linked=%s", commonDir, linked)
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
		TaskID: "review-shallow-common-linked-worktree",
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
	payload := "printf 'changed\\n' > tracked.txt && git add -A && git commit -qm isolated"
	out, err := spawnWithSnapshotNamespaces(t,
		bin, SnapshotWorkerSubcommand, wrap.SpecJSON, agent.SnapshotWorkerSep,
		"/bin/sh", "-c", payload)
	if err != nil {
		t.Fatalf("worker failed for shallow common linked worktree: %v\\n%s", err, out)
	}
	if after := linkedGitFingerprint(t, linked, commonDir); after != before {
		t.Fatalf("shallow-common linked worktree wrote through to pristine Git state\\nbefore: %s\\nafter:  %s", before, after)
	}
	if got, err := os.ReadFile(filepath.Join(linked, "tracked.txt")); err != nil || string(got) != "base\n" {
		t.Fatalf("linked pristine tracked file = %q, err=%v; want base content", got, err)
	}

	clone := filepath.Join(scratch, "clone")
	gitForSnapshotTest(t, "", "clone", "-q", base, clone)
	gitForSnapshotTest(t, clone, "fetch", filepath.Join(artifactDir, "changes.bundle"), "HEAD:refs/heads/snapshot")
	gitForSnapshotTest(t, clone, "checkout", "-q", "snapshot")
	if got := gitForSnapshotTest(t, clone, "show", "snapshot:tracked.txt"); got != "changed\n" {
		t.Fatalf("captured shallow-common tracked content = %q, want changed", got)
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

func TestSnapshotWorkerConcurrentViewsRemainPinnedAcrossHostGitOperationsSmoke(t *testing.T) {
	bin := snapshotSmokeBinary(t)
	scratch := t.TempDir()
	repo := filepath.Join(scratch, "host")
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
	gitForSnapshotTest(t, repo, "commit", "-qm", "baseline")
	baseline := strings.TrimSpace(gitForSnapshotTest(t, repo, "rev-parse", "HEAD"))
	mainBranch := strings.TrimSpace(gitForSnapshotTest(t, repo, "branch", "--show-current"))

	remote := filepath.Join(scratch, "remote.git")
	gitForSnapshotTest(t, "", "init", "--bare", "-q", remote)
	gitForSnapshotTest(t, repo, "remote", "add", "origin", remote)
	gitForSnapshotTest(t, repo, "push", "-q", "-u", "origin", mainBranch)

	type worker struct {
		name        string
		unit        SnapshotUnitSpec
		envRoot     string
		artifactDir string
		ready       string
		release     string
		outputDir   string
		done        chan snapshotSmokeWorkerResult
	}
	control := filepath.Join(scratch, "control")
	if err := os.MkdirAll(control, 0o700); err != nil {
		t.Fatal(err)
	}
	newWorker := func(name string) *worker {
		root := filepath.Join(scratch, name)
		envRoot := filepath.Join(root, "env")
		alias, upper, work := PlanSnapshotTree(envRoot, name)
		for _, dir := range []string{alias, upper, work} {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
		}
		artifactDir := SnapshotGitArtifactDir(envRoot, name)
		unit := SnapshotUnitSpec{Target: repo, Alias: alias, Upper: upper, Work: work}
		return &worker{
			name:        name,
			unit:        unit,
			envRoot:     envRoot,
			artifactDir: artifactDir,
			ready:       filepath.Join(control, name+"-ready"),
			release:     filepath.Join(control, name+"-release"),
			outputDir:   control,
			done:        make(chan snapshotSmokeWorkerResult, 1),
		}
	}
	workers := []*worker{newWorker("alpha"), newWorker("walkthrough")}

	for _, w := range workers {
		artifact := &SnapshotGitArtifactSpec{ArtifactDir: w.artifactDir, Baseline: baseline, EnvRoot: w.envRoot}
		if err := RecordSnapshotStart(w.envRoot, SnapshotLedgerEntry{
			TaskID:      w.name,
			IssueID:     "RUYI-942",
			Root:        w.envRoot,
			Primary:     repo,
			Units:       []SnapshotUnitSpec{w.unit},
			GitArtifact: artifact,
		}); err != nil {
			t.Fatal(err)
		}
		spec := &SnapshotRunSpec{
			TaskID:      w.name,
			Units:       []SnapshotUnitSpec{w.unit},
			Cwd:         repo,
			GitArtifact: artifact,
		}
		wrap, err := BuildSnapshotWrap(bin, spec)
		if err != nil {
			t.Fatal(err)
		}
		payload := snapshotSmokeProbePayload(w.ready, w.release, w.outputDir, w.name, 6)
		go func(name, specJSON string, done chan<- snapshotSmokeWorkerResult) {
			cmd := exec.Command(bin, SnapshotWorkerSubcommand, specJSON, agent.SnapshotWorkerSep, "/bin/sh", "-c", payload)
			cmd.SysProcAttr = &syscall.SysProcAttr{
				Cloneflags:  syscall.CLONE_NEWUSER | syscall.CLONE_NEWNS,
				UidMappings: []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Getuid(), Size: 1}},
				GidMappings: []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Getgid(), Size: 1}},
			}
			cmd.Env = snapshotSmokeEnvironment()
			out, err := cmd.CombinedOutput()
			done <- snapshotSmokeWorkerResult{name: name, out: out, err: err}
		}(w.name, wrap.SpecJSON, w.done)
	}
	for _, w := range workers {
		waitForSnapshotSmokeFile(t, w.ready)
	}

	assertProbe := func(stage int) {
		for _, w := range workers {
			if err := os.WriteFile(filepath.Join(control, fmt.Sprintf("probe-%d", stage)), []byte("go\n"), 0o600); err != nil && !os.IsExist(err) {
				t.Fatal(err)
			}
			waitForSnapshotSmokeFile(t, filepath.Join(control, fmt.Sprintf("%s-probe-%d", w.name, stage)))
			got, err := os.ReadFile(filepath.Join(control, fmt.Sprintf("%s-probe-%d", w.name, stage)))
			if err != nil {
				t.Fatal(err)
			}
			want := fmt.Sprintf("head=%s\ntracked=base\npulled=absent\n", baseline)
			if string(got) != want {
				t.Fatalf("%s view drifted after %s\ngot:\n%s\nwant:\n%s", w.name, snapshotSmokeStageName(stage), got, want)
			}
		}
	}

	assertProbe(0)
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("host-uncommitted\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	assertProbe(1)

	gitForSnapshotTest(t, repo, "add", "tracked.txt")
	gitForSnapshotTest(t, repo, "commit", "-qm", "host commit")
	gitForSnapshotTest(t, repo, "push", "-q", "origin", mainBranch)
	assertProbe(2)

	gitForSnapshotTest(t, repo, "switch", "-q", "-c", "host-alt")
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("host-switch\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitForSnapshotTest(t, repo, "add", "tracked.txt")
	gitForSnapshotTest(t, repo, "commit", "-qm", "host switch")
	assertProbe(3)

	gitForSnapshotTest(t, repo, "switch", "-q", mainBranch)
	updater := filepath.Join(scratch, "updater")
	gitForSnapshotTest(t, "", "clone", "-q", remote, updater)
	gitForSnapshotTest(t, updater, "config", "user.name", "Test")
	gitForSnapshotTest(t, updater, "config", "user.email", "test@example.invalid")
	if err := os.WriteFile(filepath.Join(updater, "pulled.txt"), []byte("remote\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitForSnapshotTest(t, updater, "add", "pulled.txt")
	gitForSnapshotTest(t, updater, "commit", "-qm", "remote fast forward")
	gitForSnapshotTest(t, updater, "push", "-q", "origin", mainBranch)
	gitForSnapshotTest(t, repo, "pull", "-q", "--ff-only", "origin", mainBranch)
	assertProbe(4)

	gitForSnapshotTest(t, repo, "reset", "--hard", "-q", baseline)
	assertProbe(5)

	for _, w := range workers {
		if err := os.WriteFile(w.release, []byte("release\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, w := range workers {
		select {
		case result := <-w.done:
			if result.err != nil {
				t.Fatalf("%s worker failed: %v\n%s", result.name, result.err, result.out)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("%s worker did not finish", w.name)
		}
	}

	if got := strings.TrimSpace(gitForSnapshotTest(t, repo, "rev-parse", "HEAD")); got != baseline {
		t.Fatalf("host reset head = %s, want baseline %s", got, baseline)
	}
	if got := gitForSnapshotTest(t, repo, "status", "--porcelain"); got != "" {
		t.Fatalf("host repository must remain clean, status=%q", got)
	}
	for _, w := range workers {
		clone := filepath.Join(scratch, "clone-"+w.name)
		gitForSnapshotTest(t, "", "clone", "-q", repo, clone)
		gitForSnapshotTest(t, clone, "fetch", w.artifactDir+"/changes.bundle", "HEAD:refs/heads/snapshot")
		if parent := strings.TrimSpace(gitForSnapshotTest(t, clone, "rev-parse", "snapshot^")); parent != baseline {
			t.Fatalf("%s artifact parent = %s, want baseline %s", w.name, parent, baseline)
		}
		if got := gitForSnapshotTest(t, clone, "show", "snapshot:task-result.txt"); got != w.name+"\n" {
			t.Fatalf("%s artifact output = %q", w.name, got)
		}
		if err := FinalizeSnapshot(w.envRoot, w.name, false); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Dir(w.unit.Alias)); !os.IsNotExist(err) {
			t.Fatalf("%s overlay tree retained after finalization: %v", w.name, err)
		}
		if _, err := os.Stat(w.artifactDir); err != nil {
			t.Fatalf("%s delivered artifact missing after finalization: %v", w.name, err)
		}
		ReapExpiredSnapshots(w.envRoot, time.Now().Add(snapshotFailedRetention))
		if _, err := os.Stat(w.artifactDir); !os.IsNotExist(err) {
			t.Fatalf("%s delivered artifact retained after expiry: %v", w.name, err)
		}
	}
}

type snapshotSmokeWorkerResult struct {
	name string
	out  []byte
	err  error
}

func snapshotSmokeProbePayload(ready, release, outputDir, name string, stages int) string {
	var script strings.Builder
	fmt.Fprintf(&script, "printf ready > %s; ", ready)
	for stage := 0; stage < stages; stage++ {
		fmt.Fprintf(&script, "while [ ! -e %s/probe-%d ]; do sleep 0.01; done; ", outputDir, stage)
		fmt.Fprintf(&script, "{ printf 'head='; git rev-parse HEAD; printf 'tracked='; tr -d '\\n' < tracked.txt; printf '\\npulled='; if [ -e pulled.txt ]; then printf present; else printf absent; fi; printf '\\n'; } > %s/%s-probe-%d.tmp; mv %s/%s-probe-%d.tmp %s/%s-probe-%d; ", outputDir, name, stage, outputDir, name, stage, outputDir, name, stage)
	}
	fmt.Fprintf(&script, "while [ ! -e %s ]; do sleep 0.01; done; printf '%s\\n' > task-result.txt", release, name)
	return script.String()
}

func waitForSnapshotSmokeFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		} else if !os.IsNotExist(err) {
			t.Fatalf("stat %s: %v", path, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}

func snapshotSmokeStageName(stage int) string {
	return []string{"initial snapshot", "host uncommitted write", "host commit", "host switch", "host fast-forward pull", "host forced reset"}[stage]
}
