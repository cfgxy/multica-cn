package util

import (
	"fmt"
	"os"
	"path/filepath"
)

// ResolveSymlinksBestEffort resolves the existing portion of path and retains
// any missing descendants. It is useful when a caller needs a canonical parent
// before creating a new path below it.
func ResolveSymlinksBestEffort(path string) (string, error) {
	path = filepath.Clean(path)
	missing := make([]string, 0)
	for {
		resolved, err := filepath.EvalSymlinks(path)
		if err == nil {
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return resolved, nil
		}
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("resolve symlinks for %s: %w", path, err)
		}
		parent := filepath.Dir(path)
		if parent == path {
			return "", fmt.Errorf("resolve symlinks for %s: %w", path, err)
		}
		missing = append(missing, filepath.Base(path))
		path = parent
	}
}
