//go:build linux

package execenv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/multica-ai/multica/server/pkg/agent"
	"golang.org/x/sys/unix"
)

// RunSnapshotWorker is the stage-2 entrypoint (agent.SnapshotWorkerSubcommand).
//
// The process was ALREADY cloned into a fresh user namespace + mount namespace
// by its spawner (forkExec with clone flags) — an in-process unshare(2) is not
// an option: the kernel refuses CLONE_NEWUSER for multithreaded callers, and
// every Go process is multithreaded. Being cloned at birth also makes this
// process the first resident of a fresh user namespace, holding its full
// capability set there, which is exactly what mounting overlayfs requires.
//
// argv layout: <self> <subcommand> <specJSON> -- <agentPath> [agentArgs...]
//
// Steps, in the only order that works:
//  1. Make the copied mount tree private — the host table arrives with shared
//     propagation, and mounts made in this namespace must not leak back.
//  2. Per unit: bind the pristine directory to its alias, then mount the
//     overlay over the pristine path itself (decision 1: same-path view). The
//     bind lands FIRST — after the overlay covers the target, the underlying
//     content is only reachable through the alias.
//  3. chdir to the primary view. exec inherits the cwd as a handle; a cwd
//     adopted before step 2 would keep pointing at the pristine directory
//     under the view.
//  4. Exec the agent command. Same PID, same namespaces: the agent (and its
//     whole process tree) lives inside the snapshot for the rest of the run,
//     and the namespace dies with the process.
//
// Any failure before exec prints to stderr and exits non-zero; the backend's
// stderr capture surfaces it and the task fails — snapshot mode never falls
// back to running in the pristine directory.
func RunSnapshotWorker(args []string) int {
	specJSON, rest, err := splitWorkerArgv(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "snapshot-worker: %v\n", err)
		return 2
	}
	var spec SnapshotRunSpec
	if err := json.Unmarshal([]byte(specJSON), &spec); err != nil {
		fmt.Fprintf(os.Stderr, "snapshot-worker: decode spec: %v\n", err)
		return 2
	}
	if len(rest) == 0 || rest[0] == "" {
		fmt.Fprintln(os.Stderr, "snapshot-worker: no agent command after --")
		return 2
	}

	if err := mountSnapshotViews(&spec); err != nil {
		fmt.Fprintf(os.Stderr, "snapshot-worker: %v\n", err)
		return 1
	}

	if spec.Cwd != "" {
		if err := unix.Chdir(spec.Cwd); err != nil {
			fmt.Fprintf(os.Stderr, "snapshot-worker: chdir %s: %v\n", spec.Cwd, err)
			return 1
		}
	}
	// Preserve the original exec path for ordinary snapshots. Git delivery alone
	// needs the mount to stay alive briefly after the child exits.
	argv := append([]string{rest[0]}, rest[1:]...)
	if spec.GitArtifact == nil {
		if err := syscall.Exec(rest[0], argv, os.Environ()); err != nil {
			fmt.Fprintf(os.Stderr, "snapshot-worker: exec %s: %v\n", rest[0], err)
			return 127
		}
		return 0 // unreachable
	}
	cmd := exec.Command(rest[0], argv[1:]...)
	cmd.Env = os.Environ()
	cmd.Stdout, cmd.Stderr, cmd.Stdin = os.Stdout, os.Stderr, os.Stdin
	err = cmd.Run()
	if captureErr := CaptureSnapshotGitArtifact(spec.Cwd, spec.GitArtifact); captureErr != nil {
		fmt.Fprintf(os.Stderr, "snapshot-worker: capture Git artifact: %v\n", captureErr)
		if err == nil {
			return 1
		}
	}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode()
		}
		fmt.Fprintf(os.Stderr, "snapshot-worker: run %s: %v\n", rest[0], err)
		return 127
	}
	return 0
}

