//go:build linux

package daemon

import (
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func snapshotFileIdentity(info os.FileInfo) (uint64, uint64, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, false
	}
	return uint64(stat.Dev), uint64(stat.Ino), true
}

func snapshotPathMountID(path string) (uint64, bool, error) {
	var stat unix.Statx_t
	if err := unix.Statx(unix.AT_FDCWD, path, unix.AT_STATX_SYNC_AS_STAT, unix.STATX_MNT_ID, &stat); err != nil {
		return 0, true, err
	}
	return stat.Mnt_id, true, nil
}
