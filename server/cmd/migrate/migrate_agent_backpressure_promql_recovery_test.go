package main

import (
	"context"
	"fmt"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// TestAgentBackpressurePromQLRecoveryMigration models a deployment that ran
// the original stage-A migrations before their files gained PromQL fields.
// Those stems are already in its ledger, so only the same-number recovery
// stem can bring the persisted schema forward.
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
		`CREATE TABLE agent_backpressure_policy (
			workspace_id UUID NOT NULL,
			agent_id UUID NOT NULL,
			PRIMARY KEY (workspace_id, agent_id)
		)`,
		`CREATE TABLE agent_suppression_event (
			id UUID PRIMARY KEY,
			workspace_id UUID NOT NULL,
			agent_id UUID NOT NULL
		)`,
		`INSERT INTO schema_migrations (version) VALUES
			('982_agent_backpressure_policy'),
			('983_agent_suppression_event_index'),
			('985_scheduling_pause_system_source')`,
	} {
		if _, err := pool.Exec(ctx, statement); err != nil {
			t.Fatalf("create historical fixture: %v", err)
		}
	}

	const recoveryVersion = "985_scheduling_pause_system_source_promql_recovery"
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
