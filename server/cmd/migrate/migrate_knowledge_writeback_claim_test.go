package main

import (
	"context"
	"fmt"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestKnowledgeWritebackClaimMigrationRoundTripWithActiveClaim(t *testing.T) {
	pool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	schema := fmt.Sprintf("migrate_knowledge_claim_%d_%d", time.Now().UnixNano(), rand.Uint32())
	ident := pgx.Identifier{schema}.Sanitize()
	if _, err := pool.Exec(ctx, "CREATE SCHEMA "+ident); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if _, err := pool.Exec(cleanupCtx, "DROP SCHEMA IF EXISTS "+ident+" CASCADE"); err != nil {
			t.Logf("drop schema %s: %v", schema, err)
		}
	})

	schemaPool := openTestPoolWithSearchPath(t, schema)
	if _, err := schemaPool.Exec(ctx, `
CREATE TABLE knowledge_entry (
	    id UUID PRIMARY KEY,
	    writeback_state TEXT NOT NULL DEFAULT 'not_requested',
	    writeback_error TEXT NOT NULL DEFAULT '',
	    CONSTRAINT knowledge_entry_writeback_state_check
	        CHECK (writeback_state IN ('not_requested', 'queued', 'succeeded', 'failed', 'cancelled'))
);
CREATE TABLE knowledge_lifecycle_event (
    id UUID PRIMARY KEY,
    entry_id UUID NOT NULL REFERENCES knowledge_entry(id),
    writeback_state TEXT NOT NULL DEFAULT 'not_requested',
    writeback_error TEXT NOT NULL DEFAULT '',
    completed_at TIMESTAMPTZ,
    CONSTRAINT knowledge_lifecycle_event_writeback_state_check
        CHECK (writeback_state IN ('not_requested', 'queued', 'succeeded', 'failed', 'cancelled'))
);`); err != nil {
		t.Fatalf("create knowledge claim fixture: %v", err)
	}

	upOptions := runOptions{
		Direction:             "up",
		SchemaMigrationsTable: schema + ".schema_migrations",
		AdvisoryLockKey:       int64(rand.Uint64()&0x7fffffffffffffff) | 1,
		Hooks:                 hooksForDirection("up"),
		Conditions:            conditionsForDirection("up"),
		Files: realMigrationFiles(t, []string{
			"999_knowledge_lifecycle_writeback_claim",
			"999_knowledge_lifecycle_writeback_claim_index",
		}, "up"),
	}
	if err := runMigrations(ctx, schemaPool, upOptions); err != nil {
		t.Fatalf("apply knowledge claim migrations: %v", err)
	}

	entryID := "00000000-0000-0000-0000-000000000001"
	eventID := "00000000-0000-0000-0000-000000000002"
	if _, err := schemaPool.Exec(ctx,
		`INSERT INTO knowledge_entry (id, writeback_state) VALUES ($1, 'in_progress')`, entryID); err != nil {
		t.Fatalf("insert active claim: %v", err)
	}
	if _, err := schemaPool.Exec(ctx, `
	INSERT INTO knowledge_lifecycle_event
	    (id, entry_id, writeback_state, writeback_claimed_by, writeback_claimed_at, writeback_claim_token)
	VALUES ($1, $2, 'in_progress', 'daemon-under-test', now(), gen_random_uuid())`, eventID, entryID); err != nil {
		t.Fatalf("insert active claim event: %v", err)
	}

	downOptions := upOptions
	downOptions.Direction = "down"
	downOptions.Hooks = hooksForDirection("down")
	downOptions.Conditions = conditionsForDirection("down")
	downOptions.Files = realMigrationFiles(t, []string{
		"999_knowledge_lifecycle_writeback_claim_index",
		"999_knowledge_lifecycle_writeback_claim",
	}, "down")
	if err := runMigrations(ctx, schemaPool, downOptions); err != nil {
		t.Fatalf("roll back knowledge claim migrations with active claim: %v", err)
	}

	var eventState, entryState string
	if err := schemaPool.QueryRow(ctx, `SELECT writeback_state FROM knowledge_lifecycle_event WHERE id = $1`, eventID).Scan(&eventState); err != nil {
		t.Fatalf("read rolled back event state: %v", err)
	}
	if err := schemaPool.QueryRow(ctx, `SELECT writeback_state FROM knowledge_entry WHERE id = $1`, entryID).Scan(&entryState); err != nil {
		t.Fatalf("read rolled back entry state: %v", err)
	}
	if eventState != "failed" || entryState != "failed" {
		t.Fatalf("active claim rollback states = event %q entry %q, want failed/failed", eventState, entryState)
	}

	var claimColumns int
	if err := schemaPool.QueryRow(ctx, `
SELECT count(*) FROM information_schema.columns
WHERE table_schema = current_schema() AND table_name = 'knowledge_lifecycle_event'
  AND column_name IN ('writeback_claimed_by', 'writeback_claimed_at', 'writeback_claim_token')`).Scan(&claimColumns); err != nil {
		t.Fatalf("inspect rolled back claim columns: %v", err)
	}
	if claimColumns != 0 {
		t.Fatalf("claim columns remain after rollback: %d", claimColumns)
	}
}
