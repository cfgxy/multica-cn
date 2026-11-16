//go:build linux

package execenv

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

	mounts, err := mountSnapshotViews(&spec)
	if err != nil {
		fmt.Fprintf(os.Stderr, "snapshot-worker: %v\n", err)
		return 1
	}
	defer mounts.Close()

	if mounts.cwdView != nil {
		if err := unix.Fchdir(int(mounts.cwdView.Fd())); err != nil {
			fmt.Fprintf(os.Stderr, "snapshot-worker: chdir %s: %v\n", spec.Cwd, err)
			return 1
		}
	}
	gitCwd := mounts.cwdPath
	if spec.GitArtifact != nil && gitCwd != "" {
		gitCwd, err = mounts.persistentCwdPath()
		if err != nil {
			fmt.Fprintf(os.Stderr, "snapshot-worker: %v\n", err)
			return 1
		}
	}
	if gitCwd == "" {
		gitCwd = spec.Cwd
	}
	gitEnv := SnapshotGitEnvironment()
	if spec.GitArtifact != nil && spec.GitArtifact.ScopePath != "" {
		// A declared business unit is its own overlay mount while the repository
		// .git is a separate internal mount above it. Git must cross that mount
		// boundary to discover the private metadata view.
		gitEnv = SnapshotGitEnvironmentAcrossFilesystems()
	}
	if spec.GitArtifact != nil {
		if err := ValidateSnapshotGitHooks(gitCwd, &spec, gitEnv); err != nil {
			fmt.Fprintf(os.Stderr, "snapshot-worker: %v\n", err)
			return 1
		}
	}
	// Preserve the original exec path for ordinary snapshots. Git delivery alone
	// needs the mount to stay alive briefly after the child exits.
	argv := append([]string{rest[0]}, rest[1:]...)
	if spec.GitArtifact == nil {
		if err := syscall.Exec(rest[0], argv, gitEnv); err != nil {
			fmt.Fprintf(os.Stderr, "snapshot-worker: exec %s: %v\n", rest[0], err)
			return 127
		}
		return 0 // unreachable
	}
	cmd := exec.Command(rest[0], argv[1:]...)
	cmd.Env = gitEnv
	cmd.Stdout, cmd.Stderr, cmd.Stdin = os.Stdout, os.Stderr, os.Stdin
	err = cmd.Run()
	if captureErr := CaptureSnapshotGitArtifact(gitCwd, spec.GitArtifact, gitEnv); captureErr != nil {
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

// snapshotMounts keeps the opened objects alive for the worker lifetime. The
// cwd anchor is opened outside the shallowest mounted unit, so a renamed
// ancestor cannot redirect the worker into an uncovered replacement path.
type snapshotMounts struct {
	targets        []*os.File
	targetMountIDs []uint64
	cwdAnchor      *os.File
	cwdView        *os.File
	cwdPath        string
	cwdRelative    string
	gitCwdAnchor   *os.File
}

func (m *snapshotMounts) Close() {
	for _, target := range m.targets {
		if target != nil {
			_ = target.Close()
		}
	}
	if m.cwdView != nil {
		_ = m.cwdView.Close()
	}
	if m.cwdAnchor != nil {
		_ = m.cwdAnchor.Close()
	}
	if m.gitCwdAnchor != nil {
		_ = m.gitCwdAnchor.Close()
	}
}

func (m *snapshotMounts) persistentCwdPath() (string, error) {
	if m.cwdView == nil {
		return m.cwdPath, nil
	}
	fd, err := unix.Dup(int(m.cwdView.Fd()))
	if err != nil {
		return "", fmt.Errorf("duplicate Git cwd anchor: %w", err)
	}
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_SETFD, 0); err != nil {
		_ = unix.Close(fd)
		return "", fmt.Errorf("clear Git cwd anchor close-on-exec: %w", err)
	}
	m.gitCwdAnchor = os.NewFile(uintptr(fd), "git-cwd-anchor")
	return filepath.Join("/proc/self/fd", fmt.Sprint(fd)), nil
}

func snapshotCwdAnchor(spec *SnapshotRunSpec) (*os.File, string, error) {
	if spec.Cwd == "" {
		return nil, "", nil
	}
	cwd := filepath.Clean(spec.Cwd)
	var anchorTarget string
	bestDepth := int(^uint(0) >> 1)
	for _, unit := range spec.Units {
		rel, err := filepath.Rel(filepath.Clean(unit.Target), cwd)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		depth := strings.Count(filepath.Clean(unit.Target), string(filepath.Separator))
		if depth < bestDepth {
			bestDepth = depth
			anchorTarget = filepath.Clean(unit.Target)
		}
	}
	if anchorTarget == "" {
		return nil, "", fmt.Errorf("cwd %s is not covered by a snapshot unit", spec.Cwd)
	}
	parent := filepath.Dir(anchorTarget)
	relative, err := filepath.Rel(parent, cwd)
	if err != nil {
		return nil, "", fmt.Errorf("resolve cwd anchor: %w", err)
	}
	fd, err := unix.Open(parent, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, "", fmt.Errorf("open cwd anchor %s: %w", parent, err)
	}
	return os.NewFile(uintptr(fd), parent), relative, nil
}

