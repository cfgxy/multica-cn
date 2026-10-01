package migrations

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// createTablePattern matches the permanent-table creation statements this
// schema's migrations use. TEMPORARY tables sit between CREATE and TABLE and
// so never match; that is correct, they do not outlive their migration run.
var createTablePattern = regexp.MustCompile(
	`(?im)CREATE\s+(?:UNLOGGED\s+)?TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?"?([a-z_][a-z0-9_]*)"?`)
var dropTablePattern = regexp.MustCompile(
	`(?im)DROP\s+TABLE\s+(?:IF\s+EXISTS\s+)?"?([a-z_][a-z0-9_]*)"?`)
var lineCommentPattern = regexp.MustCompile(`--[^\n]*`)
var blockCommentPattern = regexp.MustCompile(`(?s)/\*.*?\*/`)

// stripComments removes SQL comments so prose like "-- CREATE TABLE IF NOT
// EXISTS, so rerunning…" (migration 442) cannot be mistaken for DDL.
func stripComments(sql string) string {
	sql = blockCommentPattern.ReplaceAllString(sql, " ")
	return lineCommentPattern.ReplaceAllString(sql, "")
}

// RequiredTables returns the permanent tables a full run of this branch's up
// migrations leaves behind: every CREATE TABLE target minus every table some
// later up migration drops. The value is the version that creates the table,
// so a physical mismatch can name the migration it belongs to.
//
// It backs the test-suite precheck's physical spot check (RUYI-311): a
// schema_migrations row can claim a migration applied while the table it
// creates is physically gone, and handler tests then fail as 500s in
// unrelated cases. Parsing is deliberately limited to plain CREATE/DROP
// TABLE — the two statement shapes every lifecycle fact this check needs is
// written in — and every extracted name must survive a real migration run,
// which the positive-path test proves on an aligned database.
func RequiredTables() (map[string]string, error) {
	files, err := Files("up")
	if err != nil {
		return nil, err
	}

	required := make(map[string]string)
	for _, file := range files {
		content, err := os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", file, err)
		}
		version := ExtractVersion(file)
		sql := stripComments(string(content))
		// Line order matters: a migration may drop and re-create the same
		// table in one file (113, 344), so statements are applied in the
		// order they appear.
		for _, line := range strings.Split(sql, "\n") {
			if match := createTablePattern.FindStringSubmatch(line); match != nil {
				required[match[1]] = version
			}
			if match := dropTablePattern.FindStringSubmatch(line); match != nil {
				delete(required, match[1])
			}
		}
	}
	return required, nil
}
