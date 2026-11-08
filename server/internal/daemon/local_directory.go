package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

// localDirectoryResourceType is the project_resource discriminator the daemon
// looks for when deciding whether a task should run against an existing
// user directory rather than a fresh git worktree. Mirrors the server-side
// constant — keep in sync if the type string is ever renamed.
const localDirectoryResourceType = "local_directory"

// Execution modes for local_directory resources. Mirrors the server-side
// constants in handler/project_resource.go — keep in sync. An absent or empty
// value means in_place, so resources created before worktree mode existed keep
// their original behavior.
const (
	localDirectoryModeInPlace  = "in_place"
	localDirectoryModeWorktree = "worktree"
	// localDirectoryModeSnapshot runs each task on a per-task copy-on-write
	// view of the directory (RUYI-669). Mirrors the server-side constant.
	localDirectoryModeSnapshot = "snapshot"
)

// localDirectoryRef mirrors the server-side ref shape for local_directory
// project resources. Defined locally so the daemon does not have to import
// the server handler package.
type localDirectoryRef struct {
	LocalPath     string `json:"local_path"`
	DaemonID      string `json:"daemon_id"`
	Label         string `json:"label,omitempty"`
	ExecutionMode string `json:"execution_mode,omitempty"`
	// Primary marks the default snapshot directory of a multi-resource
	// project: where an undeclared task's workdir lands. Server-enforced to
	// at most one per project; the daemon only reads it as a tiebreaker.
	Primary bool `json:"primary,omitempty"`
	// Subdirectories is the resource's effective set; empty = whole
	// directory. Task declarations must sit inside it (server-validated;
	// re-checked here because the daemon is the last line of defense).
	Subdirectories []string `json:"subdirectories,omitempty"`
}

// localDirectoryAssignment is the resolved view of a task's local_directory
// resource: the absolute path the daemon will use as the agent's workdir,
// plus the underlying ref for callers that still need the raw label / daemon
// id (validation log messages, mostly). RealPath is the symlink-resolved
// absolute path; the path mutex keys on it so two different routes to the
// same directory are serialised.
type localDirectoryAssignment struct {
	Ref      localDirectoryRef
	AbsPath  string // user-provided path, cleaned but not symlink-resolved
	RealPath string // canonical key for the path mutex
}

// UsesWorktree reports whether this assignment runs each task in its own git
// worktree instead of in the user's directory. Worktree tasks skip the per-path
// mutex entirely — that is the whole point of the mode — so every caller that
// serialises, cleans up sidecars, or exempts the env root from GC must branch
// on this rather than on "is there a local_directory assignment at all".
func (a *localDirectoryAssignment) UsesWorktree() bool {
	return a != nil && strings.TrimSpace(a.Ref.ExecutionMode) == localDirectoryModeWorktree
}

// DisplayName is the human-facing name for this directory, safe to render in
// UI. It is deliberately NOT the absolute path: the wait reason built from it
// is stored server-side and pushed to every client on the session, and a chip
// in a chat transcript ends up in screen shares and screenshots. Absolute
// paths carry the account name, which is why handler.relativeWorkDir exists
// for the sibling work_dir chip; this is the same contract for the same reason.
//
// The resource's own label wins when the user set one — that is the name they
// chose for this directory. Otherwise the basename, which is what distinguishes
// sibling checkouts ("NuvioTV" vs "multica") without naming their parent.
func (a *localDirectoryAssignment) DisplayName() string {
	if a == nil {
		return ""
	}
	if label := strings.TrimSpace(a.Ref.Label); label != "" {
		return label
	}
	return filepath.Base(a.AbsPath)
}

// ValidateExecutionMode rejects a mode this daemon does not implement.
//
// Falling back to in_place would be the wrong direction, even though it is the
// older and more conservative code path. execution_mode is how a user asks for
// ISOLATION, not merely for concurrency: silently running in_place instead
// would let the agent edit the working copy the user explicitly asked it to
// stay out of. Losing concurrency is a nuisance; ignoring a request to not
// touch someone's files is a broken promise. So an unrecognised mode fails the
// task with a message naming the version skew.
func (a *localDirectoryAssignment) ValidateExecutionMode() error {
	if a == nil {
		return nil
	}
	switch strings.TrimSpace(a.Ref.ExecutionMode) {
	case "", localDirectoryModeInPlace, localDirectoryModeWorktree:
		return nil
	case localDirectoryModeSnapshot:
		// Snapshot assignments are resolved by the snapshot plan path, not
		// this one; if one ever reaches here (phase-2 per-subdir routing),
		// the claim gate has already capability-checked the daemon. Accept.
		return nil
	default:
		return fmt.Errorf(
			"local_directory: this daemon does not support execution_mode %q for %q "+
				"(update the daemon, or set the resource's execution mode to %q or %q); "+
				"refusing to run in place, since that would modify a directory the resource asked to isolate",
			a.Ref.ExecutionMode, a.AbsPath, localDirectoryModeInPlace, localDirectoryModeWorktree)
	}
}

// localDirectoryAssignmentForTask returns the local_directory assignment a task
// should execute inside. Squad-leader tasks are coordinators: they may create
// child issues or comments, but should not bind to the user's repo worktree or
// hold the path mutex while downstream workers are ready to write.
//
// This answers WHERE a task runs, and only that. Its result also drives the
// agent's working directory (daemon.runTask plumbs AbsPath into
// execenv.PrepareParams.LocalWorkDir) and the GC-meta stamp that exempts a
// user-owned path from env-root cleanup. Whether the task additionally takes
// the per-path mutex is a SEPARATE question, answered by
// localDirectoryLockExempt — collapsing the two is what made a read-only chat
// turn queue behind a 20-minute build (issue #7344), and answering "no
// assignment" there to free the lock would have silently moved chat out of the
// user's directory as well.
func localDirectoryAssignmentForTask(task Task, daemonID string) (*localDirectoryAssignment, error) {
	if task.IsLeaderTask {
		return nil, nil
	}
	return findLocalDirectoryAssignment(task.ProjectResources, daemonID)
}

