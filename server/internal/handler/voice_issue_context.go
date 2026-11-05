package handler

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// composeVoiceIssueContext projects the explicit Issue entry point into Live.
// It rechecks current membership and workspace ownership at injection time so a
// long-lived socket cannot retain an Issue after access has changed.
func (h *Handler) composeVoiceIssueContext(ctx context.Context, session db.LiveSession, issueID pgtype.UUID) *voiceChatContext {
	if !issueID.Valid || !session.UserID.Valid {
		return nil
	}
	if _, err := h.getWorkspaceMember(ctx, uuidToString(session.UserID), uuidToString(session.WorkspaceID)); err != nil {
		return nil
	}
	issue, err := h.Queries.GetIssueInWorkspace(ctx, db.GetIssueInWorkspaceParams{ID: issueID, WorkspaceID: session.WorkspaceID})
	if err != nil {
		return nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "### Issue context\n%s\n", renderVoiceIssueReference(issue))
	return &voiceChatContext{Text: strings.TrimSpace(b.String())}
}
