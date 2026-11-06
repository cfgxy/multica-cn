package execenv

import (
	"os"
	"strings"
)

var snapshotBlockedGitEnvironment = map[string]bool{
	"GIT_DIR": true, "GIT_WORK_TREE": true, "GIT_COMMON_DIR": true,
	"GIT_OBJECT_DIRECTORY": true, "GIT_ALTERNATE_OBJECT_DIRECTORIES": true,
	"GIT_INDEX_FILE": true,
}

// SnapshotGitEnvironment removes Git location overrides so every snapshot
// operation resolves metadata through the overlay view rather than a daemon
// process's ambient Git context.
func SnapshotGitEnvironment() []string {
	return snapshotGitEnvironment(os.Environ())
}

func snapshotGitEnvironment(env []string) []string {
	if env == nil {
		env = os.Environ()
	}
	filtered := make([]string, 0, len(env))
	for _, item := range env {
		key, _, _ := strings.Cut(item, "=")
		if !snapshotBlockedGitEnvironment[key] {
			filtered = append(filtered, item)
		}
	}
	return filtered
}