// validateWorktreeGitMetadata applies the same read-only containment audit to
// the established worktree backend that snapshot delivery uses. The gate can
// leave an ordinary worktree resource out of snapshot planning, but it must
// never let git worktree operations follow a metadata redirect into an
// unmaterialized host path.
func validateWorktreeGitMetadata(assignment *localDirectoryAssignment) error {
	if !assignment.UsesWorktree() {
		return nil
	}
	unit := snapshotUnit{Ref: assignment.Ref, AbsPath: assignment.AbsPath}
	audit := auditSnapshotGitScope(&snapshotPlan{Units: []snapshotUnit{unit}, Primary: &unit})
	if !audit.Safe {
		return fmt.Errorf("local_directory: unsafe Git metadata prevents worktree preparation: %s", audit.Reason)
	}
	commonDirs := gitSnapshotCommonDirs(audit.Repositories)
	for _, repository := range audit.Repositories {
		writableMetadata := compactContainedPaths([]string{repository.GitDir, repository.CommonDir})
		if err := auditGitMetadataRedirectsWithin(repository.Worktree, repository.GitDir, repository.CommonDir, repository.Dependencies, writableMetadata, repository.Dependencies, commonDirs, true); err != nil {
			return fmt.Errorf("local_directory: unsafe Git metadata prevents worktree preparation: %s", err)
		}
	}
	return nil
}

// localDirectoryLockExempt reports whether a task may run inside an in_place
// local_directory WITHOUT serialising on the per-path mutex. It is asked only
// after an assignment has been resolved and validated, so an exempt task still
// runs in the user's directory — it just doesn't queue for it.
//
// What the mutex actually protects: two long coding runs interleaving two sets
// of edits into one working tree. It was never a general write barrier and
// cannot become one — the user's own editor, their terminal, and any external
// script write to that same tree unsynchronised, and always have. So the
// question a task must answer to earn the lock is not "could it ever write?"
// (everything could) but "is it a second heavyweight writer?".
//
// A chat turn is not. It is a conversation that reads the tree to answer
// questions and at most saves a file the way the user's own Cmd+S does — the
// risk class the lock already declines to cover. Serialising it bought nothing
// and muted the squad leader for the length of every build (issue #7344).
//
// Keyed on ChatSessionID because that is the daemon's only chat discriminator
// (see Task.ChatSessionID). IsLeaderTask cannot serve here even though the
// comment above it describes chat's semantics exactly: the server never writes
// that column on the chat-task insert path (service.EnqueueChatTask →
// db.CreateChatTaskParams has no such field), so it is false on every chat
// turn ever dispatched.
func localDirectoryLockExempt(task Task) bool {
	return task.ChatSessionID != ""
}

// findLocalDirectoryAssignment scans the task's project resources for one of
// type local_directory whose daemon_id matches this daemon. Returns nil
// (without error) when no such resource exists — the task takes the regular
// github_repo / worktree code path. Returns an error only when the matching
// resource is structurally broken (bad JSON, missing fields) OR when more
// than one resource is pinned to this daemon — that's a server-side
// invariant violation, and silently picking the first match would let the
// agent write into an arbitrary directory the user didn't intend.
//
// Server-side `findLocalDirectoryConflict` enforces a single local_directory
// per (project, daemon), so two matches here means either the constraint
// was bypassed (older API client) or the data was corrupted. Either way,
// fail fast rather than guess.
func findLocalDirectoryAssignment(resources []ProjectResourceData, daemonID string) (*localDirectoryAssignment, error) {
	var match *localDirectoryAssignment
	for _, r := range resources {
		if r.ResourceType != localDirectoryResourceType {
			continue
		}
		var ref localDirectoryRef
		if err := json.Unmarshal(r.ResourceRef, &ref); err != nil {
			return nil, fmt.Errorf("local_directory: parse resource_ref: %w", err)
		}
		if strings.TrimSpace(ref.ExecutionMode) == localDirectoryModeSnapshot {
			// Snapshot rows are routed by the snapshot plan (resource_id /
			// primary), never by the daemon-pick heuristic — a project may
			// legitimately carry several snapshot rows for this daemon. Skip
			// them here so the one-per-daemon multiplicity error below keeps
			// describing only the rows this resolver actually routes.
			continue
		}
		ref.DaemonID = strings.TrimSpace(ref.DaemonID)
		if ref.DaemonID == "" {
			return nil, errors.New("local_directory: resource_ref missing daemon_id")
		}
		if ref.DaemonID != daemonID {
			// A different daemon owns this resource. Skip silently; the
			// project may have multiple local_directory resources, one
			// per daemon, and other daemons will resolve their own row.
			continue
		}
		if match != nil {
			// Server-side invariant: at most one local_directory per
			// (project, daemon). Two matches here means the constraint
			// was bypassed by an older API client or by direct DB writes.
			// Either way, refuse to guess which directory the user meant.
			return nil, fmt.Errorf(
				"local_directory: project has multiple local_directory resources for this daemon (%q and %q); remove the extra in project settings",
				match.AbsPath,
				strings.TrimSpace(ref.LocalPath),
			)
		}
		absPath, err := normalizeLocalPath(ref.LocalPath)
		if err != nil {
			return nil, err
		}
		realPath, err := resolveRealPath(absPath)
		if err != nil {
			return nil, err
		}
		match = &localDirectoryAssignment{
			Ref:      ref,
			AbsPath:  absPath,
			RealPath: realPath,
		}
	}
	return match, nil
}

// normalizeLocalPath strips whitespace and resolves the path to an absolute
// cleaned form. It does NOT touch the filesystem (no symlink resolution, no
// existence check) — callers do that separately via validateLocalPath.
func normalizeLocalPath(p string) (string, error) {
	trimmed := strings.TrimSpace(p)
	if trimmed == "" {
		return "", errors.New("local_directory: local_path is empty")
	}
	if !filepath.IsAbs(trimmed) {
		return "", fmt.Errorf("local_directory: local_path must be absolute, got %q", trimmed)
	}
	return filepath.Clean(trimmed), nil
}

// resolveRealPath returns the symlink-resolved absolute form of path. The
// path mutex keys on this value so a task on `/Users/u/proj` and another on
// `/private/var/folders/.../proj-symlink → /Users/u/proj` collapse to one
// lock. When EvalSymlinks fails (path is missing or not yet a real link),
// fall back to the cleaned absolute form so callers can still proceed to
// the existence-check stage which surfaces a clearer error.
func resolveRealPath(absPath string) (string, error) {
	real, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		// validateLocalPath will surface the underlying error with better
		// context; for the mutex key the cleaned absolute path is a safe
		// fallback (it just slightly weakens the dedup on broken symlinks).
		return absPath, nil
	}
	return real, nil
}

