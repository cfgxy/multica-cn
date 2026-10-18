//go:build !windows

package supervisor

import (
	"syscall"

	"github.com/multica-ai/multica/server/pkg/agent"
)

// signalWorkerProcessGroup delivers sig to the worker's whole process tree:
// the worker leads its own process group (Setpgid in workerSysProcAttr), so
// the negative-pid kill reaches the provider CLI and its children like
// signalProcessGroup did for legacy children. The direct-pid kill follows up
// in case the group send raced an exited leader.
func signalWorkerProcessGroup(pid int, sig syscall.Signal) {
	_ = syscall.Kill(-pid, sig)
	_ = syscall.Kill(pid, sig)
}

// workerSysProcAttrFor puts the worker at the head of its own process group
// (Setpgid, so group-signal delivery (kill(-pid)) reaches the provider CLI's
// whole tree). A snapshot-wrapped worker (RUYI-669) additionally gets
// user+mount namespace clone flags; the worker child is the daemon binary's
// stage-2 entrypoint, which mounts the overlay views and execs the agent in
// the same namespaces.
func workerSysProcAttrFor(argv []string) (*syscall.SysProcAttr, error) {
	attr := &syscall.SysProcAttr{Setpgid: true}
	snap, err := agent.SnapshotSysProcAttrFor(argv)
	if err != nil {
		return nil, err
	}
	if snap != nil {
		attr.Cloneflags = snap.Cloneflags
		attr.UidMappings = snap.UidMappings
		attr.GidMappings = snap.GidMappings
	}
	return attr, nil
}
