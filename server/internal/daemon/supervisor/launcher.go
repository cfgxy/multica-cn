package supervisor

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"time"
)

// launcherSpecEnvVar carries the launcher brief's path. The environment, not
// argv: /proc/PID/cmdline is world-readable while /proc/PID/environ is not,
// and the brief embeds the worker env (which may carry credentials).
const launcherSpecEnvVar = "MULTICA_WORKER_LAUNCHER_SPEC"

// LauncherSubcommand is the hidden entrypoint the daemon binary re-execs as.
const LauncherSubcommand = "__worker-launcher"

// RunWorkerLauncher is the resident launcher: the worker's parent inside the
// transient unit. It owns exactly the pieces that must outlive the daemon —
// the worker's stdin pipe, its append-only log files, the control socket, and
// the proven exit record. It exits when the worker exits (mirroring the exit
// code), which deactivates the unit.
//
// Returns the process exit code for the caller in cmd/multica.
func RunWorkerLauncher() int {
	specPath := os.Getenv(launcherSpecEnvVar)
	if specPath == "" {
		fmt.Fprintf(os.Stderr, "supervisor: launcher requires %s\n", launcherSpecEnvVar)
		return 2
	}
	spec, err := ReadSpec(specPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "supervisor: launcher read spec: %v\n", err)
		return 2
	}
	if err := ValidateRunID(spec.RunID); err != nil {
		fmt.Fprintf(os.Stderr, "supervisor: launcher %v\n", err)
		return 2
	}

	outF, err := os.OpenFile(spec.StdoutLog, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		fmt.Fprintf(os.Stderr, "supervisor: launcher open stdout log: %v\n", err)
		return 2
	}
	defer outF.Close()
	errF, err := os.OpenFile(spec.StderrLog, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		fmt.Fprintf(os.Stderr, "supervisor: launcher open stderr log: %v\n", err)
		return 2
	}
	defer errF.Close()

	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		fmt.Fprintf(os.Stderr, "supervisor: launcher stdin pipe: %v\n", err)
		return 2
	}

	exit := make(chan WorkerExit, 1)
	deliverSignal := func(sig syscall.Signal) {
		if pid := workerPIDOf(spec); pid > 0 {
			// The worker leads its own group; TERM/KILL the whole tree like
			// signalProcessGroup did for legacy children.
			_ = syscall.Kill(-pid, sig)
			_ = syscall.Kill(pid, sig)
		}
	}

	cmd := exec.Command(spec.Path, spec.Args...)
	cmd.Env = spec.Env
	cmd.Dir = spec.Dir
	cmd.Stdin = stdinR
	cmd.Stdout = outF
	cmd.Stderr = errF
	cmd.SysProcAttr = workerSysProcAttr()

	if err := cmd.Start(); err != nil {
		stdinR.Close()
		stdinW.Close()
		// The worker never ran: record a conventional 127 so convergence has
		// evidence instead of a hang, then serve the exit to the daemon.
		we := WorkerExit{Code: 127}
		updateManifestExit(spec, ExitRecord{Code: we.Code, At: time.Now().UTC(), Source: ExitSourceLauncher})
		exit <- we
		_ = serveControl(spec.Socket, stdinW, 0, exit, deliverSignal)
		return we.Code
	}
	stdinR.Close() // the worker holds the read end now
	pid := cmd.Process.Pid
	updateManifestRunning(spec, pid)

	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()

	srvDone := make(chan error, 1)
	go func() {
		srvDone <- serveControl(spec.Socket, stdinW, pid, exit, deliverSignal)
	}()

	waitErr := <-waitDone
	we := workerExitFromError(waitErr)
	updateManifestExit(spec, ExitRecord{
		Code:   we.Code,
		Signal: we.Signal,
		At:     time.Now().UTC(),
		Source: ExitSourceLauncher,
	})
	stdinW.Close()
	exit <- we
	<-srvDone // listener closed by the exit broadcast

	if we.Code >= 0 {
		return we.Code
	}
	return 1
}

func workerPIDOf(spec *launchSpec) int {
	man, err := readManifestQuiet(spec.Manifest)
	if err != nil {
		return 0
	}
	return man.WorkerPID
}

// updateManifestRunning refreshes the launcher-side identity fields (PIDs).
// The daemon wrote the initial manifest before systemd-run; the launcher is
// the only writer once the unit is up.
func updateManifestRunning(spec *launchSpec, workerPID int) {
	man, err := readManifestQuiet(spec.Manifest)
	if err != nil {
		return
	}
	man.WorkerPID = workerPID
	man.LauncherPID = os.Getpid()
	man.State = StateRunning
	_ = writeJSONFile(spec.Manifest, man, 0o600)
}

func updateManifestExit(spec *launchSpec, rec ExitRecord) {
	man, err := readManifestQuiet(spec.Manifest)
	if err != nil {
		return
	}
	man.State = StateExited
	man.Exit = &rec
	_ = writeJSONFile(spec.Manifest, man, 0o600)
}

func readManifestQuiet(path string) (*Manifest, error) {
	var man Manifest
	if err := readJSONFile(path, &man); err != nil {
		return nil, err
	}
	return &man, nil
}

// workerExitFromError converts cmd.Wait's result into exec-mirroring evidence.
func workerExitFromError(err error) WorkerExit {
	if err == nil {
		return WorkerExit{Code: 0}
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return WorkerExit{Code: -1, Signal: syscall.Signal(ws.Signal()).String()}
		}
		return WorkerExit{Code: ee.ExitCode()}
	}
	return WorkerExit{Code: -1}
}

// workerSysProcAttr puts the worker at the head of its own process group so
// group-signal delivery (kill(-pid)) reaches the provider CLI's whole tree.
func workerSysProcAttr() *syscall.SysProcAttr {
	if runtime.GOOS == "windows" {
		return nil
	}
	return &syscall.SysProcAttr{Setpgid: true}
}
