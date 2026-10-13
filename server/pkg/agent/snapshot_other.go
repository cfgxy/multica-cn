//go:build !linux

package agent

import (
	"errors"
	"os/exec"
	"syscall"
)

// applySnapshotWrap is the non-linux fail-closed stub: snapshot isolation is
// Linux-only (user namespaces + kernel overlayfs). The daemon never advertises
// the capability on other platforms, so a wrap arriving here is a wiring bug —
// the command must fail to start, never fall back to running unwrapped.
func applySnapshotWrap(cmd *exec.Cmd, wrap *SnapshotWrap) {
	cmd.Err = errors.New("agent: snapshot isolation requires Linux")
}

// snapshotSysProcAttrFor mirrors the linux behavior's refusal path: snapshot
// launches cannot be reconstructed here, so a wrapped argv is refused outright.
func SnapshotSysProcAttrFor(argv []string) (*syscall.SysProcAttr, error) {
	if isSnapshotWorkerArgv(argv) {
		return nil, errors.New("supervisor: snapshot workers require Linux")
	}
	return nil, nil
}
