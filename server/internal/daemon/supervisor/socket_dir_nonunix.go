//go:build !(aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris)

package supervisor

import "os"

func privateSocketDirOwnedByCurrentUser(_ os.FileInfo, _ int) bool {
	return false
}
