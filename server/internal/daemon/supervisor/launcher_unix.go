//go:build !windows

package supervisor

import "syscall"

// signalWorkerProcessGroup delivers sig to the worker's whole process tree:
// the worker leads its own process group (Setpgid in workerSysProcAttr), so
// the negative-pid kill reaches the provider CLI and its children like
// signalProcessGroup did for legacy children. The direct-pid kill follows up
// in case the group send raced an exited leader.
func signalWorkerProcessGroup(pid int, sig syscall.Signal) {
	_ = syscall.Kill(-pid, sig)
	_ = syscall.Kill(pid, sig)
}

// workerSysProcAttr puts the worker at the head of its own process group so
// group-signal delivery (kill(-pid)) reaches the provider CLI's whole tree.
func workerSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}
