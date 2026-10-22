package execenv

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/pkg/agent"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Snapshot workspace support (RUYI-669): the non-git half of local_directory
// isolation. Where worktree mode materializes a git worktree per task, snapshot
// mode mounts a per-task overlayfs view over the user's pristine directory
// inside a private user+mount namespace: same path, copy-on-write, and the
// whole mount evaporates when the worker process (the namespace's last
// resident) exits.
//
// Layout under the daemon env root mirrors localWorktreeDirName:
//
//	<envRoot>/local-snapshots/ledger.json            ownership records
//	<envRoot>/local-snapshots/<stem>/upper           per-task upper layer
//	<envRoot>/local-snapshots/<stem>/work            overlay workdir
//	<envRoot>/local-snapshots/<stem>/pristine        bind-mount alias of the pristine dir
//
// The stem carries the issue and (when the resource declares subdirectories)
// the subdirectory segment, per the ADR naming rule. Recycle is driven by the
// ledger — never by name matching — so a user-created directory that happens to
// share the prefix is unreachable by GC by construction.
const (
	// SnapshotWorkerSubcommand and SnapshotProbeSubcommand live on pkg/agent
	// (the wrapping spawner owns the argv contract); execenv hosts the
	// entrypoint implementations.
	SnapshotWorkerSubcommand = agent.SnapshotWorkerSubcommand
	SnapshotProbeSubcommand  = agent.SnapshotProbeSubcommand
	// SnapshotWorkerSep separates the worker's own arguments from the agent
	// argv it must exec.
	SnapshotWorkerSep = agent.SnapshotWorkerSep

	localSnapshotDirName = "local-snapshots"
	snapshotLedgerName   = "ledger.json"
)

// SnapshotUnitSpec is one overlay materialization: the pristine directory the
// view covers, and the three envRoot directories backing it. For a
// whole-root resource there is exactly one unit with Subdir ""; a declared
// subdirectory produces one unit per declared path.
//
// All fields are plain paths shared between daemon and worker; the spec travels
// in argv and contains no credentials.
type SnapshotUnitSpec struct {
	// Target is the pristine real path this unit's view covers. Relative to
	// the mount table of the worker's namespace, this exact path is covered
	// by the overlay.
	Target string `json:"target"`
	// Subdir is the resource-declared subdirectory this unit materializes,
	// "" for the whole root. It is metadata here (naming, diagnostics); the
	// mount itself only needs Target.
	Subdir string `json:"subdir,omitempty"`
	// Alias receives the pristine bind mount, taken BEFORE the overlay covers
	// Target: inside the namespace the pristine content stays reachable
	// through Alias for the lifetime of the view (decision 1: pristine via
	// bind alias).
	Alias string `json:"alias"`
	// Upper accumulates the task's writes (copy-up targets and new files).
	Upper string `json:"upper"`
	// Work is the overlayfs working directory paired with Upper.
	Work string `json:"work"`
}

// SnapshotRunSpec is the worker's full brief: every unit to mount, and where
// the exec'd agent process should land (the primary unit's target).
type SnapshotRunSpec struct {
	TaskID string             `json:"task_id"`
	Units  []SnapshotUnitSpec `json:"units"`
	// Cwd is the directory the agent process starts in: the primary unit's
	// target. The worker chdirs here AFTER mounting, because the exec inherits
	// a cwd handle — one opened before the overlay covers the path would keep
	// pointing at the pristine directory underneath the view.
	Cwd string `json:"cwd,omitempty"`
	// HostUID/HostGID are the daemon's real uid/gid, mapped to root inside
	// the worker's user namespace. They ride in the spec because the
	// supervised launcher keeps only the stored argv: agent.snapshotWrapFromArgv
	// rebuilds the clone flags from this JSON and fails closed when either
	// key is missing, refusing to spawn rather than run the agent unwrapped.
	// BuildSnapshotWrap fills them; constructors leave them zero.
	HostUID int `json:"host_uid"`
	HostGID int `json:"host_gid"`
	// GitArtifact is present only for the narrow, opt-in ordinary-repository
	// delivery mode. The worker writes the bundle while its overlay mount is
	// still alive; the daemon must never reconstruct it from an upperdir.
	GitArtifact *SnapshotGitArtifactSpec `json:"git_artifact,omitempty"`
}

// SnapshotGitArtifactSpec describes one independently importable Git bundle.
// ArtifactDir is outside the disposable overlay trees so success can reclaim
// its upperdir while failures and cancellations retain their recovery bundle.
type SnapshotGitArtifactSpec struct {
	ArtifactDir string `json:"artifact_dir"`
	Baseline    string `json:"baseline"`
	EnvRoot     string `json:"env_root"`
	Provider    string `json:"provider"`
}

