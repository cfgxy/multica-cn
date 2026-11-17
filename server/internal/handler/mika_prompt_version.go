package handler

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// ensureMikaPromptSnapshot stores only the workspace-owned notes as the
// restorable carrier. The static composed prompt hash distinguishes product
// releases without making binary instructions eligible for switch/rollback.
func (h *Handler) ensureMikaPromptSnapshot(ctx context.Context, workspaceID, agentID pgtype.UUID, notes, staticPrompt string) (int32, error) {
	if staticPrompt == "" {
		return 0, nil
	}
	note := "Mika static prompt sha256:" + sha256Hex(staticPrompt)

	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin mika prompt snapshot: %w", err)
	}
	defer tx.Rollback(ctx)
	qtx := h.Queries.WithTx(tx)

	if _, err := lockScopeEntityResult(ctx, qtx, promptVersionScopeAgent, workspaceID, agentID); err != nil {
		return 0, fmt.Errorf("lock mika agent: %w", err)
	}

	latest, err := qtx.GetLatestPromptVersion(ctx, db.GetLatestPromptVersionParams{
		Scope:   string(promptVersionScopeAgent),
		ScopeID: agentID,
	})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return 0, fmt.Errorf("read mika prompt version: %w", err)
	}
	if err == nil && latest.Content == notes && latest.ChangeNote == note {
		if err := tx.Commit(ctx); err != nil {
			return 0, fmt.Errorf("commit mika prompt snapshot check: %w", err)
		}
		return latest.Version, nil
	}

	next, err := qtx.NextPromptVersion(ctx, db.NextPromptVersionParams{
		Scope:   string(promptVersionScopeAgent),
		ScopeID: agentID,
	})
	if err != nil {
		return 0, fmt.Errorf("next mika prompt version: %w", err)
	}
	if _, err := qtx.CreatePromptVersion(ctx, db.CreatePromptVersionParams{
		WorkspaceID:   workspaceID,
		Scope:         string(promptVersionScopeAgent),
		ScopeID:       agentID,
		Version:       next,
		Content:       notes,
		ContentSha256: sha256Hex(notes),
		Source:        "auto_snapshot",
		ChangeNote:    note,
		GateResult:    []byte("[]"),
	}); err != nil {
		return 0, fmt.Errorf("write mika prompt snapshot: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit mika prompt snapshot: %w", err)
	}
	return next, nil
}
