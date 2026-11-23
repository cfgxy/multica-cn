package migrations

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const pendingTaskSlotRepairSchema = "pending_task_slot_repair_migration_test"

const (
	legacyIssueID = "00000000-0000-0000-0000-000000000001"
	legacyAgentID = "00000000-0000-0000-0000-000000000002"
)

// TestCreateRetryTaskEquivalentSurvivesLegacyV1Index reproduces the incident
// schema: v1 is present but its queued/dispatched predicate cannot infer the
// later v2 conflict target. The old statement must fail with 42P10; the query
// shape used by CreateRetryTask now commits the parent failure and yields an
// already-held pending slot instead.
func TestCreateRetryTaskEquivalentSurvivesLegacyV1Index(t *testing.T) {
	ctx, pool, conn := openPendingTaskRepairSchema(t)
	defer pool.Close()
	defer conn.Release()

	if _, err := conn.Exec(ctx, `
		CREATE UNIQUE INDEX idx_one_pending_task_per_issue_agent
		ON agent_task_queue (issue_id, agent_id)
		WHERE status IN ('queued', 'dispatched')
	`); err != nil {
		t.Fatalf("create legacy v1 index: %v", err)
	}

	const parentID = "00000000-0000-0000-0000-000000000003"
	insertTask(t, ctx, conn, parentID, legacyIssueID, legacyAgentID, "running")

	_, err := conn.Exec(ctx, `
		INSERT INTO agent_task_queue (id, issue_id, agent_id, status)
		SELECT '00000000-0000-0000-0000-000000000004', issue_id, agent_id, 'queued'
		FROM agent_task_queue WHERE id = $1
		ON CONFLICT (issue_id, agent_id) WHERE status IN ('queued', 'dispatched')
		   OR (status = 'deferred' AND context->>'channel_issue_media_pending' = 'true')
		DO NOTHING
	`, parentID)
	if pgCode(err) != "42P10" {
		t.Fatalf("legacy v2 conflict target against v1-only schema: got %v, want SQLSTATE 42P10", err)
	}

	if created := failAndCreateRetry(t, ctx, conn, parentID, "00000000-0000-0000-0000-000000000004"); created != 1 {
		t.Fatalf("retry rows on v1-only schema = %d, want 1", created)
	}
	assertTaskStatus(t, ctx, conn, parentID, "failed")
	assertTaskStatus(t, ctx, conn, "00000000-0000-0000-0000-000000000004", "queued")

	const (
		occupiedIssueID  = "00000000-0000-0000-0000-000000000008"
		occupiedParentID = "00000000-0000-0000-0000-000000000005"
		successorID      = "00000000-0000-0000-0000-000000000006"
	)
	insertTask(t, ctx, conn, occupiedParentID, occupiedIssueID, legacyAgentID, "running")
	insertTask(t, ctx, conn, successorID, occupiedIssueID, legacyAgentID, "queued")
	if created := failAndCreateRetry(t, ctx, conn, occupiedParentID, "00000000-0000-0000-0000-000000000007"); created != 0 {
		t.Fatalf("retry rows with an occupied legacy slot = %d, want 0", created)
	}
	assertTaskStatus(t, ctx, conn, occupiedParentID, "failed")
	assertTaskStatus(t, ctx, conn, successorID, "queued")
}

func TestPendingTaskSlotRepairMigrationRestoresMissingIndex(t *testing.T) {
	ctx, pool, conn := openPendingTaskRepairSchema(t)
	defer pool.Close()
	defer conn.Release()

	applyMigrationFile(t, ctx, conn.Conn(), "1011_agent_task_queue_pending_slot_repair.up.sql")
	insertTask(t, ctx, conn, "00000000-0000-0000-0000-000000000003", legacyIssueID, legacyAgentID, "queued")
	assertUniqueViolation(t, ctx, conn, "00000000-0000-0000-0000-000000000004", "dispatched", "{}")
	assertUniqueViolation(t, ctx, conn, "00000000-0000-0000-0000-000000000005", "deferred", `{"channel_issue_media_pending":true}`)

	applyMigrationFile(t, ctx, conn.Conn(), "1011_agent_task_queue_pending_slot_repair.down.sql")
}

// TestPendingTaskSlotRepairMigrationRefusesInvalidV3 proves that an interrupted
// concurrent v3 build cannot be silently marked repaired. The duplicate data
// makes PostgreSQL retain an indisvalid=false v3 index; migration 1011 then
// fails on that name, so the runner cannot record it in schema_migrations.
func TestPendingTaskSlotRepairMigrationRefusesInvalidV3(t *testing.T) {
	ctx, pool, conn := openPendingTaskRepairSchema(t)
	defer pool.Close()
	defer conn.Release()

	insertTask(t, ctx, conn, "00000000-0000-0000-0000-000000000003", legacyIssueID, legacyAgentID, "queued")
	insertTask(t, ctx, conn, "00000000-0000-0000-0000-000000000004", legacyIssueID, legacyAgentID, "queued")
	if _, err := conn.Exec(ctx, `
		CREATE UNIQUE INDEX CONCURRENTLY idx_one_pending_task_per_issue_agent_v3
		ON agent_task_queue (issue_id, agent_id)
		WHERE status IN ('queued', 'dispatched')
		   OR (status = 'deferred' AND context->>'channel_issue_media_pending' = 'true')
	`); err == nil {
		t.Fatal("create duplicate v3 index: expected unique-index build failure")
	}
	var valid bool
	if err := conn.QueryRow(ctx, `
		SELECT i.indisvalid
		FROM pg_index i
		WHERE i.indexrelid = 'idx_one_pending_task_per_issue_agent_v3'::regclass
	`).Scan(&valid); err != nil {
		t.Fatalf("read invalid v3 index: %v", err)
	}
	if valid {
		t.Fatal("failed concurrent v3 build unexpectedly produced a valid index")
	}

	_, err := conn.Exec(ctx, readMigrationFile(t, "1011_agent_task_queue_pending_slot_repair.up.sql"))
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("migration with invalid v3 must fail for explicit repair, got %v", err)
	}
}