// SnapshotLedgerEntry is one ownership record: which task materialized which
// snapshot tree, since when, and whether the run ended in a state worth
// keeping (failure preserves the upper layer for inspection and — in phase 2 —
// delivery, mirroring the worktree half-finished-work semantics).
type SnapshotLedgerEntry struct {
	TaskID    string             `json:"task_id"`
	IssueID   string             `json:"issue_id,omitempty"`
	Root      string             `json:"root"`
	Primary   string             `json:"primary"`
	Units     []SnapshotUnitSpec `json:"units"`
	CreatedAt time.Time          `json:"created_at"`
	// State is "active" while the task runs, "failed" once a run that must be
	// preserved exits, and "reaped" marks tombstones GC writes before deleting
	// the tree. Success removes the entry entirely.
	State       string                   `json:"state"`
	GitArtifact *SnapshotGitArtifactSpec `json:"git_artifact,omitempty"`
}

const (
	snapshotStateActive = "active"
	snapshotStateFailed = "failed"
	// snapshotStateDelivered keeps a successful Git bundle reviewable after
	// its overlay tree is gone. It shares the failed-run retention window.
	snapshotStateDelivered = "delivered"
	// snapshotFailedRetention bounds how long a failed run's upper layer is
	// kept for inspection before GC reaps it. Mirrors the spirit of the
	// worktree stale-worktree sweep: long enough for a human to look, short
	// enough that a dead daemon's leftovers do not accumulate forever.
	snapshotFailedRetention = 7 * 24 * time.Hour
	// snapshotOrphanAge bounds crash leftovers: directories under the
	// snapshot root that no ledger entry claims. Entries are written before
	// the directories exist, so an orphan only appears if the daemon died
	// between tree creation and its first ledger write — reaped purely by age.
	snapshotOrphanAge = 24 * time.Hour
)

// snapshotLedger is the on-disk record. One file, rewritten atomically: the
// ledger is tiny (a handful of concurrent local tasks) and read-modify-write
// simplicity beats incremental bookkeeping at this size.
type snapshotLedger struct {
	Entries []SnapshotLedgerEntry `json:"entries"`
}

// SnapshotRoot returns the snapshot tree root under a daemon env root.
func SnapshotRoot(envRoot string) string {
	return filepath.Join(envRoot, localSnapshotDirName)
}

// SnapshotLedgerPath returns the ledger file under a daemon env root.
func SnapshotLedgerPath(envRoot string) string {
	return filepath.Join(SnapshotRoot(envRoot), snapshotLedgerName)
}

// SnapshotStem builds the per-task tree name: issue-prefixed, subdirectory
// segment when the unit materializes a declared subdir, task-keyed so two
// tasks on the same issue never collide (mirrors the worktree branch fork
// suffix rule at directory granularity).
func SnapshotStem(issueID, subdir, taskID string) string {
	parts := []string{"i" + taskKey(issueID)}
	if s := sanitizeSegment(filepath.ToSlash(subdir)); s != "" {
		parts = append(parts, s)
	}
	parts = append(parts, taskKey(taskID))
	return strings.Join(parts, "-")
}

// PlanSnapshotTree lays out the envRoot directories for one unit.
func PlanSnapshotTree(envRoot, stem string) (alias, upper, work string) {
	base := filepath.Join(SnapshotRoot(envRoot), stem)
	return filepath.Join(base, "pristine"), filepath.Join(base, "upper"), filepath.Join(base, "work")
}

// SnapshotGitArtifactDir is task-keyed and daemon-owned. It is intentionally
// separate from a snapshot stem: one ordinary repository has exactly one
// bundle, and the artifact must survive successful tree reclamation.
func SnapshotGitArtifactDir(envRoot, taskID string) string {
	return filepath.Join(SnapshotRoot(envRoot), "git-artifacts", taskKey(taskID))
}