// validateLocalPath enforces the daemon-side preconditions for running an
// agent against a user-supplied directory:
//
//   - the path is absolute and not in the system blacklist (root, $HOME,
//     /Users, /home, the current user's $HOME — picking one of those would
//     scope the agent to the entire account, which is never what the user
//     intended);
//   - the symlink-resolved target is ALSO not in the blacklist — without
//     this a symlink like /Users/me/proj/home -> /Users/me would slip the
//     literal-equality check above while still routing every daemon write
//     into $HOME;
//   - the path exists, is a directory (not a regular file or device);
//   - the daemon process can read and write inside it (the agent will need
//     both — read for context discovery, write for the issue's edits).
//
// Each failure returns a typed error message so the daemon can forward it
// onto the task's fail comment verbatim.
func validateLocalPath(absPath string) error {
	if absPath == "" {
		return errors.New("local_directory: local_path is empty")
	}
	if !filepath.IsAbs(absPath) {
		return fmt.Errorf("local_directory: local_path must be absolute, got %q", absPath)
	}
	if reason, blocked := isBlacklistedLocalPath(absPath); blocked {
		return fmt.Errorf("local_directory: %s (%q)", reason, absPath)
	}
	info, err := os.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("local_directory: path does not exist: %q", absPath)
		}
		return fmt.Errorf("local_directory: stat %q: %w", absPath, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("local_directory: path is not a directory: %q", absPath)
	}
	// Re-check the blacklist after resolving symlinks. Two ways the
	// literal check can be bypassed even when absPath itself is clean:
	//
	//   1. A user-created symlink (or a parent component) routes writes
	//      into a banned target. Example: ~/proj/home-link -> /Users/me.
	//   2. The user directly selects a canonical OS path that aliases a
	//      banned root via an OS-level symlink. Example on macOS: typing
	//      /private/tmp slips past the /tmp entry because the literal
	//      strings don't match, and EvalSymlinks is a no-op since the
	//      input is already canonical. This must be checked
	//      unconditionally — not gated on realPath != absPath — or the
	//      direct-canonical case is silently allowed.
	//
	// EvalSymlinks walks intermediate components too, so a non-symlink
	// absPath whose parent is a symlink also fails closed.
	realPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		return fmt.Errorf("local_directory: resolve symlinks for %q: %w", absPath, err)
	}
	realPath = filepath.Clean(realPath)
	if reason, blocked := isBlacklistedRealPath(realPath); blocked {
		if realPath != filepath.Clean(absPath) {
			return fmt.Errorf("local_directory: %s (symlink target of %q is %q)", reason, absPath, realPath)
		}
		return fmt.Errorf("local_directory: %s (canonical path %q)", reason, absPath)
	}
	if err := checkDirReadWrite(absPath); err != nil {
		return fmt.Errorf("local_directory: %w", err)
	}
	return nil
}

