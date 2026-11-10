//go:build linux

package daemon

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestWalkGitSnapshotTreeRejectsBindMountBoundary(t *testing.T) {
	root := t.TempDir()
	mountpoint := filepath.Join(root, "nested")
	if err := os.Mkdir(mountpoint, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mount(root, mountpoint, "", unix.MS_BIND, ""); err != nil {
		if errors.Is(err, unix.EPERM) || errors.Is(err, unix.EACCES) {
			t.Skipf("bind mounts unavailable: %v", err)
		}
		t.Fatalf("bind mount: %v", err)
	}
	defer func() { _ = unix.Unmount(mountpoint, unix.MNT_DETACH) }()

	ctx := context.Background()
	budget := newGitSnapshotAuditBudget(ctx)
	err := walkGitSnapshotTree(ctx, root, budget, func(string, fs.DirEntry) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "crosses a mount boundary") {
		t.Fatalf("bind mount boundary must fail closed, got %v", err)
	}
}
