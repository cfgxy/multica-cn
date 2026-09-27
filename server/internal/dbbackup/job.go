package dbbackup

import (
	"context"
	"log/slog"
	"time"
)

// RunBackupCycle performs one backup plus the retention sweep that goes
// with it. A failed dump is logged at error level and returned — never
// swallowed — while a retention failure after a successful dump is logged
// but does not fail the cycle: the fresh archive is already on disk.
func (r *Runner) RunBackupCycle(ctx context.Context) error {
	if _, _, err := r.BackupOnce(ctx); err != nil {
		slog.Error("database backup failed", "dir", r.Config.Dir, "error", err)
		return err
	}
	removed, err := EnforceRetention(r.Config.Dir, r.now(), r.Config.RetentionDays)
	if err != nil {
		slog.Error("backup retention cleanup failed", "dir", r.Config.Dir, "error", err)
		return nil
	}
	if removed > 0 {
		slog.Info("backup retention removed expired backups", "count", removed, "retention_days", r.Config.RetentionDays)
	}
	return nil
}

// RunJob drives the daily schedule until ctx is cancelled. On start it
// backs up immediately when the newest archive is older than the interval
// (catch-up after restart), then dumps on every tick. Each round gets its
// own timeout budget so a hung pg_dump delays the next round instead of
// stacking up. Every failure is logged; none of them stops the schedule.
func RunJob(ctx context.Context, cfg Config) {
	if !cfg.Enabled {
		return
	}
	runner := &Runner{Config: cfg, Now: time.Now}

	runRound := func() {
		roundCtx, cancel := context.WithTimeout(ctx, cfg.Timeout)
		defer cancel()
		_ = runner.RunBackupCycle(roundCtx)
	}

	if runner.NeedsBackup() {
		runRound()
	}

	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runRound()
		}
	}
}
