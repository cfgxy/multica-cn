package dbbackup

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// writeFakePGDump installs a stand-in pg_dump that honors the -f <path>
// argument, so the real exec plumbing (argument wiring, temp file, rename)
// is exercised without a PostgreSQL server.
func writeFakePGDump(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake pg_dump shell script requires a POSIX shell")
	}
	path := filepath.Join(t.TempDir(), "fake-pg-dump.sh")
	script := "#!/bin/sh\n" + body + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake pg_dump: %v", err)
	}
	return path
}

func TestBackupOnceProducesTimestampedCompressedFile(t *testing.T) {
	dir := t.TempDir()
	fake := writeFakePGDump(t, `
for arg in "$@"; do
  if [ "$prev" = "-f" ]; then out="$arg"; fi
  prev="$arg"
done
[ -n "$out" ] || { echo "fake pg_dump: no -f argument" >&2; exit 3; }
printf 'pgc-dump-payload' > "$out"
`)

	now := time.Date(2026, 9, 27, 9, 30, 0, 0, time.Local)
	runner := &Runner{
		Config: Config{
			Enabled:     true,
			Dir:         dir,
			PGDumpPath:  fake,
			DatabaseURL: "postgres://user:pass@localhost:5432/multica",
			Timeout:     time.Minute,
			Interval:    DefaultInterval,
		},
		Now: func() time.Time { return now },
	}

	path, size, err := runner.BackupOnce(context.Background())
	if err != nil {
		t.Fatalf("BackupOnce: %v", err)
	}
	want := filepath.Join(dir, "multica-db-20260927-093000.dump")
	if path != want {
		t.Fatalf("BackupOnce path = %q, want %q", path, want)
	}
	if size != int64(len("pgc-dump-payload")) {
		t.Fatalf("size = %d, want %d", size, len("pgc-dump-payload"))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if string(data) != "pgc-dump-payload" {
		t.Fatalf("backup content = %q, want the fake dump payload", data)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat backup: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("backup mode = %o, want 600 (backups may contain user data)", info.Mode().Perm())
	}
	// No temp file may survive a successful backup.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("backup dir has %d entries, want exactly the one final file", len(entries))
	}
}

func TestBackupOnceFailureLeavesNoArtifacts(t *testing.T) {
	dir := t.TempDir()
	fake := writeFakePGDump(t, `
echo "pg_dump: error: connection refused" >&2
exit 1
`)

	runner := &Runner{
		Config: Config{
			Enabled:     true,
			Dir:         dir,
			PGDumpPath:  fake,
			DatabaseURL: "postgres://user:pass@localhost:5432/multica",
			Timeout:     time.Minute,
			Interval:    DefaultInterval,
		},
		Now: func() time.Time { return time.Date(2026, 9, 27, 9, 30, 0, 0, time.Local) },
	}

	_, _, err := runner.BackupOnce(context.Background())
	if err == nil {
		t.Fatal("BackupOnce = nil error, want the pg_dump failure to surface")
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("error %q does not carry the pg_dump stderr for diagnosis", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("failed backup left artifacts %v, want a clean directory", names)
	}
}

func TestBackupOnceHonorsContextCancellation(t *testing.T) {
	dir := t.TempDir()
	fake := writeFakePGDump(t, `
sleep 30
`)
	runner := &Runner{
		Config: Config{
			Enabled:     true,
			Dir:         dir,
			PGDumpPath:  fake,
			DatabaseURL: "postgres://db",
			Timeout:     time.Minute,
			Interval:    DefaultInterval,
		},
		Now: func() time.Time { return time.Now() },
	}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, _, err := runner.BackupOnce(ctx); err == nil {
		t.Fatal("BackupOnce = nil error, want context cancellation to fail the backup")
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("BackupOnce took %s, cancellation should abort the dump promptly", elapsed)
	}
}

func TestNeedsBackup(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.Local)
	runner := &Runner{
		Config: Config{Dir: dir, Interval: 24 * time.Hour},
		Now:    func() time.Time { return now },
	}

	// No backups at all: must back up.
	if !runner.NeedsBackup() {
		t.Fatal("NeedsBackup = false with empty dir, want true")
	}

	// A backup well inside the window: no new dump.
	writeBackupFile(t, dir, FormatBackupName(now.Add(-2*time.Hour)))
	if runner.NeedsBackup() {
		t.Fatal("NeedsBackup = true with a 2h-old backup and a 24h interval, want false")
	}

	// Exactly at the interval boundary: due.
	runner2 := &Runner{
		Config: Config{Dir: dir, Interval: 2 * time.Hour},
		Now:    func() time.Time { return now },
	}
	if !runner2.NeedsBackup() {
		t.Fatal("NeedsBackup = false at the interval boundary, want true")
	}
}

func TestRunBackupCycleBacksUpAndAppliesRetention(t *testing.T) {
	dir := t.TempDir()
	fake := writeFakePGDump(t, `
out=""
prev=""
for arg in "$@"; do
  if [ "$prev" = "-f" ]; then out="$arg"; fi
  prev="$arg"
done
printf 'payload' > "$out"
`)
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.Local)
	// An expired backup from before this cycle must be cleaned in the same round.
	writeBackupFile(t, dir, FormatBackupName(now.Add(-30*24*time.Hour)))

	runner := &Runner{
		Config: Config{
			Enabled:       true,
			Dir:           dir,
			PGDumpPath:    fake,
			DatabaseURL:   "postgres://db",
			Timeout:       time.Minute,
			Interval:      DefaultInterval,
			RetentionDays: 7,
		},
		Now: func() time.Time { return now },
	}

	if err := runner.RunBackupCycle(context.Background()); err != nil {
		t.Fatalf("RunBackupCycle: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 1 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("cycle left %v, want exactly the fresh backup", names)
	}
	parsed, ok := ParseBackupName(entries[0].Name())
	if !ok || !parsed.Equal(now) {
		t.Fatalf("cycle produced %q, want the backup named at %s", entries[0].Name(), now)
	}
}