type snapshotMountedUnit struct {
	Target string
	Alias  string
}

func snapshotMountedAncestor(mounted []snapshotMountedUnit, target string) (snapshotMountedUnit, string, bool) {
	var best snapshotMountedUnit
	bestDepth := -1
	bestRelative := ""
	for _, parent := range mounted {
		rel, err := filepath.Rel(filepath.Clean(parent.Target), filepath.Clean(target))
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		depth := strings.Count(filepath.Clean(parent.Target), string(filepath.Separator))
		if depth > bestDepth {
			best = parent
			bestDepth = depth
			bestRelative = rel
		}
	}
	return best, bestRelative, bestDepth >= 0
}

// mountSnapshotViews materializes every unit's overlay. See RunSnapshotWorker
// for the ordering constraints.
func mountSnapshotViews(spec *SnapshotRunSpec) (*snapshotMounts, error) {
	if err := unix.Mount("", "/", "", unix.MS_REC|unix.MS_PRIVATE, ""); err != nil {
		return nil, fmt.Errorf("make mount tree private: %w", err)
	}
	rootFD, err := unix.Open("/", unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open mount path anchor: %w", err)
	}
	rootAnchor := os.NewFile(uintptr(rootFD), "/")
	mounts := &snapshotMounts{
		targets:        make([]*os.File, len(spec.Units)),
		targetMountIDs: make([]uint64, len(spec.Units)),
	}
	mounts.cwdAnchor, mounts.cwdRelative, err = snapshotCwdAnchor(spec)
	if err != nil {
		_ = rootAnchor.Close()
		return nil, err
	}
	prechecked := make([]*os.File, len(spec.Units))
	closePrechecked := func() {
		for _, opened := range prechecked {
			if opened != nil {
				_ = opened.Close()
			}
		}
	}
	for i := range spec.Units {
		prechecked[i], err = openSnapshotTargetFromRoot(&spec.Units[i], rootAnchor, true)
		if err != nil {
			closePrechecked()
			mounts.Close()
			_ = rootAnchor.Close()
			return nil, fmt.Errorf("open unit %d (%s): %w", i, spec.Units[i].Target, err)
		}
	}
	closePrechecked()
	mounted := make([]snapshotMountedUnit, 0, len(spec.Units))
	gitRootIndex, err := snapshotGitRootIndex(spec)
	if err != nil {
		mounts.Close()
		_ = rootAnchor.Close()
		return nil, err
	}
	for _, i := range snapshotMountOrder(spec.Units, gitRootIndex) {
		parent, relative, nested := snapshotMountedAncestor(mounted, spec.Units[i].Target)
		var lower *os.File
		if nested {
			verificationPath := filepath.Join(parent.Alias, relative)
			// Every host target was admitted and identity-checked above. A Git
			// artifact's nested lower is different: it is a daemon-materialized
			// private clone, so the host TargetDevice/TargetInode must not be
			// applied to this new object. Non-Git nested units retain the
			// original identity check and inode comparison below.
			// Every host target was admitted and identity-checked above. A Git
			// artifact's nested lower is different: it is a daemon-materialized
			// private clone, so the host TargetDevice/TargetInode must not be
			// applied to this new object. Non-Git nested units retain the
			// original identity check and inode comparison below.
			// Every host target was admitted and identity-checked above. A Git
			// artifact's nested lower is different: it is a daemon-materialized
			// private clone, so the host TargetDevice/TargetInode must not be
			// applied to this new object. Non-Git nested units retain the
			// original identity check and inode comparison below.
			verifyHostIdentity := spec.GitArtifact == nil
			verificationTarget, openErr := openSnapshotTargetAt(&spec.Units[i], verificationPath, verifyHostIdentity)
			if openErr != nil {
				mounts.Close()
				_ = rootAnchor.Close()
				return nil, fmt.Errorf("verify nested unit %d (%s): %w", i, spec.Units[i].Target, openErr)
			}
			lower = verificationTarget
		}
		target, openErr := openSnapshotTargetFromRoot(&spec.Units[i], rootAnchor, !nested)
		if openErr != nil {
			if lower != nil {
				_ = lower.Close()
			}
			mounts.Close()
			_ = rootAnchor.Close()
			return nil, fmt.Errorf("open unit %d (%s): %w", i, spec.Units[i].Target, openErr)
		}
		if lower == nil {
			lower = target
		}
		if nested && spec.GitArtifact == nil {
			if err := snapshotTargetsShareInode(lower, target); err != nil {
				_ = lower.Close()
				_ = target.Close()
				mounts.Close()
				_ = rootAnchor.Close()
				return nil, fmt.Errorf("verify nested unit %d (%s): %w", i, spec.Units[i].Target, err)
			}
		}
		gitRoot := spec.GitArtifact != nil && i == gitRootIndex
		if err := mountSnapshotUnit(&spec.Units[i], lower, target, spec.GitArtifact, gitRoot); err != nil {
			if lower != target {
				_ = lower.Close()
			}
			_ = target.Close()
			mounts.Close()
			_ = rootAnchor.Close()
			return nil, fmt.Errorf("unit %d (%s): %w", i, spec.Units[i].Target, err)
		}
		mountID, err := snapshotOverlayMountID(&spec.Units[i], rootAnchor)
		if err != nil {
			if lower != target {
				_ = lower.Close()
			}
			_ = target.Close()
			mounts.Close()
			_ = rootAnchor.Close()
			return nil, fmt.Errorf("unit %d (%s): %w", i, spec.Units[i].Target, err)
		}
		if err := snapshotTargetStillAtPath(target, spec.Units[i].Target); err != nil {
			if lower != target {
				_ = lower.Close()
			}
			_ = target.Close()
			mounts.Close()
			_ = rootAnchor.Close()
			return nil, fmt.Errorf("unit %d (%s): %w", i, spec.Units[i].Target, err)
		}
		if lower != target {
			_ = lower.Close()
		}
		mounts.targets[i] = target
		mounts.targetMountIDs[i] = mountID
		mounted = append(mounted, snapshotMountedUnit{Target: spec.Units[i].Target, Alias: spec.Units[i].Alias})
	}
	if mounts.cwdAnchor != nil {
		cwdMountID, openErr := snapshotCwdMountID(spec, mounts.targetMountIDs)
		if openErr != nil {
			mounts.Close()
			_ = rootAnchor.Close()
			return nil, openErr
		}
		cwdFD, openErr := snapshotCwdView(mounts.cwdAnchor, mounts.cwdRelative, spec.Cwd, cwdMountID)
		if openErr != nil {
			mounts.Close()
			_ = rootAnchor.Close()
			return nil, fmt.Errorf("open mounted cwd %s: %w", spec.Cwd, openErr)
		}
		mounts.cwdView = os.NewFile(uintptr(cwdFD), "snapshot-cwd")
		mounts.cwdPath = filepath.Join("/proc/self/fd", fmt.Sprint(cwdFD))
	}
	_ = rootAnchor.Close()
	return mounts, nil
}

