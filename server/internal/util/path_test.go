package util

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveSymlinksBestEffortRetainsMissingDescendants(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "target")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	resolved, err := ResolveSymlinksBestEffort(filepath.Join(link, "missing", "child"))
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(target, "missing", "child"); resolved != want {
		t.Fatalf("resolved path = %q, want %q", resolved, want)
	}
}
