//go:build !windows

package agent

import (
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// hideAgentWindow is a no-op on non-Windows platforms.
func hideAgentWindow(cmd *exec.Cmd) {}

// configureProcessGroup puts the child into its own process group (it becomes
// the group leader, so the group id equals the child pid). This lets the
// daemon signal the entire tree — the agent CLI plus any tool subprocess it
// spawns — in one call, instead of killing only the direct child and leaking
// grandchildren that keep running (and, for opencode, spinning on EPIPE) after
// a task is cancelled or the daemon restarts. See signalProcessGroup.
//
// Called by newRuntimeCmd in launch.go, which is the single point where a
// runtime process is constructed. No backend calls it directly: the group has
// to exist for every runtime process, and per-backend opt-in did not deliver
// that (GH #7522).
func configureProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// startOwnedProcessTree is a plain Start on non-Windows platforms:
// newRuntimeCmd already put the child in its own process group before it
// existed, so there is nothing left to claim once it is running. The logger is
// unused here; Windows needs it to report degraded ownership.
//
// It is still the only way this package starts a long-lived runtime process,
// so the two platforms share one call site per backend.
//
// RUYI-349 note: os/exec's startCalled guard makes retrying cmd.Start after a
// failed attempt impossible ("exec: already started"), so an ETXTBSY here — a
// concurrent O_WRONLY hold on the binary at exec time, seen from test-suite
// helper races — surfaces as the Start error it is; the holder dump below
// names any live foreign fd for diagnosis.
func startOwnedProcessTree(cmd *exec.Cmd, logger *slog.Logger) error {
	err := cmd.Start()
	if errors.Is(err, syscall.ETXTBSY) {
		dumpTxtbsyHolders(cmd.Path, logger)
	}
	return err
}

// dumpTxtbsyHolders is RUYI-349 diagnostics: on ETXTBSY, name every process
// holding an fd (any mode) whose inode equals the exec target's — the writer
// may have closed already, but an inode-number match against a long-lived
// foreign fd proves inode recycling and identifies the holder.
func dumpTxtbsyHolders(path string, logger *slog.Logger) {
	self, err := os.Stat(path)
	if err != nil {
		logger.Error("etxtbsy forensics: stat target failed", "path", path, "error", err)
		return
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pid := e.Name()
		fds, err := os.ReadDir("/proc/" + pid + "/fd")
		if err != nil {
			continue
		}
		for _, fd := range fds {
			link := "/proc/" + pid + "/fd/" + fd.Name()
			target, err := os.Readlink(link)
			if err != nil || !strings.HasPrefix(target, "/tmp/") {
				continue
			}
			st, err := os.Stat(link)
			if err != nil || !st.Mode().IsRegular() {
				continue
			}
			if !os.SameFile(st, self) {
				continue
			}
			cmdline, _ := os.ReadFile("/proc/" + pid + "/cmdline")
			logger.Error("etxtbsy inode match", "pid", pid, "fd", fd.Name(),
				"target", target,
				"cmd", strings.ReplaceAll(string(cmdline), "\x00", " "))
		}
	}
}

// releaseProcessGroup is a no-op on non-Windows platforms: a process group needs
// no handle and is gone once its members are.
func releaseProcessGroup(cmd *exec.Cmd) {}

func codexInitializeRetrySupported() bool { return true }

// signalProcessGroup sends sig to the whole process group led by the command
// (when it was started with configureProcessGroup), falling back to the single
// process if the group send fails. Targeting the group (negative pid) reaches
// the descendants the agent spawned, not just the leader.
func signalProcessGroup(cmd *exec.Cmd, sig syscall.Signal) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	if err := syscall.Kill(-cmd.Process.Pid, sig); err != nil {
		_ = cmd.Process.Signal(sig)
	}
}

func waitProcessGroupGone(cmd *exec.Cmd, timeout time.Duration) bool {
	if cmd == nil || cmd.Process == nil {
		return false
	}
	deadline := time.Now().Add(timeout)
	for {
		err := syscall.Kill(-cmd.Process.Pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}