// snapshotGitRootIndex selects the unit whose worktree owns the private Git
// lower. The production daemon marks this unit explicitly. Older hand-built
// specs are accepted only when Cwd and the unit topology identify one root;
// mount order is never used as a fallback because a linked-worktree common
// directory can be shallower than the worktree.
func snapshotGitRootIndex(spec *SnapshotRunSpec) (int, error) {
	if spec == nil || spec.GitArtifact == nil {
		return -1, nil
	}
	rootIndex := -1
	for i, unit := range spec.Units {
		if !unit.GitRoot {
			continue
		}
		if rootIndex >= 0 {
			return -1, errors.New("snapshot Git spec marks multiple private lower roots")
		}
		rootIndex = i
	}
	if rootIndex >= 0 {
		return rootIndex, nil
	}
	if spec.Cwd == "" {
		return -1, errors.New("snapshot Git spec has no explicit root or cwd")
	}

	cwd := filepath.Clean(spec.Cwd)
	bestIndex := -1
	bestDepth := -1
	for i, unit := range spec.Units {
		target := filepath.Clean(unit.Target)
		rel, err := filepath.Rel(target, cwd)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		depth := strings.Count(target, string(filepath.Separator))
		// Prefer the deepest proper ancestor when Cwd is a declared business
		// directory. Its repository containment unit owns .git; the business
		// overlay itself does not. An exact Cwd unit wins for linked worktrees.
		if rel == "." {
			if bestIndex < 0 || bestDepth < depth {
				bestIndex, bestDepth = i, depth
			}
			continue
		}
		if bestIndex < 0 || bestDepth < depth {
			bestIndex, bestDepth = i, depth
		}
	}
	if bestIndex < 0 {
		return -1, fmt.Errorf("snapshot Git cwd %s is not covered by a unit", spec.Cwd)
	}

	// If an exact Cwd unit exists and a proper ancestor also exists, the
	// ancestor is the repository containment unit used by declared subdirs.
	// Otherwise the exact Cwd unit is the primary/worktree unit.
	exactIndex := -1
	exactDepth := -1
	ancestorIndex := -1
	ancestorDepth := -1
	for i, unit := range spec.Units {
		target := filepath.Clean(unit.Target)
		depth := strings.Count(target, string(filepath.Separator))
		rel, err := filepath.Rel(target, cwd)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		if rel == "." && depth > exactDepth {
			exactIndex, exactDepth = i, depth
		} else if rel != "." && depth > ancestorDepth {
			ancestorIndex, ancestorDepth = i, depth
		}
	}
	if exactIndex >= 0 && ancestorIndex >= 0 {
		return ancestorIndex, nil
	}
	return bestIndex, nil
}

