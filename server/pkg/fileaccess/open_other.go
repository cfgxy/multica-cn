//go:build !linux

package fileaccess

import "os"

// Platforms without a kernel primitive that can prove beneath-root resolution
// fail closed rather than falling back to a check-then-open path race.
func OpenBeneath([]string, string, int) (*os.File, error) {
	return nil, ErrorUnsupportedResolution
}

func OpenDirectoryBeneath([]string, string) (*os.File, error) {
	return nil, ErrorUnsupportedResolution
}
