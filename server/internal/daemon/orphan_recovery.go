package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

// orphanRecoveryErrMsg matches the error string RecoverOrphanedTasksForRuntime
// writes, so a task failed by the probe reads exactly like one failed by the
// blanket recovery that used to be its only path back to the queue.
const orphanRecoveryErrMsg = "daemon restarted while task was in flight"

// recoverInFlightTasksForRuntime fails the in-flight tasks on runtimeID whose
// worker provably died with a previous daemon process, and leaves everything
// it cannot prove dead alone (RUYI-225).
//
// The liveness signal is the env root's kernel advisory execution lock
// (.task_lock, held for a worker's whole lifetime by ClaimEnvRoot — and by
// LockEnvRootForReuse for the adopted directory of a reuse run): the kernel
// releases it when the holding process dies, so an acquirable lock proves the
// worker is gone while a held one proves live work. That turns MUL-3332's
// "cannot distinguish an orphan from a live session" into a per-task
// decision, which is what makes this safe on the converge registration path
// where the blind RecoverOrphans must never run.
//
// Conservative by design — anything it cannot prove dead stays untouched:
//   - a task this process is executing right now is skipped by ID;
//   - a task without a pinned work_dir (waiting_local_directory rows never
//     pin one) is skipped — its wait mutex lived inside the dead process,
//     which no probe from outside can see;
//   - a task whose env root cannot be resolved read-only is skipped, because
//     the resolution must never install a record a retry would later trust;
//   - any probe error is skipped.
//
// The lock is released immediately after the probe and the directory is never
// reset — the auto-retry's own claim owns that lifecycle.
func (d *Daemon) recoverInFlightTasksForRuntime(ctx context.Context, runtimeID string) {
	tasks, err := d.client.ListInFlightTasks(ctx, runtimeID)
	if err != nil {
		d.logger.Warn("in-flight recovery: list failed", "runtime_id", runtimeID, "error", err)
		return
	}
	if len(tasks) == 0 {
		return
	}
	if d.cfg.WorkspacesRoot == "" {
		d.logger.Warn("in-flight recovery: workspaces root unset; skipping", "runtime_id", runtimeID, "tasks", len(tasks))
		return
	}
	wsRoot, err := os.OpenRoot(d.cfg.WorkspacesRoot)
	if err != nil {
		d.logger.Warn("in-flight recovery: open workspaces root failed", "runtime_id", runtimeID, "root", d.cfg.WorkspacesRoot, "error", err)
		return
	}
	defer wsRoot.Close()

	for _, t := range tasks {
		if d.isTaskActiveInProcess(t.ID) {
			d.logger.Debug("in-flight recovery: task active in this process; skipping", "task", t.ID, "runtime_id", runtimeID)
			continue
		}
		if t.WorkDir == "" {
			d.logger.Info("in-flight recovery: task has no pinned work_dir; skipping", "task", t.ID, "runtime_id", runtimeID, "status", t.Status)
			continue
		}
		alive, err := d.probeTaskWorker(wsRoot, t)
		if err != nil {
			d.logger.Warn("in-flight recovery: probe failed; skipping", "task", t.ID, "runtime_id", runtimeID, "error", err)
			continue
		}
		if alive {
			d.logger.Info("in-flight recovery: worker still alive; leaving task alone", "task", t.ID, "runtime_id", runtimeID, "work_dir", t.WorkDir)
			continue
		}
		// Supervised-worker override (RUYI-349): the env-root lock above is
		// held by the DAEMON process, so a daemon death releases it and the
		// probe reads "dead" even while a systemd-unit worker is still
		// running the task. When a live supervised manifest exists for the
		// task, the worker is alive by stronger evidence — leave the task
		// alone; startup reconciliation reattached or will reattach it.
		if d.supervisedWorkerAlive(t.ID) {
			d.logger.Info("in-flight recovery: supervised worker still alive; leaving task alone",
				"task", t.ID, "runtime_id", runtimeID, "work_dir", t.WorkDir)
			continue
		}
		if err := d.client.FailTask(ctx, t.ID, orphanRecoveryErrMsg, "", "", "", "runtime_recovery", false, "", ""); err != nil {
			d.logger.Warn("in-flight recovery: fail task failed", "task", t.ID, "runtime_id", runtimeID, "error", err)
			continue
		}
		d.logger.Info("in-flight recovery: dead worker proven by env-root lock; task failed for auto-retry",
			"task", t.ID, "runtime_id", runtimeID, "work_dir", t.WorkDir)
	}
}

