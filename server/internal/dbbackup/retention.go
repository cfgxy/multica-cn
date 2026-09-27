package dbbackup

import (
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// EnforceRetention deletes archives (and stale partials) older than the
// retention window. Only names this package produced are considered — a
// file whose name does not parse is never touched, whatever its age.
//
// The boundary is conservative: a backup whose age equals the window
// exactly is kept; only strictly older files are removed, so nothing
// inside the retention window is ever reaped.
func EnforceRetention(dir string, now time.Time, retentionDays int) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}

	cutoff := now.AddDate(0, 0, -retentionDays)
	removed := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ts, ok := ParseBackupName(entry.Name())
		if !ok {
			continue
		}
		if !ts.Before(cutoff) {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if err := os.Remove(path); err != nil {
			// One unremovable file must not stop the sweep of the rest.
			slog.Warn("backup retention could not remove expired file", "path", path, "error", err)
			continue
		}
		removed++
		slog.Info("backup retention removed expired file", "path", path)
	}
	return removed, nil
}
