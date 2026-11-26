package handler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/knowledgegovernance"
	"github.com/multica-ai/multica/server/internal/testutil"
)

// A scheduler claim is per directory and config version. A sibling active run
// must roll its attempted claim back, while a deliberate config-version change
// gets a new namespace instead of comparing incompatible bucket strings.
func TestKnowledgeGovernanceScheduleClaimsAreAtomicAndVersionScoped(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Fatal("database fixture is required")
	}
	ctx := context.Background()
	firstDir := registerKnowledgeDir(t, "candidate_cli", t.TempDir())
	secondDir := registerKnowledgeDir(t, "candidate_cli", t.TempDir())
	var agentID string
	dbfx.QueryRow(t, `SELECT id::text FROM agent WHERE workspace_id = $1 ORDER BY created_at LIMIT 1`, testWorkspaceID).Scan(&agentID)
	now := time.Now().UTC()
	dbfx.InsertNoID(t, "knowledge_governance_config", testutil.Cols{
		"workspace_id": testWorkspaceID, "enabled": true, "agent_id": agentID, "cadence": "daily", "timezone": "UTC", "version": 1, "next_eligible_at": now.Add(-time.Minute),
	}, "workspace_id = $1", testWorkspaceID)
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM knowledge_governance_schedule_claim WHERE workspace_id = $1`, testWorkspaceID)
		testPool.Exec(ctx, `DELETE FROM knowledge_governance_config WHERE workspace_id = $1`, testWorkspaceID)
	})

	runner := &knowledgegovernance.Runner{DB: testPool, Queries: testHandler.Queries, Enqueue: testHandler.KnowledgeGovernanceTaskEnqueuer(), Now: func() time.Time { return now }}
	first := knowledgegovernance.ScheduleTarget{WorkspaceID: testWorkspaceID, DirID: firstDir, ActorID: testUserID, Cadence: "daily", Timezone: "UTC", ConfigVersion: 1, ScheduleBucket: "2026-11-26", EvaluatedAt: now}
	if _, err := runner.RunScheduledWorkspace(ctx, first); err != nil {
		t.Fatalf("first scheduled run: %v", err)
	}
	if _, err := runner.RunScheduledWorkspace(ctx, first); !errors.Is(err, knowledgegovernance.ErrScheduleClaimed) {
		t.Fatalf("same directory/bucket must be claimed, got %v", err)
	}

	// End the first active run so version 2 can prove its fresh namespace.
	dbfx.Exec(t, `UPDATE knowledge_governance_run SET status = 'cancelled', finished_at = now() WHERE workspace_id = $1 AND dir_id = $2`, testWorkspaceID, firstDir)
	dbfx.Exec(t, `UPDATE knowledge_governance_config SET version = 2, next_eligible_at = $2 WHERE workspace_id = $1`, testWorkspaceID, now.Add(-time.Minute))
	changed := first
	changed.ConfigVersion = 2
	changed.Cadence = "monthly"
	changed.ScheduleBucket = "2026-11"
	if _, err := runner.RunScheduledWorkspace(ctx, changed); err != nil {
		t.Fatalf("changed cadence/version must not compare old daily bucket: %v", err)
	}

	// A failed active-scope insertion rolls the claim back. The sibling run
	// therefore cannot poison a later scheduler retry for this directory.
	if _, err := runner.RunWorkspace(ctx, testWorkspaceID, secondDir, testUserID, "manual"); err != nil {
		t.Fatalf("start sibling active run: %v", err)
	}
	blocked := changed
	blocked.DirID = secondDir
	if _, err := runner.RunScheduledWorkspace(ctx, blocked); !errors.Is(err, knowledgegovernance.ErrScopeRunning) {
		t.Fatalf("active sibling scope: got %v, want ErrScopeRunning", err)
	}
	var claims int
	dbfx.QueryRow(t, `SELECT count(*) FROM knowledge_governance_schedule_claim WHERE workspace_id = $1 AND dir_id = $2`, testWorkspaceID, secondDir).Scan(&claims)
	if claims != 0 {
		t.Fatalf("active scope left %d committed schedule claims, want 0", claims)
	}
}
