package migrations

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const pendingTaskSlotRepairSchema = "pending_task_slot_repair_migration_test"

func TestPendingTaskSlotRepairMigrationRestoresMissingIndex(t *testing.T) {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		t.Skip("integration test requires Postgres at DATABASE_URL")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect to Postgres: %v", err)
	}
	defer pool.Close()

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire Postgres connection: %v", err)
	}
	defer conn.Release()

	cleanup := func() {
		_, _ = pool.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+pendingTaskSlotRepairSchema+" CASCADE")
	}
	cleanup()
	t.Cleanup(cleanup)
	if _, err := conn.Exec(ctx, "CREATE SCHEMA "+pendingTaskSlotRepairSchema); err != nil {
		t.Fatalf("create isolated migration schema: %v", err)
	}
	if _, err := conn.Exec(ctx, `SELECT set_config('search_path', $1, false)`, pendingTaskSlotRepairSchema); err != nil {
		t.Fatalf("set isolated migration search path: %v", err)
	}
	if _, err := conn.Exec(ctx, `
		CREATE TABLE agent_task_queue (
			id UUID PRIMARY KEY,
			issue_id UUID,
			agent_id UUID,
			status TEXT NOT NULL,
			context JSONB NOT NULL DEFAULT '{}'::jsonb
		)
	`); err != nil {
		t.Fatalf("create pre-repair queue table: %v", err)
	}

	applyMigrationFile(t, ctx, conn.Conn(), "1011_agent_task_queue_pending_slot_repair.up.sql")

	const issueID = "00000000-0000-0000-0000-000000000001"
	const agentID = "00000000-0000-0000-0000-000000000002"
	if _, err := conn.Exec(ctx, `
		INSERT INTO agent_task_queue (id, issue_id, agent_id, status)
		VALUES ('00000000-0000-0000-0000-000000000003', $1, $2, 'queued')
	`, issueID, agentID); err != nil {
		t.Fatalf("insert first pending task: %v", err)
	}
	if _, err := conn.Exec(ctx, `
		INSERT INTO agent_task_queue (id, issue_id, agent_id, status)
		VALUES ('00000000-0000-0000-0000-000000000004', $1, $2, 'dispatched')
	`, issueID, agentID); !isUniqueViolation(err) {
		t.Fatalf("second queued/dispatched slot: got %v, want unique violation", err)
	}
	if _, err := conn.Exec(ctx, `
		INSERT INTO agent_task_queue (id, issue_id, agent_id, status, context)
		VALUES ('00000000-0000-0000-0000-000000000005', $1, $2, 'deferred', '{"channel_issue_media_pending":true}')
	`, issueID, agentID); !isUniqueViolation(err) {
		t.Fatalf("channel-media deferred slot: got %v, want unique violation", err)
	}

	applyMigrationFile(t, ctx, conn.Conn(), "1011_agent_task_queue_pending_slot_repair.down.sql")
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
