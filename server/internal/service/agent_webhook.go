package service

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

// StartAgentWebhookSession creates a fresh chat session on the agent and sends
// the webhook's bound prompt as its first user message — the whole "visit the
// webhook URL, get a new conversation" behaviour (RUYI-52) in one call.
//
// The session's creator and the task's initiator are the user who created the
// webhook row (initiatorUserID): chat_session.creator_id is NOT NULL and every
// run must carry a human attribution anchor, and the webhook's manager is the
// person who deliberately exposed this trigger. SendDirectChatMessage stamps
// the same DirectHumanRun attribution a hand-typed chat send gets, so the
// audit trail is identical to ordinary chat.
//
// Session creation mirrors handler.CreateChatSession's protocol: the workspace
// FOR KEY SHARE lock keeps a session from being created into a workspace whose
// delete is in progress (#5219 create/delete protocol). The message send then
// runs through SendDirectChatMessage, which re-checks archived/runtime state
// under the chat_session lock and persists the task + message atomically.
// The session is intentionally not marked explicitly-created: its first user
// message makes it publicly visible on its own (GetPublicChatSessionInWorkspace).
func (s *TaskService) StartAgentWebhookSession(
	ctx context.Context,
	agent db.Agent,
	initiatorUserID pgtype.UUID,
	prompt string,
) (db.ChatSession, *DirectChatSendResult, error) {
	var session db.ChatSession
	if err := s.runInTx(ctx, func(qtx *db.Queries) error {
		if _, err := qtx.LockWorkspaceForChatSessionCreate(ctx, agent.WorkspaceID); err != nil {
			return fmt.Errorf("lock workspace: %w", err)
		}
		created, err := qtx.CreateChatSession(ctx, db.CreateChatSessionParams{
			ID:          dbid.NewV7(),
			WorkspaceID: agent.WorkspaceID,
			AgentID:     agent.ID,
			CreatorID:   initiatorUserID,
			Title:       "",
			ProjectID:   pgtype.UUID{Valid: false},
		})
		if err != nil {
			return fmt.Errorf("create chat session: %w", err)
		}
		session = created
		return nil
	}); err != nil {
		return db.ChatSession{}, nil, err
	}

	sent, err := s.SendDirectChatMessage(ctx, session, agent, initiatorUserID, prompt, nil, "member", initiatorUserID)
	if err != nil {
		return db.ChatSession{}, nil, err
	}
	return session, sent, nil
}
