package service

// RUYI-397 workload weight: the claim budget is weighted, not a raw count.
// A running task occupies resource_weight slots of max_concurrent_tasks, so
// a heavy agent must stop claiming while its raw running count is still well
// below the ceiling, and a light agent with identical counts keeps claiming.
// The daemon's own slot semaphore stays count-based; this server-side budget
// is the mechanism that makes the weight observable.

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestClaimTaskResourceWeightBudget(t *testing.T) {
	ctx := context.Background()
	pool := newResolveOriginatorPool(t)
	bootstrap := testutil.New(pool, "", "")
	suffix := time.Now().UnixNano()
	userID := bootstrap.User(t,
		fmt.Sprintf("weight-user-%d", suffix),
		fmt.Sprintf("weight-user-%d@example.com", suffix),
	)
	workspaceID := bootstrap.Workspace(t,
		fmt.Sprintf("claim-weight-%d", suffix),
		fmt.Sprintf("claim-weight-%d", suffix),
	)
	fx := testutil.New(pool, workspaceID, userID)
	rt := fx.Runtime(t, "weight-runtime", testutil.Cols{"visibility": "public"})

	// Three agents on the same runtime, all max_concurrent_tasks=6, holding
	// 2 dispatched (running) tasks each:
	//   heavy (weight 3): 2×3 = 6 ≥ 6 → budget already exhausted
	//   light (weight 1): 2×1 = 2 < 6 → two more slots
	//   mid    (weight 2): 2×2 = 4 < 6 → one more slot, then exhausted
	heavy := fx.Agent(t, "heavy-agent", rt, testutil.Cols{
		"max_concurrent_tasks": 6, "resource_weight": 3,
	})
	light := fx.Agent(t, "light-agent", rt, testutil.Cols{
		"max_concurrent_tasks": 6, "resource_weight": 1,
	})
	mid := fx.Agent(t, "mid-agent", rt, testutil.Cols{
		"max_concurrent_tasks": 6, "resource_weight": 2,
	})

	// Each queued task sits on its OWN issue: claim eligibility also enforces
	// per-(issue, agent) serialization, and a queued row sharing an issue
	// with an active row of the same agent would be blocked by that fence
	// instead of by the weighted budget under test.
	seedRunning := func(agentID string, n int) {
		t.Helper()
		// One issue per running row: the (issue, agent) unique index admits
		// only a single pending task per pair.
		for i := 0; i < n; i++ {
			issueID := fx.Issue(t, fmt.Sprintf("weight running %s %d %d", agentID, time.Now().UnixNano(), i))
			fx.Task(t, agentID, testutil.Cols{"runtime_id": rt, "issue_id": issueID, "status": "dispatched"})
		}
	}
	seedRunning(heavy, 2)
	seedRunning(light, 2)
	seedRunning(mid, 2)

	seedQueued := func(agentID string) string {
		t.Helper()
		issueID := fx.Issue(t, fmt.Sprintf("weight queued %s %d", agentID, time.Now().UnixNano()))
		return fx.Task(t, agentID, testutil.Cols{"runtime_id": rt, "issue_id": issueID, "status": "queued"})
	}
	heavyQueued := seedQueued(heavy)
	lightQueued := seedQueued(light)
	midQueued1 := seedQueued(mid)
	midQueued2 := seedQueued(mid)

	svc := NewTaskService(db.New(pool), pool, nil, events.New())
	claim := func(agentID string) (string, error) {
		t.Helper()
		claimed, err := svc.claimTask(ctx, util.MustParseUUID(agentID), util.MustParseUUID(rt))
		if claimed == nil || err != nil {
			return "", err
		}
		return util.UUIDToString(claimed.ID), nil
	}
	taskStatus := func(taskID string) string {
		t.Helper()
		var status string
		if err := pool.QueryRow(ctx,
			`SELECT status FROM agent_task_queue WHERE id = $1`, taskID,
		).Scan(&status); err != nil {
			t.Fatalf("read task %s: %v", taskID, err)
		}
		return status
	}

	// Heavy agent: 2 running × 3 = 6 already fills the budget, even though
	// the raw count (2) is far below max (6). Its queued task stays queued.
	claimed, err := claim(heavy)
	if err != nil {
		t.Fatalf("heavy claim: %v", err)
	}
	if claimed != "" {
		t.Fatalf("heavy agent claimed %s at 2 running × weight 3 against max 6 — the weighted budget must bind before the raw count", claimed)
	}
	if status := taskStatus(heavyQueued); status != "queued" {
		t.Fatalf("heavy queued task status = %q, want queued", status)
	}

	// Control: identical counts, weight 1 — the claim goes through.
	claimed, err = claim(light)
	if err != nil {
		t.Fatalf("light claim: %v", err)
	}
	if claimed != lightQueued {
		t.Fatalf("light agent claimed %q, want %q (weight 1 must keep historical one-task-per-slot behaviour)", claimed, lightQueued)
	}

	// Boundary sequence for weight 2: 2×2 = 4 < 6 admits one more task;
	// after that claim 3×2 = 6 ≥ 6 blocks the second queued task.
	claimed, err = claim(mid)
	if err != nil {
		t.Fatalf("mid first claim: %v", err)
	}
	if claimed != midQueued1 {
		t.Fatalf("mid agent first claim = %q, want %q (2×2=4 is still under the budget)", claimed, midQueued1)
	}
	claimed, err = claim(mid)
	if err != nil {
		t.Fatalf("mid second claim: %v", err)
	}
	if claimed != "" {
		t.Fatalf("mid agent second claim = %q, want none (3×2=6 exhausts the budget)", claimed)
	}
	if status := taskStatus(midQueued2); status != "queued" {
		t.Fatalf("mid second queued task status = %q, want queued", status)
	}
}
