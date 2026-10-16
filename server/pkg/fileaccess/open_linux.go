//go:build linux

package fileaccess

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// OpenBeneath opens a path relative to an allowlisted root with kernel-enforced
// beneath/no-magic-link resolution. Symlinks may resolve inside the root but
// cannot escape it, including during concurrent path replacement.
func OpenBeneath(roots []string, path string, flags int) (*os.File, error) {
	canonical, err := ResolveRootPath(roots, path)
	if err != nil {
		return nil, err
	}
	for _, configured := range roots {
		root, err := filepath.Abs(configured)
		if err != nil {
			continue
		}
		root, err = filepath.EvalSymlinks(root)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(root, canonical)
		if err != nil || rel == ".." || filepath.IsAbs(rel) {
			continue
		}
		rootFD, err := unix.Open(root, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
		if err != nil {
			continue
		}
		openHow := &unix.OpenHow{
			Flags:   uint64(flags | unix.O_CLOEXEC | unix.O_NONBLOCK),
			Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_MAGICLINKS | unix.RESOLVE_NO_SYMLINKS,
		}
		fd, openErr := unix.Openat2(rootFD, rel, openHow)
		unix.Close(rootFD)
		if openErr != nil {
			switch {
			case errors.Is(openErr, unix.EXDEV), errors.Is(openErr, unix.ELOOP):
				return nil, ErrorSymlinkEscapeDenied
			case errors.Is(openErr, unix.ENOENT):
				return nil, ErrorNotFound
			case errors.Is(openErr, unix.ENOSYS), errors.Is(openErr, unix.EINVAL):
				return nil, ErrorUnsupportedResolution
			default:
				return nil, ErrorPathNotAllowed
			}
		}
		return os.NewFile(uintptr(fd), canonical), nil
	}
	return nil, ErrorPathNotAllowed
}

func OpenDirectoryBeneath(roots []string, path string) (*os.File, error) {
	return OpenBeneath(roots, path, unix.O_RDONLY|unix.O_DIRECTORY)
}