// recordSnapshotLedger rewrites the ledger file atomically after applying fn.
// Writes are serialized by construction, not by a lock: every task runs in its
// own env root, so one ledger file belongs to one task's lifecycle, and the GC
// sweep only touches ledgers of env roots no live task holds (the daemon's
// isActiveEnvRoot guard runs before this sweep). The temp-file rename keeps
// any reader from seeing a partial file.
func recordSnapshotLedger(envRoot string, fn func(*snapshotLedger)) error {
	root := SnapshotRoot(envRoot)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return fmt.Errorf("execenv: snapshot ledger root: %w", err)
	}
	path := filepath.Join(root, snapshotLedgerName)
	var led snapshotLedger
	if raw, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(raw, &led); err != nil {
			return fmt.Errorf("execenv: snapshot ledger %s: %w", path, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("execenv: snapshot ledger read: %w", err)
	}
	fn(&led)
	raw, err := json.Marshal(&led)
	if err != nil {
		return fmt.Errorf("execenv: snapshot ledger encode: %w", err)
	}
	tmp, err := os.CreateTemp(root, ".ledger-*")
	if err != nil {
		return fmt.Errorf("execenv: snapshot ledger temp: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return fmt.Errorf("execenv: snapshot ledger write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("execenv: snapshot ledger write: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("execenv: snapshot ledger commit: %w", err)
	}
	return nil
}

// RecordSnapshotStart inserts the ownership record BEFORE the worker spawns:
// from this moment any tree on disk is attributable, which is what makes GC
// ledger-driven instead of name-driven.
func RecordSnapshotStart(envRoot string, entry SnapshotLedgerEntry) error {
	entry.State = snapshotStateActive
	entry.CreatedAt = time.Now().UTC()
	return recordSnapshotLedger(envRoot, func(led *snapshotLedger) {
		led.Entries = append(led.Entries, entry)
	})
}

// FinalizeSnapshot closes one run's record. A conventional successful snapshot
// removes its entry and tree. A successful Git delivery retains only its
// ledger-owned bundle as "delivered" for the retention window; its overlay
// tree is still deleted. keep=true is every other terminal shape: a failure
// and a mid-run cancellation flip an active entry to "failed" and leave its
// tree/artifact for inspection. A cancellation before recording (lock-wait
// abort) finds no entry and is a no-op.
func FinalizeSnapshot(envRoot, taskID string, keep bool) error {
	var removed []SnapshotLedgerEntry
	if err := recordSnapshotLedger(envRoot, func(led *snapshotLedger) {
		kept := led.Entries[:0]
		for _, e := range led.Entries {
			if e.TaskID == taskID && !keep {
				if e.GitArtifact != nil {
					e.State = snapshotStateDelivered
					kept = append(kept, e)
					removed = append(removed, e)
					continue
				}
				removed = append(removed, e)
				continue
			}
			if e.TaskID == taskID && e.State == snapshotStateActive {
				e.State = snapshotStateFailed
			}
			kept = append(kept, e)
		}
		led.Entries = kept
	}); err != nil {
		return err
	}
	// Delete trees only after the ledger commit succeeded: a crash in this
	// window leaves an unclaimed stem the orphan sweep reclaims by age,
	// whereas the reverse order would leave a tree with no ledger entry
	// pointing at it for the full retention window.
	for _, e := range removed {
		RemoveSnapshotTree(e)
	}
	return nil
}

// snapshotTreeRoots returns the stem directory of every unit — one per
// declared subdirectory, since SnapshotStem folds the subdir segment into the
// stem name. Deduplicated and empty/slash-free; zero units returns nil (a
// unit-less entry owns no tree, and falling back to entry.Root would point
// the remover at the env root itself).
func snapshotTreeRoots(entry SnapshotLedgerEntry) []string {
	roots := make([]string, 0, len(entry.Units))
	seen := make(map[string]bool, len(entry.Units))
	for i := range entry.Units {
		root := filepath.Dir(entry.Units[i].Alias)
		if root == "" || root == string(filepath.Separator) || seen[root] {
			continue
		}
		seen[root] = true
		roots = append(roots, root)
	}
	return roots
}

// RemoveSnapshotTree deletes one task's envRoot directories — the stem
// directory of every unit, best-effort per tree: a partially-created tree
// removes as far as it exists.
func RemoveSnapshotTree(entry SnapshotLedgerEntry) {
	for _, root := range snapshotTreeRoots(entry) {
		_ = os.RemoveAll(root)
	}
}

func removeSnapshotGitArtifact(entry SnapshotLedgerEntry) {
	if entry.GitArtifact != nil && entry.GitArtifact.ArtifactDir != "" {
		_ = os.RemoveAll(entry.GitArtifact.ArtifactDir)
	}
}

// ReapExpiredSnapshots is the GC sweep. Ledger-driven end to end:
//
//   - "failed" entries older than the retention window release their tree and
//     the record (the preserve window is over).
//   - Directories under the snapshot root no ledger entry claims are crash
//     leftovers from a daemon that died mid-prepare; they carry only ever
//     daemon-written material, and the age bound keeps a concurrent prepare
//     (entry not yet committed) out of the sweep.
//   - A ledger that exists but cannot be read or parsed (disk error, corrupt
//     file, future schema) disables the sweep for this env root: with no
//     trustworthy claim set, reclaiming by age alone would delete trees the
//     ledger still holds, retention windows included. Fail closed and let
//     the next cycle retry; the corruption surfaces through the same
//     recordSnapshotLedger path the next task on this env root hits.
//
// It never walks the user's directories: pristine content is only ever read.
func ReapExpiredSnapshots(envRoot string, now time.Time) []string {
	// Env roots that never hosted a snapshot are the overwhelmingly common
	// case, and recordSnapshotLedger would MkdirAll the root on its way
	// through — an empty directory on every task dir every GC cycle. Exit
	// before touching anything.
	root := SnapshotRoot(envRoot)
	if _, err := os.Stat(root); err != nil {
		return nil
	}
	var reaped []string
	claimed := map[string]bool{}
	ledgerRead := false
	_ = recordSnapshotLedger(envRoot, func(led *snapshotLedger) {
		ledgerRead = true
		kept := led.Entries[:0]
		for _, e := range led.Entries {
			switch e.State {
			case snapshotStateFailed, snapshotStateDelivered:
				if now.Sub(e.CreatedAt) >= snapshotFailedRetention {
					for _, treeRoot := range snapshotTreeRoots(e) {
						_ = os.RemoveAll(treeRoot)
						reaped = append(reaped, treeRoot)
					}
					removeSnapshotGitArtifact(e)
					continue
				}
				// Still inside the retention window: keep the record AND
				// claim its trees, otherwise the orphan sweep below would
				// reap them at the much shorter orphan age — silently
				// cutting the 7-day preservation promise down to 24h.
				for _, treeRoot := range snapshotTreeRoots(e) {
					claimed[treeRoot] = true
				}
				if e.GitArtifact != nil && e.GitArtifact.ArtifactDir != "" {
					claimed[e.GitArtifact.ArtifactDir] = true
				}
			case snapshotStateActive:
				if now.Sub(e.CreatedAt) >= snapshotOrphanAge {
					// A task cannot stay active this long under a live
					// daemon: the GC only scans env roots no live task
					// holds, so an entry this old is a dead daemon's
					// leftover — reclaim its trees and drop the record.
					for _, treeRoot := range snapshotTreeRoots(e) {
						_ = os.RemoveAll(treeRoot)
						reaped = append(reaped, treeRoot)
					}
					continue
				}
				for _, treeRoot := range snapshotTreeRoots(e) {
					claimed[treeRoot] = true
				}
			}
			kept = append(kept, e)
		}
		led.Entries = kept
	})
	if !ledgerRead {
		// fn never ran: the ledger could not be read or parsed, so claimed
		// is empty and reclaiming by age alone would delete trees the
		// on-disk ledger still holds — retention windows included. Skip the
		// orphan sweep; the worst case is delaying leftover reclamation by
		// one GC cycle. A ledger-write failure does not land here: fn had
		// already run by then, so claimed reflects the on-disk ledger.
		return reaped
	}

	stems, err := os.ReadDir(root)
	if err != nil {
		return reaped
	}
	for _, st := range stems {
		if !st.IsDir() || st.Name() == snapshotLedgerName {
			continue
		}
		path := filepath.Join(root, st.Name())
		if claimed[path] {
			continue
		}
		info, err := st.Info()
		if err != nil || now.Sub(info.ModTime()) < snapshotOrphanAge {
			continue
		}
		if os.RemoveAll(path) == nil {
			reaped = append(reaped, path)
		}
	}
	return reaped
}

// BuildSnapshotWrap encodes a run spec into the launch brief pkg/agent
// applies to the backend's agent command: the daemon binary as the stage-2
// wrapper host, the spec JSON in argv, and the daemon's own host IDs for the
// namespace uid/gid maps. The host IDs are stamped into the spec before
// encoding — not just onto the returned wrap — because a supervised launcher
// rebuilds the spawn from the stored argv alone, and agent.snapshotWrapFromArgv
// refuses any spec that does not carry them.
func BuildSnapshotWrap(selfPath string, spec *SnapshotRunSpec) (*agent.SnapshotWrap, error) {
	completed := *spec
	completed.HostUID = os.Getuid()
	completed.HostGID = os.Getgid()
	raw, err := json.Marshal(&completed)
	if err != nil {
		return nil, fmt.Errorf("execenv: snapshot spec encode: %w", err)
	}
	return &agent.SnapshotWrap{
		WrapperPath: selfPath,
		SpecJSON:    string(raw),
		HostUID:     os.Getuid(),
		HostGID:     os.Getgid(),
	}, nil
}

// ParseSnapshotScope validates and decodes the task-level
// local_directory_scope declaration stored on agent_task_queue and delivered
// in the claim payload. Delegates to the wire parser in pkg/protocol — the
// same validation the quick-create API ran at save time, re-run at claim
// because the column's later writers (phase-2 surfaces) may be older.
func ParseSnapshotScope(raw []byte) ([]TaskDirectoryScope, error) {
	return protocol.ParseTaskDirectoryScope(raw)
}

// TaskDirectoryScope is one local_directory_scope entry; the wire shape and
// its reject-to-guess rules live in pkg/protocol.
type TaskDirectoryScope = protocol.TaskDirectoryScope
