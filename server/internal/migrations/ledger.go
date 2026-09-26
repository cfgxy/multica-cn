package migrations

import (
	"fmt"
	"sort"
	"strings"
)

// RenumberedLedgerEntry is one schema_migrations row whose version no longer
// exists on disk, but whose name part identifies a migration file that was
// renumbered. LedgerVersion is the stale recorded version, DiskStem the file's
// current version, and NewStemApplied reports whether the new version is also
// already recorded — which happens when the migration was replayed under its
// new number before anybody noticed the stale row.
type RenumberedLedgerEntry struct {
	LedgerVersion  string
	DiskStem       string
	NewStemApplied bool
}

// LedgerReport is the result of comparing schema_migrations against the
// migration files this checkout ships.
//
// Only Renumbered is a defect: it means the runner's per-file EXISTS check no
// longer sees the migration as applied, so a `down` run cannot remove it and an
// `up` run replays its DDL. That replay is how 901_project_instructions dropped
// live prompt content in RUYI-212.
//
// Unknown and Pending are informational. A shared development database
// accumulates rows from other branches, and a checkout that has not migrated
// yet legitimately has files with no row.
type LedgerReport struct {
	Renumbered []RenumberedLedgerEntry
	Unknown    []string
	Pending    []string
}

// HasMismatch reports whether the ledger needs repair. Unknown rows and pending
// files are excluded deliberately: failing on them would turn every developer
// database that ever checked out another branch permanently red.
func (r LedgerReport) HasMismatch() bool {
	return len(r.Renumbered) > 0
}

// RepairStatements returns the parameter-free SQL an operator pastes to make the
// ledger match disk, in Renumbered order.
//
// Per the renumbering procedure the surviving row is re-pointed at the new
// version with UPDATE, never deleted — deleting it would let the runner replay
// the migration. DELETE is emitted only for the redundant stale row in the case
// where the new version is already recorded.
func (r LedgerReport) RepairStatements() []string {
	statements := make([]string, 0, len(r.Renumbered))
	for _, entry := range r.Renumbered {
		if entry.NewStemApplied {
			statements = append(statements, fmt.Sprintf(
				"DELETE FROM schema_migrations WHERE version = '%s';", entry.LedgerVersion))
			continue
		}
		statements = append(statements, fmt.Sprintf(
			"UPDATE schema_migrations SET version = '%s' WHERE version = '%s';",
			entry.DiskStem, entry.LedgerVersion))
	}
	return statements
}

// ReconcileLedger compares the migration versions on disk with the versions
// recorded in schema_migrations and classifies every difference.
//
// Matching is by name part — the stem with its numeric prefix removed — because
// that is the only part of a migration's identity a renumber preserves.
func ReconcileLedger(diskStems, ledgerVersions []string) LedgerReport {
	diskStemSet := make(map[string]bool, len(diskStems))
	diskStemByName := make(map[string]string, len(diskStems))
	for _, stem := range diskStems {
		diskStemSet[stem] = true
		if _, name, ok := SplitStem(stem); ok {
			diskStemByName[name] = stem
		}
	}

	ledgerSet := make(map[string]bool, len(ledgerVersions))
	for _, version := range ledgerVersions {
		ledgerSet[version] = true
	}

	var report LedgerReport
	// Disk stems whose migration is recorded under some version, exact or
	// renumbered. Anything left over is genuinely pending.
	accountedDiskStems := make(map[string]bool, len(diskStems))

	for _, version := range ledgerVersions {
		if diskStemSet[version] {
			accountedDiskStems[version] = true
			continue
		}

		_, name, ok := SplitStem(version)
		if !ok {
			report.Unknown = append(report.Unknown, version)
			continue
		}
		diskStem, renumbered := diskStemByName[name]
		if !renumbered {
			report.Unknown = append(report.Unknown, version)
			continue
		}

		accountedDiskStems[diskStem] = true
		report.Renumbered = append(report.Renumbered, RenumberedLedgerEntry{
			LedgerVersion:  version,
			DiskStem:       diskStem,
			NewStemApplied: ledgerSet[diskStem],
		})
	}

	for _, stem := range diskStems {
		if !accountedDiskStems[stem] {
			report.Pending = append(report.Pending, stem)
		}
	}

	sort.Slice(report.Renumbered, func(i, j int) bool {
		return report.Renumbered[i].LedgerVersion < report.Renumbered[j].LedgerVersion
	})
	sort.Strings(report.Unknown)
	sort.Strings(report.Pending)
	return report
}

// SplitStem splits "901_project_instructions" into its numeric prefix and name
// part. It reports false for a stem without a numeric prefix followed by an
// underscore, which the lint gate rejects at the file level.
func SplitStem(stem string) (prefix, name string, ok bool) {
	underscore := strings.IndexByte(stem, '_')
	if underscore <= 0 || underscore == len(stem)-1 {
		return "", "", false
	}
	prefix, name = stem[:underscore], stem[underscore+1:]
	for i := 0; i < len(prefix); i++ {
		if prefix[i] < '0' || prefix[i] > '9' {
			return "", "", false
		}
	}
	return prefix, name, true
}