// splitWorkerArgv splits <specJSON> -- <agent argv...>.
func splitWorkerArgv(args []string) (string, []string, error) {
	for i, a := range args {
		if a == agent.SnapshotWorkerSep {
			if i != 1 {
				return "", nil, fmt.Errorf("expected <spec> %s <agent...>, got %d leading args", agent.SnapshotWorkerSep, i)
			}
			if i+1 >= len(args) {
				return "", nil, fmt.Errorf("no agent command after %s", agent.SnapshotWorkerSep)
			}
			return args[0], args[i+1:], nil
		}
	}
	return "", nil, fmt.Errorf("missing %s separator", agent.SnapshotWorkerSep)
}

// mountSnapshotViews materializes every unit's overlay. See RunSnapshotWorker
// for the ordering constraints.
func mountSnapshotViews(spec *SnapshotRunSpec) error {
	if err := unix.Mount("", "/", "", unix.MS_REC|unix.MS_PRIVATE, ""); err != nil {
		return fmt.Errorf("make mount tree private: %w", err)
	}
	for _, i := range mountOrder(spec.Units) {
		if err := mountSnapshotUnit(&spec.Units[i]); err != nil {
			return fmt.Errorf("unit %d (%s): %w", i, spec.Units[i].Target, err)
		}
	}
	return nil
}

// mountOrder returns the unit indices in mount order: by target depth,
// shallow before deep, stable within one depth. An overlay covers everything
// under its target, so a child nested under a sibling unit's target (a
// declaration like ["a/b", "a"]) must mount only after the parent's overlay
// does — otherwise the parent view would shadow the child mount and the
// child unit's writes would silently land in the parent's upperdir, skewing
// the per-unit delivery diff. Declaration order carries no such guarantee.
func mountOrder(units []SnapshotUnitSpec) []int {
	idx := make([]int, len(units))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool {
		return strings.Count(units[idx[a]].Target, "/") < strings.Count(units[idx[b]].Target, "/")
	})
	return idx
}

// mountSnapshotUnit mounts one overlay view. The userns-forced downgrades are
// deliberate (2026-10-11 spike, recorded in the ADR): userxattr instead of
// trusted.* xattrs, and no redirect_dir / metacopy / index options — the
// kernel rejects the first two outright in a user namespace and silently drops
// the third, so the mount never asks for them.
func mountSnapshotUnit(u *SnapshotUnitSpec) error {
	if u.Target == "" || u.Alias == "" || u.Upper == "" || u.Work == "" {
		return errors.New("unit is missing target/alias/upper/work")
	}
	for _, d := range []string{u.Alias, u.Upper, u.Work} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return fmt.Errorf("prepare %s: %w", d, err)
		}
	}
	if err := unix.Mount(u.Target, u.Alias, "", unix.MS_BIND, ""); err != nil {
		return fmt.Errorf("bind %s -> %s: %w", u.Target, u.Alias, err)
	}
	opts := fmt.Sprintf("lowerdir=%s,upperdir=%s,workdir=%s,userxattr", u.Alias, u.Upper, u.Work)
	if err := unix.Mount("overlay", u.Target, "overlay", 0, opts); err != nil {
		return fmt.Errorf("overlay on %s: %w", u.Target, err)
	}
	return nil
}

