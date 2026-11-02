//go:build darwin

package execenv

import "golang.org/x/sys/unix"

func exchangeTaskContextMarker(dirFD int, tmpName, markerName string) error {
	return unix.RenameatxNp(dirFD, tmpName, dirFD, markerName, unix.RENAME_SWAP)
}
