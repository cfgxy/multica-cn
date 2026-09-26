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

// pluginV1UpVersions is the V1 plugin migration batch in apply order: the
// lifecycle tables (285-299), private identity (310-312) and the Remote MCP
// plugin plus its OAuth connections (319-326).
var pluginV1UpVersions = []string{
	"285_plugin_lifecycle_v1",
	"286_plugin_identity_key_index",
	"287_plugin_release_version_index",
	"288_plugin_installation_workspace_plugin_index",
	"289_plugin_contribution_key_index",
	"290_plugin_contribution_ordinal_index",
	"291_plugin_grant_revision_index",
	"292_plugin_binding_revision_index",
	"293_plugin_installation_workspace_index",
	"294_plugin_snapshot_execution_v1",
	"295_plugin_artifact_file_index",
	"296_plugin_snapshot_revision_index",
	"297_plugin_execution_task_index",
	"298_plugin_health_index",
	"299_agent_task_plugin_manifest_index",
	"310_workspace_private_plugin_identity",
	"311_plugin_identity_scoped_key_index",
	"312_drop_global_plugin_identity_key_index",
	"319_remote_mcp_plugin_v1",
	"320_plugin_installation_config_revision_index",
	"321_plugin_installation_config_workspace_index",
	"322_plugin_remote_mcp_secret_revision_index",
	"323_plugin_remote_mcp_secret_workspace_index",
	"324_plugin_remote_mcp_one_active_secret_index",
	"325_plugin_remote_mcp_oauth",
	"326_plugin_remote_mcp_oauth_state_expiry_index",
}

// pluginV1RollbackExclusions lists batch members whose down direction cannot
// be made safe in SQL. 312 rebuilds idx_plugin_identity_key with CREATE UNIQUE
// INDEX CONCURRENTLY, which the repository requires to stay a single-statement
// migration, so it cannot carry a relation guard and still fails once 344 has
// removed plugin_identity. Skipping the deleted table there needs a
// down-direction condition in the runner, which is a runner change rather than
// a migration change.
var pluginV1RollbackExclusions = map[string]bool{
	"312_drop_global_plugin_identity_key_index": true,
}

// TestPluginV1RollbackAfterPluginV2Reset covers the database shape where
// migration 344 has already dropped the V1 plugin schema: the batch keeps its
// schema_migrations rows while every object it created is gone. Rolling back
// across it must be a no-op, not "relation ... does not exist".
func TestPluginV1RollbackAfterPluginV2Reset(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	schema, pool := createPluginV1Fixture(t, ctx)
	options := runOptions{
		Direction:             "up",
		SchemaMigrationsTable: schema + ".schema_migrations",
		AdvisoryLockKey:       int64(rand.Uint64()&0x7fffffffffffffff) | 1,
		Hooks:                 hooksForDirection("up"),
		Files:                 realMigrationFiles(t, pluginV1UpVersions, "up"),
	}
	if err := runMigrations(ctx, pool, options); err != nil {
		t.Fatalf("apply plugin V1 batch: %v", err)
	}

	options.Files = realMigrationFiles(t, []string{"344_plugin_v2_reset"}, "up")
	if err := runMigrations(ctx, pool, options); err != nil {
		t.Fatalf("apply plugin V2 reset: %v", err)
	}
	for _, table := range []string{
		"plugin_identity",
		"plugin_contribution",
		"plugin_installation_config",
		"plugin_remote_mcp_secret",
		"plugin_remote_mcp_oauth_state",
	} {
		assertRelationExists(t, ctx, pool, table, false)
	}

	downVersions := []string{"344_plugin_v2_reset"}
	for i := len(pluginV1UpVersions) - 1; i >= 0; i-- {
		if pluginV1RollbackExclusions[pluginV1UpVersions[i]] {
			continue
		}
		downVersions = append(downVersions, pluginV1UpVersions[i])
	}
	options.Direction = "down"
	options.Hooks = hooksForDirection("down")
	options.Files = realMigrationFiles(t, downVersions, "down")
	if err := runMigrations(ctx, pool, options); err != nil {
		t.Fatalf("roll back plugin batch on a database already reset by 344: %v", err)
	}

	for _, version := range downVersions {
		assertMigrationVersionRecorded(t, ctx, pool, schema, version, false)
	}
	assertColumnExists(t, ctx, pool, "agent_task_queue", "plugin_execution_manifest_id", false)
}

