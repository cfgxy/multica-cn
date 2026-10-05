package supervisor

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"
)

// DefaultRetention is how long a consumed run's evidence stays on disk after
// the daemon marked it converged. The window exists so a post-mortem (billing
// audit, failure investigation, read-cursor replay) still finds the logs a
// day later; nothing in the product reads converged runs after the converge
// pass, so anything past the window is pure disk rent.
const DefaultRetention = 24 * time.Hour

// RetentionGC removes run directories whose evidence is provably spent. The
// bar is deliberately conservative — the dispatch card's rule is "宁可多留，
// 不可误删" (prefer keeping evidence over deleting live history):
//
//   - the manifest must be readable (a corrupted or half-written run is
//     evidence we cannot classify, and unclassifiable evidence survives);
//   - the run must be consumed (ConvergedAt set by the daemon task layer)
//     AND carry proven exit evidence AND be past the retention window;
//   - a quarantine record on the run vetoes removal (it is unresolved
//     audit trail);
//   - when a UnitLister is wired, a unit systemd still reports active vetoes
//     removal too — belt and braces behind ConvergedAt, which only a
//     terminal run can carry.
//
// It is safe to run concurrently with a reconcile pass: reconciliation skips
// converged runs, so it never writes a directory GC has judged; and a run
// GC removes cannot come back — run IDs carry the attempt/generation, so a
// relaunched worker always lands in a fresh directory.
type RetentionGC struct {
	Mgr *Manager
	// Units is optional. nil skips the liveness cross-check.
	Units UnitLister
	Log   *slog.Logger
	// Retain is the grace window after convergence. Zero means DefaultRetention.
	Retain time.Duration
}

// GCSummary reports one pass, for the daemon log and the audit comment.
type GCSummary struct {
	// Scanned is the number of run directories found on disk.
	Scanned int
	// Removed is the count of removed run directories.
	Removed int
	// RemovedRunIDs names what was removed, so the log line is auditable.
	RemovedRunIDs []string
}

// Run performs one retention pass over the whole runs root.
func (g *RetentionGC) Run(ctx context.Context) (GCSummary, error) {
	log := g.Log
	if log == nil {
		log = slog.Default()
	}
	retain := g.Retain
	if retain <= 0 {
		retain = DefaultRetention
	}

	ids, err := g.Mgr.ListRunIDs()
	if err != nil {
		return GCSummary{}, err
	}
	summary := GCSummary{Scanned: len(ids)}
	for _, id := range ids {
		select {
		case <-ctx.Done():
			return summary, ctx.Err()
		default:
		}
		man, err := g.Mgr.ReadManifest(id)
		if err != nil {
			log.Warn("supervisor: retention keeping run with unreadable manifest", "run_id", id, "error", err)
			continue
		}
		if man.ConvergedAt == nil || man.Exit == nil {
			continue
		}
		if _, err := g.Mgr.ReadQuarantine(id); err == nil {
			log.Warn("supervisor: retention keeping quarantined run", "run_id", id)
			continue
		}
		if time.Since(*man.ConvergedAt) < retain {
			continue
		}
		if g.Units != nil {
			active, err := g.Units.UnitActive(ctx, man.Unit)
			if err == nil && active {
				log.Warn("supervisor: retention keeping run on a live unit", "run_id", id, "unit", man.Unit)
				continue
			}
		}
		if err := g.Mgr.RemoveRun(id); err != nil {
			log.Error("supervisor: retention removal failed", "run_id", id, "error", err)
			continue
		}
		summary.Removed++
		summary.RemovedRunIDs = append(summary.RemovedRunIDs, id)
	}
	if summary.Removed > 0 {
		log.Info("supervisor: retention GC removed converged runs",
			"count", summary.Removed, "run_ids", fmt.Sprintf("%v", summary.RemovedRunIDs))
	}
	return summary, nil
}

// RemoveRun deletes a run's whole directory. The id is validated first so a
// malformed id can never walk out of the runs root.
func (m *Manager) RemoveRun(runID string) error {
	if err := ValidateRunID(runID); err != nil {
		return err
	}
	return os.RemoveAll(m.Dir(runID))
}