// snapshotMountOrder keeps the overlay parent-before-child rule while moving
// the Git root ahead of unrelated metadata units. A linked-worktree common
// directory may be shallower than the worktree; mounting it first would hide
// the common metadata before the root can be materialized from its source.
func snapshotMountOrder(units []SnapshotUnitSpec, gitRootIndex int) []int {
	order := mountOrder(units)
	if gitRootIndex < 0 || gitRootIndex >= len(units) {
		return order
	}
	rootPosition := -1
	insertPosition := 0
	for position, index := range order {
		if index == gitRootIndex {
			rootPosition = position
			continue
		}
		if snapshotPathIsProperAncestor(units[index].Target, units[gitRootIndex].Target) {
			insertPosition = position + 1
		}
	}
	if rootPosition < 0 || rootPosition <= insertPosition {
		return order
	}
	root := order[rootPosition]
	order = append(order[:rootPosition], order[rootPosition+1:]...)
	if insertPosition > rootPosition {
		insertPosition--
	}
	order = append(order, 0)
	copy(order[insertPosition+1:], order[insertPosition:])
	order[insertPosition] = root
	return order
}

func snapshotPathIsProperAncestor(parent, child string) bool {
	rel, err := filepath.Rel(filepath.Clean(parent), filepath.Clean(child))
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// snapshotCwdView opens Cwd through the stable parent anchor and requires the
// result to be on OverlayFS. A replacement after the mount path check may win
// pathname resolution, but it cannot masquerade as the snapshot view and the
// worker fails before Fchdir.
func snapshotCwdView(anchor *os.File, relative, cwd string, expectedMountID uint64) (int, error) {
	if anchor == nil || relative == "" || cwd == "" || expectedMountID == 0 {
		return -1, errors.New("snapshot cwd anchor is missing")
	}
	fd, err := unix.Openat(
		int(anchor.Fd()),
		relative,
		unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC,
		0,
	)
	if err != nil {
		return -1, fmt.Errorf("open mounted cwd %s: %w", cwd, err)
	}
	var fs unix.Statfs_t
	if err := unix.Fstatfs(fd, &fs); err != nil {
		_ = unix.Close(fd)
		return -1, fmt.Errorf("stat mounted cwd %s: %w", cwd, err)
	}
	if fs.Type != unix.OVERLAYFS_SUPER_MAGIC {
		_ = unix.Close(fd)
		return -1, fmt.Errorf("mounted cwd %s is not an overlay view", cwd)
	}
	mountID, err := snapshotMountIDFromFD(fd)
	if err != nil {
		_ = unix.Close(fd)
		return -1, fmt.Errorf("read mounted cwd %s identity: %w", cwd, err)
	}
	if mountID != expectedMountID {
		_ = unix.Close(fd)
		return -1, fmt.Errorf("mounted cwd %s is not the expected snapshot view", cwd)
	}
	return fd, nil
}

func snapshotCwdMountID(spec *SnapshotRunSpec, mountIDs []uint64) (uint64, error) {
	cwd := filepath.Clean(spec.Cwd)
	bestIndex := -1
	bestDepth := -1
	for i, unit := range spec.Units {
		rel, err := filepath.Rel(filepath.Clean(unit.Target), cwd)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		depth := strings.Count(filepath.Clean(unit.Target), string(filepath.Separator))
		if depth > bestDepth {
			bestIndex = i
			bestDepth = depth
		}
	}
	if bestIndex < 0 || bestIndex >= len(mountIDs) || mountIDs[bestIndex] == 0 {
		return 0, fmt.Errorf("cwd %s has no mounted snapshot view", spec.Cwd)
	}
	return mountIDs[bestIndex], nil
}

func snapshotOverlayMountID(u *SnapshotUnitSpec, root *os.File) (uint64, error) {
	view, err := openSnapshotTargetFromRoot(u, root, false)
	if err != nil {
		return 0, err
	}
	defer view.Close()
	var fs unix.Statfs_t
	if err := unix.Fstatfs(int(view.Fd()), &fs); err != nil {
		return 0, fmt.Errorf("stat snapshot overlay: %w", err)
	}
	if fs.Type != unix.OVERLAYFS_SUPER_MAGIC {
		return 0, errors.New("snapshot mount is not overlayfs")
	}
	return snapshotMountIDFromFD(int(view.Fd()))
}

func snapshotMountIDFromFD(fd int) (uint64, error) {
	var stat unix.Statx_t
	if err := unix.Statx(fd, "", unix.AT_EMPTY_PATH, unix.STATX_MNT_ID, &stat); err != nil {
		return 0, err
	}
	if stat.Mask&unix.STATX_MNT_ID == 0 || stat.Mnt_id == 0 {
		return 0, errors.New("mount id is unavailable")
	}
	return stat.Mnt_id, nil
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
func openSnapshotTarget(u *SnapshotUnitSpec) (*os.File, error) {
	return openSnapshotTargetAt(u, u.Target, true)
}

func openSnapshotTargetAt(u *SnapshotUnitSpec, path string, verifyIdentity bool) (*os.File, error) {
	if u.Target == "" || path == "" {
		return nil, errors.New("unit is missing target")
	}
	fd, err := unix.Open(path, unix.O_PATH|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open target %s: %w", u.Target, err)
	}
	return validateSnapshotTargetFD(u, fd, verifyIdentity)
}

// openSnapshotTargetFromRoot resolves every target component through the
// stable root descriptor and rejects symlinks atomically. Nested overlays need
// a target in the parent's visible mount, while their lower source is the
// separately verified pristine descriptor; following a replacement symlink
// here could otherwise mount an unrelated host directory.
func openSnapshotTargetFromRoot(u *SnapshotUnitSpec, root *os.File, verifyIdentity bool) (*os.File, error) {
	if u.Target == "" {
		return nil, errors.New("unit is missing target")
	}
	if !filepath.IsAbs(u.Target) {
		return nil, fmt.Errorf("snapshot target must be absolute: %q", u.Target)
	}
	if root == nil {
		return nil, errors.New("snapshot root anchor is missing")
	}
	relative := strings.TrimPrefix(filepath.Clean(u.Target), string(filepath.Separator))
	if relative == "" {
		relative = "."
	}
	fd, err := unix.Openat2(int(root.Fd()), relative, &unix.OpenHow{
		Flags:   unix.O_PATH | unix.O_DIRECTORY | unix.O_CLOEXEC,
		Resolve: unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS,
	})
	if err != nil {
		return nil, fmt.Errorf("open target %s: %w", u.Target, err)
	}
	return validateSnapshotTargetFD(u, fd, verifyIdentity)
}

func validateSnapshotTargetFD(u *SnapshotUnitSpec, fd int, verifyIdentity bool) (*os.File, error) {
	file := os.NewFile(uintptr(fd), u.Target)
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("stat target %s: %w", u.Target, err)
	}
	if verifyIdentity && u.TargetIdentitySet &&
		(uint64(stat.Dev) != u.TargetDevice || uint64(stat.Ino) != u.TargetInode) {
		_ = file.Close()
		return nil, fmt.Errorf("target %s changed before snapshot mount", u.Target)
	}
	return file, nil
}

// snapshotTargetStillAtPath detects a rename after the target descriptor was
// opened. Mounting through the descriptor cannot follow a replacement symlink,
// but proceeding after the named target has moved would leave the resource path
// pointing outside the snapshot view.
func snapshotTargetStillAtPath(target *os.File, expected string) error {
	if target == nil || expected == "" {
		return errors.New("snapshot target path check is missing target")
	}
	actual, err := os.Readlink(filepath.Join("/proc/self/fd", fmt.Sprint(target.Fd())))
	if err != nil {
		return fmt.Errorf("read opened target path: %w", err)
	}
	if filepath.Clean(actual) != filepath.Clean(expected) {
		return fmt.Errorf("snapshot target moved before mount: opened=%q expected=%q", actual, expected)
	}
	return nil
}

// snapshotTargetsShareInode 保证普通 bind lowerdir 与将承载嵌套 overlay 的
// target 指向同一 inode。Git artifact snapshot 刻意使用独立的私有副本。
func snapshotTargetsShareInode(lower, target *os.File) error {
	if lower == nil || target == nil {
		return errors.New("nested snapshot target check is missing descriptor")
	}
	var lowerStat, targetStat unix.Stat_t
	if err := unix.Fstat(int(lower.Fd()), &lowerStat); err != nil {
		return fmt.Errorf("stat verified nested lower: %w", err)
	}
	if err := unix.Fstat(int(target.Fd()), &targetStat); err != nil {
		return fmt.Errorf("stat nested mount target: %w", err)
	}
	if lowerStat.Ino != targetStat.Ino {
		return fmt.Errorf("nested mount target changed before snapshot mount")
	}
	return nil
}

func mountSnapshotUnit(u *SnapshotUnitSpec, lower, target *os.File, gitArtifact *SnapshotGitArtifactSpec, gitRoot bool) error {
	if u.Target == "" || u.Alias == "" || u.Upper == "" || u.Work == "" {
		return errors.New("unit is missing target/alias/upper/work")
	}
	if lower == nil || target == nil {
		return errors.New("unit is missing opened lower or target")
	}
	for _, d := range []string{u.Alias, u.Upper, u.Work} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return fmt.Errorf("prepare %s: %w", d, err)
		}
	}
	if gitArtifact != nil {
		switch {
		case gitRoot:
			if err := materializeSnapshotGitLower(lower, u.Alias, gitArtifact); err != nil {
				return fmt.Errorf("materialize Git private lower for %s: %w", u.Target, err)
			}
		case lower != target:
			if err := materializeSnapshotLower(lower, u.Alias); err != nil {
				return fmt.Errorf("materialize nested private lower for %s: %w", u.Target, err)
			}
		default:
			// A Git metadata unit is covered by the independent clone made for
			// the repository root. Keep this lower empty; mounting the host
			// descriptor here would reintroduce the very source of drift this
			// private view is meant to prevent.
		}
	} else {
		lowerFDPath := filepath.Join("/proc/self/fd", fmt.Sprint(lower.Fd()))
		if err := unix.Mount(lowerFDPath, u.Alias, "", unix.MS_BIND, ""); err != nil {
			return fmt.Errorf("bind %s -> %s: %w", u.Target, u.Alias, err)
		}
	}
	targetFDPath := filepath.Join("/proc/self/fd", fmt.Sprint(target.Fd()))
	opts := fmt.Sprintf("lowerdir=%s,upperdir=%s,workdir=%s,userxattr", u.Alias, u.Upper, u.Work)
	if err := unix.Mount("overlay", targetFDPath, "overlay", 0, opts); err != nil {
		return fmt.Errorf("overlay on %s: %w", u.Target, err)
	}
	return nil
}

