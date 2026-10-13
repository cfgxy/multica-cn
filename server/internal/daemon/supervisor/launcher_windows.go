//go:build windows

package supervisor

import (
	"os"
	"syscall"
)

// signalWorkerProcessGroup terminates the worker process directly: Windows
// has no POSIX process groups, so the tree-level group kill is unavailable
// and TerminateProcess (os.Process.Kill) is the only stdlib delivery path.
// The supervised launch path itself is systemd transient units, a
// Linux-only feature, so this branch exists to keep the Windows build
// complete rather than to serve a production Windows supervisor.
func signalWorkerProcessGroup(pid int, sig syscall.Signal) {
	if p, err := os.FindProcess(pid); err == nil {
		_ = p.Kill()
	}
}

// workerSysProcAttr needs no Windows customization: process groups are a
// POSIX concept and no Windows spawn flags are required.
func workerSysProcAttr() *syscall.SysProcAttr {
	return nil
}

// workerSysProcAttrFor refuses snapshot-wrapped spawn specs on Windows: the
// namespace machinery does not exist there, and the daemon never advertises
// the capability on this platform — a wrapped spec arriving anyway is a
// wiring bug that must fail the spawn, not run the agent unwrapped.
func workerSysProcAttrFor(argv []string) (*syscall.SysProcAttr, error) {
	if _, err := snapshotSysProcAttrFor(argv); err != nil {
		return nil, err
	}
	return workerSysProcAttr(), nil
}
