//go:build linux

package execenv

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
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

func TestOpenSnapshotTargetFromRootRejectsNestedSymlinkReplacementAfterVerification(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "child")
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.MkdirAll(child, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	var admitted unix.Stat_t
	if err := unix.Stat(child, &admitted); err != nil {
		t.Fatal(err)
	}
	unit := SnapshotUnitSpec{
		Target:            child,
		TargetDevice:      uint64(admitted.Dev),
		TargetInode:       uint64(admitted.Ino),
		TargetIdentitySet: true,
	}
	verified, err := openSnapshotTarget(&unit)
	if err != nil {
		t.Fatal(err)
	}
	defer verified.Close()

	previous := child + ".previous"
	if err := os.Rename(child, previous); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, child); err != nil {
		t.Fatal(err)
	}
	rootFD, err := unix.Open("/", unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	rootAnchor := os.NewFile(uintptr(rootFD), "/")
	defer rootAnchor.Close()
	if _, err := openSnapshotTargetFromRoot(&unit, rootAnchor, false); err == nil {
		t.Fatal("nested target replaced by an external symlink must fail before mount")
	}
	anchoredInfo, err := os.Stat(filepath.Join("/proc/self/fd", fmt.Sprint(verified.Fd())))
	if err != nil {
		t.Fatal(err)
	}
	if uint64(anchoredInfo.Sys().(*syscall.Stat_t).Dev) != uint64(admitted.Dev) || uint64(anchoredInfo.Sys().(*syscall.Stat_t).Ino) != uint64(admitted.Ino) {
		t.Fatal("verified nested lower no longer identifies the admitted child")
	}
}

func TestSnapshotTargetStillAtPathRejectsReplacementBeforeMount(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "child")
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.MkdirAll(child, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	opened, err := openSnapshotTarget(&SnapshotUnitSpec{Target: child})
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()

	if err := os.Rename(child, child+".previous"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, child); err != nil {
		t.Fatal(err)
	}
	if err := snapshotTargetStillAtPath(opened, child); err == nil {
		t.Fatal("target replaced by an external symlink before mount must fail path binding")
	}
}

func TestSnapshotTargetsShareInodeRejectsNestedDirectoryReplacement(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "child")
	if err := os.MkdirAll(child, 0o700); err != nil {
		t.Fatal(err)
	}
	unit := SnapshotUnitSpec{Target: child}
	lower, err := openSnapshotTarget(&unit)
	if err != nil {
		t.Fatal(err)
	}
	defer lower.Close()

	if err := os.Rename(child, child+".previous"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(child, 0o700); err != nil {
		t.Fatal(err)
	}
	rootFD, err := unix.Open("/", unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	rootAnchor := os.NewFile(uintptr(rootFD), "/")
	defer rootAnchor.Close()
	target, err := openSnapshotTargetFromRoot(&unit, rootAnchor, false)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()

	if err := snapshotTargetsShareInode(lower, target); err == nil {
		t.Fatal("nested mount target replaced by another directory must fail identity binding")
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

func TestSnapshotCwdViewRejectsReplacementAfterMountPathCheck(t *testing.T) {
	root := filepath.Join(t.TempDir(), "mount-root")
	target := filepath.Join(root, "repo")
	cwd := filepath.Join(target, "subdir")
	if err := os.MkdirAll(cwd, 0o700); err != nil {
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

	if err := os.Rename(root, root+".replaced"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "repo", "subdir"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := snapshotCwdView(anchor, relative, cwd); err == nil {
		t.Fatal("replacement outside the overlay view must fail cwd validation")
	}
}
