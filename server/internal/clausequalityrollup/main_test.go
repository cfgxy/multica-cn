package clausequalityrollup

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/testutil"
)

var (
	testPool        *pgxpool.Pool
	testWorkspaceID string
	testUserID      string
)

const (
	fixtureEmail = "clausequalityrollup-fixture@multica.ai"
	fixtureSlug  = "clausequalityrollup-fixtures"
)

var fixtureSeq atomic.Uint64

// TestMain follows the same contract as internal/testutil's: without a
// reachable database the suite exits green rather than red, so a checkout
// without Postgres still runs the rest of the tree.
func TestMain(m *testing.M) {
	ctx := context.Background()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://multica:multica@localhost:5432/multica?sslmode=disable"
	}
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		fmt.Printf("Skipping clause rollup DB tests: could not connect: %v\n", err)
		os.Exit(m.Run())
	}
	if err := pool.Ping(ctx); err != nil {
		fmt.Printf("Skipping clause rollup DB tests: database not reachable: %v\n", err)
		pool.Close()
		os.Exit(m.Run())
	}
	if err := seed(ctx, pool); err != nil {
		fmt.Printf("Skipping clause rollup DB tests: seed failed: %v\n", err)
		pool.Close()
		os.Exit(m.Run())
	}
	testPool = pool
	code := m.Run()
	_, _ = pool.Exec(ctx, `DELETE FROM workspace WHERE slug = $1`, fixtureSlug)
	_, _ = pool.Exec(ctx, `DELETE FROM "user" WHERE email = $1`, fixtureEmail)
	pool.Close()
	os.Exit(code)
}

func seed(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, `DELETE FROM workspace WHERE slug = $1`, fixtureSlug); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, `DELETE FROM "user" WHERE email = $1`, fixtureEmail); err != nil {
		return err
	}
	if err := pool.QueryRow(ctx, `INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id`,
		"Clause Quality Fixture", fixtureEmail).Scan(&testUserID); err != nil {
		return err
	}
	if err := pool.QueryRow(ctx, `INSERT INTO workspace (name, slug, issue_prefix) VALUES ($1, $2, $3) RETURNING id`,
		"Clause Quality Fixtures", fixtureSlug, "CQF").Scan(&testWorkspaceID); err != nil {
		return err
	}
	_, err := pool.Exec(ctx, `INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'owner')`,
		testWorkspaceID, testUserID)
	return err
}

// clauseFixture is one clause with two versions and a live workspace binding,
// ready to take injections through a dedicated agent's finished runs.
type clauseFixture struct {
	f       *testutil.Fixture
	t       *testing.T
	id      string
	slug    string
	agentID string
}

func newClauseFixture(t *testing.T) *clauseFixture {
	t.Helper()
	if testPool == nil {
		t.Skip("database not available")
	}
	f := testutil.New(testPool, testWorkspaceID, testUserID)

	// The watermark is global and single-row; rewind and restore so the
	// suite stays serial-safe within one package.
	var saved time.Time
	f.QueryRow(t, `SELECT watermark_at FROM clause_quality_rollup_state WHERE id = 1`).Scan(&saved)
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(),
			`UPDATE clause_quality_rollup_state SET watermark_at = $1 WHERE id = 1`, saved)
	})

	// Runtime and agent names carry a serial counter: the workspace-wide
	// unique index on agent names would collide across fixtures in one test.
	fxSeq := fixtureSeq.Add(1)
	runtimeID := f.Runtime(t, fmt.Sprintf("cq-runtime-%d", fxSeq))
	c := &clauseFixture{
		f:       f,
		t:       t,
		slug:    fmt.Sprintf("cls_cqfx%04d", time.Now().UnixNano()%10000),
		agentID: f.Agent(t, fmt.Sprintf("cq-agent-%d", fxSeq), runtimeID),
	}
	if err := testPool.QueryRow(context.Background(),
		`INSERT INTO clause (workspace_id, slug, title, status, created_by_type)
		 VALUES ($1, $2, '条款测试', 'active', 'member') RETURNING id`,
		testWorkspaceID, c.slug).Scan(&c.id); err != nil {
		t.Fatalf("seed clause: %v", err)
	}
	for v, body := range map[int]string{
		1: "- **条款测试**：基线版本文本。",
		2: "- **条款测试**：修订版本文本，比基线更长一些。",
	} {
		f.Exec(t, `INSERT INTO clause_version (clause_id, version, content, content_sha256, source, carrier_scope, created_by_type)
			VALUES ($1, $2, $3, $4, 'legislation', 'workspace', 'member')`,
			c.id, v, body, fmt.Sprintf("%064d", v))
	}
	f.Exec(t, `INSERT INTO clause_binding (clause_id, scope, scope_id, status, since_clause_version, audit_log)
		VALUES ($1, 'workspace', $2, 'active', 2, '[]'::jsonb)`, c.id, testWorkspaceID)
	t.Cleanup(func() {
		ctx := context.Background()
		for _, stmt := range []string{
			`DELETE FROM clause_injection WHERE workspace_id = $1`,
			`DELETE FROM clause_quality_daily WHERE workspace_id = $1`,
			`DELETE FROM prompt_proposal WHERE workspace_id = $1 AND clause_id = $2`,
			`DELETE FROM clause_binding WHERE clause_id = $2`,
			`DELETE FROM clause_version WHERE clause_id = $2`,
			`DELETE FROM clause WHERE id = $2`,
		} {
			_, _ = testPool.Exec(ctx, stmt, testWorkspaceID, c.id)
		}
	})
	return c
}

// inject queues one finished run carrying this clause's injection row.
func (c *clauseFixture) inject(finished time.Time, version int, status string) string {
	c.t.Helper()
	issueID := c.f.Issue(c.t, "clause rollup issue")
	taskID := c.f.Task(c.t, c.agentID, testutil.Cols{
		"issue_id":     issueID,
		"status":       status,
		"attempt":      1,
		"completed_at": finished,
		"prompt_versions": testutil.Raw(
			fmt.Sprintf(`'{"workspace": %q}'::jsonb`, testWorkspaceID)),
	})
	c.f.Exec(c.t, `INSERT INTO clause_injection (workspace_id, task_id, clause_id, clause_version, carrier_scope)
		VALUES ($1, $2, $3, $4, 'workspace')`, testWorkspaceID, taskID, c.id, version)
	return taskID
}
