package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// This suite requires an explicitly provisioned disposable database whose name
// includes ruyi800. It never falls back to the shared dev or QA databases.
func issueCoordinatorTestDB(t *testing.T) (*pgxpool.Pool, *db.Queries) {
	t.Helper()
	dsn := os.Getenv("RUYI800_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("RUYI800_TEST_DATABASE_URL is not set to a disposable database")
	}
	parsed, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse isolated database URL: %v", err)
	}
	if !strings.Contains(parsed.ConnConfig.Database, "ruyi800") {
		t.Fatal("isolated database name must include ruyi800")
	}
	admin, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect isolated database: %v", err)
	}
	t.Cleanup(admin.Close)
	schema := "issue_coordinator_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.Exec(context.Background(), "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatalf("create test schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
	})
	parsed.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, "SET search_path TO "+pgx.Identifier{schema}.Sanitize())
		return err
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), parsed)
	if err != nil {
		t.Fatalf("connect isolated test schema: %v", err)
	}
	t.Cleanup(pool.Close)
	_, err = pool.Exec(context.Background(), `
CREATE TABLE issue_coordinator_session (
    issue_id uuid NOT NULL, leader_agent_id uuid NOT NULL, runtime_id uuid NOT NULL,
    session_id text, generation bigint NOT NULL DEFAULT 1, snapshot_version bigint NOT NULL DEFAULT 0,
    snapshot jsonb NOT NULL DEFAULT '{}'::jsonb, claim_generation bigint NOT NULL DEFAULT 1,
    claim_snapshot_version bigint NOT NULL DEFAULT 0, active_issue_reads integer NOT NULL DEFAULT 0,
    active_comment_reads integer NOT NULL DEFAULT 0, read_observations jsonb NOT NULL DEFAULT '[]'::jsonb,
    last_event_sequence bigint NOT NULL DEFAULT 0, active_task_id uuid, last_task_id uuid,
    continuity_state text NOT NULL DEFAULT 'fresh', updated_at timestamptz NOT NULL DEFAULT now()
);
`)
	if err != nil {
		t.Fatalf("create isolated coordinator table: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `CREATE UNIQUE INDEX issue_coordinator_identity_idx ON issue_coordinator_session (issue_id, leader_agent_id)`); err != nil {
		t.Fatalf("create isolated coordinator identity index: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `CREATE TABLE agent_task_queue (id uuid PRIMARY KEY, status text NOT NULL)`); err != nil {
		t.Fatalf("create isolated task table: %v", err)
	}
	return pool, db.New(pool)
}

func TestIssueCoordinatorPostgresCASRecoveryAndReadObservations(t *testing.T) {
	ctx := context.Background()
	pool, queries := issueCoordinatorTestDB(t)
	issueID, leaderID, runtimeID := util.MustParseUUID(uuid.NewString()), util.MustParseUUID(uuid.NewString()), util.MustParseUUID(uuid.NewString())
	task1, task2 := util.MustParseUUID(uuid.NewString()), util.MustParseUUID(uuid.NewString())
	for _, taskID := range []pgtype.UUID{task1, task2} {
		if _, err := pool.Exec(ctx, `INSERT INTO agent_task_queue (id, status) VALUES ($1, 'completed')`, taskID); err != nil {
			t.Fatal(err)
		}
	}
	claimed, err := queries.ClaimIssueCoordinatorSession(ctx, db.ClaimIssueCoordinatorSessionParams{
		IssueID: issueID, LeaderAgentID: leaderID, RuntimeID: runtimeID, TaskID: task1,
	})
	if err != nil || claimed.Generation != 1 || claimed.SnapshotVersion != 0 {
		t.Fatalf("first claim = %+v, err=%v", claimed, err)
	}
	for i := 0; i < 3; i++ {
		if _, err := queries.RecordIssueCoordinatorRead(ctx, db.RecordIssueCoordinatorReadParams{IssueID: issueID, LeaderAgentID: leaderID, TaskID: task1, ReadKind: "issue"}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		if _, err := queries.RecordIssueCoordinatorRead(ctx, db.RecordIssueCoordinatorReadParams{IssueID: issueID, LeaderAgentID: leaderID, TaskID: task1, ReadKind: "comment"}); err != nil {
			t.Fatal(err)
		}
	}
	complete := func(task pgtype.UUID, runtime pgtype.UUID, session string) error {
		_, err := queries.CompleteIssueCoordinatorSession(ctx, db.CompleteIssueCoordinatorSessionParams{
			RuntimeID: runtime, SessionID: session, Snapshot: []byte(`{"version":1}`),
			TaskID: task, IssueID: issueID, LeaderAgentID: leaderID,
		})
		return err
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); <-start; errs <- complete(task1, runtimeID, "session-1") }()
	}
	close(start)
	wg.Wait()
	close(errs)
	completed, conflicts := 0, 0
	for err := range errs {
		if err == nil {
			completed++
		} else if err == pgx.ErrNoRows {
			conflicts++
		} else {
			t.Fatalf("concurrent completion: %v", err)
		}
	}
	if completed != 1 || conflicts != 1 {
		t.Fatalf("completion winner/conflict = %d/%d, want 1/1", completed, conflicts)
	}
	observationsJSON, err := queries.GetIssueCoordinatorReadObservations(ctx, db.GetIssueCoordinatorReadObservationsParams{
		IssueID: issueID, LeaderAgentID: leaderID,
	})
	if err != nil {
		t.Fatal(err)
	}
	var observations []map[string]any
	if err := json.Unmarshal(observationsJSON, &observations); err != nil {
		t.Fatal(err)
	}
	if len(observations) != 1 || observations[0]["duplicate_issue_reads"] != float64(2) || observations[0]["duplicate_comment_reads"] != float64(1) {
		raw, _ := json.Marshal(observations)
		t.Fatalf("read observations = %s", raw)
	}
	second, err := queries.ClaimIssueCoordinatorSession(ctx, db.ClaimIssueCoordinatorSessionParams{
		IssueID: issueID, LeaderAgentID: leaderID, RuntimeID: runtimeID, TaskID: task2,
	})
	if err != nil || second.Generation != claimed.Generation || second.ContinuityState != "resumable" {
		t.Fatalf("continuation claim = %+v, err=%v", second, err)
	}
	if err := complete(task2, runtimeID, "session-2"); err != nil {
		t.Fatalf("complete second run: %v", err)
	}
	newRuntime := util.MustParseUUID(uuid.NewString())
	task3 := util.MustParseUUID(uuid.NewString())
	if _, err := pool.Exec(ctx, `INSERT INTO agent_task_queue (id, status) VALUES ($1, 'completed')`, task3); err != nil {
		t.Fatal(err)
	}
	switched, err := queries.ClaimIssueCoordinatorSession(ctx, db.ClaimIssueCoordinatorSessionParams{
		IssueID: issueID, LeaderAgentID: leaderID, RuntimeID: newRuntime, TaskID: task3,
	})
	if err != nil || switched.Generation != second.Generation+1 || switched.SessionID.Valid || switched.ContinuityState != "cold_start" {
		t.Fatalf("runtime switch claim = %+v, err=%v", switched, err)
	}
	if err := complete(task2, runtimeID, "stale-session"); err != pgx.ErrNoRows {
		t.Fatalf("stale completion error = %v, want explicit no-row CAS conflict", err)
	}
	if err := complete(task3, newRuntime, "session-3"); err != nil {
		t.Fatalf("complete switched runtime: %v", err)
	}
	forced, err := queries.ClaimIssueCoordinatorSession(ctx, db.ClaimIssueCoordinatorSessionParams{
		IssueID: issueID, LeaderAgentID: leaderID, RuntimeID: newRuntime, TaskID: util.MustParseUUID(uuid.NewString()), ForceColdStart: true,
	})
	if err != nil || forced.Generation != switched.Generation+1 || forced.ContinuityState != "cold_start" {
		t.Fatalf("resume rejection claim = %+v, err=%v", forced, err)
	}
	for i := 0; i < 5; i++ {
		task := util.MustParseUUID(uuid.NewString())
		if _, err := pool.Exec(ctx, `INSERT INTO agent_task_queue (id, status) VALUES ($1, 'completed')`, task); err != nil {
			t.Fatal(err)
		}
		if err := complete(forced.ActiveTaskID, newRuntime, fmt.Sprintf("session-%d", i)); err != nil {
			t.Fatalf("complete recovery step %d: %v", i, err)
		}
		if i < 4 {
			forced, err = queries.ClaimIssueCoordinatorSession(ctx, db.ClaimIssueCoordinatorSessionParams{
				IssueID: issueID, LeaderAgentID: leaderID, RuntimeID: newRuntime, TaskID: task,
			})
			if err != nil || forced.ContinuityState != "resumable" {
				t.Fatalf("recovery claim %d = %+v, err=%v", i, forced, err)
			}
		}
	}
	var observationCount int
	if err := pool.QueryRow(ctx, `SELECT jsonb_array_length(read_observations) FROM issue_coordinator_session WHERE issue_id=$1`, issueID).Scan(&observationCount); err != nil || observationCount == 0 || observationCount > 30 {
		t.Fatalf("observation history count = %d, err=%v", observationCount, err)
	}
}
