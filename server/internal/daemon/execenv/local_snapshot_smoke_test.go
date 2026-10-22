//go:build linux

package execenv

import (
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
	t.Helper()
	cmd := exec.Command(argv[0], argv[1:]...)
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Cloneflags = syscall.CLONE_NEWUSER | syscall.CLONE_NEWNS
	cmd.SysProcAttr.UidMappings = []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Getuid(), Size: 1}}
	cmd.SysProcAttr.GidMappings = []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Getgid(), Size: 1}}
	cmd.Env = append(os.Environ(), "HOME="+os.TempDir())
	return cmd.CombinedOutput()
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