// TestPluginRemoteMCPOAuthRollbackRemovesOAuthState covers the other database
// shape: 325 actually created its objects, so its down direction must still
// delete them along with the OAuth installations and their secrets.
func TestPluginRemoteMCPOAuthRollbackRemovesOAuthState(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	schema, pool := createPluginV1Fixture(t, ctx)
	options := runOptions{
		Direction:             "up",
		SchemaMigrationsTable: schema + ".schema_migrations",
		AdvisoryLockKey:       int64(rand.Uint64()&0x7fffffffffffffff) | 1,
		Hooks:                 hooksForDirection("up"),
		Files:                 realMigrationFiles(t, pluginV1UpVersions, "up"),
	}
	if err := runMigrations(ctx, pool, options); err != nil {
		t.Fatalf("apply plugin V1 batch: %v", err)
	}
	assertRelationExists(t, ctx, pool, "plugin_remote_mcp_oauth_state", true)
	assertColumnExists(t, ctx, pool, "plugin_installation_config", "discovered_tools", true)

	const (
		oauthSecretID  = "00000000-0000-0000-0000-0000000000a1"
		bearerSecretID = "00000000-0000-0000-0000-0000000000b1"
	)
	if _, err := pool.Exec(ctx, `
		INSERT INTO plugin_remote_mcp_secret (
			id, workspace_id, installation_id, contribution_id, version, ciphertext
		) VALUES
			($1, $3, $4, $5, 1, '\x01'::bytea),
			($2, $3, $6, $7, 1, '\x02'::bytea)
	`,
		oauthSecretID, bearerSecretID,
		"00000000-0000-0000-0000-000000000001",
		"00000000-0000-0000-0000-000000000002",
		"00000000-0000-0000-0000-000000000003",
		"00000000-0000-0000-0000-000000000004",
		"00000000-0000-0000-0000-000000000005",
	); err != nil {
		t.Fatalf("insert remote MCP secrets: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO plugin_installation_config (
			workspace_id, installation_id, contribution_id, revision,
			endpoint, auth_type, secret_ref
		) VALUES
			($1, $2, $3, 1, 'https://oauth.example/mcp', 'oauth', $5),
			($1, $4, $3, 1, 'https://bearer.example/mcp', 'bearer', $6)
	`,
		"00000000-0000-0000-0000-000000000001",
		"00000000-0000-0000-0000-000000000002",
		"00000000-0000-0000-0000-000000000003",
		"00000000-0000-0000-0000-000000000004",
		oauthSecretID, bearerSecretID,
	); err != nil {
		t.Fatalf("insert installation configs: %v", err)
	}

	options.Direction = "down"
	options.Hooks = hooksForDirection("down")
	options.Files = realMigrationFiles(t, []string{
		"326_plugin_remote_mcp_oauth_state_expiry_index",
		"325_plugin_remote_mcp_oauth",
	}, "down")
	if err := runMigrations(ctx, pool, options); err != nil {
		t.Fatalf("roll back migration 325 on a database that actually built it: %v", err)
	}

	assertRelationExists(t, ctx, pool, "plugin_remote_mcp_oauth_state", false)
	assertColumnExists(t, ctx, pool, "plugin_installation_config", "discovered_tools", false)
	assertColumnExists(t, ctx, pool, "plugin_installation_config", "discovered_schema_digest", false)
	assertMigrationVersionRecorded(t, ctx, pool, schema, "325_plugin_remote_mcp_oauth", false)

	var authTypes []string
	rows, err := pool.Query(ctx, `SELECT auth_type FROM plugin_installation_config ORDER BY auth_type`)
	if err != nil {
		t.Fatalf("read installation configs after rollback: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var authType string
		if err := rows.Scan(&authType); err != nil {
			t.Fatalf("scan installation config: %v", err)
		}
		authTypes = append(authTypes, authType)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate installation configs: %v", err)
	}
	if len(authTypes) != 1 || authTypes[0] != "bearer" {
		t.Fatalf("installation configs after rollback = %v, want only the bearer row", authTypes)
	}

	var remainingSecrets []string
	secretRows, err := pool.Query(ctx, `SELECT id::text FROM plugin_remote_mcp_secret ORDER BY id`)
	if err != nil {
		t.Fatalf("read remote MCP secrets after rollback: %v", err)
	}
	defer secretRows.Close()
	for secretRows.Next() {
		var id string
		if err := secretRows.Scan(&id); err != nil {
			t.Fatalf("scan remote MCP secret: %v", err)
		}
		remainingSecrets = append(remainingSecrets, id)
	}
	if err := secretRows.Err(); err != nil {
		t.Fatalf("iterate remote MCP secrets: %v", err)
	}
	if len(remainingSecrets) != 1 || remainingSecrets[0] != bearerSecretID {
		t.Fatalf("remote MCP secrets after rollback = %v, want only %s", remainingSecrets, bearerSecretID)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO plugin_installation_config (
			workspace_id, installation_id, contribution_id, revision,
			endpoint, auth_type, secret_ref
		) VALUES ($1, $2, $3, 2, 'https://oauth.example/mcp', 'oauth', $4)
	`,
		"00000000-0000-0000-0000-000000000001",
		"00000000-0000-0000-0000-000000000002",
		"00000000-0000-0000-0000-000000000003",
		bearerSecretID,
	)
	if err == nil || !strings.Contains(err.Error(), "plugin_installation_config_auth_type_check") {
		t.Fatalf("insert with auth_type=oauth after rollback error = %v, want auth_type check violation", err)
	}
}

