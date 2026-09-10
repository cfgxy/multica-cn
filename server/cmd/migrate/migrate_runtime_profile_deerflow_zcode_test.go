package main

import (
	"context"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestRuntimeProfileDeerflowZcodeMigrationRoundTrip exercises migration 907 in
// both directions inside a private schema: up must widen the protocol_family
// whitelist to the two new independent families, down must restore the
// pre-907 whitelist, and neither direction may invalidate a compatibility row
// that still declares 'kimi' while launching one of the bridges — that is the
// shape every existing profile has, and losing it would break them.
func TestRuntimeProfileDeerflowZcodeMigrationRoundTrip(t *testing.T) {
	adminPool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	suffix := fmt.Sprintf("%d_%d", time.Now().UnixNano(), rand.Uint32())
	schema := "migrate_runtime_profile_families_" + suffix
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

	pool := openTestPoolWithSearchPath(t, schema)

	// Minimal stand-in for the production table: the migration only touches
	// the protocol_family CHECK, and the constraint carries the same name it
	// does in production so DROP CONSTRAINT IF EXISTS finds it.
	if _, err := pool.Exec(ctx, `
		CREATE TABLE runtime_profile (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			display_name TEXT NOT NULL,
			protocol_family TEXT NOT NULL,
			command_name TEXT NOT NULL,
			CONSTRAINT runtime_profile_protocol_family_check
				CHECK (protocol_family IN ('kimi', 'claude', 'codex'))
		)
	`); err != nil {
		t.Fatalf("create runtime_profile fixture: %v", err)
	}

	// A profile in the shape the split has to stay compatible with.
	if _, err := pool.Exec(ctx, `
		INSERT INTO runtime_profile (display_name, protocol_family, command_name)
		VALUES ('Legacy ZCode over Kimi', 'kimi', 'zcode-acp')
	`); err != nil {
		t.Fatalf("insert compatibility profile: %v", err)
	}

	const version = "907_runtime_profile_add_deerflow_zcode"
	lockKey := int64(rand.Uint64()&0x7fffffffffffffff) | 1
	run := func(direction string) error {
		return runMigrations(ctx, pool, runOptions{
			Direction:             direction,
			Files:                 realMigrationFiles(t, []string{version}, direction),
			SchemaMigrationsTable: schema + ".schema_migrations",
			AdvisoryLockKey:       lockKey,
			Hooks:                 hooksForDirection(direction),
		})
	}

	// Before 907 the two families have no place to be stored.
	assertFamilyRejected(t, ctx, pool, "deerflow")
	assertFamilyRejected(t, ctx, pool, "zcode")

	if err := run("up"); err != nil {
		t.Fatalf("apply migration 907: %v", err)
	}
	assertMigrationLedger(t, ctx, pool, version, true)

	for _, family := range []string{"deerflow", "zcode"} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO runtime_profile (display_name, protocol_family, command_name)
			VALUES ($1, $2, $3)
		`, "Independent "+family, family, family+"-acp"); err != nil {
			t.Fatalf("after 907 up, protocol_family %q was still rejected: %v", family, err)
		}
	}
	// The pre-existing families must not have been dropped on the way.
	assertFamilyAccepted(t, ctx, pool, "kimi")
	// And something outside the new whitelist must still be refused, so the
	// widening did not degenerate into "anything goes".
	assertFamilyRejected(t, ctx, pool, "deerflow-acp")

	if err := run("down"); err != nil {
		t.Fatalf("roll back migration 907: %v", err)
	}
	assertMigrationLedger(t, ctx, pool, version, false)

	// The rows inserted while 907 was applied survive the rollback: the
	// restored constraint is NOT VALID, so it only gates new writes. New
	// writes in those families are blocked again.
	var kept int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM runtime_profile WHERE protocol_family IN ('deerflow', 'zcode')`,
	).Scan(&kept); err != nil {
		t.Fatalf("count rows after rollback: %v", err)
	}
	if kept != 2 {
		t.Errorf("rows in the new families after rollback = %d, want 2 kept by NOT VALID", kept)
	}
	assertFamilyRejected(t, ctx, pool, "deerflow")
	assertFamilyRejected(t, ctx, pool, "zcode")
	assertFamilyAccepted(t, ctx, pool, "kimi")

	// The compatibility row is untouched in both directions.
	var legacy int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM runtime_profile WHERE protocol_family = 'kimi' AND command_name = 'zcode-acp'`,
	).Scan(&legacy); err != nil {
		t.Fatalf("count compatibility profile: %v", err)
	}
	if legacy != 1 {
		t.Errorf("compatibility profile count = %d, want 1", legacy)
	}
}

// assertFamilyAccepted inserts and removes a probe row, so it reports what the
// constraint permits without leaving state behind for the next assertion.
func assertFamilyAccepted(t *testing.T, ctx context.Context, pool *pgxpool.Pool, family string) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		INSERT INTO runtime_profile (display_name, protocol_family, command_name)
		VALUES ('Probe', $1, 'probe')
	`, family); err != nil {
		t.Fatalf("protocol_family %q should be accepted: %v", family, err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM runtime_profile WHERE display_name = 'Probe'`); err != nil {
		t.Fatalf("clean up probe row: %v", err)
	}
}

func assertFamilyRejected(t *testing.T, ctx context.Context, pool *pgxpool.Pool, family string) {
	t.Helper()
	_, err := pool.Exec(ctx, `
		INSERT INTO runtime_profile (display_name, protocol_family, command_name)
		VALUES ('Probe', $1, 'probe')
	`, family)
	if err == nil {
		pool.Exec(ctx, `DELETE FROM runtime_profile WHERE display_name = 'Probe'`)
		t.Fatalf("protocol_family %q was stored, want the CHECK to reject it", family)
	}
	if !strings.Contains(err.Error(), "runtime_profile_protocol_family_check") {
		t.Fatalf("protocol_family %q: expected the CHECK to reject the insert, got: %v", family, err)
	}
}

func assertMigrationLedger(t *testing.T, ctx context.Context, pool *pgxpool.Pool, version string, want bool) {
	t.Helper()
	var exists bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = $1)`, version,
	).Scan(&exists); err != nil {
		t.Fatalf("read schema_migrations: %v", err)
	}
	if exists != want {
		t.Errorf("schema_migrations has %q = %v, want %v", version, exists, want)
	}
}
