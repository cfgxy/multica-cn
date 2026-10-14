//go:build linux

package agent

import (
	"errors"
	"os/exec"
	"syscall"
)

// applySnapshotWrap rewrites an already-built agent exec.Cmd in place so that
// starting it spawns the stage-2 wrapper inside fresh user+mount namespaces;
// the wrapper execs the original agent command once the views are up.
//
// Everything else on the command — Dir, Env, stdio, Cancel, WaitDelay,
// process-group wiring — belongs to the caller's backend and is applied to the
// wrapper process, which either inherits it (env, stdio, cwd via the
// namespace-visible path) or carries it across exec (exit status, signals).
func applySnapshotWrap(cmd *exec.Cmd, wrap *SnapshotWrap) {
	if wrap == nil || wrap.WrapperPath == "" {
		// A nil/empty wrap reaching a snapshot-mode execution is a wiring
		// bug, never a reason to run the agent unwrapped: fail the start.
		cmd.Err = errors.New("agent: snapshot wrap requested but not configured")
		return
	}
	agentPath := cmd.Path
	agentArgs := cmd.Args
	if len(agentArgs) > 0 {
		agentArgs = agentArgs[1:] // exec.Cmd.Args[0] is argv[0]; re-added below
	} else {
		agentArgs = nil
	}
	cmd.Path = wrap.WrapperPath
	cmd.Args = append([]string{wrap.WrapperPath, SnapshotWorkerSubcommand},
		wrap.snapshotWorkerSpecArgv(agentPath, agentArgs)...)
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	applySnapshotSysProcAttr(cmd.SysProcAttr, wrap)
}

// applySnapshotSysProcAttr fills the clone flags and the single-entry ID map.
// The spawning process (daemon or supervised launcher) runs the multica
// binary, whose AppArmor profile authorizes userns creation; os/exec writes
// the uid/gid maps into /proc/<pid>/… from the parent, which an unprivileged
// parent may do for its own IDs.
func applySnapshotSysProcAttr(attr *syscall.SysProcAttr, wrap *SnapshotWrap) {
	attr.Cloneflags = syscall.CLONE_NEWUSER | syscall.CLONE_NEWNS
	attr.UidMappings = []syscall.SysProcIDMap{{ContainerID: 0, HostID: wrap.HostUID, Size: 1}}
	attr.GidMappings = []syscall.SysProcIDMap{{ContainerID: 0, HostID: wrap.HostGID, Size: 1}}
}

// snapshotSysProcAttrFor returns the spawn attributes for a launcher-side
// worker spawn whose stored argv is a wrapped worker invocation, or nil when
// the argv is not wrapped. A malformed wrap returns an error instead of
// attributes: the launcher must refuse the spawn rather than run the agent
// unwrapped.
func SnapshotSysProcAttrFor(argv []string) (*syscall.SysProcAttr, error) {
	if !isSnapshotWorkerArgv(argv) {
		return nil, nil
	}
	wrap := snapshotWrapFromArgv(argv)
	if wrap == nil {
		return nil, errors.New("supervisor: malformed snapshot worker argv; refusing to spawn unwrapped")
	}
	attr := &syscall.SysProcAttr{}
	applySnapshotSysProcAttr(attr, wrap)
	return attr, nil
}
