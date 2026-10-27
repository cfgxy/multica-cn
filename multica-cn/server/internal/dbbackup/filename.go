package dbbackup

import (
	"regexp"
	"time"
)

const (
	fileNamePrefix = "multica-db-"
	// fileNameLayout is sortable, colon-free, and unambiguous in ls output.
	fileNameLayout = "20060102-150405"
	fileExtension  = ".dump"
	// partialSuffix marks an in-progress dump; a crash can leave one behind
	// and retention reaps stale partials with the same window as finals.
	partialSuffix = ".partial"
)

var fileNamePattern = regexp.MustCompile(`^` + regexp.QuoteMeta(fileNamePrefix) + `(\d{8}-\d{6})` + regexp.QuoteMeta(fileExtension) + `(` + regexp.QuoteMeta(partialSuffix) + `)?$`)

// FormatBackupName renders the archive name for a dump taken at t, in local
// time: multica-db-20260927-140305.dump.
func FormatBackupName(t time.Time) string {
	return fileNamePrefix + t.Format(fileNameLayout) + fileExtension
}

// ParseBackupName recovers the dump timestamp from an archive (or partial)
// name produced by FormatBackupName. Anything else — foreign files, renamed
// archives — reports false so retention never touches it.
func ParseBackupName(name string) (time.Time, bool) {
	m := fileNamePattern.FindStringSubmatch(name)
	if m == nil {
		return time.Time{}, false
	}
	// Names carry local wall-clock time (same host that formats them), so
	// parse in the same location instead of UTC.
	ts, err := time.ParseInLocation(fileNameLayout, m[1], time.Local)
	if err != nil {
		return time.Time{}, false
	}
	return ts, true
}

// partialName is the temp name a dump writes to before the atomic rename.
func partialName(final string) string {
	return final + partialSuffix
}
