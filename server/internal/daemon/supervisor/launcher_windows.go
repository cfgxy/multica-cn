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
