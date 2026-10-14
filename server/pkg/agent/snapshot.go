package agent

import "encoding/json"

// Snapshot workspace launch support (RUYI-669): lets any backend's agent
// process run inside a private user+mount namespace with per-task overlayfs
// views over the user's directories, without every backend knowing a thing
// about namespaces.
//
// The mechanism is a two-stage exec. The daemon builds the agent command as
// usual; when Config.Snapshot is set, Command.exec rewrites it to spawn THIS
// package's configured wrapper binary — the multica daemon itself, whose
// per-binary AppArmor profile is what authorizes unprivileged user-namespace
// creation — with clone flags CLONE_NEWUSER|CLONE_NEWNS. The wrapper child
// (first process, full capabilities in its fresh user namespace) materializes
// the overlay views described by SpecJSON, then execs the original agent
// command in the same namespaces. Same PID after exec: cancellation, process
// groups, exit codes, and the supervised launcher's PID bookkeeping all work
// unchanged.
//
// The wrapper invocation is self-describing — argv[1] names the subcommand,
// argv[2] carries the spec — which is what lets the supervised path work with
// zero new plumbing: workerSession.Start serializes Path/Args into the launch
// spec as it already does, the launcher stores them in its existing fields,
// and the launcher recognizes the wrapped shape when it rebuilds the spawn.
const (
	// SnapshotWorkerSubcommand is the hidden entrypoint the wrapped command
	// runs as. The clone flags are the spawner's business (they differ
	// between the direct path and the supervised launcher path).
	SnapshotWorkerSubcommand = "__snapshot-worker"
	// SnapshotProbeSubcommand is the startup capability probe entrypoint:
	// same clone + mount machinery against throwaway directories, exit code
	// as the verdict. The daemon runs it once per lifetime to gate the
	// local-snapshot-v1 capability advertisement.
	SnapshotProbeSubcommand = "__snapshot-probe"
	// SnapshotWorkerSep separates the wrapper's spec argument from the agent
	// argv to exec.
	SnapshotWorkerSep = "--"
)

// SnapshotWrap is the platform-neutral launch brief for one snapshot-wrapped
// process. SpecJSON is a daemon-side SnapshotRunSpec (opaque here: pkg/agent
// wraps and execs; only the daemon's execenv package interprets it).
type SnapshotWrap struct {
	// WrapperPath is the multica binary hosting the __snapshot-worker
	// entrypoint — the daemon's own executable, the binary the AppArmor
	// userns profile is bound to.
	WrapperPath string `json:"wrapper_path"`
	// SpecJSON is the encoded daemon-side run spec (units + cwd + host IDs;
	// the host IDs are what snapshotWrapFromArgv recovers for the launcher's
	// clone-flag rebuild).
	SpecJSON string `json:"spec_json"`
	// HostUID/HostGID map to root (uid 0) inside the new user namespace.
	// Mounting requires CAP_SYS_ADMIN in that namespace, which only the
	// mapped root holds; files the worker creates surface outside as the
	// daemon user's, so host-side ownership is unchanged.
	HostUID int `json:"host_uid"`
	HostGID int `json:"host_gid"`
}

// snapshotWorkerSpecArgv renders the wrapped argv tail for one agent command:
// <specJSON> -- <agentPath> <agentArgs...>. The agent's argv[0] stays its own
// path, so the exec'd CLI cannot tell it was wrapped.
func (w *SnapshotWrap) snapshotWorkerSpecArgv(agentPath string, agentArgs []string) []string {
	argv := []string{w.SpecJSON, SnapshotWorkerSep, agentPath}
	return append(argv, agentArgs...)
}

// isSnapshotWorkerArgv reports whether a post-Path argv is a wrapped worker
// invocation: <__snapshot-worker> <specJSON> -- <agent...>. The supervised
// launcher uses this to decide which clone flags the spawn needs.
func isSnapshotWorkerArgv(args []string) bool {
	return len(args) >= 4 && args[0] == SnapshotWorkerSubcommand && args[2] == SnapshotWorkerSep
}

// snapshotWrapFromArgv recovers the launch brief from a wrapped argv so the
// supervised launcher can rebuild the clone flags from its stored spec. A
// malformed wrap fails closed (nil): the caller must refuse to spawn rather
// than run the agent unwrapped.
func snapshotWrapFromArgv(args []string) *SnapshotWrap {
	if !isSnapshotWorkerArgv(args) {
		return nil
	}
	var probe struct {
		HostUID *int `json:"host_uid"`
		HostGID *int `json:"host_gid"`
	}
	if err := json.Unmarshal([]byte(args[1]), &probe); err != nil || probe.HostUID == nil || probe.HostGID == nil {
		return nil
	}
	return &SnapshotWrap{HostUID: *probe.HostUID, HostGID: *probe.HostGID}
}
