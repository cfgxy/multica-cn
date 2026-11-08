package execenv

import (
	"os"
	"strings"
)

var snapshotBlockedGitEnvironment = map[string]bool{
	"GIT_DIR": true, "GIT_WORK_TREE": true, "GIT_COMMON_DIR": true,
	"GIT_OBJECT_DIRECTORY": true, "GIT_ALTERNATE_OBJECT_DIRECTORIES": true,
	"GIT_INDEX_FILE":    true,
	"GIT_CONFIG_GLOBAL": true, "GIT_CONFIG_SYSTEM": true, "GIT_CONFIG_NOSYSTEM": true,
	"GIT_CONFIG_COUNT": true, "GIT_CONFIG_PARAMETERS": true,
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
	filtered := make([]string, 0, len(env)+2)
	for _, item := range env {
		key, _, _ := strings.Cut(item, "=")
		if !snapshotBlockedGitEnvironment[key] && !strings.HasPrefix(key, "GIT_CONFIG_KEY_") && !strings.HasPrefix(key, "GIT_CONFIG_VALUE_") {
			filtered = append(filtered, item)
		}
	}
	// Snapshot Git commands accept only repository/worktree configuration. The
	// daemon audits those files before mounting them; inherited global, system,
	// and environment-injected configuration is neither part of the snapshot nor
	// stable between admission and worker execution.
	return append(filtered, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
}