// materializeSnapshotGitLower creates a complete private repository at the
// recorded baseline. The source descriptor is used only to pack objects
// reachable from that commit. It imports neither refs, local configuration nor
// remotes from the mutable host view.
func materializeSnapshotGitLower(lower *os.File, destination string, spec *SnapshotGitArtifactSpec) error {
	if lower == nil || spec == nil || spec.Baseline == "" {
		return errors.New("Git private lower source or baseline is missing")
	}
	sourceFD, err := unix.Dup(int(lower.Fd()))
	if err != nil {
		return fmt.Errorf("duplicate Git private lower source: %w", err)
	}
	sourceFile := os.NewFile(uintptr(sourceFD), "snapshot-git-source")
	defer sourceFile.Close()
	env := snapshotGitEnvironment(nil)
	objectFormat, err := snapshotGitObjectFormat(sourceFile, env)
	if err != nil {
		return err
	}
	if out, err := runSnapshotGitSourceCommand(sourceFile, env, "cat-file", "-e", spec.Baseline+"^{commit}"); err != nil {
		return fmt.Errorf("verify private baseline %s in source: %s: %w", spec.Baseline, strings.TrimSpace(out), err)
	}
	if out, err := runSnapshotGitCommand("", env, "init", "--quiet", "--object-format="+objectFormat, destination); err != nil {
		return fmt.Errorf("initialize private Git repository with %s object format: %s: %w", objectFormat, strings.TrimSpace(out), err)
	}
	if err := preserveSnapshotGitIdentity(sourceFile, destination, env); err != nil {
		return err
	}
	if err := importSnapshotGitBaseline(sourceFile, destination, spec.Baseline, env); err != nil {
		return err
	}
	if out, err := runSnapshotGitCommand(destination, env, "update-ref", "refs/snapshot/baseline", spec.Baseline); err != nil {
		return fmt.Errorf("record private baseline ref: %s: %w", strings.TrimSpace(out), err)
	}
	if out, err := runSnapshotGitCommand(destination, env, "checkout", "--detach", "--quiet", spec.Baseline); err != nil {
		return fmt.Errorf("checkout baseline %s: %s: %w", spec.Baseline, strings.TrimSpace(out), err)
	}
	if out, err := runSnapshotGitCommand(destination, env, "rev-parse", "HEAD"); err != nil || strings.TrimSpace(out) != spec.Baseline {
		if err != nil {
			return fmt.Errorf("verify private baseline: %w", err)
		}
		return fmt.Errorf("private baseline is %s, want %s", strings.TrimSpace(out), spec.Baseline)
	}
	if out, err := runSnapshotGitCommand(destination, env, "status", "--porcelain"); err != nil {
		return fmt.Errorf("verify private baseline status: %w", err)
	} else if strings.TrimSpace(out) != "" {
		return fmt.Errorf("private baseline is not clean: %s", strings.TrimSpace(out))
	}
	return nil
}

