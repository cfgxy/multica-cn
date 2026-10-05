package main

import (
	"context"
	"fmt"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// TestLiveSessionMigrationRoundTrip exercises migration 926 in both
// directions inside a private schema (RUYI-425 stage 3, design §3.5). Up
// must create the live_session table with the full lifecycle column set
// (status CHECK, session_handle, context_snapshot, transcript, started/ended
// timestamps) plus its lookup indexes; a row inserted through the default
// columns must come out with the gateway's expected baseline values. Down
// must drop the table and nothing else — the table is pure additive log
// storage with no other schema object referencing it.
func TestLiveSessionMigrationRoundTrip(t *testing.T) {
	adminPool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	suffix := fmt.Sprintf("%d_%d", time.Now().UnixNano(), rand.Uint32())
	schema := "migrate_live_session_" + suffix
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

	// The only production table 926's up actually references: workspace,
	// for the ON DELETE CASCADE foreign key. Everything else live_session
	// stores (agent/instance/user ids) is deliberately FK-free so history
	// rows survive reference deletion.
	if _, err := pool.Exec(ctx, `
		CREATE TABLE workspace (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid()
		)
	`); err != nil {
		t.Fatalf("create workspace fixture: %v", err)
	}

	const version = "926_live_session"
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

	assertRelationExists(t, ctx, pool, "live_session", false)

	if err := run("up"); err != nil {
		t.Fatalf("apply migration 926: %v", err)
	}
	assertMigrationLedger(t, ctx, pool, version, true)

	// Gateway-shaped insert through the default columns: status opens at
	// 'active', the transcript is an empty JSON array, the resumption
	// handle starts empty, and ended_at is NULL until the relay finishes.
	var wsID string
	if err := pool.QueryRow(ctx, `INSERT INTO workspace DEFAULT VALUES RETURNING id::text`).Scan(&wsID); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	var (
		sessionID         string
		status            string
		model             string
		sessionHandle     string
		transcript        string
		endedAt           *time.Time
		agentID           = "00000000-0000-0000-0000-0000000000a1"
		runtimeInstanceID = "00000000-0000-0000-0000-0000000000a2"
		userID            = "00000000-0000-0000-0000-0000000000a3"
	)
	if err := pool.QueryRow(ctx, `
		INSERT INTO live_session (workspace_id, agent_id, runtime_instance_id, user_id, model, context_snapshot)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, 'models/gemini-3.8-live', '{"model_hash":"h1"}'::jsonb)
		RETURNING id::text, status, model, session_handle, transcript::text, ended_at
	`, wsID, agentID, runtimeInstanceID, userID).Scan(&sessionID, &status, &model, &sessionHandle, &transcript, &endedAt); err != nil {
		t.Fatalf("insert live session: %v", err)
	}
	if status != "active" {
		t.Errorf("live_session.status = %q, want 'active' (gateway default)", status)
	}
	if sessionHandle != "" {
		t.Errorf("live_session.session_handle = %q, want empty (provider has not issued one yet)", sessionHandle)
	}
	if transcript != "[]" {
		t.Errorf("live_session.transcript = %s, want '[]'", transcript)
	}
	if endedAt != nil {
		t.Errorf("live_session.ended_at = %v, want NULL before session end", endedAt)
	}

	// The status CHECK admits exactly the two lifecycle states.
	if _, err := pool.Exec(ctx, `
		INSERT INTO live_session (workspace_id, agent_id, runtime_instance_id, user_id, status)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, 'connecting')
	`, wsID, agentID, runtimeInstanceID, userID); err == nil {
		t.Fatal("inserting status 'connecting' succeeded, want CHECK to reject it")
	}

	// Terminal transition shape: ended + ended_at + final transcript in one
	// statement (the relay's EndLiveSession write).
	if _, err := pool.Exec(ctx, `
		UPDATE live_session
		SET status = 'ended', ended_at = now(), transcript = '[{"role":"user","text":"hi"}]'::jsonb
		WHERE id = $1::uuid
	`, sessionID); err != nil {
		t.Fatalf("end live session: %v", err)
	}

	if err := run("down"); err != nil {
		t.Fatalf("roll back migration 926: %v", err)
	}
	assertMigrationLedger(t, ctx, pool, version, false)
	assertRelationExists(t, ctx, pool, "live_session", false)

	// The workspace fixture the FK pointed at must be untouched by the down.
	var wsCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM workspace`).Scan(&wsCount); err != nil {
		t.Fatalf("count workspace after down: %v", err)
	}
	if wsCount != 1 {
		t.Errorf("workspace rows after down = %d, want 1 (down must drop only live_session)", wsCount)
	}
}
