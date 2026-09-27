package dbbackup

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeBackupFile(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("payload"), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

func TestEnforceRetentionRemovesOnlyExpiredBackups(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.Local)

	fresh := writeBackupFile(t, dir, FormatBackupName(now.Add(-24*time.Hour)))
	stale := writeBackupFile(t, dir, FormatBackupName(now.Add(-8*24*time.Hour)))

	removed, err := EnforceRetention(dir, now, 7)
	if err != nil {
		t.Fatalf("EnforceRetention: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("fresh backup within the retention window was deleted: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale backup still exists after cleanup: %v", err)
	}
}

func TestEnforceRetentionKeepsBackupExactlyAtCutoff(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.Local)

	// A backup whose age equals the retention window exactly sits on the
	// boundary; the conservative side keeps it and only strictly older
	// files are removed.
	boundary := writeBackupFile(t, dir, FormatBackupName(now.AddDate(0, 0, -7)))
	justPast := writeBackupFile(t, dir, FormatBackupName(now.AddDate(0, 0, -7).Add(-time.Second)))

	removed, err := EnforceRetention(dir, now, 7)
	if err != nil {
		t.Fatalf("EnforceRetention: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	if _, err := os.Stat(boundary); err != nil {
		t.Fatalf("backup exactly at the retention boundary was deleted: %v", err)
	}
	if _, err := os.Stat(justPast); !os.IsNotExist(err) {
		t.Fatalf("backup just past the boundary still exists: %v", err)
	}
}

func TestEnforceRetentionIgnoresForeignFilesAndDirectories(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.Local)

	// Files we did not create must never be touched, even when their names
	// look close to ours.
	foreign := []string{
		"notes.txt",
		"multica-db-not-a-timestamp.dump",
		"other-db-20200101-000000.dump",
		"multica-db-20200101-000000.sql",
	}
	for _, name := range foreign {
		writeBackupFile(t, dir, name)
	}
	subDir := filepath.Join(dir, "multica-db-20200101-000000.dump.dir")
	if err := os.Mkdir(subDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	removed, err := EnforceRetention(dir, now, 7)
	if err != nil {
		t.Fatalf("EnforceRetention: %v", err)
	}
	if removed != 0 {
		t.Fatalf("removed = %d, want 0 (foreign files must be ignored)", removed)
	}
	for _, name := range foreign {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("foreign file %s was modified: %v", name, err)
		}
	}
	if _, err := os.Stat(subDir); err != nil {
		t.Fatalf("directory was removed: %v", err)
	}
}

func TestEnforceRetentionCleansStalePartialFiles(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.Local)

	stalePartial := writeBackupFile(t, dir, FormatBackupName(now.Add(-8*24*time.Hour))+".partial")
	freshPartial := writeBackupFile(t, dir, FormatBackupName(now.Add(-time.Hour))+".partial")

	removed, err := EnforceRetention(dir, now, 7)
	if err != nil {
		t.Fatalf("EnforceRetention: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	if _, err := os.Stat(freshPartial); err != nil {
		t.Fatalf("fresh partial was deleted: %v", err)
	}
	if _, err := os.Stat(stalePartial); !os.IsNotExist(err) {
		t.Fatalf("stale partial still exists: %v", err)
	}
}

func TestEnforceRetentionOnEmptyAndMissingDir(t *testing.T) {
	removed, err := EnforceRetention(t.TempDir(), time.Now(), 7)
	if err != nil || removed != 0 {
		t.Fatalf("empty dir: removed=%d err=%v, want 0/nil", removed, err)
	}

	missing := filepath.Join(t.TempDir(), "does-not-exist")
	removed, err = EnforceRetention(missing, time.Now(), 7)
	if err != nil {
		t.Fatalf("missing dir should be a no-op, got err=%v", err)
	}
	if removed != 0 {
		t.Fatalf("missing dir: removed=%d, want 0", removed)
	}
}