func snapshotGitObjectFormat(source *os.File, env []string) (string, error) {
	out, err := runSnapshotGitSourceCommand(source, env, "rev-parse", "--show-object-format")
	if err != nil {
		return "", fmt.Errorf("resolve source Git object format: %s: %w", strings.TrimSpace(out), err)
	}
	format := strings.TrimSpace(out)
	switch format {
	case "sha1", "sha256":
		return format, nil
	default:
		return "", fmt.Errorf("unsupported source Git object format %q", format)
	}
}

func preserveSnapshotGitIdentity(source *os.File, destination string, env []string) error {
	for _, key := range []string{"user.name", "user.email"} {
		out, err := runSnapshotGitSourceCommand(source, env, "config", "--local", "--no-includes", "--get", key)
		if err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
				continue
			}
			return fmt.Errorf("read private Git identity %s: %s: %w", key, strings.TrimSpace(out), err)
		}
		if _, err := runSnapshotGitCommand(destination, env, "config", "--local", key, strings.TrimSuffix(out, "\n")); err != nil {
			return fmt.Errorf("record private Git identity %s: %w", key, err)
		}
	}
	return nil
}

func importSnapshotGitBaseline(source *os.File, destination, baseline string, env []string) error {
	pack := exec.Command("git", "-C", "/proc/self/fd/3", "pack-objects", "--stdout", "--revs")
	pack.Env = env
	pack.ExtraFiles = []*os.File{source}
	pack.Stdin = strings.NewReader(baseline + "\n")
	packOut, err := pack.StdoutPipe()
	if err != nil {
		return fmt.Errorf("open private baseline object stream: %w", err)
	}
	var packErr bytes.Buffer
	pack.Stderr = &packErr

	index := exec.Command("git", "-C", destination, "index-pack", "--stdin", "--fix-thin")
	index.Env = env
	index.Stdin = packOut
	var indexOut bytes.Buffer
	index.Stdout = &indexOut
	index.Stderr = &indexOut
	if err := pack.Start(); err != nil {
		return fmt.Errorf("start private baseline object pack: %w", err)
	}
	if err := index.Start(); err != nil {
		_ = pack.Process.Kill()
		_ = pack.Wait()
		return fmt.Errorf("start private baseline object import: %w", err)
	}
	if err := pack.Wait(); err != nil {
		_ = index.Wait()
		return fmt.Errorf("pack private baseline objects: %s: %w", strings.TrimSpace(packErr.String()), err)
	}
	if err := index.Wait(); err != nil {
		return fmt.Errorf("import private baseline objects: %s: %w", strings.TrimSpace(indexOut.String()), err)
	}
	if out, err := runSnapshotGitCommand(destination, env, "cat-file", "-e", baseline+"^{commit}"); err != nil {
		return fmt.Errorf("verify imported private baseline %s: %s: %w", baseline, strings.TrimSpace(out), err)
	}
	return nil
}