// probeTaskWorker decides whether the task's worker is still alive by probing
// execution locks. A worker holds its own env root's lock; a worker on the
// reuse path additionally holds the adopted directory's lock, which is why
// the work_dir's parent is probed alongside the record-resolved root — the
// live worker shows up on either, and only a worker dead everywhere is dead.
func (d *Daemon) probeTaskWorker(wsRoot *os.Root, t InFlightTask) (bool, error) {
	envRoot, err := execenv.ResolveRootDirReadOnly(execenv.RootDirParams{
		WorkspacesRoot: d.cfg.WorkspacesRoot,
		WorkspaceID:    t.WorkspaceID,
		TaskID:         t.ID,
	})
	if err != nil {
		return true, fmt.Errorf("resolve env root: %w", err)
	}
	if envRoot == "" {
		// No task-root record: the task never resolved a root, so there is
		// nothing to interrogate and nothing to conclude. Probing a
		// predicted guess instead could miss a live worker entirely.
		return true, fmt.Errorf("execenv: no task-root record for task %s", t.ID)
	}

	held, err := probeEnvRootLock(wsRoot, d.cfg.WorkspacesRoot, envRoot)
	if err != nil || held {
		return held, err
	}
	if prior := filepath.Dir(filepath.Clean(t.WorkDir)); prior != envRoot &&
		pathInside(prior, d.cfg.WorkspacesRoot) && looksLikeEnvRoot(prior) {
		held, err = probeEnvRootLock(wsRoot, d.cfg.WorkspacesRoot, prior)
		if err != nil || held {
			return held, err
		}
	}
	return false, nil
}

// probeEnvRootLock tries the directory's execution lock non-blocking and
// reports whether a live worker holds it. An acquirable lock is released
// immediately and the directory is left untouched — the auto-retry's own
// claim owns reset and reuse.
func probeEnvRootLock(wsRoot *os.Root, workspacesRoot, envRoot string) (bool, error) {
	rel, err := filepath.Rel(workspacesRoot, envRoot)
	if err != nil {
		return false, fmt.Errorf("execenv: make env root relative: %w", err)
	}
	claim, _, err := execenv.LockEnvRootForReuse(wsRoot, rel, envRoot)
	if errors.Is(err, execenv.ErrEnvRootBusy) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if claim != nil {
		claim.Release()
	}
	// A nil claim without error means the env root does not exist: no
	// directory, no lock, no worker — nothing live runs without it.
	return false, nil
}

// looksLikeEnvRoot reports whether path carries an env-root owner marker.
// The adopted-directory candidate is only probed when this passes, so the
// probe never creates a .task_lock in a directory that was never an env root.
func looksLikeEnvRoot(path string) bool {
	owner, err := execenv.ReadEnvRootOwner(path)
	return err == nil && owner != nil && owner.TaskID != ""
}

// pathInside reports whether child lies within parent (or equals it).
func pathInside(child, parent string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// markTaskActiveInProcess / unmarkTaskActiveInProcess / isTaskActiveInProcess
// maintain the per-task ID registry the recovery consults before probing.
// The write helpers lazy-init the map so directly-constructed test daemons
// (which leave the field nil) stay safe if they dispatch tasks.
func (d *Daemon) markTaskActiveInProcess(taskID string) {
	d.activeTaskIDsMu.Lock()
	if d.activeTaskIDs == nil {
		d.activeTaskIDs = make(map[string]struct{})
	}
	d.activeTaskIDs[taskID] = struct{}{}
	d.activeTaskIDsMu.Unlock()
}

func (d *Daemon) unmarkTaskActiveInProcess(taskID string) {
	d.activeTaskIDsMu.Lock()
	delete(d.activeTaskIDs, taskID)
	d.activeTaskIDsMu.Unlock()
}

func (d *Daemon) isTaskActiveInProcess(taskID string) bool {
	d.activeTaskIDsMu.Lock()
	defer d.activeTaskIDsMu.Unlock()
	_, ok := d.activeTaskIDs[taskID]
	return ok
}
