package execenv

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Snapshot-mode brief injection (RUYI-669 phase-2 increment): the daemon
// composes the brief into the task's overlay upper layer instead of the
// host-side workdir the view covers. Two risk surfaces are pinned here:
//
//   - crash leak: the pristine directory must keep zero transient files, so
//     a daemon crash between inject and cleanup cannot leave the brief in
//     the user's tree;
//   - concurrent clobber: two tasks rooted at the same directory each write
//     their own upper, so there is no shared host file to overwrite.

func TestInjectRuntimeConfigSnapshotWritesUpperHostUntouched(t *testing.T) {
	t.Parallel()
	viewDir := t.TempDir()
	upperDir := t.TempDir()
	const userContent = "# User-authored AGENTS.md\n\ndon't touch this\n"
	hostPath := filepath.Join(viewDir, "AGENTS.md")
	if err := os.WriteFile(hostPath, []byte(userContent), 0o644); err != nil {
		t.Fatalf("seed pristine: %v", err)
	}

	content, err := InjectRuntimeConfigSnapshot(viewDir, upperDir, "codex", TaskContextForEnv{
		IssueID: "11111111-2222-3333-4444-555555555555",
	})
	if err != nil {
		t.Fatalf("InjectRuntimeConfigSnapshot: %v", err)
	}
	if content == "" {
		t.Fatalf("returned brief content must be non-empty")
	}

	upper, err := os.ReadFile(filepath.Join(upperDir, "AGENTS.md"))
	if err != nil {
		t.Fatalf("read upper AGENTS.md: %v", err)
	}
	s := string(upper)
	if !strings.HasPrefix(s, userContent) {
		t.Errorf("upper file must carry the pristine user content verbatim at the top, got:\n%s", s)
	}
	if !strings.Contains(s, runtimeMarkerBegin) || !strings.Contains(s, runtimeMarkerEnd) {
		t.Errorf("upper file must contain the runtime marker block, got:\n%s", s)
	}

	// Crash-leak surface: the pristine file the view covers must be
	// byte-identical to its pre-inject state.
	got, err := os.ReadFile(hostPath)
	if err != nil {
		t.Fatalf("read pristine AGENTS.md: %v", err)
	}
	if string(got) != userContent {
		t.Errorf("pristine AGENTS.md must stay byte-identical, got:\n%s", string(got))
	}
	// And the host directory must gain no transient files at all.
	entries, err := os.ReadDir(viewDir)
	if err != nil {
		t.Fatalf("read pristine dir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "AGENTS.md" {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("pristine dir must keep zero transient files, got %v", names)
	}
}

func TestReadRuntimeConfigFilePrefersSnapshotUpperAndFallsBackToView(t *testing.T) {
	t.Parallel()
	viewDir := t.TempDir()
	upperDir := t.TempDir()
	const viewContent = "view instructions"
	const upperContent = "upper instructions"
	if err := os.WriteFile(filepath.Join(viewDir, "AGENTS.md"), []byte(viewContent), 0o644); err != nil {
		t.Fatalf("write view config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(upperDir, "AGENTS.md"), []byte(upperContent), 0o644); err != nil {
		t.Fatalf("write upper config: %v", err)
	}

	got, err := ReadRuntimeConfigFile(viewDir, upperDir, "codex")
	if err != nil || string(got) != upperContent {
		t.Fatalf("read snapshot upper = %q, %v; want %q", got, err, upperContent)
	}
	if err := os.Remove(filepath.Join(upperDir, "AGENTS.md")); err != nil {
		t.Fatalf("remove upper config: %v", err)
	}
	got, err = ReadRuntimeConfigFile(viewDir, upperDir, "codex")
	if err != nil || string(got) != viewContent {
		t.Fatalf("read view fallback = %q, %v; want %q", got, err, viewContent)
	}
}

func TestInjectRuntimeConfigSnapshotNoBaseCreatesUpperOnly(t *testing.T) {
	t.Parallel()
	viewDir := t.TempDir()
	upperDir := t.TempDir()

	if _, err := InjectRuntimeConfigSnapshot(viewDir, upperDir, "codex", TaskContextForEnv{
		IssueID: "11111111-2222-3333-4444-555555555555",
	}); err != nil {
		t.Fatalf("InjectRuntimeConfigSnapshot: %v", err)
	}

	upper, err := os.ReadFile(filepath.Join(upperDir, "AGENTS.md"))
	if err != nil {
		t.Fatalf("read upper AGENTS.md: %v", err)
	}
	s := string(upper)
	if !strings.HasPrefix(s, runtimeMarkerBegin) {
		t.Errorf("created-from-scratch upper file must start with the marker block, got:\n%s", s)
	}
	entries, err := os.ReadDir(viewDir)
	if err != nil {
		t.Fatalf("read pristine dir: %v", err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("pristine dir must stay empty, got %v", names)
	}
}

// Concurrent root-domain tasks share the pristine directory but own disjoint
// upper trees; each brief must compose into its own upper with its own
// identifying content, and neither may touch the host file or the sibling.
func TestInjectRuntimeConfigSnapshotConcurrentRootTasksIsolated(t *testing.T) {
	t.Parallel()
	viewDir := t.TempDir()
	upperA := t.TempDir()
	upperB := t.TempDir()

	briefA, err := InjectRuntimeConfigSnapshot(viewDir, upperA, "codex", TaskContextForEnv{
		IssueID:   "11111111-2222-3333-4444-555555555555",
		AgentName: "Agent A",
	})
	if err != nil {
		t.Fatalf("inject A: %v", err)
	}
	briefB, err := InjectRuntimeConfigSnapshot(viewDir, upperB, "codex", TaskContextForEnv{
		IssueID:   "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		AgentName: "Agent B",
	})
	if err != nil {
		t.Fatalf("inject B: %v", err)
	}
	if briefA == briefB {
		t.Fatalf("the two briefs must differ for the isolation assertions to mean anything")
	}

	fileA, err := os.ReadFile(filepath.Join(upperA, "AGENTS.md"))
	if err != nil {
		t.Fatalf("read upper A: %v", err)
	}
	fileB, err := os.ReadFile(filepath.Join(upperB, "AGENTS.md"))
	if err != nil {
		t.Fatalf("read upper B: %v", err)
	}
	if string(fileA) == string(fileB) {
		t.Fatalf("upper A and upper B must carry task-specific briefs, got identical bytes")
	}
	if !strings.Contains(string(fileA), "Agent A") || strings.Contains(string(fileA), "Agent B") {
		t.Errorf("upper A must carry only task A's brief, got:\n%s", string(fileA))
	}
	if !strings.Contains(string(fileB), "Agent B") || strings.Contains(string(fileB), "Agent A") {
		t.Errorf("upper B must carry only task B's brief, got:\n%s", string(fileB))
	}
	entries, err := os.ReadDir(viewDir)
	if err != nil {
		t.Fatalf("read pristine dir: %v", err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("concurrent injections must leave the shared pristine dir empty, got %v", names)
	}
}

// A retry re-injects into the same upper; the block must be replaced in place
// instead of stacking a second copy, mirroring writeRuntimeConfigFile.
func TestInjectRuntimeConfigSnapshotReinjectReplacesOwnBlock(t *testing.T) {
	t.Parallel()
	viewDir := t.TempDir()
	upperDir := t.TempDir()
	ctx := TaskContextForEnv{IssueID: "11111111-2222-3333-4444-555555555555"}

	if _, err := InjectRuntimeConfigSnapshot(viewDir, upperDir, "codex", ctx); err != nil {
		t.Fatalf("first inject: %v", err)
	}
	if _, err := InjectRuntimeConfigSnapshot(viewDir, upperDir, "codex", ctx); err != nil {
		t.Fatalf("second inject: %v", err)
	}

	upper, err := os.ReadFile(filepath.Join(upperDir, "AGENTS.md"))
	if err != nil {
		t.Fatalf("read upper: %v", err)
	}
	if n := strings.Count(string(upper), runtimeMarkerBegin); n != 1 {
		t.Errorf("re-inject must replace the block in place, found %d begin markers:\n%s", n, string(upper))
	}
}

// A pristine file carrying a stale marker block (e.g. left by an earlier
// in-place run) composes into the upper with the block refreshed; the
// pristine file itself stays untouched.
func TestInjectRuntimeConfigSnapshotRefreshesStaleBlockInUpperOnly(t *testing.T) {
	t.Parallel()
	viewDir := t.TempDir()
	upperDir := t.TempDir()
	stale := "# User rules\n\n" + runtimeMarkerBegin + "\nstale brief\n" + runtimeMarkerEnd + "\n"
	hostPath := filepath.Join(viewDir, "AGENTS.md")
	if err := os.WriteFile(hostPath, []byte(stale), 0o644); err != nil {
		t.Fatalf("seed pristine: %v", err)
	}

	if _, err := InjectRuntimeConfigSnapshot(viewDir, upperDir, "codex", TaskContextForEnv{
		IssueID: "11111111-2222-3333-4444-555555555555",
	}); err != nil {
		t.Fatalf("InjectRuntimeConfigSnapshot: %v", err)
	}

	upper, err := os.ReadFile(filepath.Join(upperDir, "AGENTS.md"))
	if err != nil {
		t.Fatalf("read upper: %v", err)
	}
	s := string(upper)
	if strings.Contains(s, "stale brief") {
		t.Errorf("upper file must replace the stale block, got:\n%s", s)
	}
	if !strings.HasPrefix(s, "# User rules") || strings.Count(s, runtimeMarkerBegin) != 1 {
		t.Errorf("upper file must keep user content and exactly one fresh block, got:\n%s", s)
	}
	got, err := os.ReadFile(hostPath)
	if err != nil {
		t.Fatalf("read pristine: %v", err)
	}
	if string(got) != stale {
		t.Errorf("pristine file with stale block must stay untouched, got:\n%s", string(got))
	}
}

func TestInjectRuntimeConfigSnapshotUnknownProviderWritesNothing(t *testing.T) {
	t.Parallel()
	viewDir := t.TempDir()
	upperDir := t.TempDir()

	content, err := InjectRuntimeConfigSnapshot(viewDir, upperDir, "deerflow", TaskContextForEnv{
		IssueID: "11111111-2222-3333-4444-555555555555",
	})
	if err != nil {
		t.Fatalf("InjectRuntimeConfigSnapshot: %v", err)
	}
	if content == "" {
		t.Fatalf("brief content must still be returned for prompt-only mode")
	}
	for _, dir := range []string{viewDir, upperDir} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			t.Fatalf("read %s: %v", dir, err)
		}
		if len(entries) != 0 {
			names := make([]string, 0, len(entries))
			for _, e := range entries {
				names = append(names, e.Name())
			}
			t.Errorf("unknown provider must not write files in %s, got %v", dir, names)
		}
	}
}
