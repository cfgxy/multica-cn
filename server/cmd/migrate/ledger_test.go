package main

import (
	"context"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/multica-ai/multica/server/internal/migrations"
)

// The gate must name the two states separately: a stale row whose new version is
// missing is the dangerous one, because the next `up` run replays the migration
// under its new number — the 901_project_instructions path that dropped live
// prompt content in RUYI-212.
func TestFormatLedgerReportTellsReplayApartFromRedundantRow(t *testing.T) {
	report := migrations.ReconcileLedger(
		[]string{"901_project_instructions", "902_user_admin_state"},
		[]string{"441_project_instructions", "441_user_admin_state", "901_project_instructions"},
	)

	got := formatLedgerReport(report)

	for _, want := range []string{
		"441_project_instructions -> 901_project_instructions",
		"the stale row is redundant",
		"441_user_admin_state -> 902_user_admin_state",
		"would replay this migration",
		`DELETE FROM schema_migrations WHERE version = '441_project_instructions';`,
		`UPDATE schema_migrations SET version = '902_user_admin_state' WHERE version = '441_user_admin_state';`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("report does not mention %q; full report:\n%s", want, got)
		}
	}
}

// A clean ledger must not print repair statements — an operator who sees SQL
// offered will paste it.
func TestFormatLedgerReportOffersNoRepairWhenLedgerMatchesDisk(t *testing.T) {
	report := migrations.ReconcileLedger([]string{"901_a"}, []string{"901_a"})

	got := formatLedgerReport(report)

	if !strings.Contains(got, "matches the migration files on disk") {
		t.Errorf("report does not state the ledger is clean:\n%s", got)
	}
	if strings.Contains(got, "schema_migrations SET") || strings.Contains(got, "DELETE FROM") {
		t.Errorf("clean report must not offer repair SQL:\n%s", got)
	}
}

// Pending files and rows from other branches are reported for context, but the
// gate must stay green for them: a shared development database always has both.
func TestFormatLedgerReportReportsPendingAndUnknownWithoutRepair(t *testing.T) {
	report := migrations.ReconcileLedger(
		[]string{"901_a", "902_pending"},
		[]string{"901_a", "903_other_branch"},
	)

	if report.HasMismatch() {
		t.Fatalf("pending/unknown must not be a mismatch: %+v", report)
	}

	got := formatLedgerReport(report)
	if !strings.Contains(got, "902_pending") {
		t.Errorf("report omits the pending migration:\n%s", got)
	}
	if !strings.Contains(got, "903_other_branch") {
		t.Errorf("report omits the unknown ledger row:\n%s", got)
	}
	if strings.Contains(got, "Repair by running") {
		t.Errorf("clean report must not offer repair SQL:\n%s", got)
	}
}

// The database side: a ledger carrying a renumbered row must be detected against
// a real schema_migrations table, and the emitted statements must actually
// reconcile it when executed.
func TestReconcileLedgerAgainstDatabaseDetectsAndRepairsRenumberedRow(t *testing.T) {
	adminPool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	suffix := fmt.Sprintf("%d_%d", time.Now().UnixNano(), rand.Uint32())
	schema := "migrate_ledger_" + suffix
	schemaIdent := pgx.Identifier{schema}.Sanitize()
	if _, err := adminPool.Exec(ctx, "CREATE SCHEMA "+schemaIdent); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if _, err := adminPool.Exec(cleanupCtx, "DROP SCHEMA IF EXISTS "+schemaIdent+" CASCADE"); err != nil {
			t.Logf("drop schema %s: %v", schema, err)
		}
	})

	table := schema + ".schema_migrations"
	tableIdent, err := quoteQualifiedIdentifier(table)
	if err != nil {
		t.Fatalf("quote %s: %v", table, err)
	}
	if _, err := adminPool.Exec(ctx, fmt.Sprintf(
		"CREATE TABLE %s (version TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())", tableIdent,
	)); err != nil {
		t.Fatalf("create ledger table: %v", err)
	}

	// Record every shipped migration, then rewrite one row to the number that
	// migration carried before it was renumbered.
	diskStems, err := migrations.AllVersions()
	if err != nil {
		t.Fatalf("list migrations: %v", err)
	}
	for _, stem := range diskStems {
		if _, err := adminPool.Exec(ctx, fmt.Sprintf("INSERT INTO %s (version) VALUES ($1)", tableIdent), stem); err != nil {
			t.Fatalf("record %s: %v", stem, err)
		}
	}
	renumbered := diskStems[len(diskStems)-1]
	_, name, ok := migrations.SplitStem(renumbered)
	if !ok {
		t.Fatalf("migration %s has no numeric prefix", renumbered)
	}
	staleVersion := "441_" + name
	if _, err := adminPool.Exec(ctx, fmt.Sprintf("UPDATE %s SET version = $1 WHERE version = $2", tableIdent),
		staleVersion, renumbered); err != nil {
		t.Fatalf("plant stale row: %v", err)
	}

	report, err := reconcileLedgerAgainstDatabase(ctx, adminPool, table)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if !report.HasMismatch() {
		t.Fatalf("stale row %s was not detected: %+v", staleVersion, report)
	}
	statements := report.RepairStatements()
	want := fmt.Sprintf("UPDATE schema_migrations SET version = '%s' WHERE version = '%s';", renumbered, staleVersion)
	if len(statements) != 1 || statements[0] != want {
		t.Fatalf("RepairStatements() = %v, want [%s]", statements, want)
	}

	// The published statement targets the unqualified table name, so apply it
	// against this test's schema by pointing search_path at it.
	repair := strings.Replace(statements[0], "schema_migrations", tableIdent, 1)
	if _, err := adminPool.Exec(ctx, repair); err != nil {
		t.Fatalf("apply repair %q: %v", repair, err)
	}

	after, err := reconcileLedgerAgainstDatabase(ctx, adminPool, table)
	if err != nil {
		t.Fatalf("reconcile after repair: %v", err)
	}
	if after.HasMismatch() {
		t.Fatalf("ledger still mismatched after repair: %+v", after)
	}
}

// A database that has never run a migration must report every file pending
// rather than erroring on the missing ledger table.
func TestReconcileLedgerAgainstDatabaseTreatsMissingLedgerAsAllPending(t *testing.T) {
	adminPool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	suffix := fmt.Sprintf("%d_%d", time.Now().UnixNano(), rand.Uint32())
	table := "migrate_ledger_absent_" + suffix

	report, err := reconcileLedgerAgainstDatabase(ctx, adminPool, table)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if report.HasMismatch() {
		t.Fatalf("missing ledger must not be a mismatch: %+v", report)
	}

	diskStems, err := migrations.AllVersions()
	if err != nil {
		t.Fatalf("list migrations: %v", err)
	}
	if len(report.Pending) != len(diskStems) {
		t.Fatalf("Pending has %d entries, want all %d migrations", len(report.Pending), len(diskStems))
	}
}
