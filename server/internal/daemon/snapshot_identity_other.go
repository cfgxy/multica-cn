//go:build !linux

package daemon

import "os"

func snapshotFileIdentity(os.FileInfo) (uint64, uint64, bool) {
	return 0, 0, false
}

func snapshotPathMountID(string) (uint64, bool, error) {
	return 0, false, nil
}
