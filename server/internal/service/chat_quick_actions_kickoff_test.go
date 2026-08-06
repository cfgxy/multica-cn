package service

import (
	"context"
	"testing"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// TestTaskHasOnboardingKickoffInput covers the query the quick-actions
// eligibility gate uses to skip Mika's onboarding opening (MUL-5765): it must
// be true only when the task's user input row is the hidden kickoff, so an
// ordinary turn keeps its suggestion pass.
func TestTaskHasOnboardingKickoffInput(t *testing.T) {
	pool := newResolveOriginatorPool(t)
	ctx := context.Background()
	q := db.New(pool)
	workspaceID, userID, agentID, _ := seedAttributionFixture(t, pool)

	var chatSessionID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO chat_session (workspace_id, agent_id, creator_id)
		VALUES ($1, $2, $3) RETURNING id`, workspaceID, agentID, userID).Scan(&chatSessionID); err != nil {
		t.Fatalf("seed chat session: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM chat_message WHERE chat_session_id = $1`, chatSessionID)
		pool.Exec(context.Background(), `DELETE FROM chat_session WHERE id = $1`, chatSessionID)
	})

	seedInput := func(kind string) string {
		var taskID string
		if err := pool.QueryRow(ctx, `
			INSERT INTO chat_message (chat_session_id, role, content, message_kind, task_id)
			VALUES ($1, 'user', 'input', $2, gen_random_uuid())
			RETURNING task_id::text`, chatSessionID, kind).Scan(&taskID); err != nil {
			t.Fatalf("seed %s input: %v", kind, err)
		}
		return taskID
	}

	kickoffTaskID := seedInput("onboarding_kickoff")
	ordinaryTaskID := seedInput("message")

	got, err := q.TaskHasOnboardingKickoffInput(ctx, util.MustParseUUID(kickoffTaskID))
	if err != nil {
		t.Fatalf("TaskHasOnboardingKickoffInput(kickoff): %v", err)
	}
	if !got {
		t.Errorf("kickoff input task = false, want true")
	}

	got, err = q.TaskHasOnboardingKickoffInput(ctx, util.MustParseUUID(ordinaryTaskID))
	if err != nil {
		t.Fatalf("TaskHasOnboardingKickoffInput(ordinary): %v", err)
	}
	if got {
		t.Errorf("ordinary input task = true, want false")
	}
}
