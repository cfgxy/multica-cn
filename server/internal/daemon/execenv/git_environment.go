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

// SnapshotGitEnvironmentAcrossFilesystems permits Git discovery from a
// separately mounted declared business directory to its private .git mount.
// It replaces, rather than appends to, an inherited setting so the worker and
// its Git subprocesses never observe duplicate environment keys.
func SnapshotGitEnvironmentAcrossFilesystems() []string {
	env := SnapshotGitEnvironment()
	filtered := make([]string, 0, len(env)+1)
	for _, item := range env {
		key, _, _ := strings.Cut(item, "=")
		if key != "GIT_DISCOVERY_ACROSS_FILESYSTEM" {
			filtered = append(filtered, item)
		}
	}
	return append(filtered, "GIT_DISCOVERY_ACROSS_FILESYSTEM=1")
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