func openPendingTaskRepairSchema(t *testing.T) (context.Context, *pgxpool.Pool, *pgxpool.Conn) {
	t.Helper()
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		t.Skip("integration test requires Postgres at DATABASE_URL")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect to Postgres: %v", err)
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		pool.Close()
		t.Fatalf("acquire Postgres connection: %v", err)
	}
	cleanup := func() {
		_, _ = pool.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+pendingTaskSlotRepairSchema+" CASCADE")
	}
	cleanup()
	t.Cleanup(cleanup)
	if _, err := conn.Exec(ctx, "CREATE SCHEMA "+pendingTaskSlotRepairSchema); err != nil {
		conn.Release()
		pool.Close()
		t.Fatalf("create isolated migration schema: %v", err)
	}
	if _, err := conn.Exec(ctx, `SELECT set_config('search_path', $1, false)`, pendingTaskSlotRepairSchema); err != nil {
		conn.Release()
		pool.Close()
		t.Fatalf("set isolated migration search path: %v", err)
	}
	if _, err := conn.Exec(ctx, `
		CREATE TABLE agent_task_queue (
			id UUID PRIMARY KEY,
			issue_id UUID NOT NULL,
			agent_id UUID NOT NULL,
			status TEXT NOT NULL,
			failure_reason TEXT,
			context JSONB NOT NULL DEFAULT '{}'::jsonb
		)
	`); err != nil {
		conn.Release()
		pool.Close()
		t.Fatalf("create isolated task queue: %v", err)
	}
	return ctx, pool, conn
}

func insertTask(t *testing.T, ctx context.Context, conn *pgxpool.Conn, id, issueID, agentID, status string) {
	t.Helper()
	if _, err := conn.Exec(ctx, `
		INSERT INTO agent_task_queue (id, issue_id, agent_id, status)
		VALUES ($1, $2, $3, $4)
	`, id, issueID, agentID, status); err != nil {
		t.Fatalf("insert %s task %s: %v", status, id, err)
	}
}

// failAndCreateRetry is the transactionally relevant fragment of FailTask and
// CreateRetryTask: a retryable provider failure marks its parent failed, then
// clones a queued successor. It intentionally uses the production generic
// conflict action rather than the historical v2-targeted form above.
func failAndCreateRetry(t *testing.T, ctx context.Context, conn *pgxpool.Conn, parentID, childID string) int64 {
	t.Helper()
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin failure transaction: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
		UPDATE agent_task_queue
		SET status = 'failed', failure_reason = 'agent_error.provider_server_error'
		WHERE id = $1 AND status = 'running'
	`, parentID); err != nil {
		t.Fatalf("persist retryable failure: %v", err)
	}
	result, err := tx.Exec(ctx, `
		INSERT INTO agent_task_queue (id, issue_id, agent_id, status, context)
		SELECT $2, p.issue_id, p.agent_id, 'queued', p.context
		FROM agent_task_queue p
		WHERE p.id = $1
		ON CONFLICT DO NOTHING
	`, parentID, childID)
	if err != nil {
		t.Fatalf("CreateRetryTask-equivalent insert: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit failure transaction: %v", err)
	}
	return result.RowsAffected()
}

func assertTaskStatus(t *testing.T, ctx context.Context, conn *pgxpool.Conn, taskID, want string) {
	t.Helper()
	var got string
	if err := conn.QueryRow(ctx, `SELECT status FROM agent_task_queue WHERE id = $1`, taskID).Scan(&got); err != nil {
		t.Fatalf("read task %s: %v", taskID, err)
	}
	if got != want {
		t.Fatalf("task %s status = %q, want %q", taskID, got, want)
	}
}

func assertUniqueViolation(t *testing.T, ctx context.Context, conn *pgxpool.Conn, id, status, contextJSON string) {
	t.Helper()
	_, err := conn.Exec(ctx, `
		INSERT INTO agent_task_queue (id, issue_id, agent_id, status, context)
		VALUES ($1, $2, $3, $4, $5::jsonb)
	`, id, legacyIssueID, legacyAgentID, status, contextJSON)
	if !isUniqueViolation(err) {
		t.Fatalf("%s pending slot: got %v, want unique violation", status, err)
	}
}

func pgCode(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}

func isUniqueViolation(err error) bool {
	return pgCode(err) == "23505"
}
