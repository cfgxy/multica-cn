package main

import (
	"context"
	"math/rand/v2"
	"slices"
	"testing"
	"time"
)

func TestRuntimeModelConfigurationMigrationRoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	schema := createScratchSchema(t, ctx, openTestPool(t), "migrate_runtime_models_")
	pool := openTestPoolWithSearchPath(t, schema)
	versions := []string{"947_computer_management", "953_model_key_directory_name", "954_user_model_key_grant_position", "955_model_key_source_identity", "956_model_key_directory_entry_identity", "957_user_model_key_grant_identity", "958_runtime_config_template_identity", "959_computer_runtime_model_config_identity"}
	options := runOptions{SchemaMigrationsTable: schema + ".schema_migrations", AdvisoryLockKey: int64(rand.Uint64()&0x7fffffffffffffff) | 1}
	for _, direction := range []string{"up", "down", "up"} {
		options.Direction = direction
		ordered := slices.Clone(versions)
		if direction == "down" {
			slices.Reverse(ordered)
		}
		options.Files = realMigrationFiles(t, ordered, direction)
		options.Hooks = hooksForDirection(direction)
		if err := runMigrations(ctx, pool, options); err != nil {
			t.Fatalf("%s model configuration migrations: %v", direction, err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO model_key_source(id,base_url,directory_token_ciphertext,updated_by) VALUES(1,'https://example.test','fixture-ciphertext','10000000-0000-0000-0000-000000000001') ON CONFLICT(id) DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_model_key_grant(user_id,source_id,upstream_id,position,api_key_name) VALUES('10000000-0000-0000-0000-000000000001',1,'9007199254740993',0,'example') ON CONFLICT(user_id,source_id,upstream_id) DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO computer_runtime_model_config(binding_id,runtime_kind,file_manifest) VALUES('10000000-0000-0000-0000-000000000002','codex','{}') ON CONFLICT(binding_id,runtime_kind) DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	var invalid int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND NOT i.indisvalid`, schema).Scan(&invalid); err != nil || invalid != 0 {
		t.Fatalf("invalid indexes: %d / %v", invalid, err)
	}
}
