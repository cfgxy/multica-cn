package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/migrations"
)

const migrateUsage = "Usage: go run ./cmd/migrate <up|down|check-ledger>"

// reconcileLedgerAgainstDatabase reads schema_migrations and compares it with
// the migration files this binary ships.
//
// The comparison is read-only by design: repairing the ledger rewrites run
// records, which is an operator action taken deliberately after a renumber, not
// something a migration run should do on its own. The procedure is in this
// package's README, under "Renumber a migration".
func reconcileLedgerAgainstDatabase(ctx context.Context, pool *pgxpool.Pool, table string) (migrations.LedgerReport, error) {
	diskStems, err := migrations.AllVersions()
	if err != nil {
		return migrations.LedgerReport{}, fmt.Errorf("list migration files: %w", err)
	}

	tableIdent, err := quoteQualifiedIdentifier(table)
	if err != nil {
		return migrations.LedgerReport{}, fmt.Errorf("invalid schema migrations table %q: %w", table, err)
	}

	var ledgerExists bool
	if err := pool.QueryRow(ctx,
		"SELECT EXISTS(SELECT 1 FROM pg_tables WHERE schemaname || '.' || tablename = $1 OR ($1 NOT LIKE '%.%' AND tablename = $1))",
		table,
	).Scan(&ledgerExists); err != nil {
		return migrations.LedgerReport{}, fmt.Errorf("check for %s: %w", table, err)
	}
	if !ledgerExists {
		// A database that has never migrated has every file pending and
		// nothing to reconcile.
		return migrations.ReconcileLedger(diskStems, nil), nil
	}

	rows, err := pool.Query(ctx, fmt.Sprintf("SELECT version FROM %s", tableIdent))
	if err != nil {
		return migrations.LedgerReport{}, fmt.Errorf("read %s: %w", table, err)
	}
	defer rows.Close()

	var ledgerVersions []string
	for rows.Next() {
		var version string
		if err := rows.Scan(&version); err != nil {
			return migrations.LedgerReport{}, fmt.Errorf("scan %s row: %w", table, err)
		}
		ledgerVersions = append(ledgerVersions, version)
	}
	if err := rows.Err(); err != nil {
		return migrations.LedgerReport{}, fmt.Errorf("iterate %s: %w", table, err)
	}

	return migrations.ReconcileLedger(diskStems, ledgerVersions), nil
}

// formatLedgerReport renders the report for an operator, ending with the repair
// statements when there is something to repair. Pending and unknown entries are
// listed for context but never presented as something to fix.
func formatLedgerReport(report migrations.LedgerReport) string {
	var out strings.Builder

	if !report.HasMismatch() {
		out.WriteString("schema_migrations matches the migration files on disk.\n")
	} else {
		fmt.Fprintf(&out, "%d migration(s) are recorded under a version that no longer exists on disk:\n", len(report.Renumbered))
		for _, entry := range report.Renumbered {
			state := "the new version is NOT recorded — the next up run would replay this migration"
			if entry.NewStemApplied {
				state = "the new version is already recorded — the stale row is redundant"
			}
			fmt.Fprintf(&out, "  %s -> %s (%s)\n", entry.LedgerVersion, entry.DiskStem, state)
		}
	}

	if len(report.Pending) > 0 {
		fmt.Fprintf(&out, "\n%d migration(s) on disk are not applied yet: %s\n",
			len(report.Pending), strings.Join(report.Pending, ", "))
	}
	if len(report.Unknown) > 0 {
		fmt.Fprintf(&out, "\n%d recorded version(s) match no file in this checkout (normal on a shared database): %s\n",
			len(report.Unknown), strings.Join(report.Unknown, ", "))
	}

	if report.HasMismatch() {
		out.WriteString("\nRepair by running these statements, then re-running check-ledger:\n")
		for _, statement := range report.RepairStatements() {
			fmt.Fprintf(&out, "  %s\n", statement)
		}
	}

	return out.String()
}
