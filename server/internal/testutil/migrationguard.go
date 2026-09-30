package testutil

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/migrations"
)

// The precheck exists because of RUYI-311: a shared development database can
// carry a schema_migrations ledger this branch cannot reason about — versions
// an unmerged branch applied (superset), branch migrations that were never
// applied, or rows recorded as applied while the tables they create are
// physically gone. Handler tests against such a database fail in misleading
// ways: dynamic schema-manifest cases go structurally red, unrelated cases
// 500 on missing objects. CheckMigrationLedger names the environment problem
// before the first test runs. It is strictly read-only.
//
// The trade-off is deliberate: a database another branch migrated becomes
// unusable for this branch's suite until the branches or the database are
// aligned. That is the point — the alternative was the misleading failure.

// ledgerDrift classifies the difference between the branch's on-disk
// migration set and the database's ledger: extras are ledger rows no file in
// this branch defines, missing are branch migrations the database has not
// applied. Both result slices are sorted.
func ledgerDrift(branch, ledger []string) (extras, missing []string) {
	branchSet := make(map[string]bool, len(branch))
	for _, v := range branch {
		branchSet[v] = true
	}
	ledgerSet := make(map[string]bool, len(ledger))
	for _, v := range ledger {
		ledgerSet[v] = true
	}
	for _, v := range ledger {
		if !branchSet[v] {
			extras = append(extras, v)
		}
	}
	for _, v := range branch {
		if !ledgerSet[v] {
			missing = append(missing, v)
		}
	}
	sort.Strings(extras)
	sort.Strings(missing)
	return extras, missing
}

// newGuardPool connects the precheck to a database.
func newGuardPool(dsn string) (*pgxpool.Pool, error) {
	return pgxpool.New(context.Background(), dsn)
}

// ledgerVersions reads the database's applied migration versions.
func ledgerVersions(ctx context.Context, pool *pgxpool.Pool) ([]string, error) {
	rows, err := pool.Query(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "42P01" {
			return nil, fmt.Errorf("schema_migrations does not exist; apply migrations first (cd server && go run ./cmd/migrate up; make test does this)")
		}
		return nil, err
	}
	defer rows.Close()

	var versions []string
	for rows.Next() {
		var version string
		if err := rows.Scan(&version); err != nil {
			return nil, err
		}
		versions = append(versions, version)
	}
	return versions, rows.Err()
}

// CheckMigrationLedger verifies, read-only, that the target database's
// schema_migrations ledger and physical schema match this branch's
// server/migrations set. It fails with the offending versions and tables
// named, so the environment problem is reported as itself instead of
// surfacing as unrelated test failures (RUYI-311).
func CheckMigrationLedger(ctx context.Context, pool *pgxpool.Pool) error {
	branch, err := migrations.AllVersions()
	if err != nil {
		return fmt.Errorf("resolve branch migrations: %w", err)
	}
	ledger, err := ledgerVersions(ctx, pool)
	if err != nil {
		return err
	}

	extras, missing := ledgerDrift(branch, ledger)
	if len(extras) > 0 {
		return fmt.Errorf("migration ledger drift: the database's schema_migrations records %d versions this branch's server/migrations does not define: %s. The database was migrated by a different branch; this suite would run against a schema it cannot reason about and fail in misleading ways. Run the suite against an isolated database migrated by this branch, or align the branches first",
			len(extras), strings.Join(extras, ", "))
	}
	if len(missing) > 0 {
		return fmt.Errorf("migration ledger drift: %d migrations of this branch are not recorded in the database's schema_migrations: %s. Apply migrations first (cd server && go run ./cmd/migrate up; make test does this)",
			len(missing), strings.Join(missing, ", "))
	}

	required, err := migrations.RequiredTables()
	if err != nil {
		return fmt.Errorf("extract required tables: %w", err)
	}
	var absent []string
	for table, version := range required {
		var exists bool
		if err := pool.QueryRow(ctx,
			`SELECT to_regclass(format('public.%I', $1::text)) IS NOT NULL`, table,
		).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			absent = append(absent, fmt.Sprintf("%s (created by %s)", table, version))
		}
	}
	if len(absent) > 0 {
		sort.Strings(absent)
		return fmt.Errorf("migration ledger drift: %d tables that on-disk migrations create (and no later migration drops) are missing from the database while their migration is recorded as applied: %s. The ledger and the physical schema diverged; repair the database (or rebuild it with migrate up) before running tests",
			len(absent), strings.Join(absent, ", "))
	}
	return nil
}
