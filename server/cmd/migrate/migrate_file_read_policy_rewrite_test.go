package main

import (
	"context"
	"fmt"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestRewrite977UpgradesAlreadyAppliedFileAccessSchema(t *testing.T) {
	adminPool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	schema := fmt.Sprintf("migrate_file_policy_%d_%d", time.Now().UnixNano(), rand.Uint32())
	schemaIdent := pgx.Identifier{schema}.Sanitize()
	if _, err := adminPool.Exec(ctx, "CREATE SCHEMA "+schemaIdent); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = adminPool.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+schemaIdent+" CASCADE")
	})
	pool := openTestPoolWithSearchPath(t, schema)
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, `
		CREATE TABLE schema_migrations (version TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now());
		CREATE TABLE daemon_file_operation (id UUID PRIMARY KEY, status TEXT NOT NULL);
		CREATE TABLE mcp_file_access_instance_binding (
			instance_id TEXT NOT NULL, workspace_id UUID NOT NULL, target_id TEXT NOT NULL,
			enabled BOOLEAN NOT NULL DEFAULT FALSE, updated_by UUID NOT NULL, updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
		);
		CREATE TABLE mcp_pre_auth_rejection (
			invocation_id UUID NOT NULL, instance_id TEXT NOT NULL, reason_code TEXT NOT NULL,
			file_tool_name TEXT, recorded_at TIMESTAMPTZ NOT NULL DEFAULT now(), expires_at TIMESTAMPTZ NOT NULL
		);
		INSERT INTO daemon_file_operation (id, status) VALUES ('11111111-1111-1111-1111-111111111111', 'succeeded');
		INSERT INTO mcp_file_access_instance_binding (instance_id, workspace_id, target_id, enabled, updated_by)
		VALUES ('fixture', '22222222-2222-2222-2222-222222222222', 'target', TRUE, '33333333-3333-3333-3333-333333333333');
		INSERT INTO mcp_pre_auth_rejection (invocation_id, instance_id, reason_code, expires_at)
		VALUES ('44444444-4444-4444-4444-444444444444', 'fixture', 'unknown_tool', now() + interval '1 day');
		INSERT INTO schema_migrations (version) VALUES ('977_mcp_file_access');
	`); err != nil {
		t.Fatalf("create previous 977 schema: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM schema_migrations WHERE version = '977_mcp_file_access'`); err != nil {
		t.Fatalf("reset only rewritten migration ledger row: %v", err)
	}
	options := runOptions{
		Direction:             "up",
		Files:                 realMigrationFiles(t, []string{"977_mcp_file_access"}, "up"),
		SchemaMigrationsTable: schema + ".schema_migrations",
		AdvisoryLockKey:       int64(rand.Uint64()&0x7fffffffffffffff) | 1,
	}
	if err := runMigrations(ctx, pool, options); err != nil {
		t.Fatalf("apply rewritten 977 to existing schema: %v", err)
	}
	assertFilePolicyUpgradeState(t, ctx, pool)
	if _, err := pool.Exec(ctx, `DELETE FROM schema_migrations WHERE version = '977_mcp_file_access'`); err != nil {
		t.Fatalf("prepare idempotent repeat: %v", err)
	}
	if err := runMigrations(ctx, pool, options); err != nil {
		t.Fatalf("repeat rewritten 977: %v", err)
	}
	assertFilePolicyUpgradeState(t, ctx, pool)
}

func assertFilePolicyUpgradeState(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	var operations, bindings, rejections, policies, defaultRules int
	var version int64
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM daemon_file_operation`).Scan(&operations); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM mcp_file_access_instance_binding`).Scan(&bindings); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM mcp_pre_auth_rejection`).Scan(&rejections); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*), COALESCE(max(version), 0), COALESCE(max(jsonb_array_length(rules)), 0) FROM mcp_file_read_policy`).Scan(&policies, &version, &defaultRules); err != nil {
		t.Fatal(err)
	}
	if operations != 1 || bindings != 1 || rejections != 1 || policies != 1 || version != 1 || defaultRules < 10 {
		t.Fatalf("upgrade state operations=%d bindings=%d rejections=%d policies=%d version=%d rules=%d", operations, bindings, rejections, policies, version, defaultRules)
	}
	var invocationColumn bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = 'daemon_file_operation' AND column_name = 'invocation_id')`).Scan(&invocationColumn); err != nil {
		t.Fatal(err)
	}
	if !invocationColumn {
		t.Fatal("rewritten 977 did not add invocation_id")
	}
}