// RunSnapshotProbe is the startup capability probe
// (agent-side spawn via ProbeSnapshotSupport). It exercises the exact mount
// machinery of RunSnapshotWorker against throwaway directories and reports
// pass/fail by exit code.
//
// argv: <self> <subcommand> <probeRoot>
func RunSnapshotProbe(args []string) int {
	if len(args) != 1 || args[0] == "" {
		fmt.Fprintln(os.Stderr, "snapshot-probe: requires exactly one root argument")
		return 2
	}
	root := args[0]
	// Layout mirrors the real worker: the covered directory (view/) is a
	// sibling of the overlay backing dirs, never their ancestor — a
	// self-covering overlay (upperdir inside the covered tree) recurses into
	// itself and fails with ELOOP.
	view := filepath.Join(root, "view")
	upper := filepath.Join(root, "upper")
	work := filepath.Join(root, "work")
	alias := filepath.Join(root, "alias")
	for _, d := range []string{view, upper, work, alias} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			fmt.Fprintf(os.Stderr, "snapshot-probe: mkdir %s: %v\n", d, err)
			return 1
		}
	}
	// A file only the lower layer has, written at the covered path BEFORE the
	// mount (pre-existing content is exactly what the lower layer is). Seeing
	// it at the same path through the view proves the merge.
	marker := "snapshot-probe-marker"
	if err := os.WriteFile(filepath.Join(view, marker), []byte("probe\n"), 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "snapshot-probe: write marker: %v\n", err)
		return 1
	}
	spec := SnapshotRunSpec{Units: []SnapshotUnitSpec{{
		Target: view,
		Alias:  alias,
		Upper:  upper,
		Work:   work,
	}}}
	if err := mountSnapshotViews(&spec); err != nil {
		fmt.Fprintf(os.Stderr, "snapshot-probe: mount: %v\n", err)
		return 1
	}
	// The view covers view/, so the probe's checks read IN the view.
	if _, err := os.Stat(filepath.Join(view, marker)); err != nil {
		fmt.Fprintf(os.Stderr, "snapshot-probe: lower file not visible through view: %v\n", err)
		return 1
	}
	if err := os.WriteFile(filepath.Join(view, "copied-up"), []byte("up\n"), 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "snapshot-probe: view write: %v\n", err)
		return 1
	}
	if _, err := os.Stat(filepath.Join(upper, "copied-up")); err != nil {
		fmt.Fprintf(os.Stderr, "snapshot-probe: view write did not land in upper: %v\n", err)
		return 1
	}
	if _, err := os.Stat(filepath.Join(alias, marker)); err != nil {
		fmt.Fprintf(os.Stderr, "snapshot-probe: pristine alias not reachable: %v\n", err)
		return 1
	}
	if _, err := os.Stat(filepath.Join(alias, "copied-up")); !errors.Is(err, os.ErrNotExist) {
		fmt.Fprintln(os.Stderr, "snapshot-probe: view write leaked into pristine alias")
		return 1
	}
	fmt.Fprintln(os.Stderr, "snapshot-probe: OK (userns overlay same-path mount verified)")
	return 0
}

// ProbeSnapshotSupport runs the capability probe: spawn this binary's
// __snapshot-probe entrypoint inside throwaway namespaces and report whether
// the userns overlay path works on this host. One probe per daemon lifetime;
// callers gate the capability advertisement and snapshot-mode task execution
// on it. scratchBase is any writable directory on a production filesystem —
// the probe runs before workspace/env roots exist, so the daemon passes the
// workspaces root and the scratch dir (cleaned up afterwards) is a hidden
// temporary there.
func ProbeSnapshotSupport(selfPath, scratchBase string) (bool, string) {
	if selfPath == "" || scratchBase == "" {
		return false, "probe requires the daemon binary path and a scratch base dir"
	}
	if err := os.MkdirAll(scratchBase, 0o700); err != nil {
		return false, fmt.Sprintf("probe scratch base: %v", err)
	}
	root, err := os.MkdirTemp(scratchBase, ".snapshot-probe-*")
	if err != nil {
		return false, fmt.Sprintf("probe scratch dir: %v", err)
	}
	defer os.RemoveAll(root)

	// Bounded: a host where the clone or mount wedges must not wedge daemon
	// startup — the capability simply stays off and the next daemon start
	// probes again.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, selfPath, agent.SnapshotProbeSubcommand, root)
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	// Same clone + ID-map shape as pkg/agent's wrap path: fresh user+mount
	// namespaces, daemon's own IDs mapped to root.
	cmd.SysProcAttr.Cloneflags = syscall.CLONE_NEWUSER | syscall.CLONE_NEWNS
	cmd.SysProcAttr.UidMappings = []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Getuid(), Size: 1}}
	cmd.SysProcAttr.GidMappings = []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Getgid(), Size: 1}}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false, strings.TrimSpace(string(out))
	}
	return true, strings.TrimSpace(string(out))
}
