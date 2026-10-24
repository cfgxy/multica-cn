package migrations

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestTaskUsageModelCallCountMigrationMarksExistingBucketsUnknown(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("integration test requires Postgres at DATABASE_URL")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	adminPool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("connect to Postgres: %v", err)
	}
	schema := fmt.Sprintf("task_usage_model_calls_%d_%d", time.Now().UnixNano(), rand.Uint32())
	schemaIdent := pgx.Identifier{schema}.Sanitize()
	if _, err := adminPool.Exec(ctx, "CREATE SCHEMA "+schemaIdent); err != nil {
		t.Fatalf("create isolated migration schema: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if _, err := adminPool.Exec(cleanupCtx, "DROP SCHEMA IF EXISTS "+schemaIdent+" CASCADE"); err != nil {
			t.Errorf("drop schema %s: %v", schema, err)
		}
		adminPool.Close()
	})

	config, err := pgxpool.ParseConfig(dbURL)
	if err != nil {
		t.Fatalf("parse Postgres config: %v", err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatalf("connect to isolated schema: %v", err)
	}
	defer pool.Close()

	if _, err := pool.Exec(ctx, `
		CREATE TABLE task_usage (task_id UUID);
		CREATE TABLE task_usage_hourly (event_count BIGINT NOT NULL);
		INSERT INTO task_usage_hourly (event_count) VALUES (3), (0);
	`); err != nil {
		t.Fatalf("create pre-migration schema and buckets: %v", err)
	}

	applyMigrationFile(t, ctx, pool, "987_task_usage_model_call_count.up.sql")
	// Concurrent indexes must remain a one-statement migration. Run the
	// companion migration separately to prove the table/index ordering is viable.
	applyMigrationFile(t, ctx, pool, "987_task_usage_model_call_evidence_index.up.sql")

	var knownZero, unknownCount int64
	if err := pool.QueryRow(ctx, `
		SELECT model_call_count, model_call_unknown_count
		FROM task_usage_hourly
		WHERE event_count = 3
	`).Scan(&knownZero, &unknownCount); err != nil {
		t.Fatalf("read migrated historical bucket: %v", err)
	}
	if knownZero != 0 || unknownCount != 3 {
		t.Fatalf("historical bucket calls = known %d / unknown %d, want 0 / 3", knownZero, unknownCount)
	}

	var emptyKnown, emptyUnknown int64
	if err := pool.QueryRow(ctx, `
		SELECT model_call_count, model_call_unknown_count
		FROM task_usage_hourly
		WHERE event_count = 0
	`).Scan(&emptyKnown, &emptyUnknown); err != nil {
		t.Fatalf("read migrated empty bucket: %v", err)
	}
	if emptyKnown != 0 || emptyUnknown != 0 {
		t.Fatalf("empty bucket calls = known %d / unknown %d, want 0 / 0", emptyKnown, emptyUnknown)
	}
}