// createPluginV1Fixture builds the private schema the plugin batch expects:
// its own bookkeeping table plus the agent_task_queue migration 294 alters.
func createPluginV1Fixture(t *testing.T, ctx context.Context) (string, *pgxpool.Pool) {
	t.Helper()
	adminPool := openTestPool(t)

	suffix := fmt.Sprintf("%d_%d", time.Now().UnixNano(), rand.Uint32())
	schema := "migrate_plugin_v1_" + suffix
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
	if _, err := pool.Exec(ctx, `
		CREATE TABLE agent_task_queue (
			id UUID PRIMARY KEY,
			agent_id UUID,
			runtime_id UUID,
			retry_of_task_id UUID
		)
	`); err != nil {
		t.Fatalf("create agent_task_queue fixture: %v", err)
	}
	return schema, pool
}

func assertRelationExists(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	relation string,
	want bool,
) {
	t.Helper()
	var exists bool
	if err := pool.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", relation).Scan(&exists); err != nil {
		t.Fatalf("look up relation %s: %v", relation, err)
	}
	if exists != want {
		t.Fatalf("relation %s exists = %v, want %v", relation, exists, want)
	}
}

func assertColumnExists(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	relation string,
	column string,
	want bool,
) {
	t.Helper()
	var exists bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM pg_attribute
			WHERE attrelid = to_regclass($1)
			  AND attname = $2
			  AND NOT attisdropped
			  AND attnum > 0
		)
	`, relation, column).Scan(&exists); err != nil {
		t.Fatalf("look up column %s.%s: %v", relation, column, err)
	}
	if exists != want {
		t.Fatalf("column %s.%s exists = %v, want %v", relation, column, exists, want)
	}
}