// isBlacklistedLocalPath rejects paths that map to the whole machine or an
// entire user profile. The intent is to keep the daemon from accidentally
// stamping context files (.agent_context/, .claude/skills/, .multica/) at
// the root of a user's account or the OS — a misconfiguration on the UI
// side should fail fast rather than litter the user's home.
//
// The check is by literal equality after Clean(), not prefix containment:
// a legitimate project under /Users/<user>/code/proj should pass.
func isBlacklistedLocalPath(absPath string) (reason string, blocked bool) {
	cleaned := filepath.Clean(absPath)
	if isDriveRoot(cleaned) {
		return fmt.Sprintf("path is a drive root %q", cleaned), true
	}
	for _, banned := range systemRootBlacklist() {
		if cleaned == banned {
			return fmt.Sprintf("path is a protected system root %q", banned), true
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		if cleaned == filepath.Clean(home) {
			return "path is the user's home directory", true
		}
	}
	return "", false
}

// isBlacklistedRealPath is the canonical-aware variant of
// isBlacklistedLocalPath. It compares the symlink-resolved realPath against
// the symlink-resolved form of each blacklist entry so OS-level redirects
// (notably macOS's /etc -> /private/etc, /tmp -> /private/tmp, /var ->
// /private/var) cannot be used to slip a candidate past the literal
// blacklist — whether the redirect is reached via a user-created symlink
// (~/proj/home-link -> /Users/me) or by directly typing the canonical form
// (/private/tmp), which is identical to the OS view of /tmp.
func isBlacklistedRealPath(realPath string) (reason string, blocked bool) {
	realClean := filepath.Clean(realPath)
	if isDriveRoot(realClean) {
		return fmt.Sprintf("path is a drive root %q", realClean), true
	}
	for _, banned := range systemRootBlacklist() {
		bannedClean := filepath.Clean(banned)
		if realClean == bannedClean {
			return fmt.Sprintf("path is a protected system root %q", banned), true
		}
		if r, err := filepath.EvalSymlinks(banned); err == nil {
			if filepath.Clean(r) == realClean {
				return fmt.Sprintf("path is a protected system root %q", banned), true
			}
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		homeClean := filepath.Clean(home)
		if realClean == homeClean {
			return "path is the user's home directory", true
		}
		if r, err := filepath.EvalSymlinks(home); err == nil {
			if filepath.Clean(r) == realClean {
				return "path is the user's home directory", true
			}
		}
	}
	return "", false
}

// isDriveRoot reports whether absPath is the root of a Windows volume — any
// of `C:\`, `D:\`, ..., `Z:\`, plus less common cases like `\\server\share`
// (filepath.VolumeName treats UNC roots as volumes too). On non-Windows
// this is always false because POSIX has no concept of drive letters and
// `/` is covered by systemRootBlacklist.
//
// We rely on filepath.VolumeName rather than enumerating drive letters
// statically: removable / network drives can be mounted at any letter
// (`G:\`, `H:\`, ...), and Windows installs are increasingly happy to put
// the user profile on a non-C drive. A static list (C..F) would miss them
// all.
func isDriveRoot(absPath string) bool {
	if runtime.GOOS != "windows" {
		return false
	}
	vol := filepath.VolumeName(absPath)
	if vol == "" {
		return false
	}
	// VolumeName returns the volume without trailing separator (`C:` or
	// `\\srv\share`). A drive root is volume + one separator (or, after
	// filepath.Clean, just the volume on bare-volume input).
	rest := absPath[len(vol):]
	return rest == "" || rest == `\` || rest == "/"
}

// systemRootBlacklist returns the per-OS list of paths the daemon never
// allows as a local_directory root. POSIX systems get `/`, `/Users`, `/home`
// (and macOS's `/Users/Shared` for good measure); Windows gets the
// well-known account / shared trees under C:. Drive roots themselves are
// handled by isDriveRoot so we don't have to enumerate G:\, H:\, etc.
// The list is intentionally conservative — it errs on the side of
// rejecting more, since the desktop UI is expected to surface a friendly
// picker that never produces these values.
func systemRootBlacklist() []string {
	if runtime.GOOS == "windows" {
		return []string{`C:\Users`, `C:\ProgramData`, `C:\Program Files`, `C:\Program Files (x86)`, `C:\Windows`}
	}
	return []string{"/", "/Users", "/Users/Shared", "/home", "/root", "/var", "/etc", "/tmp", "/usr", "/opt"}
}

// checkDirReadWrite verifies the daemon process can both read directory
// contents and create/remove a probe file inside dir. The probe filename is
// long, hidden, and unlikely to clash with user files; we delete it
// immediately and ignore the delete error (best-effort cleanup is fine —
// the worst case is leaving a 0-byte file the user can ignore).
func checkDirReadWrite(dir string) error {
	if _, err := os.ReadDir(dir); err != nil {
		return fmt.Errorf("read %q: %w", dir, err)
	}
	probe, err := os.CreateTemp(dir, ".multica-rwcheck-*")
	if err != nil {
		return fmt.Errorf("write %q: %w", dir, err)
	}
	probePath := probe.Name()
	_ = probe.Close()
	_ = os.Remove(probePath)
	return nil
}

// isGitWorkTree reports whether path is the working tree of a git repo. The
// daemon uses this to skip branch / worktree machinery when the user has
// already pointed the project at their own clone — the agent operates on
// the current branch in place. Returns false on any error (git not on PATH,
// path not in a repo, exec failure) so the caller can treat "not a git
// tree" and "can't tell" the same way: skip the git-specific path.
func isGitWorkTree(ctx context.Context, path string) bool {
	cmd := exec.CommandContext(ctx, "git", "-C", path, "rev-parse", "--is-inside-work-tree")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "true"
}

// LocalPathLocker serialises agent tasks that share the same on-disk path.
// The lock is owned for the entire lifetime of a task (claim → context
// write → agent execution → result report), not just the agent execution
// window, because the context files and skill scratch directories the
// daemon writes at task-prepare time can race with a sibling task on the
// same path.
//
// Implementation: per-key sync.Mutex inside a map guarded by mu. When a
// task can't take the lock immediately, the waiter blocks on the per-key
// Mutex itself — that gives FIFO-ish behaviour from the Go scheduler
// (sufficient for our load; the issue body asks for a wait queue, not a
// strict-priority queue). Holder bookkeeping (current holder task id) is
// surfaced via Holder so callers can build a UI-friendly wait_reason.
type LocalPathLocker struct {
	mu    sync.Mutex
	locks map[string]*pathLockEntry

	// scopeMu and scopeLocks are deliberately separate from locks. Ordinary
	// local-directory users retain exact-path semantics; snapshot declarations
	// need the stricter rule that ancestor and descendant paths conflict.
	scopeMu      sync.Mutex
	scopeLocks   []*scopePathLockEntry
	scopeChanged chan struct{}
}

type pathLockEntry struct {
	mu       sync.Mutex // serialises holders for this key
	mu2      sync.Mutex // guards holderID under contention
	holderID string     // current owner, for UI hints; empty when free
}

type scopePathLockEntry struct {
	taskID string
	paths  []string
}

// NewLocalPathLocker returns an empty locker. Safe for concurrent use.
func NewLocalPathLocker() *LocalPathLocker {
	return &LocalPathLocker{
		locks:        make(map[string]*pathLockEntry),
		scopeChanged: make(chan struct{}),
	}
}

// Holder returns the task id currently holding the lock for realPath, or
// "" if no task holds it. Used to populate the wait_reason hint the daemon
// posts to the server when it parks a task — the UI then shows "waiting for
// <path> (held by task <short id>)".
func (l *LocalPathLocker) Holder(realPath string) string {
	l.mu.Lock()
	entry, ok := l.locks[realPath]
	l.mu.Unlock()
	if !ok {
		return ""
	}
	entry.mu2.Lock()
	defer entry.mu2.Unlock()
	return entry.holderID
}

// Acquire takes the lock for realPath on behalf of taskID. If the lock is
// already held, onWait is invoked (synchronously, before this goroutine
// blocks) with the current holder id so callers can flip the task into the
// server-side waiting_local_directory state. onWait may be nil for callers
// that don't need the side effect.
//
// Returns a release func that the caller must invoke (typically deferred)
// to free the lock. The release is idempotent.
//
// Acquire is cancellable via ctx. When ctx is cancelled while the goroutine
// is blocked on the lock, Acquire returns ctx.Err() and the lock is NOT
// taken. This is the same contract as sync.Mutex.Lock paired with
// context-aware cancellation — a daemon shutdown won't wedge inside the
// per-path wait queue.
func (l *LocalPathLocker) Acquire(ctx context.Context, realPath, taskID string, onWait func(holder string)) (func(), error) {
	if realPath == "" {
		return nil, errors.New("local_directory: realpath required for lock")
	}
	if taskID == "" {
		return nil, errors.New("local_directory: taskID required for lock")
	}

	l.mu.Lock()
	entry, ok := l.locks[realPath]
	if !ok {
		entry = &pathLockEntry{}
		l.locks[realPath] = entry
	}
	l.mu.Unlock()

	// Try the fast path first — no allocation, no waiter goroutine.
	if entry.mu.TryLock() {
		entry.mu2.Lock()
		entry.holderID = taskID
		entry.mu2.Unlock()
		return l.releaser(realPath, entry), nil
	}

	// Slow path: somebody else holds the lock. Fire onWait once with the
	// current holder so the daemon can stamp the server-side wait state,
	// then block until either we win the lock or ctx is cancelled.
	if onWait != nil {
		entry.mu2.Lock()
		holder := entry.holderID
		entry.mu2.Unlock()
		onWait(holder)
	}

	acquired := make(chan struct{})
	go func() {
		entry.mu.Lock()
		close(acquired)
	}()

	select {
	case <-acquired:
		entry.mu2.Lock()
		entry.holderID = taskID
		entry.mu2.Unlock()
		return l.releaser(realPath, entry), nil
	case <-ctx.Done():
		// We lost the wait — the goroutine above will still complete and
		// take the lock. Spin off a clean-up goroutine that releases it
		// the moment the acquire returns so a future caller isn't stuck
		// behind a phantom holder. The bookkeeping is best-effort: no
		// holder id is set, since this task never owned the lock.
		go func() {
			<-acquired
			entry.mu.Unlock()
		}()
		return nil, ctx.Err()
	}
}

// AcquireOverlapping takes one atomic snapshot-scope lease. Unlike Acquire,
// which intentionally only serializes identical paths, this method conflicts
// when any requested path is equal to, contains, or is contained by a path in
// an existing snapshot-scope lease. Acquiring the complete set atomically
// avoids a task holding one scope while waiting for another, so overlapping
// multi-scope declarations cannot form a lock-ordering deadlock.
//
// onWait runs at most once, synchronously and outside the locker mutex. It
// receives the requested path that conflicted and the current holder task ID.
// Waiting is cancellable without a waiter goroutine: release closes a
// generation channel, which wakes all waiters to re-check the protected state.
func (l *LocalPathLocker) AcquireOverlapping(ctx context.Context, realPaths []string, taskID string, onWait func(realPath, holder string)) (func(), error) {
	if taskID == "" {
		return nil, errors.New("local_directory: taskID required for lock")
	}
	paths := normalizedScopePaths(realPaths)
	if len(paths) == 0 {
		return nil, errors.New("local_directory: at least one realpath required for scope lock")
	}

	waited := false
	for {
		l.scopeMu.Lock()
		if l.scopeChanged == nil {
			l.scopeChanged = make(chan struct{})
		}
		conflictPath, holder := l.overlapHolderLocked(paths)
		if conflictPath == "" {
			entry := &scopePathLockEntry{taskID: taskID, paths: paths}
			l.scopeLocks = append(l.scopeLocks, entry)
			l.scopeMu.Unlock()
			return l.overlapReleaser(entry), nil
		}
		changed := l.scopeChanged
		l.scopeMu.Unlock()

		if !waited && onWait != nil {
			waited = true
			onWait(conflictPath, holder)
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func normalizedScopePaths(realPaths []string) []string {
	seen := make(map[string]struct{}, len(realPaths))
	paths := make([]string, 0, len(realPaths))
	for _, realPath := range realPaths {
		if realPath == "" {
			continue
		}
		path := filepath.Clean(realPath)
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

func (l *LocalPathLocker) overlapHolderLocked(paths []string) (string, string) {
	for _, held := range l.scopeLocks {
		for _, path := range paths {
			for _, heldPath := range held.paths {
				if scopePathsOverlap(path, heldPath) {
					return path, held.taskID
				}
			}
		}
	}
	return "", ""
}

func scopePathsOverlap(a, b string) bool {
	a = filepath.Clean(a)
	b = filepath.Clean(b)
	return scopePathContains(a, b) || scopePathContains(b, a)
}

func scopePathContains(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func (l *LocalPathLocker) overlapReleaser(entry *scopePathLockEntry) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			l.scopeMu.Lock()
			for i, held := range l.scopeLocks {
				if held != entry {
					continue
				}
				l.scopeLocks = append(l.scopeLocks[:i], l.scopeLocks[i+1:]...)
				close(l.scopeChanged)
				l.scopeChanged = make(chan struct{})
				break
			}
			l.scopeMu.Unlock()
		})
	}
}

// releaser returns the unlock callback. Idempotent via a once flag so a
// deferred release is safe even when the caller has already explicitly
// released after task completion.
func (l *LocalPathLocker) releaser(realPath string, entry *pathLockEntry) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			entry.mu2.Lock()
			entry.holderID = ""
			entry.mu2.Unlock()
			entry.mu.Unlock()
			// We deliberately keep the entry in the map even when nothing
			// is queued. The cost is one *pathLockEntry per distinct path
			// the daemon has ever served, which is bounded by the number
			// of local_directory project resources a workspace has — tiny
			// in practice. Pruning would race with a sibling caller that
			// just looked up the same entry and is about to TryLock.
			_ = realPath
		})
	}
}

// snapshotUnit is one materializable overlay unit of a snapshot task: a
// pristine directory that gets a per-task copy-on-write view in the worker's
// mount namespace. A whole-root unit covers the resource root (Subdir ""); a
// declared task materializes one unit per declared subdirectory — materializa-
// tion and mutex both at set granularity, per the ADR.
type snapshotUnit struct {
	ResourceID string
	Ref        localDirectoryRef
	AbsPath    string // unit target: resource root, or root joined with Subdir
	Subdir     string // "" for a whole-root unit
	Primary    bool
	// LockPath is the symlink-resolved unit target — the per-subdir lock key
	// when the task declared its scope. Empty on undeclared (lock-free) units.
	LockPath string
	// InternalKey is only for daemon-internal Git containment and metadata
	// overlays. It is outside the user's declared scope and its path hash keeps
	// the snapshot tree name separate from resource subdirectories.
	InternalKey string
	// TargetDevice and TargetInode carry the identity captured at scope-lock
	// admission into the worker spec. IdentityCaptured distinguishes an admitted
	// zero-value identity from an undeclared unit that still needs path lookup.
	TargetDevice     uint64
	TargetInode      uint64
	IdentityCaptured bool
}

type snapshotLockedUnit struct {
	Path   string
	Info   os.FileInfo
	Device uint64
	Inode  uint64
}

type snapshotLockBinding struct {
	Units map[string]snapshotLockedUnit
}

func snapshotUnitKey(unit snapshotUnit) string {
	return unit.ResourceID + "\x00" + unit.Subdir
}

func snapshotTargetIdentity(path string) (uint64, uint64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, 0, err
	}
	device, inode, ok := snapshotFileIdentity(info)
	if !ok {
		return 0, 0, nil
	}
	return device, inode, nil
}

// snapshotPlan is the resolved execution brief for a snapshot-mode task.
type snapshotPlan struct {
	Units               []snapshotUnit
	GitContainmentUnits []snapshotUnit
	GitMetadataUnits    []snapshotUnit
	Primary             *snapshotUnit // where the task's cwd lands
	Declared            bool          // task carried a local_directory_scope declaration
}

// materializedUnits returns the complete mount set shared by the worker and
// ledger. A declared Git subdirectory gets a private repository-root
// containment mount before its business and metadata mounts, so writes through
// sibling paths cannot reach the pristine worktree. Internal units do not
// participate in user-visible scope locks or change the primary cwd.
func (p *snapshotPlan) materializedUnits() []snapshotUnit {
	if p == nil {
		return nil
	}
	units := make([]snapshotUnit, 0, len(p.Units)+len(p.GitContainmentUnits)+len(p.GitMetadataUnits))
	units = append(units, p.GitContainmentUnits...)
	units = append(units, p.Units...)
	units = append(units, p.GitMetadataUnits...)
	return units
}

func snapshotUnitStem(issueID, taskID string, unit snapshotUnit) string {
	subdir := unit.Subdir
	if unit.InternalKey != "" {
		sum := sha256.Sum256([]byte(unit.InternalKey))
		subdir = "git-metadata-" + hex.EncodeToString(sum[:8])
	}
	return execenv.SnapshotStem(issueID, subdir, taskID)
}

// DeclaredLockPaths returns the real paths every declared subdirectory locks
// for the task's lifetime — the exclusive-lease keys acceptance 4 defines.
// Undeclared plans return nil: their isolation is the private upperdir, and
// taking no lock is what lets two undeclared tasks on the same directory run
// concurrently (acceptance 1).
func (p *snapshotPlan) DeclaredLockPaths() []string {
	if p == nil || !p.Declared {
		return nil
	}
	var paths []string
	for _, u := range p.Units {
		paths = append(paths, u.LockPath)
	}
	return paths
}

// resolveSnapshotPlan resolves a snapshot-mode task into its units. A
// worktree resource becomes a candidate only while the deployment-local Git
// snapshot gate is enabled; its stored mode remains worktree and any failed
// eligibility check still routes it through the existing worktree path.
// Returns nil (without error) when none of the task's project resources is a
// snapshot-mode local_directory, or a gate-enabled worktree candidate, of this
// daemon — the task takes the regular in_place/worktree paths.
//
// Resolution rules (ADR §3, "拒绝猜测"):
//   - Declared scope: every entry must name a snapshot resource of this
//     daemon; drift against the server's save-time validation fails the task
//     rather than widening the write set. One unit per declared subdirectory.
//   - Undeclared: exactly one whole-root unit on the primary resource; with
//     no primary, the sole snapshot resource stands in; with several and no
//     primary the task fails — "which directory does this run in" is never
//     guessed.
func resolveSnapshotPlan(task Task, daemonID string) (*snapshotPlan, error) {
	if task.IsLeaderTask {
		return nil, nil
	}
	type snapRow struct {
		id  string
		ref localDirectoryRef
		abs string
	}
	gitSnapshotEnabled := os.Getenv("MULTICA_ENABLE_GIT_SNAPSHOT") == "1"
	var rows []snapRow
	for _, r := range task.ProjectResources {
		if r.ResourceType != localDirectoryResourceType {
			continue
		}
		var ref localDirectoryRef
		if err := json.Unmarshal(r.ResourceRef, &ref); err != nil {
			return nil, fmt.Errorf("local_directory: parse resource_ref: %w", err)
		}
		mode := strings.TrimSpace(ref.ExecutionMode)
		if mode != localDirectoryModeSnapshot && !(gitSnapshotEnabled && mode == localDirectoryModeWorktree) {
			continue
		}
		if strings.TrimSpace(ref.DaemonID) != daemonID {
			continue
		}
		abs, err := normalizeLocalPath(ref.LocalPath)
		if err != nil {
			return nil, err
		}
		// The deployment gate changes only ordinary Git worktree execution.
		// Keep non-Git worktree resources on their established backend even
		// while the gate is enabled.
		if mode == localDirectoryModeWorktree {
			top, isGit := gitToplevel(abs)
			if !isGit || filepath.Clean(top) != filepath.Clean(abs) {
				continue
			}
		}
		rows = append(rows, snapRow{id: r.ID, ref: ref, abs: abs})
	}
	if len(rows) == 0 {
		return nil, nil
	}

	scopes, err := execenv.ParseSnapshotScope(task.LocalDirectoryScope)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]*snapRow, len(rows))
	for i := range rows {
		byID[rows[i].id] = &rows[i]
	}

	plan := &snapshotPlan{Declared: len(scopes) > 0}
	if plan.Declared {
		for i, sc := range scopes {
			row, ok := byID[sc.ResourceID]
			if !ok {
				return nil, fmt.Errorf(
					"local_directory scope: entry %d names resource %s, which is not a snapshot directory of this machine; refusing to widen the declaration",
					i+1, sc.ResourceID)
			}
			for _, sub := range sc.Subdirectories {
				if !subdirectoryInEffectiveSet(sub, row.ref.Subdirectories) {
					return nil, fmt.Errorf(
						"local_directory scope: %q is outside the effective set declared on resource %q; refusing to widen it",
						sub, row.ref.LocalPath)
				}
				abs := filepath.Join(row.abs, filepath.FromSlash(sub))
				if err := validateLocalPath(abs); err != nil {
					return nil, err
				}
				real, err := resolveRealPath(abs)
				if err != nil {
					return nil, err
				}
				plan.Units = append(plan.Units, snapshotUnit{
					ResourceID: row.id,
					Ref:        row.ref,
					AbsPath:    real,
					Subdir:     sub,
					Primary:    row.ref.Primary,
					LockPath:   real,
				})
			}
		}
		if len(plan.Units) == 0 {
			return nil, errors.New("local_directory scope: declaration is present but names no subdirectory")
		}
		plan.Primary = plan.primaryUnit()
		return plan, nil
	}

	// Undeclared: primary picks the directory. Server-side
	// findSnapshotPrimaryConflict keeps at most one primary per project, so
	// more than one here is bypassed or corrupted data — refuse to guess.
	var primary *snapRow
	for i := range rows {
		if rows[i].ref.Primary {
			if primary != nil {
				return nil, fmt.Errorf(
					"local_directory: project has multiple primary snapshot directories for this daemon (%q and %q); fix the project settings",
					primary.ref.LocalPath, rows[i].ref.LocalPath)
			}
			primary = &rows[i]
		}
	}
	if primary == nil {
		if len(rows) == 1 {
			primary = &rows[0]
		} else {
			return nil, fmt.Errorf(
				"local_directory: task declares no directory scope and the project has %d snapshot directories with no primary; declare the scope or mark one primary",
				len(rows))
		}
	}
	// Claim-time fail-closed on the target, same bar the in_place mutex path
	// applies: a missing or blacklisted directory must fail the task with a
	// readable reason now, not with a mount error after the worker spawns.
	// The read-write check is stricter than the mechanism strictly needs (a
	// read-only checkout could be snapshot-mounted); keeping it preserves
	// in_place parity in phase 1.
	if err := validateLocalPath(primary.abs); err != nil {
		return nil, err
	}
	real, err := resolveRealPath(primary.abs)
	if err != nil {
		return nil, err
	}
	plan.Units = []snapshotUnit{{
		ResourceID: primary.id,
		Ref:        primary.ref,
		AbsPath:    real,
		Primary:    true,
		LockPath:   real,
	}}
	plan.Primary = &plan.Units[0]
	return plan, nil
}

// primaryUnit picks where the task's cwd lands: the first unit of a primary
// resource when one is marked, else the first unit. Declared order is the
// request's own order, so the choice is deterministic for a given task.
func (p *snapshotPlan) primaryUnit() *snapshotUnit {
	for i := range p.Units {
		if p.Units[i].Primary {
			return &p.Units[i]
		}
	}
	return &p.Units[0]
}

// subdirectoryInEffectiveSet reports whether sub sits inside a resource's
// effective set — equal to a member or nested under one. An absent set means
// the whole directory is in scope. Mirrors the server-side check of the same
// name; the daemon re-runs it because it is the last line of defense before
// a mount is built.
func subdirectoryInEffectiveSet(sub string, set []string) bool {
	if len(set) == 0 {
		return true
	}
	for _, member := range set {
		if sub == member || strings.HasPrefix(sub, member+"/") {
			return true
		}
	}
	return false
}

// gitToplevel reports the git working-tree root containing dir, or ok=false
// when dir is not inside one. Used to route snapshot tasks on git
// directories to the existing worktree flow, which the ADR keeps as the fast
// path for anything with a .git: branch delivery, resume, and half-finished
// preservation stay exactly as they are.
func gitToplevel(dir string) (string, bool) {
	cmd := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel")
	cmd.Env = execenv.SnapshotGitEnvironment()
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	top := strings.TrimSpace(string(out))
	if top == "" {
		return "", false
	}
	return top, true
}

// snapshotWorktreeFallback decides whether a snapshot task must run through
// the existing worktree flow instead of mounting: the primary unit's
// resource root is inside a git working tree. Per-subdirectory git routing
// inside a non-git root is phase 2; a git ROOT always wins for the whole
// task. Returns a worktree-mode assignment for the resource root — the
// worktree flow branches on that mode — or nil to proceed with mounts.
func snapshotWorktreeFallback(plan *snapshotPlan) (*localDirectoryAssignment, error) {
	return snapshotWorktreeFallbackWithGitGate(plan, os.Getenv("MULTICA_ENABLE_GIT_SNAPSHOT") == "1")
}

func snapshotWorktreeFallbackWithGitGate(plan *snapshotPlan, gitSnapshotEnabled bool) (*localDirectoryAssignment, error) {
	return snapshotWorktreeFallbackWithGitGateAndCapability(plan, gitSnapshotEnabled, true)
}

// snapshotWorktreeFallbackWithGitGateAndCapability keeps worktree-mode
// resources on their established backend when this daemon did not advertise
// snapshot support. Snapshot resources are capability-gated at claim time;
// worktree resources are not, so the local check is necessary before the
// deployment gate may adopt one for Git snapshot delivery.
func snapshotWorktreeFallbackWithGitGateAndCapability(plan *snapshotPlan, gitSnapshotEnabled, snapshotCapable bool) (*localDirectoryAssignment, error) {
	if plan == nil || plan.Primary == nil {
		return nil, nil
	}
	// Audit Git metadata before selecting snapshot delivery. An unsafe graph
	// also cannot use the legacy worktree backend: it writes refs, worktree
	// admin state, and objects that could follow an escaped metadata redirect
	// into the host. Fail closed before startup; safe topologies outside the
	// snapshot artifact contract retain the established worktree backend.
	// Git snapshot delivery is deliberately narrower than the audit and is
	// off by default. Operators opt in per daemon process only after the
	// snapshot artifact/recovery path has been deployed.
	audit := auditSnapshotGitScope(plan)
	plan.GitContainmentUnits = nil
	plan.GitMetadataUnits = nil
	if !audit.Safe {
		return nil, fmt.Errorf("local_directory: unsafe Git metadata prevents snapshot delivery and worktree fallback: %s", audit.Reason)
	}
	worktreeCandidate := strings.TrimSpace(plan.Primary.Ref.ExecutionMode) == localDirectoryModeWorktree
	if audit.Safe && !audit.HasGit {
		return nil, nil
	}
	if gitSnapshotEnabled && audit.HasGit {
		if err := auditSnapshotGitConfiguration(audit); err != nil {
			return nil, fmt.Errorf("local_directory: unsafe Git configuration prevents snapshot delivery and worktree fallback: %w", err)
		}
	}
	if audit.Safe && (!worktreeCandidate || snapshotCapable) && gitSnapshotDeliveryEligibleFromAudit(plan, gitSnapshotEnabled, audit) {
		if containment, ok := gitSnapshotContainmentUnits(plan, audit); ok {
			if units, ok := gitSnapshotMetadataUnits(plan, audit); ok {
				plan.GitContainmentUnits = containment
				plan.GitMetadataUnits = units
				return nil, nil
			}
		}
	}
	root := plan.Primary.Ref.LocalPath
	abs, err := normalizeLocalPath(root)
	if err != nil {
		return nil, err
	}
	real, err := resolveRealPath(abs)
	if err != nil {
		return nil, err
	}
	ref := plan.Primary.Ref
	ref.ExecutionMode = localDirectoryModeWorktree
	fallback := &localDirectoryAssignment{Ref: ref, AbsPath: abs, RealPath: real}
	if err := validateWorktreeGitMetadata(fallback); err != nil {
		return nil, err
	}
	return fallback, nil
}

// gitSnapshotContainmentUnits adds a root CoW view only for the already
// eligible ordinary-repository declared-subdirectory shape. This keeps the
// declared path as the worker cwd and lock scope, while preventing relative
// traversal into an unmounted sibling from writing into the host worktree.
func gitSnapshotContainmentUnits(plan *snapshotPlan, audit gitSnapshotAudit) ([]snapshotUnit, bool) {
	repository, ok := gitSnapshotDeliveryRepository(plan, true, audit)
	if !ok {
		return nil, false
	}
	if plan.Primary == nil || !plan.Declared || plan.Primary.Subdir == "" {
		return nil, true
	}
	if filepath.Clean(repository.Worktree) == filepath.Clean(plan.Primary.AbsPath) {
		return nil, false
	}
	return []snapshotUnit{{
		AbsPath:     repository.Worktree,
		InternalKey: "git-worktree-containment:" + repository.Worktree,
	}}, true
}

func gitSnapshotMetadataUnits(plan *snapshotPlan, audit gitSnapshotAudit) ([]snapshotUnit, bool) {
	covered := make([]string, 0, len(plan.Units))
	for _, unit := range plan.Units {
		real, err := canonicalGitDirectory(unit.AbsPath)
		if err != nil {
			return nil, false
		}
		covered = append(covered, real)
	}
	var units []snapshotUnit
	for _, dependency := range audit.Dependencies {
		if withinAnyScope(dependency, covered) {
			continue
		}
		for _, scope := range covered {
			if withinAnyScope(scope, []string{dependency}) {
				return nil, false
			}
		}
		units = append(units, snapshotUnit{AbsPath: dependency, InternalKey: dependency})
		covered = append(covered, dependency)
	}
	return units, true
}

// revalidateSnapshotGitPlan closes the interval between claim-time planning
// and worker startup. Metadata units are mount inputs, so a topology change
// cannot be applied in place after their overlay paths have been planned.
func revalidateSnapshotGitPlan(plan *snapshotPlan, enabled bool) (gitSnapshotAudit, error) {
	audit := auditSnapshotGitScope(plan)
	if !audit.Safe {
		return audit, fmt.Errorf("local_directory: unsafe Git metadata prevents snapshot startup: %s", audit.Reason)
	}
	if enabled && audit.HasGit {
		if err := auditSnapshotGitConfiguration(audit); err != nil {
			return audit, fmt.Errorf("local_directory: unsafe Git configuration prevents snapshot startup: %w", err)
		}
	}
	if !audit.HasGit {
		if len(plan.GitContainmentUnits) != 0 || len(plan.GitMetadataUnits) != 0 {
			return audit, errors.New("local_directory: Git metadata topology changed after snapshot planning")
		}
		return audit, nil
	}
	if _, eligible := gitSnapshotDeliveryRepository(plan, enabled, audit); !eligible {
		return audit, errors.New("local_directory: Git metadata topology no longer supports snapshot delivery")
	}
	containment, containmentOK := gitSnapshotContainmentUnits(plan, audit)
	metadata, metadataOK := gitSnapshotMetadataUnits(plan, audit)
	if !containmentOK || !metadataOK || !sameSnapshotUnits(plan.GitContainmentUnits, containment) || !sameSnapshotUnits(plan.GitMetadataUnits, metadata) {
		return audit, errors.New("local_directory: Git metadata topology changed after snapshot planning")
	}
	return audit, nil
}

func sameSnapshotUnits(left, right []snapshotUnit) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if filepath.Clean(left[i].AbsPath) != filepath.Clean(right[i].AbsPath) || left[i].InternalKey != right[i].InternalKey {
			return false
		}
	}
	return true
}

// snapshotExecutionObservation is the non-sensitive decision record emitted
// for a snapshot resource at task start. Operators need the actual backend,
// not merely the resource's requested mode: a Git resource can deliberately
// route to the established worktree flow without being an error.
type snapshotExecutionObservation struct {
	ExecutionMode string
	GateEnabled   bool
	Capability    bool
	DowngradeCode string
}

// snapshotExecutionObservationFor describes the decision already made by
// snapshotWorktreeFallback. DowngradeCode is intentionally a stable category,
// rather than the audit's path-bearing diagnostic, because this record is
// emitted to the daemon log and may be shown in operator tooling.
func snapshotExecutionObservationFor(plan *snapshotPlan, fallback *localDirectoryAssignment, gateEnabled, capability bool) snapshotExecutionObservation {
	observation := snapshotExecutionObservation{
		ExecutionMode: localDirectoryModeSnapshot,
		GateEnabled:   gateEnabled,
		Capability:    capability,
	}
	if fallback == nil {
		return observation
	}

	observation.ExecutionMode = localDirectoryModeWorktree
	audit := auditSnapshotGitScope(plan)
	switch {
	case !audit.Safe:
		observation.DowngradeCode = "git_metadata_audit_failed"
	case !gateEnabled:
		observation.DowngradeCode = "git_snapshot_gate_disabled"
	case !capability && strings.TrimSpace(plan.Primary.Ref.ExecutionMode) == localDirectoryModeWorktree:
		observation.DowngradeCode = "git_snapshot_capability_unavailable"
	case !gitSnapshotDeliveryEligible(plan, true):
		observation.DowngradeCode = "git_snapshot_unsupported_topology"
	default:
		// Keep an explicit code if future eligibility checks add a shape that
		// the current categorisation does not recognise.
		observation.DowngradeCode = "git_snapshot_ineligible"
	}
	return observation
}

// localDirectoryGCExemption reports whether a finished task's env root must
// outlive the ordinary GC sweep: an in_place assignment runs inside the
// user's own tree, and a snapshot run hosts the local-snapshots retention
// surface. A snapshot task whose root turns out to be a git repo routes to
// the worktree flow instead — its env root is disposable exactly like any
// worktree task's, so it must NOT be stamped, or every such task leaves one
// exempt env root behind forever. Resolution errors stamp nothing: the
// disposable default is the safe side of the guess.
func localDirectoryGCExemption(task Task, daemonID string, snapshotCapable bool) bool {
	if plan, err := resolveSnapshotPlan(task, daemonID); err == nil && plan != nil {
		fb, fbErr := snapshotWorktreeFallbackWithGitGateAndCapability(plan, os.Getenv("MULTICA_ENABLE_GIT_SNAPSHOT") == "1", snapshotCapable)
		return fbErr == nil && fb == nil
	}
	if assignment, err := localDirectoryAssignmentForTask(task, daemonID); err == nil && assignment != nil {
		return !assignment.UsesWorktree()
	}
	return false
}

// materializeSnapshotTrees creates every unit's alias/upper/work directories
// under the env root and returns the primary unit's upperdir. Idempotent, and
// deliberately called from both runTask sites that touch the tree: Prepare
// needs the primary upper to exist before it writes context files into it,
// and the run-brief block re-runs this defensively for an env-reuse path that
// skipped the prepare branch.
func (p *snapshotPlan) materializeTrees(envRoot, issueID, taskID string) (string, error) {
	if p == nil {
		return "", nil
	}
	for _, u := range p.materializedUnits() {
		alias, upper, work := execenv.PlanSnapshotTree(envRoot, snapshotUnitStem(issueID, taskID, u))
		for _, dir := range []string{alias, upper, work} {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return "", fmt.Errorf("snapshot: create tree dir %s: %w", dir, err)
			}
		}
	}
	_, primaryUpper, _ := execenv.PlanSnapshotTree(envRoot, snapshotUnitStem(issueID, taskID, *p.Primary))
	return primaryUpper, nil
}

// displayName is the human-facing wait-reason name for one snapshot unit,
// built by the same contract as localDirectoryAssignment.DisplayName: the
// resource label wins when set, otherwise the root's basename — never an
// absolute path, because the reason is stored server-side and rendered in
// chat transcripts.
func (u *snapshotUnit) displayName() string {
	name := strings.TrimSpace(u.Ref.Label)
	if name == "" {
		name = filepath.Base(u.AbsPath)
	}
	if u.Subdir != "" {
		name = name + "/" + filepath.ToSlash(u.Subdir)
	}
	return name
}
