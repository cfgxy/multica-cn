package dbbackup

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// maxStderrTail bounds how much pg_dump stderr is embedded in an error, so
// a chatty failure cannot flood the server log.
const maxStderrTail = 2000

// Runner executes single backup cycles against the configured directory.
// Now is injectable so callers (and tests) control archive naming without
// sleeping.
type Runner struct {
	Config Config
	Now    func() time.Time
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// NeedsBackup reports whether a fresh dump is due: true when the directory
// holds no archive yet, or when the newest one is at least Interval old.
// This is what makes the schedule survive restarts — a server that restarts
// several times a day still gets exactly one dump per interval instead of
// resetting its timer on every boot.
func (r *Runner) NeedsBackup() bool {
	newest, found, err := newestBackupTime(r.Config.Dir)
	if err != nil {
		// An unreadable directory will fail the backup attempt with the
		// same error; trying is what surfaces it.
		return true
	}
	if !found {
		return true
	}
	return r.now().Sub(newest) >= r.Config.Interval
}

// BackupOnce runs pg_dump into the backup directory and returns the final
// archive path and its size. The dump is written to a .partial temp file
// and renamed into place, so a crash or failed run never leaves a file
// that looks like a completed archive.
func (r *Runner) BackupOnce(ctx context.Context) (string, int64, error) {
	cfg := r.Config
	if err := ensureDir(cfg.Dir); err != nil {
		return "", 0, fmt.Errorf("create backup dir %s: %w", cfg.Dir, err)
	}

	final := filepath.Join(cfg.Dir, FormatBackupName(r.now()))
	tmp := partialName(final)

	// -Fc: custom-format archive, internally compressed — one file that
	// satisfies "dump and compress" while supporting selective restore via
	// pg_restore. --no-owner/--no-privileges keeps the archive restorable
	// into a cluster whose roles differ from the source.
	cmd := exec.CommandContext(ctx, cfg.PGDumpPath,
		"-Fc",
		"-d", cfg.DatabaseURL,
		"-f", tmp,
		"--no-owner",
		"--no-privileges",
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	// When the context cancels a dump, exec kills only pg_dump itself; any
	// descendant holding the stderr pipe would otherwise keep Run blocked.
	// WaitDelay abandons the pipes shortly after the kill so the round ends
	// promptly.
	cmd.WaitDelay = 5 * time.Second

	start := time.Now()
	if err := cmd.Run(); err != nil {
		os.Remove(tmp)
		return "", 0, fmt.Errorf("pg_dump failed: %w: %s", err, stderrTail(stderr.String()))
	}
	// pg_dump honors the creating umask; archives carry the whole database,
	// so restrict them to the owner explicitly.
	if err := os.Chmod(tmp, 0o600); err != nil {
		os.Remove(tmp)
		return "", 0, fmt.Errorf("restrict backup file mode: %w", err)
	}
	if err := os.Rename(tmp, final); err != nil {
		os.Remove(tmp)
		return "", 0, fmt.Errorf("finalize backup file: %w", err)
	}
	info, err := os.Stat(final)
	if err != nil {
		return final, 0, fmt.Errorf("stat finished backup: %w", err)
	}
	slog.Info("database backup completed",
		"path", final,
		"size_bytes", info.Size(),
		"duration", time.Since(start).Round(time.Millisecond),
	)
	return final, info.Size(), nil
}

// newestBackupTime scans dir for archives this package named and returns
// the most recent timestamp.
func newestBackupTime(dir string) (time.Time, bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return time.Time{}, false, nil
		}
		return time.Time{}, false, err
	}
	var newest time.Time
	found := false
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ts, ok := ParseBackupName(entry.Name())
		if !ok {
			continue
		}
		if !found || ts.After(newest) {
			newest = ts
			found = true
		}
	}
	return newest, found, nil
}

func stderrTail(s string) string {
	s = strings.TrimRight(strings.TrimSpace(s), "\n")
	if len(s) <= maxStderrTail {
		return s
	}
	return "…" + s[len(s)-maxStderrTail:]
}
