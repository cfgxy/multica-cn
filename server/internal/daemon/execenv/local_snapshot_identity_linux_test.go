//go:build linux

package execenv

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestOpenSnapshotTargetRemainsAnchoredAfterPathReplacement(t *testing.T) {
	target := filepath.Join(t.TempDir(), "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	originalInfo, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}

	opened, err := openSnapshotTarget(&SnapshotUnitSpec{Target: target})
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()

	replaced := target + ".replaced"
	if err := os.Rename(target, replaced); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "sentinel"), []byte("replacement\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	currentInfo, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(originalInfo, currentInfo) {
		t.Fatal("replacement unexpectedly retained the admitted directory identity")
	}
	anchoredInfo, err := os.Stat(filepath.Join("/proc/self/fd", fmt.Sprint(opened.Fd())))
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(originalInfo, anchoredInfo) {
		t.Fatalf("opened target followed replacement: anchored=%v original=%v", anchoredInfo, originalInfo)
	}
}

func TestOpenSnapshotTargetRejectsIdentityMismatch(t *testing.T) {
	target := filepath.Join(t.TempDir(), "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	var admitted unix.Stat_t
	if err := unix.Stat(target, &admitted); err != nil {
		t.Fatal(err)
	}

	replaced := target + ".replaced"
	if err := os.Rename(target, replaced); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := openSnapshotTarget(&SnapshotUnitSpec{
		Target:            target,
		TargetDevice:      uint64(admitted.Dev),
		TargetInode:       uint64(admitted.Ino),
		TargetIdentitySet: true,
	}); err == nil {
		t.Fatal("replaced target must fail identity validation")
	}
}

func TestSnapshotCwdAnchorSurvivesAncestorReplacement(t *testing.T) {
	root := filepath.Join(t.TempDir(), "mount-root")
	target := filepath.Join(root, "repo")
	cwd := filepath.Join(target, "subdir")
	if err := os.MkdirAll(cwd, 0o700); err != nil {
		t.Fatal(err)
	}
	originalInfo, err := os.Stat(cwd)
	if err != nil {
		t.Fatal(err)
	}

	anchor, relative, err := snapshotCwdAnchor(&SnapshotRunSpec{
		Units: []SnapshotUnitSpec{{Target: target}},
		Cwd:   cwd,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer anchor.Close()

	replaced := root + ".replaced"
	if err := os.Rename(root, replaced); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "repo", "subdir"), 0o700); err != nil {
		t.Fatal(err)
	}
	viewFD, err := unix.Openat(int(anchor.Fd()), relative, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(viewFD)
	anchoredInfo, err := os.Stat(filepath.Join("/proc/self/fd", fmt.Sprint(viewFD)))
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(originalInfo, anchoredInfo) {
		t.Fatalf("cwd anchor followed replaced ancestor: anchored=%v original=%v", anchoredInfo, originalInfo)
	}
}
