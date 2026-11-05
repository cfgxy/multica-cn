package main

import (
	"context"
	"fmt"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// TestAgentInvocationPermissionMigrationHandlesPreexistingColumn models a
// database whose agent permission_mode exists before migration 130 is recorded.
func TestAgentInvocationPermissionMigrationHandlesPreexistingColumn(t *testing.T) {
	adminPool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	suffix := fmt.Sprintf("%d_%d", time.Now().UnixNano(), rand.Uint32())
	schema := "migrate_ruyi859_permission_collision_" + suffix
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
	for _, statement := range []string{
		`CREATE TABLE schema_migrations (
			version TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		`CREATE TABLE agent (
			id UUID PRIMARY KEY,
			workspace_id UUID NOT NULL,
			visibility TEXT NOT NULL DEFAULT 'private',
			permission_mode TEXT NOT NULL DEFAULT 'private',
			composio_toolkit_allowlist TEXT[]
		)`,
		`CREATE TABLE agent_task_queue (
			originator_user_id UUID
		)`,
	} {
		if _, err := pool.Exec(ctx, statement); err != nil {
			t.Fatalf("create collision fixture: %v", err)
		}
	}

	const version = "130_agent_invocation_permission"
	options := runOptions{
		Direction:             "up",
		Files:                 realMigrationFiles(t, []string{version}, "up"),
		SchemaMigrationsTable: schema + ".schema_migrations",
		AdvisoryLockKey:       int64(rand.Uint64()&0x7fffffffffffffff) | 1,
		Hooks:                 hooksForDirection("up"),
	}
	if err := runMigrations(ctx, pool, options); err != nil {
		t.Fatalf("apply permission migration with preexisting column: %v", err)
	}
	assertMigrationLedger(t, ctx, pool, version, true)

	var targetsTableExists bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('agent_invocation_target') IS NOT NULL`).Scan(&targetsTableExists); err != nil {
		t.Fatalf("read agent_invocation_target: %v", err)
	}
	if !targetsTableExists {
		t.Fatal("permission migration did not create agent_invocation_target")
	}
}

// TestAgentBackpressurePromQLRecoveryMigration models a deployment with a
// recorded 130 that lacks permission_mode and original RUYI-859 stems that
// lack PromQL fields. Those stems are already in its ledger, so only the
// renamed same-number recovery stem can bring the persisted schema forward.
func TestAgentBackpressurePromQLRecoveryMigration(t *testing.T) {
	adminPool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	suffix := fmt.Sprintf("%d_%d", time.Now().UnixNano(), rand.Uint32())
	schema := "migrate_ruyi859_promql_recovery_" + suffix
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
	for _, statement := range []string{
		`CREATE TABLE schema_migrations (
			version TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		`CREATE TABLE agent (
			id UUID PRIMARY KEY
		)`,
		`CREATE TABLE agent_backpressure_policy (
			workspace_id UUID NOT NULL,
			agent_id UUID NOT NULL,
			PRIMARY KEY (workspace_id, agent_id)
		)`,
		`CREATE TABLE agent_suppression_event (
			id UUID PRIMARY KEY,
			workspace_id UUID NOT NULL,
			agent_id UUID NOT NULL,
			occurred_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		`INSERT INTO schema_migrations (version) VALUES
			('130_agent_invocation_permission'),
			('982_agent_backpressure_policy'),
			('983_agent_suppression_event_index'),
			('985_scheduling_pause_system_source')`,
	} {
		if _, err := pool.Exec(ctx, statement); err != nil {
			t.Fatalf("create historical fixture: %v", err)
		}
	}

	const recoveryVersion = "982_scheduling_pause_system_source_promql_recovery"
	options := runOptions{
		Direction:             "up",
		Files:                 realMigrationFiles(t, []string{recoveryVersion}, "up"),
		SchemaMigrationsTable: schema + ".schema_migrations",
		AdvisoryLockKey:       int64(rand.Uint64()&0x7fffffffffffffff) | 1,
		Hooks:                 hooksForDirection("up"),
	}
	if err := runMigrations(ctx, pool, options); err != nil {
		t.Fatalf("apply PromQL recovery migration: %v", err)
	}

	for _, column := range []struct {
		table string
		name  string
	}{
		{"agent", "permission_mode"},
		{"agent_backpressure_policy", "promql_rule_ids"},
		{"agent_suppression_event", "source"},
		{"agent_suppression_event", "rule_id"},
		{"agent_suppression_event", "rule_name"},
		{"agent_suppression_event", "reason"},
	} {
		var exists bool
		if err := pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM information_schema.columns
				WHERE table_schema = current_schema()
				  AND table_name = $1 AND column_name = $2
			)
		`, column.table, column.name).Scan(&exists); err != nil {
			t.Fatalf("read %s.%s: %v", column.table, column.name, err)
		}
		if !exists {
			t.Errorf("recovery did not add %s.%s", column.table, column.name)
		}
	}
	var pauseTableExists bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('agent_promql_rule_pause') IS NOT NULL`).Scan(&pauseTableExists); err != nil {
		t.Fatalf("read PromQL pause table: %v", err)
	}
	if !pauseTableExists {
		t.Fatal("recovery did not create agent_promql_rule_pause")
	}
	assertMigrationLedger(t, ctx, pool, recoveryVersion, true)

	if err := runMigrations(ctx, pool, options); err != nil {
		t.Fatalf("repeat PromQL recovery migration: %v", err)
	}
}

// TestAgentBackpressureMigrationRenumberPreservesLedger proves the RUYI-859
// consolidation can repair a database that recorded all pre-release migration
// identities. Every renamed file executes idempotently before its ledger row
// moves, preserving the original applied_at timestamp.
func TestAgentBackpressureMigrationRenumberPreservesLedger(t *testing.T) {
	adminPool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	suffix := fmt.Sprintf("%d_%d", time.Now().UnixNano(), rand.Uint32())
	schema := "migrate_ruyi859_renumber_" + suffix
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
	for _, statement := range []string{
		`CREATE TABLE schema_migrations (
			version TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		`CREATE TABLE agent (
			id UUID PRIMARY KEY
		)`,
		`CREATE TABLE agent_backpressure_policy (
			workspace_id UUID NOT NULL,
			agent_id UUID NOT NULL,
			PRIMARY KEY (workspace_id, agent_id)
		)`,
		`CREATE TABLE agent_suppression_event (
			id UUID PRIMARY KEY,
			workspace_id UUID NOT NULL,
			agent_id UUID NOT NULL,
			occurred_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		`CREATE INDEX idx_agent_suppression_event_agent
			ON agent_suppression_event (agent_id, occurred_at DESC)`,
		`CREATE TABLE scheduling_pause (
			created_by UUID NOT NULL,
			reason TEXT NOT NULL
		)`,
	} {
		if _, err := pool.Exec(ctx, statement); err != nil {
			t.Fatalf("create historical fixture: %v", err)
		}
	}

	versions := []struct {
		legacy  string
		target  string
		applied time.Time
	}{
		{
			legacy:  "983_agent_suppression_event_index",
			target:  "982_agent_suppression_event_index",
			applied: time.Date(2026, time.October, 24, 1, 2, 3, 0, time.UTC),
		},
		{
			legacy:  "985_scheduling_pause_system_source",
			target:  "982_scheduling_pause_system_source",
			applied: time.Date(2026, time.October, 24, 1, 2, 4, 0, time.UTC),
		},
		{
			legacy:  "985_scheduling_pause_system_source_promql_recovery",
			target:  "982_scheduling_pause_system_source_promql_recovery",
			applied: time.Date(2026, time.October, 24, 1, 2, 5, 0, time.UTC),
		},
	}
	for _, version := range versions {
		if _, err := pool.Exec(ctx, `INSERT INTO schema_migrations (version, applied_at) VALUES ($1, $2)`, version.legacy, version.applied); err != nil {
			t.Fatalf("record historical migration %s: %v", version.legacy, err)
		}
	}

	options := runOptions{
		Direction:             "up",
		Files:                 realMigrationFiles(t, []string{versions[0].target, versions[1].target, versions[2].target}, "up"),
		SchemaMigrationsTable: schema + ".schema_migrations",
		AdvisoryLockKey:       int64(rand.Uint64()&0x7fffffffffffffff) | 1,
		Hooks:                 hooksForDirection("up"),
	}
	if err := runMigrations(ctx, pool, options); err != nil {
		t.Fatalf("apply consolidated RUYI-859 migrations: %v", err)
	}

	for _, version := range versions {
		var got time.Time
		if err := pool.QueryRow(ctx, `SELECT applied_at FROM schema_migrations WHERE version = $1`, version.target).Scan(&got); err != nil {
			t.Errorf("read renamed migration %s: %v", version.target, err)
			continue
		}
		if !got.Equal(version.applied) {
			t.Errorf("renamed migration %s applied_at = %s, want %s", version.target, got, version.applied)
		}
		var legacyRows int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM schema_migrations WHERE version = $1`, version.legacy).Scan(&legacyRows); err != nil {
			t.Errorf("count legacy migration %s: %v", version.legacy, err)
		} else if legacyRows != 0 {
			t.Errorf("legacy migration %s remained after consolidation", version.legacy)
		}
	}
}
