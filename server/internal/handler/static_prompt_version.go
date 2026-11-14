package handler

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// recordStaticPromptWriteTx accompanies an ordinary business-column write.
// The caller must use the same transaction for that write and must abort on
// error. Locking the carrier serializes these saves with governance writes;
// seeding first preserves the original text of carriers without history.
// Keeping the business UPDATE with the caller preserves PATCH null semantics
// and avoids incrementing a project's revision twice.
func recordStaticPromptWriteTx(ctx context.Context, qtx *db.Queries, workspaceID pgtype.UUID, scope promptVersionScope, scopeID pgtype.UUID, content, source string) error {
	current, err := lockScopeEntityResult(ctx, qtx, scope, workspaceID, scopeID)
	if err != nil {
		return err
	}
	if err := seedPromptVersionV1(ctx, qtx, scope, workspaceID, scopeID, current); err != nil {
		return err
	}
	if current == content {
		return nil
	}
	next, err := qtx.NextPromptVersion(ctx, db.NextPromptVersionParams{Scope: string(scope), ScopeID: scopeID})
	if err != nil {
		return err
	}
	_, err = qtx.CreatePromptVersion(ctx, db.CreatePromptVersionParams{
		WorkspaceID:   workspaceID,
		Scope:         string(scope),
		ScopeID:       scopeID,
		Version:       next,
		Content:       content,
		ContentSha256: sha256Hex(content),
		Source:        source,
		ChangeNote:    "Static prompt updated",
		GateResult:    []byte("[]"),
	})
	return err
}