func runSnapshotGitSourceCommand(source *os.File, env []string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", "/proc/self/fd/3"}, args...)...)
	cmd.Env = env
	cmd.ExtraFiles = []*os.File{source}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func runSnapshotGitCommand(directory string, env []string, args ...string) (string, error) {
	full := args
	if directory != "" {
		full = append([]string{"-C", directory}, args...)
	}
	cmd := exec.Command("git", full...)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// materializeSnapshotLower 为 Git artifact snapshot 生成稳定的 lowerdir。
// bind mount 只是实时源文件系统的别名，无法在主工作目录持续变更时表示时间点。
func materializeSnapshotLower(lower *os.File, destination string) error {
	if lower == nil {
		return errors.New("private lower source is missing")
	}
	entries, err := os.ReadDir(destination)
	if err != nil {
		return fmt.Errorf("read private lower root: %w", err)
	}
	if len(entries) != 0 {
		return errors.New("private lower root is not empty")
	}
	sourcePath := filepath.Join("/proc/self/fd", fmt.Sprint(lower.Fd()))
	sourceFD, err := unix.Open(sourcePath, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open private lower source: %w", err)
	}
	return copySnapshotDirectory(sourceFD, destination)
}

func copySnapshotDirectory(sourceFD int, destination string) error {
	source := os.NewFile(uintptr(sourceFD), "snapshot-lower")
	defer source.Close()
	var info unix.Stat_t
	if err := unix.Fstat(sourceFD, &info); err != nil {
		return fmt.Errorf("stat source directory: %w", err)
	}
	if info.Mode&unix.S_IFMT != unix.S_IFDIR {
		return errors.New("private lower source is not a directory")
	}
	if err := os.Chmod(destination, os.FileMode(info.Mode&0o777)); err != nil {
		return fmt.Errorf("set private lower directory mode: %w", err)
	}
	entries, err := source.ReadDir(-1)
	if err != nil {
		return fmt.Errorf("read source directory: %w", err)
	}
	for _, entry := range entries {
		if err := copySnapshotEntry(sourceFD, destination, entry.Name()); err != nil {
			return err
		}
	}
	return nil
}

func copySnapshotEntry(sourceFD int, destination, name string) error {
	var before unix.Stat_t
	if err := unix.Fstatat(sourceFD, name, &before, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return fmt.Errorf("stat private lower entry %q: %w", name, err)
	}
	destinationPath := filepath.Join(destination, name)
	switch before.Mode & unix.S_IFMT {
	case unix.S_IFDIR:
		if err := os.Mkdir(destinationPath, os.FileMode(before.Mode&0o777)); err != nil {
			return fmt.Errorf("create private lower directory %q: %w", name, err)
		}
		childFD, err := unix.Openat(sourceFD, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return fmt.Errorf("open private lower directory %q: %w", name, err)
		}
		var after unix.Stat_t
		if err := unix.Fstat(childFD, &after); err != nil {
			_ = unix.Close(childFD)
			return fmt.Errorf("stat private lower directory %q: %w", name, err)
		}
		if before.Dev != after.Dev || before.Ino != after.Ino {
			_ = unix.Close(childFD)
			return fmt.Errorf("private lower directory %q changed while materializing", name)
		}
		if err := copySnapshotDirectory(childFD, destinationPath); err != nil {
			return err
		}
		return nil
	case unix.S_IFREG:
		return copySnapshotRegularFile(sourceFD, destinationPath, name, &before)
	case unix.S_IFLNK:
		target, err := readSnapshotSymlink(sourceFD, name)
		if err != nil {
			return err
		}
		if err := os.Symlink(target, destinationPath); err != nil {
			return fmt.Errorf("copy private lower symlink %q: %w", name, err)
		}
		return nil
	default:
		return fmt.Errorf("private lower entry %q has unsupported mode %o", name, before.Mode)
	}
}

func copySnapshotRegularFile(sourceFD int, destination, name string, before *unix.Stat_t) error {
	fd, err := unix.Openat(sourceFD, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open private lower file %q: %w", name, err)
	}
	source := os.NewFile(uintptr(fd), "snapshot-lower-file")
	defer source.Close()
	var after unix.Stat_t
	if err := unix.Fstat(fd, &after); err != nil {
		return fmt.Errorf("stat private lower file %q: %w", name, err)
	}
	if after.Mode&unix.S_IFMT != unix.S_IFREG || before.Dev != after.Dev || before.Ino != after.Ino {
		return fmt.Errorf("private lower file %q changed while materializing", name)
	}
	destinationFile, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, os.FileMode(before.Mode&0o777))
	if err != nil {
		return fmt.Errorf("create private lower file %q: %w", name, err)
	}
	_, copyErr := io.Copy(destinationFile, source)
	closeErr := destinationFile.Close()
	if copyErr != nil {
		return fmt.Errorf("copy private lower file %q: %w", name, copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close private lower file %q: %w", name, closeErr)
	}
	return nil
}

func readSnapshotSymlink(sourceFD int, name string) (string, error) {
	buf := make([]byte, 4096)
	n, err := unix.Readlinkat(sourceFD, name, buf)
	if err != nil {
		return "", fmt.Errorf("read private lower symlink %q: %w", name, err)
	}
	if n == len(buf) {
		return "", fmt.Errorf("private lower symlink %q exceeds the supported length", name)
	}
	return string(buf[:n]), nil
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
	mounts, err := mountSnapshotViews(&spec)
	if err != nil {
		fmt.Fprintf(os.Stderr, "snapshot-probe: mount: %v\n", err)
		return 1
	}
	defer mounts.Close()
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
