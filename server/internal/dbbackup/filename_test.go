package dbbackup

import (
	"testing"
	"time"
)

func TestFormatBackupName(t *testing.T) {
	// A wall-clock timestamp with no colons, so the name is shell and rsync friendly.
	got := FormatBackupName(time.Date(2026, 9, 27, 14, 3, 5, 0, time.Local))
	want := "multica-db-20260927-140305.dump"
	if got != want {
		t.Fatalf("FormatBackupName = %q, want %q", got, want)
	}
}

func TestParseBackupName(t *testing.T) {
	valid := map[string]time.Time{
		"multica-db-20260927-140305.dump":          time.Date(2026, 9, 27, 14, 3, 5, 0, time.Local),
		"multica-db-20260101-000000.dump":          time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local),
		"multica-db-20260927-140305.dump.partial":  time.Date(2026, 9, 27, 14, 3, 5, 0, time.Local),
	}
	for name, want := range valid {
		got, ok := ParseBackupName(name)
		if !ok {
			t.Fatalf("ParseBackupName(%q) = not parsed, want %s", name, want)
		}
		if !got.Equal(want) {
			t.Fatalf("ParseBackupName(%q) = %s, want %s", name, got, want)
		}
	}

	invalid := []string{
		"",
		"notes.txt",
		"multica-db-20260927-140305.sql",
		"multica-db-20260927-140305",
		"other-db-20260927-140305.dump",
		"multica-db-20261399-996099.dump",  // invalid month/day
		"multica-db-2026092-1403050.dump",  // wrong digit counts
		"multica-db-notatime.dump",
	}
	for _, name := range invalid {
		if got, ok := ParseBackupName(name); ok {
			t.Fatalf("ParseBackupName(%q) = %s, want not parsed", name, got)
		}
	}
}

func TestBackupNameRoundTrip(t *testing.T) {
	now := time.Date(2026, 9, 27, 8, 1, 2, 0, time.Local)
	parsed, ok := ParseBackupName(FormatBackupName(now))
	if !ok {
		t.Fatalf("round trip of %s failed to parse", now)
	}
	if !parsed.Equal(now) {
		t.Fatalf("round trip = %s, want %s", parsed, now)
	}
}
