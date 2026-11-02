//go:build linux

package execenv

import "golang.org/x/sys/unix"

func exchangeTaskContextMarker(dirFD int, tmpName, markerName string) error {
	return unix.Renameat2(dirFD, tmpName, dirFD, markerName, unix.RENAME_EXCHANGE)
}
